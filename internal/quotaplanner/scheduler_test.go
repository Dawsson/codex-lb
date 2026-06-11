package quotaplanner_test

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/limitwarmup"
	"github.com/soju06/codex-lb/internal/quotaplanner"
)

func TestQuotaPlannerSchedulerLogsNoOpDecision(t *testing.T) {
	ctx := context.Background()
	store := newQuotaPlannerTestStore(t)
	repo := quotaplanner.NewRepository(store)
	if _, err := repo.UpsertSettings(ctx, quotaplanner.Settings{
		Mode:                   "shadow",
		Timezone:               "UTC",
		WorkingDays:            []int{0, 1, 2, 3, 4},
		WorkingHoursStart:      "09:00",
		WorkingHoursEnd:        "18:00",
		PrewarmEnabled:         true,
		PrewarmLeadMinutes:     300,
		MaxWarmupsPerDay:       3,
		MaxWarmupCreditsPerDay: 0,
		MinExpectedGain:        1,
		ForecastQuantile:       "p75",
	}); err != nil {
		t.Fatalf("upsert settings: %v", err)
	}
	scheduler := quotaplanner.NewScheduler(store, slog.Default(), config.Config{
		QuotaPlannerEnabled:  true,
		QuotaPlannerInterval: time.Hour,
	}, fakeSchedulerSender{}, "quota-test").WithClock(func() time.Time {
		return time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	})
	scheduler.RunOnce(ctx)

	decisions, err := repo.RecentDecisions(ctx, 10)
	if err != nil {
		t.Fatalf("recent decisions: %v", err)
	}
	if len(decisions) != 1 || decisions[0].Action != "no_op" || decisions[0].Status != "skipped" {
		t.Fatalf("expected skipped no-op decision, got %#v", decisions)
	}
}

func TestQuotaPlannerSchedulerExecutesDueWarmup(t *testing.T) {
	ctx := context.Background()
	store := newQuotaPlannerTestStore(t)
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO accounts (
			id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
			id_token_encrypted, last_refresh, status
		) VALUES ('acct-1', 'a@example.com', 'plus', x'00', x'00', x'00', '2026-01-01 00:00:00', 'active')
	`); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	repo := quotaplanner.NewRepository(store)
	if _, err := repo.UpsertSettings(ctx, quotaplanner.Settings{
		Mode:                   "auto",
		Timezone:               "UTC",
		WorkingDays:            []int{0, 1, 2, 3, 4},
		WorkingHoursStart:      "09:00",
		WorkingHoursEnd:        "18:00",
		PrewarmEnabled:         true,
		PrewarmLeadMinutes:     300,
		MaxWarmupsPerDay:       3,
		MaxWarmupCreditsPerDay: 0,
		MinExpectedGain:        1,
		ForecastQuantile:       "p75",
		WarmupModelPreference:  sql.NullString{String: "gpt-5.5", Valid: true},
	}); err != nil {
		t.Fatalf("upsert settings: %v", err)
	}
	decision, err := repo.LogDecision(ctx, quotaplanner.LogDecisionParams{
		Mode:           "auto",
		Action:         "warmup",
		AccountID:      sql.NullString{String: "acct-1", Valid: true},
		ScheduledAt:    "2026-06-11 11:00:00",
		Reason:         sql.NullString{String: "test", Valid: true},
		Status:         "planned",
		IdempotencyKey: "test:due",
	})
	if err != nil {
		t.Fatalf("log decision: %v", err)
	}
	scheduler := quotaplanner.NewScheduler(store, slog.Default(), config.Config{
		QuotaPlannerEnabled:  true,
		QuotaPlannerInterval: time.Hour,
	}, fakeSchedulerSender{}, "quota-test").WithClock(func() time.Time {
		return time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	})
	scheduler.RunOnce(ctx)

	updated, err := repo.GetDecision(ctx, decision.ID)
	if err != nil {
		t.Fatalf("get decision: %v", err)
	}
	if updated == nil || updated.Status != "executed" || !updated.ExecutedAt.Valid {
		t.Fatalf("expected executed decision, got %#v", updated)
	}
	var count int
	if err := store.DB().QueryRowContext(ctx, `
		SELECT count(*) FROM request_logs
		 WHERE account_id = 'acct-1'
		   AND request_kind = 'warmup'
		   AND transport = 'quota_planner'
		   AND status = 'success'
	`).Scan(&count); err != nil {
		t.Fatalf("count request logs: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one warmup request log, got %d", count)
	}
}

type fakeSchedulerSender struct{}

func (fakeSchedulerSender) Send(context.Context, accounts.Account, limitwarmup.SendParams) (limitwarmup.SendResult, error) {
	return limitwarmup.SendResult{
		RequestID:    "quota-scheduler-warmup",
		Success:      true,
		LatencyMS:    10,
		InputTokens:  int64Ptr(2),
		OutputTokens: int64Ptr(1),
	}, nil
}
