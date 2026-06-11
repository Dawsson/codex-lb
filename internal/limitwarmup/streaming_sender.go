package limitwarmup

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/crypto"
	"github.com/soju06/codex-lb/internal/proxy"
	"github.com/soju06/codex-lb/internal/upstream"
)

type StreamingSender struct {
	encryptor *crypto.Encryptor
	baseURL   string
	client    *http.Client
}

func NewStreamingSender(encryptor *crypto.Encryptor, cfg config.Config) *StreamingSender {
	return &StreamingSender{
		encryptor: encryptor,
		baseURL:   cfg.UpstreamBaseURL,
		client: &http.Client{
			Timeout: 0,
		},
	}
}

func (s *StreamingSender) WithHTTPClient(client *http.Client) *StreamingSender {
	s.client = client
	return s
}

func (s *StreamingSender) Send(ctx context.Context, account accounts.Account, params SendParams) (SendResult, error) {
	started := time.Now()
	if strings.ToLower(account.Status) != "active" {
		return SendResult{
			RequestID:    params.RequestID,
			Success:      false,
			LatencyMS:    elapsedMS(started),
			ErrorCode:    "account_not_active",
			ErrorMessage: fmt.Sprintf("Account status is %s", account.Status),
		}, nil
	}
	if s.encryptor == nil {
		return SendResult{}, fmt.Errorf("limit warmup streaming sender requires encryptor")
	}
	accessToken, err := s.encryptor.Decrypt(account.AccessTokenEncrypted)
	if err != nil {
		return SendResult{}, err
	}

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	payload := map[string]any{
		"model":               params.Model,
		"instructions":        firstNonEmpty(params.Instructions, defaultWarmupInstructions),
		"input":               params.Prompt,
		"tools":               []any{},
		"parallel_tool_calls": false,
		"stream":              true,
		"store":               false,
		"max_output_tokens":   4,
	}
	headers := http.Header{}
	headers.Set("X-Request-Id", params.RequestID)
	headers.Set(Header, "1")
	headers.Set("User-Agent", "codex-lb-limit-warmup")

	chatGPTAccountID := nullStringValue(account.ChatGPTAccountID)
	events, errs := upstream.OpenResponseStream(reqCtx, upstream.StreamOptions{
		BaseURL:        s.baseURL,
		Payload:        payload,
		InboundHeaders: headers,
		AccessToken:    accessToken,
		AccountID:      chatGPTAccountID,
		Transport:      upstream.TransportHTTP,
		Client:         s.client,
	})

	var usage responseUsage
	for event := range events {
		payload := proxy.ParseSSEDataJSON(event)
		if payload == nil {
			continue
		}
		usage.mergeFromPayload(payload)
		eventType, _ := payload["type"].(string)
		switch eventType {
		case "response.completed":
			return SendResult{
				RequestID:    params.RequestID,
				Success:      true,
				LatencyMS:    elapsedMS(started),
				InputTokens:  usage.inputTokensPtr(),
				OutputTokens: usage.outputTokensPtr(),
				Transport:    "http",
			}, nil
		case "response.failed", "response.incomplete", "error":
			code, message := eventError(payload)
			if code == "" {
				code = eventType
			}
			if message == "" {
				message = eventType
			}
			return SendResult{
				RequestID:     params.RequestID,
				Success:       false,
				LatencyMS:     elapsedMS(started),
				InputTokens:   usage.inputTokensPtr(),
				OutputTokens:  usage.outputTokensPtr(),
				ErrorCode:     code,
				ErrorMessage:  message,
				Transport:     "http",
				RequestedTier: stringPtrFromMap(payload, "service_tier"),
			}, nil
		}
	}

	select {
	case err, ok := <-errs:
		if ok && err != nil {
			return SendResult{}, err
		}
	default:
	}
	return SendResult{
		RequestID:    params.RequestID,
		Success:      false,
		LatencyMS:    elapsedMS(started),
		InputTokens:  usage.inputTokensPtr(),
		OutputTokens: usage.outputTokensPtr(),
		ErrorCode:    "stream_incomplete",
		ErrorMessage: "Warm-up stream ended without a terminal event",
		Transport:    "http",
	}, nil
}

type responseUsage struct {
	inputTokens  int64
	outputTokens int64
	hasInput     bool
	hasOutput    bool
}

func (u *responseUsage) mergeFromPayload(payload map[string]any) {
	response, _ := payload["response"].(map[string]any)
	if response == nil {
		return
	}
	rawUsage, _ := response["usage"].(map[string]any)
	if rawUsage == nil {
		return
	}
	if value, ok := int64FromAny(rawUsage["input_tokens"]); ok {
		u.inputTokens = value
		u.hasInput = true
	}
	if value, ok := int64FromAny(rawUsage["output_tokens"]); ok {
		u.outputTokens = value
		u.hasOutput = true
	}
}

func (u responseUsage) inputTokensPtr() *int64 {
	if !u.hasInput {
		return nil
	}
	return &u.inputTokens
}

func (u responseUsage) outputTokensPtr() *int64 {
	if !u.hasOutput {
		return nil
	}
	return &u.outputTokens
}

func eventError(payload map[string]any) (string, string) {
	for _, key := range []string{"error"} {
		if code, message := errorFields(payload[key]); code != "" || message != "" {
			return code, message
		}
	}
	if response, _ := payload["response"].(map[string]any); response != nil {
		return errorFields(response["error"])
	}
	return "", ""
}

func errorFields(value any) (string, string) {
	errMap, _ := value.(map[string]any)
	if errMap == nil {
		return "", ""
	}
	code, _ := errMap["code"].(string)
	message, _ := errMap["message"].(string)
	return code, message
}

func int64FromAny(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	default:
		return 0, false
	}
}

func elapsedMS(started time.Time) int64 {
	elapsed := time.Since(started).Milliseconds()
	if elapsed < 0 {
		return 0
	}
	return elapsed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func nullStringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func stringPtrFromMap(payload map[string]any, key string) *string {
	value, _ := payload[key].(string)
	if value == "" {
		return nil
	}
	return &value
}
