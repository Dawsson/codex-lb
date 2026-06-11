package limitwarmup_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/crypto"
	"github.com/soju06/codex-lb/internal/limitwarmup"
)

func TestStreamingSenderSendsWarmupRequestAndParsesCompletion(t *testing.T) {
	var gotAuth string
	var gotAccountID string
	var gotWarmupHeader string
	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotAccountID = r.Header.Get("chatgpt-account-id")
		gotWarmupHeader = r.Header.Get(limitwarmup.Header)
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.completed\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"usage":{"input_tokens":3,"output_tokens":1}}}` + "\n\n"))
	}))
	defer server.Close()

	encryptor, err := crypto.NewEncryptor(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	encryptedToken, err := encryptor.Encrypt("access-token")
	if err != nil {
		t.Fatalf("encrypt token: %v", err)
	}
	sender := limitwarmup.NewStreamingSender(encryptor, config.Config{UpstreamBaseURL: server.URL})

	result, err := sender.Send(context.Background(), accounts.Account{
		ID:                   "acct-1",
		ChatGPTAccountID:     sql.NullString{String: "chatgpt-1", Valid: true},
		Status:               "active",
		AccessTokenEncrypted: encryptedToken,
	}, limitwarmup.SendParams{
		Model:        "gpt-5.5",
		Prompt:       "ping",
		Instructions: "Reply with OK only.",
		RequestID:    "req-warmup",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !result.Success || result.InputTokens == nil || *result.InputTokens != 3 || result.OutputTokens == nil || *result.OutputTokens != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if gotAuth != "Bearer access-token" {
		t.Fatalf("unexpected authorization header %q", gotAuth)
	}
	if gotAccountID != "chatgpt-1" {
		t.Fatalf("unexpected chatgpt account id %q", gotAccountID)
	}
	if gotWarmupHeader != "1" {
		t.Fatalf("unexpected warmup header %q", gotWarmupHeader)
	}
	if gotPayload["model"] != "gpt-5.5" || gotPayload["input"] != "ping" || gotPayload["stream"] != true || gotPayload["store"] != false {
		t.Fatalf("unexpected payload: %#v", gotPayload)
	}
	if gotPayload["max_output_tokens"].(float64) != 4 {
		t.Fatalf("expected max_output_tokens=4, got %#v", gotPayload["max_output_tokens"])
	}
}

func TestStreamingSenderParsesTerminalError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.failed\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"no quota"},"usage":{"input_tokens":2,"output_tokens":0}}}` + "\n\n"))
	}))
	defer server.Close()

	encryptor, err := crypto.NewEncryptor(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	encryptedToken, err := encryptor.Encrypt("access-token")
	if err != nil {
		t.Fatalf("encrypt token: %v", err)
	}
	sender := limitwarmup.NewStreamingSender(encryptor, config.Config{UpstreamBaseURL: server.URL})
	result, err := sender.Send(context.Background(), accounts.Account{
		ID:                   "acct-1",
		Status:               "active",
		AccessTokenEncrypted: encryptedToken,
	}, limitwarmup.SendParams{Model: "gpt-5.5", Prompt: "ping", RequestID: "req-warmup"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if result.Success || result.ErrorCode != "rate_limit_exceeded" || result.ErrorMessage != "no quota" {
		t.Fatalf("unexpected terminal error result: %#v", result)
	}
}
