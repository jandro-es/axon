import Foundation

/// The one decoder every AxonKit type is decoded through.
///
/// The daemon is inconsistent by history: REST and most `--json` commands use
/// snake_case, but `axon status --json` emits Go's default PascalCase
/// (CONTRACT.md §10). Rather than have some types opt into a key strategy and
/// others not — a trap that silently drops fields — every model declares
/// explicit `CodingKeys`, and this decoder applies **no** key conversion.
public enum AxonJSON {
    public static func decode<T: Decodable>(_ type: T.Type, from data: Data) throws -> T {
        try decoder.decode(type, from: data)
    }

    /// Shared, immutable, and safe to hand across actors.
    nonisolated(unsafe) static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        // Timestamps are decoded per-field: REST uses RFC3339 with `Z`, SSE
        // uses a local offset with fractional seconds, and several fields are
        // plain `YYYY-MM-DD` day strings that are never dates at all. A global
        // date strategy would be wrong for at least one of them.
        return d
    }()
}

/// Parses the timestamp formats the daemon actually emits.
///
/// REST: `2026-08-16T16:50:03Z`. SSE: `2026-08-16T17:55:00.414609+01:00`.
/// `ISO8601DateFormatter` needs to be told about fractional seconds explicitly,
/// and returns nil rather than throwing on the wrong variant — so try both.
enum AxonTimestamp {
    nonisolated(unsafe) private static let withFraction: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()

    nonisolated(unsafe) private static let plain: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()

    static func parse(_ string: String?) -> Date? {
        guard let string, !string.isEmpty else { return nil }
        return withFraction.date(from: string) ?? plain.date(from: string)
    }
}
