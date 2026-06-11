## ADDED Requirements

### Requirement: Go forwards Codex control-plane HTTP routes

The Go API SHALL forward Codex control-plane routes under
`/backend-api/codex/*` to upstream using selected account credentials and the
same proxy API-key authentication rules as Python.

#### Scenario: Thread goal get accepts GET and POST
- **WHEN** a client calls `GET` or `POST /backend-api/codex/thread/goal/get`
- **THEN** Go forwards the request to upstream `/backend-api/codex/thread/goal/get`
- **AND** preserves query parameters for GET, JSON body for POST, and selected
  account authentication.

#### Scenario: Codex control response is raw passthrough
- **WHEN** upstream returns a non-error control response
- **THEN** Go returns the same HTTP status and body
- **AND** forwards only the Python allowlisted response headers.

#### Scenario: Codex v1 alias reaches canonical route
- **WHEN** a client requests `/backend-api/codex/v1/<rest>` with a non-empty
  rest path
- **THEN** Go routes it as `/backend-api/codex/<rest>`.

### Requirement: Go exposes opportunistic admission control

The Go API SHALL expose `GET /backend-api/codex/opportunistic/admission`.

#### Scenario: Foreground clients are admitted
- **WHEN** a non-opportunistic API key or loopback unauthenticated client calls
  the endpoint
- **THEN** Go returns `{"admitted": true}`.

#### Scenario: Opportunistic keys are rejected when no account is available
- **WHEN** an opportunistic API key calls the endpoint and account selection
  cannot return a candidate
- **THEN** Go returns HTTP 429 with an OpenAI-style rate-limit error and a
  `Retry-After` header.
