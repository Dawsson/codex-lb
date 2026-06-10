## 1. SSE primitives

- [ ] Port `format_sse_event`, `inject_sse_keepalives`,
      `parse_sse_data_json`, `CODEX_KEEPALIVE_FRAME`/`SSE_KEEPALIVE_FRAME`.

## 2. Streaming chat completions

- [ ] `POST /v1/chat/completions` streaming path (SSE response).
- [ ] Stream-level error handling and terminal event detection.

## 3. Responses (Codex) endpoints

- [ ] `POST /backend-api/codex/responses/compact`
- [ ] WebSocket `/backend-api/codex/responses`
- [ ] WebSocket `/v1/responses`
- [ ] Previous-response-not-found recovery handling.

## 4. Warmup endpoints

- [ ] `POST /v1/warmup`
- [ ] `POST /v1/warmup/{mode}`

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

- [ ] `go test ./...` including partial-failure stream tests (mid-stream
      upstream error, client disconnect).
- [ ] `openspec validate add-go-proxy-streaming --strict`
