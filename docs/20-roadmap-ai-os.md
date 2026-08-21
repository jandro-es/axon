# 20 — Roadmap: the AI operating system *(candidates — nothing scheduled)*

The sibling of `docs/19-roadmap-second-brain.md`. That document is about the
**vault** getting smarter; this one is about the **daemon** becoming a platform:
where AXON shows up, what runs on it, which models it can drive, and how it
maintains itself. Everything below is a **candidate slice** — no dates, no
provisional FR/ADR numbers; numbers are assigned only when a slice graduates
through its own brainstorm → spec → ADR → FR rows cycle.

**The two cardinal rules bind every slice on this page, without exception:**
no Claude call bypasses the Component 07 chokepoint, and no vault mutation
bypasses the wikilink-safe ops (there is no `vault.delete`). A platform is
worth having only if the guarantees survive it. Three themes re-propose work
cut from 1.3 on 2026-07-10; each states why the economics changed.

## Theme A — OS-level presence *(macOS 27)*

### A1 — Siri & Spotlight via App Intents (M) · **SHIPPED 2026-08-20 in v1.4.0 / companion-v0.2.0 — FR-198 + CFR-92…95** (the MCP-bridge half is deferred, see below)
**Value:** "ask my vault" without opening anything — AXON's answers where the
OS already listens.
**Shipped shape:** `docs/21-roadmap-macos27.md` **M4**. The premise was that
macOS 27 exposes MCP through App Intents; verifying it against the macOS 27
SDK showed **that is not public API** (no MCP symbol — the press coverage
described early internal testing), so M4 shipped the layer such a bridge
would attach to: four plain App Intents in the Companion with Siri phrases
(Search Vault, Ask Vault, Check Tasks, Capture Thought), pure-REST against
the daemon, plus the guarded `GET /api/search` seam (FR-198) built on
`/api/related`'s trust boundary. Ask spends tokens through the same
chokepoint guard the dashboard applies; nothing is indexed into Spotlight.
**Deferred (still a candidate):** the MCP bridge itself — exposing the
**agentic-read tool set** (`vault_search`, `vault_read`, `knowledge_search`,
`actions_list`, …) through App Intents, never the write tools — if and when
Apple makes MCP-in-App-Intents public. Until then, adding a verb means adding
an App Intent, which is why the intent set is deliberately small.
**Remaining:** Siri verb visibility and the spoken flows are human QA
(`apps/companion/QA.md`, 0.2.0 section).

## Theme B — Channel delivery & capture-back *(previously cut from 1.3)*

**Re-proposed because the economics changed:** the 1.3 cut bundled delivery
with a channel *platform* (bot frameworks, webhooks, chat state). The
re-proposal is the minimal shape the constitution allows: **outbound pushes of
things AXON already renders** (briefing, review-queue count, budget warnings)
and **inbound capture-only** (a message becomes a `00-Inbox` capture file — the
FR-26 funnel — never a command). ntfy-class topic push makes the outbound half
nearly free; the D1 capture endpoint (ADR-024) already proved the guarded
inbound pattern.

### B1 — Outbound notifications (S) · candidate — not scheduled
**Value:** the briefing and "Needs you" panel reach the owner instead of
waiting to be opened.
**Shape:** a `notify` provider seam (ntfy topic first; the OS-native path is
Companion user notifications, which needs no egress at all) fed by the existing
event bus; per-event-kind opt-in; egress to the push host allow-listed like any
other; redaction applies to payloads; work profile default-off.
**Open decisions:** ntfy vs Companion-local first; digest vs per-event
granularity.

### B2 — Capture-back channel (M) · candidate — not scheduled
**Value:** send AXON a link or thought from a phone; it lands in the inbox
funnel.
**Shape:** inbound is **capture-only, never command** — a polled allow-listed
source (an ntfy subscription, or a Telegram bot long-poll) whose messages
become `00-Inbox/capture-*.md` files exactly as the dashboard capture endpoint
writes them; the capture automation ingests URLs from there as it does today.
Content is data (NFR-05) — nothing in a message is ever executed.
**Open decisions:** transport (ntfy symmetry vs Telegram reach); sender
authentication; personal-only vs configurable.

## Theme C — User-defined automations

### C1 — Declarative automation recipes (L) · **SHIPPED 2026-08-21 in v1.5.0 — FR-199…FR-201, ADR-039** (spec: `docs/superpowers/specs/2026-08-20-automation-recipes-design.md`)
**Value:** today a new automation is a Go type, a registry entry, and three
count-assertion bumps. The 24 shipped automations decompose into a small
vocabulary — trigger (cron + change-gate), retrieval (search/list readers),
zero-or-one model call (tier + prompt + output contract), and a wikilink-safe
sink (managed block, review-queue proposal) — which a YAML recipe could
express without Go.
**Shape:** a `recipes:` config section validated like built-ins (schema +
`validateAgenticTools`-style allowlists); recipes run inside the existing
engine, so the chokepoint, change-gates, budgets, dry-run, catalog, and
dashboard reliability table apply automatically. Sinks are restricted to the
existing safe writers — a recipe can *propose* anything and *directly write*
only managed blocks.
**Decisions (2026-08-20, all recommended options picked):** vocabulary v1 =
three readers (`note`/`search`/`recent_notes`) + `prompt`-or-`render` + one
sink; not agentic (one-shot `runModel` only); recipes live in `config.yaml`
(outside every model write path — vault-portable sharing deferred), scheduled
by ordinary `automations.<name>` entries.

### C2 — Recipe vocabulary v2: let recipes read `.axon/`, then reach the derived tables (S) · candidate — not scheduled
*Reframed 2026-08-21 after a second experiment; the first draft led with a
`sources` reader, which the D3 result demoted.*

**Why now — two experiments, opposite results.** `docs/19` **E2** (source
freshness) does not fit as a recipe at all: its signal lives in the `sources`
table, which nothing reaches. `docs/19` **D3** (weekly review) mostly *does* —
a working recipe composed a real weekly review from the GTD board and
recently-touched notes. The difference is the finding: **automation output is
just a note, so recipes compose over other automations.** `actions-consolidate`
renders the whole action board into `01-Projects/Actions.md`, so a `note`
reader inherits overdue/next/someday without an actions reader existing.
`project-pulse` works the same way. Recipes are meaningfully more capable than
the three-reader vocabulary suggests, and the gaps that remain are narrow.

**Priority 1 — split the path validator so recipes may READ `.axon/` (XS).**
Today one shared `validRecipePath` governs both inputs and the block sink, so
the rule that stops recipes *writing* system files also stops them *reading*
one. That blocks `.axon/review-queue.md` — plain Markdown the dashboard
already serves — which is exactly what D3's "pending proposals" section needs.
Reading is not writing; the sink rule stays as-is. This is the cheapest, most
enabling change on this page, and it corrects an over-broad rule rather than
adding vocabulary.

**Priority 2 — a `sources` reader and an age selector (S).** Still needed for
E2, now second in line:
- **`sources {older_than_days, limit}`** → the `sources` table as
  `[[note]] — url (fetched DATE, kind, status)` lines.
- **`older_than_days`** (on `recent_notes`, or a sibling `stale_notes`) → the
  inverse of today's lookback, lifting the 90-day cap for it, since staleness
  is by definition about older material. `db.NotesUpdatedBefore` already
  exists — `actions-review` uses it — so this is nearly free.

**Priority 3 — link topology / orphans.** D3's only genuinely unreachable
component. Unlike actions and pulse, no automation renders orphans into a
note, so there is nothing to compose over. Worth doing only if `docs/19` E1
(orphan & decay report) does not land first — if it does, it will render a
note that recipes can read, and this priority disappears. Prefer that order.

**Deliberately rejected — a fan-out sink.** E2 wanted an advisory line on
*each* matched report note. That takes a recipe's blast radius from one named
note to N discovered ones, exactly the boundary ADR-039 drew ("anything
needing a new sink is a Go automation"). Per-note annotation stays Go; an
aggregate digest is the useful 80%.

**Nested markers are now the common case, not an edge case.** Composing over
automation output embeds *that* note's `axon:` markers in the recipe's own
block. The v1.5.0 marker-neutralization fix already handles this (verified
live: a review recipe reading `Actions.md` produced an intact block with the
nested markers made inert). Any new reader must keep neutralizing, and the
D3-shaped recipe belongs in the test suite as the regression that proves it.

**Open decisions:** whether `.axon/` reads are allowed wholesale or only for
an allow-listed set (review queue, archive); whether `sources` filters by
status or renders it; one reader with `older_than_days` vs a separate
`stale_notes`; whether the 90-day cap lifts generally or only for the
age-selecting path.

## Theme D — Local model fleet

### D1 — Multi-provider eval-gated routing (M) · candidate — not scheduled
**Value:** the local tier currently means Ollama. macOS 27 adds `apple-fm`
(on-device + Private Cloud Compute — `docs/21` M2); MLX-served models are a
plausible third. The eval harness + admission gate (1.2 R5) was built for
exactly this: **a provider earns a tier by passing evals, not by being new.**
**Shape:** provider prefixes on tier model strings (`ollama:` today,
`apple-fm:` per docs/21, later others) behind the ADR-015 seam;
`axon eval --model` already measures any candidate; eval-drift re-checks on
provider updates (once it is schedulable — `docs/ISSUES.md` #1).
**Open decisions:** per-operation routing (classify→on-device, routine→Ollama)
vs per-tier; whether the cascade-with-verification (ADR-031) judge may be a
different provider than the answerer.

## Theme E — Continuous capture *(previously cut from 1.3)*

**Re-proposed because the economics changed:** the cut version implied a
platform of importers. The re-proposal is one primitive: **watch-folders**. The
capture funnel (FR-26) already turns files-in-a-folder into captures — this
merely widens *which* folders, reusing every downstream guarantee.

### E1 — Watch-folders (S) · candidate — not scheduled
**Value:** drop a PDF in `~/Downloads/axon`, screenshot to a watched folder,
export from any app — it flows into the inbox without opening Obsidian.
**Shape:** config-listed external folders polled by the capture automation
(same cadence, same archive-originals behaviour, same `AllowLocalFiles`
kind-classification); off by default; each folder explicitly listed (no home-dir
scanning, ever).
**Open decisions:** copy vs move semantics from watched folders; per-folder
kind hints (e.g. a screenshots folder defaulting to image ingestion).

### E2 — macOS share extension (S, Companion) · candidate — not scheduled
**Value:** the system Share sheet becomes a capture path from every app.
**Shape:** a Companion share extension writing through the daemon's existing
guarded capture endpoint (ADR-024) — zero new daemon surface.
**Open decisions:** none daemon-side; Companion packaging/entitlements work.

## Theme F — Interop

### F1 — Third-party MCP clients (S) · candidate — not scheduled
**Value:** AXON's MCP server currently wires into Claude Code and Claude
Desktop (Component 13). Any MCP-speaking client (editors, other assistants)
could read the vault through the same tools and the same trust boundary.
**Shape:** documentation + `axon mcp install --client` growing beyond the two
Claude clients; the read/write tool split and the `--tools` server-side filter
already express the containment. The chokepoint caveat is the design question:
`vault_ask` spends tokens and today assumes a Claude-shaped caller.
**Open decisions:** which client second; whether non-Claude clients get
read-only registration by default (recommendation: yes).

### F2 — Obsidian Bases & CLI *(previously cut from 1.3)* (S) · candidate — not scheduled
**Re-proposed because the economics changed:** Bases has moved from beta
toward a stable format with a published spec — reading `.base` files is now a
parsing task, not a moving target.
**Value:** AXON's derived views (actions, entities, health) rendered as native
Obsidian Bases; Bases-defined queries visible to AXON's retrieval.
**Shape:** read-only first (index `.base` definitions like any note); a written
`.base` view is just a managed file (Create/Patch, wikilink-safe).
**Open decisions:** read-only vs also emitting Bases views; whether emitting
replaces the Dataview dashboards in the scaffold.

## Theme G — Self-maintenance

### G1 — The daemon proposes its own fixes (M) · candidate — not scheduled
**Value:** doctor already knows what's wrong *and* the fix (FR-185
remediations); the "Needs you" panel shows it. The last step is AXON filing the
work where the owner already reviews work: the review queue. A failing
automation, a stale service unit, a drifted config, an available update — each
becomes a proposal with the exact remediation attached.
**Shape:** a zero-model sweep translating doctor warnings + run-failure streaks
into review-queue items (ADR-020); **accept never self-applies system changes**
in v1 — it surfaces the copyable command and records the acknowledgement
(config-file edits via the comment-preserving `config set` writer are the one
candidate for a later, separately-designed auto-apply).
**Open decisions:** which check classes are proposal-worthy; dedup/backoff so a
persistent warning doesn't nag weekly; whether `axon update` availability
belongs here or stays dashboard-only.

## Sequencing sketch *(not a commitment)*

*Updated 2026-08-21, after A1 (v1.4.0) and C1 (v1.5.0) shipped.*

**Shipped:** **A1** (as plain App Intents — the MCP half deferred until Apple
makes it public) and **C1 recipes**, which was the largest and most
platform-defining slice and did get its own release.

**Recipes were dogfooded first, and the answer was useful.** `docs/19` E2
(source freshness) was tried as a recipe on 2026-08-21 and does not fit: the
staleness signal lives in the `sources` table, which no reader reaches, and
the one reader with dates points at recently-*updated* notes with a 90-day
cap. E2 stays Go; the reader gaps became **C2** above. The remaining
candidates worth trying the same way are `docs/19`'s D3 (weekly review flow)
and F2 (MOC materialisation) — both read notes rather than sources, so both
may already be expressible today.

**Then:** **G1** is the strongest remaining pick — cheap, it compounds trust,
and recipes just widened what doctor can warn about (name collisions,
unscheduled recipes), which is exactly the raw material G1 turns into review
items. **E1 watch-folders** and **B1 notifications** remain the small
high-leverage starts of their themes; **D1** is the local-fleet play. As with
docs/19, these compete for the same build slots; the two roadmaps are
deliberately separate lenses, not separate teams.

## Explicit non-goals

Hosted/multi-user anything; a plugin system executing third-party *code*
(recipes are declarative data, not programs); agent-initiated egress; inbound
channels that execute commands (capture-only, always); self-applying system
mutations without their own destructive-op design pass; any path to Claude that
skips the chokepoint, including via new providers or clients.
