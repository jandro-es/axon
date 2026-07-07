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
            │            │  └ R2 contradiction-ask   │
            │            │  └ R9 resurfacing/review  │
            │            └───────────┐               │
1.3:  H2 meetings ◄── entities       │   H1 channels ◄── pulse, H5 ICS
      H3 multimodal ◄── R5 seams     │   H4 research ◄── A3 questions
      H6 import (independent)        │   H7 CLI/Bases (independent, stretch)
```

Two long poles: **R1** (memory representation touches MEMORY.md format, SessionStart injection, C1 reconcile flow, reindex) and **R5** (eval harness + router cascade). They are independent of each other — build them in parallel lanes if 1.1 finishing leaves appetite, otherwise R1 first (it unlocks more of the tree).

## 1.2 build order

| # | Slice | Size | Why here | Gate (abridged) |
|---|-------|------|----------|-----------------|
| 1 | R3 entity pages (C2) | M | Carry-over with an existing spec path; R1, R4, H2 all link to entities | Mentions accrue wikilink-safely; extraction content-hash gated |
| 2 | R1 temporal memory | M | The headline; wants entities to exist for fact subjects | Reconcile closes intervals; `reindex` rebuilds fact index; injection prefers valid facts |
| 3 | R5 local-tier promotion + `axon eval` | M | Frees budget for everything after; independent lane, can start alongside 1–2 | ≥50% routine calls local with passing evals; transparent degradation |
| 4 | R2 contradiction-aware ask | S | Cheap once R1's intervals exist | Conflicting dated sources flagged + both cited |
| 5 | R4 project pulse (C3) | S | Rides entities; feeds 1.3 briefing | Stale project → one nudge, proposal memory |
| 6 | R8 related-notes surface | S | Zero-model, high visible value; exercises ANN seam | <100 ms warm, no model call |
| 7 | R9 resurfacing/review scheduling | S | Rides R1 signals + resurfacer primitives | Declined item respects interval; intervals adapt |
| 8 | R6 reranker (B2) | S | Quality knob, slot anywhere; kept late as pure polish | Ordering-only effect; zero Claude tokens |
| 9 | R7 merge proposals (B3) | M | Own destructive-op design pass; deliberately last (unchanged from 1.1 reasoning) | Zero broken links; originals recoverable |

**Ship 1.2 when:** R1 + R5 done plus two of {R3, R4, R6, R7} — expected shape: slices 1–5 in, 6–9 as they land, stragglers roll to 1.3.

## 1.3 build order

| # | Slice | Size | Why here | Gate (abridged) |
|---|-------|------|----------|-----------------|
| 1 | H1 channels (Telegram out + capture-in) | M | The retention feature; only needs heartbeat/pulse data that exists | Briefing on schedule, template-only content; inbound lands in inbox; zero egress when off |
| 2 | H5 calendar ICS (read-only) | S/M | Small, makes H1's briefing genuinely useful ("first meeting 09:30") | Events in briefing, no model call, no account writes |
| 3 | H2 meeting/voice pipeline | M | Most-loved feature in the field; entities ready from 1.2 | Local-only transcript → linked summary; ambiguous entities go to review queue |
| 4 | H3 multimodal ingestion | M | Table stakes; Ollama-vision first, Apple FM upgrade later behind the seam | Screenshot + YouTube URL → retrievable notes, hash-idempotent |
| 5 | H4 deep research | M | Highest new token spend — wants R5's savings banked first | One flagged question → budgeted report + source notes; denied domains never fetched |
| 6 | H6 continuous-capture import | S | Independent, opportunistic wedge | Sample export → linked notes, idempotent |
| 7 | H7 Obsidian CLI/Bases | S | Stretch; adopt when the CLI stabilises | Bases properties populated from index |

**Ship 1.3 when:** H1 + H2 done plus one of {H3, H4, H5}.

## New seams & ADR workload (design-cycle budget)

Expected new ADRs (provisional): R1 memory representation & fact index; R5 eval-gated model promotion (extends ADR-015); R7 merge accept semantics (already flagged in 1.1); H1 channel provider + egress surface; H2 audio provider seam (pattern-copy of ADR-013/026); H4 bounded research egress. H3/H5/H6 likely ride existing ADRs. That is ~6 design cycles across two releases — consistent with 1.1's cadence.

## Testing strategy deltas

- **R5 makes evals first-class:** golden sets live in-repo, run in CI against fakes and locally against real Ollama (`--profile test`); promotion state is config + doctor-visible, never implicit.
- **Policy tests grow teeth for 1.3:** every H-slice adds deny-path tests (feature off / domain not allow-listed / work profile) asserting *zero* egress and zero writes — the S8 discipline applied to reach.
- **Audio/vision fixtures:** small committed fixtures (short WAV, sample screenshot, captions file) keep H2/H3 testable without network or real recordings.
- Live smoke per slice on the scratch vault, as today.

## Token & budget posture

1.2 is net-negative on Claude spend by design (R5 moves routine work local; R1 consolidation replaces repeated context with compact facts). 1.3 introduces three new spenders (H2 summaries, H3 vision descriptions when routed to Claude, H4 research) — each gets a per-automation budget line and dashboard split from day one, and H4 defaults to the personal profile only. Sequencing R5 before H4 is deliberate: bank the savings before spending them.

## Out of scope (both releases)

Hosted/multi-user anything; native app; WhatsApp channel; agent-driven `vault_move`; recording (H6 imports, never captures); Gmail OAuth (unless the H5 open question resolves otherwise); bi-temporal Graphiti-style memory modelling (intervals + supersedence only).

## Next actions

- [ ] Finish 1.1 (B2/C2/C3/B3 or ship at criterion and roll them)
- [ ] Owner review of [[Axon 1.2–1.3 — PRD]] §Assumptions (channel choice, STT default, H5 scope, R5 target)
- [ ] Graduate 1.2 into `docs/15-roadmap-1.2.md` via the standing design cycle when adopted
