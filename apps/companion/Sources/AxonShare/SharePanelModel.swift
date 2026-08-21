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
