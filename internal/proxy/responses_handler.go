package proxy

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/httputil"
	"github.com/soju06/codex-lb/internal/settings"
)

type ResponsesHandler struct {
	service      *Service
	apiKeysRepo  apikeys.Repository
	settingsRepo settings.Repository
}

func NewResponsesHandler(service *Service, apiKeysRepo apikeys.Repository, settingsRepo settings.Repository) ResponsesHandler {
	return ResponsesHandler{service: service, apiKeysRepo: apiKeysRepo, settingsRepo: settingsRepo}
}

func (h ResponsesHandler) CodexResponses(w http.ResponseWriter, r *http.Request) {
	h.serveResponses(w, r, StreamResponsesOptions{CodexSessionAffinity: true})
}

func (h ResponsesHandler) V1Responses(w http.ResponseWriter, r *http.Request) {
	h.serveResponses(w, r, StreamResponsesOptions{CodexSessionAffinity: false, EnforceOpenAIContract: true})
}

func (h ResponsesHandler) CodexResponsesCompact(w http.ResponseWriter, r *http.Request) {
	h.serveCompact(w, r, true)
}

func (h ResponsesHandler) V1ResponsesCompact(w http.ResponseWriter, r *http.Request) {
	h.serveCompact(w, r, false)
}

func (h ResponsesHandler) serveResponses(w http.ResponseWriter, r *http.Request, opts StreamResponsesOptions) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	apiKey, err := ValidateProxyAPIKey(r.Context(), h.apiKeysRepo, h.settingsRepo, r)
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	var body map[string]any
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		envelope := OpenAIError("invalid_json", "Invalid JSON body", "invalid_request_error")
		httputil.WriteJSON(w, http.StatusBadRequest, envelope)
		return
	}
	if _, ok := body["instructions"]; !ok {
		body["instructions"] = ""
	}
	if err := EnforceStrictTextFormat(body); err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	if err := EnforceStrictFunctionToolsFormat(body["tools"], "tools[{index}].parameters", false); err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	turnState := EnsureHTTPDownstreamTurnState(r.Header)
	r.Header.Set(turnStateHeader, turnState)

	events, errs, err := h.service.StreamResponses(r.Context(), r, apiKey, body, opts)
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	for key, values := range DownstreamTurnStateResponseHeaders(turnState) {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	h.service.WriteSSEStream(w, events, errs)
}

func (h ResponsesHandler) serveCompact(w http.ResponseWriter, r *http.Request, codexSessionAffinity bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	apiKey, err := ValidateProxyAPIKey(r.Context(), h.apiKeysRepo, h.settingsRepo, r)
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	var body map[string]any
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		envelope := OpenAIError("invalid_json", "Invalid JSON body", "invalid_request_error")
		httputil.WriteJSON(w, http.StatusBadRequest, envelope)
		return
	}
	if _, ok := body["instructions"]; !ok {
		body["instructions"] = ""
	}
	result, envelope, status, err := h.service.CompactResponses(r.Context(), r, apiKey, body, codexSessionAffinity)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if envelope != nil {
		httputil.WriteJSON(w, status, envelope)
		return
	}
	httputil.WriteJSON(w, status, result)
}
