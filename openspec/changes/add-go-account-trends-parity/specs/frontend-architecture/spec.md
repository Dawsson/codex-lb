## ADDED Requirements

### Requirement: Go account trends normalize weekly primary usage

The Go API SHALL match Python account trend mapping for weekly-only accounts.

#### Scenario: Weekly primary bucket maps to secondary trend
- **WHEN** an account trend bucket has `window = "primary"` and a weekly window length
- **THEN** the Go API maps that bucket into the `secondary` trend series
- **AND** the primary series remains empty when there is no true primary trend data

#### Scenario: Weekly primary bucket contributes scheduled secondary line
- **WHEN** a weekly primary bucket includes reset time and window minutes
- **THEN** the Go API emits matching `secondaryScheduled` points

#### Scenario: Secondary bucket wins when weekly primary is not preferred
- **WHEN** primary and secondary buckets exist for the same account and bucket
- **AND** the secondary row is at least as representative as the weekly primary row
- **THEN** the Go API uses the secondary row for the secondary trend
