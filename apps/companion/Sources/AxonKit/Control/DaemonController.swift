import Foundation

/// Why the icon is showing its attention badge, most actionable first.
public enum AttentionReason: Sendable, Equatable {
    /// A named component the daemon itself reports unhealthy.
    case degraded(component: String)
    /// The budget guard has paused automations. Interactive use is unaffected.
    case budgetGuard
    /// A newer daemon release is available.
    case updateAvailable

    public var summary: String {
        switch self {
        case .degraded(let component): "\(component) unhealthy"
        case .budgetGuard: "budget guard paused automations"
        case .updateAvailable: "update available"
        }
    }
}

extension DaemonState {
    /// Running and clean.
    static func running(_ health: AxonHealth) -> DaemonState { .runningWith(health, []) }

    /// The single place daemon facts become an icon state.
    ///
    /// Kept as a pure function so every rule is testable without a poll loop,
    /// a subprocess or a clock.
    public static func derive(
        health: AxonHealth?,
        usage: UsageSnapshot?,
        binaryPresent: Bool
    ) -> DaemonState {
        // Nothing to control beats everything else: with no binary, Start and
        // Doctor are meaningless and onboarding is the only useful offer.
        guard binaryPresent else { return .notInstalled }
        guard let health else { return .stopped }

        var reasons: [AttentionReason] = []
        // Ordered by how much the user can act on it.
        for component in health.degradedComponents {
            reasons.append(.degraded(component: component))
        }
        if usage?.isGuardTripped == true {
            reasons.append(.budgetGuard)
        }
        if health.updateAvailable == true {
            reasons.append(.updateAvailable)
        }
        return .runningWith(health, reasons)
    }
}

/// Reads daemon health. Narrower than `DashboardClient` so tests can script it.
public protocol DaemonHealthReading: Sendable {
    func health() async throws -> AxonHealth
}

extension DashboardClient: DaemonHealthReading {}

/// Starts and stops the daemon. Narrower than `AxonCLI` for the same reason.
public protocol DaemonLifecycleControlling: Sendable {
    /// Proves the binary is usable even when the dashboard port is dead.
    func statusProbe() async throws -> Bool
    func start() async throws
    func stop() async throws
}

/// Adapts the full CLI to the lifecycle protocol.
public struct AxonCLILifecycle: DaemonLifecycleControlling {
    private let cli: AxonCLI
    public init(cli: AxonCLI) { self.cli = cli }

    public func statusProbe() async throws -> Bool {
        _ = try await cli.status()
        return true
    }
    public func start() async throws { try await cli.start() }
    public func stop() async throws { try await cli.stop() }
}

/// The source of truth for the menu bar icon and popover (CFR-01).
@MainActor
@Observable
public final class DaemonController {
    public private(set) var state: DaemonState = .unknown
    /// The most recent state change, for the notification router.
    public private(set) var lastTransition: DaemonTransition?
    /// A user-facing failure from the last lifecycle action.
    public private(set) var lastError: String?
    /// True when the last stop came from the user, not from a crash — the
    /// router must not cry wolf every time someone clicks Stop (CFR-70).
    public private(set) var userInitiatedStop = false
    /// The last successful health read, for the popover header.
    public private(set) var health: AxonHealth?
    public private(set) var usage: UsageSnapshot?

    private let reader: DaemonHealthReading
    private let lifecycle: DaemonLifecycleControlling?
    private let binaryPresent: @Sendable () -> Bool
    private let usageReader: @Sendable () async -> UsageSnapshot?
    private let pollInterval: Duration

    /// How long a start or stop may stay pending before reverting with an
    /// error. Long enough for a cold start (index open, scheduler wiring),
    /// short enough that a wedged daemon does not pin the UI indefinitely.
    public static let defaultTransitionDeadline: Duration = .seconds(20)

    private let transitionDeadline: Duration
    /// How often a pending transition re-probes.
    private let settlePollInterval: Duration

    /// Held in a nonisolated box so `deinit` — which cannot touch MainActor
    /// state — can still cancel it. Without the cancel, the poll loop outlives
    /// the controller, spinning on a nil weak self forever.
    private let monitor = TaskBox()

    public init(
        reader: DaemonHealthReading,
        lifecycle: DaemonLifecycleControlling?,
        binaryPresent: @escaping @Sendable () -> Bool,
        usage: @escaping @Sendable () async -> UsageSnapshot?,
        pollInterval: Duration = .seconds(5),
        transitionDeadline: Duration = DaemonController.defaultTransitionDeadline,
        settlePollInterval: Duration = .milliseconds(400)
    ) {
        self.reader = reader
        self.lifecycle = lifecycle
        self.binaryPresent = binaryPresent
        self.usageReader = usage
        self.pollInterval = pollInterval
        self.transitionDeadline = transitionDeadline
        self.settlePollInterval = settlePollInterval
    }

    /// Whether Start/Stop/Restart can do anything at all.
    public var canControlLifecycle: Bool { lifecycle != nil }

    // MARK: monitoring

    /// Polls health forever. Also call ``refresh()`` from the SSE disconnect
    /// fast path so a drop is noticed well inside the 5s budget (CFR-01).
    public func startMonitoring() {
        guard !monitor.isRunning else { return }
        monitor.replace(with: Task { [weak self, pollInterval] in
            while !Task.isCancelled {
                await self?.refresh()
                try? await Task.sleep(for: pollInterval)
            }
        })
    }

    public func stopMonitoring() {
        monitor.cancel()
    }

    deinit { monitor.cancel() }

    /// One health read, applied to the state machine.
    public func refresh() async {
        // A pinned transition owns the state until it resolves or times out;
        // otherwise a poll landing mid-start would flap the icon.
        if state == .starting || state == .stopping { return }
        await applyProbe()
    }

    @discardableResult
    private func applyProbe() async -> DaemonState {
        let present = binaryPresent()
        let observed = try? await reader.health()
        let currentUsage = observed == nil ? nil : await usageReader()

        if let observed { health = observed }
        if let currentUsage { usage = currentUsage }

        let next = DaemonState.derive(
            health: observed, usage: currentUsage, binaryPresent: present
        )
        apply(next)
        return next
    }

    private func apply(_ next: DaemonState) {
        guard next != state else { return }
        lastTransition = DaemonTransition(from: state, to: next)
        state = next
    }

    // MARK: lifecycle

    public func startDaemon() async {
        guard let lifecycle else {
            lastError = "no axon binary found — run setup first"
            return
        }
        lastError = nil
        userInitiatedStop = false
        apply(.starting)

        do {
            try await lifecycle.start()
        } catch {
            lastError = Self.describe(error)
            await applyProbeAfterPinnedTransition()
            return
        }
        await settle(until: { $0.isRunning }, failure: "AXON did not come up in time")
    }

    public func stopDaemon() async {
        guard let lifecycle else {
            lastError = "no axon binary found"
            return
        }
        lastError = nil
        // Set before the state change so an observer of the transition already
        // knows this stop was intentional.
        userInitiatedStop = true
        apply(.stopping)

        do {
            try await lifecycle.stop()
        } catch {
            lastError = Self.describe(error)
            await applyProbeAfterPinnedTransition()
            return
        }
        await settle(until: { $0 == .stopped }, failure: "AXON did not stop in time")
    }

    public func restartDaemon() async {
        await stopDaemon()
        // A restart is one user intent, and it ends running — do not leave the
        // "user stopped it" flag set for the notifier to trip over.
        userInitiatedStop = false
        await startDaemon()
    }

    /// Polls until the expected state arrives or the deadline passes.
    private func settle(
        until isSettled: @escaping (DaemonState) -> Bool, failure: String
    ) async {
        let deadline = ContinuousClock.now + transitionDeadline
        while ContinuousClock.now < deadline {
            let pinned = state
            state = .unknown  // release the pin for one probe
            let observed = await applyProbe()
            if isSettled(observed) { return }
            if state == .unknown { state = pinned }
            try? await Task.sleep(for: settlePollInterval)
        }
        lastError = failure
        await applyProbeAfterPinnedTransition()
    }

    /// Clears a pinned `.starting`/`.stopping` and re-derives from reality.
    private func applyProbeAfterPinnedTransition() async {
        state = .unknown
        await applyProbe()
    }

    private static func describe(_ error: Error) -> String {
        (error as? AxonCLIError)?.message ?? String(describing: error)
    }
}

/// One observed state change.
public struct DaemonTransition: Sendable, Equatable {
    public let from: DaemonState
    public let to: DaemonState
}


/// A cancellable task holder that `deinit` can reach from any isolation.
private final class TaskBox: @unchecked Sendable {
    private let lock = NSLock()
    private var task: Task<Void, Never>?

    var isRunning: Bool { lock.withLock { task != nil } }

    func replace(with new: Task<Void, Never>) {
        let previous = lock.withLock { () -> Task<Void, Never>? in
            let old = task
            task = new
            return old
        }
        previous?.cancel()
    }

    func cancel() {
        let current = lock.withLock { () -> Task<Void, Never>? in
            let old = task
            task = nil
            return old
        }
        current?.cancel()
    }
}
