import Foundation

/// What Companion believes the daemon is doing right now.
///
/// `DaemonController.state` is the single source of truth for the menu bar icon
/// and the popover; every view switches on this and nothing else.
public enum DaemonState: Equatable, Sendable {
    /// Nothing has been observed yet — the first poll has not returned.
    case unknown
    /// No `axon` binary could be located, so there is nothing to control.
    case notInstalled
    /// The binary exists but nothing is serving the dashboard port.
    case stopped
    /// A start is pending. Pinned until health confirms or the deadline passes.
    case starting
    /// A stop is pending.
    case stopping
    /// Serving. `reasons` is empty when clean, non-empty when the icon should
    /// wear its attention badge.
    ///
    /// Running and attention are one case rather than two because they carry
    /// identical data and differ only by whether anything is wrong — splitting
    /// them made every call site handle two branches that behave the same.
    case runningWith(AxonHealth, [AttentionReason])

    /// Serving, whether clean or wanting attention.
    public var isRunning: Bool {
        if case .runningWith = self { return true }
        return false
    }

    /// True only when running AND something needs the user.
    public var needsAttention: Bool { !attentionReasons.isEmpty }

    public var attentionReasons: [AttentionReason] {
        if case .runningWith(_, let reasons) = self { return reasons }
        return []
    }

    public var health: AxonHealth? {
        if case .runningWith(let health, _) = self { return health }
        return nil
    }

    /// True while a lifecycle action is pending — buttons disable on this.
    public var isTransitioning: Bool { self == .starting || self == .stopping }

    /// One short line for a popover subtitle or a VoiceOver label.
    public var summary: String {
        switch self {
        case .unknown: "checking…"
        case .notInstalled: "not installed"
        case .stopped: "stopped"
        case .starting: "starting…"
        case .stopping: "stopping…"
        case .runningWith(_, let reasons):
            reasons.isEmpty
                ? "running"
                : reasons.map(\.summary).joined(separator: " · ")
        }
    }
}
