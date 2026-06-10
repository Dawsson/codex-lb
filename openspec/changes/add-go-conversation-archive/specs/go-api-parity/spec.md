## ADDED Requirements

### Requirement: Go exposes conversation archive endpoints

The Go API SHALL expose `GET /api/conversation-archive/files` and
`GET /api/conversation-archive/records`, matching the Python response
schemas.

#### Scenario: List archived conversation files

- **WHEN** a dashboard client requests `GET /api/conversation-archive/files`
- **THEN** the response lists archived conversation files with the same
  field names as the Python `ConversationArchiveFileResponse` schema.
