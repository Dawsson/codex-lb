package accounts_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/config"
	dbpkg "github.com/soju06/codex-lb/internal/db"
)

func newUpsertTestStore(t *testing.T) *dbpkg.Store {
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

func baseOAuthAccount() accounts.OAuthAccount {
	return accounts.OAuthAccount{
		ID:                    "acct-1",
		ChatGPTAccountID:      sql.NullString{String: "chatgpt-1", Valid: true},
		Email:                 "user@example.com",
		PlanType:              "pro",
		AccessTokenEncrypted:  []byte("access"),
		RefreshTokenEncrypted: []byte("refresh"),
		IDTokenEncrypted:      []byte("id"),
		LastRefresh:           "2026-01-01 00:00:00",
		Status:                "active",
	}
}

func TestUpsertOAuthAccountInsertsNewAccount(t *testing.T) {
	ctx := context.Background()
	store := newUpsertTestStore(t)
	repo := accounts.NewRepository(store)

	account, err := repo.UpsertOAuthAccount(ctx, baseOAuthAccount())
	if err != nil {
		t.Fatalf("upsert oauth account: %v", err)
	}
	if account.ID != "acct-1" {
		t.Fatalf("expected id acct-1, got %s", account.ID)
	}
	if account.Email != "user@example.com" {
		t.Fatalf("expected email user@example.com, got %s", account.Email)
	}
}

func TestUpsertOAuthAccountReauthByChatGPTIdentity(t *testing.T) {
	ctx := context.Background()
	store := newUpsertTestStore(t)
	repo := accounts.NewRepository(store)

	first := baseOAuthAccount()
	if _, err := repo.UpsertOAuthAccount(ctx, first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Re-auth with the same chatgpt identity but a different incoming id and
	// updated email/tokens should reuse the canonical row, not insert a new
	// __copy row.
	reauth := baseOAuthAccount()
	reauth.ID = "acct-2"
	reauth.Email = "renamed@example.com"
	reauth.AccessTokenEncrypted = []byte("access-2")

	account, err := repo.UpsertOAuthAccount(ctx, reauth)
	if err != nil {
		t.Fatalf("reauth upsert: %v", err)
	}
	if account.ID != "acct-1" {
		t.Fatalf("expected canonical id acct-1, got %s", account.ID)
	}
	if account.Email != "renamed@example.com" {
		t.Fatalf("expected updated email, got %s", account.Email)
	}

	rows, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 account row, got %d", len(rows))
	}
}

func TestUpsertOAuthAccountIDCollisionGetsCopySuffix(t *testing.T) {
	ctx := context.Background()
	store := newUpsertTestStore(t)
	repo := accounts.NewRepository(store)

	first := baseOAuthAccount()
	if _, err := repo.UpsertOAuthAccount(ctx, first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Different chatgpt identity but same deterministic account id: must not
	// be merged into the existing row, and must not collide on id.
	second := baseOAuthAccount()
	second.ChatGPTAccountID = sql.NullString{String: "chatgpt-2", Valid: true}
	second.Email = "other@example.com"

	account, err := repo.UpsertOAuthAccount(ctx, second)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if account.ID != "acct-1__copy2" {
		t.Fatalf("expected acct-1__copy2, got %s", account.ID)
	}

	rows, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 account rows, got %d", len(rows))
	}
}

func TestUpsertOAuthAccountSlotByWorkspace(t *testing.T) {
	ctx := context.Background()
	store := newUpsertTestStore(t)
	repo := accounts.NewRepository(store)

	first := baseOAuthAccount()
	first.WorkspaceID = sql.NullString{String: "ws-1", Valid: true}
	first.WorkspaceLabel = sql.NullString{String: "Workspace One", Valid: true}
	if _, err := repo.UpsertOAuthAccount(ctx, first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Same chatgpt identity + workspace, different incoming id: should reuse
	// the existing slot row via _account_by_slot_identity.
	reauth := baseOAuthAccount()
	reauth.ID = "acct-1_deadbeef"
	reauth.WorkspaceID = sql.NullString{String: "ws-1", Valid: true}
	reauth.WorkspaceLabel = sql.NullString{String: "Workspace One Renamed", Valid: true}

	account, err := repo.UpsertOAuthAccount(ctx, reauth)
	if err != nil {
		t.Fatalf("reauth upsert: %v", err)
	}
	if account.ID != "acct-1" {
		t.Fatalf("expected reuse of acct-1, got %s", account.ID)
	}
	if !account.WorkspaceLabel.Valid || account.WorkspaceLabel.String != "Workspace One Renamed" {
		t.Fatalf("expected updated workspace label, got %#v", account.WorkspaceLabel)
	}
}

func TestUpsertOAuthAccountEmailConflict(t *testing.T) {
	ctx := context.Background()
	store := newUpsertTestStore(t)
	repo := accounts.NewRepository(store)

	// Two pre-existing accounts with the same email but different chatgpt
	// identities and no workspace (e.g. legacy duplicates from before
	// identity tracking), inserted directly.
	for _, id := range []string{"acct-a", "acct-b"} {
		if _, err := store.DB().ExecContext(ctx, `
			INSERT INTO accounts (
				id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
				id_token_encrypted, last_refresh, status
			) VALUES (?, 'shared@example.com', 'pro', x'00', x'00', x'00', '2026-01-01 00:00:00', 'active')
		`, id); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	// A third account with a brand new id and the same email, no chatgpt
	// identity: the email-fallback lookup finds 2 matches and must report a
	// conflict rather than guessing.
	c := baseOAuthAccount()
	c.ID = "acct-c"
	c.ChatGPTAccountID = sql.NullString{}
	c.Email = "shared@example.com"

	_, err := repo.UpsertOAuthAccount(ctx, c)
	if !errors.Is(err, accounts.ErrAccountIdentityConflict) {
		t.Fatalf("expected ErrAccountIdentityConflict, got %v", err)
	}
}
