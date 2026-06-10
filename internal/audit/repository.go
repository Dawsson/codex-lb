package audit

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

// Entry mirrors a row in audit_logs.
type Entry struct {
	ID        int64
	Timestamp string
	Action    string
	ActorIP   sql.NullString
	Details   sql.NullString
	RequestID sql.NullString
}

// Insert writes a new audit log entry, defaulting timestamp to now if unset.
func (r Repository) Insert(ctx context.Context, entry Entry) (Entry, error) {
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	result, err := r.store.DB().ExecContext(ctx, `
		INSERT INTO audit_logs (timestamp, action, actor_ip, details, request_id)
		VALUES (?, ?, ?, ?, ?)
	`, entry.Timestamp, entry.Action, entry.ActorIP, entry.Details, entry.RequestID)
	if err != nil {
		return Entry{}, fmt.Errorf("insert audit log: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Entry{}, fmt.Errorf("audit log last insert id: %w", err)
	}
	entry.ID = id
	return entry, nil
}

// List returns audit log entries ordered most-recent first, optionally filtered by action.
func (r Repository) List(ctx context.Context, action string, limit, offset int) ([]Entry, error) {
	var conditions []string
	var args []any
	if action != "" {
		conditions = append(conditions, "action = ?")
		args = append(args, action)
	}

	query := "SELECT id, timestamp, action, actor_ip, details, request_id FROM audit_logs"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY timestamp DESC, id DESC"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	if offset > 0 {
		query += " OFFSET ?"
		args = append(args, offset)
	}

	rows, err := r.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.ID, &entry.Timestamp, &entry.Action, &entry.ActorIP, &entry.Details, &entry.RequestID); err != nil {
			return nil, fmt.Errorf("scan audit log: %w", err)
		}
		entries = append(entries, entry)
	}
	return httputil.EmptySlice(entries), rows.Err()
}
