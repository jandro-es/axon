# 15 — Roadmap 1.2 *(plan — "remember & reason")*

1.0 built the self-maintaining vault; **1.1 made it answer** — grounded-or-silent
`ask` with wikilink citations on three surfaces, ANN retrieval, a local reranker,
standing research questions, entity pages, project pulse, a capture endpoint, OCR,
and memory contradiction proposals (FR-108…133, ADR-023…027). **1.2 makes it
*remember and reason, cheaply***: a real memory architecture (temporal facts with
validity intervals and supersedence, background consolidation) instead of an
append-only log; the intelligence that exploits it (contradiction-aware answers,
scheduled resurfacing); an ambient related-notes surface off the embeddings we
already have; and an eval-gated local model tier so Claude is reserved for what
deserves it. Same constitution: local-first, every model call through the
chokepoint, every write wikilink-safe, everything toggleable, all-off still useful
(S8), the vault rebuilds the DB and never the reverse (S9).

This document is the 1.2 *plan*, in the style of the
[1.1 roadmap](14-roadmap-1.1.md), graduated from the vault planning notes
(`Axon 1.2–1.3 — PRD / Purpose / Development Plan / Research Notes`). Every slice
below still requires its own design cycle (brainstorm → spec → **ADR** → FR rows)
before any code; the FR/ADR numbers here are **provisional** (current maxima:
FR-133, ADR-027) and are assigned for real in those cycles. A slice isn't "done"
until its acceptance gate passes.

## Carry-over reconciliation *(read this first)*

The PRD/Development-Plan notes were written 2026-07-05, when 1.1 was still open,
and list **four** 1.1 leftovers rolling into 1.2 (R3 entities, R4 pulse, R6
reranker, R7 merge). 1.1 then shipped further than expected — **three of the four
are already done**, so 1.2's real scope is smaller than the PRD implies:

| PRD slice | PRD status | Actual |
|-----------|------------|--------|
| R3 — entity pages (was C2) | carry-over | ✅ **shipped in 1.1** (FR-128/129/130) |
| R4 — project pulse (was C3) | carry-over | ✅ **shipped in 1.1** (FR-131/132/133) |
| R6 — local reranker (was B2) | carry-over | ✅ **shipped in 1.1** (FR-126/127, ADR-027) |
| R7 — merge proposals (was B3) | carry-over | ⏳ **genuinely rolls to 1.2** |

**Net-new 1.2 work is therefore R1, R2, R5, R7, R8, R9.** Because R3/R4/R6 are
banked, the PRD release criterion ("R1 + R5 + two of {R3,R4,R6,R7}") is already
two-thirds satisfied — 1.2 effectively ships when **R1 + R5** land, with R7/R8/R9
as they follow.

## Phase R — Memory & reasoning *(the headline; build first)*

### R1 — Temporal memory layer (M) · provisional FR-134…137, ADR-028
**Build:** evolve memory from an append-only dated log into **episodic entries +
semantic facts with validity intervals**. The vault stays the source of truth:
`MEMORY.md`'s `axon:memory` block gains structured entries — fact, `valid_from`,
optional `valid_until`, superseded-by pointer, source wikilinks; raw dated
observations (episodes) stay lightweight and consolidation promotes them to facts.
Facts land in SQLite (entity, key, interval, embedding) purely as a re-derivable
index for retrieval and contradiction detection. **Supersedence, not
coexistence** extends 1.1's C1: "moved London → Tokyo" invalidates the old fact
(tombstone + interval close) through the existing reconcile review-queue flow —
nothing deleted. A **sleep-time consolidation** automation (routine tier,
chokepoint, change-gated) compacts recent episodes into facts and proposes prunes
of stale episodic noise to the review queue.
**Gate:** a seeded life-change produces one reconcile proposal; accept closes the
old fact's interval and the `MEMORY.md` projection reads correctly; `reindex`
rebuilds the fact index byte-equivalently (S9); SessionStart injection prefers
*currently-valid* facts and stays within its token ceiling.

### R2 — Contradiction-aware ask (S) · provisional FR-138/139 (rides R1; no ADR)
**Build:** when retrieval surfaces sources that disagree, the answer says so —
both claims cited with their dates, newest-valid preferred, no silent averaging.
Rides on R1's intervals. No consumer product does this well yet.
**Gate:** a vault seeded with two dated, conflicting notes yields an answer that
flags the conflict and cites both; non-conflicting corpora are unaffected.

### R5 — Local synthesis validation & routine-tier promotion (M) · provisional FR-140…143, ADR-029
**Build:** turn the ADR-015 validation gate into a **promotion procedure**. An
eval harness (`axon eval`) with golden sets drawn from AXON's own real tasks
(triage classifications, digest summaries, consolidation rewrites), graded
pass/fail by rubric, runnable against any `(provider, model)` pair. A config-gated
promotion: `models.routine: ollama:<model>` becomes supported **only** when the
harness passes thresholds on this machine; `doctor` reports eval status; silent
regressions caught by re-running evals on model/version change (the embedding-model
discipline, extended to chat models). A cascade with verification for promoted
tiers: local attempt → cheap local verifier → escalate to Claude on low
confidence, all ledgered. `synthesis` (user-facing prose) stays Claude until the
harness says otherwise *per task family* — grounded in evals, not vibes.
ADR-029 extends ADR-015 (eval-gated promotion; the synthesis gate becomes a
procedure, not a permanent "no").
**Status (2026-07-08): R5 COMPLETE.** Shipped in three sub-slices — R5.1 eval
harness + `axon eval` (FR-140/141, ADR-029), R5.2 eval-gated admission gate
(FR-142/143, ADR-030), R5.3 per-call cascade-with-verification (FR-144/145,
ADR-031: a successful local `routine` answer is judged by a cheap local model and
escalates to Claude below `models.verify_min_score`, all ledgered, default off).
With R1 already shipped, the **R1 + R5 release criterion for 1.2.0 is met.**
**Gate:** with a passing local routine model configured, a full automation cycle
runs with measurably fewer Claude tokens (target: ≥50% of routine-tier calls
local) and zero unledgered calls; with Ollama down, everything degrades to Claude
transparently.

### R8 — Ambient related-notes surface (S) · provisional FR-144/145 (rides ADR-025; likely no ADR)
**Build:** expose the embeddings AXON already has as a **live "related to what I'm
looking at" surface** — `axon related <note>`, a `vault_related` MCP tool, and a
dashboard panel, plus a documented loopback endpoint an Obsidian sidebar plugin
can consume. Zero model calls — pure vector math over the ANN seam. (Smart
Connections charges ~$20/mo for exactly this.)
**Gate:** the related list for a note returns in <100 ms warm, respects the ANN
seam (B1/ADR-025), and makes **no** model call.

### R9 — Resurfacing with review scheduling (S) · provisional FR-146/147 (extends resurfacer + review queue; no ADR)
**Build:** upgrade the resurfacer from "similar old note" to a light
**FSRS-flavoured review queue for ideas** — stale-but-relevant notes and
R1-detected "this new note echoes/contradicts `[[old note]]`" pairs scheduled into
the weekly review at spaced intervals, with proposal memory so items surface once
per interval.
**Gate:** a resurfaced item declined this week does not reappear next week;
intervals lengthen on acceptance.

### R7 — Near-duplicate merge proposals (M, 1.1 B3 carry-over) · provisional FR-148/149, ADR-030
**Build:** an embedding sweep reusing the resurfacer primitives
(`db.NoteMeanVectors`/`db.Cosine`) and the shared proposal-memory helpers,
proposing note merges to the review queue. **Accept semantics get their own
design pass (ADR-030)** — merge is the closest thing to a destructive op AXON has;
direction: merged note + originals archived + inbound links rewritten, never
deletion. Deliberately last (unchanged from the 1.1 reasoning).
**Gate:** duplicates surface once (proposal memory); an accepted merge leaves
zero broken links and both originals recoverable from the archive.

## Suggested build order & sizing

| Order | Slice | Size | Why here |
|-------|-------|------|----------|
| 1 | R1 temporal memory | M | The headline; entities it links to already exist (R3 shipped). Unlocks R2/R9. |
| 2 | R5 local-tier promotion + `axon eval` ✅ **done** | M | Frees budget for everything after; independent lane, can run alongside R1. Shipped R5.1/R5.2/R5.3 (FR-140…145, ADR-029…031). |
| 3 | R2 contradiction-aware ask | S | Cheap once R1's intervals exist. |
| 4 | R8 related-notes surface | S | Zero-model, high visible value; exercises the ANN seam. |
| 5 | R9 resurfacing/review scheduling | S | Rides R1's signals + resurfacer primitives. |
| 6 | R7 merge proposals | M | Own destructive-op design pass (ADR-030); deliberately last. |

**Two long poles:** R1 (memory representation touches `MEMORY.md` format,
SessionStart injection, the C1 reconcile flow, and reindex) and R5 (eval harness +
router cascade). They are independent — build in parallel lanes if there's
appetite, otherwise R1 first (it unlocks more of the tree). Sequencing R5 before
1.3's new token spenders is deliberate: bank the savings before spending them.

**Release criterion:** 1.2.0 ships when **R1 + R5** are done (R3/R4/R6 already
banked satisfy the PRD's two-carry-over clause). R7/R8/R9 land as they complete;
leftovers roll to 1.3 without renumbering.

## Cross-cutting rules

- Every model call through the chokepoint; classify/routine work stays
  local-routable (ADR-015); budget-exempt local calls remain fully ledgered.
- Every new writer is wikilink-safe (managed blocks or additive; no deletes —
  merge archives, never deletes).
- Every feature independently toggleable; all-off still runs and is useful (S8);
  the vault rebuilds the DB, never the reverse (S9); `doctor` reports each new
  subsystem.
- Each slice: brainstorm → spec → ADR → FR rows → TDD plan → inline execution →
  live smoke → merge + push (the standing cycle).

## Explicit non-goals for 1.2

Bi-temporal Graphiti-style memory modelling (intervals + supersedence only — no
transaction-time axis); any deletion (merge and consolidation archive/tombstone,
never delete); cloud/server dependencies; a server-based vector DB; agent-driven
`vault_move`; a native macOS app (dismissed 2026-07-03). The 1.3 "reach" surfaces
(channels, meetings, multimodal, deep research, calendar) stay in the vault
planning notes until 1.2 closes and they graduate to `docs/16-roadmap-1.3.md`.
