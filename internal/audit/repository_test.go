package audit_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/soju06/codex-lb/internal/audit"
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

func TestAuditRepositoryInsertAndList(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := audit.NewRepository(store)

	if _, err := repo.Insert(ctx, audit.Entry{
		Timestamp: "2026-06-01 00:00:00",
		Action:    "account.pause",
		ActorIP:   sql.NullString{String: "127.0.0.1", Valid: true},
		RequestID: sql.NullString{String: "req-1", Valid: true},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := repo.Insert(ctx, audit.Entry{
		Timestamp: "2026-06-02 00:00:00",
		Action:    "account.resume",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	all, err := repo.List(ctx, "", 0, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(all))
	}
	if all[0].Action != "account.resume" {
		t.Fatalf("expected most recent first, got %q", all[0].Action)
	}

	filtered, err := repo.List(ctx, "account.pause", 10, 0)
	if err != nil {
		t.Fatalf("list filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Action != "account.pause" {
		t.Fatalf("expected 1 filtered entry, got %#v", filtered)
	}
}
