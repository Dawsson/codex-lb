package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/crypto"
	dbpkg "github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/requestlogs"
	"github.com/soju06/codex-lb/internal/settings"
	"github.com/soju06/codex-lb/internal/stickysessions"
	"github.com/soju06/codex-lb/internal/upstream"
	"github.com/soju06/codex-lb/internal/usage"
)

func newWarmupTestStore(t *testing.T) (*dbpkg.Store, *crypto.Encryptor) {
	t.Helper()
	dir := t.TempDir()
	store, err := dbpkg.Open(config.Config{DatabasePath: filepath.Join(dir, "store.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.RunMigrations("../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	encryptor, err := crypto.NewEncryptor(filepath.Join(dir, "encryption.key"))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	return store, encryptor
}

func newWarmupTestService(t *testing.T, store *dbpkg.Store, encryptor *crypto.Encryptor) *Service {
	t.Helper()
	accountRepo := accounts.NewRepository(store)
	settingsRepo := settings.NewRepository(store, encryptor)
	apiKeysRepo := apikeys.NewRepository(store)
	modelRegistry := NewModelRegistry(0)
	loadBalancer := NewLoadBalancer(accountRepo, settingsRepo, usage.NewRepository(store), encryptor, modelRegistry, NewAdditionalQuotaRegistry())
	service := NewService(loadBalancer, settingsRepo, requestlogs.NewRepository(store), apiKeysRepo, stickysessions.NewRepository(store), modelRegistry, "")
	service.warmupSubmitter = func(ctx context.Context, opts upstream.StreamOptions) (map[string]any, error) {
		return map[string]any{
			"id": "resp_warmup",
			"usage": map[string]any{
				"input_tokens":  int64(3),
				"output_tokens": int64(1),
			},
		}, nil
	}
	return service
}

func insertWarmupAccount(t *testing.T, store *dbpkg.Store, encryptor *crypto.Encryptor, accountID string, usedPercent float64) {
	t.Helper()
	ctx := context.Background()
	access, err := encryptor.Encrypt("access-" + accountID)
	if err != nil {
		t.Fatalf("encrypt access: %v", err)
	}
	refresh, err := encryptor.Encrypt("refresh-" + accountID)
	if err != nil {
		t.Fatalf("encrypt refresh: %v", err)
	}
	idToken, err := encryptor.Encrypt("id-" + accountID)
	if err != nil {
		t.Fatalf("encrypt id token: %v", err)
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO accounts (
			id, chatgpt_account_id, email, plan_type, routing_policy,
			access_token_encrypted, refresh_token_encrypted, id_token_encrypted,
			last_refresh, status
		) VALUES (?, ?, ?, 'plus', 'normal', ?, ?, ?, ?, 'active')
	`, accountID, "chatgpt-"+accountID, accountID+"@example.com", access, refresh, idToken, now); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO usage_history (account_id, recorded_at, window, used_percent, reset_at, window_minutes)
		VALUES (?, ?, 'primary', ?, 1893456000, 300)
	`, accountID, now, usedPercent); err != nil {
		t.Fatalf("insert usage: %v", err)
	}
}

func TestWarmupSubmitsEligibleAccount(t *testing.T) {
	ctx := context.Background()
	store, encryptor := newWarmupTestStore(t)
	insertWarmupAccount(t, store, encryptor, "acct-1", 0)
	service := newWarmupTestService(t, store, encryptor)

	response, envelope, status, err := service.Warmup(ctx, httptest.NewRequest(http.MethodPost, "/v1/warmup", nil), nil, "normal")
	if err != nil {
		t.Fatalf("warmup: %v", err)
	}
	if envelope != nil || status != 200 {
		t.Fatalf("expected success, status=%d envelope=%#v", status, envelope)
	}
	if response.TotalAccounts != 1 || len(response.Submitted) != 1 {
		t.Fatalf("expected one submitted account, got %#v", response)
	}
	if response.Submitted[0].AccountID != "acct-1" || response.Submitted[0].RequestID != "resp_warmup" {
		t.Fatalf("unexpected submitted response: %#v", response.Submitted[0])
	}
	var logCount int
	if err := store.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM request_logs WHERE account_id = 'acct-1' AND request_kind = 'normal' AND status = 'success'
	`).Scan(&logCount); err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if logCount != 1 {
		t.Fatalf("expected one request log, got %d", logCount)
	}
}

func TestLoadBalancerSelectAccountExcludesFailedAccounts(t *testing.T) {
	ctx := context.Background()
	store, encryptor := newWarmupTestStore(t)
	insertWarmupAccount(t, store, encryptor, "acct-low", 0)
	insertWarmupAccount(t, store, encryptor, "acct-high", 90)
	accountRepo := accounts.NewRepository(store)
	settingsRepo := settings.NewRepository(store, encryptor)
	lb := NewLoadBalancer(accountRepo, settingsRepo, usage.NewRepository(store), encryptor, NewModelRegistry(0), NewAdditionalQuotaRegistry())

	selection, err := lb.SelectAccount(ctx, SelectAccountParams{
		RoutingStrategy:   RoutingStrategyUsageWeighted,
		ExcludeAccountIDs: map[string]struct{}{"acct-low": {}},
	})
	if err != nil {
		t.Fatalf("select account: %v", err)
	}
	if selection.Account == nil {
		t.Fatalf("expected fallback account after exclusion: %#v", selection)
	}
	if selection.Account.ID != "acct-high" {
		t.Fatalf("expected excluded account to leave selection loop, got %s", selection.Account.ID)
	}
}

func TestWarmupSkipsIneligibleNormalAndRejectsStrict(t *testing.T) {
	ctx := context.Background()
	store, encryptor := newWarmupTestStore(t)
	insertWarmupAccount(t, store, encryptor, "acct-1", 10)
	service := newWarmupTestService(t, store, encryptor)

	response, envelope, status, err := service.Warmup(ctx, httptest.NewRequest(http.MethodPost, "/v1/warmup", nil), nil, "normal")
	if err != nil {
		t.Fatalf("normal warmup: %v", err)
	}
	if envelope != nil || status != 200 {
		t.Fatalf("expected normal success, status=%d envelope=%#v", status, envelope)
	}
	if len(response.Submitted) != 0 || len(response.Skipped) != 1 || response.Skipped[0].Reason != warmupSkipIneligiblePrimary {
		t.Fatalf("expected ineligible skip, got %#v", response)
	}

	_, envelope, status, err = service.Warmup(ctx, httptest.NewRequest(http.MethodPost, "/v1/warmup/strict", nil), nil, "strict")
	if err != nil {
		t.Fatalf("strict warmup: %v", err)
	}
	if envelope == nil || status != 400 {
		t.Fatalf("expected strict invalid request, status=%d envelope=%#v", status, envelope)
	}
}

func TestWarmupRejectsInvalidMode(t *testing.T) {
	store, encryptor := newWarmupTestStore(t)
	service := newWarmupTestService(t, store, encryptor)

	_, envelope, status, err := service.Warmup(context.Background(), httptest.NewRequest(http.MethodPost, "/v1/warmup/nope", nil), nil, "nope")
	if err != nil {
		t.Fatalf("warmup: %v", err)
	}
	if envelope == nil || status != 400 || envelope.Error.Code != "invalid_request_error" {
		t.Fatalf("expected invalid_request_error, status=%d envelope=%#v", status, envelope)
	}
}
