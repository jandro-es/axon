import Foundation

/// One frame from `GET /events` (CONTRACT.md §9).
///
/// `kind` is deliberately a **string, not an enum**: the daemon emits kinds
/// outside its own documented union (`ask.refused` is live today and absent
/// from `docs/09`), so an exhaustive enum would silently drop real events.
public struct AxonEvent: Sendable, Equatable {
    public let kind: String
    public let level: String
    public let message: String
    public let ts: String?
    public let data: [String: JSONValue]?

    public init(
        kind: String, level: String = "info", message: String = "",
        ts: String? = nil, data: [String: JSONValue]? = nil
    ) {
        self.kind = kind
        self.level = level
        self.message = message
        self.ts = ts
        self.data = data
    }

    /// SSE timestamps carry a local UTC offset and fractional seconds, unlike
    /// the REST endpoints' `Z` form.
    public var date: Date? { AxonTimestamp.parse(ts) }

    public var isError: Bool { level == "error" }

    /// `data.outcome.automation` — the dedup key for automation notifications.
    public var automationName: String? {
        guard case .object(let outcome)? = data?["outcome"],
              case .string(let name)? = outcome["automation"]
        else { return nil }
        return name
    }

    /// `data.outcome.status` for automation events.
    public var outcomeStatus: String? {
        guard case .object(let outcome)? = data?["outcome"],
              case .string(let status)? = outcome["status"]
        else { return nil }
        return status
    }

    /// `data.reason` — the token manager's human explanation, used verbatim as
    /// a notification body.
    public var reason: String? {
        guard case .string(let reason)? = data?["reason"] else { return nil }
        return reason
    }

    /// Which cached series an event invalidates. `nil` means "ignore" — most
    /// kinds do not warrant a refetch, and refreshing on everything would
    /// hammer the API under load.
    public enum Topic: Sendable, Equatable, CaseIterable {
        case automations, tokens, ingestion

        public init?(kind: String) {
            if kind.hasPrefix("automation.") { self = .automations }
            else if kind.hasPrefix("token.") { self = .tokens }
            else if kind.hasPrefix("ingest.") { self = .ingestion }
            else { return nil }
        }
    }

    public var topic: Topic? { Topic(kind: kind) }
}

// MARK: - decoding

extension AxonEvent: Decodable {
    enum CodingKeys: String, CodingKey {
        case kind, level, message, ts, data
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        kind = try container.decodeIfPresent(String.self, forKey: .kind) ?? ""
        level = try container.decodeIfPresent(String.self, forKey: .level) ?? "info"
        message = try container.decodeIfPresent(String.self, forKey: .message) ?? ""
        ts = try container.decodeIfPresent(String.self, forKey: .ts)
        data = try container.decodeIfPresent([String: JSONValue].self, forKey: .data)
    }
}

/// A decoded-but-unmodelled JSON value.
///
/// Event `data` payloads differ per kind and evolve independently of Companion.
/// Keeping them as loose values means a new payload shape can never fail to
/// decode; the few fields Companion actually reads are pulled out by name.
public enum JSONValue: Decodable, Sendable, Equatable {
    case string(String)
    case number(Double)
    case bool(Bool)
    case object([String: JSONValue])
    case array([JSONValue])
    case null

    public init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() {
            self = .null
        } else if let value = try? container.decode(Bool.self) {
            self = .bool(value)
        } else if let value = try? container.decode(Double.self) {
            self = .number(value)
        } else if let value = try? container.decode(String.self) {
            self = .string(value)
        } else if let value = try? container.decode([String: JSONValue].self) {
            self = .object(value)
        } else if let value = try? container.decode([JSONValue].self) {
            self = .array(value)
        } else {
            self = .null
        }
    }

    public var stringValue: String? {
        if case .string(let value) = self { return value }
        return nil
    }

    public var intValue: Int? {
        if case .number(let value) = self { return Int(value) }
        return nil
    }
}
