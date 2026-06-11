package quotaplanner

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/httputil"
)

const settingsID = 1

type Repository struct {
	store *db.Store
}

type Settings struct {
	Mode                   string
	Timezone               string
	WorkingDays            []int
	WorkingHoursStart      string
	WorkingHoursEnd        string
	PrewarmEnabled         bool
	PrewarmLeadMinutes     int
	MaxWarmupsPerDay       int
	MaxWarmupCreditsPerDay float64
	MinExpectedGain        float64
	ForecastQuantile       string
	AllowSyntheticTraffic  bool
	WarmupModelPreference  sql.NullString
	DryRun                 bool
}

type Decision struct {
	ID              string
	CreatedAt       sql.NullString
	Mode            string
	AccountID       sql.NullString
	Action          string
	ScheduledAt     sql.NullString
	ExecutedAt      sql.NullString
	Score           float64
	Reason          sql.NullString
	StateBeforeJSON sql.NullString
	Status          string
	IdempotencyKey  string
}

type ForecastSlot struct {
	SlotStart    string
	DemandUnits  float64
	RequestCount int
	Source       string
}

type Forecast struct {
	GeneratedAt        string
	HorizonHours       int
	SlotSeconds        int
	TotalDemandUnits   float64
	PeakSlotStart      *string
	PeakDemandUnits    float64
	Slots              []ForecastSlot
	SimulationLoss     float64
	SimulationUnmet    float64
	SimulationWasted   float64
	SimulationCold     float64
	SimulationSync     float64
	SimulationForecast float64
	SimulationServed   float64
}

type decisionScanner interface {
	Scan(dest ...any) error
}

func NewRepository(store *db.Store) Repository {
	return Repository{store: store}
}

func (r Repository) GetSettings(ctx context.Context) (Settings, error) {
	row := r.store.DB().QueryRowContext(ctx, `
		SELECT mode, timezone, working_days_json, working_hours_start, working_hours_end,
		       prewarm_enabled, prewarm_lead_minutes, max_warmups_per_day, max_warmup_credits_per_day,
		       min_expected_gain, forecast_quantile, allow_synthetic_traffic, warmup_model_preference, dry_run
		  FROM quota_planner_settings
		 WHERE id = ?
	`, settingsID)
	settings, err := scanSettings(row)
	if err == sql.ErrNoRows {
		return defaultSettings(), nil
	}
	return settings, err
}

func (r Repository) UpsertSettings(ctx context.Context, settings Settings) (Settings, error) {
	workingDaysJSON, err := json.Marshal(settings.WorkingDays)
	if err != nil {
		return Settings{}, err
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	_, err = r.store.DB().ExecContext(ctx, `
		INSERT INTO quota_planner_settings (
			id, mode, timezone, working_days_json, working_hours_start, working_hours_end,
			prewarm_enabled, prewarm_lead_minutes, max_warmups_per_day, max_warmup_credits_per_day,
			min_expected_gain, forecast_quantile, allow_synthetic_traffic, warmup_model_preference, dry_run,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			mode = excluded.mode,
			timezone = excluded.timezone,
			working_days_json = excluded.working_days_json,
			working_hours_start = excluded.working_hours_start,
			working_hours_end = excluded.working_hours_end,
			prewarm_enabled = excluded.prewarm_enabled,
			prewarm_lead_minutes = excluded.prewarm_lead_minutes,
			max_warmups_per_day = excluded.max_warmups_per_day,
			max_warmup_credits_per_day = excluded.max_warmup_credits_per_day,
			min_expected_gain = excluded.min_expected_gain,
			forecast_quantile = excluded.forecast_quantile,
			allow_synthetic_traffic = excluded.allow_synthetic_traffic,
			warmup_model_preference = excluded.warmup_model_preference,
			dry_run = excluded.dry_run,
			updated_at = excluded.updated_at
	`, settingsID, settings.Mode, settings.Timezone, string(workingDaysJSON), settings.WorkingHoursStart, settings.WorkingHoursEnd,
		boolToInt(settings.PrewarmEnabled), settings.PrewarmLeadMinutes, settings.MaxWarmupsPerDay, settings.MaxWarmupCreditsPerDay,
		settings.MinExpectedGain, settings.ForecastQuantile, boolToInt(settings.AllowSyntheticTraffic),
		nullString(settings.WarmupModelPreference), boolToInt(settings.DryRun), now, now)
	if err != nil {
		return Settings{}, fmt.Errorf("upsert quota planner settings: %w", err)
	}
	return r.GetSettings(ctx)
}

func (r Repository) RecentDecisions(ctx context.Context, limit int) ([]Decision, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT id, created_at, mode, account_id, action, scheduled_at, executed_at,
		       score, reason, state_before_json, status, idempotency_key
		  FROM quota_planner_decisions
		 ORDER BY created_at DESC
		 LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list quota planner decisions: %w", err)
	}
	defer rows.Close()

	var decisions []Decision
	for rows.Next() {
		decision, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		decisions = append(decisions, decision)
	}
	return httputil.EmptySlice(decisions), rows.Err()
}

func (r Repository) GetDecision(ctx context.Context, decisionID string) (*Decision, error) {
	row := r.store.DB().QueryRowContext(ctx, `
		SELECT id, created_at, mode, account_id, action, scheduled_at, executed_at,
		       score, reason, state_before_json, status, idempotency_key
		  FROM quota_planner_decisions
		 WHERE id = ?
	`, decisionID)
	decision, err := scanDecision(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &decision, nil
}

func (r Repository) DuePlannedWarmups(ctx context.Context, now time.Time, limit int) ([]Decision, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT id, created_at, mode, account_id, action, scheduled_at, executed_at,
		       score, reason, state_before_json, status, idempotency_key
		  FROM quota_planner_decisions
		 WHERE status = 'planned'
		   AND action = 'warmup'
		   AND account_id IS NOT NULL
		   AND (scheduled_at IS NULL OR scheduled_at <= ?)
		 ORDER BY scheduled_at ASC, created_at ASC
		 LIMIT ?
	`, now.UTC().Format("2006-01-02 15:04:05"), limit)
	if err != nil {
		return nil, fmt.Errorf("list due quota planner warmups: %w", err)
	}
	defer rows.Close()
	var decisions []Decision
	for rows.Next() {
		decision, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		decisions = append(decisions, decision)
	}
	return httputil.EmptySlice(decisions), rows.Err()
}

func (r Repository) HasDecisionWithIdempotencyKey(ctx context.Context, key string) (bool, error) {
	var count int
	if err := r.store.DB().QueryRowContext(ctx, `
		SELECT count(*) FROM quota_planner_decisions WHERE idempotency_key = ?
	`, key).Scan(&count); err != nil {
		return false, fmt.Errorf("check quota planner decision idempotency: %w", err)
	}
	return count > 0, nil
}

type LogDecisionParams struct {
	Mode           string
	Action         string
	AccountID      sql.NullString
	ScheduledAt    string
	Score          float64
	Reason         sql.NullString
	Status         string
	IdempotencyKey string
}

func (r Repository) LogDecision(ctx context.Context, params LogDecisionParams) (Decision, error) {
	id := uuid.NewString()
	if params.Status == "" {
		params.Status = "planned"
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	_, err := r.store.DB().ExecContext(ctx, `
		INSERT INTO quota_planner_decisions (
			id, created_at, mode, account_id, action, scheduled_at, score, reason, status, idempotency_key
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(idempotency_key) DO NOTHING
	`, id, now, params.Mode, nullString(params.AccountID), params.Action, nullableStringValue(params.ScheduledAt),
		params.Score, nullString(params.Reason), params.Status, params.IdempotencyKey)
	if err != nil {
		return Decision{}, fmt.Errorf("log quota planner decision: %w", err)
	}
	row := r.store.DB().QueryRowContext(ctx, `
		SELECT id, created_at, mode, account_id, action, scheduled_at, executed_at,
		       score, reason, state_before_json, status, idempotency_key
		  FROM quota_planner_decisions
		 WHERE id = ? OR idempotency_key = ?
		 ORDER BY created_at DESC
		 LIMIT 1
	`, id, params.IdempotencyKey)
	decision, err := scanDecision(row)
	if err != nil {
		return Decision{}, err
	}
	return decision, nil
}

func (r Repository) UpdateDecisionStatus(ctx context.Context, decisionID, status, reason string, executedAt sql.NullString, expectedStatuses ...string) (*Decision, bool, error) {
	args := []any{status, reason, nullString(executedAt), decisionID}
	where := "id = ?"
	if len(expectedStatuses) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(expectedStatuses)), ",")
		where += " AND status IN (" + placeholders + ")"
		for _, expected := range expectedStatuses {
			args = append(args, expected)
		}
	}
	result, err := r.store.DB().ExecContext(ctx, `
		UPDATE quota_planner_decisions
		   SET status = ?, reason = ?, executed_at = COALESCE(?, executed_at)
		 WHERE `+where, args...)
	if err != nil {
		return nil, false, fmt.Errorf("update quota planner decision status: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	decision, err := r.GetDecision(ctx, decisionID)
	if err != nil {
		return nil, false, err
	}
	return decision, rows > 0, nil
}

func scanDecision(row decisionScanner) (Decision, error) {
	var decision Decision
	if err := row.Scan(
		&decision.ID, &decision.CreatedAt, &decision.Mode, &decision.AccountID, &decision.Action,
		&decision.ScheduledAt, &decision.ExecutedAt, &decision.Score, &decision.Reason,
		&decision.StateBeforeJSON, &decision.Status, &decision.IdempotencyKey,
	); err != nil {
		return Decision{}, err
	}
	return decision, nil
}

func (r Repository) CancelDecision(ctx context.Context, decisionID string) (*Decision, bool, error) {
	result, err := r.store.DB().ExecContext(ctx, `
		UPDATE quota_planner_decisions
		   SET status = 'canceled', reason = 'admin_canceled'
		 WHERE id = ? AND status IN ('planned', 'skipped')
	`, decisionID)
	if err != nil {
		return nil, false, fmt.Errorf("cancel quota planner decision: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	decision, err := r.GetDecision(ctx, decisionID)
	if err != nil {
		return nil, false, err
	}
	if decision == nil {
		return nil, false, nil
	}
	return decision, rows > 0, nil
}

func (r Repository) BuildForecast(ctx context.Context, horizonHours int) (Forecast, error) {
	const slotSeconds = 3600
	now := time.Now().UTC().Truncate(time.Duration(slotSeconds) * time.Second)
	slotCount := horizonHours
	if slotCount < 1 {
		slotCount = 36
	}
	since := now.Add(-7 * 24 * time.Hour).Format("2006-01-02 15:04:05")
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT strftime('%H', requested_at) AS hour_bucket,
		       COUNT(*) AS request_count,
		       COALESCE(SUM(COALESCE(input_tokens, 0) + COALESCE(output_tokens, 0) + COALESCE(reasoning_tokens, 0)), 0) AS demand_units
		  FROM request_logs
		 WHERE deleted_at IS NULL
		   AND request_kind NOT IN ('warmup', 'limit_warmup')
		   AND requested_at >= ?
		 GROUP BY hour_bucket
	`, since)
	if err != nil {
		return Forecast{}, fmt.Errorf("aggregate forecast demand: %w", err)
	}
	defer rows.Close()

	hourDemand := map[int]float64{}
	hourCount := map[int]int{}
	for rows.Next() {
		var hour string
		var count int
		var demand float64
		if err := rows.Scan(&hour, &count, &demand); err != nil {
			return Forecast{}, err
		}
		h, _ := parseHour(hour)
		hourDemand[h] = demand / 7.0
		hourCount[h] = count / 7
	}
	if err := rows.Err(); err != nil {
		return Forecast{}, err
	}

	slots := make([]ForecastSlot, 0, slotCount)
	var totalDemand float64
	var peakDemand float64
	var peakStart *string
	for i := 0; i < slotCount; i++ {
		slotStart := now.Add(time.Duration(i) * time.Hour)
		hour := slotStart.Hour()
		demand := hourDemand[hour]
		count := hourCount[hour]
		totalDemand += demand
		if demand >= peakDemand {
			peakDemand = demand
			start := slotStart.Format(time.RFC3339Nano)
			peakStart = &start
		}
		slots = append(slots, ForecastSlot{
			SlotStart:    slotStart.Format(time.RFC3339Nano),
			DemandUnits:  demand,
			RequestCount: count,
			Source:       "historical_hourly",
		})
	}
	return Forecast{
		GeneratedAt:        now.Format(time.RFC3339Nano),
		HorizonHours:       slotCount,
		SlotSeconds:        slotSeconds,
		TotalDemandUnits:   totalDemand,
		PeakSlotStart:      peakStart,
		PeakDemandUnits:    peakDemand,
		Slots:              httputil.EmptySlice(slots),
		SimulationLoss:     0,
		SimulationUnmet:    0,
		SimulationWasted:   0,
		SimulationCold:     0,
		SimulationSync:     0,
		SimulationForecast: totalDemand,
		SimulationServed:   totalDemand,
	}, nil
}

func defaultSettings() Settings {
	return Settings{
		Mode:                   "shadow",
		Timezone:               "UTC",
		WorkingDays:            []int{0, 1, 2, 3, 4},
		WorkingHoursStart:      "09:00",
		WorkingHoursEnd:        "18:00",
		PrewarmEnabled:         true,
		PrewarmLeadMinutes:     300,
		MaxWarmupsPerDay:       3,
		MaxWarmupCreditsPerDay: 0,
		MinExpectedGain:        1,
		ForecastQuantile:       "p75",
		AllowSyntheticTraffic:  false,
		DryRun:                 true,
	}
}

func scanSettings(row *sql.Row) (Settings, error) {
	var settings Settings
	var workingDaysJSON string
	var prewarmEnabled int
	var allowSynthetic int
	var dryRun int
	var warmupModel sql.NullString
	err := row.Scan(
		&settings.Mode, &settings.Timezone, &workingDaysJSON, &settings.WorkingHoursStart, &settings.WorkingHoursEnd,
		&prewarmEnabled, &settings.PrewarmLeadMinutes, &settings.MaxWarmupsPerDay, &settings.MaxWarmupCreditsPerDay,
		&settings.MinExpectedGain, &settings.ForecastQuantile, &allowSynthetic, &warmupModel, &dryRun,
	)
	if err != nil {
		return Settings{}, err
	}
	settings.PrewarmEnabled = prewarmEnabled != 0
	settings.AllowSyntheticTraffic = allowSynthetic != 0
	settings.DryRun = dryRun != 0
	settings.WarmupModelPreference = warmupModel
	settings.WorkingDays = parseWorkingDays(workingDaysJSON)
	sort.Ints(settings.WorkingDays)
	return settings, nil
}

func parseWorkingDays(raw string) []int {
	var days []int
	if err := json.Unmarshal([]byte(raw), &days); err != nil || len(days) == 0 {
		return []int{0, 1, 2, 3, 4}
	}
	normalized := make([]int, 0, len(days))
	seen := map[int]struct{}{}
	for _, day := range days {
		if day < 0 || day > 6 {
			continue
		}
		if _, ok := seen[day]; ok {
			continue
		}
		seen[day] = struct{}{}
		normalized = append(normalized, day)
	}
	if len(normalized) == 0 {
		return []int{0, 1, 2, 3, 4}
	}
	return normalized
}

func parseHour(raw string) (int, error) {
	var hour int
	_, err := fmt.Sscanf(strings.TrimSpace(raw), "%d", &hour)
	return hour, err
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullString(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}

func nullableStringValue(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
