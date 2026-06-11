## ADDED Requirements

### Requirement: Go Chat Completions validates request shape locally

The Go API SHALL validate Chat Completions request shape before upstream
selection, reservation, or forwarding. Invalid payloads SHALL return HTTP 400
with an OpenAI error envelope.

#### Scenario: Missing messages and input fails locally

- **WHEN** a Go `/v1/chat/completions` request includes a model but omits both
  `messages` and `input`
- **THEN** the response is HTTP 400 with `error.type = "invalid_request_error"`

#### Scenario: Unsupported message content fails locally

- **WHEN** a Go `/v1/chat/completions` request includes a system message with a
  non-text content part or a user message with `input_audio`
- **THEN** the response is HTTP 400 with `error.param = "messages"`

#### Scenario: Responses-shaped chat payload is accepted

- **WHEN** a Go `/v1/chat/completions` request omits `messages` and includes
  `input`
- **THEN** local chat message validation does not reject the request solely
  because `messages` is absent
