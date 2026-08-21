# macOS Share Extension Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Put "Axon" in the macOS Share sheet so a page, a link or a selection from any app lands in `00-Inbox` through the daemon's existing capture endpoint.

**Architecture:** A new SwiftPM target `AxonShare` builds an `.appex` that `package_app.sh` assembles into `Axon.app/Contents/PlugIns/` and signs — sandboxed — before the outer app. The appex holds view code only; extraction, the wire call, the failure copy and the enablement probe all live in `AxonKit`, where they are unit-tested. No daemon change: the extension is a fourth caller of `POST /api/capture` (ADR-024).

**Tech Stack:** Swift 6.2 (toolchain 6.4), SwiftPM (no Xcode project), AppKit + SwiftUI + Observation, Swift Testing (`@Test`/`#expect`), `codesign`/`pluginkit`.

**Spec:** `docs/superpowers/specs/2026-08-21-share-extension-design.md` — read it before Task 1; every task below cites the CFR it implements.

## Global Constraints

- **Platform floor:** macOS 26.0 (`Package.swift` `.macOS("26.0")`, `MACOS_MIN_VERSION=26.0`). No macOS-27-only symbols.
- **Zero business logic in the app targets** (`docs/18`): if the daemon can't answer something, the daemon grows a seam. This slice adds **no** daemon surface — if you find yourself wanting one, stop and escalate.
- **`AxonKit` imports no SwiftUI and takes no package dependencies.** AppKit/Foundation/UniformTypeIdentifiers are fine; `Package.swift` keeps Sparkle scoped to the `Companion` target only.
- **Daemon base URL is `http://127.0.0.1:7777`** (`DashboardClient.defaultBaseURL`). The extension does not read config; a custom `dashboard.port` is a documented limitation, not a bug to fix here.
- **Bundle IDs:** app `com.axon.companion`, extension `com.axon.companion.share`.
- **Test framework is Swift Testing**, not XCTest: `@Suite`, `@Test`, `#expect`, `#require`, `await #expect(throws:)`. Suites that use a URL stub declare **their own** `StubURLProtocol` subclass (see `Tests/AxonKitTests/StubTransport.swift` — a shared store deadlocked two suites once).
- **Copy is fixed by the spec.** Use the exact user-facing strings given in Task 3 and Task 7; they are CFR-98/CFR-99 requirements, not suggestions.
- **Run tests with:** `cd apps/companion && swift test` (or `make companion-test` from the repo root).
- **Commit per task**, message prefixed `feat(companion):` / `test(companion):` / `docs:`, ending with the repo's `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>` trailer.

---

## File Structure

**Created:**
- `apps/companion/Sources/AxonKit/Share/SharePayload.swift` — the three-field value a share reduces to (CFR-97).
- `apps/companion/Sources/AxonKit/Share/ShareExtraction.swift` — `[NSExtensionItem] → SharePayload` (CFR-97).
- `apps/companion/Sources/AxonKit/Share/ShareCaptureMessage.swift` — `Error → user-facing sentence` (CFR-98).
- `apps/companion/Sources/AxonKit/Share/ShareExtensionProbe.swift` — `pluginkit` state read (CFR-99).
- `apps/companion/Sources/AxonShare/ShareViewController.swift` — appex principal class (CFR-96).
- `apps/companion/Sources/AxonShare/SharePanelModel.swift` — panel state machine (CFR-97/98).
- `apps/companion/Sources/AxonShare/SharePanel.swift` — the SwiftUI panel (CFR-97).
- `apps/companion/AxonShare.entitlements` — sandbox + network client (CFR-96).
- `apps/companion/Tests/AxonKitTests/ShareExtractionTests.swift`
- `apps/companion/Tests/AxonKitTests/ShareExtensionProbeTests.swift`

**Modified:**
- `apps/companion/Package.swift` — the `AxonShare` target.
- `apps/companion/Sources/AxonKit/Client/DashboardClient.swift:112` — `capture(text:)` widens to `capture(url:title:text:)`.
- `apps/companion/Sources/AxonKit/Control/OpenAction.swift` — `.extensionsSettings` case.
- `apps/companion/Scripts/package_app.sh` — appex assembly + signing.
- `apps/companion/Sources/Companion/Settings/GeneralPane.swift` — the CFR-99 row.
- `apps/companion/Tests/AxonKitTests/QueryClientTests.swift`, `OpenActionsTests.swift` — new cases.
- Docs listed in Task 9.

---

### Task 1: Widen the capture call (CFR-97)

Today `capture(text:)` posts only `text`, dropping URL and title. A shared web page needs the URL on its own first line — that is what makes the capture automation fetch and enrich it. Default arguments keep the existing `capture(text:)` call in `AxonIntents.swift` compiling untouched.

**Files:**
- Modify: `apps/companion/Sources/AxonKit/Client/DashboardClient.swift:110-118`
- Test: `apps/companion/Tests/AxonKitTests/QueryClientTests.swift`

**Interfaces:**
- Consumes: `postIgnoringBody(path:body:headers:)` (private, already in the file).
- Produces: `public func capture(url: String = "", title: String = "", text: String = "") async throws` on `DashboardClient`.

- [ ] **Step 1: Write the failing test**

Add to `QueryClientTests` (after `capturePostsTextAndIgnoresResponseBody`):

```swift
    @Test func capturePostsURLTitleAndTextAndOmitsEmptyFields() async throws {
        stub.reset()
        stub.routes["/api/capture"] = .init(body: Data("{}".utf8))

        try await makeClient().capture(
            url: "https://example.com/a", title: "A page", text: "the bit I selected")

        let request = try #require(stub.recorded.first)
        #expect(request.httpMethod == "POST")
        #expect(request.value(forHTTPHeaderField: "X-Axon-Capture") == "1")
        #expect(request.value(forHTTPHeaderField: "Content-Type") == "application/json")
        let body = try #require(request.axonBodyData)
        let sent = try #require(try JSONSerialization.jsonObject(with: body) as? [String: String])
        #expect(sent["url"] == "https://example.com/a")
        #expect(sent["title"] == "A page")
        #expect(sent["text"] == "the bit I selected")
    }

    @Test func captureOmitsEmptyFieldsRatherThanSendingBlanks() async throws {
        stub.reset()
        stub.routes["/api/capture"] = .init(body: Data("{}".utf8))

        try await makeClient().capture(url: "https://example.com/a")

        let body = try #require(stub.recorded.first?.axonBodyData)
        let sent = try #require(try JSONSerialization.jsonObject(with: body) as? [String: String])
        #expect(sent["url"] == "https://example.com/a")
        // An empty title would make the daemon write a blank H1 instead of
        // falling back to "Captured note".
        #expect(sent["title"] == nil)
        #expect(sent["text"] == nil)
    }
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd apps/companion && swift test --filter QueryClientTests`
Expected: FAIL — `extra argument 'url' in call` (compile error).

- [ ] **Step 3: Write the implementation**

Replace the existing `capture(text:)` in `DashboardClient.swift` with:

```swift
    /// Non-destructive inbox capture (ADR-024): creates a note under
    /// `00-Inbox/`, never edits anything. Zero model spend.
    ///
    /// Empty fields are omitted rather than sent as `""`: the daemon renders
    /// `# Captured note` when it receives no title, and writes the URL on its
    /// own first line — which is what makes the capture automation fetch it.
    /// The default arguments keep the spoken-thought caller (CFR-95) reading
    /// as `capture(text:)`.
    public func capture(url: String = "", title: String = "", text: String = "") async throws {
        var body: [String: String] = [:]
        if !url.isEmpty { body["url"] = url }
        if !title.isEmpty { body["title"] = title }
        if !text.isEmpty { body["text"] = text }
        try await postIgnoringBody(
            path: "/api/capture",
            body: body,
            headers: ["X-Axon-Capture": "1"]
        )
    }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd apps/companion && swift test --filter QueryClientTests`
Expected: PASS — all capture tests, including the pre-existing `capturePostsTextAndIgnoresResponseBody`, which must keep passing unchanged.

- [ ] **Step 5: Commit**

```bash
git add apps/companion/Sources/AxonKit/Client/DashboardClient.swift \
        apps/companion/Tests/AxonKitTests/QueryClientTests.swift
git commit -m "feat(companion): capture(url:title:text:) — stop dropping the URL (CFR-97)"
```

---

### Task 2: Reduce shared items to a payload (CFR-97)

`NSExtensionItem` is testable — you can build one with an `NSItemProvider` in a unit test — so extraction belongs in `AxonKit`, leaving the appex with view code only.

**Files:**
- Create: `apps/companion/Sources/AxonKit/Share/SharePayload.swift`
- Create: `apps/companion/Sources/AxonKit/Share/ShareExtraction.swift`
- Test: `apps/companion/Tests/AxonKitTests/ShareExtractionTests.swift`

**Interfaces:**
- Produces: `public struct SharePayload: Equatable, Sendable { public var url, title, text: String; public var isEmpty: Bool }` and `public enum ShareExtraction { public static func payload(from items: [NSExtensionItem]) async -> SharePayload }`.

- [ ] **Step 1: Write the failing test**

Create `apps/companion/Tests/AxonKitTests/ShareExtractionTests.swift`:

```swift
import Foundation
import Testing
import UniformTypeIdentifiers

@testable import AxonKit

private func urlProvider(_ string: String) -> NSItemProvider {
    NSItemProvider(item: NSURL(string: string)!, typeIdentifier: UTType.url.identifier)
}

private func textProvider(_ string: String) -> NSItemProvider {
    NSItemProvider(item: string as NSString, typeIdentifier: UTType.plainText.identifier)
}

private func item(
    attachments: [NSItemProvider] = [],
    title: String? = nil,
    selection: String? = nil
) -> NSExtensionItem {
    let item = NSExtensionItem()
    if !attachments.isEmpty { item.attachments = attachments }
    if let title { item.attributedTitle = NSAttributedString(string: title) }
    if let selection { item.attributedContentText = NSAttributedString(string: selection) }
    return item
}

/// CFR-97 — what a macOS share reduces to.
@Suite
struct ShareExtractionTests {
    @Test func safariShapedShareCarriesURLTitleAndSelection() async {
        let payload = await ShareExtraction.payload(from: [
            item(attachments: [urlProvider("https://example.com/article")],
                 title: "An article",
                 selection: "the paragraph I highlighted")
        ])

        #expect(payload.url == "https://example.com/article")
        #expect(payload.title == "An article")
        #expect(payload.text == "the paragraph I highlighted")
        #expect(payload.isEmpty == false)
    }

    @Test func plainTextSelectionYieldsTextOnly() async {
        let payload = await ShareExtraction.payload(from: [
            item(attachments: [textProvider("a thought worth keeping")])
        ])

        #expect(payload.url.isEmpty)
        #expect(payload.title.isEmpty)
        #expect(payload.text == "a thought worth keeping")
    }

    @Test func bareURLYieldsURLWithEmptyTitle() async {
        let payload = await ShareExtraction.payload(from: [
            item(attachments: [urlProvider("https://example.com/a")])
        ])

        #expect(payload.url == "https://example.com/a")
        #expect(payload.title.isEmpty)
        #expect(payload.text.isEmpty)
    }

    @Test func whitespaceOnlySelectionIsNotText() async {
        let payload = await ShareExtraction.payload(from: [
            item(attachments: [urlProvider("https://example.com/a")], selection: "   \n  ")
        ])

        // "" not "   " — the daemon would otherwise write a blank body line.
        #expect(payload.text.isEmpty)
    }

    @Test func emptyShareIsRefusedBeforeAnyRoundTrip() async {
        let payload = await ShareExtraction.payload(from: [item()])
        #expect(payload.isEmpty)
    }

    @Test func noItemsIsEmpty() async {
        let payload = await ShareExtraction.payload(from: [])
        #expect(payload.isEmpty)
    }
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd apps/companion && swift test --filter ShareExtractionTests`
Expected: FAIL — `cannot find 'ShareExtraction' in scope`.

- [ ] **Step 3: Write the implementation**

Create `apps/companion/Sources/AxonKit/Share/SharePayload.swift`:

```swift
import Foundation

/// What a macOS share carries, reduced to the three fields
/// `POST /api/capture` stores (ADR-024).
///
/// Lives in AxonKit rather than the extension target so the reduction is unit
/// tested: `NSExtensionItem` and `NSItemProvider` are constructible in a test,
/// a running share extension is not.
public struct SharePayload: Equatable, Sendable {
    public var url: String
    public var title: String
    public var text: String

    public init(url: String = "", title: String = "", text: String = "") {
        self.url = url
        self.title = title
        self.text = text
    }

    /// The daemon answers an all-empty capture with 400; refusing here saves a
    /// pointless round trip and gives the panel something to disable on.
    public var isEmpty: Bool {
        url.isEmpty && title.isEmpty && text.isEmpty
    }
}
```

Create `apps/companion/Sources/AxonKit/Share/ShareExtraction.swift`:

```swift
import Foundation
import UniformTypeIdentifiers

/// Reduces the items macOS hands a share extension to a ``SharePayload``
/// (CFR-97).
///
/// Safari sends the page URL as an attachment, the page title as
/// `attributedTitle`, and any selection as `attributedContentText`. A text
/// selection from most other apps arrives as a plain-text attachment with no
/// title. Both shapes, and everything between, fold into the same three fields.
public enum ShareExtraction {
    public static func payload(from items: [NSExtensionItem]) async -> SharePayload {
        var payload = SharePayload()

        for item in items {
            if payload.title.isEmpty {
                payload.title = clean(item.attributedTitle?.string)
            }
            if payload.text.isEmpty {
                payload.text = clean(item.attributedContentText?.string)
            }

            for provider in item.attachments ?? [] {
                if payload.url.isEmpty,
                   provider.hasItemConformingToTypeIdentifier(UTType.url.identifier) {
                    payload.url = clean(await load(provider, as: UTType.url))
                }
                if payload.text.isEmpty,
                   provider.hasItemConformingToTypeIdentifier(UTType.plainText.identifier) {
                    payload.text = clean(await load(provider, as: UTType.plainText))
                }
            }
        }

        return payload
    }

    /// `loadItem` hands back whatever the sender boxed: a URL, a string, or
    /// raw bytes. Normalise all three rather than trusting one.
    private static func load(_ provider: NSItemProvider, as type: UTType) async -> String? {
        await withCheckedContinuation { continuation in
            provider.loadItem(forTypeIdentifier: type.identifier, options: nil) { item, _ in
                switch item {
                case let url as URL: continuation.resume(returning: url.absoluteString)
                case let string as String: continuation.resume(returning: string)
                case let data as Data: continuation.resume(returning: String(decoding: data, as: UTF8.self))
                default: continuation.resume(returning: nil)
                }
            }
        }
    }

    private static func clean(_ value: String?) -> String {
        value?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    }
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd apps/companion && swift test --filter ShareExtractionTests`
Expected: PASS — 6 tests.

- [ ] **Step 5: Commit**

```bash
git add apps/companion/Sources/AxonKit/Share apps/companion/Tests/AxonKitTests/ShareExtractionTests.swift
git commit -m "feat(companion): reduce shared items to a SharePayload (CFR-97)"
```

---

### Task 3: The failure copy (CFR-98)

Every failure is shown in the panel. The words are a spec requirement — a blank or generic message is the failure mode this task exists to prevent.

**Files:**
- Create: `apps/companion/Sources/AxonKit/Share/ShareCaptureMessage.swift`
- Test: `apps/companion/Tests/AxonKitTests/ShareExtractionTests.swift` (append a second suite)

**Interfaces:**
- Consumes: `DashboardError` (`.unreachable`, `.badStatus(Int)`, `.decoding(String)`).
- Produces: `public enum ShareCaptureMessage { public static func text(for error: Error) -> String }`.

- [ ] **Step 1: Write the failing test**

Append to `apps/companion/Tests/AxonKitTests/ShareExtractionTests.swift`:

```swift
/// CFR-98 — a failed capture always says something true and specific.
@Suite
struct ShareCaptureMessageTests {
    @Test func daemonDownReadsAsAState() {
        #expect(ShareCaptureMessage.text(for: DashboardError.unreachable)
            == "Axon isn't running. Open Axon Companion to start it.")
    }

    @Test func captureDisabledIsNotAnError() {
        #expect(ShareCaptureMessage.text(for: DashboardError.badStatus(404))
            == "Capture is switched off for this profile.")
    }

    @Test func guardRejectionIsCalledOut() {
        // 403 means the request guard refused us — a bug report, not a user error.
        #expect(ShareCaptureMessage.text(for: DashboardError.badStatus(403))
            == "Axon refused the capture.")
    }

    @Test func otherStatusesCarryTheirCode() {
        #expect(ShareCaptureMessage.text(for: DashboardError.badStatus(500))
            == "Axon answered 500.")
    }

    @Test func brokenContractIsDistinctFromBeingDown() {
        #expect(ShareCaptureMessage.text(for: DashboardError.decoding("nonsense"))
            == "Axon answered with something unreadable.")
    }

    @Test func nonDashboardErrorsStillSayWhatHappened() {
        let message = ShareCaptureMessage.text(for: URLError(.timedOut))
        #expect(message.hasPrefix("Axon couldn't capture that: "))
        #expect(message.count > "Axon couldn't capture that: ".count)
    }
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd apps/companion && swift test --filter ShareCaptureMessageTests`
Expected: FAIL — `cannot find 'ShareCaptureMessage' in scope`.

- [ ] **Step 3: Write the implementation**

Create `apps/companion/Sources/AxonKit/Share/ShareCaptureMessage.swift`:

```swift
import Foundation

/// The sentence the share panel shows when a capture fails (CFR-98).
///
/// In AxonKit, not the extension target, because the mapping is exhaustive
/// over `DashboardError` and a new case must not be able to render as blank.
public enum ShareCaptureMessage {
    public static func text(for error: Error) -> String {
        guard let error = error as? DashboardError else {
            return "Axon couldn't capture that: \(error.localizedDescription)"
        }
        switch error {
        case .unreachable:
            return "Axon isn't running. Open Axon Companion to start it."
        case .badStatus(404):
            // The profile switched capture off, or has no vault configured.
            return "Capture is switched off for this profile."
        case .badStatus(403):
            // The loopback guard refused us: not something the user can fix.
            return "Axon refused the capture."
        case .badStatus(let code):
            return "Axon answered \(code)."
        case .decoding:
            return "Axon answered with something unreadable."
        }
    }
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd apps/companion && swift test --filter ShareCaptureMessageTests`
Expected: PASS — 6 tests.

- [ ] **Step 5: Commit**

```bash
git add apps/companion/Sources/AxonKit/Share/ShareCaptureMessage.swift \
        apps/companion/Tests/AxonKitTests/ShareExtractionTests.swift
git commit -m "feat(companion): share capture failure copy (CFR-98)"
```

---

### Task 4: The appex target, panel and view controller (CFR-96 code half, CFR-97 panel, CFR-98 behaviour)

This is the task the spike exists for. The linker flag is the part that looks wrong and is right: a bare `-e _NSExtensionMain` makes swiftc treat `-e` as "evaluate expression" and try to compile the symbol name as source. It must be handed to the linker explicitly.

**Files:**
- Modify: `apps/companion/Package.swift`
- Create: `apps/companion/Sources/AxonShare/ShareViewController.swift`
- Create: `apps/companion/Sources/AxonShare/SharePanelModel.swift`
- Create: `apps/companion/Sources/AxonShare/SharePanel.swift`

**Interfaces:**
- Consumes: `SharePayload`, `ShareExtraction.payload(from:)`, `ShareCaptureMessage.text(for:)`, `DashboardClient.capture(url:title:text:)`.
- Produces: the `AxonShare` product and the class name `AxonShare.ShareViewController`, which Task 5's Info.plist names as `NSExtensionPrincipalClass`. **These two strings must match exactly.**

- [ ] **Step 1: Add the target**

In `apps/companion/Package.swift`, insert before `.testTarget(`:

```swift
        // The share extension's appex executable (CFR-96). Views only — every
        // testable part lives in AxonKit.
        //
        // The linker flag is load-bearing: an appex's entry point is
        // NSExtensionMain, not main(), which is why this target has no
        // main.swift. It MUST be spelled through -Xlinker; passing "-e" to
        // swiftc directly makes it evaluate the symbol name as Swift source
        // ("cannot find '_NSExtensionMain' in scope"). unsafeFlags is legal
        // because Companion is a root package, never a dependency.
        .executableTarget(
            name: "AxonShare",
            dependencies: ["AxonKit"],
            linkerSettings: [.unsafeFlags(["-Xlinker", "-e", "-Xlinker", "_NSExtensionMain"])]
        ),
```

- [ ] **Step 2: Write the panel's state machine**

Create `apps/companion/Sources/AxonShare/SharePanelModel.swift`:

```swift
import AxonKit
import Foundation
import Observation

/// The panel's whole behaviour (CFR-97/98), kept out of the view so the rules
/// read in one place: nothing is captured until the payload has loaded, a
/// failure never closes the panel, and the extension request completes only on
/// success or an explicit cancel.
@MainActor
@Observable
final class SharePanelModel {
    private(set) var payload = SharePayload()
    private(set) var loaded = false
    private(set) var busy = false
    private(set) var failure: String?
    /// The editable note. Starts as the shared selection.
    var note: String = ""

    private let capture: @Sendable (SharePayload) async throws -> Void
    private let finish: () -> Void

    init(
        capture: @escaping @Sendable (SharePayload) async throws -> Void,
        finish: @escaping () -> Void
    ) {
        self.capture = capture
        self.finish = finish
    }

    func load(_ payload: SharePayload) {
        self.payload = payload
        note = payload.text
        loaded = true
    }

    /// Nothing to send is not an error to show — it is a disabled button.
    var canCapture: Bool {
        guard loaded, !busy else { return false }
        return !outgoing.isEmpty
    }

    var outgoing: SharePayload {
        var outgoing = payload
        outgoing.text = note.trimmingCharacters(in: .whitespacesAndNewlines)
        return outgoing
    }

    func capturePressed() {
        guard canCapture else { return }
        busy = true
        failure = nil
        let outgoing = outgoing
        Task { [capture, finish] in
            do {
                try await capture(outgoing)
                finish()
            } catch {
                // CFR-98: stay open, say what happened, let them press again.
                failure = ShareCaptureMessage.text(for: error)
                busy = false
            }
        }
    }

    /// A cancel is a user decision, never `cancelRequest(withError:)`.
    func cancelPressed() {
        finish()
    }
}
```

- [ ] **Step 3: Write the panel view**

Create `apps/companion/Sources/AxonShare/SharePanel.swift`:

```swift
import SwiftUI

/// The compose panel (CFR-97). Deliberately plain SwiftUI: `Glass.swift` lives
/// in the Companion executable target and AxonKit may not import SwiftUI, and a
/// share panel is system chrome with a small custom body.
struct SharePanel: View {
    @Bindable var model: SharePanelModel
    @FocusState private var noteFocused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(model.payload.title.isEmpty ? "Capture to Axon" : model.payload.title)
                .font(.headline)
                .lineLimit(2)

            if !model.payload.url.isEmpty {
                Text(model.payload.url)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }

            TextEditor(text: $model.note)
                .font(.body)
                .scrollContentBackground(.hidden)
                .padding(4)
                .frame(minHeight: 96)
                .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(.separator))
                .focused($noteFocused)
                .accessibilityLabel("Note")

            if let failure = model.failure {
                Label(failure, systemImage: "exclamationmark.triangle")
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
            }

            HStack {
                Spacer()
                Button("Cancel") { model.cancelPressed() }
                    .keyboardShortcut(.cancelAction)
                Button(model.busy ? "Capturing…" : "Capture") { model.capturePressed() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(!model.canCapture)
            }
        }
        .padding(16)
        .frame(width: 420)
        .onAppear { noteFocused = true }
    }
}
```

- [ ] **Step 4: Write the principal class**

Create `apps/companion/Sources/AxonShare/ShareViewController.swift`:

```swift
import AppKit
import AxonKit
import SwiftUI

/// The share extension's principal class (CFR-96) — named verbatim in the
/// appex Info.plist as `AxonShare.ShareViewController`. Rename either and the
/// extension loads to a blank sheet.
///
/// It owns no logic: AxonKit reduces the shared items, the panel model runs the
/// rules, and this class only bridges the extension context.
final class ShareViewController: NSViewController {
    private var model: SharePanelModel?

    override func loadView() {
        let model = SharePanelModel(
            capture: { payload in
                try await DashboardClient().capture(
                    url: payload.url, title: payload.title, text: payload.text)
            },
            finish: { [weak self] in
                self?.extensionContext?.completeRequest(returningItems: [], completionHandler: nil)
            }
        )
        self.model = model

        let hosting = NSHostingController(rootView: SharePanel(model: model))
        addChild(hosting)
        view = hosting.view

        let items = (extensionContext?.inputItems as? [NSExtensionItem]) ?? []
        Task { @MainActor in
            model.load(await ShareExtraction.payload(from: items))
        }
    }
}
```

- [ ] **Step 5: Build and verify the entry point**

Run:

```bash
cd apps/companion && swift build -c release --arch arm64 --build-system swiftbuild --scratch-path .build/verify
nm -m .build/verify/out/Products/Release/AxonShare | grep NSExtensionMain
otool -l .build/verify/out/Products/Release/AxonShare | grep -A2 LC_MAIN
```

Expected: the build succeeds; `nm` prints `(undefined) external _NSExtensionMain (from Foundation)`; `LC_MAIN` is present. If the build fails with `cannot find '_NSExtensionMain' in scope`, the `-Xlinker` wrapping in Step 1 was lost.

- [ ] **Step 6: Run the whole suite (nothing regressed)**

Run: `cd apps/companion && swift test`
Expected: PASS — the new target must not break `AxonKit`'s build or any existing suite.

- [ ] **Step 7: Commit**

```bash
git add apps/companion/Package.swift apps/companion/Sources/AxonShare
git commit -m "feat(companion): the AxonShare appex — panel, model, principal class (CFR-96/97/98)"
```

---

### Task 5: Package and sign the appex (CFR-96)

Nested code is signed **before** its container or the seal is invalid. The appex is sandboxed while the app is not — that asymmetry is required (extensions must be sandboxed; the app can't be, because Sparkle launches separately-signed helpers), and it must be documented where the next reader will look.

**Files:**
- Create: `apps/companion/AxonShare.entitlements`
- Modify: `apps/companion/Scripts/package_app.sh` (insert before the `# Ensure contents are writable…` block, and again before the app's own `codesign`)
- Modify: `apps/companion/ENTITLEMENTS.md`

**Interfaces:**
- Consumes: `install_binary <product> <destination>` and the `CODESIGN_ARGS` array, both already defined in `package_app.sh`; the principal class name from Task 4.
- Produces: `dist/Axon.app/Contents/PlugIns/AxonShare.appex`, bundle ID `com.axon.companion.share`.

- [ ] **Step 1: Write the entitlements file**

Create `apps/companion/AxonShare.entitlements`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <!-- Rationale: ENTITLEMENTS.md. App extensions MUST be sandboxed, even
         though their container app here is not. -->
    <key>com.apple.security.app-sandbox</key>
    <true/>
    <!-- Outgoing connections only, and in practice only to 127.0.0.1:7777.
         Without this the sandboxed extension cannot reach the daemon at all. -->
    <key>com.apple.security.network.client</key>
    <true/>
</dict>
</plist>
```

- [ ] **Step 2: Assemble the appex in the packaging script**

In `apps/companion/Scripts/package_app.sh`, insert immediately **before** the line `# Ensure contents are writable before stripping attributes and signing.`:

```bash
# The share extension (CFR-96). A .appex is an ordinary bundle whose executable
# starts at NSExtensionMain; SwiftPM builds that binary, this assembles it.
SHARE_EXT_NAME=${SHARE_EXT_NAME:-AxonShare}
APPEX="$APP/Contents/PlugIns/${SHARE_EXT_NAME}.appex"
mkdir -p "$APPEX/Contents/MacOS" "$APPEX/Contents/Resources"
install_binary "$SHARE_EXT_NAME" "$APPEX/Contents/MacOS/$SHARE_EXT_NAME"
cat > "$APPEX/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleName</key><string>${APP_NAME}</string>
    <key>CFBundleDisplayName</key><string>${APP_NAME}</string>
    <key>CFBundleIdentifier</key><string>${BUNDLE_ID}.share</string>
    <key>CFBundleExecutable</key><string>${SHARE_EXT_NAME}</string>
    <key>CFBundlePackageType</key><string>XPC!</string>
    <key>CFBundleShortVersionString</key><string>${MARKETING_VERSION}</string>
    <key>CFBundleVersion</key><string>${BUILD_NUMBER}</string>
    <key>LSMinimumSystemVersion</key><string>${MACOS_MIN_VERSION}</string>
    <key>NSExtension</key>
    <dict>
        <key>NSExtensionPointIdentifier</key><string>com.apple.share-services</string>
        <!-- Module-qualified: renaming the target or the class breaks loading. -->
        <key>NSExtensionPrincipalClass</key><string>${SHARE_EXT_NAME}.ShareViewController</string>
        <key>NSExtensionAttributes</key>
        <dict>
            <!-- URLs, web pages and text only. Files have their own door:
                 watch-folders (FR-208/209). -->
            <key>NSExtensionActivationRule</key>
            <dict>
                <key>NSExtensionActivationSupportsWebURLWithMaxCount</key><integer>1</integer>
                <key>NSExtensionActivationSupportsWebPageWithMaxCount</key><integer>1</integer>
                <key>NSExtensionActivationSupportsText</key><true/>
            </dict>
        </dict>
    </dict>
</dict>
</plist>
PLIST

```

- [ ] **Step 3: Sign it before the app**

In the same file, insert immediately **before** the existing app signing call (`codesign "${CODESIGN_ARGS[@]}" \` … `--entitlements "$APP_ENTITLEMENTS" \` … `"$APP"`):

```bash
# Nested code first: signing the container invalidates any later nested change.
APPEX_ENTITLEMENTS=${APPEX_ENTITLEMENTS:-$ROOT/AxonShare.entitlements}
codesign "${CODESIGN_ARGS[@]}" --entitlements "$APPEX_ENTITLEMENTS" "$APPEX"

```

- [ ] **Step 4: Package and verify the bundle**

Run:

```bash
cd apps/companion && DIST_DIR=dist-verify ARCHES=arm64 ./Scripts/package_app.sh release
plutil -p dist-verify/Axon.app/Contents/PlugIns/AxonShare.appex/Contents/Info.plist | grep -E "NSExtensionPointIdentifier|NSExtensionPrincipalClass|CFBundleIdentifier"
codesign -d --entitlements - dist-verify/Axon.app/Contents/PlugIns/AxonShare.appex 2>&1 | grep -c app-sandbox
codesign --verify --deep --strict --verbose=1 dist-verify/Axon.app
```

Expected: the plist shows `com.apple.share-services`, `AxonShare.ShareViewController`, `com.axon.companion.share`; the entitlements grep prints `1`; codesign prints `valid on disk` and `satisfies its Designated Requirement`.

- [ ] **Step 5: Prove it reaches the Share menu**

Run (registration from `dist-verify` deliberately leaves `/Applications` alone):

```bash
cd apps/companion
pluginkit -a "$PWD/dist-verify/Axon.app/Contents/PlugIns/AxonShare.appex"
pluginkit -e use -i com.axon.companion.share
cat > /tmp/shares.swift <<'EOF'
import AppKit
let items: [Any] = [URL(string: "https://example.com/article")!]
print(NSSharingService.sharingServices(forItems: items).map(\.title))
EOF
swift /tmp/shares.swift 2>/dev/null
```

Expected: the printed list contains `"Axon"`. If it does not, re-check that `pluginkit -mAvvv -i com.axon.companion.share` shows a leading `+`.

- [ ] **Step 6: Clean up the probe**

```bash
cd apps/companion
pluginkit -r "$PWD/dist-verify/Axon.app/Contents/PlugIns/AxonShare.appex"
rm -rf dist-verify /tmp/shares.swift
```

- [ ] **Step 7: Document the sandbox asymmetry**

Append to `apps/companion/ENTITLEMENTS.md`:

```markdown
## AxonShare.entitlements — the share extension (CFR-96)

The share extension is **sandboxed** (`com.apple.security.app-sandbox`) while
the app that contains it is not. This is not an oversight and must not be
"fixed" in either direction:

- macOS requires app extensions to be sandboxed. There is no opt-out.
- The container app cannot be sandboxed: Sparkle launches separately-signed
  helper tools, which is also why it carries
  `com.apple.security.cs.disable-library-validation`.
- `codesign --verify --deep --strict` accepts the combination; it was verified
  on macOS 27 before the design was written.

The extension's only other entitlement is
`com.apple.security.network.client` — outgoing connections, used for exactly
one destination: `POST http://127.0.0.1:7777/api/capture`. Without it the
sandboxed process cannot reach the daemon at all.
```

- [ ] **Step 8: Commit**

```bash
git add apps/companion/AxonShare.entitlements apps/companion/Scripts/package_app.sh \
        apps/companion/ENTITLEMENTS.md
git commit -m "build(companion): assemble and sign the AxonShare appex (CFR-96)"
```

---

### Task 6: Read the extension's enablement state (CFR-99)

A registered share extension stays out of the Share menu until the user enables it. Without a signpost the feature's failure mode is silence — this is the read half.

**Files:**
- Create: `apps/companion/Sources/AxonKit/Share/ShareExtensionProbe.swift`
- Test: `apps/companion/Tests/AxonKitTests/ShareExtensionProbeTests.swift`

**Interfaces:**
- Consumes: `CLIRunning` (`run(binary:arguments:timeout:) async throws -> CLIResult`, `spawnDetached(binary:arguments:) throws`) and `CLIResult(exitCode:stdout:stderr:)` from `AxonKit/CLI/CLIRunner.swift`.
- Produces: `public enum ShareExtensionState { case enabled, registeredButDisabled, notRegistered, unknown }` and `public struct ShareExtensionProbe { public init(runner:pluginkit:); public func state() async -> ShareExtensionState; static func parse(_:) -> ShareExtensionState }`.

- [ ] **Step 1: Write the failing test**

Create `apps/companion/Tests/AxonKitTests/ShareExtensionProbeTests.swift`:

```swift
import Foundation
import Testing

@testable import AxonKit

/// A runner that replays one canned `pluginkit` result.
private struct CannedRunner: CLIRunning {
    var stdout: String = ""
    var exitCode: Int32 = 0

    func run(binary: URL, arguments: [String], timeout: Duration) async throws -> CLIResult {
        CLIResult(exitCode: exitCode, stdout: Data(stdout.utf8), stderr: "")
    }
    func spawnDetached(binary: URL, arguments: [String]) throws {}
}

private struct ThrowingRunner: CLIRunning {
    func run(binary: URL, arguments: [String], timeout: Duration) async throws -> CLIResult {
        throw AxonCLIError.timedOut(command: "pluginkit")
    }
    func spawnDetached(binary: URL, arguments: [String]) throws {}
}

/// Real `pluginkit -mAvvv -i <id>` output, captured on macOS 27.
private let enabledOutput = """
+    com.axon.companion.share(0.3.0)
\t            Path = /Applications/Axon.app/Contents/PlugIns/AxonShare.appex
\t            UUID = 2AC1D7D1-FDE5-4E7F-90A9-45C517BE9A42
"""

private let disabledOutput = """
     com.axon.companion.share(0.3.0)
\t            Path = /Applications/Axon.app/Contents/PlugIns/AxonShare.appex
"""

/// CFR-99 — the Share-menu entry's state, so "nothing happened" can say why.
@Suite
struct ShareExtensionProbeTests {
    @Test func leadingPlusMeansEnabled() {
        #expect(ShareExtensionProbe.parse(enabledOutput) == .enabled)
    }

    @Test func noPrefixMeansRegisteredButOff() {
        #expect(ShareExtensionProbe.parse(disabledOutput) == .registeredButDisabled)
    }

    @Test func noMatchesMeansNotRegistered() {
        // pluginkit's literal answer when the bundle id is unknown — and it
        // exits 0, so the exit code cannot be used to detect this.
        #expect(ShareExtensionProbe.parse("  (no matches)\n") == .notRegistered)
        #expect(ShareExtensionProbe.parse("") == .notRegistered)
    }

    @Test func probeReadsTheRunnersOutput() async {
        let probe = ShareExtensionProbe(runner: CannedRunner(stdout: enabledOutput))
        #expect(await probe.state() == .enabled)
    }

    @Test func nonZeroExitIsUnknownNotAClaim() async {
        let probe = ShareExtensionProbe(runner: CannedRunner(stdout: "", exitCode: 1))
        #expect(await probe.state() == .unknown)
    }

    @Test func aFailedRunIsUnknown() async {
        let probe = ShareExtensionProbe(runner: ThrowingRunner())
        #expect(await probe.state() == .unknown)
    }
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd apps/companion && swift test --filter ShareExtensionProbeTests`
Expected: FAIL — `cannot find 'ShareExtensionProbe' in scope`.

- [ ] **Step 3: Write the implementation**

Create `apps/companion/Sources/AxonKit/Share/ShareExtensionProbe.swift`:

```swift
import Foundation

/// Whether the Share menu will actually show Axon (CFR-99).
public enum ShareExtensionState: Equatable, Sendable {
    /// Registered and switched on: it is in the Share menu.
    case enabled
    /// Installed but not switched on in System Settings → Extensions → Sharing.
    case registeredButDisabled
    /// macOS has never seen it — the app has not been launched from
    /// /Applications (the dev-build case).
    case notRegistered
    /// pluginkit could not be read. Say nothing rather than something wrong.
    case unknown
}

/// Reads the share extension's state from `pluginkit`.
///
/// Read-only by design: Companion never enables an extension on the user's
/// behalf — that is a decision macOS deliberately puts in System Settings.
public struct ShareExtensionProbe: Sendable {
    public static let bundleID = "com.axon.companion.share"

    private let runner: CLIRunning
    private let pluginkit: URL

    public init(
        runner: CLIRunning = ProcessCLIRunner(),
        pluginkit: URL = URL(fileURLWithPath: "/usr/bin/pluginkit")
    ) {
        self.runner = runner
        self.pluginkit = pluginkit
    }

    public func state() async -> ShareExtensionState {
        guard let result = try? await runner.run(
            binary: pluginkit,
            arguments: ["-mAvvv", "-i", Self.bundleID],
            timeout: .seconds(5)
        ), result.succeeded else {
            return .unknown
        }
        return Self.parse(result.stdoutText)
    }

    /// `pluginkit -mAvvv` marks an enabled plugin with a leading `+`; an
    /// unknown bundle id prints "(no matches)" — and exits 0 either way, so the
    /// exit code proves nothing.
    static func parse(_ output: String) -> ShareExtensionState {
        guard let line = output
            .split(separator: "\n")
            .first(where: { $0.contains(bundleID) })
        else {
            return .notRegistered
        }
        return line.hasPrefix("+") ? .enabled : .registeredButDisabled
    }
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd apps/companion && swift test --filter ShareExtensionProbeTests`
Expected: PASS — 6 tests.

- [ ] **Step 5: Commit**

```bash
git add apps/companion/Sources/AxonKit/Share/ShareExtensionProbe.swift \
        apps/companion/Tests/AxonKitTests/ShareExtensionProbeTests.swift
git commit -m "feat(companion): read share extension enablement from pluginkit (CFR-99)"
```

---

### Task 7: Show the state in Settings (CFR-99)

**Files:**
- Modify: `apps/companion/Sources/AxonKit/Control/OpenAction.swift`
- Modify: `apps/companion/Tests/AxonKitTests/OpenActionsTests.swift`
- Modify: `apps/companion/Sources/Companion/Settings/GeneralPane.swift`

**Interfaces:**
- Consumes: `ShareExtensionProbe`/`ShareExtensionState` (Task 6); `ReadOnlyRow(title:value:actionTitle:action:)` from `Settings/SettingsWindow.swift`.
- Produces: `OpenAction.extensionsSettings`.

- [ ] **Step 1: Write the failing test**

Append to `apps/companion/Tests/AxonKitTests/OpenActionsTests.swift` inside the existing suite:

```swift
    @Test func extensionsSettingsOpensTheSharingPane() throws {
        let url = try #require(OpenAction.extensionsSettings.url())
        #expect(url.absoluteString == "x-apple.systempreferences:com.apple.ExtensionsPreferences")
    }
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd apps/companion && swift test --filter OpenActionsTests`
Expected: FAIL — `type 'OpenAction' has no member 'extensionsSettings'`.

- [ ] **Step 3: Add the case**

In `apps/companion/Sources/AxonKit/Control/OpenAction.swift`, add to the enum (after `case capturePage`):

```swift
    /// System Settings → General → Login Items & Extensions → Sharing, where
    /// the share extension is switched on (CFR-99).
    case extensionsSettings
```

and to the `switch` in `url(baseURL:)` (after the `capturePage` case):

```swift
        case .extensionsSettings:
            return URL(string: "x-apple.systempreferences:com.apple.ExtensionsPreferences")
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd apps/companion && swift test --filter OpenActionsTests`
Expected: PASS.

- [ ] **Step 5: Add the Settings row**

In `apps/companion/Sources/Companion/Settings/GeneralPane.swift`, add state next to the existing `@State` properties:

```swift
    @State private var shareExtension: ShareExtensionState = .unknown
```

Insert a new `Section` after the existing `Section("Updates & status")` block:

```swift
            // CFR-99: a registered extension stays out of the Share menu until
            // the user switches it on. Without this row the failure mode is
            // silence.
            Section("Share extension") {
                ReadOnlyRow(
                    title: "Share menu",
                    value: shareExtensionLabel,
                    actionTitle: shareExtension == .enabled ? nil : "Open Extensions Settings…",
                    action: shareExtension == .enabled ? nil : {
                        if let url = OpenAction.extensionsSettings.url() {
                            NSWorkspace.shared.open(url)
                        }
                    }
                )
                Text("Adds Axon to the Share menu in Safari and other apps. Shared links and selections land in your inbox.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
```

Add the label mapping and the refresh, inside the same `struct GeneralPane`:

```swift
    private var shareExtensionLabel: String {
        switch shareExtension {
        case .enabled: "Enabled"
        case .registeredButDisabled: "Not enabled"
        case .notRegistered: "Not installed — launch Axon from /Applications once"
        case .unknown: "Unknown"
        }
    }
```

and attach a refresh to the `Form` (alongside its existing modifiers):

```swift
        .task {
            shareExtension = await ShareExtensionProbe().state()
        }
```

- [ ] **Step 6: Build the app target**

Run: `cd apps/companion && swift build`
Expected: build succeeds. (`ReadOnlyRow`'s `action` is `(() -> Void)?` and its button is disabled when `value == nil` — passing `nil` for both title and action when enabled hides the button.)

- [ ] **Step 7: Run the full suite**

Run: `cd apps/companion && swift test`
Expected: PASS — everything.

- [ ] **Step 8: Commit**

```bash
git add apps/companion/Sources/AxonKit/Control/OpenAction.swift \
        apps/companion/Tests/AxonKitTests/OpenActionsTests.swift \
        apps/companion/Sources/Companion/Settings/GeneralPane.swift
git commit -m "feat(companion): show share extension state in Settings (CFR-99)"
```

---

### Task 8: Manual QA on a real Share menu

Extension launch is a registration property no unit test reaches. This runs against the **real** personal daemon on 7777 — the capture notes it creates are ordinary inbox notes, which is exactly what the feature is for. Registering from `dist/` leaves the installed `/Applications/Axon.app` untouched.

**Files:**
- Modify: `apps/companion/QA.md`

- [ ] **Step 1: Package and register**

```bash
cd apps/companion && ARCHES=arm64 ./Scripts/package_app.sh release
pluginkit -a "$PWD/dist/Axon.app/Contents/PlugIns/AxonShare.appex"
pluginkit -e use -i com.axon.companion.share
pluginkit -mAvvv -i com.axon.companion.share
curl -s http://127.0.0.1:7777/health | head -c 200
```

Expected: the pluginkit line starts with `+`; `/health` reports `"status":"ok"` and `"capture_enabled":true`.

- [ ] **Step 2: Share a page with a selection from Safari**

Open any article in Safari, select a paragraph, then Share → **Axon**.
Expected: the panel shows the page title and URL, the note field pre-filled with the selection and focused. Press ⏎.
Then check the capture landed (`/health` reports the profile *name*, not its
path, so ask the config for the path):

```bash
VAULT=$(eval echo "$(axon config get profiles.personal.vault_path)")
ls -t "$VAULT/00-Inbox" | head -3
head -5 "$VAULT/00-Inbox/$(ls -t "$VAULT/00-Inbox" | head -1)"
```

Expected: a fresh `capture-<stamp>.md` whose **first line is the URL** (that is
what makes the capture automation fetch it), with the selection in the body
under the page title as an H1.

- [ ] **Step 3: Failure paths**

- Stop the daemon (`axon stop`), share again: the panel must say **"Axon isn't running. Open Axon Companion to start it."** and stay open. Start the daemon, press **Capture** again in the same panel: it must succeed without re-sharing.
- Set `dashboard.capture_enabled: false`, restart the daemon, share again: **"Capture is switched off for this profile."** Restore the setting afterwards.

- [ ] **Step 4: The Settings row**

Open Companion Settings → General. Expected: **Share menu — Enabled**, no button.
Then `pluginkit -e ignore -i com.axon.companion.share`, reopen Settings: **Not enabled** with a working **Open Extensions Settings…** button. Re-enable with `pluginkit -e use -i com.axon.companion.share`.

- [ ] **Step 5: Record the cases in QA.md**

Add a `## Share extension (CFR-96…99)` section to `apps/companion/QA.md` listing Steps 1–4 as checkboxes with their expected results, in the style of the existing sections.

- [ ] **Step 6: Unregister the dev build**

```bash
cd apps/companion && pluginkit -r "$PWD/dist/Axon.app/Contents/PlugIns/AxonShare.appex"
```

(The `/Applications` install takes over once a signed 0.3.0 is installed.)

- [ ] **Step 7: Commit**

```bash
git add apps/companion/QA.md
git commit -m "docs(companion): QA cases for the share extension (CFR-96…99)"
```

---

### Task 9: Documentation and version

**Files:**
- Modify: `apps/companion/version.env` (`MARKETING_VERSION=0.3.0`, `BUILD_NUMBER=3`)
- Modify: `docs/Axon Companion — PRD.md` (new addendum section, before the `<!-- axon:links:start -->` marker)
- Modify: `docs/18-component-companion.md` (CFR range + a share-extension line)
- Modify: `apps/companion/CONTRACT.md` (§ capture: note the fourth caller)
- Modify: `docs/20-roadmap-ai-os.md` (E2 → shipped)
- Modify: `docs/GUIDE.md` (share-sheet capture in the capture section)
- Modify: `CHANGELOG.md` (`[Unreleased]`)

- [ ] **Step 1: Bump the Companion version**

In `apps/companion/version.env`: `MARKETING_VERSION=0.3.0` and `BUILD_NUMBER=3`. Sparkle compares `CFBundleVersion`, so the build number must increase or existing installs never see the update.

- [ ] **Step 2: Add the PRD addendum**

Append to `docs/Axon Companion — PRD.md`, before the `<!-- axon:links:start -->` marker, mirroring the 0.2.0 addendum's shape:

```markdown
## 0.3.0 addendum — the share extension (CFR-96…99, 2026-08-21)

Graduates `docs/20` E2 (spec:
`docs/superpowers/specs/2026-08-21-share-extension-design.md`). Zero daemon
change: the extension is a fourth caller of `POST /api/capture` (ADR-024),
after the inbox folder, the bookmarklet page and `CaptureThoughtIntent`.

- **CFR-96 AxonShare.appex** — a SwiftPM-built share extension
  (`com.apple.share-services`; URL, web page and text activation rules only),
  sandboxed inside the unsandboxed app, assembled and signed by
  `package_app.sh` before the container.
- **CFR-97 Payload and panel** — `ShareExtraction` reduces the shared items to
  `SharePayload(url,title,text)` in AxonKit (unit-tested); the panel shows
  title and URL, pre-fills an editable note with the selection, and posts
  through the widened `capture(url:title:text:)`.
- **CFR-98 Failures are shown** — daemon down, capture disabled, guard refusal
  and any other status each get their own sentence in the panel; the request
  is completed only on success or cancel, so **Capture** can be retried.
- **CFR-99 Enablement is visible** — a Settings row reads `pluginkit` and,
  when the extension is registered but off, offers System Settings →
  Extensions. Companion never enables it on the user's behalf.

Files are deliberately out of scope: they have a door already (watch-folders,
FR-208/209). Like the App Intents, the extension talks to `127.0.0.1:7777` and
cannot follow a custom `dashboard.port`.
```

- [ ] **Step 3: Update the component doc**

In `docs/18-component-companion.md`, change the CFR sentence to read `**CFR-01…CFR-99**` and add to the target list that `AxonShare` is a third target — the share extension's appex, view-only, with its logic in `AxonKit/Share/`.

- [ ] **Step 4: Note the fourth caller in CONTRACT.md**

In the `POST /api/capture` row's surrounding prose, add: "Callers: the served `/capture` page (ADR-024), `CaptureThoughtIntent` (CFR-95), and the share extension (CFR-96). No contract change — the extension sends the same `{url,title,text}`."

- [ ] **Step 5: Mark E2 shipped**

In `docs/20-roadmap-ai-os.md`, rewrite the E2 heading and body to match the house style of E1/B1:

```markdown
### E2 — macOS share extension (S, Companion) · **SHIPPED 2026-08-21 in companion-v0.3.0 — CFR-96…CFR-99** (spec: `docs/superpowers/specs/2026-08-21-share-extension-design.md`)
**Value:** the system Share sheet becomes a capture path from every app.
**Shape:** an `AxonShare.appex` inside the Companion, sandboxed, POSTing to the
existing guarded `/api/capture` (ADR-024) — zero new daemon surface, no FR, no
ADR, no migration.
**Open decisions resolved.** **URL, web page and text only** — files already
have a door (E1 watch-folders), and a file-capable extension needs either vault
paths inside a sandbox or a new daemon endpoint. A **compose panel**, not
fire-and-forget: it is where an annotation is worth something and where a
failure has somewhere to appear.

**What the feasibility spike settled before the spec was written:** SwiftPM
*can* build an appex (`-Xlinker -e -Xlinker _NSExtensionMain`; the bare `-e`
form makes swiftc evaluate the symbol name as source), a **non-sandboxed
container app can host a sandboxed appex**, and the sandbox does not block the
loopback POST. **And the trap:** a registered extension is *not* enabled — it
stays out of the Share menu until the user switches it on in System Settings,
which is why CFR-99 exists rather than a support thread.
```

- [ ] **Step 6: Document it for users**

In `docs/GUIDE.md`, add to the capture section a short "From the Share sheet (macOS)" paragraph: install the Companion, enable **Axon** under System Settings → Extensions → Sharing, then Share → Axon from any app; the link and any selection land in `00-Inbox` and are ingested on the next capture tick.

- [ ] **Step 7: Add the changelog entry**

Under `## [Unreleased]` in `CHANGELOG.md`, in the existing `### Added` list style:

```markdown
- **macOS share extension (Companion, CFR-96…99)** — "Axon" in the system
  Share sheet: share a page, link or selection from any app into `00-Inbox`.
  A sandboxed `AxonShare.appex` inside `Axon.app` posts to the existing
  guarded `/api/capture` (ADR-024) — no new daemon surface, no schema change.
  Enable it under System Settings → Extensions → Sharing; Companion Settings
  shows whether it is on. Ships as `companion-v0.3.0`.
```

- [ ] **Step 8: Verify and commit**

Run: `cd apps/companion && swift test && swift build` and, from the repo root, `go build ./... && go test ./... 2>&1 | tail -5` (the daemon must be untouched — if a Go test fails, something went wrong that this slice has no business causing).

```bash
git add -A
git commit -m "docs: ship the share extension — docs/20 E2, CFR-96…99, companion 0.3.0"
```

---

## Self-Review

**Spec coverage:**

| Spec section | Task |
| --- | --- |
| CFR-96 bundle, activation rules, entitlements, packaging order | 4 (target + principal class), 5 (assembly, signing, ENTITLEMENTS.md) |
| CFR-97 extraction in AxonKit | 2 |
| CFR-97 widened `capture(url:title:text:)` | 1 |
| CFR-97 compose panel (title/URL read-only, pre-filled focused note, Capture/Cancel) | 4 |
| CFR-98 error table, panel stays open, retry, no `cancelRequest(withError:)` | 3 (copy), 4 (model behaviour), 8 (manual proof) |
| CFR-98 port limitation recorded | 9 (PRD addendum) |
| CFR-99 pluginkit states + Settings row + Extensions button | 6, 7 |
| Verification — extraction, wire shape, error mapping, probe parse | 1, 2, 3, 6 |
| Verification — build gate (`_NSExtensionMain`), codesign deep/strict | 4 Step 5, 5 Step 4 |
| Verification — manual QA | 8 |
| Docs to update on completion | 9 (all listed files) |

**Placeholder scan:** none — every code step carries the actual code, every run step the actual command and its expected output.

**Type consistency:** `SharePayload(url:title:text:)` and `.isEmpty` are defined in Task 2 and used in Tasks 4; `ShareCaptureMessage.text(for:)` defined in 3, used in 4; `capture(url:title:text:)` defined in 1, called in 4; `ShareExtensionProbe.state()`/`ShareExtensionState` defined in 6, used in 7; `AxonShare.ShareViewController` (Task 4) matches `NSExtensionPrincipalClass` (Task 5); `ReadOnlyRow(title:value:actionTitle:action:)` matches the existing signature in `SettingsWindow.swift`.
