## MODIFIED Requirements

### Requirement: Firewall enforcement for protected proxy paths

The Go API SHALL enforce firewall allowlist decisions for proxy-facing paths
`/backend-api/codex/*` and `/v1/*` using the same OpenAI-style forbidden error
contract as the Python API. Empty allowlists SHALL keep proxy paths in
allow-all mode. Dashboard endpoints under `/api/*` SHALL NOT be restricted by
the proxy firewall middleware.

#### Scenario: Go allowlist active blocks unlisted proxy client

- **WHEN** `api_firewall_allowlist` contains one or more IP entries
- **AND** a Go proxy request to `/v1/chat/completions` comes from an unlisted IP
- **THEN** the response is HTTP 403
- **AND** the response error code is `ip_forbidden`

#### Scenario: Go dashboard endpoints are not restricted

- **WHEN** `api_firewall_allowlist` contains one or more IP entries
- **AND** a dashboard request targets `/api/firewall/ips`
- **THEN** the firewall middleware does not reject it solely because the client
  IP is unlisted

### Requirement: Trusted proxy header handling

The Go API SHALL optionally resolve the firewall client IP from
`X-Forwarded-For` only when proxy-header trust is enabled and the socket source
IP belongs to the configured trusted proxy CIDR list.

#### Scenario: Go trusted proxy source

- **WHEN** `CODEX_LB_FIREWALL_TRUST_PROXY_HEADERS=true`
- **AND** the request socket IP matches `CODEX_LB_FIREWALL_TRUSTED_PROXY_CIDRS`
- **AND** `X-Forwarded-For` contains a valid client IP chain
- **THEN** the Go firewall checks the resolved forwarded client IP

### Requirement: Firewall IP cache TTL is operator-configurable with a safe default

The Go API SHALL cache firewall decisions per resolved client IP. The default
TTL SHALL be 30 seconds and operators SHALL be able to tune it with
`CODEX_LB_FIREWALL_IP_CACHE_TTL_SECONDS`. Firewall allowlist mutations SHALL
invalidate this cache before responding.

#### Scenario: Go firewall mutation invalidates cache

- **WHEN** an operator adds or removes an allowlist IP through the Go API
- **THEN** the next proxy request re-checks the database rather than using a
  stale cached decision
