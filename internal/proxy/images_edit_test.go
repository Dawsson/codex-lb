package proxy

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/settings"
)

func TestEditImageTranslatesMultipartImageToResponsesInput(t *testing.T) {
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
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_edit\",\"output\":[{\"type\":\"image_generation_call\",\"status\":\"completed\",\"result\":\"edited\"}],\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n"))
	}))
	t.Cleanup(server.Close)
	service.upstreamBaseURL = server.URL

	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", nil)
	result, status, envelope, err := service.EditImage(req.Context(), req, nil, map[string]any{
		"prompt": "edit this",
		"model":  "gpt-image-1",
	}, []imagePart{{Data: []byte("png"), ContentType: "image/png"}}, nil)
	if err != nil {
		t.Fatalf("edit image: %v", err)
	}
	if envelope != nil || status != http.StatusOK {
		t.Fatalf("expected success, status=%d envelope=%#v", status, envelope)
	}
	data, ok := result["data"].([]any)
	if !ok || len(data) != 1 {
		t.Fatalf("unexpected image edit response: %#v", result)
	}
	tools := upstreamPayload["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["action"] != "edit" {
		t.Fatalf("expected edit action, tool=%#v", tool)
	}
	input := upstreamPayload["input"].([]any)
	message := input[0].(map[string]any)
	content := message["content"].([]any)
	if len(content) < 2 {
		t.Fatalf("expected text and image content, got %#v", content)
	}
	image := content[1].(map[string]any)
	if image["type"] != "input_image" || !strings.HasPrefix(image["image_url"].(string), "data:image/png;base64,") {
		t.Fatalf("unexpected image content part: %#v", image)
	}
}

func TestV1ImagesEditsRejectsMissingImage(t *testing.T) {
	store, encryptor := newWarmupTestStore(t)
	service := newWarmupTestService(t, store, encryptor)
	handler := NewMediaHandler(service, apikeys.NewRepository(store), settings.NewRepository(store, encryptor))
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("prompt", "edit")
	_ = writer.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()

	handler.V1ImagesEdits(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
