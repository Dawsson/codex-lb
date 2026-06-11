package proxy

import (
	"time"
)

// SelectionInputs mirrors app.modules.proxy.load_balancer.SelectionInputs.
type SelectionInputs struct {
	Accounts                      []SelectionAccount
	LatestPrimary                 map[string]*UsageEntry
	LatestSecondary               map[string]*UsageEntry
	LatestMonthly                 map[string]*UsageEntry
	ErrorMessage                  string
	ErrorCode                     string
	IgnoreStandardQuotaAccountIDs map[string]struct{}
	IgnoreStandardQuotaStatus     bool
	RoutingPolicyOverride         *string
}

// ProxyAccount carries persisted account fields plus a decrypted access token
// for upstream forwarding.
type ProxyAccount struct {
	SelectionAccount
	AccessToken string
}

// BuildStates ports _build_states from load_balancer.py.
func BuildStates(
	accounts []SelectionAccount,
	latestPrimary map[string]*UsageEntry,
	latestSecondary map[string]*UsageEntry,
	latestMonthly map[string]*UsageEntry,
	runtime map[string]*RuntimeState,
	routingPolicyOverride *string,
	ignoreStandardQuotaAccountIDs map[string]struct{},
	in StateFromAccountInputs,
) ([]*AccountState, map[string]SelectionAccount) {
	states := make([]*AccountState, 0, len(accounts))
	accountMap := make(map[string]SelectionAccount, len(accounts))

	for _, account := range accounts {
		secondaryEntry := latestSecondary[account.ID]
		if _, ignore := ignoreStandardQuotaAccountIDs[account.ID]; !ignore {
			secondaryEntry = selectLongWindowEntry(&account, latestMonthly[account.ID], secondaryEntry)
		}
		rt := runtime[account.ID]
		if rt == nil {
			rt = &RuntimeState{}
			runtime[account.ID] = rt
		}
		inputs := in
		inputs.Account = &account
		inputs.PrimaryEntry = latestPrimary[account.ID]
		inputs.SecondaryEntry = secondaryEntry
		inputs.Runtime = rt
		state := StateFromAccount(inputs)
		if routingPolicyOverride != nil {
			if _, scoped := ignoreStandardQuotaAccountIDs[account.ID]; scoped {
				state.RoutingPolicy = *routingPolicyOverride
			}
		}
		if _, scoped := ignoreStandardQuotaAccountIDs[account.ID]; scoped {
			state.IgnoreStandardQuota = true
		}
		states = append(states, &state)
		accountMap[account.ID] = account
	}
	return states, accountMap
}

// SelectableAccounts ports _selectable_accounts.
func SelectableAccounts(accounts []SelectionAccount) []SelectionAccount {
	out := make([]SelectionAccount, 0, len(accounts))
	for _, account := range accounts {
		switch account.Status {
		case AccountStatusReauthRequired, AccountStatusDeactivated, AccountStatusPaused:
			continue
		default:
			out = append(out, account)
		}
	}
	return out
}

// FilterAccountsForModel ports _filter_accounts_for_model.
func FilterAccountsForModel(registry *ModelRegistry, accounts []SelectionAccount, model string) []SelectionAccount {
	allowedPlans := registry.PlanTypesForModel(model)
	if allowedPlans == nil {
		return accounts
	}
	out := make([]SelectionAccount, 0, len(accounts))
	for _, account := range accounts {
		if AccountPlanMatchesAllowed(account.PlanType, allowedPlans) {
			out = append(out, account)
		}
	}
	return out
}

// AccountPlanMatchesAllowed ports account_plan_matches_allowed.
func AccountPlanMatchesAllowed(planType string, allowed map[string]struct{}) bool {
	normalized := lowerTrimLocal(planType)
	if _, ok := allowed[normalized]; ok {
		return true
	}
	if alias, ok := capacityPlanAliases[normalized]; ok {
		if _, ok := allowed[alias]; ok {
			return true
		}
	}
	return false
}

// MappedModelHasRegistryEntry ports _mapped_model_has_registry_entry.
func MappedModelHasRegistryEntry(registry *ModelRegistry, model string) bool {
	if model == "" {
		return false
	}
	plans := registry.PlanTypesForModel(model)
	return plans != nil
}

// FilterStatesForAccountCaps ports _filter_states_for_account_caps.
func FilterStatesForAccountCaps(states []*AccountState, leaseKind AccountLeaseKind, responseCreateCap, streamCap int) []*AccountState {
	if leaseKind == "" {
		out := make([]*AccountState, len(states))
		copy(out, states)
		return out
	}
	filtered := make([]*AccountState, 0, len(states))
	for _, state := range states {
		switch leaseKind {
		case AccountLeaseKindResponseCreate:
			if responseCreateCap > 0 && state.InflightResponseCreates >= responseCreateCap {
				continue
			}
		case AccountLeaseKindStream:
			if streamCap > 0 && state.InflightStreams >= streamCap {
				continue
			}
		}
		filtered = append(filtered, state)
	}
	return filtered
}

// UsageEntryFromRecorded builds a UsageEntry from repository scan values.
func UsageEntryFromRecorded(
	accountID string,
	window string,
	usedPercent float64,
	resetAt *int64,
	windowMinutes *int64,
	recordedAt *time.Time,
	creditsHas *bool,
	creditsUnlimited *bool,
	creditsBalance *float64,
) *UsageEntry {
	return &UsageEntry{
		AccountID:        accountID,
		Window:           window,
		UsedPercent:      &usedPercent,
		ResetAt:          resetAt,
		WindowMinutes:    windowMinutes,
		RecordedAt:       recordedAt,
		CreditsHas:       creditsHas,
		CreditsUnlimited: creditsUnlimited,
		CreditsBalance:   creditsBalance,
	}
}
