## ADDED Requirements

### Requirement: Go dashboard overview sorts accounts by primary capacity

The Go dashboard overview API SHALL return account summaries sorted by
`capacityCreditsPrimary` descending, matching the Python dashboard service.

#### Scenario: Accounts have primary capacity
- **WHEN** overview account summaries include multiple accounts with primary capacity values
- **THEN** higher primary capacity accounts appear earlier in the response

#### Scenario: Accounts have missing or zero primary capacity
- **WHEN** an account has `capacityCreditsPrimary` of `null` or `0`
- **THEN** it appears after accounts with positive primary capacity

#### Scenario: Accounts endpoint keeps own ordering
- **WHEN** `/api/accounts` returns account summaries
- **THEN** this overview-specific sorting requirement does not change that endpoint's ordering
