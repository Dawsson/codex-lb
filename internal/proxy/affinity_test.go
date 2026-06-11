package proxy

import (
	"net/http"
	"testing"
	"time"
)

func TestStickyPolicyForResponsesRequestPriority(t *testing.T) {
	body := map[string]any{"prompt_cache_key": "cache-a"}
	headers := http.Header{}
	headers.Set("X-Codex-Turn-State", "turn-client")
	policy := StickyPolicyForResponsesRequest(body, headers, true, true, 1800, true)
	if policy.Kind != StickySessionKindCodexSession || policy.Key != "turn-client" {
		t.Fatalf("expected turn-state codex session, got %+v", policy)
	}

	headers = http.Header{}
	headers.Set("session_id", "session-a")
	policy = StickyPolicyForResponsesRequest(body, headers, true, true, 1800, true)
	if policy.Kind != StickySessionKindCodexSession || policy.Key != "session-a" {
		t.Fatalf("expected session header codex session, got %+v", policy)
	}

	policy = StickyPolicyForResponsesRequest(body, http.Header{}, false, true, 1800, true)
	if policy.Kind != StickySessionKindPromptCache || policy.Key != "cache-a" || policy.MaxAgeSeconds == nil || *policy.MaxAgeSeconds != 1800 {
		t.Fatalf("expected prompt cache policy, got %+v", policy)
	}
}

func TestResolveFileAccountForResponsesHonorsOnlyWeakAffinity(t *testing.T) {
	service := &Service{filePins: map[string]filePin{}}
	service.pinFileAccount("file-new", "acct-1")

	body := map[string]any{
		"input": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_file", "file_id": "file-new"},
				},
			},
		},
	}
	headers := http.Header{}
	headers.Set("X-Codex-Turn-State", "http_turn_0123456789abcdef0123456789abcdef")
	if accountID := service.resolveFileAccountForResponses(body, headers); accountID != "acct-1" {
		t.Fatalf("expected synthetic turn-state to allow file pin, got %s", accountID)
	}

	headers.Set("X-Codex-Turn-State", "client-turn")
	if accountID := service.resolveFileAccountForResponses(body, headers); accountID != "" {
		t.Fatalf("expected client turn-state to suppress file pin, got %s", accountID)
	}

	headers = http.Header{}
	body["prompt_cache_key"] = "cache-a"
	if accountID := service.resolveFileAccountForResponses(body, headers); accountID != "" {
		t.Fatalf("expected explicit prompt cache key to suppress file pin, got %s", accountID)
	}
}

func TestResolveFileAccountForResponsesChoosesNewestPin(t *testing.T) {
	service := &Service{filePins: map[string]filePin{
		"file-old": {AccountID: "acct-old", ExpiresAt: time.Now().Add(10 * time.Minute)},
		"file-new": {AccountID: "acct-new", ExpiresAt: time.Now().Add(20 * time.Minute)},
	}}
	body := map[string]any{
		"input": []any{
			map[string]any{"type": "input_file", "file_id": "file-old"},
			map[string]any{"type": "input_file", "file_id": "file-new"},
		},
	}
	if accountID := service.resolveFileAccountForResponses(body, http.Header{}); accountID != "acct-new" {
		t.Fatalf("expected newest pin account, got %s", accountID)
	}
}
