## 1. Repository

- [ ] `internal/limitwarmup` repository: CRUD for `account_limit_warmups`,
      uniqueness on (account_id, window, reset_at).

## 2. Service

- [ ] Port `LimitWarmupService.run_after_usage_refresh` decision logic
      (which accounts/windows qualify for warmup).
- [ ] Port `StreamingLimitWarmupSender` using the streaming proxy client
      with `x-codex-lb-limit-warmup` header and "Reply with OK only."
      instructions.
- [ ] Preserve `_MAX_CONCURRENT_WARMUP_SENDS = 4` concurrency bound.
- [ ] Port terminal event / quota error code handling
      (`_TERMINAL_ERROR_EVENTS`, `_QUOTA_ERROR_CODES`).

## 3. Wiring

- [ ] Hook into usage refresh scheduler post-write.
- [ ] `PUT /api/accounts/{accountID}/limit-warmup` toggle endpoint.
- [ ] Quota planner `warm-now` action.

## 4. Validation

- [ ] `go test ./...`
- [ ] `openspec validate add-go-limit-warmup --strict`
