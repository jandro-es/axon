# The 25 automations

AXON ships 25 automations. Five rules govern all of them:

1. **They run on new material, not on the clock for its own sake.** Every
   automation has a change gate (content hashes, cursors, "anything new since
   last run?") and skips cleanly when nothing changed — a skipped run costs
   zero tokens.
2. **Every model call goes through the token chokepoint** (pre-flight
   estimate, budget check, post-hoc ledger). Eight automations are
   **zero-model** and never spend a token at all.
3. **Every vault write is wikilink-safe** and lands in `axon:*` managed blocks
   — human prose is never edited, and nothing is ever deleted.
4. **`budget-guard` pauses everything non-essential** when spend crosses the
   guard threshold; only `budget-guard` and `heartbeat` are essential and keep
   running.
5. **An automation with no entry under `automations:` in your config is never
   scheduled.** Toggle with `axon configure automations <name> on|off`; run
   one manually with `axon run <name> [--dry-run]`.

Work profiles add a hard gate on top: `policy.allowed_automations` is a strict
allow-list checked *in addition to* `enabled` (see `docs/PROFILES.md`).

---

## Always-on infrastructure

### `budget-guard` — on, essential, zero-model, every 15 min
Watches the token ledger; past `guard_pause_at_pct` of a budget window it
pauses every non-essential automation, and resumes them when the window rolls
over. **Does not** block interactive commands (`axon ask`, MCP tools) — by
deliberate choice it governs automations only.

### `heartbeat` — on, essential, classify tier, 09:00/13:00/17:00
Situational awareness: inbox backlog, open/overdue task counts, budget
headroom, guard state — one line to the daily note and the dashboard. **Does
not** synthesise unless there's something noteworthy; the narrative is opt-in
(`automations.heartbeat.model`).

### `knowledge-reindex` — on, zero-model, daily 02:00
Rebuilds the notes mirror, link graph and embeddings from the vault — the
nightly proof that SQLite is disposable. **Does not** modify the vault at all.

### `context-export` — on, zero-model, weekly Sun 04:00
Portable snapshot bundle (manifest + Markdown + JSON) into `.axon/exports/`.
Your exit strategy, exercised weekly.

---

## Capture & triage

### `capture` — on, zero-model, every 5 min
The inbox funnel (FR-26): own-line URLs and dropped files in `00-Inbox` are
ingested through the pipeline; originals are **archived, never deleted**.
Makes no model call of its own — enrichment only happens when
`capture.enrich: claude`, and then through the chokepoint on the routine tier.

### `inbox-triage` — on, classify tier, every 30 min
Classifies new inbox items and proposes filing destinations to the **review
queue**. **Does not** move anything itself — every move is your accept.

### `subscriptions` — on, routine tier, hourly
Polls RSS/Atom feeds added with `axon subscribe` (conditional GETs, per-tick
caps) and ingests new items. Subscribe-from-now: **does not** backfill a
feed's history.

---

## Daily rhythm

### `briefing` — on, routine tier, daily 06:00
Morning `axon:briefing` block: what changed, what's due, what needs you. The
narrative degrades gracefully under budget pressure rather than deferring.

### `daily-log` — on, routine tier, daily 21:30
Synthesises the day's activity into a summary block in the daily note.

### `memory-distill` — on, synthesis tier, daily 05:00
Maintains the durable personal-memory note: distils new material into dated
facts, detects contradictions with existing memory and routes them to the
review queue as supersede proposals. **Does not** overwrite old facts —
superseded entries are struck through with their dates, never deleted.

### `session-distill` — on, classify tier, every 2 h at :15
Turns idle Claude sessions in the vault into durable MEMORY entries — one
call per session, each session tried once ever. Gated by
`memory.capture_sessions`; redaction applies before the model sees anything.

---

## Knowledge upkeep

### `link-suggester` — on, daily 01:00
Proposes Zettelkasten links between semantically close notes into the review
queue. Candidate generation is **pure vector similarity — no model call**;
proposal memory ensures a dismissed pair is never re-proposed. It **visits
orphans first** (FR-205): the scan stops once its proposal budget is spent, so
it starts with the notes that have no links in or out rather than working
through the vault alphabetically.

### `knowledge-digest` — on, synthesis tier, weekly Mon 08:00
Weekly synthesis of newly ingested sources with MOC (map-of-content)
proposals. Agentic by default (read-only tools + managed-block writes, 8
turns), degrading to a one-shot call.

### `compaction` — on, synthesis tier, weekly Sun 03:00
Distils oversized notes into `axon:summary` blocks. Archive-first: the
original content is preserved before any write, and the human region is never
touched. Agentic by default (4 turns).

### `resurfacer` — on, zero-model, weekly Mon 07:00
Spaced-repetition resurfacing of dormant notes related to what you're working
on, on a fixed interval ladder that lengthens as you accept. The
contradiction-detection path is **dormant unless you give it budget**
(`model` + `budget_tokens > 0`) — with the default `model: none` it spends
nothing.

### `merge-proposals` — off, zero-model, weekly Mon 11:00
Near-duplicate sweep by vector cosine (≥ `merge.threshold`, default 0.92) into
the review queue. Accepting merges wikilink-safely: every inbound link is
retargeted, and the losing note is **archived to `.trash/merged/` — never
deleted**.

### `orphan-report` — off, zero-model, weekly Mon 10:00
Renders the vault's disconnected and dormant notes into an `axon:orphans`
managed block in `03-Resources/Vault Health.md`: notes with no links in or out,
and notes untouched for 180 days. **Reports only** — it proposes nothing and
spends nothing. Dormant-note proposals stay with `resurfacer`, which has its
own spaced-repetition ladder; link proposals stay with `link-suggester`, which
now visits those same orphans first. The note is created on first run with a
preamble; your own prose outside the block is never touched.

---

## GTD actions

### `actions-consolidate` — on, zero-model, daily 07:00
Renders every open task in the vault into `01-Projects/Actions.md` as a GTD
board (overdue / today / this week / next / waiting / someday). Writes
**references, not checkboxes** — the source note's checkbox stays the single
source of truth.

### `actions-review` — off, zero-model, weekly Sat 08:00
Sweeps open, undated actions in notes untouched for
`actions.stale_after_days` (30) into the review queue. Accepting tags the
action `#someday` in its source note; **does not** complete, delete or move
anything.

### `action-extract` — off, routine tier, daily 06:00
Reads recent notes for implicit commitments ("I'll send the deck Friday") and
proposes them to the review queue; accepting writes a real checkbox into the
source note's `axon:tasks` block. The only token spender in the actions
family. **Does not** write anything without your accept.

---

## Research & structure (all off by default)

### `research-questions` — off, synthesis tier, weekly Mon 08:30
Answers standing questions in `03-Resources/Research Questions.md` from the
vault (grounded-or-silent) into an `axon:answers` block. **Vault-only — does
not** fetch anything from the web.

### `deep-research` — off, synthesis tier, weekly Mon 06:00
For `#deep`-tagged questions with seed URLs: fetches the seeds through the
normal ingestion pipeline (egress policy, redaction, dedup), then one
closed-book synthesis call writes a cited report under
`03-Resources/Research/`. Bounded by `research.max_fetches` and
`research.budget_tokens`; **personal-profile-only**, and a denied host is
**never fetched**. No web tools reach the model — it only sees what the
pipeline ingested.

### `entity-pages` — off, classify tier, weekly Mon 09:00
Extracts people/projects mentioned in ≥ 2 notes into auto-maintained
`Entities/` pages with mention links. Maintains its pages directly — but only
its own managed blocks.

### `project-pulse` — off, routine tier, weekly Mon 10:00
Weekly per-project pulse (last touched, stale ≥ 21 d, linked goal) plus one
narrative into `01-Projects/Project Pulse.md`; stale projects get **one**
review-queue nudge, never repeated nags.

---

## Quality gate

### `eval-drift` — off by default
Re-runs `axon eval` when a gated local model's Ollama digest changes (FR-143),
so a silently-updated local model can't degrade a tier unnoticed. Weekly
digest check (Mon 05:00) — cheap, no model call unless a digest actually
changed, and eval calls run against the concrete **local** model only (never
Claude, zero budget spend). Does nothing unless `models.eval_min_pass` is set
(the eval gate) and a classify/routine tier points at an `ollama:` model.
Enable with `axon configure automations eval-drift on`.

---

## Summary

| Automation | Default | Model | Schedule | One line |
|---|---|---|---|---|
| `budget-guard` | on (essential) | none | `*/15 * * * *` | Pause non-essentials near budget |
| `heartbeat` | on (essential) | classify | `0 9,13,17 * * *` | Situational awareness line |
| `knowledge-reindex` | on | none | `0 2 * * *` | Rebuild index from vault |
| `context-export` | on | none | `0 4 * * 0` | Weekly portable snapshot |
| `capture` | on | none | `*/5 * * * *` | Inbox URL/file funnel |
| `inbox-triage` | on | classify | `*/30 * * * *` | Classify inbox → review queue |
| `subscriptions` | on | routine | `0 * * * *` | Poll feeds, ingest new items |
| `briefing` | on | routine | `0 6 * * *` | Morning briefing block |
| `daily-log` | on | routine | `30 21 * * *` | Evening day summary |
| `memory-distill` | on | synthesis | `0 5 * * *` | Durable memory upkeep |
| `session-distill` | on | classify | `15 */2 * * *` | Sessions → memory entries |
| `link-suggester` | on | (no call) | `0 1 * * *` | Propose links by similarity |
| `knowledge-digest` | on | synthesis | `0 8 * * 1` | Weekly source digest + MOCs |
| `compaction` | on | synthesis | `0 3 * * 0` | Distil oversized notes |
| `resurfacer` | on | none¹ | `0 7 * * 1` | Spaced-rep resurfacing |
| `merge-proposals` | off | none | `0 11 * * 1` | Near-duplicate proposals |
| `orphan-report` | off | none | `0 10 * * 1` | Orphan + dormant report (no proposals) |
| `actions-consolidate` | on | none | `0 7 * * *` | Render GTD board |
| `actions-review` | off | none | `0 8 * * 6` | Stale actions → #someday |
| `action-extract` | off | routine | `0 6 * * *` | Implicit commitments → tasks |
| `research-questions` | off | synthesis | `30 8 * * 1` | Answer standing questions |
| `deep-research` | off | synthesis | `0 6 * * 1` | Seeded, cited web research |
| `entity-pages` | off | classify | `0 9 * * 1` | People/project pages |
| `project-pulse` | off | routine | `0 10 * * 1` | Weekly project pulse |
| `eval-drift` | off | none (local eval) | Mon 05:00 | Re-eval on model digest change |

¹ contradiction path spends only if given `model` + `budget_tokens > 0`.

## Your own automations — recipes

The table above is AXON's built-in set. You can add your own without writing
Go: a **recipe** is an automation declared as data in `config.yaml` — named
inputs (a note, a search, recently-updated notes, notes gone stale, or
ingested sources), optionally one model call,
and one sink (a managed block in a note, or proposals into the review queue).
Recipes appear in `axon automations` beside the built-ins, are scheduled by an
ordinary `automations.<name>` entry, and inherit every guarantee on this page:
budget enforcement, dry-run, the change-gate that skips when nothing moved, and
wikilink-safe writing.

See the worked example in `docs/GUIDE.md` (§"Write your own automation"), the
full vocabulary in `docs/06-component-automation-engine.md` §5b, and the
commented block in `axon.config.example.yaml`.
