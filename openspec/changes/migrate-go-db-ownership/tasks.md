## 1. Schema audit

- [ ] Diff `app/db/models.py` against current SQLite schema and
      `internal/*` repositories to produce a definitive table/column list.
- [ ] Identify tables with no Go model/repository yet: usage_history,
      additional_usage_history, account_limit_warmups, audit_logs,
      scheduler_leader, conversation_archive_files, conversation_archive_records.

## 2. Goose migrations

- [ ] Replace the placeholder `00001_go_api_scaffold.sql` with a baseline
      migration set that creates every table `IF NOT EXISTS` matching the
      Alembic-managed schema (column types, defaults, indexes, FKs).
- [ ] Add a baseline-detection step so running goose against an existing
      Python-created database does not fail or duplicate objects.
- [ ] Verify `go run ./cmd/codex-lb-go --check` succeeds against both a
      fresh empty database and a copy of an existing production database.

## 3. Go models & repositories

- [x] Add `internal/usage` package: UsageHistory, AdditionalUsageHistory
      models + repository (latest_by_account, aggregate_since, insert).
- [x] Add `internal/limitwarmup` package: AccountLimitWarmup model +
      repository.
- [x] Add `internal/audit` package: AuditLog model + repository
      (insert, list with filters).
- [x] Add `internal/scheduling` package: SchedulerLeader model + leader
      election repository (try_acquire/release).
- [x] Add `internal/conversationarchive` package: file/record models +
      repository.

## 4. Validation

- [x] `go test ./...`
- [x] `go run ./cmd/codex-lb-go --check` against fresh + existing DBs
- [x] `openspec validate migrate-go-db-ownership --strict`
