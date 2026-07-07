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
Classify-tier extraction of people/projects into auto-maintained entity notes with `axon:mentions` blocks. Unchanged from the 1.1 plan; lands in 1.2 because R1, C3 and 1.3's meeting pipeline all want entities to link to.
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

## Release 1.3 — "Reach" (outward senses & delivery)

**Theme:** capture and delivery wherever the owner is; audio and images become first-class; research comes home cited. Every outward surface is opt-in, policy-guarded, and off by default on the work profile.

### H1 — Channel delivery & capture-back (M) · needs ADR (egress + surface)
A **channel provider seam** (first: Telegram bot via direct HTTPS API; the seam admits ntfy/email later — WhatsApp explicitly deferred, heavier ToS/infra):
- **Outbound:** the morning briefing (heartbeat data + calendar when H5 lands + review-queue count + budget status + project pulse) rendered channel-sized; digest/pulse notifications. Composition is no-model or classify-tier.
- **Inbound:** messages to the bot land in `00-Inbox/` through the existing capture funnel (D1 pattern). **v1 of this surface is capture-only** — no approve/dismiss over the channel until it has proven itself (review actions stay loopback-guarded).
- **Policy:** `api.telegram.org` must be explicitly allow-listed; redaction applies pre-send; identity-layer content never leaves except what the briefing template explicitly includes; per-profile — work default **off**.
- **Gate:** a briefing arrives on schedule with no model call and nothing outside the template; a text/URL sent to the bot is in the inbox and triaged on the next tick; with the feature off or the domain not allow-listed, zero egress (verified by the policy tests).

### H2 — Meeting & voice pipeline (M) · needs ADR (audio provider seam)
An audio file dropped in a watched folder (or captured via Shortcuts) becomes: **local transcription** (provider seam: `whisper.cpp` or Apple Speech via the ADR-013 compiled-helper pattern — no cloud STT) → speaker-aware summary + decisions + action items (synthesis tier) → written to the daily note / a meeting note with `[[person]]`/`[[project]]` links resolved against R3's entity pages → tasks into the existing roll-forward flow.
**Gate:** a test recording yields a transcript note + linked summary with zero egress; entities link only when they resolve unambiguously (else plain text + review-queue suggestion); audio files are never deleted, only archived.

### H3 — Multimodal ingestion (M)
Extend the ingestion pipeline beyond text/PDF:
- **Images & screenshots:** OCR (ADR-026, exists) + a vision-model description/tagging pass — `ollama:<vision-model>` (Qwen-VL class) now; the Apple FM image-input tier when macOS 27 ships (fall 2026) behind the same provider seam. Screenshots become searchable, tagged source notes.
- **YouTube/podcast:** URL → transcript (captions when available; else H2's local STT) → the standard enrich/summarise/link/embed pipeline. Closes the gap with Recall/NotebookLM.
- **Gate:** a screenshot ingests into a retrievable note whose description was produced locally; a YouTube URL yields a cited source note; both idempotent by content hash.

### H4 — Deep research automation (M) · needs ADR (bounded web egress)
Standing research questions (1.1 A3) graduate: a question flagged `deep` triggers a **bounded, budgeted web research run** — N fetches through the existing Fetcher/egress/redaction machinery, sources ingested as regular Knowledge notes, one synthesis-tier report note with wikilink citations linked to the triggering question/project. Budget-capped per run; personal profile only by default.
**Gate:** one flagged question produces one report + its source notes, all within the declared token/fetch budget; un-flagged questions behave exactly as in 1.1; a denied domain is never fetched.

### H5 — Calendar & email read-only context (S/M)
The 1.1 deferral, scoped tightly: **read-only ICS** (local calendar export/subscription URL) parsed without model calls to enrich the briefing and daily note ("today: 3 meetings, first at 09:30"); optionally read-only IMAP headers for a "N unread from people you know (R3 entities)" line. No sending, no OAuth-heavy Gmail API in v1 of this slice.
**Gate:** briefing shows today's events with zero model calls and zero writes to any account; feature-off leaves no trace.

### H6 — Continuous-capture import (S)
An import adapter for Screenpipe/Limitless-style exports (the stranded-user wedge): a documented drop-folder format → daily-note appendix or Knowledge notes via the standard pipeline. Deliberately an *importer*, not a recorder — AXON does not become surveillance software.
**Gate:** a sample export lands as linked, searchable notes; re-import is idempotent.

### H7 — Obsidian CLI / Bases integration (S, stretch)
Track the new official Obsidian CLI and Bases: auto-populate Bases-compatible properties from AXON's index (entity/type/status), and document the CLI as an automation surface once it stabilises. Watch-and-adopt, not build-ahead.

**Release criterion 1.3:** H1 + H2 shipped, plus at least one of H3/H4/H5.

## Cross-release requirements

- Every new model call through the chokepoint; local calls budget-exempt but ledgered (ADR-015). New spend surfaces (H2 summaries, H3 vision, H4 research) get per-automation budgets and appear in the dashboard token chart split.
- Every writer wikilink-safe (managed blocks or additive); no deletes anywhere (archive only).
- Every feature independently toggleable; all-off still runs and is useful (S8); `doctor` reports each new subsystem.
- New egress (Telegram, research fetches) passes the policy engine: allowlist, redaction, per-profile deny-by-default on work.
- Each slice: brainstorm → spec → ADR → FR rows → TDD plan → live smoke (the standing cycle). PRD-level numbers here are provisional by design.

## Risks

| Risk | Release | Mitigation |
|---|---|---|
| Temporal memory over-engineered for a single user | 1.2 | R1's representation stays plain markdown + one SQLite table; Graphiti-style bi-temporal modelling explicitly out of scope — intervals + supersedence only |
| Local-model quality regresses silently across model updates | 1.2 | Evals re-run on model change (R5); promotion is per task family, revocable by `doctor` |
| Channel surface leaks personal data | 1.3 | Template-only briefings, redaction pre-send, allowlist egress, capture-only inbound, work-profile off |
| Apple FM image input slips past fall 2026 | 1.3 | H3 ships on Ollama vision first; Apple FM is an upgrade behind the same seam |
| STT quality on real meeting audio | 1.3 | Provider seam allows whisper.cpp model-size selection; summary marked tentative below a confidence floor |
| Scope: two releases planned while 1.1 unfinished | both | Nothing starts before 1.1's release criterion; carry-overs keep their existing specs; this PRD feeds the standing design cycle rather than bypassing it |

## Assumptions & open questions (owner review)

1. **Channel choice:** Telegram first (cleanest bot API, no business verification). Acceptable, or prefer ntfy/email-only to avoid any third-party messenger?
2. **Approve-from-channel** deliberately excluded from H1 v1 — agree, or is remote review-queue action a must-have?
3. **STT provider default:** whisper.cpp (portable, model-size choice) vs Apple Speech helper (zero-install, macOS-only). Plan assumes whisper.cpp default with Apple as the macOS fast path.
4. **H5 scope:** is read-only ICS enough, or is Gmail/Google Calendar OAuth worth its complexity in 1.3? Plan assumes ICS-only.
5. **R5 ambition:** is ≥50% of routine-tier calls local the right first target, or push for routine+digest synthesis too?
6. **Sequencing:** R1 (memory) before R5 (local tier) is the plan's order — flip if budget pressure bites before memory pain does.
