## Why

The Go request-log search path claims parity with Python, but it searches fewer
fields. Operators switching the dashboard to Go can miss rows that Python finds
by account email, reasoning effort, source, status, API key id, timestamp, or
latency.

## What Changes

- Extend Go request-log list and option filtering to search the same persisted
  fields as Python.
- Join account metadata where search predicates require account email.
- Add focused repository coverage for representative newly searchable fields.

## Impact

- Affects `GET /api/request-logs` and `GET /api/request-logs/options` search
  semantics.
- No database schema changes.
