package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/requestlogs"
	"github.com/soju06/codex-lb/internal/settings"
	"github.com/soju06/codex-lb/internal/stickysessions"
	"github.com/soju06/codex-lb/internal/upstream"
)

// Service orchestrates proxy requests: account selection, upstream forwarding,
// and request logging.
type Service struct {
	loadBalancer          *LoadBalancer
	settingsRepo          settings.Repository
	requestLogsRepo       requestlogs.Repository
	apiKeysRepo           apikeys.Repository
	stickySessionsRepo    stickysessions.Repository
	modelRegistry         *ModelRegistry
	previousResponseIndex *PreviousResponseIndex
	upstreamClient        *http.Client
	upstreamBaseURL       string
	warmupSubmitter       warmupUpstreamFunc
	compactSubmitter      compactUpstreamFunc
	filePinsMu            sync.Mutex
	filePins              map[string]filePin
}

type filePin struct {
	AccountID string
	ExpiresAt time.Time
}

const fileAccountPinTTL = 30 * time.Minute

func NewService(
	loadBalancer *LoadBalancer,
	settingsRepo settings.Repository,
	requestLogsRepo requestlogs.Repository,
	apiKeysRepo apikeys.Repository,
	stickySessionsRepo stickysessions.Repository,
	modelRegistry *ModelRegistry,
	upstreamBaseURL string,
) *Service {
	if upstreamBaseURL == "" {
		upstreamBaseURL = upstream.DefaultBaseURL
	}
	return &Service{
		loadBalancer:          loadBalancer,
		settingsRepo:          settingsRepo,
		requestLogsRepo:       requestLogsRepo,
		apiKeysRepo:           apiKeysRepo,
		stickySessionsRepo:    stickySessionsRepo,
		modelRegistry:         modelRegistry,
		previousResponseIndex: NewPreviousResponseIndex(),
		upstreamClient:        &http.Client{Timeout: 0},
		upstreamBaseURL:       upstreamBaseURL,
		filePins:              make(map[string]filePin),
	}
}

type ChatCompletionRequest struct {
	Raw map[string]any
}

func (s *Service) CompleteChat(ctx context.Context, r *http.Request, apiKey *ApiKeyData, body map[string]any) (map[string]any, *OpenAIErrorEnvelope, int, error) {
	started := time.Now()
	model := stringField(body, "model")
	if model == "" {
		envelope := OpenAIError("invalid_request", "model is required", "invalid_request_error")
		return nil, &envelope, http.StatusBadRequest, nil
	}

	effectiveModel := EffectiveModelForAPIKey(apiKey, model)
	if err := ValidateModelAccess(apiKey, effectiveModel); err != nil {
		if appErr, ok := err.(*AppError); ok {
			envelope := OpenAIError(appErr.Code, appErr.Message, appErr.ErrorType)
			return nil, &envelope, appErr.StatusCode, nil
		}
		return nil, nil, http.StatusInternalServerError, err
	}

	dashboardSettings, err := s.settingsRepo.Get(ctx)
	if err != nil {
		return nil, nil, http.StatusInternalServerError, err
	}

	responsesPayload := chatBodyToResponsesPayload(body)
	responsesPayload.Model = effectiveModel
	ApplyAPIKeyEnforcement(&responsesPayload, apiKey)
	upstreamPayload := responsesPayloadToMap(responsesPayload, body)

	reservation, err := s.reserveAPIKeyUsage(ctx, apiKey, responsesPayload.Model, upstreamPayload)
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			envelope := OpenAIError(appErr.Code, appErr.Message, appErr.ErrorType)
			return nil, &envelope, appErr.StatusCode, nil
		}
		return nil, nil, http.StatusInternalServerError, err
	}

	selection, err := s.loadBalancer.SelectAccount(ctx, SelectAccountParams{
		Model:                      responsesPayload.Model,
		PreferEarlierResetAccounts: dashboardSettings.PreferEarlierResetAccounts,
		PreferEarlierResetWindow:   ResetPreferenceWindow(dashboardSettings.PreferEarlierResetWindow),
		RoutingStrategy:            RoutingStrategy(dashboardSettings.RoutingStrategy),
		RelativeAvailabilityPower:  dashboardSettings.RelativeAvailabilityPower,
		RelativeAvailabilityTopK:   dashboardSettings.RelativeAvailabilityTopK,
		LeaseKind:                  AccountLeaseKindResponseCreate,
		TrafficClass:               trafficClassForKey(apiKey),
		SingleAccountID:            dashboardSettings.SingleAccountID,
	})
	if err != nil {
		s.releaseAPIKeyReservation(ctx, reservation)
		return nil, nil, http.StatusInternalServerError, err
	}
	defer s.loadBalancer.ReleaseLease(selection.Lease)

	if selection.ErrorCode != "" || selection.Account == nil {
		s.releaseAPIKeyReservation(ctx, reservation)
		message := selection.ErrorMessage
		if message == "" {
			message = "No available accounts"
		}
		code := selection.ErrorCode
		if code == "" {
			code = "no_available_accounts"
		}
		status := http.StatusServiceUnavailable
		if code == noPlanSupportForModel {
			status = http.StatusBadRequest
		}
		envelope := OpenAIError(code, message, "server_error")
		return nil, &envelope, status, nil
	}

	events, errs := upstream.OpenResponseStream(ctx, upstream.StreamOptions{
		BaseURL:          s.upstreamBaseURL,
		Payload:          upstreamPayload,
		InboundHeaders:   r.Header.Clone(),
		AccessToken:      selection.Account.AccessToken,
		AccountID:        selection.Account.ID,
		Transport:        upstream.Transport(dashboardSettings.UpstreamStreamTransport),
		PrefersWebSocket: s.modelRegistry.PrefersWebsockets(responsesPayload.Model),
		Client:           s.upstreamClient,
	})

	result, envelope, err := CollectChatCompletion(events, errs, responsesPayload.Model)
	latencyMS := time.Since(started).Milliseconds()
	s.logRequest(ctx, logRequestParams{
		requestID: r.Header.Get("X-Request-Id"),
		model:     responsesPayload.Model,
		account:   selection.Account,
		apiKey:    apiKey,
		status:    logStatus(envelope, err),
		envelope:  envelope,
		result:    result,
		latencyMS: latencyMS,
		userAgent: r.UserAgent(),
	})

	if err != nil {
		s.failAPIKeyReservation(reservation, responsesPayload.Model, result, "")
		envelope := OpenAIError("upstream_error", err.Error(), "server_error")
		return nil, &envelope, http.StatusBadGateway, nil
	}
	if envelope != nil {
		s.failAPIKeyReservation(reservation, responsesPayload.Model, result, "")
		status := http.StatusBadGateway
		if envelope.Error.Code == noPlanSupportForModel {
			status = http.StatusBadRequest
		}
		return nil, envelope, status, nil
	}
	s.finalizeAPIKeyReservation(reservation, responsesPayload.Model, result, "")
	return result, nil, http.StatusOK, nil
}

type logRequestParams struct {
	requestID string
	model     string
	account   *ProxyAccount
	apiKey    *ApiKeyData
	status    string
	envelope  *OpenAIErrorEnvelope
	result    map[string]any
	latencyMS int64
	userAgent string
}

func (s *Service) logRequest(ctx context.Context, params logRequestParams) {
	requestID := params.requestID
	if requestID == "" {
		requestID = fmt.Sprintf("req_%d", time.Now().UnixNano())
	}
	var inputTokens, outputTokens *int64
	if params.result != nil {
		if usage, ok := params.result["usage"].(map[string]any); ok {
			if prompt := asInt64(usage["prompt_tokens"]); prompt > 0 {
				inputTokens = &prompt
			}
			if completion := asInt64(usage["completion_tokens"]); completion > 0 {
				outputTokens = &completion
			}
		}
	}
	var errorCode, errorMessage *string
	if params.envelope != nil {
		code := params.envelope.Error.Code
		message := params.envelope.Error.Message
		errorCode = &code
		errorMessage = &message
	}
	accountID := params.account.ID
	planType := params.account.PlanType
	var apiKeyID *string
	if params.apiKey != nil {
		apiKeyID = &params.apiKey.ID
	}
	latency := params.latencyMS
	transport := "http"
	userAgent := params.userAgent
	_ = s.requestLogsRepo.Insert(ctx, requestlogs.InsertParams{
		RequestID:    requestID,
		RequestKind:  "normal",
		Model:        params.model,
		AccountID:    &accountID,
		PlanType:     &planType,
		APIKeyID:     apiKeyID,
		Status:       params.status,
		ErrorCode:    errorCode,
		ErrorMessage: errorMessage,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		LatencyMS:    &latency,
		UserAgent:    &userAgent,
		Transport:    &transport,
	})
}

func logStatus(envelope *OpenAIErrorEnvelope, err error) string {
	if err != nil || envelope != nil {
		return "error"
	}
	return "success"
}

func trafficClassForKey(apiKey *ApiKeyData) string {
	if apiKey != nil && apiKey.TrafficClass != "" {
		return apiKey.TrafficClass
	}
	return TrafficClassForeground
}

func stringField(body map[string]any, key string) string {
	if value, ok := body[key].(string); ok {
		return value
	}
	return ""
}

func asInt64(value any) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	default:
		return 0
	}
}

func (s *Service) reserveAPIKeyUsage(ctx context.Context, apiKey *ApiKeyData, model string, body map[string]any) (*apikeys.UsageReservation, error) {
	if apiKey == nil {
		return nil, nil
	}
	inputBudget := estimateRequestInputTokens(body)
	outputBudget := int64(2048)
	serviceTier := stringField(body, "service_tier")
	reservation, err := s.apiKeysRepo.EnforceRequestLimits(ctx, apiKey.ID, model, serviceTier, apikeys.UsageBudget{
		InputTokens:  inputBudget,
		OutputTokens: &outputBudget,
	})
	if err != nil {
		return nil, NewProxyRateLimitError(err.Error())
	}
	return reservation, nil
}

func (s *Service) releaseAPIKeyReservation(ctx context.Context, reservation *apikeys.UsageReservation) {
	if reservation == nil {
		return
	}
	_ = s.apiKeysRepo.ReleaseUsageReservation(ctx, reservation.ID)
}

func (s *Service) finalizeAPIKeyReservation(reservation *apikeys.UsageReservation, model string, result map[string]any, serviceTier string) {
	if reservation == nil {
		return
	}
	settlement := usageSettlementFromResult(model, result, serviceTier)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.apiKeysRepo.FinalizeUsageReservation(ctx, reservation.ID, settlement)
}

func (s *Service) failAPIKeyReservation(reservation *apikeys.UsageReservation, model string, result map[string]any, serviceTier string) {
	if reservation == nil {
		return
	}
	settlement := usageSettlementFromResult(model, result, serviceTier)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.apiKeysRepo.FailUsageReservation(ctx, reservation.ID, settlement)
}

func (s *Service) startAPIKeyReservationHeartbeat(reservation *apikeys.UsageReservation) context.CancelFunc {
	if reservation == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				touchCtx, touchCancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, _ = s.apiKeysRepo.TouchUsageReservation(touchCtx, reservation.ID)
				touchCancel()
			}
		}
	}()
	return cancel
}

func usageSettlementFromResult(model string, result map[string]any, serviceTier string) apikeys.UsageSettlement {
	settlement := apikeys.UsageSettlement{Model: model, ServiceTier: serviceTier}
	if result == nil {
		return settlement
	}
	usage, _ := result["usage"].(map[string]any)
	if usage == nil {
		return settlement
	}
	input := asInt64(usage["prompt_tokens"])
	if input == 0 {
		input = asInt64(usage["input_tokens"])
	}
	output := asInt64(usage["completion_tokens"])
	if output == 0 {
		output = asInt64(usage["output_tokens"])
	}
	cached := int64(0)
	if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
		cached = asInt64(details["cached_tokens"])
	}
	if cached == 0 {
		if details, ok := usage["input_tokens_details"].(map[string]any); ok {
			cached = asInt64(details["cached_tokens"])
		}
	}
	if input > 0 {
		settlement.InputTokens = &input
	}
	if output > 0 {
		settlement.OutputTokens = &output
	}
	if cached > 0 {
		settlement.CachedInputTokens = &cached
	}
	return settlement
}

func estimateRequestInputTokens(body map[string]any) *int64 {
	if body["previous_response_id"] != nil || body["conversation"] != nil || containsOpaqueInputReference(body["input"]) {
		return nil
	}
	data := make(map[string]any, len(body))
	for key, value := range body {
		switch key {
		case "model", "service_tier", "stream", "store", "max_output_tokens", "max_completion_tokens", "max_tokens":
			continue
		default:
			data[key] = value
		}
	}
	serialized, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	value := int64(len(serialized))
	if value > 8192 {
		value = 8192
	}
	return &value
}

func containsOpaqueInputReference(value any) bool {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if containsOpaqueInputReference(item) {
				return true
			}
		}
	case map[string]any:
		if typeName, _ := typed["type"].(string); typeName == "input_file" || typeName == "input_image" {
			return true
		}
		if _, ok := typed["file_id"]; ok {
			return true
		}
		for _, child := range typed {
			if containsOpaqueInputReference(child) {
				return true
			}
		}
	}
	return false
}

func chatBodyToResponsesPayload(body map[string]any) ResponsesPayload {
	payload := ResponsesPayload{
		Model: stringField(body, "model"),
		Extra: map[string]any{},
	}
	if reasoning, ok := body["reasoning"].(map[string]any); ok {
		if effort, ok := reasoning["effort"].(string); ok {
			payload.Reasoning = &ReasoningPayload{Effort: &effort}
		}
	}
	if tier, ok := body["service_tier"].(string); ok {
		payload.ServiceTier = &tier
	}
	return payload
}

func responsesPayloadToMap(payload ResponsesPayload, original map[string]any) map[string]any {
	out := make(map[string]any, len(original)+4)
	for key, value := range original {
		out[key] = value
	}
	out["model"] = payload.Model
	out["stream"] = true
	if payload.Reasoning != nil {
		reasoning := map[string]any{}
		if payload.Reasoning.Effort != nil {
			reasoning["effort"] = *payload.Reasoning.Effort
		}
		out["reasoning"] = reasoning
	}
	if payload.ServiceTier != nil {
		out["service_tier"] = *payload.ServiceTier
	} else {
		delete(out, "service_tier")
	}
	if _, ok := out["input"]; !ok {
		if messages, ok := original["messages"].([]any); ok {
			out["input"] = messagesToResponsesInput(messages)
			delete(out, "messages")
		}
	}
	if _, ok := out["instructions"]; !ok {
		out["instructions"] = ""
	}
	return out
}

func messagesToResponsesInput(messages []any) []any {
	items := make([]any, 0, len(messages))
	for _, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := message["role"].(string)
		content := message["content"]
		item := map[string]any{
			"type": "message",
			"role": role,
		}
		switch typed := content.(type) {
		case string:
			item["content"] = []any{map[string]any{"type": "input_text", "text": typed}}
		default:
			item["content"] = content
		}
		items = append(items, item)
	}
	return items
}
