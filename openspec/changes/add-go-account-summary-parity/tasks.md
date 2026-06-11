## 1. Implementation

- [x] Extend Go account repository rows with runtime status and auth metadata.
- [x] Load request usage aggregates for the last seven days.
- [x] Load latest additional quota rows per account/quota/window.
- [x] Load latest account limit warm-up rows.
- [x] Port account summary mapper fields and runtime status semantics.

## 2. Validation

- [x] Add focused Go tests for account summary parity.
- [x] Run `go test ./internal/accounts`.
- [x] Run `openspec validate add-go-account-summary-parity --strict`.
