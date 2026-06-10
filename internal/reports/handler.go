package reports

import (
	"database/sql"
	"math"
	"net/http"
	"time"

	"github.com/soju06/codex-lb/internal/httputil"
)

type Handler struct {
	repo Repository
}

type dailyResponse struct {
	Date               string  `json:"date"`
	Requests           int     `json:"requests"`
	InputTokens        int     `json:"inputTokens"`
	OutputTokens       int     `json:"outputTokens"`
	CachedInputTokens  int     `json:"cachedInputTokens"`
	CostUSD            float64 `json:"costUsd"`
	ActiveAccounts     int     `json:"activeAccounts"`
	ErrorCount         int     `json:"errorCount"`
}

type modelResponse struct {
	Model      string  `json:"model"`
	CostUSD    float64 `json:"costUsd"`
	Percentage float64 `json:"percentage"`
}

type accountResponse struct {
	AccountID *string `json:"accountId"`
	Alias     *string `json:"alias"`
	CostUSD   float64 `json:"costUsd"`
	Requests  int     `json:"requests"`
}

type summaryResponse struct {
	TotalCostUSD       float64 `json:"totalCostUsd"`
	TotalInputTokens   int     `json:"totalInputTokens"`
	TotalOutputTokens  int     `json:"totalOutputTokens"`
	TotalCachedTokens  int     `json:"totalCachedTokens"`
	TotalRequests      int     `json:"totalRequests"`
	TotalErrors        int     `json:"totalErrors"`
	ActiveAccounts     int     `json:"activeAccounts"`
	AvgCostPerDay      float64 `json:"avgCostPerDay"`
	AvgRequestsPerDay  float64 `json:"avgRequestsPerDay"`
}

type response struct {
	Summary   summaryResponse   `json:"summary"`
	Daily     []dailyResponse   `json:"daily"`
	ByModel   []modelResponse   `json:"byModel"`
	ByAccount []accountResponse `json:"byAccount"`
}

func NewHandler(repo Repository) Handler {
	return Handler{repo: repo}
}

func (h Handler) Get(w http.ResponseWriter, r *http.Request) {
	params := parseParams(r)
	daily, err := h.repo.AggregateDaily(r.Context(), params)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	byModel, err := h.repo.AggregateByModel(r.Context(), params)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	byAccount, err := h.repo.AggregateByAccount(r.Context(), params)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	activeAccounts, err := h.repo.CountActiveAccounts(r.Context(), params)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}

	var totalCost, totalInput, totalOutput, totalCached float64
	var totalRequests, totalErrors int
	dailyResp := make([]dailyResponse, 0, len(daily))
	for _, row := range daily {
		totalCost += row.CostUSD
		totalInput += float64(row.InputTokens)
		totalOutput += float64(row.OutputTokens)
		totalCached += float64(row.CachedInputTokens)
		totalRequests += row.RequestCount
		totalErrors += row.ErrorCount
		dailyResp = append(dailyResp, dailyResponse{
			Date:              row.Date,
			Requests:          row.RequestCount,
			InputTokens:       row.InputTokens,
			OutputTokens:      row.OutputTokens,
			CachedInputTokens: row.CachedInputTokens,
			CostUSD:           round4(row.CostUSD),
			ActiveAccounts:    row.ActiveAccounts,
			ErrorCount:        row.ErrorCount,
		})
	}

	modelTotal := 0.0
	for _, row := range byModel {
		modelTotal += row.CostUSD
	}
	byModelResp := make([]modelResponse, 0, len(byModel))
	for _, row := range byModel {
		pct := 0.0
		if modelTotal > 0 {
			pct = round1(row.CostUSD / modelTotal * 100)
		}
		byModelResp = append(byModelResp, modelResponse{
			Model:      row.Model,
			CostUSD:    round4(row.CostUSD),
			Percentage: pct,
		})
	}

	byAccountResp := make([]accountResponse, 0, len(byAccount))
	for _, row := range byAccount {
		byAccountResp = append(byAccountResp, accountResponse{
			AccountID: nullStringPtr(row.AccountID),
			Alias:     nullStringPtr(row.Alias),
			CostUSD:   round4(row.CostUSD),
			Requests:  row.RequestCount,
		})
	}

	dayCount := int(params.EndDate.Sub(params.StartDate).Hours() / 24)
	if dayCount < 1 {
		dayCount = 1
	}

	httputil.WriteJSON(w, http.StatusOK, response{
		Summary: summaryResponse{
			TotalCostUSD:      round4(totalCost),
			TotalInputTokens:  int(totalInput),
			TotalOutputTokens: int(totalOutput),
			TotalCachedTokens: int(totalCached),
			TotalRequests:     totalRequests,
			TotalErrors:       totalErrors,
			ActiveAccounts:    activeAccounts,
			AvgCostPerDay:     round4(totalCost / float64(dayCount)),
			AvgRequestsPerDay: round2(float64(totalRequests) / float64(dayCount)),
		},
		Daily:     httputil.EmptySlice(dailyResp),
		ByModel:   httputil.EmptySlice(byModelResp),
		ByAccount: httputil.EmptySlice(byAccountResp),
	})
}

func parseParams(r *http.Request) Params {
	now := time.Now().UTC()
	endDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Add(24 * time.Hour)
	startDate := endDate.Add(-7 * 24 * time.Hour)

	query := r.URL.Query()
	if raw := query.Get("start_date"); raw != "" {
		if parsed, err := time.Parse("2006-01-02", raw); err == nil {
			startDate = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC)
		}
	}
	if raw := query.Get("end_date"); raw != "" {
		if parsed, err := time.Parse("2006-01-02", raw); err == nil {
			endDate = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC).Add(24 * time.Hour)
		}
	}

	accountIDs := query["account_id"]
	if len(accountIDs) == 0 {
		accountIDs = query["accountId"]
	}

	return Params{
		StartDate:  startDate,
		EndDate:    endDate,
		AccountIDs: accountIDs,
		Model:      query.Get("model"),
	}
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func round1(value float64) float64 {
	return math.Round(value*10) / 10
}

func round2(value float64) float64 {
	return math.Round(value*100) / 100
}

func round4(value float64) float64 {
	return math.Round(value*10000) / 10000
}
