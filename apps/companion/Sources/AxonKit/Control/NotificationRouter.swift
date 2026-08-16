import Foundation

/// A notification the router decided to raise. Delivery is somebody else's job:
/// the router is pure so its rules are testable without user permission, a
/// notification centre, or a signed bundle.
public struct PlannedNotification: Equatable, Sendable {
    /// Stable per (kind, subject) so a repeat replaces rather than stacks.
    public let id: String
    public let title: String
    public let body: String
    /// Where a click should land.
    public let action: OpenAction

    public init(id: String, title: String, body: String, action: OpenAction) {
        self.id = id
        self.title = title
        self.body = body
        self.action = action
    }
}

/// Decides what deserves a notification (CFR-70/71).
///
/// Success is never notified. Ever. The rest is dedup: a failing automation on
/// a 5-minute schedule would otherwise produce twelve notifications an hour,
/// which trains the user to ignore all of them.
public final class NotificationRouter: @unchecked Sendable {
    /// One notification per automation per hour.
    public static let automationFailWindow: TimeInterval = 3600

    private let lock = NSLock()
    private let prefs: @Sendable () -> NotificationPrefs
    private let post: @Sendable (PlannedNotification) -> Void
    private let now: @Sendable () -> Date

    /// Last notification time per dedup key.
    private var lastSent: [String: Date] = [:]
    /// Whether the guard is currently tripped, so one episode notifies once
    /// rather than on every event while it stays tripped.
    private var guardEpisodeActive = false
    /// Versions already announced, so an available update notifies once.
    private var announcedVersions: Set<String> = []
    /// Set by the controller when the user asked for the stop.
    private var userInitiatedStop = false

    public init(
        prefs: @escaping @Sendable () -> NotificationPrefs,
        post: @escaping @Sendable (PlannedNotification) -> Void,
        now: @escaping @Sendable () -> Date = { Date() }
    ) {
        self.prefs = prefs
        self.post = post
        self.now = now
    }

    /// Told by `DaemonController` before a deliberate stop, so a user clicking
    /// Stop is not reported to them as a crash.
    public func noteUserInitiatedStop() {
        lock.withLock { userInitiatedStop = true }
    }

    public func noteUserInitiatedStart() {
        lock.withLock { userInitiatedStop = false }
    }

    // MARK: events

    public func handle(event: AxonEvent) {
        let preferences = prefs()
        guard preferences.allows(kind: event.kind) else { return }

        switch event.kind {
        case "automation.fail":
            let name = event.automationName ?? "an automation"
            guard claim(key: "automation.fail|\(name)", window: Self.automationFailWindow) else { return }
            post(PlannedNotification(
                id: "automation.fail|\(name)",
                title: "\(name) failed",
                body: event.message.isEmpty ? "Check the dashboard for details." : event.message,
                action: .dashboardTab("automations")
            ))

        case "token.deny", "token.defer", "token.downgrade":
            // One notification per guard episode, not per denied call: a
            // tripped guard denies every automation that tries afterwards.
            guard claimGuardEpisode() else { return }
            post(PlannedNotification(
                id: "token.guard",
                title: "Budget guard tripped",
                body: event.reason ?? "Automations are paused until the budget window resets.",
                action: .dashboardTab("tokens")
            ))

        default:
            break
        }
    }

    // MARK: state transitions

    public func handle(transition: DaemonTransition) {
        let preferences = prefs()

        // Any successful start clears the episode flags: a fresh daemon is a
        // fresh budget window and a fresh chance to fail.
        if transition.to.isRunning {
            lock.withLock {
                guardEpisodeActive = false
                userInitiatedStop = false
            }
        }

        guard preferences.daemonStoppedUnexpectedly else { return }
        guard transition.from.isRunning, transition.to == .stopped else { return }

        // A stop the user asked for is not news.
        let wasDeliberate = lock.withLock { userInitiatedStop }
        guard !wasDeliberate else { return }
        guard claim(key: "daemon.stopped", window: 60) else { return }

        post(PlannedNotification(
            id: "daemon.stopped",
            title: "AXON stopped unexpectedly",
            body: "Scheduled automations won't run until it's started again.",
            action: .dashboard
        ))
    }

    /// An available update, announced once per version.
    public func handle(updateAvailable version: String?) {
        guard prefs().updateAvailable, let version, !version.isEmpty else { return }
        let isNew = lock.withLock { announcedVersions.insert(version).inserted }
        guard isNew else { return }

        post(PlannedNotification(
            id: "update|\(version)",
            title: "AXON \(version) is available",
            body: "Update from the menu bar, or run `axon update`.",
            action: .dashboard
        ))
    }

    // MARK: dedup

    /// Returns true if this key has not fired inside `window`.
    private func claim(key: String, window: TimeInterval) -> Bool {
        lock.withLock {
            let moment = now()
            if let previous = lastSent[key], moment.timeIntervalSince(previous) < window {
                return false
            }
            lastSent[key] = moment
            return true
        }
    }

    private func claimGuardEpisode() -> Bool {
        lock.withLock {
            guard !guardEpisodeActive else { return false }
            guardEpisodeActive = true
            return true
        }
    }
}
