package proxy

import "testing"

func TestAdditionalQuotaKeyForSpark(t *testing.T) {
	registry := NewAdditionalQuotaRegistry()
	if got := registry.QuotaKeyForModel("gpt-5.3-codex-spark"); got != "codex_spark" {
		t.Fatalf("expected codex_spark, got %q", got)
	}
}

func TestFilterAccountsForAdditionalLimitSpark(t *testing.T) {
	registry := NewAdditionalQuotaRegistry()
	accounts := []SelectionAccount{
		{ID: "plus-account", PlanType: "plus", Status: AccountStatusActive},
		{ID: "pro-account", PlanType: "pro", Status: AccountStatusQuotaExceeded},
	}
	latestPrimary := map[string]*UsageEntry{
		"pro-account": {AccountID: "pro-account", UsedPercent: floatPtr(0)},
	}
	latestSecondary := map[string]*UsageEntry{
		"pro-account": {AccountID: "pro-account", UsedPercent: floatPtr(0)},
	}
	result := filterAccountsForAdditionalLimit(
		registry,
		accounts,
		"gpt-5.3-codex-spark",
		"codex_spark",
		true,
		latestPrimary,
		latestSecondary,
		latestPrimary,
		latestSecondary,
	)
	if len(result.Accounts) != 1 {
		t.Fatalf("expected one eligible account, got %d", len(result.Accounts))
	}
	if result.Accounts[0].ID != "pro-account" {
		t.Fatalf("expected pro account, got %q", result.Accounts[0].ID)
	}
}

func floatPtr(v float64) *float64 { return &v }
