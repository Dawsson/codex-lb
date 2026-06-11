package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/soju06/codex-lb/internal/settings"
	"github.com/soju06/codex-lb/internal/upstream"
)

type StreamResponsesOptions struct {
	CodexSessionAffinity  bool
	EnforceOpenAIContract bool
}

func (s *Service) StreamResponses(
	ctx context.Context,
	r *http.Request,
	apiKey *ApiKeyData,
	body map[string]any,
	opts StreamResponsesOptions,
) (<-chan string, <-chan error, error) {
	model := stringField(body, "model")
	if model == "" {
		return nil, nil, fmt.Errorf("model is required")
	}
	effectiveModel := EffectiveModelForAPIKey(apiKey, model)
	if err := ValidateModelAccess(apiKey, effectiveModel); err != nil {
		return nil, nil, err
	}

	dashboardSettings, err := s.settingsRepo.Get(ctx)
	if err != nil {
		return nil, nil, err
	}

	responsesPayload := map[string]any{}
	for key, value := range body {
		responsesPayload[key] = value
	}
	responsesPayload["model"] = effectiveModel
	responsesPayload["stream"] = true

	policyPayload := chatBodyToResponsesPayload(responsesPayload)
	ApplyAPIKeyEnforcement(&policyPayload, apiKey)
	responsesPayload = responsesPayloadToMap(policyPayload, responsesPayload)
	effectiveModel = policyPayload.Model

	reservation, err := s.reserveAPIKeyUsage(ctx, apiKey, effectiveModel, responsesPayload)
	if err != nil {
		return nil, nil, err
	}

	selectParams := streamSelectParams(effectiveModel, dashboardSettings, apiKey)
	stickyPolicy := StickyPolicyForResponsesRequest(
		responsesPayload,
		r.Header,
		opts.CodexSessionAffinity,
		true,
		dashboardSettings.OpenAICacheAffinityMaxAgeSeconds,
		dashboardSettings.StickyThreadsEnabled,
	)
	apiKeyID := ""
	if apiKey != nil {
		apiKeyID = apiKey.ID
	}
	if previousID := previousResponseIDFromPayload(responsesPayload); previousID != "" {
		ownerID, err := s.resolvePreviousResponseOwner(ctx, previousID, apiKeyID, "")
		if err != nil {
			s.releaseAPIKeyReservation(ctx, reservation)
			return nil, nil, err
		}
		if ownerID != "" {
			selectParams.PreferredAccountID = &ownerID
		}
	} else if pinnedID := s.resolveFileAccountForResponses(responsesPayload, r.Header); pinnedID != "" {
		selectParams.PreferredAccountID = &pinnedID
	} else if stickyPolicy.Key != "" && stickyPolicy.Kind != "" {
		accountID, err := s.stickySessionsRepo.GetAccountID(ctx, stickyPolicy.Key, stickyPolicy.Kind, stickyPolicy.MaxAgeSeconds)
		if err != nil {
			s.releaseAPIKeyReservation(ctx, reservation)
			return nil, nil, err
		}
		if accountID != "" {
			selectParams.PreferredAccountID = &accountID
		}
	}

	selection, err := s.loadBalancer.SelectAccount(ctx, selectParams)
	if err != nil {
		s.releaseAPIKeyReservation(ctx, reservation)
		return nil, nil, err
	}
	if selection.ErrorCode != "" || selection.Account == nil {
		s.releaseAPIKeyReservation(ctx, reservation)
		message := selection.ErrorMessage
		if message == "" {
			message = "No available accounts"
		}
		return nil, nil, NewProxyRateLimitError(message)
	}
	if stickyPolicy.Key != "" && stickyPolicy.Kind != "" {
		_ = s.stickySessionsRepo.Upsert(ctx, stickyPolicy.Key, selection.Account.ID, stickyPolicy.Kind)
	}

	events, errs := upstream.OpenResponseStream(ctx, upstream.StreamOptions{
		BaseURL:          s.upstreamBaseURL,
		Payload:          responsesPayload,
		InboundHeaders:   r.Header.Clone(),
		AccessToken:      selection.Account.AccessToken,
		AccountID:        selection.Account.ID,
		Transport:        upstream.Transport(dashboardSettings.UpstreamStreamTransport),
		PrefersWebSocket: s.modelRegistry.PrefersWebsockets(effectiveModel),
		Client:           s.upstreamClient,
	})

	keepaliveInterval := 15 * time.Second
	keepaliveFrame := SSEKeepaliveFrame
	if opts.CodexSessionAffinity {
		keepaliveFrame = CodexKeepaliveFrame
	}
	keptAlive := InjectSSEKeepalives(events, keepaliveInterval, keepaliveFrame)

	outEvents := make(chan string, 32)
	outErrs := make(chan error, 1)
	started := time.Now()
	account := selection.Account
	lease := selection.Lease

	go func() {
		defer close(outEvents)
		defer close(outErrs)
		defer s.loadBalancer.ReleaseLease(lease)

		var streamErr error
		var terminalResult map[string]any
		cancelHeartbeat := s.startAPIKeyReservationHeartbeat(reservation)
		defer cancelHeartbeat()
		for event := range keptAlive {
			if payload := ParseSSEDataJSON(event); payload != nil {
				eventType, _ := payload["type"].(string)
				if eventType == "response.completed" || eventType == "response.incomplete" || eventType == "response.failed" || eventType == "error" {
					terminalResult = map[string]any{}
					if response, ok := payload["response"].(map[string]any); ok {
						if usage, ok := response["usage"].(map[string]any); ok {
							terminalResult["usage"] = usage
						}
					}
					if eventType != "response.completed" {
						streamErr = fmt.Errorf("upstream stream ended with %s", eventType)
					}
				}
			}
			outEvents <- event
		}
		select {
		case err, ok := <-errs:
			if ok && err != nil {
				streamErr = err
			}
		default:
		}

		status := "success"
		if streamErr != nil {
			status = "error"
		}
		latency := time.Since(started).Milliseconds()
		s.logRequest(ctx, logRequestParams{
			requestID: r.Header.Get("X-Request-Id"),
			model:     effectiveModel,
			account:   account,
			apiKey:    apiKey,
			status:    status,
			latencyMS: latency,
			userAgent: r.UserAgent(),
		})
		if streamErr != nil {
			s.failAPIKeyReservation(reservation, effectiveModel, terminalResult, "")
			outErrs <- streamErr
		} else if status == "error" {
			s.failAPIKeyReservation(reservation, effectiveModel, terminalResult, "")
		} else {
			s.finalizeAPIKeyReservation(reservation, effectiveModel, terminalResult, "")
		}
	}()

	return outEvents, outErrs, nil
}

func (s *Service) selectStreamAccount(ctx context.Context, model string, dashboardSettings settings.DashboardSettings) (AccountSelection, error) {
	return s.loadBalancer.SelectAccount(ctx, streamSelectParams(model, dashboardSettings, nil))
}

func streamSelectParams(model string, dashboardSettings settings.DashboardSettings, apiKey *ApiKeyData) SelectAccountParams {
	return SelectAccountParams{
		Model:                      model,
		PreferEarlierResetAccounts: dashboardSettings.PreferEarlierResetAccounts,
		PreferEarlierResetWindow:   ResetPreferenceWindow(dashboardSettings.PreferEarlierResetWindow),
		RoutingStrategy:            RoutingStrategy(dashboardSettings.RoutingStrategy),
		RelativeAvailabilityPower:  dashboardSettings.RelativeAvailabilityPower,
		RelativeAvailabilityTopK:   dashboardSettings.RelativeAvailabilityTopK,
		LeaseKind:                  AccountLeaseKindStream,
		TrafficClass:               trafficClassForKey(apiKey),
		SingleAccountID:            dashboardSettings.SingleAccountID,
	}
}

func (s *Service) pinFileAccount(fileID, accountID string) {
	if fileID == "" || accountID == "" {
		return
	}
	s.filePinsMu.Lock()
	defer s.filePinsMu.Unlock()
	s.filePins[fileID] = filePin{AccountID: accountID, ExpiresAt: time.Now().Add(fileAccountPinTTL)}
	s.evictExpiredFilePinsLocked(time.Now())
}

func (s *Service) resolveFileAccountForResponses(body map[string]any, headers http.Header) string {
	if ExplicitPromptCacheKey(body) || PreviousResponseID(body) != "" || SessionKeyFromHeaders(headers) != "" {
		return ""
	}
	if turnState := TurnStateFromHeaders(headers); turnState != "" && !IsSynthesizedTurnState(turnState) {
		return ""
	}
	fileIDs := ExtractInputFileIDs(body["input"])
	if len(fileIDs) == 0 {
		return ""
	}
	s.filePinsMu.Lock()
	defer s.filePinsMu.Unlock()
	now := time.Now()
	s.evictExpiredFilePinsLocked(now)
	sort.Strings(fileIDs)
	var best filePin
	bestFileID := ""
	for _, fileID := range fileIDs {
		pin, ok := s.filePins[fileID]
		if !ok {
			continue
		}
		if bestFileID == "" || pin.ExpiresAt.After(best.ExpiresAt) {
			best = pin
			bestFileID = fileID
		}
	}
	return best.AccountID
}

func (s *Service) evictExpiredFilePinsLocked(now time.Time) {
	for fileID, pin := range s.filePins {
		if !pin.ExpiresAt.After(now) {
			delete(s.filePins, fileID)
		}
	}
}

func (s *Service) WriteSSEStream(w http.ResponseWriter, events <-chan string, errs <-chan error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	for {
		select {
		case err, ok := <-errs:
			if ok && err != nil {
				if appErr, ok := err.(*AppError); ok {
					_, _ = io.WriteString(w, FormatSSEEvent(map[string]any{
						"type": "error",
						"error": map[string]any{
							"message": appErr.Message,
							"type":    appErr.ErrorType,
							"code":    appErr.Code,
						},
					}))
					flusher.Flush()
				}
				return
			}
		default:
		}

		event, ok := <-events
		if !ok {
			return
		}
		_, _ = io.WriteString(w, event)
		flusher.Flush()
		if payload := ParseSSEDataJSON(event); payload != nil {
			eventType, _ := payload["type"].(string)
			if eventType == "error" || IsTerminalResponseEvent(payload) {
				return
			}
		}
	}
}
