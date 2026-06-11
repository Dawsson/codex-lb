## ADDED Requirements

### Requirement: Go runtime exposes loopback-only graceful drain controls

The Go API SHALL expose internal graceful drain controls compatible with the
Python runtime.

#### Scenario: Loopback client starts drain
- **WHEN** a loopback client posts to `POST /internal/drain/start`
- **THEN** the Go API marks HTTP drain and bridge drain active
- **AND** returns status `ok` with `checks.draining = "ok"`

#### Scenario: Non-loopback client cannot control drain
- **WHEN** a non-loopback client calls any `/internal/drain/*` route
- **THEN** the Go API returns 403

#### Scenario: Readiness fails while draining
- **WHEN** drain is active
- **THEN** `GET /health/ready` returns 503

#### Scenario: Drain middleware rejects new HTTP work
- **WHEN** drain is active
- **AND** a request targets a non-allowlisted HTTP path
- **THEN** the Go API returns 503 with a service-unavailable error envelope

#### Scenario: Drain status reports in-flight count
- **WHEN** a loopback client requests `GET /internal/drain/status`
- **THEN** the Go API returns `checks.draining`, `checks.bridge_drain_active`, and `checks.in_flight`
