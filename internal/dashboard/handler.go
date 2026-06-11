package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/cache"
)

type Handler struct {
	repo                        Repository
	accounts                    accounts.Handler
	overview                    *cache.TTL[overviewResponse]
	usageRefreshIntervalSeconds int
	weeklyPaceEnabled           bool
}

type timeframe struct {
	Key           string `json:"key"`
	WindowMinutes int    `json:"windowMinutes"`
	BucketSeconds int    `json:"bucketSeconds"`
	BucketCount   int    `json:"bucketCount"`
}

type trendValue struct {
	T string  `json:"t"`
	V float64 `json:"v"`
}

type overviewResponse struct {
	LastSyncAt         *string                   `json:"lastSyncAt"`
	Timeframe          timeframe                 `json:"timeframe"`
	Accounts           []accounts.AccountSummary `json:"accounts"`
	Summary            overviewSummary           `json:"summary"`
	Windows            overviewWindows           `json:"windows"`
	Trends             map[string][]trendValue   `json:"trends"`
	AdditionalQuotas   []any                     `json:"additionalQuotas"`
	DepletionPrimary   any                       `json:"depletionPrimary"`
	DepletionSecondary any                       `json:"depletionSecondary"`
	WeeklyCreditPace   any                       `json:"weeklyCreditPace"`
}

type overviewSummary struct {
	PrimaryWindow   summaryWindow  `json:"primaryWindow"`
	SecondaryWindow *summaryWindow `json:"secondaryWindow"`
	Cost            costSummary    `json:"cost"`
	Metrics         metricsSummary `json:"metrics"`
}

type summaryWindow struct {
	RemainingPercent float64 `json:"remainingPercent"`
	CapacityCredits  float64 `json:"capacityCredits"`
	RemainingCredits float64 `json:"remainingCredits"`
	ResetAt          *string `json:"resetAt"`
	WindowMinutes    *int64  `json:"windowMinutes"`
}

type costSummary struct {
	Currency string  `json:"currency"`
	TotalUSD float64 `json:"totalUsd"`
}

type metricsSummary struct {
	Requests          *int64   `json:"requests"`
	Tokens            *int64   `json:"tokens"`
	CachedInputTokens *int64   `json:"cachedInputTokens"`
	ErrorRate         *float64 `json:"errorRate"`
	ErrorCount        *int64   `json:"errorCount"`
	TopError          *string  `json:"topError"`
}

type usageWindow struct {
	WindowKey     string               `json:"windowKey"`
	WindowMinutes *int64               `json:"windowMinutes"`
	Accounts      []usageWindowAccount `json:"accounts"`
}

type usageWindowAccount struct {
	AccountID           string   `json:"accountId"`
	RemainingPercentAvg *float64 `json:"remainingPercentAvg"`
	CapacityCredits     float64  `json:"capacityCredits"`
	RemainingCredits    float64  `json:"remainingCredits"`
}

type overviewWindows struct {
	Primary   usageWindow  `json:"primary"`
	Secondary *usageWindow `json:"secondary"`
}

func NewHandler(repo Repository, accountHandler accounts.Handler, usageRefreshInterval ...time.Duration) Handler {
	intervalSeconds := 60
	if len(usageRefreshInterval) > 0 && usageRefreshInterval[0] > 0 {
		intervalSeconds = int(usageRefreshInterval[0].Seconds())
	}
	return Handler{
		repo:                        repo,
		accounts:                    accountHandler,
		overview:                    cache.NewTTL[overviewResponse](2 * time.Second),
		usageRefreshIntervalSeconds: intervalSeconds,
		weeklyPaceEnabled:           true,
	}
}

func (h Handler) Overview(w http.ResponseWriter, r *http.Request) {
	tf := resolveTimeframe(r.URL.Query().Get("timeframe"))
	if cached, ok := h.overview.Get(tf.Key); ok {
		writeJSON(w, http.StatusOK, cached)
		return
	}
	since := time.Now().UTC().Add(-time.Duration(tf.WindowMinutes) * time.Minute)

	accountSummaries, err := h.accounts.Summaries(r)
	if err != nil {
		writeError(w, err)
		return
	}
	sortOverviewAccounts(accountSummaries)
	activity, err := h.repo.AggregateActivitySince(r.Context(), since)
	if err != nil {
		writeError(w, err)
		return
	}
	topError, err := h.repo.TopErrorSince(r.Context(), since)
	if err != nil {
		writeError(w, err)
		return
	}
	trendRows, err := h.repo.Trends(r.Context(), since, tf.BucketSeconds)
	if err != nil {
		writeError(w, err)
		return
	}
	lastSyncAt, err := h.repo.LatestSyncAt(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	depletionPrimary, depletionSecondary, err := h.depletion(r.Context(), time.Now().UTC())
	if err != nil {
		writeError(w, err)
		return
	}
	weeklyPace, err := h.weeklyCreditPace(r, accountSummaries, time.Now().UTC())
	if err != nil {
		writeError(w, err)
		return
	}

	response := overviewResponse{
		LastSyncAt: lastSyncAt,
		Timeframe:  tf,
		Accounts:   accountSummaries,
		Summary: overviewSummary{
			PrimaryWindow:   aggregateWindow(accountSummaries, "primary"),
			SecondaryWindow: ptrSummaryWindow(aggregateWindow(accountSummaries, "secondary")),
			Cost:            costSummary{Currency: "USD", TotalUSD: nullFloat(activity.TotalCostUSD)},
			Metrics: metricsSummary{
				Requests:          &activity.Requests,
				Tokens:            int64ValuePtr(nullInt(activity.InputTokens) + nullInt(activity.OutputTokens)),
				CachedInputTokens: int64ValuePtr(nullInt(activity.CachedInputTokens)),
				ErrorRate:         errorRate(activity.Errors, activity.Requests),
				ErrorCount:        &activity.Errors,
				TopError:          topError,
			},
		},
		Windows: overviewWindows{
			Primary:   aggregateUsageWindow(accountSummaries, "primary"),
			Secondary: ptrUsageWindow(aggregateUsageWindow(accountSummaries, "secondary")),
		},
		Trends:             buildTrends(trendRows),
		AdditionalQuotas:   overviewAdditionalQuotas(accountSummaries),
		DepletionPrimary:   depletionPrimary,
		DepletionSecondary: depletionSecondary,
		WeeklyCreditPace:   weeklyPace,
	}
	h.overview.Set(tf.Key, response)
	writeJSON(w, http.StatusOK, response)
}

func sortOverviewAccounts(items []accounts.AccountSummary) {
	sort.SliceStable(items, func(i, j int) bool {
		left := primaryCapacityValue(items[i])
		right := primaryCapacityValue(items[j])
		if left == right {
			return false
		}
		return left > right
	})
}

func primaryCapacityValue(item accounts.AccountSummary) float64 {
	if item.CapacityCreditsPrimary == nil {
		return 0
	}
	return *item.CapacityCreditsPrimary
}

func overviewAdditionalQuotas(items []accounts.AccountSummary) []any {
	type keyedQuota struct {
		key   string
		quota accounts.AdditionalQuotaSummary
	}
	seen := map[string]accounts.AdditionalQuotaSummary{}
	for _, account := range items {
		for _, quota := range account.AdditionalQuotas {
			key := additionalQuotaKey(quota)
			if _, exists := seen[key]; !exists {
				seen[key] = quota
			}
		}
	}
	ordered := make([]keyedQuota, 0, len(seen))
	for key, quota := range seen {
		ordered = append(ordered, keyedQuota{key: key, quota: quota})
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].key < ordered[j].key
	})
	result := make([]any, 0, len(ordered))
	for _, item := range ordered {
		result = append(result, item.quota)
	}
	return result
}

func additionalQuotaKey(quota accounts.AdditionalQuotaSummary) string {
	key := ""
	if quota.QuotaKey != nil {
		key = *quota.QuotaKey
	}
	label := ""
	if quota.DisplayLabel != nil {
		label = *quota.DisplayLabel
	}
	return key + "\x00" + quota.LimitName + "\x00" + quota.MeteredFeature + "\x00" + quota.RoutingPolicy + "\x00" + label
}

type projectionsResponse struct {
	DepletionPrimary   any `json:"depletionPrimary"`
	DepletionSecondary any `json:"depletionSecondary"`
	WeeklyCreditPace   any `json:"weeklyCreditPace"`
}

func (h Handler) Projections(w http.ResponseWriter, r *http.Request) {
	primary, secondary, err := h.depletion(r.Context(), time.Now().UTC())
	if err != nil {
		writeError(w, err)
		return
	}
	weeklyPace, err := h.weeklyCreditPace(r, nil, time.Now().UTC())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectionsResponse{
		DepletionPrimary:   primary,
		DepletionSecondary: secondary,
		WeeklyCreditPace:   weeklyPace,
	})
}

func (h Handler) depletion(ctx context.Context, now time.Time) (*depletionResponse, *depletionResponse, error) {
	rows, err := h.repo.UsageHistorySince(ctx, now.Add(-8*24*time.Hour))
	if err != nil {
		return nil, nil, err
	}
	primary, secondary := buildDepletionByWindow(rows, now)
	return primary, secondary, nil
}

func (h Handler) weeklyCreditPace(r *http.Request, summaries []accounts.AccountSummary, now time.Time) (*weeklyCreditPaceResponse, error) {
	if !h.weeklyPaceEnabled {
		return nil, nil
	}
	if summaries == nil {
		var err error
		summaries, err = h.accounts.Summaries(r)
		if err != nil {
			return nil, err
		}
	}
	rows, err := h.repo.UsageHistorySince(r.Context(), now.Add(-8*24*time.Hour))
	if err != nil {
		return nil, err
	}
	workingDaysRaw, err := h.repo.WeeklyPaceWorkingDays(r.Context())
	if err != nil {
		return nil, err
	}
	return buildWeeklyCreditPace(summaries, rows, now, h.usageRefreshIntervalSeconds, parseWeeklyPaceWorkingDays(workingDaysRaw)), nil
}

func resolveTimeframe(key string) timeframe {
	switch key {
	case "1d":
		return timeframe{Key: "1d", WindowMinutes: 1440, BucketSeconds: 3600, BucketCount: 24}
	case "30d":
		return timeframe{Key: "30d", WindowMinutes: 43200, BucketSeconds: 86400, BucketCount: 30}
	default:
		return timeframe{Key: "7d", WindowMinutes: 10080, BucketSeconds: 21600, BucketCount: 28}
	}
}

func aggregateWindow(items []accounts.AccountSummary, window string) summaryWindow {
	var totalCapacity float64
	var totalRemaining float64
	var resetAt *string
	var windowMinutes *int64
	for _, item := range items {
		capacity, remaining, reset, minutes := selectWindow(item, window)
		if capacity != nil {
			totalCapacity += *capacity
		}
		if remaining != nil {
			totalRemaining += *remaining
		}
		if resetAt == nil {
			resetAt = reset
		}
		if windowMinutes == nil {
			windowMinutes = minutes
		}
	}
	percent := 0.0
	if totalCapacity > 0 {
		percent = (totalRemaining / totalCapacity) * 100
	}
	return summaryWindow{
		RemainingPercent: percent,
		CapacityCredits:  totalCapacity,
		RemainingCredits: totalRemaining,
		ResetAt:          resetAt,
		WindowMinutes:    windowMinutes,
	}
}

func aggregateUsageWindow(items []accounts.AccountSummary, window string) usageWindow {
	result := usageWindow{WindowKey: window, Accounts: []usageWindowAccount{}}
	for _, item := range items {
		capacity, remaining, _, minutes := selectWindow(item, window)
		if result.WindowMinutes == nil {
			result.WindowMinutes = minutes
		}
		capacityValue := 0.0
		remainingValue := 0.0
		var pct *float64
		if capacity != nil {
			capacityValue = *capacity
		}
		if remaining != nil {
			remainingValue = *remaining
		}
		if capacityValue > 0 {
			computed := (remainingValue / capacityValue) * 100
			pct = &computed
		}
		result.Accounts = append(result.Accounts, usageWindowAccount{
			AccountID:           item.AccountID,
			RemainingPercentAvg: pct,
			CapacityCredits:     capacityValue,
			RemainingCredits:    remainingValue,
		})
	}
	return result
}

func selectWindow(item accounts.AccountSummary, window string) (*float64, *float64, *string, *int64) {
	if window == "secondary" {
		return item.CapacityCreditsSecondary, item.RemainingCreditsSecondary, item.ResetAtSecondary, item.WindowMinutesSecondary
	}
	return item.CapacityCreditsPrimary, item.RemainingCreditsPrimary, item.ResetAtPrimary, item.WindowMinutesPrimary
}

func buildTrends(rows []TrendPoint) map[string][]trendValue {
	trends := map[string][]trendValue{
		"requests":  {},
		"tokens":    {},
		"cost":      {},
		"errorRate": {},
	}
	for _, row := range rows {
		t, err := time.Parse("2006-01-02 15:04:05", row.T)
		if err != nil {
			t = time.Now().UTC()
		}
		iso := t.UTC().Format(time.RFC3339)
		trends["requests"] = append(trends["requests"], trendValue{T: iso, V: float64(row.Requests)})
		trends["tokens"] = append(trends["tokens"], trendValue{T: iso, V: float64(row.Tokens)})
		trends["cost"] = append(trends["cost"], trendValue{T: iso, V: row.CostUSD})
		errRate := 0.0
		if row.Requests > 0 {
			errRate = float64(row.Errors) / float64(row.Requests)
		}
		trends["errorRate"] = append(trends["errorRate"], trendValue{T: iso, V: errRate})
	}
	return trends
}

func nullInt(value sql.NullInt64) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
}

func nullFloat(value sql.NullFloat64) float64 {
	if !value.Valid {
		return 0
	}
	return value.Float64
}

func ptrSummaryWindow(value summaryWindow) *summaryWindow {
	return &value
}

func ptrUsageWindow(value usageWindow) *usageWindow {
	return &value
}

func int64ValuePtr(value int64) *int64 {
	return &value
}

func errorRate(errors int64, requests int64) *float64 {
	value := 0.0
	if requests > 0 {
		value = float64(errors) / float64(requests)
	}
	return &value
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]any{
		"error": map[string]string{
			"code":    "server_error",
			"message": err.Error(),
		},
	})
}
