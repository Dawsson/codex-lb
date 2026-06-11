## Why

Python dashboard overview sorts account summaries by primary capacity credits
descending so high-capacity accounts appear first. The Go overview currently
inherits the general accounts-list ordering, which can differ from Python and
from the frontend architecture spec.

## What Changes

- Sort `GET /api/dashboard/overview` account summaries by
  `capacityCreditsPrimary` descending.
- Keep accounts without primary capacity after accounts with capacity.
- Preserve the existing `/api/accounts` list ordering.

## Impact

- Affects only Go dashboard overview account ordering.
- No database schema changes.
