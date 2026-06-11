package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/soju06/codex-lb/internal/db"
)

type Handler struct {
	store *db.Store
	drain *DrainState
}

func NewHandler(store *db.Store, drainState ...*DrainState) Handler {
	drain := NewDrainState()
	if len(drainState) > 0 && drainState[0] != nil {
		drain = drainState[0]
	}
	return Handler{store: store, drain: drain}
}

func (h Handler) Ready(w http.ResponseWriter, r *http.Request) {
	if h.drain.IsDraining() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "error",
			"checks": map[string]string{"draining": "true"},
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "error",
			"checks": map[string]string{"database": "error"},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"checks": map[string]string{"database": "ok"},
	})
}

func (h Handler) Live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h Handler) Health(w http.ResponseWriter, r *http.Request) {
	h.Ready(w, r)
}

func (h Handler) Startup(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
