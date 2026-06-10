package reports

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/httputil"
)

type Repository struct {
	store *db.Store
}

type DailyRow struct {
	Date               string
	RequestCount       int
	ErrorCount         int
	InputTokens        int
	OutputTokens       int
	CachedInputTokens  int
	CostUSD            float64
	ActiveAccounts     int
}

type ModelRow struct {
	Model   string
	CostUSD float64
}

type AccountRow struct {
	AccountID    sql.NullString
	Alias        sql.NullString
	CostUSD      float64
	RequestCount int
}

type Params struct {
	StartDate   time.Time
	EndDate     time.Time
	AccountIDs  []string
	Model       string
}

func NewRepository(store *db.Store) Repository {
	return Repository{store: store}
}

func (r Repository) AggregateDaily(ctx context.Context, params Params) ([]DailyRow, error) {
	where, args := buildWhere(params)
	query := `
		SELECT strftime('%Y-%m-%d', requested_at) AS day,
		       COUNT(*) AS request_count,
		       COALESCE(SUM(CASE WHEN status != 'success' THEN 1 ELSE 0 END), 0) AS error_count,
		       COALESCE(SUM(COALESCE(input_tokens, 0)), 0) AS input_tokens,
		       COALESCE(SUM(COALESCE(output_tokens, 0)), 0) AS output_tokens,
		       COALESCE(SUM(COALESCE(cached_input_tokens, 0)), 0) AS cached_input_tokens,
		       COALESCE(SUM(COALESCE(cost_usd, 0)), 0) AS cost_usd,
		       COUNT(DISTINCT account_id) AS active_accounts
		  FROM request_logs
	` + where + `
		 GROUP BY day
		 ORDER BY day ASC
	`
	return scanDailyRows(r.store.DB().QueryContext(ctx, query, args...))
}

func (r Repository) AggregateByModel(ctx context.Context, params Params) ([]ModelRow, error) {
	where, args := buildWhere(params)
	query := `
		SELECT model,
		       COALESCE(SUM(COALESCE(cost_usd, 0)), 0) AS cost_usd
		  FROM request_logs
	` + where + `
		   AND model IS NOT NULL
		 GROUP BY model
		 ORDER BY cost_usd DESC
	`
	rows, err := r.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregate reports by model: %w", err)
	}
	defer rows.Close()
	var models []ModelRow
	for rows.Next() {
		var row ModelRow
		if err := rows.Scan(&row.Model, &row.CostUSD); err != nil {
			return nil, err
		}
		models = append(models, row)
	}
	return httputil.EmptySlice(models), rows.Err()
}

func (r Repository) AggregateByAccount(ctx context.Context, params Params) ([]AccountRow, error) {
	where, args := buildWhere(params)
	query := `
		SELECT rl.account_id,
		       COALESCE(SUM(COALESCE(rl.cost_usd, 0)), 0) AS cost_usd,
		       COUNT(*) AS request_count
		  FROM request_logs rl
	` + where + `
		 GROUP BY rl.account_id
		 ORDER BY cost_usd DESC
	`
	rows, err := r.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregate reports by account: %w", err)
	}
	defer rows.Close()

	type partial struct {
		AccountID    sql.NullString
		CostUSD      float64
		RequestCount int
	}
	var partials []partial
	var accountIDs []string
	for rows.Next() {
		var row partial
		if err := rows.Scan(&row.AccountID, &row.CostUSD, &row.RequestCount); err != nil {
			return nil, err
		}
		partials = append(partials, row)
		if row.AccountID.Valid {
			accountIDs = append(accountIDs, row.AccountID.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	aliasByID := map[string]sql.NullString{}
	if len(accountIDs) > 0 {
		placeholders := strings.Repeat("?,", len(accountIDs))
		placeholders = placeholders[:len(placeholders)-1]
		aliasRows, err := r.store.DB().QueryContext(ctx, `
			SELECT id, alias FROM accounts WHERE id IN (`+placeholders+`)
		`, stringArgs(accountIDs)...)
		if err != nil {
			return nil, err
		}
		defer aliasRows.Close()
		for aliasRows.Next() {
			var id string
			var alias sql.NullString
			if err := aliasRows.Scan(&id, &alias); err != nil {
				return nil, err
			}
			aliasByID[id] = alias
		}
		if err := aliasRows.Err(); err != nil {
			return nil, err
		}
	}

	result := make([]AccountRow, 0, len(partials))
	for _, row := range partials {
		accountRow := AccountRow{
			AccountID:    row.AccountID,
			CostUSD:      row.CostUSD,
			RequestCount: row.RequestCount,
		}
		if row.AccountID.Valid {
			accountRow.Alias = aliasByID[row.AccountID.String]
		}
		result = append(result, accountRow)
	}
	return httputil.EmptySlice(result), nil
}

func (r Repository) CountActiveAccounts(ctx context.Context, params Params) (int, error) {
	where, args := buildWhere(params)
	query := `
		SELECT COUNT(DISTINCT account_id)
		  FROM request_logs
	` + where + `
		   AND account_id IS NOT NULL
	`
	var count int
	if err := r.store.DB().QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active accounts: %w", err)
	}
	return count, nil
}

func buildWhere(params Params) (string, []any) {
	clauses := []string{
		"requested_at >= ?",
		"requested_at < ?",
		"(source IS NULL OR source != 'limit_warmup')",
		"(request_kind IS NULL OR request_kind NOT IN ('warmup', 'limit_warmup'))",
	}
	args := []any{
		params.StartDate.Format("2006-01-02 15:04:05"),
		params.EndDate.Format("2006-01-02 15:04:05"),
	}
	if len(params.AccountIDs) > 0 {
		placeholders := strings.Repeat("?,", len(params.AccountIDs))
		clauses = append(clauses, "account_id IN ("+placeholders[:len(placeholders)-1]+")")
		for _, id := range params.AccountIDs {
			args = append(args, id)
		}
	}
	if params.Model != "" {
		clauses = append(clauses, "model = ?")
		args = append(args, params.Model)
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func scanDailyRows(rows *sql.Rows, err error) ([]DailyRow, error) {
	if err != nil {
		return nil, fmt.Errorf("aggregate reports daily: %w", err)
	}
	defer rows.Close()
	var daily []DailyRow
	for rows.Next() {
		var row DailyRow
		if err := rows.Scan(
			&row.Date, &row.RequestCount, &row.ErrorCount, &row.InputTokens, &row.OutputTokens,
			&row.CachedInputTokens, &row.CostUSD, &row.ActiveAccounts,
		); err != nil {
			return nil, err
		}
		daily = append(daily, row)
	}
	return httputil.EmptySlice(daily), rows.Err()
}

func stringArgs(values []string) []any {
	args := make([]any, len(values))
	for i, value := range values {
		args[i] = value
	}
	return args
}
