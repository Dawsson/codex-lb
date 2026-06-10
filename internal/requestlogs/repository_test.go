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
