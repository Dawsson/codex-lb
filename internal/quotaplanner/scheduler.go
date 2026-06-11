package quotaplanner

import (
	"context"
	"database/sql"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/limitwarmup"
	"github.com/soju06/codex-lb/internal/requestlogs"
	"github.com/soju06/codex-lb/internal/scheduling"
)

const quotaPlannerLeaderTTL = 90 * time.Second

type Scheduler struct {
	store        *db.Store
	logger       *slog.Logger
	cfg          config.Config
	repo         Repository
	accountRepo  accounts.Repository
	requestLogs  requestlogs.Repository
	warmupSender limitwarmup.Sender
	leaderID     string
	now          func() time.Time

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewScheduler(store *db.Store, logger *slog.Logger, cfg config.Config, sender limitwarmup.Sender, leaderID string) *Scheduler {
	if leaderID == "" {
		leaderID = "quota-planner"
	}
	return &Scheduler{
		store:        store,
		logger:       logger,
		cfg:          cfg,
		repo:         NewRepository(store),
		accountRepo:  accounts.NewRepository(store),
		requestLogs:  requestlogs.NewRepository(store),
		warmupSender: sender,
		leaderID:     leaderID,
		now:          func() time.Time { return time.Now().UTC() },
	}
}

func (s *Scheduler) WithClock(now func() time.Time) *Scheduler {
	s.now = now
	return s
}

func (s *Scheduler) Start(ctx context.Context) {
	if !s.cfg.QuotaPlannerEnabled {
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

func (s *Scheduler) Stop(ctx context.Context) error {
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

func (s *Scheduler) run(ctx context.Context) {
	defer close(s.done)
	s.RunOnce(ctx)
	ticker := time.NewTicker(s.cfg.QuotaPlannerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.RunOnce(ctx)
		}
	}
}

func (s *Scheduler) RunOnce(ctx context.Context) {
	acquired, err := scheduling.NewRepository(s.store).TryAcquireLeader(ctx, s.leaderID, quotaPlannerLeaderTTL)
	if err != nil {
		s.logger.Warn("quota planner leader acquire failed", "error", err)
		return
	}
	if !acquired {
		return
	}
	settings, err := s.repo.GetSettings(ctx)
	if err != nil {
		s.logger.Warn("quota planner settings load failed", "error", err)
		return
	}
	if strings.EqualFold(settings.Mode, "off") {
		return
	}
	now := s.now()
	if strings.EqualFold(settings.Mode, "auto") {
		s.executeDueWarmups(ctx, settings, now)
		return
	}
	s.logNoOp(ctx, settings, now)
}

func (s *Scheduler) executeDueWarmups(ctx context.Context, settings Settings, now time.Time) {
	decisions, err := s.repo.DuePlannedWarmups(ctx, now, settings.MaxWarmupsPerDay)
	if err != nil {
		s.logger.Warn("quota planner due warmup query failed", "error", err)
		return
	}
	if len(decisions) == 0 {
		s.logNoOp(ctx, settings, now)
		return
	}
	for _, decision := range decisions {
		if err := s.executeDecision(ctx, settings, decision); err != nil {
			s.logger.Warn("quota planner warmup execution failed", "decision_id", decision.ID, "error", err)
		}
	}
}

func (s *Scheduler) executeDecision(ctx context.Context, settings Settings, decision Decision) error {
	if !decision.AccountID.Valid || strings.TrimSpace(decision.AccountID.String) == "" {
		_, _, err := s.repo.UpdateDecisionStatus(ctx, decision.ID, "skipped", "account_not_found", sql.NullString{}, "planned")
		return err
	}
	account, err := s.accountRepo.Get(ctx, decision.AccountID.String)
	if err != nil {
		return err
	}
	if account == nil {
		_, _, err := s.repo.UpdateDecisionStatus(ctx, decision.ID, "skipped", "account_not_found", sql.NullString{}, "planned")
		return err
	}
	if strings.ToLower(account.Status) != "active" {
		_, _, err := s.repo.UpdateDecisionStatus(ctx, decision.ID, "skipped", "account_not_active", sql.NullString{}, "planned")
		return err
	}
	if _, ok, err := s.repo.UpdateDecisionStatus(ctx, decision.ID, "executing", "warmup_executing", sql.NullString{}, "planned"); err != nil || !ok {
		return err
	}
	requestID := "quota-warmup-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	model := "gpt-5.4-mini"
	if settings.WarmupModelPreference.Valid && strings.TrimSpace(settings.WarmupModelPreference.String) != "" {
		model = strings.TrimSpace(settings.WarmupModelPreference.String)
	}
	result, sendErr := s.warmupSender.Send(ctx, *account, limitwarmup.SendParams{
		Model:        model,
		Prompt:       "warmup",
		Instructions: "Reply with OK only.",
		RequestID:    requestID,
	})
	status := "executed"
	reason := "warmup_executed"
	logStatus := "success"
	errorCode := result.ErrorCode
	errorMessage := result.ErrorMessage
	if sendErr != nil || !result.Success {
		status = "failed"
		reason = "warmup_failed"
		logStatus = "error"
		if sendErr == nil && result.ErrorCode != "" {
			reason = "warmup_failed:" + result.ErrorCode
		}
		if sendErr != nil {
			errorCode = "warmup_failed"
			errorMessage = sendErr.Error()
		}
	}
	_ = s.requestLogs.Insert(ctx, requestlogs.InsertParams{
		RequestID:    requestID,
		RequestKind:  limitwarmup.RequestKind,
		Model:        model,
		AccountID:    &account.ID,
		PlanType:     &account.PlanType,
		Status:       logStatus,
		ErrorCode:    stringPtrIfNotEmpty(errorCode),
		ErrorMessage: stringPtrIfNotEmpty(errorMessage),
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
		LatencyMS:    &result.LatencyMS,
		Transport:    stringPtr("quota_planner"),
		Source:       stringPtr("quota_planner"),
	})
	_, _, err = s.repo.UpdateDecisionStatus(ctx, decision.ID, status, reason, sql.NullString{String: time.Now().UTC().Format("2006-01-02 15:04:05"), Valid: true}, "executing")
	return err
}

func (s *Scheduler) logNoOp(ctx context.Context, settings Settings, now time.Time) {
	key := now.UTC().Format("200601021504") + ":" + settings.Mode + ":no_op"
	exists, err := s.repo.HasDecisionWithIdempotencyKey(ctx, key)
	if err != nil || exists {
		return
	}
	_, err = s.repo.LogDecision(ctx, LogDecisionParams{
		Mode:           settings.Mode,
		Action:         "no_op",
		Score:          0,
		Reason:         sql.NullString{String: "no_due_quota_planner_actions", Valid: true},
		Status:         "skipped",
		IdempotencyKey: key,
	})
	if err != nil {
		s.logger.Warn("quota planner no-op decision log failed", "error", err)
	}
}
