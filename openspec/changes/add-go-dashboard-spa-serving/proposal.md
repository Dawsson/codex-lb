## Why

The Go API can act as the production replacement for the Python API, but it
does not have an optional path to serve the already-built dashboard assets.
Deployments can still front the API with Caddy/nginx, but a single Go process
should be able to serve the dashboard SPA when an operator points it at a
frontend build directory.

## What Changes

- Add optional dashboard static asset configuration.
- Serve existing static files from the configured dashboard dist directory.
- Fall back to `index.html` for dashboard SPA routes while preserving 404s for
  API/proxy/internal paths.

## Impact

- Affects Go HTTP routing only when dashboard static serving is configured.
- Does not change dashboard development or reverse-proxy deployments that leave
  the setting unset.
