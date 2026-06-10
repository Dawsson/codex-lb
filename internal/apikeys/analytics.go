package apikeys

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/soju06/codex-lb/internal/httputil"
)

const (
	sparklineDays        = 7
	detailBucketSeconds  = 3600
)

type TrendBucket struct {
	BucketEpoch  int64
	TotalTokens  int
	TotalCostUSD float64
}

type AccountCost struct {
	AccountID sql.NullString
	Email     sql.NullString
	CostUSD   float64
	IsDeleted bool
}

type Usage7Day struct {
	TotalRequests     int
	TotalTokens       int
	CachedInputTokens int
	TotalCostUSD      float64
	AccountCosts      []AccountCost
}

func (r Repository) TrendsByKey(ctx context.Context, keyID string, since, until time.Time, bucketSeconds int) ([]TrendBucket, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT (CAST(strftime('%s', requested_at) AS INTEGER) / ?) * ? AS bucket_epoch,
		       COALESCE(SUM(COALESCE(input_tokens, 0)), 0) AS total_input_tokens,
		       COALESCE(SUM(COALESCE(output_tokens, reasoning_tokens, 0)), 0) AS total_output_tokens,
		       COALESCE(SUM(COALESCE(cost_usd, 0)), 0) AS total_cost_usd
		  FROM request_logs
		 WHERE api_key_id = ?
		   AND requested_at >= ?
		   AND requested_at < ?
		   AND request_kind NOT IN ('warmup', 'limit_warmup')
		 GROUP BY bucket_epoch
		 ORDER BY bucket_epoch ASC
	`, bucketSeconds, bucketSeconds, keyID, since.Format("2006-01-02 15:04:05"), until.Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, fmt.Errorf("api key trends: %w", err)
	}
	defer rows.Close()

	var buckets []TrendBucket
	for rows.Next() {
		var bucket TrendBucket
		var inputTokens, outputTokens int
		if err := rows.Scan(&bucket.BucketEpoch, &inputTokens, &outputTokens, &bucket.TotalCostUSD); err != nil {
			return nil, err
		}
		bucket.TotalTokens = inputTokens + outputTokens
		bucket.TotalCostUSD = math.Round(bucket.TotalCostUSD*1e6) / 1e6
		buckets = append(buckets, bucket)
	}
	return httputil.EmptySlice(buckets), rows.Err()
}

func (r Repository) Usage7Day(ctx context.Context, keyID string, since, until time.Time) (Usage7Day, error) {
	row := r.store.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) AS total_requests,
		       COALESCE(SUM(COALESCE(input_tokens, 0)), 0) AS total_input_tokens,
		       COALESCE(SUM(COALESCE(output_tokens, reasoning_tokens, 0)), 0) AS total_output_tokens,
		       COALESCE(SUM(COALESCE(cached_input_tokens, 0)), 0) AS cached_input_tokens,
		       COALESCE(SUM(COALESCE(cost_usd, 0)), 0) AS total_cost_usd
		  FROM request_logs
		 WHERE api_key_id = ?
		   AND requested_at >= ?
		   AND requested_at < ?
		   AND request_kind NOT IN ('warmup', 'limit_warmup')
	`, keyID, since.Format("2006-01-02 15:04:05"), until.Format("2006-01-02 15:04:05"))

	var usage Usage7Day
	var inputTokens, outputTokens, cachedInput int
	if err := row.Scan(&usage.TotalRequests, &inputTokens, &outputTokens, &cachedInput, &usage.TotalCostUSD); err != nil {
		return Usage7Day{}, fmt.Errorf("api key usage 7d totals: %w", err)
	}
	usage.TotalTokens = inputTokens + outputTokens
	if cachedInput > inputTokens {
		cachedInput = inputTokens
	}
	if cachedInput < 0 {
		cachedInput = 0
	}
	usage.CachedInputTokens = cachedInput
	usage.TotalCostUSD = math.Round(usage.TotalCostUSD*1e6) / 1e6

	accountRows, err := r.store.DB().QueryContext(ctx, `
		SELECT rl.account_id,
		       a.email,
		       CASE WHEN rl.deleted_at IS NOT NULL THEN 1 ELSE 0 END AS is_deleted,
		       COALESCE(SUM(COALESCE(rl.cost_usd, 0)), 0) AS cost_usd
		  FROM request_logs rl
		  LEFT JOIN accounts a ON a.id = rl.account_id
		 WHERE rl.api_key_id = ?
		   AND rl.requested_at >= ?
		   AND rl.requested_at < ?
		   AND rl.request_kind NOT IN ('warmup', 'limit_warmup')
		 GROUP BY rl.account_id, a.email, is_deleted
	`, keyID, since.Format("2006-01-02 15:04:05"), until.Format("2006-01-02 15:04:05"))
	if err != nil {
		return Usage7Day{}, fmt.Errorf("api key usage 7d by account: %w", err)
	}
	defer accountRows.Close()

	var deletedCost float64
	for accountRows.Next() {
		var account AccountCost
		var isDeleted int
		if err := accountRows.Scan(&account.AccountID, &account.Email, &isDeleted, &account.CostUSD); err != nil {
			return Usage7Day{}, err
		}
		account.CostUSD = math.Round(account.CostUSD*1e6) / 1e6
		if account.CostUSD <= 0 {
			continue
		}
		account.IsDeleted = isDeleted != 0
		if account.IsDeleted {
			deletedCost += account.CostUSD
			continue
		}
		usage.AccountCosts = append(usage.AccountCosts, account)
	}
	if err := accountRows.Err(); err != nil {
		return Usage7Day{}, err
	}
	if deletedCost > 0 {
		usage.AccountCosts = append(usage.AccountCosts, AccountCost{
			CostUSD:   math.Round(deletedCost*1e6) / 1e6,
			IsDeleted: true,
		})
	}
	usage.AccountCosts = httputil.EmptySlice(usage.AccountCosts)
	return usage, nil
}

func buildTrendPoints(buckets []TrendBucket, since, until time.Time, bucketSeconds int) ([]trendPointResponse, []trendPointResponse) {
	sinceUTC := since.UTC()
	untilUTC := until.UTC()
	if !untilUTC.After(sinceUTC) {
		return []trendPointResponse{}, []trendPointResponse{}
	}

	startEpoch := (sinceUTC.Unix() / int64(bucketSeconds)) * int64(bucketSeconds)
	endEpoch := ((untilUTC.Add(-time.Microsecond).Unix()) / int64(bucketSeconds)) * int64(bucketSeconds)
	bucketCount := ((endEpoch - startEpoch) / int64(bucketSeconds)) + 1

	costByBucket := map[int64]float64{}
	tokensByBucket := map[int64]int{}
	for _, bucket := range buckets {
		costByBucket[bucket.BucketEpoch] = bucket.TotalCostUSD
		tokensByBucket[bucket.BucketEpoch] = bucket.TotalTokens
	}

	costPoints := make([]trendPointResponse, 0, bucketCount)
	tokenPoints := make([]trendPointResponse, 0, bucketCount)
	for i := int64(0); i < bucketCount; i++ {
		epoch := startEpoch + i*int64(bucketSeconds)
		t := time.Unix(epoch, 0).UTC().Format(time.RFC3339Nano)
		costPoints = append(costPoints, trendPointResponse{
			T: t,
			V: math.Round(costByBucket[epoch]*1e6) / 1e6,
		})
		tokenPoints = append(tokenPoints, trendPointResponse{
			T: t,
			V: float64(tokensByBucket[epoch]),
		})
	}
	return costPoints, tokenPoints
}
