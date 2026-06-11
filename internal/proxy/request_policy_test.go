package proxy

import (
	"strings"
	"testing"
)

func TestEnforceStrictFunctionToolsFormatRejectsResponsesTool(t *testing.T) {
	tools := []any{
		map[string]any{
			"type":   "function",
			"name":   "lookup",
			"strict": true,
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
				},
				"required": []any{"query"},
			},
		},
	}

	err := EnforceStrictFunctionToolsFormat(tools, "tools[{index}].parameters", false)
	appErr, ok := err.(*AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T %v", err, err)
	}
	if appErr.StatusCode != 400 || appErr.Code != "invalid_function_parameters" || appErr.Param != "tools[0].parameters" {
		t.Fatalf("unexpected error: %#v", appErr)
	}
	if !strings.Contains(appErr.Message, "Invalid schema for function 'lookup': In context=(), 'additionalProperties' is required to be supplied and to be false.") {
		t.Fatalf("unexpected message: %s", appErr.Message)
	}
}

func TestEnforceStrictFunctionToolsFormatRejectsNestedChatToolWithInboundIndex(t *testing.T) {
	tools := []any{
		map[string]any{"type": "web_search"},
		map[string]any{
			"function": map[string]any{
				"name":   "lookup",
				"strict": true,
				"parameters": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"query": map[string]any{},
					},
					"required": []any{"query"},
				},
			},
		},
	}

	err := EnforceStrictFunctionToolsFormat(tools, "tools[{index}].function.parameters", true)
	appErr, ok := err.(*AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T %v", err, err)
	}
	if appErr.Param != "tools[1].function.parameters" {
		t.Fatalf("expected inbound index in param, got %q", appErr.Param)
	}
	if !strings.Contains(appErr.Message, "In context=('properties', 'query'), schema must have a 'type' key.") {
		t.Fatalf("unexpected message: %s", appErr.Message)
	}
}

func TestEnforceStrictFunctionToolsFormatAllowsNonStrictViolatingTool(t *testing.T) {
	tools := []any{
		map[string]any{
			"type": "function",
			"name": "lookup",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"query": map[string]any{}},
			},
		},
	}

	if err := EnforceStrictFunctionToolsFormat(tools, "tools[{index}].parameters", false); err != nil {
		t.Fatalf("expected non-strict tool to pass, got %v", err)
	}
}

func TestEnforceStrictFunctionToolsFormatRejectsMissingRequired(t *testing.T) {
	tools := []any{
		map[string]any{
			"type":   "function",
			"name":   "lookup",
			"strict": true,
			"parameters": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
				},
				"required": []any{},
			},
		},
	}

	err := EnforceStrictFunctionToolsFormat(tools, "tools[{index}].parameters", false)
	appErr, ok := err.(*AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T %v", err, err)
	}
	if !strings.Contains(appErr.Message, "Missing 'query'") {
		t.Fatalf("unexpected message: %s", appErr.Message)
	}
}

func TestEnforceStrictTextFormatRejectsInvalidSchema(t *testing.T) {
	body := map[string]any{
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "answer",
				"strict": true,
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"value": map[string]any{"type": "string"},
					},
					"required": []any{"value"},
				},
			},
		},
	}

	err := EnforceStrictTextFormat(body)
	appErr, ok := err.(*AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T %v", err, err)
	}
	if appErr.Code != "invalid_json_schema" || appErr.Param != "text.format.schema" {
		t.Fatalf("unexpected error: %#v", appErr)
	}
	if !strings.Contains(appErr.Message, "Invalid schema for response_format 'answer': In context=(), 'additionalProperties' is required to be supplied and to be false.") {
		t.Fatalf("unexpected message: %s", appErr.Message)
	}
}

func TestEnforceStrictChatResponseFormatRejectsInvalidSchema(t *testing.T) {
	body := map[string]any{
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "answer",
				"strict": true,
				"schema": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"value": map[string]any{"type": "string"},
					},
					"required": []any{},
				},
			},
		},
	}

	err := EnforceStrictChatResponseFormat(body)
	appErr, ok := err.(*AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T %v", err, err)
	}
	if appErr.Code != "invalid_json_schema" || appErr.Param != "text.format.schema" {
		t.Fatalf("unexpected error: %#v", appErr)
	}
	if !strings.Contains(appErr.Message, "Missing 'value'") {
		t.Fatalf("unexpected message: %s", appErr.Message)
	}
}

func TestEnforceStrictTextFormatAllowsNonStrictInvalidSchema(t *testing.T) {
	body := map[string]any{
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"strict": false,
				"schema": map[string]any{"type": "object"},
			},
		},
	}

	if err := EnforceStrictTextFormat(body); err != nil {
		t.Fatalf("expected non-strict schema to pass, got %v", err)
	}
}

func TestValidateChatCompletionsRequestAllowsResponsesShapedInput(t *testing.T) {
	body := map[string]any{
		"model": "gpt-5.5",
		"input": []any{map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "input_text", "text": "hi"}},
		}},
	}

	if err := ValidateChatCompletionsRequest(body); err != nil {
		t.Fatalf("expected responses-shaped payload to pass, got %v", err)
	}
}

func TestValidateChatCompletionsRequestRejectsUnsupportedRole(t *testing.T) {
	body := map[string]any{
		"model":    "gpt-5.5",
		"messages": []any{map[string]any{"role": "moderator", "content": "hi"}},
	}

	err := ValidateChatCompletionsRequest(body)
	appErr, ok := err.(*AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T %v", err, err)
	}
	if appErr.Param != "messages" || !strings.Contains(appErr.Message, "Unsupported message role: moderator") {
		t.Fatalf("unexpected error: %#v", appErr)
	}
}

func TestValidateChatCompletionsRequestRejectsSystemImageContent(t *testing.T) {
	body := map[string]any{
		"model": "gpt-5.5",
		"messages": []any{map[string]any{
			"role": "system",
			"content": []any{map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": "data:image/png;base64,AAAA"},
			}},
		}},
	}

	err := ValidateChatCompletionsRequest(body)
	appErr, ok := err.(*AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T %v", err, err)
	}
	if appErr.Param != "messages" || !strings.Contains(appErr.Message, "system messages must be text-only.") {
		t.Fatalf("unexpected error: %#v", appErr)
	}
}

func TestValidateChatCompletionsRequestRejectsInputAudio(t *testing.T) {
	body := map[string]any{
		"model": "gpt-5.5",
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{map[string]any{
				"type":        "input_audio",
				"input_audio": map[string]any{"data": "AAAA", "format": "wav"},
			}},
		}},
	}

	err := ValidateChatCompletionsRequest(body)
	appErr, ok := err.(*AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T %v", err, err)
	}
	if appErr.Param != "messages" || !strings.Contains(appErr.Message, "Audio input is not supported.") {
		t.Fatalf("unexpected error: %#v", appErr)
	}
}

func TestValidateChatCompletionsRequestRejectsFileID(t *testing.T) {
	body := map[string]any{
		"model": "gpt-5.5",
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{map[string]any{
				"type": "file",
				"file": map[string]any{"file_id": "file_123"},
			}},
		}},
	}

	err := ValidateChatCompletionsRequest(body)
	appErr, ok := err.(*AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T %v", err, err)
	}
	if appErr.Param != "messages" || !strings.Contains(appErr.Message, "file_id is not supported") {
		t.Fatalf("unexpected error: %#v", appErr)
	}
}

func TestValidateAndNormalizeChatToolsRejectsBuiltinForMessages(t *testing.T) {
	body := map[string]any{
		"model":    "gpt-5.5",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools":    []any{map[string]any{"type": "image_generation"}},
	}

	err := ValidateAndNormalizeChatTools(body)
	appErr, ok := err.(*AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T %v", err, err)
	}
	if appErr.Param != "tools" || !strings.Contains(appErr.Message, "Unsupported tool type: image_generation") {
		t.Fatalf("unexpected error: %#v", appErr)
	}
}

func TestValidateAndNormalizeChatToolsPreservesBuiltinForResponsesShapedPayload(t *testing.T) {
	body := map[string]any{
		"model": "gpt-5.5",
		"input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}}},
		"tools": []any{map[string]any{"type": "image_generation"}},
	}

	if err := ValidateAndNormalizeChatTools(body); err != nil {
		t.Fatalf("expected responses-shaped builtin tool to pass, got %v", err)
	}
}

func TestValidateAndNormalizeChatToolsNormalizesWebSearchPreview(t *testing.T) {
	body := map[string]any{
		"model":       "gpt-5.5",
		"messages":    []any{map[string]any{"role": "user", "content": "hi"}},
		"tools":       []any{map[string]any{"type": "web_search_preview"}},
		"tool_choice": map[string]any{"type": "web_search_preview"},
	}

	if err := ValidateAndNormalizeChatTools(body); err != nil {
		t.Fatalf("expected web_search_preview to pass, got %v", err)
	}
	tools := body["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["type"] != "web_search" {
		t.Fatalf("expected normalized tool type, got %#v", tool)
	}
	toolChoice := body["tool_choice"].(map[string]any)
	if toolChoice["type"] != "web_search" {
		t.Fatalf("expected normalized tool_choice, got %#v", toolChoice)
	}
}

func TestApplyChatResponseFormatMapsJSONSchema(t *testing.T) {
	body := map[string]any{
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "answer",
				"schema": map[string]any{"type": "object"},
				"strict": false,
			},
		},
	}

	if err := ApplyChatResponseFormat(body); err != nil {
		t.Fatalf("expected response_format mapping to pass, got %v", err)
	}
	if _, ok := body["response_format"]; ok {
		t.Fatalf("response_format should be removed: %#v", body)
	}
	text := body["text"].(map[string]any)
	format := text["format"].(map[string]any)
	if format["type"] != "json_schema" || format["name"] != "answer" || format["strict"] != false {
		t.Fatalf("unexpected text.format: %#v", format)
	}
	if schema := format["schema"].(map[string]any); schema["type"] != "object" {
		t.Fatalf("unexpected schema: %#v", schema)
	}
}

func TestApplyChatResponseFormatMapsStringFormat(t *testing.T) {
	body := map[string]any{"response_format": "json_object"}

	if err := ApplyChatResponseFormat(body); err != nil {
		t.Fatalf("expected response_format mapping to pass, got %v", err)
	}
	text := body["text"].(map[string]any)
	format := text["format"].(map[string]any)
	if format["type"] != "json_object" {
		t.Fatalf("unexpected text.format: %#v", format)
	}
}

func TestApplyChatResponseFormatRejectsMissingJSONSchema(t *testing.T) {
	body := map[string]any{"response_format": map[string]any{"type": "json_schema"}}

	err := ApplyChatResponseFormat(body)
	appErr, ok := err.(*AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T %v", err, err)
	}
	if appErr.Param != "response_format.json_schema" || !strings.Contains(appErr.Message, "'response_format.json_schema' is required") {
		t.Fatalf("unexpected error: %#v", appErr)
	}
}

func TestApplyChatResponseFormatRejectsTextFormatConflict(t *testing.T) {
	body := map[string]any{
		"response_format": "json_object",
		"text":            map[string]any{"format": map[string]any{"type": "text"}},
	}

	err := ApplyChatResponseFormat(body)
	appErr, ok := err.(*AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T %v", err, err)
	}
	if appErr.Param != "response_format" || !strings.Contains(appErr.Message, "Provide either 'response_format' or 'text.format'") {
		t.Fatalf("unexpected error: %#v", appErr)
	}
}
