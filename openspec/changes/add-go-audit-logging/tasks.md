## 1. Repository & endpoint

- [x] `internal/audit` repository: insert, list(action, limit, offset).
- [x] `GET /api/audit-logs`

## 2. Instrument mutation handlers

- [x] Accounts: update, pause, reactivate, delete, import, alias,
      routing-policy, limit-warmup.
- [x] API keys: create, update, delete, regenerate.
- [x] Firewall: create, delete.
- [x] Settings: update, proxy endpoint/pool/member, account binding.
- [x] Sticky sessions: purge, delete, delete-filtered.
- [x] Quota planner: settings update, warm-now, decision cancel.
  - [x] Settings update.
  - [x] Decision cancel.
  - [x] Warm-now.

## 3. Validation

- [x] `go test ./...`
- [x] `openspec validate add-go-audit-logging --strict`
