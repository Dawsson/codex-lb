package accounts

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/soju06/codex-lb/internal/audit"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/crypto"
	dbpkg "github.com/soju06/codex-lb/internal/db"
)

func TestSendProbeUsesConfiguredUpstreamAndPythonParityPayload(t *testing.T) {
	encryptor, encryptedToken := newProbeEncryptor(t, "access-token")
	var seenPath string
	var seenAuth string
	var seenAccept string
	var seenAccountID string
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenAuth = r.Header.Get("Authorization")
		seenAccept = r.Header.Get("Accept")
		seenAccountID = r.Header.Get("chatgpt-account-id")
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)

	handler := NewHandler(Repository{}, encryptor, audit.Repository{}).WithProbeClient(server.URL, server.Client())
	status := handler.sendProbe(httptest.NewRequest(http.MethodPost, "/probe", nil), ProxyRecord{
		ID:                   "acct-1",
		ChatGPTAccountID:     sql.NullString{String: "chatgpt-account", Valid: true},
		AccessTokenEncrypted: encryptedToken,
	}, "gpt-test")

	if status != http.StatusAccepted {
		t.Fatalf("expected accepted status, got %d", status)
	}
	if seenPath != "/backend-api/codex/responses" {
		t.Fatalf("expected backend-api codex responses path, got %q", seenPath)
	}
	if seenAuth != "Bearer access-token" {
		t.Fatalf("unexpected auth header %q", seenAuth)
	}
	if seenAccept != "text/event-stream" {
		t.Fatalf("unexpected accept header %q", seenAccept)
	}
	if seenAccountID != "chatgpt-account" {
		t.Fatalf("unexpected account header %q", seenAccountID)
	}
	if payload["model"] != "gpt-test" || payload["instructions"] != "Respond with a single dot." {
		t.Fatalf("unexpected probe payload %#v", payload)
	}
	if payload["stream"] != true || payload["store"] != false || payload["max_output_tokens"] != float64(1) {
		t.Fatalf("expected minimal streaming probe payload, got %#v", payload)
	}
}

func TestSendProbeSkipsSyntheticAccountHeader(t *testing.T) {
	encryptor, encryptedToken := newProbeEncryptor(t, "access-token")
	var seenAccountID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAccountID = r.Header.Get("chatgpt-account-id")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	handler := NewHandler(Repository{}, encryptor, audit.Repository{}).WithProbeClient(server.URL+"/backend-api", server.Client())
	status := handler.sendProbe(httptest.NewRequest(http.MethodPost, "/probe", nil), ProxyRecord{
		ID:                   "acct-1",
		ChatGPTAccountID:     sql.NullString{String: "email_user@example.com", Valid: true},
		AccessTokenEncrypted: encryptedToken,
	}, "gpt-test")

	if status != http.StatusOK {
		t.Fatalf("expected ok status, got %d", status)
	}
	if seenAccountID != "" {
		t.Fatalf("expected synthetic account header to be skipped, got %q", seenAccountID)
	}
}

func TestProbeRefreshesCredentialsBeforeSending(t *testing.T) {
	ctx := context.Background()
	store := newProbeTestStore(t)
	encryptor, oldAccess := newProbeEncryptor(t, "old-access")
	newAccess, err := encryptor.Encrypt("new-access")
	if err != nil {
		t.Fatalf("encrypt new access: %v", err)
	}
	refreshToken, err := encryptor.Encrypt("refresh-token")
	if err != nil {
		t.Fatalf("encrypt refresh: %v", err)
	}
	idToken, err := encryptor.Encrypt("id-token")
	if err != nil {
		t.Fatalf("encrypt id: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO accounts (
			id, chatgpt_account_id, email, plan_type, access_token_encrypted,
			refresh_token_encrypted, id_token_encrypted, last_refresh, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "acct-1", "chatgpt-account", "a@example.com", "plus", oldAccess, refreshToken, idToken, "2026-01-01 00:00:00", "active"); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	var seenAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	repo := NewRepository(store)
	handler := NewHandler(repo, encryptor, audit.NewRepository(store)).
		WithProbeClient(server.URL, server.Client()).
		WithProbeAuthRefresher(probeTokenRefresher{repo: repo, access: newAccess, refresh: refreshToken, id: idToken})
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/acct-1/probe", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("accountID", "acct-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	recorder := httptest.NewRecorder()

	handler.Probe(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if seenAuth != "Bearer new-access" {
		t.Fatalf("expected refreshed access token, got %q", seenAuth)
	}
}

type probeTokenRefresher struct {
	repo    Repository
	access  []byte
	refresh []byte
	id      []byte
}

func (f probeTokenRefresher) Refresh(ctx context.Context, account Account) error {
	_, err := f.repo.UpdateTokens(ctx, TokenUpdate{
		AccountID:             account.ID,
		AccessTokenEncrypted:  f.access,
		RefreshTokenEncrypted: f.refresh,
		IDTokenEncrypted:      f.id,
		LastRefresh:           "2026-06-11 00:00:00",
		PlanType:              account.PlanType,
		Email:                 account.Email,
		ChatGPTAccountID:      account.ChatGPTAccountID,
		WorkspaceID:           account.WorkspaceID,
		WorkspaceLabel:        account.WorkspaceLabel,
		SeatType:              account.SeatType,
	})
	return err
}

func newProbeTestStore(t *testing.T) *dbpkg.Store {
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

func newProbeEncryptor(t *testing.T, token string) (*crypto.Encryptor, []byte) {
	t.Helper()
	encryptor, err := crypto.NewEncryptor(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("create encryptor: %v", err)
	}
	encrypted, err := encryptor.Encrypt(token)
	if err != nil {
		t.Fatalf("encrypt token: %v", err)
	}
	return encryptor, encrypted
}
