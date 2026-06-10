# Migrate database ownership to Go (schema + models phase)

## Why

The Go API currently reads an existing SQLite database whose schema is owned
and migrated by the Python (Alembic) service. To fully replace the Python API,
the Go service must own the schema: it must be able to create and migrate the
full database from scratch via goose, and have Go model structs/repositories
for every table the Python API uses. This is the foundation every other
migration phase (proxy, schedulers, oauth, usage, audit, etc.) depends on.

## What Changes

- Port the full Alembic schema to goose migrations under `migrations/`,
  covering all tables currently defined in `app/db/models.py`:
  accounts, usage_history, additional_usage_history, request_logs, api_keys
  (+ account assignments), proxy_endpoints, proxy_pools, proxy_pool_members,
  account_proxy_bindings, account_limit_warmups, audit_logs,
  scheduler_leader, sticky_sessions, dashboard_settings, firewall_ips,
  quota_planner tables, conversation_archive tables, and any
  dashboard-auth/session tables not yet present.
- Add Go model structs and sqlc queries (or equivalent repository methods)
  for tables not yet covered by `internal/*` packages: usage_history,
  additional_usage_history, account_limit_warmups, audit_logs,
  scheduler_leader, conversation_archive.
- Add a `--check`-compatible startup path that can initialize a brand-new
  database (no pre-existing Python-created schema required).
- Document the cutover plan: existing SQLite databases created by Python
  must be compatible (no destructive changes to existing tables/columns).

## Impact

- All subsequent Go migration phases depend on this change.
- No behavior change for existing Go dashboard endpoints; this is additive
  schema/model work plus a goose migration path that is idempotent against
  existing Python-managed databases (uses `IF NOT EXISTS` / baseline marker).
- Existing Python service continues to operate unchanged during this phase.
