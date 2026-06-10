# Add OAuth account import flow to Go

## Why

Operators add ChatGPT/OpenAI accounts to the pool via an OAuth device-code
or authorization-code flow handled by `app/modules/oauth`. This must be
ported so accounts can be onboarded without the Python service.

## What Changes

- Port `app/core/clients/oauth.py` (PKCE pair generation, authorization URL
  building, device code request, token exchange) to `internal/oauth`.
- Port `app/modules/oauth/service.py` account-creation flow: claims
  extraction, identity normalization, conflict detection
  (`AccountIdentityConflictError`), encrypting and storing tokens.
- Implement endpoints:
  - `POST /api/oauth/start`
  - `GET /api/oauth/status`
  - `POST /api/oauth/complete`
  - `POST /api/oauth/manual-callback`

## Impact

- Depends on `migrate-go-db-ownership` (accounts table, encryption) and
  reuses `internal/crypto` for token encryption.
- Must produce accounts compatible with the existing `internal/accounts`
  repository (same identity/dedup rules as Python
  `generate_unique_account_id`/`clean_account_identity_part`).
