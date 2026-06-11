## ADDED Requirements

### Requirement: Dashboard request-log search matches Python fields

The Go dashboard request-log API SHALL search the same request-log fields as
the Python API when a `search` query is provided.

#### Scenario: Search by related account email
- **WHEN** a request-log row is associated with an account whose email contains `needle`
- **THEN** `GET /api/request-logs?search=needle` includes that row

#### Scenario: Search by request metadata
- **WHEN** a request-log row contains the searched value in `reasoning_effort`, `source`, `status`, `api_key_id`, `requested_at`, or `latency_ms`
- **THEN** `GET /api/request-logs?search=<value>` includes that row
