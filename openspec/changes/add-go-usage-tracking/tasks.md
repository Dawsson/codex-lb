## 1. Core usage logic

- [x] Port plan-capacity tables, `capacity_for_plan`,
      `default_window_minutes`, `resolve_window_minutes`,
      `remaining_percent_from_used`, `remaining_credits_from_used`,
      `remaining_credits_from_percent`, `summarize_usage_window`,
      `normalize_usage_window` to `internal/usage/core.go`. (`pricing.py`,
      `quota.py`, `depletion.py` are not needed: the 3 endpoints below use
      only persisted `request_logs` fields via `cost_from_log`-equivalent
      SQL aggregation.)
- [x] Port `normalize_weekly_only_rows` / `_should_prefer_primary_row`,
      `resolve_window_minutes`, `capacity_for_plan` helpers.

## 2. Repository

- [x] `UsageRepository`: latest_by_account(window), aggregate_since(since,
      window), insert (already present from Phase 1).
- [x] `RequestLogsRepository.AggregateCostMetrics(since)`: cost/token/error
      aggregate query backing the summary endpoint's `cost`/`metrics`
      fields.
- [x] `AdditionalUsageRepository`: latest/insert/list/delete for additional
      quota keys used by Spark/additional-quota routing.

## 3. API endpoints

- [x] `GET /api/usage/summary`
- [x] `GET /api/usage/history?hours=`
- [x] `GET /api/usage/window?window=primary|secondary`
- [x] Match Python response schemas exactly (field names/types) for
      frontend compatibility.

## 4. Refresh scheduler

- [ ] Port `UsageUpdater.refresh_accounts` (calls upstream OpenAI usage
      endpoints per account using stored credentials).
  - [x] Primary/secondary usage fetch and insert.
  - [x] Additional quota usage fetch/merge/canonicalize/insert and stale row
        pruning.
  - [x] Additional-only freshness cache parity.
  - [x] Deactivate or mark reauth-required for permanent usage fetch errors.
  - [x] Identity metadata sync from usage payload.
  - [x] Auth refresh retry on 401 usage fetch failures.
  - [x] Usage-payload status recovery.
- [x] Port `reconcile_recoverable_account_statuses`.
- [x] Leader-election gated loop with configurable interval
      (`usage_refresh_interval_seconds`, `usage_refresh_enabled`).
- [x] Invalidate rate-limit header cache and account selection cache after
      a successful refresh.
- [x] Wire start/stop into `cmd/codex-lb-go/main.go` lifecycle.

## 5. Validation

- [x] Focused `go test ./internal/usagerefresh ./internal/usage
      ./internal/accounts ./cmd/codex-lb-go` including scheduler start/stop and
      reconciliation unit tests.
- [ ] `go test ./...` including scheduler start/stop and reconciliation
      unit tests with fake clock/repository.
- [x] `go build ./... && go test ./...`
- [x] `openspec validate add-go-usage-tracking --strict`
