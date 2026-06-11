## ADDED Requirements

### Requirement: Go dashboard top error summarizes failed logs only

The Go dashboard API SHALL compute overview `metrics.topError` from failed
request logs only.

#### Scenario: Successful row has an error code
- **WHEN** a successful request-log row has a non-empty `error_code`
- **THEN** the Go dashboard overview top-error query ignores that row

#### Scenario: Failed row has blank error code
- **WHEN** a failed request-log row has an empty `error_code`
- **THEN** the Go dashboard overview top-error query ignores that row

#### Scenario: Failed rows have error codes
- **WHEN** failed request-log rows have non-empty `error_code` values
- **THEN** the Go dashboard overview top-error query returns the most frequent code
