package dashboard

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/soju06/codex-lb/internal/db"
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
		   AND error_code IS NOT NULL
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
