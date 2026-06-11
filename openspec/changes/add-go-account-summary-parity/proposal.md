# Add Go account summary parity

## Why

The Go API currently returns a minimal `GET /api/accounts` account summary.
The dashboard depends on the richer Python `accounts/mappers.py` contract for
duplicate detection, quota-window presentation, request usage, additional
quota visibility, auth state, and limit warm-up status. Without this mapper
parity, the Go API can load the dashboard but remains schema- and behavior-risky
as a production replacement.

## What Changes

- Port the dashboard account summary mapper behavior into the Go accounts
  module.
- Load all SQLite data needed by the mapper, including runtime status fields,
  latest usage windows, request-log aggregates, additional usage rows, limit
  warm-up rows, and auth-token status metadata.
- Preserve existing dashboard JSON field names and null/empty-array semantics.
- Add focused Go tests for duplicate detection, usage-window mapping, request
  usage, additional quotas, and limit warm-up status.

## Impact

- `GET /api/accounts` and dashboard overview account payloads become closer to
  the Python API contract.
- The frontend can rely on Go responses without client-side schema surprises.
