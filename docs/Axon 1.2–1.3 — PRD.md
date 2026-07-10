---
title: Axon 1.2–1.3 — PRD
type: project
status: draft
tags: [axon, prd, roadmap]
---

# AXON 1.2 & 1.3 — Product Requirements

> Draft for owner review; planning only. Purpose and rationale in [[Axon 1.2–1.3 — Purpose]]; build map in [[Axon 1.2–1.3 — Development Plan]]. All FR/ADR numbers below are **provisional** — assigned for real in each slice's design cycle, per the standing rule. Sizes are S/M in the repo-roadmap convention.

## Release 1.2 — "Remember & reason" (inward depth)

**Theme:** a real memory architecture, the intelligence that exploits it, and eval-validated local models carrying the routine load. Plus the four slices that explicitly rolled over from 1.1 (B2, B3, C2, C3).

### R1 — Temporal memory layer (M) · needs ADR
The headline. Evolve memory from an append-only dated log into **episodic entries + semantic facts with validity intervals**:
- **Representation (vault remains source of truth):** `MEMORY.md`'s `axon:memory` block gains structured entries — fact, `valid_from`, optional `valid_until`, superseded-by pointer, source wikilinks. Episodes (raw dated observations from daily logs/sessions) stay lightweight; consolidation promotes them to facts. Human prose outside the block untouched; the whole structure re-derivable by `axon reindex` (S9 holds).
- **Index:** facts land in SQLite (entity, predicate-ish key, interval, embedding) purely as an index for retrieval and contradiction detection.
- **Supersedence, not coexistence:** extends 1.1's C1 — "moved London → Tokyo" invalidates the old fact with a tombstone + interval close, via the existing reconcile review-queue flow. Nothing is deleted.
- **Sleep-time consolidation:** a nightly/weekly automation (routine tier, chokepoint, change-gated) compacts recent episodes into facts and proposes prunes of stale episodic noise to the review queue.
- **Gate:** a seeded life-change produces one reconcile proposal; accept closes the old fact's interval and the projection in `MEMORY.md` reads correctly; `reindex` rebuilds the fact index byte-equivalently; SessionStart injection now prefers *currently-valid* facts and stays within its token ceiling.

### R2 — Contradiction-aware ask (S)
When retrieval surfaces sources that disagree, the answer says so: both claims cited with their dates, newest-valid preferred, no silent averaging. Rides on R1's intervals; no consumer product does this well yet.
**Gate:** a vault seeded with two dated, conflicting notes yields an answer that flags the conflict and cites both; non-conflicting corpora are unaffected.

### R3 — Entity pages (M, 1.1 C2 carry-over) · ADR as planned
Classify-tier extraction of people/projects into auto-maintained entity notes with `axon:mentions` blocks. Unchanged from the 1.1 plan; lands in 1.2 because R1 and C3 both want entities to link to.
**Gate:** as specified in `14-roadmap-1.1.md` (wikilink-safe accrual, prose untouched, content-hash gated).

### R4 — Project pulse (S, 1.1 C3 carry-over)
Weekly pulse block per active project + stale-project nudges via review queue. Now also feeds 1.3's briefing.
**Gate:** as specified in the 1.1 plan.

### R5 — Local synthesis validation & routine-tier promotion (M) · needs ADR
Turn the ADR-015 validation gate into a **promotion procedure**:
- An eval harness (`axon eval`) with golden sets drawn from AXON's own real tasks (triage classifications, digest summaries, consolidation rewrites) and graded pass/fail by rubric; runs against any `(provider, model)` pair.
- A config-gated promotion: `models.routine: ollama:<model>` becomes supported **only** when the harness passes thresholds on this machine; `doctor` reports eval status; silent regressions caught by re-running evals on model/version change (embedding-model discipline, extended to chat models).
- Cascade with verification for promoted tiers: local attempt → cheap local verifier → escalate to Claude on low confidence, all ledgered.
- `synthesis` (user-facing prose: ask answers, digests) stays Claude until the harness says otherwise *per task family* — grounded in evals, not vibes.
- **Gate:** with a passing local routine model configured, a full automation cycle runs with measurably fewer Claude tokens (target: ≥50% of routine-tier calls local) and zero unledgered calls; with Ollama down, everything degrades to Claude transparently.

### R6 — Optional local reranker (S, 1.1 B2 carry-over)
As planned: `retrieval.rerank: off | ollama:<model>`, budget-exempt, ledgered, pass-through on failure.

### R7 — Near-duplicate merge proposals (M, 1.1 B3 carry-over)
As planned, with its own destructive-op design pass (merge + archive originals + rewrite inbound links; never deletion). Stays last in the order.

### R8 — Ambient related-notes surface (S)
Expose the embeddings AXON already has as a **live "related to what I'm looking at" surface**: `axon related <note>`, a `vault_related` MCP tool, and a dashboard panel; a documented localhost endpoint so an Obsidian sidebar plugin (community or later ours) can consume it. Zero model calls — pure vector math. (Smart Connections charges $20/mo for exactly this moat.)
**Gate:** related list for a note returns in <100 ms warm, respects the ANN seam, and makes no model call.

### R9 — Resurfacing with review scheduling (S)
Upgrade the resurfacer from "similar old note" to a light **FSRS-flavoured review queue for ideas**: stale-but-relevant notes and R1-detected "this new note echoes/contradicts [[old note]]" pairs scheduled into the weekly review at spaced intervals, with proposal memory so items surface once per interval.
**Gate:** a resurfaced item declined this week does not reappear next week; intervals lengthen on acceptance.

**Release criterion 1.2:** R1 + R5 shipped, plus at least two of the carry-overs (R3, R4, R6, R7). Leftovers roll to 1.3 without renumbering.

## Release 1.3 — "Perceive & research" (richer inputs, cited knowledge)

**Theme:** widen what AXON can take in — images and screenshots become understood, searchable notes; long-form video/podcast URLs become linked, cited knowledge; and questions flagged for depth trigger bounded, cited web research. Richer inputs in, cited knowledge out. Every new input surface is opt-in, policy-guarded, and off by default on the work profile.

> **Scope note (2026-07-10).** 1.3 originally scoped seven slices under a "reach" theme (outward channels, voice, calendar). Five were **removed** — channel delivery & capture-back, the meeting & voice pipeline, calendar & email read-only context, continuous-capture import, and Obsidian CLI / Bases integration — to focus the release on perceiving richer inputs and researching them. The two survivors below are renumbered H1/H2. The removed outward-channel and voice work is **not currently scheduled**; it may be reconsidered in a later roadmap on its own merits. See `17-roadmap-1.3.md` for the build plan.

### H1 — Multimodal ingestion (M)
Extend the ingestion pipeline beyond text/PDF:
- **Images & screenshots:** OCR (ADR-026, exists) + a vision-model description/tagging pass — `ollama:<vision-model>` (Qwen-VL class) now; the Apple FM image-input tier when macOS 27 ships (fall 2026) behind the same provider seam. Screenshots become searchable, tagged source notes.
- **YouTube/podcast:** URL → transcript (native captions when available) → the standard enrich/summarise/link/embed pipeline; a caption-less URL is captured and flagged (local STT is out of 1.3 scope). Closes the gap with Recall/NotebookLM.
- **Gate:** a screenshot ingests into a retrievable note whose description was produced locally; a YouTube URL with captions yields a cited source note; both idempotent by content hash; vision provider absent ⇒ OCR-only note, no crash.

### H2 — Deep research automation (M) · needs ADR (bounded web egress)
Standing research questions (1.1 A3) graduate: a question flagged `deep` triggers a **bounded, budgeted web research run** — N fetches through the existing Fetcher/egress/redaction machinery, sources ingested as regular Knowledge notes, one synthesis-tier report note with wikilink citations linked to the triggering question/project. Budget-capped per run; personal profile only by default.
**Gate:** one flagged question produces one report + its source notes, all within the declared token/fetch budget; un-flagged questions behave exactly as in 1.1; a denied domain is never fetched.

**Release criterion 1.3:** H1 + H2 both shipped, each passing its acceptance gate. Either may ship first; neither depends on the removed slices.

## Cross-release requirements

- Every new model call through the chokepoint; local calls budget-exempt but ledgered (ADR-015). New spend surfaces (1.3 H1 vision, 1.3 H2 research) get per-automation budgets and appear in the dashboard token chart split.
- Every writer wikilink-safe (managed blocks or additive); no deletes anywhere (archive only).
- Every feature independently toggleable; all-off still runs and is useful (S8); `doctor` reports each new subsystem.
- New egress (1.3 H2 research fetches) passes the policy engine: allowlist, redaction, per-profile deny-by-default on work.
- Each slice: brainstorm → spec → ADR → FR rows → TDD plan → live smoke (the standing cycle). PRD-level numbers here are provisional by design.

## Risks

| Risk | Release | Mitigation |
|---|---|---|
| Temporal memory over-engineered for a single user | 1.2 | R1's representation stays plain markdown + one SQLite table; Graphiti-style bi-temporal modelling explicitly out of scope — intervals + supersedence only |
| Local-model quality regresses silently across model updates | 1.2 | Evals re-run on model change (R5); promotion is per task family, revocable by `doctor` |
| Apple FM image input slips past fall 2026 | 1.3 | H1 ships on Ollama vision first; Apple FM is an upgrade behind the same seam |
| Research run leaks data or over-spends | 1.3 | Redaction pre-send, allowlist egress, per-run fetch/token budget, personal-profile-only default, work-profile off |
| Scope: two releases planned while 1.1 unfinished | both | Nothing starts before 1.1's release criterion; carry-overs keep their existing specs; this PRD feeds the standing design cycle rather than bypassing it |

## Assumptions & open questions (owner review)

*(1.2's open questions are resolved — 1.2 shipped 2026-07-10. The questions below are the live 1.3 ones after the 2026-07-10 rescope.)*

1. **Vision seam (H1):** does the vision-model pass warrant its own ADR, or does it ride ADR-013 (compiled helper) + ADR-026 (OCR provider)? Decide once the Ollama-vision call shape is prototyped.
2. **Image description trigger (H1):** always-on description for every image, or OCR-first-then-vision-only-if-text-is-sparse (the ADR-026 fallback shape)?
3. **Research fetch strategy (H2):** start with direct allow-listed fetches on the existing egress engine, or introduce a search-API provider seam now? Plan assumes direct allow-listed fetches first; a search provider is a later seam.
4. **Research budget defaults (H2):** what per-run fetch count and token ceiling ship as defaults, and is personal-profile-only the right default gate?
