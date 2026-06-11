package stickysessions

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

type ListParams struct {
	StaleOnly    bool
	AccountQuery string
	KeyQuery     string
	SortBy       string
	SortDir      string
	Offset       int
	Limit        int
}

type Entry struct {
	Key         string
	DisplayName string
	Kind        string
	CreatedAt   sql.NullString
	UpdatedAt   sql.NullString
}

type Session struct {
	Key       string
	Kind      string
	AccountID string
	UpdatedAt string
}

func NewRepository(store *db.Store) Repository {
	return Repository{store: store}
}

func (r Repository) CacheAffinityMaxAgeSeconds(ctx context.Context) (int, error) {
	var ttl int
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT openai_cache_affinity_max_age_seconds
		  FROM dashboard_settings
		 ORDER BY id
		 LIMIT 1
	`).Scan(&ttl)
	if err == sql.ErrNoRows {
		return 1800, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load cache affinity ttl: %w", err)
	}
	if ttl <= 0 {
		return 1800, nil
	}
	return ttl, nil
}

func (r Repository) GetAccountID(ctx context.Context, key, kind string, maxAgeSeconds *int) (string, error) {
	row, err := r.GetEntry(ctx, key, kind)
	if err != nil || row == nil {
		return "", err
	}
	if maxAgeSeconds != nil && *maxAgeSeconds > 0 {
		cutoff := time.Now().UTC().Add(-time.Duration(*maxAgeSeconds) * time.Second)
		updatedAt, parseErr := parseStickySessionTime(row.UpdatedAt)
		if parseErr != nil {
			return "", parseErr
		}
		if updatedAt.Before(cutoff) {
			_, deleteErr := r.Delete(ctx, key, kind)
			if deleteErr != nil {
				return "", deleteErr
			}
			return "", nil
		}
	}
	return row.AccountID, nil
}

func parseStickySessionTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z07:00",
		time.RFC3339Nano,
	} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("parse sqlite time %q", value)
}

func (r Repository) GetEntry(ctx context.Context, key, kind string) (*Session, error) {
	key = strings.TrimSpace(key)
	kind = strings.TrimSpace(kind)
	if key == "" || kind == "" {
		return nil, nil
	}
	var row Session
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT key, kind, account_id, updated_at
		  FROM sticky_sessions
		 WHERE key = ? AND kind = ?
		 LIMIT 1
	`, key, kind).Scan(&row.Key, &row.Kind, &row.AccountID, &row.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get sticky session: %w", err)
	}
	return &row, nil
}

func (r Repository) Upsert(ctx context.Context, key, accountID, kind string) error {
	key = strings.TrimSpace(key)
	accountID = strings.TrimSpace(accountID)
	kind = strings.TrimSpace(kind)
	if key == "" || accountID == "" || kind == "" {
		return nil
	}
	_, err := r.store.DB().ExecContext(ctx, `
		INSERT INTO sticky_sessions (key, kind, account_id, created_at, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(key, kind) DO UPDATE SET
			account_id = excluded.account_id,
			updated_at = CURRENT_TIMESTAMP
	`, key, kind, accountID)
	if err != nil {
		return fmt.Errorf("upsert sticky session: %w", err)
	}
	return nil
}

func (r Repository) CountStalePromptCache(ctx context.Context, staleCutoff string) (int, error) {
	var count int
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT COUNT(*)
		  FROM sticky_sessions ss
		  JOIN accounts a ON a.id = ss.account_id
		 WHERE ss.kind = 'prompt_cache'
		   AND ss.updated_at < ?
	`, staleCutoff).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count stale prompt cache: %w", err)
	}
	return count, nil
}

func (r Repository) CountEntries(ctx context.Context, params ListParams, updatedBefore *string) (int, error) {
	where, args := buildWhere(params, updatedBefore)
	query := `
		SELECT COUNT(*)
		  FROM sticky_sessions ss
		  JOIN accounts a ON a.id = ss.account_id
	` + where
	var count int
	if err := r.store.DB().QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count sticky sessions: %w", err)
	}
	return count, nil
}

func (r Repository) ListEntries(ctx context.Context, params ListParams, updatedBefore *string) ([]Entry, error) {
	where, args := buildWhere(params, updatedBefore)
	orderBy := buildOrderBy(params.SortBy, params.SortDir)
	query := fmt.Sprintf(`
		SELECT ss.key, COALESCE(NULLIF(a.email, ''), a.id) AS display_name, ss.kind, ss.created_at, ss.updated_at
		  FROM sticky_sessions ss
		  JOIN accounts a ON a.id = ss.account_id
		%s
		ORDER BY %s
		LIMIT ? OFFSET ?
	`, where, orderBy)
	args = append(args, params.Limit, params.Offset)
	rows, err := r.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sticky sessions: %w", err)
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.Key, &entry.DisplayName, &entry.Kind, &entry.CreatedAt, &entry.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan sticky session: %w", err)
		}
		entries = append(entries, entry)
	}
	return httputil.EmptySlice(entries), rows.Err()
}

func (r Repository) Delete(ctx context.Context, key, kind string) (bool, error) {
	result, err := r.store.DB().ExecContext(ctx, `
		DELETE FROM sticky_sessions WHERE key = ? AND kind = ?
	`, key, kind)
	if err != nil {
		return false, fmt.Errorf("delete sticky session: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (r Repository) DeleteEntries(ctx context.Context, targets [][2]string) ([][2]string, error) {
	deleted := make([][2]string, 0, len(targets))
	for _, target := range targets {
		ok, err := r.Delete(ctx, target[0], target[1])
		if err != nil {
			return deleted, err
		}
		if ok {
			deleted = append(deleted, target)
		}
	}
	return deleted, nil
}

func (r Repository) ListIdentifiers(ctx context.Context, params ListParams, updatedBefore *string) ([][2]string, error) {
	where, args := buildWhere(params, updatedBefore)
	query := `
		SELECT ss.key, ss.kind
		  FROM sticky_sessions ss
		  JOIN accounts a ON a.id = ss.account_id
	` + where + `
		 ORDER BY ss.updated_at DESC, ss.created_at DESC, ss.key ASC
	`
	rows, err := r.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sticky session identifiers: %w", err)
	}
	defer rows.Close()

	var identifiers [][2]string
	for rows.Next() {
		var key, kind string
		if err := rows.Scan(&key, &kind); err != nil {
			return nil, err
		}
		identifiers = append(identifiers, [2]string{key, kind})
	}
	return identifiers, rows.Err()
}

func (r Repository) PurgePromptCacheBefore(ctx context.Context, cutoff string) (int, error) {
	result, err := r.store.DB().ExecContext(ctx, `
		DELETE FROM sticky_sessions
		 WHERE kind = 'prompt_cache'
		   AND updated_at < ?
	`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge sticky sessions: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(rows), nil
}

func buildWhere(params ListParams, updatedBefore *string) (string, []any) {
	clauses := make([]string, 0, 4)
	args := make([]any, 0, 4)
	if params.StaleOnly {
		clauses = append(clauses, "ss.kind = 'prompt_cache'")
	}
	if updatedBefore != nil {
		clauses = append(clauses, "ss.updated_at < ?")
		args = append(args, *updatedBefore)
	}
	if q := strings.TrimSpace(params.AccountQuery); q != "" {
		clauses = append(clauses, "LOWER(a.email) LIKE ?")
		args = append(args, "%"+strings.ToLower(q)+"%")
	}
	if q := strings.TrimSpace(params.KeyQuery); q != "" {
		clauses = append(clauses, "LOWER(ss.key) LIKE ?")
		args = append(args, "%"+strings.ToLower(q)+"%")
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func buildOrderBy(sortBy, sortDir string) string {
	dir := "DESC"
	if strings.EqualFold(sortDir, "asc") {
		dir = "ASC"
	}
	switch sortBy {
	case "created_at":
		return fmt.Sprintf("ss.created_at %s, ss.updated_at DESC, ss.key ASC", dir)
	case "account":
		return fmt.Sprintf("a.email %s, ss.updated_at DESC, ss.key ASC", dir)
	case "key":
		return fmt.Sprintf("ss.key %s, ss.updated_at DESC", dir)
	default:
		return fmt.Sprintf("ss.updated_at %s, ss.created_at DESC, ss.key ASC", dir)
	}
}

func staleCutoff(ttlSeconds int) string {
	return time.Now().UTC().Add(-time.Duration(ttlSeconds) * time.Second).Format("2006-01-02 15:04:05")
}
