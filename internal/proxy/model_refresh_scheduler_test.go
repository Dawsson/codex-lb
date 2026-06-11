package proxy

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/crypto"
	dbpkg "github.com/soju06/codex-lb/internal/db"
)

type fakeModelFetcher struct {
	calls []ModelFetchAccount
}

func (f *fakeModelFetcher) FetchModelsForPlan(_ context.Context, account ModelFetchAccount) ([]UpstreamModel, error) {
	f.calls = append(f.calls, account)
	return []UpstreamModel{{
		Slug:             "live-" + account.PlanType,
		DisplayName:      "Live " + account.PlanType,
		Description:      "Live " + account.PlanType,
		AvailableInPlans: map[string]struct{}{account.PlanType: {}},
		SupportedInAPI:   true,
	}}, nil
}

func newModelRefreshTestStore(t *testing.T) (*dbpkg.Store, config.Config) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{
		DatabasePath:               filepath.Join(dir, "store.db"),
		EncryptionKeyPath:          filepath.Join(dir, "encryption.key"),
		ModelRefreshEnabled:        true,
		ModelRefreshInterval:       time.Hour,
		ModelRegistryClientVersion: "0.101.0",
		UpstreamBaseURL:            "https://chatgpt.com/backend-api",
	}
	store, err := dbpkg.Open(cfg)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.RunMigrations("../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return store, cfg
}

func insertModelRefreshAccount(t *testing.T, store *dbpkg.Store, cfg config.Config, id, plan, status, token string) {
	t.Helper()
	encryptor, err := crypto.NewEncryptor(cfg.EncryptionKeyPath)
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	accessToken, err := encryptor.Encrypt(token)
	if err != nil {
		t.Fatalf("encrypt access token: %v", err)
	}
	refreshToken, err := encryptor.Encrypt("refresh-" + id)
	if err != nil {
		t.Fatalf("encrypt refresh token: %v", err)
	}
	idToken, err := encryptor.Encrypt("id-" + id)
	if err != nil {
		t.Fatalf("encrypt id token: %v", err)
	}
	_, err = store.DB().Exec(`
		INSERT INTO accounts (
			id, chatgpt_account_id, email, plan_type, access_token_encrypted,
			refresh_token_encrypted, id_token_encrypted, last_refresh, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, '2026-01-01 00:00:00', ?)
	`, id, "chatgpt-"+id, id+"@example.com", plan, accessToken, refreshToken, idToken, status)
	if err != nil {
		t.Fatalf("insert account %s: %v", id, err)
	}
}

func TestModelRegistryUpdateMergesPlanSnapshots(t *testing.T) {
	registry := NewModelRegistry(time.Hour)
	registry.Update(map[string][]UpstreamModel{
		"plus": {{Slug: "plus-model", AvailableInPlans: map[string]struct{}{"plus": {}}}},
		"pro":  {{Slug: "pro-model", AvailableInPlans: map[string]struct{}{"pro": {}}}},
	})
	registry.Update(map[string][]UpstreamModel{
		"plus": {{Slug: "new-plus-model", AvailableInPlans: map[string]struct{}{"plus": {}}}},
	})
	models := registry.GetModelsWithFallback()
	if _, ok := models["new-plus-model"]; !ok {
		t.Fatalf("expected refreshed plus model, got %#v", models)
	}
	if _, ok := models["pro-model"]; !ok {
		t.Fatalf("expected stale pro model to be preserved, got %#v", models)
	}
	if _, ok := models["plus-model"]; ok {
		t.Fatalf("expected old plus model to be replaced, got %#v", models)
	}
}

func TestModelRefreshSchedulerRunsInitialTick(t *testing.T) {
	ctx := context.Background()
	store, cfg := newModelRefreshTestStore(t)
	insertModelRefreshAccount(t, store, cfg, "acct-plus", "plus", AccountStatusActive, "token-plus")
	insertModelRefreshAccount(t, store, cfg, "acct-pro", "pro", AccountStatusActive, "token-pro")
	insertModelRefreshAccount(t, store, cfg, "acct-paused", "plus", AccountStatusPaused, "token-paused")
	registry := NewModelRegistry(time.Hour)
	fetcher := &fakeModelFetcher{}
	scheduler := NewModelRefreshScheduler(
		store,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		cfg,
		registry,
		fetcher,
		"test-leader",
	)
	scheduler.Start(ctx)
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = scheduler.Stop(stopCtx)
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if snapshot := registry.GetSnapshot(); snapshot != nil && len(snapshot.PlanModels) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	snapshot := registry.GetSnapshot()
	if snapshot == nil {
		t.Fatal("expected model registry snapshot")
	}
	if _, ok := snapshot.Models["live-plus"]; !ok {
		t.Fatalf("expected plus model in snapshot: %#v", snapshot.Models)
	}
	if _, ok := snapshot.Models["live-pro"]; !ok {
		t.Fatalf("expected pro model in snapshot: %#v", snapshot.Models)
	}
	if len(fetcher.calls) != 2 {
		t.Fatalf("expected two active plan fetches, got %d", len(fetcher.calls))
	}
	for _, call := range fetcher.calls {
		if call.ID == "acct-paused" {
			t.Fatal("paused account was used for model refresh")
		}
		if call.AccessToken == "" || call.ChatGPTAccountID == nil {
			t.Fatalf("expected decrypted token and chatgpt account id, got %#v", call)
		}
	}
}
