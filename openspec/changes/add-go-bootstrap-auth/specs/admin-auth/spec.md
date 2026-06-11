## ADDED Requirements

### Requirement: Go dashboard auth enforces remote bootstrap setup

The Go API SHALL enforce the dashboard bootstrap token contract for remote
first-run password setup.

#### Scenario: Remote first-run session requires bootstrap
- **WHEN** no dashboard password is configured
- **AND** a non-local client requests the dashboard auth session
- **THEN** the Go API returns `authenticated: false`
- **AND** `bootstrapRequired: true`
- **AND** `bootstrapTokenConfigured` reflects whether a manual or shared bootstrap token is active

#### Scenario: Local first-run session bypasses bootstrap
- **WHEN** no dashboard password is configured
- **AND** a loopback client using a localhost host header requests the dashboard auth session
- **THEN** the Go API does not require a bootstrap token

#### Scenario: Remote setup validates bootstrap token
- **WHEN** no dashboard password is configured
- **AND** a non-local client posts to `POST /api/auth/password/setup`
- **THEN** the Go API requires a valid bootstrap token before setting the password

#### Scenario: Startup creates a shared bootstrap token
- **WHEN** no dashboard password is configured and no manual bootstrap token is configured
- **THEN** Go startup creates or reuses a shared encrypted and hashed bootstrap token in `dashboard_settings`
- **AND** logs the plaintext token when it can be recovered

#### Scenario: Password removal restores bootstrap
- **WHEN** an authenticated operator removes the dashboard password
- **THEN** the Go API clears password and TOTP state
- **AND** creates or reuses a shared bootstrap token for the next remote setup
