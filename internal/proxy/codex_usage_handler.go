package proxy

import (
	"net/http"
	"strconv"
	"time"

	"github.com/soju06/codex-lb/internal/apikeys"
	"github.com/soju06/codex-lb/internal/httputil"
	"github.com/soju06/codex-lb/internal/settings"
)

type CodexUsageHandler struct {
	apiKeysRepo  apikeys.Repository
	settingsRepo settings.Repository
}

type codexUsagePayload struct {
	PlanType             string                 `json:"planType"`
	RateLimit            *codexRateLimitDetails `json:"rateLimit"`
	Credits              *codexCreditDetails    `json:"credits"`
	AdditionalRateLimits []any                  `json:"additionalRateLimits"`
}

type codexRateLimitDetails struct {
	Allowed         bool                  `json:"allowed"`
	LimitReached    bool                  `json:"limitReached"`
	PrimaryWindow   *codexRateLimitWindow `json:"primaryWindow,omitempty"`
	SecondaryWindow *codexRateLimitWindow `json:"secondaryWindow,omitempty"`
	MonthlyWindow   *codexRateLimitWindow `json:"monthlyWindow,omitempty"`
}

type codexRateLimitWindow struct {
	UsedPercent        int64  `json:"usedPercent"`
	LimitWindowSeconds *int64 `json:"limitWindowSeconds,omitempty"`
	ResetAfterSeconds  *int64 `json:"resetAfterSeconds,omitempty"`
	ResetAt            *int64 `json:"resetAt,omitempty"`
}

type codexCreditDetails struct {
	HasCredits          bool    `json:"hasCredits"`
	Unlimited           bool    `json:"unlimited"`
	Balance             *string `json:"balance"`
	ApproxLocalMessages []any   `json:"approxLocalMessages"`
	ApproxCloudMessages []any   `json:"approxCloudMessages"`
}

func NewCodexUsageHandler(apiKeysRepo apikeys.Repository, settingsRepo settings.Repository) CodexUsageHandler {
	return CodexUsageHandler{apiKeysRepo: apiKeysRepo, settingsRepo: settingsRepo}
}

func (h CodexUsageHandler) Get(w http.ResponseWriter, r *http.Request) {
	apiKey, err := ValidateProxyAPIKeyRequired(r.Context(), h.apiKeysRepo, r)
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}

	usage, err := h.apiKeysRepo.SelfUsage(r.Context(), apiKey.ID)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if usage == nil {
		httputil.WriteError(w, http.StatusUnauthorized, "invalid_api_key", "Invalid API key")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, buildCodexUsagePayload(usage))
}

func (h CodexUsageHandler) V1Usage(w http.ResponseWriter, r *http.Request) {
	apiKey, err := ValidateProxyAPIKeyRequired(r.Context(), h.apiKeysRepo, r)
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			WriteError(w, appErr)
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	if apiKey == nil {
		httputil.WriteError(w, http.StatusUnauthorized, "invalid_api_key", "Invalid API key")
		return
	}
	usage, err := h.apiKeysRepo.SelfUsage(r.Context(), apiKey.ID)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if usage == nil {
		httputil.WriteError(w, http.StatusUnauthorized, "invalid_api_key", "Invalid API key")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, usage)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func buildCodexUsagePayload(usage *apikeys.SelfUsage) codexUsagePayload {
	primary := selectCodexUsageLimit(usage.Limits, "5h")
	if primary == nil {
		primary = selectCodexUsageLimit(usage.Limits, "daily")
	}
	secondary := selectCodexUsageLimit(usage.Limits, "7d")
	if secondary == nil {
		secondary = selectCodexUsageLimit(usage.Limits, "weekly")
	}
	monthly := selectCodexUsageLimit(usage.Limits, "monthly")
	return codexUsagePayload{
		PlanType:             "api_key",
		RateLimit:            codexRateLimitFromWindows(codexUsageWindowSnapshot(primary), codexUsageWindowSnapshot(secondary), codexUsageWindowSnapshot(monthly)),
		Credits:              codexCreditSnapshot(primary, secondary, monthly),
		AdditionalRateLimits: []any{},
	}
}

func selectCodexUsageLimit(limits []apikeys.SelfLimit, window string) *apikeys.SelfLimit {
	for i := range limits {
		limit := limits[i]
		if limit.LimitWindow == window && limit.ModelFilter == nil && limit.LimitType == "credits" {
			return &limit
		}
	}
	return nil
}

func codexRateLimitFromWindows(primary, secondary, monthly *codexRateLimitWindow) *codexRateLimitDetails {
	if primary == nil && secondary == nil && monthly == nil {
		return nil
	}
	limitReached := windowLimitReached(primary) || windowLimitReached(secondary) || windowLimitReached(monthly)
	return &codexRateLimitDetails{
		Allowed:         !limitReached,
		LimitReached:    limitReached,
		PrimaryWindow:   primary,
		SecondaryWindow: secondary,
		MonthlyWindow:   monthly,
	}
}

func windowLimitReached(window *codexRateLimitWindow) bool {
	return window != nil && window.UsedPercent >= 100
}

func codexUsageWindowSnapshot(limit *apikeys.SelfLimit) *codexRateLimitWindow {
	if limit == nil || limit.MaxValue <= 0 {
		return nil
	}
	resetAt, ok := parseISOTime(limit.ResetAt)
	if !ok {
		return nil
	}
	resetEpoch := resetAt.Unix()
	nowEpoch := time.Now().UTC().Unix()
	usedPercent := int64(0)
	if limit.MaxValue > 0 {
		usedPercent = (limit.CurrentValue * 100) / limit.MaxValue
	}
	usedPercent = maxInt64(0, minInt64(100, usedPercent))
	resetAfter := maxInt64(0, resetEpoch-nowEpoch)
	return &codexRateLimitWindow{
		UsedPercent:        usedPercent,
		LimitWindowSeconds: limitWindowSeconds(limit.LimitWindow),
		ResetAfterSeconds:  &resetAfter,
		ResetAt:            &resetEpoch,
	}
}

func codexCreditSnapshot(primary, secondary, monthly *apikeys.SelfLimit) *codexCreditDetails {
	preferred := monthly
	if preferred == nil {
		preferred = secondary
	}
	if preferred == nil {
		preferred = primary
	}
	if preferred == nil || preferred.LimitType != "credits" {
		return nil
	}
	balance := formatInt64(maxInt64(0, preferred.RemainingValue))
	return &codexCreditDetails{
		HasCredits:          preferred.RemainingValue > 0,
		Unlimited:           false,
		Balance:             &balance,
		ApproxLocalMessages: nil,
		ApproxCloudMessages: nil,
	}
}

func limitWindowSeconds(window string) *int64 {
	var seconds int64
	switch window {
	case "5h":
		seconds = 18000
	case "daily":
		seconds = 86400
	case "7d", "weekly":
		seconds = 604800
	case "monthly":
		seconds = 2592000
	default:
		return nil
	}
	return &seconds
}

func parseISOTime(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02 15:04:05"} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func formatInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}
