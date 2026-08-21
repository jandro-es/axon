# 18 — Component: Axon Companion (macOS menu-bar app)

> **Status: shipped 2026-08-16 as `companion-v0.1.0`** — signed (Developer ID),
> notarised, stapled, hardened runtime, Sparkle auto-updates via the signed
> appcast at `apps/companion/appcast/companion-appcast.xml`. This doc is a
> pointer, not a spec: the product definition lives in
> `docs/Axon Companion — PRD.md`, the frozen API contract in
> `apps/companion/CONTRACT.md`, and the QA state in `apps/companion/QA.md`.

## What it is

A SwiftPM-built (no Xcode project) macOS menu-bar app in `apps/companion/`,
three targets:

- **`AxonKit`** — models, dashboard REST client, SSE client, CLI wrapper, and
  the `DaemonController` state machine.
- **`AxonShare`** — the share extension's appex (CFR-96…99, 0.3.0): views
  only, its logic in `AxonKit/Share/`. Sandboxed inside the unsandboxed app;
  `package_app.sh` assembles and signs it into `Contents/PlugIns/`.
- **`Companion`** — MenuBar presence, Insights (Swift Charts), Settings,
  Doctor, Onboarding, and the Liquid-Glass support layer (`Glass.swift`, whose
  rules the web dashboard now mirrors — ADR-037).

**Architectural stance: zero business logic.** The Companion only reads the
daemon's REST/SSE surfaces and drives the `axon` CLI. When it needs something
the daemon cannot answer, the daemon grows a *general* seam (FR-184…FR-188 all
exist because of this rule) — the client never works around the daemon, and no
seam is a Companion special case. It is strictly optional: the web dashboard
remains the product UI, and the daemon is fully operable without the app.

## Contract and numbering

- `apps/companion/CONTRACT.md` freezes the daemon API surface the app consumes
  (with fixtures enforced as a CI regression net). Treat a contract change as a
  daemon API change, not an app detail.
- The Companion carries its own requirement namespace, **CFR-01…CFR-99**, in
  its PRD. This is deliberate: CFRs bind the *client*, FRs bind the *daemon*.
  Where a Companion need produced daemon behaviour, that behaviour has an FR
  (FR-184…FR-188) — CFRs are never cited as daemon requirements.

## Build & release

`make companion`, `companion-test`, `companion-release`, `companion-appcast`;
CI runs a `companion` job plus `companion-beta-sdk` (fails on any deprecation
warning). Version pinned in `apps/companion/version.env`
(`MARKETING_VERSION=0.1.0`, `BUILD_NUMBER=1`). Requires the Swift 6.2
toolchain; targets macOS 26+.

## Known QA debt (tracked in `docs/ISSUES.md`)

Verified on macOS 27 only, never on the macOS 26 floor it declares; UI
automation cannot reach `MenuBarExtra(.window)` popovers, so glass rendering,
keyboard traversal, VoiceOver, Reduce-Transparency fallback and WCAG contrast
are human-verification items; a Sparkle update has never been exercised
end-to-end. See `apps/companion/QA.md` for the full list.
