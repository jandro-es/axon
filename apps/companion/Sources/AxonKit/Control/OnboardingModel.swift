import Foundation

/// The detections onboarding performs. Injected so the wizard is testable
/// without a machine in a particular state.
public protocol PrerequisiteProbing: Sendable {
    /// Whether the `axon` binary can be located.
    func axonInstalled() async -> Bool
    /// Whether the `claude` CLI is on PATH.
    func claudeInstalled() async -> Bool
    /// Whether Ollama answers on its local port.
    func ollamaReachable() async -> Bool
    /// Whether the daemon is serving.
    func daemonRunning() async -> Bool
}

/// Real detection. Deliberately cheap and local — no installers, ever (CFR-51).
public struct SystemPrerequisiteProbe: PrerequisiteProbing {
    private let explicitBinaryPath: String?
    private let ollamaURL: URL
    private let session: URLSession

    public init(
        explicitBinaryPath: String? = nil,
        ollamaURL: URL = URL(string: "http://127.0.0.1:11434/api/tags")!,
        session: URLSession = .shared
    ) {
        self.explicitBinaryPath = explicitBinaryPath
        self.ollamaURL = ollamaURL
        self.session = session
    }

    public func axonInstalled() async -> Bool {
        BinaryLocator.locate(explicit: explicitBinaryPath) != nil
    }

    public func claudeInstalled() async -> Bool {
        BinaryLocator.locate(
            explicit: nil,
            // Claude Code installs to ~/.local/bin as well as the usual places.
            searchPaths: BinaryLocator.defaultSearchPaths + [
                FileManager.default.homeDirectoryForCurrentUser.appending(path: ".local/bin").path
            ],
            names: ["claude"]
        ) != nil
    }

    public func ollamaReachable() async -> Bool {
        var request = URLRequest(url: ollamaURL)
        request.timeoutInterval = 2
        guard let (_, response) = try? await session.data(for: request) else { return false }
        return (response as? HTTPURLResponse).map { (200..<400).contains($0.statusCode) } ?? false
    }

    public func daemonRunning() async -> Bool {
        (try? await DashboardClient().health()) != nil
    }
}

/// Drives the first-run wizard (CFR-50/51/52).
@MainActor
@Observable
public final class OnboardingModel {
    public enum Step: String, CaseIterable, Sendable {
        case welcome, axonBinary, claudeCLI, ollama, daemon, done

        public var title: String {
            switch self {
            case .welcome: "Welcome"
            case .axonBinary: "Install AXON"
            case .claudeCLI: "Connect Claude"
            case .ollama: "Local embeddings"
            case .daemon: "Start AXON"
            case .done: "You're set"
            }
        }

        /// Whether the wizard refuses to advance past this step unfinished.
        ///
        /// Ollama is skippable: the Apple on-device embeddings path exists, and
        /// blocking someone who chose it would be simply wrong.
        public var isRequired: Bool {
            switch self {
            case .axonBinary, .claudeCLI: true
            case .welcome, .ollama, .daemon, .done: false
            }
        }

        /// The command the user runs. Companion shows and copies it; it never
        /// runs an installer itself (CFR-51).
        public var command: String? {
            switch self {
            case .axonBinary:
                "curl -fsSL https://raw.githubusercontent.com/jandro-es/axon/main/scripts/install-macos.sh | bash"
            case .claudeCLI:
                "npm install -g @anthropic-ai/claude-code && claude login"
            case .ollama:
                "brew install ollama && ollama serve & ollama pull nomic-embed-text"
            case .daemon, .welcome, .done:
                nil
            }
        }

        public var explanation: String {
            switch self {
            case .welcome:
                "AXON turns an Obsidian vault into a self-maintaining second brain. "
                    + "This walks through what it needs. Companion guides you — it never "
                    + "installs anything itself."
            case .axonBinary:
                "The AXON daemon runs beside your vault. It is one binary and one "
                    + "local database; nothing leaves your machine except the calls you configure."
            case .claudeCLI:
                "AXON reaches Claude through your own Claude Code login — your Max or "
                    + "enterprise subscription, not an API key."
            case .ollama:
                "Ollama provides local embeddings for search. Optional: AXON can use "
                    + "Apple's on-device embeddings instead, which needs no install at all."
            case .daemon:
                "Starting AXON runs the scheduler and the dashboard on 127.0.0.1:7777."
            case .done:
                "AXON is running. The menu bar icon shows its state, and the dashboard "
                    + "has the full picture."
            }
        }
    }

    public enum CheckState: Equatable, Sendable {
        case unknown, checking, pass
        case fail(String)

        public var isPass: Bool { self == .pass }
    }

    public private(set) var current: Step = .welcome
    public private(set) var checks: [Step: CheckState] = [:]
    public private(set) var isRechecking = false

    private let probe: PrerequisiteProbing
    private let settings: SettingsStore?
    private let onStartDaemon: @MainActor () async -> Void

    public init(
        probe: PrerequisiteProbing,
        settings: SettingsStore? = nil,
        startDaemon: @escaping @MainActor () async -> Void = {}
    ) {
        self.probe = probe
        self.settings = settings
        self.onStartDaemon = startDaemon
    }

    /// Auto-present on first launch, or whenever there is no binary to control.
    public func shouldAutoPresent(daemonState: DaemonState) -> Bool {
        if daemonState == .notInstalled { return true }
        return settings?.hasCompletedOnboarding == false
    }

    public func state(of step: Step) -> CheckState {
        checks[step] ?? .unknown
    }

    /// Whether the wizard may move past `current`.
    public var canAdvance: Bool {
        guard current.isRequired else { return true }
        return state(of: current).isPass
    }

    public var isLastStep: Bool { current == .done }

    public func advance() {
        guard canAdvance, let index = Step.allCases.firstIndex(of: current),
              index + 1 < Step.allCases.count
        else { return }
        current = Step.allCases[index + 1]
    }

    public func goBack() {
        guard let index = Step.allCases.firstIndex(of: current), index > 0 else { return }
        current = Step.allCases[index - 1]
    }

    public func go(to step: Step) { current = step }

    /// Re-runs every detection. Called on entry and on a timer while a step is
    /// visible, so the wizard turns green on its own once the user acts.
    public func recheck() async {
        isRechecking = true
        defer { isRechecking = false }

        checks[.axonBinary] = await probe.axonInstalled()
            ? .pass : .fail("No `axon` binary found in the usual locations.")
        checks[.claudeCLI] = await probe.claudeInstalled()
            ? .pass : .fail("The `claude` CLI isn't on your PATH.")
        checks[.ollama] = await probe.ollamaReachable()
            ? .pass : .fail("Ollama isn't answering on 127.0.0.1:11434.")
        checks[.daemon] = await probe.daemonRunning()
            ? .pass : .fail("AXON isn't serving 127.0.0.1:7777 yet.")
    }

    /// The final step: start the daemon and open the dashboard (CFR-52).
    public func finish() async {
        await onStartDaemon()
        await recheck()
        settings?.hasCompletedOnboarding = true
    }

    /// Marks the wizard done without finishing every step — a user who already
    /// knows what they are doing should not be trapped in it.
    public func dismiss() {
        settings?.hasCompletedOnboarding = true
    }

    /// Everything required is green.
    public var isReady: Bool {
        Step.allCases.filter(\.isRequired).allSatisfy { state(of: $0).isPass }
    }
}
