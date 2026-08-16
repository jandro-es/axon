import Foundation
import Testing

@testable import AxonKit

@Test func daemonStateSummariesAreDistinct() throws {
    let health = try AxonJSON.decode(AxonHealth.self, from: Data(#"{"version":"1.3.2"}"#.utf8))
    let states: [DaemonState] = [
        .unknown, .notInstalled, .stopped, .starting, .stopping,
        .runningWith(health, []), .runningWith(health, [.budgetGuard]),
    ]
    let summaries = Set(states.map(\.summary))
    #expect(summaries.count == states.count)
}

/// The fixtures are the regression net for daemon drift (CONTRACT.md). If the
/// resource bundle stops being copied, every decoder test would "pass" by
/// silently not running — so assert the bundle is wired before anything else.
@Test func fixtureBundleIsAvailable() throws {
    let url = try #require(
        Bundle.module.url(forResource: "Fixtures/health", withExtension: "json"),
        "Fixtures resource directory is not in the test bundle"
    )
    #expect(try Data(contentsOf: url).count > 0)
}
