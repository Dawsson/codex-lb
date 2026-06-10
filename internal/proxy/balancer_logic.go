package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"
)

// This file ports the pure account-selection algorithm from
// app/core/balancer/logic.py (select_account and its strategy helpers).
//
// Simplification: the relative_availability and capacity_weighted strategies
// use weighted random selection (random.choices in Python) when
// deterministicProbe is false. math/rand's global source is used directly,
// matching the non-deterministic intent of the Python implementation.
//
// Simplification: the structured logging calls in the Python implementation
// (logger.debug/info for relative-availability candidate scoring) are not
// ported; they are operational diagnostics with no behavioral effect on
// selection.

const (
	secondsPerDay  = 60 * 60 * 24
	secondsPerHour = 60 * 60
	secondsPerWeek = 7 * secondsPerDay

	unknownResetBucketDays                = 10_000
	unknownResetFallbackSeconds           = 7 * secondsPerDay
	relativeAvailabilityMinDivisorSeconds = 5 * 60
	relativeAvailabilityMinWeightFraction = 0.1

	preserveMinWeeklyFloorPct          = 5.0
	preserveMinShortWindowFloorPct     = 10.0
	normalLastAccountEmergencyFloorPct = 5.0
	recentForegroundActivitySeconds    = 30 * 60
)

// unknownPlanFallback and capacityPlanAliases mirror UNKNOWN_PLAN_FALLBACK and
// CAPACITY_PLAN_ALIASES.
const unknownPlanFallback = "free"

var capacityPlanAliases = map[string]string{
	"education":      "edu",
	"k12":            "edu",
	"guest":          "free",
	"go":             "free",
	"free_workspace": "free",
	"quorum":         "free",
	"unknown":        "free",
}

// planCapacityCreditsSecondary mirrors PLAN_CAPACITY_CREDITS_SECONDARY for use
// by the fallback-capacity helper below.
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

func fallbackSecondaryCapacityCredits(planType string) float64 {
	normalized := lowerTrimLocal(planType)
	resolvedPlan := normalized
	if alias, ok := capacityPlanAliases[normalized]; ok {
		resolvedPlan = alias
	} else if normalized == "" {
		resolvedPlan = unknownPlanFallback
	}
	if value, ok := planCapacityCreditsSecondary[resolvedPlan]; ok {
		return value
	}
	return planCapacityCreditsSecondary[unknownPlanFallback]
}

func lowerTrimLocal(value string) string {
	out := make([]byte, 0, len(value))
	start, end := 0, len(value)
	for start < end && (value[start] == ' ' || value[start] == '\t' || value[start] == '\n') {
		start++
	}
	for end > start && (value[end-1] == ' ' || value[end-1] == '\t' || value[end-1] == '\n') {
		end--
	}
	for i := start; i < end; i++ {
		c := value[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out = append(out, c)
	}
	return string(out)
}

// SelectAccountOptions holds the keyword arguments accepted by
// app.core.balancer.logic.select_account.
type SelectAccountOptions struct {
	Now                           *float64
	PreferEarlierReset            bool
	PreferEarlierResetWindow      ResetPreferenceWindow
	RoutingStrategy               RoutingStrategy
	AllowBackoffFallback          bool
	DeterministicProbe            bool
	RelativeAvailabilityPower     float64
	RelativeAvailabilityTopK      int
	UsageWeightedOrder            UsageWeightedOrder
	TrafficClass                  string
	IgnoreStandardQuota           bool
	BypassQuotaExceeded           bool
	BypassQuotaExceededAccountIDs map[string]struct{}
	PrimaryFirstUsageWeighted     bool
	RoutingCosts                  RoutingCostsByAccount
}

// DefaultSelectAccountOptions returns the default option set, mirroring the
// keyword-argument defaults of select_account.
func DefaultSelectAccountOptions() SelectAccountOptions {
	return SelectAccountOptions{
		PreferEarlierResetWindow:  ResetPreferenceWindowSecondary,
		RoutingStrategy:           RoutingStrategyCapacityWeighted,
		AllowBackoffFallback:      true,
		RelativeAvailabilityPower: DefaultRelativeAvailabilityPower,
		RelativeAvailabilityTopK:  DefaultRelativeAvailabilityTopK,
		UsageWeightedOrder:        UsageWeightedOrderSecondaryFirst,
		TrafficClass:              TrafficClassForeground,
	}
}

// SelectAccount ports app.core.balancer.logic.select_account.
//
// states are mutated in place for state recovery transitions (rate-limited /
// quota-exceeded accounts whose reset_at has passed are flipped back to
// active), exactly as the Python implementation mutates AccountState objects
// in place.
func SelectAccount(states []*AccountState, opts SelectAccountOptions) SelectionResult {
	current := time.Now().UnixNano()
	var currentSeconds float64
	if opts.Now != nil {
		currentSeconds = *opts.Now
	} else {
		currentSeconds = float64(current) / float64(time.Second)
	}

	if opts.RoutingStrategy == "" {
		opts.RoutingStrategy = RoutingStrategyCapacityWeighted
	}
	if opts.PreferEarlierResetWindow == "" {
		opts.PreferEarlierResetWindow = ResetPreferenceWindowSecondary
	}
	if opts.RelativeAvailabilityPower == 0 {
		opts.RelativeAvailabilityPower = DefaultRelativeAvailabilityPower
	}
	if opts.RelativeAvailabilityTopK == 0 {
		opts.RelativeAvailabilityTopK = DefaultRelativeAvailabilityTopK
	}
	if opts.UsageWeightedOrder == "" {
		opts.UsageWeightedOrder = UsageWeightedOrderSecondaryFirst
	}
	if opts.TrafficClass == "" {
		opts.TrafficClass = TrafficClassForeground
	}

	var available []*AccountState
	var inErrorBackoff []*AccountState
	allStates := states

	for _, state := range allStates {
		bypassStandardQuota := opts.IgnoreStandardQuota ||
			state.IgnoreStandardQuota ||
			opts.BypassQuotaExceeded
		if !bypassStandardQuota && opts.BypassQuotaExceededAccountIDs != nil {
			if _, ok := opts.BypassQuotaExceededAccountIDs[state.AccountID]; ok {
				bypassStandardQuota = true
			}
		}

		if state.Status == AccountStatusReauthRequired || state.Status == AccountStatusDeactivated {
			continue
		}
		if state.Status == AccountStatusPaused {
			continue
		}
		if state.Status == AccountStatusRateLimited {
			if state.ResetAt != nil && currentSeconds >= *state.ResetAt {
				state.Status = AccountStatusActive
				zero := 0.0
				state.UsedPercent = &zero
				state.ErrorCount = 0
				state.ResetAt = nil
			} else if !bypassStandardQuota {
				continue
			}
		}
		if state.Status == AccountStatusQuotaExceeded {
			if state.ResetAt != nil && currentSeconds >= *state.ResetAt {
				state.Status = AccountStatusActive
				zero := 0.0
				state.UsedPercent = &zero
				state.SecondaryUsedPercent = &zero
				state.ResetAt = nil
			} else if !bypassStandardQuota {
				continue
			}
		}
		if state.CooldownUntil != nil && currentSeconds >= *state.CooldownUntil {
			state.CooldownUntil = nil
			state.LastErrorAt = nil
			state.ErrorCount = 0
		}
		if state.CooldownUntil != nil && currentSeconds < *state.CooldownUntil {
			continue
		}
		if state.ErrorCount >= 3 {
			backoff := errorBackoffSeconds(state.ErrorCount)
			if state.LastErrorAt != nil && currentSeconds-*state.LastErrorAt < backoff {
				inErrorBackoff = append(inErrorBackoff, state)
				continue
			}
			state.ErrorCount = 0
			state.LastErrorAt = nil
		}
		available = append(available, state)
	}

	if opts.TrafficClass == TrafficClassOpportunistic && len(available) > 0 {
		opportunisticAvailable, reason := filterOpportunisticCandidates(available, currentSeconds)
		if len(opportunisticAvailable) == 0 {
			return SelectionResult{ErrorMessage: fmt.Sprintf("opportunistic burn window closed: %s", reason)}
		}
		available = opportunisticAvailable
	}

	if len(available) == 0 {
		inErrorBackoffIDs := map[string]struct{}{}
		for _, state := range inErrorBackoff {
			inErrorBackoffIDs[state.AccountID] = struct{}{}
		}
		hardBlockedExists := false
		for _, state := range allStates {
			switch state.Status {
			case AccountStatusPaused, AccountStatusReauthRequired, AccountStatusDeactivated, AccountStatusRateLimited, AccountStatusQuotaExceeded:
				if _, ok := inErrorBackoffIDs[state.AccountID]; !ok {
					hardBlockedExists = true
				}
			}
			if hardBlockedExists {
				break
			}
		}

		if opts.AllowBackoffFallback && (len(inErrorBackoff) > 1 || (len(inErrorBackoff) > 0 && hardBlockedExists)) {
			best := inErrorBackoff[0]
			bestExpiry := backoffExpiresAt(best)
			for _, state := range inErrorBackoff[1:] {
				if expiry := backoffExpiresAt(state); expiry < bestExpiry {
					best = state
					bestExpiry = expiry
				}
			}
			available = append(available, best)
			if opts.TrafficClass == TrafficClassOpportunistic {
				opportunisticAvailable, reason := filterOpportunisticCandidates(available, currentSeconds)
				if len(opportunisticAvailable) == 0 {
					return SelectionResult{ErrorMessage: fmt.Sprintf("opportunistic burn window closed: %s", reason)}
				}
				available = opportunisticAvailable
			}
		} else {
			var reauthRequired, deactivated, paused, rateLimited, quotaExceeded []*AccountState
			for _, state := range allStates {
				switch state.Status {
				case AccountStatusReauthRequired:
					reauthRequired = append(reauthRequired, state)
				case AccountStatusDeactivated:
					deactivated = append(deactivated, state)
				case AccountStatusPaused:
					paused = append(paused, state)
				case AccountStatusRateLimited:
					rateLimited = append(rateLimited, state)
				case AccountStatusQuotaExceeded:
					quotaExceeded = append(quotaExceeded, state)
				}
			}

			if len(rateLimited) == 0 && len(quotaExceeded) == 0 {
				switch {
				case len(paused) > 0 && len(reauthRequired) > 0 && len(deactivated) > 0:
					return SelectionResult{ErrorMessage: "All accounts are paused, deactivated, or require re-authentication"}
				case len(paused) > 0 && len(reauthRequired) > 0:
					return SelectionResult{ErrorMessage: "All accounts are paused or require re-authentication"}
				case len(paused) > 0 && len(deactivated) > 0:
					return SelectionResult{ErrorMessage: "All accounts are paused or deactivated"}
				case len(reauthRequired) > 0 && len(deactivated) > 0:
					return SelectionResult{ErrorMessage: "All accounts are deactivated or require re-authentication"}
				case len(paused) > 0:
					return SelectionResult{ErrorMessage: "All accounts are paused"}
				case len(reauthRequired) > 0:
					return SelectionResult{ErrorMessage: "All accounts require re-authentication"}
				case len(deactivated) > 0:
					return SelectionResult{ErrorMessage: "All accounts are deactivated"}
				}
			}
			if len(quotaExceeded) > 0 {
				var resetCandidates []float64
				for _, s := range quotaExceeded {
					if s.ResetAt != nil {
						resetCandidates = append(resetCandidates, *s.ResetAt)
					}
				}
				if len(resetCandidates) > 0 {
					minReset := resetCandidates[0]
					for _, r := range resetCandidates[1:] {
						if r < minReset {
							minReset = r
						}
					}
					waitSeconds := math.Max(0, minReset-math.Floor(currentSeconds))
					return SelectionResult{ErrorMessage: formatRetryHint(waitSeconds)}
				}
			}
			var cooldowns []float64
			for _, s := range allStates {
				if s.CooldownUntil != nil && *s.CooldownUntil > currentSeconds {
					cooldowns = append(cooldowns, *s.CooldownUntil)
				}
			}
			if len(cooldowns) > 0 {
				minCooldown := cooldowns[0]
				for _, c := range cooldowns[1:] {
					if c < minCooldown {
						minCooldown = c
					}
				}
				waitSeconds := math.Max(0, minCooldown-currentSeconds)
				return SelectionResult{ErrorMessage: formatRetryHint(waitSeconds)}
			}
			return SelectionResult{ErrorMessage: "No available accounts"}
		}
	}

	switch opts.RoutingStrategy {
	case RoutingStrategySingleAccount:
		candidatePool := filterNotExhausted(available)
		if len(candidatePool) == 0 {
			return SelectionResult{ErrorMessage: "Selected account is exhausted or unavailable"}
		}
		selected := candidatePool[0]
		for _, s := range candidatePool[1:] {
			if s.AccountID < selected.AccountID {
				selected = s
			}
		}
		return SelectionResult{Account: selected}

	case RoutingStrategySequentialDrain:
		candidatePool := filterNotExhausted(available)
		if len(candidatePool) == 0 {
			return SelectionResult{ErrorMessage: "No available accounts"}
		}
		selected := minBy(candidatePool, sequentialDrainSortKey)
		return SelectionResult{Account: selected}

	case RoutingStrategyResetDrain:
		candidatePool := filterNotExhausted(available)
		if len(candidatePool) == 0 {
			return SelectionResult{ErrorMessage: "No available accounts"}
		}
		selected := minBy(candidatePool, func(s *AccountState) resetDrainKey {
			return resetDrainSortKey(s, currentSeconds)
		})
		return SelectionResult{Account: selected}
	}

	var healthy, probing, draining []*AccountState
	for _, s := range available {
		switch s.HealthTier {
		case HealthTierHealthy:
			healthy = append(healthy, s)
		case HealthTierProbing:
			probing = append(probing, s)
		case HealthTierDraining:
			draining = append(draining, s)
		}
	}
	healthPool := available
	if len(healthy) > 0 {
		healthPool = healthy
	} else if len(probing) > 0 {
		healthPool = probing
	} else if len(draining) > 0 {
		healthPool = draining
	}

	var burnFirst, normal, preserve []*AccountState
	for _, s := range healthPool {
		switch routingPolicyOf(s) {
		case RoutingPolicyBurnFirst:
			burnFirst = append(burnFirst, s)
		case RoutingPolicyNormal:
			normal = append(normal, s)
		case RoutingPolicyPreserve:
			preserve = append(preserve, s)
		}
	}
	effectivePool := healthPool
	if len(burnFirst) > 0 {
		effectivePool = burnFirst
	} else if len(normal) > 0 {
		effectivePool = normal
	} else if len(preserve) > 0 {
		effectivePool = preserve
	}

	effectivePreferEarlierReset := opts.PreferEarlierReset && opts.RoutingStrategy != RoutingStrategyRelativeAvailability

	var selected *AccountState
	switch opts.RoutingStrategy {
	case RoutingStrategyRoundRobin:
		selected = minBy(effectivePool, func(s *AccountState) roundRobinKey {
			return roundRobinSortKey(s, opts.RoutingCosts)
		})
	case RoutingStrategyCapacityWeighted:
		candidatePool := effectivePool
		if effectivePreferEarlierReset {
			candidatePool = preferEarlierResetCandidates(effectivePool, currentSeconds, opts.PreferEarlierResetWindow)
		}
		if opts.DeterministicProbe {
			selected = minBy(candidatePool, func(s *AccountState) capacityProbeWithCostKey {
				return capacityProbeSortKeyWithCost(s, opts.RoutingCosts)
			})
		} else {
			candidatePool = lowestPlannerCostCandidates(candidatePool, opts.RoutingCosts)
			selected = selectCapacityWeighted(candidatePool)
		}
	case RoutingStrategyRelativeAvailability:
		candidatePool := lowestPlannerCostCandidates(effectivePool, opts.RoutingCosts)
		selected = selectRelativeAvailability(candidatePool, currentSeconds, opts.RelativeAvailabilityPower, opts.RelativeAvailabilityTopK, opts.DeterministicProbe)
	case RoutingStrategyFillFirst:
		candidatePool := effectivePool
		if opts.PreferEarlierReset {
			candidatePool = preferEarlierResetCandidates(effectivePool, currentSeconds, opts.PreferEarlierResetWindow)
		}
		selected = selectFillFirst(candidatePool)
	default:
		effectiveUsageWeightedOrder := opts.UsageWeightedOrder
		if opts.PrimaryFirstUsageWeighted {
			effectiveUsageWeightedOrder = UsageWeightedOrderPrimaryFirst
		}
		if effectiveUsageWeightedOrder == UsageWeightedOrderPrimaryFirst {
			if effectivePreferEarlierReset {
				selected = minBy(effectivePool, func(s *AccountState) primaryResetFirstKey {
					return primaryResetFirstSortKey(s, currentSeconds, opts.PreferEarlierResetWindow, opts.RoutingCosts)
				})
			} else {
				selected = minBy(effectivePool, func(s *AccountState) primaryUsageWithCostKey {
					return primaryUsageSortKeyWithCost(s, opts.RoutingCosts)
				})
			}
		} else {
			if effectivePreferEarlierReset {
				selected = minBy(effectivePool, func(s *AccountState) resetFirstKey {
					return resetFirstSortKey(s, currentSeconds, opts.PreferEarlierResetWindow, opts.RoutingCosts)
				})
			} else {
				selected = minBy(effectivePool, func(s *AccountState) usageWithCostKey {
					return usageSortKeyWithCost(s, opts.RoutingCosts)
				})
			}
		}
	}
	return SelectionResult{Account: selected}
}

// errorBackoffSeconds mirrors the inline `min(300, 30 * (2 ** (error_count - 3)))`
// backoff formula used in select_account.
func errorBackoffSeconds(errorCount int) float64 {
	return math.Min(300, 30*math.Pow(2, float64(errorCount-3)))
}

func backoffExpiresAt(s *AccountState) float64 {
	backoff := errorBackoffSeconds(s.ErrorCount)
	lastErrorAt := 0.0
	if s.LastErrorAt != nil {
		lastErrorAt = *s.LastErrorAt
	}
	return lastErrorAt + backoff
}

func formatRetryHint(waitSeconds float64) string {
	capped := math.Min(math.Max(0.0, waitSeconds), float64(SelectorRetryHintMaxSeconds))
	return fmt.Sprintf("Rate limit exceeded. Try again in %.0fs", capped)
}

func filterNotExhausted(states []*AccountState) []*AccountState {
	result := make([]*AccountState, 0, len(states))
	for _, s := range states {
		if !usageExhausted(s) {
			result = append(result, s)
		}
	}
	return result
}

// usageExhausted ports _usage_exhausted.
func usageExhausted(state *AccountState) bool {
	primaryUsed := 0.0
	if state.UsedPercent != nil {
		primaryUsed = *state.UsedPercent
	}
	secondaryUsed := primaryUsed
	if state.SecondaryUsedPercent != nil {
		secondaryUsed = *state.SecondaryUsedPercent
	}
	return primaryUsed >= 100.0 || secondaryUsed >= 100.0
}

// routingPolicyOf ports _routing_policy.
func routingPolicyOf(state *AccountState) string {
	switch state.RoutingPolicy {
	case RoutingPolicyBurnFirst, RoutingPolicyNormal, RoutingPolicyPreserve:
		return state.RoutingPolicy
	}
	return RoutingPolicyNormal
}

// --- usage / sort-key helpers -----------------------------------------------

// priorityPrimaryUsed ports _priority_primary_used.
func priorityPrimaryUsed(state *AccountState) float64 {
	if state.PriorityUsedPercent != nil {
		return *state.PriorityUsedPercent
	}
	if state.UsedPercent != nil {
		return *state.UsedPercent
	}
	return 0.0
}

// prioritySecondaryUsed ports _priority_secondary_used. primaryUsed may be nil
// to signal "compute it lazily", matching the Python default of None.
func prioritySecondaryUsed(state *AccountState, primaryUsed *float64) float64 {
	if state.LimitScopedUsage && state.PrioritySecondaryUsedPercent == nil {
		if primaryUsed != nil {
			return *primaryUsed
		}
		return priorityPrimaryUsed(state)
	}
	if state.PrioritySecondaryUsedPercent != nil {
		return *state.PrioritySecondaryUsedPercent
	}
	if state.SecondaryUsedPercent != nil {
		return *state.SecondaryUsedPercent
	}
	if primaryUsed != nil {
		return *primaryUsed
	}
	return priorityPrimaryUsed(state)
}

func lastSelectedAt(state *AccountState) float64 {
	if state.LastSelectedAt != nil {
		return *state.LastSelectedAt
	}
	return 0.0
}

// plannerCost ports _planner_cost.
func plannerCost(state *AccountState, routingCosts RoutingCostsByAccount) float64 {
	if len(routingCosts) == 0 {
		return 0.0
	}
	cost, ok := routingCosts[state.AccountID]
	if !ok {
		return 0.0
	}
	return cost.Total
}

// usageSortKey ports _usage_sort_key: returns (secondaryUsed, primaryUsed, lastSelected, accountID).
func usageSortKey(state *AccountState) (float64, float64, float64, string) {
	primaryUsed := priorityPrimaryUsed(state)
	pu := primaryUsed
	secondaryUsed := prioritySecondaryUsed(state, &pu)
	return secondaryUsed, primaryUsed, lastSelectedAt(state), state.AccountID
}

// primaryUsageSortKey ports _primary_usage_sort_key: returns (primaryUsed, secondaryUsed, lastSelected, accountID).
func primaryUsageSortKey(state *AccountState) (float64, float64, float64, string) {
	primaryUsed := priorityPrimaryUsed(state)
	pu := primaryUsed
	secondaryUsed := prioritySecondaryUsed(state, &pu)
	return primaryUsed, secondaryUsed, lastSelectedAt(state), state.AccountID
}

// usedPct ports _used_pct.
func usedPct(state *AccountState, secondary bool) *float64 {
	if secondary {
		if state.SecondaryUsedPercent != nil {
			return state.SecondaryUsedPercent
		}
		return state.UsedPercent
	}
	return state.UsedPercent
}

// remainingPct ports _remaining_pct.
func remainingPct(state *AccountState, secondary bool) *float64 {
	used := usedPct(state, secondary)
	if used == nil {
		return nil
	}
	value := math.Max(0.0, 100.0-math.Min(100.0, *used))
	return &value
}

// seconds_until ports _seconds_until.
func secondsUntil(resetAt *int64, current float64) *float64 {
	if resetAt == nil {
		return nil
	}
	value := math.Max(0.0, float64(*resetAt)-current)
	return &value
}

func secondsUntilFloat(resetAt *float64, current float64) *float64 {
	if resetAt == nil {
		return nil
	}
	value := math.Max(0.0, *resetAt-current)
	return &value
}

// recentForegroundActivity ports _recent_foreground_activity.
func recentForegroundActivity(state *AccountState, current float64) bool {
	return state.LastSelectedAt != nil && current-*state.LastSelectedAt <= recentForegroundActivitySeconds
}

// reset_preference_bucket ports _reset_preference_bucket.
func resetPreferenceBucket(state *AccountState, current float64, window ResetPreferenceWindow) int64 {
	var resetAt *float64
	if window == ResetPreferenceWindowPrimary {
		if state.PrimaryResetAt != nil {
			v := float64(*state.PrimaryResetAt)
			resetAt = &v
		} else if state.PriorityResetAt != nil {
			v := float64(*state.PriorityResetAt)
			resetAt = &v
		} else if state.SecondaryResetAt != nil {
			v := float64(*state.SecondaryResetAt)
			resetAt = &v
		}
	} else {
		if state.PriorityResetAt != nil {
			v := float64(*state.PriorityResetAt)
			resetAt = &v
		} else if state.SecondaryResetAt != nil {
			v := float64(*state.SecondaryResetAt)
			resetAt = &v
		}
		if resetAt == nil && state.PrimaryResetAt != nil {
			v := float64(*state.PrimaryResetAt)
			resetAt = &v
		}
	}
	if resetAt == nil {
		return int64(unknownResetBucketDays) * secondsPerDay
	}
	remainingSeconds := int64(*resetAt - current)
	if remainingSeconds < 0 {
		remainingSeconds = 0
	}
	if window == ResetPreferenceWindowSecondary {
		return remainingSeconds / secondsPerDay
	}
	return remainingSeconds
}

func preferEarlierResetCandidates(available []*AccountState, current float64, window ResetPreferenceWindow) []*AccountState {
	earliest := resetPreferenceBucket(available[0], current, window)
	for _, s := range available[1:] {
		if b := resetPreferenceBucket(s, current, window); b < earliest {
			earliest = b
		}
	}
	result := make([]*AccountState, 0, len(available))
	for _, s := range available {
		if resetPreferenceBucket(s, current, window) == earliest {
			result = append(result, s)
		}
	}
	return result
}

// --- generic min-by helper ---------------------------------------------------

func minBy[K interface{ less(K) bool }](states []*AccountState, key func(*AccountState) K) *AccountState {
	best := states[0]
	bestKey := key(best)
	for _, s := range states[1:] {
		k := key(s)
		if k.less(bestKey) {
			best = s
			bestKey = k
		}
	}
	return best
}

// --- sort key types -----------------------------------------------------------

type usageWithCostKey struct {
	cost, secondaryUsed, primaryUsed, lastSelected float64
	accountID                                      string
}

func usageSortKeyWithCost(state *AccountState, routingCosts RoutingCostsByAccount) usageWithCostKey {
	secondaryUsed, primaryUsed, lastSelected, accountID := usageSortKey(state)
	return usageWithCostKey{plannerCost(state, routingCosts), secondaryUsed, primaryUsed, lastSelected, accountID}
}

func (a usageWithCostKey) less(b usageWithCostKey) bool {
	return cmp4(a.cost, b.cost, a.secondaryUsed, b.secondaryUsed, a.primaryUsed, b.primaryUsed, a.lastSelected, b.lastSelected, a.accountID, b.accountID)
}

type primaryUsageWithCostKey struct {
	cost, primaryUsed, secondaryUsed, lastSelected float64
	accountID                                      string
}

func primaryUsageSortKeyWithCost(state *AccountState, routingCosts RoutingCostsByAccount) primaryUsageWithCostKey {
	primaryUsed, secondaryUsed, lastSelected, accountID := primaryUsageSortKey(state)
	return primaryUsageWithCostKey{plannerCost(state, routingCosts), primaryUsed, secondaryUsed, lastSelected, accountID}
}

func (a primaryUsageWithCostKey) less(b primaryUsageWithCostKey) bool {
	return cmp4(a.cost, b.cost, a.primaryUsed, b.primaryUsed, a.secondaryUsed, b.secondaryUsed, a.lastSelected, b.lastSelected, a.accountID, b.accountID)
}

type resetFirstKey struct {
	resetBucket                                    int64
	cost, secondaryUsed, primaryUsed, lastSelected float64
	accountID                                      string
}

func resetFirstSortKey(state *AccountState, current float64, window ResetPreferenceWindow, routingCosts RoutingCostsByAccount) resetFirstKey {
	bucket := resetPreferenceBucket(state, current, window)
	secondaryUsed, primaryUsed, lastSelected, accountID := usageSortKey(state)
	return resetFirstKey{bucket, plannerCost(state, routingCosts), secondaryUsed, primaryUsed, lastSelected, accountID}
}

func (a resetFirstKey) less(b resetFirstKey) bool {
	if a.resetBucket != b.resetBucket {
		return a.resetBucket < b.resetBucket
	}
	return cmp4(a.cost, b.cost, a.secondaryUsed, b.secondaryUsed, a.primaryUsed, b.primaryUsed, a.lastSelected, b.lastSelected, a.accountID, b.accountID)
}

type primaryResetFirstKey struct {
	resetBucket                                    int64
	cost, primaryUsed, secondaryUsed, lastSelected float64
	accountID                                      string
}

func primaryResetFirstSortKey(state *AccountState, current float64, window ResetPreferenceWindow, routingCosts RoutingCostsByAccount) primaryResetFirstKey {
	bucket := resetPreferenceBucket(state, current, window)
	primaryUsed, secondaryUsed, lastSelected, accountID := primaryUsageSortKey(state)
	return primaryResetFirstKey{bucket, plannerCost(state, routingCosts), primaryUsed, secondaryUsed, lastSelected, accountID}
}

func (a primaryResetFirstKey) less(b primaryResetFirstKey) bool {
	if a.resetBucket != b.resetBucket {
		return a.resetBucket < b.resetBucket
	}
	return cmp4(a.cost, b.cost, a.primaryUsed, b.primaryUsed, a.secondaryUsed, b.secondaryUsed, a.lastSelected, b.lastSelected, a.accountID, b.accountID)
}

type roundRobinKey struct {
	cost, lastSelected float64
	accountID          string
}

func roundRobinSortKey(state *AccountState, routingCosts RoutingCostsByAccount) roundRobinKey {
	return roundRobinKey{plannerCost(state, routingCosts), lastSelectedAt(state), state.AccountID}
}

func (a roundRobinKey) less(b roundRobinKey) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	if a.lastSelected != b.lastSelected {
		return a.lastSelected < b.lastSelected
	}
	return a.accountID < b.accountID
}

type capacityProbeWithCostKey struct {
	cost, negRemainingCredits, secondaryUsed, primaryUsed, lastSelected float64
	accountID                                                           string
}

func capacityProbeSortKeyWithCost(state *AccountState, routingCosts RoutingCostsByAccount) capacityProbeWithCostKey {
	secondaryUsed, primaryUsed, lastSelected, accountID := usageSortKey(state)
	return capacityProbeWithCostKey{
		plannerCost(state, routingCosts),
		-remainingSecondaryCredits(state),
		secondaryUsed, primaryUsed, lastSelected, accountID,
	}
}

func (a capacityProbeWithCostKey) less(b capacityProbeWithCostKey) bool {
	return cmp5(a.cost, b.cost, a.negRemainingCredits, b.negRemainingCredits, a.secondaryUsed, b.secondaryUsed, a.primaryUsed, b.primaryUsed, a.lastSelected, b.lastSelected, a.accountID, b.accountID)
}

type resetDrainKey struct {
	resetBucket                                int64
	negPrimaryRemaining, negSecondaryRemaining float64
	resetAt                                    float64
	tieBreaker, accountID                      string
}

func resetDrainSortKey(state *AccountState, current float64) resetDrainKey {
	primaryUsed := 0.0
	if state.UsedPercent != nil {
		primaryUsed = *state.UsedPercent
	}
	primaryRemaining := math.Max(0.0, 100.0-primaryUsed)

	var secondaryUsed float64
	if state.SecondaryUsedPercent != nil {
		secondaryUsed = *state.SecondaryUsedPercent
	} else if state.UsedPercent != nil {
		secondaryUsed = *state.UsedPercent
	}
	secondaryRemaining := math.Max(0.0, 100.0-secondaryUsed)

	resetAt := weeklyResetTimestamp(state, current)
	var resetBucket int64
	if math.IsInf(resetAt, 1) {
		resetBucket = unknownResetBucketDays
	} else {
		bucket := int64((resetAt - current) / secondsPerDay)
		if bucket < 0 {
			bucket = 0
		}
		resetBucket = bucket
	}
	return resetDrainKey{resetBucket, -primaryRemaining, -secondaryRemaining, resetAt, stableTieBreaker(state.AccountID), state.AccountID}
}

func (a resetDrainKey) less(b resetDrainKey) bool {
	if a.resetBucket != b.resetBucket {
		return a.resetBucket < b.resetBucket
	}
	if a.negPrimaryRemaining != b.negPrimaryRemaining {
		return a.negPrimaryRemaining < b.negPrimaryRemaining
	}
	if a.negSecondaryRemaining != b.negSecondaryRemaining {
		return a.negSecondaryRemaining < b.negSecondaryRemaining
	}
	if a.resetAt != b.resetAt {
		return a.resetAt < b.resetAt
	}
	if a.tieBreaker != b.tieBreaker {
		return a.tieBreaker < b.tieBreaker
	}
	return a.accountID < b.accountID
}

// weeklyResetTimestamp ports _weekly_reset_timestamp.
func weeklyResetTimestamp(state *AccountState, current float64) float64 {
	if state.SecondaryResetAt != nil && float64(*state.SecondaryResetAt) > current {
		return float64(*state.SecondaryResetAt)
	}
	if state.ResetAt != nil && *state.ResetAt > current {
		return *state.ResetAt
	}
	return math.Inf(1)
}

// stableTieBreaker ports _stable_tie_breaker.
func stableTieBreaker(accountID string) string {
	sum := sha256.Sum256([]byte(accountID))
	return hex.EncodeToString(sum[:])
}

type sequentialDrainKey struct {
	capacityCredits float64
	tieBreaker      string
	accountID       string
}

func sequentialDrainSortKey(state *AccountState) sequentialDrainKey {
	return sequentialDrainKey{configuredCapacityCredits(state), stableTieBreaker(state.AccountID), state.AccountID}
}

func (a sequentialDrainKey) less(b sequentialDrainKey) bool {
	if a.capacityCredits != b.capacityCredits {
		return a.capacityCredits < b.capacityCredits
	}
	if a.tieBreaker != b.tieBreaker {
		return a.tieBreaker < b.tieBreaker
	}
	return a.accountID < b.accountID
}

// configuredCapacityCredits ports _configured_capacity_credits.
func configuredCapacityCredits(state *AccountState) float64 {
	if state.CapacityCredits != nil && *state.CapacityCredits > 0 {
		return *state.CapacityCredits
	}
	return fallbackSecondaryCapacityCredits(state.PlanType)
}

// --- comparison helpers -------------------------------------------------------

func cmp4(a1, b1, a2, b2, a3, b3, a4, b4 float64, a5, b5 string) bool {
	if a1 != b1 {
		return a1 < b1
	}
	if a2 != b2 {
		return a2 < b2
	}
	if a3 != b3 {
		return a3 < b3
	}
	if a4 != b4 {
		return a4 < b4
	}
	return a5 < b5
}

func cmp5(a1, b1, a2, b2, a3, b3, a4, b4, a5, b5 float64, a6, b6 string) bool {
	if a1 != b1 {
		return a1 < b1
	}
	if a2 != b2 {
		return a2 < b2
	}
	if a3 != b3 {
		return a3 < b3
	}
	if a4 != b4 {
		return a4 < b4
	}
	if a5 != b5 {
		return a5 < b5
	}
	return a6 < b6
}

// --- capacity-weighted / relative-availability / fill-first ------------------

// remainingSecondaryCredits ports _remaining_secondary_credits.
func remainingSecondaryCredits(state *AccountState) float64 {
	var capacity float64
	if state.PriorityCapacityCredits != nil {
		capacity = *state.PriorityCapacityCredits
	} else if state.CapacityCredits != nil {
		capacity = *state.CapacityCredits
	} else {
		capacity = fallbackSecondaryCapacityCredits(state.PlanType)
	}
	if state.PriorityCapacityCredits == nil && state.CapacityCredits != nil && *state.CapacityCredits <= 0 {
		return 0.0
	}
	if state.PriorityCapacityCredits != nil && *state.PriorityCapacityCredits <= 0 {
		return 0.0
	}
	primaryUsed := priorityPrimaryUsed(state)
	pu := primaryUsed
	usedPct := prioritySecondaryUsed(state, &pu)
	if usedPct > 100.0 {
		usedPct = 100.0
	}
	remaining := capacity * (1.0 - usedPct/100.0)
	if remaining < 0 {
		remaining = 0
	}
	return remaining
}

func lowestPlannerCostCandidates(available []*AccountState, routingCosts RoutingCostsByAccount) []*AccountState {
	if len(routingCosts) == 0 {
		return available
	}
	lowest := plannerCost(available[0], routingCosts)
	for _, s := range available[1:] {
		if c := plannerCost(s, routingCosts); c < lowest {
			lowest = c
		}
	}
	result := make([]*AccountState, 0, len(available))
	for _, s := range available {
		if plannerCost(s, routingCosts) == lowest {
			result = append(result, s)
		}
	}
	return result
}

// selectCapacityWeighted ports _select_capacity_weighted.
func selectCapacityWeighted(available []*AccountState) *AccountState {
	weights := make([]float64, len(available))
	total := 0.0
	for i, s := range available {
		weights[i] = remainingSecondaryCredits(s)
		total += weights[i]
	}
	if total <= 0.0 {
		return minBy(available, usageSortKeyWithCostNoCost)
	}
	return weightedChoice(available, weights)
}

// usageSortKeyWithCostNoCost adapts usageSortKey (no routing costs) for minBy.
func usageSortKeyWithCostNoCost(state *AccountState) usageWithCostKey {
	return usageSortKeyWithCost(state, nil)
}

// weightedChoice ports random.choices(states, weights=weights, k=1)[0].
func weightedChoice(states []*AccountState, weights []float64) *AccountState {
	total := 0.0
	for _, w := range weights {
		total += w
	}
	if total <= 0 {
		return states[0]
	}
	r := rand.Float64() * total
	cumulative := 0.0
	for i, w := range weights {
		cumulative += w
		if r < cumulative {
			return states[i]
		}
	}
	return states[len(states)-1]
}

// relativeAvailabilityDivisorSeconds ports _relative_availability_divisor_seconds.
func relativeAvailabilityDivisorSeconds(state *AccountState, current float64) float64 {
	remaining := relativeAvailabilityRemainingSeconds(state, current)
	return math.Max(remaining, float64(relativeAvailabilityMinDivisorSeconds))
}

// relativeAvailabilityRemainingSeconds ports _relative_availability_remaining_seconds.
func relativeAvailabilityRemainingSeconds(state *AccountState, current float64) float64 {
	var resetAt *int64
	if state.PriorityResetAt != nil {
		resetAt = state.PriorityResetAt
	} else {
		resetAt = state.SecondaryResetAt
	}
	if resetAt == nil {
		return float64(unknownResetFallbackSeconds)
	}
	return math.Max(0.0, float64(*resetAt)-current)
}

// relativeAvailabilityRawScore ports _relative_availability_raw_score.
func relativeAvailabilityRawScore(state *AccountState, current float64) float64 {
	remainingCredits := remainingSecondaryCredits(state)
	if remainingCredits <= 0.0 {
		return 0.0
	}
	return remainingCredits / relativeAvailabilityDivisorSeconds(state, current)
}

// relativeAvailabilityWeightedCandidate is one entry in
// _relative_availability_weighted_candidates' result.
type relativeAvailabilityWeightedCandidate struct {
	state    *AccountState
	weight   float64
	rawScore float64
}

// relativeAvailabilityWeightedCandidates ports _relative_availability_weighted_candidates.
func relativeAvailabilityWeightedCandidates(available []*AccountState, current float64, power float64, topK int) []relativeAvailabilityWeightedCandidate {
	type rawScorePair struct {
		state    *AccountState
		rawScore float64
	}
	rawScores := make([]rawScorePair, len(available))
	bestRawScore := 0.0
	for i, s := range available {
		score := relativeAvailabilityRawScore(s, current)
		rawScores[i] = rawScorePair{s, score}
		if score > bestRawScore {
			bestRawScore = score
		}
	}
	if bestRawScore <= 0.0 {
		return nil
	}

	safePower := power
	if safePower <= 0.0 {
		safePower = DefaultRelativeAvailabilityPower
	}

	var weighted []relativeAvailabilityWeightedCandidate
	for _, pair := range rawScores {
		normalizedScore := pair.rawScore / bestRawScore
		weight := math.Pow(normalizedScore, safePower)
		if weight < relativeAvailabilityMinWeightFraction {
			continue
		}
		weighted = append(weighted, relativeAvailabilityWeightedCandidate{pair.state, weight, pair.rawScore})
	}
	if len(weighted) == 0 {
		return nil
	}

	sort.SliceStable(weighted, func(i, j int) bool {
		a, b := weighted[i], weighted[j]
		if a.weight != b.weight {
			return a.weight > b.weight
		}
		if a.rawScore != b.rawScore {
			return a.rawScore > b.rawScore
		}
		aSecUsed, aPriUsed, aLastSel, aID := usageSortKey(a.state)
		bSecUsed, bPriUsed, bLastSel, bID := usageSortKey(b.state)
		return cmp4(aSecUsed, bSecUsed, aPriUsed, bPriUsed, aLastSel, bLastSel, 0, 0, aID, bID)
	})

	safeTopK := topK
	if safeTopK < 1 {
		safeTopK = 1
	}
	if safeTopK > len(weighted) {
		safeTopK = len(weighted)
	}
	return weighted[:safeTopK]
}

// selectRelativeAvailability ports _select_relative_availability.
func selectRelativeAvailability(available []*AccountState, current float64, power float64, topK int, deterministicProbe bool) *AccountState {
	weighted := relativeAvailabilityWeightedCandidates(available, current, power, topK)
	if len(weighted) == 0 {
		return minBy(available, usageSortKeyWithCostNoCost)
	}
	if deterministicProbe {
		return weighted[0].state
	}
	states := make([]*AccountState, len(weighted))
	weights := make([]float64, len(weighted))
	total := 0.0
	for i, w := range weighted {
		states[i] = w.state
		weights[i] = w.weight
		total += w.weight
	}
	if total <= 0.0 {
		return minBy(available, usageSortKeyWithCostNoCost)
	}
	return weightedChoice(states, weights)
}

// fillFirstSortKey ports _fill_first_sort_key.
type fillFirstKey struct {
	negPrimaryUsed, negSecondaryUsed float64
	accountID                        string
}

func fillFirstSortKey(state *AccountState) fillFirstKey {
	primaryUsed := 0.0
	if state.UsedPercent != nil {
		primaryUsed = *state.UsedPercent
	}
	secondaryUsed := 0.0
	if state.SecondaryUsedPercent != nil {
		secondaryUsed = *state.SecondaryUsedPercent
	}
	return fillFirstKey{-primaryUsed, -secondaryUsed, state.AccountID}
}

func (a fillFirstKey) less(b fillFirstKey) bool {
	if a.negPrimaryUsed != b.negPrimaryUsed {
		return a.negPrimaryUsed < b.negPrimaryUsed
	}
	if a.negSecondaryUsed != b.negSecondaryUsed {
		return a.negSecondaryUsed < b.negSecondaryUsed
	}
	return a.accountID < b.accountID
}

// selectFillFirst ports _select_fill_first.
func selectFillFirst(available []*AccountState) *AccountState {
	return minBy(available, fillFirstSortKey)
}

// --- preserve / opportunistic helpers -----------------------------------------

// weeklyPaceFloorPct ports _weekly_pace_floor_pct.
func weeklyPaceFloorPct(state *AccountState, current float64) float64 {
	remainingSeconds := secondsUntil(state.SecondaryResetAt, current)
	used := usedPct(state, true)
	if remainingSeconds == nil || used == nil {
		return 100.0
	}

	elapsedSeconds := math.Max(0.0, secondsPerWeek-*remainingSeconds)
	expectedUsedPct := math.Min(100.0, (elapsedSeconds/secondsPerWeek)*100.0)
	behindPace := *used+5.0 < expectedUsedPct

	var paceFloor float64
	switch {
	case *remainingSeconds <= 6*secondsPerHour && behindPace:
		paceFloor = 0.0
	case *remainingSeconds <= secondsPerDay && behindPace:
		paceFloor = 2.0
	case behindPace:
		paceFloor = 5.0
	default:
		paceFloor = 15.0
	}

	if recentForegroundActivity(state, current) {
		paceFloor = math.Max(paceFloor, 25.0)
	}

	return math.Max(preserveMinWeeklyFloorPct, paceFloor)
}

// shortWindowFloorPct ports _short_window_floor_pct.
func shortWindowFloorPct(state *AccountState, current float64, preserveCount int) float64 {
	remainingSeconds := secondsUntilFloat(state.ResetAt, current)
	floor := preserveMinShortWindowFloorPct
	if remainingSeconds == nil {
		return 100.0
	}
	if *remainingSeconds > secondsPerHour {
		floor = math.Max(floor, 20.0)
	}
	if recentForegroundActivity(state, current) {
		floor = math.Max(floor, 30.0)
	}
	if preserveCount <= 1 {
		floor = math.Max(floor, 25.0)
	}
	return floor
}

// preserveAllowsOpportunisticBurn ports _preserve_allows_opportunistic_burn.
func preserveAllowsOpportunisticBurn(state *AccountState, current float64, preserveCount int) bool {
	if remainingPct(state, true) == nil || remainingPct(state, false) == nil {
		return false
	}
	if state.SecondaryResetAt == nil || state.ResetAt == nil {
		return false
	}
	weeklyFloor := weeklyPaceFloorPct(state, current)
	shortFloor := shortWindowFloorPct(state, current, preserveCount)
	secondaryRemaining := 0.0
	if r := remainingPct(state, true); r != nil {
		secondaryRemaining = *r
	}
	primaryRemaining := 0.0
	if r := remainingPct(state, false); r != nil {
		primaryRemaining = *r
	}
	return secondaryRemaining > weeklyFloor && primaryRemaining > shortFloor
}

// hasOtherUsableForegroundCapacity ports _has_other_usable_foreground_capacity.
func hasOtherUsableForegroundCapacity(candidate *AccountState, available []*AccountState, current float64) bool {
	preserveCount := 0
	for _, s := range available {
		if routingPolicyOf(s) == RoutingPolicyPreserve {
			preserveCount++
		}
	}
	for _, other := range available {
		if other.AccountID == candidate.AccountID {
			continue
		}
		if other.Status != AccountStatusActive {
			continue
		}
		if routingPolicyOf(other) == RoutingPolicyPreserve {
			if preserveAllowsOpportunisticBurn(other, current, preserveCount) {
				return true
			}
			continue
		}
		return true
	}
	return false
}

// aboveEmergencyFloor ports _above_emergency_floor.
func aboveEmergencyFloor(state *AccountState) bool {
	primaryRemaining := remainingPct(state, false)
	secondaryRemaining := remainingPct(state, true)
	if primaryRemaining == nil || secondaryRemaining == nil {
		return false
	}
	return *primaryRemaining > normalLastAccountEmergencyFloorPct && *secondaryRemaining > normalLastAccountEmergencyFloorPct
}

// filterOpportunisticCandidates ports _filter_opportunistic_candidates.
func filterOpportunisticCandidates(available []*AccountState, current float64) ([]*AccountState, string) {
	var burnFirst, normal, preserve []*AccountState
	preserveCount := 0
	for _, s := range available {
		if routingPolicyOf(s) == RoutingPolicyPreserve {
			preserveCount++
		}
	}

	for _, state := range available {
		policy := routingPolicyOf(state)
		switch policy {
		case RoutingPolicyBurnFirst:
			if hasOtherUsableForegroundCapacity(state, available, current) || aboveEmergencyFloor(state) {
				burnFirst = append(burnFirst, state)
			}
		case RoutingPolicyPreserve:
			if preserveAllowsOpportunisticBurn(state, current, preserveCount) {
				preserve = append(preserve, state)
			}
		default:
			if hasOtherUsableForegroundCapacity(state, available, current) || aboveEmergencyFloor(state) {
				normal = append(normal, state)
			}
		}
	}

	if len(burnFirst) > 0 || len(normal) > 0 || len(preserve) > 0 {
		result := make([]*AccountState, 0, len(burnFirst)+len(normal)+len(preserve))
		result = append(result, burnFirst...)
		result = append(result, normal...)
		result = append(result, preserve...)
		return result, ""
	}

	for _, state := range available {
		if routingPolicyOf(state) == RoutingPolicyPreserve {
			return nil, "preserve floor or stale usage data blocks opportunistic burn"
		}
	}
	return nil, "no expendable account has emergency foreground reserve"
}

// --- health-tier evaluation ----------------------------------------------------

// EvaluateHealthTierOptions holds the keyword arguments accepted by
// app.core.balancer.logic.evaluate_health_tier.
type EvaluateHealthTierOptions struct {
	Now                        *float64
	DrainEnteredAt             *float64
	ProbeSuccessStreak         int
	DrainPrimaryThresholdPct   float64
	DrainSecondaryThresholdPct float64
	DrainErrorWindowSeconds    float64
	DrainErrorCountThreshold   int
	ProbeQuietSeconds          float64
	ProbeSuccessStreakRequired int
}

// DefaultEvaluateHealthTierOptions returns the Python defaults for
// evaluate_health_tier's keyword arguments.
func DefaultEvaluateHealthTierOptions() EvaluateHealthTierOptions {
	return EvaluateHealthTierOptions{
		DrainPrimaryThresholdPct:   85.0,
		DrainSecondaryThresholdPct: 90.0,
		DrainErrorWindowSeconds:    60.0,
		DrainErrorCountThreshold:   2,
		ProbeQuietSeconds:          60.0,
		ProbeSuccessStreakRequired: 3,
	}
}

// EvaluateHealthTier ports app.core.balancer.logic.evaluate_health_tier.
func EvaluateHealthTier(state *AccountState, opts EvaluateHealthTierOptions) int {
	current := nowSeconds()
	if opts.Now != nil {
		current = *opts.Now
	}

	switch state.Status {
	case AccountStatusRateLimited, AccountStatusQuotaExceeded, AccountStatusPaused, AccountStatusReauthRequired, AccountStatusDeactivated:
		return state.HealthTier
	}

	shouldDrain := false
	if state.UsedPercent != nil && *state.UsedPercent >= opts.DrainPrimaryThresholdPct {
		shouldDrain = true
	}
	if state.SecondaryUsedPercent != nil && *state.SecondaryUsedPercent >= opts.DrainSecondaryThresholdPct {
		shouldDrain = true
	}
	if state.ErrorCount >= opts.DrainErrorCountThreshold && state.LastErrorAt != nil && current-*state.LastErrorAt < opts.DrainErrorWindowSeconds {
		shouldDrain = true
	}

	switch state.HealthTier {
	case HealthTierHealthy:
		if shouldDrain {
			return HealthTierDraining
		}
		return HealthTierHealthy
	case HealthTierDraining:
		if shouldDrain {
			return HealthTierDraining
		}
		if opts.DrainEnteredAt != nil && current-*opts.DrainEnteredAt >= opts.ProbeQuietSeconds {
			return HealthTierProbing
		}
		return HealthTierDraining
	case HealthTierProbing:
		if shouldDrain {
			return HealthTierDraining
		}
		if opts.ProbeSuccessStreak >= opts.ProbeSuccessStreakRequired {
			return HealthTierHealthy
		}
		return HealthTierProbing
	}
	return HealthTierHealthy
}

func nowSeconds() float64 {
	return float64(time.Now().UnixNano()) / float64(time.Second)
}

// --- error-state mutation helpers (mark_*) -------------------------------------

// UpstreamError mirrors app.core.balancer.types.UpstreamError.
type UpstreamError struct {
	Message         string
	ResetsAt        *float64
	ResetsInSeconds *float64
}

// extractResetAt ports _extract_reset_at.
func extractResetAt(err UpstreamError) *int64 {
	if err.ResetsAt != nil {
		v := int64(*err.ResetsAt)
		return &v
	}
	if err.ResetsInSeconds != nil {
		v := int64(nowSeconds() + *err.ResetsInSeconds)
		return &v
	}
	return nil
}

// HandleRateLimit ports app.core.balancer.logic.handle_rate_limit.
//
// Simplification: backoff_seconds/parse_retry_after (app.core.utils.retry)
// are not yet ported; this uses the same error-count-based backoff formula
// as select_account's in-pool backoff (errorBackoffSeconds), and does not
// parse a "Retry-After"-style hint from err.Message. Callers needing the
// message-based retry-after override should apply it before calling this
// function.
func HandleRateLimit(state *AccountState, err UpstreamError) {
	state.Status = AccountStatusRateLimited
	state.ErrorCount++
	now := nowSeconds()
	state.LastErrorAt = &now
	state.BlockedAt = &now

	if resetAt := extractResetAt(err); resetAt != nil {
		v := float64(*resetAt)
		state.ResetAt = &v
	}

	delay := errorBackoffSeconds(state.ErrorCount)
	cooldown := now + delay
	state.CooldownUntil = &cooldown
}

// HandleQuotaExceeded ports app.core.balancer.logic.handle_quota_exceeded.
func HandleQuotaExceeded(state *AccountState, err UpstreamError) {
	state.Status = AccountStatusQuotaExceeded
	hundred := 100.0
	state.UsedPercent = &hundred
	now := nowSeconds()
	state.BlockedAt = &now
	cooldown := now + QuotaExceededCooldownSeconds
	state.CooldownUntil = &cooldown

	if resetAt := extractResetAt(err); resetAt != nil {
		v := float64(*resetAt)
		state.ResetAt = &v
	} else {
		v := now + 3600
		state.ResetAt = &v
	}
}

// PermanentFailureCodes mirrors PERMANENT_FAILURE_CODES.
var PermanentFailureCodes = map[string]string{
	"refresh_token_expired":     "Refresh token expired - re-login required",
	"refresh_token_reused":      "Refresh token was reused - re-login required",
	"refresh_token_invalidated": "Refresh token was revoked - re-login required",
	"invalid_grant":             "Refresh token grant invalid - re-login required",
	"token_invalidated":         "Authentication token invalidated - re-login required",
	"token_expired":             "Authentication token expired - re-login required",
	"account_session_expired":   "ChatGPT session ended - re-login required",
	"account_auth_invalidated":  "Authentication failed after token refresh - re-login required",
	"account_deactivated":       "Account has been deactivated",
	"account_suspended":         "Account has been suspended",
	"account_deleted":           "Account has been deleted",
}

// reauthRequiredFailureCodes mirrors REAUTH_REQUIRED_FAILURE_CODES.
var reauthRequiredFailureCodes = map[string]struct{}{
	"refresh_token_expired":     {},
	"refresh_token_reused":      {},
	"refresh_token_invalidated": {},
	"invalid_grant":             {},
	"token_invalidated":         {},
	"token_expired":             {},
	"account_session_expired":   {},
	"account_auth_invalidated":  {},
}

// AccountStatusForPermanentFailure ports account_status_for_permanent_failure.
func AccountStatusForPermanentFailure(errorCode string) string {
	if _, ok := reauthRequiredFailureCodes[errorCode]; ok {
		return AccountStatusReauthRequired
	}
	return AccountStatusDeactivated
}

// HandlePermanentFailure ports app.core.balancer.logic.handle_permanent_failure.
func HandlePermanentFailure(state *AccountState, errorCode string) {
	state.Status = AccountStatusForPermanentFailure(errorCode)
	if reason, ok := PermanentFailureCodes[errorCode]; ok {
		r := reason
		state.DeactivationReason = &r
	} else {
		r := fmt.Sprintf("Authentication failed: %s", errorCode)
		state.DeactivationReason = &r
	}
	state.BlockedAt = nil
}
