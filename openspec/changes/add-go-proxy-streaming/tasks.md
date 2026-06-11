## 1. SSE primitives

- [x] Port `format_sse_event`, `inject_sse_keepalives`,
      `parse_sse_data_json`, `CODEX_KEEPALIVE_FRAME`/`SSE_KEEPALIVE_FRAME`.

## 2. Streaming chat completions

- [x] `POST /v1/chat/completions` streaming path (SSE response).
- [x] Stream-level error handling and terminal event detection.

## 3. Responses (Codex) endpoints

- [x] `POST /backend-api/codex/responses/compact`
- [x] `POST /backend-api/codex/responses` (SSE)
- [x] WebSocket `/backend-api/codex/responses`
- [x] WebSocket `/v1/responses`
- [x] `POST /v1/responses` (SSE)
- [x] `POST /v1/responses/compact`
  - [x] Base compact payload mapping, account selection, upstream compact call,
        request logging, and API-key reservation settlement.
- [ ] Previous-response-not-found recovery handling (basic in-memory owner index only).

## 4. Warmup endpoints

- [x] `POST /v1/warmup`
- [x] `POST /v1/warmup/{mode}`
  - [x] Mode validation for `normal`, `strict`, and `force`.
  - [x] Python-compatible `WarmupResponse` submitted/skipped/failed schema.
  - [x] Active account fan-out with API-key account-scope filtering,
        primary-window eligibility, request logging, and reservation settlement.

## 5. Bridge / ring membership

- [ ] Port `ring_membership.py` (register, heartbeat, mark_stale,
      list_active) -> `internal/proxy/ring`.
- [ ] Port `durable_bridge_repository.py` and
      `durable_bridge_coordinator.py`.
- [ ] `POST /internal/bridge/{endpointID}/session/{sessionID}` +
      `parse_forwarded_request`.
- [ ] Wire heartbeat task + graceful drain (mark stale, close sessions) into
      `cmd/codex-lb-go/main.go` lifecycle.

## 6. Validation

- [x] `go test ./...` including stream unit tests (SSE keepalive, chat chunks,
      previous-response index, turn-state).
- [ ] Manual smoke test with Codex CLI against Go API.
- [x] `openspec validate add-go-proxy-streaming --strict`
