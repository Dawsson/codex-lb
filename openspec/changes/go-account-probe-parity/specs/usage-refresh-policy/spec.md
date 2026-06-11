## MODIFIED Requirements

### Requirement: Operators can probe an account to wake the upstream limiter

The dashboard MUST expose an admin-only endpoint that sends a single minimal `responses.create` directly to upstream pinned to one account, bypassing load-balancer scoring, then immediately refreshes or invalidates available account state so operators can verify whether the upstream limiter re-evaluated. The endpoint MUST surface the before/after usage and account status.

#### Scenario: Go probe uses configured upstream and fresh credentials

- **WHEN** the Go account probe endpoint sends the upstream wake-up request
- **THEN** it uses the configured upstream base URL and the existing HTTP client behavior supplied by runtime wiring
- **AND** it sends the Python-compatible minimal streaming `responses.create` payload with `max_output_tokens=1`, `stream=true`, and `store=false`
- **AND** it attempts existing authguardian credential refresh before decrypting the access token when authguardian is wired
- **AND** it reloads account credentials after a successful refresh before sending the upstream request
- **AND** it invalidates available account summary or routing caches after the probe attempt

#### Scenario: Go probe does not send synthetic account headers

- **WHEN** a probed account has a `chatgpt_account_id` beginning with `email_` or `local_`
- **THEN** the Go probe request omits the `chatgpt-account-id` upstream header
