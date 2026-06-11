## ADDED Requirements

### Requirement: Go dashboard projections compute weekly credit pace

The Go dashboard API SHALL compute `weeklyCreditPace` from active weekly
account summaries, recent secondary-window usage history, the configured
usage-refresh interval, and the dashboard weekly pace working-days setting.

#### Scenario: Active weekly accounts have fresh usage
- **WHEN** at least one active account has secondary capacity, remaining credits,
  reset timing, window length, and fresh usage history
- **THEN** the Go projections response includes `weeklyCreditPace`
- **AND** the response includes totals, used percentages, schedule gap,
  projected shortfall, pace controls, status, account counts, stale counts,
  inactive counts, and confidence.

#### Scenario: Weekly usage data is unavailable
- **WHEN** no active account has enough weekly timing and fresh history data
- **THEN** the Go projections response returns `null` for `weeklyCreditPace`.

#### Scenario: Overview can include available weekly pace data
- **WHEN** weekly credit pace can be computed without error
- **THEN** the Go overview response includes `weeklyCreditPace` instead of always
  returning `null`.
