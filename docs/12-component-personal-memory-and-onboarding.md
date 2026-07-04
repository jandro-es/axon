# 12 — Component: Personal Memory, Identity & Onboarding *(Phase 8 — built)*

**Owns:** FR-70…FR-73, NFR-14, ADR-011.
**Goal:** Make AXON a second brain that *knows the user* — a persistent identity
and memory the agent carries into every session — without breaking the two
cardinal rules or the "vault is the source of truth" principle.

> Status: **built (Phase 8).** Implemented in `internal/identity` (layer
> generation, bounded render, wikilink-safe `Remember`), the `axon onboard`
> wizard (`cmd/axon/onboard_cmd.go`), the extended `SessionStart` hook
> (`internal/hooks`), the `memory_remember` MCP tool (`internal/mcp`) and the
> `memory-distill` automation (`internal/automations`). It reuses the Phase 5
> agent bridge (hooks + MCP + `claudeassets`) and the Phase 3 token manager.
>
> Implementation note: the MCP tool is named `memory_remember` (underscore, per
> the `mcp__axon__*` convention); `memory.remember` below is the conceptual name.
> SessionStart injection is governed by `profiles.<p>.memory` (`inject`,
> `session_tokens`, `recent_entries`).

![Personal memory & identity layer](diagrams/personal-memory.svg)

## 1. The identity layer

Three plain-Markdown notes under `02-Areas/Profile/` (PARA "areas" — ongoing,
human-owned). They are the vault's most personal data; they are durable, portable
and editable in Obsidian, and rebuildable with the vault (ADR-006, ADR-011).

```
02-Areas/Profile/
  USER.md     # who the user is
  SOUL.md     # the agent's persona
  MEMORY.md   # durable decisions, lessons, learned preferences
```

### 1.1 `USER.md` — the user profile
Frontmatter `type: user`. Human-readable sections AXON reads by heading:

```yaml
---
title: "User profile"
type: user
updated: 2026-06-28
---
## Identity
name: …
role: …
timezone: …
## Working style
communication: "concise, no preamble; bullet points"
## Now
goals: [ … ]            # current objectives
people: [ "[[…]]" ]     # key collaborators (wikilinks)
projects: [ "[[…]]" ]   # active projects (wikilinks)
tools: [ … ]
```

### 1.2 `SOUL.md` — the agent persona
Frontmatter `type: soul`. The agent's name, voice/tone, values and **boundaries**
(what it should and shouldn't do). This is steering, not data — but it is still
*the user's* steering, edited by the human.

### 1.3 `MEMORY.md` — durable personal memory
Frontmatter `type: memory`. A running, append-only list of **decisions, lessons
and learned preferences** inside an `axon:memory` managed block, newest first:

```markdown
<!-- axon:memory:start -->
- 2026-06-28 — Prefers Go over Python for daemons. (source: session)
- 2026-06-27 — Decided AXON's vector store stays brute-force until 10^5 chunks. (ADR-010)
<!-- axon:memory:end -->
```

Only the managed block is machine-maintained; prose outside it is the human's
(cardinal rule 2). Entries are short, dated, and may cite a `[[note]]` / ADR.

## 2. The onboarding wizard (`axon onboard`)

Interactive, idempotent, **no model call** — it is an interview, not a generation.
It is the single entry point that sets the initial values for the identity layer
(#1) *and* offers to wire additional Claude clients (#2 / Component 13).

Flow:
1. **Detect.** If `02-Areas/Profile/` is absent → first-run onboarding. If present
   → "update" mode (show current values, edit only what the user changes).
2. **Interview.** Prompt for: name, role, timezone, communication preferences,
   top 1–3 current goals, key people/projects; then the agent's name, tone and
   boundaries. Each prompt has a sensible default and may be skipped.
3. **Write.** Populate `USER.md`/`SOUL.md` and seed `MEMORY.md` via the vault's
   wikilink-safe creation helpers — **never clobbering** existing content
   (converge; ask before overwrite).
4. **Wire clients (optional).** Offer to register the AXON MCP server with Claude
   Code (already done by `axon init`) and/or **Claude Desktop** (Component 13).
5. **Summary.** Report what was created vs updated; point to the files (editable
   any time in Obsidian).

`axon init` detects a missing identity layer and prints: *"Run `axon onboard` to
teach AXON who you are."* Onboarding is never required for the rest of the system
to work (S8 still holds).

`--json` emits the resulting profile (secret-free) for scripting; `--non-interactive`
with flags/`--from <file>` allows unattended setup.

## 3. Session injection (the agent "knows me")

The existing `SessionStart` hook (Component 08) is extended: in addition to the
budget + inbox status, it injects a **compact, token-bounded** rendering of the
identity layer with **no model call** (FR-72):

- `USER.md` Identity + Working style + Now (goals/people/projects).
- `SOUL.md` persona summary (name, tone, boundaries).
- The most recent N `MEMORY.md` entries (default 10), oldest dropped first.

The block is bounded by a configurable ceiling (`profile.memory.session_tokens`,
default ~1,500 tokens). If the layer exceeds the ceiling, the injection truncates
the `MEMORY` tail and notes that fuller memory is searchable via `vault_search`.
This is deterministic and free — it reuses the hook AXON already owns.

## 4. Memory maintenance

- **`memory.remember` MCP tool** — the agent records a durable fact/decision/
  lesson: `memory.remember { text, kind?: decision|lesson|preference, source? }`.
  It prepends a dated entry to the `axon:memory` block via `vault.patch`
  (wikilink-safe, never touches human prose). This is how memory grows during
  interactive work.
- **`memory-distill` automation** (optional, scheduled) — distils recent daily
  notes + session activity into a few durable `MEMORY` entries, and compacts an
  over-long `MEMORY` block into a summary (compaction-style), recording
  tokens-saved. It runs **through the token manager** (cardinal rule 1), gated on
  new activity (change-gate), `synthesis` model, dry-run aware.

Captured/distilled text is treated as **data, not instructions** (NFR-05): the
distillation prompt fences source material and never executes it.

### Session capture (ADR-021, FR-97…FR-99)

Memory also grows from the sessions themselves. The Stop hook records each
finished vault session — `{session_id → transcript_path, last_stop}` into
`automation_state`, paths only, never content, every failure silent — and the
`session-distill` automation later distills each session once it has been
idle 30+ minutes: one classify-tier chokepoint call per session (local-routable
under ADR-015), extracting up to 3 `decision | lesson | preference` items that
land via `identity.Remember` as dated entries with `(source: session)`. The
SessionStart injection then surfaces them to every future session, and
`memory-distill`'s compaction curates them over time. Each session is distilled
**once ever**; a budget defer leaves the remainder pending for the next tick.
Recording is gated by `memory.capture_sessions` (on by default; a stricter
profile sets it to `false`), only vault sessions are wired to the hook, and
profile redaction rules apply before any transcript text reaches the model.

## 5. Safety & privacy (NFR-14)

This is the most personal data in the vault, so:
- It lives **only** in the vault (local Markdown); there is no separate identity
  store and no cloud sync introduced.
- It reaches the model **only** as the bounded `SessionStart` context (which the
  user can trim by editing the files) or when the user explicitly retrieves it.
- **Redaction** (`policy.redaction_rules`) is applied to the injected block before
  it can leave the machine, exactly as for ingestion.
- It is **never** written to logs, events, the `token_ledger`, or `axon export`
  bundles. `axon export` references the profile notes by path, never inlines them.
- The work profile may disable session injection entirely
  (`profile.memory.inject: false`) for stricter environments.

## 6. Acceptance checks
- `axon onboard` on a fresh vault creates `USER.md`/`SOUL.md`/`MEMORY.md` with the
  interviewed values; a second run converges and never clobbers human edits (FR-71).
- A Claude Code session's `SessionStart` injects the user profile + persona +
  recent memory, **with no model call**, within the token ceiling (FR-72).
- `memory.remember` adds a dated entry inside the `axon:memory` block, leaving
  surrounding prose untouched (FR-73 / cardinal rule 2).
- The identity layer never appears in logs/events/ledger/exports (NFR-14).
- With onboarding skipped, the system still starts, serves the dashboard and
  supports manual ingest/search (S8 preserved).
