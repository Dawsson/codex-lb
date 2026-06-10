# add-go-proxy-core — handoff notes

## Overarching directive
Continue the Python→Go API migration roadmap end-to-end ("Complete
everything, get everything working. get it functional."), phase by phase per
`openspec/changes/add-go-*`. Current phase: `add-go-proxy-core`. The user
approved an explicit strategy for this phase: **split the ~4,000+ line port
into multiple commits by sub-area** (model registry/endpoints, account cache,
load balancer strategies, request policy/API-key enforcement, chat
completions forwarding), each independently reviewable.

## Repo conventions (binding)
- OpenSpec is SSOT. `openspec/changes/add-go-proxy-core/tasks.md` tracks
  sub-task checkboxes; check items off as you complete them, then run
  `openspec validate add-go-proxy-core --strict`.
- Plain `git commit` (NOT the `committer` tool — that's for a different
  Expo/RN project per global CLAUDE.md). Stage files by exact path, never
  `git add -A`/`git add .` — the working tree has a large amount of unrelated
  uncommitted work (see below). Each sub-area = its own commit.
- ~800 net lines / one concern per PR/commit where feasible.

## Done so far (committed)
- `8b9e1bf` — model registry (`internal/proxy/modelregistry.go`,
  `modelalias.go`), proxy API key validation (`apikey.go`), error envelope
  helpers (`errors.go`), `GET /v1/models` + `GET /backend-api/codex/models`
  (`models_handler.go`), wired into `internal/httpapi/router.go`. Also added
  `migrations/00003_seed_dashboard_settings.sql` (the `dashboard_settings`
  table had no seed row anywhere — `internal/settings.Repository.loadRow`
  was failing with "dashboard settings row missing" for every fresh DB; fixed
  by seeding `id=1` with explicit `totp_required_on_login=0`,
  `api_key_auth_enabled=0`). Also added `apikeys.Repository.GetByHash`,
  `KeyRecord.AllowedModelsList()`, `apikeys.HashKey()`.
- `1c30285` — generic `AccountSelectionCache[T]` in
  `internal/proxy/account_cache.go` (+ tests), porting
  `app/modules/proxy/account_cache.py`. **Not yet wired up** — needs a
  concrete `T` (the Go equivalent of `SelectionInputs`) once the load
  balancer orchestration layer exists.

## ⚠️ Major discovery: large uncommitted work already in the working tree
`git status` shows **77 modified/untracked paths** that are NOT part of this
session's commits and predate it. Notably, `internal/proxy/` already
contains three large **untracked** files that appear to be a substantial,
already-working port of `app/modules/proxy/load_balancer.py`:

- `internal/proxy/load_balancer.go` (820 lines) — `SelectionAccount`,
  `UsageEntry`, `AccountState`, `ApplyUsageQuota`, `StateFromAccount`,
  `BackgroundRecoveryStateFromAccount`, `StateAboveBudgetThreshold`,
  `BestHealthTierStates`, `SelectAccountPreferringBudgetSafe`, etc. Has a
  doc comment explicitly stating: *"the async LoadBalancer class (account
  leasing, persisted runtime state, prometheus metrics, circuit breakers,
  additional-quota eligibility filtering, sticky-session loading/
  orchestration, and the `_load_selection_inputs` DB-fetch pipeline) is NOT
  ported here. This file covers only the pure functions... Orchestration is
  deferred to a future change."*
- `internal/proxy/balancer_logic.go` (1547 lines) — `SelectAccount` and all
  the sort-key / tie-breaking / routing-policy helper functions
  (capacity-weighted, relative-availability, reset-preference, round-robin,
  etc.).
- `internal/proxy/balancer_types.go` (133 lines) — supporting types
  (`SelectAccountOptions`, `RoutingCostsByAccount`, `ResetPreferenceWindow`,
  `SelectionResult`, etc.).

**These three files currently have NO test file** (only
`account_cache_test.go`, `modelalias_test.go`, `modelregistry_test.go`,
`models_handler_test.go` exist in `internal/proxy/`). `go build ./...` and
`go test ./...` both pass with these files present, so they compile cleanly
against the rest of the tree as it stands.

Also untracked (and never in git history — not in `.gitignore` either):
`internal/crypto/`, `internal/firewall/`, `internal/httputil/`,
`internal/models/`, `internal/quotaplanner/`, `internal/reports/`,
`internal/settings/`, `internal/stickysessions/`, `internal/auth/{errors,
password,totp}.go`, `internal/apikeys/{analytics,handler}.go`,
`internal/accounts/trends.go`, plus several `openspec/changes/add-go-*`
directories and a few frontend files. The whole Go API currently builds and
`go test ./...` passes including all of this uncommitted code.

## Recommended next steps for the next agent
1. **Triage the uncommitted tree first**, before writing new code. Figure out
   whether this uncommitted work is: (a) finished and just never committed —
   in which case it should likely be split into its own reviewable commit(s)
   per the OpenSpec/commit-size conventions before continuing, or (b) WIP
   from a different in-flight session that shouldn't be touched/committed
   without checking with the user. **Ask the user before committing anything
   you didn't write yourself.**
2. For the `load_balancer.go` / `balancer_logic.go` / `balancer_types.go`
   trio specifically: read them against `app/modules/proxy/load_balancer.py`
   (2514 lines) and `app/modules/proxy/affinity.py` (330 lines, sticky
   session affinity — appears NOT yet covered by these three files). Write
   unit tests for `SelectAccount` / `SelectAccountPreferringBudgetSafe` and
   the sort-key helpers (pure functions, easy to test with table-driven
   tests). Port `affinity.py` (check `internal/stickysessions/repository.go`
   first — it already has sticky-session table CRUD; affinity lookups should
   reuse it rather than duplicating SQL).
3. Wire `AccountSelectionCache[T]` (from `account_cache.go`) up with
   `SelectionInputs` once that type/orchestration exists — currently
   unused/standalone.
4. Update `openspec/changes/add-go-proxy-core/tasks.md` section 1 (account
   selection / load balancing) to reflect what's actually done vs. deferred,
   matching the doc-comment in `load_balancer.go` about deferred
   orchestration (account leasing, circuit breakers, prometheus metrics,
   `_load_selection_inputs` DB pipeline — these may need their own follow-up
   sub-area or OpenSpec change).
5. Run `gofmt -l .` — note pre-existing unformatted files unrelated to this
   work: `internal/accounts/trends.go`, `internal/apikeys/{analytics,
   handler,repository}.go`, `internal/auth/totp.go`,
   `internal/quotaplanner/{handler,repository}.go`,
   `internal/reports/{handler,repository}.go`, `internal/settings/types.go`.
   These existed before this session's commits; not blocking, but worth
   fixing opportunistically or flagging.
6. After section 1 is solid (with tests + commit), proceed to section 2
   (request policy / API-key rate-limit enforcement —
   `app/modules/proxy/request_policy.py`, 486 lines) and section 4 (chat
   completions forwarding), per the original sub-area split. Then continue
   the remaining roadmap phases: `add-go-proxy-streaming`,
   `add-go-proxy-media`, `add-go-schedulers`, `add-go-audit-logging`,
   `add-go-limit-warmup`, plus deferred Section 4 of `add-go-usage-tracking`.

## Useful references
- `internal/proxy/apikey.go` — note the `parseSQLiteTimestamp` helper added
  to handle modernc.org/sqlite returning `DATETIME` columns as RFC3339
  (`2026-06-10T22:26:37Z`) rather than `"2006-01-02 15:04:05"` when scanned
  into `sql.NullString`. Apply the same pattern anywhere else timestamps are
  parsed from sqlite.
- `internal/proxy/models_handler_test.go` — test pattern for DB-backed
  handler tests (temp sqlite store via `dbpkg.Open` +
  `store.RunMigrations("../../migrations")`).
