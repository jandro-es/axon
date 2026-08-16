# Axon Companion — QA record

**Executed 2026-08-16** against Companion `0.1.0 (1)` (commit `6a3e917`) on
macOS **27.0** (build 26A5406e), Xcode 27 / Swift 6.4, Apple Silicon.

Status key: **PASS** verified · **BLOCKED** could not be verified here, with the
reason · **DEFERRED** needs a machine state this one is not in.

---

## 0. Environment caveat, stated up front

The PRD targets **macOS 26** and CFR-90 asks for a build against the macOS 26
SDK. This machine runs **macOS 27** with the 27 SDK. The deployment target is
`.macOS("26.0")` and `LSMinimumSystemVersion` is `26.0`, so the *floor* is
correct and the binary runs on 26 — but nothing here was executed on macOS 26
itself. **Every result below is a macOS 27 result.**

That inverts CFR-91's intent in a useful way: the beta-SDK lane exists to catch
macOS 27 problems early, and this whole pass effectively ran on it. Zero
deprecation warnings on the 27 SDK is a stronger signal than the same result
on 26 would have been. It does not substitute for a 26 run before shipping to
users on 26.

---

## 1. Distribution and Gatekeeper (CFR-80)

| Check | Result |
|---|---|
| `spctl -a -vv dist/Axon.app` | **PASS** — `accepted`, `source=Notarized Developer ID` |
| Notarisation | **PASS** — submission `881ded88-7f09-431f-8a53-1b5928a4b0a0`, status `Accepted` |
| Ticket stapled | **PASS** — `stapler validate` → "The validate action worked!" |
| Signing identity | **PASS** — `Developer ID Application: Filtercode LTD (5R59WRDGLW)` |
| Hardened runtime | **PASS** — CodeDirectory `flags=0x10000(runtime)` |
| Secure timestamp | **PASS** — present |
| Universal binary | **PASS** — `lipo -archs` → `x86_64 arm64` |
| Entitlements as signed | **PASS** — exactly one: `cs.disable-library-validation` (Sparkle; see `ENTITLEMENTS.md`) |
| `LSUIElement` | **PASS** — `true`; no Dock icon, no menu bar app menu |
| Bundle id | **PASS** — `com.axon.companion` |

Launched from the notarised bundle and confirmed running.

---

## 2. Status and presence (CFR-01, CFR-02)

| Check | Result |
|---|---|
| Icon reflects running state | **PASS** — accessibility name `AXON — update available`, derived from `/health.update_available` |
| Icon flips on daemon death ≤5 s | **PASS** — `launchctl bootout` → `AXON — stopped` within the poll interval; verified twice |
| Icon recovers on daemon start | **PASS** — `launchctl bootstrap` → back to `AXON — update available` |
| Update availability surfaced | **PASS** — drives the attention badge |
| Uptime pill | **DEFERRED** — the installed daemon predates the `started_at` seam this branch added, so the pill correctly hides itself. Re-check after the daemon binary is updated. |

---

## 3. Popover (CFR-02, CFR-10, CFR-12, CFR-20)

| Check | Result |
|---|---|
| Popover renders live data | **BLOCKED** — see §8 |
| Start/Stop/Restart from popover | **BLOCKED** for UI-driven; the underlying paths are unit-tested (`startWalksStoppedThroughStartingToRunning`, `startIsSpawnedDetachedRatherThanAwaited`) |
| Stop confirmation names consequences | **PASS by inspection** — `QuickActions.swift` copy states runs are cut short and that launchd will relaunch if the login service is installed |
| Deep links resolve | **PASS** — `#review`, `#actions`, `#tokens` all HTTP 200 against the live dashboard |
| Quit never stops the daemon | **PASS by inspection** — `NSApplication.terminate` only; no lifecycle call on that path |

---

## 4. Insights (CFR-30, CFR-31, CFR-32)

| Check | Result |
|---|---|
| Charts match the dashboard for the same range | **PASS at the data layer** — `DashboardAgreementTests` pins aggregation against `web/src/App.jsx`: totals are `input + output` with cache excluded, and labels mirror `shortOp`. This caught a real mismatch (`ingest.` → `ingest:`). Visual side-by-side is **BLOCKED** (§8). |
| Export uses `/api/export` | **PASS by inspection** — no local serialisation; the menu states it exports the daemon's window, not the picker's |
| Charts on opaque cards, never glass | **PASS** — `axonCard()` throughout `Insights/`; no `axonGlass` in any chart |
| VoiceOver labels on every series | **PASS by inspection** — `.accessibilityLabel` on each `Chart`, each gauge, and each stat tile |

---

## 5. Settings (CFR-40, CFR-41, CFR-42)

| Check | Result |
|---|---|
| Budget change reaches the daemon | **PASS** — live round trip: set `limits.daily_tokens` to 1234567, `axon config get` reflected it, original 1500000 restored |
| Automation toggle agrees with `axon automations` | **PASS at the CLI layer** — argv asserted (`config set automations.<name>.enabled`), and the store re-reads rather than trusting the write |
| Policy-forbidden automations are not togglable | **PASS** — `isTogglable` false when `allowed == false`; the row disables with a reason |
| Dangerous settings absent | **PASS** — no egress/redaction/profile editing anywhere in `Settings/` |
| Two login toggles kept distinct | **PASS** — `SMAppService` for Companion, `axon service install/uninstall` for the daemon; separate copy for each |

---

## 6. Doctor (CFR-60, CFR-61)

| Check | Result |
|---|---|
| Reproduces `axon doctor` including remediation | **PASS at the model layer** — checks render `detail` verbatim, which *is* the daemon's remediation text |
| Works with the daemon stopped | **PASS** — health failure does not block the report |
| Older daemon degrades usefully | **PASS** — verified against this machine's own installed daemon, which predates `doctor --json`: shows an actionable message instead of `unknown flag: --json` |
| Diagnostics redacted | **PASS** — planted `sk-`, OAuth and JWT secrets removed; home directory rewritten to `~`; commit hashes and ordinary check text survive |
| Bundle contains no vault content | **PASS by construction** — sources are versions, `doctor`, and a re-encoded `/health` |

---

## 7. Onboarding and notifications (CFR-50…52, CFR-70, CFR-71)

| Check | Result |
|---|---|
| Real prerequisite detection | **PASS** — all four probes resolve correctly here (axon, claude, Ollama :11434, daemon :7777) |
| Companion never installs anything | **PASS** — asserted by test as well as by inspection |
| Unexpected stop notifies | **PASS** — stopped the daemon outside the app; banner read "AXON stopped unexpectedly — Scheduled automations won't run until it's started again." |
| User-initiated stop is silent | **PASS at the router layer** — `aUserInitiatedStopIsSilent`; UI-driven Stop is **BLOCKED** (§8) |
| Success never notified | **PASS** — test covers every success-shaped kind |
| First-run wizard on a clean machine | **DEFERRED** — needs a machine without AXON; the flow is covered by model tests driving scripted machine states |

---

## 8. What could not be verified here, and why

**The `MenuBarExtra` popover cannot be driven by UI automation.** A menu bar
popover dismisses as soon as another process takes keyboard focus, which is
exactly what both `osascript` and `screencapture` do. The window is also not
exposed as an `AXWindow` while unfocused, so the accessibility tree cannot be
walked either. Attempts: AppleScript click + `entire contents`, click + screen
capture, click + synthetic Return. All either dismissed the popover or returned
zero windows.

Verified instead, at the layers that *are* reachable:

- the menu bar item's own accessibility name, live, across state changes;
- every model and formatting rule behind the popover, by unit test;
- the data paths, against the live daemon.

**Not verified, and needing a human at the keyboard:**

1. Popover layout, glass rendering, and the <2 s glance budget.
2. Full keyboard traversal and Esc-to-close.
3. VoiceOver actually speaking the popover, Insights and Doctor.
4. Reduce Transparency producing the solid fallback (the code path exists and
   is the only branch in `Glass.swift`, but its appearance is unverified).
5. Light/Dark appearance switches.
6. Contrast ≥ WCAG AA over light and dark desktops.
7. A Sparkle update actually installing — the appcast is generated and signed,
   and the guard rejecting a stale build number was rehearsed, but no install
   has consumed a feed.
8. First run on a clean macOS 26 account.

---

## 9. Watch-items for macOS 27 and beyond

- **Glass API surface.** All of it is in `Support/Glass.swift`. If macOS 27
  changes `glassEffect` or `GlassEffectContainer`, that file is the whole fix.
- **`x86_64` deprecation.** The 27 SDK warns that x86_64 is deprecated for a
  macOS 27 deployment target. Companion targets 26 and still ships universal;
  when the floor moves to 27, drop `x86_64` from `ARCHES` and the warning with
  it.
- **`MenuBarExtra(.window)` focus behaviour.** The dismissal that blocks
  automation is also what makes the popover feel native. If a future macOS
  changes it, §8 may become testable.
- **Swift Testing parallelism.** The suites sharing a URL-protocol stub
  deadlocked until each got its own store. Any new suite that stubs the
  network must not reuse another's.

---

## 10. Test suite

```
200 tests in 17 suites — passed in 0.31s
swift build -c release — 0 warnings
```

Go side unaffected: `go build ./...`, `go vet ./...`, `cmd/axon` and
`internal/dashboard` tests all green.
