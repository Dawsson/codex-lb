package limitwarmup_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/limitwarmup"
	"github.com/soju06/codex-lb/internal/requestlogs"
	"github.com/soju06/codex-lb/internal/settings"
)

func TestServiceRunAfterUsageRefreshCreatesSendsCompletesAndLogs(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	attempts := limitwarmup.NewRepository(store)
	logs := requestlogs.NewRepository(store)
	sender := &fakeSender{result: limitwarmup.SendResult{
		RequestID:    "warmup-request-1",
		Success:      true,
		LatencyMS:    42,
		InputTokens:  int64Ptr(5),
		OutputTokens: int64Ptr(1),
	}}

	service := limitwarmup.NewService(attempts, logs, sender).WithClock(fixedWarmupTime)
	err := service.RunAfterUsageRefresh(ctx, limitwarmup.RefreshInputs{
		Accounts: []accounts.Account{warmupAccount()},
		Settings: warmupSettings(),
		Before: limitwarmup.UsageSnapshot{
			Primary: map[string]accounts.LatestUsage{"acct-1": usageEntry("acct-1", 100, 1000, 300)},
		},
		After: limitwarmup.UsageSnapshot{
			Primary: map[string]accounts.LatestUsage{"acct-1": usageEntry("acct-1", 20, 2000, 300)},
		},
		DefaultModelSlug: "gpt-5.5",
	})
	if err != nil {
		t.Fatalf("run after usage refresh: %v", err)
	}
	if sender.callCount() != 1 {
		t.Fatalf("expected one warmup send, got %d", sender.callCount())
	}

	latest, ok, err := attempts.LatestAttemptForAccount(ctx, "acct-1")
	if err != nil {
		t.Fatalf("latest attempt: %v", err)
	}
	if !ok || latest.Status != "succeeded" || latest.Model != "gpt-5.5" || latest.Window != "primary" || latest.ResetAt != 2000 {
		t.Fatalf("unexpected latest attempt: %#v ok=%v", latest, ok)
	}

	var count int
	if err := store.DB().QueryRow(`
		SELECT count(*) FROM request_logs
		 WHERE request_id = 'warmup-request-1'
		   AND request_kind = 'warmup'
		   AND source = 'limit_warmup'
		   AND status = 'success'
		   AND account_id = 'acct-1'
	`).Scan(&count); err != nil {
		t.Fatalf("query request log: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one warmup request log, got %d", count)
	}
}

func TestServiceRunAfterUsageRefreshHonorsCooldown(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	attempts := limitwarmup.NewRepository(store)
	_, created, err := attempts.TryCreateAttempt(ctx, "acct-1", "primary", 1000, "gpt-5.5", "2026-06-10 11:59:30")
	if err != nil {
		t.Fatalf("create prior attempt: %v", err)
	}
	if !created {
		t.Fatalf("expected prior attempt")
	}
	sender := &fakeSender{}
	service := limitwarmup.NewService(attempts, requestlogs.NewRepository(store), sender).WithClock(fixedWarmupTime)

	err = service.RunAfterUsageRefresh(ctx, limitwarmup.RefreshInputs{
		Accounts: []accounts.Account{warmupAccount()},
		Settings: warmupSettings(),
		Before: limitwarmup.UsageSnapshot{
			Primary: map[string]accounts.LatestUsage{"acct-1": usageEntry("acct-1", 100, 1000, 300)},
		},
		After: limitwarmup.UsageSnapshot{
			Primary: map[string]accounts.LatestUsage{"acct-1": usageEntry("acct-1", 20, 2000, 300)},
		},
		DefaultModelSlug: "gpt-5.5",
	})
	if err != nil {
		t.Fatalf("run after usage refresh: %v", err)
	}
	if sender.callCount() != 0 {
		t.Fatalf("expected cooldown to suppress sends, got %d", sender.callCount())
	}
}

func TestServiceRunAfterUsageRefreshNormalizesQuotaErrors(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	attempts := limitwarmup.NewRepository(store)
	sender := &fakeSender{result: limitwarmup.SendResult{
		RequestID:    "warmup-quota",
		Success:      false,
		LatencyMS:    7,
		ErrorCode:    "rate_limit_exceeded",
		ErrorMessage: "still out",
	}}
	service := limitwarmup.NewService(attempts, requestlogs.NewRepository(store), sender).WithClock(fixedWarmupTime)

	err := service.RunAfterUsageRefresh(ctx, limitwarmup.RefreshInputs{
		Accounts: []accounts.Account{warmupAccount()},
		Settings: warmupSettings(),
		Before: limitwarmup.UsageSnapshot{
			Primary: map[string]accounts.LatestUsage{"acct-1": usageEntry("acct-1", 100, 1000, 300)},
		},
		After: limitwarmup.UsageSnapshot{
			Primary: map[string]accounts.LatestUsage{"acct-1": usageEntry("acct-1", 20, 2000, 300)},
		},
		DefaultModelSlug: "gpt-5.5",
	})
	if err != nil {
		t.Fatalf("run after usage refresh: %v", err)
	}

	latest, ok, err := attempts.LatestAttemptForAccount(ctx, "acct-1")
	if err != nil {
		t.Fatalf("latest attempt: %v", err)
	}
	if !ok || latest.Status != "failed" || !latest.ErrorCode.Valid || latest.ErrorCode.String != "quota_still_exhausted" {
		t.Fatalf("expected normalized quota failure, got %#v ok=%v", latest, ok)
	}
}

type fakeSender struct {
	mu     sync.Mutex
	calls  int
	result limitwarmup.SendResult
	err    error
}

func (s *fakeSender) Send(context.Context, accounts.Account, limitwarmup.SendParams) (limitwarmup.SendResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return limitwarmup.SendResult{}, s.err
	}
	if s.result.RequestID == "" && s.result.LatencyMS == 0 && !s.result.Success {
		return limitwarmup.SendResult{}, errors.New("unexpected send")
	}
	return s.result, nil
}

func (s *fakeSender) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func warmupAccount() accounts.Account {
	return accounts.Account{
		ID:                 "acct-1",
		PlanType:           "plus",
		Status:             "active",
		LimitWarmupEnabled: true,
	}
}

func warmupSettings() settings.DashboardSettings {
	return settings.DashboardSettings{
		LimitWarmupEnabled:             true,
		LimitWarmupWindows:             "primary",
		LimitWarmupModel:               "auto",
		LimitWarmupPrompt:              "ping",
		LimitWarmupCooldownSeconds:     60,
		LimitWarmupMinAvailablePercent: 1,
	}
}

func usageEntry(accountID string, usedPercent float64, resetAt int64, windowMinutes int64) accounts.LatestUsage {
	return accounts.LatestUsage{
		AccountID:     accountID,
		UsedPercent:   usedPercent,
		ResetAt:       sql.NullInt64{Int64: resetAt, Valid: true},
		WindowMinutes: sql.NullInt64{Int64: windowMinutes, Valid: true},
	}
}

func fixedWarmupTime() time.Time {
	return time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
}

func int64Ptr(value int64) *int64 {
	return &value
}
