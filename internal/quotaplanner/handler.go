package quotaplanner

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/audit"
	"github.com/soju06/codex-lb/internal/httputil"
	"github.com/soju06/codex-lb/internal/limitwarmup"
	"github.com/soju06/codex-lb/internal/platform"
	"github.com/soju06/codex-lb/internal/requestlogs"
)

type Handler struct {
	repo         Repository
	accountRepo  *accounts.Repository
	requestLogs  *requestlogs.Repository
	warmupSender limitwarmup.Sender
	auditRepo    *audit.Repository
}

type settingsResponse struct {
	Mode                   string  `json:"mode"`
	Timezone               string  `json:"timezone"`
	WorkingDays            []int   `json:"workingDays"`
	WorkingHoursStart      string  `json:"workingHoursStart"`
	WorkingHoursEnd        string  `json:"workingHoursEnd"`
	PrewarmEnabled         bool    `json:"prewarmEnabled"`
	PrewarmLeadMinutes     int     `json:"prewarmLeadMinutes"`
	MaxWarmupsPerDay       int     `json:"maxWarmupsPerDay"`
	MaxWarmupCreditsPerDay float64 `json:"maxWarmupCreditsPerDay"`
	MinExpectedGain        float64 `json:"minExpectedGain"`
	ForecastQuantile       string  `json:"forecastQuantile"`
	AllowSyntheticTraffic  bool    `json:"allowSyntheticTraffic"`
	WarmupModelPreference  *string `json:"warmupModelPreference"`
	DryRun                 bool    `json:"dryRun"`
}

type decisionResponse struct {
	ID             string         `json:"id"`
	CreatedAt      string         `json:"createdAt"`
	Mode           string         `json:"mode"`
	AccountID      *string        `json:"accountId"`
	Action         string         `json:"action"`
	ScheduledAt    *string        `json:"scheduledAt"`
	ExecutedAt     *string        `json:"executedAt"`
	Score          float64        `json:"score"`
	Reason         *string        `json:"reason"`
	Details        map[string]any `json:"details,omitempty"`
	Status         string         `json:"status"`
	IdempotencyKey string         `json:"idempotencyKey"`
}

type forecastSlotResponse struct {
	SlotStart    string  `json:"slotStart"`
	DemandUnits  float64 `json:"demandUnits"`
	RequestCount int     `json:"requestCount"`
	Source       string  `json:"source"`
}

type simulationResponse struct {
	Loss                   float64 `json:"loss"`
	UnmetDemand            float64 `json:"unmetDemand"`
	WastedCapacity         float64 `json:"wastedCapacity"`
	ColdStartPenalty       float64 `json:"coldStartPenalty"`
	SynchronizationPenalty float64 `json:"synchronizationPenalty"`
	ForecastUnits          float64 `json:"forecastUnits"`
	ServedUnits            float64 `json:"servedUnits"`
}

type forecastResponse struct {
	GeneratedAt      string                 `json:"generatedAt"`
	HorizonHours     int                    `json:"horizonHours"`
	SlotSeconds      int                    `json:"slotSeconds"`
	TotalDemandUnits float64                `json:"totalDemandUnits"`
	PeakSlotStart    *string                `json:"peakSlotStart"`
	PeakDemandUnits  float64                `json:"peakDemandUnits"`
	Simulation       simulationResponse     `json:"simulation"`
	Slots            []forecastSlotResponse `json:"slots"`
}

type warmNowRequest struct {
	AccountID  string  `json:"accountId"`
	Model      *string `json:"model"`
	APIKeyID   *string `json:"apiKeyId"`
	ForceProbe *bool   `json:"forceProbe"`
}

type warmupActionResponse struct {
	DecisionID string  `json:"decisionId"`
	Status     string  `json:"status"`
	Reason     string  `json:"reason"`
	RequestID  *string `json:"requestId"`
	ExecutedAt *string `json:"executedAt"`
}

func NewHandler(repo Repository) Handler {
	return Handler{repo: repo}
}

func (h Handler) WithAudit(repo audit.Repository) Handler {
	h.auditRepo = &repo
	return h
}

func (h Handler) WithWarmup(accountRepo accounts.Repository, requestLogs requestlogs.Repository, sender limitwarmup.Sender) Handler {
	h.accountRepo = &accountRepo
	h.requestLogs = &requestLogs
	h.warmupSender = sender
	return h
}

func (h Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := h.repo.GetSettings(r.Context())
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, toSettingsResponse(settings))
}

func (h Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	current, err := h.repo.GetSettings(r.Context())
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	var payload map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	updated := mergeSettings(current, payload)
	if err := validateWorkingDays(updated.WorkingDays); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_quota_planner", err.Error())
		return
	}
	saved, err := h.repo.UpsertSettings(r.Context(), updated)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	h.audit(r, "quota_planner_settings_changed", map[string]any{"mode": saved.Mode})
	httputil.WriteJSON(w, http.StatusOK, toSettingsResponse(saved))
}

func (h Handler) ListDecisions(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := h.repo.RecentDecisions(r.Context(), limit)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	decisions := make([]decisionResponse, 0, len(rows))
	for _, row := range rows {
		decisions = append(decisions, toDecisionResponse(row))
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.EmptySlice(decisions))
}

func (h Handler) Forecast(w http.ResponseWriter, r *http.Request) {
	horizonHours := 36
	rawHorizon := r.URL.Query().Get("horizonHours")
	if rawHorizon == "" {
		rawHorizon = r.URL.Query().Get("horizon_hours")
	}
	if rawHorizon != "" {
		if parsed, err := strconv.Atoi(rawHorizon); err == nil && parsed > 0 {
			horizonHours = parsed
		}
	}
	if horizonHours > 168 {
		horizonHours = 168
	}
	forecast, err := h.repo.BuildForecast(r.Context(), horizonHours)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	slots := make([]forecastSlotResponse, 0, len(forecast.Slots))
	for _, slot := range forecast.Slots {
		slots = append(slots, forecastSlotResponse(slot))
	}
	httputil.WriteJSON(w, http.StatusOK, forecastResponse{
		GeneratedAt:      forecast.GeneratedAt,
		HorizonHours:     forecast.HorizonHours,
		SlotSeconds:      forecast.SlotSeconds,
		TotalDemandUnits: forecast.TotalDemandUnits,
		PeakSlotStart:    forecast.PeakSlotStart,
		PeakDemandUnits:  forecast.PeakDemandUnits,
		Simulation: simulationResponse{
			Loss:                   forecast.SimulationLoss,
			UnmetDemand:            forecast.SimulationUnmet,
			WastedCapacity:         forecast.SimulationWasted,
			ColdStartPenalty:       forecast.SimulationCold,
			SynchronizationPenalty: forecast.SimulationSync,
			ForecastUnits:          forecast.SimulationForecast,
			ServedUnits:            forecast.SimulationServed,
		},
		Slots: httputil.EmptySlice(slots),
	})
}

func (h Handler) WarmNow(w http.ResponseWriter, r *http.Request) {
	var payload warmNowRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	if strings.TrimSpace(payload.AccountID) == "" {
		httputil.WriteError(w, http.StatusBadRequest, "account_required", "accountId is required")
		return
	}
	if payload.APIKeyID != nil && strings.TrimSpace(*payload.APIKeyID) != "" {
		httputil.WriteError(w, http.StatusNotImplemented, "api_key_reservations_not_implemented", "Quota planner warm-now API-key reservations are not available in the Go API yet")
		return
	}
	if h.accountRepo == nil || h.requestLogs == nil || h.warmupSender == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "not_implemented", "Quota planner warm-now is not configured")
		return
	}
	settings, err := h.repo.GetSettings(r.Context())
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	account, err := h.accountRepo.Get(r.Context(), payload.AccountID)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	now := time.Now().UTC()
	decision, err := h.repo.LogDecision(r.Context(), LogDecisionParams{
		Mode:           settings.Mode,
		Action:         "warmup",
		AccountID:      sql.NullString{String: payload.AccountID, Valid: account != nil},
		ScheduledAt:    now.Format("2006-01-02 15:04:05"),
		Reason:         sql.NullString{String: "manual_warm_now_requested", Valid: true},
		Status:         "planned",
		IdempotencyKey: "manual:" + now.Format("20060102150405") + ":" + payload.AccountID + ":" + strconv.FormatInt(now.UnixNano(), 36),
	})
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if account == nil {
		updated, _, err := h.repo.UpdateDecisionStatus(r.Context(), decision.ID, "skipped", "account_not_found", sql.NullString{}, "planned")
		if err != nil {
			httputil.WriteServerError(w, err)
			return
		}
		h.writeWarmNowResult(w, r, updated, decision.ID, "skipped", "account_not_found", nil)
		return
	}
	if strings.ToLower(account.Status) != "active" && !boolValue(payload.ForceProbe) {
		updated, _, err := h.repo.UpdateDecisionStatus(r.Context(), decision.ID, "skipped", "account_not_active", sql.NullString{}, "planned")
		if err != nil {
			httputil.WriteServerError(w, err)
			return
		}
		h.writeWarmNowResult(w, r, updated, decision.ID, "skipped", "account_not_active", nil)
		return
	}
	if _, ok, err := h.repo.UpdateDecisionStatus(r.Context(), decision.ID, "executing", "warmup_executing", sql.NullString{}, "planned"); err != nil {
		httputil.WriteServerError(w, err)
		return
	} else if !ok {
		current, _ := h.repo.GetDecision(r.Context(), decision.ID)
		h.writeWarmNowResult(w, r, current, decision.ID, "skipped", "decision_not_claimed", nil)
		return
	}

	model := resolvedWarmNowModel(payload.Model, settings)
	requestID := "quota-warmup-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	result, sendErr := h.warmupSender.Send(r.Context(), *account, limitwarmup.SendParams{
		Model:        model,
		Prompt:       "warmup",
		Instructions: "Reply with OK only.",
		RequestID:    requestID,
	})
	status := "executed"
	reason := "warmup_executed"
	logStatus := "success"
	if sendErr != nil || !result.Success {
		status = "failed"
		reason = "warmup_failed"
		logStatus = "error"
		if sendErr == nil && result.ErrorCode != "" {
			reason = "warmup_failed:" + result.ErrorCode
		}
	}
	errorCode := result.ErrorCode
	errorMessage := result.ErrorMessage
	if sendErr != nil {
		errorCode = "warmup_failed"
		errorMessage = sendErr.Error()
	}
	_ = h.requestLogs.Insert(r.Context(), requestlogs.InsertParams{
		RequestID:    requestID,
		RequestKind:  limitwarmup.RequestKind,
		Model:        model,
		AccountID:    &account.ID,
		PlanType:     &account.PlanType,
		Status:       logStatus,
		ErrorCode:    stringPtrIfNotEmpty(errorCode),
		ErrorMessage: stringPtrIfNotEmpty(errorMessage),
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
		LatencyMS:    &result.LatencyMS,
		Transport:    stringPtr("quota_planner"),
		Source:       stringPtr("quota_planner"),
	})
	updated, _, err := h.repo.UpdateDecisionStatus(r.Context(), decision.ID, status, reason, sql.NullString{String: time.Now().UTC().Format("2006-01-02 15:04:05"), Valid: true}, "executing")
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	h.writeWarmNowResult(w, r, updated, decision.ID, status, reason, &requestID)
}

func (h Handler) CancelDecision(w http.ResponseWriter, r *http.Request) {
	decisionID := chi.URLParam(r, "decisionID")
	decision, canceled, err := h.repo.CancelDecision(r.Context(), decisionID)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	if decision == nil {
		httputil.WriteError(w, http.StatusBadRequest, "quota_planner_decision_not_found", "Decision not found")
		return
	}
	reason := "not_cancelable"
	if canceled {
		reason = "admin_canceled"
	}
	h.audit(r, "quota_planner_decision_cancel", map[string]any{"decision_id": decisionID, "status": decision.Status, "canceled": canceled})
	httputil.WriteJSON(w, http.StatusOK, warmupActionResponse{
		DecisionID: decision.ID,
		Status:     decision.Status,
		Reason:     reason,
		RequestID:  nil,
		ExecutedAt: nullTimePtr(decision.ExecutedAt),
	})
}

func (h Handler) audit(r *http.Request, action string, details map[string]any) {
	audit.LogRequest(h.auditRepo, r, action, details)
}

func (h Handler) writeWarmNowResult(w http.ResponseWriter, r *http.Request, decision *Decision, fallbackID, fallbackStatus, fallbackReason string, requestID *string) {
	decisionID := fallbackID
	status := fallbackStatus
	reason := fallbackReason
	var executedAt *string
	if decision != nil {
		decisionID = decision.ID
		status = decision.Status
		if decision.Reason.Valid && decision.Reason.String != "" {
			reason = decision.Reason.String
		}
		executedAt = nullTimePtr(decision.ExecutedAt)
	}
	h.audit(r, "quota_planner_warm_now", map[string]any{"decision_id": decisionID, "status": status})
	httputil.WriteJSON(w, http.StatusOK, warmupActionResponse{
		DecisionID: decisionID,
		Status:     status,
		Reason:     reason,
		RequestID:  requestID,
		ExecutedAt: executedAt,
	})
}

func resolvedWarmNowModel(model *string, settings Settings) string {
	if model != nil && strings.TrimSpace(*model) != "" {
		return strings.TrimSpace(*model)
	}
	if settings.WarmupModelPreference.Valid && strings.TrimSpace(settings.WarmupModelPreference.String) != "" {
		return strings.TrimSpace(settings.WarmupModelPreference.String)
	}
	return "gpt-5.4-mini"
}

func boolValue(value *bool) bool {
	return value != nil && *value
}

func stringPtr(value string) *string {
	return &value
}

func stringPtrIfNotEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func toSettingsResponse(settings Settings) settingsResponse {
	var warmupModel *string
	if settings.WarmupModelPreference.Valid {
		warmupModel = &settings.WarmupModelPreference.String
	}
	return settingsResponse{
		Mode:                   settings.Mode,
		Timezone:               settings.Timezone,
		WorkingDays:            httputil.EmptySlice(settings.WorkingDays),
		WorkingHoursStart:      settings.WorkingHoursStart,
		WorkingHoursEnd:        settings.WorkingHoursEnd,
		PrewarmEnabled:         settings.PrewarmEnabled,
		PrewarmLeadMinutes:     settings.PrewarmLeadMinutes,
		MaxWarmupsPerDay:       settings.MaxWarmupsPerDay,
		MaxWarmupCreditsPerDay: settings.MaxWarmupCreditsPerDay,
		MinExpectedGain:        settings.MinExpectedGain,
		ForecastQuantile:       settings.ForecastQuantile,
		AllowSyntheticTraffic:  settings.AllowSyntheticTraffic,
		WarmupModelPreference:  warmupModel,
		DryRun:                 settings.DryRun,
	}
}

func toDecisionResponse(row Decision) decisionResponse {
	var details map[string]any
	if row.StateBeforeJSON.Valid && row.StateBeforeJSON.String != "" {
		_ = json.Unmarshal([]byte(row.StateBeforeJSON.String), &details)
	}
	return decisionResponse{
		ID:             row.ID,
		CreatedAt:      formatTime(row.CreatedAt),
		Mode:           row.Mode,
		AccountID:      nullStringPtr(row.AccountID),
		Action:         row.Action,
		ScheduledAt:    nullTimePtr(row.ScheduledAt),
		ExecutedAt:     nullTimePtr(row.ExecutedAt),
		Score:          row.Score,
		Reason:         nullStringPtr(row.Reason),
		Details:        details,
		Status:         row.Status,
		IdempotencyKey: row.IdempotencyKey,
	}
}

func mergeSettings(current Settings, payload map[string]json.RawMessage) Settings {
	updated := current
	if raw, ok := payload["mode"]; ok {
		_ = json.Unmarshal(raw, &updated.Mode)
	}
	if raw, ok := payload["timezone"]; ok {
		var timezone string
		if json.Unmarshal(raw, &timezone) == nil && timezone != "" {
			updated.Timezone = timezone
		}
	}
	if raw, ok := payload["workingDays"]; ok {
		var days []int
		if json.Unmarshal(raw, &days) == nil {
			updated.WorkingDays = days
		}
	}
	if raw, ok := payload["workingHoursStart"]; ok {
		_ = json.Unmarshal(raw, &updated.WorkingHoursStart)
	}
	if raw, ok := payload["workingHoursEnd"]; ok {
		_ = json.Unmarshal(raw, &updated.WorkingHoursEnd)
	}
	if raw, ok := payload["prewarmEnabled"]; ok {
		_ = json.Unmarshal(raw, &updated.PrewarmEnabled)
	}
	if raw, ok := payload["prewarmLeadMinutes"]; ok {
		_ = json.Unmarshal(raw, &updated.PrewarmLeadMinutes)
	}
	if raw, ok := payload["maxWarmupsPerDay"]; ok {
		_ = json.Unmarshal(raw, &updated.MaxWarmupsPerDay)
	}
	if raw, ok := payload["maxWarmupCreditsPerDay"]; ok {
		_ = json.Unmarshal(raw, &updated.MaxWarmupCreditsPerDay)
	}
	if raw, ok := payload["minExpectedGain"]; ok {
		_ = json.Unmarshal(raw, &updated.MinExpectedGain)
	}
	if raw, ok := payload["forecastQuantile"]; ok {
		_ = json.Unmarshal(raw, &updated.ForecastQuantile)
	}
	if raw, ok := payload["allowSyntheticTraffic"]; ok {
		_ = json.Unmarshal(raw, &updated.AllowSyntheticTraffic)
	}
	if raw, ok := payload["warmupModelPreference"]; ok {
		if string(raw) == "null" {
			updated.WarmupModelPreference = sql.NullString{}
		} else {
			var model string
			if json.Unmarshal(raw, &model) == nil {
				updated.WarmupModelPreference = sql.NullString{String: model, Valid: true}
			}
		}
	}
	if raw, ok := payload["dryRun"]; ok {
		_ = json.Unmarshal(raw, &updated.DryRun)
	}
	return updated
}

func validateWorkingDays(days []int) error {
	if len(days) == 0 {
		return errWorkingDaysEmpty
	}
	seen := map[int]struct{}{}
	for _, day := range days {
		if day < 0 || day > 6 {
			return errWorkingDaysInvalid
		}
		if _, ok := seen[day]; ok {
			return errWorkingDaysDuplicate
		}
		seen[day] = struct{}{}
	}
	return nil
}

var (
	errWorkingDaysEmpty     = errorString("workingDays must include at least one weekday")
	errWorkingDaysInvalid   = errorString("workingDays must contain unique weekday numbers 0-6")
	errWorkingDaysDuplicate = errorString("workingDays must contain unique weekday numbers 0-6")
)

type errorString string

func (e errorString) Error() string { return string(e) }

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
	if iso := platform.SQLiteTimeToISO(value); iso != nil {
		return iso
	}
	return nil
}
