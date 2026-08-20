import AppIntents
import AxonKit
import Foundation

// The Siri / Shortcuts / Spotlight verbs (CFR-92…95, docs/21 M4). Every
// intent is pure-REST against the daemon through AxonKit — never the CLI (an
// intent process inherits the minimal LaunchServices PATH; the 1.3.4 lesson).
// Vault content is answered on demand only; nothing is indexed into
// Spotlight. Plain App Intents API throughout — no macOS-27-only symbols, so
// the app's macOS 26 floor is untouched.

// MARK: - shared

private let axonDownDialog = IntentDialog(
    "Axon isn't running. Open Axon Companion to start it.")

private func intentClient() -> DashboardClient { DashboardClient() }

// MARK: - CFR-92 Search

struct SearchVaultIntent: AppIntent {
    static let title: LocalizedStringResource = "Search Vault"
    static let description = IntentDescription(
        "Search your Axon vault (lexical + semantic). Read-only; spends no tokens.")

    @Parameter(title: "Query", requestValueDialog: "What should I search your vault for?")
    var query: String

    static var parameterSummary: some ParameterSummary {
        Summary("Search the vault for \(\.$query)")
    }

    func perform() async throws -> some IntentResult & ProvidesDialog {
        do {
            let hits = try await intentClient().search(query)
            guard !hits.isEmpty else {
                return .result(dialog: "No notes matched “\(query)”.")
            }
            let names = hits.prefix(3).map { noteName($0.path) }
            let more = hits.count > 3 ? " and \(hits.count - 3) more" : ""
            return .result(dialog: IntentDialog(
                "Top matches: \(names.joined(separator: ", "))\(more)."))
        } catch DashboardError.unreachable {
            return .result(dialog: axonDownDialog)
        } catch DashboardError.badStatus(404) {
            return .result(dialog: "Search is switched off for this profile.")
        }
    }
}

// MARK: - CFR-93 Ask

struct AskVaultIntent: AppIntent {
    static let title: LocalizedStringResource = "Ask Vault"
    static let description = IntentDescription(
        "Ask a question answered strictly from your own notes, with citations. Uses your Claude budget through Axon's token manager.")

    @Parameter(title: "Question", requestValueDialog: "What do you want to ask your vault?")
    var question: String

    static var parameterSummary: some ParameterSummary {
        Summary("Ask the vault \(\.$question)")
    }

    func perform() async throws -> some IntentResult & ProvidesDialog {
        do {
            let a = try await intentClient().ask(question)
            if a.refused {
                let why = a.reason.map { " (\($0))" } ?? ""
                return .result(dialog: IntentDialog(
                    "Your notes don't answer that\(why). Nothing was spent."))
            }
            var text = a.answer ?? ""
            if let cites = a.citations, !cites.isEmpty {
                text += " — from \(cites.prefix(2).map(noteName).joined(separator: " and "))"
            }
            if a.conflicted == true {
                text = "Heads up — your sources disagree. " + text
            }
            return .result(dialog: IntentDialog(stringLiteral: text))
        } catch DashboardError.unreachable {
            return .result(dialog: axonDownDialog)
        } catch DashboardError.badStatus(404) {
            return .result(dialog: "Asking is switched off for this profile.")
        }
    }
}

// MARK: - CFR-94 Tasks

struct CheckTasksIntent: AppIntent {
    static let title: LocalizedStringResource = "Check Tasks"
    static let description = IntentDescription(
        "How many tasks are open in your vault. Read-only; spends no tokens.")

    func perform() async throws -> some IntentResult & ProvidesDialog {
        do {
            guard let open = try await intentClient().actionsCount() else {
                return .result(dialog: "Task tracking is switched off for this profile.")
            }
            let dialog = open == 0
                ? "No open tasks. Clear runway."
                : "You have \(open) open task\(open == 1 ? "" : "s") in your vault."
            return .result(dialog: IntentDialog(stringLiteral: dialog))
        } catch DashboardError.unreachable {
            return .result(dialog: axonDownDialog)
        }
    }
}

// MARK: - CFR-95 Capture

struct CaptureThoughtIntent: AppIntent {
    static let title: LocalizedStringResource = "Capture Thought"
    static let description = IntentDescription(
        "Save a thought into your vault's inbox. Creates a new note under 00-Inbox — never edits anything.")

    @Parameter(title: "Thought", requestValueDialog: "What should I capture?")
    var thought: String

    static var parameterSummary: some ParameterSummary {
        Summary("Capture \(\.$thought) into the inbox")
    }

    func perform() async throws -> some IntentResult & ProvidesDialog {
        do {
            try await intentClient().capture(text: thought)
            return .result(dialog: "Captured to your Axon inbox.")
        } catch DashboardError.unreachable {
            return .result(dialog: axonDownDialog)
        } catch DashboardError.badStatus(404) {
            return .result(dialog: "Capture is switched off for this profile.")
        }
    }
}

// MARK: - App Shortcuts (Siri phrases)

struct AxonShortcuts: AppShortcutsProvider {
    static var appShortcuts: [AppShortcut] {
        AppShortcut(
            intent: AskVaultIntent(),
            phrases: ["Ask \(.applicationName)", "Ask my vault with \(.applicationName)"],
            shortTitle: "Ask Vault",
            systemImageName: "brain.head.profile")
        AppShortcut(
            intent: SearchVaultIntent(),
            phrases: ["Search \(.applicationName)", "Search my vault in \(.applicationName)"],
            shortTitle: "Search Vault",
            systemImageName: "magnifyingglass")
        AppShortcut(
            intent: CheckTasksIntent(),
            phrases: ["\(.applicationName) tasks", "Check my \(.applicationName) tasks"],
            shortTitle: "Check Tasks",
            systemImageName: "checklist")
        AppShortcut(
            intent: CaptureThoughtIntent(),
            phrases: ["Capture in \(.applicationName)", "\(.applicationName) capture"],
            shortTitle: "Capture Thought",
            systemImageName: "tray.and.arrow.down")
    }
}

// MARK: - helpers

/// "03-Resources/Knowledge/sqlite-internals.md" → "sqlite-internals": Siri
/// reads names, not paths.
private func noteName(_ path: String) -> String {
    let base = (path as NSString).lastPathComponent
    return base.hasSuffix(".md") ? String(base.dropLast(3)) : base
}
