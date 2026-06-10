## ADDED Requirements

### Requirement: Go API starts with SQLite-compatible defaults

The Go API SHALL use the same default local data directory contract as the
current service: `~/.codex-lb/store.db` unless `CODEX_LB_DATA_DIR` or
`CODEX_LB_DATABASE_URL` explicitly selects another SQLite location.

#### Scenario: Default local database is opened

- **WHEN** the Go API starts without database environment overrides
- **THEN** it opens `~/.codex-lb/store.db`
- **AND** it serves health checks from that database connection.

#### Scenario: Repo-local copied production data can be selected

- **WHEN** `CODEX_LB_DATA_DIR` points at a copied data directory
- **THEN** the Go API opens `store.db` from that directory
- **AND** it does not require Python settings code to resolve the path.

### Requirement: Go migrations are scaffolded without taking Alembic ownership

The Go API SHALL include a goose migration hook and migration directory, but it
MUST NOT run Go migrations by default while the existing Python Alembic graph
remains the production schema owner.

#### Scenario: Default startup does not mutate migration metadata

- **WHEN** the Go API starts with default settings
- **THEN** it does not create or update goose migration tables
- **AND** the existing `alembic_version` table remains untouched.

#### Scenario: Explicit migration mode runs the Go migration hook

- **WHEN** `CODEX_LB_GO_RUN_MIGRATIONS=true` is configured
- **THEN** the Go API runs goose migrations before serving requests.
