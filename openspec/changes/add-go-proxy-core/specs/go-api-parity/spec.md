## ADDED Requirements

### Requirement: Go proxy selects accounts using configured routing strategy

The Go API SHALL select an upstream account for proxy requests according to
the dashboard-configured routing strategy (capacity_weighted,
relative_availability, fill_first, single_account) and per-account routing
policy (normal, burn_first, preserve), matching the Python load balancer's
selection semantics.

#### Scenario: Capacity-weighted selection skips unhealthy accounts

- **WHEN** a proxy request arrives and `routing_strategy` is
  `capacity_weighted`
- **THEN** only accounts with status `active` and not bound to an
  unavailable proxy pool are eligible
- **AND** selection probability is weighted by remaining capacity as
  computed by the ported load balancer logic.

#### Scenario: Burn-first accounts are preferred until exhausted

- **GIVEN** an account has `routing_policy = burn_first`
- **WHEN** that account is `active` and has remaining capacity
- **THEN** the load balancer prefers it over `normal` accounts before
  falling back to the configured strategy.

### Requirement: Go proxy validates API keys and enforces request policy

The Go API SHALL validate proxy API keys, enforce per-key rate limits, and
apply request policy normalization (model alias resolution, access
validation, strict text/function-tools formatting) before forwarding
requests upstream.

#### Scenario: Invalid or rate-limited API key is rejected

- **WHEN** a request to `/v1/chat/completions` includes an API key that is
  invalid or has exceeded its configured rate limit
- **THEN** the Go API returns an OpenAI-formatted error response without
  forwarding the request upstream.

### Requirement: Go exposes model listing and rate-limit status endpoints

The Go API SHALL expose `GET /v1/models`, `GET /backend-api/codex/models`,
and `GET /api/codex/usage` (and trailing-slash variant) matching the Python
schemas.

#### Scenario: Model listing filters non-public models

- **WHEN** a client requests `GET /v1/models`
- **THEN** the response includes only models for which `is_public_model`
  is true, using the same model registry data as the Python service.

### Requirement: Go forwards non-streaming chat completions to upstream

The Go API SHALL forward non-streaming `POST /v1/chat/completions` requests
to the selected account's upstream using that account's credentials, and
record a request log entry with token usage and latency.

#### Scenario: Successful non-streaming completion is logged

- **WHEN** a non-streaming chat completion request succeeds upstream
- **THEN** the Go API returns the OpenAI-formatted completion response
- **AND** writes a `request_logs` row including model, account_id, status,
  input/output token counts, and latency_ms.
