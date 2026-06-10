## ADDED Requirements

### Requirement: Go sends limit warmup requests after account recovery

The Go API SHALL, after the usage refresh scheduler detects an account has
recovered usage headroom, optionally send a minimal warmup request (subject
to the account's `limit_warmup_enabled` flag) and record the attempt in
`account_limit_warmups`.

#### Scenario: Warmup attempt recorded after recovery

- **GIVEN** an account has `limit_warmup_enabled = true` and just
  transitioned from `rate_limited` to `active`
- **WHEN** the usage refresh scheduler completes its run
- **THEN** the Go API sends a warmup request with the
  `x-codex-lb-limit-warmup` header and "Reply with OK only." instructions,
  and records the result (success/failure, error code) in
  `account_limit_warmups`.

#### Scenario: Warmup concurrency is bounded

- **GIVEN** more than 4 accounts are eligible for warmup in a single refresh
  cycle
- **WHEN** the limit warmup service runs
- **THEN** at most 4 warmup requests are in flight concurrently.

### Requirement: Go supports per-account limit warmup toggle

The Go API SHALL expose `PUT /api/accounts/{accountID}/limit-warmup` to
enable or disable limit warmup for an account.

#### Scenario: Disabling limit warmup stops future warmup attempts

- **WHEN** an operator sets `limit_warmup_enabled = false` for an account
- **THEN** subsequent usage refresh cycles do not send warmup requests for
  that account.
