package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/auth"
	"github.com/soju06/codex-lb/internal/authguardian"
	"github.com/soju06/codex-lb/internal/cacheinvalidation"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/crypto"
	"github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/httpapi"
	"github.com/soju06/codex-lb/internal/limitwarmup"
	"github.com/soju06/codex-lb/internal/proxy"
	"github.com/soju06/codex-lb/internal/quotaplanner"
	"github.com/soju06/codex-lb/internal/stickysessions"
	"github.com/soju06/codex-lb/internal/usagerefresh"
)

func main() {
	checkOnly := flag.Bool("check", false, "open the configured database and exit")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	store, err := db.Open(cfg)
	if err != nil {
		logger.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := store.Ping(context.Background()); err != nil {
		logger.Error("database ping failed", "error", err)
		os.Exit(1)
	}
	if cfg.RunMigrations {
		if err := store.RunMigrations(cfg.MigrationsDir); err != nil {
			logger.Error("database migration failed", "error", err)
			os.Exit(1)
		}
	}
	if *checkOnly {
		logger.Info("go api check passed", "database", cfg.DatabasePath)
		return
	}

	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()
	leaderID := uuid.NewString()
	encryptor, err := crypto.NewEncryptor(cfg.EncryptionKeyPath)
	if err != nil {
		logger.Error("failed to initialize scheduler encryptor", "error", err)
		os.Exit(1)
	}
	bootstrapService := auth.NewBootstrapService(auth.NewRepository(store), encryptor, cfg.DashboardBootstrapToken, logger)
	bootstrapToken, err := bootstrapService.EnsureAutoToken(context.Background())
	if err != nil {
		logger.Error("failed to ensure dashboard bootstrap token", "error", err)
		os.Exit(1)
	}
	bootstrapService.LogToken(bootstrapToken, "first-run")
	cacheInvalidationPoller := cacheinvalidation.NewPoller(store, logger, cfg)
	usageRefreshScheduler := usagerefresh.NewRefreshScheduler(store, logger, cfg, nil, func() {
		_ = cacheInvalidationPoller.Bump(context.Background(), cacheinvalidation.NamespaceSettings)
	})
	usageRefreshScheduler.SetLeaderID(leaderID)
	usageRefreshScheduler.Start(rootCtx)
	apiKeyLimitResetScheduler := apikeys.NewLimitResetScheduler(store, logger, cfg, leaderID)
	apiKeyLimitResetScheduler.Start(rootCtx)
	stickyCleanupScheduler := stickysessions.NewCleanupScheduler(store, logger, cfg, leaderID)
	stickyCleanupScheduler.Start(rootCtx)
	modelRegistry := proxy.NewModelRegistry(5 * time.Minute)
	modelRefreshScheduler := proxy.NewModelRefreshScheduler(store, logger, cfg, modelRegistry, nil, leaderID)
	modelRefreshScheduler.Start(rootCtx)
	authGuardianScheduler := authguardian.NewScheduler(store, logger, cfg, nil, leaderID, func() {
		_ = cacheInvalidationPoller.Bump(context.Background(), cacheinvalidation.NamespaceSettings)
	})
	authGuardianScheduler.Start(rootCtx)
	quotaPlannerScheduler := quotaplanner.NewScheduler(store, logger, cfg, limitwarmup.NewStreamingSender(encryptor, cfg), leaderID)
	quotaPlannerScheduler.Start(rootCtx)
	server := &http.Server{
		Addr: cfg.Addr(),
		Handler: httpapi.NewRouter(store, logger, cfg, httpapi.RouterOptions{
			CacheInvalidationPoller: cacheInvalidationPoller,
			ModelRegistry:           modelRegistry,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	cacheInvalidationPoller.Start(rootCtx)

	go func() {
		logger.Info("go api listening", "addr", server.Addr, "database", cfg.DatabasePath)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	cancelRoot()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := usageRefreshScheduler.Stop(ctx); err != nil {
		logger.Error("usage refresh scheduler shutdown failed", "error", err)
	}
	if err := apiKeyLimitResetScheduler.Stop(ctx); err != nil {
		logger.Error("api key limit reset scheduler shutdown failed", "error", err)
	}
	if err := cacheInvalidationPoller.Stop(ctx); err != nil {
		logger.Error("cache invalidation poller shutdown failed", "error", err)
	}
	if err := stickyCleanupScheduler.Stop(ctx); err != nil {
		logger.Error("sticky session cleanup scheduler shutdown failed", "error", err)
	}
	if err := modelRefreshScheduler.Stop(ctx); err != nil {
		logger.Error("model refresh scheduler shutdown failed", "error", err)
	}
	if err := authGuardianScheduler.Stop(ctx); err != nil {
		logger.Error("auth guardian scheduler shutdown failed", "error", err)
	}
	if err := quotaPlannerScheduler.Stop(ctx); err != nil {
		logger.Error("quota planner scheduler shutdown failed", "error", err)
	}
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("server shutdown failed", "error", err)
		os.Exit(1)
	}
}
