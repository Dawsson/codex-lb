package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDrainRoutesRequireLoopback(t *testing.T) {
	handler := NewHandler(nil, NewDrainState())
	req := httptest.NewRequest(http.MethodPost, "/internal/drain/start", nil)
	req.RemoteAddr = "203.0.113.10:5555"
	rec := httptest.NewRecorder()

	handler.StartDrain(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDrainStartStatusStop(t *testing.T) {
	state := NewDrainState()
	handler := NewHandler(nil, state)

	start := httptest.NewRequest(http.MethodPost, "/internal/drain/start", nil)
	start.RemoteAddr = "127.0.0.1:5555"
	startRec := httptest.NewRecorder()
	handler.StartDrain(startRec, start)
	if startRec.Code != http.StatusOK || !state.IsDraining() || !state.IsBridgeDrainActive() {
		t.Fatalf("unexpected start response=%d draining=%v bridge=%v body=%s", startRec.Code, state.IsDraining(), state.IsBridgeDrainActive(), startRec.Body.String())
	}

	status := httptest.NewRequest(http.MethodGet, "/internal/drain/status", nil)
	status.RemoteAddr = "127.0.0.1:5555"
	statusRec := httptest.NewRecorder()
	handler.DrainStatus(statusRec, status)
	checks := decodeChecks(t, statusRec)
	if checks["draining"] != "true" || checks["bridge_drain_active"] != "true" || checks["in_flight"] != "0" {
		t.Fatalf("unexpected status checks: %#v", checks)
	}

	stop := httptest.NewRequest(http.MethodPost, "/internal/drain/stop", nil)
	stop.RemoteAddr = "127.0.0.1:5555"
	stopRec := httptest.NewRecorder()
	handler.StopDrain(stopRec, stop)
	if stopRec.Code != http.StatusOK || state.IsDraining() || state.IsBridgeDrainActive() {
		t.Fatalf("unexpected stop response=%d draining=%v bridge=%v body=%s", stopRec.Code, state.IsDraining(), state.IsBridgeDrainActive(), stopRec.Body.String())
	}
}

func TestReadyFailsWhileDraining(t *testing.T) {
	state := NewDrainState()
	state.SetDraining(true)
	handler := NewHandler(nil, state)
	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()

	handler.Ready(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDrainMiddlewareRejectsNewHTTPWork(t *testing.T) {
	state := NewDrainState()
	state.SetDraining(true)
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})
	handler := DrainMiddleware(state)(next)

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable || nextCalled {
		t.Fatalf("expected 503 before next handler, code=%d next=%v body=%s", rec.Code, nextCalled, rec.Body.String())
	}
}

func TestDrainMiddlewareTracksInFlightHTTPAndExcludesWebsocket(t *testing.T) {
	state := NewDrainState()
	var observedInFlight int64
	handler := DrainMiddleware(state)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observedInFlight = state.InFlight()
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if observedInFlight != 1 || state.InFlight() != 0 {
		t.Fatalf("expected in-flight counted during request then cleared, observed=%d final=%d", observedInFlight, state.InFlight())
	}

	observedInFlight = -1
	wsReq := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	wsReq.Header.Set("Upgrade", "websocket")
	wsRec := httptest.NewRecorder()
	handler.ServeHTTP(wsRec, wsReq)
	if observedInFlight != 0 || state.InFlight() != 0 {
		t.Fatalf("expected websocket excluded from in-flight count, observed=%d final=%d", observedInFlight, state.InFlight())
	}
}

func decodeChecks(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var payload struct {
		Checks map[string]string `json:"checks"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return payload.Checks
}
