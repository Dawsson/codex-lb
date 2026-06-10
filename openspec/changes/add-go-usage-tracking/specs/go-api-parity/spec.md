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

#### Scenario: Only one replica runs the refresh loop

- **GIVEN** multiple Go API replicas are running
- **WHEN** the usage refresh interval elapses
- **THEN** only the replica holding the scheduler leader lock performs the
  refresh and reconciliation.
