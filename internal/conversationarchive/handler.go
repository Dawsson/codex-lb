package conversationarchive

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/soju06/codex-lb/internal/httputil"
)

type Handler struct {
	repo Repository
}

func NewHandler(repo Repository) Handler {
	return Handler{repo: repo}
}

func (h Handler) ListFiles(w http.ResponseWriter, r *http.Request) {
	files, err := h.repo.ListFiles()
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(files))
	for _, file := range files {
		out = append(out, map[string]any{
			"name":       file.Name,
			"date":       file.Date,
			"sizeBytes":  file.SizeBytes,
			"compressed": file.Compressed,
			"modifiedAt": file.ModifiedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

func (h Handler) ListRecords(w http.ResponseWriter, r *http.Request) {
	fileName := r.URL.Query().Get("file")
	requestID := r.URL.Query().Get("requestId")
	if fileName == "" && requestID == "" {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "file or requestId is required"})
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 200 {
		limit = 200
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	page, err := h.repo.ReadRecords(fileName, RecordFilter{
		Direction: r.URL.Query().Get("direction"),
		Kind:      r.URL.Query().Get("kind"),
		Transport: r.URL.Query().Get("transport"),
		RequestID: requestID,
	}, offset, limit)
	if err != nil {
		if err == ErrNotFound {
			httputil.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
			return
		}
		if err == ErrInvalidFile {
			httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid conversation archive file name"})
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	records := make([]map[string]any, 0, len(page.Records))
	for _, record := range page.Records {
		records = append(records, recordResponse(record))
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"records": records,
		"total":   page.Total,
		"hasMore": page.HasMore,
	})
}

func recordResponse(record map[string]any) map[string]any {
	return map[string]any{
		"fileName":   optionalString(record["_archive_file"]),
		"timestamp":  optionalTimestamp(record["timestamp"]),
		"requestId":  optionalString(record["request_id"]),
		"direction":  optionalString(record["direction"]),
		"kind":       optionalString(record["kind"]),
		"transport":  optionalString(record["transport"]),
		"accountId":  optionalString(record["account_id"]),
		"method":     optionalString(record["method"]),
		"url":        optionalString(record["url"]),
		"statusCode": optionalInt(record["status_code"]),
		"headers":    optionalHeaders(record["headers"]),
		"payload":    record["payload"],
		"extra":      optionalMap(record["extra"]),
	}
}

func optionalString(value any) any {
	if text, ok := value.(string); ok {
		return text
	}
	return nil
}

func optionalTimestamp(value any) any {
	text, ok := value.(string)
	if !ok {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return nil
	}
	return parsed.UTC().Format("2006-01-02T15:04:05Z")
}

func optionalInt(value any) any {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return typed
	case float64:
		if typed == float64(int64(typed)) {
			return int64(typed)
		}
	}
	return nil
}

func optionalHeaders(value any) any {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	headers := make(map[string]string, len(raw))
	for key, item := range raw {
		headers[key] = fmt.Sprint(item)
	}
	return headers
}

func optionalMap(value any) any {
	if raw, ok := value.(map[string]any); ok {
		return raw
	}
	return nil
}
