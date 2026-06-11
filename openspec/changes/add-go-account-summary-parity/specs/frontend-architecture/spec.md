## MODIFIED Requirements

### Requirement: Account summary duplicate email indicator

The Go API SHALL expose the same `isEmailDuplicate` boolean on each
`AccountSummary` returned by `GET /api/accounts` as the Python API. Duplicate
detection MUST compare real non-placeholder email, ChatGPT account identity,
and workspace identity. Missing, blank, and `unknown@example.com` emails MUST
not be flagged.

#### Scenario: Duplicate real email and identity pairs are flagged by Go

- **WHEN** `GET /api/accounts` is served by `cmd/codex-lb-go`
- **AND** two accounts have the same real email, ChatGPT account id, and workspace id
- **THEN** both account summaries include `isEmailDuplicate: true`

### Requirement: Dashboard limit warm-up controls

The Go API SHALL include per-account limit warm-up status in account summaries
using the same JSON shape as the Python API.

#### Scenario: Go account summary includes latest limit warm-up attempt

- **WHEN** an account has a latest `account_limit_warmups` row
- **THEN** `GET /api/accounts` includes `limitWarmup.window`, `status`, `model`,
  `resetAt`, `attemptedAt`, `completedAt`, `errorCode`, and `errorMessage`
  for that account

## ADDED Requirements

### Requirement: Go account summaries preserve Python mapper quota semantics

The Go API SHALL map account usage windows using the Python
`build_account_summaries` behavior. Weekly-only primary usage MUST be presented
as secondary usage, monthly usage MUST suppress primary and secondary fields
when the account plan has a monthly capacity, free/zero-primary-capacity plans
MUST hide the primary quota fields, and runtime-derived account status MUST use
the latest usage windows rather than blindly returning the persisted database
status.

#### Scenario: Weekly-only primary appears as secondary

- **WHEN** the latest primary usage row has a weekly window and there is no
  newer secondary row that should override it
- **THEN** the Go account summary omits primary quota fields
- **AND** exposes the weekly usage through secondary quota fields

#### Scenario: Runtime status recovers with available long-window quota

- **WHEN** an account is persisted as `quota_exceeded`
- **AND** the latest long-window usage has remaining quota or credits
- **THEN** the Go account summary status is `active`

### Requirement: Go account summaries include dashboard side data

The Go API SHALL include request usage aggregates, additional quota entries,
auth token status, and limit warm-up status in account summaries when the
underlying SQLite rows are present. Missing side data MUST be represented with
the Python-compatible null or empty-array values.

#### Scenario: Account request usage is included

- **WHEN** an account has non-deleted non-warmup request logs in the last seven days
- **THEN** the Go account summary includes `requestUsage.requests`,
  `totalTokens`, and `errors` derived from those rows

#### Scenario: Additional quota entries are included

- **WHEN** an account has latest `additional_usage_history` rows
- **THEN** the Go account summary includes an `additionalQuotas` array with
  quota key, label metadata, remaining percent, reset timestamp, and window
  minutes for those rows

#### Scenario: Auth token status is included

- **WHEN** account token columns are present
- **THEN** the Go account summary includes `auth.access`, `auth.refresh`, and
  `auth.idToken` status objects without exposing raw token values
