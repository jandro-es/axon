# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/), and this project adheres to
[Semantic Versioning](https://semver.org/) (pre-1.0: minor versions may break).

## [Unreleased]

Developer-experience, observability, and installation improvements. Cardinal
rule 1 is generalized by ADR-015 (no generative call — Claude or local —
bypasses the token manager); the vault contract is unchanged.

### Added

- **Review-queue actions on the dashboard (ADR-020, FR-94…FR-96)** — a new
  Review tab lists every pending proposal (link suggestions, structured
  inbox-triage moves, resurfaced connections, capture records) with one-click
  accept/dismiss. Accepts are wikilink-safe by construction: links land in
  the note's `axon:links` managed block, triage moves go through the
  link-rewriting `vault.Move`, and the queue file itself is only touched by
  the new `.axon/`-guarded rewriter. The dashboard's mutation surface is
  exactly these resolutions (JSON + custom-header guard forcing a CORS
  preflight; loopback + Host-guard unchanged). Inbox-triage now emits
  structured JSON proposals so its accepts actually move notes. Also ships
  **FR-64** — every chart's data exports as CSV/JSON — closing the final
  open requirement of the original v1 contract.
- **RSS/feed subscriptions (ADR-019, FR-91…FR-93)** — declare feeds in
  `subscriptions.feeds` and AXON polls them hourly through the same
  egress-policied fetcher as every ingest, feeding new items into the
  standard pipeline (deduped, redacted, ledgered, optionally enriched on the
  routine tier). Volume is structural: subscribe-from-now (no backfill
  floods), at most `max_per_tick` items per feed per tick, one attempt per
  item. The agentic weekly digest now synthesizes across your subscriptions.
  New dependency: `mmcdole/gofeed` (feed parsing; ADR-justified).
- **Proactive layer (ADR-018, FR-88…FR-90)** — AXON now comes to you. A daily
  `briefing` automation writes an `axon:briefing` block into the daily note
  (notes changed, new sources, automation activity, review queue, budget)
  plus a short narrative on the routine tier — local-routable, budget-capped,
  degrading to facts-only under pressure — and every Claude session opens
  with a one-line pointer to it. A weekly `resurfacer` proposes review-queue
  connections between what you're working on now and notes dormant for 90+
  days, by mean-chunk-vector similarity (shared with the graph view), with
  persistent proposal memory so nothing is suggested twice. Zero model calls.
- **Agentic automations (ADR-017, FR-84…FR-87)** — knowledge-digest and
  compaction now run Claude headlessly **with AXON's read-only MCP tools**
  (vault/knowledge search, note reads, backlinks): the digest actually reads
  the week's sources instead of being told a count, and compaction checks
  backlinks before distilling. Enforcement is structural: no built-in tools,
  a per-call `--allowedTools` list **and** a server-side `axon mcp --tools`
  filter, bounded turns, and a streaming kill-switch that terminates a run
  the moment `automations.<name>.budget_tokens` is exceeded — with the real
  accumulated usage ledgered on every path, including kills
  (`token.run_budget_kill`). `agentic: false` per automation restores the
  one-shot behavior, which also remains the automatic degradation path.
  Note: `budget_tokens` was previously display-only and is now enforced for
  all automations (one-shot calls defer when the estimated input exceeds it).
- **Universal capture (ADR-016, FR-26 + FR-81…FR-83)** — the new `capture`
  automation turns `00-Inbox/` into a capture funnel: paste a URL on its own
  line in any inbox note, or drop a PDF/file into the folder, and AXON ingests
  it within minutes through the standard pipeline (egress-policied, deduped,
  ledgered), files the result under `03-Resources/Knowledge/`, and moves the
  original wikilink-safely to `04-Archive/Capture/YYYY-MM/` — nothing is ever
  deleted, and inbox notes are never modified. Ticks are change-gated on the
  inbox listing; failures are remembered (no retry spam) and surfaced once in
  the review queue. Mobile capture works with zero mobile code via vault sync.
  New optional config: `capture.enrich` (heuristic default | claude via the
  chokepoint) and `capture.archive_dir`.
- **Local model routing (ADR-015, FR-77…FR-80)** — the `classify` and
  `routine` tiers can now be served by local providers via provider-prefixed
  model strings: `models.classify: "ollama:qwen3:8b"` (local Ollama chat) or
  `models.classify: "apple"` (Apple Foundation Models on-device model;
  macOS 26+, Apple Silicon, Apple Intelligence, classify tier only — delivered
  with the same compiled-at-init Swift helper pattern as Apple embeddings).
  Local calls run through the token-manager chokepoint and are fully ledgered
  (provider-prefixed model strings, `cost_usd` null) but **budget-exempt**:
  they never consume the day/week windows or trigger defer/deny/downgrade —
  budgets keep meaning Claude quota. On local failure or schema-invalid
  output, `models.local_fallback` (default `claude`) retries locally once and
  then falls forward to Claude through the normal budget path, or fails
  visibly when set to `fail`. `axon configure models` gained a provider step
  with convergence probes, and `axon doctor`/`axon init` report and converge
  the configured local providers. New optional config: `models.ollama_host`,
  `models.local_fallback`, `models.apple_helper`.

- **Build versioning you can check** — `axon version` reports the version,
  commit, build date, and Go/OS/arch, with `--short` for scripts; `axon --version`
  works too. Every build is stamped: `make build`/`make release` inject the exact
  `git describe` version, commit, and date via `-ldflags`, and a plain
  `go build`/`go install` falls back to Go's embedded VCS commit — so no build is
  ever an anonymous `0.0.0-dev`.
- **`axon automations` [--json]** — list every automation with its enabled state
  (config + policy), purpose, schedule, and last run (status, when, tokens, and
  the skip/error reason).
- **`axon health` [--json]** — a 0–100 vault health score with a letter grade and
  per-dimension breakdown: index & link integrity, automation reliability, and
  knowledge freshness. Read-only; no model call.
- **`axon ingest --enrich`** — opt into Claude-backed metadata enrichment routed
  through the token-manager chokepoint; the ingest result now reports how the
  metadata was produced and the tokens it cost (deterministic heuristic remains
  the default, at zero tokens).
- **Professional install/update system** — a self-documenting `Makefile`
  (`make` lists everything): `doctor`, `install`, `setup`, `update`, `reload`,
  `uninstall`, and `release` (cross-compiled macOS/Linux × amd64/arm64 binaries).
  A cross-platform dependency **preflight** (`scripts/preflight.sh`, `make doctor`)
  checks the build + runtime toolchain and prints the exact install command for
  your package manager. New Linux (`systemd --user`) install/update/uninstall
  scripts sit alongside the macOS ones, and an `update` flow rebuilds, swaps the
  binary (reporting the version delta), converges the profile (`axon init` — DB
  migrations, scaffold, wiring, dashboards), restarts the daemon, and lists newly
  shipped config settings. See [INSTALL.md](INSTALL.md).

### Changed

- **Clearer console output** — a shared `internal/ui` styler gives commands
  consistent colour + status glyphs, auto-disabled for pipes/non-TTY and honouring
  `NO_COLOR`/`FORCE_COLOR`. Errors now render as a clear block with an actionable
  fix hint (e.g. a missing config points you at `axon init`).
- **Descriptive budget-guard messaging** — when the token guard pauses a
  non-essential automation, the skip reason now names the window and threshold
  (e.g. "budget guard active — daily 82% ≥ 80% …") instead of a bare "budget",
  and `axon status` shows the same reason.
- `make uninstall` replaces `make uninstall-macos` (now OS-aware); `make setup`
  works on Linux as well as macOS.

## [0.10.0] — 2026-06-28

Completed the remaining deferred requirements, so every M/S requirement in the
contract (`docs/03`) is now implemented.

### Added

- **PDF ingestion (FR-21)** — PDFs go through the same fetch→extract→enrich→
  chunk→embed pipeline as URLs and text files (`internal/ingestion`, via
  `ledongthuc/pdf`); malformed PDFs surface a clear error, never a crash.
- **`config get` / `config set` (FR-04)** — read and update config values by
  dotted key (resolved relative to the active profile). `set` preserves comments
  and formatting and re-validates before writing; invalid changes are refused.
- **`stop` (FR-04)** — gracefully stops the daemon for the active profile via a
  per-profile pidfile (`start` now writes one); stale pidfiles are cleaned up.
- **`metrics_query` MCP tool (FR-50)** — token-ledger aggregates (by day/
  operation/model) plus current budget windows, for dashboards and agents.
- **Obsidian MCP interop (FR-54)** — `profiles.<p>.interop.obsidian_mcp` registers
  a community Obsidian MCP server alongside AXON's own when running
  `axon mcp install`; AXON's server stays the default vault contract.
- **`api_key` direct-API adapter (FR-33/FR-40/FR-41)** — in `auth_mode: api_key`
  AXON calls the Anthropic API directly (`anthropic-sdk-go`) with **exact
  `count_tokens`** pre-flight and per-token cost; subscription/enterprise still
  use Claude Code. Still mediated by the token-manager chokepoint.
- **Keychain secrets** — `keychain:NAME` references resolve from the OS keychain
  (`zalando/go-keyring`), alongside `env:NAME`.

### Notes / optional future polish (not contract requirements)

- Heartbeat is intentionally model-free (cheapest automation); an optional
  one-line model synthesis remains a possible enhancement.
- The ingest fetcher re-validates egress policy on every redirect and blocks
  link-local/metadata IPs (NFR-05); pinning the resolved IP across the dial is a
  further defense-in-depth hardening.

## [0.9.0] — 2026-06-28

Phase 9 — multi-client integration (Claude Desktop) (FR-74…FR-76, ADR-012,
Component 13). With this, the full spec pack (`docs/00`–`13`) is implemented.

### Added

- **`axon mcp install --client code|desktop`** — registers the AXON MCP server
  with a Claude client. `desktop` merges a profile-scoped entry into
  `claude_desktop_config.json` **non-destructively** (other servers preserved;
  an unparseable existing file is refused, not clobbered); `code` (re)generates
  the project `.claude/` wiring. `--print` previews the registration JSON.
- **`internal/clients`** — OS-specific Claude Desktop config-path resolution,
  the non-destructive merge, and registration detection.
- **Per-client `doctor` checks** — `client:claude-code` and
  `client:claude-desktop` report whether AXON is registered (and for which
  profile) and state Claude Desktop's reduced guarantees honestly: tools only,
  no hooks/skills/profile injection.

### Notes

- Claude Desktop receives AXON's **tools** but not hooks, skills, subagents or
  headless automations (those remain Claude Code). AXON's own tools stay
  wikilink-safe and path-sandboxed **in the server**, so vault safety does not
  depend on the client.

## [0.8.0] — 2026-06-28

Phase 8 — the personal memory & identity layer (FR-70…FR-73, NFR-14, ADR-011,
Component 12).

### Added

- **Identity layer** (`internal/identity`) — a first-class set of vault notes
  under `02-Areas/Profile/`: `USER.md` (profile), `SOUL.md` (assistant persona &
  boundaries) and `MEMORY.md` (durable entries in an `axon:memory` managed
  block). Generated wikilink-safely and never clobbering human edits.
- **`axon onboard`** — an interactive, idempotent wizard (no model call) that
  interviews the user, writes the identity layer, and (re)ensures the Claude Code
  wiring. Supports `--non-interactive`, `--from <file>` (YAML/JSON answers) and
  `--json` (secret-free report). `axon init` now nudges to run it.
- **SessionStart identity injection** — the hook injects a token-bounded snapshot
  of USER + SOUL + recent `MEMORY` into each Claude Code session with **no model
  call**; governed by `profiles.<p>.memory` (`inject`, `session_tokens`,
  `recent_entries`) and disablable per profile.
- **`memory_remember`** MCP tool — appends a dated durable entry to the
  `axon:memory` block, wikilink-safe, never touching human prose.
- **`memory-distill`** automation — distils recent daily-note activity into new
  memory entries and compacts an over-long block, through the token manager,
  change-gated and dry-run aware.

### Security

- **Personal-data privacy (NFR-14)** — the identity layer never reaches logs,
  events, the token ledger or exports; redaction (`policy.redaction_rules`) is
  applied to the injected block before any egress.

## [0.7.0] — 2026-06-28

The initial feature-complete build, implemented in phases against
[`docs/11-build-roadmap.md`](docs/11-build-roadmap.md).

### Added

- **CLI & bootstrap** — `axon init` (idempotent, verbose), `config validate`,
  `doctor`, profile resolution and a single self-contained binary.
- **Vault core** — wikilink-safe filesystem (`read`/`write`/`patch`/`move`,
  atomic, sandboxed), frontmatter parsing, `axon:*` managed blocks, link-graph
  builder, vault scaffold + note templates, and `reindex`.
- **Knowledge ingestion & search** — fetch → extract → clean → redact → hash →
  enrich → write → chunk → embed (Ollama) → index; hybrid FTS5 + vector search;
  `ingest`/`search` commands. (Vectors use a brute-force cosine store behind a
  repository seam — see ADR-010.)
- **Token & context manager** — the mandatory chokepoint (`Authorize`/`Run`/
  `BuildContext`/`Status`): local pre-flight estimate, day/week token windows,
  model selection + downgrade, ledger, and `status`.
- **Automation engine** — portable scheduler (cron + jitter + locks + catch-up),
  the run lifecycle (change-gate → budget pre-check → dry-run → record), the real
  `claude -p` adapter, the nine standard automations, and `run`/`start`.
- **Agent bridge** — the AXON MCP server (wikilink-safe vault tools, hybrid
  search, token/automation tools), Claude Code hooks (SessionStart/PreToolUse/
  PostToolUse/Stop), and a plugin (skills + subagents + `CLAUDE.md`).
- **Dashboard & observability** — a localhost HTTP API + SSE, an embedded
  Vite/React/Recharts SPA (tokens, usage, runs, ingestion, vault growth,
  knowledge graph, activity feed), `/health`, and in-vault Dataview dashboards.
- **Multi-profile, policy & hardening** — full profile isolation, policy
  enforcement everywhere, OS service units (`service`), portable `export`,
  `profiles` inspection, and docs.

### Security

- Vault path-traversal sandbox; SSRF protection (per-redirect egress
  re-validation, link-local/metadata IP block); agent-path local-file ingestion
  refused; provenance-field redaction; dashboard `Host`-header (anti
  DNS-rebinding) guard; hardened `PreToolUse` denylist.

### Notes / not yet implemented (at 0.7.0)

- PDF ingestion, the optional `auth_mode: api_key` in-process adapter, heartbeat
  model synthesis, richer `/health`, DNS-rebinding IP pinning on ingest, and
  `config get/set`. *(PDF ingestion, the api_key adapter and `config get/set`
  were implemented in 0.10.0.)*

[Unreleased]: https://github.com/jandro-es/axon/compare/v0.10.0...HEAD
[0.10.0]: https://github.com/jandro-es/axon/releases/tag/v0.10.0
[0.9.0]: https://github.com/jandro-es/axon/releases/tag/v0.9.0
[0.8.0]: https://github.com/jandro-es/axon/releases/tag/v0.8.0
[0.7.0]: https://github.com/jandro-es/axon/releases/tag/v0.7.0
