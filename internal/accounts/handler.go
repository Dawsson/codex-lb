package accounts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/soju06/codex-lb/internal/audit"
	"github.com/soju06/codex-lb/internal/cache"
	"github.com/soju06/codex-lb/internal/crypto"
	"github.com/soju06/codex-lb/internal/httputil"
	"github.com/soju06/codex-lb/internal/platform"
)

type Handler struct {
	repo               Repository
	summary            *cache.TTL[[]AccountSummary]
	encryptor          *crypto.Encryptor
	auditRepo          audit.Repository
	probeBaseURL       string
	probeClient        *http.Client
	probeAuthRefresher interface {
		Refresh(context.Context, Account) error
	}
	probeInvalidate func()
}

type UsageSummary struct {
	PrimaryRemainingPercent   *float64 `json:"primaryRemainingPercent"`
	SecondaryRemainingPercent *float64 `json:"secondaryRemainingPercent"`
	MonthlyRemainingPercent   *float64 `json:"monthlyRemainingPercent,omitempty"`
}

type AccountSummary struct {
	AccountID                 string                   `json:"accountId"`
	Email                     string                   `json:"email"`
	Alias                     *string                  `json:"alias"`
	DisplayName               string                   `json:"displayName"`
	WorkspaceID               *string                  `json:"workspaceId,omitempty"`
	WorkspaceLabel            *string                  `json:"workspaceLabel,omitempty"`
	SeatType                  *string                  `json:"seatType,omitempty"`
	PlanType                  string                   `json:"planType"`
	RoutingPolicy             string                   `json:"routingPolicy"`
	Status                    string                   `json:"status"`
	SecurityWorkAuthorized    bool                     `json:"securityWorkAuthorized"`
	Usage                     *UsageSummary            `json:"usage"`
	ResetAtPrimary            *string                  `json:"resetAtPrimary"`
	ResetAtSecondary          *string                  `json:"resetAtSecondary"`
	ResetAtMonthly            *string                  `json:"resetAtMonthly,omitempty"`
	WindowMinutesPrimary      *int64                   `json:"windowMinutesPrimary"`
	WindowMinutesSecondary    *int64                   `json:"windowMinutesSecondary"`
	WindowMinutesMonthly      *int64                   `json:"windowMinutesMonthly,omitempty"`
	CapacityCreditsPrimary    *float64                 `json:"capacityCreditsPrimary"`
	RemainingCreditsPrimary   *float64                 `json:"remainingCreditsPrimary"`
	CapacityCreditsSecondary  *float64                 `json:"capacityCreditsSecondary"`
	RemainingCreditsSecondary *float64                 `json:"remainingCreditsSecondary"`
	CapacityCreditsMonthly    *float64                 `json:"capacityCreditsMonthly,omitempty"`
	RemainingCreditsMonthly   *float64                 `json:"remainingCreditsMonthly,omitempty"`
	CreditsHas                *bool                    `json:"creditsHas"`
	CreditsUnlimited          *bool                    `json:"creditsUnlimited"`
	CreditsBalance            *float64                 `json:"creditsBalance"`
	RequestUsage              *RequestUsageSummary     `json:"requestUsage,omitempty"`
	AdditionalQuotas          []AdditionalQuotaSummary `json:"additionalQuotas"`
	DeactivationReason        *string                  `json:"deactivationReason,omitempty"`
	Auth                      *AccountAuthStatus       `json:"auth,omitempty"`
	LimitWarmupEnabled        bool                     `json:"limitWarmupEnabled"`
	LimitWarmup               *LimitWarmupStatus       `json:"limitWarmup"`
	IsEmailDuplicate          bool                     `json:"isEmailDuplicate"`
}

type RequestUsageSummary struct {
	RequestCount      int64   `json:"requestCount"`
	TotalTokens       int64   `json:"totalTokens"`
	CachedInputTokens int64   `json:"cachedInputTokens"`
	TotalCostUSD      float64 `json:"totalCostUsd"`
	Errors            int64   `json:"errors,omitempty"`
}

type AccountTokenStatus struct {
	ExpiresAt *string `json:"expiresAt,omitempty"`
	State     *string `json:"state,omitempty"`
}

type AccountAuthStatus struct {
	Access  *AccountTokenStatus `json:"access,omitempty"`
	Refresh *AccountTokenStatus `json:"refresh,omitempty"`
	IDToken *AccountTokenStatus `json:"idToken,omitempty"`
}

type LimitWarmupStatus struct {
	Window       string  `json:"window"`
	ResetAt      int64   `json:"resetAt"`
	Status       string  `json:"status"`
	Model        string  `json:"model"`
	AttemptedAt  string  `json:"attemptedAt"`
	CompletedAt  *string `json:"completedAt,omitempty"`
	ErrorCode    *string `json:"errorCode,omitempty"`
	ErrorMessage *string `json:"errorMessage,omitempty"`
}

type AdditionalWindowSummary struct {
	UsedPercent   float64 `json:"usedPercent"`
	ResetAt       *int64  `json:"resetAt,omitempty"`
	WindowMinutes *int64  `json:"windowMinutes,omitempty"`
}

type AdditionalQuotaSummary struct {
	QuotaKey        *string                  `json:"quotaKey,omitempty"`
	LimitName       string                   `json:"limitName"`
	MeteredFeature  string                   `json:"meteredFeature"`
	DisplayLabel    *string                  `json:"displayLabel,omitempty"`
	RoutingPolicy   string                   `json:"routingPolicy"`
	PrimaryWindow   *AdditionalWindowSummary `json:"primaryWindow,omitempty"`
	SecondaryWindow *AdditionalWindowSummary `json:"secondaryWindow,omitempty"`
}

type ListResponse struct {
	Accounts []AccountSummary `json:"accounts"`
}

func NewHandler(repo Repository, encryptor *crypto.Encryptor, auditRepo audit.Repository) Handler {
	return Handler{
		repo:         repo,
		summary:      cache.NewTTL[[]AccountSummary](2 * time.Second),
		encryptor:    encryptor,
		auditRepo:    auditRepo,
		probeBaseURL: "https://chatgpt.com/backend-api",
		probeClient:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (h Handler) WithProbeClient(baseURL string, client *http.Client) Handler {
	if strings.TrimSpace(baseURL) != "" {
		h.probeBaseURL = strings.TrimSpace(baseURL)
	}
	if client != nil {
		h.probeClient = client
	}
	return h
}

func (h Handler) WithProbeAuthRefresher(refresher interface {
	Refresh(context.Context, Account) error
}) Handler {
	h.probeAuthRefresher = refresher
	return h
}

func (h Handler) WithProbeInvalidation(invalidate func()) Handler {
	h.probeInvalidate = invalidate
	return h
}

// InvalidateSummaryCache clears the cached account summary list. It is
// called after writes that change account routing identity (e.g. OAuth
// token persistence), mirroring
// OauthService._invalidate_account_routing_caches.
func (h Handler) InvalidateSummaryCache() {
	h.summary.Clear()
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	summaries, err := h.Summaries(r)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, ListResponse{Accounts: summaries})
}

func (h Handler) Trends(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "accountID")
	ctx := r.Context()
	exists, err := h.repo.Exists(ctx, accountID)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if !exists {
		httputil.WriteError(w, http.StatusNotFound, "account_not_found", "Account not found")
		return
	}

	since := time.Now().UTC().Add(-sparklineDays * 24 * time.Hour)
	sinceEpoch := since.Unix()
	bucketCount := (sparklineDays * 24 * 3600) / detailBucketSeconds
	buckets, err := h.repo.TrendsByBucket(ctx, accountID, since, detailBucketSeconds)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	trends := BuildAccountTrends(buckets, sinceEpoch, detailBucketSeconds, bucketCount)
	trends.AccountID = accountID
	httputil.WriteJSON(w, http.StatusOK, trends)
}

func (h Handler) ExportAuth(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "accountID")
	record, err := h.repo.GetProxyRecord(r.Context(), accountID)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if record == nil {
		httputil.WriteError(w, http.StatusNotFound, "account_not_found", "Account not found")
		return
	}
	accessToken, refreshToken, idToken, err := h.decryptTokens(*record)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	filename := authExportFilename(*record)
	response := map[string]any{
		"filename": filename,
		"account": map[string]any{
			"accountId":        record.ID,
			"chatgptAccountId": nullStringValue(record.ChatGPTAccountID),
			"email":            record.Email,
		},
		"tokens": map[string]any{
			"idToken":      idToken,
			"accessToken":  accessToken,
			"refreshToken": refreshToken,
			"expiresAtMs":  tokenExpiryEpochMS(accessToken),
		},
		"codexAuthJson": map[string]any{
			"auth_mode":      "chatgpt",
			"OPENAI_API_KEY": nil,
			"tokens": map[string]any{
				"id_token":      idToken,
				"access_token":  accessToken,
				"refresh_token": refreshToken,
				"account_id":    nullStringValue(record.ChatGPTAccountID),
			},
			"last_refresh": sqliteTimeToISO(record.LastRefresh),
		},
		"opencodeAuthJson": map[string]any{
			"openai": map[string]any{
				"type":       "oauth",
				"refresh":    refreshToken,
				"access":     accessToken,
				"expires":    tokenExpiryEpochMS(accessToken),
				"account_id": nullStringValue(record.ChatGPTAccountID),
			},
		},
	}
	noStore(w)
	h.audit(r, "account_auth_exported", map[string]any{"account_id": accountID})
	httputil.WriteJSON(w, http.StatusOK, response)
}

func (h Handler) Export(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "accountID")
	record, err := h.repo.GetProxyRecord(r.Context(), accountID)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if record == nil {
		httputil.WriteError(w, http.StatusNotFound, "account_not_found", "Account not found")
		return
	}
	accessToken, refreshToken, idToken, err := h.decryptTokens(*record)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	authJSON := map[string]any{
		"auth_mode":      "chatgpt",
		"OPENAI_API_KEY": nil,
		"tokens": map[string]any{
			"id_token":      idToken,
			"access_token":  accessToken,
			"refresh_token": refreshToken,
			"account_id":    nullStringValue(record.ChatGPTAccountID),
		},
		"last_refresh": sqliteTimeToISO(record.LastRefresh),
	}
	encoded, _ := json.MarshalIndent(authJSON, "", "  ")
	noStore(w)
	h.audit(r, "account_exported", map[string]any{"account_id": accountID})
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"accountId":      record.ID,
		"email":          record.Email,
		"workspaceId":    nullStringValue(record.WorkspaceID),
		"workspaceLabel": nullStringValue(record.WorkspaceLabel),
		"seatType":       nullStringValue(record.SeatType),
		"planType":       record.PlanType,
		"status":         record.Status,
		"authJson":       string(encoded),
	})
}

func (h Handler) ExportOpenCodeAuth(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "accountID")
	record, err := h.repo.GetProxyRecord(r.Context(), accountID)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if record == nil {
		httputil.WriteError(w, http.StatusNotFound, "account_not_found", "Account not found")
		return
	}
	accessToken, refreshToken, _, err := h.decryptTokens(*record)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	noStore(w)
	h.audit(r, "account_auth_exported", map[string]any{"account_id": accountID})
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"filename": authExportFilename(*record),
		"account": map[string]any{
			"accountId":        record.ID,
			"chatgptAccountId": nullStringValue(record.ChatGPTAccountID),
			"email":            record.Email,
		},
		"authJson": map[string]any{
			"openai": map[string]any{
				"type":       "oauth",
				"refresh":    refreshToken,
				"access":     accessToken,
				"expires":    tokenExpiryEpochMS(accessToken),
				"account_id": nullStringValue(record.ChatGPTAccountID),
			},
		},
	})
}

func (h Handler) Import(w http.ResponseWriter, r *http.Request) {
	raw, err := readAuthJSONPayload(r)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_auth_json", "Invalid auth.json payload")
		return
	}
	parsed, err := parseAuthFile(raw)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_auth_json", "Invalid auth.json payload")
		return
	}
	account, err := h.oauthAccountFromAuth(parsed)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	saved, err := h.repo.UpsertOAuthAccount(r.Context(), account)
	if err != nil {
		if errors.Is(err, ErrAccountIdentityConflict) {
			httputil.WriteError(w, http.StatusConflict, "duplicate_identity_conflict", err.Error())
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	h.InvalidateSummaryCache()
	h.audit(r, "account_created", map[string]any{"account_id": saved.ID})
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"accountId":      saved.ID,
		"email":          saved.Email,
		"workspaceId":    nullStringValue(saved.WorkspaceID),
		"workspaceLabel": nullStringValue(saved.WorkspaceLabel),
		"seatType":       nullStringValue(saved.SeatType),
		"planType":       saved.PlanType,
		"status":         saved.Status,
	})
}

func (h Handler) Reactivate(w http.ResponseWriter, r *http.Request) {
	h.updateStatus(w, r, "active", "reactivated", "account_reactivated")
}

func (h Handler) Pause(w http.ResponseWriter, r *http.Request) {
	h.updateStatus(w, r, "paused", "paused", "account_paused")
}

func (h Handler) Probe(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "accountID")
	record, err := h.repo.GetProxyRecord(r.Context(), accountID)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if record == nil {
		httputil.WriteError(w, http.StatusNotFound, "account_not_found", "Account not found")
		return
	}
	if record.Status == "paused" || record.Status == "reauth_required" || record.Status == "deactivated" {
		httputil.WriteError(w, http.StatusConflict, "account_not_probable", fmt.Sprintf("Account is %s and cannot be probed", record.Status))
		return
	}
	var payload struct {
		Model string `json:"model"`
	}
	_ = json.NewDecoder(r.Body).Decode(&payload)
	model := strings.TrimSpace(payload.Model)
	if model == "" {
		model = "gpt-5.5"
	}
	primaryBefore, secondaryBefore := h.latestUsagePercents(r, accountID)
	statusBefore := record.Status
	if h.probeAuthRefresher != nil {
		refreshed, err := h.refreshProbeCredentials(r.Context(), accountID)
		if err != nil {
			httputil.WriteServerError(w, err)
			return
		}
		record = refreshed
	}
	probeStatus := h.sendProbe(r, *record, model)
	h.InvalidateSummaryCache()
	if h.probeInvalidate != nil {
		h.probeInvalidate()
	}
	primaryAfter, secondaryAfter := h.latestUsagePercents(r, accountID)
	afterRecord, _ := h.repo.GetProxyRecord(r.Context(), accountID)
	statusAfter := statusBefore
	if afterRecord != nil {
		statusAfter = afterRecord.Status
	}
	h.audit(r, "account_probed", map[string]any{"account_id": accountID, "probe_status_code": probeStatus, "model": model})
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":                     "probed",
		"accountId":                  accountID,
		"probeStatusCode":            probeStatus,
		"primaryUsedPercentBefore":   primaryBefore,
		"primaryUsedPercentAfter":    primaryAfter,
		"secondaryUsedPercentBefore": secondaryBefore,
		"secondaryUsedPercentAfter":  secondaryAfter,
		"accountStatusBefore":        statusBefore,
		"accountStatusAfter":         statusAfter,
	})
}

func (h Handler) Patch(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "accountID")
	var payload struct {
		SecurityWorkAuthorized *bool `json:"securityWorkAuthorized"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	if payload.SecurityWorkAuthorized == nil {
		httputil.WriteError(w, http.StatusBadRequest, "empty_account_update", "No supported account fields to update")
		return
	}
	ok, err := h.repo.UpdateSecurityWorkAuthorized(r.Context(), accountID, *payload.SecurityWorkAuthorized)
	h.finishMutation(w, r, ok, err, "account_updated", map[string]any{"account_id": accountID, "changed_fields": []string{"security_work_authorized"}}, map[string]string{"status": "updated"})
}

func (h Handler) Alias(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "accountID")
	var payload struct {
		Alias *string `json:"alias"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	alias := sql.NullString{}
	if payload.Alias != nil {
		trimmed := strings.TrimSpace(*payload.Alias)
		if trimmed != "" {
			alias = sql.NullString{String: trimmed, Valid: true}
		}
	}
	ok, err := h.repo.UpdateAlias(r.Context(), accountID, alias)
	response := map[string]any{"accountId": accountID, "alias": nil}
	if alias.Valid {
		response["alias"] = alias.String
	}
	h.finishMutation(w, r, ok, err, "account_alias_updated", map[string]any{"account_id": accountID}, response)
}

func (h Handler) LimitWarmup(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "accountID")
	var payload struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	ok, err := h.repo.UpdateLimitWarmupEnabled(r.Context(), accountID, payload.Enabled)
	status := "disabled"
	if payload.Enabled {
		status = "enabled"
	}
	h.finishMutation(w, r, ok, err, "account_limit_warmup_updated", map[string]any{"account_id": accountID, "enabled": payload.Enabled}, map[string]any{"status": status, "enabled": payload.Enabled})
}

func (h Handler) RoutingPolicy(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "accountID")
	var payload struct {
		RoutingPolicy string `json:"routingPolicy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	switch payload.RoutingPolicy {
	case "normal", "burn_first", "preserve":
	default:
		httputil.WriteError(w, http.StatusBadRequest, "invalid_routing_policy", "Invalid routing policy")
		return
	}
	ok, err := h.repo.UpdateRoutingPolicy(r.Context(), accountID, payload.RoutingPolicy)
	h.finishMutation(w, r, ok, err, "account_routing_policy_updated", map[string]any{"account_id": accountID, "routing_policy": payload.RoutingPolicy}, map[string]any{"accountId": accountID, "routingPolicy": payload.RoutingPolicy})
}

func (h Handler) Delete(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "accountID")
	deleteHistory := r.URL.Query().Get("delete_history") == "true" || r.URL.Query().Get("deleteHistory") == "true"
	ok, err := h.repo.Delete(r.Context(), accountID, deleteHistory)
	h.finishMutation(w, r, ok, err, "account_deleted", map[string]any{"account_id": accountID, "delete_history": deleteHistory}, map[string]string{"status": "deleted"})
}

func (h Handler) Summaries(r *http.Request) ([]AccountSummary, error) {
	if cached, ok := h.summary.Get("accounts"); ok {
		return cached, nil
	}
	ctx := r.Context()
	accountRows, err := h.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	primary, err := h.repo.LatestUsageByWindow(ctx, "primary")
	if err != nil {
		return nil, err
	}
	secondary, err := h.repo.LatestUsageByWindow(ctx, "secondary")
	if err != nil {
		return nil, err
	}
	monthly, err := h.repo.LatestUsageByWindow(ctx, "monthly")
	if err != nil {
		return nil, err
	}
	requestUsage, err := h.repo.RequestUsageSince(ctx, time.Now().UTC().Add(-7*24*time.Hour).Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, err
	}
	additionalQuotas, err := h.repo.LatestAdditionalQuotas(ctx)
	if err != nil {
		return nil, err
	}
	limitWarmups, err := h.repo.LatestLimitWarmups(ctx)
	if err != nil {
		return nil, err
	}
	duplicateKeys := duplicateDetectionKeys(accountRows)

	summaries := make([]AccountSummary, 0, len(accountRows))
	for _, account := range accountRows {
		p := primary[account.ID]
		s := secondary[account.ID]
		m := monthly[account.ID]
		effectivePrimary, effectiveSecondary := effectiveUsageWindows(p, s)
		if m.AccountID != "" && capacityForPlan(account.PlanType, "monthly") == nil {
			m = LatestUsage{}
		}
		if m.AccountID != "" {
			effectivePrimary = LatestUsage{}
			effectiveSecondary = LatestUsage{}
		}
		if capacity := capacityForPlan(account.PlanType, "primary"); capacity != nil && *capacity == 0 {
			effectivePrimary = LatestUsage{}
		}
		displayName := account.Email
		if account.Alias.Valid && account.Alias.String != "" {
			displayName = account.Alias.String
		}
		status := effectiveAccountStatus(account, effectivePrimary, effectiveSecondary, m)
		creditsHas, creditsUnlimited, creditsBalance := creditStatus(effectivePrimary, effectiveSecondary, m, p, s)
		summary := AccountSummary{
			AccountID:              account.ID,
			Email:                  account.Email,
			Alias:                  nullStringPtr(account.Alias),
			DisplayName:            displayName,
			WorkspaceID:            nullStringPtr(account.WorkspaceID),
			WorkspaceLabel:         nullStringPtr(account.WorkspaceLabel),
			SeatType:               nullStringPtr(account.SeatType),
			PlanType:               account.PlanType,
			RoutingPolicy:          account.RoutingPolicy,
			Status:                 status,
			SecurityWorkAuthorized: account.SecurityWorkAuthorized,
			Usage: &UsageSummary{
				PrimaryRemainingPercent:   remainingPercentPtr(effectivePrimary),
				SecondaryRemainingPercent: remainingPercentPtr(effectiveSecondary),
				MonthlyRemainingPercent:   remainingPercentPtr(m),
			},
			ResetAtPrimary:         platform.UnixSecondsToISO(effectivePrimary.ResetAt),
			ResetAtSecondary:       platform.UnixSecondsToISO(effectiveSecondary.ResetAt),
			ResetAtMonthly:         platform.UnixSecondsToISO(m.ResetAt),
			WindowMinutesPrimary:   nullInt64Ptr(effectivePrimary.WindowMinutes),
			WindowMinutesSecondary: nullInt64Ptr(effectiveSecondary.WindowMinutes),
			WindowMinutesMonthly:   nullInt64Ptr(m.WindowMinutes),
			CreditsHas:             creditsHas,
			CreditsBalance:         creditsBalance,
			CreditsUnlimited:       creditsUnlimited,
			RequestUsage:           requestUsageSummary(requestUsage[account.ID]),
			AdditionalQuotas:       additionalQuotaSummaries(additionalQuotas[account.ID]),
			DeactivationReason:     nullStringPtr(account.DeactivationReason),
			Auth:                   h.authStatus(account),
			LimitWarmupEnabled:     account.LimitWarmupEnabled,
			LimitWarmup:            limitWarmupStatus(limitWarmups[account.ID]),
			IsEmailDuplicate:       duplicateKeys[duplicateDetectionKey(account)],
		}
		summary.CapacityCreditsPrimary = capacityForPlan(account.PlanType, "primary")
		summary.RemainingCreditsPrimary = remainingCreditsFromUsage(effectivePrimary, summary.CapacityCreditsPrimary)
		summary.CapacityCreditsSecondary = capacityForPlan(account.PlanType, "secondary")
		summary.RemainingCreditsSecondary = remainingCreditsFromUsage(effectiveSecondary, summary.CapacityCreditsSecondary)
		summary.CapacityCreditsMonthly = capacityForPlan(account.PlanType, "monthly")
		summary.RemainingCreditsMonthly = remainingCreditsFromUsage(m, summary.CapacityCreditsMonthly)
		summaries = append(summaries, summary)
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		left := summaries[i].CapacityCreditsPrimary
		right := summaries[j].CapacityCreditsPrimary
		leftValue, rightValue := 0.0, 0.0
		if left != nil {
			leftValue = *left
		}
		if right != nil {
			rightValue = *right
		}
		if leftValue != rightValue {
			return leftValue > rightValue
		}
		return summaries[i].DisplayName < summaries[j].DisplayName
	})
	h.summary.Set("accounts", summaries)
	return summaries, nil
}

func remainingPercentPtr(usage LatestUsage) *float64 {
	if usage.AccountID == "" {
		return nil
	}
	value := max(0, 100-usage.UsedPercent)
	return &value
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid || value.String == "" {
		return nil
	}
	return &value.String
}

func nullInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func nullFloat64Ptr(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

func nullBoolPtr(value sql.NullBool) *bool {
	if !value.Valid {
		return nil
	}
	return &value.Bool
}

func boolValuePtr(value bool) *bool {
	return &value
}

func effectiveUsageWindows(primary LatestUsage, secondary LatestUsage) (LatestUsage, LatestUsage) {
	if primary.AccountID == "" {
		return LatestUsage{}, secondary
	}
	if !isWeeklyUsage(primary) {
		return primary, secondary
	}
	if secondary.AccountID == "" || shouldUseWeeklyPrimary(primary, secondary) {
		return LatestUsage{}, primary
	}
	return LatestUsage{}, secondary
}

func isWeeklyUsage(usage LatestUsage) bool {
	return usage.WindowMinutes.Valid && usage.WindowMinutes.Int64 == 10080
}

func shouldUseWeeklyPrimary(primary LatestUsage, secondary LatestUsage) bool {
	if !isWeeklyUsage(primary) {
		return false
	}
	if secondary.AccountID == "" {
		return true
	}
	if primary.RecordedAt.Valid && secondary.RecordedAt.Valid && primary.RecordedAt.String != secondary.RecordedAt.String {
		return primary.RecordedAt.String > secondary.RecordedAt.String
	}
	if primary.ResetAt.Valid && secondary.ResetAt.Valid && primary.ResetAt.Int64 != secondary.ResetAt.Int64 {
		return primary.ResetAt.Int64 > secondary.ResetAt.Int64
	}
	return primary.UsedPercent >= secondary.UsedPercent
}

func capacityForPlan(planType string, window string) *float64 {
	plan := strings.ToLower(strings.TrimSpace(planType))
	if plan == "" {
		return nil
	}
	var table map[string]float64
	switch window {
	case "primary":
		table = map[string]float64{
			"free": 0, "plus": 225, "business": 225, "team": 225, "edu": 225,
			"pro": 1500, "prolite": 1125, "enterprise": 1500,
		}
	case "secondary":
		table = map[string]float64{
			"free": 1134, "plus": 7560, "business": 7560, "team": 7560, "edu": 7560,
			"pro": 50400, "prolite": 37800, "enterprise": 50400,
		}
	case "monthly":
		table = map[string]float64{"free": 1134}
	default:
		return nil
	}
	value, ok := table[plan]
	if !ok {
		return nil
	}
	return &value
}

func remainingCreditsFromUsage(usage LatestUsage, capacity *float64) *float64 {
	if usage.AccountID == "" || capacity == nil {
		return nil
	}
	used := usage.UsedPercent
	if used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}
	value := *capacity * (100 - used) / 100
	if value < 0 {
		value = 0
	}
	return &value
}

func creditStatus(entries ...LatestUsage) (*bool, *bool, *float64) {
	for _, entry := range entries {
		if entry.AccountID == "" {
			continue
		}
		if entry.CreditsHas.Valid || entry.CreditsUnlimited.Valid || entry.CreditsBalance.Valid {
			return nullBoolPtr(entry.CreditsHas), nullBoolPtr(entry.CreditsUnlimited), nullFloat64Ptr(entry.CreditsBalance)
		}
	}
	return nil, nil, nil
}

func effectiveAccountStatus(account Account, primary LatestUsage, secondary LatestUsage, monthly LatestUsage) string {
	switch account.Status {
	case "paused", "reauth_required", "deactivated":
		return account.Status
	}
	longWindow := secondary
	if monthly.AccountID != "" {
		longWindow = monthly
	}
	hasCredits := false
	if _, unlimited, balance := creditStatus(primary, longWindow); (unlimited != nil && *unlimited) || (balance != nil && *balance > 0) {
		hasCredits = true
	}
	if account.Status == "quota_exceeded" && (hasCredits || (longWindow.AccountID != "" && longWindow.UsedPercent < 100)) {
		return "active"
	}
	if primary.AccountID != "" && primary.UsedPercent >= 100 {
		return "rate_limited"
	}
	if longWindow.AccountID != "" && longWindow.UsedPercent >= 100 && !hasCredits {
		return "quota_exceeded"
	}
	if account.Status == "rate_limited" {
		now := time.Now().UTC().Unix()
		if account.ResetAt.Valid && account.ResetAt.Int64 <= now {
			return "active"
		}
	}
	return account.Status
}

func requestUsageSummary(usage RequestUsage) *RequestUsageSummary {
	if usage.RequestCount == 0 && usage.TotalTokens == 0 && usage.CachedInputTokens == 0 && usage.TotalCostUSD == 0 && usage.Errors == 0 {
		return nil
	}
	return &RequestUsageSummary{
		RequestCount:      usage.RequestCount,
		TotalTokens:       usage.TotalTokens,
		CachedInputTokens: usage.CachedInputTokens,
		TotalCostUSD:      usage.TotalCostUSD,
		Errors:            usage.Errors,
	}
}

func additionalQuotaSummaries(rows []AdditionalQuota) []AdditionalQuotaSummary {
	summaries := make([]AdditionalQuotaSummary, 0, len(rows))
	for _, row := range rows {
		routingPolicy := row.RoutingPolicy.String
		if routingPolicy == "" {
			routingPolicy = "inherit"
		}
		displayLabel := nullStringPtr(row.DisplayLabel)
		if displayLabel == nil && row.LimitName != "" {
			displayLabel = &row.LimitName
		}
		summaries = append(summaries, AdditionalQuotaSummary{
			QuotaKey:        nullStringPtr(row.QuotaKey),
			LimitName:       row.LimitName,
			MeteredFeature:  row.MeteredFeature,
			DisplayLabel:    displayLabel,
			RoutingPolicy:   routingPolicy,
			PrimaryWindow:   additionalWindowSummary(row.PrimaryWindow),
			SecondaryWindow: additionalWindowSummary(row.SecondaryWindow),
		})
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		return summaries[i].LimitName < summaries[j].LimitName
	})
	return summaries
}

func additionalWindowSummary(row *AdditionalQuotaWindow) *AdditionalWindowSummary {
	if row == nil {
		return nil
	}
	return &AdditionalWindowSummary{
		UsedPercent:   row.UsedPercent,
		ResetAt:       nullInt64Ptr(row.ResetAt),
		WindowMinutes: nullInt64Ptr(row.WindowMinutes),
	}
}

func limitWarmupStatus(row LimitWarmup) *LimitWarmupStatus {
	if row.AccountID == "" {
		return nil
	}
	return &LimitWarmupStatus{
		Window:       row.Window,
		ResetAt:      row.ResetAt,
		Status:       row.Status,
		Model:        row.Model,
		AttemptedAt:  sqliteTimeToISO(row.AttemptedAt),
		CompletedAt:  nullISOTimePtr(row.CompletedAt),
		ErrorCode:    nullStringPtr(row.ErrorCode),
		ErrorMessage: nullStringPtr(row.ErrorMessage),
	}
}

func nullISOTimePtr(value sql.NullString) *string {
	if !value.Valid || value.String == "" {
		return nil
	}
	iso := sqliteTimeToISO(value.String)
	return &iso
}

func (h Handler) authStatus(account Account) *AccountAuthStatus {
	accessToken := decryptOptional(h.encryptor, account.AccessTokenEncrypted)
	refreshToken := decryptOptional(h.encryptor, account.RefreshTokenEncrypted)
	idToken := decryptOptional(h.encryptor, account.IDTokenEncrypted)
	refreshState := "missing"
	if refreshToken != "" {
		refreshState = "stored"
	}
	idState := "unknown"
	if idToken != "" {
		claims := extractClaims(idToken)
		if claims.Email != "" || claims.ChatGPTAccountID != "" || claims.Exp > 0 {
			idState = "parsed"
		}
	}
	return &AccountAuthStatus{
		Access:  &AccountTokenStatus{ExpiresAt: tokenExpiryISO(accessToken)},
		Refresh: &AccountTokenStatus{State: &refreshState},
		IDToken: &AccountTokenStatus{State: &idState},
	}
}

func decryptOptional(encryptor *crypto.Encryptor, value []byte) string {
	if encryptor == nil || len(value) == 0 {
		return ""
	}
	token, err := encryptor.Decrypt(value)
	if err != nil {
		return ""
	}
	return token
}

func tokenExpiryISO(token string) *string {
	expiresMS := tokenExpiryEpochMS(token)
	if expiresMS <= 0 {
		return nil
	}
	iso := time.UnixMilli(expiresMS).UTC().Format("2006-01-02T15:04:05.000000Z")
	return &iso
}

type duplicateKey struct {
	Email            string
	ChatGPTAccountID string
	WorkspaceID      string
}

func duplicateDetectionKeys(accounts []Account) map[duplicateKey]bool {
	counts := map[duplicateKey]int{}
	for _, account := range accounts {
		key := duplicateDetectionKey(account)
		if key.Email == "" {
			continue
		}
		counts[key]++
	}
	result := map[duplicateKey]bool{}
	for key, count := range counts {
		if count > 1 {
			result[key] = true
		}
	}
	return result
}

func duplicateDetectionKey(account Account) duplicateKey {
	if strings.TrimSpace(account.Email) == "" || account.Email == "unknown@example.com" || !account.ChatGPTAccountID.Valid || account.ChatGPTAccountID.String == "" {
		return duplicateKey{}
	}
	return duplicateKey{Email: account.Email, ChatGPTAccountID: account.ChatGPTAccountID.String, WorkspaceID: account.WorkspaceID.String}
}

func (h Handler) updateStatus(w http.ResponseWriter, r *http.Request, nextStatus string, responseStatus string, auditAction string) {
	accountID := chi.URLParam(r, "accountID")
	record, err := h.repo.GetProxyRecord(r.Context(), accountID)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if record == nil {
		httputil.WriteError(w, http.StatusNotFound, "account_not_found", "Account not found")
		return
	}
	if nextStatus == "active" && record.Status == "reauth_required" {
		httputil.WriteError(w, http.StatusConflict, "account_state_transition_invalid", "Account requires re-authentication and cannot be reactivated directly")
		return
	}
	if nextStatus == "paused" && (record.Status == "reauth_required" || record.Status == "deactivated") {
		httputil.WriteError(w, http.StatusConflict, "account_state_transition_invalid", fmt.Sprintf("Account is %s and cannot be paused", record.Status))
		return
	}
	update := StatusCompareUpdate{
		AccountID:                  accountID,
		Status:                     nextStatus,
		ExpectedStatus:             record.Status,
		ExpectedDeactivationReason: record.DeactivationReason,
		ExpectedResetAt:            nullFloatToInt(record.ResetAt),
		ExpectedBlockedAt:          nullFloatToInt(record.BlockedAt),
	}
	ok, err := h.repo.UpdateStatusIfCurrent(r.Context(), update)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if !ok {
		httputil.WriteError(w, http.StatusConflict, "account_state_transition_invalid", "Account state changed; retry the operation")
		return
	}
	h.InvalidateSummaryCache()
	h.audit(r, auditAction, map[string]any{"account_id": accountID})
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": responseStatus})
}

func (h Handler) finishMutation(w http.ResponseWriter, r *http.Request, ok bool, err error, auditAction string, details map[string]any, response any) {
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if !ok {
		httputil.WriteError(w, http.StatusNotFound, "account_not_found", "Account not found")
		return
	}
	h.InvalidateSummaryCache()
	h.audit(r, auditAction, details)
	httputil.WriteJSON(w, http.StatusOK, response)
}

func (h Handler) decryptTokens(record ProxyRecord) (string, string, string, error) {
	if h.encryptor == nil {
		return "", "", "", fmt.Errorf("encryptor is not configured")
	}
	accessToken, err := h.encryptor.Decrypt(record.AccessTokenEncrypted)
	if err != nil {
		return "", "", "", fmt.Errorf("decrypt access token: %w", err)
	}
	refreshToken, err := h.encryptor.Decrypt(record.RefreshTokenEncrypted)
	if err != nil {
		return "", "", "", fmt.Errorf("decrypt refresh token: %w", err)
	}
	idToken, err := h.encryptor.Decrypt(record.IDTokenEncrypted)
	if err != nil {
		return "", "", "", fmt.Errorf("decrypt id token: %w", err)
	}
	return accessToken, refreshToken, idToken, nil
}

func (h Handler) latestUsagePercents(r *http.Request, accountID string) (*float64, *float64) {
	primaryRows, err := h.repo.LatestUsageByWindow(r.Context(), "primary")
	if err != nil {
		return nil, nil
	}
	secondaryRows, err := h.repo.LatestUsageByWindow(r.Context(), "secondary")
	if err != nil {
		return nil, nil
	}
	var primary *float64
	if row := primaryRows[accountID]; row.AccountID != "" {
		primary = &row.UsedPercent
	}
	var secondary *float64
	if row := secondaryRows[accountID]; row.AccountID != "" {
		secondary = &row.UsedPercent
	}
	return primary, secondary
}

func (h Handler) refreshProbeCredentials(ctx context.Context, accountID string) (*ProxyRecord, error) {
	account, err := h.repo.Get(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, fmt.Errorf("account %s not found for probe auth refresh", accountID)
	}
	if err := h.probeAuthRefresher.Refresh(ctx, *account); err != nil {
		return nil, err
	}
	record, err := h.repo.GetProxyRecord(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, fmt.Errorf("account %s not found after probe auth refresh", accountID)
	}
	return record, nil
}

func (h Handler) sendProbe(r *http.Request, record ProxyRecord, model string) int {
	accessToken, err := h.encryptor.Decrypt(record.AccessTokenEncrypted)
	if err != nil {
		return 0
	}
	body, _ := json.Marshal(map[string]any{
		"model":        model,
		"instructions": "Respond with a single dot.",
		"input": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": "."},
				},
			},
		},
		"max_output_tokens": 1,
		"stream":            true,
		"store":             false,
	})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, probeResponsesURL(h.probeBaseURL), bytes.NewReader(body))
	if err != nil {
		return 0
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if record.ChatGPTAccountID.Valid && shouldSendProbeAccountHeader(record.ChatGPTAccountID.String) {
		req.Header.Set("chatgpt-account-id", record.ChatGPTAccountID.String)
	}
	client := h.probeClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode
}

func probeResponsesURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = "https://chatgpt.com/backend-api"
	}
	if !strings.Contains(base, "/backend-api") {
		base += "/backend-api"
	}
	return base + "/codex/responses"
}

func shouldSendProbeAccountHeader(accountID string) bool {
	accountID = strings.TrimSpace(accountID)
	return accountID != "" && !strings.HasPrefix(accountID, "email_") && !strings.HasPrefix(accountID, "local_")
}

func (h Handler) audit(r *http.Request, action string, details map[string]any) {
	if action == "" {
		return
	}
	rawDetails, _ := json.Marshal(details)
	actorIP := sql.NullString{}
	if r.RemoteAddr != "" {
		actorIP = sql.NullString{String: r.RemoteAddr, Valid: true}
	}
	requestID := sql.NullString{}
	if value := r.Header.Get("X-Request-Id"); value != "" {
		requestID = sql.NullString{String: value, Valid: true}
	}
	_, _ = h.auditRepo.Insert(r.Context(), audit.Entry{
		Action:    action,
		ActorIP:   actorIP,
		Details:   sql.NullString{String: string(rawDetails), Valid: len(rawDetails) > 0},
		RequestID: requestID,
	})
}

func (h Handler) oauthAccountFromAuth(auth authFile) (OAuthAccount, error) {
	if h.encryptor == nil {
		return OAuthAccount{}, fmt.Errorf("encryptor is not configured")
	}
	claims := extractClaims(auth.Tokens.IDToken)
	rawAccountID := firstNonEmpty(auth.Tokens.AccountID, claims.ChatGPTAccountID)
	email := claims.Email
	if email == "" {
		email = "unknown@example.com"
	}
	workspaceID := cleanIdentityPart(claims.WorkspaceID)
	workspaceLabel := cleanIdentityPart(claims.WorkspaceLabel)
	seatType := normalizeSeatType(cleanIdentityPart(claims.SeatType))
	accountID := generateAccountID(rawAccountID, email, workspaceID)
	planType := coercePlanType(claims.ChatGPTPlanType)

	accessEncrypted, err := h.encryptor.Encrypt(auth.Tokens.AccessToken)
	if err != nil {
		return OAuthAccount{}, err
	}
	refreshEncrypted, err := h.encryptor.Encrypt(auth.Tokens.RefreshToken)
	if err != nil {
		return OAuthAccount{}, err
	}
	idEncrypted, err := h.encryptor.Encrypt(auth.Tokens.IDToken)
	if err != nil {
		return OAuthAccount{}, err
	}
	lastRefresh := time.Now().UTC().Format("2006-01-02 15:04:05")
	if auth.LastRefresh != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, auth.LastRefresh); err == nil {
			lastRefresh = parsed.UTC().Format("2006-01-02 15:04:05")
		}
	}
	return OAuthAccount{
		ID:                    accountID,
		ChatGPTAccountID:      nullableString(rawAccountID),
		Email:                 email,
		WorkspaceID:           nullableString(workspaceID),
		WorkspaceLabel:        nullableString(workspaceLabel),
		SeatType:              nullableString(seatType),
		PlanType:              planType,
		AccessTokenEncrypted:  accessEncrypted,
		RefreshTokenEncrypted: refreshEncrypted,
		IDTokenEncrypted:      idEncrypted,
		LastRefresh:           lastRefresh,
		Status:                "active",
	}, nil
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

func nullStringValue(value sql.NullString) any {
	if !value.Valid || value.String == "" {
		return nil
	}
	return value.String
}

func nullFloatToInt(value sql.NullFloat64) sql.NullInt64 {
	if !value.Valid {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(value.Float64), Valid: true}
}

func authExportFilename(record ProxyRecord) string {
	base := strings.TrimSpace(record.Email)
	if base == "" {
		base = record.ID
	}
	base = strings.NewReplacer("@", "_", ".", "_", "/", "_", "\\", "_").Replace(base)
	return base + "_auth.json"
}

func sqliteTimeToISO(value string) string {
	if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", value, time.UTC); err == nil {
		return parsed.UTC().Format("2006-01-02T15:04:05.000000Z")
	}
	return value
}

type authFile struct {
	Tokens      authTokens `json:"tokens"`
	LastRefresh string     `json:"last_refresh"`
}

type authTokens struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
}

func parseAuthFile(raw []byte) (authFile, error) {
	var auth authFile
	if err := json.Unmarshal(raw, &auth); err != nil {
		return authFile{}, err
	}
	if auth.Tokens.IDToken == "" || auth.Tokens.AccessToken == "" || auth.Tokens.RefreshToken == "" {
		return authFile{}, fmt.Errorf("missing tokens")
	}
	return auth, nil
}

func readAuthJSONPayload(r *http.Request) ([]byte, error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(16 << 20); err != nil {
			return nil, err
		}
		file, _, err := r.FormFile("auth_json")
		if err != nil {
			file, _, err = r.FormFile("authJson")
		}
		if err != nil {
			return nil, err
		}
		defer file.Close()
		return io.ReadAll(io.LimitReader(file, 16<<20))
	}
	return io.ReadAll(io.LimitReader(r.Body, 16<<20))
}

type tokenClaims struct {
	Email            string
	ChatGPTAccountID string
	ChatGPTPlanType  string
	WorkspaceID      string
	WorkspaceLabel   string
	SeatType         string
	Exp              int64
}

func extractClaims(idToken string) tokenClaims {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return tokenClaims{}
	}
	payload := parts[1]
	if missing := len(payload) % 4; missing != 0 {
		payload += strings.Repeat("=", 4-missing)
	}
	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return tokenClaims{}
	}
	var raw struct {
		Email              string         `json:"email"`
		ChatGPTAccountID   string         `json:"chatgpt_account_id"`
		ChatGPTPlanType    string         `json:"chatgpt_plan_type"`
		WorkspaceID        string         `json:"workspace_id"`
		ChatGPTWorkspaceID string         `json:"chatgpt_workspace_id"`
		OrganizationID     string         `json:"organization_id"`
		WorkspaceLabel     string         `json:"workspace_label"`
		WorkspaceName      string         `json:"workspace_name"`
		OrganizationName   string         `json:"organization_name"`
		SeatType           string         `json:"seat_type"`
		ChatGPTSeatType    string         `json:"chatgpt_seat_type"`
		EntitlementType    string         `json:"entitlement_type"`
		Exp                int64          `json:"exp"`
		Auth               map[string]any `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(decoded, &raw); err != nil {
		return tokenClaims{}
	}
	claims := tokenClaims{
		Email:            raw.Email,
		ChatGPTAccountID: raw.ChatGPTAccountID,
		ChatGPTPlanType:  raw.ChatGPTPlanType,
		WorkspaceID:      firstNonEmpty(raw.WorkspaceID, raw.ChatGPTWorkspaceID, raw.OrganizationID),
		WorkspaceLabel:   firstNonEmpty(raw.WorkspaceLabel, raw.WorkspaceName, raw.OrganizationName),
		SeatType:         firstNonEmpty(raw.SeatType, raw.ChatGPTSeatType, raw.EntitlementType),
		Exp:              raw.Exp,
	}
	if raw.Auth != nil {
		claims.ChatGPTAccountID = firstNonEmpty(stringFromMap(raw.Auth, "chatgpt_account_id"), claims.ChatGPTAccountID)
		claims.ChatGPTPlanType = firstNonEmpty(stringFromMap(raw.Auth, "chatgpt_plan_type"), claims.ChatGPTPlanType)
		claims.WorkspaceID = firstNonEmpty(stringFromMap(raw.Auth, "workspace_id"), stringFromMap(raw.Auth, "chatgpt_workspace_id"), claims.WorkspaceID)
		claims.WorkspaceLabel = firstNonEmpty(stringFromMap(raw.Auth, "workspace_label"), stringFromMap(raw.Auth, "workspace_name"), claims.WorkspaceLabel)
		claims.SeatType = firstNonEmpty(stringFromMap(raw.Auth, "seat_type"), stringFromMap(raw.Auth, "chatgpt_seat_type"), claims.SeatType)
	}
	return claims
}

func tokenExpiryEpochMS(accessToken string) int64 {
	claims := extractClaims(accessToken)
	if claims.Exp <= 0 {
		return 0
	}
	return claims.Exp * 1000
}

func stringFromMap(values map[string]any, key string) string {
	if value, ok := values[key].(string); ok {
		return value
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func cleanIdentityPart(value string) string {
	return strings.TrimSpace(value)
}

func normalizeSeatType(value string) string {
	if value == "" {
		return ""
	}
	return strings.ReplaceAll(strings.ToLower(value), "-", "_")
}

func coercePlanType(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "unknown"
	}
	return value
}

func nullableString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func generateAccountID(accountID string, email string, workspaceID string) string {
	workspaceID = cleanIdentityPart(workspaceID)
	if accountID != "" && workspaceID != "" {
		return accountID + "_" + shaHex(workspaceID)[:8]
	}
	if accountID != "" && email != "" && email != "unknown@example.com" {
		return accountID + "_" + shaHex(email)[:8]
	}
	if accountID != "" {
		return accountID
	}
	return "email_" + shaHex(email)[:12]
}

func shaHex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
