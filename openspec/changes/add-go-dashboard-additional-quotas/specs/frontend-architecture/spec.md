## ADDED Requirements

### Requirement: Go dashboard overview includes additional quota rollup

The Go dashboard overview API SHALL populate top-level `additionalQuotas` from
the additional quota data already present on account summaries.

#### Scenario: Account summaries include additional quotas
- **WHEN** one or more overview accounts include additional quota entries
- **THEN** the Go overview response includes those quota descriptors in top-level `additionalQuotas`

#### Scenario: Multiple accounts share the same quota descriptor
- **WHEN** multiple accounts include the same quota key, limit name, metered feature, and routing policy
- **THEN** the Go overview response includes one representative entry for that descriptor

#### Scenario: No accounts include additional quotas
- **WHEN** no overview accounts include additional quota entries
- **THEN** the Go overview response includes `additionalQuotas: []`
