## ADDED Requirements

### Requirement: Go Codex usage maps API-key credit limits

`GET /api/codex/usage` in the Go API SHALL accept valid codex-lb API keys and
map the calling key's unscoped credit limits into the Codex rate-limit status
payload.

#### Scenario: API key has primary secondary and monthly credit limits
- **WHEN** a valid API-key caller has unscoped credit limits for `5h`, `7d`,
  and `monthly`
- **THEN** the response has `planType: "api_key"`
- **AND** `rateLimit.primaryWindow`, `rateLimit.secondaryWindow`, and
  `rateLimit.monthlyWindow` reflect the corresponding limits
- **AND** `credits` reflects the preferred monthly, then secondary, then
  primary remaining credit balance.

#### Scenario: API key has no credit limits
- **WHEN** a valid API-key caller has no unscoped credit limits
- **THEN** the response has `planType: "api_key"`
- **AND** `rateLimit` and `credits` are `null`
- **AND** `additionalRateLimits` is an array.
