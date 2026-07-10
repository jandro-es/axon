# 17 — Roadmap 1.3 *(plan — "reach")*

1.0 built the self-maintaining vault (capture, ingestion, search, automations,
memory, budgets, observability — FR-01…107). 1.1 made it **answer** (grounded
`ask`, ANN retrieval, standing research questions — FR-108…133). 1.2 made it
**remember and reason** (temporal facts, contradiction-aware ask, resurfacing,
merge proposals, an eval-gated local tier — FR-134…156). 1.2.5 made it **act**
(GTD actions: one trusted list, dashboard tab, the hash-addressed complete
mutation — FR-157…170). **1.3 makes it *reach* you**: the brain acquires senses
and a voice. Meetings and voice memos become linked notes; images and
screenshots are understood; briefings arrive where you already are; bounded web
research lands as cited vault notes; your calendar colours the day. 1.2 deepened
what AXON *knows*; 1.3 widens what AXON *touches*.

Every new surface here reaches **outward** — the network, the microphone, the
camera roll, a messaging channel — which is exactly why it lands under the same
unchanged constitution: local-first, every model call through the chokepoint,
every write wikilink-safe, everything toggleable, all-off still useful (S8), the
vault rebuilds the DB and never the reverse (S9). Reaching outward is the whole
point of the release *and* the whole risk of it, so §"Reach constitution" below
is the contract the outward-facing slices are held to.

This document is the 1.3 *plan*, in the style of the earlier roadmap docs
(`14`, `15`, `16`) and graduated from the vault thinking-notes (`docs/Axon
1.2–1.3 — PRD/Purpose/Development Plan/Research Notes`). Every slice still runs
its own design cycle (brainstorm → spec → **ADR** → FR rows → TDD → live smoke)
before any code. FR/ADR numbers here are **provisional** — current maxima:
**FR-170, ADR-034, migration 0007** — and are assigned for real in each slice's
cycle. Provisional range for the release: **FR-171…187, ADR-035…038**. A slice
isn't "done" until its acceptance gate passes.

## Reach constitution *(read this first)*

Six rules bind every outward-facing slice below. They are the trust machinery
the hosted proactive-assistant products lack, and they are enforced in code and
policy tests, never by asking the model nicely.

1. **Opt-in, off by default, deny-by-default on work.** Every new channel,
   microphone, camera, and egress destination ships **disabled**. On the work
   profile they default **off** and stay off unless explicitly enabled per
   profile. A fresh install reaches nowhere until the owner says so.
2. **Egress passes the policy engine or it does not happen.** Every outbound
   host — `api.telegram.org`, every research fetch domain — must be explicitly
   **allow-listed**; redaction (NFR-06) applies **pre-send**; identity-layer
   content never leaves except the exact fields a briefing template names. The
   deny path is a first-class tested case: feature off, or domain not
   allow-listed, or work profile ⇒ **zero egress, zero writes**, asserted.
3. **Capture-only inbound, until it earns more.** Messages arriving over a
   channel land in `00-Inbox/` through the existing capture funnel (the D1
   `POST /api/capture` pattern) and are triaged on the next tick. **No
   approve/dismiss of review-queue items over a channel in 1.3** — mutations
   stay loopback-guarded (ADR-020/023/034). A remote actor can *give* AXON
   material; it cannot *act* on the vault.
4. **Senses transcribe and describe locally; content is data, never
   commands.** STT and vision run on-device or via Ollama through the
   ADR-013 compiled-helper / provider-seam pattern — **no cloud STT, no cloud
   vision** as the default path. Transcripts, captions, and image descriptions
   are treated as ingested data (NFR-05): AXON never executes instructions
   found inside a recording, a screenshot, or a fetched page.
5. **New spenders are ledgered, budgeted, and split on the dashboard.** H2
   summaries, H3 vision descriptions (when routed to Claude), and H4 research
   are new Claude spend surfaces. Each goes through the chokepoint with a
   **per-automation budget line**, appears in the token chart split from day
   one, and prefers the eval-gated local tier (1.2 R5) where the harness
   permits. H4 defaults to the **personal profile only**.
6. **Archive, never delete; import, never record.** Audio files, screenshots,
   and imported captures are archived (`.trash/`-style or an archive folder),
   never deleted (there is no `vault.delete`). H6 is an **importer** of
   existing exports, not a recorder — AXON does not become surveillance
   software, and no slice here opens a microphone unbidden.

## Phase H — Reach *(build in this order)*

### H1 — Channel delivery & capture-back (M) · provisional FR-171/172/173, ADR-035
**Build:** a **channel-provider seam** (first provider: a Telegram bot over the
direct HTTPS Bot API; the seam admits ntfy / email later — WhatsApp explicitly
deferred, heavier ToS/infra). **Outbound:** the morning briefing rendered
channel-sized — heartbeat data, the **GTD actions counter** (`tasks: N open,
M overdue` — the counter 1.2.5 T2 already computes), the review-queue count,
budget status, project pulse (1.2 R4), and today's calendar once H5 lands;
plus digest/pulse notifications. Composition is **no-model or classify-tier**
— a briefing is a template fill, not a synthesis call. **Inbound:** a text or
URL sent to the bot lands in `00-Inbox/` through the existing capture funnel
(the D1 pattern; URL on its own first line so the capture automation ingests
it) — **capture-only** (constitution §3). **ADR-035** records the channel
provider seam *and* the egress surface: what may leave, redacted, template-only,
per-profile, work-off — the outbound analogue of ADR-023's inbound trust
boundary.
**Decisions at build:** channel default — Telegram (cleanest bot API, no
business verification) vs ntfy/email-only *(plan-of-record: Telegram first,
seam-pluggable)*; whether the briefing is a new `channel-briefing` automation
or an outbound sink on the existing heartbeat *(recommendation: a thin
automation reading existing data, no new synthesis)*; poll vs webhook for
inbound *(recommendation: long-poll — no inbound port, no public URL)*.
**Gate:** a briefing arrives on schedule with **zero model calls** and nothing
outside the template; a text/URL sent to the bot is in `00-Inbox/` and triaged
on the next tick; with the feature off **or** `api.telegram.org` not
allow-listed **or** on the work profile ⇒ **zero egress** (policy tests);
identity content never appears in an outbound message beyond the named
template fields.

### H2 — Meeting & voice pipeline (M) · provisional FR-174/175/176, ADR-036
**Build:** an audio file dropped in a watched folder (or captured via Shortcuts)
becomes a linked note. **Local transcription** behind an audio provider seam
(`whisper.cpp` portable, or Apple Speech via the ADR-013 compiled-helper
pattern — **no cloud STT**) → a **synthesis-tier** speaker-aware summary +
decisions + **action items** → written to a meeting note (and/or the daily
note) with `[[person]]` / `[[project]]` links resolved against **1.2 R3 entity
pages**. Action items materialise as **real checkboxes in the meeting note's
`axon:tasks` managed block** — the 1.2.5 T6 accept-pattern — so they are indexed
by the T1 actions parser and flow straight into the GTD system (the consolidated
`Actions.md`, the dashboard tab, completion). Ambiguous entities that don't
resolve unambiguously stay plain text and go to the **review queue** (1.2
R9 machinery) as link suggestions rather than guessing. **ADR-036** records the
audio provider seam (a pattern-copy of ADR-013/026) and the below-confidence
handling.
**Decisions at build:** STT default — `whisper.cpp` (portable, model-size
choice) vs Apple Speech helper (zero-install, macOS-only) *(plan-of-record:
whisper.cpp default, Apple as the macOS fast path behind the same seam)*;
transcript-note shape (one note with transcript + `axon:summary` block, vs a
transcript note linked from a summary note) *(recommendation: one note, summary
in a managed block, raw transcript below the fold)*; confidence floor below
which the summary is marked tentative.
**Gate:** a test recording yields a transcript note + a linked summary with
**zero egress**; entities link only when they resolve unambiguously (else plain
text + a review-queue suggestion); extracted action items are indexed,
completable tasks; audio files are **archived, never deleted**; STT unavailable
⇒ the file is captured and flagged, nothing crashes.

### H3 — Multimodal ingestion (M) · provisional FR-177/178/179, ADR-037 *(may fold into ADR-013/026)*
**Build:** extend the ingestion pipeline beyond text/PDF. **Images &
screenshots:** OCR (ADR-026, already shipped) **+** a vision-model
description/tagging pass — `ollama:<vision-model>` (Qwen-VL class) now, the
Apple FM image-input tier when macOS 27 ships (~fall 2026) behind the **same
provider seam**; screenshots become searchable, tagged source notes. **YouTube
/ podcast:** URL → transcript (captions when available; else H2's local STT) →
the standard enrich / summarise / link / embed pipeline (closes the
Recall/NotebookLM gap). The vision pass is a **new model-call type** and goes
through the chokepoint (local-first, budgeted); **ADR-037** is provisional — it
records the vision provider seam only if that seam is genuinely new decision
surface, otherwise this slice rides ADR-013 (compiled helper) + ADR-026 (OCR
provider) and no ADR is minted.
**Decisions at build:** whether the vision seam is a distinct ADR or a reuse of
ADR-013/026 *(decide once the Ollama-vision call shape is prototyped)*; caption
source priority for YouTube (native captions vs STT) *(recommendation: native
captions first, STT fallback)*; whether image description is always-on or
OCR-first-then-vision-if-sparse (the ADR-026 fallback shape).
**Gate:** a screenshot ingests into a retrievable note whose description was
produced **locally**; a YouTube URL yields a cited source note; both
**idempotent by content hash** (NFR re-ingest is a no-op); vision provider
absent ⇒ OCR-only note, no crash.

### H4 — Deep research automation (M) · provisional FR-180/181/182, ADR-038
**Build:** graduate the shipped **A3 `research-questions`** automation. A
question flagged `deep` (in the human region of `03-Resources/Research
Questions.md`) triggers a **bounded, budgeted web research run**: N fetches
through the **existing Fetcher / egress-policy / redaction** machinery, sources
ingested as regular Knowledge notes, then **one synthesis-tier report note**
with `[[wikilink]]` citations, linked back to the triggering question and any
`[[project]]`. **Budget-capped per run** (fetch count *and* token budget);
**personal profile only** by default (constitution §1/§5). Un-flagged questions
behave exactly as they do today. **ADR-038** records the bounded web-egress
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

### H5 — Calendar & email read-only context (S/M) · provisional FR-183/184, no ADR
**Build:** the 1.1 deferral, scoped tight. **Read-only ICS** (a local calendar
export or a subscription URL) parsed **without model calls** to enrich the
briefing (H1) and the daily note — "today: 3 meetings, first at 09:30".
Optionally read-only **IMAP headers** for a "N unread from people you know"
line, resolved against **1.2 R3 entities**. **No sending, no writes to any
account, no OAuth-heavy Gmail/Calendar API** in this slice (constitution §1/§6).
The ICS URL is an allow-listed egress destination like any other.
**Decisions at build:** H5 scope — read-only ICS only vs adding Gmail/Google
Calendar OAuth *(plan-of-record: **ICS-only**; OAuth deferred unless demand
proves it worth the complexity)*; whether the IMAP-headers line ships in this
slice or is a follow-up.
**Gate:** the briefing shows today's events with **zero model calls** and
**zero writes** to any account; a subscription-URL fetch is allow-listed and
redaction-clean; feature-off leaves **no trace** (no note edits, no egress).

### H6 — Continuous-capture import (S) · provisional FR-185/186, no ADR
**Build:** an **import adapter** for Screenpipe/Limitless-style exports (the
stranded-user wedge — Meta's Limitless shutdown left a base looking for a
local-first home). A documented **drop-folder format** → daily-note appendix or
Knowledge notes via the standard ingestion pipeline. Deliberately an
*importer*, not a recorder (constitution §6): AXON reads exports the owner
produces elsewhere; it never opens a microphone or screen recorder itself.
**Decisions at build:** which export format(s) to support first (Screenpipe
SQLite/JSON export vs a generic timestamped-text drop) *(recommendation: one
documented generic format + one real-world adapter)*; note target (daily-note
appendix vs discrete Knowledge notes) per import type.
**Gate:** a sample export lands as **linked, searchable notes**; **re-import is
idempotent** (content-hash dedup); nothing in the slice captures live data.

### H7 — Obsidian CLI / Bases integration (S, stretch) · provisional FR-187, no ADR
**Build:** watch-and-adopt, not build-ahead. Track the official Obsidian **CLI**
and **Bases**: auto-populate Bases-compatible frontmatter properties from AXON's
index (entity / type / status) so AXON's derived knowledge shows up natively in
Bases views; document the CLI as an additional automation surface once it
stabilises. A stretch slice — adopt when the CLI/Bases APIs settle.
**Decisions at build:** which properties AXON owns vs leaves to the human
*(recommendation: write only into an `axon:`-namespaced property set or a
managed frontmatter region, never clobber human properties — the managed-block
discipline applied to frontmatter)*; whether CLI adoption is documentation-only
or a wired surface.
**Gate:** Bases properties populate from the index wikilink-safely (human
properties untouched); with the feature off, frontmatter is unchanged; no
dependency on unreleased Obsidian APIs on the default path.

## Suggested build order & sizing

| Order | Slice | Size | Why here |
|-------|-------|------|----------|
| 1 | H1 channels (Telegram out + capture-in) | M | The retention feature; only needs heartbeat/pulse/actions data that already exists. ADR-035. |
| 2 | H5 calendar ICS (read-only) | S/M | Small; makes H1's briefing genuinely useful ("first meeting 09:30"). No ADR. |
| 3 | H2 meeting/voice pipeline | M | Most-loved feature in the field; entities (R3) and the actions system (1.2.5) are ready. ADR-036. |
| 4 | H3 multimodal ingestion | M | Table stakes; Ollama-vision first, Apple FM upgrade behind the seam. ADR-037 (provisional). |
| 5 | H4 deep research | M | Highest new token spend — wants R5's local savings banked first. ADR-038, personal-only. |
| 6 | H6 continuous-capture import | S | Independent, opportunistic wedge. No ADR. |
| 7 | H7 Obsidian CLI/Bases | S | Stretch; adopt when the CLI/Bases stabilise. No ADR. |

**Two long poles:** H1 (channel seam + the egress trust surface, ADR-035) and
H2 (audio provider seam + STT, ADR-036). They are independent — build in
parallel lanes if appetite allows, else H1 first (it unblocks delivery for
everything after and reuses the most existing data).

**Release criterion 1.3:** ships when **H1 + H2** are done, plus **at least one
of H3 / H4 / H5**. H6/H7 land as they complete; leftovers roll forward without
renumbering.

## Config & observability *(accumulated across slices, all defaults shown)*

```yaml
channels:                        # H1 — top-level profile block
  telegram:
    enabled: false               # off by default; work profile stays off
    # bot_token in .env, never config
    chat_id: ""
  briefing:
    schedule: "0 7 * * *"
    include: [heartbeat, actions, reviews, budget, pulse, calendar]
audio:                           # H2
  watch_dir: ""                  # empty ⇒ pipeline off
  stt: "off"                     # off | "whisper:<model>" | "apple"
  confidence_floor: 0.6          # below ⇒ summary marked tentative
vision:                          # H3
  provider: "off"                # off | "ollama:<vision-model>" | "apple"
research:                        # H4
  enabled: false                 # personal profile only
  max_fetches: 8
  budget_tokens: 120_000
calendar:                        # H5
  ics_url: ""                    # allow-listed; empty ⇒ off
  imap_headers: false
policy:
  egress_allow: []               # api.telegram.org, ICS host, research domains — explicit
automations:
  channel-briefing: { enabled: false, schedule: "0 7 * * *", model: none,      budget_tokens: 0 }
  meeting-ingest:   { enabled: false, schedule: "@watch",    model: synthesis, budget_tokens: 40_000 }
  deep-research:    { enabled: false, schedule: "@flag",     model: synthesis, budget_tokens: 120_000 }
```

*(Illustrative — final keys are fixed per slice at build.)* Every new subsystem
gets an advisory `doctor` check (channel reachability, STT/vision provider
present, ICS parse, egress allow-list sanity); every send / ingest / research
run is a bus event (dashboard ≤5s, NFR-07); new Claude spend appears in the
token-chart split (H2/H3/H4), local calls budget-exempt but ledgered (ADR-015).

## Cross-cutting rules

- Every new model call through the **chokepoint**; local STT/vision/rerank
  calls are retrieval/transcription primitives — budget-exempt but **ledgered**
  where they touch Claude (cardinal rule 1 intact).
- Every writer **wikilink-safe**: managed blocks (`axon:summary`, `axon:tasks`,
  the briefing template) or additive appends; **no deletes anywhere** (archive
  only); no agent-driven `vault_move`.
- Every feature **independently toggleable**; all-off still runs and is useful
  (S8); `doctor` reports each new subsystem; the vault rebuilds the DB, never
  the reverse (S9).
- Every new egress (Telegram, ICS host, research fetches) passes the **policy
  engine**: allow-list, redaction pre-send, per-profile deny-by-default on
  work — with an asserted zero-egress deny path per slice.
- Each slice: brainstorm → spec → **ADR** → FR rows → TDD plan → inline
  execution → live smoke → merge + push (the standing cycle). Reassign
  provisional FR/ADR numbers at build.

## Explicit non-goals for 1.3

Hosted / multi-user anything; a native app (dismissed 2026-07-03); the WhatsApp
channel (heavier ToS/infra — deferred behind the seam); **cloud STT or cloud
vision** as a default path; sending email or writing to any calendar/mail
account; Gmail/Google Calendar **OAuth** in v1 (unless the H5 open question
resolves otherwise); **approve/dismiss review actions over a channel**
(capture-only inbound in 1.3); **recording** of any kind (H6 imports existing
exports, never captures); agent-initiated egress or mutation. The senses point
inward to the vault; the vault stays the source of truth.
