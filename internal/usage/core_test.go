package usage

import "testing"

func ptrFloat(v float64) *float64 { return &v }
func ptrInt(v int64) *int64       { return &v }

func TestCapacityForPlan(t *testing.T) {
	if got := capacityForPlan("pro", "primary"); got == nil || *got != 1500.0 {
		t.Fatalf("expected 1500 for pro/primary, got %v", got)
	}
	if got := capacityForPlan("free", "primary"); got == nil || *got != 0.0 {
		t.Fatalf("expected 0 for free/primary, got %v", got)
	}
	if got := capacityForPlan("free", "monthly"); got == nil || *got != 1134.0 {
		t.Fatalf("expected 1134 for free/monthly, got %v", got)
	}
	if got := capacityForPlan("plus", "monthly"); got != nil {
		t.Fatalf("expected nil capacity for plus/monthly, got %v", *got)
	}
	if got := capacityForPlan("unknown", "primary"); got != nil {
		t.Fatalf("expected nil capacity for unknown plan, got %v", *got)
	}
}

func TestRemainingPercentFromUsed(t *testing.T) {
	if got := remainingPercentFromUsed(nil); got != nil {
		t.Fatalf("expected nil, got %v", *got)
	}
	if got := remainingPercentFromUsed(ptrFloat(40)); got == nil || *got != 60 {
		t.Fatalf("expected 60, got %v", got)
	}
	if got := remainingPercentFromUsed(ptrFloat(150)); got == nil || *got != 0 {
		t.Fatalf("expected 0 (clamped), got %v", got)
	}
}

func TestRemainingCreditsFromPercent(t *testing.T) {
	capacity := ptrFloat(1000)
	if got := remainingCreditsFromPercent(ptrFloat(25), capacity); got == nil || *got != 750 {
		t.Fatalf("expected 750, got %v", got)
	}
	if got := remainingCreditsFromPercent(nil, capacity); got != nil {
		t.Fatalf("expected nil, got %v", *got)
	}
}

func TestResolveWindowMinutes(t *testing.T) {
	if got := resolveWindowMinutes("primary", nil); got == nil || *got != defaultWindowMinutesPrimary {
		t.Fatalf("expected default primary minutes, got %v", got)
	}
	rows := []WindowRow{{WindowMinutes: ptrInt(123)}}
	if got := resolveWindowMinutes("primary", rows); got == nil || *got != 123 {
		t.Fatalf("expected 123, got %v", got)
	}
	rows = []WindowRow{{WindowMinutes: ptrInt(123)}, {WindowMinutes: ptrInt(456)}}
	if got := resolveWindowMinutes("primary", rows); got == nil || *got != defaultWindowMinutesPrimary {
		t.Fatalf("expected default to win on multiple distinct values, got %v", got)
	}
}

func TestSummarizeUsageWindow(t *testing.T) {
	planByAccount := map[string]string{"acct-1": "pro", "acct-2": "plus"}
	rows := []WindowRow{
		{AccountID: "acct-1", UsedPercent: ptrFloat(50), WindowMinutes: ptrInt(300), ResetAt: ptrInt(1000)},
		{AccountID: "acct-2", UsedPercent: ptrFloat(20), WindowMinutes: ptrInt(300), ResetAt: ptrInt(2000)},
	}
	summary := summarizeUsageWindow(rows, planByAccount, "primary")

	wantCapacity := 1500.0 + 225.0
	if summary.CapacityCredits != wantCapacity {
		t.Fatalf("expected capacity %v, got %v", wantCapacity, summary.CapacityCredits)
	}
	wantUsed := 1500.0*0.5 + 225.0*0.2
	if summary.UsedCredits != wantUsed {
		t.Fatalf("expected used credits %v, got %v", wantUsed, summary.UsedCredits)
	}
	if summary.ResetAt == nil || *summary.ResetAt != 1000 {
		t.Fatalf("expected earliest reset_at 1000, got %v", summary.ResetAt)
	}
	if summary.WindowMinutes == nil || *summary.WindowMinutes != 300 {
		t.Fatalf("expected window minutes 300, got %v", summary.WindowMinutes)
	}
	if summary.UsedPercent == nil {
		t.Fatalf("expected non-nil used percent")
	}
	wantPercent := (wantUsed / wantCapacity) * 100.0
	if *summary.UsedPercent != wantPercent {
		t.Fatalf("expected used percent %v, got %v", wantPercent, *summary.UsedPercent)
	}
}

func TestNormalizeWeeklyOnlyRows(t *testing.T) {
	// Free-plan account reports only a weekly (10080-minute) primary row and
	// has no secondary row yet -- it should be moved into secondary.
	primary := []WindowRow{
		{AccountID: "acct-free", UsedPercent: ptrFloat(10), WindowMinutes: ptrInt(defaultWindowMinutesSecondary)},
		{AccountID: "acct-pro", UsedPercent: ptrFloat(20), WindowMinutes: ptrInt(defaultWindowMinutesPrimary)},
	}
	var secondary []WindowRow

	normalizedPrimary, normalizedSecondary := normalizeWeeklyOnlyRows(primary, secondary)

	if len(normalizedPrimary) != 1 || normalizedPrimary[0].AccountID != "acct-pro" {
		t.Fatalf("expected only acct-pro to remain in primary, got %#v", normalizedPrimary)
	}
	if len(normalizedSecondary) != 1 || normalizedSecondary[0].AccountID != "acct-free" {
		t.Fatalf("expected acct-free to be moved to secondary, got %#v", normalizedSecondary)
	}
}

func TestNormalizeAccountPlanType(t *testing.T) {
	if got := normalizeAccountPlanType(" Pro "); got != "pro" {
		t.Fatalf("expected pro, got %q", got)
	}
	if got := normalizeAccountPlanType("bogus"); got != "" {
		t.Fatalf("expected empty for unknown plan, got %q", got)
	}
}
