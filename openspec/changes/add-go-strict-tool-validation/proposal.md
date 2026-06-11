## Why

The Python API pre-validates strict-mode function tool parameter schemas before
opening an upstream Responses connection. The Go API currently forwards those
requests and lets the Codex upstream reject them later, which turns a
deterministic client payload error into a retryable upstream failure.

## What Changes

- Port strict function-tool schema validation to Go.
- Apply it to native `/v1/responses` / Codex Responses payloads and to
  `/v1/chat/completions` before chat-to-Responses coercion changes tool indexes.
- Return the same OpenAI-shaped `400 invalid_function_parameters` envelope and
  `error.param` paths as Python.

## Impact

- Affects strict-mode function tools on Responses and Chat Completions routes.
- No database schema changes.
