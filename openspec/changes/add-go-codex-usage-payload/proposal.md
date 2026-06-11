## Why

The Go `/api/codex/usage` route currently returns a minimal hardcoded payload
for API-key callers. Python maps the calling API key's credit limits into the
Codex rate-limit status shape so Codex-compatible clients can see primary,
secondary, monthly, and credit balance state.

## What Changes

- Build the Go `/api/codex/usage` payload from `api_key_limits` via the
  existing self-usage repository path.
- Map credit limits into `rateLimit.primaryWindow`,
  `rateLimit.secondaryWindow`, `rateLimit.monthlyWindow`, and `credits`.
- Preserve the API-key compatibility auth path already used by Go.

## Impact

- Affects API-key callers of `GET /api/codex/usage`.
- No database schema changes.
