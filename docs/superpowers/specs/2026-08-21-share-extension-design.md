# macOS share extension — the system Share sheet as a capture path (design)

**Status:** approved 2026-08-21 (all three decisions Jandro-picked-recommended:
**spike the appex before speccing**; **URL + title + text only**, no files;
a **compose panel**, not fire-and-forget).
**CFR-96…CFR-99. No FR, no ADR, no migration.** Graduates `docs/20` **E2**.

**Zero daemon change.** No new endpoint, no contract change, no schema change,
no new automation. The extension is a fourth caller of the capture endpoint
ADR-024 already built and CFR-95 already uses.

Ships as **companion-v0.3.0**.

## The idea

Capture into `00-Inbox` has three doors today: dropping a file in the folder,
the browser bookmarklet through the served `/capture` page (ADR-024), and
`CaptureThoughtIntent` via Siri/Shortcuts (CFR-95). E1 added a fourth for
files (watch-folders). The one still missing is the door macOS itself offers
from every app: the Share sheet.

The shape is entirely client-side. `POST /api/capture` accepts
`{url, title, text}`, is guarded by loopback + `Host` + `X-Axon-Capture: 1` +
JSON content type, is gated on `dashboard.capture_enabled`, writes
`00-Inbox/capture-<stamp>.md`, publishes `capture.received`, and spends no
tokens. A share extension that POSTs that payload inherits every guarantee —
including the work-profile kill switch, which needs no client logic at all
because a disabled profile answers `404`.

**Why no ADR.** ADR-040 was written because watch-folders moved an ingress
boundary — the daemon began reading host directories outside the vault. This
slice moves nothing: same endpoint, same guard, same origin, same
loopback-only exposure, and the payload originates from a user action in an
app the user is already looking at. The only new surface is a bundle inside
the Companion, which is a packaging fact, not an architectural one.

## The feasibility spike (run before this spec, findings folded in)

The appex build path was unproven in this repo — `package_app.sh` hand-assembles
the `.app` from SwiftPM products with no Xcode project, and the container app
is deliberately **not** sandboxed. A throwaway probe (macOS 27, Swift 6.4,
since discarded; recipe retained here) established all four unknowns:

1. **SwiftPM can build an appex executable.** A plain `executableTarget` with
   no `main.swift`, plus
   `linkerSettings: [.unsafeFlags(["-Xlinker", "-e", "-Xlinker", "_NSExtensionMain"])]`.
   The obvious `.unsafeFlags(["-e", "_NSExtensionMain"])` **fails** — swiftc
   reads a bare `-e` as "evaluate expression" and tries to compile the symbol
   name as source. `unsafeFlags` is legal here because `Companion` is a root
   package, never a dependency.
2. **A non-sandboxed app can host a sandboxed appex.** Signing the appex with
   `app-sandbox` + `network.client` inside the unsandboxed `Axon.app` passes
   `codesign --verify --deep --strict`.
3. **It reaches the Share menu.** Once registered, "Axon" is returned by
   `NSSharingService.sharingServices(forItems:)` for a URL item — the same
   enumeration the Share menu renders.
4. **The sandbox does not block loopback.** Performing the service launched
   the extension, ran its principal view controller, and the sandboxed process
   POSTed to a loopback listener with the guard header, which recorded the hit.

And one finding that changed the scope: `pluginkit` listed the extension
registered but **not enabled** until `pluginkit -e use`. Enablement is a user
setting we do not control, so CFR-99 exists to make its state visible rather
than let the feature look broken.

## CFR-96 — The extension bundle

A new SwiftPM target `AxonShare` (executable → `.appex`), depending on
`AxonKit`. It is a view and nothing else; see CFR-97 for where the logic lives.

**Bundle:** `Contents/PlugIns/AxonShare.appex`, `CFBundleIdentifier`
`com.axon.companion.share`, `CFBundlePackageType` `XPC!`, version and build
number inherited from `version.env` so the appex never disagrees with its host.

**Extension point:** `com.apple.share-services`, principal class
`AxonShare.ShareViewController`, activation rule:

```
NSExtensionActivationSupportsWebURLWithMaxCount   1
NSExtensionActivationSupportsWebPageWithMaxCount  1
NSExtensionActivationSupportsText                 true
```

No file, image, movie, or attachment rules — that is the scope decision made
explicit in the Info.plist, not just in prose. A shared *file* has a door
already: E1 watch-folders.

**Entitlements:** a committed `AxonShare.entitlements` with
`com.apple.security.app-sandbox` and `com.apple.security.network.client`, and
nothing else. App extensions must be sandboxed; the container app must not be
(Sparkle's helper tools). `ENTITLEMENTS.md` gains a section saying exactly
that, because a future reader will otherwise "fix" the inconsistency.

**Packaging:** `Scripts/package_app.sh` assembles the appex (binary via the
existing `install_binary`, Info.plist from a heredoc beside the app's own) and
signs it **before** the outer app, with its own entitlements — nested code
first, container second, or the seal is invalid. `sign-and-notarize.sh` and
the Sparkle flow need no change: notarisation covers nested code, and the
hardened-runtime/timestamp flags already apply per-`codesign` call.

## CFR-97 — Payload extraction and the compose panel

**Extraction lives in `AxonKit`, not in the appex.** `ShareExtraction` is a
pure async function `[NSExtensionItem] -> SharePayload(url, title, text)`:

- **URL** — the first attachment conforming to `public.url`.
- **Title** — the item's `attributedTitle` (Safari sends the page title), else
  empty; the daemon renders `# Captured note` when it is.
- **Text** — the item's `attributedContentText` (the selection), else the
  first `public.plain-text` attachment.
- A payload with all three empty is refused client-side with the same words
  the daemon uses (`nothing to capture`) rather than a round trip.

This is the whole reason the appex target stays thin: `NSExtensionItem` and
`NSItemProvider` are constructible in tests, so extraction is unit-tested in
`AxonKitTests` while the appex holds only view code, which is not.

**The wire call widens.** `DashboardClient.capture(text:)` today posts only
`text`, dropping URL and title — harmless for CFR-95 (a spoken thought has
neither), wrong for a shared web page, where the URL on its own first line is
what makes the capture automation fetch and enrich it. It becomes
`capture(url:title:text:)`, omitting empty fields from the JSON body, with the
Intent updated to call it with text only. One call site shape, two callers.

**The panel** is a SwiftUI view in an `NSHostingController`:

- The detected title and URL, read-only — the user is confirming what macOS
  handed over, not editing it.
- One editable note field, pre-filled with the shared selection, focused on
  appear, so annotating at capture time costs nothing.
- `Capture` (default, ⏎) and `Cancel` (⎋).

Plain SwiftUI, no Liquid Glass: `Support/Glass.swift` lives in the `Companion`
executable target and is not shareable without moving UI code into `AxonKit`,
which its "no SwiftUI import" rule forbids. A share panel is system chrome
with a small custom body; matching the system is the correct look here.

## CFR-98 — Failure is shown, never swallowed

The panel stays open and the extension request stays uncompleted until the
capture actually succeeds or the user cancels. `completeRequest` on success;
`cancelRequest(withError:)` never — a cancel is a user decision, not an error.

| Condition | What the panel says |
| --- | --- |
| Daemon unreachable | "Axon isn't running. Open Axon Companion to start it." |
| `404` (capture disabled, or no vault) | "Capture is switched off for this profile." |
| `403` (guard rejected) | "Axon refused the capture." — a bug report, not a user error |
| Other non-2xx / transport error | The status or error, verbatim |

`Capture` remains enabled after a failure, so a user who starts the daemon can
retry without re-sharing. The mapping reuses `DashboardError`, so it stays in
step with the rest of the app.

**Known limitation, recorded not hidden:** the extension talks to
`127.0.0.1:7777`, the `DashboardClient` default. A profile with a custom
`dashboard.port` is unreachable from the extension. This is the exact
limitation CFR-92…95 already ship with (an App Intent process has no config
access either); fixing it needs a shared container or app group and is
deliberately out of this slice. The panel's "Axon isn't running" message is
what a custom-port user sees.

## CFR-99 — Making enablement visible

A new share extension can be registered and still absent from the Share menu
until the user enables it in **System Settings → Extensions → Sharing**. The
spike hit exactly this. Without a signpost, the feature's failure mode is
"nothing happened, and nothing said why".

Companion Settings gains one row — **Share extension** — showing:

- **Enabled** — `pluginkit -m -i com.axon.companion.share` reports it present
  and enabled (a leading `+` in `pluginkit -mAvvv`).
- **Not enabled** — registered but off, with a button opening
  `x-apple.systempreferences:com.apple.ExtensionsPreferences`.
- **Not registered** — the app has not been launched from `/Applications`
  (the dev-build case), with that sentence as the explanation.

`AxonKit`'s `CLIRunning`/`ProcessCLIRunner` seam already takes an arbitrary
binary URL, so `/usr/bin/pluginkit` runs through it and a fake runner makes
the parsing testable — this is a Settings row, not a new subsystem. It reads
state; it never enables the extension on the user's behalf.

## Out of scope

- **Files, images, PDFs.** No file activation rules. The door is E1
  watch-folders; a file-capable extension needs either vault-path knowledge in
  a sandboxed appex or a new multipart daemon endpoint, and both break "zero
  new daemon surface".
- **Port discovery.** See CFR-98's limitation.
- **A Services-menu (`NSServices`) entry.** The Share sheet is what E2 asks
  for; a second entry point with the same payload is duplication.
- **Selecting a destination folder or tags at capture time.** The inbox is the
  contract; the capture automation decides what a capture becomes.
- **Multi-URL shares.** `MaxCount 1`, matching what the endpoint stores.

## Verification

**Unit — `AxonKitTests/ShareExtractionTests`:** a Safari-shaped item (URL
attachment + `attributedTitle` + selection) yields all three fields; a
plain-text-only item yields text alone; a URL-only item yields the URL with an
empty title; an item with an empty selection string yields no text (not `""`);
an empty item is refused as nothing-to-capture.

**Unit — `AxonKitTests/DashboardClientTests`:** `capture(url:title:text:)`
posts to `/api/capture` with `X-Axon-Capture: 1` and JSON content type;
empty fields are omitted from the body rather than sent as `""`; a `404` maps
to the capture-disabled case and a transport failure to `unreachable`.

**Unit — the error mapping** feeding the panel, table-driven over
`DashboardError`, so a new case cannot silently render as blank.

**Unit — the CFR-99 state parse**, over a fake `CLIRunning`: `pluginkit`
output with a leading `+` reads as enabled, without it as registered-but-off,
empty output as not-registered, and a non-zero exit as unknown (the row hides
rather than lying).

**Build gate:** `swift build` produces `AxonShare` with `_NSExtensionMain` as
its entry point (`otool -l | grep -A2 LC_MAIN` is the spike's check;
`nm -m` showing the undefined `_NSExtensionMain` import is the readable one),
and `package_app.sh` output passes `codesign --verify --deep --strict`.

**Manual QA (`QA.md`), because extension launch is a registration property no
unit test reaches:** install to `/Applications`, launch once, confirm **Axon**
in Safari's Share menu; share a page with a selection and confirm the panel
pre-fills title/URL/note; capture and confirm the note lands in `00-Inbox`
with the URL on its own first line and `capture.received` on the dashboard
event stream; stop the daemon and confirm the "isn't running" message with a
working retry; set `dashboard.capture_enabled: false` and confirm the
switched-off message; disable the extension in System Settings and confirm the
Settings row reflects it.

**Live smoke** in an isolated env (scratch `AXON_HOME`, dashboard port 7799 —
**never 7777**) is *not* applicable to the extension itself, which hardcodes
7777: the smoke here is the real personal daemon, and the capture note is a
real note in `00-Inbox` (harmless — it is exactly what the feature is for).

## Docs to update on completion

`docs/18-component-companion.md` (the CFR range grows past CFR-91; a line on
the share extension), `docs/Axon Companion — PRD.md` (a `0.3.0 addendum —
share extension (CFR-96…99)` section mirroring the 0.2.0 one),
`apps/companion/ENTITLEMENTS.md` (why the appex is sandboxed and the app is
not), `apps/companion/QA.md` (the manual cases above),
`apps/companion/CONTRACT.md` (no API change — note only that the share
extension is a fourth caller of `POST /api/capture`), `docs/20` E2 (shipped),
`docs/GUIDE.md` (share-sheet capture in the capture section), and
`CHANGELOG.md` `[Unreleased]`.
