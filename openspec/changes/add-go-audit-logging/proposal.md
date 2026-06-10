# Add audit logging to Go

## Why

Dashboard mutations (account changes, API key changes, settings changes,
firewall rules, etc.) are recorded in `audit_logs` and surfaced via
`GET /api/audit-logs`. The Go dashboard handlers currently perform these
mutations without recording audit entries.

## What Changes

- Add `internal/audit` repository (insert, list with action/limit/offset
  filters) - if not already added in `migrate-go-db-ownership`.
- Implement `GET /api/audit-logs`.
- Add audit-log writes to existing Go dashboard mutation handlers:
  accounts (update, pause, reactivate, delete, import, alias,
  routing-policy, limit-warmup), api-keys (create, update, delete,
  regenerate), firewall (create, delete), settings (update, proxy
  endpoint/pool/binding changes), sticky-sessions (purge/delete),
  quota-planner (settings update, decision cancel, warm-now).

## Impact

- Depends on `migrate-go-db-ownership` for the `audit_logs` table.
- Touches many existing handler files; keep each handler's audit-write
  change minimal (one insert call) to stay reviewable.
- Must capture actor IP and request ID consistent with Python
  (`actor_ip`, `request_id` fields).
