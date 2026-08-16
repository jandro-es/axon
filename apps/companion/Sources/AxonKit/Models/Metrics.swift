import Foundation

// MARK: - Usage (budget gauges)

/// `GET /api/usage` — numerically identical to `axon status` (CONTRACT.md §3).
public struct UsageSnapshot: Decodable, Sendable, Equatable {
    public let profile: String?
    public let dayUsed: Int64?
    public let dayLimit: Int64?
    public let weekUsed: Int64?
    public let weekLimit: Int64?
    public let guardPaused: Bool?
    public let guardReason: String?
    public let guardPct: Double?

    // Cost accounting is zero outside `auth_mode: api_key`, and several of
    // these fields are simply absent. All optional by necessity.
    public let dayCostUsed: Double?
    public let dayCostCap: Double?
    public let weekCostUsed: Double?
    public let weekCostCap: Double?

    enum CodingKeys: String, CodingKey {
        case profile
        case dayUsed = "day_used"
        case dayLimit = "day_limit"
        case weekUsed = "week_used"
        case weekLimit = "week_limit"
        case guardPaused = "guard_paused"
        case guardReason = "guard_reason"
        case guardPct = "guard_pct"
        case dayCostUsed = "day_cost_used"
        case dayCostCap = "day_cost_cap"
        case weekCostUsed = "week_cost_used"
        case weekCostCap = "week_cost_cap"
    }

    /// Day budget consumed, as 0...1 for `Gauge`.
    ///
    /// Computed from used/limit rather than the daemon's `day_pct` because the
    /// daemon reports 0-100, and a gauge fed a percentage renders 100x wrong.
    public var dayFraction: Double { Self.fraction(dayUsed, dayLimit) }
    public var weekFraction: Double { Self.fraction(weekUsed, weekLimit) }

    /// A zero limit means "unlimited", not "divide by zero"; over-budget
    /// saturates so the gauge cannot overflow its track.
    private static func fraction(_ used: Int64?, _ limit: Int64?) -> Double {
        guard let limit, limit > 0, let used else { return 0 }
        return min(1, max(0, Double(used) / Double(limit)))
    }

    /// True only when the profile actually meters dollars (`api_key` mode).
    /// Elsewhere the cost caps are 0 and cost rows are hidden rather than
    /// showing a misleading "$0.00 of $0.00".
    public var tracksCost: Bool { (dayCostCap ?? 0) > 0 || (weekCostCap ?? 0) > 0 }

    /// The budget guard has paused automations. Interactive use is unaffected.
    public var isGuardTripped: Bool { guardPaused == true }
}

// MARK: - Tokens

/// One day/operation/model bucket from `GET /api/tokens`.
public struct TokenPoint: Decodable, Sendable, Equatable, Identifiable {
    public let day: String
    public let operation: String
    public let model: String
    public let input: Int64?
    public let output: Int64?
    public let cacheRead: Int64?
    public let cacheWrite: Int64?

    enum CodingKeys: String, CodingKey {
        case day, operation, model, input, output
        case cacheRead = "cache_read"
        case cacheWrite = "cache_write"
    }

    public var id: String { "\(day)|\(operation)|\(model)" }

    /// What the budget counts. Cache columns are informational and are
    /// deliberately excluded so charts agree with `axon status`.
    public var total: Int64 { (input ?? 0) + (output ?? 0) }

    /// Display name: `automation.briefing` reads as `briefing` in a legend.
    public var label: String {
        let prefix = "automation."
        return operation.hasPrefix(prefix) ? String(operation.dropFirst(prefix.count)) : operation
    }

    /// Calendar day as a `Date` for Swift Charts' `.value(_:_:unit:)`.
    public var date: Date? { DayString.date(from: day) }
}

/// The whole `/api/tokens` response: a flat array of buckets.
public struct TokenSeries: Decodable, Sendable, Equatable {
    public let points: [TokenPoint]

    public init(points: [TokenPoint]) { self.points = points }

    public init(from decoder: Decoder) throws {
        points = try [TokenPoint](from: decoder)
    }

    /// Distinct model ids present, sorted for a stable legend order.
    public var models: [String] { Set(points.map(\.model)).sorted() }

    /// Distinct automation/operation labels, sorted for a stable legend order.
    public var automations: [String] { Set(points.map(\.label)).sorted() }

    public var total: Int64 { points.reduce(0) { $0 + $1.total } }

    /// Client-side range filtering. `/api/tokens` takes `?days=`, but refetching
    /// on every picker change would hammer the API for data already held.
    public func filtered(sinceDay day: String) -> TokenSeries {
        TokenSeries(points: points.filter { $0.day >= day })
    }
}

// MARK: - Runs

/// One automation run from `GET /api/runs`.
public struct RunRecord: Decodable, Sendable, Equatable, Identifiable {
    public enum Outcome: Sendable, Equatable {
        case ok, skipped, failed, running, unknown
    }

    public let id: Int64
    public let automation: String
    public let status: String?
    public let skipReason: String?
    public let error: String?
    public let tokens: Int64?

    private let startedAtRaw: String?
    private let finishedAtRaw: String?

    enum CodingKeys: String, CodingKey {
        case id, automation, status, tokens, error
        case skipReason = "skip_reason"
        case startedAtRaw = "started_at"
        case finishedAtRaw = "finished_at"
    }

    public var startedAtString: String { startedAtRaw ?? "" }
    public var finishedAt: String { finishedAtRaw ?? "" }
    public var startedAt: Date? { AxonTimestamp.parse(startedAtRaw) }

    /// Wall-clock duration, or nil while the run is still in flight (the daemon
    /// leaves `finished_at` empty rather than absent).
    public var duration: TimeInterval? {
        guard let start = AxonTimestamp.parse(startedAtRaw),
              let end = AxonTimestamp.parse(finishedAtRaw)
        else { return nil }
        return max(0, end.timeIntervalSince(start))
    }

    /// `status` stays a string on the wire so a new daemon status cannot break
    /// decoding; `.unknown` makes sure it also cannot masquerade as success.
    public var outcome: Outcome {
        switch status {
        case "ok": .ok
        case "skipped": .skipped
        case "failed": .failed
        case "running": .running
        default: .unknown
        }
    }
}

// MARK: - Ingestion

/// One day/status ingestion bucket.
public struct SourceBucket: Decodable, Sendable, Equatable, Identifiable {
    public let day: String
    public let status: String
    public let count: Int

    public var id: String { "\(day)|\(status)" }
    public var date: Date? { DayString.date(from: day) }
}

/// `GET /api/ingestion` (CONTRACT.md §6).
public struct IngestionStats: Decodable, Sendable, Equatable {
    /// ⚠️ The daemon returns `null`, not `[]`, when no sources exist.
    public let series: [SourceBucket]?
    public let embeddingQueue: Int?

    enum CodingKeys: String, CodingKey {
        case series
        case embeddingQueue = "embedding_queue"
    }

    /// Null-safe accessor — use this everywhere instead of `series`.
    public var buckets: [SourceBucket] { series ?? [] }

    public var successCount: Int {
        buckets.filter { $0.status == "ok" }.reduce(0) { $0 + $1.count }
    }

    /// Failed and redacted both mean "did not land cleanly" for the chart.
    public var problemCount: Int {
        buckets.filter { $0.status != "ok" }.reduce(0) { $0 + $1.count }
    }

    public func buckets(sinceDay day: String) -> [SourceBucket] {
        buckets.filter { $0.day >= day }
    }
}

// MARK: - Vault

public struct VaultCounts: Decodable, Sendable, Equatable {
    public let notes: Int?
    public let links: Int?
    public let words: Int?
    public let sources: Int?
    public let inboxBacklog: Int?

    enum CodingKeys: String, CodingKey {
        case notes, links, words, sources
        case inboxBacklog = "inbox_backlog"
    }
}

/// A point on the vault-growth curve. Carries **notes and words only** — there
/// is no `links` series (CONTRACT.md §6).
public struct GrowthPoint: Decodable, Sendable, Equatable, Identifiable {
    public let day: String
    public let notes: Int?
    public let words: Int?

    public var id: String { day }
    public var date: Date? { DayString.date(from: day) }
}

/// `GET /api/vault`.
public struct VaultStats: Decodable, Sendable, Equatable {
    public let stats: VaultCounts?
    public let growth: [GrowthPoint]?

    public func growth(sinceDay day: String) -> [GrowthPoint] {
        (growth ?? []).filter { $0.day >= day }
    }
}

// MARK: - Badge counts

/// `GET /api/review` — Companion reads `pending` and ignores `items`, which can
/// be ~100 KB (CONTRACT.md §7).
public struct ReviewMeta: Decodable, Sendable, Equatable {
    public let pending: Int?
}

/// `GET /api/actions` — Companion reads `counts.open`.
public struct ActionsMeta: Decodable, Sendable, Equatable {
    public struct Counts: Decodable, Sendable, Equatable {
        public let open: Int?
        public let overdue: Int?
        public let today: Int?
        public let waiting: Int?
        public let someday: Int?
        public let done7: Int?
    }

    public let counts: Counts?

    public var openCount: Int? { counts?.open }
}

// MARK: - Day strings

/// The daemon's `YYYY-MM-DD` day keys. Parsed in **UTC** to match how the
/// daemon buckets them; rendering converts to the user's calendar.
enum DayString {
    nonisolated(unsafe) private static let formatter: DateFormatter = {
        let f = DateFormatter()
        f.calendar = Calendar(identifier: .iso8601)
        f.locale = Locale(identifier: "en_US_POSIX")
        f.timeZone = TimeZone(identifier: "UTC")
        f.dateFormat = "yyyy-MM-dd"
        return f
    }()

    static func date(from day: String) -> Date? { formatter.date(from: day) }
    static func string(from date: Date) -> String { formatter.string(from: date) }
}
