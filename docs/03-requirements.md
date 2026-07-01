# 03 — Requirements

Requirements are the build contract. Each is testable. Priority: **M** (must, v1), **S** (should, v1 if time), **C** (could, post-v1). IDs are stable references for the roadmap and acceptance gates.

> **Status:** every **M** and **S** requirement is implemented except the
> known partials listed here (implemented set incl. PDF ingestion FR-21,
> `config get/set` + `stop` FR-04, the `metrics_query` tool FR-50, the
> `api_key` exact-`count_tokens` adapter FR-40/41, and the Obsidian MCP
> interop FR-54). **Known partials:** FR-01 (init probes the embedding model
> but does not pull it — the install scripts pull), FR-05 (doctor checks
> `claude` presence, not per-profile authentication), FR-42 (`daily_cost_usd`
> is configured and recorded but not yet enforced as a cap), FR-44
> (compaction's `tokens_saved_est` is reported in the run summary, not
> persisted; raw source is not archived), FR-52 (the PostToolUse hook is a
> deliberate no-op — usage is ledgered at the chokepoint instead), FR-60
> (cache-token split and vault-growth-over-time series not yet charted),
> FR-61 (similarity edges + toggle in the graph view pending — wikilink/embed
> edges only). The remaining **C** items (FR-26 capture-by-Inbox, FR-64 chart
> CSV/JSON export; NFR-13 is done) are explicitly post-v1.

## Functional requirements

### Setup, profiles & CLI

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-01 | M | `axon init` scaffolds the vault structure, initialises the SQLite DB (with `sqlite-vec` + FTS5), installs the Claude Code plugin and writes `.claude/` (`CLAUDE.md`, `.mcp.json`, hooks), pulls the embedding model, and prints a clear, ordered progress log of every step and its result. |
| FR-02 | M | `axon init` is **idempotent**: re-running converges state and reports "no changes" where nothing differs; it never duplicates or clobbers user content. |
| FR-03 | M | All behaviour derives from `config.yaml` (default `~/.axon/config.yaml`; `--config` overrides) + `.env`; **profiles** (`personal`, `work`, …) fully isolate data dir, secrets, Claude account/`auth_mode`, policy and automation set. One installation runs one active profile (personal and work are separate installations); profile chosen via `AXON_PROFILE` or `--profile`. |
| FR-04 | M | CLI commands: `init`, `start`, `stop`, `status`, `doctor`, `ingest <url\|path>`, `search <query>`, `reindex`, `run <automation>`, `export`, `mcp`, `config <get\|set\|validate>`. Each supports `--profile` and `--json`. |
| FR-05 | M | `axon doctor` checks prerequisites (Go toolchain if building, Ollama reachable + model present, the `claude` CLI present and authenticated for the profile's `auth_mode`, **no stray `ANTHROPIC_API_KEY` on subscription/enterprise**, vault writable, DB healthy, ports free) and reports each as pass/warn/fail with a remediation hint. |
| FR-06 | S | `axon` can emit OS service units (launchd/systemd/Task Scheduler) to supervise the daemon, without the core depending on them. |
| FR-07 | M | A first run with **all automations disabled** still starts, serves the dashboard, and supports manual `ingest`/`search`. |

### Vault & methodology

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-10 | M | Scaffold a PARA + Inbox layout plus `Daily/`, `MOCs/`, `Templates/`, `.axon/`, `.claude/` (see Component 04), each non-system folder seeded with a short README explaining its purpose. |
| FR-11 | M | Provide note templates (daily note, atomic note, project, knowledge/source, MOC) with consistent YAML frontmatter (see Component 04 §frontmatter). |
| FR-12 | M | All vault mutations use **wikilink-safe** operations: renaming/moving a note updates every inbound `[[link]]` and alias; deletes are blocked from automation and require explicit confirmation. |
| FR-13 | M | The daemon maintains an up-to-date **link graph** (notes, wikilinks, tags, backlinks) derived from the vault, rebuildable via `reindex`. |
| FR-14 | S | Generate in-vault **Dataview/Bases dashboards** (open inbox, active projects, recent ingests, link-suggestion queue) and rely on Obsidian's native graph for in-vault visualisation. |

### Knowledge ingestion

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-20 | M | Ingest a **URL**: fetch (respect egress allowlist), extract main readable content, convert to clean Markdown, strip boilerplate. |
| FR-21 | M | Ingest a **PDF** and a local **Markdown/text** file via the same pipeline. |
| FR-22 | M | Enrich each source under a token budget: title, source URL/author/date, concise summary, tags, and ≥1 suggested wikilink to existing notes; write as a note in `03-Resources/Knowledge`. |
| FR-23 | M | Chunk and **embed** via the embedding provider (Ollama default), upsert vectors into `sqlite-vec` and text into FTS5; store source + chunk metadata. |
| FR-24 | M | Ingestion is idempotent on content hash; re-ingesting updates the note and re-embeds only changed chunks. |
| FR-25 | M | **Hybrid search** (lexical FTS5 + semantic vector) with rank fusion, exposed via CLI and MCP, returning note refs + snippets + scores. |
| FR-26 | C | Capture-by-Inbox: a special Inbox note/format where pasted URLs are auto-detected and queued for ingestion on the next ingestion tick. |

### Automation engine

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-30 | M | A portable **scheduler** runs automations on cron expressions and/or change events, with per-automation enable flag, schedule, model, token budget, dry-run, and catch-up policy — all from config. |
| FR-31 | M | Each automation **gates on new material** (content hashes / change detection); with nothing relevant changed, it logs a skip and makes **no** Claude call. |
| FR-32 | M | Ship these standard automations (each independently toggleable): **heartbeat**, **daily-log**, **inbox-triage**, **compaction**, **context-export**, **knowledge-reindex**, **knowledge-digest**, **link-suggester**, **budget-guard**. (Specified in Component 06.) |
| FR-33 | M | Automations run via the agent adapter (Claude Code `claude -p` by default; the direct-API in-process adapter only in `auth_mode: api_key`), respect dry-run (compute + log intended changes without writing), and record a run record with status, duration, tokens (cost in `api_key` mode) and a diff summary. |
| FR-34 | M | `axon run <automation> [--dry-run]` triggers any automation manually with the same code path as the scheduler. |
| FR-35 | S | Per-automation **locks** prevent overlapping runs; a hung run times out and is recorded as failed. |

### Context & token awareness

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-40 | M | Before any Claude call, **pre-flight** with a token estimate (local estimator on subscription/enterprise; exact `count_tokens` in `auth_mode: api_key`); record the estimate; refuse/downgrade/defer if it would breach the active token window/credit. |
| FR-41 | M | After every Claude call (interactive-via-hooks and headless `claude -p`), record reported `usage` (input/output/cache tokens), model, operation, profile, timestamp — and computed cost **in `api_key` mode** — to the **token ledger**. |
| FR-42 | M | Enforce **budgets**: per-profile daily and weekly **token** caps and per-automation caps (plus per-dollar caps in `auth_mode: api_key`); expose remaining budget via CLI, MCP and dashboard. |
| FR-43 | M | **budget-guard** automatically pauses non-essential automations as the cap approaches (configurable thresholds) and resumes when the window resets; essential automations and interactive use are never silently blocked but are surfaced. |
| FR-44 | M | **Compaction** distils stale session logs and oversized notes into durable summary notes, shrinking future context, and records tokens-saved estimates. |
| FR-45 | M | **Model selection** per operation/automation (e.g. Haiku for classification, Sonnet for routine edits, Opus for synthesis), overridable in config. |
| FR-46 | S | Retrieval-first context assembly: never send the whole vault; build context from top-k hybrid-search results with a configurable token ceiling. |

### Agent bridge (Claude Code integration)

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-50 | M | Ship an **MCP server** exposing tools: `vault.search`, `vault.read`, `vault.write`, `vault.patch`, `vault.move` (wikilink-safe), `daily.append`, `knowledge.ingest`, `knowledge.search`, `tokens.status`, `metrics.query`, `automations.list`, `automations.run`. (Tool contracts in Component 08.) |
| FR-51 | M | `axon init` writes a valid `.mcp.json` (or equivalent) so Claude Code discovers the AXON MCP server, scoped to the active profile. |
| FR-52 | M | Provide Claude Code **hooks**: `SessionStart` (inject compact vault/budget status), `PreToolUse` (block unsafe vault file ops; enforce wikilink-safe path), `PostToolUse` (log AXON-tool token round-trips, flag budget), `Stop` (suggest compaction when context is large). Hooks are deterministic and policy-enforcing. |
| FR-53 | M | Provide a Claude Code **plugin** bundling skills (e.g. `ingest-url`, `run-daily-log`, `triage-inbox`, `suggest-links`) and subagents (e.g. `librarian` for deep vault search, `summariser` for distillation) plus a `CLAUDE.md` template encoding the vault schema and conventions. |
| FR-54 | S | Interop: allow configuring an external/community Obsidian MCP server as an alternative vault backend behind the same tool contract. *(Built: `profiles.<p>.interop.obsidian_mcp` is registered alongside AXON's own server by `axon mcp install`; AXON's server stays the default — Component 13 §6, `internal/clients`.)* |

### Dashboard & observability

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-60 | M | A local web dashboard (React/Recharts SPA) at a configurable port shows, with **real-time** updates (SSE/WebSocket): tokens over time (by day/automation/model), usage/budget gauges (cost gauges in `api_key` mode), automation run timeline + success rate, ingestion throughput + queue depth, and vault growth (notes/links/words). |
| FR-61 | M | An interactive **knowledge graph** view: nodes = notes, edges = wikilinks plus high-similarity vector neighbours (toggle), with basic filtering by folder/tag. |
| FR-62 | M | A structured **event log/activity feed** of runs, ingests, skips, budget events and errors, filterable, streamed live. |
| FR-63 | M | Dashboard reads only from the daemon API; it never holds secrets and binds to localhost by default. |
| FR-64 | C | Export any chart's underlying data as CSV/JSON. |

### Personal memory, identity & multi-client *(Phases 8–9 — built)*

FR-70…FR-73 and NFR-14 are **implemented** (Phase 8 — `internal/identity`, the
`axon onboard` wizard, the `SessionStart` injection, the `memory_remember` MCP
tool and the `memory-distill` automation). FR-74…FR-76 are **implemented**
(Phase 9 — `internal/clients`, the `axon mcp install --client code|desktop`
command, and the per-client `axon doctor` checks).

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-70 | M | **Personal identity & memory layer.** AXON maintains a first-class set of vault notes under `02-Areas/Profile/`: **`USER.md`** (who the user is — role, timezone, goals, communication preferences, key people/projects/tools), **`SOUL.md`** (the agent's persona — name, voice/tone, values, boundaries), and **`MEMORY.md`** (durable decisions, lessons and learned preferences). They are plain Markdown (the vault is the source of truth) and remain human-editable. (Spec in Component 12.) |
| FR-71 | M | **Onboarding wizard.** `axon onboard` is an interactive, idempotent wizard that interviews the user, populates `USER.md`/`SOUL.md`, seeds `MEMORY.md`, and offers to wire additional Claude clients (FR-74). It converges (never clobbers existing content; asks before overwrite). `axon init` detects a missing identity layer and prompts the user to run it. |
| FR-72 | S | **Session profile injection.** The `SessionStart` hook injects a compact, **token-bounded** rendering of the identity layer (USER profile + SOUL persona + most-recent `MEMORY` entries) into every Claude Code session, with **no model call**, so the agent "knows the user" without retrieval. The injection respects a configurable token ceiling. |
| FR-73 | S | **Memory maintenance.** An MCP tool `memory_remember` lets the agent append a durable decision/lesson/preference into `MEMORY.md` (wikilink-safe, into an `axon:memory` managed block); an optional `memory-distill` automation periodically distils recent daily-note activity into `MEMORY.md` and compacts an over-long block (a model call **through the token manager**). Captured memory is treated as data, not instructions (NFR-05). |
| FR-74 | M | **Multi-client MCP wiring.** `axon mcp install --client <code\|desktop>` (and `--print`) generates/installs a client's MCP registration. For **Claude Desktop** it writes a profile-scoped `mcpServers` entry into `claude_desktop_config.json` **non-destructively**, so Desktop can use AXON's vault + knowledge + token tools. (Spec in Component 13.) |
| FR-75 | S | **Client-capability honesty.** AXON documents and `axon doctor`-reports that Claude Desktop receives the MCP **tools** but not hooks/skills/subagents/headless automations. AXON's own tools remain wikilink-safe and path-sandboxed in the server, so vault safety for AXON operations does **not** depend on the client's `PreToolUse` hook. |
| FR-76 | C | **Concurrent clients.** Multiple Claude clients (Code + Desktop) may target the same profile/vault; the daemon remains the single owner of scheduled writes, and the single-writer SQLite caveat is documented. |

## Non-functional requirements

| ID | Pri | Requirement |
|----|-----|-------------|
| NFR-01 | M | **Local-first:** no required cloud services beyond Claude itself (reached via Claude Code, or the Claude API in `api_key` mode) and user-chosen ingestion URLs; all persistent state on local disk. |
| NFR-02 | M | **Cross-platform:** macOS, Linux, Windows (WSL allowed only where it clearly simplifies, and documented). |
| NFR-03 | M | **Reproducible:** clone → set ≤6 values → `axon init` → running system in ≤15 min; deterministic given the same config. |
| NFR-04 | M | **Isolated profiles:** no shared data, secrets or account between profiles; verified by inspection. |
| NFR-05 | M | **Safety:** secrets never logged or sent to the model; redaction applied pre-send; egress allow-listed; destructive ops gated; fetched content treated as data not instructions. |
| NFR-06 | M | **Durability:** atomic vault writes; vault is reconstructable source of truth; DB rebuildable via `reindex`. |
| NFR-07 | M | **Observability:** every Claude call, automation run, ingest and error is logged with structured fields and visible on the dashboard within ≤5s. |
| NFR-08 | M | **Frugality:** mandatory token chokepoint (ADR-007); no Claude call without pre-flight + budget check. |
| NFR-09 | S | **Performance:** hybrid search returns in <500ms for a 5k-note vault on commodity hardware; daemon idle CPU negligible; embedding batch size and worker pool tuned for Ollama. |
| NFR-10 | M | **Clear output:** every long-running command streams human-readable progress; failures state the cause and a remediation step. |
| NFR-11 | S | **Testability:** providers (agent, embeddings, fetcher) are interfaces with fakes; automations runnable in dry-run; a `--profile test` uses a temp vault + in-memory/temp DB. |
| NFR-12 | M | **Config-driven extensibility:** new automations and ingestion sources are added by dropping a module + config, not by editing core wiring. |
| NFR-13 | C | **Backups:** `axon export` snapshots are portable, plain-format and self-describing (manifest + Markdown + JSON). |
| NFR-14 | M | **Personal-data privacy** *(Phase 8 — built)*: the identity/memory layer (`USER.md`/`SOUL.md`/`MEMORY.md`) is local vault Markdown; it is surfaced to the model only as bounded, user-controllable session context; redaction (NFR-05) applies before any egress; it is never written to logs, events, the ledger, or exports (`memory_remember` makes no model call so it is never ledgered; `memory-distill` ledgers only token usage, never the memory text; `axon export` writes counts, not note bodies). |

## Traceability

Each roadmap milestone (Component 11) lists the FR/NFR IDs it satisfies; each component spec restates the IDs it owns. No requirement is "done" until a documented acceptance check passes.
