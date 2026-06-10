## 1. Repository & endpoint

- [ ] `internal/audit` repository: insert, list(action, limit, offset).
- [ ] `GET /api/audit-logs`

## 2. Instrument mutation handlers

- [ ] Accounts: update, pause, reactivate, delete, import, alias,
      routing-policy, limit-warmup.
- [ ] API keys: create, update, delete, regenerate.
- [ ] Firewall: create, delete.
- [ ] Settings: update, proxy endpoint/pool/member, account binding.
- [ ] Sticky sessions: purge, delete, delete-filtered.
- [ ] Quota planner: settings update, warm-now, decision cancel.

## 3. Validation

- [ ] `go test ./...`
- [ ] `openspec validate add-go-audit-logging --strict`
