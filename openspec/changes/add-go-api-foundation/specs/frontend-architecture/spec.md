## ADDED Requirements

### Requirement: Go dashboard read slice preserves existing response contracts

The Go API SHALL expose the initial dashboard read routes using the same JSON
field names expected by the current frontend for session, accounts, and
dashboard overview data.

#### Scenario: Dashboard auth session returns frontend-compatible fields

- **WHEN** the dashboard frontend requests `GET /api/dashboard-auth/session`
- **THEN** the Go API returns `authenticated`, `passwordRequired`,
  `totpRequiredOnLogin`, `totpConfigured`, `bootstrapRequired`,
  `bootstrapTokenConfigured`, `authMode`, `passwordManagementEnabled`, and
  `passwordSessionActive`.

#### Scenario: Accounts list is read from existing SQLite rows

- **WHEN** the dashboard frontend requests `GET /api/accounts`
- **THEN** the Go API returns an `accounts` array using account summary field
  names already accepted by the frontend.

#### Scenario: Dashboard overview is read from existing SQLite rows

- **WHEN** the dashboard frontend requests `GET /api/dashboard/overview`
- **THEN** the Go API returns timeframe, account summaries, summary windows,
  trends, and metrics using the existing frontend JSON contract.
