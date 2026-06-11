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
	StatusFilter StatusFilter
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
	LatencyFirstTokenMS  *int64         `json:"latencyFirstTokenMs"`
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
	statusRows, err := h.repo.Statuses(r.Context(), filters)
	if err != nil {
		writeError(w, err)
		return
	}
	statuses := normalizeStatusOptions(statusRows)
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
		StatusFilter: MapStatusFilter(query["status"]),
		ModelOptions: query["modelOption"],
		Since:        query.Get("since"),
		Until:        query.Get("until"),
	}
}

func normalizeStatusOptions(rows []StatusRow) []string {
	statuses := make([]string, 0, len(rows))
	errorCodes := make([]*string, 0, len(rows))
	for _, row := range rows {
		statuses = append(statuses, row.Status)
		errorCodes = append(errorCodes, nullString(row.ErrorCode))
	}
	return NormalizeStatusValues(statuses, errorCodes)
}

func mapEntry(entry Entry) logEntry {
	outputTokens := outputTokensFromEntry(entry)
	cachedInputTokens := cachedInputTokensFromEntry(entry)
	tokens := totalTokensFromEntry(entry, outputTokens)
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
		RequestKind:          NormalizeRequestKind(entry.RequestKind),
		Model:                entry.Model,
		Source:               nullString(entry.Source),
		Transport:            nullString(entry.Transport),
		UserAgent:            nullString(entry.UserAgent),
		UserAgentGroup:       nullString(entry.UserAgentGroup),
		ServiceTier:          nullString(entry.ServiceTier),
		RequestedServiceTier: nullString(entry.RequestedServiceTier),
		ActualServiceTier:    nullString(entry.ActualServiceTier),
		Status:               NormalizeLogStatus(entry.Status, nullString(entry.ErrorCode)),
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
		OutputTokens:         outputTokens,
		CachedInputTokens:    cachedInputTokens,
		ReasoningEffort:      nullString(entry.ReasoningEffort),
		CostUSD:              nullFloat64(entry.CostUSD),
		CostBreakdown:        costBreakdownFromEntry(entry),
		LatencyMS:            nullInt64(entry.LatencyMS),
		LatencyFirstTokenMS:  nullInt64(entry.LatencyFirstTokenMS),
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

func outputTokensFromEntry(entry Entry) *int64 {
	if entry.OutputTokens.Valid {
		return &entry.OutputTokens.Int64
	}
	if entry.ReasoningTokens.Valid {
		return &entry.ReasoningTokens.Int64
	}
	return nil
}

func cachedInputTokensFromEntry(entry Entry) *int64 {
	if !entry.CachedInputTokens.Valid {
		return nil
	}
	cached := entry.CachedInputTokens.Int64
	if entry.InputTokens.Valid && cached > entry.InputTokens.Int64 {
		cached = entry.InputTokens.Int64
	}
	if cached < 0 {
		cached = 0
	}
	return &cached
}

func totalTokensFromEntry(entry Entry, outputTokens *int64) *int64 {
	var total int64
	var found bool
	if entry.InputTokens.Valid {
		total += entry.InputTokens.Int64
		found = true
	}
	if outputTokens != nil {
		total += *outputTokens
		found = true
	}
	if !found {
		return nil
	}
	return &total
}

func costBreakdownFromEntry(entry Entry) map[string]any {
	breakdown := costBreakdownFromLogEntry(entry, 6)
	return map[string]any{
		"inputUsd":       breakdown.inputUSD,
		"cachedInputUsd": breakdown.cachedInputUSD,
		"outputUsd":      breakdown.outputUSD,
		"totalUsd":       breakdown.totalUSD,
	}
}

func costBreakdownFromLogEntry(entry Entry, precision int) costBreakdown {
	var full *costBreakdown
	var inputUSD *float64
	var cachedInputUSD *float64
	var outputUSD *float64
	var rawTotalUSD *float64
	var totalUSD *float64
	price, ok := priceForModel(entry.Model)
	if ok {
		inputTokens := nullInt64(entry.InputTokens)
		cachedTokens := cachedInputTokensFromEntry(entry)
		outputTokens := outputTokensFromEntry(entry)
		serviceTier := ""
		if entry.ServiceTier.Valid {
			serviceTier = entry.ServiceTier.String
		}
		if inputTokens != nil && outputTokens != nil {
			fullUsage := usageTokens{
				inputTokens:       float64(*inputTokens),
				outputTokens:      float64(*outputTokens),
				cachedInputTokens: 0,
			}
			if cachedTokens != nil {
				fullUsage.cachedInputTokens = float64(*cachedTokens)
			}
			calculated := calculateCostBreakdown(fullUsage, price, serviceTier, &precision)
			full = &calculated
			rawTotalUSD = calculated.rawTotalUSD
			totalUSD = calculated.totalUSD
		}
		if inputTokens != nil && cachedTokens != nil {
			inputOnly := calculateCostBreakdown(usageTokens{
				inputTokens:       float64(*inputTokens),
				outputTokens:      0,
				cachedInputTokens: float64(*cachedTokens),
			}, price, serviceTier, &precision)
			inputUSD = inputOnly.inputUSD
			cachedInputUSD = inputOnly.cachedInputUSD
		}
		if outputTokens != nil {
			outputUsage := usageTokens{outputTokens: float64(*outputTokens)}
			if inputTokens != nil {
				outputUsage.inputTokens = float64(*inputTokens)
			}
			if cachedTokens != nil {
				outputUsage.cachedInputTokens = float64(*cachedTokens)
			}
			outputOnly := calculateCostBreakdown(outputUsage, price, serviceTier, &precision)
			outputUSD = outputOnly.outputUSD
		}
	}

	if entry.CostUSD.Valid {
		persisted := roundTo(entry.CostUSD.Float64, precision)
		persistedRaw := entry.CostUSD.Float64
		if !totalsMatch(&persistedRaw, rawTotalUSD, precision) {
			return costBreakdown{totalUSD: &persisted}
		}
		return costBreakdown{
			inputUSD:       inputUSD,
			cachedInputUSD: cachedInputUSD,
			outputUSD:      outputUSD,
			totalUSD:       &persisted,
		}
	}
	if full != nil {
		return costBreakdown{
			inputUSD:       inputUSD,
			cachedInputUSD: cachedInputUSD,
			outputUSD:      outputUSD,
			totalUSD:       totalUSD,
		}
	}
	return costBreakdown{
		inputUSD:       inputUSD,
		cachedInputUSD: cachedInputUSD,
		outputUSD:      outputUSD,
	}
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
