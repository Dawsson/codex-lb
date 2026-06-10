# Add remaining background schedulers to Go

## Why

Beyond the usage refresh scheduler (`add-go-usage-tracking`), the Python
service runs several leader-elected or continuous background loops that
keep the system healthy. These must run in the Go process for full parity.

## What Changes

- Port `app/modules/api_keys/reset_scheduler.py`: hourly reset of expired
  API key limits and release of stale usage reservations.
- Port `app/core/openai/model_refresh_scheduler.py`: periodic refresh of
  the OpenAI model registry.
- Port `app/modules/sticky_sessions/cleanup_scheduler.py`: periodic purge
  of expired sticky sessions.
- Port `app/modules/quota_planner/scheduler.py`: periodic quota-planning
  decisions and warmup triggers.
- Port `app/core/auth/guardian.py` (`build_auth_guardian_scheduler`):
  periodic token refresh/validation for accounts nearing expiry.
- Port `CacheInvalidationPoller` (`app/core/cache/invalidation.py`):
  continuous polling for API-key-cache and firewall-IP-cache invalidation
  signals.
- Wire all schedulers into `cmd/codex-lb-go/main.go` startup/shutdown with
  the same leader-election gating used in `add-go-usage-tracking`.

## Impact

- Depends on `migrate-go-db-ownership` (scheduler_leader, account_limit_warmups)
  and `add-go-usage-tracking` (leader election helper, usage repo).
- Depends on `add-go-limit-warmup` for the quota planner's warmup trigger
  path (or that phase can land after, with the scheduler initially calling
  a stub/no-op until limit warmup lands).
- Each scheduler should be testable in isolation (start/stop, single-tick
  execution) without requiring real upstream calls.
