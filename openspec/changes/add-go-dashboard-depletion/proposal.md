## Why

The Go dashboard projections endpoint currently returns only `null`
projection fields. Python computes depletion safe-line markers from
`usage_history` using EWMA burn-rate formulas so the dashboard can render
server-provided `depletionPrimary` and `depletionSecondary`.

## What Changes

- Add Go depletion calculations using the Python EWMA/risk/safe-line formulas.
- Query recent `usage_history` rows and aggregate the worst-risk account per
  primary and secondary window.
- Return depletion results from `GET /api/dashboard/projections` and reuse them
  on `GET /api/dashboard/overview` when available.
- Leave weekly-credit pace and the Python memoized depletion cache for follow-up
  batches.

## Impact

- Affects Go dashboard overview/projections JSON.
- No database schema changes.
