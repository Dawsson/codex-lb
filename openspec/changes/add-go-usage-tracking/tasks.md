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
- [ ] `AdditionalUsageRepository`: latest/insert for additional quota keys
      (deferred -- not required by `/api/usage/{summary,history,window}`).

## 3. API endpoints

- [x] `GET /api/usage/summary`
- [x] `GET /api/usage/history?hours=`
- [x] `GET /api/usage/window?window=primary|secondary`
- [x] Match Python response schemas exactly (field names/types) for
      frontend compatibility.

## 4. Refresh scheduler

- [ ] Port `UsageUpdater.refresh_accounts` (calls upstream OpenAI usage
      endpoints per account using stored credentials).
- [ ] Port `reconcile_recoverable_account_statuses`.
- [ ] Leader-election gated loop with configurable interval
      (`usage_refresh_interval_seconds`, `usage_refresh_enabled`).
- [ ] Invalidate rate-limit header cache and account selection cache after
      a successful refresh.
- [ ] Wire start/stop into `cmd/codex-lb-go/main.go` lifecycle.

## 5. Validation

- [ ] `go test ./...` including scheduler start/stop and reconciliation
      unit tests with fake clock/repository.
- [ ] `openspec validate add-go-usage-tracking --strict`
