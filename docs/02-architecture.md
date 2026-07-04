# 02 — Architecture

## 1. System at a glance

```mermaid
flowchart TB
    subgraph Human["Human"]
        OBS["Obsidian (editor + native graph)"]
        DASH["Dashboard (browser)"]
        CC["Claude Code (interactive, in vault)"]
    end

    subgraph Vault["Vault (source of truth — Markdown on disk)"]
        PARA["00-Inbox / 01-Projects / 02-Areas / 03-Resources / 04-Archive"]
        DAILY["Daily/ + MOCs/ + Templates/"]
        AXDIR[".axon/ (logs, exports, snapshots, dashboards)"]
        CLDIR[".claude/ (CLAUDE.md, skills/, agents/, hooks, .mcp.json)"]
    end

    subgraph Daemon["axon daemon (Go — single static binary)"]
        SCHED["Scheduler (robfig/cron)"]
        AUTO["Automation runners"]
        INGEST["Knowledge ingestion pipeline"]
        TOKENS["Context & token manager"]
        MCP["AXON MCP server"]
        API["Dashboard API + SSE"]
        DB[("SQLite + FTS5 + vectors (ADR-010)\n(one file per profile)")]
    end

    subgraph External["External"]
        CLAUDE["Claude Code (claude -p + interactive)\n— subscription / enterprise login —\n(optional: Claude API in api_key mode)"]
        OLLAMA["Ollama (local embeddings)"]
        WEB["URLs / articles / PDFs"]
    end

    OBS <--> Vault
    CC <--> MCP
    CC <-->|hooks/skills/CLAUDE.md| CLDIR
    DASH <--> API
    SCHED --> AUTO
    AUTO -->|claude -p headless| CLAUDE
    AUTO --> Vault
    AUTO --> DB
    INGEST --> WEB
    INGEST --> OLLAMA
    INGEST --> Vault
    INGEST --> DB
    MCP --> DB
    MCP --> Vault
    MCP --> OLLAMA
    TOKENS --> CLAUDE
    TOKENS --> DB
    AUTO --> TOKENS
    MCP --> TOKENS
    API --> DB
```

The **vault** is durable memory. The **daemon** is the runtime around it. **Claude Code** is the brain, reached two ways: interactively (via the MCP server + the installed plugin + hooks + `CLAUDE.md`) and on a schedule (headless `claude -p`). Both authenticate with your Claude **subscription** (personal: Max) or **enterprise** login (work) — there is no API key in the default modes (see ADR-009). The **dashboard** observes everything. The only required external services are **Claude Code** (which talks to Claude) and **Ollama**.

## 2. Module boundaries (single Go module)

One Go module (`module github.com/<you>/axon`), one binary, clear package seams. The backend, CLI, MCP server and automations are all Go; the **only** JavaScript is the dashboard SPA under `web/` (Vite + React + Recharts), whose built assets are embedded into the binary via `embed.FS` so distribution stays a single self-contained file.

```
axon/
  cmd/
    axon/        # main package — wires the CLI (init, start, stop, status, doctor, ingest, search, reindex, run,
                 #  export, config get/set/validate, mcp [install], hook, service, onboard, profiles, automations,
                 #  health, version) and composes the daemon (start_cmd.go: scheduler + dashboard + pidfile)
  internal/      # all application packages (private — not importable outside the module)
    config/      # types, schema (struct tags + validator), paths, profile resolution, secrets, content hashing
    core/        # cross-cutting operations: init (provisioning), doctor, reindex, reembed
    mcp/         # AXON MCP server (stdio): vault + knowledge + token tools
    dashboard/   # dashboard HTTP + SSE server (Go) that serves the SPA and streams events
    ...          # (db, vault, ingestion, embeddings, agent, tokens, scheduler, automations, events,
                 #  hooks, identity, clients, claudeassets, scaffold, search, service, health, ui — see below)
  web/           # dashboard SPA — Vite + React + Recharts; built to web/dist and embedded via embed.FS
  scripts/       # preflight + install/update/uninstall for macOS (launchd) & Linux (systemd), + _common.sh: build, install, service/Ollama wiring
  axon.config.example.yaml
  .env.example
```

Daemon orchestration (signal handling, pidfile, scheduler + dashboard + event
persistence wiring) lives in `cmd/axon/start_cmd.go` — `cmd` composes, packages
provide. The Claude Code plugin assets (CLAUDE.md template, skills, agents,
hooks wiring) and the vault scaffolding are embedded Go assets under
`internal/claudeassets/assets/` and `internal/scaffold/assets/`, written into
the vault by `axon init`.

**Dependency rule:** `internal/config` ← everyone. `internal/db`, `vault`, `embeddings`, `agent`, `events` are leaf packages; `tokens` is the only importer of `agent` (the chokepoint); `core` and `automations` compose the leaves; `mcp` composes its tools from the service layer (vault, db, search, tokens, ingestion, automations, identity); `dashboard` reads the db read-layer, event bus and token status. Nothing imports `cmd`. Keep the graph acyclic — Go enforces this at compile time, so a cycle is a design smell to fix, not silence.

### Core internal layout (`internal/`)

```
config/        # load + validate + resolve profile, secrets, policy types
db/            # modernc.org/sqlite: migrations, repositories (tokens, runs, sources, chunks, events, links), FTS5 + vector search
vault/         # markdown read/write, frontmatter, managed blocks, wikilink-safe ops
ingestion/     # fetch (egress-policied), extract, redact, chunk, enrich, persist (see Component 05)
embeddings/    # provider interface + Ollama impl + Apple on-device impl (ADR-013)
agent/         # Claude Code adapter (subprocess: `claude -p`); direct-API adapter (anthropic-sdk-go) for auth_mode: api_key
tokens/        # the chokepoint: estimates, budgets, ledger, redaction (see Component 07)
scheduler/     # robfig/cron/v3 wrapper: jitter, panic-safety, catch-up policy
automations/   # the engine + the standard automation set (see Component 06)
events/        # in-process event bus -> SSE + events table; structured logger (log/slog)
hooks/         # Claude Code hook logic (invoked via `axon hook <event>`)
identity/      # personal memory layer: USER/SOUL/MEMORY + onboarding (Component 12)
clients/       # Claude Desktop config merge (Component 13)
claudeassets/  # embedded .claude/ wiring assets (CLAUDE.md template, skills, agents, hooks)
scaffold/      # embedded vault scaffolding (folder READMEs, templates, Dataview dashboards)
search/        # hybrid search facade over db + embeddings
service/       # launchd/systemd unit generation for `axon service`
health/        # vault health scoring for `axon health`
ui/            # terminal styling for CLI output
```

## 3. Key data flows

### 3.1 Knowledge ingestion (Component 05)
`URL/PDF → fetch → extract main content → clean to Markdown → LLM enrich (title/summary/tags/links) under token budget → write note to 03-Resources/Knowledge → chunk → embed (Ollama) → upsert vectors (float32 BLOBs) + FTS5 → emit event`. Idempotent on content hash; re-ingest updates in place.

### 3.2 Scheduled automation (Component 06)
`Scheduler fires → runner acquires per-automation lock → change-gate (content hashes) → if no new material, log skip & exit → else build minimal context via retrieval → pre-flight token estimate vs budget → choose model → run (`claude -p`, or the direct-API adapter in `api_key` mode) → apply wikilink-safe vault writes (dry-run aware) → record run + tokens → emit event`.

### 3.3 Interactive session (Component 08)
`Claude Code starts in vault → SessionStart hook injects compact vault status (budget, inbox count, recent changes) → user works → MCP tools serve search/read/write/ingest/token-status → PostToolUse hook logs tokens of any AXON tool round-trips & flags budget → PreToolUse hook blocks unsafe file ops (enforces wikilink-safe path) → Stop hook suggests compaction if context large`.

### 3.4 Token accounting (Component 07)
Every path that calls Claude goes through the `agent` adapter, which (a) takes a pre-flight token estimate (exact `count_tokens` only in `api_key` mode), (b) consults the budget, (c) records reported `usage` post-hoc to the `token_ledger`, (d) emits an event the dashboard streams live.

## 4. Process & runtime model

- A single **long-running daemon** per profile (`axon start`) hosts the scheduler, ingestion workers, token manager, dashboard API/SSE and (optionally) the MCP server over a local socket. The MCP server is *also* runnable standalone via stdio for Claude Code to spawn (`axon mcp`) — Claude Code launches it per the generated `.mcp.json`.
- Concurrency: a small worker pool for embeddings (respect Ollama cold start + batch size 32); per-automation advisory locks so two runs never collide; ingestion queued.
- Persistence of daemon state is in SQLite; restart-safe. Missed schedules follow a configurable **catch-up policy** (`skip` | `run-once`).
- Crash safety: all vault writes are atomic (write temp + rename) and, for multi-file edits, staged then committed; a failed automation never leaves a half-edited note.

## 5. Security & policy model

- **Secrets** in `.env` or OS keychain, never in `config.yaml`, never committed, never logged, never sent to the model.
- **Egress allowlist** per profile: ingestion may only fetch from allowed domains (work profile defaults restrictive). The Claude API and Ollama hosts are always allowed.
- **Redaction** rules (regex/denylist) scrub matched content (secrets, client names) before anything leaves the machine for the Claude API.
- **Destructive-op protection:** delete/move/overwrite go through wikilink-safe ops with dry-run + confirmation; hard delete is never automated.
- **Prompt-injection posture:** content fetched from the web or read from files is *data, not instructions*. Ingestion never executes instructions found in fetched content; the enrichment prompt treats fetched text as quoted material.

## 6. Profiles & reproducibility

A profile is the unit of isolation. Resolution order for any setting: CLI flag → env (`AXON_*`) → `profiles/<name>` overlay → base `config.yaml` → built-in default. Each profile has its own data dir (`$AXON_HOME/profiles/<name>/`: `db.sqlite`, `logs/`, `exports/`, `snapshots/`), its own secrets, its own `CLAUDE_CONFIG_DIR`/API key, its own policy block and automation set. Nothing is shared. See Components 04 and 10.

---

## Architecture Decision Records

### ADR-001 — What "local-first" means here
**Decision:** All *data and infrastructure* are local (vault, SQLite, embeddings via Ollama, scheduler, dashboard). The *LLM* is reached through Claude Code (subscription/enterprise login) — or, optionally, the Claude API (`auth_mode: api_key`) — and is not localised in v1. The `agent` and `embeddings` modules are interfaces, leaving a seam for a local model later.
**Why:** Localising the frontier model is out of scope and would gut capability; localising *data* delivers the privacy, cost and offline-resilience benefits that motivate "local-first" for a second brain. Stating this prevents scope confusion.

### ADR-002 — One SQLite file (relational + vector + lexical)
**Decision:** SQLite with `sqlite-vec` (vectors) and FTS5 (lexical) in a single file per profile. *(Library choice amended by ADR-010 below: implemented on `modernc.org/sqlite` with brute-force cosine over float32 BLOBs; the single-file, pure-Go intent stands.)*
**Why:** Single-process, cross-platform, zero extra infrastructure; metadata, vectors and full-text live together so hybrid retrieval and budget queries need no cross-store joins. In Go this is reached via `ncruces/go-sqlite3` + `asg017/sqlite-vec-go-bindings/ncruces` (pure-Go WASM, no cgo — best fit for the single-binary goal) or `mattn/go-sqlite3` + the cgo bindings (faster, needs a C toolchain); the `db` package hides which. Alternatives (LanceDB, libSQL, server stores) are documented but rejected for v1 simplicity. Revisit only if vector count or latency targets break (see Component 05 §scale).

### ADR-003 — Go for the daemon
**Decision:** Go 1.22+, a single Go module, compiled to one self-contained static binary. CLI via `spf13/cobra`; config via `goccy/go-yaml` (or `gopkg.in/yaml.v3`) + `go-playground/validator`; scheduling via `gocron` (or `robfig/cron/v3`); HTTP/SSE via the standard library (`net/http`, Go 1.22 method-aware routing); the dashboard is a Vite + React + Recharts SPA under `web/`, built and embedded with `embed.FS` (the only JS in the repo).
**Why:** A single static binary with no runtime to install is the cleanest possible "clone, set values, one command" story, and Go cross-compiles trivially to the different machines and OSes the multi-profile requirement implies. Go's concurrency model fits a long-running daemon that juggles a scheduler, ingestion workers, an HTTP/SSE server and an MCP server. Official, maintained Go SDKs now exist for both halves of the brain: the **Claude API** (`github.com/anthropics/anthropic-sdk-go`, with `Messages.New` and `CountTokens`) and **MCP** (`github.com/modelcontextprotocol/go-sdk`, stdio transport, generic `AddTool`). `sqlite-vec` runs from Go either pure-Go (`ncruces/go-sqlite3` over wazero WASM, no cgo) or via cgo (`mattn/go-sqlite3`); FTS5 is built in. The maintainer also simply prefers Go.
**Trade-offs (stated honestly):** (1) Rich browser charting is the one place JS leads, so the operational dashboard is a small **React + Recharts SPA** under `web/` — the only JavaScript in the repo. Its built assets are embedded via `embed.FS`, so the shipped binary is still self-contained; `web/` needs a Node toolchain only at build time. Everything else — daemon, CLI, MCP, automations — is Go. (2) Some Go libraries here (the `sqlite-vec` bindings especially) are younger and move faster than their TS equivalents — pin versions, and keep `embeddings`, `db` and `agent` behind interfaces so a binding swap is local. (3) TypeScript/Node is a viable alternative for the whole stack (first-class everything, but needs a runtime); choosing it back would be a follow-up ADR.

### ADR-004 — External scheduler invoking headless Claude, not hook-spawned agents
**Decision:** Automations are driven by AXON's own scheduler calling `claude -p` headless (or the in-process Claude adapter for small tasks). Claude Code hooks handle only *in-session* deterministic concerns.
**Why:** Claude Code hooks cannot reliably spawn background agents, and scheduling belongs to a supervised, observable, budget-aware component. This keeps automations measurable and restart-safe, and keeps token spend inside AXON's ledger.

### ADR-005 — AXON owns its MCP server; community Obsidian MCP is optional interop
**Decision:** Ship a purpose-built MCP server with wikilink-safe writes and hybrid search; treat community Obsidian MCP servers as an optional, swappable fallback.
**Why:** The core loop must not depend on a fast-moving third-party server, and AXON needs token-status and knowledge-base tools no generic server provides. Wikilink safety is non-negotiable and best owned.

### ADR-006 — Vault is the source of truth; databases are derived
**Decision:** Any knowledge in SQLite must be reconstructable from the Markdown vault. `axon reindex` rebuilds everything from the vault.
**Why:** Durability and portability — if Obsidian or AXON vanish, the notes still work in any text editor. Prevents lock-in and makes the vector index disposable.

### ADR-007 — Frugality gates before every model call
**Decision:** No code path calls Claude without passing through the token manager (pre-flight count, budget check, change-gate, model selection).
**Why:** "Token-aware, not wasting tokens" is a stated requirement; making the manager a mandatory chokepoint is the only way to guarantee it rather than hope for it.

### ADR-008 — Scheduling in-daemon, OS units optional *(implemented with robfig/cron/v3)*
**Decision:** Default scheduling runs inside the daemon for cross-platform parity; `axon` can *emit* launchd/systemd/Task-Scheduler units on request.
**Why:** One config behaves identically on macOS/Linux/Windows; users who want OS-level supervision can opt in without the core depending on a specific OS scheduler.

### ADR-009 — Auth via Claude subscription/enterprise; Claude Code is the execution path
**Decision:** AXON reaches Claude **through Claude Code**, not a direct API key. Each installation sets one `claude.auth_mode`:
- `subscription` (personal, Claude Max): interactive Claude Code uses `claude login`; headless automations use a `CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`.
- `enterprise` (work, Claude Enterprise SSO, **no API available**): SSO login, governed by org policy; the same headless token *if* the org permits `setup-token`, else automations run only under an authenticated session.
- `api_key` (optional): direct Claude API via `anthropic-sdk-go`, for accounts that have Console API access.

The two installations are **separate** (different machines, accounts, restrictions); one installation runs one active profile. `ANTHROPIC_API_KEY` is left **unset** in subscription/enterprise modes (Claude Code prioritises it and would bill the API account); `axon doctor` flags a stray key.

**Why:** The maintainer's accounts are a personal Max subscription and a work Enterprise plan with no API access; both are first-class through Claude Code, and `claude -p` usage draws on the plan's Agent SDK credit rather than per-token billing. Routing AXON's own program through a subscription OAuth token outside Claude Code would breach the consumer Terms, so the direct-API path is reserved for genuine API-key accounts.

**Consequences (mainly Component 07):** without API access there is no `count_tokens` endpoint, so pre-flight counting becomes a **local estimate** (tokeniser approximation) used to keep context bounded and to guard against rate-limit / Agent-SDK-credit burn; the ledger tracks **tokens and limit/credit consumption** rather than dollars (dollar cost applies only to `api_key` mode). Model selection is a *preference* passed to `claude -p --model`; actual availability follows the plan tier. The mandatory chokepoint (ADR-007) is unchanged — every call still passes through the token manager.

### ADR-010 — Pure-Go SQLite via `modernc.org/sqlite`; vectors brute-forced behind a seam (amends ADR-002)
**Decision:** Use `modernc.org/sqlite` (a maintained, cgo-free transpilation of current SQLite, **FTS5 built in**) as the single SQLite driver. Store chunk embeddings as `float32` BLOBs in a `vec_chunks` table and run **brute-force cosine KNN in Go** for semantic search, behind the `db` repository seam. Lexical search uses native **FTS5/bm25**. The `EmbeddingProvider` + vector-repository interfaces are unchanged, so an ANN backend can be swapped in later with no caller change.
**Why:** ADR-002's named pure-Go path — `ncruces/go-sqlite3` + `asg017/sqlite-vec-go-bindings/ncruces` — does not hold up in practice: the sqlite-vec binding (latest v0.1.6) is pinned to the long-superseded `ncruces` v0.17 API (`sqlite3.Binary`) and fails to build against current `ncruces` v0.35, while current `ncruces` ships **neither FTS5 nor sqlite-vec** in its embedded WASM. The choice was: freeze the database foundation ~18 minor versions behind to keep the official sqlite-vec binding, adopt a cgo build (breaking the single-static-binary / no-toolchain goal), or take a maintained pure-Go SQLite and defer the ANN extension. `modernc.org/sqlite` preserves every goal ADR-002 actually cares about (one file, pure-Go, single static binary, FTS5) and **sqlite-vec was always a scale optimisation, not a correctness requirement** — docs/05 §7 already documents brute-force as fine to ~10^5–10^6 chunks and names the swap path. So we take the simpler, maintained foundation now and keep the seam.
**Trade-offs:** brute-force KNN is O(n·dim) per query — comfortably within NFR-09 at personal-vault scale, but not the path to millions of chunks; when a vault approaches that (or p95 search breaks the NFR-09 budget) revisit with a real ANN index (sqlite-vec on a compatible driver, or LanceDB) behind the same repository seam. Re-affirms ADR-002's single-file, pure-Go intent; supersedes only its specific library names.

### ADR-011 — Personal memory & identity live in the vault, injected via SessionStart *(Phase 8 — built)*
**Decision:** Model the user profile (`USER.md`), agent persona (`SOUL.md`) and durable personal memory (`MEMORY.md`) as plain Markdown notes in the vault (`02-Areas/Profile/`) — **not** a separate store. They are generated/seeded by the `axon onboard` wizard, edited by the human, maintained by the `memory_remember` MCP tool plus an optional `memory-distill` automation, and surfaced to the agent by injecting a **bounded** summary in the existing `SessionStart` hook (no model call).
**Why:** Consistent with ADR-006 (the vault is the source of truth) — identity is just more durable, portable, human-editable Markdown that shows up in Obsidian and rebuilds with the vault. `SessionStart` injection makes "the brain knows me" deterministic and free (no retrieval, no token spend), reusing a hook AXON already owns and that already injects status. A dedicated identity store would fragment the source of truth and add a sync surface for no benefit.
**Trade-offs:** injecting every session has a context cost, so the injection is a *compact, token-bounded* rendering (full files stay in the vault); large memories are kept in budget by the `memory-distill` automation (a compaction-style summarisation). This is the most personal data in the vault, so it is covered explicitly by redaction and NFR-14 and never leaves except as the bounded session context the user controls.

### ADR-012 — Multi-client via standard MCP; Claude Code is full-featured, Claude Desktop is tools-only *(Phase 9 — built)*
**Decision:** AXON's MCP server is the single integration point for any MCP client. **Claude Code** gets the full experience (MCP tools + hooks + plugin + generated `CLAUDE.md` + headless `claude -p` automations). **Claude Desktop** is supported as a **tools-only** client: `axon mcp install --client desktop` writes a profile-scoped `mcpServers` entry into `claude_desktop_config.json` (non-destructively); Desktop gets the vault/knowledge/token tools but not hooks, skills, subagents or headless runs.
**Why:** The MCP server already speaks the standard protocol and the registration JSON shape is identical across clients, so Desktop support is a thin wiring + documentation task, not a second server. Being explicit that Desktop lacks the deterministic hooks prevents a false sense of the `SessionStart`/`PreToolUse` guarantees there. Crucially, AXON's own tools are wikilink-safe and path-sandboxed *in the server*, so vault safety for AXON operations does not depend on the client's hooks.
**Trade-offs:** on Desktop the user loses the session-start profile injection (FR-72) and the `PreToolUse` guard over the *client's built-in* file tools; mitigations are to keep all vault mutation in AXON's tools and to document the gap (FR-75). Concurrent clients share one single-writer SQLite per profile, and the daemon remains the owner of scheduled writes (FR-76).

### ADR-013 — Apple on-device embeddings via a compiled-at-init Swift helper subprocess
**Decision:** Offer `embeddings.provider: apple` as a config-selectable alternative to Ollama, backed by Apple's on-device **NLContextualEmbedding** (NaturalLanguage framework, macOS 14+, dim 512). The API is Swift-only, so AXON ships the helper **as source embedded in the Go binary**, compiles it once during `axon init` with `swiftc` (Xcode CLT is the prerequisite; a SHA-256 marker makes re-runs skip the compile), installs it at `~/.axon/bin/axon-apple-embed` (machine-level, outside profile isolation), and shells out to it with JSON over stdin/stdout — the same subprocess seam as the `claude -p` adapter. The Go side is another `embeddings.Provider` implementation; no caller changes.
**Why:** macOS users get server-less, fully local embeddings with zero model management (assets are fetched on-device by the framework), and the Go binary stays pure Go. Rejected: **cgo** linking of NaturalLanguage (breaks the single-static-binary goal of ADR-010 and cross-compilation), and **committed prebuilt binaries** (drift, signing/Gatekeeper questions, binaries in git). Compile-at-init from embedded source keeps `axon init` reproducible from a bare installed binary (FR-01) and the helper trivially rebuildable when the source changes.
**Trade-offs:** darwin-only (config validation accepts `apple` everywhere, but init/doctor/the provider surface a clear macOS-only warning elsewhere, and the Linux installer refuses it); requires Xcode CLT once; the model is fixed (no alternative sizes/dims like Ollama offers) and its dim (512) differs from the Ollama defaults, so switching provider in either direction requires `axon reindex --embeddings`. Ollama remains the default and the cross-platform path.

### ADR-014 — Charm TUI stack (bubbletea + huh + lipgloss) for the entire CLI surface
**Decision:** Adopt the Charm family as the one terminal-UI dependency set: `bubbletea` runs live interactive views, `huh` provides forms/menus (onboard, configure, setup), `lipgloss` styles all output. Every command renders a live view on a TTY; the pre-existing plain renderers remain the canonical output for non-TTY, `--json`, and CI — enforced by a single `tui.Interactive` gate so headless paths can never block on a prompt (NFR-05 posture).
**Why:** The ops surface (install, update, configure, provider switching, vault migration) needs real interactivity; hand-rolled ANSI + bufio prompts don't scale to menus/progress and were already duplicated across onboard and the installers. Charm is the de-facto standard, pure Go, and replaces bespoke code rather than adding to it (`internal/ui` shrinks to a lipgloss facade). User-directed adoption; recorded because it crosses the "no heavyweight framework without an ADR" guardrail.
**Trade-offs:** three new dependencies and a second rendering path per command (live vs plain). Mitigations: plain renderers stay canonical and tested; live views are thin adapters over the same structured results; all Charm usage funnels through `internal/tui` so a future swap is one package.

### ADR-015 — Local model routing through the token-manager chokepoint (Ollama + Apple Foundation Models) *(built)*
**Decision:** Allow the cheap model tiers to be routed to a **local** provider via provider-prefixed model strings in the existing config fields: `models.classify: ollama:<model>` (Ollama `/api/chat`), `models.classify: apple` (Apple Foundation Models on-device `SystemLanguageModel`, macOS 26+/Apple Silicon/Apple Intelligence, classify tier only), anything else remains a Claude model string. Both new adapters implement the existing `agent.Agent` interface and are dispatched by a small router **inside the token-manager chokepoint** — `tokens` remains the only importer of `agent`, and cardinal rule 1 generalizes to *no generative call, Claude or local, bypasses the token manager*. Local calls are **ledgered but budget-exempt** (windows keep meaning "Claude quota"; no defer/deny/downgrade, no budget-guard pressure), and a `models.local_fallback: claude | fail` toggle (default `claude`) governs what happens when a local model fails or produces schema-invalid output: one local retry, then fall forward to Claude through the normal budget path, or fail visibly. The Apple adapter reuses ADR-013's delivery verbatim (Swift source embedded via `go:embed`, compiled at init with `swiftc`, SHA-256 marker, JSON over stdin/stdout, `--check-availability` probe) and uses guided generation when the call supplies an output schema. `synthesis` always stays on Claude. Scope is the **on-device model only** — the framework's Private Cloud Compute and third-party backends are excluded.
**Why:** ADR-001 deliberately left this seam ("the LLM stays remote *for now*"), and the economics changed: classify-tier work (triage labels, enrichment metadata) neither needs a frontier model nor should spend subscription quota. Ollama is already a system dependency (embeddings) and the Swift-helper pattern is already proven (ADR-013), so both adapters are marginal additions rather than new infrastructure. Routing through the chokepoint preserves the observability invariant (every call ledgered and streamed) and the dependency rule unchanged. Prefixed strings were chosen over a structured `{provider, model}` block because they are backward compatible with every existing config and the TUI. This amends ADR-007 (chokepoint now fronts multiple providers) and ADR-009 (a per-tier provider axis orthogonal to the global `auth_mode`), and supersedes the apple-embeddings spec's note that FoundationModels generation was out of scope — it is in scope precisely because it runs *inside* the chokepoint.
**Trade-offs:** small local models have a real quality floor — mitigated by output-schema validation (guided generation on Apple; JSON mode + validate on Ollama) and the fall-forward default; users choosing `local_fallback: fail` trade reliability for strict frugality. The Apple path adds an OS/hardware/Apple-Intelligence gate (doctor-surfaced) and a small shared context window, so it is confined to `classify` with a pre-flight input cap. Budget exemption means the token windows no longer describe *total* model traffic — the ledger and dashboard remain the complete record. (Spec: `docs/superpowers/specs/2026-07-03-local-model-routing-design.md`; FR-77…FR-80.)

### ADR-016 — Poll-based inbox capture on the automation scheduler *(built)*
**Decision:** Implement FR-26 (capture-by-Inbox) as a standard **`capture` automation** on the existing scheduler (suggested `*/5 * * * *`, `catch_up: run-once`), not a filesystem watcher and not an HTTP endpoint. Each tick is change-gated on a hash of the `00-Inbox/` listing; when the inbox changed, the automation (a) scans inbox `.md` notes for URLs standing on their own line and ingests any URL not already in `sources` through the existing pipeline (egress-policied, redacted, hash-deduped, evented) — never editing the note; and (b) ingests non-`.md` dropped files (`AllowLocalFiles`, **sandboxed to files physically enumerated in the inbox listing** — paths written inside notes are never file targets, NFR-05), then moves the original wikilink-safely to `04-Archive/Capture/YYYY-MM/` (nothing is deleted; there is no `vault.delete`). Failed items are remembered in automation state (skipped until they change), surfaced **once** in `.axon/review-queue.md`, and emitted as events. Enrichment is `capture.enrich: heuristic | claude` (default heuristic; `claude` goes through the ADR-007 chokepoint on the routine tier, where ADR-015 routing applies). Mobile capture falls out for free: Obsidian sync carries the vault, so a URL shared into an inbox note on a phone is captured on the next tick with zero mobile code.
**Why:** FR-26's own wording is poll-based ("queued for ingestion on the next ingestion tick"), and the automation framework already supplies scheduling, enabled/schedule/catch-up/dry-run config, panic-safety, run accounting, the change-gate convention, and event plumbing — a watcher would re-invent all of it around a new dependency. Rejected: **fsnotify** (new dependency; debounce and partial-sync-write races with vault sync tools; still needs a scan-on-start for files that arrived while the daemon was down — at which point the poll *is* the design) and **a `POST /capture` route on the dashboard server** (that server's invariant is read-only: never writes the vault, never calls Claude). A separate localhost capture listener (bookmarklet/Shortcut target) is a possible future extension behind its own ADR.
**Trade-offs:** minutes-level latency instead of seconds — acceptable for a sync-fed funnel and tunable via cron. Processed URLs are tracked in SQLite (derived), so a full reindex may re-fetch a URL once before the content-hash dedupe skips it. Failed items stay visible in the inbox rather than moving to a quarantine folder. (Spec: `docs/superpowers/specs/2026-07-03-universal-capture-design.md`; FR-26, FR-81…FR-83.)

### ADR-022 — Write tools for agentic automation runs *(accepted — built)*
**Decision:** Extend ADR-017's agentic path from read-only to read-write, resolving the two concerns ADR-017 named when it deferred write tools. An automation's agentic allowlist (`runAgentic`'s `toolsAllow`) may now include the **managed-block-safe write tools** — `vault_patch` (edits only `axon:*` managed blocks, never prose), `vault_write` (creates notes; refuses to clobber prose), `daily_append`, `memory_remember`. `vault_move` is **excluded**: vault restructuring stays human-approved through the review queue (ADR-020). Enforcement is the **existing dual allowlist**, unchanged and now covering writes: the subprocess is spawned as `axon mcp --tools <csv>` so it *physically lacks* every unlisted tool (server side), and `--allowedTools mcp__axon__<tool>…` gates the client side. **ADR-017 blocker 1 (a killed mid-run agent could half-finish multi-note work):** resolved by per-tool atomicity — each write tool call is atomic (NFR-06) and idempotent (managed-block patch, create-if-absent, append), so a budget kill leaves a *prefix of completed, individually-consistent writes*, never a half-edited note, and a re-run converges. No journal, staging vault, or rollback is introduced; this matches how the deterministic automations already treat cross-note edits (each note independent). **ADR-017 blocker 2 (MCP tools have no dry-run story):** resolved by a **server-enforced report-only mode** — a new `Deps.DryRun` on the MCP server, threaded from `axon run <automation> --dry-run` through the adapter as `axon mcp --tools <csv> --dry-run`; each write method runs full validation, computes the change, and returns `{would: …, applied: false}` **without** calling the vault mutation, so the subprocess literally cannot write in dry-run regardless of model behaviour. The honest cost: a dry-run of a write-capable agentic automation *spawns the model and spends tokens* (a real preview requires a real run) — fully ledgered and chokepoint-governed, surfaced in the run summary, and an occasional operator action rather than the scheduled path. **Demonstrator:** **compaction** (already agentic — it reads backlinks before distilling) now writes its distilled summary via `vault_patch` into the target note's `axon:summary` block on its agentic path; its `agentic: false` one-shot + deterministic-Go write stays the fallback and degradation target unchanged, so a fresh clone with agentic off behaves exactly as today (S8). Knowledge-digest and every other automation are untouched.
**Why:** The write tools already exist, are atomic and wikilink-safe, and serve the interactive Claude Code path; the dual allowlist already exists. The only genuinely new machinery is the report-only server mode and the decision to let an automation request write tools — both small, both server-enforced. Tool-driven writes unlock patterns a one-shot cannot express (read a cluster of notes, decide which managed blocks to update across several), while cardinal rule 2 holds by construction (managed-block-scoped or additive tools only; no `vault_move`). Rejected: a transactional staging buffer with commit-at-end (a large new mechanism contradicting the per-tool-atomic model AXON already uses; per-tool atomicity + idempotent convergence gives an adequate, simpler guarantee); including `vault_move` (autonomous vault restructuring is exactly the human-approved operation); making `--dry-run` estimate-only for these runs (leaves ADR-017's dry-run gap open — the whole point is a real preview).
**Trade-offs:** an agentic-write automation is non-deterministic where its deterministic-Go predecessor was exact — mitigated by keeping the `agentic:false` path as the always-available fallback and by managed-block scoping. A dry-run now costs tokens for write-capable agentic automations (documented; still governed by the chokepoint and budget_tokens). A budget kill can leave some target notes updated and others not — recoverable by re-run, never internally inconsistent. Supersedes ADR-017's "write tools in v1 — rejected" for opted-in automations; read-only remains the default for everything that does not request writes. (Spec: `docs/superpowers/specs/2026-07-04-agentic-write-tools-design.md`; FR-105…FR-107.)

### ADR-021 — Session memory via a recording hook and a distilling automation *(accepted — built)*
**Decision:** Capture what vault sessions decided without ever calling a model from a hook. The **Stop hook** becomes a deterministic recorder: when `memory.capture_sessions` is enabled (pointer-default-ON, like `memory.inject`), it upserts `{session_id → transcript_path, last_stop}` into an `automation_state` JSON row — paths only, never content, every error silent. A new **`session-distill` automation** (every 2 hours) drains sessions idle ≥ 30 minutes: reads the transcript JSONL (user + assistant text only, tail-capped), applies redaction, and makes **one classify-tier chokepoint call per session** (local-routable per ADR-015, `ValidateOutput`-guarded) extracting up to 3 `decision|lesson|preference` items or NONE; each becomes `identity.Remember(…, Source: "session")` — the exact entry format memory-distill already produces and compacts, so provenance stays legible and curation is inherited. Sessions are tried **once ever** (mark-seen-after-attempt, capped sets); a budget defer leaves the remainder pending for the next tick.
**Why:** The identity layer answers "who am I working with" but forgot "what did we decide" — the highest-value facts surface in conversation, not daily notes (memory-distill's input). The recorder/distiller split is forced by two standing rules: hooks make no model calls and never block, and every model call goes through the chokepoint. Stop fires per turn, hence upsert + idle threshold rather than distill-on-fire. Rejected: distilling inside the hook (both rules violated); SessionEnd wiring (initially deferred; adopted as FR-104 — SessionEnd marks a session immediately ready via a sticky `ended` flag, the idle threshold remaining the crash fallback); storing transcript text in SQLite (NFR-14 — paths only); retry loops for failed sessions (one try, manual `memory_remember` as recovery).
**Trade-offs:** per-turn tiny DB upserts during active sessions; a 30-minute delay before a long session's insights land; transiently failed distills are not retried. NFR-14 posture: vault sessions only (per-vault hook wiring), redaction before the model and before writes, `capture_sessions: false` for stricter profiles, `allowed_automations` excludes the distiller independently, and MEMORY.md remains the human-visible, editable record. (Spec: `docs/superpowers/specs/2026-07-04-session-memory-design.md`; FR-97…FR-99.)

### ADR-020 — Human-in-the-loop review actions on the dashboard *(built)*
**Decision:** The dashboard gains a **Review tab**: `GET /api/review` parses `.axon/review-queue.md` into typed items (link / pair / triage / resurface / info) and `POST /api/review/action` accepts or dismisses one. This **narrowly renegotiates** the dashboard's "never writes the vault" invariant: the server still never calls Claude and never free-form writes — the *only* mutations are review resolutions applied through the same wikilink-safe ops as everything else. Accepted link/pair/resurface proposals append `- [[target]]` to the note's **`axon:links` managed block** (cardinal rule 2: prose untouched; Patch creates the block if absent; idempotent). Accepted triage proposals — upgraded to structured JSON `{folder, tags}` validated at the chokepoint, written as parseable queue lines — perform the wikilink-safe `vault.Move` out of the inbox. Queue lines flip to `- [x] … ✓ applied / ✗ dismissed <date>` via a new `vault.RewriteSystemFile` that **refuses any path outside `.axon/`** (the guard is code, not convention). Mutation POSTs require `Content-Type: application/json` plus an `X-Axon-Review: 1` header — both force a CORS preflight no cross-origin page can pass (the server sends no CORS headers) — on top of the loopback bind and Host-guard (FR-63). Every action emits `review.accept`/`review.dismiss` events. The slice also delivers **FR-64** (`GET /api/export` CSV/JSON of every chart dataset + per-card links), closing the last unbuilt v1 requirement. Logic lives in a new `internal/review` package (vault-only dependency, fully Go-tested; the SPA stays thin — its first POST, no JS test harness needed).
**Why:** Five producers (ADR-016…019 plus the pipeline) feed the queue; a proposal system whose only resolution path is hand-editing markdown leaves the loop open — accepted-link application and triage moves were precisely the wikilink-sensitive operations humans do worst by hand. Rejected: a separate localhost "actions" server (same trust boundary, more surface); free-form link insertion into prose (cardinal rule 2); acting via MCP tools only (the point is a human UI); dismiss-only triage (the queue's most common item would stay inert — hence the structured upgrade).
**Trade-offs:** the read-only invariant is gone in its absolute form — replaced by a structural one (enumerable mutations, all wikilink-safe, `.axon/`-guarded rewriter). Queue rewrite/append races are possible and benign (atomic whole-file writes; producers re-propose via their own memory). Pre-upgrade freeform triage lines stay dismiss-only. Resolved lines older than 7 days compact into `.axon/review-queue-archive.md` on the next resolution write (FR-103), closing the noted future slice. (Spec: `docs/superpowers/specs/2026-07-04-review-actions-design.md`; FR-64, FR-94…FR-96.)

### ADR-019 — Feed subscriptions on the capture pattern, parsed by gofeed *(built)*
**Decision:** A `subscriptions` automation polls config-declared RSS/Atom feeds hourly (ADR-016's poll pattern): feed XML fetched through the existing egress-policied, SSRF-guarded `HTTPFetcher`; parsed by **`github.com/mmcdole/gofeed`** (the one new dependency — pure-Go leaf, justified because the stdlib has no feed parser and real-world feeds are non-conformant in exactly the ways gofeed tolerates: RSS 0.9–2.0/Atom/JSON Feed, chaotic dates, encodings); new items ingested through the standard pipeline with `subscriptions.enrich` (heuristic default | claude on the routine tier). Volume is controlled structurally: **subscribe-from-now** (a feed's first tick marks current entries seen, ingests nothing), a per-feed `max_per_tick` cap (default 5), a capped seen-state JSON row in `automation_state`, and **mark-seen-after-attempt** (an item is tried exactly once; failures surface in the run summary and events, with explicit `axon ingest` as the retry path). Feed-level failures never abort other feeds. Downstream needs nothing: items become ordinary knowledge notes that the agentic digest reads, the resurfacer vectors, and the dashboard charts.
**Why:** The capture funnel (ADR-016) covers deliberate pulls; subscriptions cover the standing sources a research assistant should watch — and the agentic knowledge-digest (ADR-017) is only as good as the material that reaches the knowledge base. All the hard parts already exist (fetcher policy, pipeline dedupe/enrichment, scheduler, state rows); the only genuinely new machinery is feed parsing, which is precisely the wheel not worth reinventing. Rejected: a hand-rolled RSS2/Atom parser (silent failure on malformed feeds; date-parsing bug farm); full backfill or newest-N on subscribe (flood or arbitrariness); per-item failure memory with retries (unbounded state, hourly spam).
**Trade-offs:** a transiently-failed item is lost unless explicitly re-ingested; feeds are managed by the `axon subscribe` CLI (FR-100) or by editing config.yaml. Polling is conditional (ETag/304, FR-101). (Spec: `docs/superpowers/specs/2026-07-04-subscriptions-design.md`; FR-91…FR-93, FR-100, FR-101.)

### ADR-018 — The proactive layer: deterministic briefing with a local-routable narrative, and vector resurfacing with proposal memory *(built)*
**Decision:** Two new automations make AXON come to the user. **`briefing`** (daily, once-per-day date cursor) writes an `axon:briefing` managed block into `Daily/<date>.md` (heartbeat's ensure→Patch pattern): deterministically assembled facts — notes changed (new `db.NotesUpdatedSince`), new sources, automation activity, review-queue pending, budget state — always; plus a 2–4 sentence narrative from **one one-shot `routine`-tier call** through the chokepoint, which ADR-015 makes local-routable (effectively free with a local model configured) and ADR-017's `budget_tokens` caps; on budget defer the briefing degrades to facts-only, never a failed run. The SessionStart hook injects a **one-line pointer** to the block when today's exists (deterministic, error-silent). **`resurfacer`** (weekly, no model call) computes mean-chunk-vector cosine between recently-touched notes (≤7 days) and dormant ones (≥90 days) — the similarity machinery is **extracted from the dashboard graph's unexported `similarityEdges` into shared exported db primitives** — and proposes the top pairs (≥0.75) into the review queue, excluding already-linked pairs and anything in its **persistent proposal memory** (an `automation_state` JSON row, the pattern capture introduced), so a pair is never re-proposed.
**Why:** The system was entirely reactive — it maintained the vault but never surfaced anything. The briefing is the cheapest possible orientation loop: the facts are free DB reads, the narrative rides the cheapest adequate tier (frugality by construction), and the pointer keeps sessions lean instead of spending hundreds of tokens injecting prose one read away. Resurfacing is the spaced-serendipity function that defines a second brain, and it needs no model at all — the vectors already exist. Rejected: agentic briefing synthesis (daily frontier spend for facts the queries already know); injecting the full block at SessionStart; a model-phrased resurfacer rationale (the similarity + dates are the rationale).
**Trade-offs:** "changed yesterday" is as fresh as the last reindex (day-granular `updated`); brute-force recent×dormant cosine is bounded by a 50-recent cap, the graph's existing 1500-note ceiling, and weekly cadence. Proposal memory grows (capped at 500 newest pairs). The link-suggester adopted the same proposal memory (FR-102, `link-suggester:proposed`, shared helpers), closing the noted follow-up. (Spec: `docs/superpowers/specs/2026-07-04-proactive-layer-design.md`; FR-88…FR-90.)

### ADR-017 — Agentic automations through the chokepoint with streaming per-turn budget enforcement *(built)*
**Decision:** Allow opted-in automations to run Claude headlessly **with AXON's own MCP tools**: the adapter's agentic shape is `claude --print --output-format stream-json --max-turns <N> --tools "" --strict-mcp-config --mcp-config <inline: axon mcp --tools <csv>> --allowedTools mcp__axon__<tool>… --no-session-persistence --setting-sources ""`. Doc 08's precondition — per-turn budget enforcement — is met with a **streaming kill-switch**: the adapter accumulates real usage from each turn's stream-json event and kills the process group the moment the run's token cap (`automations.<name>.budget_tokens`, wired to runtime by this ADR after being display-only) is exceeded; the chokepoint ledgers the **accumulated real usage** on every path, including kills (`token.run_budget_kill` event + `:failed` row). Tool access is **read-only in v1** (`vault_search`, `vault_read`, `vault_links`, `knowledge_search`, `tokens_status`) and enforced on **both sides of the wire**: `--allowedTools` client-side and a new `axon mcp --tools <csv>` server-side filter, so the subprocess MCP server physically lacks everything else. Writes stay in the automations' deterministic Go code (wikilink-safe managed-block patches), preserving the engine's "never leave a half-edited note" guarantee and trivial dry-run semantics. Agentic calls require the Claude provider (local models cannot drive MCP; synthesis is Claude by ADR-015 validation). v1 opts in **knowledge-digest** (reads the week's sources instead of a count) and **compaction** (checks backlinks before distilling), each keeping its one-shot prompt as the `agentic: false` fallback and the degradation path.
**Why:** One-shot automations cap out at "summarize what Go handed you" — knowledge-digest literally received a source *count*. Tool use upgrades the entire automation pattern (retrieve-then-synthesize with grounded wikilinks) using infrastructure AXON already owns: the MCP server, the WAL/busy-timeout multi-process DB story, and the chokepoint. Rejected: post-hoc-only accounting (a run could overshoot its cap within allowed turns — fails doc 08's precondition); `--max-budget-usd` (dollar-denominated, meaningless on subscription); write tools in v1 (a killed mid-run agent could half-finish multi-note work, and MCP tools have no dry-run story yet); hook-based guarding (unnecessary — with `--tools ""` there are no built-in tools to guard).
**Trade-offs:** agentic runs cost multiple turns of frontier-tier tokens — bounded by turn caps, the now-live budget_tokens, the kill-switch, and budget-guard; P3 already moved cheap work to local tiers. The stream-json schema is CLI surface that can drift (isolated in one adapter function, like parseClaudeJSON). Activating budget_tokens is a behavior change for one-shot automations (previously-dead config now defers oversized calls; starter values are generous). Supersedes doc 08 §3's "headless automations deliberately do not use tools" for opted-in automations; the inert shape remains the default for everything else. (Spec: `docs/superpowers/specs/2026-07-03-agentic-automations-design.md`; FR-84…FR-87.)
