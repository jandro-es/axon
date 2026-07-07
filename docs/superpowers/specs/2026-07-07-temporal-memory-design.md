# R1 — Temporal memory layer (design)

**Date:** 2026-07-07
**Roadmap slice:** R1 (`docs/15-roadmap-1.2.md`, Phase R — Memory & reasoning; the 1.2 headline)
**Requirements:** FR-134, FR-135, FR-136, FR-137
**ADR:** ADR-028 (temporal memory representation + derived fact index — extends
ADR-011 "the vault is the source of truth"; SQLite gains a *derived* projection
of memory facts, rebuilt from Markdown, never authoritative)

## Problem

AXON's memory is an **append-only dated log**: `02-Areas/Profile/MEMORY.md`, one
`axon:memory` managed block, newest-first lines `- DATE — text [kind] (source: …)`.
1.1's C1 added contradiction handling — a new fact that conflicts with an
existing entry becomes a review-queue `reconcile` proposal, and Accept tombstones
the old line (`- ~~…~~ (superseded DATE)`) and prepends the new one.

But the log has no *time model*. Nothing knows that "Lives in London" was true
**until** "Lives in Tokyo" replaced it — the leading date and the tombstone's
"superseded DATE" already bracket that interval, but they are prose, not data.
So: retrieval and SessionStart injection can't prefer *currently-valid* facts
over stale ones; `ask` (R2) can't reason about which of two dated claims holds
now; and there is no queryable index of what memory knows.

R1 evolves memory into **semantic facts carrying validity intervals**, with a
**derived SQLite index** for retrieval and reasoning — while keeping the vault
the sole source of truth, the format human-readable, and every existing entry
valid without migration.

## Cardinal-rule & principle compliance

- **Chokepoint (rule 1):** no new model call. Consolidation stays the single
  `memory-distill` call, moved `synthesis → routine` tier (cheaper, local-routable
  via R5 later; routine defaults to Claude). The fact *index* is built by pure
  vector/parse code — no Claude path.
- **Wikilink-safe (rule 2):** every memory write remains a `vault.Patch` into the
  `axon:memory` block; the derived index is read-only Markdown→DB and **never**
  writes to the vault. No deletes — superseded facts are tombstoned in place, now
  with an explicit interval.
- **Vault is source of truth (S9 / ADR-011):** `memory_facts` is derived and
  disposable; `axon reindex` rebuilds it byte-equivalently from the block. Delete
  the DB, `reindex`, and every fact + interval returns.
- **New material (FR-31):** consolidation rides `memory-distill`'s existing
  change-gate; the index rebuild is cheap and runs in the reindex transaction.
- **Data not commands (NFR-05):** activity + current facts still reach the model
  through `ingestion.NeutralizeDelimiters`.
- **All-off still useful (S8):** with `memory-distill` disabled, facts are still
  authored (onboarding, agentic `memory_remember`), still parse with intervals,
  still filter correctly on injection; only auto-consolidation stops.

## Approach (decisions taken)

1. **One block, richer facts.** The `axon:memory` block stays the durable fact
   store. Intervals become machine-readable as a **backward-compatible** grammar
   extension. Raw dated observations ("episodes") stay where they already live —
   daily notes and session records — which consolidation reads and promotes;
   AXON never rewrites that human prose. (Not a second `axon:episodes` block.)
2. **Consolidation extends `memory-distill`, at routine tier.** No new automation.
   `distil` promotes observations to interval-bearing facts and detects
   contradictions in its one call; the accept path closes the superseded fact's
   interval. `compact` mode is unchanged (it remains the "fold stale noise" story
   — no new review-queue pruning surface).
3. **Detection stays model-driven (whole-memory feed).** The proven C1 mechanism:
   current facts numbered into the prompt, `CONFLICT <n>` referenced by number.
   The index powers injection + R2, not detection (kept simple while memory is
   single-user-small; index-assisted pre-filtering is a deferred optimisation).
4. **Injection prefers currently-valid facts, DB-independent.** SessionStart
   filters to newest-N *open* facts by parsing the block directly — the hook
   takes no DB dependency (a hook must never fail).

## The fact grammar

**Open fact** (valid now):
```
- 2026-07-05 — Lives in Tokyo [fact] (source: [[2026-07-05]])
```
- `valid_from` = the leading date. `valid_until` = open. `[kind]` extends the
  existing set → `fact | decision | lesson | preference`. `fact` marks a durable
  state that can later be superseded; the others keep their current meaning.
- `source` may be a `[[wikilink]]` (preferred for promoted facts — points at the
  daily note the observation came from) or a plain token (`session`,
  `memory-distill`, `reconcile`, `onboarding`) as today.

**Closed fact** (superseded) — extends the C1 tombstone to be interval-explicit:
```
- ~~2026-07-05 — Lives in Tokyo~~ (until 2026-08-01; superseded by "Lives in Osaka")
```
- `valid_until` = the "until" date; `superseded_by` = the quoted new fact text
  (human-readable pointer, quotes sanitized to `'`). No fact-id machinery — the
  lean choice.

**Backward compatibility (no Markdown migration):**
- Today's `- DATE — text [kind] (source: …)` parses as an **open fact**,
  `valid_from` = leading date, kind as-is (absent kind → untyped, treated as a
  durable fact for injection).
- A legacy `- ~~…~~ (superseded DATE)` tombstone parses as a **closed fact**,
  `valid_until` = DATE, `superseded_by` = "" (unknown). Both forms round-trip.

## Components

### 1. Fact parse/format — `internal/identity/`

New value type + parser (in `remember.go` or a new `fact.go`):

```go
// Fact is the parsed view of one axon:memory line. Struck marks a tombstoned
// (superseded) fact; ValidUntil/SupersededBy are set only when Struck.
type Fact struct {
    Text         string
    Kind         string // fact|decision|lesson|preference|"" (untyped)
    Source       string // wikilink target or token
    ValidFrom    string // YYYY-MM-DD (the leading date)
    ValidUntil   string // YYYY-MM-DD or "" (open)
    SupersededBy string // quoted new-fact text or "" (unknown/none)
    Struck       bool
}

// ParseFact parses one "- …" memory line into a Fact. Legacy lines (no [fact]
// kind, bare "(superseded DATE)" tombstones) parse correctly. Returns ok=false
// for a non-entry line (blank, non-"- " prefix).
func ParseFact(line string) (Fact, bool)
```

- `FormatEntry` gains an optional `ValidFrom` on `Entry` (defaults to today,
  as `Remember` does now) and continues to emit the open-fact form.
- `tombstone(line, date, supersededBy string)` now emits
  `- ~~<inner>~~ (until <date>; superseded by "<supersededBy>")`; when
  `supersededBy == ""` it falls back to the legacy `(superseded <date>)` form so
  existing tests and hand-authored tombstones stay valid.
- `memoryEntryText` (the bare-text stripper used by `memory-distill`) is unchanged
  in contract but must also strip a trailing `(until …; superseded by …)`.

### 2. Supersede helper — `internal/identity/remember.go`

`Reconcile` closes the interval instead of only striking:

```go
// Reconcile supersedes an existing memory entry with a new one inside the
// axon:memory block (rule 2). It closes the first non-struck line containing
// oldText — rewriting it as "- ~~<inner>~~ (until DATE; superseded by \"newText\")"
// — and prepends a fresh open fact for newText (source: reconcile, valid_from=date).
// If no line matches oldText the new fact is still prepended and matched=false.
// Makes no model call. (unchanged signature)
func Reconcile(ctx context.Context, v *vault.FS, oldText, newText, date string) (matched bool, err error)
```

The only change is the tombstone text (interval + superseded-by). Accept-path
argument order in `review.Accept` is unchanged (`oldText=it.Target`,
`newText=it.Note`).

### 3. Injection — `internal/identity/render.go`

- `RecentEntries` / `Render` filter to **open** facts: parse each candidate with
  `ParseFact`, skip `Struck` and any with a non-empty `ValidUntil`, then take the
  newest N within the token ceiling (unchanged ceiling logic). Non-fact/legacy
  lines (untyped) are treated as open and included.
- No DB dependency — pure block parse, as today. If parsing yields nothing (empty
  block), behaviour is identical to current.

### 4. Derived fact index — `internal/db/` + `internal/core/reindex.go`

Migration `0005_memory_facts.sql`:
```sql
CREATE TABLE memory_facts (
  id            INTEGER PRIMARY KEY,
  text          TEXT NOT NULL,
  kind          TEXT,
  source        TEXT,
  valid_from    TEXT NOT NULL,
  valid_until   TEXT,
  superseded_by TEXT,
  struck        INTEGER NOT NULL DEFAULT 0,
  embedding     BLOB,
  line_no       INTEGER,
  updated       TEXT NOT NULL
);
CREATE INDEX idx_memory_facts_open ON memory_facts(valid_until) WHERE valid_until IS NULL;
```

- **Repository** (`internal/db/memory.go`): `ReplaceMemoryFacts(ctx, Execer,
  []MemoryFact) error` (delete-all + insert, one txn — the block is small);
  `OpenFacts(ctx, Queryer) ([]MemoryFact, error)`; `type MemoryFact struct{ … }`
  mirroring the columns. Embedding is `[]float32` (nullable).
- **Reindex step:** `core.Reindex` gains `rebuildMemoryFacts(ctx, v, tx)` — read
  `02-Areas/Profile/MEMORY.md`, `extractBlock("memory")`, `ParseFact` each line,
  `ReplaceMemoryFacts`. Runs inside the existing reindex transaction. **Never
  writes to the vault.** Embeddings: filled best-effort *after* the txn via the
  existing `ReembedPending` pattern (compute nomic vectors for facts whose text
  hash changed); a nil embedder (Ollama down) leaves them NULL — the index is
  still valid for interval/injection use.
- Deliberately **no entity/predicate columns** — that is the Graphiti-style
  modelling the PRD scopes out. Fact text + interval + embedding suffices for
  R2/R8/R9.

### 5. Consolidation tier — `internal/automations/memory.go`

- `distil` and `compact` `runModel` calls: `ModelKey: "synthesis" → "routine"`.
- Promoted facts written via `identity.Remember` now pass `Kind: "fact"` and
  `Source` = a `[[daily-note]]` wikilink when the promotion is traceable to one
  note (else `memory-distill`), `ValidFrom` = run date.
- Everything else (change-gate, proposal memory, `CONFLICT n` parsing, dry-run
  counts) unchanged.

### 6. doctor — `internal/core/` (or wherever checks live)

`memoryFactsCheck`: opens the DB read-only, reports `N facts (M open / K
superseded)` and flags any block line that fails `ParseFact` (a parse anomaly =
someone hand-edited a fact into an unparseable shape) — advisory, never fatal.

## Data flow

```
daily notes / sessions (episodes, raw) ─┐
                                        ├─► memory-distill (routine tier, 1 call, chokepoint)
current facts (numbered) ───────────────┘        │
                                                  ├─ new facts ─► identity.Remember
                                                  │               ([fact], valid_from, [[source]])
                                                  └─ CONFLICT n ─► review-queue reconcile line
                                                                          │ (proposal memory dedups)
                                                                          ▼
                                             Accept ─► identity.Reconcile
                                                        (close old interval + prepend new)

axon:memory block ──ParseFact──► rebuildMemoryFacts (in reindex txn, read-only)
                                        │
                                        ▼
                                 memory_facts (derived index) ──► SessionStart? NO (parses block)
                                                                └► R2 ask / R8 related / R9 (later)
```

## Error handling & edge cases

- **Legacy entries** (pre-R1): parse as open facts / legacy tombstones; injection
  includes them; the index rebuild handles them. No migration, no breakage.
- **Old fact gone at accept time:** `Reconcile` returns `matched=false`, still
  prepends the new fact — unchanged C1 behaviour.
- **Hand-edited unparseable line:** `ParseFact` returns `ok=false`; reindex skips
  it (not indexed); `doctor` surfaces it; injection skips it. Never crashes.
- **Ollama down at reindex:** facts index with NULL embeddings; interval/injection
  paths unaffected; embeddings backfill on the next reindex with Ollama up.
- **Superseded-by quote injection:** the quoted new-fact text is sanitized (`"`→
  `'`) before composing the tombstone, so the annotation can't break parsing.
- **Two supersessions of the same subject:** each Accept closes the then-current
  open fact; older tombstones are left as-is (audit chain by date).
- **Reindex determinism (S9):** `ReplaceMemoryFacts` is delete-all+insert ordered
  by block position (`line_no`), so a rebuild is row-for-row identical to the
  block — asserted by test.

## Testing

- `ParseFact`: table — open fact, closed fact (new + legacy tombstone form),
  untyped legacy line, each `[kind]`, wikilink vs token source, non-entry line
  (ok=false), embedded quotes.
- `FormatEntry`/`tombstone` round-trip: `ParseFact(FormatEntry(e))` recovers the
  fields; `tombstone` with/without superseded-by emits the right form.
- `Reconcile`: closes the interval (`until DATE; superseded by "new"`), prepends
  open new fact; not-found → `matched=false` still prepends; legacy fallback when
  superseded-by empty.
- `Render`/`RecentEntries`: superseded/closed facts excluded from injection;
  newest-N open facts within ceiling; legacy untyped lines still included.
- `rebuildMemoryFacts`: block → rows exactly (order, intervals, struck flag);
  **asserts the vault file is byte-unchanged** after reindex; delete-DB→reindex
  reproduces identical rows; unparseable line skipped.
- `memory-distill` (fake agent): promotes a fact with `[fact]`+valid_from+[[src]]
  at **routine** tier (assert the tier via a fake that records `ModelKey`); a
  contradiction still queues a reconcile; accept closes the interval.
- `doctor memoryFactsCheck`: counts open/closed; flags a seeded bad line.
- **Live smoke:** scratch `AXON_HOME`; seed MEMORY with a legacy entry + a daily
  note asserting a superseding fact; `axon run memory-distill --dry-run` then
  real; Accept the reconcile; `axon reindex`; confirm the block shows the closed
  interval, `memory_facts` has one open + one closed row, and SessionStart
  injection shows only the current fact. (Model path needs Claude/Ollama auth —
  covered by fake-agent units where absent.)

## Non-goals

- No entity/predicate graph, no bi-temporal (transaction-time) axis — intervals +
  supersedence only (PRD risk mitigation).
- No second `axon:episodes` block and no new episode-pruning writes — episodes are
  the existing raw notes; `compact` remains the folding story.
- No R2 ask-integration here — R1 only builds the index R2 will query.
- No SessionStart DB dependency — injection parses the block.
- No new automation, MCP tool, or config key (tier change rides `models.routine`;
  the index is always built, cheaply).
- No deletion — tombstone + interval close only.

## Requirements

- **FR-134** — the `axon:memory` grammar carries machine-readable validity
  intervals: an open fact's `valid_from` is its leading date; a superseded fact is
  tombstoned with `(until DATE; superseded by "…")` giving `valid_until` +
  superseded-by; the extension is backward-compatible (legacy entries and legacy
  `(superseded DATE)` tombstones parse correctly with no Markdown migration).
- **FR-135** — a derived `memory_facts` SQLite table (text, kind, source,
  valid_from, valid_until, superseded_by, struck, embedding) is rebuilt from the
  `axon:memory` block during `axon reindex` as a read-only Markdown→DB pass that
  never writes to the vault; deleting the DB and reindexing reproduces it exactly
  (S9).
- **FR-136** — supersedence is interval-aware: accepting a `reconcile` proposal
  closes the superseded fact's interval (sets `valid_until` + superseded-by,
  tombstoned in place, never deleted) and prepends the new open fact; `memory-distill`
  runs consolidation at the routine tier through the chokepoint.
- **FR-137** — SessionStart memory injection prefers currently-valid facts —
  superseded/closed facts are excluded — selecting the newest open facts within
  the existing token ceiling, parsing the block directly with no DB dependency.
