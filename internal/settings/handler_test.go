package settings_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soju06/codex-lb/internal/audit"
	"github.com/soju06/codex-lb/internal/cacheinvalidation"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/crypto"
	dbpkg "github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/settings"
)

type fakeBumper struct {
	namespaces []string
}

func (b *fakeBumper) Bump(_ context.Context, namespace string) error {
	b.namespaces = append(b.namespaces, namespace)
	return nil
}

func newSettingsTestStore(t *testing.T) (*dbpkg.Store, *crypto.Encryptor) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{DatabasePath: filepath.Join(dir, "store.db")}
	store, err := dbpkg.Open(cfg)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.RunMigrations("../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	encryptor, err := crypto.NewEncryptor(filepath.Join(dir, "encryption.key"))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	return store, encryptor
}

func TestUpdateBumpsSettingsInvalidation(t *testing.T) {
	store, encryptor := newSettingsTestStore(t)
	bumper := &fakeBumper{}
	auditRepo := audit.NewRepository(store)
	handler := settings.NewHandler(settings.NewRepository(store, encryptor), bumper).WithAudit(auditRepo)

	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"stickyThreadsEnabled":false}`))
	req.RemoteAddr = "203.0.113.10:5555"
	req.Header.Set("X-Request-Id", "req-settings")
	rec := httptest.NewRecorder()
	handler.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(bumper.namespaces) != 1 || bumper.namespaces[0] != cacheinvalidation.NamespaceSettings {
		t.Fatalf("expected settings invalidation bump, got %#v", bumper.namespaces)
	}
	entries, err := auditRepo.List(context.Background(), "settings_changed", 10, 0)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one audit entry, got %d", len(entries))
	}
	if !entries[0].ActorIP.Valid || entries[0].ActorIP.String != "203.0.113.10" {
		t.Fatalf("unexpected actor ip: %#v", entries[0].ActorIP)
	}
	if !entries[0].RequestID.Valid || entries[0].RequestID.String != "req-settings" {
		t.Fatalf("unexpected request id: %#v", entries[0].RequestID)
	}
}
