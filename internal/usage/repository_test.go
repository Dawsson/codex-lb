package usage_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/soju06/codex-lb/internal/config"
	dbpkg "github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/usage"
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

func TestUsageRepositoryAddAndLatest(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := usage.NewRepository(store)

	if _, err := repo.AddEntry(ctx, usage.Entry{
		AccountID:   "acct-1",
		RecordedAt:  "2026-06-01 00:00:00",
		UsedPercent: 10,
	}); err != nil {
		t.Fatalf("add entry: %v", err)
	}
	if _, err := repo.AddEntry(ctx, usage.Entry{
		AccountID:   "acct-1",
		RecordedAt:  "2026-06-02 00:00:00",
		UsedPercent: 20,
		Window:      sql.NullString{String: "primary", Valid: true},
	}); err != nil {
		t.Fatalf("add entry: %v", err)
	}

	latest, err := repo.LatestByAccount(ctx, "primary", nil)
	if err != nil {
		t.Fatalf("latest by account: %v", err)
	}
	entry, ok := latest["acct-1"]
	if !ok {
		t.Fatalf("expected entry for acct-1, got %#v", latest)
	}
	if entry.UsedPercent != 20 {
		t.Fatalf("expected latest used_percent 20, got %v", entry.UsedPercent)
	}

	rows, err := repo.AggregateSince(ctx, "2026-06-01 00:00:00", "primary")
	if err != nil {
		t.Fatalf("aggregate since: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 aggregate row, got %d", len(rows))
	}
	if rows[0].Samples != 2 {
		t.Fatalf("expected 2 samples, got %d", rows[0].Samples)
	}
}

func TestAdditionalUsageRepository(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := usage.NewRepository(store)

	entry, err := repo.AddAdditionalEntry(ctx, usage.AdditionalEntry{
		AccountID:      "acct-1",
		QuotaKey:       "gpt5_codex_high",
		LimitName:      "gpt5_codex_high",
		MeteredFeature: "codex",
		Window:         "primary",
		UsedPercent:    5,
		RecordedAt:     "2026-06-01 00:00:00",
	})
	if err != nil {
		t.Fatalf("add additional entry: %v", err)
	}
	if entry.ID == 0 {
		t.Fatalf("expected non-zero id")
	}

	if err := repo.DeleteAdditionalForAccount(ctx, "acct-1"); err != nil {
		t.Fatalf("delete additional for account: %v", err)
	}

	var count int
	if err := store.DB().QueryRow(`SELECT count(*) FROM additional_usage_history WHERE account_id = 'acct-1'`).Scan(&count); err != nil {
		t.Fatalf("count additional usage history: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows after delete, got %d", count)
	}
}

func TestAdditionalUsageRepositoryQuotaKeyHelpers(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := usage.NewRepository(store)

	for _, entry := range []usage.AdditionalEntry{
		{AccountID: "acct-1", QuotaKey: "codex_spark", LimitName: "codex_other", MeteredFeature: "codex_bengalfox", Window: "primary", UsedPercent: 10, RecordedAt: "2026-06-01 00:00:00"},
		{AccountID: "acct-1", QuotaKey: "codex_spark", LimitName: "codex_other", MeteredFeature: "codex_bengalfox", Window: "secondary", UsedPercent: 20, RecordedAt: "2026-06-01 00:01:00"},
		{AccountID: "acct-1", QuotaKey: "stale_quota", LimitName: "stale", MeteredFeature: "stale", Window: "primary", UsedPercent: 30, RecordedAt: "2026-06-01 00:02:00"},
	} {
		if _, err := repo.AddAdditionalEntry(ctx, entry); err != nil {
			t.Fatalf("add additional entry: %v", err)
		}
	}

	keys, err := repo.ListAdditionalQuotaKeys(ctx, []string{"acct-1"})
	if err != nil {
		t.Fatalf("list additional quota keys: %v", err)
	}
	if len(keys) != 2 || keys[0] != "codex_spark" || keys[1] != "stale_quota" {
		t.Fatalf("unexpected keys: %#v", keys)
	}
	latest, err := repo.LatestAdditionalRecordedAtForAccount(ctx, "acct-1")
	if err != nil {
		t.Fatalf("latest additional recorded at: %v", err)
	}
	if !latest.Valid || latest.String != "2026-06-01 00:02:00" {
		t.Fatalf("unexpected latest recorded at: %#v", latest)
	}
	if err := repo.DeleteAdditionalForAccountQuotaKeyWindow(ctx, "acct-1", "codex_spark", "secondary"); err != nil {
		t.Fatalf("delete window: %v", err)
	}
	if err := repo.DeleteAdditionalForAccountAndQuotaKey(ctx, "acct-1", "stale_quota"); err != nil {
		t.Fatalf("delete quota key: %v", err)
	}
	var count int
	if err := store.DB().QueryRow(`SELECT count(*) FROM additional_usage_history WHERE account_id = 'acct-1'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one remaining row, got %d", count)
	}
}
