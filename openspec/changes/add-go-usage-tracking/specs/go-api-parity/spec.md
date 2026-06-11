## ADDED Requirements

### Requirement: Go usage API matches Python response contracts

The Go API SHALL expose `/api/usage/summary`, `/api/usage/history`, and
`/api/usage/window` with the same JSON field names and semantics as the
Python implementation.

#### Scenario: Usage summary reflects latest windows

- **WHEN** a dashboard client requests `GET /api/usage/summary`
- **THEN** the response includes per-account primary, secondary, and
  monthly usage derived from the latest `usage_history` rows, normalized
  the same way as the Python `normalize_weekly_only_rows` logic.

#### Scenario: Usage history honors the hours parameter

- **WHEN** a dashboard client requests `GET /api/usage/history?hours=48`
- **THEN** the response aggregates `usage_history` rows recorded since
  48 hours ago for the primary window.

### Requirement: Go usage refresh scheduler reconciles account status

The Go API SHALL run a leader-elected background scheduler that refreshes
per-account usage from upstream and reconciles `rate_limited` /
`quota_exceeded` accounts back to `active` when usage data indicates
recovery.

#### Scenario: Recovered account status reconciliation

- **GIVEN** an account with status `rate_limited` and a stale `reset_at`
- **WHEN** the usage refresh scheduler runs and the latest usage indicates
  the account is below its limit
- **THEN** the scheduler updates the account status to `active` using an
  optimistic-concurrency check against the account's previous status,
  deactivation reason, reset_at, and blocked_at.

#### Scenario: Monthly usage recovers monthly-capacity accounts

- **GIVEN** an account with status `quota_exceeded` on a plan with monthly
  capacity
- **WHEN** the usage refresh scheduler runs and the latest monthly usage
  indicates the account is below its limit
- **THEN** the scheduler selects the monthly row as the long-window usage
  signal and updates the account status to `active` when cooldown allows.

#### Scenario: Only one replica runs the refresh loop

- **GIVEN** multiple Go API replicas are running
- **WHEN** the usage refresh interval elapses
- **THEN** only the replica holding the scheduler leader lock performs the
  refresh and reconciliation.

#### Scenario: Additional-only usage rows suppress redundant refreshes

- **GIVEN** an account has no fresh primary usage row but has an
  `additional_usage_history` row recorded within the usage refresh interval
- **WHEN** the Go usage updater considers the account for refresh
- **THEN** the updater treats the account as fresh and does not fetch upstream
  usage again until the additional usage timestamp becomes stale.

#### Scenario: Permanent usage fetch errors update account status

- **WHEN** an upstream usage fetch fails with HTTP 402 or 404, with an error
  code listed in the permanent failure table, or with a deactivated-account
  message
- **THEN** the Go updater updates the account status to `deactivated` or
  `reauth_required` using the same permanent failure code mapping as account
  selection
- **AND** generic 403 usage fetch failures do not permanently deactivate the
  account.

#### Scenario: Usage payload identity metadata is synced

- **WHEN** a successful upstream usage payload includes plan, workspace, or
  seat metadata that differs from the stored account row
- **THEN** the Go updater persists the metadata to the account row before
  writing usage history
- **AND** existing stored metadata is preserved when the payload omits a field.

#### Scenario: Usage refresh retries after token refresh on 401

- **WHEN** an upstream usage fetch fails with HTTP 401
- **THEN** the Go updater refreshes the account OAuth tokens using the existing
  auth guardian refresher
- **AND** retries the usage fetch once with the refreshed access token
- **AND** writes usage rows from the retried response when it succeeds.

#### Scenario: Monthly-only usage payload is persisted as monthly

- **WHEN** a successful upstream usage payload reports only a primary window
  whose duration is 30 days
- **THEN** the Go updater persists that row as `window = "monthly"`
- **AND** does not persist it as a primary window.
