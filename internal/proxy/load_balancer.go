package proxy

import (
	"time"

	"github.com/soju06/codex-lb/internal/usage"
)

// This file ports the pure account-state derivation and budget-aware
// selection wrappers from app/modules/proxy/load_balancer.py and
// app/core/usage/quota.py.
//
// Simplification: the async LoadBalancer class (account leasing, persisted
// runtime state, prometheus metrics, circuit breakers, additional-quota
// eligibility filtering, sticky-session loading/orchestration, and the
// _load_selection_inputs DB-fetch pipeline) is NOT ported here. This file
// covers only the pure functions needed to turn a persisted Account + usage
// rows into an AccountState, and to run select_account with budget-threshold
// and routing-policy pooling on top. Orchestration is deferred to a future
// change.

// SelectionAccount is a self-contained view of the persisted account fields
// needed by _state_from_account / background_recovery_state_from_account.
// It intentionally does not reuse internal/accounts.Account, which lacks
// ResetAt, BlockedAt, and DeactivationReason.
//
// Simplification: field names mirror app.db.models.Account's relevant
// columns. Callers are responsible for mapping from the accounts repository
// (or a dedicated query) into this struct.
type SelectionAccount struct {
	ID                     string
	Status                 string
	PlanType               string
	RoutingPolicy          string
	ResetAt                *float64
	BlockedAt              *float64
	DeactivationReason     *string
	SecurityWorkAuthorized bool
}

// UsageEntry mirrors the subset of app.db.models.UsageHistory /
// AdditionalUsageHistory fields read by _state_from_account.
type UsageEntry struct {
	AccountID        string
	Window           string
	UsedPercent      *float64
	ResetAt          *int64
	WindowMinutes    *int64
	RecordedAt       *time.Time
	CreditsHas       *bool
	CreditsUnlimited *bool
	CreditsBalance   *float64
}

// ToWindowRow ports usage_history_to_window_row.
func (e *UsageEntry) ToWindowRow() usage.WindowRow {
	row := usage.WindowRow{AccountID: e.AccountID, UsedPercent: e.UsedPercent, ResetAt: e.ResetAt, WindowMinutes: e.WindowMinutes}
	if e.RecordedAt != nil {
		row.RecordedAt = e.RecordedAt.UTC().Format("2006-01-02 15:04:05.999999")
	}
	return row
}

// RuntimeState mirrors app.modules.proxy.load_balancer.RuntimeState: the
// in-memory, per-account runtime fields layered on top of persisted account
// state for one balancer process.
//
// Simplification: the Leases field (account lease tracking) is not ported;
// leasing is part of the deferred LoadBalancer orchestration.
type RuntimeState struct {
	ResetAt                 *float64
	CooldownUntil           *float64
	LastErrorAt             *float64
	LastSelectedAt          *float64
	ErrorCount              int
	Version                 int
	BlockedAt               *float64
	HealthTier              int
	DrainEnteredAt          *float64
	ProbeSuccessStreak      int
	InflightResponseCreates int
	InflightStreams         int
	LeasedTokens            float64
}

// accountRoutingPolicies mirrors _ACCOUNT_ROUTING_POLICIES.
var accountRoutingPolicies = map[string]struct{}{
	RoutingPolicyNormal:    {},
	RoutingPolicyBurnFirst: {},
	RoutingPolicyPreserve:  {},
}

// NormalizeAccountRoutingPolicy ports _normalize_account_routing_policy.
func NormalizeAccountRoutingPolicy(value *string) string {
	if value != nil {
		if _, ok := accountRoutingPolicies[*value]; ok {
			return *value
		}
	}
	return RoutingPolicyNormal
}

// usageRefreshIntervalSeconds mirrors _DEFAULT_USAGE_REFRESH_INTERVAL_SECONDS.
//
// Simplification: the Python implementation reads
// settings.usage_refresh_interval_seconds from the dynamic settings cache.
// That cache is not threaded through here, so this always uses the documented
// default (60s), giving a 180s "recent enough" window (max(60*2, 180)).
const usageRefreshIntervalSeconds = 60

// usageEntryIsRecentEnough ports _usage_entry_is_recent_enough.
func usageEntryIsRecentEnough(recordedAt *time.Time, now time.Time) bool {
	if recordedAt == nil {
		return false
	}
	intervalSeconds := usageRefreshIntervalSeconds * 2
	if intervalSeconds < 180 {
		intervalSeconds = 180
	}
	cutoff := now.Add(-time.Duration(intervalSeconds) * time.Second)
	return !recordedAt.UTC().Before(cutoff)
}

// usageEntryIsRecentAvailable ports _usage_entry_is_recent_available.
func usageEntryIsRecentAvailable(entry *UsageEntry, now time.Time) bool {
	return entry != nil &&
		usageEntryIsRecentEnough(entry.RecordedAt, now) &&
		entry.UsedPercent != nil &&
		*entry.UsedPercent < 100.0
}

// usageEntryRecordedAfterBlock ports _usage_entry_recorded_after_block.
func usageEntryRecordedAfterBlock(entry *UsageEntry, blockedAt float64) bool {
	if entry == nil || entry.RecordedAt == nil {
		return false
	}
	return float64(entry.RecordedAt.UTC().Unix()) > blockedAt
}

// rateLimitedFreshnessEntry ports _rate_limited_freshness_entry.
func rateLimitedFreshnessEntry(account *SelectionAccount, primaryEntry, longWindowEntry *UsageEntry) *UsageEntry {
	if longWindowEntry != nil && longWindowEntry.Window == "monthly" && usage.CapacityForPlan(account.PlanType, "monthly") != nil {
		return longWindowEntry
	}
	if primaryEntry != nil {
		return primaryEntry
	}
	return nil
}

// selectLongWindowEntry ports _select_long_window_entry.
func selectLongWindowEntry(account *SelectionAccount, monthlyEntry, secondaryEntry *UsageEntry) *UsageEntry {
	if monthlyEntry != nil && usage.CapacityForPlan(account.PlanType, "monthly") != nil {
		return monthlyEntry
	}
	return secondaryEntry
}

// extractCreditStatus ports _extract_credit_status. Only entries with
// non-nil credit fields are considered; the most-recently-recorded entry
// (by RecordedAt, treating nil as the earliest possible time) wins.
func extractCreditStatus(entries ...*UsageEntry) (creditsHas, creditsUnlimited *bool, creditsBalance *float64) {
	var best *UsageEntry
	var bestTime time.Time
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		if entry.CreditsHas == nil && entry.CreditsUnlimited == nil && entry.CreditsBalance == nil {
			continue
		}
		recordedAt := time.Time{}
		if entry.RecordedAt != nil {
			recordedAt = *entry.RecordedAt
		}
		if best == nil || recordedAt.After(bestTime) {
			best = entry
			bestTime = recordedAt
		}
	}
	if best == nil {
		return nil, nil, nil
	}
	return best.CreditsHas, best.CreditsUnlimited, best.CreditsBalance
}

// ApplyUsageQuota ports app.core.usage.quota.apply_usage_quota.
func ApplyUsageQuota(
	status string,
	primaryUsed *float64,
	primaryReset *int64,
	primaryWindowMinutes *int64,
	runtimeReset *float64,
	secondaryUsed *float64,
	secondaryReset *int64,
	creditsHas *bool,
	creditsUnlimited *bool,
	creditsBalance *float64,
) (string, *float64, *float64) {
	usedPercent := primaryUsed
	resetAt := runtimeReset
	now := nowSeconds()

	switch status {
	case AccountStatusReauthRequired, AccountStatusDeactivated, AccountStatusPaused:
		return status, usedPercent, resetAt
	}

	hasCreditOverride := hasUsableCredits(creditsHas, creditsUnlimited, creditsBalance)

	if secondaryUsed != nil {
		if *secondaryUsed >= 100.0 {
			if hasCreditOverride {
				if status == AccountStatusQuotaExceeded {
					status = AccountStatusActive
					resetAt = nil
				}
			} else {
				status = AccountStatusQuotaExceeded
				hundred := 100.0
				usedPercent = &hundred
				if secondaryReset != nil {
					v := float64(*secondaryReset)
					resetAt = &v
				}
				return status, usedPercent, resetAt
			}
		} else if status == AccountStatusQuotaExceeded {
			if runtimeReset != nil && *runtimeReset > now {
				resetAt = runtimeReset
			} else {
				status = AccountStatusActive
				resetAt = nil
			}
		}
	} else if status == AccountStatusQuotaExceeded && secondaryReset != nil {
		v := float64(*secondaryReset)
		resetAt = &v
	}

	if hasCreditOverride && status == AccountStatusQuotaExceeded {
		primaryExhausted := primaryUsed != nil && *primaryUsed >= 100.0
		if !primaryExhausted {
			status = AccountStatusActive
			resetAt = nil
		}
	}

	if primaryUsed != nil {
		if *primaryUsed >= 100.0 {
			status = AccountStatusRateLimited
			hundred := 100.0
			usedPercent = &hundred
			if primaryReset != nil {
				v := float64(*primaryReset)
				resetAt = &v
			} else if fallback := fallbackPrimaryReset(primaryWindowMinutes); fallback != nil {
				resetAt = fallback
			}
			return status, usedPercent, resetAt
		}
		if status == AccountStatusRateLimited {
			if runtimeReset != nil && *runtimeReset > now {
				resetAt = runtimeReset
			} else {
				status = AccountStatusActive
				resetAt = nil
			}
		}
	}

	return status, usedPercent, resetAt
}

// fallbackPrimaryReset ports _fallback_primary_reset.
func fallbackPrimaryReset(primaryWindowMinutes *int64) *float64 {
	windowMinutes := primaryWindowMinutes
	if windowMinutes == nil || *windowMinutes == 0 {
		windowMinutes = usage.DefaultWindowMinutes("primary")
	}
	if windowMinutes == nil || *windowMinutes == 0 {
		return nil
	}
	value := nowSeconds() + float64(*windowMinutes)*60.0
	return &value
}

// hasUsableCredits ports _has_usable_credits / _has_credit_override.
func hasUsableCredits(creditsHas, creditsUnlimited *bool, creditsBalance *float64) bool {
	if creditsUnlimited != nil && *creditsUnlimited {
		return true
	}
	if creditsHas != nil && *creditsHas {
		return true
	}
	if creditsBalance == nil {
		return false
	}
	return *creditsBalance > 0.0
}

// StateFromAccountInputs bundles the parameters of _state_from_account.
type StateFromAccountInputs struct {
	Account        *SelectionAccount
	PrimaryEntry   *UsageEntry
	SecondaryEntry *UsageEntry
	Runtime        *RuntimeState

	// SoftDrainEnabled mirrors settings.soft_drain_enabled (default true).
	SoftDrainEnabled  bool
	HealthTierOptions EvaluateHealthTierOptions

	// InflightPenaltyPct mirrors settings.proxy_account_inflight_penalty_pct
	// (default 2.5).
	InflightPenaltyPct float64
	// LeaseTokenWeight mirrors settings.proxy_account_lease_token_weight
	// (default 1.0).
	LeaseTokenWeight float64
}

// DefaultStateFromAccountInputs seeds the settings-derived defaults used by
// _state_from_account.
func DefaultStateFromAccountInputs() StateFromAccountInputs {
	return StateFromAccountInputs{
		SoftDrainEnabled:   true,
		HealthTierOptions:  DefaultEvaluateHealthTierOptions(),
		InflightPenaltyPct: 2.5,
		LeaseTokenWeight:   1.0,
	}
}

// StateFromAccount ports app.modules.proxy.load_balancer._state_from_account.
//
// Simplification: settings are passed explicitly via StateFromAccountInputs
// rather than read from a global dynamic settings cache; callers should
// populate them from the dashboard settings snapshot.
func StateFromAccount(in StateFromAccountInputs) AccountState {
	account := in.Account
	runtime := in.Runtime
	now := nowSeconds()

	routingPolicy := NormalizeAccountRoutingPolicy(&account.RoutingPolicy)

	var primaryUsed *float64
	var primaryReset *int64
	var primaryWindowMinutes *int64
	if in.PrimaryEntry != nil {
		primaryUsed = in.PrimaryEntry.UsedPercent
		primaryReset = in.PrimaryEntry.ResetAt
		primaryWindowMinutes = in.PrimaryEntry.WindowMinutes
	}

	effectiveSecondaryEntry := in.SecondaryEntry
	if effectiveSecondaryEntry != nil && effectiveSecondaryEntry.Window == "monthly" && usage.CapacityForPlan(account.PlanType, "monthly") == nil {
		effectiveSecondaryEntry = nil
	}

	var primaryRow *usage.WindowRow
	if in.PrimaryEntry != nil {
		row := in.PrimaryEntry.ToWindowRow()
		primaryRow = &row
	}
	var secondaryRow *usage.WindowRow
	if effectiveSecondaryEntry != nil {
		row := effectiveSecondaryEntry.ToWindowRow()
		secondaryRow = &row
	}
	if primaryRow != nil && usage.ShouldUseWeeklyPrimary(*primaryRow, secondaryRow) {
		effectiveSecondaryEntry = in.PrimaryEntry
		primaryUsed = nil
		primaryReset = nil
		primaryWindowMinutes = nil
	}

	var secondaryUsed *float64
	var secondaryReset *int64
	if effectiveSecondaryEntry != nil {
		secondaryUsed = effectiveSecondaryEntry.UsedPercent
		secondaryReset = effectiveSecondaryEntry.ResetAt
	}

	creditsHas, creditsUnlimited, creditsBalance := extractCreditStatus(in.PrimaryEntry, effectiveSecondaryEntry, in.SecondaryEntry)

	// Stale-data guard: if the usage window has reset (reset_at in the past)
	// but the last sample still shows 100% usage, zero it out.
	nowEpoch := int64(now)
	if primaryUsed != nil && *primaryUsed >= 100.0 {
		if primaryReset != nil && *primaryReset <= nowEpoch {
			zero := 0.0
			primaryUsed = &zero
			primaryReset = nil
		}
	}
	if secondaryUsed != nil && *secondaryUsed >= 100.0 {
		if secondaryReset != nil && *secondaryReset <= nowEpoch {
			zero := 0.0
			secondaryUsed = &zero
			secondaryReset = nil
		}
	}

	ignoreZeroCapacityPrimaryRuntimeReset := false
	statusSeed := account.Status
	longWindowQuotaAvailable := effectiveSecondaryEntry != nil &&
		usageEntryIsRecentEnough(effectiveSecondaryEntry.RecordedAt, time.Unix(int64(now), 0)) &&
		effectiveSecondaryEntry.UsedPercent != nil &&
		*effectiveSecondaryEntry.UsedPercent < 100.0

	primaryCapacity := usage.CapacityForPlan(account.PlanType, "primary")
	if primaryCapacity != nil && *primaryCapacity == 0.0 {
		condition := account.Status != AccountStatusRateLimited ||
			(primaryWindowMinutes != nil && !usage.IsPrimaryWindowMinutes(primaryWindowMinutes) && longWindowQuotaAvailable) ||
			(in.PrimaryEntry == nil && longWindowQuotaAvailable)
		if condition {
			primaryUsed = nil
			primaryReset = nil
			primaryWindowMinutes = nil
			ignoreZeroCapacityPrimaryRuntimeReset = account.Status == AccountStatusRateLimited
			if account.Status == AccountStatusRateLimited {
				statusSeed = AccountStatusActive
			}
		}
	}

	var dbResetAt *float64
	if !ignoreZeroCapacityPrimaryRuntimeReset {
		dbResetAt = account.ResetAt
	}
	effectiveRuntimeReset := dbResetAt
	if effectiveRuntimeReset == nil {
		effectiveRuntimeReset = runtime.ResetAt
	}
	effectiveBlockedAt := account.BlockedAt
	if effectiveBlockedAt == nil {
		effectiveBlockedAt = runtime.BlockedAt
	}

	if account.Status == AccountStatusQuotaExceeded &&
		effectiveRuntimeReset != nil && *effectiveRuntimeReset > now &&
		effectiveBlockedAt == nil &&
		effectiveSecondaryEntry != nil &&
		usageEntryIsRecentEnough(effectiveSecondaryEntry.RecordedAt, time.Unix(int64(now), 0)) &&
		effectiveSecondaryEntry.UsedPercent != nil && *effectiveSecondaryEntry.UsedPercent < 100.0 &&
		effectiveSecondaryEntry.ResetAt != nil && float64(*effectiveSecondaryEntry.ResetAt) > *effectiveRuntimeReset {
		effectiveRuntimeReset = nil
	}

	cooldownReady := false
	if account.Status == AccountStatusQuotaExceeded {
		cooldownReady = effectiveBlockedAt != nil && now >= *effectiveBlockedAt+QuotaExceededCooldownSeconds
	} else if runtime.CooldownUntil != nil && *runtime.CooldownUntil <= now && runtime.BlockedAt != nil {
		cooldownReady = true
	}

	if cooldownReady && effectiveBlockedAt != nil {
		var freshnessEntry *UsageEntry
		switch account.Status {
		case AccountStatusQuotaExceeded:
			freshnessEntry = effectiveSecondaryEntry
		case AccountStatusRateLimited:
			freshnessEntry = rateLimitedFreshnessEntry(account, in.PrimaryEntry, effectiveSecondaryEntry)
		}
		if freshnessEntry != nil && freshnessEntry.RecordedAt != nil {
			recordedEpoch := float64(freshnessEntry.RecordedAt.UTC().Unix())
			if recordedEpoch > *effectiveBlockedAt {
				effectiveRuntimeReset = nil
			}
		}
	}

	status, usedPercent, resetAt := ApplyUsageQuota(
		statusSeed,
		primaryUsed,
		primaryReset,
		primaryWindowMinutes,
		effectiveRuntimeReset,
		secondaryUsed,
		secondaryReset,
		creditsHas,
		creditsUnlimited,
		creditsBalance,
	)

	var nextBlockedAt *float64
	if status == AccountStatusQuotaExceeded {
		nextBlockedAt = effectiveBlockedAt
	} else if status == AccountStatusRateLimited && account.Status != AccountStatusQuotaExceeded {
		nextBlockedAt = effectiveBlockedAt
	}

	healthOpts := in.HealthTierOptions
	healthOpts.Now = &now
	healthOpts.DrainEnteredAt = runtime.DrainEnteredAt
	healthOpts.ProbeSuccessStreak = runtime.ProbeSuccessStreak

	var newTier int
	if in.SoftDrainEnabled {
		probeState := &AccountState{
			AccountID:            account.ID,
			Status:               status,
			UsedPercent:          usedPercent,
			SecondaryUsedPercent: secondaryUsed,
			LastErrorAt:          runtime.LastErrorAt,
			ErrorCount:           runtime.ErrorCount,
			HealthTier:           runtime.HealthTier,
			RoutingPolicy:        routingPolicy,
		}
		newTier = EvaluateHealthTier(probeState, healthOpts)
		if newTier == HealthTierDraining && runtime.HealthTier != HealthTierDraining {
			n := now
			runtime.DrainEnteredAt = &n
			runtime.ProbeSuccessStreak = 0
		}
		if newTier == HealthTierHealthy {
			runtime.DrainEnteredAt = nil
			runtime.ProbeSuccessStreak = 0
		}
		runtime.HealthTier = newTier
	} else {
		newTier = HealthTierHealthy
		runtime.DrainEnteredAt = nil
		runtime.ProbeSuccessStreak = 0
		runtime.HealthTier = HealthTierHealthy
	}

	inflightPressurePct := float64(runtime.InflightResponseCreates+runtime.InflightStreams) * in.InflightPenaltyPct
	leasedTokenPressurePct := 0.0
	longWindowKey := "secondary"
	if effectiveSecondaryEntry != nil && effectiveSecondaryEntry.Window == "monthly" {
		longWindowKey = "monthly"
	}
	capacityCredits := 0.0
	if cap := usage.CapacityForPlan(account.PlanType, longWindowKey); cap != nil {
		capacityCredits = *cap
	}
	if capacityCredits > 0.0 && runtime.LeasedTokens > 0 {
		leasedTokenPressurePct = runtime.LeasedTokens * in.LeaseTokenWeight / capacityCredits * 100.0
	}
	pressurePct := inflightPressurePct + leasedTokenPressurePct

	effectiveUsedPercent := clampMin100(usedPercent, pressurePct)
	effectiveSecondaryUsedPercent := clampMin100(secondaryUsed, pressurePct)

	return AccountState{
		AccountID:               account.ID,
		Status:                  status,
		UsedPercent:             effectiveUsedPercent,
		ResetAt:                 resetAt,
		PrimaryResetAt:          primaryReset,
		BlockedAt:               nextBlockedAt,
		CooldownUntil:           runtime.CooldownUntil,
		SecondaryUsedPercent:    effectiveSecondaryUsedPercent,
		SecondaryResetAt:        secondaryReset,
		LastErrorAt:             runtime.LastErrorAt,
		LastSelectedAt:          runtime.LastSelectedAt,
		ErrorCount:              runtime.ErrorCount,
		DeactivationReason:      account.DeactivationReason,
		PlanType:                account.PlanType,
		CapacityCredits:         &capacityCredits,
		HealthTier:              newTier,
		InflightResponseCreates: runtime.InflightResponseCreates,
		InflightStreams:         runtime.InflightStreams,
		LeasedTokens:            runtime.LeasedTokens,
		RoutingPolicy:           routingPolicy,
	}
}

// clampMin100 ports `min(100.0, value + pressure_pct)` for an optional value.
func clampMin100(value *float64, pressurePct float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value + pressurePct
	if result > 100.0 {
		result = 100.0
	}
	return &result
}

// BackgroundRecoveryStateFromAccount ports
// app.modules.proxy.load_balancer.background_recovery_state_from_account.
//
// It evaluates whether a persisted blocked account can return to `active`
// without live runtime state, by seeding a throwaway RuntimeState from the
// persisted block marker.
func BackgroundRecoveryStateFromAccount(account *SelectionAccount, primaryEntry, secondaryEntry *UsageEntry) AccountState {
	runtime := &RuntimeState{}
	var blockedAt *float64
	var resetAt *float64
	if account.BlockedAt != nil {
		v := *account.BlockedAt
		blockedAt = &v
	}
	if account.ResetAt != nil {
		v := *account.ResetAt
		resetAt = &v
	}

	if blockedAt != nil {
		runtime.BlockedAt = blockedAt
	}
	if account.Status == AccountStatusRateLimited && blockedAt != nil {
		if resetAt != nil {
			runtime.CooldownUntil = resetAt
		}
	}

	in := DefaultStateFromAccountInputs()
	in.Account = account
	in.PrimaryEntry = primaryEntry
	in.SecondaryEntry = secondaryEntry
	in.Runtime = runtime
	state := StateFromAccount(in)

	if account.Status == AccountStatusRateLimited {
		freshnessEntry := rateLimitedFreshnessEntry(account, primaryEntry, secondaryEntry)
		now := nowSeconds()
		switch {
		case blockedAt != nil && resetAt != nil && *resetAt <= now:
			if !usageEntryRecordedAfterBlock(freshnessEntry, *blockedAt) {
				state.Status = AccountStatusRateLimited
				state.ResetAt = resetAt
				state.BlockedAt = blockedAt
				state.CooldownUntil = resetAt
				return state
			}
		case blockedAt == nil && resetAt != nil && *resetAt <= now:
			if !usageEntryIsRecentAvailable(freshnessEntry, time.Unix(int64(now), 0)) {
				state.Status = AccountStatusRateLimited
				state.ResetAt = resetAt
				state.BlockedAt = nil
				state.CooldownUntil = nil
				return state
			}
		}
		if resetAt == nil {
			state.Status = AccountStatusRateLimited
			state.ResetAt = nil
			state.BlockedAt = blockedAt
			state.CooldownUntil = nil
			return state
		}
	}
	return state
}

// --- budget-threshold-aware selection wrapper -----------------------------

// StateAboveBudgetThreshold ports _state_above_budget_threshold.
func StateAboveBudgetThreshold(state *AccountState, budgetThresholdPct float64) bool {
	usedPercent := state.UsedPercent
	if state.PriorityUsedPercent != nil {
		usedPercent = state.PriorityUsedPercent
	}
	return usedPercent != nil && *usedPercent > budgetThresholdPct
}

// StateAboveStickyBudgetThreshold ports _state_above_sticky_budget_threshold.
// secondaryBudgetThresholdPct of nil mirrors the Python default of using
// budgetThresholdPct for the secondary window too.
func StateAboveStickyBudgetThreshold(state *AccountState, budgetThresholdPct float64, secondaryBudgetThresholdPct *float64) bool {
	secondaryThreshold := budgetThresholdPct
	if secondaryBudgetThresholdPct != nil {
		secondaryThreshold = *secondaryBudgetThresholdPct
	}
	usedPercent := state.UsedPercent
	if state.PriorityUsedPercent != nil {
		usedPercent = state.PriorityUsedPercent
	}
	var secondaryUsedPercent *float64
	if state.LimitScopedUsage && state.PrioritySecondaryUsedPercent == nil {
		secondaryUsedPercent = usedPercent
	} else if state.PrioritySecondaryUsedPercent != nil {
		secondaryUsedPercent = state.PrioritySecondaryUsedPercent
	} else {
		secondaryUsedPercent = state.SecondaryUsedPercent
	}
	return (usedPercent != nil && *usedPercent > budgetThresholdPct) ||
		(secondaryUsedPercent != nil && *secondaryUsedPercent > secondaryThreshold)
}

// BestHealthTierStates ports _best_health_tier_states.
func BestHealthTierStates(states []*AccountState) []*AccountState {
	var healthy, probing, draining []*AccountState
	for _, s := range states {
		switch s.HealthTier {
		case HealthTierHealthy:
			healthy = append(healthy, s)
		case HealthTierProbing:
			probing = append(probing, s)
		case HealthTierDraining:
			draining = append(draining, s)
		}
	}
	if len(healthy) > 0 {
		return healthy
	}
	if len(probing) > 0 {
		return probing
	}
	if len(draining) > 0 {
		return draining
	}
	return states
}

// SelectAccountPreferringBudgetSafeOptions holds the keyword arguments
// accepted by _select_account_preferring_budget_safe.
type SelectAccountPreferringBudgetSafeOptions struct {
	PreferEarlierReset            bool
	PreferEarlierResetWindow      ResetPreferenceWindow
	RoutingStrategy               RoutingStrategy
	RelativeAvailabilityPower     float64
	RelativeAvailabilityTopK      int
	BudgetThresholdPct            float64
	SecondaryBudgetThresholdPct   float64
	ApplySecondaryBudgetThreshold bool
	AllowBackoffFallback          bool
	DeterministicProbe            bool
	TrafficClass                  string
	IgnoreStandardQuota           bool
	RoutingCosts                  RoutingCostsByAccount
}

// DefaultSelectAccountPreferringBudgetSafeOptions returns the Python keyword
// defaults for _select_account_preferring_budget_safe.
func DefaultSelectAccountPreferringBudgetSafeOptions() SelectAccountPreferringBudgetSafeOptions {
	return SelectAccountPreferringBudgetSafeOptions{
		PreferEarlierResetWindow:    ResetPreferenceWindowSecondary,
		RelativeAvailabilityPower:   DefaultRelativeAvailabilityPower,
		RelativeAvailabilityTopK:    DefaultRelativeAvailabilityTopK,
		SecondaryBudgetThresholdPct: 100.0,
		AllowBackoffFallback:        true,
		TrafficClass:                TrafficClassForeground,
	}
}

// SelectAccountPreferringBudgetSafe ports
// app.modules.proxy.load_balancer._select_account_preferring_budget_safe.
func SelectAccountPreferringBudgetSafe(states []*AccountState, opts SelectAccountPreferringBudgetSafeOptions) SelectionResult {
	baseOpts := SelectAccountOptions{
		PreferEarlierReset:        opts.PreferEarlierReset,
		PreferEarlierResetWindow:  opts.PreferEarlierResetWindow,
		RoutingStrategy:           opts.RoutingStrategy,
		AllowBackoffFallback:      opts.AllowBackoffFallback,
		DeterministicProbe:        opts.DeterministicProbe,
		RelativeAvailabilityPower: opts.RelativeAvailabilityPower,
		RelativeAvailabilityTopK:  opts.RelativeAvailabilityTopK,
		TrafficClass:              opts.TrafficClass,
		IgnoreStandardQuota:       opts.IgnoreStandardQuota,
		RoutingCosts:              opts.RoutingCosts,
	}

	stateBudgetThreshold := func(state *AccountState) bool {
		if opts.ApplySecondaryBudgetThreshold {
			secondary := opts.SecondaryBudgetThresholdPct
			return StateAboveStickyBudgetThreshold(state, opts.BudgetThresholdPct, &secondary)
		}
		return StateAboveBudgetThreshold(state, opts.BudgetThresholdPct)
	}

	switch opts.RoutingStrategy {
	case RoutingStrategySequentialDrain, RoutingStrategyResetDrain, RoutingStrategySingleAccount:
		var budgetSafe []*AccountState
		for _, state := range states {
			if state.RoutingPolicy != RoutingPolicyPreserve && !stateBudgetThreshold(state) {
				budgetSafe = append(budgetSafe, state)
			}
		}
		pool := budgetSafe
		if len(pool) == 0 {
			pool = states
		}
		return SelectAccount(pool, baseOpts)
	}

	bestHealthStates := BestHealthTierStates(states)
	var burnFirstStates []*AccountState
	for _, s := range bestHealthStates {
		if s.RoutingPolicy == RoutingPolicyBurnFirst {
			burnFirstStates = append(burnFirstStates, s)
		}
	}
	if len(burnFirstStates) > 0 {
		burnFirstOpts := baseOpts
		burnFirstOpts.AllowBackoffFallback = false
		burnFirst := SelectAccount(burnFirstStates, burnFirstOpts)
		if burnFirst.Account != nil {
			return burnFirst
		}
	}

	var preferredStates []*AccountState
	for _, state := range states {
		if state.RoutingPolicy != RoutingPolicyPreserve && !stateBudgetThreshold(state) {
			preferredStates = append(preferredStates, state)
		}
	}
	if len(preferredStates) > 0 {
		selectionPool := preferredStates
		if len(preferredStates) == len(states) {
			selectionPool = states
		}
		preferred := SelectAccount(selectionPool, baseOpts)
		if preferred.Account != nil {
			return preferred
		}
		if len(preferredStates) == len(states) {
			return preferred
		}
	}

	if opts.RoutingStrategy == RoutingStrategyUsageWeighted && len(states) > 0 {
		usageWeightedOpts := baseOpts
		usageWeightedOpts.PrimaryFirstUsageWeighted = true
		return SelectAccount(states, usageWeightedOpts)
	}

	return SelectAccount(states, baseOpts)
}
