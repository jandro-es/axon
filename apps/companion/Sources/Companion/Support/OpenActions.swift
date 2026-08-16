import AppKit
import AxonKit
import SwiftUI

/// Performs an ``OpenAction``. URL *construction* is tested in AxonKit; this is
/// only the AppKit handoff.
@MainActor
enum Opener {
    /// Opens the action's target, returning false when there was nothing to
    /// open. Callers disable the control rather than offering a dead button.
    @discardableResult
    static func open(_ action: OpenAction, baseURL: URL = DashboardClient.defaultBaseURL) -> Bool {
        guard let url = action.url(baseURL: baseURL) else { return false }

        if action.isFileURL {
            // Reveal rather than open: a folder should land in Finder, and a
            // file the user probably wants to see in context, not launch.
            guard FileManager.default.fileExists(atPath: url.path) else { return false }
            NSWorkspace.shared.activateFileViewerSelecting([url])
            return true
        }

        if url.scheme == "obsidian" {
            // Obsidian may not be installed. NSWorkspace.open returns false
            // when no app claims the scheme, so fall back to Finder instead of
            // failing silently — the user still gets to their notes.
            if NSWorkspace.shared.open(url) { return true }
            return openFallbackPath(for: action)
        }

        return NSWorkspace.shared.open(url)
    }

    /// Reveals the underlying filesystem path when a URL scheme has no handler.
    private static func openFallbackPath(for action: OpenAction) -> Bool {
        let path: String? =
            switch action {
            case .vaultInObsidian(let vaultPath): vaultPath
            case .todaysDailyNote(let vaultPath, _): vaultPath
            default: nil
            }
        guard let path, FileManager.default.fileExists(atPath: path) else { return false }
        NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: path)])
        return true
    }

    /// Opens Terminal, for onboarding's "run this command" steps (CFR-50).
    /// Companion shows the command and opens a terminal; it never runs
    /// installers itself (CFR-51).
    static func openTerminal() {
        let terminal = URL(fileURLWithPath: "/System/Applications/Utilities/Terminal.app")
        NSWorkspace.shared.openApplication(at: terminal, configuration: .init())
    }

    /// Copies text and returns it, for the "copy this command" buttons.
    @discardableResult
    static func copy(_ text: String) -> String {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        return text
    }
}
