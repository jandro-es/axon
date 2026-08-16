import AxonKit
import SwiftUI

struct SettingsWindow: View {
    let settings: SettingsStore
    let app: AppModel

    var body: some View {
        TabView {
            Tab("General", systemImage: "gearshape") {
                GeneralPane(settings: settings, app: app)
            }
            Tab("Daemon", systemImage: "cpu") {
                DaemonPane(settings: settings, app: app)
            }
            Tab("Automations", systemImage: "clock.arrow.trianglehead.counterclockwise.rotate.90") {
                AutomationsPane(settings: settings)
            }
            Tab("About", systemImage: "info.circle") {
                AboutPane(app: app)
            }
        }
        .frame(width: 520, height: 430)
    }
}

/// Rows shared by the panes: a labelled control, and a read-only value with an
/// optional action. Keeps the four panes visually identical.
struct SettingRow<Control: View>: View {
    let title: String
    var caption: String?
    @ViewBuilder let control: Control

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            control
            if let caption {
                Text(caption)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(title)
    }
}

struct ReadOnlyRow: View {
    let title: String
    let value: String?
    var actionTitle: String?
    var action: (() -> Void)?

    var body: some View {
        HStack {
            Text(title)
            Spacer()
            Text(value ?? "—")
                .foregroundStyle(.secondary)
                .textSelection(.enabled)
                .lineLimit(1)
                .truncationMode(.middle)
                .help(value ?? "")
            if let actionTitle, let action {
                Button(actionTitle, action: action)
                    .controlSize(.small)
                    .disabled(value == nil)
            }
        }
        .accessibilityElement(children: .combine)
    }
}
