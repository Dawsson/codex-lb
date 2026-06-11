package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/soju06/codex-lb/internal/audit"
	"github.com/soju06/codex-lb/internal/cacheinvalidation"
	"github.com/soju06/codex-lb/internal/httputil"
)

var (
	ErrInvalidIP     = errors.New("invalid ip address")
	ErrAlreadyExists = errors.New("ip already exists")
)

type Handler struct {
	repo      Repository
	firewall  *Firewall
	bumper    cacheBumper
	auditRepo *audit.Repository
}

type cacheBumper interface {
	Bump(ctx context.Context, namespace string) error
}

type ipEntryResponse struct {
	IPAddress string `json:"ipAddress"`
	CreatedAt string `json:"createdAt"`
}

type listResponse struct {
	Mode    string            `json:"mode"`
	Entries []ipEntryResponse `json:"entries"`
}

type createRequest struct {
	IPAddress string `json:"ipAddress"`
}

type deleteResponse struct {
	Status string `json:"status"`
}

func NewHandler(repo Repository, firewall *Firewall, bumpers ...cacheBumper) Handler {
	var bumper cacheBumper
	if len(bumpers) > 0 {
		bumper = bumpers[0]
	}
	fw := firewall
	return Handler{repo: repo, firewall: fw, bumper: bumper}
}

func (h Handler) WithAudit(repo audit.Repository) Handler {
	h.auditRepo = &repo
	return h
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.repo.List(r.Context())
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	mode := "allow_all"
	if len(rows) > 0 {
		mode = "allowlist_active"
	}
	entries := make([]ipEntryResponse, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, ipEntryResponse{
			IPAddress: row.IPAddress,
			CreatedAt: formatCreatedAt(row.CreatedAt),
		})
	}
	httputil.WriteJSON(w, http.StatusOK, listResponse{
		Mode:    mode,
		Entries: httputil.EmptySlice(entries),
	})
}

func (h Handler) Create(w http.ResponseWriter, r *http.Request) {
	var payload createRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	row, err := h.repo.Add(r.Context(), payload.IPAddress)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidIP):
			httputil.WriteError(w, http.StatusBadRequest, "invalid_ip", "Invalid IP address")
		case errors.Is(err, ErrAlreadyExists):
			httputil.WriteError(w, http.StatusConflict, "ip_exists", "IP address already exists")
		default:
			httputil.WriteServerError(w, err)
		}
		return
	}
	h.invalidateCache()
	h.bumpInvalidation(r.Context())
	h.audit(r, "firewall_ip_created", map[string]any{"ip_address": row.IPAddress})
	httputil.WriteJSON(w, http.StatusOK, ipEntryResponse{
		IPAddress: row.IPAddress,
		CreatedAt: formatCreatedAt(row.CreatedAt),
	})
}

func (h Handler) Delete(w http.ResponseWriter, r *http.Request) {
	ipAddress := chi.URLParam(r, "ipAddress")
	deleted, err := h.repo.Delete(r.Context(), ipAddress)
	if err != nil {
		if errors.Is(err, ErrInvalidIP) {
			httputil.WriteError(w, http.StatusBadRequest, "invalid_ip", "Invalid IP address")
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	if !deleted {
		httputil.WriteError(w, http.StatusNotFound, "ip_not_found", "IP address not found")
		return
	}
	h.invalidateCache()
	h.bumpInvalidation(r.Context())
	h.audit(r, "firewall_ip_deleted", map[string]any{"ip_address": ipAddress})
	httputil.WriteJSON(w, http.StatusOK, deleteResponse{Status: "deleted"})
}

func (h Handler) invalidateCache() {
	if h.firewall != nil {
		h.firewall.InvalidateCache()
	}
}

func (h Handler) bumpInvalidation(ctx context.Context) {
	if h.bumper != nil {
		_ = h.bumper.Bump(ctx, cacheinvalidation.NamespaceFirewall)
	}
}

func (h Handler) audit(r *http.Request, action string, details map[string]any) {
	audit.LogRequest(h.auditRepo, r, action, details)
}
