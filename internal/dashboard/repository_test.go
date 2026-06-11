package dashboard

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/soju06/codex-lb/internal/config"
	dbpkg "github.com/soju06/codex-lb/internal/db"
)

func newDashboardTestRepo(t *testing.T) Repository {
	t.Helper()
	dir := t.TempDir()
	store, err := dbpkg.Open(config.Config{DatabasePath: filepath.Join(dir, "store.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.RunMigrations("../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return NewRepository(store)
}

func TestTopErrorSinceUsesFailedNonBlankCodesOnly(t *testing.T) {
	repo := newDashboardTestRepo(t)
	ctx := context.Background()
	_, err := repo.store.DB().ExecContext(ctx, `
		INSERT INTO request_logs (request_id, requested_at, model, status, error_code)
		VALUES
		  ('success-with-code', '2030-01-01 00:00:00', 'gpt-5.5', 'success', 'success_code'),
		  ('blank-error', '2030-01-01 00:01:00', 'gpt-5.5', 'error', ''),
		  ('quota-1', '2030-01-01 00:02:00', 'gpt-5.5', 'error', 'quota_exceeded'),
		  ('quota-2', '2030-01-01 00:03:00', 'gpt-5.5', 'error', 'quota_exceeded'),
		  ('rate-1', '2030-01-01 00:04:00', 'gpt-5.5', 'error', 'rate_limit_exceeded')
	`)
	if err != nil {
		t.Fatalf("insert logs: %v", err)
	}

	top, err := repo.TopErrorSince(ctx, time.Date(2029, 12, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("top error: %v", err)
	}
	if top == nil || *top != "quota_exceeded" {
		t.Fatalf("expected quota_exceeded, got %v", top)
	}
}

func TestAggregateActivitySinceExcludesWarmupRequestKinds(t *testing.T) {
	repo := newDashboardTestRepo(t)
	ctx := context.Background()
	_, err := repo.store.DB().ExecContext(ctx, `
		INSERT INTO request_logs (
			request_id, requested_at, model, status, request_kind,
			input_tokens, output_tokens, cached_input_tokens, cost_usd
		)
		VALUES
		  ('normal-success', '2030-01-01 00:00:00', 'gpt-5.5', 'success', 'normal', 10, 20, 3, 0.50),
		  ('normal-error', '2030-01-01 00:01:00', 'gpt-5.5', 'error', 'normal', 5, 7, 2, 0.25),
		  ('warmup-error', '2030-01-01 00:02:00', 'gpt-5.5', 'error', 'warmup', 100, 200, 30, 9.00),
		  ('limit-warmup-error', '2030-01-01 00:03:00', 'gpt-5.5', 'error', 'limit_warmup', 1000, 2000, 300, 90.00)
	`)
	if err != nil {
		t.Fatalf("insert logs: %v", err)
	}

	activity, err := repo.AggregateActivitySince(ctx, time.Date(2029, 12, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("aggregate activity: %v", err)
	}

	if activity.Requests != 2 {
		t.Fatalf("expected 2 visible requests, got %d", activity.Requests)
	}
	if got := activity.Errors; got != 1 {
		t.Fatalf("expected 1 visible error, got %d", got)
	}
	if got := activity.InputTokens.Int64; got != 15 {
		t.Fatalf("expected 15 visible input tokens, got %d", got)
	}
	if got := activity.OutputTokens.Int64; got != 27 {
		t.Fatalf("expected 27 visible output tokens, got %d", got)
	}
	if got := activity.CachedInputTokens.Int64; got != 5 {
		t.Fatalf("expected 5 visible cached tokens, got %d", got)
	}
	if got := activity.TotalCostUSD.Float64; got != 0.75 {
		t.Fatalf("expected 0.75 visible cost, got %f", got)
	}
}

func TestTrendsExcludesWarmupRequestKinds(t *testing.T) {
	repo := newDashboardTestRepo(t)
	ctx := context.Background()
	_, err := repo.store.DB().ExecContext(ctx, `
		INSERT INTO request_logs (
			request_id, requested_at, model, status, request_kind,
			input_tokens, output_tokens, cached_input_tokens, cost_usd
		)
		VALUES
		  ('normal-success', '2030-01-01 00:00:00', 'gpt-5.5', 'success', 'normal', 10, 20, 3, 0.50),
		  ('normal-error', '2030-01-01 00:10:00', 'gpt-5.5', 'error', 'normal', 5, 7, 2, 0.25),
		  ('warmup-error', '2030-01-01 00:20:00', 'gpt-5.5', 'error', 'warmup', 100, 200, 30, 9.00),
		  ('limit-warmup-error', '2030-01-01 00:30:00', 'gpt-5.5', 'error', 'limit_warmup', 1000, 2000, 300, 90.00)
	`)
	if err != nil {
		t.Fatalf("insert logs: %v", err)
	}

	points, err := repo.Trends(ctx, time.Date(2029, 12, 31, 0, 0, 0, 0, time.UTC), 3600)
	if err != nil {
		t.Fatalf("trends: %v", err)
	}

	if len(points) != 1 {
		t.Fatalf("expected 1 visible trend bucket, got %d", len(points))
	}
	point := points[0]
	if point.Requests != 2 {
		t.Fatalf("expected 2 visible trend requests, got %d", point.Requests)
	}
	if point.Errors != 1 {
		t.Fatalf("expected 1 visible trend error, got %d", point.Errors)
	}
	if point.Tokens != 42 {
		t.Fatalf("expected 42 visible trend tokens, got %d", point.Tokens)
	}
	if point.CachedTokens != 5 {
		t.Fatalf("expected 5 visible trend cached tokens, got %d", point.CachedTokens)
	}
	if point.CostUSD != 0.75 {
		t.Fatalf("expected 0.75 visible trend cost, got %f", point.CostUSD)
	}
}

func TestTopErrorSinceExcludesWarmupRequestKinds(t *testing.T) {
	repo := newDashboardTestRepo(t)
	ctx := context.Background()
	_, err := repo.store.DB().ExecContext(ctx, `
		INSERT INTO request_logs (request_id, requested_at, model, status, request_kind, error_code)
		VALUES
		  ('normal-quota', '2030-01-01 00:00:00', 'gpt-5.5', 'error', 'normal', 'quota_exceeded'),
		  ('warmup-rate-1', '2030-01-01 00:01:00', 'gpt-5.5', 'error', 'warmup', 'rate_limit_exceeded'),
		  ('warmup-rate-2', '2030-01-01 00:02:00', 'gpt-5.5', 'error', 'limit_warmup', 'rate_limit_exceeded')
	`)
	if err != nil {
		t.Fatalf("insert logs: %v", err)
	}

	top, err := repo.TopErrorSince(ctx, time.Date(2029, 12, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("top error: %v", err)
	}
	if top == nil || *top != "quota_exceeded" {
		t.Fatalf("expected quota_exceeded, got %v", top)
	}
}
