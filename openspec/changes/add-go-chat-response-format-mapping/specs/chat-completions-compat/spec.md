## ADDED Requirements

### Requirement: Go Chat Completions maps response_format to Responses text.format

The Go API SHALL translate Chat Completions `response_format` into Responses
`text.format` before upstream forwarding and SHALL remove the original
`response_format` field from the forwarded payload. If both `response_format`
and `text.format` are specified, or if `response_format.type = "json_schema"`
omits `json_schema`, the Go API SHALL return HTTP 400 with an OpenAI error
envelope.

#### Scenario: JSON schema response format is mapped

- **WHEN** a Go `/v1/chat/completions` request includes
  `response_format = {"type":"json_schema","json_schema":{"name":"answer","schema":{"type":"object"},"strict":false}}`
- **THEN** the forwarded Responses payload includes
  `text.format = {"type":"json_schema","name":"answer","schema":{"type":"object"},"strict":false}`
- **AND** the forwarded payload does not include `response_format`

#### Scenario: Missing json_schema fails locally

- **WHEN** a Go `/v1/chat/completions` request includes
  `response_format = {"type":"json_schema"}`
- **THEN** the response is HTTP 400 with an OpenAI error envelope
