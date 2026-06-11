## Why

The Python request-log API exposes the persisted first-token latency field, but
the Go request-log mapper drops it even though the repository already reads the
column. Dashboard clients need the Go API response contract to include this
field so switching backends does not hide stream timing data.

## What Changes

- Expose `latencyFirstTokenMs` from `GET /api/request-logs` in the Go API.
- Accept the field in the dashboard request-log response schema.
- Cover the mapper behavior with a focused regression test.

## Impact

- Affects the Go request-log response contract.
- Affects dashboard schema validation for request-log rows.
