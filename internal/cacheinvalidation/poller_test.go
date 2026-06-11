package cacheinvalidation

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/soju06/codex-lb/internal/config"
	dbpkg "github.com/soju06/codex-lb/internal/db"
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

func TestPollerBumpFiresCallbacksAfterBaseline(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	poller := NewPoller(store, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{
		CacheInvalidationEnabled:  true,
		CacheInvalidationInterval: time.Hour,
	})
	calls := 0
	poller.OnInvalidation(NamespaceFirewall, func() { calls++ })

	poller.pollOnce(ctx)
	if calls != 0 {
		t.Fatalf("baseline poll fired callback %d times", calls)
	}
	if err := poller.Bump(ctx, NamespaceFirewall); err != nil {
		t.Fatalf("bump firewall namespace: %v", err)
	}
	poller.pollOnce(ctx)
	if calls != 1 {
		t.Fatalf("expected one callback after bump, got %d", calls)
	}
	poller.pollOnce(ctx)
	if calls != 1 {
		t.Fatalf("expected unchanged version not to fire, got %d", calls)
	}
}

func TestPollerStartStop(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	poller := NewPoller(store, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{
		CacheInvalidationEnabled:  true,
		CacheInvalidationInterval: 10 * time.Millisecond,
	})
	called := make(chan struct{}, 1)
	poller.OnInvalidation(NamespaceAPIKey, func() { called <- struct{}{} })
	poller.pollOnce(ctx)
	poller.Start(ctx)
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = poller.Stop(stopCtx)
	})

	if err := poller.Bump(ctx, NamespaceAPIKey); err != nil {
		t.Fatalf("bump api key namespace: %v", err)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("poller did not fire callback")
	}
}
