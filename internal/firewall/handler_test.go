package firewall_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/soju06/codex-lb/internal/audit"
	"github.com/soju06/codex-lb/internal/firewall"
)

func TestCreateFirewallIPWritesAuditLog(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	auditRepo := audit.NewRepository(store)
	handler := firewall.NewHandler(firewall.NewRepository(store), nil).WithAudit(auditRepo)

	req := httptest.NewRequest(http.MethodPost, "/api/firewall/ips", strings.NewReader(`{"ipAddress":"203.0.113.44"}`))
	req.RemoteAddr = "203.0.113.21:5555"
	req.Header.Set("X-Request-Id", "req-firewall")
	rec := httptest.NewRecorder()
	handler.Create(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	entries, err := auditRepo.List(ctx, "firewall_ip_created", 10, 0)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one audit entry, got %d", len(entries))
	}
	if !entries[0].Details.Valid || !strings.Contains(entries[0].Details.String, "203.0.113.44") {
		t.Fatalf("expected ip details, got %#v", entries[0].Details)
	}
}
