package usage_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/config"
	dbpkg "github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/requestlogs"
	"github.com/soju06/codex-lb/internal/usage"
)

func newHandlerTestStore(t *testing.T) *dbpkg.Store {
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
	if _, err := store.DB().Exec(`
		INSERT INTO accounts (
			id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
			id_token_encrypted, last_refresh, status
		) VALUES ('acct-1', 'a@example.com', 'pro', x'00', x'00', x'00', '2026-01-01 00:00:00', 'active')
	`); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	return store
}

func TestUsageHandlerEndpoints(t *testing.T) {
	ctx := context.Background()
	store := newHandlerTestStore(t)

	usageRepo := usage.NewRepository(store)
	now := time.Now().UTC()
	if _, err := usageRepo.AddEntry(ctx, usage.Entry{
		AccountID:   "acct-1",
		RecordedAt:  now.Format("2006-01-02 15:04:05"),
		UsedPercent: 50,
		Window:      sql.NullString{String: "primary", Valid: true},
	}); err != nil {
		t.Fatalf("add primary entry: %v", err)
	}
	if _, err := usageRepo.AddEntry(ctx, usage.Entry{
		AccountID:   "acct-1",
		RecordedAt:  now.Format("2006-01-02 15:04:05"),
		UsedPercent: 25,
		Window:      sql.NullString{String: "secondary", Valid: true},
	}); err != nil {
		t.Fatalf("add secondary entry: %v", err)
	}

	if _, err := store.DB().Exec(`
		INSERT INTO request_logs (request_id, requested_at, model, status, cost_usd, input_tokens, output_tokens, cached_input_tokens)
		VALUES ('req-1', ?, 'gpt-test', 'success', 1.25, 100, 50, 20)
	`, now.Format("2006-01-02 15:04:05")); err != nil {
		t.Fatalf("insert request log: %v", err)
	}

	handler := usage.NewHandler(usageRepo, accounts.NewRepository(store), requestlogs.NewRepository(store))

	t.Run("summary", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/usage/summary", nil)
		rec := httptest.NewRecorder()
		handler.Summary(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		primary, ok := body["primaryWindow"].(map[string]any)
		if !ok {
			t.Fatalf("expected primaryWindow object, got %#v", body["primaryWindow"])
		}
		if primary["capacityCredits"].(float64) != 1500 {
			t.Fatalf("expected capacityCredits 1500 for pro plan, got %v", primary["capacityCredits"])
		}
		if primary["remainingPercent"].(float64) != 50 {
			t.Fatalf("expected remainingPercent 50, got %v", primary["remainingPercent"])
		}

		cost, ok := body["cost"].(map[string]any)
		if !ok {
			t.Fatalf("expected cost object, got %#v", body["cost"])
		}
		if cost["totalUsd7d"].(float64) != 1.25 {
			t.Fatalf("expected totalUsd7d 1.25, got %v", cost["totalUsd7d"])
		}

		metrics, ok := body["metrics"].(map[string]any)
		if !ok {
			t.Fatalf("expected metrics object, got %#v", body["metrics"])
		}
		if metrics["requests7d"].(float64) != 1 {
			t.Fatalf("expected requests7d 1, got %v", metrics["requests7d"])
		}
	})

	t.Run("history", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/usage/history?hours=24", nil)
		rec := httptest.NewRecorder()
		handler.History(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["windowHours"].(float64) != 24 {
			t.Fatalf("expected windowHours 24, got %v", body["windowHours"])
		}
		accountsList, ok := body["accounts"].([]any)
		if !ok || len(accountsList) != 1 {
			t.Fatalf("expected 1 account entry, got %#v", body["accounts"])
		}
	})

	t.Run("history invalid hours", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/usage/history?hours=200", nil)
		rec := httptest.NewRecorder()
		handler.History(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("window", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/usage/window?window=secondary", nil)
		rec := httptest.NewRecorder()
		handler.Window(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["windowKey"] != "secondary" {
			t.Fatalf("expected windowKey secondary, got %v", body["windowKey"])
		}
		accountsList, ok := body["accounts"].([]any)
		if !ok || len(accountsList) != 1 {
			t.Fatalf("expected 1 account entry, got %#v", body["accounts"])
		}
		entry := accountsList[0].(map[string]any)
		if entry["remainingPercentAvg"].(float64) != 75 {
			t.Fatalf("expected remainingPercentAvg 75, got %v", entry["remainingPercentAvg"])
		}
	})

	t.Run("window invalid", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/usage/window?window=monthly", nil)
		rec := httptest.NewRecorder()
		handler.Window(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})
}
