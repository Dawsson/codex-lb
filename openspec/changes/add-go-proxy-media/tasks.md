## 1. Images

- [x] `POST /v1/images/generations`
  - [x] Non-streaming single-image JSON requests translate to an upstream
        Responses `image_generation` tool call and return OpenAI Images
        `created/data[]` JSON.
- [x] `POST /v1/images/edits`
  - [x] Multipart `image` / `image[]` input translates to Responses
        `input_image` parts, forces the `image_generation` tool with
        `action=edit`, and maps completed image output to OpenAI Images JSON.
- [x] `POST /v1/images/variations`
  - [x] Authenticates like other image routes and returns Python-compatible
        unsupported 404 guidance.

## 2. Audio

- [x] `POST /v1/audio/transcriptions`
- [x] `POST /backend-api/transcribe`
  - [x] Validates `gpt-4o-transcribe` on `/v1/audio/transcriptions`,
        forwards multipart audio to upstream `/transcribe`, logs requests,
        and enforces API-key admission reservation.

## 3. Files

- [x] `POST /backend-api/files`
- [x] `POST /backend-api/files/{fileID}/uploaded`
  - [x] Forwards JSON payloads to upstream using selected account
        credentials, logs requests, enforces API-key admission reservation,
        and pins `file_id` to the selected account for finalize affinity.

## 4. Wham

- [x] `GET /backend-api/wham/agent-identities/jwks`

## 5. Validation

- [x] `go test ./...`
- [x] `openspec validate add-go-proxy-media --strict`
