package proxy

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/httputil"
	"github.com/soju06/codex-lb/internal/settings"
)

type WarmupHandler struct {
	service      *Service
	apiKeysRepo  apikeys.Repository
	settingsRepo settings.Repository
}

type warmupRequest struct {
	Mode string `json:"mode"`
}

func NewWarmupHandler(service *Service, apiKeysRepo apikeys.Repository, settingsRepo settings.Repository) WarmupHandler {
	return WarmupHandler{service: service, apiKeysRepo: apiKeysRepo, settingsRepo: settingsRepo}
}

func (h WarmupHandler) V1Warmup(w http.ResponseWriter, r *http.Request) {
	mode := "normal"
	if r.Body != nil {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			httputil.WriteServerError(w, err)
			return
		}
		if len(body) > 0 {
			var payload warmupRequest
			if err := json.Unmarshal(body, &payload); err != nil {
				envelope := OpenAIError("invalid_json", "Invalid JSON body", "invalid_request_error")
				httputil.WriteJSON(w, http.StatusBadRequest, envelope)
				return
			}
			mode = payload.Mode
		}
	}
	h.serve(w, r, mode)
}

func (h WarmupHandler) V1WarmupMode(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, chi.URLParam(r, "mode"))
}

func (h WarmupHandler) serve(w http.ResponseWriter, r *http.Request, mode string) {
	apiKey, err := ValidateProxyAPIKey(r.Context(), h.apiKeysRepo, h.settingsRepo, r)
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	response, envelope, status, err := h.service.Warmup(r.Context(), r, apiKey, mode)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if envelope != nil {
		httputil.WriteJSON(w, status, envelope)
		return
	}
	httputil.WriteJSON(w, status, response)
}
