import AxonKit
import SwiftUI

struct AutomationsPane: View {
    let settings: SettingsStore

    @State private var automations: [AutomationInfo] = []
    @State private var busy: String?
    @State private var error: String?

    var body: some View {
        VStack(spacing: 0) {
            if let error {
                Label(error, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(8)
            }

            List(automations) { automation in
                AutomationRow(
                    automation: automation,
                    isBusy: busy == automation.name,
                    onToggle: { enabled in
                        Task { @MainActor in toggle(automation.name, enabled: enabled) }
                    }
                )
            }
            .listStyle(.inset)
            .overlay {
                if automations.isEmpty {
                    ContentUnavailableView(
                        "No automations",
                        systemImage: "clock.badge.questionmark",
                        description: Text("AXON must be installed to list automations.")
                    )
                }
            }
        }
        .task { await reload() }
    }

    private func reload() async {
        do {
            automations = try await settings.automationList()
            error = nil
        } catch {
            self.error = (error as? AxonCLIError)?.message ?? error.localizedDescription
        }
    }

    private func toggle(_ name: String, enabled: Bool) {
        Task {
            busy = name
            defer { busy = nil }
            do {
                // Reflects reality, never optimism: the write can be vetoed by
                // profile policy, and the row must show what actually happened.
                automations = try await settings.setAutomation(name, enabled: enabled)
                error = nil
            } catch {
                self.error = (error as? AxonCLIError)?.message ?? error.localizedDescription
                await reload()
            }
        }
    }
}

struct AutomationRow: View {
    let automation: AutomationInfo
    let isBusy: Bool
    /// @Sendable because SwiftUI hands a Binding's setter across an isolation
    /// boundary; without it this is a data-race warning under strict concurrency.
    let onToggle: @Sendable (Bool) -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(automation.name).font(.body.monospaced())
                    if automation.isFree {
                        Tag(text: "no tokens", tint: .green)
                    }
                    if automation.essential == true {
                        Tag(text: "essential", tint: .secondary)
                    }
                }
                Text(subtitle)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
            Spacer(minLength: 8)

            if isBusy {
                ProgressView().controlSize(.small)
            } else {
                Toggle("", isOn: Binding(
                    get: { automation.enabled == true },
                    set: onToggle
                ))
                .labelsHidden()
                // A policy-forbidden automation gets a disabled switch and a
                // reason: flipping its config key would change the file and
                // change nothing about behaviour.
                .disabled(!automation.isTogglable)
                .help(
                    automation.isTogglable
                        ? "Enable or disable this automation"
                        : "This profile's policy does not allow this automation"
                )
            }
        }
        .padding(.vertical, 3)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("\(automation.name), \(automation.enabled == true ? "on" : "off"). \(subtitle)")
    }

    private var subtitle: String {
        var parts: [String] = []
        if let schedule = automation.schedule { parts.append(schedule) }
        if !automation.isTogglable {
            parts.append("blocked by profile policy")
        } else if let run = automation.lastRun {
            let when = AxonFormat.relative(run.finishedAt ?? run.startedAt)
            parts.append("last run \(run.status ?? "?") \(when)")
        } else {
            parts.append("never run")
        }
        return parts.joined(separator: " · ")
    }
}

struct Tag: View {
    let text: String
    let tint: Color

    var body: some View {
        Text(text)
            .font(.caption2)
            .padding(.horizontal, 5)
            .padding(.vertical, 1)
            .background(tint.opacity(0.15), in: .capsule)
            .foregroundStyle(tint)
    }
}
