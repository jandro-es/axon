# A3 — Standing Research Questions — Design

**Status:** approved (design), pending spec review
**Date:** 2026-07-05
**Roadmap:** `docs/14-roadmap-1.1.md` Phase A, slice A3
**New IDs:** FR-116, FR-117 (A3's provisional FR-113 was reused by B1); **no new ADR**

## Goal

Let the user keep a list of standing research questions in a managed vault note;
a weekly automation attempts a grounded answer to each open question from the
whole vault, writing answers + citations + a confidence marker into an
`axon:answers` managed block. Unanswered questions persist and are re-attempted
as the vault grows. The feature is off until the note exists and has questions,
and deleting the note disables it cleanly.

## Non-goals

- No new retrieval path — answers come from the existing whole-vault `ask`
  engine (A1), not a time-scoped "this week's material only" query.
- No second model call for confidence — it is derived from the `ask.Answer`.
- No edits to the human region of the note (cardinal rule 2): AXON never checks
  off bullets or rewrites prose; question status lives only in the answers block.
- Not folded into `knowledge-digest` — it is a separately toggleable automation.

## What it composes (no new engine)

- **`ask.Ask(ctx, ask.Deps{Searcher, Manager, Config}, question, topK) → ask.Answer`**
  — the A1 grounded-or-silent engine, unchanged. `RunCtx` already carries
  `Searcher`, `Manager`, `Config`. Every call is a synthesis-tier chokepoint
  spend (cardinal rule 1). A `Refused` answer *is* "still open".
- **`vault.FS.Patch(ctx, rel, block, content)`** — wikilink-safe write into the
  `<!-- axon:answers:start/end -->` region (cardinal rule 2), the same primitive
  `memory.go` uses.
- **`db.CountSourcesSince(ctx, db, weekStartRFC3339)`** — the digest's
  new-material probe, reused for the change-gate.
- The ADR-016/017 automation framework: `Name/Essential/DetectChange/Run`,
  `LastCursor`, dry-run, budget deferral, event plumbing.

## The note contract — `03-Resources/Research Questions.md`

Two regions:

```markdown
# Research Questions

Ask AXON standing questions here — write each as a list item ending in "?".
AXON re-attempts open ones every week as your vault grows. It never edits
above this line; answers appear in the managed block below.

- How does spaced repetition interact with my note-taking workflow?
- What did I conclude about SQLite vs Postgres for local-first apps?

<!-- axon:answers:start -->
<!-- axon:answers:end -->
```

**Parsing (human region only, deterministic):** a *question* is a top-level list
item (`- `, `* `, or `- [ ] ` / `- [x] `) whose text contains a `?`, read from
the body **above** the `<!-- axon:answers:start -->` marker. Everything at/after
the marker is ignored on read (AXON's own output is never re-parsed). Non-list
prose is ignored, so the user may write context freely. The checkbox state is
never modified.

**Rendering into `axon:answers`** — the block is rebuilt whole each run, one
entry per question in list order:

```markdown
### <question text>
<marker> · sources: [[path/a]], [[path/b]]

<answer text>
```

or, when refused:

```markdown
### <question text>
🔍 **Open** — no grounded answer in the vault yet; will re-attempt next week.
```

A footer line closes the block: `_Updated <weekStart YYYY-MM-DD> · <A> answered · <O> open_`.

**Confidence marker (derived, no extra model call):**
- `Refused` → `🔍 **Open**`
- answered, exactly 1 citation → `📝 **Tentative**`
- answered, ≥2 citations → `✅ **Answered**`

Citations are the wikilinks `ask.Answer.Citations` already returns (a subset of
the retrieved sources).

## The automation — `research-questions`

- **`Name()`** → `"research-questions"`; **`Essential()`** → `false`.
- **`DetectChange`:** if the note is absent or parses to zero questions →
  `Changed: false` (feature off / nothing to do). Otherwise the cursor is
  `hash(questionList) + ":" + weekStart(YYYY-MM-DD) + ":" + sourceCountThisWeek`.
  Changed when the cursor differs from `rc.LastCursor` — i.e. the question list
  changed *or* new sources arrived this week. Skips (zero tokens) otherwise.
- **`Run`:**
  1. Read + parse the note's questions (bail to a no-op summary if absent/empty).
  2. On `rc.DryRun`: return `Summary: "would answer N question(s)"` with a
     token estimate; write nothing.
  3. For each question, call `ask.Ask`. Accumulate `(question, Answer)`. On a
     returned **error** (including budget exhaustion — the chokepoint rejects at
     the local pre-flight, spending nothing), render that question as `🔍 Open`
     and **continue** — a single failure never aborts the batch. A `Refused`
     answer is likewise Open. (Under a fully-spent budget every remaining call
     is a cheap local rejection, so the loop is safe to run to completion.)
  4. Render the `axon:answers` block from the accumulated results and
     `rc.Vault.Patch(...)` it.
  5. Return `RunResult{Summary: "answered A/N research question(s)", Changes:
     [notePath], EstimatedTokens: sum}`.

Because the block is rebuilt whole and Open questions are re-attempted next run,
the automation is idempotent with no partial-state bookkeeping.

## Config

Add to `axon.config.example.yaml` (personal profile) and the starter template:

```yaml
research-questions: { enabled: true, schedule: "30 8 * * 1", model: synthesis, budget_tokens: 150_000 }
```

Monday 08:30 — just after `knowledge-digest` (08:00). Default-enabled is safe for
S8: with no note (or no questions) it is a no-op, so a fresh clone does nothing
until the user opts in by writing a question. In the locked-down `work` profile
it is disabled by default, like `knowledge-digest` (`research-questions: { enabled: false }`).

## Scaffolding

`axon init` seeds an **inert template** at `03-Resources/Research Questions.md`:
the instructions, an empty `<!-- axon:answers:start/end -->` block, and example
questions inside a fenced code block (so they are NOT parsed as live questions
and spend nothing). Discoverable, but silent until the user writes a real
question in the list. Re-running init never clobbers an existing note.

## Registration & wiring

- Register `ResearchQuestions{}` in `internal/automations/registry.go`.
- Add a one-line description in `internal/automations/catalog.go`.
- Implement in a new `internal/automations/researchquestions.go` (keeps
  `model.go` from growing; mirrors the existing per-automation file style where
  present).

## Testing

- **Parse:** human-region list items ending in `?` become questions; prose,
  non-`?` bullets, and anything inside the answers block are ignored; checkbox
  bullets parse by text and are never mutated.
- **Absent/empty note:** `DetectChange` → not changed; `Run` → clean no-op.
- **Change-gate:** same list + same week + same source count → not changed;
  editing the list OR a new source → changed (cursor differs).
- **Answer + render:** with a seeded vault + a fake agent returning a cited
  answer, the `axon:answers` block gains an `✅/📝` entry with the citation
  wikilinks; a refused question renders a `🔍 Open` entry; footer counts match.
- **Idempotent rebuild:** a second run replaces the block (no duplication) and
  re-attempts open questions.
- **Dry-run:** reports "would answer N" and writes nothing.
- **Cardinal rule 2:** the human region (including checkbox state) is byte-for-
  byte unchanged after a run; only the managed block changes.
- **Registry/scheduler:** `research-questions` appears in the registry and is
  schedulable when enabled; disabled/absent in the `work` allowlist path.
- **Scaffold:** init writes the template with an empty answers block and the
  example questions fenced (parsing the scaffolded note yields zero live
  questions).

All suites run with `env -u FORCE_COLOR go test ...`.

## FR mapping

- **FR-116** — Standing-research-questions automation: weekly, change-gated on
  (question-list hash ∨ new sources), one grounded `ask` per open question
  through the chokepoint, answers + citations + derived confidence rendered into
  the `axon:answers` managed block; deferral-safe and idempotent.
- **FR-117** — Note contract & clean disable: human-region list parsing (items
  ending in `?`, answers block ignored on read), absent-note/empty-list → feature
  off, dry-run reports without writing, cardinal-rule-2 (human region never
  edited), inert scaffolded template.
