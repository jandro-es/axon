# T2 — Actions consolidation automation — design

**Slice:** T2 (roadmap `docs/16-roadmap-1.2.5.md`) · **Date:** 2026-07-10
**FR:** FR-160, FR-161 · **ADR:** none (pure composition of the `project-pulse`
managed-block pattern + T1's `actions` index)
**Status:** design approved; ready for implementation plan.

> Current maxima before this slice: FR-159, ADR-033. After: FR-161, ADR-033
> (no new ADR).

## 1. Summary

The headline user value of 1.2.5: **one trusted, always-current list of every
action in the vault**, in GTD engage order, maintained automatically and for
free. Two pieces:

- **FR-160 — `actions-consolidate` automation.** A new **zero-model** automation
  (daily, **enabled by default**) that renders the whole T1 `actions` index into
  the `axon:actions` managed block of `01-Projects/Actions.md` — a direct clone of
  the `project-pulse` pattern (change-gated, DryRun-safe, wikilink-safe `Patch`).
  Every line is a **plain-list reference** (`- text — [[source]] · 📅 due`), never
  a duplicate checkbox, so the index can't double-count and the source line stays
  the single place a task lives.
- **FR-161 — heartbeat task counter.** The essential `heartbeat` gains a
  deterministic `tasks: N open (M overdue)` counter sourced from the index
  (`db.ListActions` + `actions.Bucket`), replacing *nothing* (it has no task
  awareness today) and giving "one grammar, one truth" its first consumer beyond
  the CLI.

**Zero model calls, one managed-block write.** No new ADR — this composes the
existing `project-pulse`/`briefing` automation shape (ADR-018) with T1's index.

## 2. Decisions (approved)

| # | Decision | Choice |
|---|----------|--------|
| 1 | Default state | **Enabled by default.** Like the other zero-model utility automations (`knowledge-reindex`, `context-export`, `budget-guard`), `actions-consolidate` ships **on** — a fresh install gets a live `Actions.md`. Zero tokens, purely additive, one note. S8 is unaffected: disabling it removes only `Actions.md`; `axon actions` still works. |
| 2 | FR-161 scope | **Heartbeat counter only.** Add a deterministic open/overdue task count to the heartbeat status line from the index. Leave `daily-log`'s model prompt alone — its "roll unfinished tasks" was always prompt-only (no Go parsing to converge), and `Actions.md` is now the durable roll surface, so `daily-log` needn't duplicate it. |

Folded in without a question (stated for the record):

- **Note path** `01-Projects/Actions.md`, sibling to `Project Pulse.md`.
- **"This week" = rolling 7 days** from today (matches the T1 `axon actions --status week`).
- **Plain-list references, not checkboxes** (constitution §3). Completing happens
  at the source (or via the T3 dashboard), so the projection carries a
  `[[source]]` link to click through, never a tickable box. (The T1 parser skips
  `axon:actions` blocks anyway, so this is a UX/clarity rule, not just a
  double-index guard.)
- **Change gate = hash of the rendered projection** (minus the volatile footer
  timestamp), so a day where no bucket actually shifts writes nothing (FR-31).

## 3. Rendering — sections & line format (FR-160)

The block is rendered from `db.ListActions(ctx, rc.DB, {IncludeAll: true})` +
`actions.Bucket(row, today)`. Sections, in GTD engage order:

| Section | Membership (open unless noted) |
|---------|-------------------------------|
| `## 🔴 Overdue` | `Bucket == overdue` |
| `## 📅 Today` | `Bucket == today` |
| `## ⏳ This week` | `Bucket ∈ {next, scheduled}` **and** `Due` within `(today, today+7]` |
| `## ▶ Next actions` | `Bucket ∈ {next, scheduled}` **and** not in "This week" — grouped by project then context |
| `## 🕓 Waiting for` | `Bucket == waiting` |
| `## 💭 Someday / Maybe` | `Bucket == someday` |
| `## ✅ Done this week` | `state == done` **and** `DoneDate ≥ today-7` (rendered as a count + a collapsed list) |

Because `Bucket` resolves `waiting`/`someday` *before* dates (the T1 precedence),
a `#waiting` task with a due date lands in **Waiting for**, not This week — the
"one bucket" model. `cancelled` and `archived` rows are omitted entirely.

**Line format** (a reference, never a checkbox):

```
- <text> — [[<source-stem>]]<· section?> <· 📅 due?> <priority-glyph?>
```

e.g. `- fix login bug — [[work]] · Sprint · 📅 2000-01-01 ⏫`. `text` is the T1
`Text` (date/priority emoji already stripped); `source-stem` is the note
basename without `.md`; `section` and the due/priority markers are appended only
when present. Within a section, lines sort by `Due` (empty last) then `Text`.

**Next actions grouping:** a `**[[project]]**` sub-heading per project (project =
the row's explicit `[[project]]` link, else the source-note stem), projects
ordered by name; within a project, lines ordered by context (`@ctx`) then text. A
project with one loose note just shows its stem as the heading.

**Empty state:** a section with no members renders its heading + `_none_` (so the
structure is always legible). If the index is **completely empty and the note
does not yet exist**, `DetectChange` returns `Changed:false` ("no actions yet") —
we don't create an empty note on a fresh vault. Once the note exists it is kept
current (an emptied vault shows all-`_none_`).

**Footer:** `_generated <YYYY-MM-DD HH:MM> UTC · N actions_`. The timestamp is
**excluded from the change-gate hash** (see §4).

## 4. The automation — `internal/automations/actions_consolidate.go` (FR-160)

A clone of `pulse.go`. Consts:

```go
const (
	actionsNotePath = "01-Projects/Actions.md"
	actionsBlock    = "actions"
)
```

```go
type ActionsConsolidate struct{}

func (ActionsConsolidate) Name() string    { return "actions-consolidate" }
func (ActionsConsolidate) Essential() bool  { return false }
```

**Shared renderer** (pure over rows + `today`, so `DetectChange` and `Run` agree):

```go
// buildActionsBody renders every section EXCEPT the footer, plus the count.
// The footer (with its volatile timestamp) is added by Run so it never affects
// the change-gate hash.
func buildActionsBody(ctx context.Context, rc RunCtx) (body string, total int, err error)
```

**`DetectChange`:** call `buildActionsBody`; if `total == 0 && !rc.Vault.Exists(actionsNotePath)` → `Changed:false`. Else `cursor := "actions:" + hashShort(body)`; if `cursor == rc.LastCursor` → `Changed:false` ("actions unchanged"); else `Changed:true, Cursor:cursor`. (No date component — the rendered `body` already reflects today's buckets, so any date-driven move changes the hash; a day with no visible change writes nothing.)

**`Run`:** honour `rc.DryRun` (early return with a `(dry-run)` change line, before any write); ensure the note via `rc.Vault.Exists`→`rc.Vault.Create(actionsNotePath, actionsNoteStub())`; `block := body + "\n\n" + footer(rc, total)`; `rc.Vault.Patch(ctx, actionsNotePath, actionsBlock, strings.TrimSpace(block))`. `EstimatedTokens: 0`.

**Stub** (`actionsNoteStub()`), mirroring `pulseNoteStub()`:

```markdown
---
title: "Actions"
type: actions
tags: [actions]
---

> AXON maintains your consolidated action list below inside the `axon:actions` block.
> These are references — tick tasks off in their source notes (linked), not here.
> Write your own notes above this line — AXON never overwrites them.
```

**Row→Action mapping:** a package-level helper `actionFromRow(db.Action) actions.Action` (the `cmd/axon` CLI has an equivalent `toActionValue`, but that's `package main` and can't be shared) — used by both the renderer and the heartbeat counter.

## 5. Heartbeat task counter (FR-161)

In `internal/automations/model.go`, `Heartbeat.Run` currently builds:

```go
line := fmt.Sprintf("inbox: %d · budget day %.0f%% week %.0f%%%s", inbox, st.Day.Pct, st.Week.Pct, guardSuffix(st))
```

Add a deterministic counter between inbox and budget:

```go
open, overdue := openTaskCounts(ctx, rc)
taskClause := fmt.Sprintf(" · tasks: %d open", open)
if overdue > 0 {
	taskClause += fmt.Sprintf(" (%d overdue)", overdue)
}
line := fmt.Sprintf("inbox: %d%s · budget day %.0f%% week %.0f%%%s", inbox, taskClause, st.Day.Pct, st.Week.Pct, guardSuffix(st))
```

`openTaskCounts` (same file or the consolidation file — same package):

```go
func openTaskCounts(ctx context.Context, rc RunCtx) (open, overdue int) {
	rows, err := db.ListActions(ctx, rc.DB, db.ListActionsOpts{State: "open"})
	if err != nil {
		return 0, 0 // heartbeat is essential — never fail on a DB hiccup
	}
	today := rc.now()
	for _, r := range rows {
		open++
		if actions.Bucket(actionFromRow(r), today) == "overdue" {
			overdue++
		}
	}
	return open, overdue
}
```

The synthesis `facts` string (model.go:161) gains a matching
`open tasks: %d (%d overdue)` line so the optional narrative can mention them,
and the noteworthy gate additionally fires when `overdue > 0` (an overdue task is
worth a nudge). **Two pinned heartbeat test strings must be updated** to the new
`line` format (`standard_test.go` ≈ lines 138 and 387) — the only test fallout.

## 6. Config & registration

- **Registry** (`internal/automations/registry.go`): one line —
  `ActionsConsolidate{}.Name(): ActionsConsolidate{},`.
- **`registry_test.go`** `want` slice: add `"actions-consolidate"` (20 → **21**).
- **Config seeds** (both, kept in sync) — `internal/config/starter.go` and
  `axon.config.example.yaml`:
  ```yaml
  actions-consolidate: { enabled: true, schedule: "0 7 * * *", model: none, budget_tokens: 0 }
  ```
  Enabled by default (decision 1); daily at 07:00; zero-model. The `work` profile
  template inherits it (no override needed — it's zero-token and local).
- **docs/04** `automations:` map is illustrative (a subset); add the
  `actions-consolidate` line there and a short narrative bullet next to
  `merge-proposals`, plus a `docs/06` automation entry.

## 7. Guardrails & invariants

- **Cardinal rule 1 (no Claude bypass):** N/A — `model: none`, `runModel` never
  called, `EstimatedTokens: 0`, no ledger entry.
- **Cardinal rule 2 (wikilink-safe):** the only write is `rc.Vault.Patch` into the
  `axon:actions` managed block (+ `Create` of the stub on first run). Human prose
  above the block is never touched; no `Move`, no raw `fs` write, no delete.
- **S8 (all-off still useful):** disabling `actions-consolidate` removes only
  `Actions.md`; `axon actions`, the index, and everything else are unaffected.
- **S9 (vault rebuilds DB):** the automation only *reads* the derived index and
  *writes* a projection note; it stores no new authoritative state. `reindex`
  rebuilds the index the automation reads.
- **FR-31 (change gate):** an unchanged projection (same tasks, same day, same
  buckets) is a hash match → skip, no write, no event.
- **NFR-06 (atomic writes):** `Patch` writes via temp+rename; a failed run leaves
  the note intact.
- **NFR-05 (content is data):** task text is rendered as data; no model call, so
  nothing is ever interpreted as instructions.

## 8. Testing strategy

- **Renderer (`buildActionsBody`), table-driven** with a real in-memory SQLite +
  seeded `db.ReplaceActions`: assert each task lands in the right section by
  bucket (overdue/today/this-week/next/waiting/someday/done-this-week);
  waiting-with-due goes to Waiting not This week; `cancelled`/`archived` omitted;
  Next-actions grouped by project then context; empty sections show `_none_`;
  Done-this-week honours the 7-day window; line format carries `[[source]]` + due
  + priority and **no `- [ ]` checkbox**.
- **Change gate:** two runs over an unchanged index → second is `Changed:false`
  (footer timestamp excluded from the hash, proven by rendering at two clocks);
  a new/edited task → `Changed:true`. Empty index + no note → `Changed:false`.
- **Run/DryRun:** DryRun writes nothing but reports the intended change; a real
  run creates the stub then patches the `axon:actions` block; human preamble
  survives a re-run; re-run with no change is a skip.
- **Heartbeat (FR-161):** seed open + overdue actions, assert the status line
  contains `tasks: N open (M overdue)`; zero tasks → `tasks: 0 open` (no overdue
  suffix); update the two pinned `standard_test.go` strings.
- **Registry:** `want` slice + count (20→21) updated; `Schedulables` includes
  `actions-consolidate` when enabled.
- **Live smoke** (real binary, isolated `AXON_HOME`, never `:7777`): seed tasks,
  `axon reindex`, `axon run actions-consolidate` → inspect `01-Projects/Actions.md`
  (correct sections, references not checkboxes, human preamble intact); re-run →
  change-gate skip; `axon run actions-consolidate --dry-run` writes nothing;
  trigger `heartbeat` and see the task counter. `env -u FORCE_COLOR`.

## 9. Build order (for the implementation plan)

1. `actions_consolidate.go`: `actionFromRow`, `buildActionsBody` (+ section/group
   helpers), `actionsNoteStub`, footer — with renderer unit tests. *(Pure; nothing
   downstream yet.)*
2. `ActionsConsolidate` `DetectChange`/`Run` + change-gate & DryRun tests.
3. Registry line + `registry_test.go` want-list (20→21).
4. Config seeds: `starter.go` + `axon.config.example.yaml` (enabled, daily,
   `model: none`).
5. Heartbeat counter (`openTaskCounts` + `line`/`facts` wiring) + the two pinned
   `standard_test.go` string updates.
6. Docs at build: `docs/03` FR-160/161 rows; `docs/06` automation entry; `docs/04`
   `automations:` map line + narrative; `docs/16` T2 marked built; CLAUDE.md
   FR range → FR-161; README/GUIDE automation count (20 → 21).

## 10. Out of scope (this slice)

- The dashboard Actions tab and the completion mutation (T3/ADR-034) — this slice
  is read-only-plus-one-managed-block, no checkbox toggling.
- Injecting task lists into `daily-log`'s model prompt (decision 2 — deferred).
- MCP tools / SessionStart pointer (T4).
- Stale sweep / weekly-review nudges (T5).
- Per-project or per-context *separate* notes (one consolidated note for v1).
- Any recurring-task (`🔁`) expansion — markers are surfaced verbatim in `text`.
