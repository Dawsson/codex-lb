package proxy

import (
	"context"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/soju06/codex-lb/internal/upstream"
)

func (s *Service) GenerateImage(ctx context.Context, r *http.Request, apiKey *ApiKeyData, body map[string]any) (map[string]any, int, *OpenAIErrorEnvelope, error) {
	prompt := stringField(body, "prompt")
	if prompt == "" {
		envelope := OpenAIError("invalid_request_error", "prompt is required", "invalid_request_error")
		return nil, http.StatusBadRequest, &envelope, nil
	}
	if stream, _ := body["stream"].(bool); stream {
		envelope := OpenAIError("invalid_request_error", "streaming image generation is not available in the Go API yet", "invalid_request_error")
		return nil, http.StatusBadRequest, &envelope, nil
	}
	if n := asInt64(body["n"]); n > 1 {
		envelope := OpenAIError("invalid_request_error", "n > 1 is not supported by codex-lb image generation", "invalid_request_error")
		return nil, http.StatusBadRequest, &envelope, nil
	}
	imageModel := stringField(body, "model")
	if imageModel == "" {
		imageModel = "gpt-image-1"
	}
	if !stringsHasPrefix(imageModel, "gpt-image-") {
		envelope := OpenAIError("invalid_request_error", "Unsupported image model '"+imageModel+"'. Use a 'gpt-image-*' model.", "invalid_request_error")
		return nil, http.StatusBadRequest, &envelope, nil
	}

	hostModel := "gpt-5.5"
	responsesPayload := map[string]any{
		"model":        hostModel,
		"instructions": "Generate an image using the image_generation tool.",
		"input": []any{map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": prompt},
			},
		}},
		"tools":       []any{imageToolFromBody(body, imageModel)},
		"tool_choice": map[string]any{"type": "image_generation"},
		"stream":      true,
		"store":       false,
	}
	return s.callImageResponses(ctx, r, apiKey, responsesPayload, hostModel)
}

func (s *Service) EditImage(ctx context.Context, r *http.Request, apiKey *ApiKeyData, body map[string]any, images []imagePart, mask *imagePart) (map[string]any, int, *OpenAIErrorEnvelope, error) {
	prompt := stringField(body, "prompt")
	if prompt == "" {
		envelope := OpenAIError("invalid_request_error", "prompt is required", "invalid_request_error")
		return nil, http.StatusBadRequest, &envelope, nil
	}
	if len(images) == 0 {
		envelope := OpenAIError("invalid_request_error", "At least one `image` (or `image[]`) multipart part is required.", "invalid_request_error")
		return nil, http.StatusBadRequest, &envelope, nil
	}
	if stream, _ := body["stream"].(bool); stream {
		envelope := OpenAIError("invalid_request_error", "streaming image edits are not available in the Go API yet", "invalid_request_error")
		return nil, http.StatusBadRequest, &envelope, nil
	}
	if n := asInt64(body["n"]); n > 1 {
		envelope := OpenAIError("invalid_request_error", "n > 1 is not supported by codex-lb image edits", "invalid_request_error")
		return nil, http.StatusBadRequest, &envelope, nil
	}
	imageModel := stringField(body, "model")
	if imageModel == "" {
		imageModel = "gpt-image-1"
	}
	if !stringsHasPrefix(imageModel, "gpt-image-") {
		envelope := OpenAIError("invalid_request_error", "Unsupported image model '"+imageModel+"'. Use a 'gpt-image-*' model.", "invalid_request_error")
		return nil, http.StatusBadRequest, &envelope, nil
	}
	content := []any{map[string]any{"type": "input_text", "text": prompt}}
	for _, image := range images {
		if len(image.Data) == 0 {
			envelope := OpenAIError("invalid_request_error", "image part is empty", "invalid_request_error")
			return nil, http.StatusBadRequest, &envelope, nil
		}
		content = append(content, inputImagePart(image))
	}
	if mask != nil {
		if len(mask.Data) == 0 {
			envelope := OpenAIError("invalid_request_error", "mask part is empty", "invalid_request_error")
			return nil, http.StatusBadRequest, &envelope, nil
		}
		content = append(content, inputImagePart(*mask))
	}
	hostModel := "gpt-5.5"
	tool := imageToolFromBody(body, imageModel)
	tool["action"] = "edit"
	if fidelity := stringField(body, "input_fidelity"); fidelity != "" {
		tool["input_fidelity"] = fidelity
	}
	responsesPayload := map[string]any{
		"model":        hostModel,
		"instructions": "Generate or edit an image using the image_generation tool.",
		"input": []any{map[string]any{
			"type":    "message",
			"role":    "user",
			"content": content,
		}},
		"tools":       []any{tool},
		"tool_choice": map[string]any{"type": "image_generation"},
		"stream":      true,
		"store":       false,
	}
	return s.callImageResponses(ctx, r, apiKey, responsesPayload, hostModel)
}

func (s *Service) callImageResponses(ctx context.Context, r *http.Request, apiKey *ApiKeyData, responsesPayload map[string]any, hostModel string) (map[string]any, int, *OpenAIErrorEnvelope, error) {
	dashboardSettings, err := s.settingsRepo.Get(ctx)
	if err != nil {
		return nil, 0, nil, err
	}
	reservation, err := s.reserveAPIKeyUsage(ctx, apiKey, hostModel, responsesPayload)
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			envelope := OpenAIError(appErr.Code, appErr.Message, appErr.ErrorType)
			return nil, appErr.StatusCode, &envelope, nil
		}
		return nil, 0, nil, err
	}
	selectParams := streamSelectParams(hostModel, dashboardSettings, apiKey)
	selectParams.LeaseKind = AccountLeaseKindResponseCreate
	selection, err := s.loadBalancer.SelectAccount(ctx, selectParams)
	if err != nil {
		s.releaseAPIKeyReservation(ctx, reservation)
		return nil, 0, nil, err
	}
	defer s.loadBalancer.ReleaseLease(selection.Lease)
	if selection.ErrorCode != "" || selection.Account == nil {
		s.releaseAPIKeyReservation(ctx, reservation)
		message := selection.ErrorMessage
		if message == "" {
			message = "No available accounts"
		}
		envelope := OpenAIError(selectionErrorCode(selection.ErrorCode), message, "server_error")
		return nil, http.StatusServiceUnavailable, &envelope, nil
	}

	started := time.Now()
	result, err := collectUpstreamResponse(ctx, upstream.StreamOptions{
		BaseURL:        s.upstreamBaseURL,
		Payload:        responsesPayload,
		InboundHeaders: r.Header.Clone(),
		AccessToken:    selection.Account.AccessToken,
		AccountID:      selection.Account.ID,
		Client:         s.upstreamClient,
	})
	latency := time.Since(started).Milliseconds()
	status := "success"
	var envelope *OpenAIErrorEnvelope
	if err != nil {
		status = "error"
		envelopeValue := OpenAIError("upstream_error", err.Error(), "server_error")
		envelope = &envelopeValue
	}
	s.logRequest(ctx, logRequestParams{
		requestID: r.Header.Get("X-Request-Id"),
		model:     hostModel,
		account:   selection.Account,
		apiKey:    apiKey,
		status:    status,
		envelope:  envelope,
		result:    result,
		latencyMS: latency,
		userAgent: r.UserAgent(),
	})
	if err != nil {
		s.failAPIKeyReservation(reservation, hostModel, result, "")
		return nil, http.StatusBadGateway, envelope, nil
	}
	s.finalizeAPIKeyReservation(reservation, hostModel, result, "")
	imageResponse, imageEnvelope := imageResponseFromResponses(result)
	if imageEnvelope != nil {
		return nil, http.StatusBadGateway, imageEnvelope, nil
	}
	return imageResponse, http.StatusOK, nil, nil
}

func imageToolFromBody(body map[string]any, imageModel string) map[string]any {
	tool := map[string]any{
		"type":               "image_generation",
		"model":              imageModel,
		"size":               stringOrDefault(body, "size", "auto"),
		"quality":            stringOrDefault(body, "quality", "auto"),
		"background":         stringOrDefault(body, "background", "auto"),
		"output_format":      stringOrDefault(body, "output_format", "png"),
		"output_compression": intOrDefault(body, "output_compression", 100),
		"moderation":         stringOrDefault(body, "moderation", "auto"),
	}
	return tool
}

type imagePart struct {
	Data        []byte
	ContentType string
}

func inputImagePart(part imagePart) map[string]any {
	contentType := part.ContentType
	if contentType == "" {
		contentType = "image/png"
	}
	return map[string]any{
		"type":      "input_image",
		"image_url": "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(part.Data),
	}
}

func imageResponseFromResponses(response map[string]any) (map[string]any, *OpenAIErrorEnvelope) {
	output, _ := response["output"].([]any)
	if len(output) == 0 {
		envelope := OpenAIError("image_generation_failed", "Upstream response did not include an output array", "server_error")
		return nil, &envelope
	}
	data := make([]any, 0, len(output))
	for _, item := range output {
		entry, ok := item.(map[string]any)
		if !ok || entry["type"] != "image_generation_call" {
			continue
		}
		if status, _ := entry["status"].(string); status == "failed" {
			envelope := OpenAIError("image_generation_failed", "Image generation failed", "server_error")
			return nil, &envelope
		}
		result, _ := entry["result"].(string)
		if result == "" {
			continue
		}
		image := map[string]any{"b64_json": result}
		if revised, _ := entry["revised_prompt"].(string); revised != "" {
			image["revised_prompt"] = revised
		}
		data = append(data, image)
	}
	if len(data) == 0 {
		envelope := OpenAIError("image_generation_failed", "Upstream image_generation_call items contained no image data", "server_error")
		return nil, &envelope
	}
	return map[string]any{
		"created": time.Now().Unix(),
		"data":    data,
	}, nil
}

func stringOrDefault(body map[string]any, key string, fallback string) string {
	if value, _ := body[key].(string); value != "" {
		return value
	}
	return fallback
}

func intOrDefault(body map[string]any, key string, fallback int64) int64 {
	if value := asInt64(body[key]); value > 0 {
		return value
	}
	return fallback
}

func stringsHasPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}
