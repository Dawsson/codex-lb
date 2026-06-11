## Why

The dashboard overview schema includes a top-level `additionalQuotas` array so
clients can discover non-core quota surfaces from the overview payload. Go
currently always returns an empty array even when account summaries contain
additional quota data.

## What Changes

- Roll up distinct additional quota descriptors from overview account summaries
  into the top-level `additionalQuotas` field.
- Keep per-account additional quota details unchanged.
- Add focused tests for deterministic de-duplication and ordering.

## Impact

- Affects `GET /api/dashboard/overview` JSON.
- No database schema changes.
