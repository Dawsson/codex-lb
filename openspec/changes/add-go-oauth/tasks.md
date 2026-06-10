## 1. OAuth client primitives

- [x] Port PKCE pair generation, `build_authorization_url`,
      `request_device_code`, `exchange_authorization_code`,
      `exchange_device_token`.

## 2. Identity & account creation

- [x] Port `extract_id_token_claims`, `clean_account_identity_part`,
      `generate_unique_account_id`, `normalize_seat_type`,
      `coerce_account_plan_type`.
- [x] Port `AccountIdentityConflictError` handling and account upsert via
      `internal/accounts` repository.
- [x] Encrypt access/refresh/id tokens via `internal/crypto`.

## 3. Endpoints

- [x] `POST /api/oauth/start`
- [x] `GET /api/oauth/status`
- [x] `POST /api/oauth/complete`
- [x] `POST /api/oauth/manual-callback`

## 4. Validation

- [x] `go test ./...`
- [ ] Manual end-to-end OAuth account add against a real OpenAI account.
- [x] `openspec validate add-go-oauth --strict`
