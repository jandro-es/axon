import Foundation
import Testing

@testable import AxonKit

/// A login item that records calls instead of touching the real registry,
/// which needs a signed bundled app and fails inside a test runner.
final class FakeLoginItem: LoginItemControlling, @unchecked Sendable {
    private let lock = NSLock()
    private var registered: Bool
    var failWith: Error?

    init(registered: Bool = false, failWith: Error? = nil) {
        self.registered = registered
        self.failWith = failWith
    }

    var isRegistered: Bool { lock.withLock { registered } }

    func setRegistered(_ newValue: Bool) throws {
        if let failWith { throw failWith }
        lock.withLock { registered = newValue }
    }
}

private struct LoginItemError: Error, LocalizedError {
    var errorDescription: String? { "not signed" }
}

@MainActor
private func makeStore(
    loginItem: LoginItemControlling = FakeLoginItem(),
    cli: AxonCLI? = nil
) throws -> (SettingsStore, UserDefaults) {
    // An isolated suite so tests never read or write the developer's real
    // preferences, and never leak into each other.
    let suite = "com.axon.companion.tests.\(UUID().uuidString)"
    let defaults = try #require(UserDefaults(suiteName: suite))
    return (SettingsStore(defaults: defaults, cli: cli, loginItem: loginItem), defaults)
}

@Suite @MainActor
struct SettingsStoreTests {
    @Test func refreshIntervalDefaultsToFiveSeconds() throws {
        let (store, _) = try makeStore()

        #expect(store.refreshSeconds == 5)
        #expect(store.refreshInterval == .seconds(5))
    }

    @Test func refreshIntervalPersists() throws {
        let (store, defaults) = try makeStore()
        store.refreshSeconds = 12

        #expect(store.refreshSeconds == 12)
        #expect(defaults.integer(forKey: SettingsStore.Key.refreshSeconds.rawValue) == 12)
    }

    /// Below 2s wastes cycles against a local daemon; above 30s breaks the ≤5s
    /// state promise for the rest of the session.
    @Test func refreshIntervalIsClampedInBothDirections() throws {
        let (store, _) = try makeStore()

        store.refreshSeconds = 0
        #expect(store.refreshSeconds == SettingsStore.refreshRange.lowerBound)

        store.refreshSeconds = 9999
        #expect(store.refreshSeconds == SettingsStore.refreshRange.upperBound)

        store.refreshSeconds = -5
        #expect(store.refreshSeconds == SettingsStore.refreshRange.lowerBound)
    }

    @Test func notificationPrefsDefaultConservatively() throws {
        let (store, _) = try makeStore()
        let prefs = store.notifications

        // Failures and budget trouble yes; routine success never (CFR-71).
        #expect(prefs.automationFailed)
        #expect(prefs.budgetGuardTripped)
        #expect(prefs.daemonStoppedUnexpectedly)
        #expect(prefs.updateAvailable == false)
    }

    @Test func notificationPrefsRoundTrip() throws {
        let (store, _) = try makeStore()
        var prefs = store.notifications
        prefs.automationFailed = false
        prefs.updateAvailable = true
        store.notifications = prefs

        #expect(store.notifications.automationFailed == false)
        #expect(store.notifications.updateAvailable)
    }

    @Test func notificationPrefsGateByEventKind() {
        var prefs = NotificationPrefs()

        #expect(prefs.allows(kind: "automation.fail"))
        #expect(prefs.allows(kind: "token.deny"))
        // Success is never notified, regardless of preferences.
        #expect(!prefs.allows(kind: "automation.run"))
        #expect(!prefs.allows(kind: "ingest.done"))

        prefs.automationFailed = false
        #expect(!prefs.allows(kind: "automation.fail"))
    }

    // MARK: login item

    @Test func togglingLoginItemRegistersCompanion() throws {
        let loginItem = FakeLoginItem()
        let (store, _) = try makeStore(loginItem: loginItem)
        #expect(store.launchCompanionAtLogin == false)

        store.launchCompanionAtLogin = true
        #expect(loginItem.isRegistered)

        store.launchCompanionAtLogin = false
        #expect(loginItem.isRegistered == false)
    }

    @Test func initialLoginItemStateReflectsTheRegistry() throws {
        let (store, _) = try makeStore(loginItem: FakeLoginItem(registered: true))
        #expect(store.launchCompanionAtLogin)
    }

    /// A toggle that silently fails is worse than one that refuses: the user
    /// believes Companion will launch at login and it will not.
    @Test func failedRegistrationRevertsTheToggleAndReportsWhy() throws {
        let loginItem = FakeLoginItem(failWith: LoginItemError())
        let (store, _) = try makeStore(loginItem: loginItem)

        store.launchCompanionAtLogin = true

        #expect(store.launchCompanionAtLogin == false)
        #expect(store.lastError?.contains("not signed") == true)
        #expect(loginItem.isRegistered == false)
    }

    // MARK: daemon service (CFR-11)

    @Test func daemonServiceStateComesFromTheCLINotLaunchctl() async throws {
        let cli = try makeFakeCLI()
        let (store, _) = try makeStore(cli: cli)

        #expect(await store.daemonServiceInstalled())
    }

    @Test func daemonServiceIsUnknownWithoutACLI() async throws {
        let (store, _) = try makeStore(cli: nil)
        #expect(await store.daemonServiceInstalled() == false)
    }

    @Test func serviceToggleShellsToTheCLI() async throws {
        let log = URL(fileURLWithPath: NSTemporaryDirectory())
            .appending(path: "svc-\(UUID().uuidString).log")
        let cli = try makeFakeCLI(env: ["AXON_ARGV_LOG": log.path])
        let (store, _) = try makeStore(cli: cli)

        await store.setDaemonServiceInstalled(true)
        await store.setDaemonServiceInstalled(false)

        let argv = (try? String(contentsOf: log, encoding: .utf8)) ?? ""
        #expect(argv.contains("service install"))
        #expect(argv.contains("service uninstall"))
        // Never launchctl, never a plist write.
        #expect(!argv.contains("launchctl"))
    }

    // MARK: budgets

    @Test func budgetsReadThroughConfigGet() async throws {
        let (store, _) = try makeStore(cli: try makeFakeCLI())
        let budget = try await store.budget()

        #expect(budget.day == 1_500_000)
    }

    /// `config set` echoes its value as a string while `config get` returns a
    /// number, so the set result is not a trustworthy confirmation — the store
    /// re-reads and returns reality (CONTRACT.md §10).
    @Test func setBudgetReturnsARereadNotTheSetEcho() async throws {
        let log = URL(fileURLWithPath: NSTemporaryDirectory())
            .appending(path: "budget-\(UUID().uuidString).log")
        let cli = try makeFakeCLI(env: ["AXON_ARGV_LOG": log.path])
        let (store, _) = try makeStore(cli: cli)

        let result = try await store.setBudget(day: 2_000_000, week: nil)

        let argv = (try? String(contentsOf: log, encoding: .utf8)) ?? ""
        #expect(argv.contains("config set limits.daily_tokens 2000000 --json"))
        // The fake still reports the old value: the store must surface the
        // re-read, not echo back what it asked for.
        #expect(result.day == 1_500_000)
        // A nil week must not be written at all.
        #expect(!argv.contains("limits.weekly_tokens 0"))
    }

    @Test func automationToggleRereadsTheList() async throws {
        let (store, _) = try makeStore(cli: try makeFakeCLI())
        let list = try await store.setAutomation("briefing", enabled: false)

        #expect(!list.isEmpty)
        #expect(list.contains { $0.name == "briefing" })
    }

    @Test func settingsWithoutACLIThrowRatherThanPretend() async throws {
        let (store, _) = try makeStore(cli: nil)

        await #expect(throws: AxonCLIError.self) { _ = try await store.budget() }
        await #expect(throws: AxonCLIError.self) { _ = try await store.automationList() }
    }

    @Test func onboardingCompletionPersists() throws {
        let (store, _) = try makeStore()
        #expect(store.hasCompletedOnboarding == false)

        store.hasCompletedOnboarding = true
        #expect(store.hasCompletedOnboarding)
    }

    @Test func explicitBinaryPathPersists() throws {
        let (store, _) = try makeStore()
        #expect(store.explicitBinaryPath == nil)

        store.explicitBinaryPath = "/opt/axon/bin/axon"
        #expect(store.explicitBinaryPath == "/opt/axon/bin/axon")
    }
}

/// Shared fake-binary CLI for settings tests.
func makeFakeCLI(env: [String: String] = [:]) throws -> AxonCLI {
    let url = try #require(
        Bundle.module.url(forResource: "Fixtures/fake-axon", withExtension: nil)
    )
    try? FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: url.path)
    return AxonCLI(runner: ProcessCLIRunner(environment: env), binary: url)
}
