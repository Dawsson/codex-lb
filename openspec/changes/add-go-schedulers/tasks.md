## 1. API key limit reset scheduler

- [x] Port `reset_expired_limits` and `release_stale_usage_reservations`
      to `internal/apikeys` repository.
  - [x] `reset_expired_limits`.
  - [x] `release_stale_usage_reservations`.
- [x] Hourly leader-elected loop.

## 2. Model refresh scheduler

- [x] Port model registry refresh against upstream OpenAI model list.
- [x] Configurable interval; update in-memory/cached registry used by
      `add-go-proxy-core`.

## 3. Sticky session cleanup scheduler

- [x] Port expired sticky session purge using
      `internal/stickysessions` repository.

## 4. Quota planner scheduler

- [x] Port periodic decision loop and warmup trigger
      (`app/modules/quota_planner/scheduler.py`).
  - [x] Leader-elected periodic loop with configurable enable/interval.
  - [x] Shadow/suggest modes record bounded no-op decision ticks.
  - [x] Auto mode executes due planned warmup decisions through the
        limit-warmup sender and request-log path.

## 5. Auth guardian scheduler

- [x] Port `app/core/auth/guardian.py` token refresh/validation loop.
  - [x] Leader election, stale active account selection, batch/concurrency
        bounds, failure backoff, OAuth refresh, token persistence, permanent
        failure status updates, and cache invalidation.

## 6. Cache invalidation poller

- [x] Port `CacheInvalidationPoller`: continuous poll of invalidation
      signal table, clears API key cache and firewall IP cache.

## 7. Wiring & validation

- [ ] Start/stop all schedulers in `cmd/codex-lb-go/main.go` lifecycle,
      matching Python's startup order and graceful-shutdown drain.
- [x] Start/stop usage refresh scheduler in `cmd/codex-lb-go/main.go`
      lifecycle.
- [x] Start/stop API key limit reset scheduler in `cmd/codex-lb-go/main.go`
      lifecycle.
- [x] Start/stop cache invalidation poller in `cmd/codex-lb-go/main.go`
      lifecycle.
- [x] Start/stop sticky session cleanup scheduler in `cmd/codex-lb-go/main.go`
      lifecycle.
- [x] Start/stop model refresh scheduler in `cmd/codex-lb-go/main.go`
      lifecycle.
- [x] Start/stop auth guardian scheduler in `cmd/codex-lb-go/main.go`
      lifecycle.
- [ ] `go test ./...` with start/stop and single-tick tests per scheduler.
  - [x] Quota planner single-tick tests cover no-op and auto warmup execution.
- [x] `go build ./... && go test ./...`
- [x] `openspec validate add-go-schedulers --strict`
