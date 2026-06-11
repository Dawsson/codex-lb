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
	PasswordHash            sql.NullString
	BootstrapTokenEncrypted []byte
	BootstrapTokenHash      []byte
	TOTPRequiredOnLogin     bool
	TOTPConfigured          bool
}

func NewRepository(store *db.Store) Repository {
	return Repository{store: store}
}

func (r Repository) Settings(ctx context.Context) (Settings, error) {
	var settings Settings
	var totpSecret []byte
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT password_hash, bootstrap_token_encrypted, bootstrap_token_hash,
		       totp_required_on_login, totp_secret_encrypted
		  FROM dashboard_settings
		 ORDER BY id
		 LIMIT 1
	`).Scan(&settings.PasswordHash, &settings.BootstrapTokenEncrypted, &settings.BootstrapTokenHash, &settings.TOTPRequiredOnLogin, &totpSecret)
	if err == sql.ErrNoRows {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("load auth settings: %w", err)
	}
	settings.TOTPConfigured = len(totpSecret) > 0
	return settings, nil
}

func (r Repository) StoreBootstrapTokenIfAbsent(ctx context.Context, tokenEncrypted []byte, tokenHash []byte) (bool, error) {
	result, err := r.store.DB().ExecContext(ctx, `
		UPDATE dashboard_settings
		   SET bootstrap_token_encrypted = ?, bootstrap_token_hash = ?
		 WHERE password_hash IS NULL
		   AND bootstrap_token_hash IS NULL
	`, tokenEncrypted, tokenHash)
	if err != nil {
		return false, fmt.Errorf("store bootstrap token: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (r Repository) ClearBootstrapToken(ctx context.Context) (bool, error) {
	result, err := r.store.DB().ExecContext(ctx, `
		UPDATE dashboard_settings
		   SET bootstrap_token_encrypted = NULL,
		       bootstrap_token_hash = NULL
		 WHERE bootstrap_token_hash IS NOT NULL
	`)
	if err != nil {
		return false, fmt.Errorf("clear bootstrap token: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (r Repository) TrySetPasswordHash(ctx context.Context, passwordHash string) (bool, error) {
	result, err := r.store.DB().ExecContext(ctx, `
		UPDATE dashboard_settings
		   SET password_hash = ?, bootstrap_token_encrypted = NULL, bootstrap_token_hash = NULL
		 WHERE password_hash IS NULL
	`, passwordHash)
	if err != nil {
		return false, fmt.Errorf("set password hash: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (r Repository) SetPasswordHash(ctx context.Context, passwordHash string) error {
	_, err := r.store.DB().ExecContext(ctx, `
		UPDATE dashboard_settings SET password_hash = ?
	`, passwordHash)
	if err != nil {
		return fmt.Errorf("update password hash: %w", err)
	}
	return nil
}

func (r Repository) ClearPasswordAndTOTP(ctx context.Context) error {
	_, err := r.store.DB().ExecContext(ctx, `
		UPDATE dashboard_settings
		   SET password_hash = NULL,
		       totp_secret_encrypted = NULL,
		       totp_last_verified_step = NULL,
		       totp_required_on_login = 0
	`)
	if err != nil {
		return fmt.Errorf("clear password and totp: %w", err)
	}
	return nil
}

func (r Repository) SetTOTPSecret(ctx context.Context, secretEncrypted []byte) error {
	_, err := r.store.DB().ExecContext(ctx, `
		UPDATE dashboard_settings
		   SET totp_secret_encrypted = ?, totp_last_verified_step = NULL
	`, secretEncrypted)
	if err != nil {
		return fmt.Errorf("set totp secret: %w", err)
	}
	return nil
}

func (r Repository) ClearTOTP(ctx context.Context) error {
	_, err := r.store.DB().ExecContext(ctx, `
		UPDATE dashboard_settings
		   SET totp_secret_encrypted = NULL,
		       totp_last_verified_step = NULL,
		       totp_required_on_login = 0
	`)
	if err != nil {
		return fmt.Errorf("clear totp: %w", err)
	}
	return nil
}

func (r Repository) TOTPSecretEncrypted(ctx context.Context) ([]byte, error) {
	var secret []byte
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT totp_secret_encrypted FROM dashboard_settings ORDER BY id LIMIT 1
	`).Scan(&secret)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load totp secret: %w", err)
	}
	return secret, nil
}
