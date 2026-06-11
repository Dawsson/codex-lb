package proxy

import (
	"fmt"
	"strings"
)

var unsupportedUpstreamReasoningEfforts = stringSet("minimal")

const defaultReasoningEffortFallback = "low"

var upstreamOmitServiceTiers = stringSet("auto", "default")

var supportedChatRoles = stringSet("system", "developer", "user", "assistant", "tool")
var textContentPartTypes = stringSet("text", "input_text", "output_text")
var unsupportedChatBuiltinToolTypes = stringSet("file_search", "code_interpreter", "computer_use", "computer_use_preview", "image_generation")

// ValidateModelAccess ports validate_model_access.
func ValidateModelAccess(apiKey *ApiKeyData, model string) error {
	if apiKey == nil || len(apiKey.AllowedModels) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(apiKey.AllowedModels))
	for _, allowedModel := range apiKey.AllowedModels {
		allowed[ResolveModelAlias(allowedModel)] = struct{}{}
	}
	effectiveModel := ResolveModelAlias(model)
	if _, ok := allowed[effectiveModel]; ok {
		return nil
	}
	return NewProxyModelNotAllowedError(fmt.Sprintf("This API key does not have access to model '%s'", model))
}

// ResponsesPayload is the subset of Responses API fields the proxy mutates.
type ResponsesPayload struct {
	Model       string
	Reasoning   *ReasoningPayload
	ServiceTier *string
	Extra       map[string]any
}

type ReasoningPayload struct {
	Effort *string
}

// ApplyAPIKeyEnforcement ports apply_api_key_enforcement for Responses payloads.
func ApplyAPIKeyEnforcement(payload *ResponsesPayload, apiKey *ApiKeyData) {
	NormalizeUpstreamModelAlias(payload)
	if apiKey == nil {
		normalizeUnsupportedReasoningEffort(payload)
		return
	}
	if apiKey.EnforcedModel != nil && payload.Model != *apiKey.EnforcedModel {
		payload.Model = *apiKey.EnforcedModel
		NormalizeUpstreamModelAlias(payload)
	}
	if apiKey.EnforcedReasoning != nil {
		if payload.Reasoning == nil {
			payload.Reasoning = &ReasoningPayload{}
		}
		payload.Reasoning.Effort = apiKey.EnforcedReasoning
	}
	normalizeUnsupportedReasoningEffort(payload)
	if apiKey.EnforcedServiceTier != nil {
		if _, omit := upstreamOmitServiceTiers[*apiKey.EnforcedServiceTier]; omit {
			payload.ServiceTier = nil
		} else {
			tier := *apiKey.EnforcedServiceTier
			payload.ServiceTier = &tier
		}
	}
}

// NormalizeUpstreamModelAlias ports normalize_upstream_model_alias.
func NormalizeUpstreamModelAlias(payload *ResponsesPayload) {
	if payload == nil || payload.Model == "" {
		return
	}
	base, reasoning, tier := ResolveModelAliasParts(payload.Model)
	if base != "" {
		payload.Model = base
	}
	if reasoning != "" {
		if payload.Reasoning == nil {
			payload.Reasoning = &ReasoningPayload{}
		}
		payload.Reasoning.Effort = &reasoning
	}
	if tier != "" {
		payload.ServiceTier = &tier
	}
}

func normalizeUnsupportedReasoningEffort(payload *ResponsesPayload) {
	if payload == nil || payload.Reasoning == nil || payload.Reasoning.Effort == nil {
		return
	}
	effort := strings.ToLower(strings.TrimSpace(*payload.Reasoning.Effort))
	if _, unsupported := unsupportedUpstreamReasoningEfforts[effort]; !unsupported {
		return
	}
	fallback := defaultReasoningEffortFallback
	payload.Reasoning.Effort = &fallback
}

// EffectiveModelForAPIKey resolves the model after alias normalization and
// optional API-key enforcement.
func EffectiveModelForAPIKey(apiKey *ApiKeyData, model string) string {
	if apiKey != nil && apiKey.EnforcedModel != nil {
		return *apiKey.EnforcedModel
	}
	return ResolveModelAlias(model)
}

type strictSchemaViolation struct {
	context []string
	reason  string
}

func EnforceStrictFunctionToolsFormat(tools any, paramTemplate string, nested bool) error {
	if paramTemplate == "" {
		paramTemplate = "tools[{index}].parameters"
	}
	items, ok := tools.([]any)
	if !ok || len(items) == 0 {
		return nil
	}
	for index, rawTool := range items {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		descriptor := tool
		if nested {
			functionValue, ok := tool["function"].(map[string]any)
			if !ok {
				continue
			}
			descriptor = functionValue
		} else if tool["type"] != "function" {
			continue
		}
		if strict, ok := descriptor["strict"].(bool); !ok || !strict {
			continue
		}
		parameters, ok := descriptor["parameters"]
		if !ok || parameters == nil {
			continue
		}
		violation := findStrictSchemaViolation(parameters, nil)
		if violation == nil {
			continue
		}
		name, _ := descriptor["name"].(string)
		if name == "" {
			name = "function"
		}
		message := fmt.Sprintf("Invalid schema for function '%s': In context=%s, %s.", name, renderStrictSchemaContext(violation.context), violation.reason)
		param := strings.ReplaceAll(paramTemplate, "{index}", fmt.Sprintf("%d", index))
		return NewClientPayloadError(message, param, "invalid_function_parameters", "invalid_request_error")
	}
	return nil
}

func EnforceStrictTextFormat(body map[string]any) error {
	text, ok := body["text"].(map[string]any)
	if !ok {
		return nil
	}
	format, ok := text["format"].(map[string]any)
	if !ok {
		return nil
	}
	return enforceStrictJSONSchemaFormat(format, "text.format.schema")
}

func EnforceStrictChatResponseFormat(body map[string]any) error {
	responseFormat, ok := body["response_format"].(map[string]any)
	if !ok {
		return nil
	}
	if responseFormat["type"] != "json_schema" {
		return nil
	}
	jsonSchema, ok := responseFormat["json_schema"].(map[string]any)
	if !ok {
		return nil
	}
	format := map[string]any{
		"type":   "json_schema",
		"name":   jsonSchema["name"],
		"schema": jsonSchema["schema"],
		"strict": jsonSchema["strict"],
	}
	return enforceStrictJSONSchemaFormat(format, "text.format.schema")
}

func ValidateChatCompletionsRequest(body map[string]any) error {
	if strings.TrimSpace(stringField(body, "model")) == "" {
		return NewClientPayloadError("model is required", "model", "invalid_request_error", "invalid_request_error")
	}
	messages, hasMessages := body["messages"].([]any)
	if !hasMessages || len(messages) == 0 {
		if _, hasInput := body["input"]; hasInput {
			return nil
		}
		return NewClientPayloadError("Provide either 'messages' or 'input'.", "", "invalid_request_error", "invalid_request_error")
	}
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			return chatMessagesPayloadError("Each message must be an object.")
		}
		role, ok := message["role"].(string)
		if !ok || role == "" {
			return chatMessagesPayloadError("Each message must include a string 'role'.")
		}
		if _, ok := supportedChatRoles[role]; !ok {
			return chatMessagesPayloadError(fmt.Sprintf("Unsupported message role: %s", role))
		}
		switch role {
		case "system", "developer":
			if err := ensureTextOnlyChatContent(message["content"], role); err != nil {
				return err
			}
		case "user":
			if err := validateChatUserContent(message["content"]); err != nil {
				return err
			}
			if err := rejectChatFileID(message["content"]); err != nil {
				return err
			}
		case "assistant":
			if err := validateAssistantToolCalls(message); err != nil {
				return err
			}
		case "tool":
			if err := validateToolMessage(message); err != nil {
				return err
			}
		}
	}
	return nil
}

func ValidateAndNormalizeChatTools(body map[string]any) error {
	responsesShapedPayload := isResponsesShapedChatPayload(body)
	tools, ok := body["tools"].([]any)
	if ok {
		normalized := make([]any, 0, len(tools))
		for _, rawTool := range tools {
			tool, ok := rawTool.(map[string]any)
			if !ok {
				normalized = append(normalized, rawTool)
				continue
			}
			toolType, _ := tool["type"].(string)
			normalizedType := normalizeToolType(toolType)
			if normalizedType != toolType {
				copyTool := copyMap(tool)
				copyTool["type"] = normalizedType
				tool = copyTool
				toolType = normalizedType
			}
			if !responsesShapedPayload {
				if _, unsupported := unsupportedChatBuiltinToolTypes[toolType]; unsupported {
					return NewClientPayloadError(fmt.Sprintf("Unsupported tool type: %s", toolType), "tools", "invalid_request_error", "invalid_request_error")
				}
			}
			normalized = append(normalized, tool)
		}
		body["tools"] = normalized
	}
	if toolChoice, ok := body["tool_choice"].(map[string]any); ok {
		toolType, _ := toolChoice["type"].(string)
		normalizedType := normalizeToolType(toolType)
		if normalizedType != toolType {
			copyChoice := copyMap(toolChoice)
			copyChoice["type"] = normalizedType
			body["tool_choice"] = copyChoice
		}
	}
	return nil
}

func ApplyChatResponseFormat(body map[string]any) error {
	responseFormat, ok := body["response_format"]
	if !ok || responseFormat == nil {
		return nil
	}
	text, ok := body["text"].(map[string]any)
	if ok {
		if format := text["format"]; format != nil {
			return NewClientPayloadError("Provide either 'response_format' or 'text.format', not both.", "response_format", "invalid_request_error", "invalid_request_error")
		}
	} else if body["text"] != nil {
		return NewClientPayloadError("'text' must be an object when using 'response_format'.", "text", "invalid_request_error", "invalid_request_error")
	} else {
		text = map[string]any{}
	}

	format, err := chatResponseFormatToTextFormat(responseFormat)
	if err != nil {
		return err
	}
	text["format"] = format
	body["text"] = text
	delete(body, "response_format")
	return nil
}

func chatResponseFormatToTextFormat(value any) (map[string]any, error) {
	if formatType, ok := value.(string); ok {
		return textFormatFromChatType(formatType)
	}
	responseFormat, ok := value.(map[string]any)
	if !ok {
		return nil, NewClientPayloadError("'response_format' must be a string or object.", "response_format", "invalid_request_error", "invalid_request_error")
	}
	formatType, _ := responseFormat["type"].(string)
	if formatType == "" {
		return nil, NewClientPayloadError("Unsupported response_format.type: ", "response_format", "invalid_request_error", "invalid_request_error")
	}
	if formatType == "json_schema" {
		jsonSchema, ok := responseFormat["json_schema"].(map[string]any)
		if !ok {
			return nil, NewClientPayloadError("'response_format.json_schema' is required when type is 'json_schema'.", "response_format.json_schema", "invalid_request_error", "invalid_request_error")
		}
		return map[string]any{
			"type":   "json_schema",
			"schema": jsonSchema["schema"],
			"name":   jsonSchema["name"],
			"strict": jsonSchema["strict"],
		}, nil
	}
	return textFormatFromChatType(formatType)
}

func textFormatFromChatType(formatType string) (map[string]any, error) {
	switch formatType {
	case "json_object", "text":
		return map[string]any{"type": formatType}, nil
	case "json_schema":
		return nil, NewClientPayloadError("'response_format' must include 'json_schema' when type is 'json_schema'.", "response_format", "invalid_request_error", "invalid_request_error")
	default:
		return nil, NewClientPayloadError(fmt.Sprintf("Unsupported response_format.type: %s", formatType), "response_format", "invalid_request_error", "invalid_request_error")
	}
}

func isResponsesShapedChatPayload(body map[string]any) bool {
	messages, ok := body["messages"].([]any)
	return (!ok || len(messages) == 0) && body["input"] != nil
}

func normalizeToolType(toolType string) string {
	if toolType == "web_search_preview" {
		return "web_search"
	}
	return toolType
}

func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func chatMessagesPayloadError(message string) *AppError {
	return NewClientPayloadError(message, "messages", "invalid_request_error", "invalid_request_error")
}

func ensureTextOnlyChatContent(content any, role string) error {
	if content == nil {
		return nil
	}
	if _, ok := content.(string); ok {
		return nil
	}
	for _, part := range contentParts(content) {
		if _, ok := part.(string); ok {
			continue
		}
		partMap, ok := part.(map[string]any)
		if !ok {
			return chatMessagesPayloadError(fmt.Sprintf("%s messages must be text-only.", role))
		}
		partType, _ := partMap["type"].(string)
		if partType != "" {
			if _, ok := textContentPartTypes[partType]; !ok {
				return chatMessagesPayloadError(fmt.Sprintf("%s messages must be text-only.", role))
			}
		}
		if _, ok := partMap["text"].(string); !ok {
			return chatMessagesPayloadError(fmt.Sprintf("%s messages must be text-only.", role))
		}
	}
	return nil
}

func validateChatUserContent(content any) error {
	if content == nil {
		return nil
	}
	if _, ok := content.(string); ok {
		return nil
	}
	for _, part := range contentParts(content) {
		if _, ok := part.(string); ok {
			continue
		}
		partMap, ok := part.(map[string]any)
		if !ok {
			return chatMessagesPayloadError("User message content parts must be objects.")
		}
		partType := chatPartType(partMap)
		if _, ok := textContentPartTypes[partType]; ok {
			if _, ok := partMap["text"].(string); !ok {
				return chatMessagesPayloadError("Text content parts must include a string 'text'.")
			}
			continue
		}
		switch partType {
		case "image_url":
			imageURL, ok := partMap["image_url"].(map[string]any)
			if !ok {
				return chatMessagesPayloadError("Image content parts must include image_url.url.")
			}
			if _, ok := imageURL["url"].(string); !ok {
				return chatMessagesPayloadError("Image content parts must include image_url.url.")
			}
		case "input_audio":
			return chatMessagesPayloadError("Audio input is not supported.")
		case "file":
			if _, ok := partMap["file"].(map[string]any); !ok {
				return chatMessagesPayloadError("File content parts must include file metadata.")
			}
		default:
			return chatMessagesPayloadError(fmt.Sprintf("Unsupported user content part type: %s", partType))
		}
	}
	return nil
}

func rejectChatFileID(content any) error {
	for _, part := range contentParts(content) {
		partMap, ok := part.(map[string]any)
		if !ok {
			continue
		}
		fileInfo, ok := partMap["file"].(map[string]any)
		if !ok {
			continue
		}
		if fileID, ok := fileInfo["file_id"].(string); ok && fileID != "" {
			return chatMessagesPayloadError("file_id is not supported")
		}
	}
	return nil
}

func validateToolMessage(message map[string]any) error {
	for _, key := range []string{"tool_call_id", "toolCallId", "call_id"} {
		if value, ok := message[key].(string); ok && value != "" {
			return nil
		}
	}
	return chatMessagesPayloadError("tool messages must include 'tool_call_id'.")
}

func validateAssistantToolCalls(message map[string]any) error {
	toolCalls, ok := message["tool_calls"].([]any)
	if !ok || len(toolCalls) == 0 {
		return nil
	}
	for _, rawToolCall := range toolCalls {
		toolCall, ok := rawToolCall.(map[string]any)
		if !ok {
			return chatMessagesPayloadError("tool_calls entries must be objects.")
		}
		if id, ok := toolCall["id"].(string); !ok || id == "" {
			return chatMessagesPayloadError("tool_calls[].id is required.")
		}
		function, ok := toolCall["function"].(map[string]any)
		if !ok {
			return chatMessagesPayloadError("tool_calls[].function is required.")
		}
		if name, ok := function["name"].(string); !ok || name == "" {
			return chatMessagesPayloadError("tool_calls[].function.name is required.")
		}
		if _, ok := function["arguments"].(string); !ok {
			return chatMessagesPayloadError("tool_calls[].function.arguments must be a string.")
		}
	}
	return nil
}

func contentParts(content any) []any {
	if items, ok := content.([]any); ok {
		return items
	}
	return []any{content}
}

func chatPartType(part map[string]any) string {
	if partType, ok := part["type"].(string); ok {
		return partType
	}
	if _, ok := part["text"]; ok {
		return "text"
	}
	return ""
}

func enforceStrictJSONSchemaFormat(format map[string]any, param string) error {
	if format["type"] != "json_schema" {
		return nil
	}
	if strict, ok := format["strict"].(bool); !ok || !strict {
		return nil
	}
	schema, ok := format["schema"]
	if !ok || schema == nil {
		return nil
	}
	violation := findStrictSchemaViolation(schema, nil)
	if violation == nil {
		return nil
	}
	name, _ := format["name"].(string)
	if name == "" {
		name = "response_format"
	}
	message := fmt.Sprintf("Invalid schema for response_format '%s': In context=%s, %s.", name, renderStrictSchemaContext(violation.context), violation.reason)
	return NewClientPayloadError(message, param, "invalid_json_schema", "invalid_request_error")
}

func findStrictSchemaViolation(node any, path []string) *strictSchemaViolation {
	schema, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	if _, hasType := schema["type"]; !hasType && !hasStrictSchemaCombinator(schema) && !isStrictSchemaRef(schema) {
		return &strictSchemaViolation{context: appendPath(nil, path...), reason: "schema must have a 'type' key"}
	}
	schemaType, _ := schema["type"].(string)
	if schemaType == "object" || schema["properties"] != nil {
		if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
			return &strictSchemaViolation{context: appendPath(nil, path...), reason: "'additionalProperties' is required to be supplied and to be false"}
		}
		if properties, ok := schema["properties"].(map[string]any); ok {
			required := stringSetFromAny(schema["required"])
			missing := make([]string, 0)
			for propName := range properties {
				if _, ok := required[propName]; !ok {
					missing = append(missing, propName)
				}
			}
			if len(missing) > 0 {
				quoted := make([]string, 0, len(missing))
				for _, name := range missing {
					quoted = append(quoted, fmt.Sprintf("'%s'", name))
				}
				return &strictSchemaViolation{
					context: appendPath(nil, path...),
					reason:  "'required' is required to be supplied and to be an array including every key in properties. Missing " + strings.Join(quoted, ", "),
				}
			}
			for propName, propSchema := range properties {
				if violation := findStrictSchemaViolation(propSchema, appendPath(path, "properties", propName)); violation != nil {
					return violation
				}
			}
		}
	}
	if schemaType == "array" || schema["items"] != nil {
		if items, ok := schema["items"]; ok && items != nil {
			if violation := findStrictSchemaViolation(items, appendPath(path, "items")); violation != nil {
				return violation
			}
		}
	}
	for _, combinator := range []string{"anyOf", "oneOf", "allOf"} {
		candidates, ok := schema[combinator].([]any)
		if !ok {
			continue
		}
		for index, candidate := range candidates {
			if violation := findStrictSchemaViolation(candidate, appendPath(path, combinator, fmt.Sprintf("%d", index))); violation != nil {
				return violation
			}
		}
	}
	defs, ok := schema["$defs"].(map[string]any)
	if !ok {
		defs, _ = schema["definitions"].(map[string]any)
	}
	for defName, defSchema := range defs {
		if violation := findStrictSchemaViolation(defSchema, appendPath(path, "$defs", defName)); violation != nil {
			return violation
		}
	}
	return nil
}

func hasStrictSchemaCombinator(schema map[string]any) bool {
	for _, name := range []string{"anyOf", "oneOf", "allOf"} {
		if _, ok := schema[name].([]any); ok {
			return true
		}
	}
	return false
}

func isStrictSchemaRef(schema map[string]any) bool {
	_, ok := schema["$ref"].(string)
	return ok
}

func stringSetFromAny(value any) map[string]struct{} {
	out := map[string]struct{}{}
	items, ok := value.([]any)
	if !ok {
		return out
	}
	for _, item := range items {
		if text, ok := item.(string); ok {
			out[text] = struct{}{}
		}
	}
	return out
}

func renderStrictSchemaContext(path []string) string {
	if len(path) == 0 {
		return "()"
	}
	quoted := make([]string, 0, len(path))
	for _, part := range path {
		quoted = append(quoted, fmt.Sprintf("'%s'", part))
	}
	return "(" + strings.Join(quoted, ", ") + ")"
}

func appendPath(base []string, parts ...string) []string {
	out := make([]string, 0, len(base)+len(parts))
	out = append(out, base...)
	out = append(out, parts...)
	return out
}
