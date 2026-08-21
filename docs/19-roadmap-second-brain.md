# 19 — Roadmap: the second brain *(candidates — nothing scheduled)*

1.0 → 1.3 built the loop: capture, ingest, remember, act, perceive, research.
This document is the forward view of **what makes the vault smarter** — the
second-brain half of the split roadmap (its sibling, `docs/20-roadmap-ai-os.md`,
covers what makes the daemon a platform). Everything below is a **candidate
slice**: no dates, no provisional FR/ADR numbers. A slice earns numbers only
when it graduates through its own brainstorm → spec → ADR → FR rows cycle, per
the standing process. Sizing letters (S/M/L) are relative effort guesses, not
commitments.

Three of the themes re-propose work **deliberately cut from 1.3** on
2026-07-10. The cut was a focus decision, not a veto; each re-proposal states
why the economics changed. Every slice is held to the Ingestion constitution
(`docs/17` §"Ingestion constitution"): opt-in and off by default, deny-by-default
on work, egress through the policy engine, perception local, content is data
(NFR-05), archive-never-delete — and to the two cardinal rules (every model call
through the chokepoint; every vault write wikilink-safe).

## Theme A — Memory quality

The temporal memory layer (1.2 R1) records facts with intervals and
supersedence; consolidation (memory-distill) and contradiction handling exist.
What's missing is *judgement about the memory itself*.

### A1 — Consolidation confidence (S) · candidate — not scheduled
**Value:** distilled facts today are all equal; a fact seen once in a session
transcript and a fact reinforced across ten notes read identically, so the
SessionStart injection can't prefer the trustworthy ones.
**Shape:** extend the `memory_facts` derived table (rebuild-from-block stays
S9-safe) with a reinforcement count derived at reindex from source multiplicity;
memory-distill's one synthesis call already sees the numbered current entries,
so surfacing low-confidence facts for review rides the existing ADR-020 review
queue with no new mutation.
**Open decisions:** whether confidence is derived-only (pure reindex) or a
distill output; whether injection filters or merely orders by it.

### A2 — Fact decay & aging (S) · candidate — not scheduled
**Value:** open facts never expire; a preference recorded in January is asserted
in August with the same force. Aging is what the resurfacer's interval ladder
already does for notes, applied to facts.
**Shape:** zero-model sweep (resurfacer pattern) proposing `stale fact` review
items for open facts past an age threshold with no reinforcement; accept closes
the interval via the existing `identity.Reconcile` tombstone — no new write
primitive.
**Open decisions:** age threshold source (config vs ladder); whether `[kind]`
classes age differently (a `preference` ages slower than a `status`).

### A3 — Proactive contradiction surfacing (M) · candidate — not scheduled
**Value:** contradiction detection exists in two reactive places (ask's
`CONFLICT` line; the resurfacer's opt-in pair check). Neither watches *new
material as it lands*.
**Shape:** an ingest/distill-time check of new facts against `OpenFacts` (vector
+ same-kind narrowing, then one classify-tier chokepoint call per suspect pair),
proposing `reconcile` items to the review queue — the exact accept path
memory-distill's conflicts already use.
**Open decisions:** where it hooks (ingest tail vs distill); per-run pair cap;
whether the work profile gets it at all (memory features ship off there —
`memory.inject: false` is the standing convention).

## Theme B — Meeting & voice capture *(previously cut from 1.3)*

**Re-proposed because the economics changed:** the 1.3 cut said "local STT is
out of scope" when local STT meant wiring and validating whisper.cpp ourselves.
whisper.cpp has since matured as a detected-binary candidate (the `yt-dlp`
precedent, ADR-026's detected-system-tool pattern), and macOS 27 ships
on-device speech APIs (`docs/21-roadmap-macos27.md` M6) — transcription can now
be **cheap, private, and behind the same provider seam pattern** as OCR/vision.
The constitution line is unchanged and non-negotiable: **AXON never records.**
It transcribes audio files the owner already has.

### B1 — Audio-file ingestion via local STT (M) · candidate — not scheduled
**Value:** a voice memo or downloaded recording becomes a searchable, citable
source note — the single biggest untapped personal input.
**Shape:** a `KindAudio` in the ingestion pipeline (H1's `KindImage` pattern):
extension-classified, CLI-only under the `AllowLocalFiles` guard, transcribed by
an STT provider seam (`whisper:<model>` detected binary now; the Apple
on-device tier behind the same seam later), then the unchanged
enrich→chunk→embed tail; source audio archived to `attachments/<hash>`
(archive-never-delete), zero model calls when no provider.
**Open decisions:** whisper.cpp vs Apple STT as the first provider; diarisation
(probably out of v1); max duration guard.

### B2 — Meeting notes with action extraction (S, rides B1) · candidate — not scheduled
**Value:** a transcribed meeting yields decisions and commitments, not just
text.
**Shape:** pure composition — the transcript note flows through the existing
opt-in `action-extract` automation (routine tier, review-queue accept into
`axon:tasks`, T6 pattern); a meeting-shaped enrich template adds
attendees/decisions frontmatter.
**Open decisions:** whether meeting-ness is inferred (filename/frontmatter) or
flagged at ingest.

## Theme C — Calendar & email read-only context *(previously cut from 1.3)*

**Re-proposed because the economics changed:** the cut version implied
OAuth-shaped integrations. The re-proposal is deliberately smaller: **read-only,
file-shaped, local** — an `.ics` file/URL and a local maildir are just more
ingestable inputs, which the policy engine, redaction, and work-profile
deny-by-default already govern. No OAuth, no send path, no sync.

### C1 — ICS calendar context for briefing & pulse (S) · candidate — not scheduled
**Value:** the morning briefing knows today's shape; project-pulse can correlate
stale projects with calendar-quiet weeks.
**Shape:** a zero-model parser (allow-listed `.ics` URL or local file) feeding
structured events into the existing briefing/pulse prompts as *data*; nothing
stored beyond the automation run; off by default, work-off.
**Open decisions:** file vs URL first; lookahead window; whether events are ever
persisted (recommendation: no — regenerate per run, S9-trivial).

### C2 — Local email context (M) · candidate — not scheduled
**Value:** commitments made over email reach the review queue like everything
else.
**Shape:** a maildir/`.eml` reader (local files only, never IMAP) feeding
`action-extract`'s existing scan-cap-propose loop; redaction rules apply before
any model call.
**Open decisions:** whether this is worth doing before B1/B2 prove the
"more inputs into existing extractors" pattern; personal-only vs configurable.

## Theme D — Richer GTD

1.2.5 shipped the trusted list (parse → derived table → consolidate → complete
mutation). The lifecycle around the list is thinner than the list itself.

### D1 — Recurring tasks (M) · candidate — not scheduled
**Value:** the Obsidian Tasks emoji grammar AXON already parses includes
recurrence (`🔁`); AXON reads it as text and does nothing.
**Shape:** parse recurrence in `internal/actions` (grammar already tolerated);
on completion via the ADR-034 toggle, propose the next occurrence — the delicate
part is that writing a *new* checkbox line into human prose is a new mutation
class, so v1 should route the "next occurrence" through the review queue rather
than auto-writing.
**Open decisions:** auto-write vs review-queue proposal (recommendation:
review queue; revisit after trust builds); where the next line lands (source
note vs `axon:tasks` block).

### D2 — Project lifecycle (S) · candidate — not scheduled
**Value:** projects have states in the owner's head (someday → active → done)
that the vault doesn't encode, so pulse and actions views can't respect them.
**Shape:** a frontmatter `status:` convention on `01-Projects/` notes, read at
index time into the existing notes mirror; actions views and project-pulse
filter by it; stale-project nudges (already shipped) propose *state changes*
through the review queue instead of free-text nudges.
**Open decisions:** state vocabulary; whether accept-side state edits are a
frontmatter patch (new narrow mutation, needs its own design pass) or a manual
step.

### D3 — Weekly review flow (S) · **SHIPPED 2026-08-21 in v1.7.0 as a documented example recipe — no Go, no FR**
**Value:** GTD's weekly review is the habit AXON is best placed to scaffold —
everything it needs (stale actions, someday list, pulse, orphans, pending
proposals) already exists, just not in one place.
**Shape:** a zero-model automation composing existing readers into a weekly
`axon:review` managed block (consolidate pattern); dashboard "Needs you" panel
already deep-links the interactive halves.
**Open decisions:** its own note vs a section of Actions.md; day/time default.

**Tried as a recipe (ADR-039) and three of five components already work.** A
real recipe was built and run: it produced a usable weekly review from the
action board plus recently-touched notes. The enabling property — worth
knowing before building anything here — is that **automation output is just a
note, so a recipe composes over other automations**: reading
`01-Projects/Actions.md` inherits the whole GTD board (overdue / next /
someday) that `actions-consolidate` renders, with no actions reader involved,
and `01-Projects/Project Pulse.md` supplies the pulse the same way.
- ✅ stale actions, someday list — via `Actions.md`
- ✅ project pulse — via `Project Pulse.md` (when `project-pulse` is enabled)
- ❌ pending proposals — `.axon/review-queue.md` is refused as an input; the
  rule that stops recipes *writing* system files is over-broad and blocks
  reads too. `docs/20` **C2 priority 1** fixes exactly this.
- ❌ orphans — link topology; nothing renders it into a note to compose over
  (see `docs/20` C2 priority 3, and prefer shipping **E1** below first, which
  would render one).

**D3 needed no Go slice.** Both blockers closed the same day — FR-202 made
`.axon/review-queue.md` readable, and E1's `orphan-report` (FR-204) renders
orphans into a note — so D3 shipped as a **documented example recipe** in
`axon.config.example.yaml` plus a `docs/GUIDE.md` walkthrough. Zero model
calls, zero new code, no FR number: it is config and prose. Verified live —
both sections compose, the nested `axon:actions` markers come back inert, the
change-gate skips an unchanged re-run and re-arms on an input edit, and human
prose outside the block survives.

**Open decisions resolved.** *Its own note, not a section of `Actions.md`* —
and that was settled by the code rather than taste: `actions` is a reserved
block name, so a recipe cannot extend the board's own block. *Day/time:*
Friday 17:00 (`0 17 * * 5`), the GTD convention of reviewing before the week
closes.

**Two characteristics worth knowing before building on this pattern.** A
`note` input reads the **whole** note, so the source's title and headings come
along — the output is useful rather than tidy, and a future "section" or
"body-only" reader is the obvious refinement if that grates. And a recipe
**idles entirely** while any input note is missing, so the shipped example
uses only the two always-present sources and documents the optional pulse and
orphan inputs in prose rather than half-YAML.

## Theme E — Knowledge health

`axon health` scores the vault; 1.3.8's Hubs & orphans made link topology
visible. The gap is *acting* on health, not seeing it.

### E1 — Orphan & decay report with proposals (S) · **SHIPPED 2026-08-21 in v1.6.0 — FR-204/FR-205** (spec: `docs/superpowers/specs/2026-08-21-orphan-decay-report-design.md`)
**Value:** orphans and dormant clusters are visible but inert; the fix (link it,
merge it, archive it) still requires the owner to notice.
**Shape:** a zero-model weekly sweep pairing each orphan with its nearest
vector neighbours and proposing `link` items (link-suggester's accept path) or
`merge` items (R7's) — pure composition of two shipped accept paths.
**Open decisions:** proposal budget per week; whether "archive candidate" is a
proposal kind at all (it edges toward delete-shaped territory — likely no).

**Shipped smaller than specified, on purpose.** Reading the code first showed
the proposal half was already built: `link-suggester` proposes `pair` items
from search neighbours and `merge-proposals` proposes `merge` items from
cosine similarity, both with proposal memory and both accepting through
shipped paths. A third proposer would have meant three dedup stores that can
disagree about the same pair. What was actually missing was (a) any Go-side
notion of an orphan — they were computed only in the SPA's Graph panel — and
(b) that `link-suggester` scanned lexically and stopped at its budget, so
orphans late in the alphabet were never reached. E1 therefore shipped as a
zero-model **report** (`orphan-report`, the 25th automation, off by default)
plus an **ordering fix**, not as a new proposal sweep. Both open decisions
resolved: the proposal budget is unchanged (the fix was where it is spent, not
how much), and the `archive candidate` kind was **rejected** exactly as this
entry predicted — it is delete-shaped, and there is no `vault.delete`.

### E2 — Source freshness for research notes (S) · candidate — not scheduled *(partly unblocked 2026-08-21: the aggregate half is now a recipe, the per-note half stays Go)*
**Value:** ingested sources go stale; a research report citing a 2024 page
presents as current.
**Shape:** derived staleness from `source:`+ingest date already in the DB;
deep-research's currency-skip logic inverted into an advisory "stale sources"
line on report notes and the health score. Re-fetch stays manual (egress is
owner-initiated).
**Open decisions:** staleness thresholds per source kind; health-score weight.

**Update 2026-08-21 — C2 P1+P2 shipped (FR-202/FR-203) and removed three of
the four blockers below.** A `sources {older_than_days, limit}` reader now
renders `[[note]] — url (fetched DATE, kind, status)`, and a sibling
`stale_notes {older_than_days ≤ 3650}` reader lifts the 90-day cap for the
age-selecting path. What remains Go is blocker 4: E2 wants an advisory line on
*each* matched report note, and a fan-out sink was **deliberately rejected** —
it would take a recipe's blast radius from one named note to N discovered
ones, exactly the boundary ADR-039 drew. So: **E2's aggregate "stale sources"
digest is expressible as a recipe today; its per-note annotation and its
health-score weight stay Go.**

**Original finding — tried as a recipe (ADR-039) and it did not fit; four
independent blockers, found by building and running the closest possible
recipe:**
1. **The signal is unreachable.** Staleness lives in the `sources` table
   (`url`, `fetched_at`, `kind`, `status`); no recipe reader touches it.
   `recent_notes` surfaces the `notes` table, so the only dates a recipe sees
   are `updated` — reindex/file-mtime, not when the source was fetched. Wrong
   dates, not just missing ones.
2. **The reader can't reach back far enough.** `lookback_days` is capped at
   90 (validation refuses more), and staleness is by definition about older
   material.
3. **It points the wrong way.** `recent_notes` returns recently-*updated*
   notes; E2 needs the inverse.
4. **The sink is singular.** A recipe rebuilds one block in one named note;
   E2 wants an advisory line on *each* report note, plus a health-score
   contribution (Go regardless).

Blockers 1–3 are addressed by the proposed **`docs/20` C2** reader additions,
which would make a *partial* E2 (an aggregate "stale sources" digest note)
expressible as a recipe. The per-report annotation in blocker 4 stays Go on
purpose — see C2's rejected option.

## Theme F — Writing & synthesis

The digest proposes MOCs; ask answers questions. Neither helps the owner
*write*.

### F1 — Draft-from-vault (M) · candidate — not scheduled
**Value:** "draft me a post/brief from what I know about X" — retrieval-grounded
long-form generation with citations, the ask engine pointed at production
instead of Q&A.
**Shape:** rides the ask engine's grounding gate + citation contract
(synthesis tier, chokepoint); output lands as a **new draft note under
`00-Inbox/`** (Create-only, never patching an existing human note), clearly
frontmattered `axon:draft`.
**Open decisions:** CLI-only vs dashboard surface (spend-is-state, ADR-023
guard if the latter); grounding floor for long-form (stricter than ask's?).

### F2 — MOC materialisation (S) · candidate — not scheduled
**Value:** knowledge-digest proposes MOCs as prose; accepting one still means
hand-building the note.
**Shape:** a `moc` review-queue kind whose accept materialises the MOC note
(Create + `axon:moc` managed block of wikilinks) — entity-pages' materialise
pattern applied to topics.
**Open decisions:** dedup against existing MOCs; refresh policy once created.

## Sequencing sketch *(not a commitment)*

*Updated 2026-08-21: **D3** and **E1** have both shipped — D3 as a documented
example recipe (no Go), E1 as `orphan-report` plus the link-suggester ordering
fix (FR-204/205).* Of what remains, **B1 STT ingestion** is the biggest single
unlock and gates B2/M6; **E2**'s aggregate half is now expressible as a recipe
while its per-note half stays Go; **F2 MOC materialisation** needs a new
review-queue kind, so it is a Go slice. Theme A deepens quality behind the
scenes; C and F are optional reach. Anything here can be displaced by
`docs/20`/`docs/21` slices — the roadmaps compete for the same build slots.

## Explicit non-goals

No recording of any kind (transcription of owned files only); no OAuth'd
cloud calendar/email sync; no send/reply path anywhere; no cloud STT as a
default; no auto-applied vault mutations beyond the existing narrow set — new
mutation classes get their own destructive-op design pass (the R7/ADR-032
precedent) or go through the review queue.
