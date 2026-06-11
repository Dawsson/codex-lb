package authguardian

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/crypto"
	dbpkg "github.com/soju06/codex-lb/internal/db"
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

func TestSelectCandidatesStaleActiveOldestFirst(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	candidates := SelectCandidates([]accounts.Account{
		{ID: "fresh", Status: "active", LastRefresh: "2026-06-10 11:59:00"},
		{ID: "old-2", Status: "active", LastRefresh: "2026-06-01 00:00:00"},
		{ID: "paused", Status: "paused", LastRefresh: "2026-06-01 00:00:00"},
		{ID: "old-1", Status: "active", LastRefresh: "2026-05-01 00:00:00"},
	}, now, time.Hour, 10)
	if len(candidates) != 2 || candidates[0].ID != "old-1" || candidates[1].ID != "old-2" {
		t.Fatalf("unexpected candidates: %#v", candidates)
	}
}

func TestSchedulerRefreshOnceRefreshesAndInvalidates(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.DB().Exec(`
		INSERT INTO accounts (
			id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
			id_token_encrypted, last_refresh, status
		) VALUES ('acct-1', 'a@example.com', 'plus', x'00', x'00', x'00', '2026-01-01 00:00:00', 'active')
	`); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	refresher := &fakeRefresher{}
	var invalidations int64
	scheduler := NewScheduler(store, slog.Default(), config.Config{
		AuthGuardianEnabled:       true,
		AuthGuardianInterval:      time.Hour,
		AuthGuardianMaxRefreshAge: time.Hour,
		AuthGuardianBatchSize:     10,
		AuthGuardianConcurrency:   2,
	}, refresher, "test-leader", func() { atomic.AddInt64(&invalidations, 1) }).WithClock(func() time.Time {
		return time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	})
	scheduler.refreshOnce(context.Background())
	if atomic.LoadInt64(&refresher.calls) != 1 {
		t.Fatalf("expected one refresh, got %d", refresher.calls)
	}
	if atomic.LoadInt64(&invalidations) != 1 {
		t.Fatalf("expected one invalidation, got %d", invalidations)
	}
}

func TestSchedulerBackoffSkipsRecentFailure(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.DB().Exec(`
		INSERT INTO accounts (
			id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
			id_token_encrypted, last_refresh, status
		) VALUES ('acct-1', 'a@example.com', 'plus', x'00', x'00', x'00', '2026-01-01 00:00:00', 'active')
	`); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	refresher := &fakeRefresher{err: errors.New("boom")}
	scheduler := NewScheduler(store, slog.Default(), config.Config{
		AuthGuardianEnabled:       true,
		AuthGuardianInterval:      time.Hour,
		AuthGuardianMaxRefreshAge: time.Hour,
		AuthGuardianBatchSize:     10,
		AuthGuardianConcurrency:   1,
	}, refresher, "test-leader", nil).WithClock(func() time.Time { return now })
	scheduler.refreshOnce(context.Background())
	scheduler.refreshOnce(context.Background())
	if atomic.LoadInt64(&refresher.calls) != 1 {
		t.Fatalf("expected second pass skipped by backoff, got %d calls", refresher.calls)
	}
}

func TestOAuthRefresherPersistsTokens(t *testing.T) {
	store := newTestStore(t)
	encryptor, err := crypto.NewEncryptor(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	refreshEncrypted, err := encryptor.Encrypt("refresh-old")
	if err != nil {
		t.Fatalf("encrypt refresh: %v", err)
	}
	if _, err := store.DB().Exec(`
		INSERT INTO accounts (
			id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
			id_token_encrypted, last_refresh, status
		) VALUES ('acct-1', 'old@example.com', 'plus', x'00', ?, x'00', '2026-01-01 00:00:00', 'active')
	`, refreshEncrypted); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-new",
			"refresh_token": "refresh-new",
			"id_token":      testJWT(map[string]any{"email": "new@example.com", "chatgpt_account_id": "chatgpt-1", "chatgpt_plan_type": "pro"}),
		})
	}))
	defer server.Close()
	repo := accounts.NewRepository(store)
	account, err := repo.Get(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	refresher := NewOAuthRefresher(repo, encryptor, config.Config{
		OAuthAuthBaseURL:    server.URL,
		OAuthClientID:       "client",
		OAuthScope:          "openid",
		OAuthTimeoutSeconds: 5,
	}, server.Client())
	if err := refresher.Refresh(context.Background(), *account); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	updated, err := repo.Get(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("get updated account: %v", err)
	}
	if updated.Email != "new@example.com" || updated.PlanType != "pro" || !updated.ChatGPTAccountID.Valid || updated.ChatGPTAccountID.String != "chatgpt-1" {
		t.Fatalf("unexpected updated account: %#v", updated)
	}
	access, err := encryptor.Decrypt(updated.AccessTokenEncrypted)
	if err != nil || access != "access-new" {
		t.Fatalf("unexpected encrypted access token %q err=%v", access, err)
	}
}

func TestOAuthRefresherPermanentFailureMarksReauthRequired(t *testing.T) {
	store := newTestStore(t)
	encryptor, err := crypto.NewEncryptor(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	refreshEncrypted, err := encryptor.Encrypt("refresh-old")
	if err != nil {
		t.Fatalf("encrypt refresh: %v", err)
	}
	if _, err := store.DB().Exec(`
		INSERT INTO accounts (
			id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
			id_token_encrypted, last_refresh, status
		) VALUES ('acct-1', 'old@example.com', 'plus', x'00', ?, x'00', '2026-01-01 00:00:00', 'active')
	`, refreshEncrypted); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "invalid_grant", "message": "bad grant"}})
	}))
	defer server.Close()
	repo := accounts.NewRepository(store)
	account, _ := repo.Get(context.Background(), "acct-1")
	refresher := NewOAuthRefresher(repo, encryptor, config.Config{
		OAuthAuthBaseURL:    server.URL,
		OAuthClientID:       "client",
		OAuthScope:          "openid",
		OAuthTimeoutSeconds: 5,
	}, server.Client())
	if err := refresher.Refresh(context.Background(), *account); err == nil {
		t.Fatalf("expected refresh error")
	}
	var status string
	var reason sql.NullString
	if err := store.DB().QueryRow(`SELECT status, deactivation_reason FROM accounts WHERE id = 'acct-1'`).Scan(&status, &reason); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "reauth_required" || !reason.Valid {
		t.Fatalf("expected reauth_required with reason, got %s %#v", status, reason)
	}
}

type fakeRefresher struct {
	calls int64
	err   error
}

func (f *fakeRefresher) Refresh(context.Context, accounts.Account) error {
	atomic.AddInt64(&f.calls, 1)
	return f.err
}

func testJWT(payload map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body, _ := json.Marshal(payload)
	return header + "." + base64.RawURLEncoding.EncodeToString(body) + ".sig"
}
