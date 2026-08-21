import Foundation
import Testing

@testable import AxonKit

/// A runner that replays one canned `pluginkit` result.
private struct CannedRunner: CLIRunning {
    var stdout: String = ""
    var exitCode: Int32 = 0

    func run(binary: URL, arguments: [String], timeout: Duration) async throws -> CLIResult {
        CLIResult(exitCode: exitCode, stdout: Data(stdout.utf8), stderr: "")
    }
    func spawnDetached(binary: URL, arguments: [String]) throws {}
}

private struct ThrowingRunner: CLIRunning {
    func run(binary: URL, arguments: [String], timeout: Duration) async throws -> CLIResult {
        throw AxonCLIError.timedOut(command: "pluginkit")
    }
    func spawnDetached(binary: URL, arguments: [String]) throws {}
}

/// Real `pluginkit -mAvvv -i <id>` output, captured on macOS 27.
private let enabledOutput = """
+    com.axon.companion.share(0.3.0)
\t            Path = /Applications/Axon.app/Contents/PlugIns/AxonShare.appex
\t            UUID = 2AC1D7D1-FDE5-4E7F-90A9-45C517BE9A42
"""

private let disabledOutput = """
     com.axon.companion.share(0.3.0)
\t            Path = /Applications/Axon.app/Contents/PlugIns/AxonShare.appex
"""

/// CFR-99 — the Share-menu entry's state, so "nothing happened" can say why.
@Suite
struct ShareExtensionProbeTests {
    @Test func leadingPlusMeansEnabled() {
        #expect(ShareExtensionProbe.parse(enabledOutput) == .enabled)
    }

    @Test func noPrefixMeansRegisteredButOff() {
        #expect(ShareExtensionProbe.parse(disabledOutput) == .registeredButDisabled)
    }

    @Test func noMatchesMeansNotRegistered() {
        // pluginkit's literal answer when the bundle id is unknown — and it
        // exits 0, so the exit code cannot be used to detect this.
        #expect(ShareExtensionProbe.parse("  (no matches)\n") == .notRegistered)
        #expect(ShareExtensionProbe.parse("") == .notRegistered)
    }

    @Test func probeReadsTheRunnersOutput() async {
        let probe = ShareExtensionProbe(runner: CannedRunner(stdout: enabledOutput))
        #expect(await probe.state() == .enabled)
    }

    @Test func nonZeroExitIsUnknownNotAClaim() async {
        let probe = ShareExtensionProbe(runner: CannedRunner(stdout: "", exitCode: 1))
        #expect(await probe.state() == .unknown)
    }

    @Test func aFailedRunIsUnknown() async {
        let probe = ShareExtensionProbe(runner: ThrowingRunner())
        #expect(await probe.state() == .unknown)
    }
}
