## Why

Codex CLI uses several `/backend-api/codex/*` control-plane routes outside
the Responses stream itself. Python forwards these routes to upstream with the
selected account credentials. The Go API currently only forwards JWKS control
routes, leaving thread goals, telemetry, safety, realtime, memory summarize,
opportunistic admission, and duplicated `/backend-api/codex/v1/*` aliases
missing.

## What Changes

- Add Go passthrough handlers for Codex control-plane HTTP routes.
- Forward raw upstream status/body and the Python allowlisted downstream
  response headers.
- Preserve query parameters, request method, request body, content type, and
  selected-account authentication.
- Add `/backend-api/codex/v1/<rest>` path aliasing before route matching.
- Add opportunistic admission checks for opportunistic API keys.

## Impact

- Affects Codex CLI compatibility through the Go API.
- No database schema changes.
