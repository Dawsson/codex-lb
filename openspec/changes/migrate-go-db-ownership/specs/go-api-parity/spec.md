## ADDED Requirements

### Requirement: Go service owns database schema migrations

The Go API SHALL be able to create and migrate the full application database
schema via goose, including every table required by the proxy, dashboard,
and scheduler features, without requiring the Python service to have run
first.

#### Scenario: Fresh database initialization

- **WHEN** the Go API starts against an empty SQLite database with
  `CODEX_LB_GO_RUN_MIGRATIONS` enabled
- **THEN** all tables required by accounts, usage history, request logs,
  API keys, proxy pools/endpoints, sticky sessions, dashboard settings,
  firewall rules, audit logs, scheduler leader election, account limit
  warmups, and conversation archive are created.

#### Scenario: Existing Python-managed database remains compatible

- **WHEN** the Go API starts against a SQLite database previously created
  and migrated by the Python (Alembic) service
- **THEN** the goose migration run completes without error and without
  altering or dropping existing tables/columns.

### Requirement: Go models exist for all proxy-relevant tables

The Go API SHALL provide model structs and repository methods for
usage_history, additional_usage_history, account_limit_warmups, audit_logs,
scheduler_leader, and conversation archive tables.

#### Scenario: Repository access to new tables

- **WHEN** another Go package calls the usage, limit-warmup, audit, leader
  election, or conversation-archive repositories
- **THEN** it can read and write rows using Go structs that mirror the
  corresponding SQLAlchemy models' columns.
