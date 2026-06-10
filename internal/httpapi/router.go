package httpapi

import (
	"log/slog"
	"net/http"
	"time"

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

	router.Get("/health/live", healthHandler.Live)
	router.Get("/health/ready", healthHandler.Ready)
	router.Get("/api/dashboard-auth/session", auth.Session)
	router.Get("/api/accounts", accountHandler.List)
	router.Get("/api/dashboard/overview", dashboardHandler.Overview)

	return router
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
