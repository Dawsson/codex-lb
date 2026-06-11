package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/settings"
	"github.com/soju06/codex-lb/internal/upstream"
)

var websocketUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type WebSocketResponsesHandler struct {
	service      *Service
	apiKeysRepo  apikeys.Repository
	settingsRepo settings.Repository
	codexSession bool
}

func NewWebSocketResponsesHandler(
	service *Service,
	apiKeysRepo apikeys.Repository,
	settingsRepo settings.Repository,
	codexSession bool,
) WebSocketResponsesHandler {
	return WebSocketResponsesHandler{
		service:      service,
		apiKeysRepo:  apiKeysRepo,
		settingsRepo: settingsRepo,
		codexSession: codexSession,
	}
}

func (h WebSocketResponsesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	apiKey, err := ValidateProxyAPIKey(r.Context(), h.apiKeysRepo, h.settingsRepo, r)
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	turnState := EnsureDownstreamTurnState(r.Header)
	responseHeaders := DownstreamTurnStateResponseHeaders(turnState)
	conn, err := websocketUpgrader.Upgrade(w, r, responseHeaders)
	if err != nil {
		return
	}
	defer conn.Close()

	forwarded := r.Header.Clone()
	forwarded.Set("X-Codex-Turn-State", turnState)

	for {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
			continue
		}

		body, payloadErr := parseWebSocketResponsePayload(payload)
		if payloadErr != nil {
			h.writeTerminalError(conn, payloadErr.Code, payloadErr.Message, payloadErr.ErrorType, payloadErr.Param)
			continue
		}
		if !isWebSocketResponseCreate(body) {
			h.writeTerminalError(
				conn,
				"invalid_request_error",
				"WebSocket connection has no active upstream session",
				"invalid_request_error",
				"",
			)
			continue
		}

		if err := h.proxyOneResponse(r, conn, forwarded, apiKey, body); err != nil {
			if appErr, ok := err.(*AppError); ok {
				h.writeTerminalError(conn, appErr.Code, appErr.Message, appErr.ErrorType, appErr.Param)
			}
		}
	}
}

func (h WebSocketResponsesHandler) proxyOneResponse(
	r *http.Request,
	conn *websocket.Conn,
	forwarded http.Header,
	apiKey *ApiKeyData,
	body map[string]any,
) error {
	model, _ := body["model"].(string)
	if model == "" {
		return NewClientPayloadError("model is required", "model", "invalid_request_error", "invalid_request_error")
	}
	effectiveModel := EffectiveModelForAPIKey(apiKey, model)
	if err := ValidateModelAccess(apiKey, effectiveModel); err != nil {
		return err
	}

	dashboardSettings, err := h.settingsRepo.Get(r.Context())
	if err != nil {
		return err
	}

	policyPayload := chatBodyToResponsesPayload(body)
	policyPayload.Model = effectiveModel
	ApplyAPIKeyEnforcement(&policyPayload, apiKey)
	body = responsesPayloadToMap(policyPayload, body)
	effectiveModel = policyPayload.Model

	reservation, err := h.service.reserveAPIKeyUsage(r.Context(), apiKey, effectiveModel, body)
	if err != nil {
		return err
	}

	apiKeyID := ""
	if apiKey != nil {
		apiKeyID = apiKey.ID
	}
	selectParams := SelectAccountParams{
		Model:                      effectiveModel,
		PreferEarlierResetAccounts: dashboardSettings.PreferEarlierResetAccounts,
		PreferEarlierResetWindow:   ResetPreferenceWindow(dashboardSettings.PreferEarlierResetWindow),
		RoutingStrategy:            RoutingStrategy(dashboardSettings.RoutingStrategy),
		RelativeAvailabilityPower:  dashboardSettings.RelativeAvailabilityPower,
		RelativeAvailabilityTopK:   dashboardSettings.RelativeAvailabilityTopK,
		LeaseKind:                  AccountLeaseKindStream,
		TrafficClass:               trafficClassForKey(apiKey),
		SingleAccountID:            dashboardSettings.SingleAccountID,
	}
	if previousID := previousResponseIDFromPayload(body); previousID != "" {
		ownerID, err := h.service.resolvePreviousResponseOwner(r.Context(), previousID, apiKeyID, "")
		if err != nil {
			h.service.releaseAPIKeyReservation(r.Context(), reservation)
			return err
		}
		if ownerID != "" {
			selectParams.PreferredAccountID = &ownerID
		}
	}

	selection, err := h.service.loadBalancer.SelectAccount(r.Context(), selectParams)
	if err != nil {
		h.service.releaseAPIKeyReservation(r.Context(), reservation)
		return err
	}
	defer h.service.loadBalancer.ReleaseLease(selection.Lease)
	if selection.Account == nil {
		h.service.releaseAPIKeyReservation(r.Context(), reservation)
		message := selection.ErrorMessage
		if message == "" {
			message = "No available accounts"
		}
		return NewProxyRateLimitError(message)
	}

	events, errs := upstream.OpenResponseStream(r.Context(), upstream.StreamOptions{
		BaseURL:          h.service.upstreamBaseURL,
		Payload:          body,
		InboundHeaders:   forwarded,
		AccessToken:      selection.Account.AccessToken,
		AccountID:        selection.Account.ID,
		Transport:        upstream.TransportWebSocket,
		PrefersWebSocket: true,
		Client:           h.service.upstreamClient,
	})

	var completedResponseID string
	var terminalResult map[string]any
	status := "success"
	cancelHeartbeat := h.service.startAPIKeyReservationHeartbeat(reservation)
	defer func() {
		cancelHeartbeat()
		if status == "success" {
			h.service.finalizeAPIKeyReservation(reservation, effectiveModel, terminalResult, "")
		} else {
			h.service.failAPIKeyReservation(reservation, effectiveModel, terminalResult, "")
		}
	}()
	for event := range events {
		text := strings.TrimSpace(event)
		if text == "" {
			continue
		}
		if err := conn.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
			return err
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(text)); err != nil {
			return err
		}
		if payload := ParseSSEDataJSON(event); payload != nil {
			eventType, _ := payload["type"].(string)
			if eventType == "response.completed" || eventType == "response.incomplete" {
				if id := responseIDFromEvent(payload); id != "" {
					completedResponseID = id
				}
			}
			if eventType == "response.completed" || eventType == "response.incomplete" || eventType == "response.failed" || eventType == "error" {
				terminalResult = map[string]any{}
				if response, ok := payload["response"].(map[string]any); ok {
					if usage, ok := response["usage"].(map[string]any); ok {
						terminalResult["usage"] = usage
					}
				}
				if eventType != "response.completed" {
					status = "error"
				}
			}
			if IsTerminalResponseEvent(payload) {
				break
			}
		}
	}
	if err, ok := <-errs; ok && err != nil {
		status = "error"
		return err
	}
	if completedResponseID != "" {
		h.service.previousResponseIndex.Remember(completedResponseID, apiKeyID, selection.Account.ID)
	}
	return nil
}

func parseWebSocketResponsePayload(payload []byte) (map[string]any, *AppError) {
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, NewClientPayloadError("Invalid JSON payload", "", "invalid_json", "invalid_request_error")
	}
	if body == nil {
		return nil, NewClientPayloadError("Invalid JSON payload", "", "invalid_json", "invalid_request_error")
	}
	return body, nil
}

func isWebSocketResponseCreate(payload map[string]any) bool {
	payloadType, _ := payload["type"].(string)
	return payloadType == "response.create"
}

func (h WebSocketResponsesHandler) writeTerminalError(conn *websocket.Conn, code, message, errorType, param string) {
	if errorType == "" {
		errorType = "server_error"
	}
	errorPayload := map[string]any{
		"code":    code,
		"message": message,
		"type":    errorType,
	}
	if param != "" {
		errorPayload["param"] = param
	}
	event := FormatSSEEvent(map[string]any{
		"type":  "error",
		"error": errorPayload,
	})
	_ = conn.WriteMessage(websocket.TextMessage, []byte(event))
}
