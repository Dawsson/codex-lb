## ADDED Requirements

### Requirement: Go request logs expose Python-compatible cost breakdowns

The Go API SHALL return `costBreakdown` from `GET /api/request-logs` with
Python-compatible `inputUsd`, `cachedInputUsd`, `outputUsd`, and `totalUsd`
fields.

#### Scenario: Request log has complete usage data
- **WHEN** a request-log row has a known model, input tokens, cached input tokens, and output tokens
- **THEN** the Go API computes `inputUsd`, `cachedInputUsd`, `outputUsd`, and `totalUsd` using the same model pricing and service-tier rules as the Python API

#### Scenario: Persisted total differs from recalculated total
- **WHEN** a request-log row has a persisted `cost_usd` that does not match the recalculated total
- **THEN** the Go API returns the persisted value as `costBreakdown.totalUsd`
- **AND** the segment fields are `null`

#### Scenario: Request log uses reasoning tokens as output fallback
- **WHEN** a request-log row has no persisted `output_tokens` and has `reasoning_tokens`
- **THEN** the Go API uses the reasoning-token value for `outputUsd` and top-level `outputTokens`

#### Scenario: Request log has partial legacy data
- **WHEN** a request-log row is missing model pricing, token values, or persisted cost
- **THEN** the Go API still returns a `costBreakdown` object with all four fields present
- **AND** unavailable fields are `null`
