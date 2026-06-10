package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/auth"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/crypto"
	"github.com/soju06/codex-lb/internal/dashboard"
	"github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/firewall"
	"github.com/soju06/codex-lb/internal/health"
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

func NewRouter(store *db.Store, logger *slog.Logger, cfg config.Config) http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(accessLog(logger))

	encryptor, err := crypto.NewEncryptor(cfg.EncryptionKeyPath)
	if err != nil {
		logger.Error("failed to initialize encryptor", "error", err)
		panic(err)
	}

	healthHandler := health.NewHandler(store)
	accountRepo := accounts.NewRepository(store)
	accountHandler := accounts.NewHandler(accountRepo)
	dashboardHandler := dashboard.NewHandler(dashboard.NewRepository(store), accountHandler)
	requestLogsRepo := requestlogs.NewRepository(store)
	requestLogsHandler := requestlogs.NewHandler(requestLogsRepo)
	usageHandler := usage.NewHandler(usage.NewRepository(store), accountRepo, requestLogsRepo)
	settingsHandler := settings.NewHandler(settings.NewRepository(store, encryptor))
	firewallHandler := firewall.NewHandler(firewall.NewRepository(store))
	stickySessionsHandler := stickysessions.NewHandler(stickysessions.NewRepository(store))
	quotaPlannerHandler := quotaplanner.NewHandler(quotaplanner.NewRepository(store))
	apiKeysHandler := apikeys.NewHandler(apikeys.NewRepository(store))
	modelsHandler := models.NewHandler(store)
	reportsHandler := reports.NewHandler(reports.NewRepository(store))
	sessionStore := sessionManager()
	authHandler := auth.NewHandler(auth.NewRepository(store), sessionStore, cfg.AuthDisabled, encryptor)
	oauthService := oauth.NewService(cfg, accountRepo, encryptor, accountHandler.InvalidateSummaryCache, logger)
	oauthHandler := oauth.NewHandler(oauthService)
	modelRegistry := proxy.NewModelRegistry(5 * time.Minute)
	proxyModelsHandler := proxy.NewModelsHandler(apikeys.NewRepository(store), settings.NewRepository(store, encryptor), modelRegistry)

	router.Get("/health/live", healthHandler.Live)
	router.Get("/health/ready", healthHandler.Ready)

	router.Get("/v1/models", proxyModelsHandler.V1Models)
	router.Get("/backend-api/codex/models", proxyModelsHandler.CodexModels)
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
		r.Post("/api/dashboard-auth/logout", authHandler.Logout)

		r.Group(func(protected chi.Router) {
			protected.Use(authHandler.RequireSession)
			protected.Get("/api/runtime/version", runtime.Version)
			protected.Get("/api/accounts", accountHandler.List)
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
		})
	})

	return router
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
