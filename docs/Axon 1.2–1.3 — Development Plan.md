---
title: Axon 1.2–1.3 — Development Plan
type: project
status: draft
tags: [axon, plan, roadmap]
---

# AXON 1.2 & 1.3 — Development Plan

> Companion to [[Axon 1.2–1.3 — PRD]] and [[Axon 1.2–1.3 — Purpose]]. Planning only; nothing starts before 1.1's release criterion passes. Format mirrors the repo's roadmap docs (`11-build-roadmap.md`, `14-roadmap-1.1.md`): ordered slices, S/M sizes, acceptance gates. Every slice still runs the standing cycle (brainstorm → spec → ADR → FR rows → TDD → live smoke) — this plan fixes order and dependencies, not final numbering.

## Dependency picture

```
1.1 leftovers:        B2 reranker   C2 entities   C3 pulse   B3 merge
                                        │            │
1.2:  R5 local-tier   R1 temporal ──────┼────────────┤
      (eval harness)   memory           │            │
            │            │  └ R2 contradiction-ask
            │            │  └ R9 resurfacing/review
            │            └──────────────┐
1.3:  H1 multimodal ◄── R5 vision seam, ADR-026 OCR
      H2 research   ◄── A3 questions, R5 local savings
```

*(1.3 rescoped 2026-07-10 to these two slices; the former channel/voice/calendar/import/Bases slices were removed — see [[Axon 1.2–1.3 — PRD]] and `17-roadmap-1.3.md`.)*

Two long poles: **R1** (memory representation touches MEMORY.md format, SessionStart injection, C1 reconcile flow, reindex) and **R5** (eval harness + router cascade). They are independent of each other — build them in parallel lanes if 1.1 finishing leaves appetite, otherwise R1 first (it unlocks more of the tree).

## 1.2 build order

| # | Slice | Size | Why here | Gate (abridged) |
|---|-------|------|----------|-----------------|
| 1 | R3 entity pages (C2) | M | Carry-over with an existing spec path; R1, R4, H2 all link to entities | Mentions accrue wikilink-safely; extraction content-hash gated |
| 2 | R1 temporal memory | M | The headline; wants entities to exist for fact subjects | Reconcile closes intervals; `reindex` rebuilds fact index; injection prefers valid facts |
| 3 | R5 local-tier promotion + `axon eval` | M | Frees budget for everything after; independent lane, can start alongside 1–2 | ≥50% routine calls local with passing evals; transparent degradation |
| 4 | R2 contradiction-aware ask | S | Cheap once R1's intervals exist | Conflicting dated sources flagged + both cited |
| 5 | R4 project pulse (C3) | S | Rides entities; weekly pulse + stale-project nudges | Stale project → one nudge, proposal memory |
| 6 | R8 related-notes surface | S | Zero-model, high visible value; exercises ANN seam | <100 ms warm, no model call |
| 7 | R9 resurfacing/review scheduling | S | Rides R1 signals + resurfacer primitives | Declined item respects interval; intervals adapt |
| 8 | R6 reranker (B2) | S | Quality knob, slot anywhere; kept late as pure polish | Ordering-only effect; zero Claude tokens |
| 9 | R7 merge proposals (B3) | M | Own destructive-op design pass; deliberately last (unchanged from 1.1 reasoning) | Zero broken links; originals recoverable |

**Ship 1.2 when:** R1 + R5 done plus two of {R3, R4, R6, R7} — expected shape: slices 1–5 in, 6–9 as they land, stragglers roll to 1.3.

## 1.3 build order

| # | Slice | Size | Why here | Gate (abridged) |
|---|-------|------|----------|-----------------|
| 1 | H1 multimodal ingestion | M | Table stakes; Ollama-vision first, Apple FM upgrade later behind the seam | Screenshot + captioned YouTube URL → retrievable notes, hash-idempotent; vision absent ⇒ OCR-only, no crash |
| 2 | H2 deep research | M | Highest new token spend — wants R5's savings banked first | One flagged question → budgeted report + source notes; denied domains never fetched |

The two are largely independent — build in either order. **Ship 1.3 when:** H1 + H2 both done, each passing its gate.

## New seams & ADR workload (design-cycle budget)

Expected new ADRs (provisional): R1 memory representation & fact index; R5 eval-gated model promotion (extends ADR-015); R7 merge accept semantics (already flagged in 1.1); 1.3 H2 bounded research egress (ADR-036). 1.3 H1 multimodal likely rides existing ADRs (ADR-013 compiled helper + ADR-026 OCR); it mints an ADR only if the vision-call shape is a genuinely new decision surface (ADR-035 provisional). That is ~4 design cycles across the two releases — consistent with 1.1's cadence.

## Testing strategy deltas

- **R5 makes evals first-class:** golden sets live in-repo, run in CI against fakes and locally against real Ollama (`--profile test`); promotion state is config + doctor-visible, never implicit.
- **Policy tests grow teeth for 1.3:** the research slice (H2) adds deny-path tests (feature off / domain not allow-listed / work profile) asserting *zero* egress and zero writes — the S8 discipline applied to research egress.
- **Vision fixtures:** small committed fixtures (a sample screenshot, a captions file) keep H1 multimodal testable without network or real recordings.
- Live smoke per slice on the scratch vault, as today.

## Token & budget posture

1.2 is net-negative on Claude spend by design (R5 moves routine work local; R1 consolidation replaces repeated context with compact facts). 1.3 introduces two new spenders (H1 vision descriptions when routed to Claude, H2 research) — each gets a per-automation budget line and dashboard split from day one, and H2 research defaults to the personal profile only. Sequencing R5 before H2 research is deliberate: bank the savings before spending them.

## Out of scope (both releases)

Hosted/multi-user anything; native app; agent-driven `vault_move`; recording of any kind; cloud STT or cloud vision as a default path; bi-temporal Graphiti-style memory modelling (intervals + supersedence only). Also removed from 1.3 on 2026-07-10 (not currently scheduled): channel delivery & capture-back, the meeting & voice pipeline, calendar & email read-only context, continuous-capture import, and Obsidian CLI / Bases integration.

## Next actions

- [ ] Finish 1.1 (B2/C2/C3/B3 or ship at criterion and roll them)
- [ ] Owner review of [[Axon 1.2–1.3 — PRD]] §Assumptions (H1 vision seam/ADR, image-description trigger; H2 research fetch strategy, budget defaults)
- [ ] Graduate 1.2 into `docs/15-roadmap-1.2.md` via the standing design cycle when adopted
