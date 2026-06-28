# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/), and this project adheres to
[Semantic Versioning](https://semver.org/) (pre-1.0: minor versions may break).

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

### Notes / not yet implemented

- PDF ingestion, the optional `auth_mode: api_key` in-process adapter, heartbeat
  model synthesis, richer `/health`, DNS-rebinding IP pinning on ingest, and
  `config get/set`.

[0.9.0]: https://github.com/jandro-es/axon/releases/tag/v0.9.0
[0.8.0]: https://github.com/jandro-es/axon/releases/tag/v0.8.0
[0.7.0]: https://github.com/jandro-es/axon/releases/tag/v0.7.0
