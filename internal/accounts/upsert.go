package accounts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrAccountIdentityConflict ports
// app.modules.accounts.repository.AccountIdentityConflictError: it is
// returned when more than one existing account row matches the email used
// as a fallback merge target.
var ErrAccountIdentityConflict = errors.New("multiple accounts match email")

const duplicateAccountSuffix = "__copy"

// OAuthAccount carries the fields persisted by the OAuth token-exchange
// flow. It mirrors the subset of app.db.models.Account written by
// OauthService._persist_tokens.
type OAuthAccount struct {
	ID                    string
	ChatGPTAccountID      sql.NullString
	Email                 string
	WorkspaceID           sql.NullString
	WorkspaceLabel        sql.NullString
	SeatType              sql.NullString
	PlanType              string
	AccessTokenEncrypted  []byte
	RefreshTokenEncrypted []byte
	IDTokenEncrypted      []byte
	LastRefresh           string
	Status                string
	DeactivationReason    sql.NullString
	ResetAt               sql.NullInt64
	BlockedAt             sql.NullInt64
}

// accountRow is the full set of columns read/written by the upsert paths.
type accountRow struct {
	ID                    string
	ChatGPTAccountID      sql.NullString
	Email                 string
	WorkspaceID           sql.NullString
	WorkspaceLabel        sql.NullString
	SeatType              sql.NullString
	PlanType              string
	AccessTokenEncrypted  []byte
	RefreshTokenEncrypted []byte
	IDTokenEncrypted      []byte
	LastRefresh           string
	Status                string
	DeactivationReason    sql.NullString
	ResetAt               sql.NullInt64
	BlockedAt             sql.NullInt64
	CreatedAt             string
}

const accountRowColumns = `
	id, chatgpt_account_id, email, workspace_id, workspace_label, seat_type,
	plan_type, access_token_encrypted, refresh_token_encrypted, id_token_encrypted,
	last_refresh, status, deactivation_reason, reset_at, blocked_at, created_at
`

func scanAccountRow(row *sql.Row) (accountRow, error) {
	var a accountRow
	err := row.Scan(
		&a.ID, &a.ChatGPTAccountID, &a.Email, &a.WorkspaceID, &a.WorkspaceLabel, &a.SeatType,
		&a.PlanType, &a.AccessTokenEncrypted, &a.RefreshTokenEncrypted, &a.IDTokenEncrypted,
		&a.LastRefresh, &a.Status, &a.DeactivationReason, &a.ResetAt, &a.BlockedAt, &a.CreatedAt,
	)
	if err != nil {
		return accountRow{}, err
	}
	return a, nil
}

// UpsertOAuthAccount ports the OAuth reauth path of
// app.modules.accounts.repository.AccountsRepository.upsert_reauthorized:
// when the incoming account has a chatgpt_account_id it merges by ChatGPT
// identity (upsert with merge_by_chatgpt_identity=True); otherwise it falls
// back to the slot-identity upsert (upsert_account_slot with
// preserve_unknown_workspace_duplicates=False).
//
// Cross-table duplicate reconciliation
// (_reconcile_chatgpt_identity_duplicates) is intentionally not ported: it
// merges usage/audit/session history across tables for legacy duplicate
// rows, which is an edge case outside the primary login/reauth path.
func (r Repository) UpsertOAuthAccount(ctx context.Context, account OAuthAccount) (Account, error) {
	tx, err := r.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return Account{}, fmt.Errorf("begin upsert oauth account: %w", err)
	}
	defer tx.Rollback()

	var result accountRow
	hasWorkspace := account.WorkspaceID.Valid && account.WorkspaceID.String != ""
	if account.ChatGPTAccountID.Valid && account.ChatGPTAccountID.String != "" && !hasWorkspace {
		result, err = upsertByChatGPTIdentity(ctx, tx, account)
	} else {
		result, err = upsertAccountSlot(ctx, tx, account)
	}
	if err != nil {
		return Account{}, err
	}

	if err := tx.Commit(); err != nil {
		return Account{}, fmt.Errorf("commit upsert oauth account: %w", err)
	}

	return Account{
		ID:             result.ID,
		Email:          result.Email,
		PlanType:       result.PlanType,
		Status:         result.Status,
		WorkspaceID:    result.WorkspaceID,
		WorkspaceLabel: result.WorkspaceLabel,
		SeatType:       result.SeatType,
	}, nil
}

// upsertByChatGPTIdentity ports the merge_by_chatgpt_identity=True branch of
// AccountsRepository.upsert.
func upsertByChatGPTIdentity(ctx context.Context, tx *sql.Tx, account OAuthAccount) (accountRow, error) {
	canonical, err := accountByChatGPTIdentity(ctx, tx, account.ChatGPTAccountID.String, account.WorkspaceID)
	if err != nil {
		return accountRow{}, err
	}
	if canonical != nil {
		return applyAccountUpdates(ctx, tx, *canonical, account)
	}

	existing, err := getAccountRow(ctx, tx, account.ID)
	if err != nil {
		return accountRow{}, err
	}
	if existing != nil {
		if isWorkspaceLessReauthForKnownSlot(*existing, account) {
			return applyAccountUpdates(ctx, tx, *existing, account)
		}
		account.ID, err = nextAvailableAccountID(ctx, tx, account.ID)
		if err != nil {
			return accountRow{}, err
		}
	}

	return insertAccount(ctx, tx, account)
}

// upsertAccountSlot ports
// AccountsRepository.upsert_account_slot(preserve_unknown_workspace_duplicates=False).
func upsertAccountSlot(ctx context.Context, tx *sql.Tx, account OAuthAccount) (accountRow, error) {
	existing, err := accountBySlotIdentity(ctx, tx, account)
	if err != nil {
		return accountRow{}, err
	}
	if existing != nil {
		return applyAccountUpdates(ctx, tx, *existing, account)
	}

	existingByID, err := getAccountRow(ctx, tx, account.ID)
	if err != nil {
		return accountRow{}, err
	}
	if existingByID != nil {
		if sameUnknownWorkspaceIdentity(*existingByID, account) {
			return applyAccountUpdates(ctx, tx, *existingByID, account)
		}
		account.ID, err = nextAvailableAccountID(ctx, tx, account.ID)
		if err != nil {
			return accountRow{}, err
		}
	} else {
		var existingByEmail *accountRow
		if account.WorkspaceID.Valid && account.WorkspaceID.String != "" {
			existingByEmail, err = singleUnknownWorkspaceAccountByEmail(ctx, tx, account.Email)
		} else {
			existingByEmail, err = singleAccountByEmail(ctx, tx, account.Email)
		}
		if err != nil {
			return accountRow{}, err
		}
		if existingByEmail != nil && !canReuseEmailFallback(*existingByEmail, account) {
			existingByEmail = nil
		}
		if existingByEmail != nil {
			return applyAccountUpdates(ctx, tx, *existingByEmail, account)
		}
	}

	return insertAccount(ctx, tx, account)
}

// accountByChatGPTIdentity ports
// AccountsRepository._account_by_chatgpt_identity.
func accountByChatGPTIdentity(ctx context.Context, tx *sql.Tx, chatgptAccountID string, workspaceID sql.NullString) (*accountRow, error) {
	var query string
	args := []any{chatgptAccountID}
	if workspaceID.Valid && workspaceID.String != "" {
		query = `SELECT ` + accountRowColumns + `
			FROM accounts
			WHERE chatgpt_account_id = ?
			  AND (workspace_id = ? OR workspace_id IS NULL)
			ORDER BY (workspace_id IS NULL) ASC, created_at ASC, id ASC
			LIMIT 1`
		args = append(args, workspaceID.String)
	} else {
		query = `SELECT ` + accountRowColumns + `
			FROM accounts
			WHERE chatgpt_account_id = ?
			  AND workspace_id IS NULL
			ORDER BY created_at ASC, id ASC
			LIMIT 1`
	}
	row, err := scanAccountRow(tx.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("account by chatgpt identity: %w", err)
	}
	return &row, nil
}

// accountBySlotIdentity ports AccountsRepository._account_by_slot_identity.
func accountBySlotIdentity(ctx context.Context, tx *sql.Tx, account OAuthAccount) (*accountRow, error) {
	if account.ChatGPTAccountID.Valid && account.ChatGPTAccountID.String != "" &&
		account.WorkspaceID.Valid && account.WorkspaceID.String != "" {
		row, err := scanAccountRow(tx.QueryRowContext(ctx, `SELECT `+accountRowColumns+`
			FROM accounts
			WHERE chatgpt_account_id = ? AND workspace_id = ?
			ORDER BY created_at ASC, id ASC
			LIMIT 1`, account.ChatGPTAccountID.String, account.WorkspaceID.String))
		if err == nil {
			return &row, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("account by slot identity: %w", err)
		}
	}

	if account.WorkspaceID.Valid && account.WorkspaceID.String != "" && account.Email != "" {
		row, err := scanAccountRow(tx.QueryRowContext(ctx, `SELECT `+accountRowColumns+`
			FROM accounts
			WHERE email = ? AND workspace_id = ?
			ORDER BY created_at ASC, id ASC
			LIMIT 1`, account.Email, account.WorkspaceID.String))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("account by slot identity (email+workspace): %w", err)
		}
		if canReuseEmailFallback(row, account) {
			return &row, nil
		}
	}

	return nil, nil
}

// singleAccountByEmail ports AccountsRepository._single_account_by_email.
func singleAccountByEmail(ctx context.Context, tx *sql.Tx, email string) (*accountRow, error) {
	rows, err := queryAccountRows(ctx, tx, `SELECT `+accountRowColumns+`
		FROM accounts WHERE email = ?
		ORDER BY created_at ASC, id ASC LIMIT 2`, email)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if len(rows) > 1 {
		return nil, ErrAccountIdentityConflict
	}
	return &rows[0], nil
}

// singleUnknownWorkspaceAccountByEmail ports
// AccountsRepository._single_unknown_workspace_account_by_email.
func singleUnknownWorkspaceAccountByEmail(ctx context.Context, tx *sql.Tx, email string) (*accountRow, error) {
	rows, err := queryAccountRows(ctx, tx, `SELECT `+accountRowColumns+`
		FROM accounts WHERE email = ? AND workspace_id IS NULL
		ORDER BY created_at ASC, id ASC LIMIT 2`, email)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if len(rows) > 1 {
		return nil, ErrAccountIdentityConflict
	}
	return &rows[0], nil
}

func queryAccountRows(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]accountRow, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query accounts: %w", err)
	}
	defer rows.Close()

	var result []accountRow
	for rows.Next() {
		var a accountRow
		if err := rows.Scan(
			&a.ID, &a.ChatGPTAccountID, &a.Email, &a.WorkspaceID, &a.WorkspaceLabel, &a.SeatType,
			&a.PlanType, &a.AccessTokenEncrypted, &a.RefreshTokenEncrypted, &a.IDTokenEncrypted,
			&a.LastRefresh, &a.Status, &a.DeactivationReason, &a.ResetAt, &a.BlockedAt, &a.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		result = append(result, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accounts: %w", err)
	}
	return result, nil
}

func getAccountRow(ctx context.Context, tx *sql.Tx, id string) (*accountRow, error) {
	row, err := scanAccountRow(tx.QueryRowContext(ctx, `SELECT `+accountRowColumns+` FROM accounts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get account by id: %w", err)
	}
	return &row, nil
}

// nextAvailableAccountID ports AccountsRepository._next_available_account_id.
func nextAvailableAccountID(ctx context.Context, tx *sql.Tx, baseID string) (string, error) {
	candidate := baseID
	sequence := 2
	for {
		existing, err := getAccountRow(ctx, tx, candidate)
		if err != nil {
			return "", err
		}
		if existing == nil {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s%s%d", baseID, duplicateAccountSuffix, sequence)
		sequence++
	}
}

// applyAccountUpdates ports _apply_account_updates, applied in-place via an
// UPDATE statement, and returns the resulting row.
func applyAccountUpdates(ctx context.Context, tx *sql.Tx, target accountRow, source OAuthAccount) (accountRow, error) {
	chatGPTAccountID := target.ChatGPTAccountID
	if source.ChatGPTAccountID.Valid {
		chatGPTAccountID = source.ChatGPTAccountID
	}

	workspaceID := target.WorkspaceID
	workspaceLabel := target.WorkspaceLabel
	seatType := target.SeatType
	if (source.WorkspaceID.Valid && source.WorkspaceID.String != "") || !target.WorkspaceID.Valid {
		workspaceID = source.WorkspaceID
		workspaceLabel = source.WorkspaceLabel
		seatType = source.SeatType
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE accounts
		   SET chatgpt_account_id = ?,
		       email = ?,
		       workspace_id = ?,
		       workspace_label = ?,
		       seat_type = ?,
		       plan_type = ?,
		       access_token_encrypted = ?,
		       refresh_token_encrypted = ?,
		       id_token_encrypted = ?,
		       last_refresh = ?,
		       status = ?,
		       deactivation_reason = ?,
		       reset_at = ?,
		       blocked_at = ?
		 WHERE id = ?
	`,
		chatGPTAccountID, source.Email, workspaceID, workspaceLabel, seatType,
		source.PlanType, source.AccessTokenEncrypted, source.RefreshTokenEncrypted, source.IDTokenEncrypted,
		source.LastRefresh, source.Status, source.DeactivationReason, source.ResetAt, source.BlockedAt,
		target.ID,
	); err != nil {
		return accountRow{}, fmt.Errorf("apply account updates: %w", err)
	}

	updated, err := getAccountRow(ctx, tx, target.ID)
	if err != nil {
		return accountRow{}, err
	}
	if updated == nil {
		return accountRow{}, fmt.Errorf("apply account updates: account %q vanished", target.ID)
	}
	return *updated, nil
}

// insertAccount inserts a brand-new account row for account.ID.
func insertAccount(ctx context.Context, tx *sql.Tx, account OAuthAccount) (accountRow, error) {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO accounts (
			id, chatgpt_account_id, email, workspace_id, workspace_label, seat_type,
			plan_type, access_token_encrypted, refresh_token_encrypted, id_token_encrypted,
			last_refresh, status, deactivation_reason, reset_at, blocked_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		account.ID, account.ChatGPTAccountID, account.Email, account.WorkspaceID, account.WorkspaceLabel, account.SeatType,
		account.PlanType, account.AccessTokenEncrypted, account.RefreshTokenEncrypted, account.IDTokenEncrypted,
		account.LastRefresh, account.Status, account.DeactivationReason, account.ResetAt, account.BlockedAt,
	); err != nil {
		return accountRow{}, fmt.Errorf("insert account: %w", err)
	}

	inserted, err := getAccountRow(ctx, tx, account.ID)
	if err != nil {
		return accountRow{}, err
	}
	if inserted == nil {
		return accountRow{}, fmt.Errorf("insert account: account %q not found after insert", account.ID)
	}
	return *inserted, nil
}

// sameUnknownWorkspaceIdentity ports _same_unknown_workspace_identity.
func sameUnknownWorkspaceIdentity(existing accountRow, incoming OAuthAccount) bool {
	return !existing.WorkspaceID.Valid &&
		(!incoming.WorkspaceID.Valid || incoming.WorkspaceID.String == "") &&
		existing.ChatGPTAccountID == incoming.ChatGPTAccountID &&
		existing.Email == incoming.Email
}

// isWorkspaceLessReauthForKnownSlot ports
// _is_workspace_less_reauth_for_known_slot for the
// merge_by_chatgpt_identity=True caller.
func isWorkspaceLessReauthForKnownSlot(existing accountRow, incoming OAuthAccount) bool {
	return existing.WorkspaceID.Valid && existing.WorkspaceID.String != "" &&
		(!incoming.WorkspaceID.Valid || incoming.WorkspaceID.String == "") &&
		incoming.ChatGPTAccountID.Valid && incoming.ChatGPTAccountID.String != "" &&
		existing.ChatGPTAccountID == incoming.ChatGPTAccountID &&
		existing.Email == incoming.Email
}

// canReuseEmailFallback ports _can_reuse_email_fallback.
func canReuseEmailFallback(existing accountRow, incoming OAuthAccount) bool {
	if !incoming.ChatGPTAccountID.Valid || incoming.ChatGPTAccountID.String == "" {
		return true
	}
	if !existing.ChatGPTAccountID.Valid || existing.ChatGPTAccountID.String == "" {
		return true
	}
	return existing.ChatGPTAccountID == incoming.ChatGPTAccountID
}
