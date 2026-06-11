## ADDED Requirements

### Requirement: Go dashboard responses preserve nullable projection keys

The Go dashboard API SHALL preserve Python-compatible nullable projection keys
even when server-side projection computation is not yet available.

#### Scenario: Overview includes nullable projection keys
- **WHEN** the Go API serves `GET /api/dashboard/overview`
- **THEN** the JSON response includes `depletionPrimary`, `depletionSecondary`, and `weeklyCreditPace`
- **AND** unavailable projection values are encoded as `null`

#### Scenario: Projections includes nullable projection keys
- **WHEN** the Go API serves `GET /api/dashboard/projections`
- **THEN** the JSON response includes `depletionPrimary`, `depletionSecondary`, and `weeklyCreditPace`
- **AND** unavailable projection values are encoded as `null`

#### Scenario: Additional quotas remain array-shaped
- **WHEN** the Go API serves `GET /api/dashboard/overview`
- **THEN** the JSON response includes `additionalQuotas` as an array
