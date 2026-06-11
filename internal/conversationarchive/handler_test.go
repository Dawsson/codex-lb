package conversationarchive_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/soju06/codex-lb/internal/conversationarchive"
)

func TestHandlerListRecordsRequiresFileOrRequestID(t *testing.T) {
	handler := conversationarchive.NewHandler(conversationarchive.NewRepository(t.TempDir()))
	req := httptest.NewRequest(http.MethodGet, "/api/conversation-archive/records", nil)
	rec := httptest.NewRecorder()

	handler.ListRecords(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerListRecordsMapsDashboardShape(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "2026-06-01.jsonl", `{"timestamp":"2026-06-01T12:00:00Z","direction":"request","kind":"chat","transport":"http","request_id":"r1","account_id":"acct-1","method":"POST","url":"/v1/responses","status_code":200,"headers":{"A":1},"payload":{"ok":true},"extra":{"phase":"send"}}
`)
	handler := conversationarchive.NewHandler(conversationarchive.NewRepository(dir))
	req := httptest.NewRequest(http.MethodGet, "/api/conversation-archive/records?file=2026-06-01.jsonl", nil)
	rec := httptest.NewRecorder()

	handler.ListRecords(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Records []map[string]any `json:"records"`
		Total   int              `json:"total"`
		HasMore bool             `json:"hasMore"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Total != 1 || body.HasMore {
		t.Fatalf("unexpected pagination: %#v", body)
	}
	record := body.Records[0]
	if record["fileName"] != "2026-06-01.jsonl" || record["requestId"] != "r1" || record["accountId"] != "acct-1" || record["statusCode"] != float64(200) {
		t.Fatalf("unexpected record mapping: %#v", record)
	}
	headers, ok := record["headers"].(map[string]any)
	if !ok || headers["A"] != "1" {
		t.Fatalf("unexpected headers mapping: %#v", record["headers"])
	}
}

func TestHandlerListRecordsRejectsInvalidFileName(t *testing.T) {
	handler := conversationarchive.NewHandler(conversationarchive.NewRepository(t.TempDir()))
	req := httptest.NewRequest(http.MethodGet, "/api/conversation-archive/records?file=../escape.jsonl", nil)
	rec := httptest.NewRecorder()

	handler.ListRecords(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
