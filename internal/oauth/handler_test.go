package oauth_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/crypto"
	dbpkg "github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/oauth"
)

func newOAuthTestService(t *testing.T) *oauth.Service {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "store.db")
	store, err := dbpkg.Open(config.Config{DatabasePath: dbPath})
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

	cfg := config.Config{
		OAuthAuthBaseURL:    "https://auth.example.com",
		OAuthClientID:       "client-id",
		OAuthOriginator:     "codex_chatgpt_desktop",
		OAuthScope:          "openid profile email",
		OAuthTimeoutSeconds: 5,
		OAuthRedirectURI:    "http://localhost:0/auth/callback",
		OAuthCallbackHost:   "127.0.0.1",
		OAuthCallbackPort:   0,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	return oauth.NewService(cfg, accounts.NewRepository(store), encryptor, nil, logger)
}

func TestOAuthStatusHandlerDefaultsToPending(t *testing.T) {
	service := newOAuthTestService(t)
	handler := oauth.NewHandler(service)

	req := httptest.NewRequest(http.MethodGet, "/api/oauth/status", nil)
	rec := httptest.NewRecorder()
	handler.Status(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "pending" {
		t.Fatalf("expected pending status, got %v", body)
	}
}

func TestOAuthCompleteHandlerWithoutFlow(t *testing.T) {
	service := newOAuthTestService(t)
	handler := oauth.NewHandler(service)

	req := httptest.NewRequest(http.MethodPost, "/api/oauth/complete", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	handler.Complete(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "pending" {
		t.Fatalf("expected pending status with no active flow, got %v", body)
	}
}

func TestOAuthManualCallbackInvalidRequest(t *testing.T) {
	service := newOAuthTestService(t)
	handler := oauth.NewHandler(service)

	req := httptest.NewRequest(http.MethodPost, "/api/oauth/manual-callback", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	handler.ManualCallback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestOAuthManualCallbackUnknownState(t *testing.T) {
	service := newOAuthTestService(t)
	handler := oauth.NewHandler(service)

	body, _ := json.Marshal(map[string]string{
		"callbackUrl": "http://localhost:1455/auth/callback?code=abc&state=unknown",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/manual-callback", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ManualCallback(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp["status"] != "error" {
		t.Fatalf("expected error status for unknown state, got %v", resp)
	}
}
