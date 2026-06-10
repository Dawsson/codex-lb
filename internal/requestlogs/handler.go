package requestlogs

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/soju06/codex-lb/internal/httputil"
	"github.com/soju06/codex-lb/internal/platform"
)

type Handler struct {
	repo Repository
}

type Filters struct {
	Limit        int
	Offset       int
	Search       string
	AccountIDs   []string
	APIKeyIDs    []string
	Statuses     []string
	ModelOptions []string
	Since        string
	Until        string
}

type logResponse struct {
	Requests []logEntry `json:"requests"`
	Total    int64      `json:"total"`
	HasMore  bool       `json:"hasMore"`
}

type logEntry struct {
	RequestedAt          string         `json:"requestedAt"`
	AccountID            *string        `json:"accountId"`
	PlanType             *string        `json:"planType"`
	APIKeyName           *string        `json:"apiKeyName"`
	APIKeyID             *string        `json:"apiKeyId"`
	RequestID            string         `json:"requestId"`
	RequestKind          string         `json:"requestKind"`
	Model                string         `json:"model"`
	Source               *string        `json:"source"`
	Transport            *string        `json:"transport"`
	UserAgent            *string        `json:"useragent"`
	UserAgentGroup       *string        `json:"useragentGroup"`
	ServiceTier          *string        `json:"serviceTier"`
	RequestedServiceTier *string        `json:"requestedServiceTier"`
	ActualServiceTier    *string        `json:"actualServiceTier"`
	Status               string         `json:"status"`
	ErrorCode            *string        `json:"errorCode"`
	ErrorMessage         *string        `json:"errorMessage"`
	FailurePhase         *string        `json:"failurePhase"`
	FailureDetail        *string        `json:"failureDetail"`
	FailureExceptionType *string        `json:"failureExceptionType"`
	UpstreamStatusCode   *int64         `json:"upstreamStatusCode"`
	UpstreamErrorCode    *string        `json:"upstreamErrorCode"`
	BridgeStage          *string        `json:"bridgeStage"`
	Tokens               *int64         `json:"tokens"`
	InputTokens          *int64         `json:"inputTokens"`
	OutputTokens         *int64         `json:"outputTokens"`
	CachedInputTokens    *int64         `json:"cachedInputTokens"`
	ReasoningEffort      *string        `json:"reasoningEffort"`
	CostUSD              *float64       `json:"costUsd"`
	CostBreakdown        map[string]any `json:"costBreakdown"`
	LatencyMS            *int64         `json:"latencyMs"`
}

type optionsResponse struct {
	AccountIDs   []string       `json:"accountIds"`
	ModelOptions []modelOption  `json:"modelOptions"`
	APIKeys      []apiKeyOption `json:"apiKeys"`
	Statuses     []string       `json:"statuses"`
}

type modelOption struct {
	Model           string  `json:"model"`
	ReasoningEffort *string `json:"reasoningEffort"`
}

type apiKeyOption struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	KeyPrefix *string `json:"keyPrefix"`
}

func NewHandler(repo Repository) Handler {
	return Handler{repo: repo}
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	filters := parseFilters(r)
	page, err := h.repo.List(r.Context(), filters)
	if err != nil {
		writeError(w, err)
		return
	}
	requests := make([]logEntry, 0, len(page.Entries))
	for _, entry := range page.Entries {
		requests = append(requests, mapEntry(entry))
	}
	writeJSON(w, http.StatusOK, logResponse{
		Requests: httputil.EmptySlice(requests),
		Total:    page.Total,
		HasMore:  int64(filters.Offset+filters.Limit) < page.Total,
	})
}

func (h Handler) Options(w http.ResponseWriter, r *http.Request) {
	filters := parseFilters(r)
	accountIDs, err := h.repo.AccountIDs(r.Context(), filters)
	if err != nil {
		writeError(w, err)
		return
	}
	modelRows, err := h.repo.ModelOptions(r.Context(), filters)
	if err != nil {
		writeError(w, err)
		return
	}
	apiKeyRows, err := h.repo.APIKeys(r.Context(), filters)
	if err != nil {
		writeError(w, err)
		return
	}
	statuses, err := h.repo.Statuses(r.Context(), filters)
	if err != nil {
		writeError(w, err)
		return
	}
	models := make([]modelOption, 0, len(modelRows))
	for _, row := range modelRows {
		models = append(models, modelOption{Model: row.Model, ReasoningEffort: nullString(row.ReasoningEffort)})
	}
	keys := make([]apiKeyOption, 0, len(apiKeyRows))
	for _, row := range apiKeyRows {
		keys = append(keys, apiKeyOption{ID: row.ID, Name: row.Name, KeyPrefix: nullString(row.KeyPrefix)})
	}
	writeJSON(w, http.StatusOK, optionsResponse{
		AccountIDs:   httputil.EmptySlice(accountIDs),
		ModelOptions: httputil.EmptySlice(models),
		APIKeys:      httputil.EmptySlice(keys),
		Statuses:     httputil.EmptySlice(statuses),
	})
}

func parseFilters(r *http.Request) Filters {
	query := r.URL.Query()
	limit := parseInt(query.Get("limit"), 25)
	if limit < 1 {
		limit = 25
	}
	if limit > 1000 {
		limit = 1000
	}
	offset := parseInt(query.Get("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	return Filters{
		Limit:        limit,
		Offset:       offset,
		Search:       query.Get("search"),
		AccountIDs:   query["accountId"],
		APIKeyIDs:    query["apiKeyId"],
		Statuses:     query["status"],
		ModelOptions: query["modelOption"],
		Since:        query.Get("since"),
		Until:        query.Get("until"),
	}
}

func mapEntry(entry Entry) logEntry {
	tokens := nullInt64Sum(entry.InputTokens, entry.OutputTokens)
	requestedAt := "1970-01-01T00:00:00Z"
	if converted := platform.SQLiteTimeToISO(entry.RequestedAt); converted != nil {
		requestedAt = *converted
	}
	return logEntry{
		RequestedAt:          requestedAt,
		AccountID:            nullString(entry.AccountID),
		PlanType:             nullString(entry.PlanType),
		APIKeyName:           nullString(entry.APIKeyName),
		APIKeyID:             nullString(entry.APIKeyID),
		RequestID:            entry.RequestID,
		RequestKind:          entry.RequestKind,
		Model:                entry.Model,
		Source:               nullString(entry.Source),
		Transport:            nullString(entry.Transport),
		UserAgent:            nullString(entry.UserAgent),
		UserAgentGroup:       nullString(entry.UserAgentGroup),
		ServiceTier:          nullString(entry.ServiceTier),
		RequestedServiceTier: nullString(entry.RequestedServiceTier),
		ActualServiceTier:    nullString(entry.ActualServiceTier),
		Status:               entry.Status,
		ErrorCode:            nullString(entry.ErrorCode),
		ErrorMessage:         nullString(entry.ErrorMessage),
		FailurePhase:         nullString(entry.FailurePhase),
		FailureDetail:        nullString(entry.FailureDetail),
		FailureExceptionType: nullString(entry.FailureExceptionType),
		UpstreamStatusCode:   nullInt64(entry.UpstreamStatusCode),
		UpstreamErrorCode:    nullString(entry.UpstreamErrorCode),
		BridgeStage:          nullString(entry.BridgeStage),
		Tokens:               tokens,
		InputTokens:          nullInt64(entry.InputTokens),
		OutputTokens:         nullInt64(entry.OutputTokens),
		CachedInputTokens:    nullInt64(entry.CachedInputTokens),
		ReasoningEffort:      nullString(entry.ReasoningEffort),
		CostUSD:              nullFloat64(entry.CostUSD),
		CostBreakdown:        map[string]any{"inputUsd": nil, "cachedInputUsd": nil, "outputUsd": nil, "totalUsd": nullFloat64(entry.CostUSD)},
		LatencyMS:            nullInt64(entry.LatencyMS),
	}
}

func parseInt(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func nullString(value sql.NullString) *string {
	if !value.Valid || value.String == "" {
		return nil
	}
	return &value.String
}

func nullInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func nullFloat64(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

func nullInt64Sum(values ...sql.NullInt64) *int64 {
	var total int64
	var found bool
	for _, value := range values {
		if value.Valid {
			total += value.Int64
			found = true
		}
	}
	if !found {
		return nil
	}
	return &total
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]any{
		"error": map[string]string{"code": "server_error", "message": err.Error()},
	})
}
