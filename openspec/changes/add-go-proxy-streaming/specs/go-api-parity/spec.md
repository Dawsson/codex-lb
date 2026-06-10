## ADDED Requirements

### Requirement: Go proxy supports SSE streaming for chat completions and responses

The Go API SHALL support Server-Sent Events streaming for
`POST /v1/chat/completions` and `POST /backend-api/codex/responses/compact`,
including periodic keepalive frames and terminal event detection, matching
the Python SSE framing.

#### Scenario: Streaming completion emits keepalive frames

- **WHEN** a streaming chat completion request is open and the upstream has
  not produced output for the configured keepalive interval
- **THEN** the Go API injects a keepalive SSE frame to the client without
  terminating the stream.

#### Scenario: Mid-stream upstream error produces a terminal error event

- **WHEN** the upstream connection fails partway through a streamed response
- **THEN** the Go API emits a terminal SSE event (`response.failed` or
  `error`) and closes the stream, and records the failure in the request
  log.

### Requirement: Go proxy supports WebSocket responses endpoints

The Go API SHALL expose WebSocket endpoints at `/backend-api/codex/responses`
and `/v1/responses` that proxy bidirectional response streaming to the
selected account's upstream.

#### Scenario: WebSocket client disconnect cleans up upstream connection

- **WHEN** a client disconnects from a WebSocket responses session before
  the upstream response completes
- **THEN** the Go API closes the corresponding upstream connection and does
  not leak the in-flight request slot.

### Requirement: Go proxy supports warmup requests

The Go API SHALL expose `POST /v1/warmup` and `POST /v1/warmup/{mode}` to
trigger limit-warmup sends for eligible accounts.

#### Scenario: Warmup request returns per-account results

- **WHEN** an operator calls `POST /v1/warmup`
- **THEN** the response lists submitted, skipped, and failed accounts
  matching the Python `WarmupResponse` schema.

### Requirement: Go ring membership keeps bridge registration current

The Go API SHALL register itself in the bridge ring on startup, send
periodic heartbeats, and mark itself stale on graceful shutdown, matching
the Python `RingMembershipService` semantics.

#### Scenario: Instance marks itself stale on shutdown

- **WHEN** the Go API receives a shutdown signal
- **THEN** it ages its ring membership row to stale within the configured
  grace period before the process exits.
