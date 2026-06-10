package scheduling

import (
	"context"
	"fmt"
	"time"

	"github.com/soju06/codex-lb/internal/db"
)

type Repository struct {
	store *db.Store
}

func NewRepository(store *db.Store) Repository {
	return Repository{store: store}
}

// TryAcquireLeader attempts to claim or renew the singleton scheduler_leader row (id = 1) for
// leaderID. It returns true if leaderID holds the lease afterwards, which happens when the
// existing lease is expired or already held by leaderID.
func (r Repository) TryAcquireLeader(ctx context.Context, leaderID string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	expiresAt := time.Now().UTC().Add(ttl).Format("2006-01-02 15:04:05")

	if _, err := r.store.DB().ExecContext(ctx, `
		INSERT INTO scheduler_leader (id, leader_id, acquired_at, expires_at)
		VALUES (1, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			leader_id = excluded.leader_id,
			acquired_at = excluded.acquired_at,
			expires_at = excluded.expires_at
		WHERE scheduler_leader.expires_at < ? OR scheduler_leader.leader_id = ?
	`, leaderID, now, expiresAt, now, leaderID); err != nil {
		return false, fmt.Errorf("acquire scheduler leader: %w", err)
	}

	var currentLeaderID string
	if err := r.store.DB().QueryRowContext(ctx, `
		SELECT leader_id FROM scheduler_leader WHERE id = 1
	`).Scan(&currentLeaderID); err != nil {
		return false, fmt.Errorf("read scheduler leader: %w", err)
	}
	return currentLeaderID == leaderID, nil
}

// RenewLeader extends the lease for leaderID, if it currently holds it.
func (r Repository) RenewLeader(ctx context.Context, leaderID string, ttl time.Duration) (bool, error) {
	expiresAt := time.Now().UTC().Add(ttl).Format("2006-01-02 15:04:05")
	result, err := r.store.DB().ExecContext(ctx, `
		UPDATE scheduler_leader SET expires_at = ? WHERE id = 1 AND leader_id = ?
	`, expiresAt, leaderID)
	if err != nil {
		return false, fmt.Errorf("renew scheduler leader: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("scheduler leader rows affected: %w", err)
	}
	return rows > 0, nil
}
