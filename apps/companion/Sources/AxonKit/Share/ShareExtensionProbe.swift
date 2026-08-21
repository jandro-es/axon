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
