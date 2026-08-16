import Foundation
import Testing

@testable import AxonKit

/// A probe whose answers the test sets, so a wizard flow can be described as a
/// sequence of machine states.
actor ScriptedProbe: PrerequisiteProbing {
    var axon: Bool
    var claude: Bool
    var ollama: Bool
    var daemon: Bool

    init(axon: Bool = false, claude: Bool = false, ollama: Bool = false, daemon: Bool = false) {
        self.axon = axon
        self.claude = claude
        self.ollama = ollama
        self.daemon = daemon
    }

    func set(axon: Bool? = nil, claude: Bool? = nil, ollama: Bool? = nil, daemon: Bool? = nil) {
        if let axon { self.axon = axon }
        if let claude { self.claude = claude }
        if let ollama { self.ollama = ollama }
        if let daemon { self.daemon = daemon }
    }

    func axonInstalled() async -> Bool { axon }
    func claudeInstalled() async -> Bool { claude }
    func ollamaReachable() async -> Bool { ollama }
    func daemonRunning() async -> Bool { daemon }
}

@MainActor
private func makeSettings() throws -> SettingsStore {
    let suite = "com.axon.companion.onboarding.\(UUID().uuidString)"
    let defaults = try #require(UserDefaults(suiteName: suite))
    return SettingsStore(defaults: defaults, cli: nil, loginItem: FakeLoginItem())
}

@Suite @MainActor
struct OnboardingModelTests {
    @Test func startsAtWelcomeWithNothingChecked() {
        let model = OnboardingModel(probe: ScriptedProbe())

        #expect(model.current == .welcome)
        #expect(model.state(of: .axonBinary) == .unknown)
        // Welcome is not a gate — the user can always read past it.
        #expect(model.canAdvance)
    }

    @Test func recheckReflectsTheMachine() async {
        let probe = ScriptedProbe(axon: true, claude: false, ollama: true, daemon: false)
        let model = OnboardingModel(probe: probe)

        await model.recheck()

        #expect(model.state(of: .axonBinary) == .pass)
        #expect(model.state(of: .ollama) == .pass)
        if case .fail(let why) = model.state(of: .claudeCLI) {
            #expect(!why.isEmpty)
        } else {
            Issue.record("expected claude to fail")
        }
    }

    /// A failing required step is a wall: advancing past it would produce a
    /// "you're set" page on a machine that is not set.
    @Test func aFailingRequiredStepBlocksAdvancing() async {
        let model = OnboardingModel(probe: ScriptedProbe())
        await model.recheck()
        model.go(to: .axonBinary)

        #expect(model.canAdvance == false)
        model.advance()
        #expect(model.current == .axonBinary)
    }

    @Test func aPassingRequiredStepAllowsAdvancing() async {
        let model = OnboardingModel(probe: ScriptedProbe(axon: true))
        await model.recheck()
        model.go(to: .axonBinary)

        #expect(model.canAdvance)
        model.advance()
        #expect(model.current == .claudeCLI)
    }

    /// Ollama is skippable — the Apple on-device embeddings path exists, and
    /// blocking someone who chose it would be wrong.
    @Test func ollamaIsSkippableEvenWhenItFails() async {
        let model = OnboardingModel(probe: ScriptedProbe(axon: true, claude: true))
        await model.recheck()
        model.go(to: .ollama)

        #expect(model.state(of: .ollama) != .pass)
        #expect(model.canAdvance)
        model.advance()
        #expect(model.current == .daemon)
    }

    @Test func onlyAxonAndClaudeAreRequired() {
        let required = OnboardingModel.Step.allCases.filter(\.isRequired)
        #expect(required == [.axonBinary, .claudeCLI])
    }

    /// The live re-check is the whole point: the user runs a command in
    /// Terminal and the wizard turns green without them clicking anything.
    @Test func recheckFlipsFailToPassWhenTheMachineChanges() async {
        let probe = ScriptedProbe()
        let model = OnboardingModel(probe: probe)
        await model.recheck()
        #expect(model.state(of: .axonBinary) != .pass)

        await probe.set(axon: true)
        await model.recheck()

        #expect(model.state(of: .axonBinary) == .pass)
    }

    @Test func readinessRequiresEveryRequiredStep() async {
        let probe = ScriptedProbe(axon: true)
        let model = OnboardingModel(probe: probe)
        await model.recheck()
        #expect(model.isReady == false)

        await probe.set(claude: true)
        await model.recheck()
        #expect(model.isReady)
    }

    // MARK: completion

    @Test func finishStartsTheDaemonAndMarksCompletion() async throws {
        let probe = ScriptedProbe(axon: true, claude: true)
        let settings = try makeSettings()
        let started = Counter()
        let model = OnboardingModel(
            probe: probe, settings: settings,
            startDaemon: { await started.increment(); await probe.set(daemon: true) }
        )

        await model.finish()

        #expect(await started.value == 1)
        #expect(model.state(of: .daemon) == .pass)
        #expect(settings.hasCompletedOnboarding)
    }

    @Test func dismissMarksCompletionWithoutFinishing() throws {
        let settings = try makeSettings()
        let model = OnboardingModel(probe: ScriptedProbe(), settings: settings)

        model.dismiss()

        // A user who already knows what they are doing must not be trapped.
        #expect(settings.hasCompletedOnboarding)
    }

    // MARK: auto-presentation

    @Test func autoPresentsOnFirstLaunch() throws {
        let settings = try makeSettings()
        let model = OnboardingModel(probe: ScriptedProbe(), settings: settings)

        #expect(model.shouldAutoPresent(daemonState: .stopped))

        settings.hasCompletedOnboarding = true
        #expect(model.shouldAutoPresent(daemonState: .stopped) == false)
    }

    /// A missing binary re-presents the wizard even after completion — the
    /// user uninstalled AXON, or Companion moved to a fresh machine.
    @Test func autoPresentsWheneverTheBinaryIsMissing() throws {
        let settings = try makeSettings()
        settings.hasCompletedOnboarding = true
        let model = OnboardingModel(probe: ScriptedProbe(), settings: settings)

        #expect(model.shouldAutoPresent(daemonState: .notInstalled))
    }

    // MARK: content

    @Test func everyActionableStepOffersACopyableCommand() {
        #expect(OnboardingModel.Step.axonBinary.command?.contains("install-macos.sh") == true)
        #expect(OnboardingModel.Step.claudeCLI.command?.contains("claude login") == true)
        #expect(OnboardingModel.Step.ollama.command?.contains("nomic-embed-text") == true)
        // Starting the daemon is a button, not a command to paste.
        #expect(OnboardingModel.Step.daemon.command == nil)
    }

    @Test func everyStepExplainsItselfInPlainLanguage() {
        for step in OnboardingModel.Step.allCases {
            #expect(!step.title.isEmpty)
            #expect(step.explanation.count > 40, "step \(step) has no real explanation")
        }
    }

    /// Companion guides but never installs (CFR-51). The commands it shows are
    /// for the user to run; nothing here may invoke a package manager.
    @Test func noStepClaimsCompanionWillInstallAnything() {
        for step in OnboardingModel.Step.allCases {
            let text = step.explanation.lowercased()
            #expect(!text.contains("companion will install"))
            #expect(!text.contains("we'll install"))
        }
        #expect(OnboardingModel.Step.welcome.explanation.contains("never installs"))
    }
}

actor Counter {
    private(set) var value = 0
    func increment() { value += 1 }
}
