import Foundation
import Testing

@testable import AxonKit

// MARK: - scripted doubles

/// Serves a scripted sequence of health results, so a test can describe a
/// timeline ("refused, refused, then healthy") rather than a single state.
actor ScriptedHealth: DaemonHealthReading {
    private var script: [Result<AxonHealth, Error>]
    private(set) var callCount = 0

    init(_ script: [Result<AxonHealth, Error>]) { self.script = script }

    /// Repeats the last entry once the script runs out.
    func health() async throws -> AxonHealth {
        callCount += 1
        guard !script.isEmpty else { throw DashboardError.unreachable }
        let next = script.count > 1 ? script.removeFirst() : script[0]
        return try next.get()
    }
}

actor ScriptedLifecycle: DaemonLifecycleControlling {
    private(set) var started = 0
    private(set) var stopped = 0
    var startThrows: Error?

    init(startThrows: Error? = nil) { self.startThrows = startThrows }

    func statusProbe() async throws -> Bool { true }
    func start() async throws {
        started += 1
        if let startThrows { throw startThrows }
    }
    func stop() async throws { stopped += 1 }
}

/// A lifecycle whose `statusProbe` fails — the binary is there but unusable.
actor UnreadableLifecycle: DaemonLifecycleControlling {
    func statusProbe() async throws -> Bool { throw AxonCLIError.notExecutable(path: "/x/axon") }
    func start() async throws {}
    func stop() async throws {}
}

private func health(
    status: String = "ok", db: Bool = true, updateAvailable: Bool? = nil
) -> AxonHealth {
    var json = #"{"version":"1.3.2","profile":"personal","status":"\#(status)","db":\#(db)"#
    if let updateAvailable {
        json += #","update_available":\#(updateAvailable),"latest_version":"1.4.0""#
    }
    json += "}"
    return try! AxonJSON.decode(AxonHealth.self, from: Data(json.utf8))
}

private func usage(guardPaused: Bool) -> UsageSnapshot {
    let json = #"{"day_used":1,"day_limit":10,"week_used":1,"week_limit":10,"guard_paused":\#(guardPaused),"guard_reason":"\#(guardPaused ? "daily budget exhausted" : "")"}"#
    return try! AxonJSON.decode(UsageSnapshot.self, from: Data(json.utf8))
}

// MARK: - state derivation

@Suite @MainActor
struct DaemonStateDerivationTests {
    @Test func healthyDaemonIsRunning() {
        let state = DaemonState.derive(
            health: health(), usage: usage(guardPaused: false), binaryPresent: true
        )
        #expect(state == .running(health()))
    }

    @Test func noBinaryIsNotInstalledRegardlessOfHealth() {
        // Ordering matters: without a binary there is nothing to control, so
        // the onboarding path must win even if a daemon is somehow answering.
        let state = DaemonState.derive(
            health: health(), usage: usage(guardPaused: false), binaryPresent: false
        )
        #expect(state == .notInstalled)
    }

    @Test func unreachableWithBinaryPresentIsStopped() {
        let state = DaemonState.derive(health: nil, usage: nil, binaryPresent: true)
        #expect(state == .stopped)
    }

    @Test func degradedHealthIsAttention() {
        let state = DaemonState.derive(
            health: health(status: "degraded", db: false),
            usage: usage(guardPaused: false), binaryPresent: true
        )
        #expect(state.attentionReasons == [.degraded(component: "db")])
    }

    @Test func trippedBudgetGuardIsAttention() {
        let state = DaemonState.derive(
            health: health(), usage: usage(guardPaused: true), binaryPresent: true
        )
        #expect(state.attentionReasons == [.budgetGuard])
    }

    @Test func availableUpdateIsAttention() {
        let state = DaemonState.derive(
            health: health(updateAvailable: true),
            usage: usage(guardPaused: false), binaryPresent: true
        )
        #expect(state.attentionReasons == [.updateAvailable])
    }

    @Test func reasonsAccumulateMostSevereFirst() {
        let state = DaemonState.derive(
            health: health(status: "degraded", db: false, updateAvailable: true),
            usage: usage(guardPaused: true), binaryPresent: true
        )

        // Degraded health is the most actionable; an available update the least.
        #expect(state.attentionReasons == [
            .degraded(component: "db"), .budgetGuard, .updateAvailable,
        ])
    }

    @Test func updateNotAvailableIsNotAttention() {
        let state = DaemonState.derive(
            health: health(updateAvailable: false),
            usage: usage(guardPaused: false), binaryPresent: true
        )
        #expect(state.attentionReasons.isEmpty)
        #expect(state.isRunning)
    }

    /// Usage is a separate request; a healthy daemon whose usage read failed
    /// must still read as running, not attention.
    @Test func missingUsageDoesNotFabricateAGuardTrip() {
        let state = DaemonState.derive(health: health(), usage: nil, binaryPresent: true)
        #expect(state.isRunning)
        #expect(state.attentionReasons.isEmpty)
    }
}

// MARK: - controller

@Suite @MainActor
struct DaemonControllerTests {
    private func makeController(
        health: ScriptedHealth,
        lifecycle: DaemonLifecycleControlling? = ScriptedLifecycle(),
        binaryPresent: Bool = true
    ) -> DaemonController {
        DaemonController(
            reader: health,
            lifecycle: lifecycle,
            binaryPresent: { binaryPresent },
            usage: { usage(guardPaused: false) },
            // Short deadlines: these tests assert the state machine, not the
            // production patience budget, and the real 20s deadline made the
            // suite take 20s to prove one revert.
            transitionDeadline: .milliseconds(600),
            settlePollInterval: .milliseconds(20)
        )
    }

    @Test func refreshMovesUnknownToRunning() async {
        let controller = makeController(health: ScriptedHealth([.success(health())]))
        #expect(controller.state == .unknown)

        await controller.refresh()
        #expect(controller.state.isRunning)
    }

    @Test func refreshMovesToStoppedWhenUnreachable() async {
        let controller = makeController(
            health: ScriptedHealth([.failure(DashboardError.unreachable)])
        )
        await controller.refresh()

        #expect(controller.state == .stopped)
    }

    @Test func noBinaryYieldsNotInstalled() async {
        let controller = makeController(
            health: ScriptedHealth([.failure(DashboardError.unreachable)]),
            lifecycle: nil, binaryPresent: false
        )
        await controller.refresh()

        #expect(controller.state == .notInstalled)
    }

    @Test func startWalksStoppedThroughStartingToRunning() async {
        // Refused once (still stopped), then healthy — the real shape of a start.
        let script = ScriptedHealth([
            .failure(DashboardError.unreachable),
            .success(health()),
        ])
        let lifecycle = ScriptedLifecycle()
        let controller = makeController(health: script, lifecycle: lifecycle)
        await controller.refresh()
        #expect(controller.state == .stopped)

        await controller.startDaemon()

        #expect(await lifecycle.started == 1)
        #expect(controller.state.isRunning)
        #expect(controller.lastError == nil)
    }

    @Test func failedStartRevertsWithAnError() async {
        let lifecycle = ScriptedLifecycle(
            startThrows: AxonCLIError.failed(command: "start", stderr: "port busy", exitCode: 1)
        )
        let controller = makeController(
            health: ScriptedHealth([.failure(DashboardError.unreachable)]),
            lifecycle: lifecycle
        )

        await controller.startDaemon()

        // It must not sit in .starting forever, and must say why.
        #expect(controller.state != .starting)
        #expect(controller.lastError?.contains("port busy") == true)
    }

    @Test func stopWalksRunningThroughStoppingToStopped() async {
        let script = ScriptedHealth([
            .success(health()),
            .failure(DashboardError.unreachable),
        ])
        let lifecycle = ScriptedLifecycle()
        let controller = makeController(health: script, lifecycle: lifecycle)
        await controller.refresh()

        await controller.stopDaemon()

        #expect(await lifecycle.stopped == 1)
        #expect(controller.state == .stopped)
    }

    /// A user-initiated stop must be distinguishable from a crash, or the
    /// notification router cries wolf every time the user clicks Stop (CFR-70).
    @Test func stopIsRecordedAsUserInitiated() async {
        let controller = makeController(
            health: ScriptedHealth([.success(health()), .failure(DashboardError.unreachable)])
        )
        await controller.refresh()
        #expect(controller.userInitiatedStop == false)

        await controller.stopDaemon()
        #expect(controller.userInitiatedStop)
    }

    @Test func anUnexpectedStopIsNotMarkedUserInitiated() async {
        let controller = makeController(
            health: ScriptedHealth([.success(health()), .failure(DashboardError.unreachable)])
        )
        await controller.refresh()
        await controller.refresh()

        #expect(controller.state == .stopped)
        #expect(controller.userInitiatedStop == false)
    }

    @Test func restartStopsThenStarts() async {
        let lifecycle = ScriptedLifecycle()
        // Running, then down while stopping, then back up — the real shape.
        let controller = makeController(
            health: ScriptedHealth([
                .success(health()),
                .failure(DashboardError.unreachable),
                .success(health()),
            ]),
            lifecycle: lifecycle
        )

        await controller.restartDaemon()

        #expect(await lifecycle.stopped == 1)
        #expect(await lifecycle.started == 1)
    }

    /// Without a CLI there is nothing to control — the buttons must be inert
    /// rather than silently doing nothing.
    @Test func lifecycleActionsAreUnavailableWithoutACLI() async {
        let controller = makeController(
            health: ScriptedHealth([.success(health())]), lifecycle: nil
        )
        #expect(controller.canControlLifecycle == false)

        await controller.startDaemon()
        #expect(controller.lastError != nil)
    }

    @Test func transitionsAreObservableForNotificationRouting() async {
        let controller = makeController(
            health: ScriptedHealth([.success(health()), .failure(DashboardError.unreachable)])
        )
        await controller.refresh()
        await controller.refresh()

        let transition = try? #require(controller.lastTransition)
        #expect(transition?.from.isRunning == true)
        #expect(transition?.to == .stopped)
    }

    @Test func repeatedIdenticalPollsDoNotEmitTransitions() async {
        let controller = makeController(health: ScriptedHealth([.success(health())]))
        await controller.refresh()
        let first = controller.lastTransition
        await controller.refresh()

        // A transition that never happened must not wake the notifier.
        #expect(controller.lastTransition == first)
    }
}
