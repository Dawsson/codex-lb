package models

import (
	"net/http"
	"sort"
	"strings"

	"github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/httputil"
)

type Handler struct {
	store *db.Store
}

type itemResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type listResponse struct {
	Models []itemResponse `json:"models"`
}

func NewHandler(store *db.Store) Handler {
	return Handler{store: store}
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.DB().QueryContext(r.Context(), `
		SELECT DISTINCT model
		  FROM request_logs
		 WHERE deleted_at IS NULL
		   AND model IS NOT NULL
		   AND TRIM(model) != ''
		 ORDER BY model ASC
	`)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	defer rows.Close()

	seen := map[string]struct{}{}
	var models []itemResponse
	for rows.Next() {
		var model string
		if err := rows.Scan(&model); err != nil {
			httputil.WriteServerError(w, err)
			return
		}
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		models = append(models, itemResponse{ID: model, Name: model})
	}
	if err := rows.Err(); err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	httputil.WriteJSON(w, http.StatusOK, listResponse{Models: httputil.EmptySlice(models)})
}
