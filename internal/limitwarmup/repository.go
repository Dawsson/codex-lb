package limitwarmup

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/soju06/codex-lb/internal/db"
)

type Repository struct {
	store *db.Store
}

func NewRepository(store *db.Store) Repository {
	return Repository{store: store}
}

// Attempt mirrors a row in account_limit_warmups.
type Attempt struct {
	ID           int64
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

const attemptColumns = `
	id, account_id, window, reset_at, status, model, attempted_at,
	completed_at, error_code, error_message
`

func scanAttempt(row interface{ Scan(...any) error }) (Attempt, error) {
	var attempt Attempt
	if err := row.Scan(
		&attempt.ID, &attempt.AccountID, &attempt.Window, &attempt.ResetAt, &attempt.Status, &attempt.Model, &attempt.AttemptedAt,
		&attempt.CompletedAt, &attempt.ErrorCode, &attempt.ErrorMessage,
	); err != nil {
		return Attempt{}, err
	}
	return attempt, nil
}

// LatestByAccount returns the most recent warmup attempt per account.
func (r Repository) LatestByAccount(ctx context.Context, accountIDs []string) (map[string]Attempt, error) {
	if len(accountIDs) == 0 {
		return map[string]Attempt{}, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(accountIDs)), ",")
	args := make([]any, len(accountIDs))
	for i, id := range accountIDs {
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT %s
		  FROM account_limit_warmups
		 WHERE account_id IN (%s)
		 ORDER BY account_id, attempted_at DESC, id DESC
	`, attemptColumns, placeholders)

	rows, err := r.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list limit warmup attempts: %w", err)
	}
	defer rows.Close()

	result := make(map[string]Attempt, len(accountIDs))
	for rows.Next() {
		attempt, err := scanAttempt(rows)
		if err != nil {
			return nil, fmt.Errorf("scan limit warmup attempt: %w", err)
		}
		if _, seen := result[attempt.AccountID]; !seen {
			result[attempt.AccountID] = attempt
		}
	}
	return result, rows.Err()
}

// LatestAttemptForAccount returns the most recent warmup attempt for a single account, if any.
func (r Repository) LatestAttemptForAccount(ctx context.Context, accountID string) (Attempt, bool, error) {
	query := fmt.Sprintf(`
		SELECT %s
		  FROM account_limit_warmups
		 WHERE account_id = ?
		 ORDER BY attempted_at DESC, id DESC
		 LIMIT 1
	`, attemptColumns)

	row := r.store.DB().QueryRowContext(ctx, query, accountID)
	attempt, err := scanAttempt(row)
	if err == sql.ErrNoRows {
		return Attempt{}, false, nil
	}
	if err != nil {
		return Attempt{}, false, fmt.Errorf("scan latest limit warmup attempt: %w", err)
	}
	return attempt, true, nil
}

// TryCreateAttempt inserts a new pending warmup attempt unless one already exists for the
// given account/window/reset_at combination. Returns ok=false if an attempt already exists.
func (r Repository) TryCreateAttempt(ctx context.Context, accountID, window string, resetAt int64, model, attemptedAt string) (Attempt, bool, error) {
	if attemptedAt == "" {
		attemptedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	result, err := r.store.DB().ExecContext(ctx, `
		INSERT INTO account_limit_warmups (account_id, window, reset_at, status, model, attempted_at)
		VALUES (?, ?, ?, 'pending', ?, ?)
	`, accountID, window, resetAt, model, attemptedAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return Attempt{}, false, nil
		}
		return Attempt{}, false, fmt.Errorf("insert limit warmup attempt: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Attempt{}, false, fmt.Errorf("limit warmup attempt last insert id: %w", err)
	}
	return Attempt{
		ID:          id,
		AccountID:   accountID,
		Window:      window,
		ResetAt:     resetAt,
		Status:      "pending",
		Model:       model,
		AttemptedAt: attemptedAt,
	}, true, nil
}

// CompleteAttempt updates a warmup attempt with its final status.
func (r Repository) CompleteAttempt(ctx context.Context, attemptID int64, status, completedAt string, errorCode, errorMessage sql.NullString) (Attempt, bool, error) {
	if completedAt == "" {
		completedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	result, err := r.store.DB().ExecContext(ctx, `
		UPDATE account_limit_warmups
		   SET status = ?, completed_at = ?, error_code = ?, error_message = ?, updated_at = ?
		 WHERE id = ?
	`, status, completedAt, errorCode, errorMessage, completedAt, attemptID)
	if err != nil {
		return Attempt{}, false, fmt.Errorf("update limit warmup attempt: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Attempt{}, false, fmt.Errorf("limit warmup attempt rows affected: %w", err)
	}
	if rows == 0 {
		return Attempt{}, false, nil
	}

	row := r.store.DB().QueryRowContext(ctx, fmt.Sprintf(`
		SELECT %s FROM account_limit_warmups WHERE id = ?
	`, attemptColumns), attemptID)
	attempt, err := scanAttempt(row)
	if err != nil {
		return Attempt{}, false, fmt.Errorf("scan completed limit warmup attempt: %w", err)
	}
	return attempt, true, nil
}
