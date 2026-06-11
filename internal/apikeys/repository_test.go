package apikeys_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/config"
	dbpkg "github.com/soju06/codex-lb/internal/db"
)

func newTestStore(t *testing.T) *dbpkg.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "store.db")
	store, err := dbpkg.Open(config.Config{DatabasePath: dbPath})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.RunMigrations("../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return store
}

func TestSelfUsageResetsExpiredLimitsAndExcludesWarmupDeletedRows(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := apikeys.NewRepository(store)
	now := time.Now().UTC()
	oldReset := now.Add(-time.Hour).Format("2006-01-02 15:04:05")
	logTime := now.Format("2006-01-02 15:04:05")

	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO api_keys (id, name, key_hash, key_prefix, is_active, created_at)
		VALUES ('key-1', 'Test Key', 'hash', 'clb_test', 1, ?)
	`, logTime); err != nil {
		t.Fatalf("insert api key: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO api_key_limits (api_key_id, limit_type, limit_window, max_value, current_value, model_filter, reset_at)
		VALUES ('key-1', 'requests', 'daily', 10, 7, NULL, ?)
	`, oldReset); err != nil {
		t.Fatalf("insert limit: %v", err)
	}
	requests := []struct {
		id          string
		kind        string
		deletedAt   any
		inputTokens int
		output      any
		reasoning   any
		cached      int
	}{
		{"req-ok", "normal", nil, 100, 20, nil, 30},
		{"req-reasoning", "normal", nil, 10, nil, 5, 50},
		{"req-warmup", "warmup", nil, 1000, 1000, nil, 1000},
		{"req-deleted", "normal", logTime, 1000, 1000, nil, 1000},
	}
	for _, request := range requests {
		if _, err := store.DB().ExecContext(ctx, `
			INSERT INTO request_logs (
				api_key_id, request_id, request_kind, requested_at, model, status,
				input_tokens, output_tokens, reasoning_tokens, cached_input_tokens, deleted_at
			) VALUES ('key-1', ?, ?, ?, 'gpt-test', 'success', ?, ?, ?, ?, ?)
		`, request.id, request.kind, logTime, request.inputTokens, request.output, request.reasoning, request.cached, request.deletedAt); err != nil {
			t.Fatalf("insert request log %s: %v", request.id, err)
		}
	}

	usage, err := repo.SelfUsage(ctx, "key-1")
	if err != nil {
		t.Fatalf("self usage: %v", err)
	}
	if usage == nil {
		t.Fatalf("expected usage")
	}
	if usage.RequestCount != 2 {
		t.Fatalf("expected 2 counted requests, got %d", usage.RequestCount)
	}
	if usage.TotalTokens != 135 {
		t.Fatalf("expected 135 total tokens, got %d", usage.TotalTokens)
	}
	if usage.CachedInputTokens != 40 {
		t.Fatalf("expected cached tokens clamped to 40, got %d", usage.CachedInputTokens)
	}
	if len(usage.Limits) != 1 {
		t.Fatalf("expected 1 limit, got %d", len(usage.Limits))
	}
	if usage.Limits[0].CurrentValue != 0 || usage.Limits[0].RemainingValue != 10 {
		t.Fatalf("expected expired limit reset, got %#v", usage.Limits[0])
	}
}

func TestEnforceRequestLimitsReservesAndReleasesUsage(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := apikeys.NewRepository(store)
	now := time.Now().UTC()
	createdAt := now.Format("2006-01-02 15:04:05")
	resetAt := "2030-01-01 00:00:00"
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO api_keys (id, name, key_hash, key_prefix, is_active, created_at)
		VALUES ('key-1', 'Test Key', 'hash', 'clb_test', 1, ?)
	`, createdAt); err != nil {
		t.Fatalf("insert api key: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO api_key_limits (api_key_id, limit_type, limit_window, max_value, current_value, model_filter, reset_at)
		VALUES ('key-1', 'input_tokens', 'daily', 100, 0, 'gpt-5.5', ?)
	`, resetAt); err != nil {
		t.Fatalf("insert limit: %v", err)
	}
	inputBudget := int64(40)
	reservation, err := repo.EnforceRequestLimits(ctx, "key-1", "gpt-5.5", "", apikeys.UsageBudget{InputTokens: &inputBudget})
	if err != nil {
		t.Fatalf("reserve usage: %v", err)
	}
	if reservation == nil || reservation.ID == "" {
		t.Fatalf("expected reservation, got %#v", reservation)
	}
	var current int64
	if err := store.DB().QueryRowContext(ctx, `SELECT current_value FROM api_key_limits WHERE api_key_id = 'key-1'`).Scan(&current); err != nil {
		t.Fatalf("load current value: %v", err)
	}
	if current != 40 {
		t.Fatalf("expected current value 40, got %d", current)
	}
	if err := repo.ReleaseUsageReservation(ctx, reservation.ID); err != nil {
		t.Fatalf("release reservation: %v", err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT current_value FROM api_key_limits WHERE api_key_id = 'key-1'`).Scan(&current); err != nil {
		t.Fatalf("load released current value: %v", err)
	}
	if current != 0 {
		t.Fatalf("expected current value release to 0, got %d", current)
	}
}

func TestUsageReservationTouchAndFinalizeSettleActualUsage(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := apikeys.NewRepository(store)
	createdAt := time.Now().UTC().Format("2006-01-02 15:04:05")
	resetAt := "2030-01-01 00:00:00"
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO api_keys (id, name, key_hash, key_prefix, is_active, created_at)
		VALUES ('key-1', 'Test Key', 'hash', 'clb_test', 1, ?)
	`, createdAt); err != nil {
		t.Fatalf("insert api key: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO api_key_limits (id, api_key_id, limit_type, limit_window, max_value, current_value, model_filter, reset_at)
		VALUES (100, 'key-1', 'input_tokens', 'daily', 100, 0, 'gpt-5.5', ?)
	`, resetAt); err != nil {
		t.Fatalf("insert limit: %v", err)
	}
	inputBudget := int64(40)
	reservation, err := repo.EnforceRequestLimits(ctx, "key-1", "gpt-5.5", "", apikeys.UsageBudget{InputTokens: &inputBudget})
	if err != nil {
		t.Fatalf("reserve usage: %v", err)
	}
	touched, err := repo.TouchUsageReservation(ctx, reservation.ID)
	if err != nil {
		t.Fatalf("touch reservation: %v", err)
	}
	if !touched {
		t.Fatal("expected reservation to be touched")
	}
	actualInput := int64(12)
	actualOutput := int64(3)
	if err := repo.FinalizeUsageReservation(ctx, reservation.ID, apikeys.UsageSettlement{
		Model:        "gpt-5.5",
		InputTokens:  &actualInput,
		OutputTokens: &actualOutput,
	}); err != nil {
		t.Fatalf("finalize reservation: %v", err)
	}
	var current int64
	if err := store.DB().QueryRowContext(ctx, `SELECT current_value FROM api_key_limits WHERE id = 100`).Scan(&current); err != nil {
		t.Fatalf("load current value: %v", err)
	}
	if current != 12 {
		t.Fatalf("expected current value settled to 12, got %d", current)
	}
	var status string
	var inputTokens int64
	if err := store.DB().QueryRowContext(ctx, `
		SELECT status, input_tokens FROM api_key_usage_reservations WHERE id = ?
	`, reservation.ID).Scan(&status, &inputTokens); err != nil {
		t.Fatalf("load reservation: %v", err)
	}
	if status != "finalized" || inputTokens != 12 {
		t.Fatalf("unexpected reservation settlement status=%s input=%d", status, inputTokens)
	}
	var actualDelta int64
	if err := store.DB().QueryRowContext(ctx, `
		SELECT actual_delta FROM api_key_usage_reservation_items WHERE reservation_id = ?
	`, reservation.ID).Scan(&actualDelta); err != nil {
		t.Fatalf("load item actual delta: %v", err)
	}
	if actualDelta != 12 {
		t.Fatalf("expected actual delta 12, got %d", actualDelta)
	}
	touched, err = repo.TouchUsageReservation(ctx, reservation.ID)
	if err != nil {
		t.Fatalf("touch finalized reservation: %v", err)
	}
	if touched {
		t.Fatal("expected finalized reservation not to be touched")
	}
}

func TestFailUsageReservationSettlesReservedUsageToActualZero(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := apikeys.NewRepository(store)
	createdAt := time.Now().UTC().Format("2006-01-02 15:04:05")
	resetAt := "2030-01-01 00:00:00"
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO api_keys (id, name, key_hash, key_prefix, is_active, created_at)
		VALUES ('key-1', 'Test Key', 'hash', 'clb_test', 1, ?)
	`, createdAt); err != nil {
		t.Fatalf("insert api key: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO api_key_limits (id, api_key_id, limit_type, limit_window, max_value, current_value, reset_at)
		VALUES (100, 'key-1', 'input_tokens', 'daily', 100, 0, ?)
	`, resetAt); err != nil {
		t.Fatalf("insert limit: %v", err)
	}
	inputBudget := int64(40)
	reservation, err := repo.EnforceRequestLimits(ctx, "key-1", "gpt-5.5", "", apikeys.UsageBudget{InputTokens: &inputBudget})
	if err != nil {
		t.Fatalf("reserve usage: %v", err)
	}
	if err := repo.FailUsageReservation(ctx, reservation.ID, apikeys.UsageSettlement{Model: "gpt-5.5"}); err != nil {
		t.Fatalf("fail reservation: %v", err)
	}
	var current int64
	if err := store.DB().QueryRowContext(ctx, `SELECT current_value FROM api_key_limits WHERE id = 100`).Scan(&current); err != nil {
		t.Fatalf("load current value: %v", err)
	}
	if current != 0 {
		t.Fatalf("expected failed reservation to settle current value to 0, got %d", current)
	}
	var status string
	if err := store.DB().QueryRowContext(ctx, `SELECT status FROM api_key_usage_reservations WHERE id = ?`, reservation.ID).Scan(&status); err != nil {
		t.Fatalf("load status: %v", err)
	}
	if status != "failed" {
		t.Fatalf("expected failed status, got %s", status)
	}
}

func TestEnforceRequestLimitsRejectsExceededLimit(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := apikeys.NewRepository(store)
	now := time.Now().UTC()
	createdAt := now.Format("2006-01-02 15:04:05")
	resetAt := "2030-01-01 00:00:00"
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO api_keys (id, name, key_hash, key_prefix, is_active, created_at)
		VALUES ('key-1', 'Test Key', 'hash', 'clb_test', 1, ?)
	`, createdAt); err != nil {
		t.Fatalf("insert api key: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO api_key_limits (api_key_id, limit_type, limit_window, max_value, current_value, reset_at)
		VALUES ('key-1', 'requests', 'daily', 1, 1, ?)
	`, resetAt); err != nil {
		t.Fatalf("insert limit: %v", err)
	}
	if _, err := repo.EnforceRequestLimits(ctx, "key-1", "gpt-5.5", "", apikeys.UsageBudget{}); err == nil {
		t.Fatal("expected rate limit error")
	}
}

func TestReleaseStaleUsageReservations(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := apikeys.NewRepository(store)
	now := time.Now().UTC()
	createdAt := now.Format("2006-01-02 15:04:05")
	resetAt := "2030-01-01 00:00:00"
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO api_keys (id, name, key_hash, key_prefix, is_active, created_at)
		VALUES ('key-1', 'Test Key', 'hash', 'clb_test', 1, ?)
	`, createdAt); err != nil {
		t.Fatalf("insert api key: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO api_key_limits (id, api_key_id, limit_type, limit_window, max_value, current_value, reset_at)
		VALUES (100, 'key-1', 'input_tokens', 'daily', 100, 0, ?)
	`, resetAt); err != nil {
		t.Fatalf("insert limit: %v", err)
	}
	inputBudget := int64(40)
	stale, err := repo.EnforceRequestLimits(ctx, "key-1", "gpt-5.5", "", apikeys.UsageBudget{InputTokens: &inputBudget})
	if err != nil {
		t.Fatalf("reserve stale usage: %v", err)
	}
	fresh, err := repo.EnforceRequestLimits(ctx, "key-1", "gpt-5.5", "", apikeys.UsageBudget{InputTokens: &inputBudget})
	if err != nil {
		t.Fatalf("reserve fresh usage: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		UPDATE api_key_usage_reservations SET updated_at = ? WHERE id = ?
	`, now.Add(-7*time.Hour).Format("2006-01-02 15:04:05"), stale.ID); err != nil {
		t.Fatalf("age stale reservation: %v", err)
	}

	released, err := repo.ReleaseStaleUsageReservations(ctx, now.Add(-6*time.Hour).Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("release stale reservations: %v", err)
	}
	if released != 1 {
		t.Fatalf("expected 1 released reservation, got %d", released)
	}
	var current int64
	if err := store.DB().QueryRowContext(ctx, `SELECT current_value FROM api_key_limits WHERE id = 100`).Scan(&current); err != nil {
		t.Fatalf("load current value: %v", err)
	}
	if current != 40 {
		t.Fatalf("expected fresh reservation to remain counted, got %d", current)
	}
	statuses := map[string]string{}
	rows, err := store.DB().QueryContext(ctx, `SELECT id, status FROM api_key_usage_reservations WHERE id IN (?, ?)`, stale.ID, fresh.ID)
	if err != nil {
		t.Fatalf("load reservation statuses: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var status string
		if err := rows.Scan(&id, &status); err != nil {
			t.Fatalf("scan reservation status: %v", err)
		}
		statuses[id] = status
	}
	if statuses[stale.ID] != "released" || statuses[fresh.ID] != "reserved" {
		t.Fatalf("unexpected reservation statuses: %#v", statuses)
	}
}

func TestLimitResetSchedulerRunsInitialTick(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Now().UTC()
	oldReset := now.Add(-time.Hour).Format("2006-01-02 15:04:05")
	createdAt := now.Format("2006-01-02 15:04:05")
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO api_keys (id, name, key_hash, key_prefix, is_active, created_at)
		VALUES ('key-1', 'Test Key', 'hash', 'clb_test', 1, ?)
	`, createdAt); err != nil {
		t.Fatalf("insert api key: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO api_key_limits (id, api_key_id, limit_type, limit_window, max_value, current_value, reset_at)
		VALUES (100, 'key-1', 'requests', 'daily', 10, 7, ?)
	`, oldReset); err != nil {
		t.Fatalf("insert limit: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	scheduler := apikeys.NewLimitResetScheduler(store, logger, config.Config{
		APIKeyLimitResetEnabled:   true,
		APIKeyLimitResetInterval:  time.Hour,
		APIKeyReservationStaleAge: 6 * time.Hour,
	}, "test-leader")
	scheduler.Start(ctx)
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = scheduler.Stop(stopCtx)
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var current int64
		if err := store.DB().QueryRowContext(ctx, `SELECT current_value FROM api_key_limits WHERE id = 100`).Scan(&current); err != nil {
			t.Fatalf("load current value: %v", err)
		}
		if current == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("scheduler did not reset expired limit")
}
