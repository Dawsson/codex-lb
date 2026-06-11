## Why

The main query-caching spec requires dashboard depletion calculations to reuse
per-account EWMA state when the in-window usage history is unchanged. The Go
dashboard depletion port currently recomputes each account history on every
poll.

## What Changes

- Add an in-memory compact-signature cache for Go dashboard depletion state.
- Reuse cached EWMA state when row count, edge rows, and content digest match.
- Invalidate on appended rows, aged-out rows, corrected row content, expired
  reset windows, and inactive account/window keys.

## Impact

- Internal dashboard performance behavior only.
- No response schema or database changes.
