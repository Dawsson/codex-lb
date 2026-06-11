## ADDED Requirements

### Requirement: Go dashboard overview excludes warm-up logs from activity aggregates

The Go dashboard overview API SHALL exclude request-log rows whose `request_kind`
is `warmup` or `limit_warmup` from overview activity metrics, trend buckets, and
top-error selection.

#### Scenario: Warm-up rows do not affect overview metrics
- **WHEN** request logs include normal rows and rows with `request_kind` equal to `warmup` or `limit_warmup`
- **THEN** Go dashboard overview activity metrics count only the normal rows
- **AND** token totals, cached token totals, error totals, and cost totals exclude the warm-up rows

#### Scenario: Warm-up rows do not affect overview trends
- **WHEN** request logs include normal rows and rows with `request_kind` equal to `warmup` or `limit_warmup` in the same overview window
- **THEN** Go dashboard overview trend buckets include only the normal rows

#### Scenario: Warm-up rows do not affect top error
- **WHEN** failed warm-up rows have error codes that would otherwise be the most frequent error code
- **THEN** Go dashboard overview top-error selection ignores those rows
- **AND** returns the most frequent non-warm-up failed error code when one exists
