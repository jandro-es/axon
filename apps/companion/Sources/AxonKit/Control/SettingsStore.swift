import Foundation
import ServiceManagement

/// Which events may raise a user notification. Conservative by default:
/// failures and budget trouble only, never routine success (CFR-71).
public struct NotificationPrefs: Equatable, Sendable, Codable {
    public var automationFailed = true
    public var budgetGuardTripped = true
    public var daemonStoppedUnexpectedly = true
    public var updateAvailable = false

    public init() {}

    /// Whether a given event kind may notify.
    public func allows(kind: String) -> Bool {
        switch kind {
        case "automation.fail": automationFailed
        case "token.deny", "token.defer", "token.downgrade": budgetGuardTripped
        default: false
        }
    }
}

/// Companion's own preferences, plus the two daemon-side toggles it owns.
///
/// The two "at login" toggles are deliberately **separate** (CFR-11):
/// Companion's own login item is an `SMAppService` registration, while the
/// daemon's is a launchd unit the `axon` CLI owns. Conflating them would mean
/// quitting Companion silently stopped the user's automations.
@MainActor
@Observable
public final class SettingsStore {
    public enum Key: String {
        case refreshSeconds = "com.axon.companion.refreshSeconds"
        case notifications = "com.axon.companion.notifications"
        case explicitBinaryPath = "com.axon.companion.binaryPath"
        case hasCompletedOnboarding = "com.axon.companion.hasCompletedOnboarding"
    }

    /// How often to poll `/health`. Bounded: below 2s is wasteful against a
    /// local daemon, above 30s breaks the ≤5s state promise for good.
    public static let refreshRange: ClosedRange<Int> = 2...30
    public static let defaultRefreshSeconds = 5

    private let defaults: UserDefaults
    private let cli: AxonCLI?
    /// Injected so tests do not touch the real login-item registry, which
    /// requires a signed, bundled app and fails inside a test runner.
    private let loginItem: LoginItemControlling

    public init(
        defaults: UserDefaults = .standard,
        cli: AxonCLI? = nil,
        loginItem: LoginItemControlling = SMAppServiceLoginItem()
    ) {
        self.defaults = defaults
        self.cli = cli
        self.loginItem = loginItem
        self.launchAtLoginBacking = loginItem.isRegistered
    }

    // MARK: Companion preferences

    /// Backing store. The public property below is computed rather than using
    /// `didSet`: reverting a failed toggle from inside its own observer
    /// re-enters the observer and recurses until the stack overflows.
    private var launchAtLoginBacking = false

    /// Registers Companion itself as a login item. Failures surface in
    /// ``lastError`` and the toggle snaps back — never a switch that lies.
    public var launchCompanionAtLogin: Bool {
        get { launchAtLoginBacking }
        set {
            guard newValue != launchAtLoginBacking else { return }
            do {
                try loginItem.setRegistered(newValue)
                launchAtLoginBacking = newValue
                lastError = nil
            } catch {
                // Leave the backing value untouched so the switch snaps back.
                lastError = "Could not change the login item: \(error.localizedDescription)"
            }
        }
    }

    public var refreshSeconds: Int {
        get {
            let stored = defaults.integer(forKey: Key.refreshSeconds.rawValue)
            guard stored != 0 else { return Self.defaultRefreshSeconds }
            return min(max(stored, Self.refreshRange.lowerBound), Self.refreshRange.upperBound)
        }
        set {
            let clamped = min(max(newValue, Self.refreshRange.lowerBound), Self.refreshRange.upperBound)
            defaults.set(clamped, forKey: Key.refreshSeconds.rawValue)
        }
    }

    public var refreshInterval: Duration { .seconds(refreshSeconds) }

    public var notifications: NotificationPrefs {
        get {
            guard let data = defaults.data(forKey: Key.notifications.rawValue),
                  let decoded = try? JSONDecoder().decode(NotificationPrefs.self, from: data)
            else { return NotificationPrefs() }
            return decoded
        }
        set {
            guard let data = try? JSONEncoder().encode(newValue) else { return }
            defaults.set(data, forKey: Key.notifications.rawValue)
        }
    }

    /// An explicit `axon` path, for installs outside the known locations.
    public var explicitBinaryPath: String? {
        get { defaults.string(forKey: Key.explicitBinaryPath.rawValue) }
        set { defaults.set(newValue, forKey: Key.explicitBinaryPath.rawValue) }
    }

    public var hasCompletedOnboarding: Bool {
        get { defaults.bool(forKey: Key.hasCompletedOnboarding.rawValue) }
        set { defaults.set(newValue, forKey: Key.hasCompletedOnboarding.rawValue) }
    }

    /// The most recent user-facing failure from a settings change.
    public var lastError: String?

    // MARK: daemon-side settings

    /// Whether the daemon's OS service unit is installed — i.e. whether AXON
    /// itself starts at login. Distinct from ``launchCompanionAtLogin``.
    public func daemonServiceInstalled() async -> Bool {
        guard let cli else { return false }
        return (try? await cli.serviceStatus())?.installed == true
    }

    public func setDaemonServiceInstalled(_ install: Bool) async {
        guard let cli else {
            lastError = "no axon binary found"
            return
        }
        do {
            if install {
                try await cli.serviceInstall()
            } else {
                try await cli.serviceUninstall()
            }
        } catch {
            lastError = (error as? AxonCLIError)?.message ?? error.localizedDescription
        }
    }

    // MARK: budgets (CFR-40)

    public func budget() async throws -> (day: Int?, week: Int?) {
        guard let cli else { throw AxonCLIError.notExecutable(path: "axon") }
        return (
            day: try await cli.configGet("limits.daily_tokens").intValue,
            week: try await cli.configGet("limits.weekly_tokens").intValue
        )
    }

    /// Writes budgets, then **re-reads** them. `config set` echoes its value as
    /// a string while `config get` returns a number (CONTRACT.md §10), so the
    /// set result is not a trustworthy confirmation — reality is.
    public func setBudget(day: Int?, week: Int?) async throws -> (day: Int?, week: Int?) {
        guard let cli else { throw AxonCLIError.notExecutable(path: "axon") }
        if let day { try await cli.configSet("limits.daily_tokens", String(day)) }
        if let week { try await cli.configSet("limits.weekly_tokens", String(week)) }
        return try await budget()
    }

    public func automationList() async throws -> [AutomationInfo] {
        guard let cli else { throw AxonCLIError.notExecutable(path: "axon") }
        return try await cli.automations()
    }

    /// Toggles an automation and returns the daemon's actual resulting state.
    /// Never optimistic: policy can veto the config (`allowed: false`).
    public func setAutomation(_ name: String, enabled: Bool) async throws -> [AutomationInfo] {
        guard let cli else { throw AxonCLIError.notExecutable(path: "axon") }
        try await cli.setAutomation(name, enabled: enabled)
        return try await cli.automations()
    }
}

// MARK: - login item

/// Registers the app as a login item. A protocol so tests can avoid the real
/// registry, which needs a signed bundled app.
public protocol LoginItemControlling: Sendable {
    var isRegistered: Bool { get }
    func setRegistered(_ registered: Bool) throws
}

/// `SMAppService.mainApp` — Companion's own login item, never the daemon's.
public struct SMAppServiceLoginItem: LoginItemControlling {
    public init() {}

    public var isRegistered: Bool {
        SMAppService.mainApp.status == .enabled
    }

    public func setRegistered(_ registered: Bool) throws {
        if registered {
            try SMAppService.mainApp.register()
        } else {
            try SMAppService.mainApp.unregister()
        }
    }
}
