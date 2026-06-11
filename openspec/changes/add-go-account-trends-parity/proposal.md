## Why

Python account trends normalize weekly-only primary usage into the secondary
trend series and build `secondaryScheduled` from weekly reset metadata. The Go
trend builder already exposes `secondaryScheduled`, but it does not apply the
same weekly-primary conflict handling, so some weekly accounts can render the
wrong trend series.

## What Changes

- Port Python `_effective_usage_trend_buckets` behavior into Go trend mapping.
- Treat weekly primary-window buckets as secondary trend data when appropriate.
- Add focused mapper tests for weekly-primary normalization and
  `secondaryScheduled`.

## Impact

- Affects `GET /api/accounts/{id}/trends` JSON values in the Go API.
- No database schema changes.
