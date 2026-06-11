## Why

Python normalizes Chat Completions tool types and rejects Responses-only builtin
tools when the request uses Chat messages. The Go route currently forwards
those tool definitions unchanged, which can produce upstream contract
surprises.

## What Changes

- Normalize `web_search_preview` to `web_search` for chat `tools` and
  `tool_choice`.
- Reject Responses-only builtin tool types for message-shaped Chat
  Completions requests.
- Preserve builtin tools for Responses-shaped Chat Completions payloads that
  use `input` without `messages`.

## Impact

- Affects `/v1/chat/completions` tool payloads.
- No database schema changes.
