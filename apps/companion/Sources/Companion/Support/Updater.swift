import AxonKit
import Sparkle
import SwiftUI

/// Companion's self-update, via Sparkle 2 on a signed appcast.
///
/// Companion and the AXON daemon update **independently**: this updates the
/// app, `axon update` updates the daemon. Conflating them would mean a
/// Companion update silently replacing the thing that actually does the work.
@MainActor
@Observable
final class UpdaterModel {
    private let controller: SPUStandardUpdaterController

    /// Whether automatic checks are on. Mirrored from Sparkle rather than
    /// stored separately, so the toggle always shows what Sparkle will do.
    var automaticallyChecks: Bool {
        get { controller.updater.automaticallyChecksForUpdates }
        set { controller.updater.automaticallyChecksForUpdates = newValue }
    }

    var canCheck: Bool { controller.updater.canCheckForUpdates }

    /// The feed this app checks, shown in About so the one piece of network
    /// egress Companion adds is disclosed rather than implied (CFR-81).
    var feedURL: String {
        controller.updater.feedURL?.absoluteString
            ?? Bundle.main.object(forInfoDictionaryKey: "SUFeedURL") as? String
            ?? "not configured"
    }

    var lastCheck: Date? { controller.updater.lastUpdateCheckDate }

    init() {
        // startingUpdater: true is safe for a menu bar app — Sparkle schedules
        // its own checks and does not present UI until it has something to say.
        controller = SPUStandardUpdaterController(
            startingUpdater: true, updaterDelegate: nil, userDriverDelegate: nil
        )
    }

    func checkForUpdates() {
        controller.updater.checkForUpdates()
    }
}
