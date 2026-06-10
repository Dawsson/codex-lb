# Add usage tracking API and refresh scheduler to Go

## Why

The dashboard and proxy depend on per-account usage windows (primary,
secondary, monthly) to drive routing, account status recovery, and the
usage dashboard pages. The Python `usage` module and
`app/core/usage/refresh_scheduler.py` are the source of truth for this and
must be ported before the proxy can make routing decisions in Go.

## What Changes

- Port `app/core/usage/*` (pricing, quota, depletion, types, models) logic
  to `internal/usage`.
- Implement `GET /api/usage/summary`, `GET /api/usage/history`,
  `GET /api/usage/window` matching the Python response schemas.
- Implement the usage refresh scheduler: periodically calls upstream OpenAI
  usage APIs per account, writes `usage_history`/`additional_usage_history`
  rows, and reconciles `RATE_LIMITED`/`QUOTA_EXCEEDED` account statuses back
  to `ACTIVE` when usage allows (port of `reconcile_recoverable_account_statuses`).
- Add leader-election gating (depends on `migrate-go-db-ownership`'s
  `scheduler_leader` table/repository) so only one replica runs the refresh.
- Wire the scheduler into `cmd/codex-lb-go/main.go` startup/shutdown.

## Impact

- Depends on `migrate-go-db-ownership` for usage tables and leader election.
- New scheduled background work in the Go process; must respect graceful
  shutdown (drain in-flight refresh before exit).
- Account status transitions performed by this scheduler must use the same
  optimistic-concurrency guard as Python (`update_status_if_current`) to
  avoid races with manual dashboard actions.
