package proxy

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/soju06/codex-lb/internal/accounts"
	"github.com/soju06/codex-lb/internal/crypto"
	"github.com/soju06/codex-lb/internal/settings"
	"github.com/soju06/codex-lb/internal/usage"
)

const (
	noPlanSupportForModel              = "no_plan_support_for_model"
	accountResponseCreateCapErrorCode  = "account_response_create_cap"
	accountStreamCapErrorCode          = "account_stream_cap"
	defaultResponseCreateCap           = 4
	defaultStreamCap                   = 8
	defaultAccountLeaseTTLSeconds      = 900.0
)

// LoadBalancer orchestrates account selection, leasing, and runtime state.
type LoadBalancer struct {
	accountsRepo          accounts.Repository
	settingsRepo          settings.Repository
	usageRepo             usage.Repository
	encryptor             *crypto.Encryptor
	modelRegistry         *ModelRegistry
	additionalQuotaRegistry *AdditionalQuotaRegistry

	mu             sync.Mutex
	runtime        map[string]*RuntimeState
	selectionCache *AccountSelectionCache[SelectionInputs]

	responseCreateCap int
	streamCap         int
	leaseTTLSeconds   float64
}

func NewLoadBalancer(
	accountRepo accounts.Repository,
	settingsRepo settings.Repository,
	usageRepo usage.Repository,
	encryptor *crypto.Encryptor,
	modelRegistry *ModelRegistry,
	additionalQuotaRegistry *AdditionalQuotaRegistry,
) *LoadBalancer {
	if additionalQuotaRegistry == nil {
		additionalQuotaRegistry = NewAdditionalQuotaRegistry()
	}
	return &LoadBalancer{
		accountsRepo:            accountRepo,
		settingsRepo:            settingsRepo,
		usageRepo:                 usageRepo,
		encryptor:                 encryptor,
		modelRegistry:             modelRegistry,
		additionalQuotaRegistry:   additionalQuotaRegistry,
		runtime:                   make(map[string]*RuntimeState),
		selectionCache:            NewAccountSelectionCache[SelectionInputs](2 * time.Second),
		responseCreateCap:         defaultResponseCreateCap,
		streamCap:                 defaultStreamCap,
		leaseTTLSeconds:           defaultAccountLeaseTTLSeconds,
	}
}

type SelectAccountParams struct {
	Model                         string
	ExcludeAccountIDs             map[string]struct{}
	RequireSecurityWorkAuthorized bool
	PreferEarlierResetAccounts    bool
	PreferEarlierResetWindow      ResetPreferenceWindow
	RoutingStrategy               RoutingStrategy
	RelativeAvailabilityPower     float64
	RelativeAvailabilityTopK      int
	BudgetThresholdPct            float64
	SecondaryBudgetThresholdPct   float64
	RoutingCosts                  RoutingCostsByAccount
	LeaseKind                     AccountLeaseKind
	EstimatedLeaseTokens          float64
	TrafficClass                  string
	SingleAccountID               *string
	PreferredAccountID            *string
}

func (lb *LoadBalancer) SelectAccount(ctx context.Context, params SelectAccountParams) (AccountSelection, error) {
	inputs, err := lb.loadSelectionInputs(ctx, params.Model, params.SingleAccountID)
	if err != nil {
		return AccountSelection{}, err
	}
	if inputs.ErrorCode != "" && len(inputs.Accounts) == 0 {
		return AccountSelection{
			ErrorMessage: inputs.ErrorMessage,
			ErrorCode:    inputs.ErrorCode,
		}, nil
	}

	accountsList := inputs.Accounts
	if params.RequireSecurityWorkAuthorized {
		filtered := make([]SelectionAccount, 0, len(accountsList))
		for _, account := range accountsList {
			if account.SecurityWorkAuthorized {
				filtered = append(filtered, account)
			}
		}
		if len(filtered) == 0 {
			return AccountSelection{
				ErrorMessage: "No accounts marked as authorized for security work",
				ErrorCode:    "no_security_work_authorized_accounts",
			}, nil
		}
		accountsList = filtered
	}
	if len(params.ExcludeAccountIDs) > 0 {
		filtered := make([]SelectionAccount, 0, len(accountsList))
		for _, account := range accountsList {
			if _, excluded := params.ExcludeAccountIDs[account.ID]; !excluded {
				filtered = append(filtered, account)
			}
		}
		accountsList = filtered
	}
	inputs.Accounts = accountsList

	dashboardSettings, err := lb.settingsRepo.Get(ctx)
	if err != nil {
		return AccountSelection{}, err
	}

	lb.mu.Lock()

	lb.reclaimStaleLeasesLocked()
	states, accountMap := BuildStates(
		inputs.Accounts,
		inputs.LatestPrimary,
		inputs.LatestSecondary,
		inputs.LatestMonthly,
		lb.runtime,
		inputs.RoutingPolicyOverride,
		inputs.IgnoreStandardQuotaAccountIDs,
		stateFromAccountInputs(dashboardSettings),
	)

	selectionStates := FilterStatesForAccountCaps(states, params.LeaseKind, lb.responseCreateCap, lb.streamCap)
	var result SelectionResult
	if params.PreferredAccountID != nil && *params.PreferredAccountID != "" {
		preferred := strings.TrimSpace(*params.PreferredAccountID)
		for _, state := range selectionStates {
			if state.AccountID == preferred {
				result = SelectionResult{Account: state}
				break
			}
		}
		if result.Account == nil {
			for _, state := range states {
				if state.AccountID == preferred {
					result = SelectionResult{Account: state}
					break
				}
			}
		}
	}
	if result.Account == nil {
		if len(selectionStates) == 0 && len(states) > 0 {
			result = SelectionResult{ErrorMessage: "No available accounts"}
		} else {
		opts := DefaultSelectAccountPreferringBudgetSafeOptions()
		opts.PreferEarlierReset = params.PreferEarlierResetAccounts
		if params.PreferEarlierResetWindow != "" {
			opts.PreferEarlierResetWindow = params.PreferEarlierResetWindow
		}
		if params.RoutingStrategy != "" {
			opts.RoutingStrategy = params.RoutingStrategy
		}
		if params.RelativeAvailabilityPower > 0 {
			opts.RelativeAvailabilityPower = params.RelativeAvailabilityPower
		}
		if params.RelativeAvailabilityTopK > 0 {
			opts.RelativeAvailabilityTopK = params.RelativeAvailabilityTopK
		}
		if params.BudgetThresholdPct > 0 {
			opts.BudgetThresholdPct = params.BudgetThresholdPct
		}
		if params.SecondaryBudgetThresholdPct > 0 {
			opts.SecondaryBudgetThresholdPct = params.SecondaryBudgetThresholdPct
		}
		if params.TrafficClass != "" {
			opts.TrafficClass = params.TrafficClass
		}
		if params.RoutingCosts != nil {
			opts.RoutingCosts = params.RoutingCosts
		}
		opts.IgnoreStandardQuota = inputs.IgnoreStandardQuotaStatus
		pool := selectionStates
		if len(pool) == 0 {
			pool = states
		}
		result = SelectAccountPreferringBudgetSafe(pool, opts)
		}
	}

	if result.Account == nil {
		code := ""
		if result.ErrorMessage == "No available accounts" && params.LeaseKind != "" {
			code = accountCapErrorCode(params.LeaseKind)
		}
		return AccountSelection{ErrorMessage: result.ErrorMessage, ErrorCode: code}, nil
	}

	selectedAccount, ok := accountMap[result.Account.AccountID]
	if !ok {
		return AccountSelection{ErrorMessage: result.ErrorMessage}, nil
	}

	var lease *AccountLease
	selectedID := result.Account.AccountID
	if params.LeaseKind != "" {
		lease = lb.acquireLeaseLocked(selectedID, params.LeaseKind, params.EstimatedLeaseTokens)
	}
	lb.mu.Unlock()

	tokenRecord, err := lb.accountsRepo.GetProxyRecord(ctx, selectedID)
	if err != nil {
		lb.ReleaseLease(lease)
		return AccountSelection{}, err
	}
	if tokenRecord == nil {
		lb.ReleaseLease(lease)
		return AccountSelection{ErrorMessage: "Selected account not found"}, nil
	}
	token, err := lb.accountsRepo.DecryptAccessToken(lb.encryptor, *tokenRecord)
	if err != nil {
		lb.ReleaseLease(lease)
		return AccountSelection{}, err
	}

	proxyAccount := &ProxyAccount{
		SelectionAccount: selectedAccount,
		AccessToken:      token,
	}
	proxyAccount.Status = result.Account.Status
	if result.Account.DeactivationReason != nil {
		proxyAccount.DeactivationReason = result.Account.DeactivationReason
	}
	if result.Account.ResetAt != nil {
		proxyAccount.ResetAt = result.Account.ResetAt
	}

	return AccountSelection{
		Account: proxyAccount,
		Lease:   lease,
	}, nil
}

func (lb *LoadBalancer) ReleaseLease(lease *AccountLease) {
	if lease == nil {
		return
	}
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.releaseLeaseLocked(lease)
}

func (lb *LoadBalancer) InvalidateSelectionCache() {
	lb.selectionCache.Invalidate()
}

func (lb *LoadBalancer) loadSelectionInputs(ctx context.Context, model string, singleAccountID *string) (SelectionInputs, error) {
	quotaKey := lb.additionalQuotaRegistry.QuotaKeyForModel(model)
	cacheKey := AccountCacheKey{
		Model:              model,
		TrafficClass:       TrafficClassForeground,
		AdditionalQuotaKey: quotaKey,
	}
	if cached, ok := lb.selectionCache.Get(cacheKey); ok {
		return cached, nil
	}
	gen := lb.selectionCache.Generation()

	records, err := lb.accountsRepo.ListProxyRecords(ctx)
	if err != nil {
		return SelectionInputs{}, err
	}

	selectionAccounts := make([]SelectionAccount, 0, len(records))
	for _, record := range records {
		if singleAccountID != nil && record.ID != *singleAccountID {
			continue
		}
		selectionAccounts = append(selectionAccounts, selectionAccountFromRecord(record))
	}
	selectable := SelectableAccounts(selectionAccounts)

	if model != "" && MappedModelHasRegistryEntry(lb.modelRegistry, model) {
		filtered := FilterAccountsForModel(lb.modelRegistry, selectable, model)
		if len(filtered) == 0 {
			if len(records) == 0 {
				inputs := SelectionInputs{Accounts: []SelectionAccount{}}
				lb.selectionCache.Set(inputs, cacheKey, &gen)
				return inputs, nil
			}
			if len(selectable) == 0 {
				inputs := SelectionInputs{Accounts: []SelectionAccount{}}
				lb.selectionCache.Set(inputs, cacheKey, &gen)
				return inputs, nil
			}
			inputs := SelectionInputs{
				Accounts:     []SelectionAccount{},
				ErrorMessage: fmt.Sprintf("No accounts with a plan supporting model '%s'", model),
				ErrorCode:    noPlanSupportForModel,
			}
			lb.selectionCache.Set(inputs, cacheKey, &gen)
			return inputs, nil
		}
		selectable = filtered
	}

	primary, err := lb.loadUsageMap(ctx, "primary")
	if err != nil {
		return SelectionInputs{}, err
	}
	secondary, err := lb.loadUsageMap(ctx, "secondary")
	if err != nil {
		return SelectionInputs{}, err
	}
	monthly, err := lb.loadUsageMap(ctx, "monthly")
	if err != nil {
		return SelectionInputs{}, err
	}

	inputs := SelectionInputs{
		Accounts:                      selectable,
		LatestPrimary:                 primary,
		LatestSecondary:               secondary,
		LatestMonthly:                 monthly,
		IgnoreStandardQuotaAccountIDs: map[string]struct{}{},
	}

	if quotaKey != "" {
		accountIDs := make([]string, 0, len(selectable))
		for _, account := range selectable {
			accountIDs = append(accountIDs, account.ID)
		}
		freshSince := additionalUsageFreshSince(time.Now().UTC())
		latestAdditionalPrimaryRows, err := lb.usageRepo.LatestAdditionalByQuotaKey(ctx, quotaKey, "primary", accountIDs, nil)
		if err != nil {
			return SelectionInputs{}, err
		}
		latestAdditionalSecondaryRows, err := lb.usageRepo.LatestAdditionalByQuotaKey(ctx, quotaKey, "secondary", accountIDs, nil)
		if err != nil {
			return SelectionInputs{}, err
		}
		freshAdditionalPrimaryRows, err := lb.usageRepo.LatestAdditionalByQuotaKey(ctx, quotaKey, "primary", accountIDs, &freshSince)
		if err != nil {
			return SelectionInputs{}, err
		}
		freshAdditionalSecondaryRows, err := lb.usageRepo.LatestAdditionalByQuotaKey(ctx, quotaKey, "secondary", accountIDs, &freshSince)
		if err != nil {
			return SelectionInputs{}, err
		}

		additionalFilter := filterAccountsForAdditionalLimit(
			lb.additionalQuotaRegistry,
			selectable,
			model,
			quotaKey,
			true,
			usageEntriesFromAdditional(latestAdditionalPrimaryRows),
			usageEntriesFromAdditional(latestAdditionalSecondaryRows),
			usageEntriesFromAdditional(freshAdditionalPrimaryRows),
			usageEntriesFromAdditional(freshAdditionalSecondaryRows),
		)
		if len(additionalFilter.Accounts) == 0 {
			inputs := SelectionInputs{
				Accounts:     []SelectionAccount{},
				ErrorMessage: additionalFilter.ErrorMessage,
				ErrorCode:    additionalFilter.ErrorCode,
			}
			lb.selectionCache.Set(inputs, cacheKey, &gen)
			return inputs, nil
		}
		selectable = additionalFilter.Accounts
		inputs.Accounts = selectable
		inputs.IgnoreStandardQuotaStatus = true
		if policy := lb.additionalQuotaRegistry.RoutingPolicyOverride(quotaKey); policy != nil {
			inputs.RoutingPolicyOverride = policy
		}
		accountIDsSet := make(map[string]struct{}, len(selectable))
		for _, account := range selectable {
			accountIDsSet[account.ID] = struct{}{}
		}
		mergeAdditionalUsageIntoInputs(
			&inputs,
			accountIDsSet,
			additionalFilter.LatestPrimary,
			additionalFilter.LatestSecondary,
		)
	}

	lb.selectionCache.Set(inputs, cacheKey, &gen)
	return inputs, nil
}

func (lb *LoadBalancer) loadUsageMap(ctx context.Context, window string) (map[string]*UsageEntry, error) {
	rows, err := lb.accountsRepo.LatestUsageByWindow(ctx, window)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*UsageEntry, len(rows))
	for accountID, row := range rows {
		recordedAt := parseRecordedAtString(row.RecordedAt)
		out[accountID] = UsageEntryFromRecorded(
			accountID,
			row.Window,
			row.UsedPercent,
			nullInt64ToPtr(row.ResetAt),
			nullInt64ToPtr(row.WindowMinutes),
			recordedAt,
			nullBoolToPtr(row.CreditsHas),
			nil,
			nullFloat64ToPtr(row.CreditsBalance),
		)
	}
	return out, nil
}

func (lb *LoadBalancer) acquireLeaseLocked(accountID string, kind AccountLeaseKind, estimatedTokens float64) *AccountLease {
	runtime := lb.runtime[accountID]
	if runtime == nil {
		runtime = &RuntimeState{}
		lb.runtime[accountID] = runtime
	}
	lease := &AccountLease{
		LeaseID:         uuid.NewString(),
		AccountID:       accountID,
		Kind:            kind,
		AcquiredAt:      float64(time.Now().UnixNano()) / 1e9,
		EstimatedTokens: estimatedTokens,
	}
	switch kind {
	case AccountLeaseKindResponseCreate:
		runtime.InflightResponseCreates++
	case AccountLeaseKindStream:
		runtime.InflightStreams++
	}
	runtime.LeasedTokens += estimatedTokens
	now := nowSeconds()
	runtime.LastSelectedAt = &now
	runtime.Version++
	return lease
}

func (lb *LoadBalancer) releaseLeaseLocked(lease *AccountLease) {
	runtime := lb.runtime[lease.AccountID]
	if runtime == nil {
		return
	}
	switch lease.Kind {
	case AccountLeaseKindResponseCreate:
		if runtime.InflightResponseCreates > 0 {
			runtime.InflightResponseCreates--
		}
	case AccountLeaseKindStream:
		if runtime.InflightStreams > 0 {
			runtime.InflightStreams--
		}
	}
	runtime.LeasedTokens -= lease.EstimatedTokens
	if runtime.LeasedTokens < 0 {
		runtime.LeasedTokens = 0
	}
	runtime.Version++
}

func (lb *LoadBalancer) reclaimStaleLeasesLocked() {
	// Lease tracking is simplified: inflight counters are decremented when
	// ReleaseLease is called. Stale reclamation uses TTL-based decay on the
	// counters when leases were not explicitly released (best-effort).
	now := nowSeconds()
	for _, runtime := range lb.runtime {
		if runtime.LastSelectedAt != nil && now-*runtime.LastSelectedAt > lb.leaseTTLSeconds {
			runtime.InflightResponseCreates = 0
			runtime.InflightStreams = 0
			runtime.LeasedTokens = 0
		}
	}
}

func accountCapErrorCode(kind AccountLeaseKind) string {
	switch kind {
	case AccountLeaseKindResponseCreate:
		return accountResponseCreateCapErrorCode
	case AccountLeaseKindStream:
		return accountStreamCapErrorCode
	default:
		return ""
	}
}

func selectionAccountFromRecord(record accounts.ProxyRecord) SelectionAccount {
	account := SelectionAccount{
		ID:                     record.ID,
		Status:                 record.Status,
		PlanType:               record.PlanType,
		RoutingPolicy:          record.RoutingPolicy,
		SecurityWorkAuthorized: record.SecurityWorkAuthorized,
	}
	if record.ResetAt.Valid {
		value := record.ResetAt.Float64
		account.ResetAt = &value
	}
	if record.BlockedAt.Valid {
		value := record.BlockedAt.Float64
		account.BlockedAt = &value
	}
	if record.DeactivationReason.Valid {
		value := record.DeactivationReason.String
		account.DeactivationReason = &value
	}
	return account
}

func stateFromAccountInputs(settings settings.DashboardSettings) StateFromAccountInputs {
	in := DefaultStateFromAccountInputs()
	if settings.RoutingStrategy != "" {
		// routing strategy is applied at selection time, not state derivation
	}
	return in
}

func parseRecordedAtString(raw sql.NullString) *time.Time {
	if !raw.Valid {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, raw.String); err == nil {
		return &t
	}
	if t, err := time.Parse("2006-01-02 15:04:05.999999", raw.String); err == nil {
		return &t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", raw.String); err == nil {
		return &t
	}
	return nil
}

func nullInt64ToPtr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	value := v.Int64
	return &value
}

func nullFloat64ToPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	value := v.Float64
	return &value
}

func nullBoolToPtr(v sql.NullBool) *bool {
	if !v.Valid {
		return nil
	}
	value := v.Bool
	return &value
}
