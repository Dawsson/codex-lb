## ADDED Requirements

### Requirement: Go dashboard auth enforces TOTP after password login

The Go API SHALL mirror Python's two-step dashboard login flow when TOTP is
configured and required.

#### Scenario: Password login leaves TOTP pending
- **WHEN** a dashboard password is configured
- **AND** TOTP is configured with `totp_required_on_login = true`
- **AND** an operator submits the correct password
- **THEN** the Go API creates a password-verified session
- **AND** returns `authenticated: false`
- **AND** returns `totpRequiredOnLogin: true`
- **AND** returns `passwordSessionActive: false`

#### Scenario: TOTP verify completes login
- **WHEN** an operator has a password-verified session with TOTP pending
- **AND** submits a valid TOTP code to `/api/auth/totp/verify`
- **THEN** the Go API marks the session TOTP-verified
- **AND** returns `authenticated: true`
- **AND** returns `totpRequiredOnLogin: false`
- **AND** returns `passwordSessionActive: true`

#### Scenario: Protected dashboard routes require TOTP when enabled
- **WHEN** TOTP is required on login
- **AND** a request has only a password-verified session
- **THEN** protected dashboard routes return `401 totp_required`
