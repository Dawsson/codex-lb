package usagerefresh

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
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
	return store
}

type fakeRefresher struct {
	calls int64
	wrote bool
}

func (f *fakeRefresher) RefreshAccounts(context.Context, []accounts.ProxyRecord, map[string]usage.Entry) (bool, error) {
	atomic.AddInt64(&f.calls, 1)
	return f.wrote, nil
}

func TestRefreshSchedulerStartStop(t *testing.T) {
	store := newTestStore(t)
	refresher := &fakeRefresher{}
	scheduler := NewRefreshScheduler(
		store,
		slog.Default(),
		config.Config{UsageRefreshEnabled: true, UsageRefreshInterval: 10 * time.Millisecond},
		refresher,
		nil,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scheduler.Start(ctx)
	time.Sleep(25 * time.Millisecond)
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if err := scheduler.Stop(stopCtx); err != nil {
		t.Fatalf("stop scheduler: %v", err)
	}
	if atomic.LoadInt64(&refresher.calls) == 0 {
		t.Fatalf("expected scheduler to call refresher")
	}
}

func TestRefreshSchedulerInvalidatesAfterSuccessfulWrite(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.DB().Exec(`
		INSERT INTO accounts (
			id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
			id_token_encrypted, last_refresh, status
		) VALUES ('acct-1', 'a@example.com', 'plus', x'00', x'00', x'00', '2026-01-01 00:00:00', 'active')
	`); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	refresher := &fakeRefresher{wrote: true}
	var invalidations int64
	scheduler := NewRefreshScheduler(
		store,
		slog.Default(),
		config.Config{UsageRefreshEnabled: true, UsageRefreshInterval: time.Hour},
		refresher,
		func() { atomic.AddInt64(&invalidations, 1) },
	)
	scheduler.SetLeaderID("test-leader")
	scheduler.refreshOnce(context.Background())
	if atomic.LoadInt64(&invalidations) != 1 {
		t.Fatalf("expected one invalidation after write, got %d", invalidations)
	}
}

func TestReconcileRecoverableAccountStatuses(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := accounts.NewRepository(store)
	now := time.Now().Unix()
	blockedAt := now - 600
	resetAt := now - 60

	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO accounts (
			id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
			id_token_encrypted, last_refresh, status, reset_at, blocked_at
		) VALUES ('acct-1', 'a@example.com', 'plus', x'00', x'00', x'00', '2026-01-01 00:00:00', 'rate_limited', ?, ?)
	`, resetAt, blockedAt); err != nil {
		t.Fatalf("insert account: %v", err)
	}

	record := accounts.ProxyRecord{
		ID:                     "acct-1",
		Email:                  "a@example.com",
		PlanType:               "plus",
		Status:                 "rate_limited",
		RoutingPolicy:          "normal",
		SecurityWorkAuthorized: true,
		ResetAt:                sql.NullFloat64{Float64: float64(resetAt), Valid: true},
		BlockedAt:              sql.NullFloat64{Float64: float64(blockedAt), Valid: true},
	}
	used := 10.0
	recovered, err := ReconcileRecoverableAccountStatuses(
		ctx,
		repo,
		[]accounts.ProxyRecord{record},
		map[string]usage.Entry{
			"acct-1": {
				AccountID:   "acct-1",
				RecordedAt:  time.Now().UTC().Format("2006-01-02 15:04:05"),
				Window:      sql.NullString{String: "primary", Valid: true},
				UsedPercent: used,
			},
		},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("reconcile recoverable: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("expected 1 recovered account, got %d", recovered)
	}

	var status string
	if err := store.DB().QueryRowContext(ctx, `SELECT status FROM accounts WHERE id = 'acct-1'`).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "active" {
		t.Fatalf("expected active status, got %s", status)
	}
}

func TestHTTPUsageUpdaterSyncAdditionalUsageCanonicalizesAndPrunes(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO accounts (
			id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
			id_token_encrypted, last_refresh, status
		) VALUES ('acct-1', 'a@example.com', 'pro', x'00', x'00', x'00', '2026-01-01 00:00:00', 'active')
	`); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	repo := usage.NewRepository(store)
	if _, err := repo.AddAdditionalEntry(ctx, usage.AdditionalEntry{
		AccountID:      "acct-1",
		QuotaKey:       "stale_quota",
		LimitName:      "stale",
		MeteredFeature: "stale",
		Window:         "primary",
		UsedPercent:    99,
		RecordedAt:     "2026-06-01 00:00:00",
	}); err != nil {
		t.Fatalf("insert stale row: %v", err)
	}
	primaryUsed := 12.5
	secondaryUsed := 50.0
	resetAfter := int64(300)
	windowSeconds := int64(18000)
	updater := NewHTTPUsageUpdater(repo, accounts.Repository{}, nil, config.Config{})
	wrote, err := updater.syncAdditionalUsage(ctx, "acct-1", usagePayload{
		AdditionalRateLimits: &[]usagePayloadAdditionalRateLimit{
			{
				LimitName:      "codex_other",
				MeteredFeature: "codex_bengalfox",
				RateLimit: &usagePayloadRateLimit{
					PrimaryWindow: &usagePayloadWindow{
						UsedPercent:        &primaryUsed,
						ResetAfterSeconds:  &resetAfter,
						LimitWindowSeconds: &windowSeconds,
					},
					SecondaryWindow: &usagePayloadWindow{UsedPercent: &secondaryUsed},
				},
			},
		},
	}, 1_800_000_000)
	if err != nil {
		t.Fatalf("sync additional usage: %v", err)
	}
	if !wrote {
		t.Fatalf("expected sync to report written")
	}
	var count int
	if err := store.DB().QueryRowContext(ctx, `
		SELECT count(*) FROM additional_usage_history
		 WHERE account_id = 'acct-1' AND quota_key = 'codex_spark'
	`).Scan(&count); err != nil {
		t.Fatalf("count codex spark rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected two codex_spark rows, got %d", count)
	}
	if err := store.DB().QueryRowContext(ctx, `
		SELECT count(*) FROM additional_usage_history
		 WHERE account_id = 'acct-1' AND quota_key = 'stale_quota'
	`).Scan(&count); err != nil {
		t.Fatalf("count stale rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected stale quota to be pruned, got %d rows", count)
	}
	var resetAt, minutes int64
	if err := store.DB().QueryRowContext(ctx, `
		SELECT reset_at, window_minutes FROM additional_usage_history
		 WHERE account_id = 'acct-1' AND quota_key = 'codex_spark' AND window = 'primary'
		 ORDER BY id DESC LIMIT 1
	`).Scan(&resetAt, &minutes); err != nil {
		t.Fatalf("read primary row: %v", err)
	}
	if resetAt != 1_800_000_300 || minutes != 300 {
		t.Fatalf("unexpected reset/window minutes: %d %d", resetAt, minutes)
	}
}

func TestHTTPUsageUpdaterSyncAdditionalUsageEmptyPayloadDeletesRows(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO accounts (
			id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
			id_token_encrypted, last_refresh, status
		) VALUES ('acct-1', 'a@example.com', 'pro', x'00', x'00', x'00', '2026-01-01 00:00:00', 'active')
	`); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	repo := usage.NewRepository(store)
	if _, err := repo.AddAdditionalEntry(ctx, usage.AdditionalEntry{
		AccountID: "acct-1", QuotaKey: "codex_spark", LimitName: "codex_other", MeteredFeature: "codex_bengalfox", Window: "primary", UsedPercent: 99,
	}); err != nil {
		t.Fatalf("insert row: %v", err)
	}
	updater := NewHTTPUsageUpdater(repo, accounts.Repository{}, nil, config.Config{})
	empty := []usagePayloadAdditionalRateLimit{}
	wrote, err := updater.syncAdditionalUsage(ctx, "acct-1", usagePayload{AdditionalRateLimits: &empty}, 1_800_000_000)
	if err != nil {
		t.Fatalf("sync empty additional usage: %v", err)
	}
	if !wrote {
		t.Fatalf("expected empty additional payload to count as sync")
	}
	var count int
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM additional_usage_history WHERE account_id = 'acct-1'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected rows deleted, got %d", count)
	}
}

func TestHTTPUsageUpdaterSkipsFreshAdditionalOnlyAccount(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := usage.NewRepository(store)
	encryptor, err := crypto.NewEncryptor(filepath.Join(t.TempDir(), "encryption.key"))
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	accessToken, err := encryptor.Encrypt("access")
	if err != nil {
		t.Fatalf("encrypt access: %v", err)
	}
	if _, err := repo.AddAdditionalEntry(ctx, usage.AdditionalEntry{
		AccountID:      "acct-1",
		QuotaKey:       "codex_spark",
		LimitName:      "codex_other",
		MeteredFeature: "codex_bengalfox",
		Window:         "primary",
		UsedPercent:    12,
		RecordedAt:     time.Now().UTC().Format("2006-01-02 15:04:05"),
	}); err != nil {
		t.Fatalf("insert additional usage: %v", err)
	}

	var calls int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	updater := NewHTTPUsageUpdater(repo, accounts.NewRepository(store), encryptor, config.Config{UsageRefreshInterval: time.Hour})
	updater.client = server.Client()
	updater.usageURL = server.URL
	updater.cfg.UsageFetchMaxRetries = 0

	wrote, err := updater.RefreshAccounts(ctx, []accounts.ProxyRecord{{
		ID:                   "acct-1",
		Status:               "active",
		AccessTokenEncrypted: accessToken,
	}}, map[string]usage.Entry{})
	if err != nil {
		t.Fatalf("refresh accounts: %v", err)
	}
	if wrote {
		t.Fatalf("expected no write for fresh additional-only account")
	}
	if atomic.LoadInt64(&calls) != 0 {
		t.Fatalf("expected fresh additional-only account to skip fetch, got %d calls", calls)
	}
}

func TestHTTPUsageUpdaterCachesSuccessfulAdditionalOnlyEmptyRefresh(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := usage.NewRepository(store)
	encryptor, err := crypto.NewEncryptor(filepath.Join(t.TempDir(), "encryption.key"))
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	accessToken, err := encryptor.Encrypt("access")
	if err != nil {
		t.Fatalf("encrypt access: %v", err)
	}

	var calls int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rate_limit":null,"additional_rate_limits":[]}`))
	}))
	defer server.Close()
	updater := NewHTTPUsageUpdater(repo, accounts.NewRepository(store), encryptor, config.Config{UsageRefreshInterval: time.Hour})
	updater.client = server.Client()
	updater.usageURL = server.URL
	updater.cfg.UsageFetchMaxRetries = 0

	records := []accounts.ProxyRecord{{
		ID:                   "acct-1",
		Status:               "active",
		AccessTokenEncrypted: accessToken,
	}}
	wrote, err := updater.RefreshAccounts(ctx, records, map[string]usage.Entry{})
	if err != nil {
		t.Fatalf("first refresh accounts: %v", err)
	}
	if !wrote {
		t.Fatalf("expected empty additional payload to count as a write")
	}
	wrote, err = updater.RefreshAccounts(ctx, records, map[string]usage.Entry{})
	if err != nil {
		t.Fatalf("second refresh accounts: %v", err)
	}
	if wrote {
		t.Fatalf("expected second refresh to be skipped by local freshness cache")
	}
	if atomic.LoadInt64(&calls) != 1 {
		t.Fatalf("expected one fetch, got %d", calls)
	}
}

func TestHTTPUsageUpdaterDeactivatesForUsage404(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := usage.NewRepository(store)
	encryptor, accessToken := usageRefreshTestToken(t)
	insertUsageRefreshAccount(t, store, "acct-1", accessToken)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"account missing"}}`))
	}))
	defer server.Close()
	updater := NewHTTPUsageUpdater(repo, accounts.NewRepository(store), encryptor, config.Config{})
	updater.client = server.Client()
	updater.usageURL = server.URL
	updater.cfg.UsageFetchMaxRetries = 0

	if _, err := updater.RefreshAccounts(ctx, []accounts.ProxyRecord{{
		ID:                   "acct-1",
		Status:               "active",
		AccessTokenEncrypted: accessToken,
	}}, map[string]usage.Entry{}); err != nil {
		t.Fatalf("refresh accounts: %v", err)
	}
	assertAccountStatus(t, store, "acct-1", "deactivated", "Usage API error: HTTP 404 - account missing")
}

func TestHTTPUsageUpdaterPermanentErrorCodeMapsToReauthRequired(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := usage.NewRepository(store)
	encryptor, accessToken := usageRefreshTestToken(t)
	insertUsageRefreshAccount(t, store, "acct-1", accessToken)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"token_expired","message":"expired"}}`))
	}))
	defer server.Close()
	updater := NewHTTPUsageUpdater(repo, accounts.NewRepository(store), encryptor, config.Config{})
	updater.client = server.Client()
	updater.usageURL = server.URL
	updater.cfg.UsageFetchMaxRetries = 0

	if _, err := updater.RefreshAccounts(ctx, []accounts.ProxyRecord{{
		ID:                   "acct-1",
		Status:               "active",
		AccessTokenEncrypted: accessToken,
	}}, map[string]usage.Entry{}); err != nil {
		t.Fatalf("refresh accounts: %v", err)
	}
	assertAccountStatus(t, store, "acct-1", "reauth_required", "Usage API error: HTTP 401 - expired")
}

func TestHTTPUsageUpdaterDoesNotDeactivateForGeneric403(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := usage.NewRepository(store)
	encryptor, accessToken := usageRefreshTestToken(t)
	insertUsageRefreshAccount(t, store, "acct-1", accessToken)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"forbidden"}}`))
	}))
	defer server.Close()
	updater := NewHTTPUsageUpdater(repo, accounts.NewRepository(store), encryptor, config.Config{})
	updater.client = server.Client()
	updater.usageURL = server.URL
	updater.cfg.UsageFetchMaxRetries = 0

	if _, err := updater.RefreshAccounts(ctx, []accounts.ProxyRecord{{
		ID:                   "acct-1",
		Status:               "active",
		AccessTokenEncrypted: accessToken,
	}}, map[string]usage.Entry{}); err != nil {
		t.Fatalf("refresh accounts: %v", err)
	}
	assertAccountStatus(t, store, "acct-1", "active", "")
}

func TestHTTPUsageUpdaterSyncsIdentityMetadataFromUsagePayload(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := usage.NewRepository(store)
	encryptor, accessToken := usageRefreshTestToken(t)
	insertUsageRefreshAccount(t, store, "acct-1", accessToken)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"plan_type":"Pro",
			"workspace_id":" ws-1 ",
			"workspace_label":"Workspace One",
			"seat_type":"member",
			"rate_limit":{"primary_window":{"used_percent":10.0}}
		}`))
	}))
	defer server.Close()
	updater := NewHTTPUsageUpdater(repo, accounts.NewRepository(store), encryptor, config.Config{})
	updater.client = server.Client()
	updater.usageURL = server.URL
	updater.cfg.UsageFetchMaxRetries = 0

	if _, err := updater.RefreshAccounts(ctx, []accounts.ProxyRecord{{
		ID:                    "acct-1",
		Email:                 "a@example.com",
		PlanType:              "plus",
		Status:                "active",
		AccessTokenEncrypted:  accessToken,
		RefreshTokenEncrypted: []byte{0},
		IDTokenEncrypted:      []byte{0},
		LastRefresh:           "2026-01-01 00:00:00",
	}}, map[string]usage.Entry{}); err != nil {
		t.Fatalf("refresh accounts: %v", err)
	}
	var planType string
	var workspaceID, workspaceLabel, seatType sql.NullString
	if err := store.DB().QueryRowContext(ctx, `
		SELECT plan_type, workspace_id, workspace_label, seat_type
		  FROM accounts WHERE id = 'acct-1'
	`).Scan(&planType, &workspaceID, &workspaceLabel, &seatType); err != nil {
		t.Fatalf("read account metadata: %v", err)
	}
	if planType != "pro" {
		t.Fatalf("expected normalized plan pro, got %q", planType)
	}
	if !workspaceID.Valid || workspaceID.String != "ws-1" {
		t.Fatalf("expected workspace ws-1, got %#v", workspaceID)
	}
	if !workspaceLabel.Valid || workspaceLabel.String != "Workspace One" {
		t.Fatalf("expected workspace label, got %#v", workspaceLabel)
	}
	if !seatType.Valid || seatType.String != "member" {
		t.Fatalf("expected seat type, got %#v", seatType)
	}
	var usedPercent float64
	if err := store.DB().QueryRowContext(ctx, `
		SELECT used_percent FROM usage_history WHERE account_id = 'acct-1' AND window = 'primary'
	`).Scan(&usedPercent); err != nil {
		t.Fatalf("read usage row: %v", err)
	}
	if usedPercent != 10.0 {
		t.Fatalf("expected usage 10.0, got %.1f", usedPercent)
	}
}

func TestHTTPUsageUpdaterPreservesIdentityMetadataWhenPayloadOmitsFields(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := usage.NewRepository(store)
	encryptor, accessToken := usageRefreshTestToken(t)
	insertUsageRefreshAccount(t, store, "acct-1", accessToken)
	if _, err := store.DB().ExecContext(ctx, `
		UPDATE accounts
		   SET workspace_id = 'ws-existing',
		       workspace_label = 'Existing Workspace',
		       seat_type = 'owner',
		       plan_type = 'team'
		 WHERE id = 'acct-1'
	`); err != nil {
		t.Fatalf("seed metadata: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":5.0}}}`))
	}))
	defer server.Close()
	updater := NewHTTPUsageUpdater(repo, accounts.NewRepository(store), encryptor, config.Config{})
	updater.client = server.Client()
	updater.usageURL = server.URL
	updater.cfg.UsageFetchMaxRetries = 0

	if _, err := updater.RefreshAccounts(ctx, []accounts.ProxyRecord{{
		ID:                   "acct-1",
		Email:                "a@example.com",
		PlanType:             "team",
		Status:               "active",
		WorkspaceID:          sql.NullString{String: "ws-existing", Valid: true},
		WorkspaceLabel:       sql.NullString{String: "Existing Workspace", Valid: true},
		SeatType:             sql.NullString{String: "owner", Valid: true},
		AccessTokenEncrypted: accessToken,
		LastRefresh:          "2026-01-01 00:00:00",
	}}, map[string]usage.Entry{}); err != nil {
		t.Fatalf("refresh accounts: %v", err)
	}
	var planType string
	var workspaceID, workspaceLabel, seatType sql.NullString
	if err := store.DB().QueryRowContext(ctx, `
		SELECT plan_type, workspace_id, workspace_label, seat_type
		  FROM accounts WHERE id = 'acct-1'
	`).Scan(&planType, &workspaceID, &workspaceLabel, &seatType); err != nil {
		t.Fatalf("read account metadata: %v", err)
	}
	if planType != "team" || workspaceID.String != "ws-existing" || workspaceLabel.String != "Existing Workspace" || seatType.String != "owner" {
		t.Fatalf("metadata was not preserved: plan=%q workspace=%#v label=%#v seat=%#v", planType, workspaceID, workspaceLabel, seatType)
	}
}

func TestHTTPUsageUpdaterRefreshesAuthAndRetriesUsageOn401(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := usage.NewRepository(store)
	encryptor, accessToken := usageRefreshTestToken(t)
	refreshEncrypted, err := encryptor.Encrypt("refresh-old")
	if err != nil {
		t.Fatalf("encrypt refresh: %v", err)
	}
	idEncrypted, err := encryptor.Encrypt(testUsageRefreshJWT(map[string]any{"email": "old@example.com"}))
	if err != nil {
		t.Fatalf("encrypt id token: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO accounts (
			id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
			id_token_encrypted, last_refresh, status
		) VALUES ('acct-1', 'old@example.com', 'plus', ?, ?, ?, '2026-01-01 00:00:00', 'active')
	`, accessToken, refreshEncrypted, idEncrypted); err != nil {
		t.Fatalf("insert account: %v", err)
	}

	var usageCalls int64
	var secondAuth string
	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := atomic.AddInt64(&usageCalls, 1)
		if call == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"expired"}}`))
			return
		}
		secondAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":7.5}}}`))
	}))
	defer usageServer.Close()

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			t.Fatalf("unexpected auth path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-new",
			"refresh_token": "refresh-new",
			"id_token": testUsageRefreshJWT(map[string]any{
				"email":              "new@example.com",
				"chatgpt_account_id": "chatgpt-1",
				"chatgpt_plan_type":  "pro",
			}),
		})
	}))
	defer authServer.Close()

	updater := NewHTTPUsageUpdater(repo, accounts.NewRepository(store), encryptor, config.Config{
		OAuthAuthBaseURL:    authServer.URL,
		OAuthClientID:       "client",
		OAuthScope:          "openid",
		OAuthTimeoutSeconds: 5,
	})
	updater.client = usageServer.Client()
	updater.usageURL = usageServer.URL
	updater.cfg.UsageFetchMaxRetries = 0

	wrote, err := updater.RefreshAccounts(ctx, []accounts.ProxyRecord{{
		ID:                    "acct-1",
		Email:                 "old@example.com",
		PlanType:              "plus",
		Status:                "active",
		AccessTokenEncrypted:  accessToken,
		RefreshTokenEncrypted: refreshEncrypted,
		IDTokenEncrypted:      idEncrypted,
		LastRefresh:           "2026-01-01 00:00:00",
	}}, map[string]usage.Entry{})
	if err != nil {
		t.Fatalf("refresh accounts: %v", err)
	}
	if !wrote {
		t.Fatalf("expected retried usage response to write")
	}
	if atomic.LoadInt64(&usageCalls) != 2 {
		t.Fatalf("expected two usage calls, got %d", usageCalls)
	}
	if secondAuth != "Bearer access-new" {
		t.Fatalf("expected retry with refreshed token, got %q", secondAuth)
	}
	var usedPercent float64
	if err := store.DB().QueryRowContext(ctx, `
		SELECT used_percent FROM usage_history WHERE account_id = 'acct-1' AND window = 'primary'
	`).Scan(&usedPercent); err != nil {
		t.Fatalf("read usage row: %v", err)
	}
	if usedPercent != 7.5 {
		t.Fatalf("expected usage 7.5, got %.1f", usedPercent)
	}
	updated, err := accounts.NewRepository(store).Get(ctx, "acct-1")
	if err != nil {
		t.Fatalf("get updated account: %v", err)
	}
	if updated.Email != "new@example.com" || updated.PlanType != "pro" {
		t.Fatalf("expected auth refresh metadata, got %#v", updated)
	}
}

func TestHTTPUsageUpdaterPersistsMonthlyOnlyUsagePayload(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := usage.NewRepository(store)
	encryptor, accessToken := usageRefreshTestToken(t)
	insertUsageRefreshAccount(t, store, "acct-1", accessToken)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"rate_limit":{"primary_window":{"used_percent":12.5,"limit_window_seconds":2592000}},
			"credits":{"has_credits":true,"unlimited":false,"balance":"87.5"}
		}`))
	}))
	defer server.Close()
	updater := NewHTTPUsageUpdater(repo, accounts.NewRepository(store), encryptor, config.Config{})
	updater.client = server.Client()
	updater.usageURL = server.URL
	updater.cfg.UsageFetchMaxRetries = 0

	wrote, err := updater.RefreshAccounts(ctx, []accounts.ProxyRecord{{
		ID:                   "acct-1",
		Status:               "active",
		PlanType:             "free",
		AccessTokenEncrypted: accessToken,
	}}, map[string]usage.Entry{})
	if err != nil {
		t.Fatalf("refresh accounts: %v", err)
	}
	if !wrote {
		t.Fatalf("expected monthly usage payload to write")
	}
	var monthlyCount, primaryCount int
	if err := store.DB().QueryRowContext(ctx, `
		SELECT count(*) FROM usage_history WHERE account_id = 'acct-1' AND window = 'monthly'
	`).Scan(&monthlyCount); err != nil {
		t.Fatalf("count monthly rows: %v", err)
	}
	if err := store.DB().QueryRowContext(ctx, `
		SELECT count(*) FROM usage_history WHERE account_id = 'acct-1' AND window = 'primary'
	`).Scan(&primaryCount); err != nil {
		t.Fatalf("count primary rows: %v", err)
	}
	if monthlyCount != 1 || primaryCount != 0 {
		t.Fatalf("expected one monthly and zero primary rows, got monthly=%d primary=%d", monthlyCount, primaryCount)
	}
	var usedPercent, balance float64
	if err := store.DB().QueryRowContext(ctx, `
		SELECT used_percent, credits_balance
		  FROM usage_history
		 WHERE account_id = 'acct-1' AND window = 'monthly'
	`).Scan(&usedPercent, &balance); err != nil {
		t.Fatalf("read monthly row: %v", err)
	}
	if usedPercent != 12.5 || balance != 87.5 {
		t.Fatalf("unexpected monthly row values used=%.1f balance=%.1f", usedPercent, balance)
	}
}

func TestReconcileRecoverableAccountStatusesUsesMonthlyLongWindow(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := accounts.NewRepository(store)
	now := time.Now().Unix()
	blockedAt := now - 600

	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO accounts (
			id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
			id_token_encrypted, last_refresh, status, blocked_at
		) VALUES ('acct-1', 'a@example.com', 'free', x'00', x'00', x'00', '2026-01-01 00:00:00', 'quota_exceeded', ?)
	`, blockedAt); err != nil {
		t.Fatalf("insert account: %v", err)
	}

	record := accounts.ProxyRecord{
		ID:                     "acct-1",
		Email:                  "a@example.com",
		PlanType:               "free",
		Status:                 "quota_exceeded",
		RoutingPolicy:          "normal",
		SecurityWorkAuthorized: true,
		BlockedAt:              sql.NullFloat64{Float64: float64(blockedAt), Valid: true},
	}
	primaryUsed := 10.0
	monthlyUsed := 10.0
	recordedAt := time.Now().UTC().Format("2006-01-02 15:04:05")
	recovered, err := ReconcileRecoverableAccountStatuses(
		ctx,
		repo,
		[]accounts.ProxyRecord{record},
		map[string]usage.Entry{
			"acct-1": {
				AccountID:   "acct-1",
				RecordedAt:  recordedAt,
				Window:      sql.NullString{String: "primary", Valid: true},
				UsedPercent: primaryUsed,
			},
		},
		nil,
		map[string]usage.Entry{
			"acct-1": {
				AccountID:   "acct-1",
				RecordedAt:  recordedAt,
				Window:      sql.NullString{String: "monthly", Valid: true},
				UsedPercent: monthlyUsed,
			},
		},
	)
	if err != nil {
		t.Fatalf("reconcile recoverable: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("expected 1 recovered account, got %d", recovered)
	}
	assertAccountStatus(t, store, "acct-1", "active", "")
}

func usageRefreshTestToken(t *testing.T) (*crypto.Encryptor, []byte) {
	t.Helper()
	encryptor, err := crypto.NewEncryptor(filepath.Join(t.TempDir(), "encryption.key"))
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	accessToken, err := encryptor.Encrypt("access")
	if err != nil {
		t.Fatalf("encrypt access: %v", err)
	}
	return encryptor, accessToken
}

func testUsageRefreshJWT(payload map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body, _ := json.Marshal(payload)
	return header + "." + base64.RawURLEncoding.EncodeToString(body) + ".sig"
}

func insertUsageRefreshAccount(t *testing.T, store *dbpkg.Store, accountID string, accessToken []byte) {
	t.Helper()
	if _, err := store.DB().Exec(`
		INSERT INTO accounts (
			id, email, plan_type, access_token_encrypted, refresh_token_encrypted,
			id_token_encrypted, last_refresh, status
		) VALUES (?, 'a@example.com', 'pro', ?, x'00', x'00', '2026-01-01 00:00:00', 'active')
	`, accountID, accessToken); err != nil {
		t.Fatalf("insert account: %v", err)
	}
}

func assertAccountStatus(t *testing.T, store *dbpkg.Store, accountID, expectedStatus, expectedReason string) {
	t.Helper()
	var status string
	var reason sql.NullString
	if err := store.DB().QueryRow(`
		SELECT status, deactivation_reason FROM accounts WHERE id = ?
	`, accountID).Scan(&status, &reason); err != nil {
		t.Fatalf("read account status: %v", err)
	}
	if status != expectedStatus {
		t.Fatalf("expected status %s, got %s", expectedStatus, status)
	}
	if expectedReason == "" {
		if reason.Valid {
			t.Fatalf("expected no reason, got %q", reason.String)
		}
		return
	}
	if !reason.Valid || reason.String != expectedReason {
		t.Fatalf("expected reason %q, got %#v", expectedReason, reason)
	}
}
