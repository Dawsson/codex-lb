## 1. Implementation

- [x] Add Go firewall config for trusted proxy headers, trusted CIDRs, and cache TTL.
- [x] Add firewall decision cache and middleware for protected proxy paths.
- [x] Invalidate firewall cache on allowlist create/delete.
- [x] Register middleware in `internal/httpapi/router.go`.

## 2. Validation

- [x] Add focused Go tests for allow-all, deny, allow, trusted XFF, and mutation invalidation behavior.
- [x] Run `go test ./internal/firewall ./internal/httpapi`.
- [x] Run `openspec validate add-go-firewall-enforcement --strict`.
