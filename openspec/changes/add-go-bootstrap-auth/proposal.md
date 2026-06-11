## Why

The Go dashboard auth session currently reports bootstrap flags as false and
allows remote first-run password setup without validating a bootstrap token.
Python protects first-run remote setup with either a configured environment
token or an auto-generated shared database token.

## What Changes

- Add Go startup generation/reuse of shared dashboard bootstrap tokens.
- Expose `bootstrapRequired` and `bootstrapTokenConfigured` in session
  responses when password setup is pending for remote clients.
- Require a valid bootstrap token for remote first-run password setup while
  preserving local setup bypass.
- Regenerate a shared bootstrap token when the dashboard password is removed.

## Impact

- Affects Go `/api/auth/*` and `/api/dashboard-auth/*` password/session routes.
- Adds no new database columns; uses existing `dashboard_settings`
  `bootstrap_token_encrypted` and `bootstrap_token_hash`.
