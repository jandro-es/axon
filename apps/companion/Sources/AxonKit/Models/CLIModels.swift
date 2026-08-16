import Foundation

/// `axon status --json`.
///
/// ⚠️ The only PascalCase payload the daemon emits — Go's default field names,
/// no struct tags (CONTRACT.md §10). Every other command is snake_case.
public struct DaemonStatus: Decodable, Sendable, Equatable {
    public struct Window: Decodable, Sendable, Equatable {
        public let used: Int64?
        public let limit: Int64?
        public let costUsed: Double?
        public let costCap: Double?

        enum CodingKeys: String, CodingKey {
            case used = "Used"
            case limit = "Limit"
            case costUsed = "CostUsed"
            case costCap = "CostCap"
        }
    }

    public let profile: String?
    public let day: Window?
    public let week: Window?
    public let guardPaused: Bool?
    public let guardReason: String?

    enum CodingKeys: String, CodingKey {
        case profile = "Profile"
        case day = "Day"
        case week = "Week"
        case guardPaused = "GuardPaused"
        case guardReason = "GuardReason"
    }
}

/// One entry from `axon automations --json`.
public struct AutomationInfo: Decodable, Sendable, Equatable, Identifiable {
    /// A run summary embedded in the automation list. Same shape as
    /// `/api/runs` rows but without the `error` field.
    public struct LastRun: Decodable, Sendable, Equatable {
        public let id: Int64?
        public let status: String?
        public let skipReason: String?
        public let tokens: Int64?
        private let startedAtRaw: String?
        private let finishedAtRaw: String?

        enum CodingKeys: String, CodingKey {
            case id, status, tokens
            case skipReason = "skip_reason"
            case startedAtRaw = "started_at"
            case finishedAtRaw = "finished_at"
        }

        public var startedAt: Date? { AxonTimestamp.parse(startedAtRaw) }
        public var finishedAt: Date? { AxonTimestamp.parse(finishedAtRaw) }
    }

    public let name: String
    public let purpose: String?
    public let essential: Bool?
    /// The **effective** state: enabled in config *and* permitted by policy.
    public let enabled: Bool?
    /// What the config file says, regardless of policy.
    public let configEnabled: Bool?
    /// Whether the profile's allowlist permits this automation at all.
    public let allowed: Bool?
    public let schedule: String?
    /// `none` | `classify` | `routine` | `synthesis`. `none` spends no tokens.
    public let model: String?
    public let lastRun: LastRun?

    enum CodingKeys: String, CodingKey {
        case name, purpose, essential, enabled, allowed, schedule, model
        case configEnabled = "config_enabled"
        case lastRun = "last_run"
    }

    public var id: String { name }

    public init(
        name: String, purpose: String?, essential: Bool?, enabled: Bool?,
        configEnabled: Bool?, allowed: Bool?, schedule: String?, model: String?,
        lastRun: LastRun?
    ) {
        self.name = name
        self.purpose = purpose
        self.essential = essential
        self.enabled = enabled
        self.configEnabled = configEnabled
        self.allowed = allowed
        self.schedule = schedule
        self.model = model
        self.lastRun = lastRun
    }

    /// A policy-forbidden automation renders as a disabled toggle with a
    /// reason. Writing its config key would change the file and change nothing
    /// about behaviour — a lie the UI must not tell.
    public var isTogglable: Bool { allowed != false }

    /// True when this automation never spends tokens.
    public var isFree: Bool { model == "none" }
}

/// One entry from `axon profiles --json`.
public struct ProfileInfo: Decodable, Sendable, Equatable, Identifiable {
    public let name: String
    public let active: Bool
    public let authMode: String?
    public let vaultPath: String?
    public let dataDir: String?
    public let dbPath: String?
    public let configDir: String?
    public let allowedAutomations: [String]?

    // oauth_token_ref is deliberately NOT decoded. It is a reference, never a
    // secret value, but Companion has no reason to hold it and holding it
    // invites showing it.

    enum CodingKeys: String, CodingKey {
        case name, active
        case authMode = "auth_mode"
        case vaultPath = "vault_path"
        case dataDir = "data_dir"
        case dbPath = "db_path"
        case configDir = "config_dir"
        case allowedAutomations = "allowed_automations"
    }

    public var id: String { name }

    /// `<data_dir>/logs` — the folder the "Open logs" action reveals.
    public var logsPath: String? {
        dataDir.map { URL(fileURLWithPath: $0).appending(path: "logs").path }
    }

    /// Whether this profile permits every automation (`["*"]`).
    public var allowsAllAutomations: Bool { allowedAutomations == ["*"] }
}

/// `axon config get <key> --json`.
public struct ConfigValue: Decodable, Sendable, Equatable {
    public let key: String
    public let value: JSONValue?

    public var intValue: Int? { value?.intValue }
    public var stringValue: String? { value?.stringValue }
    public var boolValue: Bool? {
        if case .bool(let flag)? = value { return flag }
        return nil
    }
}

/// `axon config set <key> <value> --json`.
///
/// ⚠️ `value` comes back as a **string** here while `config get` returns the
/// native type (CONTRACT.md §10) — never compare the two directly.
public struct ConfigSetResult: Decodable, Sendable, Equatable {
    public let key: String
    public let ok: Bool?
    public let value: String?
}
