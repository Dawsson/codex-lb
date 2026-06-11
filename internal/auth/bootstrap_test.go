package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/crypto"
	dbpkg "github.com/soju06/codex-lb/internal/db"
)

func newAuthTestHandler(t *testing.T, manualToken string) (Handler, Repository, BootstrapService, *scs.SessionManager) {
	t.Helper()
	dir := t.TempDir()
	store, err := dbpkg.Open(config.Config{DatabasePath: filepath.Join(dir, "store.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.RunMigrations("../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	encryptor, err := crypto.NewEncryptor(filepath.Join(dir, "encryption.key"))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	repo := NewRepository(store)
	sessions := scs.New()
	sessions.Lifetime = 12 * time.Hour
	bootstrap := NewBootstrapService(repo, encryptor, manualToken, slog.Default())
	handler := NewHandler(repo, sessions, false, encryptor, bootstrap)
	return handler, repo, bootstrap, sessions
}

func TestRemoteSessionRequiresBootstrapWhenPasswordMissing(t *testing.T) {
	handler, _, bootstrap, sessions := newAuthTestHandler(t, "")
	token, err := bootstrap.EnsureAutoToken(context.Background())
	if err != nil {
		t.Fatalf("ensure token: %v", err)
	}
	if token == "" {
		t.Fatalf("expected generated bootstrap token")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	req.RemoteAddr = "203.0.113.10:5555"
	req.Host = "clb.example.test"
	rec := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(handler.Session)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body SessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Authenticated || !body.BootstrapRequired || !body.BootstrapTokenConfigured {
		t.Fatalf("unexpected session response: %#v", body)
	}
}

func TestLocalSessionBypassesBootstrapWhenPasswordMissing(t *testing.T) {
	handler, _, _, sessions := newAuthTestHandler(t, "")
	req := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Host = "localhost:2455"
	rec := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(handler.Session)).ServeHTTP(rec, req)

	var body SessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Authenticated || body.BootstrapRequired {
		t.Fatalf("unexpected local session response: %#v", body)
	}
}

func TestRemoteSetupRequiresValidBootstrapToken(t *testing.T) {
	handler, repo, bootstrap, sessions := newAuthTestHandler(t, "")
	token, err := bootstrap.EnsureAutoToken(context.Background())
	if err != nil {
		t.Fatalf("ensure token: %v", err)
	}

	badReq := httptest.NewRequest(http.MethodPost, "/api/auth/password/setup", strings.NewReader(`{"password":"password123","bootstrapToken":"wrong"}`))
	badReq.RemoteAddr = "203.0.113.10:5555"
	badReq.Host = "clb.example.test"
	badRec := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(handler.SetupPassword)).ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad token, got %d: %s", badRec.Code, badRec.Body.String())
	}

	goodReq := httptest.NewRequest(http.MethodPost, "/api/auth/password/setup", strings.NewReader(`{"password":"password123","bootstrapToken":"`+token+`"}`))
	goodReq.RemoteAddr = "203.0.113.10:5555"
	goodReq.Host = "clb.example.test"
	goodRec := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(handler.SetupPassword)).ServeHTTP(goodRec, goodReq)
	if goodRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid token, got %d: %s", goodRec.Code, goodRec.Body.String())
	}
	settings, err := repo.Settings(context.Background())
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if !settings.PasswordHash.Valid || len(settings.BootstrapTokenHash) != 0 {
		t.Fatalf("expected password set and bootstrap cleared: %#v", settings)
	}
}

func TestLocalSetupBypassesBootstrapToken(t *testing.T) {
	handler, repo, _, sessions := newAuthTestHandler(t, "")
	req := httptest.NewRequest(http.MethodPost, "/api/auth/password/setup", strings.NewReader(`{"password":"password123"}`))
	req.RemoteAddr = "127.0.0.1:5555"
	req.Host = "localhost:2455"
	rec := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(handler.SetupPassword)).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for local setup, got %d: %s", rec.Code, rec.Body.String())
	}
	settings, err := repo.Settings(context.Background())
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if !settings.PasswordHash.Valid {
		t.Fatalf("expected password hash")
	}
}

func TestEnsureAutoTokenReusesStoredTokenAndRemoveRegenerates(t *testing.T) {
	handler, repo, bootstrap, sessions := newAuthTestHandler(t, "")
	first, err := bootstrap.EnsureAutoToken(context.Background())
	if err != nil {
		t.Fatalf("ensure first token: %v", err)
	}
	second, err := bootstrap.EnsureAutoToken(context.Background())
	if err != nil {
		t.Fatalf("ensure second token: %v", err)
	}
	if first == "" || second != first {
		t.Fatalf("expected stored token reuse, first=%q second=%q", first, second)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/password/setup", strings.NewReader(`{"password":"password123","bootstrapToken":"`+first+`"}`))
	req.RemoteAddr = "203.0.113.10:5555"
	req.Host = "clb.example.test"
	rec := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(handler.SetupPassword)).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup failed: %d %s", rec.Code, rec.Body.String())
	}

	removeReq := httptest.NewRequest(http.MethodDelete, "/api/auth/password", strings.NewReader(`{"password":"password123"}`))
	removeReq.RemoteAddr = "203.0.113.10:5555"
	removeReq.Host = "clb.example.test"
	removeRec := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessions.Put(r.Context(), sessionAuthenticatedKey, true)
		handler.RemovePassword(w, r)
	})).ServeHTTP(removeRec, removeReq)
	if removeRec.Code != http.StatusOK {
		t.Fatalf("remove failed: %d %s", removeRec.Code, removeRec.Body.String())
	}
	settings, err := repo.Settings(context.Background())
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if settings.PasswordHash.Valid || len(settings.BootstrapTokenHash) == 0 || len(settings.BootstrapTokenEncrypted) == 0 {
		t.Fatalf("expected password cleared and bootstrap token stored: %#v", settings)
	}
}
