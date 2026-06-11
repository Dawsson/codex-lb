package proxy

import (
	"net/http"
	"testing"
)

func TestParseWebSocketResponsePayloadRejectsMalformedJSON(t *testing.T) {
	_, appErr := parseWebSocketResponsePayload([]byte(`{"type":`))
	if appErr == nil {
		t.Fatal("expected malformed JSON error")
	}
	if appErr.Code != "invalid_json" || appErr.ErrorType != "invalid_request_error" {
		t.Fatalf("unexpected error envelope code=%q type=%q", appErr.Code, appErr.ErrorType)
	}
}

func TestParseWebSocketResponsePayloadRejectsNonObjectJSON(t *testing.T) {
	_, appErr := parseWebSocketResponsePayload([]byte(`["response.create"]`))
	if appErr == nil {
		t.Fatal("expected non-object JSON error")
	}
	if appErr.Code != "invalid_json" || appErr.ErrorType != "invalid_request_error" {
		t.Fatalf("unexpected error envelope code=%q type=%q", appErr.Code, appErr.ErrorType)
	}
}

func TestProxyOneResponseMissingModelIsInvalidRequest(t *testing.T) {
	err := (WebSocketResponsesHandler{}).proxyOneResponse(
		httptestRequest(),
		nil,
		http.Header{},
		nil,
		map[string]any{"type": "response.create", "input": "hi"},
	)
	appErr, ok := err.(*AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.StatusCode != http.StatusBadRequest || appErr.Code != "invalid_request_error" {
		t.Fatalf("unexpected status/code: %d %q", appErr.StatusCode, appErr.Code)
	}
	if appErr.ErrorType != "invalid_request_error" || appErr.Param != "model" {
		t.Fatalf("unexpected type/param: %q %q", appErr.ErrorType, appErr.Param)
	}
}

func httptestRequest() *http.Request {
	req, _ := http.NewRequest(http.MethodGet, "/v1/responses", nil)
	return req
}
