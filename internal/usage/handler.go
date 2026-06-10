package usage

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"context"

	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/httputil"
	"github.com/soju06/codex-lb/internal/platform"
	"github.com/soju06/codex-lb/internal/requestlogs"
)

// Handler serves the /api/usage/* endpoints, mirroring app.modules.usage.api.
type Handler struct {
	repo        Repository
	accountRepo accounts.Repository
	logsRepo    requestlogs.Repository
}

func NewHandler(repo Repository, accountRepo accounts.Repository, logsRepo requestlogs.Repository) Handler {
	return Handler{repo: repo, accountRepo: accountRepo, logsRepo: logsRepo}
}

type windowResponse struct {
	RemainingPercent float64 `json:"remainingPercent"`
	CapacityCredits  float64 `json:"capacityCredits"`
	RemainingCredits float64 `json:"remainingCredits"`
	ResetAt          *string `json:"resetAt"`
	WindowMinutes    *int64  `json:"windowMinutes"`
}

type costResponse struct {
	Currency   string  `json:"currency"`
	TotalUsd7d float64 `json:"totalUsd7d"`
}

type metricsResponse struct {
	Requests7d                  int64    `json:"requests7d"`
	TokensSecondaryWindow       int64    `json:"tokensSecondaryWindow"`
	CachedTokensSecondaryWindow int64    `json:"cachedTokensSecondaryWindow"`
	ErrorRate7d                 *float64 `json:"errorRate7d"`
	TopError                    *string  `json:"topError"`
}

type summaryResponse struct {
	PrimaryWindow   windowResponse   `json:"primaryWindow"`
	SecondaryWindow *windowResponse  `json:"secondaryWindow"`
	MonthlyWindow   *windowResponse  `json:"monthlyWindow"`
	Cost            costResponse     `json:"cost"`
	Metrics         *metricsResponse `json:"metrics"`
}

type historyItem struct {
	AccountID           string   `json:"accountId"`
	RemainingPercentAvg *float64 `json:"remainingPercentAvg"`
	CapacityCredits     float64  `json:"capacityCredits"`
	RemainingCredits    float64  `json:"remainingCredits"`
}

type historyResponse struct {
	WindowHours int           `json:"windowHours"`
	Accounts    []historyItem `json:"accounts"`
}

type windowListResponse struct {
	WindowKey     string        `json:"windowKey"`
	WindowMinutes *int64        `json:"windowMinutes"`
	Accounts      []historyItem `json:"accounts"`
}

func snapshotToWindowResponse(snapshot WindowSnapshot) windowResponse {
	remainingCredits := remainingCreditsFromUsed(&snapshot.UsedCredits, &snapshot.CapacityCredits)
	remaining := 0.0
	if remainingCredits != nil {
		remaining = *remainingCredits
	}
	remainingPercent := 0.0
	if snapshot.CapacityCredits > 0 {
		remainingPercent = 100.0 - snapshot.UsedPercent
		if remainingPercent < 0 {
			remainingPercent = 0
		}
	}

	var resetAt *int64
	if snapshot.ResetAt != nil {
		resetAt = snapshot.ResetAt
	}
	return windowResponse{
		RemainingPercent: remainingPercent,
		CapacityCredits:  snapshot.CapacityCredits,
		RemainingCredits: remaining,
		ResetAt:          platform.UnixSecondsToISO(nullInt64(resetAt)),
		WindowMinutes:    snapshot.WindowMinutes,
	}
}

func nullInt64(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

// Summary handles GET /api/usage/summary.
func (h Handler) Summary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	allAccounts, err := h.accountRepo.List(ctx)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list accounts")
		return
	}
	planByAccount := make(map[string]string, len(allAccounts))
	for _, account := range allAccounts {
		planByAccount[account.ID] = account.PlanType
	}

	primaryRows, err := h.latestWindowRows(ctx, "primary")
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load primary usage")
		return
	}
	secondaryRows, err := h.latestWindowRows(ctx, "secondary")
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load secondary usage")
		return
	}
	monthlyRows, err := h.latestWindowRows(ctx, "monthly")
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load monthly usage")
		return
	}

	primaryRows, secondaryRows = normalizeWeeklyOnlyRows(primaryRows, secondaryRows)
	secondaryMinutes := resolveWindowMinutes("secondary", secondaryRows)

	var costMetrics requestlogs.CostMetrics
	if secondaryMinutes != nil && *secondaryMinutes > 0 {
		since := time.Now().UTC().Add(-time.Duration(*secondaryMinutes) * time.Minute).Format("2006-01-02 15:04:05")
		costMetrics, err = h.logsRepo.AggregateCostMetrics(ctx, since)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "server_error", "failed to aggregate request log metrics")
			return
		}
	}

	primarySnapshot := normalizeUsageWindow(summarizeUsageWindow(primaryRows, planByAccount, "primary"))
	secondarySnapshot := normalizeUsageWindow(summarizeUsageWindow(secondaryRows, planByAccount, "secondary"))
	monthlySnapshot := normalizeUsageWindow(summarizeUsageWindow(monthlyRows, planByAccount, "monthly"))

	secondaryResponse := snapshotToWindowResponse(secondarySnapshot)
	monthlyResponse := snapshotToWindowResponse(monthlySnapshot)

	resp := summaryResponse{
		PrimaryWindow:   snapshotToWindowResponse(primarySnapshot),
		SecondaryWindow: &secondaryResponse,
		MonthlyWindow:   &monthlyResponse,
		Cost: costResponse{
			Currency:   "USD",
			TotalUsd7d: roundTo(costMetrics.TotalCostUSD, 6),
		},
	}

	if costMetrics.Requests > 0 {
		var errorRate *float64
		rate := float64(costMetrics.Errors) / float64(costMetrics.Requests)
		errorRate = &rate
		resp.Metrics = &metricsResponse{
			Requests7d:                  costMetrics.Requests,
			TokensSecondaryWindow:       costMetrics.TotalTokens,
			CachedTokensSecondaryWindow: costMetrics.CachedInputTokens,
			ErrorRate7d:                 errorRate,
			TopError:                    costMetrics.TopErrorCode,
		}
	} else {
		resp.Metrics = &metricsResponse{
			Requests7d:                  0,
			TokensSecondaryWindow:       0,
			CachedTokensSecondaryWindow: 0,
			ErrorRate7d:                 nil,
			TopError:                    nil,
		}
	}

	httputil.WriteJSON(w, http.StatusOK, resp)
}

// History handles GET /api/usage/history?hours=24.
func (h Handler) History(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	hours := 24
	if raw := r.URL.Query().Get("hours"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 168 {
			httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "hours must be between 1 and 168")
			return
		}
		hours = parsed
	}

	allAccounts, err := h.accountRepo.List(ctx)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list accounts")
		return
	}

	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour).Format("2006-01-02 15:04:05")
	aggregates, err := h.repo.AggregateSince(ctx, since, "primary")
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "server_error", "failed to aggregate usage history")
		return
	}
	rows := make([]WindowRow, 0, len(aggregates))
	for _, aggregate := range aggregates {
		rows = append(rows, aggregate.ToWindowRow())
	}

	missing := 100.0
	httputil.WriteJSON(w, http.StatusOK, historyResponse{
		WindowHours: hours,
		Accounts:    buildAccountHistory(rows, allAccounts, "primary", &missing),
	})
}

// Window handles GET /api/usage/window?window=primary|secondary.
func (h Handler) Window(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	windowKey := normalizeWindowKey(r.URL.Query().Get("window"))
	if windowKey == "" {
		windowKey = "primary"
	}
	if windowKey != "primary" && windowKey != "secondary" {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "window must be primary or secondary")
		return
	}

	allAccounts, err := h.accountRepo.List(ctx)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list accounts")
		return
	}

	primaryRows, err := h.latestWindowRows(ctx, "primary")
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load primary usage")
		return
	}
	secondaryRows, err := h.latestWindowRows(ctx, "secondary")
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load secondary usage")
		return
	}
	primaryRows, secondaryRows = normalizeWeeklyOnlyRows(primaryRows, secondaryRows)

	rows := primaryRows
	if windowKey == "secondary" {
		rows = secondaryRows
	}
	windowMinutes := resolveWindowMinutes(windowKey, rows)

	httputil.WriteJSON(w, http.StatusOK, windowListResponse{
		WindowKey:     windowKey,
		WindowMinutes: windowMinutes,
		Accounts:      buildAccountHistory(rows, allAccounts, windowKey, nil),
	})
}

func (h Handler) latestWindowRows(ctx context.Context, window string) ([]WindowRow, error) {
	latest, err := h.repo.LatestByAccount(ctx, window, nil)
	if err != nil {
		return nil, err
	}
	rows := make([]WindowRow, 0, len(latest))
	for _, entry := range latest {
		rows = append(rows, entry.ToWindowRow())
	}
	return rows, nil
}

// buildAccountHistory ports app.modules.usage.builders._build_account_history.
func buildAccountHistory(rows []WindowRow, accountList []accounts.Account, window string, missingRemainingPercent *float64) []historyItem {
	usageByAccount := make(map[string]WindowRow, len(rows))
	for _, row := range rows {
		usageByAccount[row.AccountID] = row
	}

	results := make([]historyItem, 0, len(accountList))
	for _, account := range accountList {
		usage, ok := usageByAccount[account.ID]
		var usedPercent *float64
		if ok {
			usedPercent = usage.UsedPercent
		}

		remainingPercent := remainingPercentFromUsed(usedPercent)
		if remainingPercent == nil {
			remainingPercent = missingRemainingPercent
		}

		capacity := capacityForPlan(account.PlanType, window)
		capacityValue := 0.0
		if capacity != nil {
			capacityValue = *capacity
		}

		remainingCredits := remainingCreditsFromPercent(usedPercent, capacity)
		if remainingCredits == nil && missingRemainingPercent != nil {
			remainingCredits = capacity
		}
		remainingCreditsValue := 0.0
		if remainingCredits != nil {
			remainingCreditsValue = *remainingCredits
		}

		results = append(results, historyItem{
			AccountID:           account.ID,
			RemainingPercentAvg: remainingPercent,
			CapacityCredits:     capacityValue,
			RemainingCredits:    remainingCreditsValue,
		})
	}
	return results
}

func roundTo(value float64, places int) float64 {
	scale := 1.0
	for i := 0; i < places; i++ {
		scale *= 10
	}
	rounded := value * scale
	if rounded >= 0 {
		rounded = float64(int64(rounded + 0.5))
	} else {
		rounded = float64(int64(rounded - 0.5))
	}
	return rounded / scale
}
