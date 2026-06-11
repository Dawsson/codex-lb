package dashboard

import (
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"sync"
	"time"
)

const depletionAlpha = 0.4

type depletionResponse struct {
	Risk                   float64  `json:"risk"`
	RiskLevel              string   `json:"riskLevel"`
	BurnRate               float64  `json:"burnRate"`
	SafeUsagePercent       float64  `json:"safeUsagePercent"`
	ProjectedExhaustionAt  *string  `json:"projectedExhaustionAt"`
	SecondsUntilExhaustion *float64 `json:"secondsUntilExhaustion"`
}

type ewmaState struct {
	rate            *float64
	lastUsedPercent float64
	lastTimestamp   float64
	lastResetAt     *int64
}

type depletionHistorySignature struct {
	rowCount int
	first    string
	latest   string
	digest   uint64
}

type depletionCacheEntry struct {
	signature depletionHistorySignature
	state     ewmaState
}

var depletionStateCache = struct {
	sync.Mutex
	entries  map[string]depletionCacheEntry
	rebuilds int
}{entries: map[string]depletionCacheEntry{}}

func buildDepletionByWindow(rows []UsageHistoryRow, now time.Time) (*depletionResponse, *depletionResponse) {
	grouped := map[string][]UsageHistoryRow{}
	for _, row := range rows {
		window := row.Window
		if window == "" {
			window = "primary"
		}
		if window == "monthly" {
			window = "secondary"
		}
		if window == "primary" && row.WindowMinutes.Valid && row.WindowMinutes.Int64 >= 7*24*60 {
			window = "secondary"
		}
		key := window + "\x00" + row.AccountID
		grouped[key] = append(grouped[key], row)
	}
	var primary []*depletionResponse
	var secondary []*depletionResponse
	activeKeys := map[string]bool{}
	for key, history := range grouped {
		sort.SliceStable(history, func(i, j int) bool {
			return history[i].RecordedAt < history[j].RecordedAt
		})
		filtered := filterRowsToLatestWindow(history, now)
		if len(filtered) > 0 {
			activeKeys[key] = true
		}
		metric := computeDepletionForHistory(key, filtered, now)
		if metric == nil {
			continue
		}
		if stringsHasPrefixLocal(key, "primary\x00") {
			primary = append(primary, metric)
		} else if stringsHasPrefixLocal(key, "secondary\x00") {
			secondary = append(secondary, metric)
		}
	}
	pruneDepletionStateCache(activeKeys)
	return worstDepletion(primary), worstDepletion(secondary)
}

func filterRowsToLatestWindow(rows []UsageHistoryRow, now time.Time) []UsageHistoryRow {
	if len(rows) == 0 {
		return nil
	}
	latest := rows[len(rows)-1]
	windowMinutes := int64(0)
	if latest.WindowMinutes.Valid {
		windowMinutes = latest.WindowMinutes.Int64
	}
	if windowMinutes <= 0 {
		if latest.Window == "primary" || latest.Window == "" {
			windowMinutes = 300
		} else {
			windowMinutes = 10080
		}
	}
	cutoff := now.Add(-time.Duration(windowMinutes) * time.Minute)
	filtered := make([]UsageHistoryRow, 0, len(rows))
	for _, row := range rows {
		recorded, ok := parseSQLiteTime(row.RecordedAt)
		if !ok || recorded.Before(cutoff) {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}

func computeDepletionForHistory(cacheKey string, rows []UsageHistoryRow, now time.Time) *depletionResponse {
	if len(rows) < 2 {
		if len(rows) == 1 {
			seedDepletionState(cacheKey, rows[0])
		}
		return nil
	}
	signature := depletionSignature(rows)
	state := cachedDepletionState(cacheKey, signature)
	if state == nil {
		state = rebuildDepletionState(rows)
		if state != nil {
			incrementDepletionStateRebuilds()
			storeDepletionState(cacheKey, signature, *state)
		}
	}
	if state == nil || state.rate == nil {
		return nil
	}
	latest := rows[len(rows)-1]
	usedPercent := latest.UsedPercent
	secondsUntilReset := 0.0
	if latest.ResetAt.Valid {
		secondsUntilReset = math.Max(0, float64(latest.ResetAt.Int64-now.Unix()))
		if secondsUntilReset == 0 {
			deleteDepletionState(cacheKey)
			return nil
		}
	} else if latest.WindowMinutes.Valid {
		secondsUntilReset = float64(latest.WindowMinutes.Int64 * 60)
	}
	totalWindowSeconds := 0.0
	if latest.WindowMinutes.Valid {
		totalWindowSeconds = float64(latest.WindowMinutes.Int64 * 60)
	}
	secondsElapsed := math.Max(0, totalWindowSeconds-secondsUntilReset)
	risk := computeDepletionRisk(usedPercent, *state.rate, secondsUntilReset)
	burnRate := computeBurnRate(*state.rate, 100-usedPercent, secondsUntilReset)
	safeUsagePercent := computeSafeUsagePercent(secondsElapsed, totalWindowSeconds)
	var projectedAt *string
	var secondsUntilExhaustion *float64
	if *state.rate > 0 && secondsUntilReset > 0 {
		remaining := 100 - usedPercent
		seconds := remaining / *state.rate
		if seconds <= secondsUntilReset {
			value := seconds
			secondsUntilExhaustion = &value
			iso := now.Add(time.Duration(seconds * float64(time.Second))).UTC().Format(time.RFC3339)
			projectedAt = &iso
		}
	}
	return &depletionResponse{
		Risk:                   risk,
		RiskLevel:              classifyRisk(risk),
		BurnRate:               burnRate,
		SafeUsagePercent:       safeUsagePercent,
		ProjectedExhaustionAt:  projectedAt,
		SecondsUntilExhaustion: secondsUntilExhaustion,
	}
}

func rebuildDepletionState(rows []UsageHistoryRow) *ewmaState {
	var state *ewmaState
	for _, row := range rows {
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
	return state
}

func seedDepletionState(cacheKey string, row UsageHistoryRow) {
	recorded, ok := parseSQLiteTime(row.RecordedAt)
	if !ok {
		return
	}
	var resetAt *int64
	if row.ResetAt.Valid {
		resetAt = &row.ResetAt.Int64
	}
	state := ewmaUpdate(nil, row.UsedPercent, float64(recorded.Unix()), resetAt)
	storeDepletionState(cacheKey, depletionSignature([]UsageHistoryRow{row}), state)
}

func cachedDepletionState(cacheKey string, signature depletionHistorySignature) *ewmaState {
	depletionStateCache.Lock()
	defer depletionStateCache.Unlock()
	entry, ok := depletionStateCache.entries[cacheKey]
	if !ok || entry.signature != signature {
		return nil
	}
	state := entry.state
	return &state
}

func storeDepletionState(cacheKey string, signature depletionHistorySignature, state ewmaState) {
	depletionStateCache.Lock()
	depletionStateCache.entries[cacheKey] = depletionCacheEntry{signature: signature, state: state}
	depletionStateCache.Unlock()
}

func incrementDepletionStateRebuilds() {
	depletionStateCache.Lock()
	depletionStateCache.rebuilds++
	depletionStateCache.Unlock()
}

func deleteDepletionState(cacheKey string) {
	depletionStateCache.Lock()
	delete(depletionStateCache.entries, cacheKey)
	depletionStateCache.Unlock()
}

func pruneDepletionStateCache(activeKeys map[string]bool) {
	depletionStateCache.Lock()
	for key := range depletionStateCache.entries {
		if !activeKeys[key] {
			delete(depletionStateCache.entries, key)
		}
	}
	depletionStateCache.Unlock()
}

func depletionSignature(rows []UsageHistoryRow) depletionHistorySignature {
	if len(rows) == 0 {
		return depletionHistorySignature{}
	}
	hash := fnv.New64a()
	for _, row := range rows {
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%s\x00%.12f\x00%d\x00%t\x00%d\x00%t\n",
			row.AccountID,
			row.RecordedAt,
			row.Window,
			row.UsedPercent,
			row.ResetAt.Int64,
			row.ResetAt.Valid,
			row.WindowMinutes.Int64,
			row.WindowMinutes.Valid,
		)
	}
	return depletionHistorySignature{
		rowCount: len(rows),
		first:    depletionEdgeSignature(rows[0]),
		latest:   depletionEdgeSignature(rows[len(rows)-1]),
		digest:   hash.Sum64(),
	}
}

func depletionEdgeSignature(row UsageHistoryRow) string {
	return fmt.Sprintf("%s\x00%s\x00%.12f\x00%d\x00%t\x00%d\x00%t",
		row.RecordedAt,
		row.Window,
		row.UsedPercent,
		row.ResetAt.Int64,
		row.ResetAt.Valid,
		row.WindowMinutes.Int64,
		row.WindowMinutes.Valid,
	)
}

func ewmaUpdate(state *ewmaState, usedPercent float64, timestamp float64, resetAt *int64) ewmaState {
	if state == nil {
		return ewmaState{lastUsedPercent: usedPercent, lastTimestamp: timestamp, lastResetAt: resetAt}
	}
	dt := timestamp - state.lastTimestamp
	if dt == 0 {
		return *state
	}
	windowChanged := resetAt != nil && state.lastResetAt != nil && *resetAt != *state.lastResetAt
	drop := state.lastUsedPercent - usedPercent
	if drop > 0 || windowChanged {
		return ewmaState{lastUsedPercent: usedPercent, lastTimestamp: timestamp, lastResetAt: resetAt}
	}
	rawRate := math.Max((usedPercent-state.lastUsedPercent)/dt, 0)
	rate := rawRate
	if state.rate != nil {
		rate = depletionAlpha*rawRate + (1-depletionAlpha)*(*state.rate)
	}
	return ewmaState{rate: &rate, lastUsedPercent: usedPercent, lastTimestamp: timestamp, lastResetAt: resetAt}
}

func computeBurnRate(currentRate float64, remainingPercent float64, secondsUntilReset float64) float64 {
	if currentRate == 0 || secondsUntilReset == 0 {
		return 0
	}
	sustainableRate := remainingPercent / secondsUntilReset
	if sustainableRate == 0 {
		return 0
	}
	return currentRate / sustainableRate
}

func computeDepletionRisk(usedPercent float64, ratePerSecond float64, secondsUntilReset float64) float64 {
	projected := usedPercent + math.Max(0, ratePerSecond)*secondsUntilReset
	return math.Min(projected/100, 1)
}

func computeSafeUsagePercent(secondsElapsed float64, totalWindowSeconds float64) float64 {
	if totalWindowSeconds == 0 {
		return 0
	}
	progress := secondsElapsed / totalWindowSeconds
	return math.Min(math.Max(progress, 0), 1) * 100
}

func classifyRisk(risk float64) string {
	if risk >= 0.95 {
		return "critical"
	}
	if risk >= 0.80 {
		return "danger"
	}
	if risk >= 0.60 {
		return "warning"
	}
	return "safe"
}

func worstDepletion(values []*depletionResponse) *depletionResponse {
	if len(values) == 0 {
		return nil
	}
	worst := values[0]
	for _, value := range values[1:] {
		if value.Risk > worst.Risk {
			worst = value
		}
	}
	return worst
}

func parseSQLiteTime(value string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func stringsHasPrefixLocal(value string, prefix string) bool {
	if len(prefix) > len(value) {
		return false
	}
	return value[:len(prefix)] == prefix
}
