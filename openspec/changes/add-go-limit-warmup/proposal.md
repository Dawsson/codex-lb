# Add limit warmup service to Go

## Why

When an account recovers from rate-limit/quota-exceeded status, the Python
service can proactively send a minimal "warmup" request to confirm the
account is usable before routing real traffic to it
(`app/modules/limit_warmup`). This is used by the usage refresh scheduler
and the quota planner.

## What Changes

- Port `app/modules/limit_warmup/repository.py` and `service.py`:
  `AccountLimitWarmup` CRUD, `LimitWarmupService.run_after_usage_refresh`,
  `StreamingLimitWarmupSender`.
- Integrate with `add-go-proxy-streaming`'s response-streaming client to
  send the warmup request (`x-codex-lb-limit-warmup` header, "Reply with OK
  only." instructions).
- Wire into `add-go-usage-tracking`'s refresh scheduler
  (`run_after_usage_refresh` call after successful usage write) and
  `add-go-schedulers`'s quota planner (`warm-now` action,
  `PUT /api/accounts/{accountID}/limit-warmup`).

## Impact

- Depends on `migrate-go-db-ownership` (account_limit_warmups table),
  `add-go-usage-tracking` (refresh scheduler hook point), and
  `add-go-proxy-streaming` (streaming client used to send warmup requests).
- Concurrency limit (`_MAX_CONCURRENT_WARMUP_SENDS = 4`) must be preserved.
