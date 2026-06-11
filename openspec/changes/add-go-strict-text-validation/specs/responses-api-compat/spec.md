## ADDED Requirements

### Requirement: Go Responses routes pre-validate strict text formats

The Go API SHALL reject strict-mode Responses `text.format` JSON schemas whose
schema violates the existing structured-output strict schema contract before
opening an upstream connection.

#### Scenario: Strict text format schema fails locally

- **WHEN** a Go `/v1/responses` or `/backend-api/codex/responses` request
  includes `text.format.type = "json_schema"`, `text.format.strict = true`,
  and a schema whose object node omits `additionalProperties: false`
- **THEN** the response is HTTP 400 with `error.code = "invalid_json_schema"`
- **AND** `error.param = "text.format.schema"`
