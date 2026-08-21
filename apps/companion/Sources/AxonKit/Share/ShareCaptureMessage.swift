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
