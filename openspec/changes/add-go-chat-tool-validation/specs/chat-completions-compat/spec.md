## ADDED Requirements

### Requirement: Go Chat Completions validates and normalizes tool types

The Go API SHALL normalize `web_search_preview` tool aliases to `web_search`
for Chat Completions payloads. For message-shaped Chat Completions requests,
the Go API SHALL reject Responses-only builtin tool types with HTTP 400. For
Responses-shaped Chat Completions payloads using `input` with no non-empty
`messages`, the Go API SHALL preserve builtin tool definitions.

#### Scenario: Message-shaped builtin tool fails locally

- **WHEN** a Go `/v1/chat/completions` request includes non-empty `messages`
  and `tools = [{"type":"image_generation"}]`
- **THEN** the response is HTTP 400 with `error.message` containing
  `Unsupported tool type: image_generation`

#### Scenario: web_search_preview is normalized

- **WHEN** a Go `/v1/chat/completions` request includes
  `tools = [{"type":"web_search_preview"}]`
- **THEN** the payload forwarded by Go uses `{"type":"web_search"}`

#### Scenario: Responses-shaped builtin tool is preserved

- **WHEN** a Go `/v1/chat/completions` request omits `messages`, includes
  `input`, and includes `tools = [{"type":"image_generation"}]`
- **THEN** local tool validation accepts the payload
