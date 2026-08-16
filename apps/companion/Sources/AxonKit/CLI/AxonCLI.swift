import Foundation

/// Typed wrapper over the `axon` CLI (CONTRACT.md §10).
///
/// Every mutation Companion performs goes through here — Companion never edits
/// `config.yaml`, never touches `launchctl`, and never reads `.env`.
public struct AxonCLI: Sendable {
    private let runner: CLIRunning
    private let binary: URL
    private let profile: String?
    private let configPath: String?
    private let envPath: String?

    /// Reads are fast and must not block the UI; a hung read would stall the
    /// icon's ≤5s state detection (CFR-01).
    public static let readTimeout: Duration = .seconds(15)
    /// Lifecycle commands touch the filesystem and launchd — allow more.
    public static let mutateTimeout: Duration = .seconds(60)

    /// - Parameter pathEnv: PATH for child processes. Defaults to the widened
    ///   fallback, because a GUI app inherits a PATH too minimal to find the
    ///   user's tools — see `ProcessCLIRunner.resolvedPath`.
    public init(
        binary: URL,
        pathEnv: String?,
        profile: String? = nil,
        configPath: String? = nil,
        envPath: String? = nil
    ) {
        self.init(
            runner: ProcessCLIRunner(
                environment: ["PATH": ProcessCLIRunner.resolvedPath(preferring: pathEnv)]
            ),
            binary: binary, profile: profile, configPath: configPath, envPath: envPath
        )
    }

    public init(
        runner: CLIRunning = ProcessCLIRunner(),
        binary: URL,
        profile: String? = nil,
        configPath: String? = nil,
        envPath: String? = nil
    ) {
        self.runner = runner
        self.binary = binary
        self.profile = profile
        self.configPath = configPath
        self.envPath = envPath
    }

    // MARK: reads

    public func status() async throws -> DaemonStatus {
        try await json(DaemonStatus.self, ["status", "--json"])
    }

    /// `doctor` works with the daemon **stopped** — that is its job.
    ///
    /// It also exits non-zero on a failing check while still writing the full
    /// report, so this parses stdout regardless of exit code. Gating on the
    /// code would blank the Doctor window exactly when it is needed most.
    public func doctor() async throws -> DoctorReport {
        // Note `runRaw`, not `run`: `run` throws on a non-zero exit, which for
        // doctor is the *normal* path when a check fails.
        let result = try await runRaw(["doctor", "--json"], timeout: Self.readTimeout)
        do {
            return try AxonJSON.decode(DoctorReport.self, from: result.stdout)
        } catch {
            // Only now is a non-zero exit meaningful: no JSON *and* a failure.
            if !result.succeeded {
                throw AxonCLIError.failed(
                    command: "doctor", stderr: result.stderr, exitCode: result.exitCode
                )
            }
            throw AxonCLIError.decoding(command: "doctor", detail: String(describing: error))
        }
    }

    /// Whether this daemon's `doctor` has `--json` at all.
    ///
    /// Companion needs it for the Doctor window; daemons older than the seam
    /// that added it simply do not have it, and "unknown flag: --json" is not
    /// something a user can act on.
    public func doctorSupportsJSON() async -> Bool {
        await doctorHelpMentions("--json")
    }

    /// Whether this daemon supports `axon doctor --bundle` (2.0 P5).
    ///
    /// Probed from `--help` rather than assumed, so the richer bundle is picked
    /// up the moment the daemon gains it, with no Companion change.
    public func doctorSupportsBundle() async -> Bool {
        await doctorHelpMentions("--bundle")
    }

    private func doctorHelpMentions(_ flag: String) async -> Bool {
        guard let result = try? await runRaw(["doctor", "--help"], timeout: Self.readTimeout)
        else { return false }
        return result.stdoutText.contains(flag) || result.stderr.contains(flag)
    }

    public func automations() async throws -> [AutomationInfo] {
        try await json([AutomationInfo].self, ["automations", "--json"])
    }

    /// Whether the daemon's OS service unit is installed, straight from the
    /// CLI — Companion never stats a plist or shells to launchctl (CFR-11).
    public func serviceStatus() async throws -> ServiceStatus {
        try await json(ServiceStatus.self, ["service", "status", "--json"])
    }

    public func profiles() async throws -> [ProfileInfo] {
        try await json([ProfileInfo].self, ["profiles", "--json"])
    }

    public func version() async throws -> String {
        try await run(["version", "--short"], timeout: Self.readTimeout)
            .stdoutText.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    // MARK: config

    public func configGet(_ key: String) async throws -> JSONValue {
        let value = try await json(ConfigValue.self, ["config", "get", key, "--json"])
        return value.value ?? .null
    }

    @discardableResult
    public func configSet(_ key: String, _ value: String) async throws -> ConfigSetResult {
        try await json(ConfigSetResult.self, ["config", "set", key, value, "--json"],
                       timeout: Self.mutateTimeout)
    }

    /// Toggling an automation writes `automations.<name>.enabled`. Callers
    /// re-read afterwards and render reality — never an optimistic local value,
    /// because policy can veto the config (`allowed: false`).
    @discardableResult
    public func setAutomation(_ name: String, enabled: Bool) async throws -> ConfigSetResult {
        try await configSet("automations.\(name).enabled", enabled ? "true" : "false")
    }

    // MARK: lifecycle

    /// `axon start` runs in the **foreground until interrupted**, so it is
    /// spawned detached. Confirmation comes from polling `/health`, never from
    /// awaiting this call (CONTRACT.md §10).
    public func start() async throws {
        try runner.spawnDetached(binary: binary, arguments: globalFlags(["start"]))
    }

    public func stop() async throws {
        _ = try await run(["stop"], timeout: Self.mutateTimeout)
    }

    /// Installs the launchd/systemd unit. The CLI owns service semantics;
    /// Companion never runs `launchctl` or writes a plist (CFR-11).
    public func serviceInstall() async throws {
        _ = try await run(["service", "install"], timeout: Self.mutateTimeout)
    }

    public func serviceUninstall() async throws {
        _ = try await run(["service", "uninstall"], timeout: Self.mutateTimeout)
    }

    /// No `--json`; the raw output is surfaced to the user as-is.
    public func update() async throws -> CLIResult {
        try await run(["update"], timeout: .seconds(300))
    }

    // MARK: plumbing

    private func json<T: Decodable>(
        _ type: T.Type, _ arguments: [String], timeout: Duration = AxonCLI.readTimeout
    ) async throws -> T {
        let result = try await run(arguments, timeout: timeout)
        do {
            return try AxonJSON.decode(type, from: result.stdout)
        } catch {
            throw AxonCLIError.decoding(
                command: arguments.joined(separator: " "), detail: String(describing: error)
            )
        }
    }

    /// Runs without interpreting the exit code. Only `doctor` wants this.
    private func runRaw(_ arguments: [String], timeout: Duration) async throws -> CLIResult {
        try await runner.run(binary: binary, arguments: globalFlags(arguments), timeout: timeout)
    }

    @discardableResult
    private func run(_ arguments: [String], timeout: Duration) async throws -> CLIResult {
        let result = try await runRaw(arguments, timeout: timeout)

        guard result.succeeded else {
            throw AxonCLIError.failed(
                command: arguments.joined(separator: " "),
                stderr: result.stderr,
                exitCode: result.exitCode
            )
        }
        return result
    }

    /// Appends the profile/config/env overrides when Companion has them. They
    /// go **after** the subcommand because they are persistent flags.
    private func globalFlags(_ arguments: [String]) -> [String] {
        var full = arguments
        if let profile { full += ["--profile", profile] }
        if let configPath { full += ["--config", configPath] }
        if let envPath { full += ["--env", envPath] }
        return full
    }
}
