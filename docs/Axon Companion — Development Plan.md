---
title: Axon Companion — Development Plan
type: project
status: draft
created: 2026-08-16
updated: 2026-08-16
tags: [axon, macos, menu-bar, swiftui, swift-charts, plan]
---

# Axon Companion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Also load the `macos-spm-app-packaging` skill (Task 2, 15–16 use its templates verbatim), `swiftui-expert-skill` + `ecc:liquid-glass-design` (UI tasks 7–14), and `swift-concurrency` (all AxonKit tasks).

**Goal:** A notarised macOS 26 menu bar app ("Axon Companion") that surfaces daemon status, controls the AXON service, opens its surfaces, renders Swift Charts insights, guides first-run installation, and ships a doctor UI — as a strictly optional shell over the existing `axon` CLI + dashboard HTTP API.

**Architecture:** Two SwiftPM targets in the axon monorepo under `apps/companion/`: **AxonKit** (library — clients, CLI wrapper, state machines; 100% unit-tested, zero UI) and **Companion** (executable — SwiftUI `MenuBarExtra` + windows; views read `@Observable` models from AxonKit and render). All reads via REST/SSE on `127.0.0.1:7777`; all mutations via `axon … --json` subprocesses. No business logic in the app; missing daemon seams become upstream Go tasks, never workarounds.

**Tech Stack:** Swift 6.2 (strict concurrency), SwiftUI `MenuBarExtra`, Swift Charts, Swift Testing (`import Testing`), SwiftPM (no `.xcodeproj`), Sparkle 2 (packaging phase only), `macos-spm-app-packaging` script templates for bundle/sign/notarise/appcast.

**Spec:** [[Axon Companion — PRD]] (CFR numbers referenced throughout). Daemon ground truth: `~/Projects/axon` — `internal/dashboard/server.go` (routes), `internal/service/service.go` (launchd `com.axon.<profile>`), `docs/09-component-dashboard-observability.md`, `docs/10-component-installer-bootstrap.md`.

## Global Constraints

- Deployment target **macOS 26.0**; build with the macOS 26 SDK; zero deprecated-API warnings at release (CFR-90/91).
- **SwiftPM only** — no `.xcodeproj`. Every task ends with `swift build && swift test` green, run from `apps/companion/`.
- **Swift Testing**, not XCTest (`import Testing`, `@Test`, `#expect`).
- Dependencies: **none until Task 16**, then exactly **Sparkle 2.x**. No YAML/networking/utility packages — config access is `axon config get/set --json` by design (CFR-41).
- Never read or write `~/.axon/config.yaml` or `~/.axon/.env`. Never touch `launchctl` or plists directly (the CLI owns service semantics, CFR-10/11).
- HTTP only to `127.0.0.1`; tolerant JSON decoding everywhere (unknown fields ignored; new fields optional) per CFR-82.
- Bundle ID `com.axon.companion`; display name "Axon"; menu bar app (`LSUIElement` via `MENU_BAR_APP=1` packaging flag).
- All UI observes `@MainActor @Observable` models; I/O types are `actor`s; no `DispatchQueue`.
- Commits: conventional, scoped — `feat(companion): …`, `test(companion): …`, `chore(companion): …`; upstream Go work `feat(cli): …`. Commit at the end of every task at minimum.
- **The two AXON cardinal rules apply to the humans/agents building this too:** never mutate the vault outside wikilink-safe tools; this plan's work happens in the git repo, not the vault.

## File Structure

```
apps/companion/
├── Package.swift
├── version.env                      # APP_NAME, BUNDLE_ID, VERSION, BUILD_NUMBER
├── CONTRACT.md                      # frozen daemon JSON contract + captured samples
├── Scripts/                         # from macos-spm-app-packaging templates
│   ├── package_app.sh  compile_and_run.sh  launch.sh
│   ├── sign-and-notarize.sh  make_appcast.sh  setup_dev_signing.sh  build_icon.sh
├── Sources/AxonKit/
│   ├── Models/ Health.swift  Metrics.swift  Events.swift  Doctor.swift  Status.swift
│   ├── Client/ DashboardClient.swift  SSEClient.swift
│   ├── CLI/    CLIRunner.swift  AxonCLI.swift  BinaryLocator.swift
│   └── Control/ DaemonController.swift  MetricsStore.swift  SettingsStore.swift
│               NotificationRouter.swift  OnboardingModel.swift  DoctorModel.swift
├── Sources/Companion/
│   ├── CompanionApp.swift
│   ├── MenuBar/   StatusPopover.swift  MenuBarIcon.swift  QuickActions.swift
│   ├── Insights/  InsightsWindow.swift  ChartCard.swift  TokenChart.swift
│   │              BudgetGauges.swift  RunsChart.swift  IngestionChart.swift  VaultGrowthChart.swift
│   ├── Settings/  SettingsWindow.swift  GeneralPane.swift  DaemonPane.swift  AutomationsPane.swift  AboutPane.swift
│   ├── Doctor/    DoctorWindow.swift
│   ├── Onboarding/ OnboardingWindow.swift  PrereqStepView.swift
│   └── Support/   Glass.swift  Formatters.swift  OpenActions.swift
├── Tests/AxonKitTests/
│   ├── Fixtures/  health.json  tokens.json  usage.json  runs.json  ingestion.json
│   │              vault.json  status-cli.json  automations-cli.json  doctor-cli.json  events.sse
│   └── *.swift
└── .github → job added to existing workflow at repo root
```

---

## Phase 0 — Daemon contract

### Task 1: Freeze the machine contract (`CONTRACT.md` + fixtures)

**Files:**
- Create: `apps/companion/CONTRACT.md`
- Create: `apps/companion/Tests/AxonKitTests/Fixtures/{health,tokens,usage,runs,ingestion,vault}.json`, `{status-cli,automations-cli,doctor-cli}.json`, `events.sse`
- Possibly modify (upstream): `cmd/axon/doctor_cmd.go` — **only if** `axon doctor` lacks `--json` (`axon health --json` and `axon status --json` already exist; verify doctor)

**Interfaces:**
- Produces: the canonical sample payloads every AxonKit decoder test decodes; the header names the mutation endpoints require (read them from `internal/dashboard/server.go` — the review/actions guard headers and `X-Axon-Related`).

- [ ] **Step 1: Capture live payloads.** With the daemon running (`axon start`): `curl -s 127.0.0.1:7777/health | jq . > Tests/AxonKitTests/Fixtures/health.json` and likewise for `/api/tokens`, `/api/usage`, `/api/runs`, `/api/ingestion`, `/api/vault`. Capture 10 s of `curl -sN 127.0.0.1:7777/events > Fixtures/events.sse`. Capture `axon status --json`, `axon automations --json`, `axon doctor --json` (if the flag is missing, add it upstream mirroring `cmd/axon/health_cmd.go`'s `--json` pattern, with a Go test mirroring `status_cmd_test.go`; commit separately as `feat(cli): add --json to doctor`).
- [ ] **Step 2: Write CONTRACT.md.** For each endpoint/command: method+path or argv, sample payload (trimmed), field-by-field notes (which fields Companion reads; which are optional), required request headers for `POST /api/review/action`, `POST /api/actions/complete`, `GET /api/related` copied from `server.go`, and the SSE `event:`/`data:` framing with the full `kind` union from `docs/09` §3. State the compatibility rule (tolerant decoding; min daemon version = current release).
- [ ] **Step 3: Commit** — `chore(companion): freeze daemon API contract with captured fixtures`.

---

## Phase 1 — Scaffold & AxonKit foundation

### Task 2: Bootstrap the SwiftPM app skeleton

**Files:**
- Create: `apps/companion/Package.swift`, `version.env`, `Scripts/*`, `Sources/Companion/CompanionApp.swift`, `Sources/AxonKit/Models/Status.swift` (placeholder enum), `Tests/AxonKitTests/SmokeTests.swift`

**Interfaces:**
- Produces: targets `AxonKit` (library) and `Companion` (executable, depends on AxonKit); the build/package loop every later task uses.

- [ ] **Step 1:** Copy the `macos-spm-app-packaging` skill's `assets/templates/bootstrap/` into `apps/companion/`; rename `MyApp` → `Companion`; split into the two targets:

```swift
// Package.swift
// swift-tools-version: 6.2
import PackageDescription

let package = Package(
    name: "Companion",
    platforms: [.macOS("26.0")],
    targets: [
        .target(name: "AxonKit"),
        .executableTarget(name: "Companion", dependencies: ["AxonKit"]),
        .testTarget(name: "AxonKitTests", dependencies: ["AxonKit"],
                    resources: [.copy("Fixtures")]),
    ]
)
```

- [ ] **Step 2:** Minimal app entry (placeholder icon; real states in Task 7):

```swift
// Sources/Companion/CompanionApp.swift
import SwiftUI

@main
struct CompanionApp: App {
    var body: some Scene {
        MenuBarExtra("Axon", systemImage: "brain") {
            Text("AXON").padding()
        }
        .menuBarExtraStyle(.window)
    }
}
```

- [ ] **Step 3:** `version.env`: `APP_NAME=Axon`, `BUNDLE_ID=com.axon.companion`, `VERSION=0.1.0`, `BUILD_NUMBER=1`. Copy the packaging scripts from the skill's `assets/templates/` into `Scripts/`; set `MENU_BAR_APP=1` in `package_app.sh` so Info.plist gets `LSUIElement`.
- [ ] **Step 4:** Smoke test (`@Test func smoke() { #expect(true) }`), then run `swift build && swift test` and `Scripts/compile_and_run.sh` — the brain icon must appear in the menu bar.
- [ ] **Step 5: Commit** — `feat(companion): scaffold SwiftPM menu bar app skeleton`.

### Task 3: Models + DashboardClient (REST)

**Files:**
- Create: `Sources/AxonKit/Models/Health.swift`, `Models/Metrics.swift`, `Client/DashboardClient.swift`
- Test: `Tests/AxonKitTests/DecodingTests.swift`, `DashboardClientTests.swift`

**Interfaces:**
- Produces: `struct AxonHealth: Decodable, Sendable` (fields per the captured fixture: overall status, component checks, `version: String`, `latestVersion: String?`, `updateAvailable: Bool?`, feature flags, embeddings provider info — exact names from `CONTRACT.md`, mapped via `CodingKeys`); `TokenSeries`, `UsageSnapshot` (day/week used+limit+guard state), `RunRecord`, `IngestionStats`, `VaultStats` — all `Decodable, Sendable`, all-optional beyond identity fields (tolerant per CFR-82).
- Produces: `actor DashboardClient { init(baseURL: URL = URL(string:"http://127.0.0.1:7777")!, session: URLSession = .shared); func health() async throws -> AxonHealth; func tokens() async throws -> TokenSeries; func usage() async throws -> UsageSnapshot; func runs() async throws -> [RunRecord]; func ingestion() async throws -> IngestionStats; func vault() async throws -> VaultStats; func reviewCount() async throws -> Int; func actionsCount() async throws -> Int; func exportURL(chart: String, format: String) -> URL }` and `enum DashboardError: Error { case unreachable, badStatus(Int), decoding(Error) }`.

- [ ] **Step 1: Failing decode tests** — one per fixture:

```swift
import Testing, Foundation
@testable import AxonKit

@Test func decodesHealthFixture() throws {
    let url = Bundle.module.url(forResource: "Fixtures/health", withExtension: "json")!
    let health = try JSONDecoder().decode(AxonHealth.self, from: Data(contentsOf: url))
    #expect(!health.version.isEmpty)
}

@Test func tolerantToUnknownFields() throws {
    let data = Data(#"{"version":"9.9.9","some_future_field":{"x":1}}"#.utf8)
    #expect(try JSONDecoder().decode(AxonHealth.self, from: data).version == "9.9.9")
}
```

- [ ] **Step 2:** Run — FAIL (types missing). **Step 3:** Write the model structs against the fixtures (snake_case via `keyDecodingStrategy` or explicit `CodingKeys`; every non-identity field optional). **Step 4:** Tests pass.
- [ ] **Step 5: Client tests with a stub `URLProtocol`** (register a `MockURLProtocol` that serves fixture data per path; assert `health()` decodes and a connection error throws `.unreachable`). **Step 6:** implement `DashboardClient` (per-request `URLRequest`, 3 s timeout, status-code check). **Step 7:** green.
- [ ] **Step 8: Commit** — `feat(companion): AxonKit models and dashboard REST client`.

### Task 4: SSEClient (live events)

**Files:**
- Create: `Sources/AxonKit/Models/Events.swift`, `Client/SSEClient.swift`
- Test: `Tests/AxonKitTests/SSEClientTests.swift`

**Interfaces:**
- Produces: `struct AxonEvent: Sendable { let kind: String; let level: String; let message: String; let ts: Date?; let data: [String: JSONValue]? }` with the kind constants from `CONTRACT.md` (`automation.fail`, `token.deny`, `ingest.done`, …); `actor SSEClient { init(url: URL, session: URLSession); func stream() -> AsyncStream<SSEClientState> }` where `enum SSEClientState { case connected; case event(AxonEvent); case disconnected }` — reconnects with exponential backoff (1 s → 30 s cap) and surfaces connect/disconnect so `DaemonController` can use stream liveness as a health signal.

- [ ] **Step 1: Failing parser tests** — feed `Fixtures/events.sse` bytes through the frame parser; assert `event:`/`data:` pairs become `AxonEvent`s, multi-line `data:` concatenates, comments/heartbeats are skipped. Include a malformed-frame case (skipped, not fatal).
- [ ] **Step 2:** Implement: `URLSession.bytes(for:)`, split on blank lines, JSON-decode `data:` into `AxonEvent` (tolerant). **Step 3:** green. **Step 4:** Reconnect test with a scripted URLProtocol that drops after N events — assert `.disconnected` then `.connected` again and backoff growth (inject a `sleep` closure to avoid real waits).
- [ ] **Step 5: Commit** — `feat(companion): SSE client with typed events and reconnect`.

### Task 5: CLIRunner + AxonCLI + BinaryLocator

**Files:**
- Create: `Sources/AxonKit/CLI/CLIRunner.swift`, `CLI/AxonCLI.swift`, `CLI/BinaryLocator.swift`
- Test: `Tests/AxonKitTests/AxonCLITests.swift` (+ a `Fixtures/fake-axon` shell script resource, chmod +x at test setup)

**Interfaces:**
- Produces: `protocol CLIRunning: Sendable { func run(_ arguments: [String], timeout: Duration) async throws -> CLIResult }`; `struct CLIResult { let exitCode: Int32; let stdout: Data; let stderr: String }`; `actor ProcessCLIRunner: CLIRunning` (wraps `Process` + pipes, kills on timeout); `struct BinaryLocator { static func locate(explicit: String?) -> URL? }` (order: explicit setting → `/usr/local/bin/axon` → `/opt/homebrew/bin/axon` → `env` PATH scan; never `$SHELL -c`).
- Produces: `struct AxonCLI: Sendable { init(runner: CLIRunning, binary: URL); func status() async throws -> DaemonStatus; func health() async throws -> AxonHealth; func start() async throws; func stop() async throws; func doctor() async throws -> DoctorReport; func automations() async throws -> [AutomationInfo]; func configGet(_ key: String) async throws -> JSONValue; func configSet(_ key: String, _ value: String) async throws; func profiles() async throws -> [ProfileInfo]; func update() async throws -> CLIResult; func serviceInstall() async throws; func serviceUninstall() async throws }` — every read parses the command's `--json` output; non-zero exit → `AxonCLIError.failed(command:stderr:)`.
- Produces: `DoctorReport` / `DoctorCheck { name, status: pass|warn|fail, detail, remediation }` in `Models/Doctor.swift`; `DaemonStatus` in `Models/Status.swift` decoded from `status-cli.json`.

- [ ] **Step 1: Failing tests** using a fake binary — a bundled shell script that echoes fixture JSON per subcommand (`case "$1" in status) cat status-cli.json;; …`) — assert `status()` decodes, `configSet` passes `config set key value --json` argv exactly, non-zero exit throws with stderr attached, and timeout kills the process.
- [ ] **Step 2:** Implement runner (Process, `terminationHandler` continuation, `Task` timeout race) and `AxonCLI` argv builders. **Step 3:** green.
- [ ] **Step 4: Commit** — `feat(companion): axon CLI wrapper with JSON parsing and binary discovery`.

### Task 6: DaemonController state machine

**Files:**
- Create: `Sources/AxonKit/Control/DaemonController.swift` (+ `DaemonState` in `Models/Status.swift`)
- Test: `Tests/AxonKitTests/DaemonControllerTests.swift`

**Interfaces:**
- Produces: `enum DaemonState: Equatable, Sendable { case unknown, notInstalled, stopped, starting, stopping, running(AxonHealth), attention(AxonHealth, reasons: [AttentionReason]) }`; `enum AttentionReason: Equatable { case degraded(component: String), budgetGuard, updateAvailable }`.
- Produces: `@MainActor @Observable final class DaemonController { var state: DaemonState; var lastError: String?; init(client: DashboardClient, sse: SSEClient, cli: AxonCLI?, pollInterval: Duration = .seconds(5)); func startMonitoring(); func refresh() async; func startDaemon() async; func stopDaemon() async; func restartDaemon() async }` — derivation rules: no binary → `.notInstalled`; `/health` OK+clean → `.running`; OK+issues (component fail, guard tripped, `updateAvailable`) → `.attention`; port unreachable but binary present → `.stopped` (confirmed via `cli.status()` when available); transitions through `.starting`/`.stopping` pin until health confirms or a 20 s deadline reverts with `lastError`. ≤5 s detection (CFR-01) via poll + SSE disconnect fast-path.

- [ ] **Step 1: Failing tests** with stub client/CLI conformances driven by scripted responses: healthy→`.running`; guard-tripped fixture→`.attention([.budgetGuard])`; connection refused + status says stopped→`.stopped`; nil binary→`.notInstalled`; `startDaemon()` walks `.stopped → .starting → .running`; failed start reverts with `lastError`.
- [ ] **Step 2:** Implement (poll loop as a `Task` owned by the controller; SSE `.disconnected` triggers immediate `refresh()`). **Step 3:** green. **Step 4: Commit** — `feat(companion): daemon state machine with lifecycle control`.

---

## Phase 2 — Menu bar UI

### Task 7: Menu bar icon + status popover + quick actions

**Files:**
- Create: `Sources/Companion/MenuBar/MenuBarIcon.swift`, `MenuBar/StatusPopover.swift`, `MenuBar/QuickActions.swift`, `Support/Glass.swift`, `Support/Formatters.swift`, `Support/OpenActions.swift`
- Modify: `Sources/Companion/CompanionApp.swift`
- Test: `Tests/AxonKitTests/OpenActionsTests.swift` (URL construction only)

**Interfaces:**
- Consumes: `DaemonController.state`, `DashboardClient.reviewCount()/actionsCount()`, `UsageSnapshot`.
- Produces: `enum OpenAction { case dashboard, reviewTab, actionsTab, vaultInObsidian(vaultPath: String), vaultInFinder(String), logsFolder(profile: String), insights, doctor, settings; var url: URL? }` (dashboard deep links `http://127.0.0.1:7777/#/review` etc. — confirm hash routes from `web/src/App.jsx` and record in CONTRACT.md; Obsidian: `obsidian://open?path=<percent-encoded vault path>`); `Glass.card(_:)` view modifier (glass on macOS 26, solid fallback under Reduce Transparency — the **only** file that names glass APIs, CFR-91).

- [ ] **Step 1:** App wiring: create `DaemonController` + `SettingsStore` as `@State` in `CompanionApp`; `MenuBarExtra` label switches on state — running: `Image(systemName: "brain.fill")`; attention: brain + orange dot overlay (template-rendered); stopped: `brain` with `.secondary` opacity; notInstalled: brain + question overlay. `.menuBarExtraStyle(.window)`.
- [ ] **Step 2:** `StatusPopover` layout (PRD §4): header (profile · version · uptime) in a `Glass.card`; two `Gauge(value:)` budget gauges (`.gaugeStyle(.accessoryCircularCapacity)`) fed from `UsageSnapshot`; badge row (`Review n` / `Actions n` buttons → `OpenAction`); quick-actions grid — Start/Stop/Restart (state-dependent enable, confirmation dialog on Stop per CFR-12), Dashboard, Vault, Insights; footer: Doctor · Settings (`SettingsLink`) · Quit (`NSApplication.shared.terminate` — Companion only, CFR quit never touches daemon). Wrap glass tiles in one `GlassEffectContainer`; `.interactive()` on tappable tiles only.
- [ ] **Step 3:** Every state renders something actionable: `.stopped` → big Start button; `.notInstalled` → "Set up AXON…" button (opens Onboarding window, stubbed until Task 13); `.attention` → reason chips linking to Doctor/Insights.
- [ ] **Step 4:** Unit-test `OpenAction.url` for all cases (percent-encoding of vault paths with spaces). UI verification manually via `Scripts/compile_and_run.sh` against the live daemon: kill daemon (`axon stop`) → icon flips ≤5 s; start from popover → running.
- [ ] **Step 5:** Accessibility pass on the popover: labels on gauges/badges, full keyboard traversal, Esc closes.
- [ ] **Step 6: Commit** — `feat(companion): menu bar presence with status popover and lifecycle actions`.

### Task 8: Login items + windows plumbing

**Files:**
- Create: `Sources/Companion/Insights/InsightsWindow.swift` (empty shell), `Doctor/DoctorWindow.swift` (shell), `Onboarding/OnboardingWindow.swift` (shell)
- Modify: `CompanionApp.swift`, `Sources/AxonKit/Control/SettingsStore.swift` (create)

**Interfaces:**
- Produces: `@MainActor @Observable final class SettingsStore { var launchCompanionAtLogin: Bool  // SMAppService.mainApp register/unregister; var refreshInterval: Duration; var notifications: NotificationPrefs; func daemonServiceInstalled() async -> Bool; func setDaemonServiceInstalled(_ on: Bool) async }` (daemon toggle shells `axon service install|uninstall` via AxonCLI — CFR-11 keeps the two toggles distinct); UserDefaults-backed app prefs with an injected `UserDefaults` for tests.
- Produces: `Window("Axon Insights", id: "insights")`, `Window("Axon Doctor", id: "doctor")`, `Window("Welcome to Axon", id: "onboarding")` scenes + `openWindow` environment usage from the popover; windows restore frames (`windowResizability` defaults) and activate the app (`NSApp.activate`).

- [ ] **Step 1:** Tests for `SettingsStore` defaults/persistence with an isolated `UserDefaults(suiteName:)`. **Step 2:** implement store; wire `SMAppService.mainApp.register()/unregister()` behind the toggle (guard errors into a user-visible string). **Step 3:** add the three `Window` scenes; popover buttons open them. **Step 4:** manual check: windows open, appear in front, restore size. **Step 5: Commit** — `feat(companion): app windows, settings store, launch-at-login`.

---

## Phase 3 — Insights (Swift Charts)

### Task 9: MetricsStore

**Files:**
- Create: `Sources/AxonKit/Control/MetricsStore.swift`
- Test: `Tests/AxonKitTests/MetricsStoreTests.swift`

**Interfaces:**
- Consumes: `DashboardClient` reads, `SSEClient` events.
- Produces: `@MainActor @Observable final class MetricsStore { enum Range: CaseIterable { case day, week, month }; var range: Range; var tokens: TokenSeries?; var usage: UsageSnapshot?; var runs: [RunRecord]?; var ingestion: IngestionStats?; var vault: VaultStats?; var loadState: LoadState  // idle|loading|loaded|failed(String); func startLive(); func refreshAll() async }` — refresh on: window open, range change, and SSE events of kinds `token.*`, `automation.*`, `ingest.*` (coalesced ≤1 refresh per 3 s so chart data is ≤5 s stale under load without hammering the API, CFR-30).

- [ ] **Step 1:** Failing tests: scripted client → `refreshAll` populates all five; a burst of 10 SSE events causes exactly one coalesced refresh (inject clock); client failure → `.failed` with message while stale data is retained.
- [ ] **Step 2:** Implement. **Step 3:** green. **Step 4: Commit** — `feat(companion): live metrics store with SSE-driven coalesced refresh`.

### Task 10: Chart cards

**Files:**
- Create: `Sources/Companion/Insights/ChartCard.swift`, `TokenChart.swift`, `BudgetGauges.swift`, `RunsChart.swift`, `IngestionChart.swift`, `VaultGrowthChart.swift`
- Modify: `Insights/InsightsWindow.swift`

**Interfaces:**
- Consumes: `MetricsStore` published series; `DashboardClient.exportURL(chart:format:)`.
- Produces: `ChartCard(title:subtitle:exportChart:content:)` — shared frame: quiet opaque card background (**no glass behind charts**, PRD §4), title row with range-aware subtitle and an export menu (CSV/JSON → `NSSavePanel`, data fetched from `/api/export` — CFR-31).

- [ ] **Step 1:** `InsightsWindow`: scrolling `LazyVStack` of cards, top `Picker` bound to `MetricsStore.range`, empty/degraded states ("daemon stopped — start it to see live data" with a Start button).
- [ ] **Step 2:** `TokenChart` — the reference implementation the other charts copy:

```swift
Chart(store.tokens?.points ?? []) { point in
    BarMark(x: .value("Day", point.date, unit: .day),
            y: .value("Tokens", point.tokens))
    .foregroundStyle(by: .value("Automation", point.automation))
}
.chartLegend(position: .bottom)
.chartYAxis { AxisMarks(format: .number.notation(.compactName)) }
.accessibilityLabel("Tokens per day, stacked by automation")
```

with a segmented sub-toggle for by-automation vs by-model stacking (both series come from `/api/tokens`).
- [ ] **Step 3:** `BudgetGauges` (two `Gauge`s + guard-state chip, red when paused); `RunsChart` (`PointMark`/`RectangleMark` timeline coloured by status, plus a success-rate `RuleMark` average); `IngestionChart` (bars/day, split success vs failed+redacted, embed-queue-depth line on a second axis); `VaultGrowthChart` (`LineMark` ×3: notes/links/words, plus inbox-backlog + review-queue-size small multiples).
- [ ] **Step 4:** Numbers cross-check manually against the web dashboard for the same ranges (acceptance-gate item). Verify light/dark, Reduce Motion (no animated transitions when set), VoiceOver reads each series.
- [ ] **Step 5: Commit** — `feat(companion): insights window with live Swift Charts`.

---

## Phase 4 — Settings

### Task 11: Settings window (General · Daemon · Automations · About)

**Files:**
- Create: `Sources/Companion/Settings/SettingsWindow.swift`, `GeneralPane.swift`, `DaemonPane.swift`, `AutomationsPane.swift`, `AboutPane.swift`
- Modify: `Sources/AxonKit/Control/SettingsStore.swift` (daemon-facing additions)
- Test: extend `Tests/AxonKitTests/AxonCLITests.swift` for the new config keys

**Interfaces:**
- Consumes: `AxonCLI.configGet/configSet/automations/profiles`, `SettingsStore`.
- Produces: `SettingsStore` additions: `func budget() async throws -> (day: Int, week: Int)`, `func setBudget(day: Int?, week: Int?) async throws` (keys per `axon.config.example.yaml` — resolve exact key paths via `axon config get` during implementation and record in CONTRACT.md), `func automationList() async throws -> [AutomationInfo]`, `func setAutomation(_ name: String, enabled: Bool) async throws` (`config set profiles.<p>.automations.<name>.enabled …`).

- [ ] **Step 1:** `Settings` scene + `TabView`. **General:** launch Companion at login; start daemon at login (`axon service` toggle, distinct copy per CFR-11); refresh interval; notification toggles; "Run onboarding again". **Daemon:** profile + vault path (read-only rows), dashboard URL (read-only + open), day/week budget steppers with explicit Apply (writes via `configSet`, shows daemon's JSON reply, offers restart when the CLI says one is needed), embeddings provider read-only row with "how to switch" link to GUIDE §4 (CFR-42). **Automations:** list with per-row `Toggle`, run cadence and last-run columns from `automations --json`; toggling calls `setAutomation`, re-reads, and reflects reality (never optimistic). **About:** versions (Companion + daemon), links (dashboard, GUIDE, repo), update check row.
- [ ] **Step 2:** Kit-level tests: argv assertions for `setBudget`/`setAutomation` against the fake binary; toggle round-trip re-read.
- [ ] **Step 3:** Manual: change day budget → `axon config get` shows it; toggle an automation → `axon automations` agrees; both survive daemon restart.
- [ ] **Step 4: Commit** — `feat(companion): settings window backed by axon config commands`.

---

## Phase 5 — Doctor

### Task 12: Doctor window + diagnostics export

**Files:**
- Create: `Sources/AxonKit/Control/DoctorModel.swift`, `Sources/Companion/Doctor/DoctorWindow.swift` (replace shell)
- Test: `Tests/AxonKitTests/DoctorModelTests.swift`

**Interfaces:**
- Consumes: `AxonCLI.doctor()`, `DashboardClient.health()`.
- Produces: `@MainActor @Observable final class DoctorModel { var report: DoctorReport?; var running: Bool; func run() async; func diagnosticsText() async -> String }` — `diagnosticsText` concatenates: Companion version/build, macOS version, daemon `status --json`, full `doctor` report, `/health` JSON. **Redaction guard in code:** the assembled text is regex-scanned for token-looking strings (`sk-`, `oauth`, 40+ char base64 runs) and the home directory is rewritten to `~` — belt-and-braces even though none of the sources should carry secrets (CFR-61).

- [ ] **Step 1:** Failing tests: fixture doctor JSON → grouped pass/warn/fail counts; `diagnosticsText()` contains version + check names, and a planted fake token in a stubbed health payload is redacted.
- [ ] **Step 2:** Implement model. **Step 3:** `DoctorWindow`: toolbar (Re-run, Copy Diagnostics), list rows — glyph (`checkmark.circle.fill` green / `exclamationmark.triangle.fill` orange / `xmark.circle.fill` red), check name, detail, remediation as selectable monospaced text with a per-row copy button; daemon-down state runs doctor via CLI anyway (it works without the daemon — that's its job).
- [ ] **Step 4:** When 2.0 P5's `axon doctor --bundle` ships, `diagnosticsText` delegates to it — leave a guarded capability check (`doctor --help` scan), not a TODO: implement the check now, branch on it.
- [ ] **Step 5: Commit** — `feat(companion): doctor window with redacted diagnostics export`.

---

## Phase 6 — Onboarding & notifications

### Task 13: First-run onboarding wizard

**Files:**
- Create: `Sources/AxonKit/Control/OnboardingModel.swift`, `Sources/Companion/Onboarding/PrereqStepView.swift`; replace `OnboardingWindow.swift` shell
- Test: `Tests/AxonKitTests/OnboardingModelTests.swift`

**Interfaces:**
- Consumes: `BinaryLocator`, `AxonCLI.doctor()/start()`, `DashboardClient.health()`.
- Produces: `@MainActor @Observable final class OnboardingModel { enum Step: CaseIterable { case welcome, axonBinary, claudeCLI, ollama, daemonService, done }; var current: Step; var checks: [Step: CheckState]  // unknown|checking|pass|fail(detail); func recheck() async; var shouldAutoPresent: Bool }` — checks: `axonBinary` via `BinaryLocator`; `claudeCLI`/`ollama` from doctor's per-check results when `axon` exists, else direct detection (`which claude`, GET `127.0.0.1:11434/api/tags` — with the Apple on-device alternative explained per README); `daemonService` via `status`. `shouldAutoPresent` = first launch (UserDefaults flag) or `.notInstalled`.
- Per-step UI content: plain-language explanation, the copyable command (`brew install ollama` + `ollama pull nomic-embed-text`; `npm install -g @anthropic-ai/claude-code` + `claude login`; the `install.sh` one-liner from the README), a Copy button, an "Open Terminal" button (`NSWorkspace` open Terminal.app), and a live re-check every 3 s while the step is visible. **Companion never runs installers itself** (CFR-51).

- [ ] **Step 1:** Failing model tests: scripted detections drive step gating (can't advance past a failing required step; Ollama step is skippable with the on-device note); completion sets the UserDefaults flag; `recheck` transitions fail→pass when the stub starts answering.
- [ ] **Step 2:** Implement model + paged wizard UI (`TabView`-paged, progress dots, celebratory final page that calls `startDaemon()` and opens the dashboard — CFR-52). Auto-present on `shouldAutoPresent`.
- [ ] **Step 3:** Manual clean-ish rehearsal: temporarily point `BinaryLocator` at a nonexistent explicit path → full wizard flow renders; restore.
- [ ] **Step 4: Commit** — `feat(companion): guided first-run onboarding`.

### Task 14: Notifications

**Files:**
- Create: `Sources/AxonKit/Control/NotificationRouter.swift`
- Modify: `CompanionApp.swift` (wire), `GeneralPane.swift` (toggles exist since Task 11)
- Test: `Tests/AxonKitTests/NotificationRouterTests.swift`

**Interfaces:**
- Consumes: SSE `AxonEvent` stream, `DaemonState` transitions, `NotificationPrefs`.
- Produces: `final class NotificationRouter: Sendable { init(prefs: @Sendable () -> NotificationPrefs, post: @Sendable (PlannedNotification) -> Void); func handle(event: AxonEvent); func handle(transition: (from: DaemonState, to: DaemonState)) }` with `struct PlannedNotification { let id: String; let title: String; let body: String; let action: OpenAction }` — rules: `automation.fail` → one notification per automation per hour (dedup window); `token.deny`/guard trip → once per guard episode; running→stopped **without** a user-initiated stop → "AXON stopped unexpectedly"; `updateAvailable` flip → once per version. The `post` closure is the only place touching `UNUserNotificationCenter` (auth request on first enable; delivered actions deep-link via `OpenAction`). Success is never notified (CFR-71).

- [ ] **Step 1:** Failing tests on the pure router: dedup windows (injected clock), pref gating (disabled kind → no post), unexpected-stop vs user-stop discrimination (router is told of user intent via a `noteUserInitiatedStop()` call from `DaemonController`).
- [ ] **Step 2:** Implement router; wire in app; request authorization lazily. **Step 3:** manual: `axon stop` from Terminal (not the app) → notification; from the popover → none.
- [ ] **Step 4: Commit** — `feat(companion): event-driven notifications with dedup`.

---

## Phase 7 — Packaging, release, hardening

### Task 15: Icon, bundle, sign, notarise

**Files:**
- Create: `apps/companion/Assets/icon.icon` (Icon Composer) or fallback iconset; entitlements file `apps/companion/Companion.entitlements`
- Modify: `Scripts/package_app.sh` (icon + entitlements paths), `version.env`

- [ ] **Step 1:** App icon (owner supplies or derive from AXON mark; `Scripts/build_icon.sh` → `.icns`). Menu bar template icon stays SF Symbols.
- [ ] **Step 2:** Entitlements: hardened runtime, **no** app sandbox (documented inline: Companion execs `axon` and reads user-chosen folders; revisit post-2.0 — this mirrors [[Axon 2.0 — PRD]] P6 least-privilege documentation). No network-server entitlement; client-only.
- [ ] **Step 3:** `Scripts/setup_dev_signing.sh` for the dev loop; `Scripts/package_app.sh` produces `dist/Axon.app` with `LSUIElement`, correct `CFBundleVersion` from `version.env`; `codesign --verify --deep --strict` passes.
- [ ] **Step 4:** `Scripts/sign-and-notarize.sh` with the owner's Developer ID (credentials via `xcrun notarytool store-credentials`, profile name in the script env — never committed); staple; `spctl -a -vv dist/Axon.app` = accepted.
- [ ] **Step 5: Commit** — `feat(companion): signed and notarised app packaging`.

### Task 16: Sparkle updates + Makefile + CI

**Files:**
- Modify: `Package.swift` (add `https://github.com/sparkle-project/Sparkle` 2.x — the plan's only dependency), `CompanionApp.swift` (SPUStandardUpdaterController; About-pane check row), `Scripts/make_appcast.sh`, repo-root `Makefile` (`companion`, `companion-release` targets), `.github/workflows/ci.yml` (macOS job: `swift build && swift test` on PR; package on tag)
- Create: `apps/companion/appcast/README.md` (where the appcast is hosted — GitHub Releases asset, same bucket as daemon releases)

- [ ] **Step 1:** Add Sparkle; Info.plist keys via `package_app.sh` (`SUFeedURL`, `SUPublicEDKey` from `generate_keys`; private key in the owner's keychain only). Update check honours a Settings toggle; About shows "checks for updates at <feed>" (CFR-81 disclosure).
- [ ] **Step 2:** `make_appcast.sh` generates the appcast entry from `dist/` zip + `version.env` (`BUILD_NUMBER` must increase every release — enforced by a script check).
- [ ] **Step 3:** Makefile + CI wiring; CI also decodes the fixture contract against the **previous** daemon release binary when cached (skew guard, CFR-82) and adds an allowed-to-fail lane on the newest beta SDK when GitHub runners offer one (CFR-91).
- [ ] **Step 4:** End-to-end rehearsal: bump `version.env`, build twice, serve appcast locally, old build offers + installs the new one.
- [ ] **Step 5: Commit** — `feat(companion): sparkle auto-updates, make targets, CI job`.

### Task 17: Acceptance pass + macOS 27 readiness + docs

**Files:**
- Modify: `README.md` (Companion section under Install), `docs/GUIDE.md` (new "§ Companion" chapter), `INSTALL.md`
- Create: `apps/companion/QA.md` (the executed checklist, dated)

- [ ] **Step 1:** Execute the full PRD §8 acceptance gate on a clean macOS 26 account/VM; record results in `QA.md`; fix what fails before proceeding.
- [ ] **Step 2:** Platform audit: build log has zero deprecation warnings; every OS-conditional lives in `Support/Glass.swift`; Reduce Transparency/Motion, VoiceOver on popover+charts+doctor, full keyboard traversal, light/dark — all pass; note macOS 27 watch-items (glass API changes, MenuBarExtra behaviour) in `QA.md`.
- [ ] **Step 3:** Docs: README install snippet (download → drag → open), GUIDE chapter (what Companion does, that it is optional, every feature's CLI/dashboard equivalent — the complementarity promise in writing), INSTALL note on Gatekeeper.
- [ ] **Step 4:** Commit — `docs(companion): guide, install docs, executed QA checklist`; tag `companion-v0.1.0`.

---

## Self-review (executed)

- **Spec coverage:** CFR-01…03 → T6/T7; CFR-10…12 → T5/T6/T7; CFR-20/21 → T7 (`OpenActions`) + T11 read-only rows; CFR-30…32 → T9/T10; CFR-40…42 → T11; CFR-50…52 → T13; CFR-60/61 → T12; CFR-70/71 → T14; CFR-80…82 → T15/T16 (+T1 contract); CFR-90/91 → global constraints + T17. PRD open questions 1–4 do **not** block T1–T6; owner should answer before T7 (icon), T15 (name/branding), and release (pricing).
- **Placeholder scan:** the two forward references (dashboard hash-routes, budget config key paths) are explicitly resolved *inside* their tasks with a named discovery step recorded to CONTRACT.md — no TBDs remain.
- **Type consistency:** `AxonHealth`, `UsageSnapshot`, `DoctorReport`, `DaemonState`, `OpenAction`, `MetricsStore.Range` names are used identically across Tasks 3–14.

## Suggested agent execution order

Serial by default (each phase consumes the previous phase's interfaces). Safe parallel pairs once Phase 1 lands: T9+T11, T12+T13. T1 must precede everything; T15–T17 are strictly last.

<!-- axon:links:start -->
<!-- axon:links:end -->
