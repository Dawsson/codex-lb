package health

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
)

type DrainState struct {
	draining          atomic.Bool
	bridgeDrain       atomic.Bool
	inFlightHTTPCount atomic.Int64
}

func NewDrainState() *DrainState {
	return &DrainState{}
}

func (s *DrainState) SetDraining(value bool) {
	s.draining.Store(value)
}

func (s *DrainState) IsDraining() bool {
	return s != nil && s.draining.Load()
}

func (s *DrainState) SetBridgeDrainActive(value bool) {
	s.bridgeDrain.Store(value)
}

func (s *DrainState) IsBridgeDrainActive() bool {
	return s != nil && s.bridgeDrain.Load()
}

func (s *DrainState) InFlight() int64 {
	if s == nil {
		return 0
	}
	return s.inFlightHTTPCount.Load()
}

func (s *DrainState) incrementInFlight() {
	if s != nil {
		s.inFlightHTTPCount.Add(1)
	}
}

func (s *DrainState) decrementInFlight() {
	if s == nil {
		return
	}
	for {
		current := s.inFlightHTTPCount.Load()
		if current <= 0 {
			return
		}
		if s.inFlightHTTPCount.CompareAndSwap(current, current-1) {
			return
		}
	}
}

var drainAllowedPaths = map[string]struct{}{
	"/health/live":               {},
	"/internal/drain/start":      {},
	"/internal/drain/stop":       {},
	"/internal/drain/status":     {},
	"/internal/bridge/responses": {},
}

var inFlightExcludedPaths = map[string]struct{}{
	"/health/live":           {},
	"/health/ready":          {},
	"/health/startup":        {},
	"/internal/drain/start":  {},
	"/internal/drain/stop":   {},
	"/internal/drain/status": {},
}

func DrainMiddleware(state *DrainState) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if state == nil {
				next.ServeHTTP(w, r)
				return
			}
			if state.IsDraining() {
				if _, allowed := drainAllowedPaths[r.URL.Path]; !allowed {
					writeJSON(w, http.StatusServiceUnavailable, map[string]any{
						"error": map[string]string{
							"type":    "service_unavailable",
							"message": "Server is draining",
						},
					})
					return
				}
			}
			if _, excluded := inFlightExcludedPaths[r.URL.Path]; excluded || isWebsocketRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			state.incrementInFlight()
			defer state.decrementInFlight()
			next.ServeHTTP(w, r)
		})
	}
}

func (h Handler) StartDrain(w http.ResponseWriter, r *http.Request) {
	if !isInternalClient(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"detail": "Internal access required"})
		return
	}
	h.drain.SetBridgeDrainActive(true)
	h.drain.SetDraining(true)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "checks": map[string]string{"draining": "ok"}})
}

func (h Handler) StopDrain(w http.ResponseWriter, r *http.Request) {
	if !isInternalClient(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"detail": "Internal access required"})
		return
	}
	h.drain.SetDraining(false)
	h.drain.SetBridgeDrainActive(false)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "checks": map[string]string{"draining": "false"}})
}

func (h Handler) DrainStatus(w http.ResponseWriter, r *http.Request) {
	if !isInternalClient(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"detail": "Internal access required"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"checks": map[string]string{
			"draining":            strconv.FormatBool(h.drain.IsDraining()),
			"bridge_drain_active": strconv.FormatBool(h.drain.IsBridgeDrainActive()),
			"in_flight":           strconv.FormatInt(h.drain.InFlight(), 10),
		},
	})
}

func isInternalClient(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isWebsocketRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}
