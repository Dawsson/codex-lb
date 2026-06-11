package quotaplanner_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/audit"
	"github.com/soju06/codex-lb/internal/config"
	dbpkg "github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/limitwarmup"
	"github.com/soju06/codex-lb/internal/quotaplanner"
	"github.com/soju06/codex-lb/internal/requestlogs"
)

func newQuotaPlannerTestStore(t *testing.T) *dbpkg.Store {
	t.Helper()
	store, err := dbpkg.Open(config.Config{DatabasePath: filepath.Join(t.TempDir(), "store.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.RunMigrations("../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return store
}

func TestCancelDecisionCancelsPlannedDecisionAndAudits(t *testing.T) {
	ctx := context.Background()
	store := newQuotaPlannerTestStore(t)
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO quota_planner_decisions (id, mode, action, status, idempotency_key)
		VALUES ('decision-1', 'shadow', 'warmup', 'planned', 'idem-1')
	`); err != nil {
		t.Fatalf("insert decision: %v", err)
	}
	auditRepo := audit.NewRepository(store)
	handler := quotaplanner.NewHandler(quotaplanner.NewRepository(store)).WithAudit(auditRepo)

	req := httptest.NewRequest(http.MethodPost, "/api/quota-planner/decisions/decision-1/cancel", nil)
	req.RemoteAddr = "203.0.113.30:5555"
	req.Header.Set("X-Request-Id", "req-quota-cancel")
	req = withDecisionID(req, "decision-1")
	rec := httptest.NewRecorder()
	handler.CancelDecision(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	decision, err := quotaplanner.NewRepository(store).GetDecision(ctx, "decision-1")
	if err != nil {
		t.Fatalf("get decision: %v", err)
	}
	if decision == nil || decision.Status != "canceled" {
		t.Fatalf("expected canceled decision, got %#v", decision)
	}
	entries, err := auditRepo.List(ctx, "quota_planner_decision_cancel", 10, 0)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if len(entries) != 1 || !entries[0].Details.Valid || !strings.Contains(entries[0].Details.String, "decision-1") {
		t.Fatalf("expected decision cancel audit entry, got %#v", entries)
	}
}

func TestCancelDecisionReportsNotCancelable(t *testing.T) {
	store := newQuotaPlannerTestStore(t)
	if _, err := store.DB().Exec(`
		INSERT INTO quota_planner_decisions (id, mode, action, status, idempotency_key)
		VALUES ('decision-2', 'shadow', 'warmup', 'executing', 'idem-2')
	`); err != nil {
		t.Fatalf("insert decision: %v", err)
	}
	handler := quotaplanner.NewHandler(quotaplanner.NewRepository(store))

	req := withDecisionID(httptest.NewRequest(http.MethodPost, "/api/quota-planner/decisions/decision-2/cancel", nil), "decision-2")
	rec := httptest.NewRecorder()
	handler.CancelDecision(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not_cancelable") {
		t.Fatalf("expected not_cancelable response, got %s", rec.Body.String())
	}
}

func TestUpdateSettingsWritesAuditLog(t *testing.T) {
	ctx := context.Background()
	store := newQuotaPlannerTestStore(t)
	auditRepo := audit.NewRepository(store)
	handler := quotaplanner.NewHandler(quotaplanner.NewRepository(store)).WithAudit(auditRepo)

	req := httptest.NewRequest(http.MethodPut, "/api/quota-planner/settings", strings.NewReader(`{"mode":"auto"}`))
	req.RemoteAddr = "203.0.113.31:5555"
	rec := httptest.NewRecorder()
	handler.UpdateSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	entries, err := auditRepo.List(ctx, "quota_planner_settings_changed", 10, 0)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if len(entries) != 1 || !entries[0].Details.Valid || !strings.Contains(entries[0].Details.String, "auto") {
		t.Fatalf("expected settings audit entry, got %#v", entries)
	}
}

func TestWarmNowExecutesDecisionLogsRequestAndAudits(t *testing.T) {
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
	auditRepo := audit.NewRepository(store)
	handler := quotaplanner.NewHandler(quotaplanner.NewRepository(store)).
		WithAudit(auditRepo).
		WithWarmup(accounts.NewRepository(store), requestlogs.NewRepository(store), fakeWarmNowSender{})

	req := httptest.NewRequest(http.MethodPost, "/api/quota-planner/warm-now", strings.NewReader(`{"accountId":"acct-1","model":"gpt-5.5"}`))
	req.RemoteAddr = "203.0.113.32:5555"
	rec := httptest.NewRecorder()
	handler.WarmNow(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"executed"`) {
		t.Fatalf("expected executed response, got %s", rec.Body.String())
	}
	var requestLogCount int
	if err := store.DB().QueryRowContext(ctx, `
		SELECT count(*) FROM request_logs
		 WHERE account_id = 'acct-1'
		   AND request_kind = 'warmup'
		   AND transport = 'quota_planner'
		   AND status = 'success'
	`).Scan(&requestLogCount); err != nil {
		t.Fatalf("count request logs: %v", err)
	}
	if requestLogCount != 1 {
		t.Fatalf("expected one quota planner warmup request log, got %d", requestLogCount)
	}
	entries, err := auditRepo.List(ctx, "quota_planner_warm_now", 10, 0)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if len(entries) != 1 || !entries[0].Details.Valid || !strings.Contains(entries[0].Details.String, "executed") {
		t.Fatalf("expected warm-now audit entry, got %#v", entries)
	}
}

type fakeWarmNowSender struct{}

func (fakeWarmNowSender) Send(context.Context, accounts.Account, limitwarmup.SendParams) (limitwarmup.SendResult, error) {
	return limitwarmup.SendResult{
		RequestID:    "quota-warmup-test",
		Success:      true,
		LatencyMS:    12,
		InputTokens:  int64Ptr(2),
		OutputTokens: int64Ptr(1),
	}, nil
}

func int64Ptr(value int64) *int64 {
	return &value
}

func withDecisionID(req *http.Request, decisionID string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("decisionID", decisionID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}
