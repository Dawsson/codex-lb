package proxy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/crypto"
	dbpkg "github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/proxy"
	"github.com/soju06/codex-lb/internal/settings"
)

func newProxyTestStore(t *testing.T) *dbpkg.Store {
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
	return store
}

func TestV1ModelsLocalRequestNoAuth(t *testing.T) {
	store := newProxyTestStore(t)
	encryptor, err := crypto.NewEncryptor(filepath.Join(t.TempDir(), "encryption.key"))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	handler := proxy.NewModelsHandler(apikeys.NewRepository(store), settings.NewRepository(store, encryptor), proxy.NewModelRegistry(0))

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	rec := httptest.NewRecorder()
	handler.V1Models(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Object != "list" {
		t.Fatalf("expected object=list, got %q", body.Object)
	}
	if len(body.Data) == 0 {
		t.Fatalf("expected models in response")
	}
}

func TestV1ModelsRemoteRequestWithoutAPIKeyAuthRequired(t *testing.T) {
	store := newProxyTestStore(t)
	encryptor, err := crypto.NewEncryptor(filepath.Join(t.TempDir(), "encryption.key"))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	handler := proxy.NewModelsHandler(apikeys.NewRepository(store), settings.NewRepository(store, encryptor), proxy.NewModelRegistry(0))

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "203.0.113.5:5555"
	rec := httptest.NewRecorder()
	handler.V1Models(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCodexModelsFiltersByAllowedModels(t *testing.T) {
	store := newProxyTestStore(t)
	encryptor, err := crypto.NewEncryptor(filepath.Join(t.TempDir(), "encryption.key"))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	settingsRepo := settings.NewRepository(store, encryptor)
	current, err := settingsRepo.Get(context.Background())
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	enabled := true
	if _, err := settingsRepo.Update(context.Background(), current, settings.UpdateRequest{APIKeyAuthEnabled: &enabled}); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	apiKeysRepo := apikeys.NewRepository(store)
	_, plainKey, err := apiKeysRepo.Create(context.Background(), apikeys.CreateInput{
		Name:          "test-key",
		AllowedModels: []string{"gpt-5.5"},
		TrafficClass:  "foreground",
	})
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	handler := proxy.NewModelsHandler(apiKeysRepo, settingsRepo, proxy.NewModelRegistry(0))

	req := httptest.NewRequest(http.MethodGet, "/backend-api/codex/models", nil)
	req.RemoteAddr = "203.0.113.5:5555"
	req.Header.Set("Authorization", "Bearer "+plainKey)
	rec := httptest.NewRecorder()
	handler.CodexModels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Models) != 1 || body.Models[0].Slug != "gpt-5.5" {
		t.Fatalf("expected only gpt-5.5, got %#v", body.Models)
	}
}

func TestCodexModelsRejectsExpiredKey(t *testing.T) {
	store := newProxyTestStore(t)
	encryptor, err := crypto.NewEncryptor(filepath.Join(t.TempDir(), "encryption.key"))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	settingsRepo := settings.NewRepository(store, encryptor)
	current, err := settingsRepo.Get(context.Background())
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	enabled := true
	if _, err := settingsRepo.Update(context.Background(), current, settings.UpdateRequest{APIKeyAuthEnabled: &enabled}); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	apiKeysRepo := apikeys.NewRepository(store)
	expired := time.Now().UTC().Add(-time.Hour).Format("2006-01-02 15:04:05")
	_, plainKey, err := apiKeysRepo.Create(context.Background(), apikeys.CreateInput{
		Name:         "expired-key",
		TrafficClass: "foreground",
		ExpiresAt:    &expired,
	})
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	handler := proxy.NewModelsHandler(apiKeysRepo, settingsRepo, proxy.NewModelRegistry(0))

	req := httptest.NewRequest(http.MethodGet, "/backend-api/codex/models", nil)
	req.RemoteAddr = "203.0.113.5:5555"
	req.Header.Set("Authorization", "Bearer "+plainKey)
	rec := httptest.NewRecorder()
	handler.CodexModels(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestV1ModelsEnforcesAndReleasesRequestLimit(t *testing.T) {
	ctx := context.Background()
	store := newProxyTestStore(t)
	encryptor, err := crypto.NewEncryptor(filepath.Join(t.TempDir(), "encryption.key"))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	settingsRepo := settings.NewRepository(store, encryptor)
	current, err := settingsRepo.Get(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	enabled := true
	if _, err := settingsRepo.Update(ctx, current, settings.UpdateRequest{APIKeyAuthEnabled: &enabled}); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	apiKeysRepo := apikeys.NewRepository(store)
	key, plainKey, err := apiKeysRepo.Create(ctx, apikeys.CreateInput{
		Name:         "limited-key",
		TrafficClass: "foreground",
		Limits: []apikeys.LimitInput{{
			LimitType:   "requests",
			LimitWindow: "daily",
			MaxValue:    1,
		}},
	})
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	handler := proxy.NewModelsHandler(apiKeysRepo, settingsRepo, proxy.NewModelRegistry(0))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "203.0.113.5:5555"
	req.Header.Set("Authorization", "Bearer "+plainKey)
	rec := httptest.NewRecorder()
	handler.V1Models(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	loaded, err := apiKeysRepo.GetByID(ctx, key.ID)
	if err != nil {
		t.Fatalf("reload api key: %v", err)
	}
	if loaded == nil || len(loaded.Limits) != 1 {
		t.Fatalf("expected loaded limit, got %#v", loaded)
	}
	if loaded.Limits[0].CurrentValue != 0 {
		t.Fatalf("expected model-list reservation to be released, got current=%d", loaded.Limits[0].CurrentValue)
	}
}

func TestV1ModelsRejectsExceededRequestLimit(t *testing.T) {
	ctx := context.Background()
	store := newProxyTestStore(t)
	encryptor, err := crypto.NewEncryptor(filepath.Join(t.TempDir(), "encryption.key"))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	settingsRepo := settings.NewRepository(store, encryptor)
	current, err := settingsRepo.Get(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	enabled := true
	if _, err := settingsRepo.Update(ctx, current, settings.UpdateRequest{APIKeyAuthEnabled: &enabled}); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	apiKeysRepo := apikeys.NewRepository(store)
	key, plainKey, err := apiKeysRepo.Create(ctx, apikeys.CreateInput{
		Name:         "exhausted-key",
		TrafficClass: "foreground",
		Limits: []apikeys.LimitInput{{
			LimitType:   "requests",
			LimitWindow: "daily",
			MaxValue:    1,
		}},
	})
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `UPDATE api_key_limits SET current_value = 1 WHERE api_key_id = ?`, key.ID); err != nil {
		t.Fatalf("exhaust api key limit: %v", err)
	}

	handler := proxy.NewModelsHandler(apiKeysRepo, settingsRepo, proxy.NewModelRegistry(0))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "203.0.113.5:5555"
	req.Header.Set("Authorization", "Bearer "+plainKey)
	rec := httptest.NewRecorder()
	handler.V1Models(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", rec.Code, rec.Body.String())
	}
}
