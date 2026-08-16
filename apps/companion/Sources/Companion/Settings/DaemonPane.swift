import AxonKit
import SwiftUI

/// Budgets are editable; everything genuinely dangerous or rich (egress policy,
/// redaction, profiles) is **linked** to the dashboard and GUIDE rather than
/// replicated here (CFR-42).
struct DaemonPane: View {
    let settings: SettingsStore
    let app: AppModel

    @State private var dayBudget: Int?
    @State private var weekBudget: Int?
    @State private var draftDay = ""
    @State private var draftWeek = ""
    @State private var isApplying = false
    @State private var status: String?
    @State private var error: String?

    private var isDirty: Bool {
        Int(draftDay) != dayBudget || Int(draftWeek) != weekBudget
    }

    var body: some View {
        Form {
            Section("Profile") {
                ReadOnlyRow(title: "Profile", value: app.controller.health?.profile)
                ReadOnlyRow(
                    title: "Vault", value: app.vaultPath,
                    actionTitle: "Show", action: {
                        if let path = app.vaultPath { Opener.open(.revealInFinder(path: path)) }
                    }
                )
                ReadOnlyRow(
                    title: "Logs", value: app.dataDir.map { $0 + "/logs" },
                    actionTitle: "Show", action: {
                        if let dir = app.dataDir { Opener.open(.logsFolder(dataDir: dir)) }
                    }
                )
                ReadOnlyRow(
                    title: "Dashboard", value: "http://127.0.0.1:7777",
                    actionTitle: "Open", action: { Opener.open(.dashboard) }
                )
            }

            Section("Token budgets") {
                LabeledContent("Per day") {
                    TextField("tokens", text: $draftDay)
                        .textFieldStyle(.roundedBorder)
                        .frame(width: 130)
                        .monospacedDigit()
                }
                LabeledContent("Per week") {
                    TextField("tokens", text: $draftWeek)
                        .textFieldStyle(.roundedBorder)
                        .frame(width: 130)
                        .monospacedDigit()
                }
                HStack {
                    // Explicit Apply: budgets are the one setting where a
                    // mistyped digit costs real money, so no live-writing.
                    Button("Apply") { apply() }
                        .disabled(!isDirty || isApplying || !canEdit)
                    Button("Revert") { resetDrafts() }
                        .disabled(!isDirty || isApplying)
                    Spacer()
                    if let status {
                        Text(status).font(.caption).foregroundStyle(.secondary)
                    }
                }
                if let error {
                    Label(error, systemImage: "exclamationmark.triangle.fill")
                        .font(.caption)
                        .foregroundStyle(.orange)
                }
            }

            Section("Embeddings") {
                ReadOnlyRow(
                    title: "Provider",
                    value: [app.controller.health?.embeddingsProvider,
                            app.controller.health?.embeddingsModel]
                        .compactMap { $0 }.joined(separator: " · ")
                )
                Text(
                    "Switching providers re-embeds the whole vault. Do it with "
                        + "`axon configure embeddings` — see the GUIDE."
                )
                .font(.caption)
                .foregroundStyle(.secondary)
            }

            Section("Not shown here") {
                Text(
                    "Egress policy, redaction rules and profiles stay in the "
                        + "dashboard and config file, where their full context is."
                )
                .font(.caption)
                .foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .task { await load() }
    }

    private var canEdit: Bool { app.controller.canControlLifecycle }

    private func load() async {
        guard let budget = try? await settings.budget() else { return }
        dayBudget = budget.day
        weekBudget = budget.week
        resetDrafts()
    }

    private func resetDrafts() {
        draftDay = dayBudget.map(String.init) ?? ""
        draftWeek = weekBudget.map(String.init) ?? ""
        error = nil
        status = nil
    }

    private func apply() {
        Task {
            isApplying = true
            error = nil
            status = nil
            defer { isApplying = false }
            do {
                // The store writes and re-reads; what lands in the fields is
                // the daemon's own value, never the value we asked for.
                let applied = try await settings.setBudget(
                    day: Int(draftDay), week: Int(draftWeek)
                )
                dayBudget = applied.day
                weekBudget = applied.week
                resetDrafts()
                status = "Saved. Budgets apply to the next automation run."
            } catch {
                self.error = (error as? AxonCLIError)?.message ?? error.localizedDescription
            }
        }
    }
}
