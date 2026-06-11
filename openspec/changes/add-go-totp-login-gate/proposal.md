## Why

Python dashboard auth treats password login as a first step when TOTP is
configured and required: the session is password-verified but not fully
authenticated until `/totp/verify` succeeds. Go currently marks password login
as fully authenticated and does not persist a TOTP-verified session flag.

## What Changes

- Track password-verified and TOTP-verified states separately in Go sessions.
- Return `authenticated: false` and `totpRequiredOnLogin: true` after password
  login when TOTP is required.
- Require full TOTP verification for protected dashboard routes and password
  management when `totp_required_on_login` is enabled.
- Mark the session TOTP-verified after successful `/totp/verify`.

## Impact

- Affects Go `/api/auth/*` and `/api/dashboard-auth/*` session/login/TOTP
  behavior.
- No database schema changes.
