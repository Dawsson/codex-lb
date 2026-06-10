# Add core proxy endpoints (chat completions, responses, models) to Go

## Why

The proxy is the primary function of codex-lb: it accepts OpenAI-compatible
requests, selects a healthy upstream account, forwards the request with that
account's credentials, and records usage/request logs. None of this exists
in Go yet. This phase ports the non-streaming, non-media core: model
listing, account selection/load balancing, request policy enforcement, and
basic chat/responses request forwarding (buffered, not SSE/WebSocket -
streaming is `add-go-proxy-streaming`).

## What Changes

- Port `app/modules/proxy/load_balancer.py`, `account_cache.py`,
  `affinity.py`, `request_policy.py`, `helpers.py`, `repo_bundle.py` to
  `internal/proxy`.
- Implement account selection strategies from `dashboard_settings`
  (capacity_weighted, relative_availability, fill_first, single_account,
  burn_first/preserve routing policies).
- Implement `GET /v1/models` and `GET /backend-api/codex/models`
  (model registry + alias resolution, `is_public_model` filtering).
- Implement proxy API key validation (`validate_proxy_api_key`,
  `validate_proxy_api_key_authorization`) reusing `internal/apikeys`.
- Implement non-streaming `POST /v1/chat/completions` request forwarding to
  the selected account's upstream, with request log recording.
- Implement `GET /api/codex/usage` and `GET /api/codex/usage/` (rate limit
  status payload) per `app/modules/proxy/schemas.py`
  `RateLimitStatusPayload`.
- Implement request/response error envelope formatting
  (`app/core/errors.py` equivalents).

## Impact

- Depends on `migrate-go-db-ownership` (proxy tables already mostly exist
  via `internal/settings`) and `add-go-usage-tracking` (account
  status/usage used for selection).
- This phase intentionally excludes SSE streaming, WebSockets, images,
  audio, files, and warmup - those are separate phases to keep this PR
  reviewable.
- Existing Python proxy remains the production path until this and the
  streaming phase are both complete and verified.
