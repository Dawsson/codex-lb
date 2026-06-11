package accounts

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/soju06/codex-lb/internal/crypto"
)

// ProxyRecord is the persisted account row needed by proxy account selection.
type ProxyRecord struct {
	ID                     string
	ChatGPTAccountID       sql.NullString
	Email                  string
	WorkspaceID            sql.NullString
	WorkspaceLabel         sql.NullString
	SeatType               sql.NullString
	PlanType               string
	Status                 string
	RoutingPolicy          string
	SecurityWorkAuthorized bool
	ResetAt                sql.NullFloat64
	BlockedAt              sql.NullFloat64
	DeactivationReason     sql.NullString
	AccessTokenEncrypted   []byte
	RefreshTokenEncrypted  []byte
	IDTokenEncrypted       []byte
	LastRefresh            string
}

func (r Repository) GetProxyRecord(ctx context.Context, accountID string) (*ProxyRecord, error) {
	var record ProxyRecord
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT id, chatgpt_account_id, email, workspace_id, workspace_label, seat_type,
		       plan_type, status, routing_policy,
		       security_work_authorized, reset_at, blocked_at,
		       deactivation_reason, access_token_encrypted, refresh_token_encrypted,
		       id_token_encrypted, last_refresh
		  FROM accounts
		 WHERE id = ?
	`, accountID).Scan(
		&record.ID,
		&record.ChatGPTAccountID,
		&record.Email,
		&record.WorkspaceID,
		&record.WorkspaceLabel,
		&record.SeatType,
		&record.PlanType,
		&record.Status,
		&record.RoutingPolicy,
		&record.SecurityWorkAuthorized,
		&record.ResetAt,
		&record.BlockedAt,
		&record.DeactivationReason,
		&record.AccessTokenEncrypted,
		&record.RefreshTokenEncrypted,
		&record.IDTokenEncrypted,
		&record.LastRefresh,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get proxy account: %w", err)
	}
	return &record, nil
}

func (r Repository) ListProxyRecords(ctx context.Context) ([]ProxyRecord, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT id, chatgpt_account_id, email, workspace_id, workspace_label, seat_type,
		       plan_type, status, routing_policy,
		       security_work_authorized, reset_at, blocked_at,
		       deactivation_reason, access_token_encrypted, refresh_token_encrypted,
		       id_token_encrypted, last_refresh
		  FROM accounts
		 ORDER BY email, id
	`)
	if err != nil {
		return nil, fmt.Errorf("list proxy accounts: %w", err)
	}
	defer rows.Close()

	var records []ProxyRecord
	for rows.Next() {
		var record ProxyRecord
		if err := rows.Scan(
			&record.ID,
			&record.ChatGPTAccountID,
			&record.Email,
			&record.WorkspaceID,
			&record.WorkspaceLabel,
			&record.SeatType,
			&record.PlanType,
			&record.Status,
			&record.RoutingPolicy,
			&record.SecurityWorkAuthorized,
			&record.ResetAt,
			&record.BlockedAt,
			&record.DeactivationReason,
			&record.AccessTokenEncrypted,
			&record.RefreshTokenEncrypted,
			&record.IDTokenEncrypted,
			&record.LastRefresh,
		); err != nil {
			return nil, fmt.Errorf("scan proxy account: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proxy accounts: %w", err)
	}
	return records, nil
}

func (r Repository) DecryptAccessToken(encryptor *crypto.Encryptor, record ProxyRecord) (string, error) {
	if len(record.AccessTokenEncrypted) == 0 {
		return "", fmt.Errorf("account %s has no access token", record.ID)
	}
	return encryptor.Decrypt(record.AccessTokenEncrypted)
}

// LatestUsageEntries returns latest usage rows keyed by account id for one window.
func (r Repository) LatestUsageEntries(ctx context.Context, window string) (map[string]LatestUsage, error) {
	return r.LatestUsageByWindow(ctx, window)
}
