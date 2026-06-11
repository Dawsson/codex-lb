package dashboard

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/soju06/codex-lb/internal/accounts"
)

const (
	proWeeklyCapacityCredits = 50400.0
	recentBurnWindow         = 6 * time.Hour
	minFreshnessSeconds      = 300.0
	freshnessMissedRefreshes = 3.0
)

type weeklyCreditPaceResponse struct {
	TotalFullCredits                    float64  `json:"totalFullCredits"`
	TotalActualRemainingCredits         float64  `json:"totalActualRemainingCredits"`
	TotalExpectedRemainingCredits       float64  `json:"totalExpectedRemainingCredits"`
	ActualUsedPercent                   float64  `json:"actualUsedPercent"`
	ScheduledUsedPercent                float64  `json:"scheduledUsedPercent"`
	DeltaPercent                        float64  `json:"deltaPercent"`
	ScheduleGapCredits                  float64  `json:"scheduleGapCredits"`
	OverPlanCredits                     float64  `json:"overPlanCredits"`
	ProjectedShortfallCredits           float64  `json:"projectedShortfallCredits"`
	PauseForBreakEvenHours              *float64 `json:"pauseForBreakEvenHours"`
	PaceMultiplier                      *float64 `json:"paceMultiplier"`
	ThrottleToPercent                   *float64 `json:"throttleToPercent"`
	ReduceByPercent                     *float64 `json:"reduceByPercent"`
	ProAccountEquivalentToCoverOverPlan *float64 `json:"proAccountEquivalentToCoverOverPlan"`
	ProAccountsToCoverOverPlan          *int     `json:"proAccountsToCoverOverPlan"`
	ProjectedDepletionHours             *float64 `json:"projectedDepletionHours"`
	ProjectedMinimumRemainingCredits    *float64 `json:"projectedMinimumRemainingCredits"`
	ForecastBurnRateCreditsPerHour      *float64 `json:"forecastBurnRateCreditsPerHour"`
	ScheduledBurnRateCreditsPerHour     float64  `json:"scheduledBurnRateCreditsPerHour"`
	Status                              string   `json:"status"`
	AccountCount                        int      `json:"accountCount"`
	StaleAccountCount                   int      `json:"staleAccountCount"`
	InactiveAccountCount                int      `json:"inactiveAccountCount"`
	Confidence                          string   `json:"confidence"`
}

type paceAccount struct {
	fullCredits                    float64
	remainingCredits               float64
	resetAtMS                      float64
	windowMS                       float64
	forecastBurnRateCreditsPerHour *float64
}

type simulationAccount struct {
	fullCredits    float64
	balanceCredits float64
	resetAtMS      float64
	windowMS       float64
}

type weeklyProjection struct {
	projectedShortfallCredits        float64
	projectedDepletionHours          *float64
	projectedMinimumRemainingCredits float64
}

func buildWeeklyCreditPace(
	summaries []accounts.AccountSummary,
	rows []UsageHistoryRow,
	now time.Time,
	usageRefreshIntervalSeconds int,
	workingDays map[int]bool,
) *weeklyCreditPaceResponse {
	nowMS := float64(now.UTC().UnixMilli())
	if !isFinitePositive(nowMS) {
		return nil
	}
	secondaryHistory := secondaryHistoryByAccount(rows)
	freshnessCutoff := now.Add(-time.Duration(freshnessSeconds(usageRefreshIntervalSeconds)) * time.Second)

	var paceAccounts []paceAccount
	staleAccountCount := 0
	inactiveAccountCount := 0
	rateSampleCount := 0
	totalFullCredits := 0.0
	totalActualRemainingCredits := 0.0
	totalExpectedRemainingCredits := 0.0
	scheduledBurnRateCreditsPerHour := 0.0
	forecastBurnRateCreditsPerHour := 0.0

	for _, summary := range summaries {
		timing, ok := weeklyTiming(summary, nowMS)
		if !ok {
			continue
		}
		if summary.Status != "active" {
			inactiveAccountCount++
			continue
		}
		history := secondaryHistory[summary.AccountID]
		if len(history) == 0 {
			staleAccountCount++
			continue
		}
		latestTime, ok := parseSQLiteTime(history[len(history)-1].RecordedAt)
		if !ok || latestTime.Before(freshnessCutoff) {
			staleAccountCount++
			continue
		}

		fullCredits, actualRemainingCredits, effectiveResetAtMS, windowMS := timing.fullCredits, timing.remainingCredits, timing.resetAtMS, timing.windowMS
		usedScheduleFraction := usedScheduleFraction(effectiveResetAtMS, windowMS, nowMS, workingDays)
		expectedRemainingCredits := fullCredits * (1.0 - usedScheduleFraction)
		accountRate := recentBurnRateCreditsPerHour(history, fullCredits, now)

		totalFullCredits += fullCredits
		totalActualRemainingCredits += actualRemainingCredits
		totalExpectedRemainingCredits += expectedRemainingCredits
		scheduledBurnRateCreditsPerHour += fullCredits * workingScheduleSharePerHour(effectiveResetAtMS, windowMS, workingDays)
		if accountRate != nil {
			rateSampleCount++
			forecastBurnRateCreditsPerHour += *accountRate
		}
		paceAccounts = append(paceAccounts, paceAccount{
			fullCredits:                    fullCredits,
			remainingCredits:               actualRemainingCredits,
			resetAtMS:                      effectiveResetAtMS,
			windowMS:                       windowMS,
			forecastBurnRateCreditsPerHour: accountRate,
		})
	}

	if len(paceAccounts) == 0 || totalFullCredits <= 0 {
		return nil
	}

	actualUsedPercent := 100.0 * (totalFullCredits - totalActualRemainingCredits) / totalFullCredits
	scheduledUsedPercent := 100.0 * (totalFullCredits - totalExpectedRemainingCredits) / totalFullCredits
	deltaPercent := actualUsedPercent - scheduledUsedPercent
	scheduleGapCredits := math.Max(0, totalExpectedRemainingCredits-totalActualRemainingCredits)

	var forecastRate *float64
	if rateSampleCount > 0 {
		value := forecastBurnRateCreditsPerHour
		forecastRate = &value
	}
	projection := projectWeeklyPool(paceAccounts, nowMS, forecastRate)
	projectedShortfallCredits := projection.projectedShortfallCredits

	var paceMultiplier *float64
	if forecastRate != nil && scheduledBurnRateCreditsPerHour > 0 {
		value := *forecastRate / scheduledBurnRateCreditsPerHour
		paceMultiplier = &value
	}
	var pauseForBreakEvenHours *float64
	if forecastRate != nil && *forecastRate > 0 && projectedShortfallCredits > 0 {
		value := projectedShortfallCredits / *forecastRate
		pauseForBreakEvenHours = &value
	}
	var throttleToPercent *float64
	if forecastRate != nil && *forecastRate > 0 && scheduledBurnRateCreditsPerHour > 0 && projectedShortfallCredits > 0 {
		value := clamp((scheduledBurnRateCreditsPerHour / *forecastRate)*100.0, 0, 100)
		throttleToPercent = &value
	}
	var reduceByPercent *float64
	if throttleToPercent != nil {
		value := 100.0 - *throttleToPercent
		reduceByPercent = &value
	}
	var proEquivalent *float64
	var proAccounts *int
	if projectedShortfallCredits > 0 {
		equivalent := projectedShortfallCredits / proWeeklyCapacityCredits
		accountsNeeded := int(math.Ceil(equivalent))
		proEquivalent = &equivalent
		proAccounts = &accountsNeeded
	}

	minimumRemaining := projection.projectedMinimumRemainingCredits
	return &weeklyCreditPaceResponse{
		TotalFullCredits:                    totalFullCredits,
		TotalActualRemainingCredits:         totalActualRemainingCredits,
		TotalExpectedRemainingCredits:       totalExpectedRemainingCredits,
		ActualUsedPercent:                   actualUsedPercent,
		ScheduledUsedPercent:                scheduledUsedPercent,
		DeltaPercent:                        deltaPercent,
		ScheduleGapCredits:                  scheduleGapCredits,
		OverPlanCredits:                     scheduleGapCredits,
		ProjectedShortfallCredits:           projectedShortfallCredits,
		PauseForBreakEvenHours:              pauseForBreakEvenHours,
		PaceMultiplier:                      paceMultiplier,
		ThrottleToPercent:                   throttleToPercent,
		ReduceByPercent:                     reduceByPercent,
		ProAccountEquivalentToCoverOverPlan: proEquivalent,
		ProAccountsToCoverOverPlan:          proAccounts,
		ProjectedDepletionHours:             projection.projectedDepletionHours,
		ProjectedMinimumRemainingCredits:    &minimumRemaining,
		ForecastBurnRateCreditsPerHour:      forecastRate,
		ScheduledBurnRateCreditsPerHour:     scheduledBurnRateCreditsPerHour,
		Status:                              weeklyPaceStatus(deltaPercent, projectedShortfallCredits),
		AccountCount:                        len(paceAccounts),
		StaleAccountCount:                   staleAccountCount,
		InactiveAccountCount:                inactiveAccountCount,
		Confidence:                          weeklyPaceConfidence(len(paceAccounts), rateSampleCount, staleAccountCount),
	}
}

type weeklyTimingResult struct {
	fullCredits      float64
	remainingCredits float64
	resetAtMS        float64
	windowMS         float64
}

func weeklyTiming(summary accounts.AccountSummary, nowMS float64) (weeklyTimingResult, bool) {
	if summary.CapacityCreditsSecondary == nil || *summary.CapacityCreditsSecondary <= 0 ||
		summary.RemainingCreditsSecondary == nil || *summary.RemainingCreditsSecondary < 0 ||
		summary.ResetAtSecondary == nil ||
		summary.WindowMinutesSecondary == nil || *summary.WindowMinutesSecondary <= 0 {
		return weeklyTimingResult{}, false
	}
	resetAt, err := time.Parse(time.RFC3339, *summary.ResetAtSecondary)
	if err != nil {
		return weeklyTimingResult{}, false
	}
	fullCredits := *summary.CapacityCreditsSecondary
	remainingCredits := clamp(*summary.RemainingCreditsSecondary, 0, fullCredits)
	windowMS := float64(*summary.WindowMinutesSecondary) * 60000.0
	resetAtMS := float64(resetAt.UTC().UnixMilli())
	if !isFinitePositive(resetAtMS) || !isFinitePositive(windowMS) {
		return weeklyTimingResult{}, false
	}
	return weeklyTimingResult{
		fullCredits:      fullCredits,
		remainingCredits: remainingCredits,
		resetAtMS:        advanceResetAt(resetAtMS, windowMS, nowMS),
		windowMS:         windowMS,
	}, true
}

func secondaryHistoryByAccount(rows []UsageHistoryRow) map[string][]UsageHistoryRow {
	grouped := map[string][]UsageHistoryRow{}
	for _, row := range rows {
		window := row.Window
		if window == "monthly" || (window == "primary" && row.WindowMinutes.Valid && row.WindowMinutes.Int64 >= 7*24*60) || window == "secondary" {
			grouped[row.AccountID] = append(grouped[row.AccountID], row)
		}
	}
	for accountID := range grouped {
		sort.SliceStable(grouped[accountID], func(i, j int) bool {
			return grouped[accountID][i].RecordedAt < grouped[accountID][j].RecordedAt
		})
	}
	return grouped
}

func recentBurnRateCreditsPerHour(rows []UsageHistoryRow, fullCredits float64, now time.Time) *float64 {
	recentStart := now.Add(-recentBurnWindow)
	var recentRows []UsageHistoryRow
	for _, row := range rows {
		recorded, ok := parseSQLiteTime(row.RecordedAt)
		if !ok || recorded.Before(recentStart) || recorded.After(now) {
			continue
		}
		recentRows = append(recentRows, row)
	}
	if len(recentRows) < 2 {
		return nil
	}
	var state *ewmaState
	for _, row := range recentRows {
		recorded, ok := parseSQLiteTime(row.RecordedAt)
		if !ok {
			continue
		}
		var resetAt *int64
		if row.ResetAt.Valid {
			resetAt = &row.ResetAt.Int64
		}
		next := ewmaUpdate(state, row.UsedPercent, float64(recorded.Unix()), resetAt)
		state = &next
	}
	if state == nil || state.rate == nil {
		return nil
	}
	value := math.Max(0, *state.rate*fullCredits*36.0)
	return &value
}

func projectWeeklyPool(accounts []paceAccount, nowMS float64, forecastBurnRateCreditsPerHour *float64) weeklyProjection {
	totalRemaining := 0.0
	for _, account := range accounts {
		totalRemaining += account.remainingCredits
	}
	if forecastBurnRateCreditsPerHour == nil || *forecastBurnRateCreditsPerHour <= 0 {
		return weeklyProjection{projectedShortfallCredits: 0, projectedMinimumRemainingCredits: totalRemaining}
	}
	burnRateCreditsPerMS := *forecastBurnRateCreditsPerHour / 3600000.0
	simulationAccounts := make([]simulationAccount, 0, len(accounts))
	maxWindowMS := 0.0
	for _, account := range accounts {
		simulationAccounts = append(simulationAccounts, simulationAccount{
			fullCredits:    account.fullCredits,
			balanceCredits: account.remainingCredits,
			resetAtMS:      account.resetAtMS,
			windowMS:       account.windowMS,
		})
		if account.windowMS > maxWindowMS {
			maxWindowMS = account.windowMS
		}
	}
	horizonMS := nowMS + maxWindowMS*2.0
	cursorMS := nowMS
	minimumRemaining := totalRemaining
	for cursorMS < horizonMS {
		sort.SliceStable(simulationAccounts, func(i, j int) bool {
			return simulationAccounts[i].resetAtMS < simulationAccounts[j].resetAtMS
		})
		nextEventAtMS := math.Min(simulationAccounts[0].resetAtMS, horizonMS)
		intervalMS := math.Max(0, nextEventAtMS-cursorMS)
		intervalBurn := burnRateCreditsPerMS * intervalMS
		totalBalance := totalSimulationBalance(simulationAccounts)
		if intervalBurn > totalBalance {
			depletionWaitMS := 0.0
			if burnRateCreditsPerMS > 0 {
				depletionWaitMS = totalBalance / burnRateCreditsPerMS
			}
			hours := (cursorMS - nowMS + depletionWaitMS) / 3600000.0
			return weeklyProjection{
				projectedShortfallCredits:        intervalBurn - totalBalance,
				projectedDepletionHours:          &hours,
				projectedMinimumRemainingCredits: 0,
			}
		}
		consumeSimulationBalance(simulationAccounts, intervalBurn)
		minimumRemaining = math.Min(minimumRemaining, totalSimulationBalance(simulationAccounts))
		cursorMS = nextEventAtMS
		if cursorMS >= horizonMS {
			break
		}
		simulationAccounts[0].balanceCredits = simulationAccounts[0].fullCredits
		simulationAccounts[0].resetAtMS += simulationAccounts[0].windowMS
		minimumRemaining = math.Min(minimumRemaining, totalSimulationBalance(simulationAccounts))
	}
	return weeklyProjection{projectedShortfallCredits: 0, projectedMinimumRemainingCredits: minimumRemaining}
}

func consumeSimulationBalance(accounts []simulationAccount, amountCredits float64) {
	sort.SliceStable(accounts, func(i, j int) bool {
		return accounts[i].resetAtMS < accounts[j].resetAtMS
	})
	remaining := amountCredits
	for i := range accounts {
		if remaining <= 0 {
			return
		}
		consumed := math.Min(accounts[i].balanceCredits, remaining)
		accounts[i].balanceCredits -= consumed
		remaining -= consumed
	}
}

func totalSimulationBalance(accounts []simulationAccount) float64 {
	total := 0.0
	for _, account := range accounts {
		total += account.balanceCredits
	}
	return total
}

func advanceResetAt(resetAtMS float64, windowMS float64, nowMS float64) float64 {
	if resetAtMS > nowMS {
		return resetAtMS
	}
	missedWindows := math.Floor((nowMS-resetAtMS)/windowMS) + 1
	return resetAtMS + missedWindows*windowMS
}

func usedScheduleFraction(resetAtMS float64, windowMS float64, nowMS float64, workingDays map[int]bool) float64 {
	windowStartMS := resetAtMS - windowMS
	elapsedMS := clamp(nowMS-windowStartMS, 0, windowMS)
	if elapsedMS <= 0 {
		return 0
	}
	if len(workingDays) == 0 {
		return elapsedMS / windowMS
	}
	totalWorkingMS := workingDurationMS(windowStartMS, resetAtMS, workingDays)
	if totalWorkingMS <= 0 {
		return elapsedMS / windowMS
	}
	usedWorkingMS := workingDurationMS(windowStartMS, windowStartMS+elapsedMS, workingDays)
	return clamp(usedWorkingMS/totalWorkingMS, 0, 1)
}

func workingScheduleSharePerHour(resetAtMS float64, windowMS float64, workingDays map[int]bool) float64 {
	if len(workingDays) == 0 {
		return 3600000.0 / windowMS
	}
	windowStartMS := resetAtMS - windowMS
	totalWorkingMS := workingDurationMS(windowStartMS, resetAtMS, workingDays)
	if totalWorkingMS <= 0 {
		return 3600000.0 / windowMS
	}
	return 3600000.0 / totalWorkingMS
}

func workingDurationMS(startMS float64, endMS float64, workingDays map[int]bool) float64 {
	if endMS <= startMS {
		return 0
	}
	cursorMS := startMS
	totalMS := 0.0
	for cursorMS < endMS {
		nextDayMS := dayStartMS(cursorMS) + 86400000.0
		segmentEndMS := math.Min(endMS, nextDayMS)
		if workingDays[weekday(cursorMS)] {
			totalMS += segmentEndMS - cursorMS
		}
		cursorMS = segmentEndMS
	}
	return totalMS
}

func dayStartMS(epochMS float64) float64 {
	return math.Floor(epochMS/86400000.0) * 86400000.0
}

func weekday(epochMS float64) int {
	return int(time.UnixMilli(int64(epochMS)).UTC().Weekday()+6) % 7
}

func parseWeeklyPaceWorkingDays(value string) map[int]bool {
	result := map[int]bool{}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		day, err := strconv.Atoi(part)
		if err != nil || day < 0 || day > 6 {
			return map[int]bool{}
		}
		result[day] = true
	}
	if len(result) == 0 || len(result) == 7 {
		return map[int]bool{}
	}
	return result
}

func weeklyPaceStatus(deltaPercent float64, projectedShortfallCredits float64) string {
	if projectedShortfallCredits > 0 {
		return "danger"
	}
	if deltaPercent < -5 {
		return "behind"
	}
	if deltaPercent > 5 {
		return "ahead"
	}
	return "on_track"
}

func weeklyPaceConfidence(accountCount int, rateSampleCount int, staleAccountCount int) string {
	if rateSampleCount >= accountCount && staleAccountCount == 0 {
		return "high"
	}
	if rateSampleCount > 0 {
		return "medium"
	}
	return "low"
}

func freshnessSeconds(usageRefreshIntervalSeconds int) float64 {
	return math.Max(minFreshnessSeconds, float64(usageRefreshIntervalSeconds)*freshnessMissedRefreshes)
}

func clamp(value float64, minValue float64, maxValue float64) float64 {
	return math.Min(maxValue, math.Max(minValue, value))
}

func isFinitePositive(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
}
