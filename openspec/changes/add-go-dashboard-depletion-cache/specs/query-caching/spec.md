## ADDED Requirements

### Requirement: Go dashboard depletion memoizes per-account EWMA state

The Go dashboard API SHALL cache per-account depletion EWMA state in memory
using a compact history signature so repeated dashboard polls do not replay an
unchanged in-window `usage_history` slice.

#### Scenario: Repeated polls with unchanged history reuse cached EWMA state
- **GIVEN** the Go dashboard has computed depletion for an account window
- **WHEN** a later computation sees the same row count, edge rows, and content
  digest for that account window
- **THEN** it reuses the cached EWMA state.

#### Scenario: Changed history invalidates cached state
- **WHEN** the account window history gains a row, loses an aged-out row, or has
  corrected row content
- **THEN** the Go dashboard rebuilds the EWMA state from the supplied history.

#### Scenario: Absent histories are pruned
- **WHEN** an account window is absent from the current dashboard depletion input
- **THEN** any cached EWMA state for that account window is pruned.
