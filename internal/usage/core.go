package usage

import "time"

// WindowRow mirrors the Python UsageWindowRow value type: a single account's
// usage observation for one window (primary/secondary/monthly).
type WindowRow struct {
	AccountID     string
	UsedPercent   *float64
	ResetAt       *int64
	WindowMinutes *int64
	RecordedAt    string
}

// ToWindowRow converts a usage_history row into a WindowRow.
func (e Entry) ToWindowRow() WindowRow {
	usedPercent := e.UsedPercent
	row := WindowRow{AccountID: e.AccountID, UsedPercent: &usedPercent, RecordedAt: e.RecordedAt}
	if e.ResetAt.Valid {
		row.ResetAt = &e.ResetAt.Int64
	}
	if e.WindowMinutes.Valid {
		row.WindowMinutes = &e.WindowMinutes.Int64
	}
	return row
}

// ToWindowRow converts an aggregated usage_history summary into a WindowRow.
func (a AggregateRow) ToWindowRow() WindowRow {
	row := WindowRow{AccountID: a.AccountID, RecordedAt: a.LastRecordedAt}
	if a.UsedPercentAvg.Valid {
		row.UsedPercent = &a.UsedPercentAvg.Float64
	}
	if a.ResetAtMax.Valid {
		row.ResetAt = &a.ResetAtMax.Int64
	}
	if a.WindowMinutesMax.Valid {
		row.WindowMinutes = &a.WindowMinutesMax.Int64
	}
	return row
}

// Plan capacity tables, ported from app/core/usage/__init__.py.
var planCapacityCreditsPrimary = map[string]float64{
	"free":       0.0,
	"plus":       225.0,
	"business":   225.0,
	"team":       225.0,
	"edu":        225.0,
	"pro":        1500.0,
	"prolite":    1125.0,
	"enterprise": 1500.0,
}

var planCapacityCreditsSecondary = map[string]float64{
	"free":       1134.0,
	"plus":       7560.0,
	"business":   7560.0,
	"team":       7560.0,
	"edu":        7560.0,
	"pro":        50400.0,
	"prolite":    37800.0,
	"enterprise": 50400.0,
}

var planCapacityCreditsMonthly = map[string]float64{
	"free": 1134.0,
}

const (
	defaultWindowMinutesPrimary   = 300
	defaultWindowMinutesSecondary = 10080
	defaultWindowMinutesMonthly   = 43200
)

var accountPlanTypes = map[string]bool{
	"free":       true,
	"plus":       true,
	"pro":        true,
	"prolite":    true,
	"team":       true,
	"business":   true,
	"enterprise": true,
	"edu":        true,
}

// normalizeAccountPlanType ports app.core.plan_types.normalize_account_plan_type.
func normalizeAccountPlanType(planType string) string {
	normalized := lowerTrim(planType)
	if normalized == "" || !accountPlanTypes[normalized] {
		return ""
	}
	return normalized
}

func lowerTrim(value string) string {
	start, end := 0, len(value)
	for start < end && (value[start] == ' ' || value[start] == '\t' || value[start] == '\n') {
		start++
	}
	for end > start && (value[end-1] == ' ' || value[end-1] == '\t' || value[end-1] == '\n') {
		end--
	}
	trimmed := value[start:end]
	out := make([]byte, len(trimmed))
	for i := 0; i < len(trimmed); i++ {
		c := trimmed[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

func normalizeWindowKey(window string) string {
	switch lowerTrim(window) {
	case "primary", "5h":
		return "primary"
	case "secondary", "7d":
		return "secondary"
	case "monthly", "30d":
		return "monthly"
	default:
		return lowerTrim(window)
	}
}

// CapacityForPlan ports app.core.usage.capacity_for_plan. It is exported for
// use by internal/proxy's account-selection logic.
func CapacityForPlan(planType string, window string) *float64 {
	return capacityForPlan(planType, window)
}

// DefaultWindowMinutes ports app.core.usage.default_window_minutes. It is
// exported for use by internal/proxy's account-selection logic.
func DefaultWindowMinutes(window string) *int64 {
	return defaultWindowMinutes(window)
}

// IsPrimaryWindowMinutes ports app.core.usage.is_primary_window_minutes. It
// is exported for use by internal/proxy's account-selection logic.
func IsPrimaryWindowMinutes(windowMinutes *int64) bool {
	if windowMinutes == nil {
		return false
	}
	primaryDefault := defaultWindowMinutes("primary")
	return primaryDefault != nil && *windowMinutes == *primaryDefault
}

// capacityForPlan ports app.core.usage.capacity_for_plan.
func capacityForPlan(planType string, window string) *float64 {
	normalized := normalizeAccountPlanType(planType)
	if normalized == "" {
		return nil
	}
	var table map[string]float64
	switch normalizeWindowKey(window) {
	case "primary":
		table = planCapacityCreditsPrimary
	case "secondary":
		table = planCapacityCreditsSecondary
	case "monthly":
		table = planCapacityCreditsMonthly
	default:
		return nil
	}
	value, ok := table[normalized]
	if !ok {
		return nil
	}
	return &value
}

// defaultWindowMinutes ports app.core.usage.default_window_minutes.
func defaultWindowMinutes(window string) *int64 {
	var minutes int64
	switch normalizeWindowKey(window) {
	case "primary":
		minutes = defaultWindowMinutesPrimary
	case "secondary":
		minutes = defaultWindowMinutesSecondary
	case "monthly":
		minutes = defaultWindowMinutesMonthly
	default:
		return nil
	}
	return &minutes
}

// resolveWindowMinutes ports app.core.usage.resolve_window_minutes / _resolve_window_minutes.
func resolveWindowMinutes(window string, rows []WindowRow) *int64 {
	values := map[int64]struct{}{}
	for _, row := range rows {
		if row.WindowMinutes != nil && *row.WindowMinutes > 0 {
			values[*row.WindowMinutes] = struct{}{}
		}
	}
	if len(values) == 0 {
		return defaultWindowMinutes(window)
	}
	if len(values) == 1 {
		for value := range values {
			v := value
			return &v
		}
	}
	if def := defaultWindowMinutes(window); def != nil {
		return def
	}
	var min int64
	first := true
	for value := range values {
		if first || value < min {
			min = value
			first = false
		}
	}
	return &min
}

func isWeeklyWindowMinutes(windowMinutes *int64) bool {
	return windowMinutes != nil && *windowMinutes == defaultWindowMinutesSecondary
}

// ShouldUseWeeklyPrimary ports app.core.usage.should_use_weekly_primary. It is
// exported for use by internal/proxy's account-selection logic.
func ShouldUseWeeklyPrimary(primaryRow WindowRow, secondaryRow *WindowRow) bool {
	if !isWeeklyWindowMinutes(primaryRow.WindowMinutes) {
		return false
	}
	if secondaryRow == nil {
		return true
	}
	return shouldPreferPrimaryRow(primaryRow, *secondaryRow)
}

// remainingPercentFromUsed ports app.core.usage.remaining_percent_from_used.
func remainingPercentFromUsed(usedPercent *float64) *float64 {
	if usedPercent == nil {
		return nil
	}
	value := *usedPercent
	if value > 100 {
		value = 100
	}
	remaining := 100 - value
	if remaining < 0 {
		remaining = 0
	}
	return &remaining
}

// usedCreditsFromPercent ports app.core.usage.used_credits_from_percent.
func usedCreditsFromPercent(usedPercent *float64, capacityCredits *float64) *float64 {
	if usedPercent == nil || capacityCredits == nil {
		return nil
	}
	value := (*capacityCredits * *usedPercent) / 100.0
	return &value
}

// remainingCreditsFromUsed ports app.core.usage.remaining_credits_from_used.
func remainingCreditsFromUsed(usedCredits *float64, capacityCredits *float64) *float64 {
	if usedCredits == nil || capacityCredits == nil {
		return nil
	}
	value := *capacityCredits - *usedCredits
	if value < 0 {
		value = 0
	}
	return &value
}

// remainingCreditsFromPercent ports app.core.usage.remaining_credits_from_percent.
func remainingCreditsFromPercent(usedPercent *float64, capacityCredits *float64) *float64 {
	return remainingCreditsFromUsed(usedCreditsFromPercent(usedPercent, capacityCredits), capacityCredits)
}

// WindowSummary mirrors app.core.usage.types.UsageWindowSummary.
type WindowSummary struct {
	UsedPercent     *float64
	CapacityCredits float64
	UsedCredits     float64
	ResetAt         *int64
	WindowMinutes   *int64
}

// WindowSnapshot mirrors app.core.usage.types.UsageWindowSnapshot.
type WindowSnapshot struct {
	UsedPercent     float64
	CapacityCredits float64
	UsedCredits     float64
	ResetAt         *int64
	WindowMinutes   *int64
}

// normalizeUsageWindow ports app.core.usage.normalize_usage_window.
func normalizeUsageWindow(summary WindowSummary) WindowSnapshot {
	usedPercent := 0.0
	if summary.UsedPercent != nil {
		usedPercent = *summary.UsedPercent
	}
	return WindowSnapshot{
		UsedPercent:     usedPercent,
		CapacityCredits: summary.CapacityCredits,
		UsedCredits:     summary.UsedCredits,
		ResetAt:         summary.ResetAt,
		WindowMinutes:   summary.WindowMinutes,
	}
}

// summarizeUsageWindow ports app.core.usage.summarize_usage_window.
func summarizeUsageWindow(rows []WindowRow, planByAccount map[string]string, window string) WindowSummary {
	var totalCapacity, totalUsed float64
	var resetCandidates []int64
	var windowMinutesValues []int64
	seenMinutes := map[int64]struct{}{}

	for _, row := range rows {
		if row.ResetAt != nil {
			resetCandidates = append(resetCandidates, *row.ResetAt)
		}
		if row.WindowMinutes != nil && *row.WindowMinutes > 0 {
			if _, ok := seenMinutes[*row.WindowMinutes]; !ok {
				seenMinutes[*row.WindowMinutes] = struct{}{}
				windowMinutesValues = append(windowMinutesValues, *row.WindowMinutes)
			}
		}
		planType := planByAccount[row.AccountID]
		capacity := capacityForPlan(planType, window)
		if row.UsedPercent == nil || capacity == nil {
			continue
		}
		totalCapacity += *capacity
		totalUsed += (*capacity * *row.UsedPercent) / 100.0
	}

	windowMinutesRows := make([]WindowRow, 0, len(windowMinutesValues))
	for _, value := range windowMinutesValues {
		v := value
		windowMinutesRows = append(windowMinutesRows, WindowRow{WindowMinutes: &v})
	}
	windowMinutes := resolveWindowMinutes(window, windowMinutesRows)

	var overall *float64
	if totalCapacity > 0 {
		value := (totalUsed / totalCapacity) * 100.0
		overall = &value
	}

	var resetAt *int64
	if len(resetCandidates) > 0 {
		min := resetCandidates[0]
		for _, candidate := range resetCandidates[1:] {
			if candidate < min {
				min = candidate
			}
		}
		resetAt = &min
	}

	return WindowSummary{
		UsedPercent:     overall,
		CapacityCredits: totalCapacity,
		UsedCredits:     totalUsed,
		ResetAt:         resetAt,
		WindowMinutes:   windowMinutes,
	}
}

// normalizeWeeklyOnlyRows ports app.core.usage.normalize_weekly_only_rows.
// Some plans (notably free) report only one weekly window in the primary
// slot. Re-map those rows into secondary so downstream 5h/7d consumers
// operate on consistent semantics.
func normalizeWeeklyOnlyRows(primaryRows, secondaryRows []WindowRow) (primary []WindowRow, secondary []WindowRow) {
	primaryByAccount := map[string]WindowRow{}
	var primaryOrder []string
	for _, row := range primaryRows {
		if _, ok := primaryByAccount[row.AccountID]; !ok {
			primaryOrder = append(primaryOrder, row.AccountID)
		}
		primaryByAccount[row.AccountID] = row
	}

	secondaryByAccount := map[string]WindowRow{}
	var secondaryOrder []string
	for _, row := range secondaryRows {
		if _, ok := secondaryByAccount[row.AccountID]; !ok {
			secondaryOrder = append(secondaryOrder, row.AccountID)
		}
		secondaryByAccount[row.AccountID] = row
	}

	for _, accountID := range primaryOrder {
		primaryRow := primaryByAccount[accountID]
		if isWeeklyWindowMinutes(primaryRow.WindowMinutes) {
			secondaryRow, hasSecondary := secondaryByAccount[accountID]
			var preferPrimary bool
			if !hasSecondary {
				preferPrimary = true
			} else {
				preferPrimary = shouldPreferPrimaryRow(primaryRow, secondaryRow)
			}
			if preferPrimary {
				if _, ok := secondaryByAccount[accountID]; !ok {
					secondaryOrder = append(secondaryOrder, accountID)
				}
				secondaryByAccount[accountID] = primaryRow
			}
			continue
		}
		primary = append(primary, primaryRow)
	}

	secondary = make([]WindowRow, 0, len(secondaryOrder))
	for _, accountID := range secondaryOrder {
		secondary = append(secondary, secondaryByAccount[accountID])
	}
	return primary, secondary
}

// shouldPreferPrimaryRow ports app.core.usage._should_prefer_primary_row.
func shouldPreferPrimaryRow(primaryRow, secondaryRow WindowRow) bool {
	primaryRecordedAt, primaryOK := parseSQLiteTime(primaryRow.RecordedAt)
	secondaryRecordedAt, secondaryOK := parseSQLiteTime(secondaryRow.RecordedAt)
	if primaryOK && secondaryOK {
		if !primaryRecordedAt.Equal(secondaryRecordedAt) {
			return primaryRecordedAt.After(secondaryRecordedAt)
		}
	} else if primaryOK {
		return true
	} else if secondaryOK {
		return false
	}

	if primaryRow.ResetAt != nil && secondaryRow.ResetAt != nil {
		if *primaryRow.ResetAt != *secondaryRow.ResetAt {
			return *primaryRow.ResetAt > *secondaryRow.ResetAt
		}
	} else if primaryRow.ResetAt != nil {
		return true
	} else if secondaryRow.ResetAt != nil {
		return false
	}

	// Keep weekly-only semantics stable when timestamps are unavailable.
	return true
}

func parseSQLiteTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}
