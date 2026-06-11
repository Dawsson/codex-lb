package apikeys

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/soju06/codex-lb/internal/audit"
	"github.com/soju06/codex-lb/internal/cacheinvalidation"
	"github.com/soju06/codex-lb/internal/httputil"
	"github.com/soju06/codex-lb/internal/platform"
)

type Handler struct {
	repo      Repository
	bumper    cacheBumper
	auditRepo *audit.Repository
}

type cacheBumper interface {
	Bump(ctx context.Context, namespace string) error
}

type limitRuleResponse struct {
	ID           int     `json:"id"`
	LimitType    string  `json:"limitType"`
	LimitWindow  string  `json:"limitWindow"`
	MaxValue     float64 `json:"maxValue"`
	CurrentValue float64 `json:"currentValue"`
	ModelFilter  *string `json:"modelFilter"`
	ResetAt      string  `json:"resetAt"`
}

type usageSummaryResponse struct {
	RequestCount      int     `json:"requestCount"`
	TotalTokens       int     `json:"totalTokens"`
	CachedInputTokens int     `json:"cachedInputTokens"`
	TotalCostUsd      float64 `json:"totalCostUsd"`
}

type keyResponse struct {
	ID                              string                `json:"id"`
	Name                            string                `json:"name"`
	KeyPrefix                       string                `json:"keyPrefix"`
	AllowedModels                   []string              `json:"allowedModels"`
	ApplyToCodexModel               bool                  `json:"applyToCodexModel"`
	EnforcedModel                   *string               `json:"enforcedModel"`
	EnforcedReasoningEffort         *string               `json:"enforcedReasoningEffort"`
	EnforcedServiceTier             *string               `json:"enforcedServiceTier"`
	TrafficClass                    string                `json:"trafficClass"`
	ExpiresAt                       *string               `json:"expiresAt"`
	IsActive                        bool                  `json:"isActive"`
	AccountAssignmentScopeEnabled   bool                  `json:"accountAssignmentScopeEnabled"`
	AssignedAccountIDs              []string              `json:"assignedAccountIds"`
	CreatedAt                       string                `json:"createdAt"`
	LastUsedAt                      *string               `json:"lastUsedAt"`
	Limits                          []limitRuleResponse   `json:"limits"`
	UsageSummary                    *usageSummaryResponse `json:"usageSummary"`
	PooledRemainingPercentPrimary   *float64              `json:"pooledRemainingPercentPrimary"`
	PooledRemainingPercentSecondary *float64              `json:"pooledRemainingPercentSecondary"`
	PooledCapacityCreditsPrimary    float64               `json:"pooledCapacityCreditsPrimary"`
}

type createResponse struct {
	keyResponse
	Key string `json:"key"`
}

type limitCreatePayload struct {
	LimitType   string  `json:"limitType"`
	LimitWindow string  `json:"limitWindow"`
	MaxValue    int64   `json:"maxValue"`
	ModelFilter *string `json:"modelFilter"`
}

type createRequest struct {
	Name                    string               `json:"name"`
	AllowedModels           []string             `json:"allowedModels"`
	ApplyToCodexModel       *bool                `json:"applyToCodexModel"`
	EnforcedModel           *string              `json:"enforcedModel"`
	EnforcedReasoningEffort *string              `json:"enforcedReasoningEffort"`
	EnforcedServiceTier     *string              `json:"enforcedServiceTier"`
	TrafficClass            *string              `json:"trafficClass"`
	ExpiresAt               *string              `json:"expiresAt"`
	AssignedAccountIDs      []string             `json:"assignedAccountIds"`
	Limits                  []limitCreatePayload `json:"limits"`
	WeeklyTokenLimit        *int64               `json:"weeklyTokenLimit"`
}

type updateRequest struct {
	Name                    *string              `json:"name"`
	AllowedModels           []string             `json:"allowedModels"`
	ApplyToCodexModel       *bool                `json:"applyToCodexModel"`
	EnforcedModel           *string              `json:"enforcedModel"`
	EnforcedReasoningEffort *string              `json:"enforcedReasoningEffort"`
	EnforcedServiceTier     *string              `json:"enforcedServiceTier"`
	TrafficClass            *string              `json:"trafficClass"`
	ExpiresAt               *string              `json:"expiresAt"`
	IsActive                *bool                `json:"isActive"`
	AssignedAccountIDs      []string             `json:"assignedAccountIds"`
	Limits                  []limitCreatePayload `json:"limits"`
	WeeklyTokenLimit        *int64               `json:"weeklyTokenLimit"`
	ResetUsage              *bool                `json:"resetUsage"`
}

func NewHandler(repo Repository, bumpers ...cacheBumper) Handler {
	var bumper cacheBumper
	if len(bumpers) > 0 {
		bumper = bumpers[0]
	}
	return Handler{repo: repo, bumper: bumper}
}

func (h Handler) WithAudit(repo audit.Repository) Handler {
	h.auditRepo = &repo
	return h
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	keys, err := h.repo.List(r.Context())
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	response := make([]keyResponse, 0, len(keys))
	for _, key := range keys {
		response = append(response, toKeyResponse(key))
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.EmptySlice(response))
}

func (h Handler) Create(w http.ResponseWriter, r *http.Request) {
	var payload createRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	if payload.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_api_key_payload", "Name is required")
		return
	}
	input := CreateInput{
		Name:                    payload.Name,
		AllowedModels:           payload.AllowedModels,
		EnforcedModel:           payload.EnforcedModel,
		EnforcedReasoningEffort: payload.EnforcedReasoningEffort,
		EnforcedServiceTier:     payload.EnforcedServiceTier,
		ExpiresAt:               payload.ExpiresAt,
		AssignedAccountIDs:      payload.AssignedAccountIDs,
		Limits:                  toLimitInputs(payload.Limits),
	}
	if payload.ApplyToCodexModel != nil {
		input.ApplyToCodexModel = *payload.ApplyToCodexModel
	}
	if payload.TrafficClass != nil {
		input.TrafficClass = *payload.TrafficClass
	}
	if payload.WeeklyTokenLimit != nil {
		input.Limits = append(input.Limits, LimitInput{
			LimitType:   "total_tokens",
			LimitWindow: "weekly",
			MaxValue:    *payload.WeeklyTokenLimit,
		})
	}
	created, plainKey, err := h.repo.Create(r.Context(), input)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	h.bumpInvalidation(r.Context())
	h.audit(r, "api_key_created", map[string]any{"key_id": created.ID})
	resp := toKeyResponse(created)
	httputil.WriteJSON(w, http.StatusOK, createResponse{keyResponse: resp, Key: plainKey})
}

func (h Handler) Update(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "keyID")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	var payload updateRequest
	if err := json.Unmarshal(body, &payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(body, &raw)
	input := UpdateInput{}
	if payload.Name != nil {
		input.NameSet = true
		input.Name = *payload.Name
	}
	if _, ok := raw["allowedModels"]; ok {
		input.AllowedModelsSet = true
		input.AllowedModels = payload.AllowedModels
	}
	if payload.ApplyToCodexModel != nil {
		input.ApplyToCodexModelSet = true
		input.ApplyToCodexModel = *payload.ApplyToCodexModel
	}
	if _, ok := raw["enforcedModel"]; ok {
		input.EnforcedModelSet = true
		input.EnforcedModel = payload.EnforcedModel
	}
	if _, ok := raw["enforcedReasoningEffort"]; ok {
		input.EnforcedReasoningEffortSet = true
		input.EnforcedReasoningEffort = payload.EnforcedReasoningEffort
	}
	if _, ok := raw["enforcedServiceTier"]; ok {
		input.EnforcedServiceTierSet = true
		input.EnforcedServiceTier = payload.EnforcedServiceTier
	}
	if payload.TrafficClass != nil {
		input.TrafficClassSet = true
		input.TrafficClass = *payload.TrafficClass
	}
	if _, ok := raw["expiresAt"]; ok {
		input.ExpiresAtSet = true
		input.ExpiresAt = payload.ExpiresAt
	}
	if payload.IsActive != nil {
		input.IsActiveSet = true
		input.IsActive = *payload.IsActive
	}
	if _, ok := raw["assignedAccountIds"]; ok {
		input.AssignedAccountIDsSet = true
		input.AssignedAccountIDs = payload.AssignedAccountIDs
	}
	if _, ok := raw["limits"]; ok {
		input.LimitsSet = true
		input.Limits = toLimitInputs(payload.Limits)
	} else if _, ok := raw["weeklyTokenLimit"]; ok {
		input.LimitsSet = true
	}
	if input.LimitsSet && payload.WeeklyTokenLimit != nil {
		input.Limits = append(input.Limits, LimitInput{
			LimitType:   "total_tokens",
			LimitWindow: "weekly",
			MaxValue:    *payload.WeeklyTokenLimit,
		})
	}
	if payload.ResetUsage != nil {
		input.ResetUsage = *payload.ResetUsage
	}
	updated, err := h.repo.Update(r.Context(), keyID, input)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if updated == nil {
		httputil.WriteError(w, http.StatusNotFound, "not_found", "API key not found")
		return
	}
	h.bumpInvalidation(r.Context())
	if payload.IsActive != nil && !*payload.IsActive && !updated.IsActive {
		h.audit(r, "api_key_revoked", map[string]any{"key_id": updated.ID})
	} else {
		h.audit(r, "api_key_updated", map[string]any{"key_id": updated.ID})
	}
	httputil.WriteJSON(w, http.StatusOK, toKeyResponse(*updated))
}

func (h Handler) Delete(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "keyID")
	deleted, err := h.repo.Delete(r.Context(), keyID)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if !deleted {
		httputil.WriteError(w, http.StatusNotFound, "not_found", "API key not found")
		return
	}
	h.bumpInvalidation(r.Context())
	h.audit(r, "api_key_revoked", map[string]any{"key_id": keyID})
	w.WriteHeader(http.StatusNoContent)
}

type trendPointResponse struct {
	T string  `json:"t"`
	V float64 `json:"v"`
}

type trendsResponse struct {
	KeyID  string               `json:"keyId"`
	Cost   []trendPointResponse `json:"cost"`
	Tokens []trendPointResponse `json:"tokens"`
}

type accountCostResponse struct {
	AccountID *string `json:"accountId"`
	Email     *string `json:"email"`
	CostUSD   float64 `json:"costUsd"`
	IsDeleted bool    `json:"isDeleted"`
}

type usage7DayResponse struct {
	KeyID             string                `json:"keyId"`
	TotalTokens       int                   `json:"totalTokens"`
	TotalCostUSD      float64               `json:"totalCostUsd"`
	TotalRequests     int                   `json:"totalRequests"`
	CachedInputTokens int                   `json:"cachedInputTokens"`
	AccountCosts      []accountCostResponse `json:"accountCosts"`
}

func (h Handler) Trends(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "keyID")
	existing, err := h.repo.GetByID(r.Context(), keyID)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if existing == nil {
		httputil.WriteError(w, http.StatusNotFound, "not_found", "API key not found")
		return
	}
	now := time.Now().UTC()
	since := now.Add(-sparklineDays * 24 * time.Hour)
	buckets, err := h.repo.TrendsByKey(r.Context(), keyID, since, now, detailBucketSeconds)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	cost, tokens := buildTrendPoints(buckets, since, now, detailBucketSeconds)
	httputil.WriteJSON(w, http.StatusOK, trendsResponse{
		KeyID:  keyID,
		Cost:   httputil.EmptySlice(cost),
		Tokens: httputil.EmptySlice(tokens),
	})
}

func (h Handler) Usage7Day(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "keyID")
	existing, err := h.repo.GetByID(r.Context(), keyID)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if existing == nil {
		httputil.WriteError(w, http.StatusNotFound, "not_found", "API key not found")
		return
	}
	now := time.Now().UTC()
	since := now.Add(-7 * 24 * time.Hour)
	usage, err := h.repo.Usage7Day(r.Context(), keyID, since, now)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	accounts := make([]accountCostResponse, 0, len(usage.AccountCosts))
	for _, account := range usage.AccountCosts {
		accounts = append(accounts, accountCostResponse{
			AccountID: nullStringPtr(account.AccountID),
			Email:     nullStringPtr(account.Email),
			CostUSD:   account.CostUSD,
			IsDeleted: account.IsDeleted,
		})
	}
	httputil.WriteJSON(w, http.StatusOK, usage7DayResponse{
		KeyID:             keyID,
		TotalTokens:       usage.TotalTokens,
		TotalCostUSD:      usage.TotalCostUSD,
		TotalRequests:     usage.TotalRequests,
		CachedInputTokens: usage.CachedInputTokens,
		AccountCosts:      httputil.EmptySlice(accounts),
	})
}

func (h Handler) Regenerate(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "keyID")
	updated, plainKey, err := h.repo.Regenerate(r.Context(), keyID)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if updated == nil {
		httputil.WriteError(w, http.StatusNotFound, "not_found", "API key not found")
		return
	}
	h.bumpInvalidation(r.Context())
	h.audit(r, "api_key_regenerated", map[string]any{"key_id": updated.ID})
	resp := toKeyResponse(*updated)
	httputil.WriteJSON(w, http.StatusOK, createResponse{keyResponse: resp, Key: plainKey})
}

func (h Handler) audit(r *http.Request, action string, details map[string]any) {
	audit.LogRequest(h.auditRepo, r, action, details)
}

func (h Handler) bumpInvalidation(ctx context.Context) {
	if h.bumper != nil {
		_ = h.bumper.Bump(ctx, cacheinvalidation.NamespaceAPIKey)
	}
}

func toKeyResponse(key KeyRecord) keyResponse {
	var usage *usageSummaryResponse
	if key.UsageSummary != nil {
		usage = &usageSummaryResponse{
			RequestCount:      key.UsageSummary.RequestCount,
			TotalTokens:       key.UsageSummary.TotalTokens,
			CachedInputTokens: key.UsageSummary.CachedInputTokens,
			TotalCostUsd:      key.UsageSummary.TotalCostUSD,
		}
	}
	limits := make([]limitRuleResponse, 0, len(key.Limits))
	for _, limit := range key.Limits {
		limits = append(limits, limitRuleResponse{
			ID:           limit.ID,
			LimitType:    limit.LimitType,
			LimitWindow:  limit.LimitWindow,
			MaxValue:     float64(limit.MaxValue),
			CurrentValue: float64(limit.CurrentValue),
			ModelFilter:  nullStringPtr(limit.ModelFilter),
			ResetAt:      formatTime(limit.ResetAt),
		})
	}
	return keyResponse{
		ID:                              key.ID,
		Name:                            key.Name,
		KeyPrefix:                       key.KeyPrefix,
		AllowedModels:                   deserializeAllowedModels(key.AllowedModels),
		ApplyToCodexModel:               key.ApplyToCodexModel,
		EnforcedModel:                   nullStringPtr(key.EnforcedModel),
		EnforcedReasoningEffort:         nullStringPtr(key.EnforcedReasoningEffort),
		EnforcedServiceTier:             nullStringPtr(key.EnforcedServiceTier),
		TrafficClass:                    key.TrafficClass,
		ExpiresAt:                       nullTimePtr(key.ExpiresAt),
		IsActive:                        key.IsActive,
		AccountAssignmentScopeEnabled:   key.AccountAssignmentScopeEnabled,
		AssignedAccountIDs:              httputil.EmptySlice(key.AssignedAccountIDs),
		CreatedAt:                       formatTime(key.CreatedAt),
		LastUsedAt:                      nullTimePtr(key.LastUsedAt),
		Limits:                          httputil.EmptySlice(limits),
		UsageSummary:                    usage,
		PooledRemainingPercentPrimary:   nil,
		PooledRemainingPercentSecondary: nil,
		PooledCapacityCreditsPrimary:    0,
	}
}

func toLimitInputs(payload []limitCreatePayload) []LimitInput {
	inputs := make([]LimitInput, 0, len(payload))
	for _, limit := range payload {
		inputs = append(inputs, LimitInput{
			LimitType:   limit.LimitType,
			LimitWindow: limit.LimitWindow,
			MaxValue:    limit.MaxValue,
			ModelFilter: limit.ModelFilter,
		})
	}
	return inputs
}

func formatTime(value sql.NullString) string {
	if iso := platform.SQLiteTimeToISO(value); iso != nil {
		return *iso
	}
	return ""
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullTimePtr(value sql.NullString) *string {
	return platform.SQLiteTimeToISO(value)
}
