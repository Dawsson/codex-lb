package proxy

import (
	"context"
	"net/http"

	"github.com/soju06/codex-lb/internal/upstream"
)

type compactUpstreamFunc func(ctx context.Context, opts upstream.StreamOptions) (map[string]any, error)

func (s *Service) CompactResponses(ctx context.Context, r *http.Request, apiKey *ApiKeyData, body map[string]any, codexSessionAffinity bool) (map[string]any, *OpenAIErrorEnvelope, int, error) {
	model := stringField(body, "model")
	if model == "" {
		envelope := OpenAIError("invalid_request", "model is required", "invalid_request_error")
		return nil, &envelope, http.StatusBadRequest, nil
	}
	if body["input"] == nil {
		messages, ok := body["messages"]
		if !ok {
			envelope := OpenAIError("invalid_request", "Provide either 'input' or 'messages'.", "invalid_request_error")
			return nil, &envelope, http.StatusBadRequest, nil
		}
		body["input"] = messages
		delete(body, "messages")
	}

	body = compactPayload(body)
	effectiveModel := EffectiveModelForAPIKey(apiKey, model)
	body["model"] = effectiveModel
	policyPayload := chatBodyToResponsesPayload(body)
	policyPayload.Model = effectiveModel
	ApplyAPIKeyEnforcement(&policyPayload, apiKey)
	body = responsesPayloadToMap(policyPayload, body)
	model = stringField(body, "model")
	if err := ValidateModelAccess(apiKey, model); err != nil {
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
	reservation, err := s.reserveAPIKeyUsage(ctx, apiKey, model, body)
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			envelope := OpenAIError(appErr.Code, appErr.Message, appErr.ErrorType)
			return nil, &envelope, appErr.StatusCode, nil
		}
		return nil, nil, http.StatusInternalServerError, err
	}

	selectParams := streamSelectParams(model, dashboardSettings, apiKey)
	selectParams.LeaseKind = AccountLeaseKindResponseCreate
	stickyPolicy := StickyPolicyForResponsesRequest(
		body,
		r.Header,
		codexSessionAffinity,
		true,
		dashboardSettings.OpenAICacheAffinityMaxAgeSeconds,
		dashboardSettings.StickyThreadsEnabled,
	)
	if stickyPolicy.Key != "" && stickyPolicy.Kind != "" {
		accountID, err := s.stickySessionsRepo.GetAccountID(ctx, stickyPolicy.Key, stickyPolicy.Kind, stickyPolicy.MaxAgeSeconds)
		if err != nil {
			s.releaseAPIKeyReservation(ctx, reservation)
			return nil, nil, http.StatusInternalServerError, err
		}
		if accountID != "" {
			selectParams.PreferredAccountID = &accountID
		}
	}

	selection, err := s.loadBalancer.SelectAccount(ctx, selectParams)
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
		envelope := OpenAIError(selectionErrorCode(selection.ErrorCode), message, "server_error")
		return nil, &envelope, http.StatusServiceUnavailable, nil
	}
	if stickyPolicy.Key != "" && stickyPolicy.Kind != "" {
		_ = s.stickySessionsRepo.Upsert(ctx, stickyPolicy.Key, selection.Account.ID, stickyPolicy.Kind)
	}

	result, err := s.compactUpstream(ctx, upstream.StreamOptions{
		BaseURL:        s.upstreamBaseURL,
		Payload:        body,
		InboundHeaders: r.Header.Clone(),
		AccessToken:    selection.Account.AccessToken,
		AccountID:      selection.Account.ID,
		Client:         s.upstreamClient,
	})
	if err != nil {
		envelope := OpenAIError("upstream_error", err.Error(), "server_error")
		s.failAPIKeyReservation(reservation, model, result, serviceTierFromBody(body))
		s.logRequest(ctx, logRequestParams{
			requestID: r.Header.Get("X-Request-Id"),
			model:     model,
			account:   selection.Account,
			apiKey:    apiKey,
			status:    "error",
			envelope:  &envelope,
			result:    result,
			userAgent: r.UserAgent(),
		})
		return nil, &envelope, http.StatusBadGateway, nil
	}

	s.finalizeAPIKeyReservation(reservation, model, result, serviceTierFromBody(body))
	s.logRequest(ctx, logRequestParams{
		requestID: r.Header.Get("X-Request-Id"),
		model:     model,
		account:   selection.Account,
		apiKey:    apiKey,
		status:    "success",
		result:    result,
		userAgent: r.UserAgent(),
	})
	return result, nil, http.StatusOK, nil
}

func (s *Service) compactUpstream(ctx context.Context, opts upstream.StreamOptions) (map[string]any, error) {
	call := s.compactSubmitter
	if call == nil {
		call = func(ctx context.Context, opts upstream.StreamOptions) (map[string]any, error) {
			return upstream.CompactResponses(ctx, opts.Client, opts.BaseURL, opts.Payload, opts.InboundHeaders, opts.AccessToken, opts.AccountID)
		}
	}
	return call(ctx, opts)
}

func compactPayload(body map[string]any) map[string]any {
	out := make(map[string]any, len(body))
	for key, value := range body {
		switch key {
		case "store", "tools", "tool_choice", "parallel_tool_calls", "stream":
			continue
		default:
			out[key] = value
		}
	}
	return out
}

func serviceTierFromBody(body map[string]any) string {
	if tier, _ := body["service_tier"].(string); tier != "" {
		return tier
	}
	return ""
}

func selectionErrorCode(code string) string {
	if code == "" {
		return "no_available_accounts"
	}
	return code
}
