import Foundation

/// What AXON's caching and local routing avoided sending to Claude.
///
/// Deliberately conservative arithmetic. It would be easy — and wrong — to
/// report every cached token as "saved": prompt-cache reads **are** still
/// billed, just at a fraction of the fresh-input rate. So the headline number
/// is the *difference*, not the total, and the wording says what it measures.
public struct TokenSavings: Equatable, Sendable {
    /// Tokens read from the prompt cache instead of re-sent as fresh input.
    public let cacheRead: Int64
    /// Tokens written into the cache. A real cost, not a saving — surfaced so
    /// the picture is honest rather than only flattering.
    public let cacheWrite: Int64
    /// Tokens billed as fresh input + output.
    public let billed: Int64
    /// Tokens handled entirely by a local model, so never sent to Claude.
    public let local: Int64

    /// Anthropic bills a cache read at roughly a tenth of a fresh input token,
    /// so nine tenths of every cached token is the part that was avoided.
    /// A constant rather than config: it is an order-of-magnitude claim used
    /// for one summary line, and pretending to more precision than that would
    /// be its own kind of lie.
    static let cacheReadDiscount = 0.9

    public init(cacheRead: Int64, cacheWrite: Int64, billed: Int64, local: Int64) {
        self.cacheRead = cacheRead
        self.cacheWrite = cacheWrite
        self.billed = billed
        self.local = local
    }

    public init(points: [TokenPoint], isLocalModel: (String) -> Bool = TokenSavings.isLocalModel) {
        var cacheRead: Int64 = 0
        var cacheWrite: Int64 = 0
        var billed: Int64 = 0
        var local: Int64 = 0
        for point in points {
            cacheRead += point.cacheRead ?? 0
            cacheWrite += point.cacheWrite ?? 0
            if isLocalModel(point.model) {
                local += point.total
            } else {
                billed += point.total
            }
        }
        self.init(cacheRead: cacheRead, cacheWrite: cacheWrite, billed: billed, local: local)
    }

    /// A model is Claude's unless it says otherwise. Guessing the other way
    /// would silently inflate the savings figure whenever a new Claude model
    /// name appears.
    public static func isLocalModel(_ model: String) -> Bool {
        let name = model.lowercased()
        if name.hasPrefix("claude") || name.contains("sonnet")
            || name.contains("opus") || name.contains("haiku") || name.contains("fable")
        {
            return false
        }
        return !name.isEmpty
    }

    /// Fresh-input tokens avoided: the discounted share of cache reads, plus
    /// everything a local model handled outright.
    public var avoided: Int64 {
        Int64(Double(cacheRead) * Self.cacheReadDiscount) + local
    }

    /// What would have been sent without caching or local routing.
    public var withoutAxon: Int64 { billed + avoided }

    /// Share of the un-optimised total that was avoided, 0...1.
    public var fraction: Double {
        let total = withoutAxon
        guard total > 0 else { return 0 }
        return min(1, Double(avoided) / Double(total))
    }

    public var hasSavings: Bool { avoided > 0 }

    /// One line explaining exactly what the headline number counts, for a
    /// tooltip. No marketing: a user who reads it should be able to check it.
    public var explanation: String {
        var parts: [String] = []
        if cacheRead > 0 {
            parts.append(
                "\(AxonFormat.tokens(cacheRead)) tokens came from the prompt cache instead of "
                    + "being re-sent — cache reads are still billed, at roughly a tenth of fresh "
                    + "input, so about 90% of that is the part avoided"
            )
        }
        if local > 0 {
            parts.append("\(AxonFormat.tokens(local)) were handled by a local model and never sent to Claude")
        }
        if parts.isEmpty {
            return "Nothing cached or run locally in this window yet."
        }
        return parts.joined(separator: ". ") + "."
    }
}
