# 21 — Plan: macOS 27 "Golden Gate" *(candidates — nothing scheduled)*

macOS 27 ships ~September 2026 and is the first OS release whose AI surface
maps directly onto seams AXON already has. This plan sequences how AXON makes
the most of it. Slices follow the standing cycle (brainstorm → spec → ADR → FR
rows → TDD → live smoke); **no provisional FR/ADR numbers here** — they are
assigned at graduation. Nothing below is scheduled until it is.

## What macOS 27 actually provides *(confirmed at WWDC26)*

- **Apple-silicon only.** Intel support and Rosetta 2 end with this release.
- **The `fm` CLI** — Apple Foundation Models from the terminal: `respond`,
  `chat`, `token-count`, `schema`, `serve`, `available`, `quota-usage`;
  `--model pcc` routes to Private Cloud Compute; `--stream` streams. Most
  importantly, **`fm serve` exposes an OpenAI-compatible local endpoint**
  backed by Apple's models — existing OpenAI-style clients can target
  on-device inference by base URL alone.
- **The on-device model, year two**: rebuilt, better tool calling, **multimodal
  (image input)**, ~**4,096-token context** (query `SystemLanguageModel.contextSize`
  at runtime).
- **Private Cloud Compute model**: ~32K context, reasoning, **no API keys or
  auth** — but quota-limited (`fm quota-usage` reports consumption).
- **Provider abstraction** (`LanguageModel` protocol): Apple on-device, PCC,
  Claude via Anthropic's `ClaudeForFoundationModels` package, Gemini via
  Firebase, local open-weights via `coreai-models` / `mlx-swift-lm`.
- **Per-response usage APIs**: input/cached/output/reasoning token counts.
- **MCP support built into App Intents** — the layer behind Siri, Spotlight,
  Shortcuts.

Caveats that shape everything below: the `fm` CLI is new and thinly documented
(its `--help` output is currently the primary reference); PCC quota mechanics
are not yet public; and 4K on-device context confines that tier to
classify-sized operations.

## The stance

Apple's models slot into AXON as **another local/provider tier behind existing
seams** — never as a new path around the guarantees. Concretely:

- **Cardinal rule 1 is untouched.** Generative calls to Apple models route
  through the Component 07 chokepoint exactly like `ollama:` calls do under
  ADR-015: pre-flight estimate, ledgered, budget-exempt as *local* — except PCC,
  which is quota-limited and therefore budget-*tracked* (see M2).
- **Perception primitives stay primitives.** Vision/OCR remain budget-exempt,
  non-chokepoint local calls (the ADR-027/ADR-031 distinction: a call that only
  perceives or reorders is not a generative-content call; a call that gates
  spend is ledgered).
- **A provider earns a tier through the eval harness** (1.2 R5), not through
  novelty.

## Slices *(suggested order)*

### M1 — Detection & doctor (S) · **Status: Shipped to `main` 2026-08-20 (FR-191/FR-192)**

> **Built as specced** (`docs/superpowers/specs/2026-08-20-macos27-m1-detection-design.md`),
> with one reconciliation the roadmap draft missed: the codebase **already
> ships an `apple` on-device generation provider** (`agent.AppleFM`, Swift
> helper on the FoundationModels framework, chokepoint-routed, classify-only).
> M1 therefore added the missing piece — `fm` CLI awareness: `core.DetectFM`
> six-state matrix + advisory `apple-fm` doctor check (license-pending is the
> one WARN, `fix: sudo fm license`) — plus FR-192: `apple:<x>`/`apple-fm:<x>`
> tier strings are now **rejected at validation** instead of silently parsing
> as Claude model strings and misrouting to `claude -p`. Field finding baked
> into the probe: `fm`'s pre-license exit codes are inconsistent (`available`
> → 0, `--help` → 1), so state detection parses ANSI-stripped output with
> license markers checked before the exec error.
**Build:** advisory `doctor` checks: OS ≥ 27 and Apple silicon; `fm` binary
present and answering (`fm available`); which models are usable
(on-device / PCC). Config surface reserved but inert on older systems —
everything degrades to today's behaviour on macOS 26 and non-Mac platforms.
**Prerequisites:** none. **Acceptance shape:** doctor reports the full matrix
correctly on 26 (absent), 27-without-models, and 27-ready machines; zero
behaviour change anywhere else.
**Risks:** `fm` output formats may shift across betas — parse defensively,
version-gate assertions.

### M2 — the fm tier (M) · **Status: Shipped to `main` 2026-08-20 (FR-193…195, ADR-038)**

> **Built as specced** (`docs/superpowers/specs/2026-08-20-macos27-m2-fmserve-design.md`).
> Both post-M1 open decisions resolved: transport = a daemon-supervised
> `fm serve --socket` child (unix socket, `/health`-gated, restart-on-death,
> stopped in `deps.close()`); naming = the `apple:` family extends —
> `apple:system` (on-device via fm, classify-only, **measured token usage in
> the ledger**) and `apple:pcc` (PCC rung, classify+routine, 28K pre-flight
> cap, `models.pcc_enabled` opt-in, ledgered under its own ref, quota
> advisory via `fm quota-usage`); `apple-fm:*` stays permanently rejected and
> bare `apple` (Swift helper) is untouched. eval-drift keys fm tiers on the
> macOS version. Everything through the chokepoint; PCC context-gating (the
> live "use the Terminal app" finding) degrades through the FR-79 ladder.
> The section below is the original candidate text.
**Build:** a new provider prefix `apple-fm:` for tier model strings behind the
ADR-015 seam, implemented against **`fm serve`'s OpenAI-compatible endpoint**
(one HTTP client, streaming, usage fields — no Swift required in the daemon).
On-device targets **classify-tier** work (4K context: triage labels, entity
extraction, session distillation); **PCC is a separate, opt-in rung** suitable
for routine-tier ops (32K, reasoning). Admission is **eval-gated like any
local model**: `axon eval --model apple-fm:<...>` must pass the family floor
before a tier accepts it, and eval-drift re-checks after OS updates.
**PCC and the ledger:** PCC is free-of-keys but not free-of-limits. Its usage
is recorded to the token ledger as its own model string and surfaced in the
dashboard split; `fm quota-usage` feeds an advisory doctor check and a
budget-guard input, so quota exhaustion degrades gracefully to
Ollama/Claude per the existing fallback ladder. All of it **through the
chokepoint** — cardinal rule 1, restated deliberately.
**Prerequisites:** M1. **Acceptance shape:** a classify automation runs
end-to-end on-device with ledger entries; eval gate demonstrably refuses an
under-floor model; PCC opt-in off by default; quota-exhausted PCC degrades
without failing the run; non-macOS builds unaffected.
**Risks:** `fm serve` lifecycle (who starts/supervises it — probably AXON
launches it on demand like the Ollama reachability pattern, TBD at design);
OpenAI-compat completeness unknown; PCC quotas unpublished — treat as
best-effort rung, never a dependency.
**Open decisions:** `fm serve` vs shelling `fm respond` per call; **transport
& naming** (post-M1 reconciliation): the shipped `apple` tier already reaches
the on-device model through its own Swift helper — M2 must decide whether the
`fm`-backed path *replaces* that helper or sits beside it, and whether the
reserved ref form becomes `apple:<variant>` or `apple-fm:<...>` (both rejected
since FR-192, so nothing misroutes meanwhile); whether PCC
is reachable from the work profile at all (recommendation: no — its egress
posture needs its own review; PCC is Apple-operated compute, which
`data_residency: local-only` arguably excludes).

### M3 — Apple vision tier (S) · **Status: Shipped to `main` 2026-08-20 (FR-196/197, ADR-038 amended)**

> **Built as specced** (`docs/superpowers/specs/2026-08-20-macos27-m3-apple-vision-design.md`).
> ADR-035's `apple` slot is filled by a per-call `fm respond --image`
> subprocess (on-device). Owner's decision widened the original plan: a
> distinct `ingestion.vision: apple:pcc` mode adds PCC vision under the
> `models.pcc_enabled` opt-in — explicit, validation-gated, never a silent
> fallback, and disclosed as sending unredacted image bytes off-device
> (redaction applies to text, not pixels). Doctor's `vision` check reports
> the fm states. Vision-error semantics remain the FR-172 contract (OCR text
> stands; no OCR text → loud CLI error), verified live against real PCC
> context-gating. The section below is the original candidate text.
**Build:** fill the seam H1 left ready. `internal/ingestion/vision.go` already
reserves the slot — `ingestion.vision: "apple"` currently returns
*"requires macOS 27 on-device image input (not yet available) — use
ollama:<model> or off"*. Implement it via the multimodal on-device model
(attachment input), behind the same `Vision` interface; OCR-first-then-vision
ordering, budget-exemption, CLI-only image ingestion, and
archive-never-delete all unchanged (FR-171/172 semantics).
**Prerequisites:** M1 (detection); independent of M2.
**Acceptance shape:** a screenshot ingests with an Apple-produced description
on a 27 machine; the same config on macOS 26 keeps today's actionable error;
`ollama:` and `off` untouched.
**Risks:** image-attachment access from the CLI/`fm serve` path may lag the
Swift API — if so, this routes through a small helper in the existing
ADR-013 compiled-Swift-helper pattern (the OCR helper's sibling).

### M4 — App Intents MCP: Siri & Spotlight (M) · **Status: In progress 2026-08-20, reframed (FR-198 + CFR-92…95)**

> **Reframed at design**: Apple's MCP-in-App-Intents is **not public API**
> (verified against the macOS 27 SDK docs on a real 27.0 machine — the App
> Intents surface has no MCP symbol; the press coverage was early internal
> testing). M4 therefore ships **plain App Intents** in the Companion —
> Search / Ask / Tasks / Capture as Siri & Shortcuts verbs, pure-REST against
> the daemon (new guarded `GET /api/search` seam, FR-198; the other three
> ride existing guarded endpoints) — which is exactly the layer Apple says
> the MCP bridge will attach to. **The MCP hookup itself is a recorded
> deferral** until the API is public. The will-not-do stands: on-demand
> answers only, no vault content indexed into Spotlight. Spec:
> `docs/superpowers/specs/2026-08-20-macos27-m4-app-intents-design.md`.
> The section below is the original candidate text.
**Build:** the Companion hosts App Intents that bridge to the daemon's MCP
server, so Siri and Spotlight can answer from the vault. Exposure is the
**agentic-read tool set only** (`vault_search`, `vault_read`, `vault_links`,
`knowledge_search`, `tokens_status`, `vault_related`, `actions_list`) — never
the write tools, never `action_complete`, and `vault_ask` only if the
spend-is-state guard (ADR-023) can be honoured in the intent flow, otherwise
not at all. The daemon stays the single source of truth; the Companion keeps
its zero-business-logic stance (`docs/18`) — intents are thin adapters.
**Prerequisites:** none of M1–M3 strictly; Companion on macOS 27 (M5) in
practice. Apple's App-Intents-MCP API surface must be stable enough to code
against.
**Acceptance shape:** "search my vault for X" via Siri/Spotlight returns
vault hits with zero writes possible by construction (the `--tools` filter
pattern applied to the bridge); everything works with the Companion absent
(feature is additive).
**Risks:** the App Intents MCP surface is the least-documented of the WWDC26
announcements and was in early testing as of late 2025 — this slice waits for
a stable API, and its design pass starts by reading what actually shipped.
**Open decisions:** which tools make a *good* Siri surface (probably
`vault_search` + `actions_list` first); result rendering (snippet vs open-in-
Obsidian).

### M5 — Companion on macOS 27 (S) · candidate — not scheduled
**Build:** retire the QA debt on the new OS (`apps/companion/QA.md`,
`docs/ISSUES.md` #3/#4): human-verify the popover/glass/accessibility items on
release macOS 27; exercise a real Sparkle update end-to-end
(0.1.0 → 0.1.1); then **decide the deployment floor** — stay macOS 26
(and finally verify there) or require 27 (honest, given 26 was never
verified, and unlocks M3/M4/M6 API use in-app).
**Prerequisites:** release macOS 27. **Acceptance shape:** QA.md items closed
or floor raised with the rationale recorded; one successful in-the-wild
auto-update.
**Risks:** none technical; this is discipline work.

### M6 — On-device speech-to-text (M, stretch) · candidate — not scheduled
**Build:** if macOS 27's on-device STT is usable from a helper (Swift
`SpeechAnalyzer`-class API — verify what shipped), it becomes the `apple` tier
of the STT provider seam that `docs/19` Theme B (audio ingestion, the
re-proposed meeting/voice slice) defines — the same
seam-with-detected-fallback pattern as OCR (ADR-026: Apple helper or
tesseract; here: Apple helper or whisper.cpp).
**Prerequisites:** docs/19 B1 graduating (it owns the pipeline design; this
slice only supplies a provider). **Acceptance shape:** per B1's gate, with
transcription on-device.
**Risks:** API availability/quality unverified — this is why it's the stretch.

## What we will NOT do

- **No second door to Claude.** `ClaudeForFoundationModels` (Claude inside
  Apple's framework) is not a path AXON's daemon takes — Claude access remains
  the subscription-authenticated `claude -p` adapter through the chokepoint.
  A framework-mediated Claude call would bypass the auth-mode discipline and
  the adapter seam for zero gain.
- **No vault bulk-export into OS indexes.** Spotlight/Siri get what MCP
  answers *on demand*; AXON never feeds vault content into the OS semantic
  index wholesale. The vault's egress posture doesn't change because the
  caller is the OS.
- **No cardinal-rule exceptions.** Apple generative calls are
  chokepoint-routed and ledgered (M2); vision/OCR/STT stay budget-exempt local
  perception primitives (ADR-027/031 distinction); every writer stays
  wikilink-safe.
- **No Intel/macOS-26 regression.** Every slice is additive and
  detection-gated; a macOS 26 or Linux machine keeps exactly today's
  behaviour.

## Sequencing note

M1 → M2 is the spine (detection, then the tier). M3 is small and independent
after M1. M5 is calendar-bound (do it the week 27 ships). M4 and M6 wait on
Apple's API surface stabilising and on their design passes. Expect the first
real cycle (M1+M2) to also produce the ADR that records Apple-provider routing
as an ADR-015 amendment — one ADR, minted at build, not before.
