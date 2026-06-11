package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/audit"
)

func TestBuildDepletionByWindowComputesWorstRiskWindows(t *testing.T) {
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	rows := []UsageHistoryRow{
		usageHistoryRow("safe", "primary", now.Add(-2*time.Hour), 5, now.Add(3*time.Hour), 300),
		usageHistoryRow("safe", "primary", now.Add(-time.Hour), 6, now.Add(3*time.Hour), 300),
		usageHistoryRow("risky", "primary", now.Add(-2*time.Hour), 10, now.Add(time.Hour), 300),
		usageHistoryRow("risky", "primary", now.Add(-time.Hour), 70, now.Add(time.Hour), 300),
		usageHistoryRow("secondary", "monthly", now.Add(-4*time.Hour), 15, now.Add(24*time.Hour), 10080),
		usageHistoryRow("secondary", "monthly", now.Add(-2*time.Hour), 45, now.Add(24*time.Hour), 10080),
	}

	primary, secondary := buildDepletionByWindow(rows, now)

	if primary == nil {
		t.Fatal("expected primary depletion")
	}
	if primary.RiskLevel != "critical" {
		t.Fatalf("expected critical primary risk, got %#v", primary)
	}
	if primary.BurnRate <= 1 {
		t.Fatalf("expected primary burn rate above sustainable rate, got %#v", primary)
	}
	if primary.ProjectedExhaustionAt == nil || primary.SecondsUntilExhaustion == nil {
		t.Fatalf("expected projected primary exhaustion, got %#v", primary)
	}
	if secondary == nil {
		t.Fatal("expected secondary depletion")
	}
	if secondary.Risk <= 0 {
		t.Fatalf("expected positive secondary risk, got %#v", secondary)
	}
}

func TestBuildDepletionByWindowRequiresTwoIncreasingSamples(t *testing.T) {
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	primary, secondary := buildDepletionByWindow([]UsageHistoryRow{
		usageHistoryRow("single", "primary", now.Add(-time.Hour), 40, now.Add(time.Hour), 300),
		usageHistoryRow("drop", "secondary", now.Add(-2*time.Hour), 60, now.Add(time.Hour), 10080),
		usageHistoryRow("drop", "secondary", now.Add(-time.Hour), 55, now.Add(time.Hour), 10080),
	}, now)

	if primary != nil {
		t.Fatalf("expected nil primary depletion for one sample, got %#v", primary)
	}
	if secondary != nil {
		t.Fatalf("expected nil secondary depletion after usage drop, got %#v", secondary)
	}
}

func TestDepletionStateCacheReusesAndPrunesAccountWindows(t *testing.T) {
	resetDepletionStateCacheForTest()
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	rows := []UsageHistoryRow{
		usageHistoryRow("acct-1", "primary", now.Add(-2*time.Hour), 10, now.Add(time.Hour), 300),
		usageHistoryRow("acct-1", "primary", now.Add(-time.Hour), 70, now.Add(time.Hour), 300),
		usageHistoryRow("acct-2", "secondary", now.Add(-2*time.Hour), 10, now.Add(24*time.Hour), 10080),
		usageHistoryRow("acct-2", "secondary", now.Add(-time.Hour), 40, now.Add(24*time.Hour), 10080),
	}

	primary, secondary := buildDepletionByWindow(rows, now)
	if primary == nil || secondary == nil {
		t.Fatalf("expected both depletion windows, got primary=%#v secondary=%#v", primary, secondary)
	}
	if got := depletionStateCacheLenForTest(); got != 2 {
		t.Fatalf("expected two cached account windows, got %d", got)
	}

	rebuildsAfterFirstPass := depletionStateCacheRebuildsForTest()
	primaryAgain, secondaryAgain := buildDepletionByWindow(rows, now.Add(time.Minute))
	if primaryAgain == nil || secondaryAgain == nil {
		t.Fatalf("expected cached depletion windows, got primary=%#v secondary=%#v", primaryAgain, secondaryAgain)
	}
	if got := depletionStateCacheRebuildsForTest(); got != rebuildsAfterFirstPass {
		t.Fatalf("expected unchanged histories to reuse cached EWMA state, rebuilds before=%d after=%d", rebuildsAfterFirstPass, got)
	}

	buildDepletionByWindow(rows[:2], now.Add(2*time.Minute))
	if got := depletionStateCacheLenForTest(); got != 1 {
		t.Fatalf("expected absent secondary cache entry to be pruned, got %d", got)
	}
}

func TestDepletionStateCacheInvalidatesCorrectedRows(t *testing.T) {
	resetDepletionStateCacheForTest()
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	rows := []UsageHistoryRow{
		usageHistoryRow("acct-1", "primary", now.Add(-2*time.Hour), 10, now.Add(time.Hour), 300),
		usageHistoryRow("acct-1", "primary", now.Add(-time.Hour), 40, now.Add(time.Hour), 300),
	}
	primary, _ := buildDepletionByWindow(rows, now)
	if primary == nil {
		t.Fatal("expected primary depletion")
	}

	corrected := append([]UsageHistoryRow(nil), rows...)
	corrected[1].UsedPercent = 70
	correctedPrimary, _ := buildDepletionByWindow(corrected, now)
	if correctedPrimary == nil {
		t.Fatal("expected corrected primary depletion")
	}
	if correctedPrimary.BurnRate <= primary.BurnRate {
		t.Fatalf("expected corrected row to rebuild a higher burn rate: before=%#v after=%#v", primary, correctedPrimary)
	}
}

func TestProjectionsHandlerReturnsComputedDepletion(t *testing.T) {
	repo := newDashboardTestRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := repo.store.DB().ExecContext(ctx, `
		INSERT INTO accounts (
			id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
			id_token_encrypted, last_refresh, status
		) VALUES (
			'acct-1', 'acct@example.com', 'pro', X'00', X'00', X'00',
			datetime('now'), 'active'
		)
	`); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := repo.store.DB().ExecContext(ctx, `
		INSERT INTO usage_history (account_id, recorded_at, window, used_percent, reset_at, window_minutes)
		VALUES
		  ('acct-1', ?, 'primary', 10, ?, 300),
		  ('acct-1', ?, 'primary', 70, ?, 300)
	`, sqliteTime(now.Add(-2*time.Hour)), now.Add(time.Hour).Unix(), sqliteTime(now.Add(-time.Hour)), now.Add(time.Hour).Unix()); err != nil {
		t.Fatalf("insert usage history: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/dashboard/projections", nil)
	Handler{repo: repo}.Projections(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload projectionsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.DepletionPrimary == nil {
		t.Fatalf("expected primary depletion in response: %s", recorder.Body.String())
	}
	if payload.DepletionSecondary != nil {
		t.Fatalf("expected null secondary depletion, got %#v", payload.DepletionSecondary)
	}
	if payload.WeeklyCreditPace != nil {
		t.Fatalf("expected null weekly credit pace, got %#v", payload.WeeklyCreditPace)
	}
}

func TestBuildWeeklyCreditPaceComputesFreshActiveAccounts(t *testing.T) {
	now := time.Date(2030, 1, 8, 12, 0, 0, 0, time.UTC)
	resetAt := now.Add(84 * time.Hour).Format(time.RFC3339)
	capacity := 50400.0
	remaining := 20000.0
	windowMinutes := int64(10080)
	summaries := []accounts.AccountSummary{
		{
			AccountID:                 "acct-1",
			Status:                    "active",
			CapacityCreditsSecondary:  &capacity,
			RemainingCreditsSecondary: &remaining,
			ResetAtSecondary:          &resetAt,
			WindowMinutesSecondary:    &windowMinutes,
		},
	}
	rows := []UsageHistoryRow{
		usageHistoryRow("acct-1", "secondary", now.Add(-4*time.Minute), 20, now.Add(84*time.Hour), 10080),
		usageHistoryRow("acct-1", "secondary", now.Add(-time.Minute), 60, now.Add(84*time.Hour), 10080),
	}

	pace := buildWeeklyCreditPace(summaries, rows, now, 60, nil)

	if pace == nil {
		t.Fatal("expected weekly credit pace")
	}
	if pace.AccountCount != 1 || pace.StaleAccountCount != 0 || pace.InactiveAccountCount != 0 {
		t.Fatalf("unexpected account counts: %#v", pace)
	}
	if pace.ForecastBurnRateCreditsPerHour == nil || *pace.ForecastBurnRateCreditsPerHour <= 0 {
		t.Fatalf("expected forecast burn rate, got %#v", pace)
	}
	if pace.Status != "danger" {
		t.Fatalf("expected projected shortfall danger status, got %#v", pace)
	}
	if pace.Confidence != "high" {
		t.Fatalf("expected high confidence, got %#v", pace)
	}
}

func TestProjectionsHandlerReturnsComputedWeeklyCreditPace(t *testing.T) {
	repo := newDashboardTestRepo(t)
	accountHandler := accounts.NewHandler(accounts.NewRepository(repo.store), nil, audit.NewRepository(repo.store))
	ctx := context.Background()
	now := time.Now().UTC()
	resetAt := now.Add(84 * time.Hour).Unix()
	if _, err := repo.store.DB().ExecContext(ctx, `
		INSERT INTO accounts (
			id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
			id_token_encrypted, last_refresh, status
		) VALUES (
			'acct-1', 'acct@example.com', 'pro', X'00', X'00', X'00',
			datetime('now'), 'active'
		)
	`); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := repo.store.DB().ExecContext(ctx, `
		INSERT INTO usage_history (account_id, recorded_at, window, used_percent, reset_at, window_minutes)
		VALUES
		  ('acct-1', ?, 'secondary', 20, ?, 10080),
		  ('acct-1', ?, 'secondary', 60, ?, 10080)
	`, sqliteTime(now.Add(-4*time.Minute)), resetAt, sqliteTime(now.Add(-time.Minute)), resetAt); err != nil {
		t.Fatalf("insert usage history: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/dashboard/projections", nil)
	NewHandler(repo, accountHandler, 60*time.Second).Projections(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	pace, ok := payload["weeklyCreditPace"].(map[string]any)
	if !ok {
		t.Fatalf("expected weeklyCreditPace object, got %s", recorder.Body.String())
	}
	if pace["status"] != "danger" {
		t.Fatalf("expected danger pace status, got %#v", pace)
	}
	if pace["accountCount"] != float64(1) {
		t.Fatalf("expected one account, got %#v", pace)
	}
}

func usageHistoryRow(accountID string, window string, recordedAt time.Time, usedPercent float64, resetAt time.Time, windowMinutes int64) UsageHistoryRow {
	return UsageHistoryRow{
		AccountID:     accountID,
		RecordedAt:    sqliteTime(recordedAt),
		Window:        window,
		UsedPercent:   usedPercent,
		ResetAt:       sqlNullInt64(resetAt.Unix()),
		WindowMinutes: sqlNullInt64(windowMinutes),
	}
}

func sqliteTime(value time.Time) string {
	return value.UTC().Format("2006-01-02 15:04:05")
}

func sqlNullInt64(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: true}
}

func resetDepletionStateCacheForTest() {
	depletionStateCache.Lock()
	depletionStateCache.entries = map[string]depletionCacheEntry{}
	depletionStateCache.rebuilds = 0
	depletionStateCache.Unlock()
}

func depletionStateCacheLenForTest() int {
	depletionStateCache.Lock()
	defer depletionStateCache.Unlock()
	return len(depletionStateCache.entries)
}

func depletionStateCacheRebuildsForTest() int {
	depletionStateCache.Lock()
	defer depletionStateCache.Unlock()
	return depletionStateCache.rebuilds
}
