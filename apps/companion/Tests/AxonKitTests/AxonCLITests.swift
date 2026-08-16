import Foundation
import Testing

@testable import AxonKit

/// The bundled fake `axon`. SwiftPM copies resources without the exec bit, so
/// restore it before use.
private func fakeAxonURL() throws -> URL {
    let url = try #require(
        Bundle.module.url(forResource: "Fixtures/fake-axon", withExtension: nil),
        "missing Fixtures/fake-axon"
    )
    try? FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: url.path)
    return url
}

private func makeCLI(env: [String: String] = [:]) throws -> AxonCLI {
    AxonCLI(runner: ProcessCLIRunner(environment: env), binary: try fakeAxonURL())
}

// MARK: - CLIRunner

@Suite struct CLIRunnerTests {
    @Test func capturesStdoutAndExitCode() async throws {
        let result = try await ProcessCLIRunner().run(
            binary: try fakeAxonURL(), arguments: ["status"], timeout: .seconds(10)
        )

        #expect(result.exitCode == 0)
        #expect(!result.stdout.isEmpty)
        #expect(result.stderr.isEmpty)
    }

    /// The daemon's convention: errors go to stderr, stdout stays empty, exit
    /// is non-zero (CONTRACT.md §10). The runner must preserve all three.
    @Test func failureKeepsStdoutCleanAndCapturesStderr() async throws {
        let result = try await ProcessCLIRunner().run(
            binary: try fakeAxonURL(), arguments: ["boom"], timeout: .seconds(10)
        )

        #expect(result.exitCode == 1)
        #expect(result.stdout.isEmpty)
        #expect(result.stderr.contains("the daemon exploded"))
    }

    @Test func timeoutKillsTheProcess() async throws {
        let start = ContinuousClock.now
        await #expect(throws: AxonCLIError.self) {
            _ = try await ProcessCLIRunner(environment: ["AXON_FAKE_HANG": "1"]).run(
                binary: try fakeAxonURL(), arguments: ["status"], timeout: .milliseconds(300)
            )
        }
        // It must actually kill the child, not wait out the 120s sleep.
        #expect(ContinuousClock.now - start < .seconds(10))
    }

    @Test func missingBinarySurfacesAsNotFound() async throws {
        await #expect(throws: AxonCLIError.self) {
            _ = try await ProcessCLIRunner().run(
                binary: URL(fileURLWithPath: "/nonexistent/axon"),
                arguments: ["status"], timeout: .seconds(5)
            )
        }
    }
}

// MARK: - BinaryLocator

@Suite struct BinaryLocatorTests {
    @Test func explicitSettingWinsWhenItExists() throws {
        let fake = try fakeAxonURL()
        let located = BinaryLocator.locate(explicit: fake.path, searchPaths: ["/usr/local/bin"])

        #expect(located == fake)
    }

    @Test func explicitSettingIsIgnoredWhenItDoesNotExist() throws {
        // A stale setting must fall through to discovery, not brick the app.
        let fake = try fakeAxonURL()
        let located = BinaryLocator.locate(
            explicit: "/nonexistent/axon",
            searchPaths: [fake.deletingLastPathComponent().path],
            names: ["fake-axon"]
        )

        #expect(located == fake)
    }

    @Test func searchesInOrder() throws {
        let fake = try fakeAxonURL()
        let dir = fake.deletingLastPathComponent().path
        let located = BinaryLocator.locate(
            explicit: nil,
            searchPaths: ["/nonexistent/a", dir, "/nonexistent/b"],
            names: ["fake-axon"]
        )

        #expect(located == fake)
    }

    @Test func returnsNilWhenNothingIsFound() {
        // Pin the inherited PATH too, or this finds the developer's own axon.
        #expect(BinaryLocator.locate(
            explicit: nil, searchPaths: ["/nonexistent"], inheritedPath: "/nonexistent"
        ) == nil)
    }

    /// The inherited PATH is the last resort: present when Companion is
    /// launched from a terminal, usually absent for a Finder launch.
    @Test func fallsBackToTheInheritedPath() throws {
        let fake = try fakeAxonURL()
        let located = BinaryLocator.locate(
            explicit: nil, searchPaths: ["/nonexistent"], names: ["fake-axon"],
            inheritedPath: "/nonexistent:\(fake.deletingLastPathComponent().path)"
        )

        #expect(located == fake)
    }

    /// The default order is documented in CONTRACT.md §10 and matters: a
    /// Homebrew install and a curl install can both be present.
    @Test func defaultSearchPathsCoverBothInstallLocations() {
        #expect(BinaryLocator.defaultSearchPaths.contains("/usr/local/bin"))
        #expect(BinaryLocator.defaultSearchPaths.contains("/opt/homebrew/bin"))
        #expect(BinaryLocator.defaultSearchPaths.firstIndex(of: "/usr/local/bin")
            .map { index in
                index < (BinaryLocator.defaultSearchPaths.firstIndex(of: "/opt/homebrew/bin") ?? .max)
            } == true)
    }
}

// MARK: - AxonCLI

@Suite struct AxonCLITests {
    @Test func statusDecodesPascalCaseJSON() async throws {
        let status = try await makeCLI().status()

        // `axon status --json` is the one PascalCase payload (CONTRACT.md §10).
        #expect(status.profile == "personal")
        #expect(status.day?.used == 4024)
        #expect(status.week?.limit == 8_000_000)
        #expect(status.guardPaused == false)
    }

    @Test func doctorDecodesChecksAndOverallStatus() async throws {
        let report = try await makeCLI().doctor()

        #expect(report.profile == "personal")
        #expect(report.status == .ok)
        #expect(!report.checks.isEmpty)
        #expect(report.checks.contains { $0.name == "claude-cli" })
        #expect(report.checks.allSatisfy { !$0.detail.isEmpty })
    }

    /// `doctor` exits non-zero on a failing check while still writing the full
    /// report to stdout. Parsing must not be gated on the exit code, or the
    /// Doctor window would be blank exactly when it is needed most.
    @Test func doctorParsesReportEvenWhenItExitsNonZero() async throws {
        let cli = try makeCLI(env: ["AXON_FAKE_DOCTOR_FAIL": "1"])
        let report = try await cli.doctor()

        #expect(!report.checks.isEmpty)
    }

    @Test func doctorGroupsChecksByStatus() async throws {
        let report = try await makeCLI().doctor()

        #expect(report.passCount + report.warnCount + report.failCount == report.checks.count)
        #expect(report.warnCount > 0)  // the captured report has a vision warning
    }

    @Test func automationsDecodeWithEffectiveAndConfiguredState() async throws {
        let automations = try await makeCLI().automations()

        #expect(automations.count > 10)
        let consolidate = try #require(automations.first { $0.name == "actions-consolidate" })
        #expect(consolidate.enabled == true)
        #expect(consolidate.schedule == "0 7 * * *")
        #expect(consolidate.model == "none")
        #expect(consolidate.lastRun?.status == "skipped")

        // A never-run automation has no last_run at all.
        let review = try #require(automations.first { $0.name == "actions-review" })
        #expect(review.lastRun == nil)
    }

    /// An automation the profile policy forbids must render as a disabled
    /// toggle with a reason — flipping its config key would be a lie.
    @Test func automationReportsWhetherPolicyAllowsIt() async throws {
        let automations = try await makeCLI().automations()
        #expect(automations.allSatisfy { $0.allowed == true })

        let forbidden = AutomationInfo(
            name: "deep-research", purpose: "", essential: false,
            enabled: false, configEnabled: true, allowed: false,
            schedule: "0 6 * * *", model: "synthesis", lastRun: nil
        )
        #expect(forbidden.isTogglable == false)
    }

    @Test func profilesExposeTheVaultPathAndLogsFolder() async throws {
        let profiles = try await makeCLI().profiles()

        let active = try #require(profiles.first { $0.active })
        #expect(active.name == "personal")
        #expect(active.vaultPath == "/Users/jandro/Notes/Personal")
        // The only source of the vault path — there is no vault.path key.
        #expect(active.logsPath == "/Users/jandro/.axon/profiles/personal/logs")
    }

    @Test func configGetReturnsTheNativeJSONValue() async throws {
        let value = try await makeCLI().configGet("limits.daily_tokens")
        #expect(value.intValue == 1_500_000)
    }

    @Test func configGetSurfacesTheDaemonsStderrOnFailure() async throws {
        await #expect(throws: AxonCLIError.self) {
            _ = try await makeCLI().configGet("nope.nope")
        }

        do {
            _ = try await makeCLI().configGet("nope.nope")
        } catch let error as AxonCLIError {
            // The daemon's own message is what the user should see.
            #expect(error.message.contains("not found"))
        }
    }

    // MARK: argv construction

    private func recordedArgv(_ body: (AxonCLI) async throws -> Void) async throws -> [String] {
        let log = URL(fileURLWithPath: NSTemporaryDirectory())
            .appending(path: "axon-argv-\(UUID().uuidString).log")
        let cli = try makeCLI(env: ["AXON_ARGV_LOG": log.path])
        try? await body(cli)

        guard let text = try? String(contentsOf: log, encoding: .utf8) else { return [] }
        return text.split(separator: "\n").map(String.init)
    }

    @Test func configSetPassesExactArgv() async throws {
        let argv = try await recordedArgv { cli in
            try await cli.configSet("limits.daily_tokens", "2000000")
        }

        #expect(argv.first == "config set limits.daily_tokens 2000000 --json")
    }

    @Test func automationToggleUsesTheDocumentedKeyPath() async throws {
        let argv = try await recordedArgv { cli in
            try await cli.setAutomation("briefing", enabled: false)
        }

        // CONTRACT.md §10: automations.<name>.enabled — not a profile-scoped path.
        #expect(argv.first == "config set automations.briefing.enabled false --json")
    }

    @Test func readsAlwaysRequestJSON() async throws {
        let argv = try await recordedArgv { cli in
            _ = try await cli.status()
            _ = try await cli.doctor()
            _ = try await cli.automations()
            _ = try await cli.profiles()
        }

        #expect(argv.count == 4)
        #expect(argv.allSatisfy { $0.hasSuffix("--json") })
    }

    @Test func globalFlagsArePassedThroughWhenConfigured() async throws {
        let log = URL(fileURLWithPath: NSTemporaryDirectory())
            .appending(path: "axon-argv-\(UUID().uuidString).log")
        let cli = AxonCLI(
            runner: ProcessCLIRunner(environment: ["AXON_ARGV_LOG": log.path]),
            binary: try fakeAxonURL(),
            profile: "work",
            configPath: "/tmp/alt.yaml"
        )
        _ = try? await cli.status()

        let argv = (try? String(contentsOf: log, encoding: .utf8)) ?? ""
        #expect(argv.contains("--profile work"))
        #expect(argv.contains("--config /tmp/alt.yaml"))
    }

    @Test func serviceInstallAndUninstallUseTheCLINotLaunchctl() async throws {
        let argv = try await recordedArgv { cli in
            try await cli.serviceInstall()
            try await cli.serviceUninstall()
        }

        // CFR-11: the CLI owns service semantics; Companion never runs launchctl.
        #expect(argv == ["service install", "service uninstall"])
    }

    /// `axon start` runs in the foreground until interrupted (CONTRACT.md §10).
    /// Awaiting it would hang the popover forever, so it must be spawned
    /// detached and confirmed by polling /health instead.
    @Test func startIsSpawnedDetachedRatherThanAwaited() async throws {
        let start = ContinuousClock.now
        let cli = try makeCLI(env: ["AXON_FAKE_HANG": "1"])
        try await cli.start()

        #expect(ContinuousClock.now - start < .seconds(5))
    }
}

// MARK: - child PATH (the Doctor-mismatch bug)

@Suite struct ChildProcessPathTests {
    /// The bug this exists to prevent: LaunchServices starts a GUI app with
    /// `PATH=/usr/bin:/bin:/usr/sbin:/sbin`, so a child `axon doctor` reported
    /// claude, ollama and yt-dlp missing on a machine whose shell finds all
    /// three — sending the user to reinstall tools they already had.
    @Test func aMinimalGUIPathIsWidenedToWhereToolsActuallyLive() {
        let guiPath = "/usr/bin:/bin:/usr/sbin:/sbin"
        let resolved = ProcessCLIRunner.resolvedPath(preferring: nil, inherited: guiPath)
        let dirs = resolved.split(separator: ":").map(String.init)

        #expect(dirs.contains("/opt/homebrew/bin"), "Homebrew tools would be invisible")
        #expect(dirs.contains("/usr/local/bin"))
        #expect(dirs.contains { $0.hasSuffix("/.local/bin") }, "Claude Code installs here")
        // Nothing the app inherited may be dropped.
        for dir in guiPath.split(separator: ":").map(String.init) {
            #expect(dirs.contains(dir), "dropped inherited \(dir)")
        }
    }

    /// The daemon's own service unit carries a PATH resolved from the user's
    /// real shell at install time — a better answer than any static guess, so
    /// it goes first.
    @Test func theUnitsPathWinsAndComesFirst() {
        let unitPath = "/Users/x/.local/bin:/opt/homebrew/bin:/usr/bin"
        let resolved = ProcessCLIRunner.resolvedPath(
            preferring: unitPath, inherited: "/usr/bin:/bin"
        )

        #expect(resolved.hasPrefix("/Users/x/.local/bin:/opt/homebrew/bin:/usr/bin"))
    }

    @Test func directoriesAreNeverDuplicated() {
        let resolved = ProcessCLIRunner.resolvedPath(
            preferring: "/usr/local/bin:/usr/bin", inherited: "/usr/local/bin:/usr/bin"
        )
        let dirs = resolved.split(separator: ":").map(String.init)

        #expect(dirs.count == Set(dirs).count)
    }

    @Test func anAbsentInheritedPathStillYieldsAUsableOne() {
        let resolved = ProcessCLIRunner.resolvedPath(preferring: nil, inherited: nil)

        #expect(resolved.contains("/usr/bin"))
        #expect(resolved.contains("/opt/homebrew/bin"))
    }

    /// End to end: a runner given the widened PATH must actually hand it to the
    /// child, or the whole exercise is decorative.
    @Test func theChildProcessReceivesTheWidenedPath() async throws {
        let log = URL(fileURLWithPath: NSTemporaryDirectory())
            .appending(path: "pathenv-\(UUID().uuidString).log")
        let runner = ProcessCLIRunner(environment: [
            "PATH": ProcessCLIRunner.resolvedPath(preferring: "/sentinel/bin", inherited: "/usr/bin"),
            "AXON_ARGV_LOG": log.path,
        ])
        let result = try await runner.run(
            binary: try fakeAxonURL(), arguments: ["echo-path"], timeout: .seconds(10)
        )

        #expect(result.stdoutText.contains("/sentinel/bin"))
    }
}
