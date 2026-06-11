package stickysessions_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/soju06/codex-lb/internal/config"
	dbpkg "github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/stickysessions"
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

func insertAccount(t *testing.T, store *dbpkg.Store, id string) {
	t.Helper()
	_, err := store.DB().Exec(`
		INSERT INTO accounts (
			id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
			id_token_encrypted, last_refresh, status
		) VALUES (?, ?, 'plus', x'00', x'00', x'00', '2026-01-01 00:00:00', 'active')
	`, id, id+"@example.com")
	if err != nil {
		t.Fatalf("insert account %s: %v", id, err)
	}
}

func TestUpsertAndGetAccountID(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	insertAccount(t, store, "acct-1")
	insertAccount(t, store, "acct-2")
	repo := stickysessions.NewRepository(store)

	if err := repo.Upsert(ctx, "session-a", "acct-1", "codex_session"); err != nil {
		t.Fatalf("upsert sticky session: %v", err)
	}
	accountID, err := repo.GetAccountID(ctx, "session-a", "codex_session", nil)
	if err != nil {
		t.Fatalf("get account id: %v", err)
	}
	if accountID != "acct-1" {
		t.Fatalf("expected acct-1, got %s", accountID)
	}

	if err := repo.Upsert(ctx, "session-a", "acct-2", "codex_session"); err != nil {
		t.Fatalf("upsert replacement sticky session: %v", err)
	}
	accountID, err = repo.GetAccountID(ctx, "session-a", "codex_session", nil)
	if err != nil {
		t.Fatalf("get replacement account id: %v", err)
	}
	if accountID != "acct-2" {
		t.Fatalf("expected acct-2, got %s", accountID)
	}
}

func TestGetAccountIDDeletesStaleEntry(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	insertAccount(t, store, "acct-1")
	repo := stickysessions.NewRepository(store)

	old := "2000-01-01 00:00:00"
	_, err := store.DB().Exec(`
		INSERT INTO sticky_sessions (key, kind, account_id, created_at, updated_at)
		VALUES ('cache-a', 'prompt_cache', 'acct-1', ?, ?)
	`, old, old)
	if err != nil {
		t.Fatalf("insert stale session: %v", err)
	}

	maxAge := 60
	accountID, err := repo.GetAccountID(ctx, "cache-a", "prompt_cache", &maxAge)
	if err != nil {
		t.Fatalf("get stale account id: %v", err)
	}
	if accountID != "" {
		t.Fatalf("expected no account for stale session, got %s", accountID)
	}
	entry, err := repo.GetEntry(ctx, "cache-a", "prompt_cache")
	if err != nil {
		t.Fatalf("get deleted stale entry: %v", err)
	}
	if entry != nil {
		t.Fatalf("expected stale entry to be deleted, got %+v", entry)
	}
}

func TestCleanupSchedulerRunsInitialTick(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	insertAccount(t, store, "acct-1")
	now := time.Now().UTC()
	oldTime := now.Add(-time.Hour).Format("2006-01-02 15:04:05")
	recentTime := now.Format("2006-01-02 15:04:05")
	if _, err := store.DB().ExecContext(ctx, `
		UPDATE dashboard_settings SET openai_cache_affinity_max_age_seconds = 60
	`); err != nil {
		t.Fatalf("update dashboard settings: %v", err)
	}
	for _, row := range []struct {
		key       string
		kind      string
		updatedAt string
	}{
		{key: "stale-cache", kind: "prompt_cache", updatedAt: oldTime},
		{key: "fresh-cache", kind: "prompt_cache", updatedAt: recentTime},
		{key: "sticky-thread", kind: "sticky_thread", updatedAt: oldTime},
	} {
		if _, err := store.DB().ExecContext(ctx, `
			INSERT INTO sticky_sessions (key, kind, account_id, created_at, updated_at)
			VALUES (?, ?, 'acct-1', ?, ?)
		`, row.key, row.kind, row.updatedAt, row.updatedAt); err != nil {
			t.Fatalf("insert sticky session %s: %v", row.key, err)
		}
	}

	scheduler := stickysessions.NewCleanupScheduler(store, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{
		StickyCleanupEnabled:  true,
		StickyCleanupInterval: time.Hour,
	}, "test-leader")
	scheduler.Start(ctx)
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = scheduler.Stop(stopCtx)
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entry, err := stickysessions.NewRepository(store).GetEntry(ctx, "stale-cache", "prompt_cache")
		if err != nil {
			t.Fatalf("load stale cache entry: %v", err)
		}
		if entry == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, row := range []struct {
		key  string
		kind string
	}{
		{key: "stale-cache", kind: "prompt_cache"},
		{key: "fresh-cache", kind: "prompt_cache"},
		{key: "sticky-thread", kind: "sticky_thread"},
	} {
		entry, err := stickysessions.NewRepository(store).GetEntry(ctx, row.key, row.kind)
		if err != nil {
			t.Fatalf("load entry %s: %v", row.key, err)
		}
		if row.key == "stale-cache" && entry != nil {
			t.Fatalf("expected stale prompt cache to be purged")
		}
		if row.key != "stale-cache" && entry == nil {
			t.Fatalf("expected %s to remain", row.key)
		}
	}
}
