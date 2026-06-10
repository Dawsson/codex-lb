package auth

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/soju06/codex-lb/internal/db"
)

type Repository struct {
	store *db.Store
}

type Settings struct {
	PasswordHash        sql.NullString
	TOTPRequiredOnLogin bool
	TOTPConfigured      bool
}

func NewRepository(store *db.Store) Repository {
	return Repository{store: store}
}

func (r Repository) Settings(ctx context.Context) (Settings, error) {
	var settings Settings
	var totpSecret []byte
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT password_hash, totp_required_on_login, totp_secret_encrypted
		  FROM dashboard_settings
		 ORDER BY id
		 LIMIT 1
	`).Scan(&settings.PasswordHash, &settings.TOTPRequiredOnLogin, &totpSecret)
	if err == sql.ErrNoRows {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("load auth settings: %w", err)
	}
	settings.TOTPConfigured = len(totpSecret) > 0
	return settings, nil
}
