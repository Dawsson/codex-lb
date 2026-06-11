## ADDED Requirements

### Requirement: Go Chat Completions routes pre-validate nested strict function tools

The Go API SHALL reject strict-mode Chat Completions function tools whose nested
`function.parameters` schema violates the existing strict function-tool
contract before chat-to-Responses coercion changes tool indexes.

#### Scenario: Nested strict function tool schema fails locally

- **WHEN** a Go `/v1/chat/completions` request includes a nested function tool
  with `function.strict: true` and a `function.parameters` schema whose object
  node omits `additionalProperties: false`
- **THEN** the response is HTTP 400 with `error.code = "invalid_function_parameters"`
- **AND** `error.param = "tools[0].function.parameters"`
