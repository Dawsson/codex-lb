## Why

The Go dashboard projections endpoint still returns `weeklyCreditPace: null`.
Python computes this server-side from weekly account capacity, remaining
credits, recent secondary-window usage history, active account state,
freshness, and the dashboard working-days setting.

## What Changes

- Port the Python weekly credit pace response shape and core calculations to
  Go.
- Use account summaries plus recent `usage_history` rows to compute schedule
  gap, forecast burn, projected shortfall, status, and confidence.
- Return `weeklyCreditPace` from `GET /api/dashboard/projections` and reuse it
  on `GET /api/dashboard/overview` when available.

## Impact

- Affects Go dashboard overview/projections JSON.
- No database schema changes.
