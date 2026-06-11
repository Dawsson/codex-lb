## Why

The Python runtime exposes loopback-only internal drain controls so deploys can
remove a replica from readiness and reject new HTTP work while allowing health
and drain-control requests. The Go runtime lacks this production lifecycle
surface.

## What Changes

- Add Go drain state with in-flight HTTP request accounting.
- Add `POST /internal/drain/start`, `POST /internal/drain/stop`, and
  `GET /internal/drain/status` guarded to loopback clients.
- Make readiness return 503 while draining.
- Add middleware that rejects non-allowlisted HTTP requests with 503 while
  draining and excludes long-lived websocket requests from in-flight counts.

## Impact

- Affects Go health/runtime lifecycle behavior.
- No database schema changes.
