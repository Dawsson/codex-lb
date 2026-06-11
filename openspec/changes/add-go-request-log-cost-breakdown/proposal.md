## Why

The Python dashboard request-log mapper returns a structured `costBreakdown`
object with input, cached-input, output, and total USD fields. The Go API
currently preserves the object shape but leaves the segment fields null, which
prevents the dashboard from rendering the same cost detail when pointed at Go.

## What Changes

- Port the Python `cost_breakdown_from_log` request-log behavior to the Go
  request-log mapper.
- Preserve Python fallback behavior for persisted totals that do not match the
  recalculated usage total.
- Cover the mapper with focused Go tests.

## Impact

- Affects `GET /api/request-logs` response mapping in the Go API.
- No database schema changes.
