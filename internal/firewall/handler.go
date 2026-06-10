package firewall

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/soju06/codex-lb/internal/httputil"
)

var (
	ErrInvalidIP     = errors.New("invalid ip address")
	ErrAlreadyExists = errors.New("ip already exists")
)

type Handler struct {
	repo Repository
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

func NewHandler(repo Repository) Handler {
	return Handler{repo: repo}
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
	httputil.WriteJSON(w, http.StatusOK, deleteResponse{Status: "deleted"})
}
