package accounts

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"
)

const (
	sparklineDays       = 7
	detailBucketSeconds = 3600
)

type UsageTrendBucket struct {
	BucketEpoch    int64
	Window         string
	AvgUsedPercent float64
	ResetAt        sql.NullInt64
	WindowMinutes  sql.NullInt64
}

type TrendPoint struct {
	T string  `json:"t"`
	V float64 `json:"v"`
}

type TrendsResponse struct {
	AccountID          string       `json:"accountId"`
	Primary            []TrendPoint `json:"primary"`
	Secondary          []TrendPoint `json:"secondary"`
	SecondaryScheduled []TrendPoint `json:"secondaryScheduled"`
}

func (r Repository) Exists(ctx context.Context, accountID string) (bool, error) {
	var exists int
	err := r.store.DB().QueryRowContext(ctx, `SELECT 1 FROM accounts WHERE id = ? LIMIT 1`, accountID).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check account exists: %w", err)
	}
	return true, nil
}

func (r Repository) TrendsByBucket(ctx context.Context, accountID string, since time.Time, bucketSeconds int) ([]UsageTrendBucket, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		WITH base_rows AS (
			SELECT
				(cast(strftime('%s', recorded_at) AS integer) / ?) * ? AS bucket_epoch,
				id AS usage_id,
				coalesce(window, 'primary') AS window_name,
				used_percent,
				reset_at,
				window_minutes,
				recorded_at
			FROM usage_history
			WHERE recorded_at >= ?
			  AND account_id = ?
		),
		aggregate_rows AS (
			SELECT
				bucket_epoch,
				window_name,
				avg(used_percent) AS avg_used_percent,
				count(usage_id) AS samples,
				max(recorded_at) AS max_recorded_at
			FROM base_rows
			GROUP BY bucket_epoch, window_name
		),
		latest_ids AS (
			SELECT
				ar.bucket_epoch,
				ar.window_name,
				max(br.usage_id) AS usage_id
			FROM aggregate_rows ar
			JOIN base_rows br
			  ON br.bucket_epoch = ar.bucket_epoch
			 AND br.window_name = ar.window_name
			 AND br.recorded_at = ar.max_recorded_at
			GROUP BY ar.bucket_epoch, ar.window_name
		)
		SELECT
			ar.bucket_epoch,
			ar.window_name,
			ar.avg_used_percent,
			uh.reset_at,
			uh.window_minutes
		FROM aggregate_rows ar
		JOIN latest_ids li
		  ON li.bucket_epoch = ar.bucket_epoch
		 AND li.window_name = ar.window_name
		JOIN usage_history uh ON uh.id = li.usage_id
		ORDER BY ar.bucket_epoch ASC
	`, bucketSeconds, bucketSeconds, since.UTC().Format("2006-01-02 15:04:05"), accountID)
	if err != nil {
		return nil, fmt.Errorf("query account trends: %w", err)
	}
	defer rows.Close()

	var buckets []UsageTrendBucket
	for rows.Next() {
		var bucket UsageTrendBucket
		if err := rows.Scan(
			&bucket.BucketEpoch,
			&bucket.Window,
			&bucket.AvgUsedPercent,
			&bucket.ResetAt,
			&bucket.WindowMinutes,
		); err != nil {
			return nil, fmt.Errorf("scan account trend bucket: %w", err)
		}
		buckets = append(buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account trend buckets: %w", err)
	}
	return buckets, nil
}

func BuildAccountTrends(buckets []UsageTrendBucket, sinceEpoch int64, bucketSeconds int, bucketCount int) TrendsResponse {
	primaryData := map[int64]float64{}
	secondaryData := map[int64]float64{}
	secondarySchedule := map[int64]struct {
		resetAt       int64
		windowMinutes int64
	}{}

	for _, bucket := range effectiveUsageTrendBuckets(buckets) {
		window := bucket.Window
		if window == "monthly" || isWeeklyWindowMinutes(bucket.WindowMinutes) {
			window = "secondary"
		}
		switch window {
		case "primary":
			primaryData[bucket.BucketEpoch] = bucket.AvgUsedPercent
		case "secondary":
			secondaryData[bucket.BucketEpoch] = bucket.AvgUsedPercent
			if bucket.ResetAt.Valid && bucket.WindowMinutes.Valid {
				secondarySchedule[bucket.BucketEpoch] = struct {
					resetAt       int64
					windowMinutes int64
				}{
					resetAt:       bucket.ResetAt.Int64,
					windowMinutes: bucket.WindowMinutes.Int64,
				}
			}
		}
	}

	alignedStart := (sinceEpoch / int64(bucketSeconds)) * int64(bucketSeconds)
	timeGrid := make([]int64, bucketCount)
	for i := range bucketCount {
		timeGrid[i] = alignedStart + int64(i*bucketSeconds)
	}

	return TrendsResponse{
		Primary:            fillTrendPointsIfPresent(timeGrid, primaryData),
		Secondary:          fillTrendPointsIfPresent(timeGrid, secondaryData),
		SecondaryScheduled: fillScheduledSecondaryPoints(timeGrid, secondarySchedule),
	}
}

func fillTrendPointsIfPresent(timeGrid []int64, bucketData map[int64]float64) []TrendPoint {
	if len(bucketData) == 0 {
		return []TrendPoint{}
	}
	return fillTrendPoints(timeGrid, bucketData)
}

func effectiveUsageTrendBuckets(buckets []UsageTrendBucket) []UsageTrendBucket {
	secondaryByEpoch := map[int64]UsageTrendBucket{}
	weeklyPrimaryByEpoch := map[int64]UsageTrendBucket{}
	for _, bucket := range buckets {
		if bucket.Window == "secondary" {
			secondaryByEpoch[bucket.BucketEpoch] = bucket
		}
		if bucket.Window == "primary" && isWeeklyWindowMinutes(bucket.WindowMinutes) {
			weeklyPrimaryByEpoch[bucket.BucketEpoch] = bucket
		}
	}
	result := make([]UsageTrendBucket, 0, len(buckets))
	for _, bucket := range buckets {
		secondary, hasSecondary := secondaryByEpoch[bucket.BucketEpoch]
		weeklyPrimary, hasWeeklyPrimary := weeklyPrimaryByEpoch[bucket.BucketEpoch]
		if bucket.Window == "secondary" && hasWeeklyPrimary {
			if shouldUseWeeklyPrimaryTrend(weeklyPrimary, bucket) {
				continue
			}
		}
		if bucket.Window == "primary" && isWeeklyWindowMinutes(bucket.WindowMinutes) && hasSecondary {
			if !shouldUseWeeklyPrimaryTrend(bucket, secondary) {
				continue
			}
		}
		result = append(result, bucket)
	}
	return result
}

func isWeeklyWindowMinutes(value sql.NullInt64) bool {
	return value.Valid && value.Int64 >= 7*24*60
}

func shouldUseWeeklyPrimaryTrend(primary UsageTrendBucket, secondary UsageTrendBucket) bool {
	if !isWeeklyWindowMinutes(primary.WindowMinutes) {
		return false
	}
	if secondary.Window == "" {
		return true
	}
	if primary.ResetAt.Valid && secondary.ResetAt.Valid && primary.ResetAt.Int64 != secondary.ResetAt.Int64 {
		return primary.ResetAt.Int64 > secondary.ResetAt.Int64
	}
	return primary.AvgUsedPercent >= secondary.AvgUsedPercent
}

func fillTrendPoints(timeGrid []int64, bucketData map[int64]float64) []TrendPoint {
	points := make([]TrendPoint, 0, len(timeGrid))
	lastValue := 100.0
	for _, epoch := range timeGrid {
		remaining := lastValue
		if used, ok := bucketData[epoch]; ok {
			remaining = math.Max(0, math.Min(100, 100-used))
			lastValue = remaining
		}
		points = append(points, TrendPoint{
			T: time.Unix(epoch, 0).UTC().Format(time.RFC3339),
			V: math.Round(remaining*100) / 100,
		})
	}
	return points
}

func fillScheduledSecondaryPoints(
	timeGrid []int64,
	scheduleData map[int64]struct {
		resetAt       int64
		windowMinutes int64
	},
) []TrendPoint {
	points := make([]TrendPoint, 0, len(timeGrid))
	var currentResetAt int64
	var currentWindowMinutes int64
	hasSchedule := false

	for _, epoch := range timeGrid {
		if schedule, ok := scheduleData[epoch]; ok {
			currentResetAt = schedule.resetAt
			currentWindowMinutes = schedule.windowMinutes
			hasSchedule = true
		}
		if !hasSchedule || currentWindowMinutes == 0 {
			continue
		}
		windowSeconds := currentWindowMinutes * 60
		remainingSeconds := maxInt64(0, minInt64(windowSeconds, currentResetAt-epoch))
		scheduledRemaining := 100.0 * float64(remainingSeconds) / float64(windowSeconds)
		points = append(points, TrendPoint{
			T: time.Unix(epoch, 0).UTC().Format(time.RFC3339),
			V: math.Round(scheduledRemaining*100) / 100,
		})
	}
	return points
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
