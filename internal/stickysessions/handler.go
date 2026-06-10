package stickysessions

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/soju06/codex-lb/internal/httputil"
	"github.com/soju06/codex-lb/internal/platform"
)

type Handler struct {
	repo Repository
}

type entryResponse struct {
	Key         string  `json:"key"`
	DisplayName string  `json:"displayName"`
	Kind        string  `json:"kind"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
	ExpiresAt   *string `json:"expiresAt"`
	IsStale     bool    `json:"isStale"`
}

type listResponse struct {
	Entries               []entryResponse `json:"entries"`
	StalePromptCacheCount int             `json:"stalePromptCacheCount"`
	Total                 int             `json:"total"`
	HasMore               bool            `json:"hasMore"`
}

type identifierPayload struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
}

type deleteRequest struct {
	Sessions []identifierPayload `json:"sessions"`
}

type deleteResponse struct {
	DeletedCount int                 `json:"deletedCount"`
	Deleted      []identifierPayload `json:"deleted"`
	Failed       []deleteFailure     `json:"failed"`
}

type deleteFailure struct {
	Key    string `json:"key"`
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

type deleteFilteredRequest struct {
	StaleOnly    bool   `json:"staleOnly"`
	AccountQuery string `json:"accountQuery"`
	KeyQuery     string `json:"keyQuery"`
}

type deleteFilteredResponse struct {
	DeletedCount int `json:"deletedCount"`
}

type purgeRequest struct {
	StaleOnly bool `json:"staleOnly"`
}

type purgeResponse struct {
	DeletedCount int `json:"deletedCount"`
}

func NewHandler(repo Repository) Handler {
	return Handler{repo: repo}
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	params := parseListParams(r)
	ttl, err := h.repo.CacheAffinityMaxAgeSeconds(r.Context())
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	cutoff := staleCutoff(ttl)
	staleCount, err := h.repo.CountStalePromptCache(r.Context(), cutoff)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	var updatedBefore *string
	if params.StaleOnly {
		updatedBefore = &cutoff
	}
	total, err := h.repo.CountEntries(r.Context(), params, updatedBefore)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	rows, err := h.repo.ListEntries(r.Context(), params, updatedBefore)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	entries := make([]entryResponse, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, toEntryResponse(row, ttl))
	}
	httputil.WriteJSON(w, http.StatusOK, listResponse{
		Entries:               httputil.EmptySlice(entries),
		StalePromptCacheCount: staleCount,
		Total:                 total,
		HasMore:               params.Offset+len(entries) < total,
	})
}

func (h Handler) DeleteOne(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	kind := chi.URLParam(r, "kind")
	deleted, err := h.repo.Delete(r.Context(), key, kind)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if !deleted {
		httputil.WriteError(w, http.StatusNotFound, "not_found", "Sticky session not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, deleteResponse{
		DeletedCount: 1,
		Deleted:      []identifierPayload{{Key: key, Kind: kind}},
		Failed:       httputil.EmptySlice([]deleteFailure{}),
	})
}

func (h Handler) DeleteMany(w http.ResponseWriter, r *http.Request) {
	var payload deleteRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	targets := make([][2]string, 0, len(payload.Sessions))
	seen := make(map[string]struct{})
	for _, session := range payload.Sessions {
		if session.Key == "" {
			continue
		}
		id := session.Kind + ":" + session.Key
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		targets = append(targets, [2]string{session.Key, session.Kind})
	}
	deleted, err := h.repo.DeleteEntries(r.Context(), targets)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	deletedSet := make(map[string]struct{}, len(deleted))
	deletedPayload := make([]identifierPayload, 0, len(deleted))
	for _, item := range deleted {
		deletedSet[item[1]+":"+item[0]] = struct{}{}
		deletedPayload = append(deletedPayload, identifierPayload{Key: item[0], Kind: item[1]})
	}
	failed := make([]deleteFailure, 0)
	for _, target := range targets {
		if _, ok := deletedSet[target[1]+":"+target[0]]; !ok {
			failed = append(failed, deleteFailure{Key: target[0], Kind: target[1], Reason: "not_found"})
		}
	}
	httputil.WriteJSON(w, http.StatusOK, deleteResponse{
		DeletedCount: len(deletedPayload),
		Deleted:      httputil.EmptySlice(deletedPayload),
		Failed:       httputil.EmptySlice(failed),
	})
}

func (h Handler) DeleteFiltered(w http.ResponseWriter, r *http.Request) {
	var payload deleteFilteredRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	ttl, err := h.repo.CacheAffinityMaxAgeSeconds(r.Context())
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	params := ListParams{
		StaleOnly:    payload.StaleOnly,
		AccountQuery: payload.AccountQuery,
		KeyQuery:     payload.KeyQuery,
	}
	var updatedBefore *string
	if payload.StaleOnly {
		cutoff := staleCutoff(ttl)
		updatedBefore = &cutoff
	}
	identifiers, err := h.repo.ListIdentifiers(r.Context(), params, updatedBefore)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	deleted, err := h.repo.DeleteEntries(r.Context(), identifiers)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, deleteFilteredResponse{DeletedCount: len(deleted)})
}

func (h Handler) Purge(w http.ResponseWriter, r *http.Request) {
	var payload purgeRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	if !payload.StaleOnly {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Only stale prompt cache purge is supported")
		return
	}
	ttl, err := h.repo.CacheAffinityMaxAgeSeconds(r.Context())
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	count, err := h.repo.PurgePromptCacheBefore(r.Context(), staleCutoff(ttl))
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, purgeResponse{DeletedCount: count})
}

func parseListParams(r *http.Request) ListParams {
	query := r.URL.Query()
	limit := 10
	if raw := query.Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	offset := 0
	if raw := query.Get("offset"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	sortBy := query.Get("sortBy")
	if sortBy == "" {
		sortBy = query.Get("sort_by")
	}
	if sortBy == "" {
		sortBy = "updated_at"
	}
	sortDir := query.Get("sortDir")
	if sortDir == "" {
		sortDir = query.Get("sort_dir")
	}
	if sortDir == "" {
		sortDir = "desc"
	}
	staleOnly := strings.EqualFold(query.Get("staleOnly"), "true") || query.Get("staleOnly") == "1"
	return ListParams{
		StaleOnly:    staleOnly,
		AccountQuery: query.Get("accountQuery"),
		KeyQuery:     query.Get("keyQuery"),
		SortBy:       sortBy,
		SortDir:      sortDir,
		Offset:       offset,
		Limit:        limit,
	}
}

func toEntryResponse(row Entry, ttlSeconds int) entryResponse {
	createdAt := formatTime(row.CreatedAt)
	updatedAt := formatTime(row.UpdatedAt)
	var expiresAt *string
	isStale := false
	if row.Kind == "prompt_cache" {
		if parsed := parseSQLiteTime(row.UpdatedAt); parsed != nil {
			exp := parsed.Add(time.Duration(ttlSeconds) * time.Second).UTC().Format(time.RFC3339Nano)
			expiresAt = &exp
			isStale = !parsed.Add(time.Duration(ttlSeconds) * time.Second).After(time.Now().UTC())
		}
	}
	return entryResponse{
		Key:         row.Key,
		DisplayName: row.DisplayName,
		Kind:        row.Kind,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
		ExpiresAt:   expiresAt,
		IsStale:     isStale,
	}
}

func formatTime(value sql.NullString) string {
	if iso := platform.SQLiteTimeToISO(value); iso != nil {
		return *iso
	}
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func parseSQLiteTime(value sql.NullString) *time.Time {
	if iso := platform.SQLiteTimeToISO(value); iso != nil {
		if parsed, err := time.Parse(time.RFC3339Nano, *iso); err == nil {
			return &parsed
		}
	}
	return nil
}
