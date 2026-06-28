# 11 — Build Roadmap

Phased so each milestone is independently runnable and demonstrable. A phase isn't "done" until its acceptance gate passes. Build in order; later phases assume earlier seams (`agent`, `embeddings`, `Vault`, event bus) exist.

## Phase 0 — Skeleton & contracts
**Build:** single Go module (`cmd/axon` + `internal/{config,core,db,vault,mcp,dashboard,...}`); config schema (struct tags + validator) + profile resolution incl. `claude.auth_mode` and OAuth-token/`CLAUDE_CONFIG_DIR` resolution; `.env` secret resolver; structured logger (`log/slog`) + in-process event bus; SQLite connection + migration runner; the provider **interfaces** (`Agent`, `EmbeddingProvider`, `Fetcher`, `Vault`) with fakes (the `Agent` fake stands in for `claude -p`); `axon` cobra CLI skeleton with `config validate` + `doctor` stubs.
**Gate:** `axon config validate` passes on the example config; `doctor` runs, reports prerequisite status, and **warns on a stray `ANTHROPIC_API_KEY` for subscription/enterprise**; CI builds and runs unit tests against fakes.
**Satisfies:** FR-04 (partial), FR-05, NFR-11, NFR-12 foundations.

## Phase 1 — Vault core & init
**Build:** vault read/write, frontmatter, `axon:*` managed blocks, **wikilink-safe** move/patch, link-graph builder; vault scaffold + templates + folder READMEs; `axon init` steps 1–6, 9–10 (config, prereqs, data dir, DB, embedding-model assert, scaffold, first index, summary) — idempotent + verbose; `reindex`.
**Gate:** S1 (clone→running shell in ≤15 min minus automations), S2 (idempotent init), S5 (rename leaves zero broken links), S9 (reindex rebuilds from vault). Existing-vault scaffold doesn't clobber.
**Satisfies:** FR-01, FR-02, FR-10…FR-13, ADR-006.

## Phase 2 — Embeddings, ingestion & search
**Build:** Ollama `EmbeddingProvider`; ingestion pipeline (fetch→extract→clean→redact→hash→enrich→write→chunk→embed→index) with idempotency; FTS5 + sqlite-vec; **hybrid search** + `retrieve()`; `axon ingest`/`search`.
**Gate:** S6 (one-command ingest yields clean, summarised, linked, retrievable note); re-ingest unchanged → skip, no model call; denied domain fails pre-fetch (work policy).
**Satisfies:** FR-20…FR-25, NFR-05 (egress/redaction), retrieval for later phases.

## Phase 3 — Token & context manager
**Build:** the chokepoint (`Authorize`/`Run`/`BuildContext`/`Status`); local token **estimate** pre-flight + caching (exact `count_tokens` behind the `api_key` adapter); `usage` accounting + `token_ledger` (from `claude -p` JSON output); day/week/per-call token windows + `budget_state`; model selection (`claude -p --model` preference); pricing table → `cost_usd` for `api_key` mode only.
**Gate:** S4 (every Claude call ledgered with model/operation/counts; cost in `api_key` mode); a budget-breaching call is downgraded/deferred per policy and logged; `axon status` reports remaining token budget.
**Satisfies:** FR-40…FR-46, NFR-08, ADR-007. **Retrofit:** route Phase 2's enrichment call through the manager.

## Phase 4 — Automation engine
**Build:** scheduler (gocron + jitter + locks + catch-up + timeout); `Automation` runner contract + standard lifecycle (change-gate, budget pre-check, dry-run, run record); headless `claude -p` adapter + in-process adapter; the standard automations: **budget-guard, heartbeat, knowledge-reindex, context-export** (cheap/no-model first), then **inbox-triage, daily-log, link-suggester, compaction, knowledge-digest**; `axon run`.
**Gate:** S3 (no model call when nothing changed — verified by skip logs); dry-run prints intended edits + token estimate, writes nothing; budget threshold pauses non-essential automations; failed run leaves no half-edited note.
**Satisfies:** FR-30…FR-35, FR-43, FR-44 (compaction), ADR-004, ADR-008.

## Phase 5 — Agent bridge (MCP, hooks, plugin)
**Build:** AXON MCP server + tool contracts (wikilink-safe writes, hybrid/knowledge search, token status, automations); generate `.mcp.json`/`settings.json`/`CLAUDE.md`; hooks (`SessionStart`/`PreToolUse`/`PostToolUse`/`Stop`) via thin `axon hook` scripts; Claude Code **plugin** (skills + subagents); profile-scoped account isolation; complete `axon init` step 7.
**Gate:** in Claude Code in the vault, search/ingest/move work and renames keep links intact; `SessionStart` injects budget+inbox with no model call; a delete / link-breaking raw edit is blocked; plugin skills/subagents invocable.
**Satisfies:** FR-50…FR-54, FR-52 guardrails, ADR-005.

## Phase 6 — Dashboard & observability
**Build:** dashboard API + SSE off the event bus; Vite + React + Recharts SPA built to `web/dist` and embedded via `embed.FS` (Tokens, Usage & budget, Runs, Ingestion, Vault growth, Knowledge graph, Activity feed); `/health`; generated in-vault Dataview/Bases dashboards.
**Gate:** S4 latency (≤5s to dashboard); token chart splits by automation+model; usage gauge matches `axon status`; knowledge graph renders + filters; localhost-only, no secrets.
**Satisfies:** FR-60…FR-64, FR-14, NFR-07.

## Phase 7 — Multi-profile, policy & hardening
**Build:** finish profile isolation end-to-end (data/secrets/account/policy); `policy` enforcement everywhere (egress, ingest domains, redaction, `allowed_automations`, residency); OS service units (`axon service`); export/backup polish; docs + the generated repo README.
**Gate:** S7 (two isolated profiles, no shared data/secrets/account); work profile demonstrably more constrained (a denied automation never schedules; a denied domain never fetches; redaction scrubs pre-send); S8 (all-automations-off still useful).
**Satisfies:** FR-03, FR-06, FR-07, NFR-01…NFR-06, NFR-10.

## Cross-cutting (every phase)
- Tests against provider fakes; `--profile test` uses a temp vault + temp DB.
- No Claude call added without going through the token manager (enforce in review).
- No vault mutation added without wikilink-safe ops + dry-run.
- Keep `CLAUDE.md` < ~200 lines; push detail into `@docs/…`.
- Update the traceability: each PR references the FR/NFR IDs it closes.

## Suggested first slice for Claude Code
Stand up **Phase 0 → Phase 1** as a single working increment (config + init + vault-safe ops + reindex + doctor). That alone is a useful, safe, reproducible vault tool and validates the riskiest seams (wikilink safety, idempotent init) before any token spend. Then layer Phases 2–3 (knowledge + frugality), which together deliver the "useful second brain" core, before automations and UI.
