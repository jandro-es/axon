import AxonKit
import SwiftUI

@main
struct CompanionApp: App {
    @State private var app = AppModel()

    /// Development-only: render the popover in a real window (see below).
    private var previewsPopover: Bool {
        ProcessInfo.processInfo.environment["AXON_COMPANION_PREVIEW"] == "1"
    }

    var body: some Scene {
        MenuBarExtra {
            StatusPopover(
                controller: app.controller,
                badges: app.badges,
                sparkline: app.sparkline,
                vaultPath: app.vaultPath,
                dataDir: app.dataDir
            )
        } label: {
            MenuBarLabel(state: app.controller.state)
        }
        .menuBarExtraStyle(.window)

        // Shells until Tasks 10, 12 and 13 fill them in. Each remembers its
        // frame via SwiftUI's own scene restoration, keyed by the window id.
        Window("Axon Insights", id: WindowID.insights) {
            InsightsWindow()
                .environment(app)
                .frame(minWidth: 640, minHeight: 460)
                .activatesOnAppear()
        }
        .defaultSize(width: 900, height: 700)

        Window("Axon Doctor", id: WindowID.doctor) {
            DoctorWindow()
                .environment(app)
                .frame(minWidth: 520, minHeight: 400)
                .activatesOnAppear()
        }
        .defaultSize(width: 620, height: 560)

        Window("Welcome to Axon", id: WindowID.onboarding) {
            OnboardingWindow()
                .environment(app)
                .frame(minWidth: 520, minHeight: 440)
                .activatesOnAppear()
        }
        .defaultSize(width: 560, height: 480)
        .windowResizability(.contentMinSize)

        Settings {
            SettingsWindow(settings: app.settings, app: app)
        }

        // A plain window showing the popover's own content, so the layout can
        // be inspected and screenshotted during development. A MenuBarExtra
        // popover dismisses the moment anything else takes focus, which makes
        // it impossible to review any other way. Opt-in via environment; never
        // reachable in a normal launch.
        Window("Popover Preview", id: "popover-preview") {
            StatusPopover(
                controller: app.controller,
                badges: app.badges,
                sparkline: app.sparkline,
                vaultPath: app.vaultPath,
                dataDir: app.dataDir
            )
            .fixedSize()
        }
        .defaultSize(width: 360, height: 640)
        // Presented only when the environment asks for it; suppressed (and so
        // unreachable) in every normal launch.
        .defaultLaunchBehavior(previewsPopover ? .presented : .suppressed)
    }
}

private extension View {
    /// Brings the app and its new window forward.
    ///
    /// An LSUIElement app is not in the Dock and is never "active", so a window
    /// it opens can appear *behind* whatever the user was looking at — the
    /// click appears to do nothing. `NSApp.activate` alone is not enough: at
    /// `onAppear` the window is not yet key, so the raise has to happen on the
    /// next runloop pass, and the window itself must be ordered front.
    func activatesOnAppear() -> some View {
        onAppear {
            NSApp.activate(ignoringOtherApps: true)
            DispatchQueue.main.async {
                NSApp.activate(ignoringOtherApps: true)
                // The newest window is the one just opened.
                NSApp.windows.last { $0.isVisible && !$0.isMiniaturized }?
                    .makeKeyAndOrderFront(nil)
            }
        }
    }
}

/// Composition root: owns the clients, the controller and the popover's data.
///
/// Views stay free of I/O — they read this and render.
@MainActor
@Observable
final class AppModel {
    let controller: DaemonController
    let settings: SettingsStore
    let metrics: MetricsStore
    let updater = UpdaterModel()
    private(set) var badges = BadgeCounts()
    private(set) var sparkline: [TokenPoint] = []
    private(set) var vaultPath: String?
    private(set) var dataDir: String?

    private let delivery = NotificationDelivery()
    private let notifications: NotificationRouter
    private let client: DashboardClient
    private let sse: SSEClient
    private let binary: URL?
    /// Rebuilt once the daemon reports its service unit's PATH, so every child
    /// process sees the same machine the user's shell does.
    private var cli: AxonCLI?
    private var started = false

    init() {
        // An explicit path from Settings wins over discovery, so an install in
        // an unusual location does not leave Companion permanently blind.
        let explicit = UserDefaults.standard.string(
            forKey: SettingsStore.Key.explicitBinaryPath.rawValue
        )
        let binary = BinaryLocator.locate(explicit: explicit)
        let client = DashboardClient()
        // Start with the fallback PATH so even the first call sees Homebrew and
        // ~/.local/bin; `resolveToolPath()` upgrades it to the unit's PATH.
        let cli = binary.map { AxonCLI(binary: $0, pathEnv: nil) }

        self.binary = binary
        self.client = client
        self.cli = cli
        self.sse = SSEClient()
        let settings = SettingsStore(cli: cli)
        self.settings = settings
        self.metrics = MetricsStore(source: client)

        // The router reads preferences at decision time, so a toggle flipped in
        // Settings takes effect on the next event without any rewiring.
        let delivery = self.delivery
        self.notifications = NotificationRouter(
            prefs: { MainActor.assumeIsolated { settings.notifications } },
            post: { planned in
                Task { @MainActor in delivery.deliver(planned) }
            }
        )
        self.controller = DaemonController(
            reader: client,
            lifecycle: cli.map(AxonCLILifecycle.init(cli:)),
            // Re-resolved on every poll, so installing AXON while Companion is
            // running flips the icon out of .notInstalled without a relaunch.
            binaryPresent: { BinaryLocator.locate(explicit: explicit) != nil },
            usage: { try? await client.usage() }
        )

        // Start at launch, NOT from the popover's .task: a menu bar app's
        // popover content is only built when the user opens it, so monitoring
        // hung off it would leave the icon stale until first click — exactly
        // the ≤5s promise CFR-01 makes.
        Task { await start() }
    }

    func start() async {
        guard !started else { return }
        started = true

        await resolveToolPath()
        controller.startMonitoring()
        await refreshProfile()
        await refreshBadges()
        observeTransitions()

        // SSE drives two things: badge/sparkline freshness, and a fast health
        // re-probe on disconnect so a dead daemon is noticed well inside the
        // 5s budget rather than at the next poll tick (CFR-01).
        Task { [weak self] in
            guard let stream = await self?.sse.stream() else { return }
            for await state in stream {
                guard let self else { return }
                switch state {
                case .disconnected:
                    await controller.refresh()
                case .connected:
                    await refreshBadges()
                case .event(let event):
                    metrics.handle(event: event)
                    notifications.handle(event: event)
                    await handle(event)
                }
            }
        }
    }

    /// Feeds daemon state changes to the notification router.
    ///
    /// Polled alongside the controller rather than driven by a callback: the
    /// controller publishes its transitions, and one observer here keeps the
    /// controller free of any knowledge that notifications exist.
    private func observeTransitions() {
        Task { [weak self] in
            var seen: DaemonTransition?
            while !Task.isCancelled {
                guard let self else { return }
                if let transition = controller.lastTransition, transition != seen {
                    seen = transition
                    // The controller knows whether the user asked for a stop;
                    // the router must be told, or clicking Stop reports itself
                    // as a crash.
                    if controller.userInitiatedStop {
                        notifications.noteUserInitiatedStop()
                    }
                    notifications.handle(transition: transition)
                }
                if controller.state.attentionReasons.contains(.updateAvailable) {
                    notifications.handle(updateAvailable: controller.health?.latestVersion)
                }
                try? await Task.sleep(for: .seconds(2))
            }
        }
    }

    private func handle(_ event: AxonEvent) async {
        switch event.topic {
        case .tokens:
            await refreshSparkline()
        case .automations, .ingestion:
            await refreshBadges()
        case nil:
            break
        }
    }

    private func refreshBadges() async {
        badges = BadgeCounts(
            review: try? await client.reviewCount(),
            actions: (try? await client.actionsCount()) ?? nil
        )
        await refreshSparkline()
    }

    private func refreshSparkline() async {
        // A week of buckets makes a readable strip; a single day is one bar.
        sparkline = (try? await client.tokens(days: 7))?.points ?? []
    }

    /// Upgrades the child PATH to the one the daemon's own service unit uses.
    ///
    /// LaunchServices hands a GUI app `PATH=/usr/bin:/bin:/usr/sbin:/sbin`, so
    /// a child `axon doctor` reports claude, ollama and yt-dlp missing on a
    /// machine whose shell finds all three. The installed unit already carries
    /// a PATH resolved from the user's real shell (daemon v1.3.2+), so ask for
    /// that rather than guessing. Falls back to the static tool paths when
    /// there is no unit or the daemon is too old to report one.
    private func resolveToolPath() async {
        guard let binary else { return }
        let unitPath = try? await cli?.serviceStatus().pathEnv
        let resolved = ProcessCLIRunner.resolvedPath(preferring: unitPath ?? nil)
        cli = AxonCLI(binary: binary, pathEnv: resolved)
        controller.updateLifecycle(cli.map(AxonCLILifecycle.init(cli:)))
    }

    /// An onboarding model wired to real detection and the app's own start
    /// path, so the wizard's final button does exactly what the popover does.
    func makeOnboardingModel() -> OnboardingModel {
        OnboardingModel(
            probe: SystemPrerequisiteProbe(explicitBinaryPath: settings.explicitBinaryPath),
            settings: settings,
            startDaemon: { [controller] in await controller.startDaemon() }
        )
    }

    /// A Doctor model bound to the same CLI and dashboard the rest of the app
    /// uses. Built per-window rather than held, so a re-opened Doctor window
    /// starts from a fresh run rather than a stale report.
    func makeDoctorModel() -> DoctorModel {
        DoctorModel(cli: cli, health: { [client] in try await client.health() })
    }

    /// Vault path and data dir come from `axon profiles --json` — never parsed
    /// from YAML, and there is no `vault.path` config key (CFR-21).
    private func refreshProfile() async {
        guard let cli, let profiles = try? await cli.profiles() else { return }
        let active = profiles.first { $0.active } ?? profiles.first
        vaultPath = active?.vaultPath
        dataDir = active?.dataDir
    }
}
