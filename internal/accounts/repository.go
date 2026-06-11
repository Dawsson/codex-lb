package accounts

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

func (r Repository) IsZero() bool {
	return r.store == nil
}

type Account struct {
	ID                     string
	ChatGPTAccountID       sql.NullString
	Email                  string
	Alias                  sql.NullString
	PlanType               string
	Status                 string
	RoutingPolicy          string
	SecurityWorkAuthorized bool
	WorkspaceID            sql.NullString
	WorkspaceLabel         sql.NullString
	SeatType               sql.NullString
	LastRefresh            string
	DeactivationReason     sql.NullString
	ResetAt                sql.NullInt64
	BlockedAt              sql.NullInt64
	LimitWarmupEnabled     bool
	AccessTokenEncrypted   []byte
	RefreshTokenEncrypted  []byte
	IDTokenEncrypted       []byte
}

type LatestUsage struct {
	AccountID        string
	Window           string
	UsedPercent      float64
	ResetAt          sql.NullInt64
	WindowMinutes    sql.NullInt64
	CreditsHas       sql.NullBool
	CreditsUnlimited sql.NullBool
	CreditsBalance   sql.NullFloat64
	RecordedAt       sql.NullString
}

type RequestUsage struct {
	RequestCount      int64
	TotalTokens       int64
	CachedInputTokens int64
	TotalCostUSD      float64
	Errors            int64
}

type LimitWarmup struct {
	AccountID    string
	Window       string
	ResetAt      int64
	Status       string
	Model        string
	AttemptedAt  string
	CompletedAt  sql.NullString
	ErrorCode    sql.NullString
	ErrorMessage sql.NullString
}

type AdditionalQuotaWindow struct {
	UsedPercent   float64
	ResetAt       sql.NullInt64
	WindowMinutes sql.NullInt64
}

type AdditionalQuota struct {
	AccountID       string
	QuotaKey        sql.NullString
	LimitName       string
	MeteredFeature  string
	DisplayLabel    sql.NullString
	RoutingPolicy   sql.NullString
	PrimaryWindow   *AdditionalQuotaWindow
	SecondaryWindow *AdditionalQuotaWindow
}

type StatusCompareUpdate struct {
	AccountID                  string
	Status                     string
	DeactivationReason         sql.NullString
	ResetAt                    sql.NullInt64
	BlockedAt                  sql.NullInt64
	ExpectedStatus             string
	ExpectedDeactivationReason sql.NullString
	ExpectedResetAt            sql.NullInt64
	ExpectedBlockedAt          sql.NullInt64
}

type TokenUpdate struct {
	AccountID             string
	AccessTokenEncrypted  []byte
	RefreshTokenEncrypted []byte
	IDTokenEncrypted      []byte
	LastRefresh           string
	PlanType              string
	Email                 string
	ChatGPTAccountID      sql.NullString
	WorkspaceID           sql.NullString
	WorkspaceLabel        sql.NullString
	SeatType              sql.NullString
}

func NewRepository(store *db.Store) Repository {
	return Repository{store: store}
}

func (r Repository) Get(ctx context.Context, accountID string) (*Account, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT id, chatgpt_account_id, email, alias, plan_type, status, routing_policy,
		       security_work_authorized, workspace_id, workspace_label, seat_type,
		       last_refresh, deactivation_reason, reset_at, blocked_at,
		       limit_warmup_enabled, access_token_encrypted, refresh_token_encrypted,
		       id_token_encrypted
		  FROM accounts
		 WHERE id = ?
		 LIMIT 1
	`, accountID)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate account: %w", err)
		}
		return nil, nil
	}
	account, err := scanAccount(rows)
	if err != nil {
		return nil, fmt.Errorf("scan account: %w", err)
	}
	return &account, rows.Err()
}

func (r Repository) List(ctx context.Context) ([]Account, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT id, chatgpt_account_id, email, alias, plan_type, status, routing_policy,
		       security_work_authorized, workspace_id, workspace_label, seat_type,
		       last_refresh, deactivation_reason, reset_at, blocked_at,
		       limit_warmup_enabled, access_token_encrypted, refresh_token_encrypted,
		       id_token_encrypted
		  FROM accounts
		 ORDER BY email, id
	`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()

	var accounts []Account
	for rows.Next() {
		account, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accounts: %w", err)
	}
	return accounts, nil
}

func scanAccount(row interface{ Scan(...any) error }) (Account, error) {
	var account Account
	err := row.Scan(
		&account.ID,
		&account.ChatGPTAccountID,
		&account.Email,
		&account.Alias,
		&account.PlanType,
		&account.Status,
		&account.RoutingPolicy,
		&account.SecurityWorkAuthorized,
		&account.WorkspaceID,
		&account.WorkspaceLabel,
		&account.SeatType,
		&account.LastRefresh,
		&account.DeactivationReason,
		&account.ResetAt,
		&account.BlockedAt,
		&account.LimitWarmupEnabled,
		&account.AccessTokenEncrypted,
		&account.RefreshTokenEncrypted,
		&account.IDTokenEncrypted,
	)
	return account, err
}

func (r Repository) LatestUsageByWindow(ctx context.Context, window string) (map[string]LatestUsage, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT account_id,
		       coalesce(window, 'primary') AS window_name,
		       used_percent,
		       reset_at,
		       window_minutes,
		       credits_has,
		       credits_unlimited,
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
			&usage.CreditsUnlimited,
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

func (r Repository) RequestUsageSince(ctx context.Context, since string) (map[string]RequestUsage, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT account_id,
		       COUNT(*) AS request_count,
		       COALESCE(SUM(COALESCE(input_tokens, 0) + COALESCE(output_tokens, reasoning_tokens, 0)), 0) AS total_tokens,
		       COALESCE(SUM(min(COALESCE(cached_input_tokens, 0), COALESCE(input_tokens, COALESCE(cached_input_tokens, 0)))), 0) AS cached_input_tokens,
		       COALESCE(SUM(COALESCE(cost_usd, 0)), 0) AS total_cost_usd,
		       COALESCE(SUM(CASE WHEN status != 'success' THEN 1 ELSE 0 END), 0) AS errors
		  FROM request_logs
		 WHERE account_id IS NOT NULL
		   AND requested_at >= ?
		   AND request_kind NOT IN ('warmup', 'limit_warmup')
		   AND deleted_at IS NULL
		 GROUP BY account_id
	`, since)
	if err != nil {
		return nil, fmt.Errorf("account request usage: %w", err)
	}
	defer rows.Close()
	result := map[string]RequestUsage{}
	for rows.Next() {
		var accountID string
		var usage RequestUsage
		if err := rows.Scan(
			&accountID,
			&usage.RequestCount,
			&usage.TotalTokens,
			&usage.CachedInputTokens,
			&usage.TotalCostUSD,
			&usage.Errors,
		); err != nil {
			return nil, fmt.Errorf("scan account request usage: %w", err)
		}
		result[accountID] = usage
	}
	return result, rows.Err()
}

func (r Repository) LatestLimitWarmups(ctx context.Context) (map[string]LimitWarmup, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT account_id, window, reset_at, status, model, attempted_at,
		       completed_at, error_code, error_message
		  FROM (
		        SELECT account_limit_warmups.*,
		               row_number() OVER (
		                 PARTITION BY account_id
		                 ORDER BY attempted_at DESC, id DESC
		               ) AS rn
		          FROM account_limit_warmups
		       )
		 WHERE rn = 1
	`)
	if err != nil {
		return nil, fmt.Errorf("latest limit warmups: %w", err)
	}
	defer rows.Close()
	result := map[string]LimitWarmup{}
	for rows.Next() {
		var row LimitWarmup
		if err := rows.Scan(
			&row.AccountID,
			&row.Window,
			&row.ResetAt,
			&row.Status,
			&row.Model,
			&row.AttemptedAt,
			&row.CompletedAt,
			&row.ErrorCode,
			&row.ErrorMessage,
		); err != nil {
			return nil, fmt.Errorf("scan limit warmup: %w", err)
		}
		result[row.AccountID] = row
	}
	return result, rows.Err()
}

func (r Repository) LatestAdditionalQuotas(ctx context.Context) (map[string][]AdditionalQuota, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT account_id, quota_key, limit_name, metered_feature, window,
		       used_percent, reset_at, window_minutes
		  FROM (
		        SELECT additional_usage_history.*,
		               row_number() OVER (
		                 PARTITION BY account_id, quota_key, window
		                 ORDER BY recorded_at DESC, id DESC
		               ) AS rn
		          FROM additional_usage_history
		       )
		 WHERE rn = 1
		 ORDER BY account_id, quota_key, window
	`)
	if err != nil {
		return nil, fmt.Errorf("latest additional quotas: %w", err)
	}
	defer rows.Close()
	byAccountAndKey := map[string]map[string]*AdditionalQuota{}
	for rows.Next() {
		var accountID, limitName, meteredFeature, window string
		var quotaKey sql.NullString
		var usedPercent float64
		var resetAt, windowMinutes sql.NullInt64
		if err := rows.Scan(
			&accountID,
			&quotaKey,
			&limitName,
			&meteredFeature,
			&window,
			&usedPercent,
			&resetAt,
			&windowMinutes,
		); err != nil {
			return nil, fmt.Errorf("scan additional quota: %w", err)
		}
		key := quotaKey.String
		if key == "" {
			key = limitName + ":" + meteredFeature
		}
		if byAccountAndKey[accountID] == nil {
			byAccountAndKey[accountID] = map[string]*AdditionalQuota{}
		}
		quota := byAccountAndKey[accountID][key]
		if quota == nil {
			quota = &AdditionalQuota{
				AccountID:      accountID,
				QuotaKey:       quotaKey,
				LimitName:      limitName,
				MeteredFeature: meteredFeature,
			}
			byAccountAndKey[accountID][key] = quota
		}
		windowData := &AdditionalQuotaWindow{UsedPercent: usedPercent, ResetAt: resetAt, WindowMinutes: windowMinutes}
		switch window {
		case "secondary":
			quota.SecondaryWindow = windowData
		default:
			quota.PrimaryWindow = windowData
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := map[string][]AdditionalQuota{}
	for accountID, quotas := range byAccountAndKey {
		for _, quota := range quotas {
			result[accountID] = append(result[accountID], *quota)
		}
	}
	return result, nil
}

func (r Repository) UpdateStatusIfCurrent(ctx context.Context, update StatusCompareUpdate) (bool, error) {
	result, err := r.store.DB().ExecContext(ctx, `
		UPDATE accounts
		   SET status = ?,
		       deactivation_reason = ?,
		       reset_at = ?,
		       blocked_at = ?
		 WHERE id = ?
		   AND status = ?
		   AND ((deactivation_reason IS NULL AND ? IS NULL) OR deactivation_reason = ?)
		   AND ((reset_at IS NULL AND ? IS NULL) OR reset_at = ?)
		   AND ((blocked_at IS NULL AND ? IS NULL) OR blocked_at = ?)
	`,
		update.Status,
		update.DeactivationReason,
		update.ResetAt,
		update.BlockedAt,
		update.AccountID,
		update.ExpectedStatus,
		update.ExpectedDeactivationReason,
		update.ExpectedDeactivationReason,
		update.ExpectedResetAt,
		update.ExpectedResetAt,
		update.ExpectedBlockedAt,
		update.ExpectedBlockedAt,
	)
	if err != nil {
		return false, fmt.Errorf("update account status if current: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("account status rows affected: %w", err)
	}
	return rows > 0, nil
}

func (r Repository) UpdateStatus(ctx context.Context, accountID, status string, reason sql.NullString) (bool, error) {
	result, err := r.store.DB().ExecContext(ctx, `
		UPDATE accounts
		   SET status = ?, deactivation_reason = ?, reset_at = NULL, blocked_at = NULL
		 WHERE id = ?
	`, status, reason, accountID)
	if err != nil {
		return false, fmt.Errorf("update account status: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("account status rows affected: %w", err)
	}
	return rows > 0, nil
}

func (r Repository) UpdateTokens(ctx context.Context, update TokenUpdate) (bool, error) {
	if update.LastRefresh == "" {
		update.LastRefresh = time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	result, err := r.store.DB().ExecContext(ctx, `
		UPDATE accounts
		   SET access_token_encrypted = ?,
		       refresh_token_encrypted = ?,
		       id_token_encrypted = ?,
		       last_refresh = ?,
		       plan_type = ?,
		       email = ?,
		       chatgpt_account_id = ?,
		       workspace_id = ?,
		       workspace_label = ?,
		       seat_type = ?,
		       status = 'active',
		       deactivation_reason = NULL,
		       reset_at = NULL,
		       blocked_at = NULL
		 WHERE id = ?
	`,
		update.AccessTokenEncrypted,
		update.RefreshTokenEncrypted,
		update.IDTokenEncrypted,
		update.LastRefresh,
		update.PlanType,
		update.Email,
		update.ChatGPTAccountID,
		update.WorkspaceID,
		update.WorkspaceLabel,
		update.SeatType,
		update.AccountID,
	)
	if err != nil {
		return false, fmt.Errorf("update account tokens: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("account token rows affected: %w", err)
	}
	return rows > 0, nil
}

func (r Repository) UpdateSecurityWorkAuthorized(ctx context.Context, accountID string, enabled bool) (bool, error) {
	return r.updateAccountField(ctx, accountID, `security_work_authorized = ?`, enabled)
}

func (r Repository) UpdateAlias(ctx context.Context, accountID string, alias sql.NullString) (bool, error) {
	return r.updateAccountField(ctx, accountID, `alias = ?`, alias)
}

func (r Repository) UpdateLimitWarmupEnabled(ctx context.Context, accountID string, enabled bool) (bool, error) {
	return r.updateAccountField(ctx, accountID, `limit_warmup_enabled = ?`, enabled)
}

func (r Repository) UpdateRoutingPolicy(ctx context.Context, accountID string, routingPolicy string) (bool, error) {
	return r.updateAccountField(ctx, accountID, `routing_policy = ?`, routingPolicy)
}

func (r Repository) Delete(ctx context.Context, accountID string, deleteHistory bool) (bool, error) {
	tx, err := r.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin delete account: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM usage_history WHERE account_id = ?`, accountID); err != nil {
		return false, fmt.Errorf("delete usage history: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM additional_usage_history WHERE account_id = ?`, accountID); err != nil {
		return false, fmt.Errorf("delete additional usage history: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_limit_warmups WHERE account_id = ?`, accountID); err != nil {
		return false, fmt.Errorf("delete limit warmups: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sticky_sessions WHERE account_id = ?`, accountID); err != nil {
		return false, fmt.Errorf("delete sticky sessions: %w", err)
	}
	if deleteHistory {
		if _, err := tx.ExecContext(ctx, `DELETE FROM request_logs WHERE account_id = ?`, accountID); err != nil {
			return false, fmt.Errorf("delete request logs: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE request_logs SET account_id = NULL, deleted_at = CURRENT_TIMESTAMP WHERE account_id = ?`, accountID); err != nil {
			return false, fmt.Errorf("soft-delete request log account references: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, accountID)
	if err != nil {
		return false, fmt.Errorf("delete account: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete account rows affected: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit delete account: %w", err)
	}
	return rows > 0, nil
}

func (r Repository) updateAccountField(ctx context.Context, accountID string, assignment string, value any) (bool, error) {
	result, err := r.store.DB().ExecContext(ctx, `UPDATE accounts SET `+assignment+` WHERE id = ?`, value, accountID)
	if err != nil {
		return false, fmt.Errorf("update account field: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("account field rows affected: %w", err)
	}
	return rows > 0, nil
}
