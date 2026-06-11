package audit

import (
	"net/http"
	"strconv"

	"github.com/soju06/codex-lb/internal/httputil"
)

type Handler struct {
	repo Repository
}

type Response struct {
	ID        int64   `json:"id"`
	Timestamp string  `json:"timestamp"`
	Action    string  `json:"action"`
	ActorIP   *string `json:"actorIp,omitempty"`
	Details   *string `json:"details,omitempty"`
	RequestID *string `json:"requestId,omitempty"`
}

func NewHandler(repo Repository) Handler {
	return Handler{repo: repo}
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("action")
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	entries, err := h.repo.List(r.Context(), action, limit, offset)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	out := make([]Response, 0, len(entries))
	for _, entry := range entries {
		resp := Response{
			ID:        entry.ID,
			Timestamp: entry.Timestamp,
			Action:    entry.Action,
		}
		if entry.ActorIP.Valid {
			value := entry.ActorIP.String
			resp.ActorIP = &value
		}
		if entry.Details.Valid {
			value := entry.Details.String
			resp.Details = &value
		}
		if entry.RequestID.Valid {
			value := entry.RequestID.String
			resp.RequestID = &value
		}
		out = append(out, resp)
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}
