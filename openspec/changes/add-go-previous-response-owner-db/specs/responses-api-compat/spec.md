### Requirement: Go previous-response owner fallback

The Go proxy MUST attempt to resolve a `previous_response_id` owner from
successful request logs when the process-local previous-response owner index
does not contain an owner for that response.

#### Scenario: DB owner pins account after local index miss

- **GIVEN** a successful request log exists with `request_id` equal to the
  incoming `previous_response_id`
- **AND** that log has a non-empty `account_id`
- **AND** the process-local previous-response owner index misses
- **WHEN** the Go proxy selects an account for the follow-up Responses request
- **THEN** it uses the logged account as the preferred account for selection

#### Scenario: API key scope is preserved

- **GIVEN** the incoming request is authenticated with an API key
- **WHEN** the Go proxy looks up the logged owner for `previous_response_id`
- **THEN** it only uses successful request logs for the same API key
