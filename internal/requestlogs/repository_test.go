package requestlogs_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/soju06/codex-lb/internal/config"
	dbpkg "github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/requestlogs"
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

func insertLog(t *testing.T, store *dbpkg.Store, requestedAt, status string, costUSD, inputTokens, outputTokens, reasoningTokens, cachedInputTokens any, errorCode any) {
	t.Helper()
	if _, err := store.DB().Exec(`
		INSERT INTO request_logs (
			request_id, requested_at, model, status, cost_usd,
			input_tokens, output_tokens, reasoning_tokens, cached_input_tokens, error_code
		) VALUES (?, ?, 'gpt-test', ?, ?, ?, ?, ?, ?, ?)
	`, "req-"+requestedAt+status, requestedAt, status, costUSD, inputTokens, outputTokens, reasoningTokens, cachedInputTokens, errorCode); err != nil {
		t.Fatalf("insert request log: %v", err)
	}
}

func TestListExcludesWarmupDeletedAndSearchesRelatedFields(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := requestlogs.NewRepository(store)
	now := time.Now().UTC().Format("2006-01-02 15:04:05")

	if _, err := store.DB().Exec(`
		INSERT INTO accounts (
			id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
			id_token_encrypted, last_refresh, status
		) VALUES ('acct-1', 'needle@example.com', 'plus', x'00', x'00', x'00', '2026-01-01 00:00:00', 'active')
	`); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := store.DB().Exec(`
		INSERT INTO api_keys (id, name, key_hash, key_prefix, is_active, created_at)
		VALUES ('key-1', 'needle-key', 'hash', 'clb_test', 1, ?)
	`, now); err != nil {
		t.Fatalf("insert api key: %v", err)
	}
	rows := []struct {
		requestID   string
		requestKind string
		deletedAt   any
	}{
		{"req-visible", "normal", nil},
		{"req-warmup", "warmup", nil},
		{"req-limit-warmup", "limit_warmup", nil},
		{"req-deleted", "normal", now},
	}
	for _, row := range rows {
		if _, err := store.DB().Exec(`
			INSERT INTO request_logs (
				account_id, api_key_id, request_id, request_kind, requested_at,
				model, status, input_tokens, output_tokens, reasoning_tokens,
				cached_input_tokens, source, reasoning_effort, latency_ms, deleted_at
			) VALUES ('acct-1', 'key-1', ?, ?, ?, 'gpt-test', 'success', 12, NULL, 7, 99, 'codex_cli', 'low', 321, ?)
		`, row.requestID, row.requestKind, now, row.deletedAt); err != nil {
			t.Fatalf("insert request log %s: %v", row.requestID, err)
		}
	}

	page, err := repo.List(ctx, requestlogs.Filters{Limit: 25, Search: "needle-key"})
	if err != nil {
		t.Fatalf("list request logs: %v", err)
	}
	if page.Total != 1 || len(page.Entries) != 1 {
		t.Fatalf("expected one visible row, got total=%d len=%d", page.Total, len(page.Entries))
	}
	if page.Entries[0].RequestID != "req-visible" {
		t.Fatalf("expected req-visible, got %s", page.Entries[0].RequestID)
	}
	if !page.Entries[0].ReasoningTokens.Valid || page.Entries[0].ReasoningTokens.Int64 != 7 {
		t.Fatalf("expected reasoning token fallback value, got %#v", page.Entries[0].ReasoningTokens)
	}

	searches := []string{
		"needle@example.com",
		"low",
		"codex_cli",
		"success",
		"key-1",
		now[:10],
		"321",
	}
	for _, search := range searches {
		page, err := repo.List(ctx, requestlogs.Filters{Limit: 25, Search: search})
		if err != nil {
			t.Fatalf("list request logs search %q: %v", search, err)
		}
		if page.Total != 1 || len(page.Entries) != 1 || page.Entries[0].RequestID != "req-visible" {
			t.Fatalf("search %q expected only visible row, got total=%d entries=%#v", search, page.Total, page.Entries)
		}
	}
}

func TestAggregateCostMetrics(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := requestlogs.NewRepository(store)

	now := time.Now().UTC()
	recent := now.Add(-time.Hour).Format("2006-01-02 15:04:05")
	old := now.Add(-10 * 24 * time.Hour).Format("2006-01-02 15:04:05")

	// Recent successful request: 100 input, 50 output, 30 cached (clamped to 100).
	insertLog(t, store, recent, "success", 1.5, 100, 50, nil, 30, nil)
	// Recent error request, no output_tokens but has reasoning_tokens fallback.
	insertLog(t, store, recent, "error", 0.5, 10, nil, 5, 200, "rate_limited")
	// Old request outside the window, must be excluded.
	insertLog(t, store, old, "success", 100.0, 1000, 1000, nil, 1000, nil)

	since := now.Add(-7 * 24 * time.Hour).Format("2006-01-02 15:04:05")
	metrics, err := repo.AggregateCostMetrics(ctx, since)
	if err != nil {
		t.Fatalf("aggregate cost metrics: %v", err)
	}

	if metrics.Requests != 2 {
		t.Fatalf("expected 2 requests, got %d", metrics.Requests)
	}
	if metrics.Errors != 1 {
		t.Fatalf("expected 1 error, got %d", metrics.Errors)
	}
	if metrics.TotalCostUSD != 2.0 {
		t.Fatalf("expected total cost 2.0, got %v", metrics.TotalCostUSD)
	}
	// (100+50) + (10+5) = 165
	if metrics.TotalTokens != 165 {
		t.Fatalf("expected 165 total tokens, got %d", metrics.TotalTokens)
	}
	// clamp(30,100)=30 + clamp(200,10)=10 -> 40
	if metrics.CachedInputTokens != 40 {
		t.Fatalf("expected 40 cached tokens, got %d", metrics.CachedInputTokens)
	}
	if metrics.TopErrorCode == nil || *metrics.TopErrorCode != "rate_limited" {
		t.Fatalf("expected top error rate_limited, got %v", metrics.TopErrorCode)
	}
}

func TestAggregateCostMetricsEmpty(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := requestlogs.NewRepository(store)

	since := time.Now().UTC().Add(-time.Hour).Format("2006-01-02 15:04:05")
	metrics, err := repo.AggregateCostMetrics(ctx, since)
	if err != nil {
		t.Fatalf("aggregate cost metrics: %v", err)
	}
	if metrics.Requests != 0 || metrics.Errors != 0 {
		t.Fatalf("expected zero metrics, got %#v", metrics)
	}
	if metrics.TopErrorCode != nil {
		t.Fatalf("expected nil top error, got %v", *metrics.TopErrorCode)
	}
}

func TestFindLatestAccountIDForResponseID(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := requestlogs.NewRepository(store)

	rows := []struct {
		requestedAt string
		status      string
		accountID   any
		apiKeyID    any
		sessionID   any
	}{
		{"2026-01-01 00:00:00", "success", "acct-old", "key-1", nil},
		{"2026-01-01 00:01:00", "error", "acct-error", "key-1", nil},
		{"2026-01-01 00:02:00", "success", "acct-other-key", "key-2", nil},
		{"2026-01-01 00:03:00", "success", "acct-new", "key-1", nil},
		{"2026-01-01 00:04:00", "success", "", "key-1", nil},
		{"2026-01-01 00:05:00", "success", nil, "key-1", nil},
	}
	for _, row := range rows {
		if _, err := store.DB().Exec(`
			INSERT INTO request_logs (
				request_id, requested_at, request_kind, model, status,
				account_id, api_key_id, session_id
			) VALUES ('resp-owner', ?, 'normal', 'gpt-test', ?, ?, ?, ?)
		`, row.requestedAt, row.status, row.accountID, row.apiKeyID, row.sessionID); err != nil {
			t.Fatalf("insert request log: %v", err)
		}
	}

	ownerID, err := repo.FindLatestAccountIDForResponseID(ctx, " resp-owner ", "key-1", "")
	if err != nil {
		t.Fatalf("find owner: %v", err)
	}
	if ownerID != "acct-new" {
		t.Fatalf("expected acct-new, got %q", ownerID)
	}

	ownerID, err = repo.FindLatestAccountIDForResponseID(ctx, "resp-owner", "key-2", "")
	if err != nil {
		t.Fatalf("find other key owner: %v", err)
	}
	if ownerID != "acct-other-key" {
		t.Fatalf("expected acct-other-key, got %q", ownerID)
	}

	ownerID, err = repo.FindLatestAccountIDForResponseID(ctx, "resp-owner", "missing-key", "")
	if err != nil {
		t.Fatalf("find missing key owner: %v", err)
	}
	if ownerID != "" {
		t.Fatalf("expected missing key miss, got %q", ownerID)
	}
}

func TestFindLatestAccountIDForResponseIDPrefersSessionScope(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := requestlogs.NewRepository(store)

	rows := []struct {
		requestedAt string
		accountID   string
		sessionID   any
	}{
		{"2026-01-01 00:00:00", "acct-session", "session-1"},
		{"2026-01-01 00:01:00", "acct-later", nil},
	}
	for _, row := range rows {
		if _, err := store.DB().Exec(`
			INSERT INTO request_logs (
				request_id, requested_at, request_kind, model, status,
				account_id, api_key_id, session_id
			) VALUES ('resp-session', ?, 'normal', 'gpt-test', 'success', ?, 'key-1', ?)
		`, row.requestedAt, row.accountID, row.sessionID); err != nil {
			t.Fatalf("insert request log: %v", err)
		}
	}

	ownerID, err := repo.FindLatestAccountIDForResponseID(ctx, "resp-session", "key-1", "session-1")
	if err != nil {
		t.Fatalf("find session owner: %v", err)
	}
	if ownerID != "acct-session" {
		t.Fatalf("expected session owner, got %q", ownerID)
	}

	ownerID, err = repo.FindLatestAccountIDForResponseID(ctx, "resp-session", "key-1", "missing-session")
	if err != nil {
		t.Fatalf("find fallback owner: %v", err)
	}
	if ownerID != "acct-later" {
		t.Fatalf("expected fallback owner, got %q", ownerID)
	}
}
