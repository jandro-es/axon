# 01 — Product Requirements Document (PRD)

## 1. Vision

A **local-first AI operating system** that makes an Obsidian vault a second brain that maintains itself. The human captures and thinks; AXON (driven by Claude and Claude Code) triages, links, summarises, ingests external knowledge, and keeps the whole thing observable and within a token budget. One repository, configured with a handful of values, reproduces the entire system on any machine — once for personal life, once for work — separate installations, fully isolated.

## 2. Problem statement

Knowledge scatters across a dozen tools and decays into noise. Traditional PKM (PARA, Zettelkasten) provides structure but loads all maintenance onto the human, so vaults rot. Meanwhile, naïve "AI + notes" setups either corrupt the vault (filesystem renames break wikilinks), burn tokens indiscriminately, run blind (no observability), or are bespoke one-offs that can't be reproduced for a second context. AXON solves the maintenance, safety, cost-awareness, observability and reproducibility problems together.

## 3. Goals

- **G1 — Self-maintaining vault.** Routine PKM maintenance (capture triage, linking, daily logs, distillation) is automated and reliable.
- **G2 — Token-aware by construction.** Every Claude interaction is counted, budgeted and justified; automations act on new material, not on a clock.
- **G3 — Knowledge ingestion.** Articles, URLs and PDFs become clean, linked, retrievable notes.
- **G4 — Real-time observability.** Live graphs for tokens, costs, runs, ingestion and the knowledge graph.
- **G5 — Reproducible & multi-profile.** Clone → set values → `axon init` → running system. Personal and work profiles are fully isolated (account, hardware, data, restrictions).
- **G6 — Safe by default.** Wikilink integrity, dry-runs, redaction, egress allow-listing and destructive-op protection are enforced in code.
- **G7 — Local-first.** All data and infrastructure stay on the machine; the only required network dependency is Claude itself, reached via Claude Code on your subscription/enterprise login.

### Non-goals (v1)

- Not a hosted/multi-user SaaS; single user, single machine per profile.
- Not a replacement for Obsidian's editor or sync; AXON operates *beside* Obsidian.
- Not a fully-local LLM product (a seam is left for it; the supported path is Claude Code on a Claude subscription/enterprise plan, or the Claude API in `api_key` mode).
- Not a mobile app (the dashboard is responsive web; mobile capture is out of scope for v1).
- No proprietary note format; Markdown only.

## 4. Users & primary use

A single technical maintainer running two isolated instances:

- **Personal installation** — Claude Max subscription; life/learning/projects; permissive ingestion; relaxed limits.
- **Work installation** — separate machine, Claude Enterprise SSO (no API available); stricter policy (egress allowlist, ingestion domain controls, redaction, tighter limits, a reduced automation set).

The maintainer is comfortable in a terminal, uses Claude Code daily, and wants to hand the build to a coding agent. The system must also be legible to *that future maintainer* six months later — hence the emphasis on observability and clear setup output.

## 5. User journeys

1. **Stand it up.** Clone, set vault path + `auth_mode` + OAuth token + limits, run `axon init` (verbose, idempotent), `axon start`. Dashboard is live at `localhost:7777`.
2. **Capture.** Drop a thought into today's daily note or the Inbox (in Obsidian, as normal). Later, **inbox-triage** files and links it.
3. **Ingest knowledge.** `axon ingest <url>` (or paste into an Inbox capture). The pipeline produces a clean, summarised, tagged, linked note in Resources, embedded and indexed.
4. **Ask the vault.** In Claude Code (inside the vault), hybrid search and retrieval answer from notes + ingested knowledge; wikilink-safe edits apply changes.
5. **Daily rhythm.** A **heartbeat** surfaces what changed and the budget status; an end-of-day **daily-log** synthesises the day and rolls tasks forward; weekly a **knowledge-digest** surfaces new connections.
6. **Stay frugal.** The **budget-guard** pauses non-essential automations near the cap; the dashboard shows where tokens went.
7. **Distil.** **Compaction** turns stale logs and sprawling notes into durable summaries; **context-export** produces a portable snapshot.
8. **Replicate.** Repeat step 1 with `AXON_PROFILE=work` on the work machine; behaviour differs only by config/policy.

## 6. Success criteria (measurable)

- **S1.** A fresh machine goes from `git clone` to a running, healthy system (`axon doctor` green) in **≤ 15 minutes** with **≤ 6 values** set by hand.
- **S2.** `axon init` is **idempotent**: a second run makes no changes and says so.
- **S3.** With all automations enabled and a typical day's activity, **no automation makes a Claude call when nothing relevant changed** (verified by run logs showing skips).
- **S4.** Every Claude call (interactive via hooks, and headless) appears in the token ledger with model, operation, token counts (and cost in `api_key` mode) within **≤ 5s** on the dashboard.
- **S5.** A vault rename/move performed through AXON tools leaves **zero broken wikilinks** (verified by a link-integrity check).
- **S6.** Ingesting a representative article URL yields a clean note with title, source, summary, tags and ≥1 suggested link, embedded and retrievable, in **one command**.
- **S7.** The two installations share **no** data, secrets or Claude account, verified by inspecting their data dirs and credential resolution.
- **S8.** Disabling every automation still yields a system that starts, serves the dashboard, and supports manual ingest + search.
- **S9.** Deleting the SQLite file and running `axon reindex` fully rebuilds operational + vector state from the vault (the vault is the source of truth).

## 7. Constraints & assumptions

- Single language for the daemon (Go 1.22+), shipped as one static binary; single DB file (SQLite + sqlite-vec) per profile.
- Ollama installed locally for embeddings; a Claude subscription/enterprise login per installation (no API key).
- Obsidian need not be running for automations (operate on Markdown directly).
- Near-zero capital outlay: all components open-source/free; Claude usage is covered by the existing subscription/enterprise plan (no per-token API spend in the default modes).
- Cross-platform: macOS, Linux, Windows (WSL acceptable on Windows if it materially simplifies the build — call it out).

## 8. Out-of-scope risks to note for the agent

- Community Obsidian MCP servers change quickly; AXON owns its core MCP to avoid that dependency in the critical path.
- Embedding-model changes force re-indexing; make that explicit, never silent.
- Headless `claude -p` flags and hook schemas evolve; isolate them behind a thin adapter and verify against current docs at build time.
