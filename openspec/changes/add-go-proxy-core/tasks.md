## 1. Account selection / load balancing

- [x] Port `account_cache.py` (selection cache + invalidation) as a generic
      `AccountSelectionCache[T]`. The cached payload type (the Go equivalent
      of `SelectionInputs`) lands with the load balancer port below.
- [x] Port `load_balancer.py` selection strategies (capacity_weighted,
      relative_availability_power/top_k, fill_first, single_account) via
      `balancer_logic.go`.
- [x] Port routing policy handling (normal/burn_first/preserve) and
      `background_recovery_state_from_account`.
- [x] Port `affinity.py` sticky session affinity lookups for responses
      selection (codex-session headers, prompt-cache TTL lookups, sticky-thread
      fallback, and first-turn file-pin precedence).
- [x] Port additional-quota gated model routing (`codex_spark` / `gpt-5.3-codex-spark`).
- [x] Wire `LoadBalancer` orchestration (`load_balancer_service.go`) with DB
      selection-input loading, leasing, and `SelectAccountPreferringBudgetSafe`.

## 2. Request policy & validation

- [x] Port `request_policy.py`: model alias resolution, access validation,
      API-key model/reasoning/service-tier enforcement (strict schema
      validation deferred).
- [x] Port proxy API key rate limit admission enforcement
      (`apply_api_key_enforcement`, request/token/cost reservation guard).
- [ ] Port full API-key reservation lifecycle (heartbeat, exact settlement,
      stale-release scheduler, bridge reservation forwarding).
  - [x] Admission reservations for proxy request/token/cost limits.
  - [x] Heartbeat active stream/WebSocket reservations.
  - [x] Finalize/fail reservations with actual token/cost deltas.
  - [x] Stale reservation release scheduler.
  - [ ] Bridge reservation forwarding.
- [x] Port `validate_proxy_api_key` / `validate_proxy_api_key_authorization`
      (proxy bearer-token auth + loopback-only unauthenticated access).

## 3. Model registry

- [x] Port `model_registry.py` (static registry + `is_public_model`).
- [x] `GET /v1/models`
- [x] `GET /backend-api/codex/models`
- [x] Request-limit reservation/release on model-list endpoints.

## 4. Chat completions (non-streaming)

- [x] `POST /v1/chat/completions` (non-streaming path): select account,
      forward to upstream with account credentials, parse response,
      record request log + usage.
- [x] Error envelope formatting matching `app/core/errors.py` (core paths).

## 5. Usage/rate-limit status

- [x] `GET /api/codex/usage` and `/api/codex/usage/`
      (`RateLimitStatusPayload` basic shape).
- [x] `GET /v1/usage` for the calling API key, including current key
      limits and reset-before-read behavior.

## 6. Validation

- [x] `go test ./...`
- [ ] Manual smoke test against a real account via `/v1/chat/completions`.
- [x] `openspec validate add-go-proxy-core --strict`
