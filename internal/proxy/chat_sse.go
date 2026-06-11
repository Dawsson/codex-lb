package proxy

import (
	"encoding/json"
	"strings"
	"time"
)

// CollectChatCompletion aggregates upstream Responses SSE events into an
// OpenAI Chat Completions response object.
func CollectChatCompletion(events <-chan string, errs <-chan error, model string) (map[string]any, *OpenAIErrorEnvelope, error) {
	var contentParts []string
	var refusalParts []string
	var responseID string
	var usage map[string]any
	finishReason := "stop"

	for {
		select {
		case err, ok := <-errs:
			if ok && err != nil {
				return nil, nil, err
			}
		default:
		}

		block, ok := <-events
		if !ok {
			break
		}
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if raw == "" || raw == "[DONE]" {
				continue
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				continue
			}
			eventType, _ := payload["type"].(string)
			switch eventType {
			case "response.output_text.delta":
				if delta, ok := payload["delta"].(string); ok {
					contentParts = append(contentParts, delta)
				}
			case "response.refusal.delta":
				if delta, ok := payload["delta"].(string); ok {
					refusalParts = append(refusalParts, delta)
				}
			case "response.failed", "error":
				if envelope := errorEnvelopeFromEvent(payload, eventType); envelope != nil {
					return nil, envelope, nil
				}
			case "response.completed", "response.incomplete":
				if response, ok := payload["response"].(map[string]any); ok {
					if id, ok := response["id"].(string); ok {
						responseID = id
					}
					if rawUsage, ok := response["usage"].(map[string]any); ok {
						usage = mapChatUsage(rawUsage)
					}
					if eventType == "response.incomplete" {
						finishReason = "length"
					}
				}
			}
		}
	}

	message := map[string]any{"role": "assistant"}
	content := strings.Join(contentParts, "")
	if content != "" {
		message["content"] = content
	} else {
		message["content"] = nil
	}
	if refusal := strings.Join(refusalParts, ""); refusal != "" {
		message["refusal"] = refusal
	}

	if responseID == "" {
		responseID = "chatcmpl_temp"
	}
	result := map[string]any{
		"id":      responseID,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"message":       message,
				"finish_reason": finishReason,
			},
		},
	}
	if usage != nil {
		result["usage"] = usage
	}
	return result, nil, nil
}

func errorEnvelopeFromEvent(payload map[string]any, eventType string) *OpenAIErrorEnvelope {
	var raw map[string]any
	switch eventType {
	case "response.failed":
		if response, ok := payload["response"].(map[string]any); ok {
			if errObj, ok := response["error"].(map[string]any); ok {
				raw = errObj
			}
		}
	case "error":
		if errObj, ok := payload["error"].(map[string]any); ok {
			raw = errObj
		}
	}
	if raw == nil {
		envelope := OpenAIError("server_error", "Upstream request failed", "server_error")
		return &envelope
	}
	message, _ := raw["message"].(string)
	code, _ := raw["code"].(string)
	errorType, _ := raw["type"].(string)
	envelope := OpenAIError(code, message, errorType)
	return &envelope
}

func mapChatUsage(raw map[string]any) map[string]any {
	inputTokens := asInt(raw["input_tokens"])
	outputTokens := asInt(raw["output_tokens"])
	total := inputTokens + outputTokens
	return map[string]any{
		"prompt_tokens":     inputTokens,
		"completion_tokens": outputTokens,
		"total_tokens":      total,
	}
}

func asInt(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	default:
		return 0
	}
}
