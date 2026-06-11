package stickysessions

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/scheduling"
)

const cleanupLeaderTTL = 90 * time.Second

type CleanupScheduler struct {
	store    *db.Store
	logger   *slog.Logger
	cfg      config.Config
	leaderID string

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewCleanupScheduler(store *db.Store, logger *slog.Logger, cfg config.Config, leaderID string) *CleanupScheduler {
	if leaderID == "" {
		leaderID = uuid.NewString()
	}
	return &CleanupScheduler{
		store:    store,
		logger:   logger,
		cfg:      cfg,
		leaderID: leaderID,
	}
}

func (s *CleanupScheduler) Start(ctx context.Context) {
	if !s.cfg.StickyCleanupEnabled {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.run(runCtx)
}

func (s *CleanupScheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	cancel := s.cancel
	done := s.done
	s.cancel = nil
	s.done = nil
	s.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *CleanupScheduler) run(ctx context.Context) {
	defer close(s.done)
	s.cleanupOnce(ctx)
	ticker := time.NewTicker(s.cfg.StickyCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanupOnce(ctx)
		}
	}
}

func (s *CleanupScheduler) cleanupOnce(ctx context.Context) {
	leaderRepo := scheduling.NewRepository(s.store)
	acquired, err := leaderRepo.TryAcquireLeader(ctx, s.leaderID, cleanupLeaderTTL)
	if err != nil {
		s.logger.Warn("sticky session cleanup leader acquire failed", "error", err)
		return
	}
	if !acquired {
		return
	}

	repo := NewRepository(s.store)
	maxAgeSeconds, err := repo.CacheAffinityMaxAgeSeconds(ctx)
	if err != nil {
		s.logger.Warn("sticky session cleanup ttl load failed", "error", err)
		return
	}
	cutoff := time.Now().UTC().Add(-time.Duration(maxAgeSeconds) * time.Second).Format("2006-01-02 15:04:05")
	deleted, err := repo.PurgePromptCacheBefore(ctx, cutoff)
	if err != nil {
		s.logger.Warn("sticky session cleanup failed", "error", err)
		return
	}
	if deleted > 0 {
		s.logger.Info("stale prompt-cache sticky sessions purged", "count", deleted)
	}
}
