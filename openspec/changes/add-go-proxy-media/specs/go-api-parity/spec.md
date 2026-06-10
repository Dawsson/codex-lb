## ADDED Requirements

### Requirement: Go proxy supports image generation and editing

The Go API SHALL expose `POST /v1/images/generations`,
`POST /v1/images/edits`, and `POST /v1/images/variations`, forwarding
requests to the selected account's upstream and returning OpenAI-compatible
image responses.

#### Scenario: Image edit request with multipart upload

- **WHEN** a client sends `POST /v1/images/edits` with a multipart image
  payload
- **THEN** the Go API forwards the image data to the selected account's
  upstream and returns the resulting image response.

### Requirement: Go proxy supports audio transcription

The Go API SHALL expose `POST /v1/audio/transcriptions` and
`POST /backend-api/transcribe`, forwarding audio payloads to the selected
account's upstream.

#### Scenario: Audio transcription returns text

- **WHEN** a client sends `POST /v1/audio/transcriptions` with an audio file
- **THEN** the Go API returns the transcribed text from the upstream
  response.

### Requirement: Go proxy supports file upload endpoints

The Go API SHALL expose `POST /backend-api/files` and
`POST /backend-api/files/{fileID}/uploaded`, and
`GET /backend-api/wham/agent-identities/jwks`.

#### Scenario: File upload completion marker

- **WHEN** a client calls `POST /backend-api/files/{fileID}/uploaded` after
  uploading a file
- **THEN** the Go API confirms the upload with the upstream and returns the
  same response shape as the Python implementation.
