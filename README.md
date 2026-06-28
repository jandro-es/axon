# AXON — A Local-First AI Operating System for Obsidian

> **Codename:** `AXON` (placeholder — rename freely; the CLI binary, package scope and config keys all derive from one constant).
> **Status:** Specification pack, ready to hand to Claude Code for implementation.
> **Audience:** Claude Code (the build agent) and the maintainer.

AXON turns an Obsidian vault into a **second brain that maintains itself**. It is a local-first runtime that wires **Claude** and **Claude Code** into your vault, runs configurable automations (heartbeats, daily logs, compaction, exports, re-indexing), ingests external knowledge (articles, URLs, PDFs), accounts for every token it spends, and surfaces everything on a real-time local dashboard.

It is designed to be **cloned, configured with a handful of values, and stood up with one command** — twice over, in fact: a `personal` profile and a `work` profile, on different machines, under different Claude accounts and different restriction policies.

---

## What you are building (one paragraph)

A cross-platform **Go** daemon (`axon`) — a single self-contained binary — that sits beside an Obsidian vault. The vault (plain Markdown) is the durable memory. The daemon owns a single local **SQLite + sqlite-vec** database (operational metrics + vector index), a **knowledge-ingestion pipeline** (URL/article/PDF → clean Markdown → chunk → embed via local **Ollama** → index), a portable **scheduler** for automations, a **token-accounting** subsystem (local estimate + reported usage + token/credit budgets), an **MCP server** that gives any Claude client wikilink-safe vault tools and hybrid search, and a **real-time dashboard** (a small React/Recharts SPA embedded in the binary). Claude Code is the brain — reached through your Claude **subscription** (personal: Max) or **enterprise** login (work), not an API key: interactively inside the vault (hooks, skills, subagents, a `CLAUDE.md`) and headlessly on a schedule (`claude -p`) for automations. One `axon init` reproduces the whole thing from config.

---

## Design principles

1. **Local-first.** All state lives on your disk: the vault (Markdown), one SQLite file per profile, Ollama for embeddings. The only network dependency is Claude itself — reached through Claude Code on your subscription/enterprise login (the LLM) — and whatever URLs you choose to ingest. See [ADR-001](docs/02-architecture.md#adr-001-what-local-first-means-here).
2. **The vault is the source of truth.** Databases are derived and disposable; they can always be rebuilt from the Markdown. Never store knowledge that exists *only* in SQLite.
3. **Token frugality is a feature, not an afterthought.** Every Claude call is measured, budgeted, and justified. Automations run on *new material*, not on a clock for its own sake. See [Component 07](docs/07-component-context-token-manager.md).
4. **Deterministic where it matters.** Guardrails (budgets, redaction, egress, wikilink integrity, destructive-op protection) are enforced by code and hooks, never by asking the model nicely.
5. **Reproducible & multi-profile.** Behaviour is declared in `axon.config.yaml` + `.env`. Profiles are fully isolated (data dir, secrets, account, policy). Cloning the repo and running `axon init` is the entire setup.
6. **Observable.** Nothing happens silently. Every run, token, ingest and error is logged and visible on the dashboard.
7. **Start lean, evolve.** The most common failure mode of "second brain" systems is over-engineering. Ship the smallest thing that compounds, gated behind config flags.

---

## Quick start (target end-state, for the README of the generated repo)

```bash
git clone https://github.com/<you>/axon.git && cd axon
cp axon.config.example.yaml axon.config.yaml   # set vault path, profile, budgets
cp .env.example .env                           # set CLAUDE_CODE_OAUTH_TOKEN (from `claude setup-token`)
./scripts/install.sh                           # checks prereqs, builds the SPA + binary
axon init                                       # scaffolds vault, DB, Claude config — verbose
axon start                                      # daemon + dashboard (http://localhost:7777)
axon doctor                                     # health check
```

`AXON_PROFILE=work axon init` provisions the work profile on the **work machine** (Claude Enterprise SSO). Personal and work are separate installations, not two profiles running at once.

---

## How to read this pack (build order)

| # | Document | Purpose |
|---|----------|---------|
| 00 | [Research & best practices](docs/00-research-and-best-practices.md) | The findings this design is grounded in. Read for the "why". |
| 01 | [PRD](docs/01-prd.md) | Vision, goals, users, scope, success criteria. |
| 02 | [Architecture](docs/02-architecture.md) | System design, module boundaries, data flow, ADRs. |
| 03 | [Requirements](docs/03-requirements.md) | Numbered functional & non-functional requirements (the contract). |
| 04 | [Data model & config](docs/04-data-model-and-config.md) | Vault layout, DB schema, frontmatter, full config reference. |
| 05 | [Knowledge ingestion](docs/05-component-knowledge-ingestion.md) | URL/article/PDF → Markdown → chunk → embed → index. |
| 06 | [Automation engine](docs/06-component-automation-engine.md) | Scheduler, the standard automations, headless agent runs. |
| 07 | [Context & token manager](docs/07-component-context-token-manager.md) | Counting, budgets, compaction, retrieval, frugality. |
| 08 | [Agent bridge & MCP](docs/08-component-agent-bridge-mcp.md) | MCP tools, hooks, skills, subagents, wikilink safety. |
| 09 | [Dashboard & observability](docs/09-component-dashboard-observability.md) | Real-time graphs, metrics, the knowledge graph. |
| 10 | [Installer & bootstrap](docs/10-component-installer-bootstrap.md) | `axon init`, prereq checks, idempotency, profiles. |
| 11 | [Build roadmap](docs/11-build-roadmap.md) | Phased plan with milestones and acceptance gates. |

Also in this pack: [`CLAUDE.md`](CLAUDE.md) (build-agent instructions), [`axon.config.example.yaml`](axon.config.example.yaml), [`.env.example`](.env.example).

---

## Scope guardrails for the build agent

- **Do** keep the backend single-language (Go) and the database single-file (SQLite + sqlite-vec). The only JavaScript is the dashboard SPA under `web/`.
- **Do** make every subsystem toggleable via config; a fresh install with all automations off must still run and be useful.
- **Don't** add a server-based vector DB, a cloud dependency, or a heavyweight framework without an ADR justifying it.
- **Don't** invent vault knowledge in SQLite that can't be regenerated from Markdown.
- **Don't** let any automation write to the vault without wikilink-safe operations and a dry-run mode.

Everything else is specified in the documents above.
