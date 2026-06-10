# Add images, audio, and files proxy endpoints to Go

## Why

Codex/OpenAI-compatible clients also use image generation/editing, audio
transcription, and file upload endpoints. These are independent of the
text streaming path and can be ported as a separate, smaller PR once core
proxy routing exists.

## What Changes

- Port `app/modules/proxy/images_service.py`:
  - `POST /v1/images/generations`
  - `POST /v1/images/edits`
  - `POST /v1/images/variations` (internal)
- Port audio transcription:
  - `POST /v1/audio/transcriptions`
  - `POST /backend-api/transcribe`
- Port file upload:
  - `POST /backend-api/files`
  - `POST /backend-api/files/{fileID}/uploaded`
- Port `GET /backend-api/wham/agent-identities/jwks`.

## Impact

- Depends on `add-go-proxy-core` for account selection and API key
  validation.
- Multipart form handling (`File`, `Form`, `UploadFile` in FastAPI) needs
  Go equivalents (`multipart.Reader`); ensure size limits match Python
  config.
