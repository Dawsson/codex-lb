## Why

The Python Chat Completions route validates request shape and message content
before mapping to Responses. The Go route currently forwards several malformed
payloads into the upstream path, which can produce schema surprises or
misclassified upstream errors.

## What Changes

- Validate `model` and `messages` / `input` presence in the Go Chat
  Completions handler before streaming or collecting.
- Validate message objects, roles, role-specific text-only rules, user content
  parts, tool message identifiers, assistant tool-call shape, and chat file
  `file_id` rejection.
- Return OpenAI-shaped `400 invalid_request_error` envelopes with
  `error.param = "messages"` for message payload errors.

## Impact

- Affects malformed `/v1/chat/completions` requests.
- No database schema changes.
