package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGenerateImageCollectsResponsesImageResult(t *testing.T) {
	store, encryptor := newWarmupTestStore(t)
	insertWarmupAccount(t, store, encryptor, "acct-1", 0)
	service := newWarmupTestService(t, store, encryptor)
	var upstreamPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" {
			t.Fatalf("unexpected upstream path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamPayload); err != nil {
			t.Fatalf("decode upstream payload: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_img\",\"output\":[{\"type\":\"image_generation_call\",\"status\":\"completed\",\"result\":\"abc123\",\"revised_prompt\":\"better\"}],\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n"))
	}))
	t.Cleanup(server.Close)
	service.upstreamBaseURL = server.URL

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"prompt":"draw","model":"gpt-image-1"}`))
	result, status, envelope, err := service.GenerateImage(req.Context(), req, nil, map[string]any{
		"prompt": "draw",
		"model":  "gpt-image-1",
	})
	if err != nil {
		t.Fatalf("generate image: %v", err)
	}
	if envelope != nil || status != http.StatusOK {
		t.Fatalf("expected success, status=%d envelope=%#v", status, envelope)
	}
	data, ok := result["data"].([]any)
	if !ok || len(data) != 1 {
		t.Fatalf("unexpected image response: %#v", result)
	}
	first, _ := data[0].(map[string]any)
	if first["b64_json"] != "abc123" || first["revised_prompt"] != "better" {
		t.Fatalf("unexpected image data: %#v", first)
	}
	tools, _ := upstreamPayload["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected image_generation tool, payload=%#v", upstreamPayload)
	}
}

func TestGenerateImageRejectsInvalidModel(t *testing.T) {
	store, encryptor := newWarmupTestStore(t)
	service := newWarmupTestService(t, store, encryptor)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

	_, status, envelope, err := service.GenerateImage(req.Context(), req, nil, map[string]any{
		"prompt": "draw",
		"model":  "gpt-5.5",
	})
	if err != nil {
		t.Fatalf("generate image: %v", err)
	}
	if envelope == nil || status != http.StatusBadRequest {
		t.Fatalf("expected bad request, status=%d envelope=%#v", status, envelope)
	}
}
