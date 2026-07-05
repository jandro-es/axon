# C1 — Memory consolidation with contradiction handling (design)

**Date:** 2026-07-05
**Roadmap slice:** C1 (`docs/14-roadmap-1.1.md`, Phase C — Memory & entity intelligence)
**Requirements:** FR-118, FR-119, FR-120
**ADR:** none (extends ADR-020's review-queue trust boundary; the memory
mutation stays additive — see §6)

## Problem

`memory-distill` extracts new durable facts from recent daily-note activity and
appends each to the `axon:memory` managed block via `identity.Remember`. It never
compares a new fact against what memory already holds. So when a new fact
*contradicts* an existing entry — "Uses Rust for daemons" arriving while
"Prefers Go for daemons" is already stored — **both silently coexist**, and the
session-start memory snapshot injects two conflicting statements.

C1 upgrades `memory-distill` so a contradicting fact becomes a **review-queue
reconciliation proposal** the user resolves, instead of silently landing beside
the entry it contradicts.

## Cardinal-rule & principle compliance

- **Chokepoint (rule 1):** detection folds into the existing single synthesis
  call `memory-distill` already makes through `tokens.Manager` — no new model
  call, no extra path to Claude.
- **Wikilink-safe (rule 2):** every memory-block write is a `vault.Patch` into
  the `axon:memory` managed block; human prose is never touched; there is no
  delete — a superseded entry is struck in place (tombstone), not removed.
- **New material (FR-31):** rides `memory-distill`'s existing change-gate.
- **Data not commands (NFR-05):** activity and existing entries reach the model
  through `ingestion.NeutralizeDelimiters`, framed as data.
- **Frugality:** one call, bounded input (≤ the 50-entry compact threshold);
  the same contradiction is never re-queued (proposal memory).

## Approach (decisions taken)

1. **Detection — folded into the distil call.** The one synthesis call that
   already reads recent activity also receives the current memory entries
   (numbered) and emits contradiction pairs alongside plain new facts.
2. **Accept — supersede.** The contradiction becomes a review-queue `reconcile`
   line carrying the new statement + the existing entry it conflicts with. The
   new fact is **not** written to memory until accepted (no silent coexistence).
   Accept rewrites `axon:memory`; Dismiss keeps the old entry and drops the new.
3. **Old entry on accept — tombstone.** The superseded entry stays, struck and
   dated (`- ~~…~~ (superseded YYYY-MM-DD)`); the new entry is prepended.

## Components

### 1. Distil upgrade — `internal/automations/memory.go`

`distil` builds the prompt as today plus a **numbered current-memory** section,
and asks for two output shapes:

```
- <fresh durable fact>            ← NEW facts (as today)
CONFLICT <n>: <new statement>     ← new statement contradicts existing entry #n
```

Referencing the existing entry **by number** (not by echoing its text) lets the
Go side resolve `existing[n-1]` to the exact stored fact — no paraphrase drift.

New parser (same file):

```go
// conflict pairs a newly-distilled statement with the exact existing memory
// entry text it contradicts.
type conflict struct{ New, Old string }

// parseDistillOutput splits a distil reply into plain new facts and contradiction
// pairs. existing is the current memory entry texts (bare facts, newest first)
// used to resolve "CONFLICT <n>" to the exact old text. A new fact whose text
// also appears as a conflict's New is dropped from newFacts (it is handled as a
// reconciliation, not a silent add). Out-of-range or unparseable CONFLICT lines
// are ignored.
func parseDistillOutput(text string, existing []string) (newFacts []string, conflicts []conflict)
```

`existing` is the bare fact text of each current entry, obtained by stripping the
`- DATE — ` prefix and trailing `[kind] (source: …)` metadata from
`identity.RecentEntries` output. A small helper `memoryEntryText(line string) string`
does that stripping (kept in `memory.go`; it is the inverse view of
`identity.FormatEntry`).

`distil` flow after parsing:

- Non-conflicting `newFacts` → `identity.Remember` (unchanged behaviour).
- Each `conflict` → a review-queue `reconcile` line, **unless** its
  `hash(New+"\x00"+Old)` is already in this automation's proposal memory
  (stateKey `memory-distill/reconcile`, via the FR-102
  `loadProposalMemory`/`saveProposalMemory` helpers). Newly-proposed keys are
  persisted so the same contradiction never re-queues.
- Dry-run: report counts (`would add N`, `would propose M reconciliation(s)`),
  write nothing.

Queue write (existing `rc.Vault.Append` pattern):

```
## Memory reconciliation (2026-07-05)
- [ ] reconcile: "Uses Rust for daemons" supersedes "Prefers Go for daemons"
```

Both texts are sanitized with `strings.ReplaceAll(s, "\"", "'")` before
composing, so the double-quote delimiters in the line stay unambiguous. The line
is a **single line** (the queue parser is line-based).

`RunResult.Changes` gains `MEMORY ?? "new" vs "old"` entries for the reconciles
so the run is observable in the ledger/dashboard.

### 2. Review-queue `reconcile` kind — `internal/review/review.go`

- New regex `reconcileRe = ^reconcile: "(.+)" supersedes "(.+)"$`.
- `Load`: a matching body sets `it.Kind="reconcile"`, `it.Note=<new statement>`,
  `it.Target=<old entry text>`. (Reuses the existing `Note`/`Target` fields; no
  struct change.)
- `Accept`: new `case "reconcile"` calls
  `identity.Reconcile(ctx, v, it.Target /*old*/, it.Note /*new*/, today)` where
  `today = time.Now().UTC().Format("2006-01-02")` (consistent with `mark`'s
  existing non-injected time use), then `mark(…, "✓ reconciled")`.
- `Dismiss`: unchanged — marks the line resolved without touching memory.
- `review` imports `internal/identity` for this one case. No cycle: `identity`
  imports only `vault`; `review` imports only `vault` today.

### 3. Supersede helper — `internal/identity/remember.go`

```go
// Reconcile supersedes an existing memory entry with a new one inside the
// axon:memory managed block (cardinal rule 2). It tombstones the line whose
// text contains old — striking it and appending " (superseded DATE)" — and
// prepends a fresh entry for new (source: reconcile), then re-writes only the
// block via vault.Patch. If no line matches old (e.g. compacted since the
// proposal), the new entry is still prepended and matched is false, so the
// caller can report the old entry was not struck. Already-struck lines are left
// as-is. Makes no model call. (Params are oldText/newText, not old/new, to avoid
// shadowing the `new` builtin.)
func Reconcile(ctx context.Context, v *vault.FS, oldText, newText, date string) (matched bool, err error)
```

Logic: `readBody(MemoryPath)` → `extractBlock(memory)` → line-split. Find the
first non-struck line containing `oldText`; replace it with
`- ~~<line-without-leading-"- ">~~ (superseded <date>)`. Prepend
`FormatEntry(Entry{Text:newText, Source:"reconcile", Date:date})`. `vault.Patch`
the joined block. Ensures the layer exists first (mirrors `Remember`).

## Data flow

```
daily notes ─┐
             ├─► memory-distill.distil ──► 1 synthesis call (chokepoint)
current mem ─┘        │
                      ├─ newFacts ───────► identity.Remember (axon:memory)
                      └─ conflicts ──────► .axon/review-queue.md  (reconcile lines)
                                                   │  (proposal memory dedups re-queues)
                                                   ▼
                            user / dashboard: Accept ──► identity.Reconcile
                                                          (tombstone old + prepend new)
                                              Dismiss ──► mark resolved (memory untouched)
```

## Error handling & edge cases

- **Old entry gone at accept time:** `Reconcile` returns `matched=false`; the new
  entry is still added; `Accept` succeeds and the item resolves. No crash, no
  lost fact.
- **Model emits a fact as both NEW and CONFLICT:** deduped — the conflict wins,
  the plain NEW copy is dropped, so the fact is never silently added.
- **Out-of-range / malformed `CONFLICT <n>`:** ignored by `parseDistillOutput`.
- **Embedded quotes** in either text: sanitized to `'` before composing the line.
- **Re-run before the user resolves:** proposal memory suppresses re-queuing the
  same contradiction.
- **Compact mode:** unchanged — contradictions only arise in `distil` (new
  material); `compact` shrinks old entries and never proposes reconciles.
- **Budget deferral:** unchanged — a deferred distil call proposes nothing.

## Testing

- `parseDistillOutput`: table — NEW-only, CONFLICT-only, mixed, dedup of a
  fact appearing in both, out-of-range/garbage CONFLICT ignored.
- `memoryEntryText`: strips `- DATE — ` prefix and `[kind] (source: …)` suffix.
- `distil` (fake agent): a contradiction emits a `reconcile` queue line and the
  new fact is **absent** from `axon:memory`; non-conflicting facts still land.
- Re-run: the same contradiction is not re-queued (proposal memory).
- `review.Load`: parses a `reconcile` line into `Kind/Note/Target`.
- `identity.Reconcile`: tombstones the matching old line + prepends new;
  not-found path returns `matched=false` and still prepends.
- `review.Accept` reconcile end-to-end: queue line → memory block shows tombstone
  + new entry; line marked `✓ reconciled`.
- **Live smoke:** scratch `AXON_HOME`; seed a daily note asserting a fact that
  contradicts a seeded MEMORY entry; `axon run memory-distill --dry-run` then
  real; inspect `.axon/review-queue.md` and `02-Areas/Profile/MEMORY.md`.

## Non-goals

- No auto-resolution of contradictions — the human decides (Accept/Dismiss).
- No contradiction detection outside `memory-distill` (e.g. across arbitrary
  notes) — that is a broader entity/knowledge concern (Phase C follow-ups).
- No hard deletion of memory entries — tombstone only.
- No new automation, MCP tool, ADR, or config key.

## Requirements

- **FR-118** — `memory-distill` detects, within its existing single synthesis
  call, when a newly-distilled durable fact contradicts an existing `axon:memory`
  entry (existing entries supplied numbered; conflicts referenced by number).
- **FR-119** — a detected contradiction is written to the review queue as a
  `reconcile` item (new statement + superseded entry); the new fact is not
  added to memory until accepted; accepting supersedes the old entry with the
  new one, dismissing keeps the old and drops the new — all wikilink-safe.
- **FR-120** — supersession is a tombstone (struck + dated, never deleted) so
  memory history stays auditable, and the same contradiction is proposed at most
  once (proposal memory), never re-nagging.
