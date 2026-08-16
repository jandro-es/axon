import Foundation

/// Facts about the running machine that go into a diagnostics bundle.
/// Injected so the bundle is testable and contains nothing else by accident.
public struct DiagnosticsEnvironment: Sendable {
    public let companionVersion: String
    public let osVersion: String
    public let homeDirectory: String

    public init(companionVersion: String, osVersion: String, homeDirectory: String) {
        self.companionVersion = companionVersion
        self.osVersion = osVersion
        self.homeDirectory = homeDirectory
    }

    public static func current(bundle: Bundle = .main) -> DiagnosticsEnvironment {
        let info = bundle.infoDictionary
        let short = info?["CFBundleShortVersionString"] as? String ?? "0.0.0"
        let build = info?["CFBundleVersion"] as? String ?? "0"
        let os = ProcessInfo.processInfo.operatingSystemVersion
        return DiagnosticsEnvironment(
            companionVersion: "\(short) (\(build))",
            osVersion: "macOS \(os.majorVersion).\(os.minorVersion).\(os.patchVersion)",
            homeDirectory: FileManager.default.homeDirectoryForCurrentUser.path
        )
    }
}

/// Scrubs a diagnostics bundle before it leaves the machine.
///
/// None of the sources *should* carry a secret — Companion never reads `.env`,
/// and the daemon does not put credentials in `doctor` or `/health`. This runs
/// anyway because the bundle's whole purpose is to be pasted somewhere public,
/// and a redactor that is never needed costs nothing (CFR-61).
public enum DiagnosticsRedactor {
    public static let placeholder = "[REDACTED]"

    /// Patterns are deliberately shape-specific rather than "any long random
    /// string": a git commit is a 40-char hex run and a model id is a long
    /// dashed token, so a generic entropy rule would eat the report itself.
    private static let patterns: [String] = [
        // Anthropic-style API keys.
        #"sk-[A-Za-z0-9_\-]{16,}"#,
        // Anything self-describing as a token/secret/key, followed by a value
        // that actually looks like a credential.
        //
        // The value pattern is deliberately strict. A looser `\S+` matched the
        // doctor check NAMED "anthropic-api-key" followed by ": no stray…" and
        // redacted the words "api-key: no", mangling a passing check into
        // `anthropic-[REDACTED] stray ANTHROPIC_API_KEY`. Over-eager is not the
        // safe direction to err in: it can hide the very finding the bundle was
        // pasted to explain.
        #"(?i)\b(?:oauth[_-]?token|access[_-]?token|api[_-]?key|secret|password|bearer)\b\s*[:=]\s*[A-Za-z0-9_\-\.\+/]{16,}"#,
        // OAuth-ish opaque tokens with a recognisable prefix.
        #"(?i)\boauth[_A-Za-z0-9\-]*_[A-Za-z0-9]{20,}"#,
        // JWTs — three base64url segments separated by dots.
        #"\beyJ[A-Za-z0-9_\-]{6,}\.[A-Za-z0-9_\-]{6,}\.[A-Za-z0-9_\-]{6,}"#,
    ]

    public static func redact(_ text: String, homeDirectory: String) -> String {
        var out = text
        for pattern in patterns {
            out = out.replacingOccurrences(
                of: pattern, with: placeholder, options: [.regularExpression]
            )
        }
        // Not a secret, but a real person's name in a path, and the bundle is
        // meant to be shareable.
        if !homeDirectory.isEmpty {
            out = out.replacingOccurrences(of: homeDirectory, with: "~")
        }
        return out
    }
}

/// Backs the Doctor window (CFR-60/61).
@MainActor
@Observable
public final class DoctorModel {
    public private(set) var report: DoctorReport?
    public private(set) var running = false
    public private(set) var error: String?
    /// The `/health` payload, included in the bundle. Absent when the daemon
    /// is down — which does not stop the report.
    public private(set) var health: AxonHealth?
    /// True when this AXON predates the machine-readable doctor report.
    public private(set) var isDaemonTooOld = false

    static let tooOldMessage =
        "This version of AXON doesn't provide a machine-readable doctor report. "
        + "Update AXON to see checks here — or run `axon doctor` in Terminal, "
        + "which works on every version."

    private let cli: AxonCLI?
    private let healthReader: @Sendable () async throws -> AxonHealth
    private let environment: DiagnosticsEnvironment
    private var bundleFlagSupported: Bool?

    public init(
        cli: AxonCLI?,
        health: @escaping @Sendable () async throws -> AxonHealth,
        environment: DiagnosticsEnvironment = .current()
    ) {
        self.cli = cli
        self.healthReader = health
        self.environment = environment
    }

    public func run() async {
        guard let cli else {
            error = "No `axon` binary found. Install AXON, or set its location in Settings."
            return
        }
        running = true
        defer { running = false }

        do {
            // doctor works with the daemon stopped — that is the point of it.
            report = try await cli.doctor()
            error = nil
        } catch let failure as AxonCLIError {
            // Distinguish "this AXON is too old" from a real failure. Without
            // the probe the user sees `unknown flag: --json`, which is not
            // something anyone can act on (CFR-82).
            //
            // Only a clean non-zero exit is worth probing: a binary that hung
            // or could not be executed would fail the probe too, and reporting
            // that as "your AXON is old" would send the user down the wrong path.
            if case .failed = failure, await cli.doctorSupportsJSON() == false {
                error = Self.tooOldMessage
                isDaemonTooOld = true
            } else {
                error = failure.message
            }
        } catch {
            self.error = error.localizedDescription
        }
        // Health is a bonus for the bundle, never a precondition.
        health = try? await healthReader()
    }

    /// The redacted bundle the Copy Diagnostics button puts on the pasteboard.
    public func diagnosticsText() async -> String {
        var lines: [String] = [
            "AXON diagnostics",
            "================",
            "Companion:  \(environment.companionVersion)",
            "macOS:      \(environment.osVersion)",
            "Daemon:     \(AxonFormat.version(health?.version))",
            "Profile:    \(report?.profile ?? health?.profile ?? "unknown")",
            "",
        ]

        if let report {
            lines.append("axon doctor — \(report.status.rawValue)")
            lines.append(String(repeating: "-", count: 40))
            for check in report.sortedBySeverity {
                lines.append("[\(check.status.rawValue.uppercased())] \(check.name): \(check.detail)")
                if let fix = check.remediation {
                    lines.append("    fix: \(fix)")
                }
            }
            if let configError = report.error {
                lines.append("config error: \(configError)")
            }
        } else {
            lines.append("axon doctor — unavailable: \(error ?? "unknown")")
        }

        lines.append("")
        lines.append("/health")
        lines.append(String(repeating: "-", count: 40))
        if let health, let encoded = Self.encode(health) {
            lines.append(encoded)
        } else {
            lines.append("unreachable (the daemon is not serving 127.0.0.1:7777)")
        }

        return DiagnosticsRedactor.redact(
            lines.joined(separator: "\n"), homeDirectory: environment.homeDirectory
        )
    }

    /// Whether this daemon has `axon doctor --bundle` (2.0 P5).
    ///
    /// Probed, not assumed, and cached: when the flag ships, Companion picks it
    /// up on the next launch with no code change. A TODO here would go stale
    /// silently; a capability check simply starts returning true.
    public func supportsBundleFlag() async -> Bool {
        if let bundleFlagSupported { return bundleFlagSupported }
        guard let cli else { return false }
        let supported = await cli.doctorSupportsBundle()
        bundleFlagSupported = supported
        return supported
    }

    private static func encode(_ health: AxonHealth) -> String? {
        // Re-encode rather than keeping the raw bytes: this guarantees the
        // bundle contains only fields Companion actually models, so a future
        // daemon field cannot land in a pasted bundle unreviewed.
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        guard let data = try? encoder.encode(health) else { return nil }
        return String(decoding: data, as: UTF8.self)
    }
}
