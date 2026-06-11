## Why

Python maps Chat Completions `response_format` into Responses `text.format`
controls before upstream forwarding. The Go route currently leaves the
chat-only `response_format` field in the payload, which can surprise the
Responses upstream contract and misses structured-output controls.

## What Changes

- Convert chat `response_format` strings and objects into `text.format`.
- Reject payloads that specify both `response_format` and `text.format`.
- Reject malformed or unsupported `response_format` values with OpenAI-shaped
  400 errors.
- Remove `response_format` from the upstream payload after mapping.

## Impact

- Affects `/v1/chat/completions` payload mapping.
- No database schema changes.
