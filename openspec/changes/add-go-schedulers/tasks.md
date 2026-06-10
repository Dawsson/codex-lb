## 1. API key limit reset scheduler

- [ ] Port `reset_expired_limits` and `release_stale_usage_reservations`
      to `internal/apikeys` repository.
- [ ] Hourly leader-elected loop.

## 2. Model refresh scheduler

- [ ] Port model registry refresh against upstream OpenAI model list.
- [ ] Configurable interval; update in-memory/cached registry used by
      `add-go-proxy-core`.

## 3. Sticky session cleanup scheduler

- [ ] Port expired sticky session purge using
      `internal/stickysessions` repository.

## 4. Quota planner scheduler

- [ ] Port periodic decision loop and warmup trigger
      (`app/modules/quota_planner/scheduler.py`).

## 5. Auth guardian scheduler

- [ ] Port `app/core/auth/guardian.py` token refresh/validation loop.

## 6. Cache invalidation poller

- [ ] Port `CacheInvalidationPoller`: continuous poll of invalidation
      signal table, clears API key cache and firewall IP cache.

## 7. Wiring & validation

- [ ] Start/stop all schedulers in `cmd/codex-lb-go/main.go` lifecycle,
      matching Python's startup order and graceful-shutdown drain.
- [ ] `go test ./...` with start/stop and single-tick tests per scheduler.
- [ ] `openspec validate add-go-schedulers --strict`
