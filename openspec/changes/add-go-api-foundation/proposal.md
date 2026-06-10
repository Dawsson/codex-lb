# Add Go API foundation

## Why

The project is beginning a backend migration from the Python FastAPI service to
a Go API while keeping the existing dashboard frontend. The migration needs a
small, runnable foundation that preserves the current SQLite data path and
starts with compatible dashboard read endpoints instead of changing the web app.

## What Changes

- Add a Go API entrypoint based on the chi/sqlc/goose template shape.
- Use SQLite as the default database backend for the Go service.
- Add a migration hook and migration directory without replacing existing
  Alembic ownership yet.
- Implement initial compatibility routes for health, dashboard auth session,
  account listing, and dashboard overview reads against the existing database.

## Impact

- The Python API remains available as the default dev command.
- Operators can run the Go API slice explicitly while the port continues.
- The existing dashboard can load a realistic overview from the copied
  production SQLite data when pointed at the Go API.
