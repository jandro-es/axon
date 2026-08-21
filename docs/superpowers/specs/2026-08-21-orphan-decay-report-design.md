# Orphan & decay report — making disconnected notes actionable (design)

**Status:** approved 2026-08-21 (all three decisions Jandro-picked-recommended:
a report note *plus* orphan-first ordering in the existing link-suggester,
rather than a duplicate proposal automation; an orphan is a note with no
resolved inbound *and* no resolved outbound link; dormancy is reported but
proposes nothing). **FR-204, FR-205; no new ADR, no migration.** Graduates
`docs/19` **E1**, and closes `docs/20` **C2 Priority 3** by composition.

**No migration.** Schema stays `0007`. Built-in automations go 24 → **25**.

## The idea

`docs/19` E1 asks for "a zero-model weekly sweep pairing each orphan with its
nearest vector neighbours and proposing `link` items or `merge` items — pure
composition of two shipped accept paths." Reading the code first changed the
shape of the answer, and the reframing is the substance of this spec.

**Most of E1 is already shipped.** `link-suggester` (`internal/automations/
nomodel.go`) already proposes `pair` items from search neighbours with
proposal memory; `merge-proposals` (`internal/automations/dedup.go`) already
proposes `merge` items from cosine similarity with the same dedup discipline.
Both accept paths (`appendToLinksBlock`, `vault.Merge`) are live. A third
automation proposing links from vector neighbours would mean three dedup
stores that can disagree about the same pair.

**The actual gaps are different from the roadmap's framing:**

1. **Orphans have no Go-side existence.** They are computed in the SPA
   (`web/src/panels/Graph.jsx`, `degree === 0` over the graph payload). No
   `db` query returns them, so no automation — and no recipe — can act on
   them.
2. **`link-suggester` scans lexically and then stops.** `Run` does
   `rc.Vault.List(ctx)` → `sort.Strings(paths)`, then breaks once
   `MaxSuggestions` (default 10) is reached. It works through the alphabet
   and quits, so an orphan named `Zettelkasten.md` is never reached in a
   vault of any size. **The notes that most need links are the least likely
   to get proposals.** That is a defect in shipped behaviour, not a missing
   feature.
3. **Nothing renders orphans into a note**, which is precisely why `docs/20`
   C2 Priority 3 (a link-topology recipe reader) exists.

So E1 becomes two small things — a report and a reordering — instead of one
duplicate automation.

## FR-204 — `db.OrphanNotes` and the `orphan-report` automation

### The query

```go
// OrphanNotes lists notes disconnected from the wikilink graph: no resolved
// inbound link and no resolved outbound link (FR-204), newest first.
func OrphanNotes(ctx context.Context, q Queryer2, limit int) ([]NoteStamp, error)
```

```sql
SELECT id, path, COALESCE(title,''), COALESCE(updated,'') FROM notes n
 WHERE NOT EXISTS (SELECT 1 FROM links l
                    WHERE l.src_note_id = n.id AND l.kind <> 'tag'
                      AND l.dst_note_id IS NOT NULL)
   AND NOT EXISTS (SELECT 1 FROM links l
                    WHERE l.dst_note_id = n.id AND l.kind <> 'tag')
 ORDER BY updated DESC, path
```

Two judgment calls, made explicit so nobody "fixes" them later:

- **Tag edges do not count.** The `links` table stores `kind` as
  `wikilink | embed | tag`; a tag is not a link *to a note*, and the SPA's
  orphan count already ignores non-wikilink edges. Counting tags would make
  every tagged note non-orphan and empty the report.
- **A broken outbound link does not rescue a note from orphanhood.**
  `dst_note_id IS NULL` means the link points at nothing that exists, so the
  note is still disconnected from the graph. This is the more useful reading:
  a note whose only outbound link is broken is exactly a note that needs
  attention.

Returning the existing `db.NoteStamp` rather than a new type is deliberate:
it is the same shape `recent_notes` and `stale_notes` already render, so a
future `orphan_notes` recipe reader would be nearly free — though this slice
adds no reader.

### The automation

`internal/automations/orphans.go`, registered as **`orphan-report`** — the
25th built-in. Zero-model: **no Claude call, no proposals, no new review
kind.**

- `Essential() = false` (budget-guard may pause it, though it never spends).
- **`DetectChange`:** the cursor is a hash of the rendered report body, so a
  run where neither the orphan set nor the dormant set moved skips with no
  work — the `RecipeRun` change-gate pattern, applied to a built-in.
- **Caps are Go consts, not config** (the recipe-caps precedent):
  `orphanDormantDays = 180` for the dormancy cutoff, and `orphanListMax = 50`
  applied to each section independently. A section that hits its cap renders a
  trailing `- …and N more` line, so a truncated list never reads as a complete
  one.
- **`Run`:** `db.OrphanNotes(ctx, rc.DB, orphanListMax+1)` plus
  `db.NotesUpdatedBefore(ctx, rc.DB, cutoff, orphanListMax+1)` for
  the dormant section (the `+1` is what detects truncation), rendered into the `axon:orphans` managed block of
  **`03-Resources/Vault Health.md`**. If the note does not exist, stub-Create
  it with a short human-editable preamble first, then `Patch` — the
  `ActionsConsolidate` / `RecipeRun` sink pattern. Human prose outside the
  block is never touched.
- **`DryRun`:** reports the target and the counts, writes nothing.
- **Seeded disabled** in both `internal/config/starter.go` and
  `axon.config.example.yaml` (the `seeds_test` invariant checks both, and S8
  requires a fresh clone with every automation off to still be useful).

Rendered shape:

```markdown
<!-- axon:orphans:start -->
## Orphans (2)
Notes with no links in or out — nothing points here, and this points nowhere.

- [[03-Resources/Zettelkasten]] (updated 2026-08-01)
- [[00-Inbox/Untitled note]] (updated 2026-07-14)

## Dormant (1)
Not edited in 180 days. The resurfacer proposes these on its own ladder;
this list is for orientation.

- [[03-Resources/Old Idea]] (updated 2025-11-02)
<!-- axon:orphans:end -->
```

**Dormancy is reported, never proposed.** `proactive`'s resurfacer already
owns dormant-note proposals via the `resurface` kind and its own
spaced-repetition ladder. A second automation nagging about the same notes
would be a regression in the owner's attention, not a feature.

## FR-205 — `link-suggester` becomes orphan-first

The one behavioural change to shipped code, and the fix for gap 2.

In `LinkSuggester.Run`, replace the plain `sort.Strings(paths)` with an
orphan-first ordering: query `db.OrphanNotes`, emit those paths first in
query order, then every remaining path lexically as today. Everything else is
untouched — the same `MaxSuggestions` budget, the same proposal memory
(`linkSuggesterProposedState`), the same `pair` line format, the same accept
path.

**Deliberately not raising the budget.** The problem was never that ten
proposals is too few; it was that they were spent alphabetically. Spending
the same ten where the graph is actually broken is the whole fix, and it
keeps the review queue's weekly volume unchanged.

If the orphan query fails, the ordering falls back to plain lexical and the
run continues — a link suggestion is advisory, and a DB hiccup should not
take out a working automation.

## Out of scope

Recorded so their absence reads as a decision:

- **A duplicate proposal automation.** `orphan-report` proposes nothing; the
  two shipped proposers keep that job.
- **An `archive` proposal kind.** `docs/19` E1 flags it as edging toward
  delete-shaped territory, and it does. There is no `vault.delete`, and an
  "archive this" accept path would be the first proposal that removes a note
  from where the owner put it. No.
- **Dormant proposals** (the resurfacer's job) and **merge proposals for
  orphans** (`merge-proposals` already sweeps the whole vault by similarity;
  orphans are not excluded from it).
- **An `orphan_notes` recipe reader.** The report note is the composition
  surface — that is the point of C2 P3 closing by composition rather than by
  new vocabulary.
- **A dashboard panel.** The SPA already shows Hubs & orphans; this slice
  gives the *vault* a copy, not the dashboard a second one.

## Why no ADR

ADR-039's amendment set the precedent for what needs a decision record: a
moved boundary. This slice moves none. It adds no sink (the managed block is
`vault.Patch`), no model path (zero Claude calls), no egress, no new review
kind, no schema. It is a new automation and a re-ordering inside the existing
`Automation` contract and ADR-020's review model. The FR rows carry the
detail.

## Verification

**Unit — db:** `OrphanNotes` against a seeded graph covering every branch: a
true orphan; the `+1` truncation probe rendering `- …and N more`; a note with only an inbound link; a note with only a resolved
outbound link; a note whose only outbound link is **broken**
(`dst_note_id IS NULL` — must still be an orphan); a note connected only by a
`tag` edge (must still be an orphan); ordering newest-first; the limit.

**Unit — automation:** the rendered block (both sections, counts in the
headings); stub creation with the preamble when `03-Resources/Vault Health.md`
is absent; human prose outside the block surviving a re-run; the change-gate
skipping an unchanged second run and re-arming when a link is added or
removed; dry-run writing nothing; the registry/catalog/seed invariants at 25.

**Unit — link-suggester:** the regression that proves FR-205 — a vault where
an orphan sorts late alphabetically (`Zzz Orphan.md`) and several connected
notes sort early, with `MaxSuggestions` small enough that lexical order would
exhaust the budget before reaching the orphan. Assert the orphan receives
proposals. Plus the fallback: with a DB error injected, the run still
succeeds in lexical order.

**Live smoke** in an isolated env (scratch `AXON_HOME`, dashboard port 7799 —
**never 7777**, the live daemon's port; build the config by editing
`axon.config.example.yaml` and `sed` the port immediately, then
`grep -rn 7777 <scratch>` before any `axon start`, and confirm `daemon
running` in the log rather than just `scheduled …`): seed a vault with a real
orphan, a linked pair and a dormant note; `axon reindex`; run `orphan-report`
and read the block; re-run for the change-gate skip; add a wikilink to the
orphan and re-run to see it leave the list; `axon run link-suggester` and
confirm the orphan appears in the queue; `axon doctor` and `axon automations`
showing 25 built-ins.

## Docs to update on completion

`docs/03` (FR-204/FR-205 rows), `docs/06-component-automation-engine.md`,
`docs/AUTOMATIONS.md` (the table gains `orphan-report`; the link-suggester row
gains its ordering note), `README.md` ("All 24 automations" → 25),
`internal/config/starter.go` + `axon.config.example.yaml` (disabled seed),
`docs/19` E1 (shipped, with the reframing recorded — E1 shipped as a report
plus a reordering, not as the proposal sweep the entry described),
`docs/20` C2 P3 (**closed by composition** — the report note is the surface a
recipe reads), and `CHANGELOG.md` `[Unreleased]`.
