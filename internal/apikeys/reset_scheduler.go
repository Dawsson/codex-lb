package apikeys

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

const apiKeyResetLeaderTTL = 90 * time.Second

type LimitResetScheduler struct {
	store    *db.Store
	logger   *slog.Logger
	cfg      config.Config
	leaderID string

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewLimitResetScheduler(store *db.Store, logger *slog.Logger, cfg config.Config, leaderID string) *LimitResetScheduler {
	if leaderID == "" {
		leaderID = uuid.NewString()
	}
	return &LimitResetScheduler{
		store:    store,
		logger:   logger,
		cfg:      cfg,
		leaderID: leaderID,
	}
}

func (s *LimitResetScheduler) Start(ctx context.Context) {
	if !s.cfg.APIKeyLimitResetEnabled {
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

func (s *LimitResetScheduler) Stop(ctx context.Context) error {
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

func (s *LimitResetScheduler) run(ctx context.Context) {
	defer close(s.done)
	s.resetOnce(ctx)
	ticker := time.NewTicker(s.cfg.APIKeyLimitResetInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.resetOnce(ctx)
		}
	}
}

func (s *LimitResetScheduler) resetOnce(ctx context.Context) {
	leaderRepo := scheduling.NewRepository(s.store)
	acquired, err := leaderRepo.TryAcquireLeader(ctx, s.leaderID, apiKeyResetLeaderTTL)
	if err != nil {
		s.logger.Warn("api key limit reset leader acquire failed", "error", err)
		return
	}
	if !acquired {
		return
	}

	repo := NewRepository(s.store)
	now := time.Now().UTC()
	resetCount, err := repo.ResetExpiredLimits(ctx, now.Format("2006-01-02 15:04:05"))
	if err != nil {
		s.logger.Warn("api key limit reset failed", "error", err)
		return
	}
	if resetCount > 0 {
		s.logger.Info("api key limits reset", "count", resetCount)
	}

	staleAge := s.cfg.APIKeyReservationStaleAge
	if staleAge <= 0 {
		staleAge = 6 * time.Hour
	}
	releasedCount, err := repo.ReleaseStaleUsageReservations(ctx, now.Add(-staleAge).Format("2006-01-02 15:04:05"))
	if err != nil {
		s.logger.Warn("stale api key usage reservation release failed", "error", err)
		return
	}
	if releasedCount > 0 {
		s.logger.Info("stale api key usage reservations released", "count", releasedCount)
	}
}
