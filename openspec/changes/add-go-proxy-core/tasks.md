## 1. Account selection / load balancing

- [x] Port `account_cache.py` (selection cache + invalidation) as a generic
      `AccountSelectionCache[T]`. The cached payload type (the Go equivalent
      of `SelectionInputs`) lands with the load balancer port below.
- [ ] Port `load_balancer.py` selection strategies (capacity_weighted,
      relative_availability_power/top_k, fill_first, single_account).
- [ ] Port routing policy handling (normal/burn_first/preserve) and
      `background_recovery_state_from_account`.
- [ ] Port `affinity.py` (sticky session affinity lookups).

## 2. Request policy & validation

- [ ] Port `request_policy.py`: model alias resolution, access validation,
      strict text/function-tools format enforcement, payload normalization.
- [ ] Port proxy API key validation + rate limit enforcement
      (`apply_api_key_enforcement`, `ApiKeyRateLimitExceededError`).
- [x] Port `validate_proxy_api_key` / `validate_proxy_api_key_authorization`
      (proxy bearer-token auth + loopback-only unauthenticated access).
      Rate-limit reservation/release (`_enforce_request_limits` /
      `_release_reservation`) is deferred to the rate-limit enforcement work
      above.

## 3. Model registry

- [x] Port `model_registry.py` (static registry + `is_public_model`).
- [x] `GET /v1/models`
- [x] `GET /backend-api/codex/models`

## 4. Chat completions (non-streaming)

- [ ] `POST /v1/chat/completions` (non-streaming path): select account,
      forward to upstream with account credentials, parse response,
      record request log + usage.
- [ ] Error envelope formatting matching `app/core/errors.py`.

## 5. Usage/rate-limit status

- [ ] `GET /api/codex/usage` and `/api/codex/usage/`
      (`RateLimitStatusPayload`).

## 6. Validation

- [ ] `go test ./...`
- [ ] Manual smoke test against a real account via `/v1/chat/completions`.
- [ ] `openspec validate add-go-proxy-core --strict`
