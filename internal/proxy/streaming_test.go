package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/soju06/codex-lb/internal/db"
)

func TestParseSSEDataJSON(t *testing.T) {
	block := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"
	payload := ParseSSEDataJSON(block)
	if payload == nil {
		t.Fatal("expected payload")
	}
	if payload["type"] != "response.output_text.delta" {
		t.Fatalf("unexpected type: %v", payload["type"])
	}
}

func TestInjectSSEKeepalives(t *testing.T) {
	events := make(chan string, 2)
	events <- "data: {\"type\":\"response.created\"}\n\n"
	close(events)

	out := InjectSSEKeepalives(events, 50*time.Millisecond, SSEKeepaliveFrame)
	var frames []string
	for frame := range out {
		frames = append(frames, frame)
	}
	if len(frames) == 0 {
		t.Fatal("expected at least one frame")
	}
	if !strings.Contains(frames[0], "response.created") {
		t.Fatalf("expected data frame first, got %q", frames[0])
	}
}

func TestStreamChatChunksDeltaAndDone(t *testing.T) {
	events := make(chan string, 3)
	events <- FormatSSEEvent(map[string]any{
		"type":  "response.output_text.delta",
		"delta": "hello",
	})
	events <- FormatSSEEvent(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": "resp_1",
			"usage": map[string]any{
				"input_tokens":  1,
				"output_tokens": 2,
			},
		},
	})
	close(events)

	out := StreamChatChunks(events, "gpt-5.4", false)
	var chunks []string
	for chunk := range out {
		chunks = append(chunks, chunk)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected delta and done chunks, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0], "hello") {
		t.Fatalf("expected delta content, got %q", chunks[0])
	}
	if chunks[len(chunks)-1] != "data: [DONE]\n\n" {
		t.Fatalf("expected terminal done chunk, got %q", chunks[len(chunks)-1])
	}
}

func TestPreviousResponseIndexRememberLookup(t *testing.T) {
	idx := NewPreviousResponseIndex()
	idx.Remember("resp_abc", "key-1", "acct-1")
	if got := idx.Lookup("resp_abc", "key-1"); got != "acct-1" {
		t.Fatalf("lookup with api key: got %q", got)
	}
	if got := idx.Lookup("resp_abc", "other-key"); got != "" {
		t.Fatalf("lookup with other key should miss, got %q", got)
	}
}

func TestEnsureDownstreamTurnState(t *testing.T) {
	headers := make(map[string][]string)
	headers["X-Codex-Turn-State"] = []string{"turn_existing"}
	turn := EnsureDownstreamTurnState(headers)
	if turn != "turn_existing" {
		t.Fatalf("expected existing turn state, got %q", turn)
	}
	generated := EnsureDownstreamTurnState(nil)
	if !strings.HasPrefix(generated, "turn_") {
		t.Fatalf("expected generated turn state, got %q", generated)
	}
}

func TestStreamResponsesReservationUsesEnforcedModel(t *testing.T) {
	ctx := context.Background()
	store, encryptor := newWarmupTestStore(t)
	service := newWarmupTestService(t, store, encryptor)
	insertExhaustedModelLimitAPIKey(t, store, "key-enforced", "gpt-5.5")
	enforcedModel := "gpt-5.5"
	apiKey := &ApiKeyData{ID: "key-enforced", EnforcedModel: &enforcedModel}

	_, _, err := service.StreamResponses(ctx, httptest.NewRequest(http.MethodPost, "/v1/responses", nil), apiKey, map[string]any{
		"model": "gpt-5.4",
		"input": "hello",
	}, StreamResponsesOptions{})

	if err == nil {
		t.Fatal("expected enforced-model reservation to reject exhausted limit")
	}
	appErr, ok := err.(*AppError)
	if !ok || appErr.Code != "rate_limit_exceeded" {
		t.Fatalf("expected rate_limit_exceeded AppError, got %#v", err)
	}
}

func TestWebSocketResponsesReservationUsesEnforcedModel(t *testing.T) {
	store, encryptor := newWarmupTestStore(t)
	service := newWarmupTestService(t, store, encryptor)
	insertExhaustedModelLimitAPIKey(t, store, "key-enforced", "gpt-5.5")
	enforcedModel := "gpt-5.5"
	apiKey := &ApiKeyData{ID: "key-enforced", EnforcedModel: &enforcedModel}
	handler := WebSocketResponsesHandler{service: service, settingsRepo: service.settingsRepo}

	err := handler.proxyOneResponse(
		httptest.NewRequest(http.MethodGet, "/backend-api/codex/responses?stream=true", nil),
		nil,
		http.Header{},
		apiKey,
		map[string]any{
			"type":  "response.create",
			"model": "gpt-5.4",
			"input": "hello",
		},
	)

	if err == nil {
		t.Fatal("expected enforced-model reservation to reject exhausted limit")
	}
	appErr, ok := err.(*AppError)
	if !ok || appErr.Code != "rate_limit_exceeded" {
		t.Fatalf("expected rate_limit_exceeded AppError, got %#v", err)
	}
}

func insertExhaustedModelLimitAPIKey(t *testing.T, store *dbpkg.Store, keyID, model string) {
	t.Helper()
	now := time.Now().UTC()
	resetAt := now.Add(time.Hour).Format("2006-01-02 15:04:05")
	if _, err := store.DB().ExecContext(context.Background(), `
		INSERT INTO api_keys (id, name, key_hash, key_prefix, is_active, created_at)
		VALUES (?, 'enforced', ?, 'sk-clb-enforce', 1, ?)
	`, keyID, keyID+"-hash", now.Format("2006-01-02 15:04:05")); err != nil {
		t.Fatalf("insert api key: %v", err)
	}
	if _, err := store.DB().ExecContext(context.Background(), `
		INSERT INTO api_key_limits (api_key_id, limit_type, limit_window, max_value, current_value, model_filter, reset_at)
		VALUES (?, 'request', 'hour', 1, 1, ?, ?)
	`, keyID, model, resetAt); err != nil {
		t.Fatalf("insert api key limit: %v", err)
	}
}
