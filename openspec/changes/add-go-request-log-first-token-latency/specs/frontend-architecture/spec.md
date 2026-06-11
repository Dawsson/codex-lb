## ADDED Requirements

### Requirement: Dashboard request logs expose first-token latency

The dashboard request-log API SHALL expose the persisted first-token latency
for request rows when it is available.

#### Scenario: Request log includes first-token latency
- **WHEN** a request-log row has `latency_first_token_ms = 123`
- **THEN** `GET /api/request-logs` includes `latencyFirstTokenMs: 123` for that row

#### Scenario: Request log omits first-token latency
- **WHEN** a request-log row has no persisted first-token latency
- **THEN** `GET /api/request-logs` includes `latencyFirstTokenMs: null` or omits the field without failing dashboard schema validation
