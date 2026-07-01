# Production-Readiness Task List

Findings from the full-project review (2026-07-01, Claude Fable 5: five parallel audits — cardinal
rules, Go core, docs traceability, tests/CI/security, dashboard). Update the **Status** column as
items are fixed: `todo` → `in progress` → `done` (with commit) or `wontfix` (with reason).

## P0 — Blockers

| # | Task | Where | Status |
|---|------|-------|--------|
| 1 | Restore `web/dist/.gitkeep` (deleted in `531e71f`); fresh clone fails `go build` with `pattern all:dist: no matching files found`. Reorder CI so `gofmt`/`go vet` run **before** the npm build so this can't regress. | `web/dist/`, `.github/workflows/ci.yml` | done |
| 2 | Deleting a note orphans `fts_chunks` rows (no FK/trigger on FTS5 table) → after `axon reindex`, any matching search aborts with `hydrate chunk N: sql: no rows`. Delete FTS rows in `DeleteNote`; make `hydrateChunk` skip missing chunks instead of failing. | `internal/db/notes.go:64`, `internal/db/search.go:171` | done |
| 3 | SSRF: egress policy blocks only link-local IPs — loopback/RFC1918/ULA pass, no dial-time resolved-IP check (DNS rebinding), and the example config's `"*"` wildcard disables the allowlist entirely. Block private ranges unconditionally in `CheckIngestPolicy` and validate resolved IPs in a dial `Control` hook. | `internal/ingestion/policy.go:38`, `internal/ingestion/fetch.go` | done |
| 4 | INSTALL.md says "Go 1.22+" and `preflight.sh` sets `GO_MIN="1.22"`, but `go.mod` requires 1.26 — preflight green-lights a toolchain that cannot build. Align both to 1.26. | `INSTALL.md:24`, `scripts/preflight.sh:25` | done |

## P1 — Major correctness

| # | Task | Where | Status |
|---|------|-------|--------|
| 5 | Terminal run state written with the already-expired run context: on the 5-min engine timeout or SIGTERM, `FinishRun` no-ops and runs stay `running` forever. Use `context.WithoutCancel` + short fresh timeout for `finishFailed`/`SetCursor`. | `internal/automations/engine.go:98-138` | done |
| 6 | SQLite pragmas applied per-connection, not in DSN — after a `driver.ErrBadConn` reconnect, `foreign_keys=OFF` silently returns, breaking every cascade. Move pragmas into the DSN (`?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)`). | `internal/db/db.go:22-47` | done |
| 7 | `axon reindex` rebuilds only `notes`+`links`, never chunks/FTS/vectors — violates the "SQLite is derived and disposable" contract (ADR-006): after `rm db.sqlite && axon reindex`, search is empty. Re-chunk bodies whose `content_hash` changed or that have no chunks. | `internal/core/reindex.go` | done |
| 8 | No double-daemon guard: `axon start` blindly overwrites the pidfile; a service + manual start double-runs every automation (double token spend). Refuse to start if pidfile PID is alive. | `cmd/axon/start_cmd.go:45`, `cmd/axon/pidfile.go` | done |
| 9 | `Scheduler.CatchUp` lacks the `recover()` that `fire()` has — a panicking catch-up automation crashes the daemon at startup (launchd crash loop). Extract shared `safeRun`. | `internal/scheduler/scheduler.go:120` | done |
| 10 | Agent-error paths (JSON parse failure after successful run, 120s adapter timeout) write nothing to the token ledger — real quota burned invisibly, guard never trips. Record a conservative ledger row on adapter error (`operation` gets a `:failed` suffix; input debited from the pre-flight estimate). | `internal/tokens/manager.go:299` | done |

## P1 — Security & cardinal-rule hardening

| # | Task | Where | Status |
|---|------|-------|--------|
| 11 | `vault_write force=true` is a model-controllable full overwrite of any note (the de-facto destructive op, no guard/backup). Restrict `force` to `axon_managed` notes or snapshot prior content first. | `internal/mcp/tools.go:119` | todo |
| 12 | PreToolUse hook lets Claude's native `Write`/`Edit` overwrite any vault `.md` (only `.obsidian`/`.git` blocked), bypassing managed blocks. Deny `Write` to existing vault notes, steer to `vault_patch`. | `internal/hooks/hooks.go:208` | todo |
| 13 | Vault system dirs (`.claude`, `.git`, …) are skipped on List/Walk but writable via Write/Patch/Create — an injected agent can rewrite `.claude/settings.local.json`/`CLAUDE.md` for its next session. Refuse writes into system dirs; also `EvalSymlinks` in `safeAbs`. | `internal/vault/fs.go:45-80` | todo |
| 14 | Redaction never runs at the model chokepoint — automations send un-redacted note content to Claude; redactor only covers ingestion + SessionStart. Apply redactor inside `tokens.Manager.Run`. | `internal/tokens/manager.go:271`, `internal/automations/model.go` | todo |
| 15 | Prompt-injection delimiter collision: untrusted content framed with `<<< >>>` is concatenated unescaped — content containing `>>>` escapes the data block. Neutralize delimiters or use per-call nonce fences. | `internal/ingestion/claude_enrich.go:81`, automations prompts | todo |
| 16 | Wikilink integrity bugs: resolution is case-sensitive (Obsidian is not) so `[[beta]]` → `Beta.md` isn't rewritten on move; `[[Note^block]]` refs never rewritten. Fix `resolvesTo`/`splitWikilink` + tests. | `internal/vault/links.go:59-87` | todo |

## P2 — Dashboard, CI, tests

| # | Task | Where | Status |
|---|------|-------|--------|
| 17 | Dashboard event bugs: (a) live-feed dedup key never matches (SSE `time.Time` JSON vs RFC3339-UTC rows) → every event duplicates after the 15s poll; (b) SPA listens for `'budget'` (never emitted) and misses `token.defer/downgrade/error`; (c) `useFetch` swallows errors — dead daemon renders as empty states. | `web/src/App.jsx:15-40,502`, `internal/db/dashboard.go:22` | todo |
| 18 | CI gaps: no golangci-lint (no `.golangci.yml` exists at all despite CLAUDE.md requiring it green), Linux-only (no macOS leg for launchd/keychain paths), no govulncheck, no shellcheck, vite 5.4.x has a high-severity advisory (dev-only) — upgrade to Vite 7+. | `.github/workflows/ci.yml`, `web/package.json` | todo |
| 19 | Highest-risk untested behaviors: `claude -p` subprocess timeout/kill/stderr path, entire `APIKey` adapter (0%), SSRF redirect re-validation, migration upgrade path from seeded intermediate versions, concurrent budget accounting. | `internal/agent/`, `internal/ingestion/fetch.go`, `internal/db/migrate.go` | todo |

## P3 — Documentation

| # | Task | Where | Status |
|---|------|-------|--------|
| 20 | CLAUDE.md + docs/02 structure sections are stale: `plugin/`/`templates/` don't exist (now `internal/claudeassets/assets`, `internal/scaffold/assets`), `internal/api` doesn't exist (it's `internal/dashboard`), 9 packages undocumented, `core/` description wrong (orchestration lives in `cmd/axon/start_cmd.go`), still says sqlite-vec + gocron vs ADR-010 reality (`modernc.org/sqlite`, `robfig/cron/v3`). | `CLAUDE.md`, `docs/02-architecture.md` | todo |
| 21 | docs/03 "every M and S requirement is implemented" banner is overstated — name the partials: FR-61 similarity edges/toggle, FR-42 `daily_cost_usd` never enforced, FR-01 model pull (probe-only), FR-05 doctor auth check (presence-only), FR-52 PostToolUse no-op, FR-60 cache split + vault growth over time, FR-44 compaction persistence. Implement or mark deferred. | `docs/03-requirements.md` | todo |
| 22 | Decide the headless-adapter story: docs/08 promises `claude -p --agent <name>` with AXON MCP tools; code runs `--bare --max-turns 1 --tools ""` (single-turn, no tools). Align doc or implementation. | `docs/08`, `internal/agent/claudecode.go:87` | todo |
| 23 | Smaller doc fixes: GUIDE §8 omits `memory-distill` + `inbox-triage` tier mismatch; README/GUIDE omit `axon stop`/`onboard`; docs/08 MCP tool schema drift (14 tools, `knowledge_search` aliases `vault_search`, `vault_patch` heading mode unimplemented); vault CLAUDE.md template omits `metrics_query`; docs/09 SSE `kind` union wrong. | `docs/GUIDE.md`, `README.md`, `docs/08`, `docs/09`, `internal/claudeassets/assets/CLAUDE.md.tmpl` | todo |
