# Add streaming (SSE/WebSocket) proxy endpoints to Go

## Why

Codex/Responses traffic is predominantly streamed via Server-Sent Events and
WebSockets. `add-go-proxy-core` only covers buffered chat completions.
This phase ports the streaming response path, the `/v1/responses` and
`/backend-api/codex/responses` endpoints (HTTP + WebSocket), keepalive
handling, and the warmup endpoints that depend on streaming.

## What Changes

- Port `app/core/utils/sse.py` (SSE framing, keepalive injection,
  `parse_sse_data_json`).
- Implement streaming `POST /v1/chat/completions` (SSE) using
  `stream_chat_chunks` equivalent.
- Implement `POST /backend-api/codex/responses/compact` and the
  WebSocket endpoints `/backend-api/codex/responses` and
  `/v1/responses` (ws).
- Port previous-response recovery handling
  (`is_previous_response_not_found_error`,
  `PREVIOUS_RESPONSE_STREAM_INCOMPLETE_MESSAGE`).
- Implement `POST /v1/warmup` and `POST /v1/warmup/{mode}`.
- Implement `internal/bridge` durable bridge session forwarding
  (`POST /internal/bridge/{endpointID}/session/{sessionID}`,
  `parse_forwarded_request`, durable_bridge_repository,
  durable_bridge_coordinator, ring_membership heartbeat/registration).

## Impact

- Depends on `add-go-proxy-core` for account selection and request policy.
- Ring membership/bridge registration affects multi-replica deployments;
  must port `RingMembershipService` heartbeat/stale-marking semantics
  exactly to avoid orphaned ring entries.
- This is the highest-risk phase for behavioral parity (streaming edge
  cases, partial failures); needs explicit partial-failure test coverage
  per repo PR conventions.
