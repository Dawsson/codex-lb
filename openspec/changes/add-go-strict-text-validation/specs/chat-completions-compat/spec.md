## ADDED Requirements

### Requirement: Go Chat Completions routes pre-validate strict response formats

The Go API SHALL reject strict-mode Chat Completions `response_format` JSON
schemas whose schema violates the existing structured-output strict schema
contract before opening an upstream connection.

#### Scenario: Strict chat response format schema fails locally

- **WHEN** a Go `/v1/chat/completions` request includes
  `response_format.type = "json_schema"`, `response_format.json_schema.strict = true`,
  and a schema whose object node omits `additionalProperties: false`
- **THEN** the response is HTTP 400 with `error.code = "invalid_json_schema"`
- **AND** `error.param = "text.format.schema"`
