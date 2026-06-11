package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCodexV1AliasMiddlewareRewritesOnlyDuplicatedPrefix(t *testing.T) {
	var seenPath string
	handler := codexV1AliasMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/backend-api/codex/v1/models", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if seenPath != "/backend-api/codex/models" {
		t.Fatalf("expected canonical path, got %q", seenPath)
	}
}

func TestCodexV1AliasMiddlewareLeavesBareV1Alone(t *testing.T) {
	var seenPath string
	handler := codexV1AliasMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/backend-api/codex/v1", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if seenPath != "/backend-api/codex/v1" {
		t.Fatalf("expected bare v1 path unchanged, got %q", seenPath)
	}
}
