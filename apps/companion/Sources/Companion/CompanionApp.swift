import AxonKit
import SwiftUI

@main
struct CompanionApp: App {
    @State private var app = AppModel()

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

        // Shells until Tasks 10, 12 and 13 fill them in.
        Window("Axon Insights", id: WindowID.insights) {
            InsightsWindow()
        }
        Window("Axon Doctor", id: WindowID.doctor) {
            DoctorWindow()
        }
        Window("Welcome to Axon", id: WindowID.onboarding) {
            OnboardingWindow()
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
    private(set) var badges = BadgeCounts()
    private(set) var sparkline: [TokenPoint] = []
    private(set) var vaultPath: String?
    private(set) var dataDir: String?

    private let client: DashboardClient
    private let sse: SSEClient
    private let cli: AxonCLI?
    private var started = false

    init() {
        let binary = BinaryLocator.locate()
        let client = DashboardClient()
        let cli = binary.map { AxonCLI(binary: $0) }

        self.client = client
        self.cli = cli
        self.sse = SSEClient()
        self.controller = DaemonController(
            reader: client,
            lifecycle: cli.map(AxonCLILifecycle.init(cli:)),
            // Re-resolved on every poll, so installing AXON while Companion is
            // running flips the icon out of .notInstalled without a relaunch.
            binaryPresent: { BinaryLocator.locate() != nil },
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

        controller.startMonitoring()
        await refreshProfile()
        await refreshBadges()

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
                    await handle(event)
                }
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

    /// Vault path and data dir come from `axon profiles --json` — never parsed
    /// from YAML, and there is no `vault.path` config key (CFR-21).
    private func refreshProfile() async {
        guard let cli, let profiles = try? await cli.profiles() else { return }
        let active = profiles.first { $0.active } ?? profiles.first
        vaultPath = active?.vaultPath
        dataDir = active?.dataDir
    }
}
