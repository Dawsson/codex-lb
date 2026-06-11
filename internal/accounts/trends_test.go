package accounts

import (
	"database/sql"
	"testing"
	"time"
)

func TestBuildAccountTrendsMapsWeeklyPrimaryToSecondary(t *testing.T) {
	start := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	buckets := []UsageTrendBucket{
		{
			BucketEpoch:    start,
			Window:         "primary",
			AvgUsedPercent: 25,
			ResetAt:        sql.NullInt64{Int64: start + 7*24*3600, Valid: true},
			WindowMinutes:  sql.NullInt64{Int64: 7 * 24 * 60, Valid: true},
		},
	}

	trends := BuildAccountTrends(buckets, start, 3600, 2)

	if len(trends.Primary) != 0 {
		t.Fatalf("expected no primary trend points, got %#v", trends.Primary)
	}
	if len(trends.Secondary) != 2 || trends.Secondary[0].V != 75 || trends.Secondary[1].V != 75 {
		t.Fatalf("unexpected secondary trend: %#v", trends.Secondary)
	}
	if len(trends.SecondaryScheduled) != 2 || trends.SecondaryScheduled[0].V != 100 {
		t.Fatalf("unexpected scheduled secondary trend: %#v", trends.SecondaryScheduled)
	}
}

func TestBuildAccountTrendsSecondaryWinsWhenWeeklyPrimaryNotPreferred(t *testing.T) {
	start := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	buckets := []UsageTrendBucket{
		{
			BucketEpoch:    start,
			Window:         "primary",
			AvgUsedPercent: 25,
			ResetAt:        sql.NullInt64{Int64: start + 6*24*3600, Valid: true},
			WindowMinutes:  sql.NullInt64{Int64: 7 * 24 * 60, Valid: true},
		},
		{
			BucketEpoch:    start,
			Window:         "secondary",
			AvgUsedPercent: 40,
			ResetAt:        sql.NullInt64{Int64: start + 7*24*3600, Valid: true},
			WindowMinutes:  sql.NullInt64{Int64: 7 * 24 * 60, Valid: true},
		},
	}

	trends := BuildAccountTrends(buckets, start, 3600, 1)

	if len(trends.Primary) != 0 {
		t.Fatalf("expected weekly primary omitted from primary trend, got %#v", trends.Primary)
	}
	if len(trends.Secondary) != 1 || trends.Secondary[0].V != 60 {
		t.Fatalf("expected secondary row to win, got %#v", trends.Secondary)
	}
}
