package proxy

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
)

const turnStateHeader = "X-Codex-Turn-State"
const (
	StickySessionKindCodexSession = "codex_session"
	StickySessionKindStickyThread = "sticky_thread"
	StickySessionKindPromptCache  = "prompt_cache"
)

var synthesizedTurnStatePattern = regexp.MustCompile(`^(?:http_)?turn_[0-9a-f]{32}$`)

type AffinityPolicy struct {
	Key              string
	Kind             string
	ReallocateSticky bool
	MaxAgeSeconds    *int
}

// TurnStateFromHeaders returns the client turn-state header when present.
func TurnStateFromHeaders(headers http.Header) string {
	value := strings.TrimSpace(headers.Get(turnStateHeader))
	if value == "" {
		return ""
	}
	return value
}

func SessionKeyFromHeaders(headers http.Header) string {
	for _, key := range []string{"session_id", "X-Codex-Session-Id", "X-Codex-Conversation-Id"} {
		if value := strings.TrimSpace(headers.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func IsSynthesizedTurnState(value string) bool {
	return synthesizedTurnStatePattern.MatchString(value)
}

func StickyPolicyForResponsesRequest(
	body map[string]any,
	headers http.Header,
	codexSessionAffinity bool,
	openAICacheAffinity bool,
	openAICacheAffinityMaxAgeSeconds int,
	stickyThreadsEnabled bool,
) AffinityPolicy {
	if turnState := TurnStateFromHeaders(headers); turnState != "" {
		return AffinityPolicy{Key: turnState, Kind: StickySessionKindCodexSession}
	}
	if codexSessionAffinity {
		if sessionKey := SessionKeyFromHeaders(headers); sessionKey != "" {
			return AffinityPolicy{Key: sessionKey, Kind: StickySessionKindCodexSession}
		}
	}
	cacheKey := promptCacheKeyFromBody(body)
	if openAICacheAffinity && cacheKey != "" {
		maxAge := openAICacheAffinityMaxAgeSeconds
		return AffinityPolicy{Key: cacheKey, Kind: StickySessionKindPromptCache, MaxAgeSeconds: &maxAge}
	}
	if stickyThreadsEnabled && cacheKey != "" {
		return AffinityPolicy{Key: cacheKey, Kind: StickySessionKindStickyThread, ReallocateSticky: true}
	}
	return AffinityPolicy{}
}

func promptCacheKeyFromBody(body map[string]any) string {
	for _, key := range []string{"prompt_cache_key", "promptCacheKey"} {
		if value, ok := body[key].(string); ok {
			if stripped := strings.TrimSpace(value); stripped != "" {
				return stripped
			}
		}
	}
	return ""
}

func ExplicitPromptCacheKey(body map[string]any) bool {
	return promptCacheKeyFromBody(body) != ""
}

func PreviousResponseID(body map[string]any) string {
	return stringField(body, "previous_response_id")
}

func ExtractInputFileIDs(value any) []string {
	seen := map[string]struct{}{}
	var ids []string
	var walk func(any)
	walk = func(node any) {
		switch typed := node.(type) {
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			if typeName, _ := typed["type"].(string); typeName == "input_file" {
				if fileID, _ := typed["file_id"].(string); strings.TrimSpace(fileID) != "" {
					if _, ok := seen[fileID]; !ok {
						seen[fileID] = struct{}{}
						ids = append(ids, fileID)
					}
				}
			}
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	return ids
}

// EnsureDownstreamTurnState ports ensure_downstream_turn_state.
func EnsureDownstreamTurnState(headers http.Header) string {
	if existing := TurnStateFromHeaders(headers); existing != "" {
		return existing
	}
	return "turn_" + randomHex(16)
}

// EnsureHTTPDownstreamTurnState ports ensure_http_downstream_turn_state.
func EnsureHTTPDownstreamTurnState(headers http.Header) string {
	if existing := TurnStateFromHeaders(headers); existing != "" {
		return existing
	}
	return "http_turn_" + randomHex(16)
}

// DownstreamTurnStateResponseHeaders ports build_downstream_turn_state_response_headers.
func DownstreamTurnStateResponseHeaders(turnState string) http.Header {
	if turnState == "" {
		return nil
	}
	header := make(http.Header)
	header.Set(turnStateHeader, turnState)
	return header
}

func randomHex(byteCount int) string {
	buf := make([]byte, byteCount)
	if _, err := rand.Read(buf); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(buf)
}
