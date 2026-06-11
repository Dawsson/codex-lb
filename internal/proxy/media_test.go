package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/settings"
	"github.com/soju06/codex-lb/internal/upstream"
)

func TestProxyBackendFilesCreatePinsReturnedFile(t *testing.T) {
	ctx := context.Background()
	store, encryptor := newWarmupTestStore(t)
	insertWarmupAccount(t, store, encryptor, "acct-1", 0)
	service := newWarmupTestService(t, store, encryptor)
	var upstreamPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.Path
		if r.Header.Get("Authorization") == "" {
			t.Errorf("missing upstream authorization")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"file_id":    "file_1",
			"upload_url": "https://upload.example/file_1",
		})
	}))
	t.Cleanup(server.Close)
	service.upstreamBaseURL = server.URL

	req := httptest.NewRequest(http.MethodPost, "/backend-api/files", strings.NewReader(`{"file_name":"a.txt","file_size":1,"use_case":"responses"}`))
	req.Header.Set("Content-Type", "application/json")
	result, status, envelope, err := service.ProxyBackendJSON(ctx, req, nil, "files", "files-create", "", []byte(`{"file_name":"a.txt"}`))
	if err != nil {
		t.Fatalf("proxy backend json: %v", err)
	}
	if envelope != nil || status != http.StatusOK {
		t.Fatalf("expected success, status=%d envelope=%#v", status, envelope)
	}
	if upstreamPath != "/files" || result["file_id"] != "file_1" {
		t.Fatalf("unexpected upstream result path=%s result=%#v", upstreamPath, result)
	}
	if got := service.resolvePinnedFileAccount("file_1"); got != "acct-1" {
		t.Fatalf("expected file pin acct-1, got %q", got)
	}
}

func TestTranscribeAudioForwardsMultipartAndLogs(t *testing.T) {
	ctx := context.Background()
	store, encryptor := newWarmupTestStore(t)
	insertWarmupAccount(t, store, encryptor, "acct-1", 0)
	service := newWarmupTestService(t, store, encryptor)
	var sawPrompt string
	var sawFilename string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/transcribe" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Fatalf("parse upstream multipart: %v", err)
		}
		sawPrompt = r.FormValue("prompt")
		_, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("upstream file missing: %v", err)
		}
		sawFilename = header.Filename
		_ = json.NewEncoder(w).Encode(map[string]any{"text": "hello"})
	}))
	t.Cleanup(server.Close)
	service.upstreamBaseURL = server.URL

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)
	result, status, envelope, err := service.TranscribeAudio(ctx, req, nil, []byte("audio"), "clip.wav", "audio/wav", "Say hi")
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if envelope != nil || status != http.StatusOK {
		t.Fatalf("expected success, status=%d envelope=%#v", status, envelope)
	}
	if result["text"] != "hello" || sawPrompt != "Say hi" || sawFilename != "clip.wav" {
		t.Fatalf("unexpected transcription result=%#v prompt=%q filename=%q", result, sawPrompt, sawFilename)
	}
}

func TestV1AudioTranscriptionsRejectsWrongModel(t *testing.T) {
	store, encryptor := newWarmupTestStore(t)
	service := newWarmupTestService(t, store, encryptor)
	handler := NewMediaHandler(service, apikeys.NewRepository(store), settings.NewRepository(store, encryptor))
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("model", "other")
	part, err := writer.CreateFormFile("file", "clip.wav")
	if err != nil {
		t.Fatalf("create file field: %v", err)
	}
	_, _ = part.Write([]byte("audio"))
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &body)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	handler.V1AudioTranscriptions(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestV1ImagesVariationsReturnsUnsupported404AfterAuth(t *testing.T) {
	store, encryptor := newWarmupTestStore(t)
	service := newWarmupTestService(t, store, encryptor)
	handler := NewMediaHandler(service, apikeys.NewRepository(store), settings.NewRepository(store, encryptor))

	req := httptest.NewRequest(http.MethodPost, "/v1/images/variations", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	rec := httptest.NewRecorder()
	handler.V1ImagesVariations(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "/v1/images/variations is not supported") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestProxyBackendFilesFinalizeUsesPinnedFile(t *testing.T) {
	ctx := context.Background()
	store, encryptor := newWarmupTestStore(t)
	insertWarmupAccount(t, store, encryptor, "acct-1", 0)
	service := newWarmupTestService(t, store, encryptor)
	service.pinFileAccount("file_1", "acct-1")
	var upstreamPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":       "success",
			"download_url": "https://download.example/file_1",
		})
	}))
	t.Cleanup(server.Close)
	service.upstreamBaseURL = server.URL

	req := httptest.NewRequest(http.MethodPost, "/backend-api/files/file_1/uploaded", strings.NewReader(`{}`))
	result, status, envelope, err := service.ProxyBackendJSON(ctx, req, nil, "files/file_1/uploaded", "files-finalize", "file_1", []byte(`{}`))
	if err != nil {
		t.Fatalf("proxy backend json: %v", err)
	}
	if envelope != nil || status != http.StatusOK {
		t.Fatalf("expected success, status=%d envelope=%#v", status, envelope)
	}
	if upstreamPath != "/files/file_1/uploaded" || result["status"] != "success" {
		t.Fatalf("unexpected finalize result path=%s result=%#v", upstreamPath, result)
	}
}

func TestProxyBackendFilesFinalizeDoesNotCrossPinnedAccount(t *testing.T) {
	ctx := context.Background()
	store, encryptor := newWarmupTestStore(t)
	insertWarmupAccount(t, store, encryptor, "acct-1", 0)
	insertWarmupAccount(t, store, encryptor, "acct-2", 0)
	service := newWarmupTestService(t, store, encryptor)
	service.pinFileAccount("file_1", "acct-1")
	var sawAccount string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAccount = r.Header.Get("Chatgpt-Account-Id")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success"})
	}))
	t.Cleanup(server.Close)
	service.upstreamBaseURL = server.URL

	req := httptest.NewRequest(http.MethodPost, "/backend-api/files/file_1/uploaded", strings.NewReader(`{}`))
	_, status, envelope, err := service.ProxyBackendJSON(ctx, req, nil, "files/file_1/uploaded", "files-finalize", "file_1", []byte(`{}`))
	if err != nil {
		t.Fatalf("proxy backend json: %v", err)
	}
	if envelope != nil || status != http.StatusOK {
		t.Fatalf("expected success, status=%d envelope=%#v", status, envelope)
	}
	if sawAccount != "acct-1" {
		t.Fatalf("expected pinned account acct-1, saw upstream account %q", sawAccount)
	}
}

func TestForwardJSONGETJWKS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/wham/agent-identities/jwks" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kid": "k1"}}})
	}))
	t.Cleanup(server.Close)

	result, status, err := upstream.ForwardJSON(context.Background(), server.Client(), server.URL, http.MethodGet, "wham/agent-identities/jwks", nil, nil, "token", "acct")
	if err != nil {
		t.Fatalf("forward json: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	keys, ok := result["keys"].([]any)
	if !ok || len(keys) != 1 {
		t.Fatalf("unexpected jwks response: %#v", result)
	}
}

func TestForwardJSONGETCodexJWKS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/agent-identities/jwks" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kid": "codex"}}})
	}))
	t.Cleanup(server.Close)

	result, status, err := upstream.ForwardJSON(context.Background(), server.Client(), server.URL, http.MethodGet, "agent-identities/jwks", nil, nil, "token", "acct")
	if err != nil {
		t.Fatalf("forward json: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	keys, ok := result["keys"].([]any)
	if !ok || len(keys) != 1 {
		t.Fatalf("unexpected jwks response: %#v", result)
	}
}

func TestForwardRawCodexControlPreservesMethodPathQueryBodyAndHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/codex/thread/goal/set" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query()["scope"]; len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Fatalf("expected repeated query values, got %v", r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") == "" {
			t.Fatalf("missing authorization")
		}
		if r.Header.Get("Content-Type") != "application/custom+json" {
			t.Fatalf("unexpected content type %q", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"goal":"ship"}` {
			t.Fatalf("unexpected body %q", body)
		}
		w.Header().Set("ETag", "abc")
		w.Header().Set("X-Internal-Debug", "hidden")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	req := httptest.NewRequest(http.MethodPost, "/backend-api/codex/thread/goal/set?scope=a&scope=b", strings.NewReader(`{"goal":"ship"}`))
	req.Header.Set("Content-Type", "application/custom+json")
	response, err := upstream.ForwardRaw(context.Background(), server.Client(), server.URL, req.Method, "codex/thread/goal/set", req.URL.Query(), []byte(`{"goal":"ship"}`), req.Header, "token", "acct")
	if err != nil {
		t.Fatalf("forward raw: %v", err)
	}
	if response.StatusCode != http.StatusAccepted || string(response.Body) != `{"ok":true}` {
		t.Fatalf("unexpected response: %#v body=%s", response, response.Body)
	}
	if response.Header.Get("ETag") != "abc" {
		t.Fatalf("expected etag response header, got %#v", response.Header)
	}
}
