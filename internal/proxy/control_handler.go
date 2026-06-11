package proxy

import (
	"io"
	"net/http"
	"strings"

	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/httputil"
	"github.com/soju06/codex-lb/internal/settings"
)

type ControlHandler struct {
	service      *Service
	apiKeysRepo  apikeys.Repository
	settingsRepo settings.Repository
}

func NewControlHandler(service *Service, apiKeysRepo apikeys.Repository, settingsRepo settings.Repository) ControlHandler {
	return ControlHandler{service: service, apiKeysRepo: apiKeysRepo, settingsRepo: settingsRepo}
}

func (h ControlHandler) ThreadGoalGet(w http.ResponseWriter, r *http.Request) {
	h.serveCodexControl(w, r, "thread/goal/get")
}

func (h ControlHandler) ThreadGoalSet(w http.ResponseWriter, r *http.Request) {
	h.serveCodexControl(w, r, "thread/goal/set")
}

func (h ControlHandler) ThreadGoalClear(w http.ResponseWriter, r *http.Request) {
	h.serveCodexControl(w, r, "thread/goal/clear")
}

func (h ControlHandler) AnalyticsEvents(w http.ResponseWriter, r *http.Request) {
	h.serveCodexControl(w, r, "analytics-events/events")
}

func (h ControlHandler) MemoriesTraceSummarize(w http.ResponseWriter, r *http.Request) {
	h.serveCodexControl(w, r, "memories/trace_summarize")
}

func (h ControlHandler) RealtimeCalls(w http.ResponseWriter, r *http.Request) {
	h.serveCodexControl(w, r, "realtime/calls")
}

func (h ControlHandler) SafetyArc(w http.ResponseWriter, r *http.Request) {
	h.serveCodexControl(w, r, "safety/arc")
}

func (h ControlHandler) OpportunisticAdmission(w http.ResponseWriter, r *http.Request) {
	apiKey, err := ValidateProxyAPIKey(r.Context(), h.apiKeysRepo, h.settingsRepo, r)
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	if apiKey == nil || apiKey.TrafficClass != TrafficClassOpportunistic {
		httputil.WriteJSON(w, http.StatusOK, map[string]bool{"admitted": true})
		return
	}
	admitted, message, err := h.service.CheckOpportunisticAdmission(r.Context(), apiKey, r.URL.Query().Get("model"))
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if admitted {
		httputil.WriteJSON(w, http.StatusOK, map[string]bool{"admitted": true})
		return
	}
	if !strings.HasPrefix(message, "opportunistic burn window closed") {
		message = "opportunistic burn window closed: " + message
	}
	w.Header().Set("Retry-After", "60")
	httputil.WriteJSON(w, http.StatusTooManyRequests, OpenAIError("rate_limit_exceeded", message, "rate_limit_error"))
}

func (h ControlHandler) serveCodexControl(w http.ResponseWriter, r *http.Request, upstreamPath string) {
	apiKey, err := ValidateProxyAPIKey(r.Context(), h.apiKeysRepo, h.settingsRepo, r)
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	var body []byte
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		body, err = io.ReadAll(io.LimitReader(r.Body, 32<<20))
		if err != nil {
			httputil.WriteServerError(w, err)
			return
		}
	}
	response, envelope, err := h.service.ProxyCodexControlRaw(r.Context(), r, apiKey, upstreamPath, body)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if envelope != nil {
		httputil.WriteJSON(w, response.StatusCode, envelope)
		return
	}
	copyCodexControlHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = w.Write(response.Body)
	}
}

var codexControlResponseHeaders = map[string]bool{
	"cache-control":        true,
	"content-type":         true,
	"etag":                 true,
	"last-modified":        true,
	"location":             true,
	"openai-processing-ms": true,
	"request-id":           true,
	"x-request-id":         true,
}

func copyCodexControlHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		if !codexControlResponseHeaders[strings.ToLower(key)] {
			continue
		}
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
