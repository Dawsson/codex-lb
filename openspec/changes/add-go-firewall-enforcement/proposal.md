# Add Go firewall enforcement

## Why

The Go API exposes firewall allowlist management routes but does not enforce
the allowlist on proxy-facing paths. Production replacement requires the Go
runtime to preserve the Python API firewall behavior for `/v1/*` and
`/backend-api/codex/*`.

## What Changes

- Add Go proxy-path firewall middleware using `api_firewall_allowlist`.
- Resolve client IP from the socket by default and optionally from
  `X-Forwarded-For` when the socket peer is a trusted proxy.
- Cache per-IP allow/deny decisions with a configurable TTL.
- Invalidate the cache immediately on firewall allowlist mutations.
- Register the middleware for Go proxy paths without restricting dashboard
  `/api/*` routes.

## Impact

- Go proxy routes become fail-closed for unlisted remote clients when the
  allowlist is active.
- Dashboard firewall management remains available under normal dashboard auth.
