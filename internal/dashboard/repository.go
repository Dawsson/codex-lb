package dashboard

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/platform"
)

type Repository struct {
	store *db.Store
}

type ActivityAggregate struct {
	Requests          int64
	InputTokens       sql.NullInt64
	OutputTokens      sql.NullInt64
	CachedInputTokens sql.NullInt64
	Errors            int64
	TotalCostUSD      sql.NullFloat64
}

type TrendPoint struct {
	T            string
	Requests     int64
	Tokens       int64
	CachedTokens int64
	Errors       int64
	CostUSD      float64
}

type UsageHistoryRow struct {
	AccountID     string
	RecordedAt    string
	Window        string
	UsedPercent   float64
	ResetAt       sql.NullInt64
	WindowMinutes sql.NullInt64
}

const visibleRequestLogAggregateClause = "request_kind NOT IN ('warmup', 'limit_warmup')"

func NewRepository(store *db.Store) Repository {
	return Repository{store: store}
}

func (r Repository) AggregateActivitySince(ctx context.Context, since time.Time) (ActivityAggregate, error) {
	var aggregate ActivityAggregate
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT count(*),
		       sum(coalesce(input_tokens, 0)),
		       sum(coalesce(output_tokens, 0)),
		       sum(coalesce(cached_input_tokens, 0)),
		       sum(CASE WHEN lower(status) NOT IN ('ok', 'success', 'completed') THEN 1 ELSE 0 END),
		       sum(coalesce(cost_usd, 0))
		  FROM request_logs
		 WHERE deleted_at IS NULL
		   AND requested_at >= ?
		   AND `+visibleRequestLogAggregateClause+`
	`, since.UTC().Format("2006-01-02 15:04:05")).Scan(
		&aggregate.Requests,
		&aggregate.InputTokens,
		&aggregate.OutputTokens,
		&aggregate.CachedInputTokens,
		&aggregate.Errors,
		&aggregate.TotalCostUSD,
	)
	if err != nil {
		return ActivityAggregate{}, fmt.Errorf("aggregate activity: %w", err)
	}
	return aggregate, nil
}

func (r Repository) TopErrorSince(ctx context.Context, since time.Time) (*string, error) {
	var code sql.NullString
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT error_code
		  FROM request_logs
		 WHERE deleted_at IS NULL
		   AND requested_at >= ?
		   AND `+visibleRequestLogAggregateClause+`
		   AND status != 'success'
		   AND error_code IS NOT NULL
		   AND error_code != ''
		 GROUP BY error_code
		 ORDER BY count(*) DESC, error_code ASC
		 LIMIT 1
	`, since.UTC().Format("2006-01-02 15:04:05")).Scan(&code)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("top error: %w", err)
	}
	if !code.Valid {
		return nil, nil
	}
	return &code.String, nil
}

func (r Repository) Trends(ctx context.Context, since time.Time, bucketSeconds int) ([]TrendPoint, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT datetime((cast(strftime('%s', requested_at) AS integer) / ?) * ?, 'unixepoch') AS bucket,
		       count(*) AS requests,
		       sum(coalesce(input_tokens, 0) + coalesce(output_tokens, 0)) AS tokens,
		       sum(coalesce(cached_input_tokens, 0)) AS cached_tokens,
		       sum(CASE WHEN lower(status) NOT IN ('ok', 'success', 'completed') THEN 1 ELSE 0 END) AS errors,
		       sum(coalesce(cost_usd, 0)) AS cost_usd
		  FROM request_logs
		 WHERE deleted_at IS NULL
		   AND requested_at >= ?
		   AND `+visibleRequestLogAggregateClause+`
		 GROUP BY bucket
		 ORDER BY bucket ASC
	`, bucketSeconds, bucketSeconds, since.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, fmt.Errorf("query trends: %w", err)
	}
	defer rows.Close()

	var points []TrendPoint
	for rows.Next() {
		var point TrendPoint
		if err := rows.Scan(&point.T, &point.Requests, &point.Tokens, &point.CachedTokens, &point.Errors, &point.CostUSD); err != nil {
			return nil, fmt.Errorf("scan trend point: %w", err)
		}
		points = append(points, point)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trend points: %w", err)
	}
	return points, nil
}

func (r Repository) LatestSyncAt(ctx context.Context) (*string, error) {
	var recorded sql.NullString
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT max(recorded_at)
		  FROM (
			SELECT recorded_at FROM usage_history
			UNION ALL
			SELECT recorded_at FROM additional_usage_history
		  )
	`).Scan(&recorded)
	if err != nil {
		return nil, fmt.Errorf("latest sync at: %w", err)
	}
	return platform.SQLiteTimeToISO(recorded), nil
}

func (r Repository) UsageHistorySince(ctx context.Context, since time.Time) ([]UsageHistoryRow, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT account_id,
		       recorded_at,
		       coalesce(window, 'primary') AS window_name,
		       used_percent,
		       reset_at,
		       window_minutes
		  FROM usage_history
		 WHERE recorded_at >= ?
		 ORDER BY account_id, window_name, recorded_at ASC, id ASC
	`, since.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, fmt.Errorf("query usage history for depletion: %w", err)
	}
	defer rows.Close()
	var result []UsageHistoryRow
	for rows.Next() {
		var row UsageHistoryRow
		if err := rows.Scan(&row.AccountID, &row.RecordedAt, &row.Window, &row.UsedPercent, &row.ResetAt, &row.WindowMinutes); err != nil {
			return nil, fmt.Errorf("scan depletion usage history: %w", err)
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (r Repository) WeeklyPaceWorkingDays(ctx context.Context) (string, error) {
	var days sql.NullString
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT weekly_pace_working_days
		  FROM dashboard_settings
		 ORDER BY id
		 LIMIT 1
	`).Scan(&days)
	if err != nil {
		return "", fmt.Errorf("load weekly pace working days: %w", err)
	}
	if !days.Valid {
		return "", nil
	}
	return days.String, nil
}
