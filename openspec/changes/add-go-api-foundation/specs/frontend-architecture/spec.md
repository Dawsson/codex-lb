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

### Requirement: Go dashboard read cache remains short-lived

The Go API SHALL keep any in-process dashboard read cache short-lived, and the
cache MUST NOT replace SQLite as the durable source of truth.

#### Scenario: Repeated dashboard overview reads reuse a hot response

- **WHEN** the dashboard frontend repeatedly requests the same overview
  timeframe within the cache TTL
- **THEN** the Go API MAY return the cached response
- **AND** the cached response MUST expire automatically without operator action.

#### Scenario: Accounts read cache expires automatically

- **WHEN** the dashboard frontend repeatedly requests `GET /api/accounts`
- **THEN** the Go API MAY reuse a cached account summary for a short interval
- **AND** the next request after expiry MUST read from SQLite again.

### Requirement: Go dashboard auth uses first-class session routes

The Go API SHALL expose dashboard authentication through `/api/auth/session`,
`/api/auth/login`, and `/api/auth/logout` using server-side session cookies.

#### Scenario: Password login creates a server-side session

- **WHEN** an operator posts a valid password to `POST /api/auth/login`
- **THEN** the Go API verifies the password hash from the durable database
- **AND** renews the session token before marking the session authenticated.

#### Scenario: Protected dashboard reads require authentication

- **WHEN** a password hash is configured
- **AND** a request to a protected dashboard read route has no authenticated
  session
- **THEN** the Go API rejects the request with `401 authentication_required`.

#### Scenario: Logout destroys the session

- **WHEN** an operator posts to `POST /api/auth/logout`
- **THEN** the Go API destroys the server-side session
- **AND** subsequent protected dashboard reads require login again.
