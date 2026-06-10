-- +goose Up
INSERT INTO dashboard_settings (id, totp_required_on_login, api_key_auth_enabled)
SELECT 1, 0, 0
WHERE NOT EXISTS (SELECT 1 FROM dashboard_settings);

-- +goose Down
DELETE FROM dashboard_settings WHERE id = 1;
