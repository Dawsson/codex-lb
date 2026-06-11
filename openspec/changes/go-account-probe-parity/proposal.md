## Why

The Python account probe path already uses the configured upstream base URL,
the shared outbound HTTP client behavior, freshens account credentials before
sending the wake-up request, and invalidates account routing caches after the
probe. The Go account probe endpoint exists but still hardcodes the upstream
ChatGPT URL, builds a different non-streaming request body, and sends the
currently stored access token without giving authguardian a chance to refresh
credentials first.

This leaves Go probe behavior weaker during Python-to-Go parity work,
especially for accounts whose stored access token is stale but whose refresh
token is still usable.

## What Changes

- Configure the Go account probe sender from the existing upstream base URL
  setting instead of hardcoding `https://chatgpt.com/backend-api`.
- Match the Python probe request shape: `POST /codex/responses`, SSE accept
  header, `stream=true`, `store=false`, `max_output_tokens=1`, and the single
  dot prompt.
- Use the existing authguardian OAuth refresher before sending the probe when
  router wiring provides it, then reload the account proxy record so the probe
  uses refreshed tokens and identity metadata.
- Invalidate the in-process account summary cache and the existing settings
  cache-invalidation namespace after a probe attempt.
- Add focused Go tests for the configured upstream request, synthetic account
  header handling, and credential refresh before probe.

## Non-Goals

- Do not introduce a new usage refresh ownership path inside
  `internal/accounts`; the current Go usage refresher package already imports
  accounts, so direct reuse from the handler would invert package ownership.
- Do not change dashboard UI behavior or Python code.
