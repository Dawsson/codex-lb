package requestlogs

import (
	"database/sql"
	"testing"
)

func TestNormalizeLogStatus(t *testing.T) {
	code := "rate_limit_exceeded"
	if got := NormalizeLogStatus("error", &code); got != "rate_limit" {
		t.Fatalf("expected rate_limit, got %q", got)
	}
	if got := NormalizeLogStatus("success", nil); got != "ok" {
		t.Fatalf("expected ok, got %q", got)
	}
	if got := NormalizeLogStatus("error", nil); got != "error" {
		t.Fatalf("expected error, got %q", got)
	}
}

func TestNormalizeRequestKind(t *testing.T) {
	cases := map[string]string{
		"chat_completion": "normal",
		"normal":          "normal",
		"warmup":          "warmup",
		"limit_warmup":    "limit_warmup",
	}
	for input, want := range cases {
		if got := NormalizeRequestKind(input); got != want {
			t.Fatalf("%q: expected %q, got %q", input, want, got)
		}
	}
}

func TestMapStatusFilter(t *testing.T) {
	all := MapStatusFilter([]string{"all"})
	if !all.IncludeSuccess || !all.IncludeErrorOther {
		t.Fatalf("all filter should include success and other errors")
	}
	okOnly := MapStatusFilter([]string{"ok"})
	if !okOnly.IncludeSuccess || okOnly.IncludeErrorOther {
		t.Fatalf("ok filter should only include success")
	}
	if len(okOnly.ErrorCodesIn) != 0 {
		t.Fatalf("ok filter should not include error codes")
	}
}

func TestMapEntryCostBreakdownCalculatesSegments(t *testing.T) {
	entry := Entry{
		RequestedAt:       sql.NullString{String: "2030-01-01 00:00:00", Valid: true},
		RequestID:         "req-1",
		Model:             "gpt-5.5",
		Status:            "success",
		ServiceTier:       sql.NullString{String: "priority", Valid: true},
		InputTokens:       sql.NullInt64{Int64: 1000, Valid: true},
		CachedInputTokens: sql.NullInt64{Int64: 250, Valid: true},
		OutputTokens:      sql.NullInt64{Int64: 100, Valid: true},
		CostUSD:           sql.NullFloat64{Float64: 0.017188, Valid: true},
	}
	mapped := mapEntry(entry)
	assertFloatPtr(t, mapped.CostUSD, 0.017188)
	assertCostFloat(t, mapped.CostBreakdown, "inputUsd", 0.009375)
	assertCostFloat(t, mapped.CostBreakdown, "cachedInputUsd", 0.000313)
	assertCostFloat(t, mapped.CostBreakdown, "outputUsd", 0.0075)
	assertCostFloat(t, mapped.CostBreakdown, "totalUsd", 0.017188)
}

func TestMapEntryCostBreakdownSuppressesSegmentsWhenPersistedTotalDiffers(t *testing.T) {
	entry := Entry{
		RequestedAt:       sql.NullString{String: "2030-01-01 00:00:00", Valid: true},
		RequestID:         "req-1",
		Model:             "gpt-5.5",
		Status:            "success",
		InputTokens:       sql.NullInt64{Int64: 1000, Valid: true},
		CachedInputTokens: sql.NullInt64{Int64: 250, Valid: true},
		OutputTokens:      sql.NullInt64{Int64: 100, Valid: true},
		CostUSD:           sql.NullFloat64{Float64: 9.0, Valid: true},
	}
	breakdown := mapEntry(entry).CostBreakdown
	assertCostNil(t, breakdown, "inputUsd")
	assertCostNil(t, breakdown, "cachedInputUsd")
	assertCostNil(t, breakdown, "outputUsd")
	assertCostFloat(t, breakdown, "totalUsd", 9.0)
}

func TestMapEntryCostBreakdownUsesReasoningTokensFallback(t *testing.T) {
	entry := Entry{
		RequestedAt:       sql.NullString{String: "2030-01-01 00:00:00", Valid: true},
		RequestID:         "req-1",
		Model:             "gpt-5.5",
		Status:            "success",
		InputTokens:       sql.NullInt64{Int64: 1000, Valid: true},
		CachedInputTokens: sql.NullInt64{Int64: 250, Valid: true},
		ReasoningTokens:   sql.NullInt64{Int64: 100, Valid: true},
	}
	mapped := mapEntry(entry)
	assertIntPtr(t, mapped.OutputTokens, 100)
	assertCostFloat(t, mapped.CostBreakdown, "outputUsd", 0.003)
	assertCostFloat(t, mapped.CostBreakdown, "totalUsd", 0.006875)
}

func TestMapEntryCostBreakdownPreservesPartialLegacyShape(t *testing.T) {
	entry := Entry{
		RequestedAt: sql.NullString{String: "2030-01-01 00:00:00", Valid: true},
		RequestID:   "req-1",
		Model:       "unknown-model",
		Status:      "success",
		InputTokens: sql.NullInt64{Int64: 1000, Valid: true},
	}
	breakdown := mapEntry(entry).CostBreakdown
	for _, key := range []string{"inputUsd", "cachedInputUsd", "outputUsd", "totalUsd"} {
		if _, ok := breakdown[key]; !ok {
			t.Fatalf("expected costBreakdown key %q", key)
		}
		assertCostNil(t, breakdown, key)
	}
}

func TestMapEntryExposesLatencyFirstTokenMS(t *testing.T) {
	entry := Entry{
		RequestedAt:         sql.NullString{String: "2030-01-01 00:00:00", Valid: true},
		RequestID:           "req-1",
		Model:               "gpt-5.5",
		Status:              "success",
		LatencyMS:           sql.NullInt64{Int64: 456, Valid: true},
		LatencyFirstTokenMS: sql.NullInt64{Int64: 123, Valid: true},
	}
	mapped := mapEntry(entry)
	assertIntPtr(t, mapped.LatencyMS, 456)
	assertIntPtr(t, mapped.LatencyFirstTokenMS, 123)
}

func assertFloatPtr(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func assertIntPtr(t *testing.T, got *int64, want int64) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func assertCostNil(t *testing.T, breakdown map[string]any, key string) {
	t.Helper()
	value := breakdown[key]
	if value == nil {
		return
	}
	if got, ok := value.(*float64); ok && got == nil {
		return
	}
	if value != nil {
		t.Fatalf("expected %s to be nil, got %#v", key, breakdown[key])
	}
}

func assertCostFloat(t *testing.T, breakdown map[string]any, key string, want float64) {
	t.Helper()
	got, ok := breakdown[key].(*float64)
	if !ok || got == nil || *got != want {
		t.Fatalf("expected %s=%v, got %#v", key, want, breakdown[key])
	}
}
