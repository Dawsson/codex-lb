package accounts

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/soju06/codex-lb/internal/db"
)

type Repository struct {
	store *db.Store
}

type Account struct {
	ID                     string
	Email                  string
	Alias                  sql.NullString
	PlanType               string
	Status                 string
	RoutingPolicy          string
	SecurityWorkAuthorized bool
	WorkspaceID            sql.NullString
	WorkspaceLabel         sql.NullString
	SeatType               sql.NullString
	LimitWarmupEnabled     bool
}

type LatestUsage struct {
	AccountID      string
	Window         string
	UsedPercent    float64
	ResetAt        sql.NullInt64
	WindowMinutes  sql.NullInt64
	CreditsHas     sql.NullBool
	CreditsBalance sql.NullFloat64
	RecordedAt     sql.NullString
}

func NewRepository(store *db.Store) Repository {
	return Repository{store: store}
}

func (r Repository) List(ctx context.Context) ([]Account, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT id, email, alias, plan_type, status, routing_policy,
		       security_work_authorized, workspace_id, workspace_label, seat_type,
		       limit_warmup_enabled
		  FROM accounts
		 ORDER BY email, id
	`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()

	var accounts []Account
	for rows.Next() {
		var account Account
		if err := rows.Scan(
			&account.ID,
			&account.Email,
			&account.Alias,
			&account.PlanType,
			&account.Status,
			&account.RoutingPolicy,
			&account.SecurityWorkAuthorized,
			&account.WorkspaceID,
			&account.WorkspaceLabel,
			&account.SeatType,
			&account.LimitWarmupEnabled,
		); err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accounts: %w", err)
	}
	return accounts, nil
}

func (r Repository) LatestUsageByWindow(ctx context.Context, window string) (map[string]LatestUsage, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT account_id,
		       coalesce(window, 'primary') AS window_name,
		       used_percent,
		       reset_at,
		       window_minutes,
		       credits_has,
		       credits_balance,
		       recorded_at
		  FROM (
		        SELECT usage_history.*,
		               row_number() OVER (
		                 PARTITION BY account_id, coalesce(window, 'primary')
		                 ORDER BY recorded_at DESC, id DESC
		               ) AS rn
		          FROM usage_history
		         WHERE coalesce(window, 'primary') = ?
		       )
		 WHERE rn = 1
	`, window)
	if err != nil {
		return nil, fmt.Errorf("latest usage by window %s: %w", window, err)
	}
	defer rows.Close()

	result := make(map[string]LatestUsage)
	for rows.Next() {
		var usage LatestUsage
		if err := rows.Scan(
			&usage.AccountID,
			&usage.Window,
			&usage.UsedPercent,
			&usage.ResetAt,
			&usage.WindowMinutes,
			&usage.CreditsHas,
			&usage.CreditsBalance,
			&usage.RecordedAt,
		); err != nil {
			return nil, fmt.Errorf("scan latest usage: %w", err)
		}
		result[usage.AccountID] = usage
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest usage: %w", err)
	}
	return result, nil
}
