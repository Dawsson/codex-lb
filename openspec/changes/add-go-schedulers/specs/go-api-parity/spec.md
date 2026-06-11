## ADDED Requirements

### Requirement: Go runs all leader-elected background schedulers

The Go API SHALL run leader-elected background schedulers equivalent to the
Python service: API key limit reset, model refresh, sticky session cleanup,
quota planner, and auth guardian, each on its own configurable interval.

#### Scenario: API key limit reset runs hourly

- **WHEN** one hour elapses on the leader replica
- **THEN** the Go API resets expired API key limits and releases usage
  reservations older than 6 hours.

#### Scenario: Sticky session cleanup removes expired sessions

- **WHEN** the sticky session cleanup interval elapses on the leader
  replica
- **THEN** expired `sticky_sessions` rows are deleted.

### Requirement: Go runs a continuous cache invalidation poller

The Go API SHALL run a continuous (non-leader-gated) poller that watches for
cache invalidation signals and clears API key cache, firewall IP cache, and
settings-derived proxy selection cache when signaled, matching
`CacheInvalidationPoller` behavior.

#### Scenario: API key cache invalidation signal clears cache

- **WHEN** an API key is created, updated, or deleted on any replica
- **THEN** all replicas' in-process API key caches are cleared within one
  poll interval.

#### Scenario: Settings invalidation signal clears selection cache

- **WHEN** routing-affecting settings are changed on any replica
- **THEN** all replicas' in-process account selection caches are cleared
  within one poll interval.
