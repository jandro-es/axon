# ADR follow-up slices — design

Date: 2026-07-04
Status: approved
FR IDs: FR-102 (link-suggester proposal memory), FR-103 (review-queue
compaction), FR-104 (SessionEnd capture)
Closes the noted follow-ups of ADR-018, ADR-020 and ADR-021.

## Goal

Close the three ADR-noted follow-ups that remained after the P1–P7
portfolio shipped, as one feature branch with three independent slices:

1. The link-suggester re-proposes the same pairs every time the embedding
   count changes — give it the resurfacer's persistent proposal memory.
2. Resolved review-queue lines accumulate in `.axon/review-queue.md`
   forever — compact them into an archive when resolutions rewrite the file.
3. Cleanly-ended sessions wait up to 30 minutes before `session-distill`
   may pick them up — wire the `SessionEnd` hook as an immediate-ready
   signal, keeping the idle heuristic as the crash fallback.

## Decisions (user-approved)

- **One branch, three slices** (`feature/adr-followups`), one spec, per-task
  commits as usual.
- **Queue compaction: prune on resolve + archive.** No new automation, no
  scheduling; resolved lines older than 7 days move to
  `.axon/review-queue-archive.md` during the rewrite a resolution already
  performs.
- **SessionEnd + idle fallback.** `SessionEnd` marks the session immediately
  ready; the Stop-hook recorder and the 30-minute idle threshold stay as the
  fallback for crashed/abandoned sessions.

## Background / constraints

- The resurfacer (ADR-018, `internal/automations/proactive.go`) already
  carries the exact proposal-memory pattern: a sorted, capped key list
  serialized as JSON into an `automation_state` cursor row
  (`resurfacer:proposed`, cap 500), loaded permissively (any load problem →
  empty map, worst case a pair is proposed twice), saved only after a
  successful queue append, never in dry-run.
- `internal/review` owns the queue file contract: `Load` parses sections and
  `- [ ]`/`- [x]` lines into typed items with resolution-stable IDs
  (`normalizeLine` strips the ` — ✓ applied / ✗ dismissed <date>` suffix);
  `mark()` flips a line via `vault.RewriteSystemFile`, which refuses any
  path outside `.axon/` (ADR-020).
- The session recorder (ADR-021, `internal/hooks`) upserts
  `{session_id → transcript_path, last_stop}` on every Stop, silently and
  with no model call; `session-distill` (`internal/automations/sessionmem.go`)
  drains sessions idle ≥ 30 minutes, once ever per session. ADR-021
  explicitly rejected SessionEnd wiring as "revisit-able".
- Standing rules that bind all three slices: hooks never call a model and
  never break a session; vault mutations stay wikilink-safe / `.axon/`-guarded;
  automations stay dry-run-clean (no state persisted).

## Design

### Slice 1 — Link-suggester proposal memory (FR-102)

**Shared helpers** (`internal/automations`): generalize the resurfacer's
pair-memory into

```go
func loadProposalMemory(ctx context.Context, rc RunCtx, stateKey string) map[string]bool
func saveProposalMemory(ctx context.Context, rc RunCtx, stateKey string, proposed map[string]bool)
```

with the existing semantics (permissive load; sorted keys; cap
`proposalMemoryCap = 500` — the constant the resurfacer already uses,
renamed from `resurfaceMemoryCap`). `loadResurfacerMemory` /
`saveResurfacerMemory` become thin calls (or are replaced outright) with the
unchanged state key `resurfacer:proposed` — existing rows keep working.

**LinkSuggester.Run** (`internal/automations/nomodel.go`):

- Load memory under the new state key `link-suggester:proposed`.
- Key candidates by the existing unordered `pairKey(from, to)` (the queue
  line renders `[[a]] ↔ [[b]]`; direction is noise). The in-run `seen` map
  switches to the same canonical key.
- Skip candidates already in memory; add newly queued pairs to it.
- Save memory only after the queue append succeeds; dry-run persists
  nothing (matches the resurfacer).

Effect: a dismissed suggestion stays dismissed; embedding growth stops
re-proposing the same pairs. `DetectChange` is untouched.

### Slice 2 — Review-queue compaction on resolve (FR-103)

**Pure function** (`internal/review`):

```go
// compact splits queue content into what stays and what archives:
// resolved lines (- [x] … ✓ applied / ✗ dismissed <date>) older than
// archiveAfterDays move out, grouped under their original section header;
// section headers left with no items are dropped from the kept content.
func compact(content string, now time.Time) (kept, archived string)
```

Constants beside `queuePath`: `archivePath = ".axon/review-queue-archive.md"`,
`archiveAfterDays = 7`. The resolution date is parsed from the suffix
`resolutionRe` already matches; a resolved line whose date fails to parse is
kept (never archive on guesswork).

**Wiring** (`mark()`): after building the rewritten content, run `compact`.
If anything archived: append the archived block (with its section headers
and an `<!-- archived <RFC3339> -->` stamp line) to
`.axon/review-queue-archive.md` **first**, then `RewriteSystemFile` the kept
content. Archive-first ordering means a crash between the two writes
duplicates an archive line at worst — never loses one. Both paths are inside
`.axon/` (the append uses the same `.axon/`-guarded write path as the queue
rewrite; producers already append to `.axon/` files).

Semantics preserved:

- Pending (`- [ ]`) lines and their sections are never touched.
- Freshly resolved lines (< 7 days) stay visible in the queue — the human
  sees recent outcomes in Obsidian.
- Item IDs are content-hashes; archived lines simply disappear from
  `GET /api/review` (they were resolved — no API change).
- A compaction/archive failure fails the resolution write with an error, as
  any rewrite failure does today; nothing half-applies (whole-file atomic
  writes unchanged).

### Slice 3 — SessionEnd capture (FR-104)

**State** (`internal/db`): `PendingSession` gains `Ended bool`
(JSON `ended`). Old rows unmarshal as `false` — no migration.

**Hook** (`internal/hooks`): new `SessionEnd = "SessionEnd"` event constant,
routed to the same recorder as Stop but setting `Ended: true` on the upsert
(a session that later somehow Stops again simply refreshes `LastStop`;
`Ended` is sticky — the recorder never clears it). Same gates
(`memory.capture_sessions`, DB present, ids present), same silence on any
failure, no model call, paths only (FR-97 semantics unchanged).

**Wiring** (`internal/claudeassets`): the generated hook settings add
`"SessionEnd": mk("SessionEnd")` beside Stop, so `axon init` / `axon mcp
install --client code` regenerate it idempotently.

**Distiller** (`internal/automations/sessionmem.go`): `readySessions`
treats a session as ready when `Ended || idle ≥ sessionIdleMinutes`. Clean
exits distill on the next 2-hour tick with no idle wait; crashed or
abandoned sessions keep the 30-minute idle path. Once-ever semantics, the
seen set, budget-defer behaviour and redaction are untouched (FR-98/FR-99).

### Error handling

All three slices inherit the surrounding posture:

- Proposal-memory load failures degrade to "may propose twice"; save
  failures log a warning (resurfacer precedent).
- Compaction failures fail the resolution visibly; the queue file is never
  left half-written (atomic whole-file writes).
- Hook failures remain silent (a hook must never break a session); a missed
  SessionEnd only means the session falls back to the idle path.

### Testing

Table-driven, per slice:

1. **Link-suggester:** run → re-run proposes nothing new; dismissal-style
   persistence (memory survives across runs via the state row); cap
   enforced; dry-run persists nothing; resurfacer behaviour unchanged
   against its existing key.
2. **Compaction:** old resolved lines archived (grouped, stamped), fresh
   resolved kept, pending untouched, emptied section headers dropped,
   unparseable dates kept, archive-append precedes queue rewrite, `Load`
   IDs of remaining items stable across a compaction.
3. **SessionEnd:** hook records `ended: true` (and respects the
   `capture_sessions` gate); `readySessions` returns an ended session
   immediately and an idle one after the threshold; old state rows without
   the field still work; `claudeassets` emits the SessionEnd hook entry
   (existing test's event list grows).

Live smoke (scratch env, isolated `AXON_HOME`): propose → dismiss → re-run
shows no duplicate; resolve an item with a back-dated resolution → archive
file appears and queue shrinks; simulate a SessionEnd hook call → session
distills on the next tick without the idle wait.

### Docs

- `docs/03-requirements.md`: FR-102 (Proactive layer table), FR-103 (Review
  actions table), FR-104 (Session memory table), each citing this spec.
- `docs/02-architecture.md`: amend ADR-018/020/021 trade-off lines when
  built (follow-ups closed; SessionEnd adopted with idle fallback).
- `CHANGELOG.md` entry with the built slices.

## Trade-offs accepted

- Proposal memory means a suggestion rejected once is never re-proposed even
  if both notes change substantially later (same trade the resurfacer made;
  the cap ages out old pairs eventually).
- The queue only compacts when something is resolved — a queue nobody
  touches keeps its resolved lines. Acceptable: resolved lines only exist
  because resolutions happen, so growth is bounded by use.
- The archive file grows without a cap. It is plain Markdown, append-only,
  human-prunable, and outside every read path (nothing parses it).
- 7 days and the archive path are constants, not config — same style as the
  resurfacer's thresholds; config sprawl avoided.

## Out of scope

- Retro-archiving on a schedule (no new automation).
- Any change to distillation itself (model, prompt, once-ever semantics).
- Compaction of the *pending* queue (only resolved lines move).
