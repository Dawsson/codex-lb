package limitwarmup_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/soju06/codex-lb/internal/config"
	dbpkg "github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/limitwarmup"
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
	if _, err := store.DB().Exec(`
		INSERT INTO accounts (
			id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
			id_token_encrypted, last_refresh, status
		) VALUES ('acct-1', 'a@example.com', 'plus', x'00', x'00', x'00', '2026-01-01 00:00:00', 'active')
	`); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	return store
}

func TestLimitWarmupRepository(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := limitwarmup.NewRepository(store)

	attempt, ok, err := repo.TryCreateAttempt(ctx, "acct-1", "primary", 1700000000, "auto", "2026-06-01 00:00:00")
	if err != nil {
		t.Fatalf("try create attempt: %v", err)
	}
	if !ok {
		t.Fatalf("expected attempt to be created")
	}
	if attempt.ID == 0 {
		t.Fatalf("expected non-zero id")
	}

	_, ok, err = repo.TryCreateAttempt(ctx, "acct-1", "primary", 1700000000, "auto", "2026-06-01 00:00:00")
	if err != nil {
		t.Fatalf("try create duplicate attempt: %v", err)
	}
	if ok {
		t.Fatalf("expected duplicate attempt to be rejected")
	}

	latest, ok, err := repo.LatestAttemptForAccount(ctx, "acct-1")
	if err != nil {
		t.Fatalf("latest attempt: %v", err)
	}
	if !ok || latest.Status != "pending" {
		t.Fatalf("expected pending attempt, got %#v (ok=%v)", latest, ok)
	}

	completed, ok, err := repo.CompleteAttempt(ctx, attempt.ID, "success", "2026-06-01 00:01:00", sql.NullString{}, sql.NullString{})
	if err != nil {
		t.Fatalf("complete attempt: %v", err)
	}
	if !ok || completed.Status != "success" {
		t.Fatalf("expected completed attempt, got %#v (ok=%v)", completed, ok)
	}

	byAccount, err := repo.LatestByAccount(ctx, []string{"acct-1"})
	if err != nil {
		t.Fatalf("latest by account: %v", err)
	}
	if entry, ok := byAccount["acct-1"]; !ok || entry.Status != "success" {
		t.Fatalf("expected success entry for acct-1, got %#v", byAccount)
	}
}
