# 17 — Roadmap 1.3 *(plan — "perceive & research")*

1.0 built the self-maintaining vault (capture, ingestion, search, automations,
memory, budgets, observability — FR-01…107). 1.1 made it **answer** (grounded
`ask`, ANN retrieval, standing research questions — FR-108…133). 1.2 made it
**remember and reason** (temporal facts, contradiction-aware ask, resurfacing,
merge proposals, an eval-gated local tier — FR-134…156). 1.2.5 made it **act**
(GTD actions: one trusted list, dashboard tab, the hash-addressed complete
mutation — FR-157…170). **1.3 widens what AXON can *take in***: images and
screenshots become understood, searchable, tagged notes; long-form video and
podcast URLs become linked, cited knowledge; and questions flagged for depth
trigger bounded, budgeted web research that lands as cited vault notes. 1.2
deepened what AXON *knows*; 1.3 widens what AXON can *perceive and research* —
richer inputs in, cited knowledge out.

Both slices reach into new input surfaces — the camera roll, a video URL,
allow-listed research fetches — which is exactly why they land under the same
unchanged constitution: local-first, every model call through the chokepoint,
every write wikilink-safe, everything toggleable, all-off still useful (S8), the
vault rebuilds the DB and never the reverse (S9). New perception and outbound
research are the point of the release *and* the risk of it, so §"Ingestion
constitution" below is the contract these slices are held to.

This document is the 1.3 *plan*, in the style of the earlier roadmap docs
(`14`, `15`, `16`) and graduated from the vault thinking-notes (`docs/Axon
1.2–1.3 — PRD/Purpose/Development Plan/Research Notes`). Every slice still runs
its own design cycle (brainstorm → spec → **ADR** → FR rows → TDD → live smoke)
before any code. FR/ADR numbers here were **provisional** — pre-1.3 maxima were
**FR-170, ADR-034, migration 0007**. **H1 shipped and consumed FR-171…173 +
ADR-035** (no migration); **H2 is next, taking FR-174… + ADR-036**. A slice
isn't "done" until its acceptance gate passes.

> **Scope note (2026-07-10).** 1.3 originally scoped seven slices under a
> "reach" theme. Five were **removed** — channel delivery & capture-back,
> the meeting & voice pipeline, calendar & email read-only context,
> continuous-capture import, and Obsidian CLI / Bases integration — to focus the
> release on **perceiving richer inputs and researching them**. The two
> surviving slices (multimodal ingestion, deep research) are renumbered H1/H2
> below. The removed outward-channel and voice work is **not currently
> scheduled**; it may be reconsidered in a later roadmap on its own merits.

## Ingestion constitution *(read this first)*

Five rules bind both slices below. They are the trust machinery the hosted
proactive-assistant products lack, and they are enforced in code and policy
tests, never by asking the model nicely.

1. **Opt-in, off by default, deny-by-default on work.** Every new provider
   (vision, research) and every egress destination ships **disabled**. On the
   work profile they default **off** and stay off unless explicitly enabled per
   profile. Deep research defaults to the **personal profile only**. A fresh
   install perceives and reaches nowhere until the owner says so.
2. **Egress passes the policy engine or it does not happen.** Every outbound
   research-fetch host must be explicitly **allow-listed**; redaction (NFR-06)
   applies **pre-send** to anything sent to search; the deny path is a
   first-class tested case: feature off, or domain not allow-listed, or work
   profile ⇒ **zero egress, zero writes**, asserted.
3. **Perception is local; content is data, never commands.** OCR (ADR-026) and
   vision/description run **on-device or via Ollama** through the ADR-013
   compiled-helper / provider-seam pattern — **no cloud vision** as the default
   path. Captions, image descriptions, and fetched pages are treated as ingested
   data (NFR-05): AXON never executes instructions found inside a screenshot, a
   transcript, or a fetched page.
4. **New spenders are ledgered, budgeted, and split on the dashboard.** H1
   vision descriptions (when routed to Claude) and H2 research are new Claude
   spend surfaces. Each goes through the chokepoint with a **per-automation
   budget line**, appears in the token chart split from day one, and prefers the
   eval-gated local tier (1.2 R5) where the harness permits.
5. **Archive, never delete.** Ingested images, media, and source material are
   archived (an archive folder / `.trash/`-style move), never deleted (there is
   no `vault.delete`). Every writer stays wikilink-safe.

## Phase H — Perceive & research *(build in this order)*

### H1 — Multimodal ingestion (M) · **SHIPPED 2026-07-10** — FR-171/172/173, ADR-035
> **Status (2026-07-10).** Shipped to `main`. Images ingest via OCR-first
> (Apple/tesseract, incl. a new Swift `--image` helper mode) then a **local
> Vision seam** (`ollama:<model>` now, Apple image tier behind the same seam)
> when OCR is sparse; the source image is archived to
> `03-Resources/Knowledge/attachments/<hash>.<ext>` and embedded `![[…]]`
> (archive-never-delete). Media URLs (YouTube family + `--media` + `media_hosts`)
> become transcript notes via a detected `yt-dlp`; caption-less/absent URLs land
> as a flagged `00-Inbox` capture with **zero model calls**. Vision is a
> **local perception primitive** (ADR-035, an ADR-015 amendment): budget-exempt,
> **not** chokepoint-routed — no Claude vision path in v1, so cardinal rule 1 is
> untouched. Images are **CLI-only** (the `AllowLocalFiles`/SSRF guard); every
> writer wikilink-safe. Config: `ingestion.vision`/`media_hosts`/`caption_langs`;
> advisory `vision`+`media` doctor checks. **No new automation, MCP tool, or
> migration.** Spec: `docs/superpowers/specs/2026-07-10-h1-multimodal-ingestion-design.md`;
> plan: `docs/superpowers/plans/2026-07-10-h1-multimodal-ingestion.md`. Gate met
> (live-smoked on macOS with real Ollama-vision + `yt-dlp`; the real-network
> caption *download* is externally blocked here and covered by unit tests).

**Build:** extend the ingestion pipeline beyond text/PDF. **Images &
screenshots:** OCR (ADR-026, already shipped) **+** a vision-model
description/tagging pass — `ollama:<vision-model>` (Qwen-VL class) now, the
Apple FM image-input tier when macOS 27 ships (~fall 2026) behind the **same
provider seam**; screenshots become searchable, tagged source notes. **YouTube
/ podcast:** URL → transcript (native captions when available) → the standard
enrich / summarise / link / embed pipeline (closes the Recall/NotebookLM gap);
when captions are absent the item is **captured and flagged** (local STT is out
of 1.3 scope — see the scope note above). The vision pass is a **new model-call
type** and goes through the chokepoint (local-first, budgeted); **ADR-035** is
provisional — it records the vision provider seam only if that seam is genuinely
new decision surface, otherwise this slice rides ADR-013 (compiled helper) +
ADR-026 (OCR provider) and no ADR is minted.
**Decisions at build:** whether the vision seam is a distinct ADR or a reuse of
ADR-013/026 *(decide once the Ollama-vision call shape is prototyped)*; caption
source priority for YouTube (native captions vs a future STT pass)
*(recommendation: native captions first; no STT in 1.3)*; whether image
description is always-on or OCR-first-then-vision-if-sparse (the ADR-026 fallback
shape).
**Gate:** a screenshot ingests into a retrievable note whose description was
produced **locally**; a YouTube URL with captions yields a cited source note;
both **idempotent by content hash** (re-ingest is a no-op); vision provider
absent ⇒ OCR-only note, no crash; a caption-less URL is captured and flagged,
nothing crashes.

### H2 — Deep research automation (M) · provisional FR-174/175/176, ADR-036
**Build:** graduate the shipped **A3 `research-questions`** automation. A
question flagged `deep` (in the human region of `03-Resources/Research
Questions.md`) triggers a **bounded, budgeted web research run**: N fetches
through the **existing Fetcher / egress-policy / redaction** machinery, sources
ingested as regular Knowledge notes, then **one synthesis-tier report note**
with `[[wikilink]]` citations, linked back to the triggering question and any
`[[project]]`. **Budget-capped per run** (fetch count *and* token budget);
**personal profile only** by default (constitution §1/§4). Un-flagged questions
behave exactly as they do today. **ADR-036** records the bounded web-egress
policy for research: allow-list discipline, per-run fetch/token ceilings,
redaction of anything sent to search, and the deny-path guarantee.
**Decisions at build:** search/fetch strategy — direct allow-listed fetches vs a
search-API provider seam *(recommendation: start with allow-listed direct
fetches + the existing egress engine; a search provider is a later seam)*;
per-run budget defaults (fetches, tokens); report note location & naming
*(recommendation: `03-Resources/Research/<question>.md`)*.
**Gate:** one `deep`-flagged question produces **one report + its source
notes**, all within the declared token/fetch budget; un-flagged questions are
unchanged; a **denied domain is never fetched** (policy test); running on the
work profile with research off ⇒ no egress.

## Suggested build order & sizing

| Order | Slice | Size | Why here |
|-------|-------|------|----------|
| 1 | H1 multimodal ingestion | M | **SHIPPED 2026-07-10.** Table stakes; Ollama-vision first, Apple image tier behind the same seam. FR-171/172/173, ADR-035. |
| 2 | H2 deep research | M | **NEXT.** Highest new token spend — wants R5's local savings banked first. ADR-036, personal-only. |

The two are largely independent — build in either order. H1 is the broader
table-stakes slice (more surfaces reuse the vision/OCR seam); H2 is the higher
new-spend slice that benefits from the eval-gated local tier (1.2 R5) being
proven first.

**Release criterion 1.3:** ships when **both H1 (multimodal ingestion) and H2
(deep research)** land, each passing its acceptance gate. **H1 shipped
2026-07-10; H2 is the last remaining slice.** Neither depends on the removed
slices.

## Config & observability *(accumulated across slices, all defaults shown)*

```yaml
vision:                          # H1
  provider: "off"                # off | "ollama:<vision-model>" | "apple"
research:                        # H2
  enabled: false                 # personal profile only
  max_fetches: 8
  budget_tokens: 120_000
policy:
  egress_allow: []               # research fetch domains — explicit, per-profile
automations:
  deep-research:    { enabled: false, schedule: "@flag",  model: synthesis, budget_tokens: 120_000 }
```

*(Illustrative — final keys are fixed per slice at build.)* Every new subsystem
gets an advisory `doctor` check (vision provider present, egress allow-list
sanity); every ingest / research run is a bus event (dashboard ≤5s, NFR-07); new
Claude spend appears in the token-chart split (H1 vision, H2 research), local
calls budget-exempt but ledgered (ADR-015).

## Cross-cutting rules

- Every new model call through the **chokepoint**; local vision/OCR/rerank calls
  are perception/retrieval primitives — budget-exempt but **ledgered** where they
  touch Claude (cardinal rule 1 intact).
- Every writer **wikilink-safe**: managed blocks (`axon:summary`, the research
  report note) or additive appends; **no deletes anywhere** (archive only); no
  agent-driven `vault_move`.
- Every feature **independently toggleable**; all-off still runs and is useful
  (S8); `doctor` reports each new subsystem; the vault rebuilds the DB, never
  the reverse (S9).
- Every new egress (research fetches) passes the **policy engine**: allow-list,
  redaction pre-send, per-profile deny-by-default on work — with an asserted
  zero-egress deny path per slice.
- Each slice: brainstorm → spec → **ADR** → FR rows → TDD plan → inline
  execution → live smoke → merge + push (the standing cycle). Reassign
  provisional FR/ADR numbers at build.

## Explicit non-goals for 1.3

Hosted / multi-user anything; a native app (dismissed 2026-07-03); **cloud STT
or cloud vision** as a default path; agent-initiated egress or mutation;
**recording** of any kind (AXON ingests material the owner already has, it never
opens a microphone or screen recorder). Also explicitly **out of 1.3 scope**
(removed 2026-07-10, not currently scheduled): channel delivery & capture-back,
the meeting & voice pipeline, calendar & email read-only context,
continuous-capture import, and Obsidian CLI / Bases integration. The senses
point inward to the vault; the vault stays the source of truth.
