import Foundation

/// The reads Insights needs. Narrower than `DashboardClient` so the store is
/// testable without a transport.
public protocol MetricsReading: Sendable {
    func tokens(days: Int) async throws -> TokenSeries
    func usage() async throws -> UsageSnapshot
    func runs(limit: Int) async throws -> [RunRecord]
    func ingestion() async throws -> IngestionStats
    func vault() async throws -> VaultStats
}

extension DashboardClient: MetricsReading {}

/// A clock, injected so coalescing is asserted without real waiting.
public protocol MetricsClock: Sendable {
    var now: ContinuousClock.Instant { get }
}

public struct SystemMetricsClock: MetricsClock {
    public init() {}
    public var now: ContinuousClock.Instant { .now }
}

/// Live chart data for the Insights window (CFR-30).
@MainActor
@Observable
public final class MetricsStore {
    public enum Range: String, CaseIterable, Sendable {
        case day, week, month

        public var days: Int {
            switch self {
            case .day: 1
            case .week: 7
            case .month: 30
            }
        }

        public var title: String {
            switch self {
            case .day: "24h"
            case .week: "7d"
            case .month: "30d"
            }
        }
    }

    public enum LoadState: Equatable, Sendable {
        case idle, loading, loaded
        case failed(String)
    }

    /// At most one refresh per this window under a burst of events. The daemon
    /// can emit dozens of `token.ledger` events a second while an automation
    /// runs; refreshing per event would hammer the API for no visible gain,
    /// and 3s keeps charts within the ≤5s freshness promise.
    public static let coalesceWindow: Duration = .seconds(3)

    /// Row budget for `/api/runs`. It is a **row count**, not a time range, so
    /// this is set generously and the window is applied client-side.
    public static let runRowLimit = 500

    public var range: Range = .week
    public private(set) var tokens: TokenSeries?
    public private(set) var usage: UsageSnapshot?
    public private(set) var runs: [RunRecord]?
    public private(set) var ingestion: IngestionStats?
    public private(set) var vault: VaultStats?
    public private(set) var loadState: LoadState = .idle

    private let source: MetricsReading
    private let clock: MetricsClock
    private var lastRefresh: ContinuousClock.Instant?
    /// At most one refresh in flight. Not cancelled in `deinit` — that cannot
    /// touch MainActor state — but the task holds only a weak self, so a
    /// deallocated store leaves nothing running but one already-started read.
    private var pendingRefresh: Task<Void, Never>?

    public init(source: MetricsReading, clock: MetricsClock = SystemMetricsClock()) {
        self.source = source
        self.clock = clock
    }

    // MARK: refresh

    public func refreshAll() async {
        if loadState == .idle { loadState = .loading }
        lastRefresh = clock.now

        do {
            // Sequential, not concurrent: these are five loopback reads against
            // one SQLite-backed process, and firing them together only makes
            // them queue behind each other with more moving parts.
            let tokens = try await source.tokens(days: range.days)
            let usage = try await source.usage()
            let runs = try await source.runs(limit: Self.runRowLimit)
            let ingestion = try await source.ingestion()
            let vault = try await source.vault()

            self.tokens = tokens
            self.usage = usage
            self.runs = runs
            self.ingestion = ingestion
            self.vault = vault
            loadState = .loaded
        } catch {
            // Keep whatever is already on screen. A blank window reads as "you
            // have no data", which is a different and wrong story.
            loadState = .failed(Self.describe(error))
        }
    }

    /// Applies an SSE event, coalescing bursts.
    public func handle(event: AxonEvent) {
        guard event.topic != nil else { return }

        if let lastRefresh, clock.now - lastRefresh < Self.coalesceWindow {
            // Already refreshed recently; a pending task will pick this up.
            guard pendingRefresh == nil else { return }
            pendingRefresh = Task { [weak self] in
                guard let self else { return }
                await refreshAll()
                pendingRefresh = nil
            }
            return
        }

        guard pendingRefresh == nil else { return }
        pendingRefresh = Task { [weak self] in
            guard let self else { return }
            await refreshAll()
            pendingRefresh = nil
        }
    }

    /// Awaits any coalesced refresh. Test-facing; production code observes the
    /// published properties instead.
    public func drainPendingRefresh() async {
        await pendingRefresh?.value
    }

    // MARK: client-side windows

    /// The earliest day the current range includes.
    public func windowStartDay(asOf now: Date = .now) -> String {
        DayString.string(from: now.addingTimeInterval(-Double(range.days) * 86400))
    }

    /// Token buckets inside the current range.
    public func tokenPoints(asOf now: Date = .now) -> [TokenPoint] {
        tokens?.filtered(sinceDay: windowStartDay(asOf: now)).points ?? []
    }

    /// Vault growth inside the current range. `/api/vault` takes no range
    /// parameter, so the filtering happens here (CONTRACT.md §6).
    public func vaultGrowth(asOf now: Date = .now) -> [GrowthPoint] {
        vault?.growth(sinceDay: windowStartDay(asOf: now)) ?? []
    }

    /// Ingestion buckets inside the current range — likewise fixed-window.
    public func ingestionBuckets(asOf now: Date = .now) -> [SourceBucket] {
        ingestion?.buckets(sinceDay: windowStartDay(asOf: now)) ?? []
    }

    /// Runs inside a window, by **start time**. `?limit=` is a row count, so
    /// filtering by it would make the "24h" view show a month of runs whenever
    /// the daemon had been quiet.
    public func runs(within range: Range, asOf now: Date = .now) -> [RunRecord] {
        let cutoff = now.addingTimeInterval(-Double(range.days) * 86400)
        return (runs ?? []).filter { record in
            guard let started = record.startedAt else { return false }
            return started >= cutoff
        }
    }

    /// Share of finished runs that succeeded, for the success-rate rule.
    /// Skipped runs are excluded: a change-gated automation that correctly
    /// skips is neither a success nor a failure, and counting skips as either
    /// makes the rate meaningless.
    public func successRate(within range: Range, asOf now: Date = .now) -> Double? {
        let finished = runs(within: range, asOf: now)
            .filter { $0.outcome == .ok || $0.outcome == .failed }
        guard !finished.isEmpty else { return nil }
        let ok = finished.count { $0.outcome == .ok }
        return Double(ok) / Double(finished.count)
    }

    private static func describe(_ error: Error) -> String {
        switch error {
        case DashboardError.unreachable:
            "AXON isn't running — start it to see live data."
        case DashboardError.badStatus(let code):
            "The dashboard API returned HTTP \(code)."
        case DashboardError.decoding:
            "The dashboard API returned data this version of Companion can't read."
        default:
            String(describing: error)
        }
    }
}
