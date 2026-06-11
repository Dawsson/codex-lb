## ADDED Requirements

### Requirement: Go dashboard projections compute depletion safe-line data

The Go dashboard API SHALL compute `depletionPrimary` and
`depletionSecondary` from recent `usage_history` rows using Python-compatible
EWMA depletion formulas.

#### Scenario: Usage history has a rising burn rate
- **WHEN** a usage window has at least two in-window samples with increasing `used_percent`
- **THEN** the Go projections response includes depletion data for that window
- **AND** the response includes `risk`, `riskLevel`, `burnRate`, `safeUsagePercent`,
  `projectedExhaustionAt`, and `secondsUntilExhaustion`

#### Scenario: Usage history lacks enough samples
- **WHEN** no account has at least two usable samples for a window
- **THEN** the Go projections response returns `null` for that window's depletion field

#### Scenario: Overview can include available depletion data
- **WHEN** depletion data can be computed without error
- **THEN** the Go overview response includes `depletionPrimary` and
  `depletionSecondary` values instead of always returning `null`
