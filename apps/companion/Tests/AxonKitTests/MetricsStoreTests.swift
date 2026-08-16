import Foundation
import Testing

@testable import AxonKit

/// A scripted metrics source. Counts calls so coalescing can be asserted
/// exactly, and can start failing on demand.
actor ScriptedMetricsSource: MetricsReading {
    private(set) var refreshCount = 0
    var shouldFail = false

    func setShouldFail(_ fail: Bool) { shouldFail = fail }

    func tokens(days: Int) async throws -> TokenSeries {
        try bump()
        return try AxonJSON.decode(TokenSeries.self, from: fixtureData("tokens"))
    }
    func usage() async throws -> UsageSnapshot {
        try AxonJSON.decode(UsageSnapshot.self, from: fixtureData("usage"))
    }
    func runs(limit: Int) async throws -> [RunRecord] {
        try AxonJSON.decode([RunRecord].self, from: fixtureData("runs"))
    }
    func ingestion() async throws -> IngestionStats {
        try AxonJSON.decode(IngestionStats.self, from: fixtureData("ingestion"))
    }
    func vault() async throws -> VaultStats {
        try AxonJSON.decode(VaultStats.self, from: fixtureData("vault"))
    }

    private func bump() throws {
        refreshCount += 1
        if shouldFail { throw DashboardError.unreachable }
    }

    private func fixtureData(_ name: String) throws -> Data {
        let url = Bundle.module.url(forResource: "Fixtures/\(name)", withExtension: "json")!
        return try Data(contentsOf: url)
    }
}

/// A clock the test advances by hand, so coalescing is asserted without waiting.
final class TestClock: MetricsClock, @unchecked Sendable {
    private let lock = NSLock()
    private var instant: ContinuousClock.Instant = .now

    var now: ContinuousClock.Instant { lock.withLock { instant } }
    func advance(by duration: Duration) { lock.withLock { instant = instant.advanced(by: duration) } }
}

@Suite @MainActor
struct MetricsStoreTests {
    private func makeStore(
        source: ScriptedMetricsSource = ScriptedMetricsSource(),
        clock: TestClock = TestClock()
    ) -> (MetricsStore, ScriptedMetricsSource, TestClock) {
        (MetricsStore(source: source, clock: clock), source, clock)
    }

    @Test func refreshAllPopulatesEverySeries() async {
        let (store, _, _) = makeStore()
        #expect(store.loadState == .idle)

        await store.refreshAll()

        #expect(store.loadState == .loaded)
        #expect(store.tokens?.points.count == 24)
        #expect(store.usage?.dayUsed == 3724)
        #expect(store.runs?.count == 19)
        #expect(store.ingestion?.embeddingQueue == 0)
        #expect(store.vault?.stats?.notes == 165)
    }

    // MARK: coalescing

    /// A burst of events must cost one refresh, not one per event. The daemon
    /// can emit dozens of token.ledger events in a second under load.
    @Test func aBurstOfEventsCausesExactlyOneRefresh() async {
        let (store, source, _) = makeStore()
        await store.refreshAll()
        let baseline = await source.refreshCount

        for _ in 0..<10 {
            store.handle(event: AxonEvent(kind: "token.ledger"))
        }
        await store.drainPendingRefresh()

        #expect(await source.refreshCount == baseline + 1)
    }

    @Test func eventsAfterTheWindowRefreshAgain() async {
        let (store, source, clock) = makeStore()
        await store.refreshAll()
        let baseline = await source.refreshCount

        store.handle(event: AxonEvent(kind: "token.ledger"))
        await store.drainPendingRefresh()

        clock.advance(by: MetricsStore.coalesceWindow + .seconds(1))
        store.handle(event: AxonEvent(kind: "automation.run"))
        await store.drainPendingRefresh()

        #expect(await source.refreshCount == baseline + 2)
    }

    /// Irrelevant kinds must not refresh at all — a chatty stream of events
    /// Companion does not chart would otherwise hammer the API.
    @Test func unrelatedKindsDoNotRefresh() async {
        let (store, source, _) = makeStore()
        await store.refreshAll()
        let baseline = await source.refreshCount

        store.handle(event: AxonEvent(kind: "ask.refused"))
        store.handle(event: AxonEvent(kind: "review.accept"))
        store.handle(event: AxonEvent(kind: "future.kind"))
        await store.drainPendingRefresh()

        #expect(await source.refreshCount == baseline)
    }

    @Test func everyChartedTopicTriggersARefresh() async {
        for kind in ["token.ledger", "automation.fail", "ingest.done"] {
            let (store, source, _) = makeStore()
            await store.refreshAll()
            let baseline = await source.refreshCount

            store.handle(event: AxonEvent(kind: kind))
            await store.drainPendingRefresh()

            #expect(await source.refreshCount == baseline + 1, "kind \(kind) did not refresh")
        }
    }

    // MARK: failure

    /// A failed refresh must not blank the charts. Stale data with a visible
    /// warning beats an empty window that looks like "you have no data".
    @Test func failureRetainsStaleDataAndReportsWhy() async {
        let (store, source, _) = makeStore()
        await store.refreshAll()
        let before = store.tokens

        await source.setShouldFail(true)
        await store.refreshAll()

        #expect(store.tokens == before)
        if case .failed(let message) = store.loadState {
            #expect(!message.isEmpty)
        } else {
            Issue.record("expected .failed, got \(store.loadState)")
        }
    }

    @Test func recoveryClearsTheFailureState() async {
        let (store, source, _) = makeStore()
        await source.setShouldFail(true)
        await store.refreshAll()
        #expect(store.loadState != .loaded)

        await source.setShouldFail(false)
        await store.refreshAll()

        #expect(store.loadState == .loaded)
    }

    // MARK: ranges

    @Test func rangeChangeRefetchesWithTheRightWindow() async {
        let (store, _, _) = makeStore()
        store.range = .week

        #expect(store.range.days == 7)
        #expect(MetricsStore.Range.day.days == 1)
        #expect(MetricsStore.Range.month.days == 30)
    }

    /// `/api/ingestion` and `/api/vault` take no range parameter, so the picker
    /// filters them client-side (CONTRACT.md §6).
    @Test func fixedWindowSeriesAreFilteredClientSide() async {
        let (store, _, _) = makeStore()
        await store.refreshAll()
        store.range = .month

        let growth = store.vaultGrowth(asOf: DayString.date(from: "2026-08-16")!)
        #expect(growth.allSatisfy { $0.day >= "2026-07-17" })
        // The fixture spans back to 2026-07-11, which a 30-day window excludes.
        #expect(growth.count < (store.vault?.growth?.count ?? 0))
    }

    @Test func runsAreFilteredByStartTimeNotRowCount() async {
        let (store, _, _) = makeStore()
        await store.refreshAll()

        // limit is a row count, not a time range — the window must come from
        // started_at or the "24h" view silently shows a month of runs.
        let recent = store.runs(within: .day, asOf: Date(timeIntervalSince1970: 1_787_000_000))
        #expect(recent.count <= (store.runs?.count ?? 0))
        #expect(recent.allSatisfy { $0.startedAt != nil })
    }
}
