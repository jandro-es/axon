# AXON — A Local-First AI Operating System for Obsidian

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8.svg?logo=go&logoColor=white)](go.mod)
[![CI](https://github.com/jandro-es/axon/actions/workflows/ci.yml/badge.svg)](.github/workflows/ci.yml)
[![Single binary](https://img.shields.io/badge/build-single%20static%20binary-success.svg)](#install)

**AXON turns an Obsidian vault into a second brain that maintains itself.** It
is a single Go binary that runs beside your vault: it captures and ingests
knowledge, keeps the vault organised, remembers what you decide, and accounts
for every token it spends — with **Claude** (through your subscription or
enterprise login) as the brain, and local models for the cheap work. Your
vault stays plain Markdown; everything else is derived and disposable.

> 📖 **New here? Start with the [Setup & Usage Guide](docs/GUIDE.md)** — a
> complete walkthrough from a clean machine to a running system.

## What it does

**Knowledge flows in from everywhere.**
- Drop a URL, article, or PDF: `axon ingest` turns it into a clean, linked,
  redacted Markdown note — chunked, embedded (local Ollama), and indexed.
- Paste a URL into an inbox note or drop a file into `00-Inbox/` — the
  **capture** automation ingests it for you. The inbox is a funnel.
- Subscribe to RSS/Atom feeds (`axon subscribe`) and standing sources flow
  through the same pipeline, capped and deduplicated.
- **Hybrid search** (FTS5 lexical + vector semantic) over all of it, from the
  CLI or any Claude client.

**The vault maintains itself.** Fifteen scheduled automations — inbox triage,
daily log, note compaction, link suggestions, a morning **briefing**, a weekly
**resurfacer** that reconnects dormant notes to what you're working on now, a
weekly knowledge digest, memory distillation, and more. They run on *new
material* (content-hash gated), never on a clock for its own sake, and
everything they propose lands in a review queue you resolve with one click on
the dashboard's **Review** tab. Turn any of them off; a system with all
automations off still runs and is useful.

**It knows you, and remembers.** An identity layer (`USER` / `SOUL` /
`MEMORY`) is injected into every Claude Code session, and AXON captures what
your sessions *decide*: finished sessions are distilled into durable
decisions, lessons, and preferences — privately (paths only, redacted before
any model sees text, never in logs or exports).

**Every token is measured.** One chokepoint authorizes, budgets, and ledgers
every generative call — Claude via `claude -p` on your login by default, an
optional direct-API mode, or **local models** (Ollama / Apple on-device) for
the cheap tiers, which cost nothing against your budget. Agentic runs (the
digest reads the week's sources; compaction checks backlinks and writes its
own summary through wikilink-safe tools) are bounded by turn caps and a
streaming kill-switch.

**Everything is visible.** A real-time dashboard (React SPA embedded in the
binary, SSE) streams every run, token, ingest, and error — plus the token
ledger, vault-growth charts, a knowledge graph, and the Review tab. Every
chart exports as CSV/JSON.

## Safety guarantees (enforced in code, not by prompting)

1. **No generative call bypasses the token manager.** Claude, local models,
   and the optional API mode all pass one chokepoint: estimate → budget check
   → run → ledger.
2. **No vault mutation that isn't wikilink-safe.** Renames rewrite inbound
   links (`vault_move`); edits land in `axon:*` managed blocks
   (`vault_patch`) and never clobber your prose. There is **no** delete, and
   the vault FS is sandboxed against path traversal. This holds for you, for
   automations, and for any agent — the safety lives in the server.

## Install

### 1. Prepare the machine (skip anything you have)

AXON needs the `claude` CLI (the brain) and **Ollama** (local embeddings;
optional local models). On a fresh machine:

```bash
# macOS (Homebrew: https://brew.sh)
brew install ollama
ollama pull nomic-embed-text                # default embedding model
npm install -g @anthropic-ai/claude-code    # or the installer at claude.com/claude-code
claude login                                # your Claude subscription / enterprise SSO

# Linux: curl -fsSL https://ollama.com/install.sh | sh   (then the same three steps)
```

For headless automations, mint a long-lived token once: `claude setup-token`
(goes into `~/.axon/.env`).

**Ollama not allowed on your machine?** On Apple silicon, AXON can use
**Apple's on-device Foundation Models** instead — for embeddings
(`axon configure embeddings apple`) and the classify tier
(`axon configure models classify apple`, macOS 26+). No server, no downloads;
see the [Guide §4 "Providers"](docs/GUIDE.md#4-configuration). And without
either, everything still works — search is lexical-only until vectors
back-fill.

### 2. Install AXON — one line, no toolchain

```bash
curl -fsSL https://raw.githubusercontent.com/jandro-es/axon/main/install.sh | bash
```

This downloads the latest release binary (SHA-256 verified) and hands over to
the interactive **`axon setup`**: vault path, profile, embeddings provider —
then it provisions everything (data dir, DB, vault scaffold, `.claude/`
wiring, dashboards, auto-start service). Idempotent; re-run any time.

**From source instead** (needs Go 1.26+, Node, make):

```bash
git clone https://github.com/jandro-es/axon.git && cd axon
make doctor && make setup
```

### 3. Verify, run, keep current

```bash
axon doctor      # prerequisites, with the exact fix for anything missing
axon start       # scheduler + dashboard → http://127.0.0.1:7777
axon status      # remaining day/week token budget
axon update      # later: checksum-verified self-update (source installs: make update)
axon uninstall   # remove daemon + binary; --purge also removes ~/.axon. Vault untouched.
```

Full details — flags, Windows, moving your vault, troubleshooting — in
[INSTALL.md](INSTALL.md) and the [Guide](docs/GUIDE.md). Daily commands live
in the Guide's [command reference](docs/GUIDE.md#15-command-reference); the
short list is `ingest`, `search`, `subscribe`, `run`, `status`, `configure`.

## Architecture

The vault (plain Markdown) is durable memory; the **axon daemon** is the
runtime around it; **Claude** is the brain and **Ollama** does embeddings and
the cheap local tiers. The daemon owns one SQLite file per profile
(relational + FTS5 + vectors — derived and fully rebuildable with
`axon reindex`), the ingestion pipeline, the scheduler, the token chokepoint,
an MCP server of wikilink-safe tools (used by Claude Code *and* Claude
Desktop), and the embedded dashboard.

![AXON system architecture](docs/diagrams/architecture.svg)

**Ingestion** — every source (URL, PDF, file, inbox capture, feeds) takes the
same path: fetch → clean → redact → idempotency gate → enrich → linked note →
chunk → embed → index:

![AXON knowledge ingestion pipeline](docs/diagrams/ingestion-pipeline.svg)

**The token chokepoint** — every automation gates on new material; every
generative call is estimated, budgeted, and ledgered through exactly one path:

![AXON token chokepoint and automation lifecycle](docs/diagrams/token-chokepoint.svg)

*All diagrams are editable — open the `.excalidraw` sources in
[docs/diagrams](docs/diagrams) at [excalidraw.com](https://excalidraw.com).
The [Guide](docs/GUIDE.md) has two more: the personal-memory layer and the
multi-client wiring.*

## Two profiles, zero sharing

Run a `personal` profile (Claude Max) and a `work` profile (Enterprise SSO) as
separate installs — separate data, secrets, accounts, budgets, and egress
policies. The work profile is deny-by-default on ingestion and can disable
memory injection entirely. `axon profiles` shows the isolation surface.

## Principles

- **Local-first.** All state on your disk; the only network dependencies are
  Claude (via your login) and the URLs you choose to ingest.
- **The vault is the source of truth.** Databases are derived and disposable.
- **Token frugality is a feature.** Measured, budgeted, justified; local
  models for the cheap work.
- **Deterministic where it matters.** Budgets, redaction, egress allowlists,
  and wikilink integrity are enforced by code and hooks, never by asking the
  model nicely.
- **Observable.** Nothing happens silently.

## Documentation

| Document | Purpose |
|----------|---------|
| [**Setup & Usage Guide**](docs/GUIDE.md) | **Start here.** End-to-end: install, configure, run, and use every feature. |
| [Installation](INSTALL.md) | Machine prep, release + source installs, update/uninstall, Windows. |
| [Architecture](docs/02-architecture.md) | System design, module boundaries, data flow, ADR-001…022. |
| [Requirements](docs/03-requirements.md) | The numbered contract: FR-01…107, NFR-01…14. |
| [Data model & config](docs/04-data-model-and-config.md) | Vault layout, DB schema, frontmatter, full config reference. |
| [Knowledge ingestion](docs/05-component-knowledge-ingestion.md) | URL/PDF/capture/feeds → Markdown → chunk → embed → index. |
| [Automation engine](docs/06-component-automation-engine.md) | Scheduler, the fifteen standard automations, agentic runs. |
| [Context & token manager](docs/07-component-context-token-manager.md) | Counting, budgets, local routing, compaction, frugality. |
| [Agent bridge & MCP](docs/08-component-agent-bridge-mcp.md) | MCP tools, hooks, agentic allowlists, wikilink safety. |
| [Dashboard & observability](docs/09-component-dashboard-observability.md) | Live charts, the Review tab, the knowledge graph. |
| [Personal memory & onboarding](docs/12-component-personal-memory-and-onboarding.md) | The identity layer, session memory, `axon onboard`. |
| [Multi-client (Claude Desktop)](docs/13-component-multi-client-claude-desktop.md) | One MCP server, many Claude clients. |
| [1.1 roadmap](docs/14-roadmap-1.1.md) | What's next: ask-your-vault, retrieval scale, entity intelligence, capture reach. |

Deeper design notes (vision, research, installer internals) live in
[docs/](docs/); build conventions are in [`CLAUDE.md`](CLAUDE.md).

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for
build/test instructions and the two cardinal rules every change must respect.
Security issues: report privately per [SECURITY.md](SECURITY.md).

## License

AXON is released under the [MIT License](LICENSE) — © 2026 jandro-es.

> AXON is an independent, local-first tool. "Claude" and "Claude Code" are
> products of Anthropic; "Obsidian" and "Ollama" belong to their respective
> owners. AXON integrates with them but is not affiliated with or endorsed by
> them.
