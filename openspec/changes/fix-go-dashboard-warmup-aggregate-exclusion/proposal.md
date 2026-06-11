## Why

Go dashboard overview aggregates currently include request-log rows whose
`request_kind` is `warmup` or `limit_warmup`. Python dashboard aggregation
excludes those rows from overview metrics, trends, and top-error selection, so
the Go port can report operator-visible activity inflated by internal warm-up
traffic.

## What Changes

- Exclude `warmup` and `limit_warmup` request-log rows from Go dashboard
  overview activity metrics.
- Exclude those rows from Go dashboard overview trends.
- Exclude those rows from Go dashboard overview top-error selection.
- Add focused Go dashboard repository coverage for the parity predicate.

## Impact

- Affects `GET /api/dashboard/overview` summary metrics and trends in the Go
  dashboard implementation.
- No database schema changes.
