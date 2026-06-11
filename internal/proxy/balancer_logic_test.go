package proxy

import (
	"testing"
)

func float64Ptr(v float64) *float64 { return &v }

func TestSelectAccountPicksLowestUsedPercent(t *testing.T) {
	states := []*AccountState{
		{AccountID: "a", Status: AccountStatusActive, UsedPercent: float64Ptr(50)},
		{AccountID: "b", Status: AccountStatusActive, UsedPercent: float64Ptr(10)},
	}
	result := SelectAccount(states, SelectAccountOptions{RoutingStrategy: RoutingStrategyUsageWeighted})
	if result.Account == nil || result.Account.AccountID != "b" {
		t.Fatalf("expected account b, got %+v", result.Account)
	}
}

func TestSelectAccountPrefersBurnFirstPolicy(t *testing.T) {
	states := []*AccountState{
		{AccountID: "normal", Status: AccountStatusActive, UsedPercent: float64Ptr(1), RoutingPolicy: RoutingPolicyNormal},
		{AccountID: "burn", Status: AccountStatusActive, UsedPercent: float64Ptr(90), RoutingPolicy: RoutingPolicyBurnFirst},
	}
	result := SelectAccount(states, SelectAccountOptions{RoutingStrategy: RoutingStrategyUsageWeighted})
	if result.Account == nil || result.Account.AccountID != "burn" {
		t.Fatalf("expected burn account, got %+v", result.Account)
	}
}

func TestSelectAccountPreserveFallsBackWhenNeeded(t *testing.T) {
	states := []*AccountState{
		{AccountID: "preserve", Status: AccountStatusActive, UsedPercent: float64Ptr(5), RoutingPolicy: RoutingPolicyPreserve},
	}
	result := SelectAccount(states, SelectAccountOptions{RoutingStrategy: RoutingStrategyUsageWeighted})
	if result.Account == nil || result.Account.AccountID != "preserve" {
		t.Fatalf("expected preserve account fallback, got %+v", result.Account)
	}
}

func TestApplyUsageQuotaSecondaryExhaustedWithoutCredits(t *testing.T) {
	hundred := 100.0
	secondaryReset := int64(1_700_000_000)
	status, used, reset := ApplyUsageQuota(
		AccountStatusActive,
		float64Ptr(10),
		nil,
		nil,
		nil,
		float64Ptr(100),
		&secondaryReset,
		nil,
		nil,
		nil,
	)
	if status != AccountStatusQuotaExceeded {
		t.Fatalf("expected quota_exceeded, got %s", status)
	}
	if used == nil || *used != hundred {
		t.Fatalf("expected used 100, got %+v", used)
	}
	if reset == nil || *reset != float64(secondaryReset) {
		t.Fatalf("expected reset %v, got %+v", secondaryReset, reset)
	}
}

func TestBackgroundRecoveryStateFromAccount(t *testing.T) {
	blockedAt := nowSeconds() - 500
	resetAt := blockedAt + 60
	account := &SelectionAccount{
		ID:        "a",
		Status:    AccountStatusRateLimited,
		PlanType:  "plus",
		BlockedAt: &blockedAt,
		ResetAt:   &resetAt,
	}
	state := BackgroundRecoveryStateFromAccount(account, nil, nil)
	if state.Status != AccountStatusRateLimited {
		t.Fatalf("expected rate_limited without fresh usage, got %s", state.Status)
	}
}

func TestValidateModelAccessRejectsDisallowedModel(t *testing.T) {
	key := &ApiKeyData{AllowedModels: []string{"gpt-5.4"}}
	if err := ValidateModelAccess(key, "gpt-5.3-codex"); err == nil {
		t.Fatal("expected model access error")
	}
}

func TestValidateModelAccessAllowsResolvedAlias(t *testing.T) {
	key := &ApiKeyData{AllowedModels: []string{"gpt-5.4"}}
	if err := ValidateModelAccess(key, "gpt-5.4-high"); err != nil {
		t.Fatalf("expected alias to resolve: %v", err)
	}
}

func TestSelectAccountPreferringBudgetSafeKeepsBurnFirst(t *testing.T) {
	states := []*AccountState{
		{AccountID: "burn", Status: AccountStatusActive, UsedPercent: float64Ptr(96), RoutingPolicy: RoutingPolicyBurnFirst},
		{AccountID: "normal", Status: AccountStatusActive, UsedPercent: float64Ptr(10), RoutingPolicy: RoutingPolicyNormal},
	}
	opts := DefaultSelectAccountPreferringBudgetSafeOptions()
	opts.BudgetThresholdPct = 95
	result := SelectAccountPreferringBudgetSafe(states, opts)
	if result.Account == nil || result.Account.AccountID != "burn" {
		t.Fatalf("expected burn-first account, got %+v", result.Account)
	}
}
