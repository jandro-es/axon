import Foundation

/// One diagnostic line from `axon doctor --json`.
public struct DoctorCheck: Decodable, Sendable, Equatable, Identifiable {
    public enum Status: String, Decodable, Sendable {
        case ok, warn, fail
    }

    public let name: String
    public let status: Status
    /// What is wrong, in the daemon's own words. Render verbatim (CFR-60).
    public let detail: String
    /// What to do about it — a command to run. Present only when the daemon
    /// has one to offer, which is every warning a user can act on since
    /// axon 1.3.4. Older daemons omit it and the row simply shows no fix.
    public let remediation: String?

    public var id: String { name }

    public init(name: String, status: Status, detail: String, remediation: String? = nil) {
        self.name = name
        self.status = status
        self.detail = detail
        self.remediation = remediation
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        name = try container.decode(String.self, forKey: .name)
        detail = try container.decodeIfPresent(String.self, forKey: .detail) ?? ""
        let fix = try container.decodeIfPresent(String.self, forKey: .remediation)
        remediation = (fix?.isEmpty ?? true) ? nil : fix
        // An unrecognised status must not fail the whole report; treat it as a
        // warning so it is visible rather than silently dropped or shown green.
        let raw = try container.decodeIfPresent(String.self, forKey: .status) ?? ""
        status = Status(rawValue: raw) ?? .warn
    }

    enum CodingKeys: String, CodingKey { case name, status, detail, remediation }
}

/// The full `axon doctor --json` report.
public struct DoctorReport: Decodable, Sendable, Equatable {
    public enum Overall: String, Decodable, Sendable {
        case ok, fail
    }

    public let profile: String?
    public let status: Overall
    /// A config-load error, while the prerequisite checks still ran.
    public let error: String?
    public let checks: [DoctorCheck]

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        profile = try container.decodeIfPresent(String.self, forKey: .profile)
        error = try container.decodeIfPresent(String.self, forKey: .error)
        checks = try container.decodeIfPresent([DoctorCheck].self, forKey: .checks) ?? []
        let raw = try container.decodeIfPresent(String.self, forKey: .status) ?? ""
        status = Overall(rawValue: raw) ?? (checks.contains { $0.status == .fail } ? .fail : .ok)
    }

    enum CodingKeys: String, CodingKey { case profile, status, error, checks }

    public var passCount: Int { checks.count { $0.status == .ok } }
    public var warnCount: Int { checks.count { $0.status == .warn } }
    public var failCount: Int { checks.count { $0.status == .fail } }

    /// Failing checks first, then warnings — what needs doing, at the top.
    public var sortedBySeverity: [DoctorCheck] {
        checks.sorted { lhs, rhs in
            func rank(_ status: DoctorCheck.Status) -> Int {
                switch status {
                case .fail: 0
                case .warn: 1
                case .ok: 2
                }
            }
            return rank(lhs.status) < rank(rhs.status)
        }
    }
}
