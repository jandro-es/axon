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

### B1 — Outbound notifications (S) · **SHIPPED 2026-08-21 in v1.7.0 — FR-210/FR-211, ADR-041** (spec: `docs/superpowers/specs/2026-08-21-notifications-design.md`)
**Value:** the briefing and "Needs you" panel reach the owner instead of
waiting to be opened.
**Shape:** a `notify` provider seam (ntfy topic first; the OS-native path is
Companion user notifications, which needs no egress at all) fed by the existing
event bus; per-event-kind opt-in; egress to the push host allow-listed like any
other; redaction applies to payloads; work profile default-off.
**Open decisions:** ntfy vs Companion-local first; digest vs per-event
granularity.

**Open decisions resolved.** **ntfy first**, behind a `Notifier` seam — it is
daemon-side, cross-platform, works on a headless install, and delivers the
actual value (reaching you when you are away from the machine). The
Companion-local path stays available behind the same seam and needs no daemon
change. **Per-event, opt-in by kind**, which subsumes the digest for free: the
daily briefing is itself an event, so "push me the briefing" is one list entry.

**Two egress findings this entry did not anticipate.** The default
`egress_allowlist` is `["localhost", "*"]` — a wildcard — so it could not be
*the* guard; the configured URL is the allow-list, with the egress list
applying additionally so a work profile's strict list still bites. And
`BlockedIPReason` refuses loopback and private addresses, which is exactly
where a self-hosted ntfy lives — it is deliberately **not** applied here,
because a notify URL comes from config, outside every model write path.

**And one design decision reversed during implementation.** The spec had
config refuse any unrecognised event kind. Reading the emitters showed kinds
are not statically enumerable — literals at some, parameters at others, and
built at runtime from user input (`"review." + action`) — so a stale list
would refuse *valid* config. It became a doctor warning instead: nothing valid
is blocked, and the typo is still surfaced where the owner looks.

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

### C2 — Recipe vocabulary v2: let recipes read `.axon/`, then reach the derived tables (S) · **COMPLETE 2026-08-21 in v1.6.0** — P1+P2 shipped (FR-202, FR-203, ADR-039 amended; spec: `docs/superpowers/specs/2026-08-21-recipe-vocabulary-v2-design.md`), P3 closed by composition via `docs/19` E1
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

**Priority 1 — split the path validator so recipes may READ `.axon/` (XS). SHIPPED (FR-202).**
Today one shared `validRecipePath` governs both inputs and the block sink, so
the rule that stops recipes *writing* system files also stops them *reading*
one. That blocks `.axon/review-queue.md` — plain Markdown the dashboard
already serves — which is exactly what D3's "pending proposals" section needs.
Reading is not writing; the sink rule stays as-is. This is the cheapest, most
enabling change on this page, and it corrects an over-broad rule rather than
adding vocabulary.

**Priority 2 — a `sources` reader and an age selector (S). SHIPPED (FR-203)**
— as `sources {older_than_days, limit}` and a sibling `stale_notes` reader
(not an inverted `recent_notes`, so that reader keeps its honest 0–90 cap):
- **`sources {older_than_days, limit}`** → the `sources` table as
  `[[note]] — url (fetched DATE, kind, status)` lines.
- **`older_than_days`** (on `recent_notes`, or a sibling `stale_notes`) → the
  inverse of today's lookback, lifting the 90-day cap for it, since staleness
  is by definition about older material. `db.NotesUpdatedBefore` already
  exists — `actions-review` uses it — so this is nearly free.

**Priority 3 — link topology / orphans. CLOSED 2026-08-21 by composition, not
built.** This priority existed only because nothing rendered orphans into a
note. `docs/19` E1 now does: `orphan-report` maintains an `axon:orphans` block
in `03-Resources/Vault Health.md`, so a recipe reaches orphans today with an
ordinary `note` reader and no new vocabulary. That was this section's own
stated preference ("Prefer that order"). D3's only genuinely unreachable
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

**Open decisions — all resolved 2026-08-21 when P1+P2 shipped:** `.axon/`
reads are **allow-listed**, not wholesale (exactly `review-queue.md` and
`review-queue-archive.md`; widening later is one line, narrowing later breaks
configs). `sources` **renders** status and does not filter on it — a failed or
redacted source is what a freshness recipe most wants to surface. Staleness is
a **sibling `stale_notes` reader**, not a mode of `recent_notes`. The 90-day
cap lifts **only** for the age-selecting path (`stale_notes` and `sources` get
0–3650; `recent_notes` keeps 0–90). One decision emerged during the design
that this section had not anticipated: a `review {}`-sink recipe may not read
`.axon/review-queue.md`, because its own output would be its next input and
the change-gate could never skip.

## Theme D — Local model fleet

### D1 — Multi-provider eval-gated routing (M) · **CLOSED 2026-08-21 — substantially already delivered; the remaining gap fixed**
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

**Most of this was already built, by FR-142/143 and ADR-038.** Reading the
code before designing showed the eval-gated admission in `tokens/manager.go`
is **provider-agnostic**: it gates on `ref.Provider != ProviderClaude`, so any
non-Claude provider already has to earn its tier by passing evals.
`config.ParseModelRef` already resolves `ollama:`, `apple:` and `apple-fm:`;
`axon eval --model` already measures any candidate ref; and per-tier
multi-provider routing already works — `classify: apple-fm:…` alongside
`routine: ollama:…` is valid config today. "A provider earns a tier by passing
evals, not by being new" is the shipped behaviour.

**The one real gap, now closed.** Drift detection was Ollama-only: the doctor
vetting check computed a current fingerprint solely for `ollama:` refs, so an
fm-backed tier that passed evals stayed "vetted" forever — an OS update could
swap the on-device model underneath it and the gate would keep admitting a
model nobody had evaluated. The `eval-drift` automation already had this right
(FR-194: the OS version is the drift key for fm-backed tiers); its logic is now
`core.TierDriftKey`, shared by all three sites that compute or compare the key
— the automation, the doctor check, and `axon eval` when it records a run.
They have to agree, or every tier reports drift forever and none ever settles.

**The two open decisions remain open, and are separate features**, not part of
this entry: per-**operation** routing (an automation naming a provider rather
than a tier) widens the chokepoint's model-resolution surface and would need
its own ADR; and whether the ADR-031 cascade judge may be a different provider
than the answerer is a question about verification independence, not routing.
Either could be picked up on its own.

## Theme E — Continuous capture *(previously cut from 1.3)*

**Re-proposed because the economics changed:** the cut version implied a
platform of importers. The re-proposal is one primitive: **watch-folders**. The
capture funnel (FR-26) already turns files-in-a-folder into captures — this
merely widens *which* folders, reusing every downstream guarantee.

### E1 — Watch-folders (S) · **SHIPPED 2026-08-21 in v1.7.0 — FR-208/FR-209, ADR-040** (spec: `docs/superpowers/specs/2026-08-21-watch-folders-design.md`)
**Value:** drop a PDF in `~/Downloads/axon`, screenshot to a watched folder,
export from any app — it flows into the inbox without opening Obsidian.
**Shape:** config-listed external folders polled by the capture automation
(same cadence, same archive-originals behaviour, same `AllowLocalFiles`
kind-classification); off by default; each folder explicitly listed (no home-dir
scanning, ever).
**Open decisions:** copy vs move semantics from watched folders; per-folder
kind hints (e.g. a screenshots folder defaulting to image ingestion).

**Open decisions resolved.** **Move, not copy** — it matches the drop-box
model this entry describes, self-dedups (a moved file cannot be reprocessed,
so there is no seen-ledger to keep), and destroys nothing: the file lands in
`00-Inbox` and then the vault archive. Copy would have needed persistent
per-folder state and left the watched folder growing forever. **No per-folder
kind hints** — extension classification is shipped and tested, and a hint
would be a second source of truth that can disagree with the first.

**Two refusals this entry did not anticipate, both found while designing.**
**Symlinks are skipped:** `os.ReadDir` reports one as not-a-directory, so it
would have passed capture's existing filter, been moved in, and then `Ingest`
would have *followed* it — a link to `~/.ssh/id_rsa` becoming a vault note and
model context. The inbox never carried that exposure because it holds what the
owner put there. **Files still being written are skipped** for 30 seconds:
capture has no settle check because dragging a file into `00-Inbox` is atomic
from the owner's side, but a browser writing into a watched folder is not.

**And one trap that would have shipped a feature that does nothing:**
`DetectChange` runs before `Run`, so the capture cursor had to cover the
watched folders as well as the inbox — otherwise a new file outside the vault
leaves the inbox unchanged, the tick is skipped, and the sweep never runs.

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

### G1 — The daemon proposes its own fixes (M) · **SHIPPED 2026-08-21 in v1.6.0 — FR-206/FR-207** (spec: `docs/superpowers/specs/2026-08-21-self-maintenance-design.md`)
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

**Two findings from the code, neither visible from this entry.** (1) Nothing in
the daemon had ever run doctor — `core.Doctor` had exactly one caller, the CLI —
so the seam was the work, not the proposal logic. (2) The *full* report was
assembled in `cmd/axon/doctor_cmd.go`, which appended `update-available` and
`recipes` after `core.Doctor` returned; an in-daemon caller would have proposed
from a smaller report than the owner had ever seen. Both are fixed by FR-206's
shared `selfCheckExtras` helper, pinned by a divergence regression test.

**Open decisions resolved.** Proposal-worthy = any check carrying a non-empty
`Fix`, which needs no allow-list to maintain — the `Check` type already
separates "what is wrong" from "what to do". Dedup is proposal memory keyed on
name + remediation, so a standing warning proposes once and a *changed*
remediation re-proposes; there is no weekly re-nag. `update-available` belongs
here rather than staying dashboard-only, since it carries a `Fix` like any
other check. Run-failure streaks were **cut** from v1 and remain a candidate:
a failed automation carries no remediation, so the proposal would be a
notification wearing a fix's shape.

## Sequencing sketch *(not a commitment)*

*Updated 2026-08-21, after A1 (v1.4.0), C1 (v1.5.0) and C2 P1+P2 shipped.*

**Shipped:** **A1** (as plain App Intents — the MCP half deferred until Apple
makes it public) and **C1 recipes**, which was the largest and most
platform-defining slice and did get its own release.

**Recipes were dogfooded first, and the answer was useful.** `docs/19` E2
(source freshness) was tried as a recipe on 2026-08-21 and did not fit: the
staleness signal lives in the `sources` table, which no reader reached, and
the one reader with dates pointed at recently-*updated* notes with a 90-day
cap. `docs/19` D3 (weekly review) mostly did fit. Those two results became
**C2**, whose Priorities 1–2 then shipped the same day (FR-202/FR-203) — so
E2's aggregate-digest half is now expressible as a recipe, though its
per-note advisory half still needs a fan-out sink and stays Go.

**The dogfooding phase is over.** `docs/19` F2 (MOC materialisation) was
listed here as the last recipe candidate, which was wrong: F2 needs a new
review-queue *kind* plus a note `Create`, so by ADR-039's own boundary
("anything needing a new sink is a Go automation") it is a Go slice, not a
recipe experiment.

**C2 is now complete.** P1+P2 shipped as recipe vocabulary v2, and P3 closed
without being built — `docs/19` E1's `orphan-report` renders orphans into a
note, which is the surface a recipe reads.

**G1 has now shipped** — and recipes did feed it exactly as predicted: the
name-collision and unscheduled-recipe warnings both carry a `Fix`, so they
became the first things `self-check` files. **E1 watch-folders and B1 notifications have both now shipped.** **D1** turned out to be substantially already delivered and is now closed;
what remains in this theme are its two open decisions, each a separate
feature. As with
docs/19, these compete for the same build slots; the two roadmaps are
deliberately separate lenses, not separate teams.

## Explicit non-goals

Hosted/multi-user anything; a plugin system executing third-party *code*
(recipes are declarative data, not programs); agent-initiated egress; inbound
channels that execute commands (capture-only, always); self-applying system
mutations without their own destructive-op design pass; any path to Claude that
skips the chokepoint, including via new providers or clients.
