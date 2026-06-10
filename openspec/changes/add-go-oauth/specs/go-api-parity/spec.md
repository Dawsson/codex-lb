## ADDED Requirements

### Requirement: Go supports OAuth account onboarding

The Go API SHALL expose `POST /api/oauth/start`, `GET /api/oauth/status`,
`POST /api/oauth/complete`, and `POST /api/oauth/manual-callback` to onboard
new accounts via OAuth device-code or authorization-code flow, matching the
Python identity normalization and conflict-detection rules.

#### Scenario: Successful OAuth completion creates an account

- **WHEN** a user completes the OAuth flow via `POST /api/oauth/complete`
  with valid tokens
- **THEN** the Go API extracts identity claims, generates a unique account
  id consistent with the Python `generate_unique_account_id` algorithm, and
  stores encrypted access/refresh/id tokens in the accounts table.

#### Scenario: Duplicate account identity is rejected

- **WHEN** the OAuth identity matches an existing account's
  `chatgpt_account_id`/email combination
- **THEN** the Go API returns a conflict error instead of creating a
  duplicate account, matching `AccountIdentityConflictError` behavior.
