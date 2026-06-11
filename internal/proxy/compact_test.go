package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/soju06/codex-lb/internal/upstream"
)

func TestCompactResponsesReturnsUpstreamPayloadAndLogs(t *testing.T) {
	ctx := context.Background()
	store, encryptor := newWarmupTestStore(t)
	insertWarmupAccount(t, store, encryptor, "acct-1", 0)
	service := newWarmupTestService(t, store, encryptor)
	var upstreamPayload map[string]any
	service.compactSubmitter = func(ctx context.Context, opts upstream.StreamOptions) (map[string]any, error) {
		upstreamPayload = opts.Payload
		return map[string]any{
			"object": "response.compact",
			"id":     "compact_1",
			"status": "completed",
			"usage": map[string]any{
				"input_tokens":  int64(4),
				"output_tokens": int64(1),
			},
		}, nil
	}

	result, envelope, status, err := service.CompactResponses(ctx, httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil), nil, map[string]any{
		"model":    "gpt-5.5",
		"messages": []any{map[string]any{"role": "user", "content": "hello"}},
		"store":    true,
	}, false)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if envelope != nil || status != http.StatusOK {
		t.Fatalf("expected success, status=%d envelope=%#v", status, envelope)
	}
	if result["object"] != "response.compact" || result["id"] != "compact_1" {
		t.Fatalf("unexpected compact result: %#v", result)
	}
	if upstreamPayload["messages"] != nil || upstreamPayload["input"] == nil || upstreamPayload["store"] != nil {
		t.Fatalf("unexpected upstream compact payload: %#v", upstreamPayload)
	}
	var logCount int
	if err := store.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM request_logs WHERE account_id = 'acct-1' AND model = 'gpt-5.5' AND status = 'success'
	`).Scan(&logCount); err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if logCount != 1 {
		t.Fatalf("expected one request log, got %d", logCount)
	}
}

func TestCompactResponsesRequiresInputOrMessages(t *testing.T) {
	store, encryptor := newWarmupTestStore(t)
	service := newWarmupTestService(t, store, encryptor)

	_, envelope, status, err := service.CompactResponses(context.Background(), httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil), nil, map[string]any{
		"model": "gpt-5.5",
	}, false)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if envelope == nil || status != http.StatusBadRequest {
		t.Fatalf("expected bad request envelope, status=%d envelope=%#v", status, envelope)
	}
}
