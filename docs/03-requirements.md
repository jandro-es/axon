# 03 — Requirements

Requirements are the build contract. Each is testable. Priority: **M** (must, v1), **S** (should, v1 if time), **C** (could, post-v1). IDs are stable references for the roadmap and acceptance gates.

> **Status:** every **M** and **S** requirement is implemented (incl. PDF
> ingestion FR-21, `config get/set` + `stop` FR-04, the `metrics_query` tool
> FR-50, the `api_key` exact-`count_tokens` adapter FR-40/41, the Obsidian
> MCP interop FR-54, init model pull + dim assertion FR-01, per-profile auth
> checks in doctor FR-05, the `daily_cost_usd` hard cap FR-42, compaction
> archiving + persisted `tokens_saved_est` FR-44, the cache-token split and
> vault-growth series FR-60, and graph similarity edges with a toggle FR-61).
> One deliberate design deviation: FR-52's PostToolUse hook is a documented
> no-op — every Claude round-trip is already ledgered at the token-manager
> chokepoint, so a per-tool hook would double-count (see docs/08 §2). The
> C items are now implemented too — FR-26 (capture, ADR-016) and FR-64
> (chart export, ADR-020); FR-76 (concurrent clients) remains a documented
> caveat. NFR-13 is done.

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
| FR-26 | C | Capture-by-Inbox: a special Inbox note/format where pasted URLs are auto-detected and queued for ingestion on the next ingestion tick. **Implemented** by the `capture` automation (ADR-016; own-line URLs in any `00-Inbox` note, plus dropped files — FR-81…FR-83). |

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
| FR-64 | C | Export any chart's underlying data as CSV/JSON. **Implemented** (FR-96/ADR-020): `GET /api/export` + per-card download links. |

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

### Universal capture *(built)*

FR-81…FR-83 are **implemented** (ADR-016; spec in
`docs/superpowers/specs/2026-07-03-universal-capture-design.md`): the
`capture` automation (registry + `*/5 * * * *` starter schedule), inbox
listing change gate, failure memory in `automation_state`, archive-after-
ingest via the wikilink-safe move, and the `capture.enrich` toggle. The same
slice implements **FR-26 (capture-by-Inbox)**. Priorities are relative to
this slice.

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-81 | M | **File-drop capture.** Non-markdown files dropped into `00-Inbox/` are ingested through the pipeline (`AllowLocalFiles`, sandboxed to files physically enumerated in the inbox listing — paths inside notes are never file targets) and, on success, the original is moved **wikilink-safely** to `capture.archive_dir` (default `04-Archive/Capture/YYYY-MM/`). Nothing is ever deleted. |
| FR-82 | M | **Capture bookkeeping.** Ticks are change-gated on the inbox listing hash; failed items are remembered in automation state and skipped until they change, surfaced once in `.axon/review-queue.md`, and emitted as events; every capture ingest is observable through the standard run rows and `ingest.*` events. Inbox notes are never modified by capture (cardinal rule 2). |
| FR-83 | S | **Capture enrichment toggle.** `capture.enrich: heuristic \| claude` (default `heuristic`, zero tokens). `claude` routes enrichment through the token-manager chokepoint on the `routine` tier (ADR-015 local routing and fallback apply) and degrades to heuristic under budget denial. |
| FR-121 | S | **Browser/Shortcuts capture endpoint (ADR-024, roadmap 1.1 D1).** `POST /api/capture` writes a caller-supplied `{url?, title?, text?}` to a fresh `00-Inbox/capture-<UTC>.md` (URL on its own line so the capture automation ingests it next tick; text-only stays an inbox note) — wikilink-safe, non-destructive, no model call. Guarded identically to review actions (loopback + Host guard + JSON content type + `X-Axon-Capture: 1` preflight header) and gated by `dashboard.capture_enabled` (pointer-default-ON; off → 404). Emits a `capture.received` event. Spec in `docs/superpowers/specs/2026-07-05-capture-endpoint-design.md`. |
| FR-122 | S | **Served capture page + recipes.** `GET /capture` serves a tiny same-origin page (404 when capture disabled) that reads `location.hash` and performs the guarded POST — so a cross-origin bookmarklet (`window.open('…/capture#u=…')`) reaches the endpoint without weakening the guard. Docs ship the bookmarklet one-liner and a macOS Shortcuts recipe; `/health` exposes `capture_enabled`. |
| FR-123 | M | **Scanned-PDF OCR fallback (roadmap 1.1 D2).** The ingestion pipeline OCRs a PDF only when its text-layer extraction yields below `minExtractedChars` and `ingestion.ocr` is enabled; recovered text ≥ threshold replaces the body and flows through the normal enrich/chunk/embed stages as data (NFR-05), else the item is reported empty as before. Provider selected by `ingestion.ocr: off\|apple\|tesseract` (default off) behind a local-only `OCR` interface. Spec: `docs/superpowers/specs/2026-07-05-pdf-ocr-design.md`; ADR-026. |
| FR-124 | M | **Apple on-device OCR provider.** `apple` performs OCR via a compiled Swift helper (Vision `VNRecognizeTextRequest` + PDFKit rasterisation), reusing the ADR-013 pattern: pure-Go host, JSON-over-subprocess, idempotently built by `axon init` (`EnsureOCRHelper`), macOS-gated, no network. `axon doctor` reports helper availability. |
| FR-125 | S | **Tesseract OCR provider.** `tesseract` performs cross-platform OCR by orchestrating the `pdftoppm` rasteriser + the `tesseract` binary (both detected at runtime; a missing binary yields an actionable error). `axon init` warns when the binaries are absent and `axon doctor` reports their presence. All processing is local (ADR-026). |
| FR-126 | S | **Optional local reranker (roadmap 1.1 B2).** When `retrieval.rerank` is enabled, `search.Search` fetches `top_k × rerank_overfetch` (default 3) hybrid candidates, scores them against the query with a local reranker, reorders, and returns the top-k; any reranker failure falls back to the original fused top-k (best-effort, never breaks search). Reranking is a retrieval primitive outside the token-manager chokepoint (ADR-027), local-only, budget-exempt, default off; every retrieval caller inherits it via `Search`/`Retrieve`. Spec: `docs/superpowers/specs/2026-07-05-local-reranker-design.md`. |
| FR-127 | S | **Ollama pointwise reranker.** `retrieval.rerank: ollama:<model>` scores each candidate pointwise via Ollama `/api/generate` (bounded concurrency, per-call timeout), parsing a 0–10 relevance number; unreachable/garbage output degrades to the fused order. `axon doctor` reports Ollama/model availability and warns on a malformed `retrieval.rerank` value. All processing is local (ADR-027). |
| FR-128 | M | **Entity extraction (roadmap 1.1 C2).** A classify-tier `entity-pages` automation, change-gated on notes updated within a lookback window (default 7 days; `Entities/`, `.axon/` and READMEs excluded), extracts named people and projects from each new note via one structured chokepoint call (`OutputSchema` + `ValidateOutput`), treating the note as data (NFR-05). Deferred-safe and dry-run aware. Disabled by default. Spec: `docs/superpowers/specs/2026-07-05-entity-pages-design.md`. |
| FR-129 | S | **Mention threshold.** An entity's page is materialised only once it appears in ≥ `mentionThreshold` distinct notes (default 2); pending mentions are held in `automation_state` and backfilled when the page is created. Reprocessing a note never double-counts (dedup within pending and against the block). |
| FR-130 | S | **Entity pages & `axon:mentions` block.** Entity pages live under `Entities/People/` and `Entities/Projects/` (lazily created); each maintains an `axon:mentions` managed block of `- [[note]] (date)` lines appended wikilink-safely (`vault.Create`/`vault.Patch`, deduped). Human prose outside the block is never touched and there is no delete (cardinal rule 2). |

### 1.1 — Ask, connect, anticipate *(built 2026-07-05/06)*

FR-108…FR-120 and FR-131…FR-133 are the **1.1** contract (Phases A–D of
`docs/14-roadmap-1.1.md`), shipped alongside FR-121…FR-130 in the table above.
Each traces to its slice spec under `docs/superpowers/specs/`. Priorities are
relative to their slice.

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-108 | M | **Grounded-or-silent ask engine.** `internal/ask` answers a question from retrieved vault/knowledge context only: `search.Retrieve` builds the bounded context (`retrieval.top_k`/`max_context_tokens`); a deterministic pre-model gate refuses (zero tokens) when retrieval returns nothing or the best fused score is below a code-constant floor; one synthesis-tier chokepoint call with the context data-fenced; `NOT_FOUND` from the model surfaces as a grounded refusal. Read-only end to end. |
| FR-109 | M | **Code-enforced citation contract.** Every answer must cite ≥1 `[[wikilink]]` and every citation must resolve to a *retrieved* source path; `ValidateOutput` rejects hallucinated or missing citations (chokepoint retry, then the failure surfaces as a refusal listing the retrieved sources). An unverifiable answer is treated as no answer. |
| FR-110 | S | **`axon ask` CLI + observability.** `axon ask "<question>" [--top-k N] [--json]`: answer + cited sources (refusals show the reason and retrieved-but-uncited sources); exit 0 for answers and grounded refusals; every run ledgered under operation `ask` and streamed to the dashboard. |
| FR-111 | S | **`vault_ask` MCP tool (roadmap 1.1 A2).** The `ask` engine exposed as an MCP tool in the default set (Claude Code + Desktop): composes `ask.Deps` from the MCP server's existing deps, returns the `ask.Answer` (answer/citations/sources/refused/reason/tokens), read-only toward the vault, chokepoint-governed spend. Excluded from ADR-022's fixed agentic allowlist, so no agentic automation can invoke it (pinned by test). |
| FR-112 | S | **Dashboard Ask panel + `POST /api/ask` (ADR-023).** A browser-triggered, chokepoint-governed token-spend endpoint guarded identically to review actions (loopback + Host guard + JSON content-type + `X-Axon-Ask: 1` preflight header), gated by `dashboard.ask_enabled` (pointer-default-ON; when off the endpoint 404s and the SPA hides the tab). Emits an `ask.*` event so the spend shows on the activity feed. The React Ask panel renders answer + cited sources, grounded refusals, and errors. Spec in `docs/superpowers/specs/2026-07-05-vault-ask-design.md`. |
| FR-113 | M | **Pluggable vector-index seam (ADR-025, roadmap 1.1 B1).** The hybrid-search vector leg is a `db.VectorIndex` interface with `BruteIndex` (exact full scan, the default) and `IVFIndex` implementations, selected by `retrieval.index: brute \| ann` (default `brute`; `search.Searcher.Configure` threads the choice from `RetrievalConfig`). No behaviour change when unset — existing vaults keep exact brute-force search. |
| FR-114 | M | **In-house IVF-flat approximate index.** `ann` mode clusters `vec_chunks` vectors with deterministic spherical k-means (k≈√N) into an in-file `vec_centroids` table + `vec_chunks.centroid` column, probing the `nprobe` nearest lists plus the always-scanned `centroid IS NULL` overflow. Auto-falls back to exact brute below `retrieval.ann.threshold` (default 10 000); identical to brute at `nprobe ≥ k`; overflow guarantees new vectors are never missed. Rebuilt by `axon reindex` (`core.RefreshVectorIndex`) and maintained best-effort by the reindex automation. In-file only — no server, no new dependency (guardrail holds). Spec in `docs/superpowers/specs/2026-07-05-ann-index-design.md`. |
| FR-115 | S | **`doctor` vector-index advice.** `axon doctor` suggests setting `retrieval.index: ann` (and running `axon reindex`) once the vector count exceeds `retrieval.ann.threshold`, and warns when `ann` is enabled but the index has not been built. Read-only and tolerant — a missing/unreadable DB never fails doctor. |
| FR-116 | S | **Standing research questions (roadmap 1.1 A3).** A weekly `research-questions` automation answers user-authored standing questions from the whole vault: one grounded `ask` per open question through the chokepoint (synthesis tier), rendering answer + `[[wikilink]]` citations + a derived confidence marker (`✅ Answered` / `📝 Tentative` / `🔍 Open`) into an `axon:answers` managed block. Change-gated on question-list hash ∨ new-sources-this-week; unanswered questions persist and are re-attempted; deferral-safe and idempotent (the block is rebuilt whole). Disabled by default. Spec in `docs/superpowers/specs/2026-07-05-research-questions-design.md`. |
| FR-117 | S | **Research-questions note contract & clean disable.** Questions are top-level list items ending in `?` in the human region of `03-Resources/Research Questions.md` (fenced examples and the `axon:answers` block are ignored on read); AXON never edits the human region (cardinal rule 2 — checkbox state untouched). Absent note or empty list → the feature is inactive (clean disable); `--dry-run` reports without writing; `axon init` scaffolds an inert template (examples fenced so they cost nothing until the user writes a real question). |
| FR-118 | S | **Memory contradiction detection (roadmap 1.1 C1).** The `memory-distill` automation detects, within its existing single synthesis call, when a newly-distilled durable fact contradicts an existing `axon:memory` entry. The current entries are supplied to the model numbered; a contradiction is returned as `CONFLICT <n>: <new statement>` and resolved back to the exact stored entry by number. No additional model call is made (cardinal rule 1). Spec in `docs/superpowers/specs/2026-07-05-memory-consolidation-design.md`. |
| FR-119 | S | **Reconciliation proposal & supersede-on-accept.** A detected contradiction is written to the review queue as a `reconcile` item carrying the new statement and the entry it supersedes; the new fact is **not** added to memory until accepted (no silent coexistence). Accepting supersedes the old entry with the new one; dismissing keeps the old and drops the new — every write wikilink-safe into the `axon:memory` managed block (cardinal rule 2, no delete). |
| FR-120 | S | **Tombstone audit trail & no re-nag.** Supersession tombstones the old entry (struck and dated `~~…~~ (superseded YYYY-MM-DD)`, never deleted) so memory history stays auditable. The same contradiction is proposed at most once (proposal memory, FR-102 helpers); if the old entry is gone at accept time the new fact is still added and the resolution reports it was not struck. |
| FR-131 | S | **Project pulse (roadmap 1.1 C3).** A weekly `project-pulse` automation reads the notes under `01-Projects/` and the stated `- goals:` from `02-Areas/Profile/USER.md`, and writes deterministic per-project facts — last-touched, active/`⚠ stale` (≥3 weeks untouched), and any linked goal — into an `axon:pulse` managed block in `01-Projects/Project Pulse.md` (created with a human preamble it never touches; cardinal rule 2). Change-gated on the ISO-week ∨ the project set / update-stamps / goals; disabled by default. Spec in `docs/superpowers/specs/2026-07-05-project-pulse-design.md`. |
| FR-132 | S | **Budget-degrading pulse narrative.** One routine-tier chokepoint call (local-routable, ADR-015) turns the facts plus bounded per-project excerpts (retrieve-don't-dump) into a 3–6 sentence pulse of progress / stalls / next actions, grounded strictly in the supplied text as data (NFR-05). Under budget denial the narrative degrades to a facts-only block (`_(pulse narrative skipped: budget)_`) — the pulse never fails on budget pressure. |
| FR-133 | S | **Stale-project nudges.** Each project untouched for ≥3 weeks appends one `- [ ]` line to `.axon/review-queue.md` (`pulse: [[project]] untouched N weeks — review or archive?`), at most once ever (proposal memory keyed by path — never re-nags weekly). `--dry-run` reports both the pulse and the nudges without writing. |

### Temporal memory *(roadmap 1.2 R1 — planned)*

FR-134…FR-137 are the **1.2** headline (`docs/15-roadmap-1.2.md`, Phase R),
tracing to ADR-028 and the spec in
`docs/superpowers/specs/2026-07-07-temporal-memory-design.md`. Priorities are
relative to this slice.

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-134 | M | **Interval-bearing fact grammar.** The `axon:memory` block carries machine-readable validity intervals: an open fact's `valid_from` is its leading date; a superseded fact is tombstoned as `- ~~…~~ (until DATE; superseded by "…")`, giving `valid_until` + a superseded-by pointer (quotes sanitized); `[kind]` extends to `fact | decision | lesson | preference`. The extension is backward-compatible — legacy entries and legacy `(superseded DATE)` tombstones parse correctly with **no Markdown migration**. `source` may be a `[[wikilink]]` or a plain token. |
| FR-135 | M | **Derived `memory_facts` index.** A derived SQLite table (`0005_memory_facts.sql`: text, kind, source, valid_from, valid_until, superseded_by, struck, embedding, line_no) is rebuilt from the `axon:memory` block during `axon reindex` as a read-only Markdown→DB pass that **never writes to the vault**; deleting the DB and reindexing reproduces it row-for-row (S9). Embeddings fill best-effort via the existing nomic embedder (nullable when Ollama is down). `axon doctor` reports fact count (open/superseded) and flags any unparseable block line. |
| FR-136 | M | **Interval-aware supersedence.** Accepting a `reconcile` proposal closes the superseded fact's interval — `identity.Reconcile` sets `valid_until` + superseded-by and tombstones in place (never deletes, cardinal rule 2) — and prepends the new open fact; if the old fact is gone it still prepends (`matched=false`). `memory-distill` runs consolidation at the **routine** tier through the chokepoint (was synthesis), promoting facts with `[fact]` + `valid_from` + a `[[source]]` wikilink; detection stays the model-driven whole-memory feed (C1). |
| FR-137 | S | **Valid-facts injection.** SessionStart memory injection prefers **currently-valid** facts — superseded/closed facts are excluded — selecting the newest open facts within the existing token ceiling, parsing the `axon:memory` block directly with **no DB dependency** (a hook must never fail); empty/legacy blocks behave exactly as before. |

### Local-model eval harness *(roadmap 1.2 R5.1 — planned)*

FR-140…FR-141 are the first of R5's three sub-slices (`docs/15-roadmap-1.2.md`,
§R5), tracing to ADR-029 and the spec in
`docs/superpowers/specs/2026-07-07-eval-harness-design.md`. Promotion-gating +
`doctor` status (R5.2) and the runtime cascade (R5.3) are separate slices.
Priorities are relative to this slice.

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-140 | M | **`axon eval` harness.** An `internal/eval` harness runs in-repo golden sets (`internal/eval/golden/<family>/*.yaml`, embedded; `classify` + `routine` families v1) against any `(provider, model)` pair. `axon eval [--model <ref>] [--family classify\|routine\|all] [--json] [--min-pass <pct>]` prints a per-family pass-rate scorecard. Every eval call — target model **and** judge — goes **through the token-manager chokepoint** (ledgered, cardinal rule 1) via a consumer-defined `Chokepoint` interface satisfied by `*tokens.Manager`; `eval` imports neither `agent` nor `automations`. Calls run **fail-fast** (`local_fallback: fail`) and inspect `resp.Model`, scoring each case **passed-local / escalated / failed** so a Claude fall-forward can never be miscounted as a local pass. `--min-pass` sets only the exit code (CI gate), not config state. No vault mutation. |
| FR-141 | M | **Hybrid grading.** `classify` cases grade deterministically (semantic JSON equality via `expect_json`, or normalized-text equality via `expect_text`). `routine` cases grade by a `must_include` anchor-fact gate **plus**, when a `rubric` is set, one Claude **LLM-as-judge** call through the chokepoint returning `{pass,reason}` (`ValidateOutput`-guarded); the case passes iff both hold. In CI the judge is `agent.Fake` (deterministic, free) so CI exercises harness plumbing; real grading runs locally against Ollama. Malformed fixtures/judge output fail loudly, never silently pass. |
| FR-142 | M | **Eval-gated promotion (runtime admission gate).** A DB-only `eval_runs` table records each `axon eval` outcome per `(family, model_ref, digest, pass_pct, ran_at)`; `axon eval` persists a row per family (default on; `--no-save` skips). The token-manager chokepoint gains an admission gate in `Authorize`: when a local `classify`/`routine` tier resolves and `models.eval_min_pass > 0`, it reads the latest `eval_runs` row for that `(family, ref)` and, if absent or below threshold, **retargets to the tier's Claude fallback** (`fallbackClaudeKey`), emits `token.unvetted_local`, and proceeds through the normal budget path — a pure indexed SQLite read, no Ollama call on the hot path. `models.eval_min_pass` (0–100) defaults to **0 (opt-in)**; `axon init` scaffolds `80`. The eval harness's own manager sets `PromotionGateOff` so `axon eval` always measures the real local model (chicken-and-egg guard). `eval_runs` is S9-exempt (machine-local measurements; `reindex` leaves it untouched). |
| FR-143 | M | **Vetting status + drift detection.** `doctor` reports each gated local tier as vetted / not-vetted / below-threshold / **drifted** (current Ollama digest ≠ stored `eval_runs.digest`) / ungated (local tier set but `eval_min_pass == 0`), fetching the live digest only in `doctor`. An optional `eval-drift` automation (routine tier, **default off** — S8) detects digest drift for gated local tiers and re-runs the harness through the chokepoint, refreshing `eval_runs`; all-off, drift is still surfaced by `doctor` and fixed by a manual `axon eval`. |
| FR-144 | M | **Per-call verification cascade (promoted routine tier).** In the chokepoint's local path (`runLocal`), a successful, schema-valid local `routine` answer is scored 0–10 by a cheap local judge (`models.verify: ollama:<model>`); below `models.verify_min_score` (0–10, default 6) the call **escalates to Claude** via `fallbackClaudeKey` through the normal budget path. The local answer, the local judge (ledgered `<op>:verify`, budget-exempt), and any Claude escalation are **all ledgered** (`token.verify_pass` / `token.verify_escalate` / `token.verify_escalate_failed` events). The judge uses a concrete ref (never itself gated/verified — no recursion) and **never** falls forward to Claude; escalation **degrades to the retained local answer** when the judge is inconclusive or Claude is budget-blocked (S8). Scope is `routine` only (synthesis is always Claude; classify is `ValidateOutput`-graded); `models.verify` defaults **off** (opt-in), validated to a non-empty local `ollama:<model>` ref. No new automation or MCP tool. |
| FR-145 | M | **Verify subsystem status.** `doctor` reports the verification cascade: off (silent) / **misconfigured** (`models.verify` set but the `routine` tier resolves to Claude, so it never triggers) / **model-not-pulled** (verify model absent from Ollama's tag list) / ready (`verify ollama:<m> ready, floor <n>/10`). Reachability best-effort, mirroring `rerankCheck`. |
| FR-146 | S | **Contradiction-aware ask (R2).** The grounded-ask synthesis prompt instructs the model, when retrieved sources **disagree**, to not silently choose or average: emit a leading `CONFLICT` sentinel line, then a cited answer that names both conflicting sources as `[[wikilinks]]` with their dates and **prefers the newest / currently-valid claim** while noting the superseded one. `ask` parses the sentinel into an additive `Answer.Conflicted bool` and strips it from `Text`; the grounding gate, `NOT_FOUND`, and the citation contract are unchanged (a `CONFLICT` reply with no resolvable wikilink still refuses as ungrounded). Rides on **R1**'s retrieved `valid_from`/tombstone dates for the validity signal; one chokepoint synthesis call, no extra tokens; non-conflicting corpora unaffected. No new ADR. |
| FR-147 | S | **Conflict signal surfaced.** The `vault_ask` MCP tool prepends a `⚠ Sources conflict` note to its rendered answer when `Conflicted`; the dashboard `/api/ask` response carries `conflicted`. Additive — existing ask consumers (A2 `vault_ask`, A3 `research-questions`, dashboard) are otherwise untouched. |
| FR-148 | S | **Ambient related-notes engine (R8).** `search.Searcher.Related(notePath, topK)` returns the notes most similar to a given note by pure vector math over the existing ANN `VectorIndex` seam (ADR-025) — **zero model calls**: the note's mean chunk vector is read from `vec_chunks`, matched through `db.HybridSearch` with a `QueryVector` (empty `Query`, so no embedding is computed), collapsed chunk→note (max cosine per note), the target itself excluded, floored, sorted, top-K. Unknown path → error; known-but-unembedded → empty. Auto-brute-fallback below `retrieval.ann.threshold` keeps small vaults exact. Read-only; no ledger entry (no spend). |
| FR-149 | S | **Related on the CLI + MCP.** `axon related <note-path>` (vault-relative path, `--top-k`, `--json`) and a `vault_related` MCP tool (`{path, top_k?}` → `{related:[{path, similarity}]}`) surface the engine. `vault_related` is in the default MCP set and — because it spends no tokens and only reads — is added to the agentic **read** allowlist (unlike `vault_ask`, which is excluded for spending). |
| FR-150 | S | **Related on the dashboard.** `GET /api/related?path=…` returns `{related:[…]}`, gated by `dashboard.related_enabled` (`*bool`, default-ON) + an `X-Axon-Related` header (CORS-preflight-forcing) on top of the loopback/Host guard (ADR-023); it is also the **documented loopback endpoint** an Obsidian sidebar plugin consumes. A **Related** SPA tab drives it (hidden when disabled). Zero model calls; `doctor` reports a `related` check. |
| FR-151 | S | **Resurfacing with review scheduling (R9).** The resurfacer upgrades from "propose a pair once, silence it forever" to a light FSRS-flavoured schedule. Per-pair `{rung, due, last}` state (interval ladder `resurfacing.intervals_weeks`, default `[1,2,4,8,16]`, leech-capped) is persisted as JSON in `automation_state` under `resurfacer:schedule` (derived operational state; a DB wipe degrades to base interval — S9-safe). Each run reads its own outcomes back out of the review queue **and** archive (`review.Outcomes`, applied once per resolution via the `last` anchor): **dismiss advances the rung +1, accept +2** (intervals lengthen more on acceptance). A candidate is surfaced only when it is not already pending and is either new or past its `due`. Zero model calls in the base path. |
| FR-152 | S | **Opt-in note-contradiction detection.** When the resurfacer has `budget_tokens > 0` and `resurfacing.contradiction_max_checks > 0` (default 3), the top-N most-similar candidate pairs each get **one routine-tier chokepoint call** asking whether the two notes make directly contradictory claims (note bodies passed as DATA, never instructions — NFR-05). A non-`NONE` reply reclassifies that pair from a plain `resurface` line to a distinct `contradicts` item. Budget defer → the model path is skipped silently; the default config (`model: none, budget_tokens: 0`) keeps the whole path dormant (S8). |
| FR-153 | S | **`contradicts` review kind + surfaces.** The review queue gains a `contradicts [[recent]] ⚡ [[dormant]] — <summary> (sim …)` grammar; `review.Load` parses it and `Accept` links the pair via the existing `axon:links` managed block (wikilink-safe; surfaces the tension in both notes). `review.Outcomes` reports resolved resurface+contradicts items across queue+archive for the scheduler. `doctor` reports a `resurface` check (interval ladder + whether the contradiction path is active). No new MCP tool or automation → no registry changes. |

### Near-duplicate merge proposals *(R7 — built 2026-07-10)*

| ID | Priority | Requirement |
|----|----------|-------------|
| FR-154 | M | **Near-duplicate merge sweep (R7).** A new `merge-proposals` automation (weekly, **zero model calls**, disabled by default in both profiles) reuses the resurfacer's vector primitives: it loads all scannable notes (`scannableNote` filter over `db.NotesUpdatedSince(…,"0001-01-01",…)`), fetches `db.NoteMeanVectors`, and computes **all-pairs `db.Cosine`**, proposing any pair with `sim ≥ merge.threshold` (default 0.92 — a far higher bar than resurfacing's 0.75) as a `merge [[a]] + [[b]] (sim …)` line in the review queue. Pairs already pending in the queue and pairs in dismissed-pair proposal memory (`merge-proposals/proposed`) are suppressed; output is similarity-sorted and capped at `merge.max_proposals` (default 5). Dry-run computes and reports, writes nothing. |
| FR-155 | M | **Wikilink-safe merge accept (`vault.Merge`, ADR-032).** A `merge` review kind resolves through a new `vault.Merge(ctx, a, b) (survivor, err)` — the closest thing AXON has to a destructive op, defined so **nothing is ever deleted**. The **survivor** is the more inbound-linked of the pair (ties → most-recently-modified → lexical); it keeps its prose and gains the loser's body in its additive `axon:merged` managed block (loser managed-block markers neutralized so they cannot corrupt the survivor's block). Every inbound `[[loser]]` link/embed is retargeted to the survivor (survivor included → its own reference becomes a harmless self-link, never a dangling link into `.trash/`). The loser is copied **archive-first** to `.trash/merged/<base>.md` (intact, recoverable, out of the index) before the original is removed — a crash mid-op duplicates content, never loses it. `Accept` marks the line `✓ merged into [[survivor]]`. Zero model calls; no MCP tool and no agent-driven merge (user-approved via the review queue only). Result: **zero broken links, both originals recoverable.** |
| FR-156 | S | **Merge config + doctor + default-off.** A validated `merge{threshold (0<t≤1, default 0.92), max_proposals (≥0, default 5)}` config block (`MergeThresholdOr`/`MergeMaxProposalsOr`); `merge-proposals` ships disabled in both profile templates (S8); `doctor` reports an advisory `merge` check (on/off + active threshold/cap). No new DB table — detection reads derived vectors only (S9). |
| FR-157 | M | **Action grammar & parser (1.2.5 T1, ADR-033).** A pure leaf package `internal/actions` parses vault checkbox lines in the **Obsidian Tasks emoji grammar** — the single structured task parser in AXON (replacing three ad-hoc `strings.Count("- [ ]")` sites). `Parse` is tolerant: `[ ]`→open, `[x]`/`[X]`→done, `[-]`→cancelled, any other marker→open, never errors; it extracts `📅`/`⏳`/`🛫`/`✅` dates, `🔺⏫🔼🔽⏬` priority, `@context`/`#tag` tokens and the first `[[project]]` link. `Extract` walks a whole note tracking the nearest heading as section and skipping fenced code + `axon:actions` projection blocks. Identity is `sha256(source_path + normalized checkbox-stripped line)` — state-independent, content-sensitive. The GTD **bucket is computed at read time** (`done>cancelled>someday>waiting>overdue>today>scheduled>next`). Zero model calls. |
| FR-158 | M | **Derived action index (1.2.5 T1, ADR-033).** A disposable `actions` SQLite table (migration `0007`) rebuilt inside the reindex transaction from the checkbox lines of every note (delete-all+insert, the `memory_facts` pattern), so `axon reindex` reconstructs it byte-equivalently from Markdown (S9) and no task truth lives only in the DB. Rows carry source path, section, raw line, state, checkbox char, dates, priority, project, `@contexts`/`#tags` (JSON), and an `archived` flag (`04-Archive/` indexed but excluded from open views). `db.ReplaceActions`/`ListActions`/`ActionStateCounts`. Read-only over the vault; no embeddings. |
| FR-159 | S | **`axon actions` CLI (1.2.5 T1).** A read-only command listing/filtering the index — `--status` (a bucket, or the `open`/`week` aggregates), `--project`, `--context`, `--all` (include done/cancelled/archived), `--json` (carries the computed `bucket`). Prints a GTD counts header (open/overdue/today/waiting/someday/done-this-week) then the bucket-ordered list. An advisory `doctor` `actions` check reports the index size. Zero model calls, no vault mutation. |
| FR-160 | S | **Actions consolidation (1.2.5 T2).** A zero-model `actions-consolidate` automation (daily, **enabled by default**) renders the whole action index into the `axon:actions` managed block of `01-Projects/Actions.md` in GTD engage order — 🔴 Overdue · 📅 Today · ⏳ This week (rolling 7 days) · ▶ Next actions (grouped by project then context) · 🕓 Waiting for · 💭 Someday/Maybe · ✅ Done this week — as plain `[[source]]` references, **never duplicate checkboxes** (constitution §3). Change-gated on the rendered projection hash (an unchanged day writes nothing); wikilink-safe `Patch` into the managed block (human preamble untouched); DryRun-safe. Composes the `project-pulse` pattern; no new ADR. |
| FR-161 | S | **Heartbeat task counter (1.2.5 T2).** The essential `heartbeat` gains a deterministic `tasks: N open (M overdue)` count sourced from the index (`db.ListActions` + `actions.Bucket`) — the first non-CLI consumer of "one grammar, one truth". Best-effort (a DB hiccup degrades to no counter — the essential heartbeat never fails); an overdue task also arms the optional synthesis gate. `daily-log`'s task-rolling stays model-driven (it never parsed tasks; `Actions.md` is now the durable roll surface). |
| FR-162 | M | **Dashboard actions read endpoint (1.2.5 T3).** `GET /api/actions` (guarded by loopback+Host + `X-Axon-Actions` header + the `dashboard.actions_enabled` kill-switch → 404 when off, 403 header-less) returns the non-archived action list (each row tagged with its read-time GTD bucket), a counts summary (open/overdue/today/waiting/someday/done-this-week), and a 30-day completion trend (from `done_date`, no new storage) — all derived from the T1 `actions` table, matching `axon actions`. Zero model calls. |
| FR-163 | M | **Task completion mutation (1.2.5 T3, ADR-034).** `POST /api/actions/complete` `{path, hash}` → `vault.CompleteAction`, the one write in the theme: a byte-precise, hash-addressed toggle of the single **open** checkbox line whose T1 identity hash matches — `- [ ]`→`- [x]` + append `✅ YYYY-MM-DD`, atomic (temp+rename), human prose otherwise untouched. Stale/unknown hash → **409** via the exported `vault.ErrActionNotFound` sentinel (nothing written). The derived row is surgically marked done (`db.MarkActionDone`) so the list refreshes; `01-Projects/Actions.md` re-renders on the automation's next run. User-initiated via the loopback dashboard only; **never agent/model-driven** (T4's `action_complete` MCP tool is excluded from the agentic allowlists); never a delete. |
| FR-164 | S | **Actions kill-switch, health, SSE, SPA tab (1.2.5 T3).** `dashboard.actions_enabled` (`*bool`, pointer-default-ON) gates both endpoints + the tab; `/health` carries `actions_enabled` so the SPA hides the tab when off; a new `action.done` SSE event streams on completion (consolidation runs surface via the existing `automation.run`); an **Actions** React tab renders stat tiles (open/overdue/today/done-this-week), a completion-trend `AreaChart`, and the filterable open list with per-row **done** buttons + source context. |
| FR-165 | S | **MCP action tools (1.2.5 T4).** Two MCP tools over the T1 index: `actions_list` (read-only, **zero-spend**, in the default set **and** the agentic **read** allowlist — the `vault_related` precedent) returns open actions grouped by GTD bucket + counts, each row carrying its identity hash; `action_complete` (a vault mutation via `vault.CompleteAction`, in the default set but pinned out of **both** agentic allowlists — the `vault_ask` precedent, ADR-034) marks a task done from an **interactive** session (`{path, hash}` → `[ ]`→`[x]` + `✅ date`; stale hash → `ErrActionNotFound`; `DryRun`-aware). Structurally excluded from every agentic subprocess (validate-time reject + registration-time filter). |
| FR-166 | S | **SessionStart open-actions pointer (1.2.5 T4).** The SessionStart injection gains one deterministic line — `- Actions: N open (M due today, K overdue) → [[01-Projects/Actions.md]]` — computed from the `actions` table (`db.ListActions` + `actions.BucketFields`), shown only when open actions exist. **No model call**; best-effort (any error → no line, never a broken hook). Operational status like the FR-89 briefing pointer — **ungated by `memory.inject`** (surfaces on the work profile too). |
| FR-167 | S | **Stale-action sweep (1.2.5 T5).** A zero-model `actions-review` automation (weekly, **off by default**) proposes stale open actions to the review queue as a new `stalled` kind — `stalled action "…" in [[note]] (Nd) — still relevant?`. "Stale" = an open, **undated** action whose source note's `updated` predates `today − actions.stale_after_days` (default 30, via `db.NotesUpdatedBefore`). Deduped by proposal memory (propose once; a dismissal silences it), capped per run, skips already-pending items. No model call. |
| FR-168 | S | **Someday demotion (1.2.5 T5, ADR-034 ext).** Accepting a `stalled` item demotes the task to Someday/Maybe: `vault.TagAction` appends `#someday` to the source checkbox line (additive, byte-precise, atomic; text-addressed; idempotent; `ErrActionNotFound` if the line changed). It then moves to the Someday bucket everywhere (T2 note, dashboard, `axon actions`). Applied **only** by a human review-queue Accept — never auto-completes, never deletes, never an agent/MCP path. |
| FR-169 | S | **Implicit action extraction (1.2.5 T6).** A routine-tier `action-extract` automation (**off by default**, local-routable per ADR-015, through the chokepoint, change-gated on notes updated in a 7-day lookback, capped per run) makes one structured call per recently-changed note asking for the author's explicit commitments (NFR-05: the note is delimiter-neutralized data, never instructions). Findings become review-queue lines of a new `action` kind — `action "…" from [[note]]`. Deduped by proposal memory; degrades to no-op on budget defer. **The only 1.2.5 token spender.** |
| FR-170 | S | **Extracted-action accept (1.2.5 T6).** Accepting an `action` item appends a real checkbox `- [ ] …` into an `axon:tasks` managed block in the **source note** via `vault.AppendToBlock` (reuses `extractManagedBlock` + `Patch`; human prose preserved). T1 indexes `axon:tasks` (it skips only the `axon:actions` projection), so the accepted task becomes a real, completable action flowing into consolidation, the dashboard, and `axon actions`. Dismiss silences (proposal memory). |

### Session memory *(built 2026-07-04)*

FR-97…FR-99 trace to ADR-021 and the spec in
`docs/superpowers/specs/2026-07-04-session-memory-design.md`. Priorities are
relative to this slice.

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-97 | M | **Session recorder.** The Stop hook, when `memory.capture_sessions` is enabled, records `{session_id → transcript_path, last_stop}` into `automation_state` — deterministic, no model call, paths only (never transcript content), every failure silent so a session is never broken. |
| FR-98 | M | **Session distiller.** A `session-distill` automation drains sessions idle ≥ 30 minutes: one classify-tier chokepoint call per session (redacted, fenced, tail-capped transcript text) extracting up to 3 decision/lesson/preference items or NONE; entries written via `identity.Remember` with `source: session`; each session tried once ever; budget defer leaves the remainder pending. |
| FR-99 | M | **Privacy controls.** `memory.capture_sessions` (pointer-default-ON, mirroring `memory.inject`) gates the recorder; redaction rules apply before the model sees transcript text and before entries are written; only vault sessions are captured; NFR-14 applies to the whole path. |
| FR-104 | S | **SessionEnd capture.** The generated hook settings also wire `SessionEnd`, which records the session like Stop (FR-97 gates and silence unchanged) with a sticky `ended` flag; the distiller treats `ended` sessions as immediately ready, keeping the ≥30-minute idle threshold as the fallback for crashed/abandoned sessions. Old state rows without the flag keep working. Spec in `docs/superpowers/specs/2026-07-04-adr-followups-design.md`. |

### Review actions *(built)*

FR-94…FR-96 are **implemented** (ADR-020; spec in
`docs/superpowers/specs/2026-07-04-review-actions-design.md`): the
`internal/review` package, the dashboard Review tab + `/api/review` +
`/api/export`, structured triage proposals, and `vault.RewriteSystemFile`.
The same slice implemented **FR-64** (chart export), the last unbuilt v1
requirement. Priorities are relative to this slice.

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-94 | M | **Review API + tab.** The dashboard parses `.axon/review-queue.md` into typed items (`GET /api/review`) and resolves them (`POST /api/review/action`, accept/dismiss). Mutation POSTs require JSON content type + an `X-Axon-Review` header (CORS-preflight-forcing) atop the loopback bind and Host-guard; every action emits a `review.accept`/`review.dismiss` event. |
| FR-95 | M | **Wikilink-safe accepts.** Link/pair/resurface accepts append to the target note's `axon:links` managed block (prose never touched); triage accepts — now structured JSON proposals validated at the chokepoint — perform the wikilink-safe `vault.Move`; queue lines are resolved via `vault.RewriteSystemFile`, which refuses any path outside `.axon/`. |
| FR-96 | S | **Chart export (delivers FR-64).** `GET /api/export?dataset=…&format=csv\|json` serializes every chart dataset with per-card download links in the SPA. |
| FR-103 | S | **Review-queue compaction.** When a resolution rewrites `.axon/review-queue.md`, resolved lines older than 7 days move to `.axon/review-queue-archive.md` (grouped under their original section headers, stamped), and section headers left with no items are dropped. Archive-append precedes the queue rewrite (a crash duplicates an archive line at worst, never loses one); pending lines are never touched; a resolved line with an unparseable date is kept; both files stay behind the `.axon/` guard. Spec in `docs/superpowers/specs/2026-07-04-adr-followups-design.md`. |

### Subscriptions *(built)*

FR-91…FR-93 are **implemented** (ADR-019; spec in
`docs/superpowers/specs/2026-07-04-subscriptions-design.md`): the
`subscriptions` automation (`internal/automations/subscriptions.go`, gofeed
parsing), subscribe-from-now, per-tick caps, seen-state, and the shared
`enrichedPipeline` enrichment toggle. Priorities are relative to this slice.

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-91 | M | **Scheduled feed polling.** A `subscriptions` automation polls config-declared feeds (`subscriptions.feeds`) through the egress-policied, SSRF-guarded fetcher, parses RSS/Atom/JSON Feed (gofeed), and ingests new items through the standard pipeline (dedupe, redaction, events, ledger). No feeds configured → free skip. |
| FR-92 | M | **Volume control.** Subscribe-from-now (first tick marks current entries seen, ingests nothing); per-feed `max_per_tick` cap (default 5); seen-state persisted in `automation_state` (capped at 500 URLs/feed); each item attempted exactly once (mark-seen-after-attempt, failures surfaced); a feed-level failure never aborts other feeds. |
| FR-93 | S | **Subscription enrichment toggle.** `subscriptions.enrich: heuristic \| claude` (default `heuristic`, zero tokens); `claude` routes item enrichment through the token-manager chokepoint on the `routine` tier (ADR-015 local routing applies). |
| FR-100 | S | **Subscribe CLI.** `axon subscribe <url>` verifies the feed (egress-policied fetch + gofeed parse; `--no-verify` skips), checks `ingestion.CheckIngestPolicy` (refusal is explicit; `--allow` appends the host to `ingest_domains_allow`), and appends to `subscriptions.feeds` via the comment-preserving config editor with re-validation and atomic write. `subscribe list` shows feeds + seen-state; `subscribe remove <url>` deletes the feed and its seen entry (re-subscribe re-baselines). No model calls; spec in `docs/superpowers/specs/2026-07-04-subscribe-cli-design.md`. |
| FR-101 | S | **Conditional feed polling.** The subscriptions poll stores each feed's `ETag`/`Last-Modified` and sends `If-None-Match`/`If-Modified-Since` on subsequent polls (RFC 9110 §13); a 304 is a free skip — no body, no parse, no seen-state change, counted as "unchanged (304)" in the run summary. Validators live in `automation_state` (`subscriptions:http`), pruned to configured feeds; dry-run persists nothing; fetchers without conditional support fall back to plain GETs. Spec in `docs/superpowers/specs/2026-07-04-feed-conditional-get-design.md`. |

### Proactive layer *(built)*

FR-88…FR-90 are **implemented** (ADR-018; spec in
`docs/superpowers/specs/2026-07-04-proactive-layer-design.md`): the
`briefing` and `resurfacer` automations (`internal/automations/proactive.go`),
the SessionStart pointer, and the shared `db.NoteMeanVectors`/`db.Cosine`
similarity primitives (also backing the dashboard graph). Priorities are
relative to this slice.

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-88 | M | **Daily briefing.** A `briefing` automation writes an `axon:briefing` managed block into `Daily/<date>.md` at most once per day: deterministic facts (notes changed via `db.NotesUpdatedSince`, new sources, automation activity, review-queue pending, budget state) always; a short narrative via **one one-shot `routine`-tier chokepoint call** (local-routable per ADR-015, capped by `budget_tokens`), degrading to facts-only on budget defer. Dry-run writes nothing. |
| FR-89 | S | **SessionStart briefing pointer.** When today's daily note contains an `axon:briefing` block, the SessionStart hook injects a single pointer line (`- Briefing: Daily/<date>.md (axon:briefing)`). Deterministic, no model call, silent on any error. |
| FR-90 | M | **Resurfacer.** A weekly no-model automation proposes connections between recently-touched notes (≤7 days) and dormant notes (≥90 days) by mean-chunk-vector cosine (≥0.75, shared primitives with the dashboard graph), excluding already-linked pairs and previously-proposed pairs (persistent proposal memory in `automation_state`), appending at most 5 proposals per run to `.axon/review-queue.md`. |
| FR-102 | S | **Link-suggester proposal memory.** The link-suggester persists proposed pairs (unordered, canonical `pairKey`) in `automation_state` (`link-suggester:proposed`, capped at 500) via the same shared helpers as the resurfacer: pairs already proposed are never re-queued, memory is saved only after a successful queue append, dry-run persists nothing, and a memory load failure degrades to "may propose twice". Spec in `docs/superpowers/specs/2026-07-04-adr-followups-design.md`. |

### Agentic automations *(built)*

FR-84…FR-87 are **implemented** (ADR-017; spec in
`docs/superpowers/specs/2026-07-03-agentic-automations-design.md`): the
agentic adapter path (stream-json + kill-switch), the `axon mcp --tools`
server-side filter, chokepoint tool semantics with real-usage failure
ledgering, the runtime activation of `automations.<name>.budget_tokens`, and
the agentic knowledge-digest + compaction with `agentic: false` fallbacks.
Priorities are relative to this slice.

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-84 | M | **Agentic runner.** An automation may run Claude headlessly with AXON MCP tools: stream-json output, `--max-turns` cap, `--tools ""` (no built-in tools), `--strict-mcp-config` + inline `--mcp-config` launching `axon mcp`, explicit per-call `--allowedTools`. Claude provider only — the chokepoint rejects a tools call that resolves to a local provider. Every run enters through `tokens.Manager.Run` (cardinal rule 1). |
| FR-85 | M | **Per-turn budget enforcement.** The adapter accumulates real usage from per-turn stream events and kills the run when `budget_tokens` is exceeded; accumulated **real** usage is ledgered on every path (completion, turn-limit, kill), with a `token.run_budget_kill` event on kill-switch trips. `automations.<name>.budget_tokens` is enforced at runtime (per-run total for agentic calls; pre-flight input cap for one-shot calls). |
| FR-86 | M | **Dual tool allowlisting.** Agentic tool access is read-only in v1 (`vault_search`, `vault_read`, `vault_links`, `knowledge_search`, `tokens_status`) and enforced client-side (`--allowedTools`) **and** server-side (`axon mcp --tools <csv>` registers only the named tools). Model-calling and network tools are never allowlisted. |
| FR-87 | S | **Agentic knowledge-digest + compaction.** Both run agentically by default (digest: search/read the week's sources, grounded wikilinks; compaction: read backlinks before distilling), keep their one-shot prompts as the `automations.<name>.agentic: false` fallback and the degradation path, and write results through the same deterministic wikilink-safe Go code as today. Dry-run remains Authorize-only. |
| FR-105 | S | **Agentic write tools (ADR-022).** An automation's agentic allowlist may include the managed-block-safe write tools — `vault_patch` (`axon:*` managed blocks only), `vault_write` (create; refuses to clobber prose), `daily_append`, `memory_remember`; `vault_move` is excluded (restructuring stays human-approved via the review queue). Enforced by the existing dual allowlist unchanged: server-side `axon mcp --tools <csv>` (the subprocess physically lacks unlisted tools) plus client-side `--allowedTools`. A budget kill leaves a prefix of completed, per-tool-atomic, idempotent writes — never a half-edited note; a re-run converges (no journal/rollback). Spec in `docs/superpowers/specs/2026-07-04-agentic-write-tools-design.md`. |
| FR-106 | S | **Report-only dry-run for write-capable agentic runs (ADR-022).** `axon run <automation> --dry-run` threads a `Deps.DryRun` through the adapter as `axon mcp --tools <csv> --dry-run`; each write method validates and computes its change, returns `{would: …, applied: false}`, and never calls the vault mutation — suppression is server-side, not model-trusted. Such a dry-run spawns the model and spends tokens (a real preview requires a real run), fully ledgered and chokepoint-governed, and says so in the run summary. Supersedes FR-87's "dry-run remains Authorize-only" for automations that request write tools. |
| FR-107 | S | **Compaction agentic-writes (demonstrator).** On its agentic path, compaction writes its distilled summary via `vault_patch` into the target note's `axon:summary` managed block instead of returning text for deterministic Go to write; its `agentic: false` one-shot + deterministic-Go write stays the fallback and degradation target byte-for-byte, so a fresh clone with agentic off is unchanged (S8). |

### Local model routing *(built)*

FR-77…FR-80 are **implemented** (ADR-015; spec in
`docs/superpowers/specs/2026-07-03-local-model-routing-design.md`): the
`agent` Router + Ollama/Apple adapters, the tokens-chokepoint provider
routing with budget exemption and the fallback ladder, the provider-aware
`axon configure models` flow, and doctor/init convergence. Priorities are
relative to this slice, not to v1.

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-77 | M | **Per-tier local provider routing.** The `models.classify` and `models.routine` config fields accept provider-prefixed strings — `ollama:<model>` (Ollama chat) or `apple` (Apple Foundation Models on-device, `classify` only) — resolved by the token manager's adapter router. `models.synthesis` always names a Claude model (validated). Every local call passes through the token-manager chokepoint and is recorded in `token_ledger` with the provider-identifying model string. |
| FR-78 | M | **Budget exemption with full observability.** Local calls never consume the day/week token windows, are never deferred/denied/downgraded, and contribute nothing to budget-guard pressure — budgets continue to mean Claude quota. They are nonetheless fully ledgered (`cost_usd` null), emit the standard events, and appear on the dashboard. |
| FR-79 | M | **Deterministic fallback.** `models.local_fallback: claude \| fail` (default `claude`). On local transport failure, schema-invalid output (after one local retry), or an input exceeding the Apple context cap: fall forward to Claude through the normal budget-checked path, or fail the run visibly with the standard `:failed` ledger row and event. |
| FR-80 | S | **Apple Foundation Models adapter.** darwin/arm64, macOS 26+, Apple Intelligence enabled — availability doctor-checked via a `--check-availability` helper probe. Delivered with the ADR-013 compiled-at-init Swift helper pattern; uses guided generation when the call supplies an output schema. Configurable on the `classify` tier only. |

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
