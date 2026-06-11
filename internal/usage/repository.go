package usage

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

func NewRepository(store *db.Store) Repository {
	return Repository{store: store}
}

// Entry mirrors a row in usage_history.
type Entry struct {
	ID               int64
	AccountID        string
	RecordedAt       string
	Window           sql.NullString
	UsedPercent      float64
	InputTokens      sql.NullInt64
	OutputTokens     sql.NullInt64
	ResetAt          sql.NullInt64
	WindowMinutes    sql.NullInt64
	CreditsHas       sql.NullBool
	CreditsUnlimited sql.NullBool
	CreditsBalance   sql.NullFloat64
}

// AdditionalEntry mirrors a row in additional_usage_history.
type AdditionalEntry struct {
	ID             int64
	AccountID      string
	QuotaKey       string
	LimitName      string
	MeteredFeature string
	Window         string
	UsedPercent    float64
	ResetAt        sql.NullInt64
	WindowMinutes  sql.NullInt64
	RecordedAt     string
}

// AggregateRow summarizes usage_history rows for one account since a point in time.
type AggregateRow struct {
	AccountID        string
	UsedPercentAvg   sql.NullFloat64
	InputTokensSum   sql.NullInt64
	OutputTokensSum  sql.NullInt64
	Samples          int64
	LastRecordedAt   string
	ResetAtMax       sql.NullInt64
	WindowMinutesMax sql.NullInt64
}

const usageHistoryColumns = `
	id, account_id, recorded_at, window, used_percent,
	input_tokens, output_tokens, reset_at, window_minutes,
	credits_has, credits_unlimited, credits_balance
`

func windowClause(window string) (string, []any) {
	if window == "" || window == "primary" {
		return `coalesce(window, 'primary') = 'primary'`, nil
	}
	return `window = ?`, []any{window}
}

func scanEntry(row interface{ Scan(...any) error }) (Entry, error) {
	var entry Entry
	if err := row.Scan(
		&entry.ID, &entry.AccountID, &entry.RecordedAt, &entry.Window, &entry.UsedPercent,
		&entry.InputTokens, &entry.OutputTokens, &entry.ResetAt, &entry.WindowMinutes,
		&entry.CreditsHas, &entry.CreditsUnlimited, &entry.CreditsBalance,
	); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

// LatestByAccount returns the most recent usage_history entry per account for the given window.
// If accountIDs is empty, all accounts are considered.
func (r Repository) LatestByAccount(ctx context.Context, window string, accountIDs []string) (map[string]Entry, error) {
	whereWindow, windowArgs := windowClause(window)

	var accounts []string
	if len(accountIDs) > 0 {
		accounts = accountIDs
	} else {
		rows, err := r.store.DB().QueryContext(ctx, `SELECT id FROM accounts`)
		if err != nil {
			return nil, fmt.Errorf("list accounts: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, fmt.Errorf("scan account id: %w", err)
			}
			accounts = append(accounts, id)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	query := fmt.Sprintf(`
		SELECT %s
		  FROM usage_history
		 WHERE account_id = ?
		   AND %s
		 ORDER BY recorded_at DESC, id DESC
		 LIMIT 1
	`, usageHistoryColumns, whereWindow)

	result := make(map[string]Entry, len(accounts))
	for _, accountID := range accounts {
		args := append([]any{accountID}, windowArgs...)
		row := r.store.DB().QueryRowContext(ctx, query, args...)
		entry, err := scanEntry(row)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("scan latest usage history for %s: %w", accountID, err)
		}
		result[accountID] = entry
	}
	return result, nil
}

// AggregateSince returns per-account usage aggregates for entries recorded since the given time.
func (r Repository) AggregateSince(ctx context.Context, since string, window string) ([]AggregateRow, error) {
	conditions := []string{"recorded_at >= ?"}
	args := []any{since}
	if window != "" {
		clause, windowArgs := windowClause(window)
		conditions = append(conditions, clause)
		args = append(args, windowArgs...)
	}

	query := fmt.Sprintf(`
		SELECT account_id,
		       avg(used_percent) AS used_percent_avg,
		       sum(input_tokens) AS input_tokens_sum,
		       sum(output_tokens) AS output_tokens_sum,
		       count(id) AS samples,
		       max(recorded_at) AS last_recorded_at,
		       max(reset_at) AS reset_at_max,
		       max(window_minutes) AS window_minutes_max
		  FROM usage_history
		 WHERE %s
		 GROUP BY account_id
	`, strings.Join(conditions, " AND "))

	rows, err := r.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregate usage history: %w", err)
	}
	defer rows.Close()

	var results []AggregateRow
	for rows.Next() {
		var row AggregateRow
		if err := rows.Scan(
			&row.AccountID, &row.UsedPercentAvg, &row.InputTokensSum, &row.OutputTokensSum,
			&row.Samples, &row.LastRecordedAt, &row.ResetAtMax, &row.WindowMinutesMax,
		); err != nil {
			return nil, fmt.Errorf("scan usage aggregate row: %w", err)
		}
		results = append(results, row)
	}
	return httputil.EmptySlice(results), rows.Err()
}

// AddEntry inserts a new usage_history row, defaulting recorded_at to now if unset.
func (r Repository) AddEntry(ctx context.Context, entry Entry) (Entry, error) {
	if entry.RecordedAt == "" {
		entry.RecordedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	result, err := r.store.DB().ExecContext(ctx, `
		INSERT INTO usage_history (
			account_id, recorded_at, window, used_percent,
			input_tokens, output_tokens, reset_at, window_minutes,
			credits_has, credits_unlimited, credits_balance
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		entry.AccountID, entry.RecordedAt, entry.Window, entry.UsedPercent,
		entry.InputTokens, entry.OutputTokens, entry.ResetAt, entry.WindowMinutes,
		entry.CreditsHas, entry.CreditsUnlimited, entry.CreditsBalance,
	)
	if err != nil {
		return Entry{}, fmt.Errorf("insert usage history: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Entry{}, fmt.Errorf("usage history last insert id: %w", err)
	}
	entry.ID = id
	return entry, nil
}

// AddAdditionalEntry inserts a new additional_usage_history row, defaulting recorded_at to now if unset.
func (r Repository) AddAdditionalEntry(ctx context.Context, entry AdditionalEntry) (AdditionalEntry, error) {
	if entry.RecordedAt == "" {
		entry.RecordedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	result, err := r.store.DB().ExecContext(ctx, `
		INSERT INTO additional_usage_history (
			account_id, limit_name, metered_feature, window, used_percent,
			reset_at, window_minutes, recorded_at, quota_key
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		entry.AccountID, entry.LimitName, entry.MeteredFeature, entry.Window, entry.UsedPercent,
		entry.ResetAt, entry.WindowMinutes, entry.RecordedAt, entry.QuotaKey,
	)
	if err != nil {
		return AdditionalEntry{}, fmt.Errorf("insert additional usage history: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return AdditionalEntry{}, fmt.Errorf("additional usage history last insert id: %w", err)
	}
	entry.ID = id
	return entry, nil
}

// DeleteAdditionalForAccount removes all additional_usage_history rows for an account.
func (r Repository) DeleteAdditionalForAccount(ctx context.Context, accountID string) error {
	if _, err := r.store.DB().ExecContext(ctx, `
		DELETE FROM additional_usage_history WHERE account_id = ?
	`, accountID); err != nil {
		return fmt.Errorf("delete additional usage history: %w", err)
	}
	return nil
}

func (r Repository) DeleteAdditionalForAccountAndQuotaKey(ctx context.Context, accountID, quotaKey string) error {
	if _, err := r.store.DB().ExecContext(ctx, `
		DELETE FROM additional_usage_history WHERE account_id = ? AND quota_key = ?
	`, accountID, quotaKey); err != nil {
		return fmt.Errorf("delete additional usage history quota key: %w", err)
	}
	return nil
}

func (r Repository) DeleteAdditionalForAccountQuotaKeyWindow(ctx context.Context, accountID, quotaKey, window string) error {
	if _, err := r.store.DB().ExecContext(ctx, `
		DELETE FROM additional_usage_history WHERE account_id = ? AND quota_key = ? AND window = ?
	`, accountID, quotaKey, window); err != nil {
		return fmt.Errorf("delete additional usage history quota key window: %w", err)
	}
	return nil
}

func (r Repository) ListAdditionalQuotaKeys(ctx context.Context, accountIDs []string) ([]string, error) {
	conditions := []string{"quota_key IS NOT NULL", "quota_key != ''"}
	args := []any{}
	if len(accountIDs) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(accountIDs)), ",")
		conditions = append(conditions, "account_id IN ("+placeholders+")")
		for _, accountID := range accountIDs {
			args = append(args, accountID)
		}
	}
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT DISTINCT quota_key
		  FROM additional_usage_history
		 WHERE `+strings.Join(conditions, " AND ")+`
		 ORDER BY quota_key
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("list additional quota keys: %w", err)
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan additional quota key: %w", err)
		}
		keys = append(keys, key)
	}
	return httputil.EmptySlice(keys), rows.Err()
}

func (r Repository) LatestAdditionalRecordedAtForAccount(ctx context.Context, accountID string) (sql.NullString, error) {
	var recordedAt sql.NullString
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT max(recorded_at)
		  FROM additional_usage_history
		 WHERE account_id = ?
	`, accountID).Scan(&recordedAt)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("latest additional recorded_at: %w", err)
	}
	return recordedAt, nil
}

// LatestAdditionalByQuotaKey returns the latest additional_usage_history row per account
// for the given quota key and window.
func (r Repository) LatestAdditionalByQuotaKey(
	ctx context.Context,
	quotaKey string,
	window string,
	accountIDs []string,
	since *time.Time,
) (map[string]AdditionalEntry, error) {
	if quotaKey == "" || window == "" {
		return nil, fmt.Errorf("quota key and window are required")
	}
	if len(accountIDs) == 0 {
		return map[string]AdditionalEntry{}, nil
	}

	placeholders := make([]string, len(accountIDs))
	args := make([]any, 0, len(accountIDs)+3)
	args = append(args, quotaKey, window)
	for i, accountID := range accountIDs {
		placeholders[i] = "?"
		args = append(args, accountID)
	}
	query := fmt.Sprintf(`
		SELECT id, account_id, quota_key, limit_name, metered_feature, window,
		       used_percent, reset_at, window_minutes, recorded_at
		  FROM additional_usage_history
		 WHERE quota_key = ?
		   AND window = ?
		   AND account_id IN (%s)
	`, strings.Join(placeholders, ","))
	if since != nil {
		query += ` AND recorded_at >= ?`
		args = append(args, since.UTC().Format("2006-01-02 15:04:05"))
	}
	query += ` ORDER BY account_id, recorded_at DESC, id DESC`

	rows, err := r.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query additional usage history: %w", err)
	}
	defer rows.Close()

	out := make(map[string]AdditionalEntry, len(accountIDs))
	for rows.Next() {
		var entry AdditionalEntry
		if err := rows.Scan(
			&entry.ID, &entry.AccountID, &entry.QuotaKey, &entry.LimitName, &entry.MeteredFeature,
			&entry.Window, &entry.UsedPercent, &entry.ResetAt, &entry.WindowMinutes, &entry.RecordedAt,
		); err != nil {
			return nil, fmt.Errorf("scan additional usage history: %w", err)
		}
		if _, exists := out[entry.AccountID]; exists {
			continue
		}
		out[entry.AccountID] = entry
	}
	return out, rows.Err()
}
