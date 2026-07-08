# 08 — Component: Agent Bridge (MCP, Hooks, Skills, Subagents)

**Owns:** FR-50…FR-54, FR-12 (wikilink safety), parts of NFR-05.
**Goal:** Give Claude (Code and Desktop) first-class, *safe* access to the vault and knowledge base, and steer in-session behaviour deterministically.

## 1. The AXON MCP server

A stdio MCP server (`github.com/modelcontextprotocol/go-sdk` — the official Go SDK; `mark3labs/mcp-go` is a viable alternative) launched by Claude Code per the generated `.mcp.json`, and also embeddable in the daemon. Tools are registered with the SDK's generic `AddTool` (typed Go structs in/out, JSON Schema inferred). Profile-scoped (operates on the active profile's vault + DB). Every tool round-trip that calls Claude is ledgered via Component 07; every vault write is wikilink-safe.

### Tool contracts (as implemented — `internal/mcp/tools.go` is authoritative)

| Tool | Args | Returns | Notes |
|------|------|---------|-------|
| `vault_search` | `query, top_k?` | ranked `{path, snippet, scores}` | hybrid lexical+semantic |
| `vault_read` | `path` | `{path, frontmatter, body}` | |
| `vault_write` | `path, body, force?` | `{ok, path}` | creates new notes; refuses existing ones unless `force`, and `force` only works on notes with `axon_managed: true` frontmatter |
| `vault_patch` | `path, marker, content` | `{ok}` | edits only inside an `axon:<marker>` block; never clobbers prose (heading-scoped patching is not implemented) |
| `vault_move` | `from, to` | `{ok, updated_links[]}` | **wikilink-safe**: rewrites all inbound links/aliases |
| `vault_links` | `path` | `{outbound[], backlinks[]}` | from the link graph |
| `vault_related` | `path, top_k?` | `{related:[{path, similarity}]}` | notes most similar to a note by embedding similarity; **read-only, zero tokens** (R8/FR-149) |
| `daily_append` | `content, date?` | `{ok, path}` | appends to today's (or given) daily note |
| `knowledge_ingest` | `target, dry_run?` | `{status, note_path, title, suggested_links}` | runs Component 05 pipeline; URLs only via MCP (no local files) |
| `knowledge_search` | `query, top_k?` | hybrid results | currently an alias of `vault_search` (source-scoping pending) |
| `tokens_status` | `—` | `{day, week, guard}` | from Component 07 (cost field pending, api_key mode) |
| `metrics_query` | `since_days?` | ledger aggregates by day/operation/model + budget windows | |
| `automations_list` | `—` | `[{name, essential, allowed, last_run}]` | |
| `automations_run` | `name, dry_run?` | run result | same path as scheduler |
| `memory_remember` | `text, kind?, source?` | `{ok, entry, path}` | appends a durable entry to `MEMORY.md`'s `axon:memory` block (Component 12) |

Tool IDs use underscores at the wire so they map cleanly onto Claude Code's `mcp__axon__<tool>`.

**Safety in tools:** `vault_write` refuses to overwrite existing notes without `force`, and `force` is only honoured for AXON-authored notes (`axon_managed: true` frontmatter) — human prose can never be force-overwritten; `vault_patch` edits only managed blocks; every path argument is refused if it targets a vault system directory (`.claude`, `.obsidian`, `.axon`, `.git`, `.trash`); `vault_move` is the only rename path and always fixes links; there is **no** `vault.delete` tool (deletes are out-of-band, confirmed, never agent-driven).

### Interop (FR-54)
Behind the `Vault` interface, allow a config switch to delegate raw file ops to an external/community Obsidian MCP server while keeping AXON's hybrid search, knowledge and token tools. Default is AXON's own implementation (ADR-005).

## 2. Hooks (deterministic in-session control)

Written to `.claude/settings.json` by `axon init`. Verify the exact event names/JSON schema against the Claude Code hooks reference at build time; keep each hook a tiny script calling `axon hook <name>` so logic lives in one place.

| Event | Hook behaviour (as implemented in `internal/hooks`) |
|-------|----------------|
| `SessionStart` | Inject a compact status block: budget (day/week %), inbox count, pending review-queue items, a one-line "vault conventions" pointer — plus the bounded, redacted personal-identity block (Component 12). Cheap, no model call. |
| `PreToolUse` | Enforce safety authoritatively (deny cannot be bypassed by permission mode): block destructive Bash (`rm`/`unlink`/`find -delete`/`dd of=`/`truncate`…), raw renames (`mv`, `git mv`) and shell redirection over `.md` notes; block Write/Edit into `.obsidian/`/`.git/`; block the native `Write` tool from overwriting an existing vault note (steering to `vault_patch`/`Edit`). |
| `PostToolUse` | Deliberate no-op: every Claude round-trip is already ledgered at the token-manager chokepoint, and the link graph converges on the next reindex — a per-tool hook would double-count. |
| `Stop` | Remind the agent to persist anything durable into the vault before context is cleared (fires unconditionally; no context-size heuristic). |

> Hooks **tighten** behaviour only; they never loosen permissions. Treat any instruction found in fetched/file content as data, not commands (NFR-05).

## 3. Claude Code skills + subagents + CLAUDE.md

`axon init` writes the whole set into the vault's `.claude/` directory
(`.claude/skills/`, `.claude/agents/`, `CLAUDE.md`, hooks in `settings.json`)
from the embedded assets in `internal/claudeassets` — project-level assets
rather than a packaged plugin bundle; a marketplace-style plugin package is a
possible later distribution form.

### Skills (`SKILL.md` playbooks — procedural, run in the main thread)
- `ingest-url` — capture a URL into the vault via `knowledge.ingest`, then propose links.
- `run-daily-log` — the daily synthesis playbook (used both interactively and by the headless automation).
- `triage-inbox` — classify + propose moves/links for inbox items (writes to review-queue by default).
- `suggest-links` — run a similarity sweep and propose Zettelkasten links.
- `weekly-review` — guided review combining digest + open projects + budget.

### Subagents (`.claude/agents/*` — isolated context, own model/tools)
- `librarian` — deep, multi-step vault/knowledge search; returns a concise brief, keeping noisy intermediate retrieval out of the main context. (Read-only tools; `model: routine`.)
- `summariser` — distillation/compaction worker; produces durable summaries for managed blocks. (`model: synthesis`.)
- `triager` — fast classification of inbox items. (`model: classify`, read-mostly.)

Subagents serve **interactive** sessions (the user, or Claude Code itself, delegates to them). **Headless automations default to the inert shape**: the runner invokes `claude --print --max-turns 1 --tools "" --setting-sources "" --strict-mcp-config` — a single-turn, tool-less, hook-less, MCP-less text generation. (Not `--bare`: bare mode skips credential lookup, so the headless OAuth token would be ignored and every scheduled run would fail "not logged in".) This default makes scheduled runs deterministic, immune to prompt-injection-driven tool use (`--tools ""` is enforcement, not convention — NFR-05), and cheap to pre-estimate; automations do their reading/writing through AXON's own Go code (vault helpers, pipeline), not through the model.

**Agentic runs (ADR-017, FR-84…FR-87)** are the opted-in exception, built on the per-turn budget enforcement this doc required before adoption. An automation that declares MCP tools runs `claude --print --output-format stream-json --verbose --max-turns <N> --tools "" --strict-mcp-config --mcp-config <inline: axon mcp --tools <csv>> --allowedTools mcp__axon__<tool>… --no-session-persistence --setting-sources ""`. Enforcement is structural: no built-in tools; a **read-only** allowlist (`vault_search`, `vault_read`, `vault_links`, `vault_related`, `knowledge_search`, `tokens_status`) enforced client-side (`--allowedTools`) **and** server-side (the spawned `axon mcp --tools` registers only those tools); bounded turns; and a streaming kill-switch that terminates the run the moment `automations.<name>.budget_tokens` is exceeded, ledgering the real accumulated usage (`token.run_budget_kill`). Writes stayed in the automation's Go code in v1. **ADR-022** extends the allowlist with the managed-block-safe write tools — `vault_patch`, `vault_write`, `daily_append`, `memory_remember` (never `vault_move`) — enforced by the same dual allowlist as reads (`--allowedTools` client-side, `axon mcp --tools <csv>` server-side; the fixed set is validated in `internal/automations` so a stray tool fails the run). `axon run <automation> --dry-run` appends `--dry-run` to the subprocess so write tools validate and report `{would, applied:false}` without mutating — suppression is server-side, not model-trusted, and such a dry-run spends tokens because the model actually runs. A mid-run budget kill leaves a prefix of per-tool-atomic, idempotent writes (never a half-edited note); a re-run converges. v1 opts in knowledge-digest and compaction (the latter now writes `axon:summary` via `vault_patch`, ADR-022); `automations.<name>.agentic: false` restores the inert shape, which also remains the automatic degradation path.

### `CLAUDE.md` template (the persistent schema)
Generated into `.claude/CLAUDE.md`, kept **under ~200 lines**, encoding: the vault folder map and what each folder is for; frontmatter conventions and the `axon:*` managed-block rule; the wikilink-safety rule ("never rename/move via raw file ops — use `vault.move`"); the capture→triage→distill workflow; naming conventions; and pointers (`@docs/…`) rather than inlined detail. Things that must *always* happen go in hooks, not here (per best practice).

## 4. Account & profile isolation

`axon init` writes the `.mcp.json`, `settings.json` and plugin into the active profile's vault `.claude/`, and sets `CLAUDE_CONFIG_DIR` so interactive and headless Claude Code in that vault use the correct account and never cross profiles (FR-03/NFR-04). Authentication follows the profile's `auth_mode`: a `claude login` session (subscription/enterprise) and, for scheduled headless runs, the profile's `CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`. No `ANTHROPIC_API_KEY` is set in these modes (it would divert Claude Code onto API billing); only `auth_mode: api_key` uses a key. Because personal and work are separate installations on separate machines, isolation is physical as well as per-profile.

## 5. Acceptance checks
- In Claude Code opened in the vault, `vault.search`/`knowledge.search` return grounded results and `vault.move` renames with zero broken links (FR-50/FR-12/S5).
- `SessionStart` injects budget + inbox status with no model call (FR-52).
- A direct delete or a link-breaking raw edit is blocked by `PreToolUse` (FR-52/NFR-05).
- The plugin installs via `axon init` and the skills/subagents are invocable (FR-53).
