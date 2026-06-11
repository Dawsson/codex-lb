# Add Go previous response owner DB fallback

## Why

The Go proxy currently pins `previous_response_id` continuity only through a
process-local in-memory index. That is enough while a response chain stays on
one replica/process, but it can lose account ownership after restart or when
the request log already contains the response owner.

## What Changes

- Add a Go request-log lookup for the latest successful response owner by
  `request_id`, scoped by API key when present.
- Use the lookup as a fallback when the local previous-response owner index
  misses before account selection.
- Keep full durable/inter-replica bridge behavior out of scope.

## Impact

- Previous-response account pinning can survive local index loss when the
  request log contains the successful response.
- Requests without a DB owner continue through existing selection behavior.
