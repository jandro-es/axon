# R7 — Near-duplicate merge proposals (design)

**Date:** 2026-07-10
**Roadmap slice:** 1.2 R7 (docs/15) — the last net-new 1.2 slice; 1.1 B3 carry-over.
**Provisional IDs:** FR-154, FR-155, FR-156; **ADR-032** (the destructive-op design pass).
**Current maxima before this slice:** FR-153, ADR-031.

## Summary

An embedding sweep proposes near-duplicate note pairs to the review queue; accepting
one **merges** the pair — the closest thing to a destructive operation AXON has.
Merge is defined so that nothing is ever deleted: the survivor keeps its prose and
gains the loser's content in a managed block, every inbound link is retargeted to
the survivor, and the loser is relocated (intact, recoverable) into the vault's
`.trash/`. Detection is zero-model (pure vector math reusing the resurfacer
primitives); accept spends no tokens either. The whole slice is disabled by default
(S8) and the vault still rebuilds the DB, never the reverse (S9).

## Boundaries

Two halves, mirroring every prior proposal-style slice (resurfacer, link-suggester):

- **Detection** — a new zero-model automation `merge-proposals`
  (`internal/automations/dedup.go`), weekly, disabled by default, emitting
  `merge [[a]] + [[b]]` lines to `.axon/review-queue.md`.
- **Accept** — a new review kind `merge` (`internal/review`) whose Accept calls a
  **new `vault.Merge` primitive** (`internal/vault`). Everything novel and risky is
  isolated in that one vault function, independently unit-testable against a real
  temp vault.

No change to the token chokepoint (zero model calls anywhere in R7), no change to
retrieval, no new DB table, no new MCP tool.

## Component 1 — detection automation (`internal/automations/dedup.go`)

New automation type `MergeProposals` implementing the standard `Automation`
interface (`Name`/`Essential`/`DetectChange`/`Run`), registered in
`registry.go`/`catalog.go`, **disabled by default in both profile templates**.

- **`Name()`** → `"merge-proposals"`. **`Essential()`** → `false`.
- **Change gate:** cursor `merge:<vectorCount>:<year>-<week>` (via `db.CountVectors`
  and `rc.now().ISOWeek()`); unchanged cursor → skip. Zero vectors → not changed
  ("no embeddings yet"), exactly like the resurfacer.
- **Candidate build (zero-model):**
  1. All note stamps via `db.NotesUpdatedSince(ctx, rc.DB, "0001-01-01", N)`
     (the all-notes idiom already used by pulse), filtered through the existing
     `scannableNote` predicate (drops system dirs, `Entities/`, folder READMEs).
     `.trash/` is already excluded from `vault.List` and the index, so archived
     losers never reappear as candidates.
  2. `db.NoteMeanVectors(ctx, rc.DB, present)` for that set.
  3. **All-pairs** `db.Cosine`; keep pairs with `sim >= merge.threshold`
     (**default 0.92** — near-duplicate is a far higher bar than the resurfacer's
     0.75 serendipity). Iterate unordered pairs once (i < j).
- **Suppression:**
  - Skip pairs already `- [ ]` **pending** in the queue (`review.Load`, kinds
    `merge`), keyed by `pairKey`.
  - Skip pairs in **proposal memory** (`loadProposalMemory`/`saveProposalMemory`
    from `helpers.go`, state key `merge-proposals/proposed`, keyed by `pairKey`) so
    a **dismissed** duplicate never re-nags. Accepted pairs cannot recur — the loser
    leaves the index — so only dismissals need remembering.
  - Cap at `merge.max_proposals` (**default 5**), similarity-sorted (strongest
    first).
- **Emit:** append under a dated `## Near-duplicate merges (YYYY-MM-DD HH:MM)`
  section:
  `- [ ] merge [[a]] + [[b]] (sim 0.9x)` (both wikilinks `stripExt`,
  order-independent but rendered `a`,`b` lexically for stable output). Newly
  proposed pairs are added to proposal memory on a real (non-dry) run so the same
  still-pending pair isn't re-proposed next week if the user hasn't acted.
- **Dry-run:** returns the would-propose list in `Changes`; writes nothing (no
  queue append, no proposal-memory save).

`pairKey` and `scannableNote` are reused as-is; no new automation helpers beyond the
type itself.

## Component 2 — accept semantics: `vault.Merge` (ADR-032)

New primitive `func (v *FS) Merge(ctx context.Context, a, b string) (survivor string, err error)`
in a new file `internal/vault/merge.go`. `a`/`b` are vault-relative note paths
**with** `.md` (the review layer passes `it.Note+".md"`, `it.Target+".md"`). Survivor
is chosen **inside** the primitive (it needs live inbound-link counts), so the queue
line stays an unordered pair.

Steps, in crash-safe order (archive-first, mirroring the queue-compaction ordering —
a crash mid-way duplicates content at worst, never loses it):

1. **Validate:** both resolve via `safeAbs`, both exist, are distinct, both end in
   `.md`. Otherwise return a clear error (Accept surfaces it; the queue line is not
   flipped).
2. **Pick survivor:** count inbound wikilinks/embeds to each across all notes
   (`vault.List` + `ParseLinks`, matching by `TargetKey` on both path and basename
   forms). Survivor = max inbound count; tie → most recently updated (`ModTime`);
   tie → lexically first path. `loser` = the other.
3. **Archive the loser first:** copy the loser's **exact bytes** to
   `.trash/merged/<base>.md` via `writeRaw`. On collision, suffix with a UTC
   timestamp (`<base>-<unixmillis>.md`). The primitive reads the clock directly
   (`time.Now().UTC()`) for both the collision suffix and the merged-block date,
   matching `review.mark`'s convention; tests assert structural facts (archive copy
   exists and is byte-equal, block present and names the loser) rather than the
   exact stamp. `.trash/` is a system dir: not indexed, not swept, recoverable by
   hand.
4. **Preserve content in the survivor:** append the loser's body into the survivor's
   `axon:merged` managed block (additive; never clobbers prose — cardinal rule 2).
   Content is `### Merged from [[<loser stripExt>]] (YYYY-MM-DD)\n\n<loser body>`,
   accumulated after any existing `axon:merged` content (so repeated merges into one
   survivor stack). **Neutralize managed-block markers** in the loser body first
   (`<!-- axon:` → `<!-- axon​:` zero-width, or an equivalent inert
   transformation) so a loser's own managed blocks cannot corrupt the survivor's
   block structure. Uses the same extract-append pattern as
   `review.appendToLinksBlock`, then `v.Patch(survivor, "merged", content)`.
5. **Retarget inbound links:** stage `[[loser]]`/embed rewrites → survivor across
   **every note except the loser** (survivor **included** — its own reference to the
   loser becomes a harmless self-link `[[survivor]]`, never a dangling link into
   `.trash/`). Reuses `rewriteLinksForMove(body, loser, survivor)`. All rewrites are
   staged (read + compute) before any write, then applied atomically per file — a
   read error aborts with nothing changed.
6. **Remove** the original loser file (`os.Remove`) — its content now lives in the
   survivor's `axon:merged` block and, verbatim, in `.trash/merged/`.

Returns `survivor` (stripExt) for the resolution suffix.

### review layer (`internal/review/review.go`)

- New `mergeRe = ^merge \[\[([^\]]+)\]\] \+ \[\[([^\]]+)\]\]` parsed in `Load` →
  `Kind:"merge"`, `Note:a`, `Target:b`.
- `Kind` doc comment gains `merge`.
- `Accept` gains a `case "merge"`: `survivor, err := v.Merge(ctx, it.Note+".md",
  it.Target+".md")`; suffix `✓ merged into [[survivor]]`.
- `merge` is **not** an `Outcome` kind (no spaced-repetition feedback loop — accept
  is terminal, dismiss is remembered by proposal memory), so `Outcomes` is
  unchanged.
- Dismiss is the generic path (marks `✗ dismissed`); the detection automation's
  proposal memory keeps a dismissed pair from returning.

## Component 3 — config, doctor, toggles

- **Config (`internal/config/types.go`):** new `Merge` struct on `Profile`:
  ```yaml
  merge:
    threshold: 0.92        # min cosine to propose a near-duplicate pair
    max_proposals: 5       # cap per run
  ```
  with `MergeThresholdOr()` (default 0.92) and `MergeMaxProposalsOr()` (default 5),
  validated (`threshold` in (0,1], `max_proposals` >= 0). Absent block → defaults.
- **doctor (`internal/core/doctor.go`):** advisory `mergeCheck` (config-only,
  `StatusOK`), mirroring `resurfaceCheck`: reports whether `merge-proposals` is
  enabled and the active threshold/cap. No DB or model dependency.
- **Toggle:** `merge-proposals` omitted from the default-enabled automation set in
  both profile config templates (S8: all-off still runs and is useful). Every knob
  configurable; nothing hardcoded in logic.

## Data flow

```
weekly tick
  → merge-proposals.DetectChange (CountVectors + ISO week)
  → Run: NotesUpdatedSince(all) → scannableNote filter
        → NoteMeanVectors → all-pairs Cosine ≥ threshold
        → drop pending (review.Load) + proposed (proposal memory)
        → cap, sort, append "- [ ] merge [[a]] + [[b]] (sim)" to review queue
        → save proposal memory
user reviews queue
  → Accept(id) → review case "merge" → vault.Merge(a.md, b.md)
        → pick survivor (inbound links)
        → archive loser to .trash/merged/  (archive-first)
        → append loser body to survivor axon:merged block
        → retarget inbound [[loser]] → [[survivor]] across vault
        → remove original loser
        → mark queue line "✓ merged into [[survivor]]"
  → Dismiss(id) → "✗ dismissed"; proposal memory suppresses re-proposal
```

## Error handling

- `vault.Merge` refuses (clear error, queue line untouched) on: missing note, same
  note, non-`.md` path, or any staging read error (aborts before writing).
- Archive-first ordering guarantees no content loss on a mid-operation crash
  (worst case: loser content exists in survivor + `.trash/` + original — a later
  re-run is idempotent-safe because the pair is resolved in the queue).
- Detection tolerates a missing queue / empty proposal memory (empty → worst case a
  pair proposed twice, never a crash), matching the resurfacer's degradation.
- Zero model calls → no chokepoint, budget, or ledger interaction to fail.

## Testing

- **`vault.Merge`** (real temp vault): survivor selection (link-count, recency tie,
  lexical tie); inbound-link retargeting including the survivor self-link case;
  archive copy present and byte-equal in `.trash/merged/`; original loser removed;
  loser content in survivor `axon:merged` block; managed-marker neutralization;
  refusal on missing/same/non-md.
- **detection** (real in-memory SQLite + seeded chunk vectors + real vault):
  threshold boundary, all-pairs, pending-suppression, proposal-memory suppression,
  cap, dry-run writes nothing, change-gate skip.
- **review** merge Load/Accept/Dismiss round-trip (real vault): line parsed to
  `merge`; Accept produces the survivor suffix + calls Merge; Dismiss marks
  dismissed.
- **Acceptance gate (docs/15 R7):** duplicates surface **once** (proposal memory);
  an accepted merge leaves **zero broken links** (scan the vault for wikilinks
  resolving to a real note) and **both originals recoverable** (survivor prose +
  `.trash/merged/` copy).
- Count-assertion bumps expected from **+1 automation** (registry want-list; no new
  MCP tool this slice, so the MCP tool-count asserts are untouched — verify during
  implementation).

## Documentation

- `docs/03-requirements.md`: FR-154 (detection sweep), FR-155 (merge accept /
  `vault.Merge` destructive-op contract), FR-156 (config + doctor + default-off).
- `docs/02-architecture.md`: **ADR-032** — the destructive-op design pass (merge =
  retarget + archive + preserve, never delete; survivor by inbound-link centrality;
  `.trash/` archive; zero-model; disabled by default).
- `docs/06-component-automation-engine.md`: `merge-proposals` automation entry.
- `docs/15-roadmap-1.2.md`: R7 marked built; 1.2 net-new slate complete.
- README automation count bump.

## Non-goals (this slice)

- No model-synthesized merge (deterministic concatenation only — a destructive op
  must be predictable).
- No deletion anywhere (`.trash/` archive + retarget; never `os.Remove` without a
  preserved copy).
- No MCP `vault_merge` tool and no agent-driven merge (agents never invoke a
  destructive op; docs/15 non-goal). Merge is user-approved through the review queue
  only.
- No three-way / N-way merge (pairwise only; repeated merges into one survivor stack
  naturally).
- No new spaced-repetition feedback (accept is terminal; dismiss handled by proposal
  memory).
