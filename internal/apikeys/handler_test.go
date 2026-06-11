package apikeys_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/audit"
)

func TestCreateAPIKeyWritesAuditLog(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	apiKeysRepo := apikeys.NewRepository(store)
	auditRepo := audit.NewRepository(store)
	handler := apikeys.NewHandler(apiKeysRepo).WithAudit(auditRepo)

	req := httptest.NewRequest(http.MethodPost, "/api/api-keys", strings.NewReader(`{"name":"audit-key"}`))
	req.RemoteAddr = "203.0.113.20:5555"
	req.Header.Set("X-Request-Id", "req-api-key")
	rec := httptest.NewRecorder()
	handler.Create(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	entries, err := auditRepo.List(ctx, "api_key_created", 10, 0)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one audit entry, got %d", len(entries))
	}
	if !entries[0].ActorIP.Valid || entries[0].ActorIP.String != "203.0.113.20" {
		t.Fatalf("unexpected actor ip: %#v", entries[0].ActorIP)
	}
	if !entries[0].RequestID.Valid || entries[0].RequestID.String != "req-api-key" {
		t.Fatalf("unexpected request id: %#v", entries[0].RequestID)
	}
	if !entries[0].Details.Valid || !strings.Contains(entries[0].Details.String, "key_id") {
		t.Fatalf("expected key id details, got %#v", entries[0].Details)
	}
}
