package proxy

import (
	"encoding/json"
	"strings"
	"time"
)

type chatChunkState struct {
	sentRole bool
}

// StreamChatChunks converts upstream Responses SSE blocks into OpenAI chat
// completion chunk SSE frames.
func StreamChatChunks(events <-chan string, model string, includeUsage bool) <-chan string {
	out := make(chan string, 32)
	go func() {
		defer close(out)
		created := time.Now().Unix()
		state := chatChunkState{}
		for event := range events {
			payload := ParseSSEDataJSON(event)
			if payload == nil {
				continue
			}
			for _, chunk := range iterChatChunks(payload, model, created, &state, includeUsage) {
				out <- chunk
			}
		}
		out <- "data: [DONE]\n\n"
	}()
	return out
}

func iterChatChunks(payload map[string]any, model string, created int64, state *chatChunkState, includeUsage bool) []string {
	eventType, _ := payload["type"].(string)
	switch eventType {
	case "response.output_text.delta", "response.refusal.delta":
		delta, _ := payload["delta"].(string)
		message := map[string]any{"role": "assistant"}
		if !state.sentRole {
			message["role"] = "assistant"
			state.sentRole = true
		}
		if eventType == "response.refusal.delta" {
			message["refusal"] = delta
		} else {
			message["content"] = delta
		}
		return []string{dumpChatChunk(model, created, message, nil, includeUsage, nil)}
	case "response.failed", "error":
		var errObj map[string]any
		if eventType == "response.failed" {
			if response, ok := payload["response"].(map[string]any); ok {
				if maybe, ok := response["error"].(map[string]any); ok {
					errObj = maybe
				}
			}
		} else if maybe, ok := payload["error"].(map[string]any); ok {
			errObj = maybe
		}
		if errObj != nil {
			data, _ := json.Marshal(map[string]any{"error": errObj})
			return []string{"data: " + string(data) + "\n\n", "data: [DONE]\n\n"}
		}
	case "response.completed", "response.incomplete":
		finishReason := "stop"
		if eventType == "response.incomplete" {
			finishReason = "length"
		}
		var usage map[string]any
		if includeUsage {
			if response, ok := payload["response"].(map[string]any); ok {
				if rawUsage, ok := response["usage"].(map[string]any); ok {
					usage = mapChatUsage(rawUsage)
				}
			}
		}
		message := map[string]any{}
		if !state.sentRole {
			message["role"] = "assistant"
		}
		return []string{dumpChatChunk(model, created, message, &finishReason, includeUsage, usage)}
	}
	return nil
}

func dumpChatChunk(model string, created int64, message map[string]any, finishReason *string, includeUsage bool, usage map[string]any) string {
	chunk := map[string]any{
		"id":      "chatcmpl_temp",
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []any{
			map[string]any{
				"index": 0,
				"delta": message,
				"finish_reason": func() any {
					if finishReason == nil {
						return nil
					}
					return *finishReason
				}(),
			},
		},
	}
	if includeUsage && usage != nil && finishReason != nil {
		chunk["usage"] = usage
	}
	data, _ := json.Marshal(chunk)
	return "data: " + string(data) + "\n\n"
}

func dumpChatSSE(payload map[string]any) string {
	data, _ := json.Marshal(payload)
	return "data: " + string(data) + "\n\n"
}

func isChatDoneChunk(chunk string) bool {
	return strings.TrimSpace(chunk) == "data: [DONE]"
}
