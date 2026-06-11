package firewall

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"time"

	"github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/httputil"
	"github.com/soju06/codex-lb/internal/platform"
)

type Repository struct {
	store *db.Store
}

type Entry struct {
	IPAddress string
	CreatedAt sql.NullString
}

func NewRepository(store *db.Store) Repository {
	return Repository{store: store}
}

func (r Repository) List(ctx context.Context) ([]Entry, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT ip_address, created_at
		  FROM api_firewall_allowlist
		 ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list firewall ips: %w", err)
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.IPAddress, &entry.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan firewall ip: %w", err)
		}
		entries = append(entries, entry)
	}
	return httputil.EmptySlice(entries), rows.Err()
}

func (r Repository) IsAllowed(ctx context.Context, ipAddress string) (bool, error) {
	normalized, err := normalizeIP(ipAddress)
	if err != nil {
		return false, nil
	}
	var count int
	if err := r.store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM api_firewall_allowlist`).Scan(&count); err != nil {
		return false, fmt.Errorf("count firewall ips: %w", err)
	}
	if count == 0 {
		return true, nil
	}
	var exists int
	err = r.store.DB().QueryRowContext(ctx, `
		SELECT 1 FROM api_firewall_allowlist WHERE ip_address = ? LIMIT 1
	`, normalized).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check firewall allowlist: %w", err)
	}
	return true, nil
}

func (r Repository) Add(ctx context.Context, ipAddress string) (Entry, error) {
	normalized, err := normalizeIP(ipAddress)
	if err != nil {
		return Entry{}, err
	}
	var exists int
	err = r.store.DB().QueryRowContext(ctx, `
		SELECT 1 FROM api_firewall_allowlist WHERE ip_address = ? LIMIT 1
	`, normalized).Scan(&exists)
	if err == nil {
		return Entry{}, ErrAlreadyExists
	}
	if err != nil && err != sql.ErrNoRows {
		return Entry{}, fmt.Errorf("check firewall ip: %w", err)
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	_, err = r.store.DB().ExecContext(ctx, `
		INSERT INTO api_firewall_allowlist (ip_address, created_at) VALUES (?, ?)
	`, normalized, now)
	if err != nil {
		return Entry{}, fmt.Errorf("insert firewall ip: %w", err)
	}
	return Entry{IPAddress: normalized, CreatedAt: sql.NullString{String: now, Valid: true}}, nil
}

func (r Repository) Delete(ctx context.Context, ipAddress string) (bool, error) {
	normalized, err := normalizeIP(ipAddress)
	if err != nil {
		return false, err
	}
	result, err := r.store.DB().ExecContext(ctx, `
		DELETE FROM api_firewall_allowlist WHERE ip_address = ?
	`, normalized)
	if err != nil {
		return false, fmt.Errorf("delete firewall ip: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("firewall delete rows affected: %w", err)
	}
	return rows > 0, nil
}

func normalizeIP(value string) (string, error) {
	ip := net.ParseIP(value)
	if ip == nil {
		return "", ErrInvalidIP
	}
	return ip.String(), nil
}

func formatCreatedAt(value sql.NullString) string {
	if iso := platform.SQLiteTimeToISO(value); iso != nil {
		return *iso
	}
	return time.Now().UTC().Format(time.RFC3339)
}
