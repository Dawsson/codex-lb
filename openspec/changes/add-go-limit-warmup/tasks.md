## 1. Repository

- [x] `internal/limitwarmup` repository: CRUD for `account_limit_warmups`,
      uniqueness on (account_id, window, reset_at).

## 2. Service

- [x] Port `LimitWarmupService.run_after_usage_refresh` decision logic
      (which accounts/windows qualify for warmup).
- [x] Port `StreamingLimitWarmupSender` using the streaming proxy client
      with `x-codex-lb-limit-warmup` header and "Reply with OK only."
      instructions.
- [x] Preserve `_MAX_CONCURRENT_WARMUP_SENDS = 4` concurrency bound.
- [x] Port terminal event / quota error code handling
      (`_TERMINAL_ERROR_EVENTS`, `_QUOTA_ERROR_CODES`).
  - [x] Service maps quota error codes to `quota_still_exhausted`.
  - [x] Streaming sender parses upstream SSE terminal events.

## 3. Wiring

- [x] Hook into usage refresh scheduler post-write.
- [x] `PUT /api/accounts/{accountID}/limit-warmup` toggle endpoint.
- [x] Quota planner `warm-now` action.
  - [x] Direct account probe path.
  - [ ] API-key reservation enforcement for `apiKeyId` remains tracked under API-key reservation parity.

## 4. Validation

- [x] `go test ./...`
- [ ] `openspec validate add-go-limit-warmup --strict`
