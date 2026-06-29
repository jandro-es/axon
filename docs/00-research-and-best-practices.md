# 00 — Research & Best Practices

This document records the findings that the AXON design is built on, so the build agent understands the *why* behind each decision and can make consistent calls where the spec is silent. Sources were surveyed in June 2026; the landscape moves fast, so treat anything version-specific as "verify against current docs".

---

## 1. Connecting Claude to Obsidian — three paths

An Obsidian vault is just a folder of plain Markdown files. That single fact drives everything: any agent with filesystem access can already read and write it, and the notes survive even if Obsidian disappears. There are three integration paths, in increasing order of capability:

1. **Direct filesystem access.** The lowest-effort option — point Claude (Desktop or Code) at the vault folder. Good for read, draft, find-and-replace and reorganisation. **Its weakness is that it does not understand Obsidian semantics**: renaming or moving a note will silently break `[[wikilinks]]` and backlinks elsewhere in the vault. This is the single most common way these setups corrupt themselves.

2. **An Obsidian MCP server.** A small program exposing *vault-aware* operations — backlink-aware search, tag queries, daily-note append, frontmatter-safe patching, and (in the better ones) wikilink integrity on rename/move. Several community servers exist with different trade-offs:
   - Servers backed by the **Local REST API** community plugin require Obsidian to be running.
   - Servers that read **raw `.md` files directly** work with Obsidian closed, do BM25/lexical search, and (critically) handle YAML frontmatter without corrupting it.
   - Some treat the vault as a **knowledge graph** (multi-hop traversal, path-finding between concepts) rather than flat files.
   There is **no official Obsidian MCP server**; all are community-maintained and move quickly. The right posture is to treat the chosen implementation as the source of truth and re-verify it periodically.

3. **A purpose-built bridge (what AXON is).** Rather than depend on a third-party server for the core loop, AXON ships **its own MCP server** with exactly the operations the second-brain loop needs, including **wikilink-safe** writes, **hybrid (lexical + semantic) search** over both notes and ingested knowledge, and **token-status** tools. Community servers remain a valid fallback/interop target (see Component 08).

**Design takeaways**
- Treat raw filesystem writes as dangerous for anything already woven into the graph; route mutations through wikilink-safe operations.
- Don't require Obsidian to be running for automations — operate on the Markdown directly.
- Keep YAML frontmatter sacred; never let an edit corrupt it.

## 2. The "self-maintaining second brain" pattern

The classic PKM methods give you structure but **put the entire maintenance burden on the human**: review, re-link, summarise, reorganise — weekly, forever. In practice almost nobody keeps it up, and the vault decays into a graveyard. The pattern that has emerged in 2026 is to hand the *routine maintenance* to a file-editing agent. A widely-cited framing (after Karpathy's "LLM wiki" idea) is: **Obsidian is the IDE, the LLM is the programmer, the wiki is the codebase**, and `CLAUDE.md` is the persistent schema the agent reads every session (folder structure, frontmatter conventions, ingest workflow, naming rules).

This is the core thesis of AXON: **manual PKM lasts about a week; the vault becomes a true second brain only when an agent takes over the routine** — triage, linking, summarising, daily logs — while the human does the thinking and the capturing.

## 3. Second-brain methodology to encode

AXON does not invent a methodology; it encodes a pragmatic, well-trodden hybrid and lets the agent maintain it.

- **PARA** (Projects, Areas, Resources, Archive) — action-oriented, low-maintenance top-level organisation. Plus a single **Inbox** for frictionless capture. This is the *folder* layer.
- **Zettelkasten** — atomic notes connected by bidirectional links; value emerges from the *connections*, not the folders. This is the *linking* layer, and it is where AI adds the most: surfacing candidate links a human would miss.
- **CODE** (Capture → Organise → Distill → Express) — the *workflow* the automations implement: capture lands in the Inbox; organise = inbox-triage into PARA; distill = compaction/summarisation; express = digests, MOCs and exports.
- **Daily notes** — the dated entry point; a frictionless place to dump everything, which the agent later distributes via links. Doubles as a journal/timeline.
- **Maps of Content (MOCs)** — manually-or-agent-curated indexes per topic; the human-legible counterpart to the vector index.

**Anti-patterns to actively avoid (encode as guardrails):** organising everything up front, over-engineering the system, hoarding without purpose, too many tools, perfect categorisation over action. The build should ship a *lean* default vault and let it grow.

## 4. Claude Code as a programmable platform (the 2026 primitives)

Claude Code is no longer just a chat loop; it is an orchestration surface. AXON leans on these primitives (verify exact schemas against the Claude Code docs map before implementing):

- **Hooks** — deterministic scripts on lifecycle events (≈13 of them: `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`, `SubagentStop`, `Notification`, `FileChanged`, etc.). They can inject context, block tool calls, and enforce policy that the user cannot bypass by changing permission mode. Command hooks communicate via stdout/stderr/exit codes; they **cannot themselves spawn subagents** (a known limitation), but they *can* shell out. **Rule of thumb: anything that must happen 100% of the time is a hook, not a `CLAUDE.md` instruction.**
- **Subagents** — specialised agents with their **own context window**, tool allow-list, model choice, and optional persistent memory (`.claude/agent-memory/`). Their verbose intermediate work stays isolated; only a summary returns. Ideal for "noisy" side tasks (a deep search, a log analysis pass) and for parallel fan-out.
- **Skills** — `SKILL.md` playbooks; only name+description load at session start, the body loads on invocation. Best for *procedural* workflows (ingest a URL, run the daily log) that should play out the same way every time, visibly in the main thread.
- **Slash commands** — prompt templates for repeated operations.
- **Plugins** — a versioned bundle of skills + subagents + commands + hooks + MCP definitions, installable as a set. **AXON's Claude-Code surface should be packaged as a plugin** so `axon init` can install it cleanly.
- **Headless mode** — `claude -p "..."` (optionally `--agent <name>`, JSON output) runs non-interactively. **This is how scheduled automations invoke Claude.** It is the bridge between AXON's scheduler and the agent.
- **Context controls** — `/context` reports usage, `/compact` condenses a session, `/clear` resets, `/cost` reports spend. Auto-memory accumulates learnings across sessions.

**Design takeaway:** AXON's automation engine is an **external scheduler that calls `claude -p` headless** with the right skill/subagent and a token budget; AXON's in-session steering is **hooks + a plugin (skills/subagents) + `CLAUDE.md`**. Hooks do the deterministic bits (inject vault context at `SessionStart`, log tokens at `PostToolUse`, run wikilink-integrity checks before destructive file ops, nudge compaction at `Stop`).

## 5. Local RAG stack

For a single-process, cross-platform, zero-extra-infrastructure tool, the consensus 2026 stack is:

- **SQLite + `sqlite-vec`** (Alex Garcia's extension) for the vector store. One file, runs everywhere, integrates with the *same* SQLite database that holds operational metrics, supports metadata pre-filtering and optional binary quantisation for scale. This is AXON's default: **one DB file = relational tables + `vec0` virtual tables.** Alternatives if scale demands it: **LanceDB** (embedded, IVF-PQ, fast at millions of vectors) or **libSQL/Turso** (SQLite-compatible with optional sync). Avoid server-based stores (Chroma/Qdrant/Weaviate/Milvus/Pinecone) — they violate the local-first, single-process constraint.
- **Ollama** for local embeddings. Default `nomic-embed-text` (768-dim); `bge-m3` (1024-dim) for higher quality at more RAM. Notes: first call after start has a 10–30s cold start; embedding model choice is **sticky** — vectors from different models are not comparable, so changing the model forces a full re-index. The embedding provider must be an interface (Ollama default; allow an OpenAI-compatible endpoint) so the model is swappable with an explicit re-index.
- **Hybrid retrieval.** Lexical (SQLite FTS5/BM25) + semantic (sqlite-vec) with a simple fusion (e.g. reciprocal-rank). Pure vector search misses exact-term matches; pure lexical misses paraphrase. Retrieval (top-k) is also the central token-saving lever (Section 6).

## 6. Token awareness — what "token-aware, not wasting tokens" means concretely

This requirement is first-class. Mechanisms the build must implement:

- **Pre-flight counting.** Estimate input size *before* sending and refuse/downgrade/defer when it would breach a budget. With direct API access (`api_key` mode) the Messages **`count_tokens`** endpoint (`POST /v1/messages/count_tokens`) gives an exact number; on a subscription/enterprise plan (no API), use a local tokeniser estimate instead.
- **Post-hoc accounting.** Read `usage` (input, output, cache-creation, cache-read tokens) from every API response and log it per operation to SQLite. Same for headless `claude -p` runs (parse `--output-format json`).
- **Budgets.** Per-profile daily/weekly **token** budgets (which on a subscription/enterprise plan stand in for rate-limit / Agent-SDK-credit headroom; £/$ cost applies only with a direct API key), plus per-automation caps. A **budget-guard** pauses non-essential automations as the cap approaches.
- **Run on new material, not on a clock.** Automations gate on change: content hashes detect what actually changed since the last run; nothing new ⇒ no Claude call. This is the literal answer to "not just waiting/spending tokens for the sake of it".
- **Right model for the job.** Haiku for classification/triage, Sonnet for routine edits, Opus for synthesis. Encode per-automation model selection.
- **Retrieval over dumping.** Never feed the whole vault; retrieve top-k relevant chunks. Caching (embeddings, summaries) and prompt-caching guidance reduce repeat cost.
- **Compaction as a token strategy.** Distil stale session logs and long notes into durable summary notes so future context is smaller — and record the tokens saved.

## 7. Real-time graphs — two surfaces

"See graphs in real time on anything relevant" splits cleanly:

- **Operational dashboard (AXON-owned).** A local web UI streaming live metrics over SSE/WebSocket: tokens per day / per automation / per model, usage/budget gauges, automation run timeline and success rate, ingestion throughput and queue depth, vault growth (notes/links/words over time), and an interactive **knowledge graph** (nodes = notes, edges = wikilinks and high-similarity vector neighbours). Built as a small **React + Recharts** SPA, served by the Go daemon and embedded in the binary.
- **Knowledge dashboards (Obsidian-native).** The build should also generate **Dataview/Bases** dashboards and rely on Obsidian's **native Graph view** for the in-vault experience. Recommended community plugins to assume: **Dataview** (query notes as a database), **Tasks**, **Templater**, **Periodic Notes**, **Calendar**. Keep the plugin set lean.

## 8. Reproducibility & multi-profile

Requirements "easily replicable" + "personal and work, different accounts/hardware/restrictions" imply:

- **Declarative config** (`config.yaml`, at `~/.axon/config.yaml` by default) + **secrets** (`.env`/OS keychain), with **profiles** that fully isolate data dir, secrets, Claude account/credentials, automation set and a **policy** block (egress allowlist, ingestion domain allow/deny, redaction rules, budgets, which automations may run, local-only data residency).
- **Profile-scoped Claude Code config.** Separate accounts via a per-profile `CLAUDE_CONFIG_DIR`/credentials and per-profile `auth_mode` + `CLAUDE_CODE_OAUTH_TOKEN` (no API key in the default modes).
- **Idempotent bootstrap.** `axon init` is safe to re-run; it converges the environment and reports exactly what it changed. No hidden global state.
- **Cross-platform scheduling.** Prefer an in-daemon scheduler (e.g. `gocron` or `robfig/cron/v3` in Go) over OS cron so a single config works on macOS/Linux/Windows; optionally *emit* launchd/systemd/Task-Scheduler units for users who want the daemon supervised by the OS.

---

## 9. Synthesised decisions (carried into the spec)

| Concern | Decision | Rationale |
|---|---|---|
| Core language | Go 1.22+, single static binary | One self-contained binary → cleanest clone-and-run + trivial cross-compile to other machines; strong concurrency for the daemon; official Go SDKs for Claude (`anthropic-sdk-go`) and MCP (`modelcontextprotocol/go-sdk`); `sqlite-vec` via `ncruces/go-sqlite3` (pure-Go) or `mattn` (cgo). (TypeScript/Node is a viable alternative but needs a runtime; revisit via ADR.) |
| Databases | One SQLite file per profile (+ `sqlite-vec`, FTS5) | Local, single-file, relational + vector + lexical together. |
| Embeddings | Ollama, `nomic-embed-text` default, pluggable | Local, free, good enough; swappable with explicit re-index. |
| LLM / agent | Claude Code on a Claude subscription/enterprise plan (interactive + headless `claude -p`); direct Claude API optional (`api_key` mode) | The "brain"; the only required network dependency. No API key in the default modes. |
| Vault method | PARA + Inbox + Zettelkasten links + Daily notes + MOCs | Pragmatic hybrid; agent-maintained. |
| In-session steering | Hooks + a Claude Code **plugin** (skills/subagents) + `CLAUDE.md` | Deterministic guardrails + reusable playbooks + persistent schema. |
| Scheduled work | External scheduler → `claude -p` headless | Portable, observable, budgeted. |
| Dashboard | Local web app, SSE live updates + generated Dataview dashboards | Operational + knowledge surfaces. |
| Setup | `axon init`, declarative config, isolated profiles | Reproducible across machines/accounts. |

A note on "runs locally": **Claude itself runs remotely** — reached through Claude Code on your Claude subscription/enterprise login (or the Anthropic API in the optional `api_key` mode) — and is out of scope to localise. "Local" in this project means *all your data and infrastructure* (vault, databases, embeddings, scheduler, dashboard) stay on your machine. The architecture leaves a seam (the agent/embedding provider interfaces) for a fully-local model later, but the default and supported path is Claude Code. This is stated as [ADR-001](docs/02-architecture.md#adr-001-what-local-first-means-here).
