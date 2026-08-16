import Foundation

/// What Companion believes the daemon is doing right now.
///
/// Placeholder in Task 2 — `DaemonController` (Task 6) fills in the health
/// payloads and attention reasons. The cases are already the full set so the
/// menu bar icon has a stable vocabulary to switch on from the start.
public enum DaemonState: Equatable, Sendable {
    /// Nothing has been observed yet — the very first poll has not returned.
    case unknown
    /// No `axon` binary could be located, so there is nothing to control.
    case notInstalled
    /// The binary exists but nothing is serving the dashboard port.
    case stopped
    /// Running and clean.
    case running
    /// Running, but something wants the user's attention.
    case attention

    /// One short line suitable for a menu bar popover subtitle.
    public var summary: String {
        switch self {
        case .unknown: "checking…"
        case .notInstalled: "not installed"
        case .stopped: "stopped"
        case .running: "running"
        case .attention: "needs attention"
        }
    }
}
