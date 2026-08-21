# Recipe vocabulary v2 — reading `.axon/`, staleness, and sources (design)

**Status:** approved 2026-08-21 (all three decisions Jandro-picked-recommended:
`.axon/` reads allow-listed to two files; a sibling `stale_notes` reader rather
than an inverted `recent_notes`; the `sources` reader renders status and does
not filter on it). **FR-202, FR-203; ADR-039 amended, no new ADR.** Graduates
`docs/20` **C2 Priorities 1 and 2**; Priority 3 (link topology / orphans) stays
out by that section's own ordering — `docs/19` E1 would render orphans into a
note that recipes can already read, which dissolves the priority.

**No migration.** Schema stays `0007`.

## The idea

Two recipe experiments on 2026-08-21 produced opposite results, and both
pointed at the same narrow set of gaps. `docs/19` **E2** (source freshness)
could not be expressed at all: its signal lives in the `sources` table, which
no reader reaches. `docs/19` **D3** (weekly review) mostly could — a real
recipe composed a usable weekly review out of the GTD board and
recently-touched notes — because **automation output is just a note, so
recipes compose over other automations**. `actions-consolidate` renders the
whole action board into `01-Projects/Actions.md`, so a `note` reader inherits
overdue/next/someday without an actions reader existing.

What D3 could not reach was its "pending proposals" section, and the reason
turned out to be a bug in a rule rather than a missing feature: one shared
`validRecipePath` governs both inputs and the block sink, so the rule that
stops recipes *writing* system files also stops them *reading* one. The review
queue is plain Markdown the dashboard already serves. **Reading is not
writing.** This slice separates the two rules and adds the two readers E2
needed.

**Security stance is unchanged.** Recipes stay data in `config.yaml`, outside
every model write path. Both cardinal rules still hold by construction: the
only model path is `runModel` (chokepoint), and the only writers are
`vault.Patch` into an `axon:` block plus the review-queue append. Nothing in
this slice touches a sink.

## FR-202 — Read/write path split, `.axon/` read allow-list, self-feeding refusal

### The split

`validRecipePath` becomes a shared base plus two rules in
`internal/config/recipes.go`:

| rule | applies to | refuses |
|---|---|---|
| base | both | empty, non-`.md`, leading `/`, any `..` |
| `validRecipeWritePath` | `output.block.note` | `.axon/**`, `.trash/**` |
| `validRecipeReadPath` | `inputs[].note.path` | `.trash/**`, `.axon/**` *except the allow-list* |

The **write** rule is today's behaviour verbatim. The sink boundary does not
move: a recipe still cannot write a system file, and there is still no third
sink.

The **read** allow-list is exactly two files, as a Go map:

```go
// readableAxonFiles are the only .axon/ files a recipe input may read.
// Reading is not writing (FR-202), but the exception stays narrow: logs/,
// exports/, snapshots/ and dashboards/ hold material never written to be
// read back into a model call.
var readableAxonFiles = map[string]bool{
    ".axon/review-queue.md":         true,
    ".axon/review-queue-archive.md": true,
}
```

Widening the list later is a one-line change; narrowing it later breaks
someone's config. That asymmetry is why the wholesale `.axon/**.md` option was
rejected.

### The self-feeding refusal

A recipe whose sink is `review {}` may **not** read `.axon/review-queue.md`.
Its own output becomes its next input, so the input hash changes on every run,
the change-gate never skips, and a `prompt` recipe burns one model call per
tick forever. Validation refuses it with a named error:

```
recipe "x": a review-sink recipe may not read .axon/review-queue.md
(its own output would be its next input); read the archive instead
```

Two adjacent cases stay legal and are pinned by tests:

- **archive + review sink** — fine. Items reach the archive only when a human
  accepts or dismisses, so the loop needs a person in it.
- **queue + block sink** — fine, and it is D3's actual case.

This guard was not in `docs/20` C2; it emerged from the design pass and is
recorded here as part of the slice.

## FR-203 — Two readers: `stale_notes` and `sources`

The reader set goes 3 → 5. The "exactly one reader per input" rule and the
1–8 input cap are unchanged.

```yaml
recipes:
  - name: source-freshness
    purpose: "Weekly advisory on research sources that have gone stale."
    inputs:
      - name: old
        sources: {older_than_days: 180, limit: 30}
      - name: dormant
        stale_notes: {older_than_days: 365, limit: 20}
    render: |
      Sources not re-fetched in 180 days ({{today}}):
      {{old}}

      Notes untouched in a year:
      {{dormant}}
    output:
      block: {note: "03-Resources/Source Freshness.md", block: "freshness"}
```

| reader | field | range | default | renders |
|---|---|---|---|---|
| `stale_notes` | `older_than_days` | 0–3650, **0 = default** | 90 | `[[path]] (updated DATE)` |
| | `limit` | 0–100 | 20 | |
| `sources` | `older_than_days` | 0–3650, **0 = no age filter** | 0 | `[[path]] — url (fetched DATE, kind, status)` |
| | `limit` | 0–100 | 20 | |

**`stale_notes` is a sibling, not a mode of `recent_notes`.** Overloading
`recent_notes` with an inverting flag would give it a name that lies and
require a mutual-exclusion rule between its own two fields. As a separate
reader it also gets its own range: `recent_notes` keeps the honest 0–90 cap
(a "recent" lookback of five years is a misconfig), while staleness is by
definition about older material and needs the full 3650. Both new readers
follow the established convention that a zero or omitted numeric field means
"apply the default", so the accepted range is 0–3650.

**`sources` renders status and does not filter on it.** The line already
carries `status`, so a `failed` or `redacted` source is visible to whoever (or
whatever) reads the block — and those are arguably the rows most worth
surfacing. Filtering to `ok` would hide ingest failures behind a reader whose
whole purpose is telling the owner what the ingest layer knows.

A source whose `note_id` is null or dangling (the schema is
`ON DELETE SET NULL`) renders without the wikilink:
`https://example.com/x (fetched 2026-03-01, url, ok)`.

### DB seam — `internal/db`

- **`NotesUpdatedBefore` gains `limit int`** (0 = unlimited). Its one caller,
  `actions-review`, passes 0 and is otherwise untouched. One helper with a
  parameter beats a near-duplicate query.
- **New `SourcesOlderThan(ctx, q, beforeTS string, limit int) ([]SourceInfo, error)`**
  — `sources` LEFT JOIN `notes` on `note_id`, `WHERE fetched_at < ?` when
  `beforeTS != ""`, ordered `fetched_at DESC, url`, capped by `limit`.
  `SourceInfo` is `{Path, URL, Kind, FetchedAt, Status}` — a read-layer
  projection, distinct from the existing write-side `SourceRow`.

`sources.fetched_at` is RFC3339 UTC, so a lexicographic `<` is a correct
chronological compare; `CountSourcesSince` is the precedent for that in this
package. The recipe reader builds the cutoff as
`rc.now().UTC().AddDate(0, 0, -n).Format(time.RFC3339)`.

### Rendering — `internal/automations/recipe.go`

Both readers render through the existing `clipInput` cap, so the zero-model
path stays bounded exactly as before. `stale_notes` reuses the
`recent_notes` line shape deliberately: a recipe author reading a rendered
block should not have to work out which reader produced which half.

**`vault.NeutralizeMarkers` on the block sink stops being defensive and
becomes load-bearing.** Reading `.axon/review-queue.md` means reading a file
that is full of `- [ ]` proposal lines, and composing over automation output
means embedding *that* note's `axon:` markers in the recipe's own block. The
v1.5.0 marker-neutralization fix already handles this; this slice pins it with
the D3-shaped case as a regression test rather than leaving it to the
generic one.

## ADR-039 amendment (not a new ADR)

ADR-039's Status gains a dated amendment line, the ADR-038 precedent. The
amendment records that (a) the recipe path rule is two rules, read and write,
because reading is not writing; (b) the reader vocabulary is five, not three;
(c) a review-sink recipe may not read the review queue. It changes no
architectural boundary — sinks, the chokepoint, config-not-vault, the
registry story and "anything needing a new sink is a Go automation" all stand
unchanged, which is why this is an amendment and not ADR-040.

## Out of scope

Recorded so nobody reads their absence as an oversight:

- **A fan-out sink.** E2 wanted an advisory line on *each* matched report
  note. That takes a recipe's blast radius from one named note to N discovered
  ones — exactly the boundary ADR-039 drew. Per-note annotation stays Go; an
  aggregate digest is the useful 80%.
- **Link topology / orphan readers** (C2 Priority 3). Nothing renders orphans
  into a note today, so there is nothing to compose over — but `docs/19` E1
  would create exactly that note. Prefer that order.
- **Wholesale `.axon/` reads**, a status filter on `sources`, a limit on the
  allow-list beyond the two named files, and any new sink.

## Verification

**Unit — config:** a path table asserting the split (`.axon/review-queue.md`
read-ok / write-refused; `.axon/review-queue-archive.md` same;
`.axon/logs/run.md` refused both ways; `.axon/exports/x/y.md` refused both
ways; `.trash/x.md` refused both ways; `../escape.md` and `/abs.md` refused
both ways; an ordinary note ok both ways). Bounds for both new readers
(0/below/at/above each range end, defaults applied). Reader exclusivity with
the new readers in the mix (`note` + `sources` in one input → refused; no
reader → refused). The self-feeding refusal plus both legal neighbours
(archive+review ok, queue+block ok).

**Unit — db:** `SourcesOlderThan` for the age cutoff (a source either side of
it), `beforeTS == ""` returning everything, the limit, ordering, and a
null/dangling `note_id` rendering pathless. `NotesUpdatedBefore` with a limit
and with 0.

**Unit — automations:** both readers' rendered line shapes; the date
truncation on `fetched_at`; and **the D3-shaped regression `docs/20` asks for
by name** — a recipe reading `01-Projects/Actions.md` plus
`.axon/review-queue.md` into a block, asserting the result contains exactly
one `axon:<block>:end` marker and that the nested markers it swallowed are
inert.

**Live smoke** in an isolated env (scratch `AXON_HOME`, dashboard port 7799 —
**never 7777**, the live daemon's port; build the config by editing
`axon.config.example.yaml` and `sed` the port immediately, then
`grep -rn 7777 <scratch>` before any `axon start`, and confirm `daemon
running` in the log rather than just `scheduled …`): a `render` recipe reading
the real review queue into a block end-to-end via `axon run`, re-run
change-gate skip, an edit to the queue re-arming it, a `sources` recipe
against a vault with real ingested sources, human prose outside the block
intact, dry-run writing nothing, and `axon config validate` refusing both a
`.axon/logs/` read and a self-feeding review recipe.

## Docs to update on completion

`docs/03` (FR-199's reader list, plus FR-202/FR-203 rows), `docs/02` (ADR-039
amendment), `docs/04` (config reference: the two readers and the read rule),
`docs/06`, `docs/AUTOMATIONS.md`, `axon.config.example.yaml` (the commented
sample gains a stale/sources example), `docs/20` C2 (P1+P2 shipped, P3
remains), `docs/19` E2 (**qualify**, do not reverse, the 2026-08-21 "confirmed Go"
note: the aggregate-digest half of E2 becomes expressible as a recipe once
this lands; the per-note advisory half still needs a fan-out sink and stays
Go), and `CHANGELOG.md` `[Unreleased]`.
