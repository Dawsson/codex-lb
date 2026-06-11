package dashboard

import (
	"encoding/json"
	"testing"

	"github.com/soju06/codex-lb/internal/accounts"
)

func TestOverviewResponseIncludesNullableProjectionKeys(t *testing.T) {
	payload, err := json.Marshal(overviewResponse{AdditionalQuotas: []any{}})
	if err != nil {
		t.Fatalf("marshal overview: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if quotas, ok := decoded["additionalQuotas"].([]any); !ok || len(quotas) != 0 {
		t.Fatalf("expected additionalQuotas empty array, got %#v", decoded["additionalQuotas"])
	}
	for _, key := range []string{"depletionPrimary", "depletionSecondary", "weeklyCreditPace"} {
		value, ok := decoded[key]
		if !ok {
			t.Fatalf("expected key %q in overview JSON: %s", key, payload)
		}
		if value != nil {
			t.Fatalf("expected %s to be null, got %#v", key, value)
		}
	}
}

func TestProjectionsResponseIncludesNullableProjectionKeys(t *testing.T) {
	payload, err := json.Marshal(projectionsResponse{})
	if err != nil {
		t.Fatalf("marshal projections: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode projections: %v", err)
	}
	for _, key := range []string{"depletionPrimary", "depletionSecondary", "weeklyCreditPace"} {
		value, ok := decoded[key]
		if !ok {
			t.Fatalf("expected key %q in projections JSON: %s", key, payload)
		}
		if value != nil {
			t.Fatalf("expected %s to be null, got %#v", key, value)
		}
	}
}

func TestSortOverviewAccountsByPrimaryCapacity(t *testing.T) {
	low := 25.0
	high := 100.0
	zero := 0.0
	items := []accounts.AccountSummary{
		{AccountID: "nil"},
		{AccountID: "low", CapacityCreditsPrimary: &low},
		{AccountID: "zero", CapacityCreditsPrimary: &zero},
		{AccountID: "high", CapacityCreditsPrimary: &high},
	}

	sortOverviewAccounts(items)

	got := []string{items[0].AccountID, items[1].AccountID, items[2].AccountID, items[3].AccountID}
	want := []string{"high", "low", "nil", "zero"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected order %v, got %v", want, got)
		}
	}
}

func TestOverviewAdditionalQuotasRollsUpDistinctDescriptors(t *testing.T) {
	spark := "spark"
	sparkLabel := "Spark"
	mini := "mini"
	miniLabel := "Mini"
	items := []accounts.AccountSummary{
		{
			AccountID: "acct-1",
			AdditionalQuotas: []accounts.AdditionalQuotaSummary{
				{QuotaKey: &spark, LimitName: "spark_limit", MeteredFeature: "tokens", DisplayLabel: &sparkLabel, RoutingPolicy: "normal"},
				{QuotaKey: &mini, LimitName: "mini_limit", MeteredFeature: "tokens", DisplayLabel: &miniLabel, RoutingPolicy: "preserve"},
			},
		},
		{
			AccountID: "acct-2",
			AdditionalQuotas: []accounts.AdditionalQuotaSummary{
				{QuotaKey: &spark, LimitName: "spark_limit", MeteredFeature: "tokens", DisplayLabel: &sparkLabel, RoutingPolicy: "normal"},
			},
		},
	}

	rolled := overviewAdditionalQuotas(items)

	if len(rolled) != 2 {
		t.Fatalf("expected two distinct quotas, got %#v", rolled)
	}
	first, ok := rolled[0].(accounts.AdditionalQuotaSummary)
	if !ok {
		t.Fatalf("expected AdditionalQuotaSummary, got %#v", rolled[0])
	}
	if first.LimitName != "mini_limit" {
		t.Fatalf("expected deterministic key ordering with mini first, got %#v", rolled)
	}
}
