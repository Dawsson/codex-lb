package firewall_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/soju06/codex-lb/internal/config"
	dbpkg "github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/firewall"
)

func newTestStore(t *testing.T) *dbpkg.Store {
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

func TestMiddlewareAllowsWhenAllowlistEmpty(t *testing.T) {
	store := newTestStore(t)
	fw, err := firewall.NewFirewall(firewall.NewRepository(store), firewall.MiddlewareOptions{})
	if err != nil {
		t.Fatalf("new firewall: %v", err)
	}
	status := serveStatus(fw, "/v1/models", "203.0.113.10:1234", "")
	if status != http.StatusOK {
		t.Fatalf("expected allow-all OK, got %d", status)
	}
}

func TestMiddlewareDeniesUnlistedProxyClient(t *testing.T) {
	store := newTestStore(t)
	repo := firewall.NewRepository(store)
	if _, err := repo.Add(context.Background(), "198.51.100.7"); err != nil {
		t.Fatalf("add firewall ip: %v", err)
	}
	fw, err := firewall.NewFirewall(repo, firewall.MiddlewareOptions{})
	if err != nil {
		t.Fatalf("new firewall: %v", err)
	}
	if status := serveStatus(fw, "/v1/models", "203.0.113.10:1234", ""); status != http.StatusForbidden {
		t.Fatalf("expected forbidden, got %d", status)
	}
	if status := serveStatus(fw, "/api/firewall/ips", "203.0.113.10:1234", ""); status != http.StatusOK {
		t.Fatalf("expected dashboard path bypass, got %d", status)
	}
}

func TestMiddlewareAllowsTrustedForwardedClient(t *testing.T) {
	store := newTestStore(t)
	repo := firewall.NewRepository(store)
	if _, err := repo.Add(context.Background(), "198.51.100.7"); err != nil {
		t.Fatalf("add firewall ip: %v", err)
	}
	fw, err := firewall.NewFirewall(repo, firewall.MiddlewareOptions{
		TrustProxyHeaders: true,
		TrustedProxyCIDRs: []string{"10.0.0.0/8"},
	})
	if err != nil {
		t.Fatalf("new firewall: %v", err)
	}
	if status := serveStatus(fw, "/v1/models", "10.1.2.3:1234", "198.51.100.7"); status != http.StatusOK {
		t.Fatalf("expected trusted forwarded client to be allowed, got %d", status)
	}
	if status := serveStatus(fw, "/v1/models", "203.0.113.10:1234", "198.51.100.7"); status != http.StatusForbidden {
		t.Fatalf("expected untrusted forwarded header to be ignored, got %d", status)
	}
}

func TestMutationInvalidatesFirewallCache(t *testing.T) {
	store := newTestStore(t)
	repo := firewall.NewRepository(store)
	if _, err := repo.Add(context.Background(), "198.51.100.7"); err != nil {
		t.Fatalf("add initial firewall ip: %v", err)
	}
	fw, err := firewall.NewFirewall(repo, firewall.MiddlewareOptions{})
	if err != nil {
		t.Fatalf("new firewall: %v", err)
	}
	if status := serveStatus(fw, "/v1/models", "203.0.113.10:1234", ""); status != http.StatusForbidden {
		t.Fatalf("expected initial deny, got %d", status)
	}
	if _, err := repo.Add(context.Background(), "203.0.113.10"); err != nil {
		t.Fatalf("add client ip: %v", err)
	}
	fw.InvalidateCache()
	if status := serveStatus(fw, "/v1/models", "203.0.113.10:1234", ""); status != http.StatusOK {
		t.Fatalf("expected allow after cache invalidation, got %d", status)
	}
}

func serveStatus(fw *firewall.Firewall, path, remoteAddr, xff string) int {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = remoteAddr
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	fw.Middleware(next).ServeHTTP(rec, req)
	return rec.Code
}
