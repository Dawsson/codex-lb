## 1. Implementation

- [x] Add Go module and chi HTTP entrypoint.
- [x] Add SQLite configuration and DB open path.
- [x] Add goose/sqlc scaffold files for the Go backend.
- [x] Implement health and dashboard auth session routes.
- [x] Implement read-only accounts and dashboard overview routes.
- [x] Add package scripts for running the Go API without replacing Python yet.

## 2. Validation

- [x] Run `go test ./...`.
- [x] Run `go run ./cmd/codex-lb-go --check`.
- [x] Run `openspec validate add-go-api-foundation --strict`.
