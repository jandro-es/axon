import Foundation

/// One hybrid-search hit from `GET /api/search` (CONTRACT.md §8b, FR-198).
public struct SearchHit: Decodable, Sendable, Equatable {
    /// Vault-relative note path.
    public let path: String
    public let snippet: String
    /// Fused reciprocal-rank score — higher is better; only ordinal.
    public let score: Double
}

struct SearchResponse: Decodable {
    let hits: [SearchHit]
}

/// The grounded-or-silent answer from `POST /api/ask` (ADR-023). `refused`
/// true means the vault genuinely doesn't answer — a *state*, not an error.
public struct AskAnswer: Decodable, Sendable, Equatable {
    public let answer: String?
    public let citations: [String]?
    public let refused: Bool
    public let conflicted: Bool?
    public let reason: String?
}
