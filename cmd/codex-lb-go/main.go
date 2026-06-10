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

	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/httpapi"
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

	server := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           httpapi.NewRouter(store, logger),
		ReadHeaderTimeout: 10 * time.Second,
	}

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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("server shutdown failed", "error", err)
		os.Exit(1)
	}
}
