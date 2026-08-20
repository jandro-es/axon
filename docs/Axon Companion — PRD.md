---
title: Axon Companion — PRD
type: project
status: shipped (companion-v0.1.0, 2026-08-16)
created: 2026-08-16
updated: 2026-08-20
tags: [axon, macos, menu-bar, swiftui, prd]
---

# Axon Companion — PRD (macOS menu bar app)

> **Shipped 2026-08-16 as `companion-v0.1.0`** (signed, notarised, Sparkle appcast). This PRD is the product definition of record; the build plan it referenced was completed and deleted (the durable build artefacts are the code under `apps/companion/`, `apps/companion/CONTRACT.md`, and `apps/companion/QA.md`). Scope note: the launcher surfaces the review-queue badge, plus glanceable telemetry, settings, onboarding, and a doctor UI. The 2026-07-03 "no native app" decision **stands**: the web dashboard remains the product UI. Companion is a control plane and a glanceable window into it — strictly optional, never required.

## 1. Product principles

1. **Complementary, never required.** Every Companion feature has a CLI or dashboard equivalent. AXON installed without Companion is 100% functional; Companion uninstalled leaves zero residue in the daemon. Companion never becomes a dependency of setup, updates, or operation.
2. **The daemon is the brain.** Companion contains no business logic, no vault access, no model calls, and no config-file parsing. It *reads* via the dashboard's REST + SSE API (`127.0.0.1:7777`) and *mutates* only through the `axon` CLI's machine-readable commands (`start`, `stop`, `status --json`, `doctor`, `config set … --json`, …) and the two existing dashboard mutation endpoints. If Companion wants something the daemon can't say, the daemon grows the seam first (upstream Go task), not Companion.
3. **Native-first, macOS 26 Liquid Glass.** SwiftUI `MenuBarExtra`, Swift Charts, SF Symbols, system materials. Feels like it shipped with the OS; respects Reduce Transparency/Motion, Dark Mode, accent colour, VoiceOver.
4. **Same trust posture as the daemon.** No secrets ever read or held (never touches `~/.axon/.env`); localhost-only networking; the only new egress is the Sparkle update check, disclosed exactly like the daemon's own update check (2.0 P3).

## 2. Users & scenarios

- **B2 prosumer (today's user):** wants to see at a glance that AXON is alive, how much budget is left, and be told when an automation fails — without keeping a browser tab open.
- **P1 non-technical buyer (2.0):** downloads a signed app, is guided through prerequisites (Ollama, `claude` CLI, the daemon itself), and reaches a running system without Terminal. Companion is the front half of the zero-terminal onboarding gate.
- **The owner debugging at 23:00:** daemon looks stuck → opens Doctor from the menu bar, sees "Ollama not reachable — start it with `ollama serve`", fixes, restarts the daemon from the same menu.

## 3. Functional requirements

Numbered `CFR-nn` (Companion FR), provisional per the standing rule.

### Status & presence
- **CFR-01** Menu bar item always reflects daemon state within ≤5 s: *running / attention (degraded health, budget guard paused, update available) / stopped / not installed*. State derives from `GET /health` polling + SSE liveness + `axon status --json` fallback when the port is dead.
- **CFR-02** Popover header shows: profile name, daemon version, uptime pill, day/week budget gauges (same numbers as `axon status`), review-queue count and open-actions count as badges.
- **CFR-03** Update availability (daemon `update_available` from `/health`) is surfaced as a row with a one-click "Update AXON" action (`axon update` behind confirmation).

### Lifecycle control
- **CFR-10** Start / Stop / Restart the daemon from the popover; actions shell out to `axon start` / `axon stop` (never `launchctl` directly — the CLI owns the service semantics) and reconcile state by polling `/health`.
- **CFR-11** "Start AXON at login" toggle controls **Companion's** login item via `SMAppService.mainApp`; the *daemon's* login service remains launchd-owned (`axon service install|uninstall`), surfaced as a separate toggle that shells to those commands. The two are never conflated.
- **CFR-12** All destructive/irreversible actions (stop while a run is active, `axon update`) confirm first; confirmation copy names consequences.

### Open-things
- **CFR-20** One-click opens: Dashboard (`http://127.0.0.1:7777`), Review tab, Actions tab, the vault in Obsidian (`obsidian://open?path=…`, falling back to Finder), the vault folder in Finder, today's daily note, logs folder (`~/.axon/profiles/<name>/logs/`).
- **CFR-21** Vault path and profile name come from `axon config get` / `axon profiles --json` — never parsed from YAML.

### Insights (Swift Charts)
- **CFR-30** An Insights window renders, live (REST snapshot + SSE invalidation):
  - Tokens over time, stacked by automation and by model (`/api/tokens`).
  - Day/week budget gauges + guard state (`/api/usage`).
  - Automation runs: timeline, ok/skipped/failed, duration, success rate (`/api/runs`).
  - Ingestion throughput: sources/day, success vs failed/redacted, embed queue depth (`/api/ingestion`).
  - Vault growth: notes, links, words over time; inbox backlog; review-queue size (`/api/vault`).
- **CFR-31** Every chart exports its series via the daemon's `GET /api/export` (CSV/JSON) through a standard save panel — Companion adds no serialisation of its own.
- **CFR-32** Charts follow one visual system (shared axis/legend/tooltip treatment), render in light/dark, and are VoiceOver-audible (Swift Charts `accessibilityLabel` per series).

### Settings
- **CFR-40** Settings window (⌘,) with panes: **General** (login items, refresh interval, notification toggles), **Daemon** (budgets day/week, dashboard port readout, embeddings provider readout with switch guidance), **Automations** (list from `axon automations --json` with per-automation enable/disable via `axon config set`), **About**.
- **CFR-41** Every daemon-side write goes through `axon config set <key> <value> --json`; Companion shows the returned result and, where the daemon requires it, offers the restart. Companion never writes `config.yaml`.
- **CFR-42** Settings the daemon considers dangerous or rich (egress policy, redaction, profiles) are **linked to the dashboard/GUIDE**, not replicated.

### Onboarding (first run)
- **CFR-50** On first launch (or when `axon` binary is missing) a wizard walks the machine to green: checks for `axon`, `claude` CLI, Ollama (or Apple on-device path), daemon service; each step shows a copyable command (`brew install ollama`, the `install.sh` one-liner, `claude login`, …) with an "Open Terminal" convenience and re-checks automatically until green.
- **CFR-51** Companion **guides but never installs**: it does not download binaries or run installers itself; it re-runs detection (`axon doctor`) and celebrates progress. (Keeps the trust posture and Gatekeeper story clean; the daemon's own `axon setup` remains the actual provisioner.)
- **CFR-52** Final step starts the daemon and opens the dashboard; the wizard is re-runnable from Settings → General.

### Doctor & diagnostics
- **CFR-60** Doctor window runs `axon doctor` (JSON where available, else parses the health command `axon health --json`) and renders each check as pass/warn/fail with the daemon's own remediation text, a re-run button, and per-row copy.
- **CFR-61** "Copy diagnostics" exports a **redacted** bundle: `doctor` output + `/health` JSON + versions (Companion, daemon, macOS). When 2.0's `axon doctor --bundle` ships (P5), Companion switches to it; the interim bundle contains no vault content and no secrets by construction (sources listed above only).

### Notifications
- **CFR-70** Optional user notifications (all individually toggleable, default conservative): automation failed (`automation.fail`), budget guard tripped (`token.deny`/pause), daemon stopped unexpectedly, AXON update available. Sourced from SSE; deduplicated; deep-link into the relevant surface.
- **CFR-71** No notification for routine success. Ever.

### Distribution & updates
- **CFR-80** Developer ID–signed, hardened runtime, notarised, stapled; distributed as a zip/DMG next to the daemon's releases (no Mac App Store — consistent with 2.0's "direct distribution only").
- **CFR-81** Sparkle 2 auto-updates on a signed appcast; update checks disclosed in About; Companion and daemon update independently (Companion tolerates daemon version skew per the compatibility rule below).
- **CFR-82** **Compatibility rule:** Companion pins a *minimum* daemon API (the `/health` `version` field). Older daemon → Companion degrades feature-by-feature (hides what's missing, says why) rather than blocking. Newer daemon → unknown JSON fields are ignored by construction (tolerant decoding).

### Platform
- **CFR-90** macOS 26.0 minimum deployment; built with the macOS 26 SDK; Liquid Glass native.
- **CFR-91** macOS 27-ready: zero deprecated-API warnings at release; OS-version-gated adoption points isolated in one file (`Support/Glass.swift`); CI includes an allowed-to-fail lane against the newest beta SDK when available.

## 4. UX specification

### Menu bar item
| State | Icon (SF Symbols, template) | Detail |
|---|---|---|
| Running, healthy | `brain` (filled) | plain |
| Attention | `brain` + orange badge dot | degraded health / budget guard / update available |
| Stopped | `brain` outline, dimmed | |
| Not installed | `brain` outline + "?" badge | clicking opens onboarding |

Monochrome template images (menu bar rules); state colour only in the badge dot. Left-click opens the popover (`MenuBarExtra` window style); no separate right-click menu in v1.

### Popover (the one surface most users ever see)
Top→bottom: status header (profile · version · uptime) → two compact budget gauges (day/week) with the guard state → badge row (Review *n* · Actions *n* — click deep-links to dashboard tabs) → sparkline strip (tokens last 24 h) → quick actions grid (Start/Stop/Restart · Dashboard · Vault · Insights) → footer (Doctor · Settings · Quit Companion). The popover is glanceable in <2 s; anything deeper opens the Insights window or the dashboard.

### Windows
- **Insights** — sidebar-less single scroll of chart cards (Tokens, Budget, Runs, Ingestion, Vault growth), each with a range picker (24 h/7 d/30 d) and an export button. Resizable, remembers frame.
- **Settings** — standard `Settings` scene, TabView panes per CFR-40.
- **Doctor** — check list with status glyphs, remediation text, re-run and copy-diagnostics in the toolbar.
- **Onboarding** — paged wizard, one prerequisite per page, live re-check, celebratory final page.

### Liquid Glass rules
- The `MenuBarExtra` window and popover chrome use the system's native glass; content cards use `.glassEffect(…, in: .rect(cornerRadius:))` sparingly (status header and quick-action tiles only — **never** behind charts, which need quiet, opaque card backgrounds for legibility).
- Multiple adjacent glass tiles sit in one `GlassEffectContainer`; interactive tiles opt into `.interactive()`.
- All glass usage funnels through one `Glass.swift` helper so Reduce Transparency (solid fallback) and future macOS 27 material changes are one-file fixes.

### Accessibility & keyboard
Full keyboard traversal of popover actions; Esc closes; ⌘, Settings; ⌘Q quits Companion (never the daemon); VoiceOver labels on every state glyph, gauge, and chart; contrast ≥ WCAG AA on glass surfaces (verified over light and dark desktops).

## 5. Architecture

```
Companion.app (SwiftUI, menu-bar / LSUIElement)
├── Companion (executable target)        — views only; no I/O
└── AxonKit (library target)             — all logic; 100% unit-tested
     ├── DashboardClient  → REST  GET /health /api/tokens /api/usage /api/runs
     │                             /api/ingestion /api/vault /api/review /api/actions /api/export
     ├── SSEClient        → GET /events (typed AxonEvent stream, auto-reconnect)
     ├── AxonCLI          → Process → axon status|start|stop|doctor|health|update|profiles|
     │                                 automations|config get/set|service … (--json)
     ├── DaemonController — state machine (source of truth for the icon/popover)
     ├── MetricsStore     — snapshot + SSE-invalidated series for charts
     ├── SettingsStore    — app prefs (UserDefaults) + daemon config via AxonCLI
     ├── DoctorModel / OnboardingModel / NotificationRouter
     └── BinaryLocator    — explicit setting → /usr/local/bin/axon → PATH lookup
```

- **No third-party dependency except Sparkle** (added at the packaging phase). No YAML library — config access is `axon config get/set --json` by design.
- **Failure model:** every surface has an explicit empty/degraded rendering — daemon stopped (popover offers Start), port up but unhealthy (attention + Doctor shortcut), binary missing (onboarding). No spinners without a story.
- **Concurrency:** Swift 6 strict concurrency; `@Observable` models on `@MainActor`; clients are `actor`s.
- **Security:** hardened runtime; not App-Sandboxed in v1 (it must exec `axon` and open user files; documented in the entitlements file alongside 2.0 P6's least-privilege note). Networking restricted to `127.0.0.1` + Sparkle's appcast host. Mutation endpoints send the daemon's required guard headers.

### Integration contract
The daemon's JSON shapes (CLI `--json` and REST) are **frozen in `apps/companion/CONTRACT.md`** with captured sample payloads; AxonKit decodes tolerantly (unknown fields ignored, absent fields optional). Any needed daemon change (e.g., a missing `--json` flag) is an upstream Go task in the development plan, never a Companion workaround.

## 6. Packaging & distribution

- Lives in the axon monorepo at `apps/companion/` — SwiftPM package (no `.xcodeproj`), built and packaged by scripts (`package_app.sh` with `MENU_BAR_APP=1` → `LSUIElement`, `sign-and-notarize.sh`, `make_appcast.sh`), wired into the top-level Makefile (`make companion`, `make companion-release`) and CI (macOS job: build + test every PR; sign/notarise on tag).
- Bundle ID `com.axon.companion` (matches the `com.axon.<profile>` launchd convention); display name **"Axon"**; own `version.env` (semver + monotonically increasing build number for Sparkle).

## 7. Non-goals (v1)

- Not a dashboard replacement: no knowledge graph, no ask/capture UI, no review *resolution* (badge + deep link only — resolution stays on the dashboard's Review tab where the full context lives).
- No Mac App Store build; no iOS/iPadOS remote; no multi-machine/remote daemon control; no Windows/Linux tray port (2.0 P8 territory).
- No direct `launchctl`/plist manipulation; no config.yaml editing; no secret handling.

## 8. Acceptance gate

On a clean macOS 26 machine with nothing installed: download Companion.app → open (Gatekeeper clean) → onboarding walks to a green doctor and a running daemon without opening Terminal for anything Companion could reasonably guide (P1 gate compatible) → menu bar shows running state; killing the daemon flips the icon within 5 s; Start brings it back → Insights charts match the dashboard's numbers for the same ranges → toggling an automation off in Settings is visible in `axon automations` output → Doctor reproduces `axon doctor` verbatim including remediation text → VoiceOver can operate the entire popover → `spctl -a -vv` and notarisation pass.

## 9. Risks

| Risk | Mitigation |
|---|---|
| Scope creep toward a second dashboard | Non-goals section; review resolution and knowledge surfaces stay web; popover budget: glanceable in 2 s |
| Daemon/Companion version skew breaks decoding | CONTRACT.md + tolerant decoding + CFR-82 degradation rule; CI decodes captured payloads from both current and previous daemon release |
| Liquid Glass API churn in macOS 27 | Single `Glass.swift` seam; beta-SDK CI lane; charts deliberately on plain materials |
| Sparkle + hardened runtime + notarisation friction | Settled indie path (2.0 P3 already assumes Sparkle); packaging scripts from the proven SPM template |
| SPM-only (no Xcode project) surprises agents/tooling | `compile_and_run.sh` dev loop; plan's tasks carry exact build/test commands; Xcode can open the package directly when the owner wants previews |

## 10. Open questions (owner)

1. **Name & branding:** "Axon Companion" with display name "Axon" — or a distinct name to avoid confusion with the CLI in Spotlight?
2. **Popover quick-glance chart:** tokens sparkline (plan) vs runs-health strip — which earns the one slot?
3. **Free vs Pro:** 2.0 P4 gates "signed app + updates" as Pro. Does Companion ship free during 1.x/beta and become Pro-gated at 2.0, or Pro-only from the start? (Plan assumes: free while unsigned-dev, Pro at 2.0 — matching the PRD's no-rug-pull boundary since Companion is new convenience.)
4. **Menu bar icon:** `brain` SF Symbol (plan) vs a custom template mark derived from the AXON logotype?

<!-- axon:links:start -->
<!-- axon:links:end -->
