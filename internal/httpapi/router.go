package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/audit"
	"github.com/soju06/codex-lb/internal/auth"
	"github.com/soju06/codex-lb/internal/authguardian"
	"github.com/soju06/codex-lb/internal/cacheinvalidation"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/conversationarchive"
	"github.com/soju06/codex-lb/internal/crypto"
	"github.com/soju06/codex-lb/internal/dashboard"
	"github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/firewall"
	"github.com/soju06/codex-lb/internal/health"
	"github.com/soju06/codex-lb/internal/limitwarmup"
	"github.com/soju06/codex-lb/internal/models"
	"github.com/soju06/codex-lb/internal/oauth"
	"github.com/soju06/codex-lb/internal/proxy"
	"github.com/soju06/codex-lb/internal/quotaplanner"
	"github.com/soju06/codex-lb/internal/reports"
	"github.com/soju06/codex-lb/internal/requestlogs"
	"github.com/soju06/codex-lb/internal/runtime"
	"github.com/soju06/codex-lb/internal/settings"
	"github.com/soju06/codex-lb/internal/stickysessions"
	"github.com/soju06/codex-lb/internal/usage"
)

type RouterOptions struct {
	CacheInvalidationPoller *cacheinvalidation.Poller
	ModelRegistry           *proxy.ModelRegistry
}

func NewRouter(store *db.Store, logger *slog.Logger, cfg config.Config, optionList ...RouterOptions) http.Handler {
	var opts RouterOptions
	if len(optionList) > 0 {
		opts = optionList[0]
	}
	router := chi.NewRouter()
	drainState := health.NewDrainState()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(health.DrainMiddleware(drainState))
	router.Use(codexV1AliasMiddleware)
	router.Use(accessLog(logger))

	encryptor, err := crypto.NewEncryptor(cfg.EncryptionKeyPath)
	if err != nil {
		logger.Error("failed to initialize encryptor", "error", err)
		panic(err)
	}

	healthHandler := health.NewHandler(store, drainState)
	accountRepo := accounts.NewRepository(store)
	auditRepo := audit.NewRepository(store)
	accountHandler := accounts.NewHandler(accountRepo, encryptor, auditRepo).
		WithProbeClient(cfg.UpstreamBaseURL, nil).
		WithProbeAuthRefresher(authguardian.NewOAuthRefresher(accountRepo, encryptor, cfg, nil))
	if opts.CacheInvalidationPoller != nil {
		accountHandler = accountHandler.WithProbeInvalidation(func() {
			_ = opts.CacheInvalidationPoller.Bump(context.Background(), cacheinvalidation.NamespaceSettings)
		})
	}
	dashboardHandler := dashboard.NewHandler(dashboard.NewRepository(store), accountHandler, cfg.UsageRefreshInterval)
	requestLogsRepo := requestlogs.NewRepository(store)
	requestLogsHandler := requestlogs.NewHandler(requestLogsRepo)
	usageHandler := usage.NewHandler(usage.NewRepository(store), accountRepo, requestLogsRepo)
	settingsRepo := settings.NewRepository(store, encryptor)
	firewallRepo := firewall.NewRepository(store)
	firewallMiddleware, err := firewall.NewFirewall(firewallRepo, firewall.MiddlewareOptions{
		TrustProxyHeaders: cfg.FirewallTrustProxyHeaders,
		TrustedProxyCIDRs: cfg.FirewallTrustedProxyCIDRs,
		CacheTTL:          cfg.FirewallIPCacheTTL,
	})
	if err != nil {
		logger.Error("failed to initialize firewall", "error", err)
		panic(err)
	}
	router.Use(firewallMiddleware.Middleware)
	if opts.CacheInvalidationPoller != nil {
		opts.CacheInvalidationPoller.OnInvalidation(cacheinvalidation.NamespaceFirewall, firewallMiddleware.InvalidateCache)
	}
	firewallHandler := firewall.NewHandler(firewallRepo, firewallMiddleware, opts.CacheInvalidationPoller).WithAudit(auditRepo)
	stickySessionsRepo := stickysessions.NewRepository(store)
	stickySessionsHandler := stickysessions.NewHandler(stickySessionsRepo).WithAudit(auditRepo)
	quotaPlannerHandler := quotaplanner.NewHandler(quotaplanner.NewRepository(store)).
		WithAudit(auditRepo).
		WithWarmup(accountRepo, requestLogsRepo, limitwarmup.NewStreamingSender(encryptor, cfg))
	apiKeysRepo := apikeys.NewRepository(store)
	apiKeysHandler := apikeys.NewHandler(apiKeysRepo, opts.CacheInvalidationPoller).WithAudit(auditRepo)
	modelsHandler := models.NewHandler(store)
	reportsHandler := reports.NewHandler(reports.NewRepository(store))
	sessionStore := sessionManager()
	authRepo := auth.NewRepository(store)
	bootstrapService := auth.NewBootstrapService(authRepo, encryptor, cfg.DashboardBootstrapToken, logger)
	authHandler := auth.NewHandler(authRepo, sessionStore, cfg.AuthDisabled, encryptor, bootstrapService)
	oauthService := oauth.NewService(cfg, accountRepo, encryptor, accountHandler.InvalidateSummaryCache, logger)
	oauthHandler := oauth.NewHandler(oauthService)
	modelRegistry := opts.ModelRegistry
	if modelRegistry == nil {
		modelRegistry = proxy.NewModelRegistry(5 * time.Minute)
	}
	additionalQuotaRegistry := proxy.NewAdditionalQuotaRegistry()
	usageRepo := usage.NewRepository(store)
	proxyModelsHandler := proxy.NewModelsHandler(apiKeysRepo, settingsRepo, modelRegistry)
	loadBalancer := proxy.NewLoadBalancer(accountRepo, settingsRepo, usageRepo, encryptor, modelRegistry, additionalQuotaRegistry)
	if opts.CacheInvalidationPoller != nil {
		opts.CacheInvalidationPoller.OnInvalidation(cacheinvalidation.NamespaceSettings, loadBalancer.InvalidateSelectionCache)
	}
	settingsHandler := settings.NewHandler(settingsRepo, opts.CacheInvalidationPoller).WithAudit(auditRepo)
	proxyService := proxy.NewService(loadBalancer, settingsRepo, requestLogsRepo, apiKeysRepo, stickySessionsRepo, modelRegistry, "")
	chatCompletionsHandler := proxy.NewChatCompletionsHandler(proxyService, apiKeysRepo, settingsRepo)
	responsesHandler := proxy.NewResponsesHandler(proxyService, apiKeysRepo, settingsRepo)
	warmupHandler := proxy.NewWarmupHandler(proxyService, apiKeysRepo, settingsRepo)
	mediaHandler := proxy.NewMediaHandler(proxyService, apiKeysRepo, settingsRepo)
	controlHandler := proxy.NewControlHandler(proxyService, apiKeysRepo, settingsRepo)
	codexWSHandler := proxy.NewWebSocketResponsesHandler(proxyService, apiKeysRepo, settingsRepo, true)
	v1WSHandler := proxy.NewWebSocketResponsesHandler(proxyService, apiKeysRepo, settingsRepo, false)
	codexUsageHandler := proxy.NewCodexUsageHandler(apiKeysRepo, settingsRepo)
	auditHandler := audit.NewHandler(auditRepo)
	conversationArchiveHandler := conversationarchive.NewHandler(conversationarchive.NewRepository(cfg.ConversationArchiveDir))

	router.Get("/health/live", healthHandler.Live)
	router.Get("/health/ready", healthHandler.Ready)
	router.Get("/health", healthHandler.Health)
	router.Get("/health/startup", healthHandler.Startup)
	router.Post("/internal/drain/start", healthHandler.StartDrain)
	router.Post("/internal/drain/stop", healthHandler.StopDrain)
	router.Get("/internal/drain/status", healthHandler.DrainStatus)

	router.Get("/v1/models", proxyModelsHandler.V1Models)
	router.Get("/backend-api/codex/models", proxyModelsHandler.CodexModels)
	router.Post("/v1/chat/completions", chatCompletionsHandler.ServeHTTP)
	router.Post("/backend-api/codex/responses", responsesHandler.CodexResponses)
	router.Post("/backend-api/codex/responses/compact", responsesHandler.CodexResponsesCompact)
	router.Post("/v1/responses", responsesHandler.V1Responses)
	router.Post("/v1/responses/compact", responsesHandler.V1ResponsesCompact)
	router.Post("/v1/warmup", warmupHandler.V1Warmup)
	router.Post("/v1/warmup/{mode}", warmupHandler.V1WarmupMode)
	router.Post("/backend-api/files", mediaHandler.CreateFile)
	router.Post("/backend-api/files/{fileID}/uploaded", mediaHandler.FinalizeFile)
	router.Post("/backend-api/transcribe", mediaHandler.BackendTranscribe)
	router.Post("/v1/audio/transcriptions", mediaHandler.V1AudioTranscriptions)
	router.Post("/v1/images/generations", mediaHandler.V1ImagesGenerations)
	router.Post("/v1/images/edits", mediaHandler.V1ImagesEdits)
	router.Post("/v1/images/variations", mediaHandler.V1ImagesVariations)
	router.Get("/backend-api/codex/agent-identities/jwks", mediaHandler.CodexJWKS)
	router.Get("/backend-api/wham/agent-identities/jwks", mediaHandler.WhamJWKS)
	router.Get("/backend-api/codex/thread/goal/get", controlHandler.ThreadGoalGet)
	router.Post("/backend-api/codex/thread/goal/get", controlHandler.ThreadGoalGet)
	router.Post("/backend-api/codex/thread/goal/set", controlHandler.ThreadGoalSet)
	router.Post("/backend-api/codex/thread/goal/clear", controlHandler.ThreadGoalClear)
	router.Post("/backend-api/codex/analytics-events/events", controlHandler.AnalyticsEvents)
	router.Post("/backend-api/codex/memories/trace_summarize", controlHandler.MemoriesTraceSummarize)
	router.Post("/backend-api/codex/realtime/calls", controlHandler.RealtimeCalls)
	router.Post("/backend-api/codex/safety/arc", controlHandler.SafetyArc)
	router.Get("/backend-api/codex/opportunistic/admission", controlHandler.OpportunisticAdmission)
	router.Get("/backend-api/codex/responses", codexWSHandler.ServeHTTP)
	router.Get("/v1/responses", v1WSHandler.ServeHTTP)
	router.Get("/v1/usage", codexUsageHandler.V1Usage)
	router.Get("/api/codex/usage", codexUsageHandler.Get)
	router.Get("/api/codex/usage/", codexUsageHandler.Get)
	router.Group(func(r chi.Router) {
		r.Use(sessionStore.LoadAndSave)
		r.Get("/api/auth/session", authHandler.Session)
		r.Post("/api/auth/login", authHandler.Login)
		r.Post("/api/auth/logout", authHandler.Logout)
		r.Post("/api/auth/password/setup", authHandler.SetupPassword)
		r.Post("/api/auth/password/change", authHandler.ChangePassword)
		r.Delete("/api/auth/password", authHandler.RemovePassword)
		r.Post("/api/auth/totp/setup/start", authHandler.StartTOTPSetup)
		r.Post("/api/auth/totp/setup/confirm", authHandler.ConfirmTOTPSetup)
		r.Post("/api/auth/totp/verify", authHandler.VerifyTOTP)
		r.Post("/api/auth/totp/disable", authHandler.DisableTOTP)

		r.Get("/api/dashboard-auth/session", authHandler.Session)
		r.Post("/api/dashboard-auth/password/login", authHandler.Login)
		r.Post("/api/dashboard-auth/password/setup", authHandler.SetupPassword)
		r.Post("/api/dashboard-auth/password/change", authHandler.ChangePassword)
		r.Delete("/api/dashboard-auth/password", authHandler.RemovePassword)
		r.Post("/api/dashboard-auth/totp/setup/start", authHandler.StartTOTPSetup)
		r.Post("/api/dashboard-auth/totp/setup/confirm", authHandler.ConfirmTOTPSetup)
		r.Post("/api/dashboard-auth/totp/verify", authHandler.VerifyTOTP)
		r.Post("/api/dashboard-auth/totp/disable", authHandler.DisableTOTP)
		r.Post("/api/dashboard-auth/logout", authHandler.Logout)

		r.Group(func(protected chi.Router) {
			protected.Use(authHandler.RequireSession)
			protected.Get("/api/runtime/version", runtime.Version)
			protected.Get("/api/accounts", accountHandler.List)
			protected.Post("/api/accounts/import", accountHandler.Import)
			protected.Post("/api/accounts/{accountID}/export", accountHandler.Export)
			protected.Post("/api/accounts/{accountID}/export/auth", accountHandler.ExportAuth)
			protected.Post("/api/accounts/{accountID}/export/opencode-auth", accountHandler.ExportOpenCodeAuth)
			protected.Post("/api/accounts/{accountID}/reactivate", accountHandler.Reactivate)
			protected.Patch("/api/accounts/{accountID}", accountHandler.Patch)
			protected.Post("/api/accounts/{accountID}/probe", accountHandler.Probe)
			protected.Post("/api/accounts/{accountID}/pause", accountHandler.Pause)
			protected.Put("/api/accounts/{accountID}/alias", accountHandler.Alias)
			protected.Put("/api/accounts/{accountID}/limit-warmup", accountHandler.LimitWarmup)
			protected.Put("/api/accounts/{accountID}/routing-policy", accountHandler.RoutingPolicy)
			protected.Delete("/api/accounts/{accountID}", accountHandler.Delete)
			protected.Get("/api/accounts/{accountID}/trends", accountHandler.Trends)
			protected.Get("/api/dashboard/overview", dashboardHandler.Overview)
			protected.Get("/api/dashboard/projections", dashboardHandler.Projections)
			protected.Get("/api/request-logs", requestLogsHandler.List)
			protected.Get("/api/request-logs/options", requestLogsHandler.Options)

			protected.Get("/api/usage/summary", usageHandler.Summary)
			protected.Get("/api/usage/history", usageHandler.History)
			protected.Get("/api/usage/window", usageHandler.Window)

			protected.Get("/api/settings", settingsHandler.Get)
			protected.Put("/api/settings", settingsHandler.Update)
			protected.Get("/api/settings/runtime/connect-address", settingsHandler.ConnectAddress)
			protected.Get("/api/settings/upstream-proxy", settingsHandler.UpstreamProxyAdmin)
			protected.Post("/api/settings/upstream-proxy/endpoints", settingsHandler.CreateProxyEndpoint)
			protected.Post("/api/settings/upstream-proxy/pools", settingsHandler.CreateProxyPool)
			protected.Post("/api/settings/upstream-proxy/pools/{poolID}/members", settingsHandler.AddProxyPoolMember)
			protected.Put("/api/settings/upstream-proxy/accounts/{accountID}/binding", settingsHandler.PutAccountProxyBinding)

			protected.Get("/api/firewall/ips", firewallHandler.List)
			protected.Post("/api/firewall/ips", firewallHandler.Create)
			protected.Delete("/api/firewall/ips/{ipAddress}", firewallHandler.Delete)

			protected.Get("/api/sticky-sessions", stickySessionsHandler.List)
			protected.Post("/api/sticky-sessions/purge", stickySessionsHandler.Purge)
			protected.Post("/api/sticky-sessions/delete", stickySessionsHandler.DeleteMany)
			protected.Post("/api/sticky-sessions/delete-filtered", stickySessionsHandler.DeleteFiltered)
			protected.Delete("/api/sticky-sessions/{kind}/{key}", stickySessionsHandler.DeleteOne)

			protected.Get("/api/quota-planner/settings", quotaPlannerHandler.GetSettings)
			protected.Put("/api/quota-planner/settings", quotaPlannerHandler.UpdateSettings)
			protected.Get("/api/quota-planner/decisions", quotaPlannerHandler.ListDecisions)
			protected.Get("/api/quota-planner/forecast", quotaPlannerHandler.Forecast)
			protected.Post("/api/quota-planner/warm-now", quotaPlannerHandler.WarmNow)
			protected.Post("/api/quota-planner/decisions/{decisionID}/cancel", quotaPlannerHandler.CancelDecision)

			protected.Get("/api/api-keys", apiKeysHandler.List)
			protected.Get("/api/api-keys/", apiKeysHandler.List)
			protected.Post("/api/api-keys", apiKeysHandler.Create)
			protected.Post("/api/api-keys/", apiKeysHandler.Create)
			protected.Patch("/api/api-keys/{keyID}", apiKeysHandler.Update)
			protected.Delete("/api/api-keys/{keyID}", apiKeysHandler.Delete)
			protected.Post("/api/api-keys/{keyID}/regenerate", apiKeysHandler.Regenerate)

			protected.Get("/api/api-keys/{keyID}/trends", apiKeysHandler.Trends)
			protected.Get("/api/api-keys/{keyID}/usage-7d", apiKeysHandler.Usage7Day)

			protected.Post("/api/oauth/start", oauthHandler.Start)
			protected.Get("/api/oauth/status", oauthHandler.Status)
			protected.Post("/api/oauth/complete", oauthHandler.Complete)
			protected.Post("/api/oauth/manual-callback", oauthHandler.ManualCallback)

			protected.Get("/api/models", modelsHandler.List)

			protected.Get("/api/reports", reportsHandler.Get)

			protected.Get("/api/audit-logs", auditHandler.List)
			protected.Get("/api/conversation-archive/files", conversationArchiveHandler.ListFiles)
			protected.Get("/api/conversation-archive/records", conversationArchiveHandler.ListRecords)
		})
	})

	mountDashboardSPA(router, cfg.DashboardDistDir)
	return router
}

func mountDashboardSPA(router *chi.Mux, distDir string) {
	distDir = strings.TrimSpace(distDir)
	if distDir == "" {
		return
	}
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		cleanPath := filepath.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
		relativePath := strings.TrimPrefix(cleanPath, "/")
		if relativePath == "" || relativePath == "." {
			serveDashboardIndex(w, r, distDir)
			return
		}
		filePath := filepath.Join(distDir, filepath.FromSlash(relativePath))
		if isPathInside(distDir, filePath) {
			if stat, err := os.Stat(filePath); err == nil && !stat.IsDir() {
				http.ServeFile(w, r, filePath)
				return
			}
		}
		if isAPILikePath(cleanPath) {
			http.NotFound(w, r)
			return
		}
		serveDashboardIndex(w, r, distDir)
	})
}

func serveDashboardIndex(w http.ResponseWriter, r *http.Request, distDir string) {
	indexPath := filepath.Join(distDir, "index.html")
	if stat, err := os.Stat(indexPath); err != nil || stat.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, indexPath)
}

func isAPILikePath(path string) bool {
	for _, prefix := range []string{"/api/", "/backend-api/", "/v1/", "/internal/", "/health"} {
		if path == prefix[:len(prefix)-1] || strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func isPathInside(root string, candidate string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, candidateAbs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func sessionManager() *scs.SessionManager {
	manager := scs.New()
	manager.Cookie.Name = "codex_lb_go_session"
	manager.Cookie.HttpOnly = true
	manager.Cookie.SameSite = http.SameSiteLaxMode
	manager.Lifetime = 12 * time.Hour
	manager.IdleTimeout = 2 * time.Hour
	return manager
}

func accessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			next.ServeHTTP(w, r)
			logger.Info("request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds())
		})
	}
}
