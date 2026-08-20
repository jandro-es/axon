# macOS 27 M4 — Siri & Shortcuts via Companion App Intents (design)

**Status:** approved 2026-08-20 (all three decisions Jandro-picked-recommended:
plain App Intents now, MCP bridge deferred; Search+Ask+Actions+Capture verbs;
new `GET /api/search` daemon seam, intents pure-REST). **FR-198** (daemon) +
**CFR-92…CFR-95** (Companion). No new ADR — ADR-020's trust boundary extends
to one more read endpoint; the Companion side follows its PRD architecture.

## The reframing (recorded, load-bearing)

Apple's "MCP support in App Intents" is **not public API**: the macOS 27 SDK
documentation on a real 27.0 machine shows the full App Intents surface
(intents, entities, AssistantIntent schemas, Spotlight indexing) and no MCP
symbol. M4 therefore ships plain App Intents — the layer Apple says the MCP
bridge will attach to — and docs/21 records the MCP hookup as a deferral.
The will-not-do stands: vault content is answered **on demand**; no entity is
indexed into Spotlight.

## Daemon half — FR-198: `GET /api/search`

Clone of `/api/related`'s boundary: loopback bind + Host guard +
`X-Axon-Search: 1` header (CORS-preflight forcing), gated by
`dashboard.search_enabled` (*bool, pointer-default-ON, `SearchAllowed()`);
404 disabled / 403 header / 400 empty `q` / 200. Response
`{"hits": [{path, snippet, score}]}` from `Searcher.Search` (top_k clamped
via the existing `queryInt`, default 8). Zero generative spend — the query
embedding is the usual budget-exempt local call; no ledger row, no SSE event
(the related precedent). Appended to `apps/companion/CONTRACT.md`.

## Companion half — CFR-92…95

Four `AppIntent`s in the Companion target + an `AppShortcutsProvider`
(phrases: "Search ‹Axon›…", "Ask ‹Axon›…", "‹Axon› tasks", "Capture in
‹Axon›…"), pure-REST via AxonKit's client (no CLI from an intent process —
the 1.3.4 LaunchServices PATH lesson):

- **CFR-92 SearchVaultIntent** — `q` parameter → GET /api/search → dialog +
  snippet list of top hits (path + snippet). Zero-spend.
- **CFR-93 AskVaultIntent** — question → POST /api/ask (existing guarded,
  chokepoint-covered, human-initiated). `ask_enabled` off (404) → the dialog
  says asking is switched off, never an error tone. Cited answer read back.
- **CFR-94 CheckTasksIntent** — GET /api/actions → counts + top open items
  ("3 overdue, 5 due today"), deep link opens the dashboard Actions tab.
- **CFR-95 CaptureThoughtIntent** — text → POST /api/capture (the additive
  non-destructive inbox funnel; the one write, already guarded). Confirms
  with the created capture's destination.

A down daemon answers every intent with "Axon isn't running — open Axon
Companion", not a raw error. Plain App Intents API only (macOS 13-era
symbols) — the macOS 26 floor is untouched.

## The make-or-break: App Intents metadata under SwiftPM

Siri/Shortcuts discover intents via a `Metadata.appintents` bundle emitted by
Xcode's `appintentsmetadataprocessor`. The Companion is SwiftPM-without-
Xcode with a custom bundling script, so packaging must invoke the processor
explicitly. This is spiked FIRST; if the processor cannot run outside an
Xcode build, the slice fails loudly and is reassessed — intents that Siri
cannot see do not ship as if they worked.

## Versioning & release

Companion `MARKETING_VERSION` 0.1.0 → 0.2.0, `BUILD_NUMBER` 2; CFR-92…95
added to the Companion PRD; CONTRACT fixtures extended. The notarised
release + Sparkle appcast run through `make companion-release` at the end
and require the owner's signing credentials — absent those, the branch stops
at built/tested and release is a follow-up.

## Verification

Go: table-driven handler tests (404/403/400/200 + payload shape), config
gate test. Swift: intent logic against the stubbed client in the existing
suite; `make companion && make companion-test`. Live: curl all four endpoint
states; build the app; attempt real discovery via the `shortcuts` CLI.
Whatever Siri-side verification cannot be done headlessly is recorded in
`apps/companion/QA.md` beside the existing human-verification items (M5's
sweep).
