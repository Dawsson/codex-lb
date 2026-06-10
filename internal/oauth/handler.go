package oauth

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/soju06/codex-lb/internal/httputil"
)

// Handler ports app.modules.oauth.api (the dashboard-session-protected
// /api/oauth/* routes).
type Handler struct {
	service *Service
}

func NewHandler(service *Service) Handler {
	return Handler{service: service}
}

type startRequest struct {
	ForceMethod string `json:"forceMethod"`
}

type completeRequest struct {
	FlowID       string `json:"flowId"`
	DeviceAuthID string `json:"deviceAuthId"`
	UserCode     string `json:"userCode"`
}

type manualCallbackRequest struct {
	CallbackURL string `json:"callbackUrl"`
	FlowID      string `json:"flowId"`
}

// Start handles POST /api/oauth/start.
func (h Handler) Start(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	resp, err := h.service.StartOAuth(r.Context(), req.ForceMethod)
	if err != nil {
		var oauthErr *OAuthError
		if errors.As(err, &oauthErr) {
			httputil.WriteJSON(w, http.StatusBadGateway, map[string]any{
				"error": map[string]string{
					"code":    oauthErr.Code,
					"message": oauthErr.Message,
				},
			})
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
}

// Status handles GET /api/oauth/status?flowId=.
func (h Handler) Status(w http.ResponseWriter, r *http.Request) {
	flowID := r.URL.Query().Get("flowId")
	httputil.WriteJSON(w, http.StatusOK, h.service.OAuthStatus(flowID))
}

// Complete handles POST /api/oauth/complete.
func (h Handler) Complete(w http.ResponseWriter, r *http.Request) {
	var req completeRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	httputil.WriteJSON(w, http.StatusOK, h.service.CompleteOAuth(req.FlowID, req.DeviceAuthID, req.UserCode))
}

// ManualCallback handles POST /api/oauth/manual-callback.
func (h Handler) ManualCallback(w http.ResponseWriter, r *http.Request) {
	var req manualCallbackRequest
	if r.Body == nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "callbackUrl is required")
		return
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CallbackURL == "" {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "callbackUrl is required")
		return
	}

	resp := h.service.ManualCallback(r.Context(), req.CallbackURL, req.FlowID)
	httputil.WriteJSON(w, http.StatusOK, resp)
}
