## Why

The Python dashboard API serializes projection fields as explicit keys with
`null` values when depletion and weekly pace are unavailable. The Go overview
and projections handlers currently omit those fields, which can surprise
clients that diff Python and Go payloads or expect a stable response shape.

## What Changes

- Return explicit `null` values for `depletionPrimary`, `depletionSecondary`,
  and `weeklyCreditPace` from Go dashboard overview/projections responses until
  the full server-side projection implementation lands.
- Preserve `additionalQuotas` as an array field, not `null`.
- Add focused JSON shape tests.

## Impact

- Affects `GET /api/dashboard/overview` and `GET /api/dashboard/projections`
  JSON shape in the Go API.
- No database schema changes.
