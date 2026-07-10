# 16 — Roadmap 1.2.5 *(plan — "act on it")*

1.0 built the self-maintaining vault; 1.1 made it **answer**; 1.2 made it
**remember and reason** (temporal facts, contradiction-aware ask, resurfacing,
merge proposals — FR-134…156, ADR-028…032). **1.2.5 makes it act**: every
action ("task") scattered across the knowledge base — daily notes, project
notes, meeting notes, ingested material — is collected into one trusted,
always-current view, with the state (open / due / overdue / waiting / someday /
done) visible at a glance on the dashboard and inside the vault, and a
one-click way to deal with each item. Simple to use, **GTD-robust** underneath:
capture is frictionless, everything lands in one trusted system, next actions
are separated from someday/maybe and waiting-for, and a weekly-review loop
keeps the lists honest instead of letting them rot.

This is an **interposed minor** between 1.2 and 1.3: the 1.3 "reach" surfaces
(channels, meetings, multimodal, deep research, calendar) stay parked in the
vault planning notes; nothing here depends on them, and everything here rides
primitives that already shipped (reindex/content-hash change gates, managed
blocks, the review queue, the ADR-023 dashboard trust boundary, the ADR-020
mutation pattern, the chokepoint). Provisional IDs: **FR-157…170,
ADR-033/034** (current maxima before this release: FR-156, ADR-032; reassign
at build as always).

## Design constitution for actions *(read this first)*

Four decisions shape every slice below:

1. **The vault is the task database.** An action is a Markdown checkbox line
   (`- [ ]` / `- [x]`) in a note, written in the **Obsidian Tasks emoji
   grammar** (📅 due, ⏳ scheduled, 🛫 start, ✅ completed-on, ⏫/🔼/🔽
   priority) — the de-facto standard, and the Tasks plugin is already in the
   assumed lean plugin set (docs/09 §1), so everything renders and toggles
   natively in Obsidian with zero AXON lock-in. AXON adds only conventions,
   not syntax: `#someday` / `#waiting` tags for the GTD lists, `@context`
   tokens for contexts, project inferred from the containing note (an
   explicit `[[project]]` on the line wins). Plain undecorated checkboxes are
   first-class actions too — metadata is optional, never required.
2. **SQLite `actions` is a derived, disposable index** (S9, exactly like
   `memory_facts`): `axon reindex` rebuilds it byte-for-byte from Markdown;
   nothing about a task exists only in the DB. Stable identity is a hash of
   (path + normalised line text), never a line number.
3. **The consolidated view is a projection, not a copy.** The `axon:actions`
   managed block renders *references* (plain lines with `[[source]]`
   wikilinks), never duplicate checkboxes — so the index can't double-count
   and the source line stays the single place a task lives. The parser skips
   `axon:actions` blocks and fenced code everywhere.
4. **Completing a task is the one new mutation, and it gets an ADR.** Flipping
   `- [ ]` → `- [x]` in human prose is outside the managed-block rule, so
   ADR-034 records the narrow, deliberate amendment to cardinal rule 2:
   a byte-precise, user-initiated, hash-addressed, single-line checkbox
   toggle (+ `✅ date` append) — exactly what the user would do in Obsidian,
   never model-initiated, never exposed to agentic automations, stale hash →
   refusal. Everything else stays additive/managed-block as ever.

**Zero model calls in the core loop.** Parse, index, consolidate, count,
complete — all deterministic Go. The only model-touching slice (T6, implicit
action extraction) is opt-in, off by default, chokepoint-routed, and proposes
through the review queue like every other suggester (S8: all-off still works;
token frugality is a feature).

## Phase T — Actions *(build in this order)*

### T1 — Action index: grammar, derived table, CLI (M) · FR-157/158/159, ADR-033 ✅ **BUILT 2026-07-10**
**Shipped:** the pure leaf `internal/actions` (`Parse`/`Extract`/`Hash`/`Bucket`)
is the single task parser; a derived disposable `actions` table (migration
`0007`) is rebuilt in the reindex transaction (byte-equivalent, S9); `axon
actions` lists/filters/counts with `--json`; advisory `doctor` `actions` check.
All three build decisions were taken as recommended (index all-but-system-dirs
with `04-Archive/` flagged; tolerant 3-state parsing; one-bucket-by-precedence +
raw fields). Read-only, zero model calls. Final IDs FR-157/158/159, ADR-033.
**Build:** the foundation everything else reads. A tolerant parser for the
task grammar (FR-157): checkbox state, due/scheduled/start/completed dates,
priority, `#someday`/`#waiting`/`@context`/tags, the nearest enclosing
heading as `section` (an action always carries its context: source note,
section, project, dates). A derived `actions` table (FR-158; new forward-only
migration) rebuilt inside the reindex transaction (delete-all + insert, the
`memory_facts` pattern) and refreshed by ingestion/`knowledge-reindex` as
notes change; derived status is computed at read time (open, done, overdue =
due < today, due-today, deferred = start/scheduled in the future, waiting,
someday) so midnight doesn't need a writer. `axon actions` CLI (FR-159):
list/filter (`--due`, `--overdue`, `--project`, `--context`, `--status`),
counts summary, `--json`. ADR-033 records the model: checkbox lines as the
single source of truth, Tasks-grammar compatibility, the derived-index rule,
and the exclusion set.
**Decisions at build:** exclusion set (recommendation: skip `Templates/`,
`.axon/`, `.trash/`, fenced code, and `axon:actions` blocks; index
`04-Archive/` but exclude it from open views); whether `- [/]`-style
in-progress states are recognised (recommendation: tolerate, map to open).
**Gate:** a seeded vault indexes tasks with correct status/dates/contexts;
`axon reindex` rebuilds the table byte-equivalently (S9); a task edited in
Obsidian changes status on next index pass; **zero model calls**; parse of a
pathological note (huge, emoji-dense, nested lists) never errors the indexer.

### T2 — Consolidation automation (S) · FR-160/161 ✅ **BUILT 2026-07-10**
**Shipped:** a zero-model `actions-consolidate` automation (daily, **enabled by
default**), cloned from `project-pulse`, renders the T1 index into the
`axon:actions` block of `01-Projects/Actions.md` in GTD engage order as plain
`[[source]]` references (never checkboxes), change-gated on the rendered
projection hash (an unchanged day writes nothing). FR-161: the essential
heartbeat gained a deterministic `tasks: N open (M overdue)` counter from the
index; `daily-log`'s prompt stays as-is (both decisions Jandro-picked). No new
ADR. Live-smoked: correct sections/buckets, references-not-checkboxes, human
preamble intact, second run change-gate-skips, heartbeat counter live.
**Build:** the "one trusted list" — a zero-model `actions-consolidate`
automation (FR-160; daily + on-demand via `axon run`, **enabled by default**
like the other zero-model automations) that renders the whole index into the
`axon:actions` managed block of `01-Projects/Actions.md` (sibling and sibling
pattern of `Project Pulse.md`; human preamble untouched). GTD sections, in
engage order: **🔴 Overdue · 📅 Today · ⏳ This week · ▶ Next actions**
(grouped by project, then context) **· 🕓 Waiting for · 💭 Someday/Maybe ·
✅ Done this week** (count + collapsed list). Every line is a plain-list
reference: text, `[[source note]]`, section, due/priority markers — full
context, no duplicate checkboxes (constitution §3). Change-gated on a hash of
the rendered projection (FR-31 discipline: an unchanged task set writes
nothing). FR-161: the existing task-adjacent automations converge on the
index — `daily-log`'s "roll unfinished tasks" and `heartbeat`'s "today's open
tasks" count read the `actions` table instead of ad-hoc parsing (one grammar,
one truth).
**Decisions at build:** note location (recommendation above) and whether
"This week" uses ISO week or rolling 7 days (recommendation: rolling).
**Gate:** seeded tasks across ≥4 notes render into correct sections; a
completed task moves to Done on the next run; re-run with no changes is a
change-gate skip with no write; the note is useful with every other
automation off (S8).

### T3 — Dashboard Actions tab (M) · FR-162/163/164, ADR-034 ✅ **BUILT 2026-07-10**
**Shipped:** `GET /api/actions` (list + GTD counts + 30-day completion trend,
derived from the T1 index), `POST /api/actions/complete` → **`vault.CompleteAction`**
(ADR-034: the one hash-addressed byte-precise checkbox toggle `- [ ]`→`- [x]` +
`✅ date`; stale hash → 409 via `vault.ErrActionNotFound`; surgical `db.MarkActionDone`
keeps the list fresh; dashboard-only, never agentic), `dashboard.actions_enabled`
kill-switch + `/health` flag + `action.done` SSE, and an **Actions** SPA tab
(tiles + completion trend + filterable list with per-row done buttons). Both build
decisions Jandro-picked-recommended (thin endpoint — the automation re-renders
`Actions.md` on schedule; tiles+trend+list). Live-smoked end-to-end. **With T3
shipped, the T1+T2+T3 release criterion for 1.2.5 is MET.**
**Build:** the at-a-glance state and the deal-with-it loop. `GET /api/actions`
(FR-162) serving the filtered list + a counts summary (open, due today,
overdue, waiting, someday, completed-this-week, oldest-open age); an
**Actions** SPA tab with stat tiles (overdue/today/open/done-this-week), a
30-day completion trend (Recharts, `runs`/`actions` history), and the list
with status chips, project/context filters, and source-note context per row.
`POST /api/actions/complete` (FR-163): body `{path, hash}` → new
`vault.CompleteAction(path, lineHash)` — the ADR-034 toggle (exact
hash-matched line, flip checkbox, append `✅ YYYY-MM-DD`; unknown/stale hash
→ 409, nothing written). Guarded exactly like the ADR-020/023 mutations:
loopback + Host + JSON + `X-Axon-Actions` header + `dashboard.actions_enabled`
kill-switch (pointer-default-ON; `/health` carries it so the SPA hides the
tab). FR-164: SSE events (`action.done`, `actions.consolidated`) so tiles and
the activity feed move within ≤5s (NFR-07); completing from the dashboard
re-renders the consolidated note (triggers T2's automation logic directly).
**Gate:** counts match `axon actions` exactly; complete-from-dashboard flips
the source line in the vault and moves the item to Done everywhere; stale
hash → 409 and an untouched note; kill-switch off → 404 + hidden tab; header
missing → 403.

### T4 — MCP tools + session surface (S) · provisional FR-165/166
**Build:** actions where Claude and the session start are. FR-165:
`actions_list` MCP tool (default set + **agentic read allowlist** — zero-spend,
the `vault_related` precedent) so any automation/session can ask "what's
open?"; `action_complete` MCP tool (default set for interactive sessions,
**pinned OUT of the agentic allowlists** — only a human-driven session
completes tasks, the `vault_ask` precedent). FR-166: SessionStart injection
gains one deterministic pointer line when actions exist ("3 due today,
1 overdue → [[Actions]]" — the FR-89 briefing-pointer pattern, within the
existing token ceiling). Expect the usual MCP tool-count assertion bumps.
**Gate:** `actions_list` returns what the CLI returns; an agentic automation
cannot invoke `action_complete` (allowlist test); injection line appears only
when non-zero and costs no model call.

### T5 — GTD hygiene: stale sweep & weekly review (S) · provisional FR-167/168
**Build:** the reflect step that keeps the system trusted. A zero-model
weekly `actions-review` automation (off by default) sweeps open actions older
than `actions.stale_after_days` (default 30) with no due date and proposes
review-queue lines (FR-167, new kind:
`stalled action "…" in [[note]] (42d) — still relevant?`), deduped by
proposal memory (dismiss = leave it alone, re-ask per the resurfacer ladder
philosophy). Accept semantics (FR-168) are the GTD disposition: **accept →
tag `#someday`** via the ADR-034 line-edit (demote to Someday/Maybe — never
auto-complete, never delete); a variant line for old overdue items proposes
rescheduling. Consolidation (T2) already gives the weekly-review reading
surface; this slice adds the nudge.
**Decisions at build:** accept dispositions beyond `#someday` (complete /
push due date) — recommendation: start with someday-only, add dispositions
if the queue shows demand.
**Gate:** a stalled action surfaces once; dismiss keeps it out for the
interval; accept tags the source line `#someday` and it moves to
Someday/Maybe in every view; nothing proposed twice while pending.

### T6 — Implicit action extraction (S, opt-in, off by default) · provisional FR-169/170
**Build:** the capture net for actions nobody wrote as checkboxes. A
routine-tier `action-extract` automation (FR-169; **disabled by default**,
local-routable per ADR-015, chokepoint, change-gated on notes updated in the
lookback window like `entity-pages`): one structured call per new/changed
note asking for explicit commitments ("I should email John…", meeting-note
action items) with NFR-05 discipline (note content is data, never
instructions). Findings go to the review queue (FR-170:
`action "email John re contract" from [[note]]`); **accept appends the task
as a real checkbox line into an `axon:tasks` managed block in the source
note** (context stays where it arose; the parser indexes `axon:tasks` blocks
— they hold real tasks, unlike the `axon:actions` projection), from where T1
picks it up like any hand-written task. Dismiss = proposal memory.
**Gate:** a seeded meeting note yields a proposal; accept materialises an
indexed, completable task in the source note; the model never writes to the
vault directly; with the automation off, nothing anywhere references it (S8).

## Suggested build order & sizing

| Order | Slice | Size | Why here |
|-------|-------|------|----------|
| 1 | T1 action index + CLI | M | Foundation; everything reads it. ADR-033. |
| 2 | T2 consolidation automation | S | The headline user value; pure projection over T1. |
| 3 | T3 dashboard Actions tab | M | The at-a-glance ask + the one new mutation (ADR-034). |
| 4 | T4 MCP tools + session line | S | Cheap composition once T1/T3 exist. |
| 5 | T5 stale sweep / weekly review | S | GTD trust loop; rides review queue + proposal memory. |
| 6 | T6 implicit extraction | S | The only model spender; deliberately last and off by default. |

**Release criterion:** 1.2.5 ships when **T1 + T2 + T3** are done (index,
trusted list, dashboard) — that is the user's ask end-to-end. T4/T5/T6 land
as they complete; leftovers roll forward without renumbering.

## GTD mapping *(how the pieces make a trusted system)*

| GTD step | AXON surface |
|----------|--------------|
| Capture | Existing funnel (00-Inbox, `POST /api/capture`, bookmarklet) + any checkbox anywhere + T6 extraction proposals |
| Clarify | Inbox-triage (existing) + T6 review-queue accept/dismiss |
| Organize | Grammar: projects (containing note / `[[link]]`), `@contexts`, `#waiting`, `#someday`, dates (T1) |
| Reflect | T5 stale sweep + the Done/aging views (T2/T3); weekly review reads one note |
| Engage | Overdue → Today → Next ordering in the note (T2), dashboard tiles + one-click done (T3), SessionStart line (T4) |

## Config & observability (accumulated across slices)

```yaml
actions:                       # top-level profile block, all defaults shown
  note: 01-Projects/Actions.md # the consolidated projection note
  stale_after_days: 30         # T5 sweep threshold
dashboard:
  actions_enabled: true        # kill-switch for /api/actions* + the Actions tab
automations:
  actions-consolidate: { enabled: true,  schedule: "0 7 * * *",  model: none,    budget_tokens: 0 }
  actions-review:      { enabled: false, schedule: "0 8 * * 6",  model: none,    budget_tokens: 0 }
  action-extract:      { enabled: false, schedule: "0 6 * * *",  model: routine, budget_tokens: 60_000 }
```

Advisory `doctor` `actions` check (index counts, unparseable-line warnings,
consolidation staleness); every completion/consolidation is an event on the
bus (dashboard ≤5s, NFR-07); ledger untouched except T6 (which ledgers like
every chokepoint caller).

## Cross-cutting rules

- Zero model calls in T1–T5; T6 through the chokepoint, opt-in, budgeted,
  local-routable (cardinal rule 1 untouched).
- The `actions` table is derived and disposable; `axon reindex` rebuilds it
  from Markdown alone (S9). Never store task truth in SQLite.
- All writes wikilink-safe: managed blocks (`axon:actions` projection,
  `axon:tasks` accepts) or the ADR-034 hash-addressed checkbox toggle — the
  single, narrow, user-initiated exception, never agentic, never deletes.
- Every piece independently toggleable; all-off leaves a working AXON (S8);
  new automations follow the standard runner contract (change-gate, dry-run,
  locks, events).
- Each slice: brainstorm → spec → ADR → FR rows → TDD plan → inline execution
  → live smoke → merge + push (the standing cycle). Reassign provisional
  FR/ADR numbers at build.

## Explicit non-goals for 1.2.5

Recurring-task expansion (🔁 is parse-tolerated, surfaced verbatim, never
auto-generated); sync with external task managers (Todoist/Things/Reminders);
Kanban boards, time tracking, or push notifications; editing task *text* from
the dashboard (complete is the only mutation); agent-initiated completion or
deletion of any task; a dedicated tasks database (the vault is the database).
The 1.3 reach surfaces stay in the vault planning notes until they graduate
to `docs/17-roadmap-1.3.md`.
