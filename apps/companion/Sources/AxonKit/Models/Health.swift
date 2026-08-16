import Foundation

/// `GET /health` — the daemon's liveness and capability report (CONTRACT.md §2).
///
/// `version` is the only required field: it is what a min-version gate reads and
/// what proves the payload came from an AXON daemon at all. Everything else is
/// optional so an older daemon degrades feature-by-feature (CFR-82) instead of
/// failing to decode.
public struct AxonHealth: Decodable, Sendable, Equatable {
    public let version: String
    public let status: String?
    public let profile: String?
    public let db: Bool?
    public let latestVersion: String?
    public let updateAvailable: Bool?
    public let embeddingsProvider: String?
    public let embeddingsModel: String?
    public let embeddingsDim: Int?
    public let askEnabled: Bool?
    public let captureEnabled: Bool?
    public let relatedEnabled: Bool?
    public let actionsEnabled: Bool?

    /// Raw `started_at`; use ``startedAt`` for the parsed value.
    private let startedAtRaw: String?

    enum CodingKeys: String, CodingKey {
        case version
        case status
        case profile
        case db
        case latestVersion = "latest_version"
        case updateAvailable = "update_available"
        case embeddingsProvider = "embeddings_provider"
        case embeddingsModel = "embeddings_model"
        case embeddingsDim = "embeddings_dim"
        case askEnabled = "ask_enabled"
        case captureEnabled = "capture_enabled"
        case relatedEnabled = "related_enabled"
        case actionsEnabled = "actions_enabled"
        case startedAtRaw = "started_at"
    }

    /// When the daemon process began serving. Absent on daemons predating the
    /// `started_at` seam — callers hide the uptime pill rather than guess.
    public var startedAt: Date? { AxonTimestamp.parse(startedAtRaw) }

    /// Seconds this daemon has been up, or nil when the daemon cannot say.
    public func uptime(asOf now: Date) -> TimeInterval? {
        guard let startedAt else { return nil }
        return max(0, now.timeIntervalSince(startedAt))
    }

    /// The daemon itself reports trouble. Today this means only a failed DB
    /// ping, but the field is the daemon's verdict, so trust it over inference.
    public var isDegraded: Bool { status == "degraded" }

    /// Named components currently unhealthy, for `.attention` reasons.
    public var degradedComponents: [String] {
        db == false ? ["db"] : []
    }
}
