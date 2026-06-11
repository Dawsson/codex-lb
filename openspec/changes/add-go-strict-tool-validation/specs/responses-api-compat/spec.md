## ADDED Requirements

### Requirement: Go Responses routes pre-validate strict function tools

The Go API SHALL reject strict-mode Responses function tools whose `parameters`
schema violates the existing strict function-tool contract before opening an
upstream connection.

#### Scenario: Native strict function tool schema fails locally

- **WHEN** a Go `/v1/responses` or `/backend-api/codex/responses` request
  includes a flat function tool with `strict: true` and a `parameters` schema
  whose object node omits `additionalProperties: false`
- **THEN** the response is HTTP 400 with `error.code = "invalid_function_parameters"`
- **AND** `error.param = "tools[0].parameters"`
