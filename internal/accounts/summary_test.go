package accounts_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/audit"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/crypto"
	dbpkg "github.com/soju06/codex-lb/internal/db"
)

func newSummaryTestStore(t *testing.T) *dbpkg.Store {
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

func TestSummariesIncludePythonMapperSideData(t *testing.T) {
	ctx := context.Background()
	store := newSummaryTestStore(t)
	encryptor, err := crypto.NewEncryptor(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("create encryptor: %v", err)
	}
	access, _ := encryptor.Encrypt(testJWT(1_893_456_000, map[string]any{"email": "dupe@example.com"}))
	refresh, _ := encryptor.Encrypt("refresh-token")
	idToken, _ := encryptor.Encrypt(testJWT(1_893_456_000, map[string]any{"email": "dupe@example.com", "chatgpt_account_id": "chatgpt-1"}))

	insertSummaryAccount(t, store, "acct-1", "dupe@example.com", "chatgpt-1", sql.NullString{String: "ws-1", Valid: true}, "free", access, refresh, idToken)
	insertSummaryAccount(t, store, "acct-2", "dupe@example.com", "chatgpt-1", sql.NullString{String: "ws-1", Valid: true}, "plus", []byte("x"), []byte("x"), []byte("x"))
	insertSummaryAccount(t, store, "acct-3", "unknown@example.com", "chatgpt-1", sql.NullString{}, "plus", []byte("x"), []byte("x"), []byte("x"))

	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO usage_history (
			account_id, recorded_at, window, used_percent, reset_at, window_minutes,
			credits_has, credits_unlimited, credits_balance
		) VALUES
		  ('acct-1', '2026-01-01 00:00:00', 'primary', 25, 1893456000, 10080, 1, 0, 850),
		  ('acct-2', '2026-01-01 00:00:00', 'primary', 10, 1893456000, 300, NULL, NULL, NULL),
		  ('acct-2', '2026-01-01 00:00:00', 'secondary', 20, 1894056000, 10080, NULL, NULL, NULL)
	`); err != nil {
		t.Fatalf("insert usage: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO request_logs (
			account_id, request_id, request_kind, requested_at, model, status,
			input_tokens, output_tokens, reasoning_tokens, cached_input_tokens, cost_usd
		) VALUES
		  ('acct-1', 'req-1', 'normal', '2030-01-01 01:00:00', 'gpt-5.5', 'success', 10, 5, NULL, 99, 0.25),
		  ('acct-1', 'req-2', 'normal', '2030-01-01 02:00:00', 'gpt-5.5', 'error', 3, NULL, 7, 2, 0.50),
		  ('acct-1', 'req-warm', 'warmup', '2030-01-01 03:00:00', 'gpt-5.5', 'success', 100, 100, NULL, 100, 1.00)
	`); err != nil {
		t.Fatalf("insert request logs: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO additional_usage_history (
			account_id, limit_name, metered_feature, window, used_percent,
			reset_at, window_minutes, recorded_at, quota_key
		) VALUES
		  ('acct-1', 'Spark', 'responses', 'primary', 40, 1893456000, 300, '2026-01-01 00:00:00', 'spark'),
		  ('acct-1', 'Spark', 'responses', 'secondary', 60, 1894056000, 10080, '2026-01-01 00:00:00', 'spark')
	`); err != nil {
		t.Fatalf("insert additional quota: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO account_limit_warmups (
			account_id, window, reset_at, status, model, attempted_at,
			completed_at, error_code, error_message
		) VALUES (
			'acct-1', 'secondary', 1894056000, 'success', 'gpt-5.5',
			'2026-01-01 04:00:00', '2026-01-01 04:00:03', NULL, NULL
		)
	`); err != nil {
		t.Fatalf("insert limit warmup: %v", err)
	}

	handler := accounts.NewHandler(accounts.NewRepository(store), encryptor, audit.NewRepository(store))
	summaries, err := handler.Summaries(httptest.NewRequest("GET", "/api/accounts", nil))
	if err != nil {
		t.Fatalf("summaries: %v", err)
	}
	byID := map[string]accounts.AccountSummary{}
	for _, summary := range summaries {
		byID[summary.AccountID] = summary
	}
	account := byID["acct-1"]
	if !account.IsEmailDuplicate {
		t.Fatal("expected duplicate account flag")
	}
	if account.Usage == nil || account.Usage.PrimaryRemainingPercent != nil || account.Usage.SecondaryRemainingPercent == nil || *account.Usage.SecondaryRemainingPercent != 75 {
		t.Fatalf("expected weekly-only primary to map to secondary, got %#v", account.Usage)
	}
	if account.RequestUsage == nil || account.RequestUsage.RequestCount != 2 || account.RequestUsage.TotalTokens != 25 || account.RequestUsage.CachedInputTokens != 12 || account.RequestUsage.Errors != 1 {
		t.Fatalf("unexpected request usage: %#v", account.RequestUsage)
	}
	if len(account.AdditionalQuotas) != 1 || account.AdditionalQuotas[0].PrimaryWindow == nil || account.AdditionalQuotas[0].SecondaryWindow == nil {
		t.Fatalf("expected grouped additional quota windows, got %#v", account.AdditionalQuotas)
	}
	if account.LimitWarmup == nil || account.LimitWarmup.Window != "secondary" || account.LimitWarmup.Status != "success" {
		t.Fatalf("unexpected limit warmup: %#v", account.LimitWarmup)
	}
	if account.Auth == nil || account.Auth.Access == nil || account.Auth.Access.ExpiresAt == nil || account.Auth.Refresh == nil || account.Auth.Refresh.State == nil || *account.Auth.Refresh.State != "stored" {
		t.Fatalf("unexpected auth status: %#v", account.Auth)
	}
	if byID["acct-3"].IsEmailDuplicate {
		t.Fatal("placeholder email should not be duplicate")
	}
}

func insertSummaryAccount(t *testing.T, store *dbpkg.Store, id, email, chatgptID string, workspaceID sql.NullString, plan string, access, refresh, idToken []byte) {
	t.Helper()
	_, err := store.DB().Exec(`
		INSERT INTO accounts (
			id, chatgpt_account_id, email, workspace_id, plan_type,
			access_token_encrypted, refresh_token_encrypted, id_token_encrypted,
			last_refresh, status, routing_policy
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, '2026-01-01 00:00:00', 'active', 'normal')
	`, id, chatgptID, email, workspaceID, plan, access, refresh, idToken)
	if err != nil {
		t.Fatalf("insert account %s: %v", id, err)
	}
}

func testJWT(exp int64, claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	claims["exp"] = exp
	parts := make([]string, 0, len(claims))
	for key, value := range claims {
		switch typed := value.(type) {
		case string:
			parts = append(parts, fmt.Sprintf("%q:%q", key, typed))
		default:
			parts = append(parts, fmt.Sprintf("%q:%v", key, typed))
		}
	}
	payload := base64.RawURLEncoding.EncodeToString([]byte("{" + strings.Join(parts, ",") + "}"))
	return header + "." + payload + "."
}
