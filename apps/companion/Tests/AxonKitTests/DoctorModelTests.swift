import Foundation
import Testing

@testable import AxonKit

/// A runner whose every call times out, without waiting.
struct TimingOutRunner: CLIRunning {
    func run(binary: URL, arguments: [String], timeout: Duration) async throws -> CLIResult {
        throw AxonCLIError.timedOut(command: arguments.joined(separator: " "))
    }
    func spawnDetached(binary: URL, arguments: [String]) throws {}
}

@Suite @MainActor
struct DoctorModelTests {
    private func makeModel(env: [String: String] = [:]) throws -> DoctorModel {
        DoctorModel(
            cli: try makeFakeCLI(env: env),
            health: { try AxonJSON.decode(AxonHealth.self, from: fixture("health")) },
            environment: DiagnosticsEnvironment(
                companionVersion: "0.1.0 (1)",
                osVersion: "macOS 26.0",
                homeDirectory: "/Users/jandro"
            )
        )
    }

    @Test func runGroupsChecksByStatus() async throws {
        let model = try makeModel()
        await model.run()

        let report = try #require(model.report)
        #expect(!report.checks.isEmpty)
        #expect(report.passCount + report.warnCount + report.failCount == report.checks.count)
        #expect(model.running == false)
    }

    @Test func failingAndWarningChecksSortFirst() async throws {
        let model = try makeModel()
        await model.run()

        let sorted = try #require(model.report?.sortedBySeverity)
        let statuses = sorted.map(\.status)
        // Anything actionable must be reachable without scrolling.
        #expect(statuses == statuses.sorted { rank($0) < rank($1) })
    }

    private func rank(_ status: DoctorCheck.Status) -> Int {
        switch status {
        case .fail: 0
        case .warn: 1
        case .ok: 2
        }
    }

    /// Doctor is most useful when the daemon is down — that is its job. A
    /// failed health read must not stop the report from rendering.
    @Test func reportStillRendersWhenHealthIsUnreachable() async throws {
        let model = DoctorModel(
            cli: try makeFakeCLI(),
            health: { throw DashboardError.unreachable },
            environment: .init(companionVersion: "0.1.0", osVersion: "macOS 26.0", homeDirectory: "/Users/jandro")
        )
        await model.run()

        #expect(model.report?.checks.isEmpty == false)
    }

    @Test func withoutACLIItSaysSoRatherThanShowingNothing() async {
        let model = DoctorModel(cli: nil, health: { throw DashboardError.unreachable })
        await model.run()

        #expect(model.report == nil)
        #expect(model.error?.isEmpty == false)
    }

    // MARK: diagnostics bundle (CFR-61)

    @Test func diagnosticsIncludeVersionsAndCheckNames() async throws {
        let model = try makeModel()
        await model.run()
        let text = await model.diagnosticsText()

        #expect(text.contains("0.1.0 (1)"))
        #expect(text.contains("macOS 26.0"))
        #expect(text.contains("claude-cli"))
        #expect(text.contains("/health"))
    }

    /// Belt and braces: none of the sources should carry a secret, but the
    /// bundle is what the user pastes into a public issue, so it is scanned
    /// anyway (CFR-61).
    @Test func plantedSecretsAreRedacted() async throws {
        let model = DoctorModel(
            cli: try makeFakeCLI(),
            health: {
                try AxonJSON.decode(
                    AxonHealth.self,
                    from: Data(#"""
                    {"version":"1.3.2",
                     "leaked_key":"sk-ant-api03-AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIIIJJJJKKKKLLLL",
                     "leaked_token":"oauth_tok_9f8e7d6c5b4a3f2e1d0c9b8a7f6e5d4c3b2a1f0e"}
                    """#.utf8)
                )
            },
            environment: .init(companionVersion: "0.1.0", osVersion: "macOS 26.0", homeDirectory: "/Users/jandro")
        )
        await model.run()
        let text = await model.diagnosticsText()

        #expect(!text.contains("sk-ant-api03-AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIIIJJJJKKKKLLLL"))
        #expect(!text.contains("oauth_tok_9f8e7d6c5b4a3f2e1d0c9b8a7f6e5d4c3b2a1f0e"))
        #expect(text.contains(DiagnosticsRedactor.placeholder))
    }

    @Test func redactorCatchesEachSecretShape() {
        let cases = [
            "sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH",
            "OAUTH_TOKEN=abcdefghijklmnopqrstuvwxyz0123456789",
            "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk",
        ]
        for secret in cases {
            let redacted = DiagnosticsRedactor.redact(secret, homeDirectory: "/Users/x")
            #expect(!redacted.contains(secret), "not redacted: \(secret)")
        }
    }

    /// A home directory in a path is not a secret, but it is a real name — and
    /// the bundle is meant to be shareable.
    @Test func homeDirectoryIsRewrittenToTilde() {
        let text = "vault: /Users/jandro/Notes/Personal\nlogs: /Users/jandro/.axon/logs"
        let redacted = DiagnosticsRedactor.redact(text, homeDirectory: "/Users/jandro")

        #expect(!redacted.contains("/Users/jandro"))
        #expect(redacted.contains("~/Notes/Personal"))
    }

    /// Ordinary content must survive — an over-eager redactor that eats the
    /// report is as useless as one that leaks.
    @Test func ordinaryDiagnosticTextIsUntouched() {
        let text = """
            claude-cli: Claude Code CLI found
            ollama: Ollama found
            vision: provider "apple" requires macOS 27 on-device image input
            version: v1.3.1-1-gec42a3a
            """
        #expect(DiagnosticsRedactor.redact(text, homeDirectory: "/Users/x") == text)
    }

    /// A commit hash is a 40-char hex run — exactly the shape a naive
    /// "long random string" rule would eat.
    @Test func commitHashesAreNotMistakenForSecrets() {
        let text = "commit: ec42a3a9f8e7d6c5b4a3f2e1d0c9b8a7f6e5d4c3"
        #expect(DiagnosticsRedactor.redact(text, homeDirectory: "/Users/x") == text)
    }

    /// An AXON predating the machine-readable report must say so in words the
    /// user can act on — not surface `unknown flag: --json` (CFR-82).
    @Test func anOlderDaemonDegradesWithAnActionableMessage() async throws {
        let model = try makeModel(env: ["AXON_FAKE_DOCTOR_NO_JSON": "1"])
        await model.run()

        #expect(model.report == nil)
        #expect(model.isDaemonTooOld)
        let message = try #require(model.error)
        #expect(!message.contains("unknown flag"))
        #expect(message.contains("axon doctor"))
    }

    /// A genuine failure on a capable daemon must surface verbatim — the age
    /// check must not swallow real errors.
    @Test func aRealFailureOnACapableDaemonIsNotBlamedOnAge() async throws {
        let model = try makeModel(env: ["AXON_FAKE_DOCTOR_BROKEN": "1"])
        await model.run()

        #expect(model.isDaemonTooOld == false)
        #expect(model.error?.contains("config not loaded") == true)
    }

    /// A binary that hangs fails the capability probe too. Reporting that as
    /// "your AXON is old" would send the user down entirely the wrong path.
    @Test func aTimeoutIsReportedAsATimeoutNotAsAge() async throws {
        let model = DoctorModel(
            cli: AxonCLI(
                runner: TimingOutRunner(),
                binary: URL(fileURLWithPath: "/usr/local/bin/axon")
            ),
            health: { throw DashboardError.unreachable }
        )
        await model.run()

        #expect(model.isDaemonTooOld == false)
        #expect(model.error?.contains("did not respond") == true)
    }

    // MARK: capability probe

    /// 2.0 P5 adds `axon doctor --bundle`. Branch on the real capability now
    /// rather than leaving a TODO that nobody notices when it lands.
    @Test func bundleSupportIsProbedNotAssumed() async throws {
        let model = try makeModel()
        #expect(await model.supportsBundleFlag() == false)

        let withBundle = try makeModel(env: ["AXON_FAKE_DOCTOR_BUNDLE": "1"])
        #expect(await withBundle.supportsBundleFlag())
    }
}
