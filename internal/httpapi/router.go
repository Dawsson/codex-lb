package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/auth"
	"github.com/soju06/codex-lb/internal/dashboard"
	"github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/health"
)

func NewRouter(store *db.Store, logger *slog.Logger) http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(accessLog(logger))

	healthHandler := health.NewHandler(store)
	accountRepo := accounts.NewRepository(store)
	accountHandler := accounts.NewHandler(accountRepo)
	dashboardHandler := dashboard.NewHandler(dashboard.NewRepository(store), accountHandler)
	sessionStore := sessionManager()
	authHandler := auth.NewHandler(auth.NewRepository(store), sessionStore)

	router.Get("/health/live", healthHandler.Live)
	router.Get("/health/ready", healthHandler.Ready)
	router.Group(func(r chi.Router) {
		r.Use(sessionStore.LoadAndSave)
		r.Get("/api/auth/session", authHandler.Session)
		r.Post("/api/auth/login", authHandler.Login)
		r.Post("/api/auth/logout", authHandler.Logout)

		r.Get("/api/dashboard-auth/session", authHandler.Session)
		r.Post("/api/dashboard-auth/password/login", authHandler.Login)
		r.Post("/api/dashboard-auth/logout", authHandler.Logout)

		r.Group(func(protected chi.Router) {
			protected.Use(authHandler.RequireSession)
			protected.Get("/api/accounts", accountHandler.List)
			protected.Get("/api/dashboard/overview", dashboardHandler.Overview)
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
