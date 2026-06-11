# Go API — Python Parity Port Checklist

Master checklist for porting **codex-lb** from the Python (FastAPI) runtime to the Go runtime.
Use this as the single tracking doc until Go is production-ready.

**Legend**

| Mark | Meaning |
|------|---------|
| `[x]` | Done — acceptable for current scope (may still have polish gaps noted inline) |
| `[ ]` | Not done — must port, wire, or harden |
| **Better** | Deliberate Go improvement vs Python (keep unless it breaks the dashboard/Codex contract) |

**Sources:** Python `app/modules/**`, OpenSpec `add-go-*` changes, live route/handler diff (2026-06-11).

---

## Summary

| Area | Python | Go today | Gap |
|------|--------|----------|-----|
| Proxy routes | ~35 HTTP + 2 WS | 9 HTTP + 2 WS | Large |
| Dashboard admin routes | ~62 | ~50 registered, ~12 account writes missing | Medium |
| Background schedulers | 7+ loops | 0 wired | Critical |
| Account selection | Full + sticky + bridge | Core LB only | Medium–large |
| Codex CLI path | Full | Chat + responses + WS (partial mapping) | Medium |

---

## 1. Foundation & runtime shell

- [x] Go module, chi router, config, SQLite open path
- [x] `GET /health/live`, `GET /health/ready` (DB ping)
- [x] `GET /health` — basic health (Python alias)
- [x] `GET /health/startup` — startup completion probe
- [x] Graceful drain: `POST /internal/drain/{start,stop}`, `GET /internal/drain/status` (loopback-only)
- [ ] Optional Prometheus metrics server (separate port when enabled)
- [ ] Serve dashboard SPA static assets + `/{path}` fallback from Go binary (or document that dev/prod always fronts Go with nginx/Caddy)
- [x] `GET /api/runtime/version` — real version/build metadata (Go returns `"go-dev"` stub)
- [ ] Wire all background work start/stop in `cmd/codex-lb-go/main.go` with graceful shutdown ordering (match Python lifespan)
  - [x] Usage refresh scheduler starts/stops with graceful shutdown.
  - [x] API key limit reset scheduler starts/stops with graceful shutdown.
  - [x] Cache invalidation poller starts/stops with graceful shutdown.
  - [x] Sticky session cleanup scheduler starts/stops with graceful shutdown.
  - [x] Model refresh scheduler starts/stops with graceful shutdown.
- [ ] Leader election (`internal/scheduling`) used by every periodic job
  - [x] Usage refresh loop is leader-election gated.
  - [x] API key limit reset loop is leader-election gated.
  - [x] Sticky session cleanup loop is leader-election gated.
  - [x] Model refresh loop is leader-election gated.
- [x] Cross-replica cache invalidation poller (`app/core/cache/invalidation.py`)

**Better:** Single binary can skip embedded SPA if deployment always uses a reverse proxy; document the supported deployment shapes explicitly.

---

## 2. Database & migrations

- [x] Goose/sqlc scaffold, `internal/db` store, `--check` path
- [x] Core repositories: usage, limitwarmup, audit, scheduling, conversationarchive
- [ ] Definitive schema diff: Python Alembic head vs Go goose baseline (`migrate-go-db-ownership`)
- [ ] Baseline migration set with `IF NOT EXISTS` for existing Python-created DBs
- [ ] Verify upgrade path on fresh DB **and** copy of production SQLite
- [ ] (If in scope) Postgres backend parity per `openspec/specs/database-backends`

---

## 3. Dashboard auth (`admin-auth`)

- [x] Session cookie, password login/logout/setup/change/remove
- [x] TOTP setup/confirm/verify/disable
- [x] `/api/auth/*` routes
- [x] Full `/api/dashboard-auth/*` alias set
- [x] Bootstrap token: auto-generate on startup when unset (`app/core/bootstrap`)
- [x] Bootstrap token: expose `bootstrapRequired` / `bootstrapTokenConfigured` in session response
- [x] Bootstrap token: validate on `POST .../password/setup`
- [x] TOTP-required-on-login gate in session flow
- [x] `CODEX_LB_GO_AUTH_DISABLED=1` bypass for local dev

**Better:** Prefer `/api/auth/*` as canonical; keep dashboard-auth aliases read-only for frontend compat.

---

## 4. Accounts — read API & summary mapping

- [x] `GET /api/accounts` — list summaries (minimal mapper)
- [x] `GET /api/accounts/{id}/trends` — primary/secondary sparklines
- [x] Port `build_account_summaries` / `_account_to_summary` (`app/modules/accounts/mappers.py`) core dashboard fields:
  - [x] Runtime-derived `status` (not raw DB enum only)
  - [x] Weekly-only primary/secondary remapping (`normalize_weekly_only_rows`)
  - [x] Monthly window presentation for free/plan-specific accounts
  - [x] `capacityCredits*` / `remainingCredits*` with quota/credits rules
  - [x] `additionalQuotas[]` from `additional_usage_history` (registry labels/policy fallback to persisted limit metadata)
  - [x] `limitWarmup` status object
  - [x] `requestUsage` (7d aggregates from request_logs)
  - [x] `auth` token expiry/state (when `include_auth=true`)
  - [x] `isEmailDuplicate` detection
- [x] Account trends: `secondaryScheduled` series and weekly-primary normalization
- [x] Invalidate summary cache on account identity/routing writes

---

## 5. Accounts — write API (entire surface missing in Go)

- [x] `POST /api/accounts/import`
- [x] `POST /api/accounts/{id}/export/auth` (canonical auth export)
- [x] `POST /api/accounts/{id}/export` (deprecated — optional)
- [x] `POST /api/accounts/{id}/export/opencode-auth` (deprecated — optional)
- [x] `PATCH /api/accounts/{id}` — security flags
- [x] `PUT /api/accounts/{id}/alias`
- [x] `PUT /api/accounts/{id}/routing-policy`
- [x] `PUT /api/accounts/{id}/limit-warmup`
- [x] `POST /api/accounts/{id}/probe` (configured upstream SSE probe, credential refresh, cache invalidation; post-probe forced usage refresh still pending)
- [x] `POST /api/accounts/{id}/pause`
- [x] `POST /api/accounts/{id}/reactivate`
- [x] `DELETE /api/accounts/{id}`
- [x] Audit log writes for every mutation above

---

## 6. Dashboard overview & projections

- [x] `GET /api/dashboard/overview` — envelope shape, timeframe, trends, windows (partial data)
- [x] `GET /api/dashboard/projections` — route exists
- [x] Request-log-driven cost/metrics aggregates
- [x] `lastSyncAt` from latest usage timestamps
- [x] Request-log `requestKind` / `status` normalization for frontend Zod schemas
- [x] Omit-null vs explicit-null contract for optional projection fields (Go emits explicit null projection keys; `additionalQuotas` remains array-shaped)
- [x] Port `DashboardService.get_overview`:
  - [x] `additionalQuotas` rollup on overview
  - [x] Server-computed `depletionPrimary` / `depletionSecondary`
  - [x] Server-computed `weeklyCreditPace`
- [x] Port `DashboardService.get_projections` / depletion cache (`usage/depletion_service.py`, `weekly_pace.py`)
  - [x] Server-side primary/secondary depletion response values.
  - [x] Weekly credit pace parity.
  - [x] Memoized depletion cache parity.
- [x] Align overview account ordering/sorting with Python
- [x] Top-error field semantics vs normalized request-log errors (failed non-blank error codes only)
- [x] Exclude `warmup` / `limit_warmup` request logs from overview aggregates, trends, and top error

**Better:** Client-side `buildWeeklyCreditPace` fallback is fine if projections endpoint returns `{}`; document that server projections are optional enhancement.

---

## 7. Request logs

- [x] `GET /api/request-logs` — pagination, filters, camelCase JSON
- [x] `GET /api/request-logs/options` — facet options
- [x] Status filter mapping (`ok` / `rate_limit` / `quota` / `error`) in list + options
- [x] `requestKind` normalization (`chat_completion` → `normal`)
- [x] Port `_map_status_filter` SQL semantics exactly (verify edge cases: empty filter = all)
- [x] Port `to_request_log_entry` / `cost_breakdown_from_log` — full cost breakdown (model pricing, service tiers, persisted-total mismatch fallback)
- [x] Expose request-log `latencyFirstTokenMs` from persisted `latency_first_token_ms`
- [x] Token totals: reasoning_tokens fallback, cached input clamping
- [x] Default exclusion of warmup/limit_warmup rows in list (Python `RequestKind` filter)
- [x] Search parity (account email, request metadata, api key name/id, token counts, latency)
- [x] Soft-delete / `deleted_at` behavior in all queries

---

## 8. Usage API & refresh

- [x] `GET /api/usage/summary`
- [x] `GET /api/usage/history`
- [x] `GET /api/usage/window`
- [x] Core usage math helpers (`capacity_for_plan`, `normalize_weekly_only_rows`, etc.)
- [x] **`UsageUpdater.refresh_accounts`** — poll upstream OpenAI usage per account (CRITICAL for Spark/additional-quota routing)
  - [x] Primary/secondary usage fetch and insert.
  - [x] Additional quota usage fetch/merge/canonicalize/insert and stale row pruning.
  - [x] Additional-only freshness cache parity.
  - [x] Deactivate or mark reauth-required for permanent usage fetch errors.
  - [x] Identity metadata sync from usage payload.
  - [x] Auth refresh retry on 401 usage fetch failures.
  - [x] Usage-payload status recovery.
- [x] `reconcile_recoverable_account_statuses` after refresh
- [x] Leader-elected refresh loop + config (`usage_refresh_interval_seconds`, `usage_refresh_enabled`)
- [x] Post-refresh: invalidate rate-limit header cache + account selection cache
  - [x] Successful refresh bumps cross-replica settings invalidation, clearing account selection caches through the poller.
- [x] `AdditionalUsageRepository` — latest/insert/list/delete for additional quota keys (needed for dashboard additional quotas + Spark)
- [ ] Persist refresh timestamps / stale detection surfaced to operators

---

## 9. Background schedulers (none wired today)

- [x] **Usage refresh scheduler** (`app/core/usage/refresh_scheduler.py`) — includes limit-warmup hook
  - [x] Go process lifecycle, leader election, standard usage fetch/write, and recoverable status reconciliation.
  - [x] Limit-warmup post-refresh hook.
- [x] **API key limit reset** — hourly `reset_expired_limits` + `release_stale_usage_reservations`
  - [x] `reset_expired_limits` repository behavior.
  - [x] `release_stale_usage_reservations` repository behavior.
  - [x] Go process lifecycle, leader election, and shutdown wiring.
- [x] **Model refresh scheduler** — upstream model list → update registry beyond bootstrap static models
- [ ] **Sticky session cleanup** — purge stale prompt-cache + closed HTTP bridge sessions
  - [x] Stale prompt-cache cleanup scheduler.
  - [ ] Closed HTTP bridge session cleanup after Go bridge/session parity.
- [ ] **Quota planner scheduler** — shadow/auto ticks, execute warmups in auto mode
  - [x] Leader-elected Go process lifecycle, configurable enable/interval, and shutdown wiring.
  - [x] Shadow/suggest no-op ticks write bounded decision rows.
  - [x] Auto mode executes due planned warmup decisions through the limit-warmup sender and request-log path.
  - [ ] Full Python forecast/action candidate planning parity.
- [x] **Auth guardian** — proactive OAuth token refresh for stale active accounts
  - [x] Leader-elected loop selects stale active accounts, refreshes OAuth tokens, persists encrypted token/identity fields, marks permanent failures, and invalidates selection cache.
- [x] **Cache invalidation poller** — API key + firewall IP cache across replicas
- [ ] **Bridge ring heartbeat** — register, heartbeat, mark stale on shutdown
- [ ] Start/stop tests per scheduler + fake clock

---

## 10. Limit warmup

- [x] `internal/limitwarmup` repository scaffold
- [x] `LimitWarmupService.run_after_usage_refresh` decision logic
- [x] `StreamingLimitWarmupSender` (streaming proxy + `x-codex-lb-limit-warmup` header)
- [x] Concurrency cap (`_MAX_CONCURRENT_WARMUP_SENDS = 4`)
- [x] Terminal/quota error code handling
  - [x] Service normalizes quota terminal error codes to `quota_still_exhausted`.
  - [x] Streaming sender parses upstream SSE terminal events.
- [x] Hook into usage refresh scheduler (post-write)
- [x] `PUT /api/accounts/{id}/limit-warmup`
- [x] Quota planner `POST /api/quota-planner/warm-now`
  - [x] Direct account warm-now probe creates/executes decision, logs request, and audits.
  - [ ] `apiKeyId` reservation enforcement is still tracked with API-key reservation parity.
- [x] Quota planner `POST .../decisions/{id}/cancel`

---

## 11. Quota planner (read mostly done)

- [x] Settings get/put, decisions list, forecast
- [x] Implement `warm-now` and `cancel`
  - [x] `warm-now` direct account probe uses the streaming warmup sender.
  - [x] `decisions/{id}/cancel` updates planned/skipped rows to canceled and reports not-cancelable rows.
- [ ] Background scheduler (see §9)
  - [x] Executes already-planned due warmup decisions in auto mode.
  - [ ] Generates planned actions from full Python forecast/action planning logic.
- [x] Audit on settings/decision mutations
  - [x] Settings update, decision cancel, and warm-now write audit rows.
  - [x] Warm-now writes audit rows for direct account probes.

---

## 12. API keys (admin)

- [x] CRUD, regenerate, trends, usage-7d
- [x] Proxy-side **rate limit enforcement** admission reservations (`apply_api_key_enforcement` request/token/cost reservation guard)
- [x] Reservation heartbeat during long streams
- [x] Exact reservation settlement before error-health writes on proxy failures
- [x] Hourly limit reset scheduler (see §9)
- [x] `GET /v1/usage` — OpenAI-compatible usage for the **calling API key** (proxy route, not dashboard)
- [x] Cache invalidation when keys change (cross-replica)

---

## 13. OAuth

- [x] PKCE, device code, browser callback server (`/auth/callback`)
- [x] `POST /api/oauth/{start,complete,manual-callback}`, `GET /api/oauth/status`
- [x] Account upsert + token encryption + summary cache invalidation
- [ ] Identity conflict / merge-by-ChatGPT-identity parity
- [x] Auth guardian scheduler integration (see §9)
- [ ] End-to-end manual test gate (OpenSpec task)

---

## 14. Settings, firewall, sticky sessions (admin)

- [x] Settings get/put, connect-address, upstream-proxy admin CRUD
- [x] Firewall IP list/create/delete
- [x] Sticky sessions list/purge/delete/delete-filtered/delete-one
- [x] Enforce firewall on **proxy** requests (Python checks client IP against rules)
- [x] Cache invalidation on firewall allowlist changes (local process)
- [x] Cache invalidation on firewall/settings changes (multi-replica)
  - [x] Firewall allowlist mutations bump cross-replica invalidation.
  - [x] Settings mutations bump cross-replica invalidation where cached by proxy paths.
- [x] Sticky session **cleanup scheduler** (see §9)
- [x] Audit logging on mutations (see §16)
  - [x] Accounts, API keys, firewall, settings, and sticky-session mutations write audit rows.
  - [x] Quota planner warm-now direct probes write audit rows.

---

## 15. Reports, models, conversation archive, audit

- [x] `GET /api/reports` — daily/model/account breakdown
- [x] `GET /api/models` — distinct models from request logs
- [x] `GET /api/conversation-archive/{files,records}`
- [x] `GET /api/audit-logs` — list
- [ ] Verify reports numerics match Python (window boundaries, error counting)
- [x] Conversation archive: security path validation, pagination/filter parity
- [x] Audit **`Insert` on mutations** — wired for accounts/API keys/firewall/settings/sticky sessions plus quota planner settings/cancel/warm-now.

---

## 16. Audit logging instrumentation

Wire `internal/audit.Repository.Insert` on:

- [x] Accounts: update, pause, reactivate, delete, import, alias, routing-policy, limit-warmup
- [x] API keys: create, update, delete, regenerate
- [x] Firewall: create, delete
- [x] Settings: update, proxy endpoint/pool/member, account binding
- [x] Sticky sessions: purge, delete, delete-filtered
- [x] Quota planner: settings update, warm-now, decision cancel
  - [x] Settings update
  - [x] Decision cancel
  - [x] Warm-now

---

## 17. Proxy — account selection & routing (core)

- [x] Selection strategies (capacity_weighted, relative availability, fill_first, single_account)
- [x] Routing policies normal / burn_first / preserve
- [x] Additional-quota gated models (`codex_spark`, registry JSON)
- [x] Account selection cache + generation invalidation
- [x] Leasing + stale lease reclaim
- [x] Opportunistic burn logic in balancer (preserve floor)
- [x] **`affinity.py`** — sticky session / prompt-cache / first-turn file-pin lookups in responses selection loop
- [x] File-pinned requests must not cross accounts
- [ ] `previous_response_id` → account pinning in **DB** (Go: in-memory index only — OK for single replica)
- [x] Excluded accounts must leave selection loop
- [ ] Idle disconnects must not mark healthy accounts unhealthy
- [ ] Security / trusted-access routing degradation path
- [ ] Upstream proxy pool binding per account (settings exist; verify proxy honors bindings)
- [ ] Background recovery state transitions on upstream errors

---

## 18. Proxy — request policy & validation

- [x] Model alias resolution, API-key model/reasoning/service-tier enforcement (basic)
- [x] Proxy bearer auth + loopback unauthenticated access
- [x] Strict function-tool schema validation (`enforce_strict_function_tools_format`)
- [ ] Full `request_policy.py`: multimodal rules, audio/file rejection, builtin tools, web_search, response_format
  - [x] Strict `text.format` / chat `response_format` schema pre-validation
  - [x] Chat request shape, message role/content validation, `input_audio` rejection, and chat `file_id` rejection
  - [x] Chat tool type validation / builtin tool allowlist and `web_search_preview` normalization
  - [x] Full chat `response_format` -> Responses `text.format` mapping
  - [ ] Full multimodal content coercion parity
- [ ] OpenAI SDK request-shape detection and error envelopes (`ClientPayloadError` mapping)
- [ ] API key rate limits / reservations (see §12)
  - [x] Proxy admission reservations, stream heartbeats, exact finalize/fail settlement, stale release.
  - [x] Go HTTP/websocket Responses reservations use the post-policy enforced model for model-filtered limits.
  - [ ] Bridge reservation forwarding.
  - [ ] Quota planner warm-now `apiKeyId` reservation enforcement.

---

## 19. Proxy — model catalog

- [x] Static bootstrap registry + TTL snapshot holder
- [x] `GET /v1/models`, `GET /backend-api/codex/models` (basic)
- [x] Upstream-driven model refresh scheduler (see §9)
- [ ] Codex model visibility / API-key allowlist interaction parity
- [x] Request-limit reservation on model list endpoints (Python `_enforce_request_limits`)

---

## 20. Proxy — chat completions (`POST /v1/chat/completions`)

- [x] Route exists; streaming via Responses upstream + `StreamChatChunks`
- [x] Non-streaming collect path (`CompleteChat`)
- [x] SSE keepalives
- [ ] Full **`to_responses_request()`** / chat mapping:
  - [ ] Tools + tool_choice normalization
  - [ ] Parallel tool call argument routing / deduplication
  - [ ] Multimodal content (image_url, input_audio, etc.)
  - [ ] `response_format` / JSON schema / strict mode
  - [ ] Developer vs system role handling
  - [ ] `stream_options.include_usage` parity + cursor usage fallback
  - [ ] Context-limit / usage-limit stream termination behavior
  - [ ] Non-stream forced upstream stream + collect parity
- [ ] Request log: capture tokens, cost, failure_phase, upstream_status_code on stream errors
- [ ] Codex CLI smoke: `gpt-5.3-codex-spark` (manual gate)

**Better:** Keep single upstream path (Responses) if output-compatible; don't re-port Chat Completions upstream unless needed.

---

## 21. Proxy — responses SSE (`POST /backend-api/codex/responses`, `POST /v1/responses`)

- [x] SSE stream + keepalives (Codex vs OpenAI keepalive frame)
- [x] Turn-state header injection (`EnsureDownstreamTurnState`)
- [x] Codex session affinity → sticky DB lookup/upsert for responses selection
- [x] `POST /backend-api/codex/responses/compact`
- [x] `POST /v1/responses/compact`
  - [x] Base compact payload mapping, account selection, upstream compact call,
        request logging, and API-key reservation settlement.
- [ ] Previous-response-not-found recovery (beyond in-memory owner index)
- [ ] OpenAI contract enforcement path for `/v1/responses` (`EnforceOpenAIContract` — verify full parity)
- [x] File upload references in responses input route via in-process file pins when no stronger affinity signal exists
- [ ] Bridge forwarding when session owned by another replica

---

## 22. Proxy — WebSocket responses

- [x] `GET /backend-api/codex/responses` (WS upgrade)
- [x] `GET /v1/responses` (WS upgrade)
- [x] Turn-state on upgrade response headers
- [ ] Port Python WS mixin (`app/modules/proxy/_service/websocket/mixin.py`):
  - [ ] Session lifecycle, ping/pong, idle timeouts
  - [ ] Mid-stream error envelopes on WS
  - [ ] Client disconnect → upstream cleanup (partial — verify no slot leaks)
  - [ ] Multiple sequential `response.create` messages per connection
  - [ ] Strict payload validation on WS path

---

## 23. Proxy — Codex control-plane passthrough routes

All **missing** in Go (Python proxies to upstream with account credentials):

- [x] `GET|POST /backend-api/codex/thread/goal/get`
- [x] `POST /backend-api/codex/thread/goal/set`
- [x] `POST /backend-api/codex/thread/goal/clear`
- [x] `POST /backend-api/codex/analytics-events/events`
- [x] `POST /backend-api/codex/memories/trace_summarize`
- [x] `POST /backend-api/codex/realtime/calls`
- [x] `POST /backend-api/codex/safety/arc`
- [x] `GET /backend-api/codex/agent-identities/jwks`
- [x] `GET /backend-api/wham/agent-identities/jwks`
- [x] `GET /backend-api/codex/opportunistic/admission`
- [x] Path alias middleware: `/backend-api/codex/v1/*` → `/backend-api/codex/*` (HTTP + WS upgrade routing)

---

## 24. Proxy — warmup

- [x] `POST /v1/warmup`
- [x] `POST /v1/warmup/{mode}`
- [x] Response schema: submitted / skipped / failed accounts (`WarmupResponse`)
  - [x] Mode validation, active account fan-out, API-key account-scope filtering,
        primary-window eligibility, request logging, and reservation settlement.

---

## 25. Proxy — media & files

- [x] `POST /v1/audio/transcriptions`
- [x] `POST /backend-api/transcribe`
  - [x] Multipart upstream forwarding to `/transcribe`, request logging,
        API-key admission reservation, and `gpt-4o-transcribe` validation
        on `/v1/audio/transcriptions`.
- [x] `POST /backend-api/files` — register upload
- [x] `POST /backend-api/files/{fileID}/uploaded` — finalize
  - [x] JSON upstream forwarding, request logging, API-key admission reservation,
        and in-process file pinning for finalize affinity.
- [x] `POST /v1/images/generations`
  - [x] Non-streaming single-image JSON requests translate to an upstream
        Responses `image_generation` tool call and return OpenAI Images
        `created/data[]` JSON.
- [x] `POST /v1/images/edits`
  - [x] Multipart `image` / `image[]` input translates to Responses
        `input_image` parts, forces the `image_generation` tool with
        `action=edit`, and maps completed image output to OpenAI Images JSON.
- [x] `POST /v1/images/variations` (hidden schema)
  - [x] Authenticated Python-compatible unsupported 404 response.
- [ ] Image proxy helpers (streaming/fan-out Cursor compat and full error mapping)

---

## 26. Proxy — usage/status endpoints

- [x] `GET /api/codex/usage`, `/api/codex/usage/` — API-key credit-limit payload with primary/secondary/monthly rateLimit and credits snapshots
- [ ] Full `_build_codex_usage_payload_for_api_key` parity for ChatGPT bearer aggregate limits and additional rate limits
- [x] `GET /v1/usage` — per-key OpenAI usage summary

---

## 27. Proxy — HTTP bridge & multi-replica

- [ ] `POST /internal/bridge/responses` — inter-replica forward (`internal/bridge` auth)
- [ ] Ring membership service (register, heartbeat, stale on shutdown)
- [ ] Durable bridge repository + coordinator (`durable_bridge_repository.py`, `durable_bridge_coordinator.py`)
- [ ] Readiness check includes bridge ring health (`/health/ready`)
- [ ] Session affinity across replicas (DB-backed sticky + bridge)

**Better:** For single-replica dev, in-memory previous-response index is acceptable; gate multi-replica features behind config.

---

## 28. Proxy — observability & errors

- [x] Basic request logging on proxy paths
- [ ] Failure phases / upstream codes / bridge_stage on errors
- [ ] OpenAI error envelope parity (`app/core/errors.py`, `openai_client_payload_error`)
- [ ] Prometheus metrics (request counts, latency, account selection outcomes)
- [ ] Structured access logs with request_id/account_id/api_key_id

---

## 29. OpenSpec / validation gates

- [x] `add-go-api-foundation` — validated
- [x] `add-go-oauth` — validated
- [x] `migrate-go-db-ownership` — validated (schema audit items remain)
- [ ] `add-go-proxy-core` — strict validate + manual smoke
- [x] `add-go-proxy-streaming` — strict validate; manual Codex CLI smoke open
- [ ] `add-go-proxy-media`
- [x] `add-go-usage-tracking`
- [x] `add-go-schedulers`
- [x] `add-go-limit-warmup`
- [x] `add-go-audit-logging`
- [x] `add-go-conversation-archive`
- [ ] Python integration tests ported or equivalent Go integration tests for each capability
- [ ] `codex review --base origin/main` clean before merge

---

## 30. Dev ergonomics (non-production but operator-critical)

- [x] `dev.json` runs Go API via air on `api.clb.dev.dawson.gg`
- [x] `cxd` zsh shortcut → Go dev API + Spark profile
- [ ] Usage refresh so Spark/additional-quota works without manual DB inserts
- [ ] Document runbook: `dev up`, dashboard URL, proxy URL, auth bypass env vars
- [ ] Parity test script: hit Python vs Go APIs and diff JSON schemas

---

## Priority tiers (suggested)

### P0 — Dashboard unusable / Codex CLI broken

1. Usage refresh scheduler + additional usage persistence (§8, §9)
2. Request log + account summary mapper parity (§4, §7) — partial done
3. API key rate limit reservations on proxy (§12, §18)
4. Chat/responses mapping gaps blocking real Codex sessions (§20, §21)
5. Sticky affinity in account selection (§17)

### P1 — Operator workflows

6. Accounts write API (§5)
7. Limit warmup + quota planner actions (§10, §11)
8. Audit instrumentation (§16)
9. Dashboard server projections (§6)
10. Auth bootstrap + dashboard-auth aliases (§3)

### P2 — Full product parity

11. Media/files/images/audio (§25)
12. Codex control-plane passthrough routes (§23)
13. HTTP bridge + multi-replica (§27)
14. Warmup endpoints (§24)
15. Health/drain/metrics (§1)
16. SPA static serving (§1)

### P3 — Hardening & cleanup

17. DB migration baseline audit (§2)
18. OpenSpec validate all changes (§29)
19. Integration test port matrix (§29)

---

## Intentional Go differences (keep unless broken)

- [x] **Projection fields omitted when unset** — frontend falls back to client-side pace/depletion math (`omitempty` on overview/projections).
- [ ] **Canonical auth under `/api/auth`** — maintain dashboard-auth aliases only for compat.
- [ ] **Single-replica in-memory previous-response index** — promote to DB when bridge/multi-replica lands.
- [ ] **Consolidated scheduler package** — prefer one `internal/scheduling` runner over Python's scattered task modules (same behavior, clearer ownership).

---

*Last updated: 2026-06-11. Regenerate route diff after major merges.*
