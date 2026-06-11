package stickysessions_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/soju06/codex-lb/internal/audit"
	"github.com/soju06/codex-lb/internal/stickysessions"
)

func TestPurgeStickySessionsWritesAuditLog(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	insertAccount(t, store, "acct-1")
	oldTime := "2000-01-01 00:00:00"
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO sticky_sessions (key, kind, account_id, created_at, updated_at)
		VALUES ('cache-a', 'prompt_cache', 'acct-1', ?, ?)
	`, oldTime, oldTime); err != nil {
		t.Fatalf("insert sticky session: %v", err)
	}
	auditRepo := audit.NewRepository(store)
	handler := stickysessions.NewHandler(stickysessions.NewRepository(store)).WithAudit(auditRepo)

	req := httptest.NewRequest(http.MethodPost, "/api/sticky-sessions/purge", strings.NewReader(`{"staleOnly":true}`))
	req.RemoteAddr = "203.0.113.22:5555"
	req.Header.Set("X-Request-Id", "req-sticky")
	rec := httptest.NewRecorder()
	handler.Purge(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	entries, err := auditRepo.List(ctx, "sticky_sessions_purged", 10, 0)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one audit entry, got %d", len(entries))
	}
	if !entries[0].Details.Valid || !strings.Contains(entries[0].Details.String, "deleted_count") {
		t.Fatalf("expected deleted count details, got %#v", entries[0].Details)
	}
}
