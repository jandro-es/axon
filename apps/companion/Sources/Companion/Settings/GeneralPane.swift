import AxonKit
import SwiftUI

struct GeneralPane: View {
    @Bindable var settings: SettingsStore
    let app: AppModel

    @Environment(\.openWindow) private var openWindow
    @State private var daemonStartsAtLogin = false
    @State private var shareExtension: ShareExtensionState = .unknown
    @State private var isTogglingService = false

    var body: some View {
        Form {
            // The two login toggles are deliberately separate and are worded to
            // make the distinction obvious (CFR-11) — one is this app, the
            // other is the daemon that does the actual work.
            Section("Start at login") {
                Toggle("Open Axon Companion at login", isOn: $settings.launchCompanionAtLogin)
                Text("The menu bar item only. Quitting Companion never stops AXON.")
                    .font(.caption)
                    .foregroundStyle(.secondary)

                Toggle("Run AXON at login", isOn: Binding(
                    get: { daemonStartsAtLogin },
                    set: { newValue in
                        Task {
                            isTogglingService = true
                            await settings.setDaemonServiceInstalled(newValue)
                            // Re-read: the CLI owns this, and the install can
                            // fail for reasons Companion cannot predict.
                            daemonStartsAtLogin = await settings.daemonServiceInstalled()
                            isTogglingService = false
                        }
                    }
                ))
                .disabled(isTogglingService || !app.controller.canControlLifecycle)
                Text("Installs the launchd service so automations run whether or not you're signed in to this app.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Section("Updates & status") {
                Picker("Check AXON every", selection: $settings.refreshSeconds) {
                    ForEach([2, 5, 10, 15, 30], id: \.self) { seconds in
                        Text("\(seconds) seconds").tag(seconds)
                    }
                }
                Text("How often the menu bar icon re-checks the daemon.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Section("Notifications") {
                NotificationToggles(settings: settings)
            }

            // CFR-99: a registered extension stays out of the Share menu until
            // the user switches it on. Without this row the failure mode is
            // silence.
            Section("Share extension") {
                ReadOnlyRow(
                    title: "Share menu",
                    value: shareExtensionLabel,
                    actionTitle: shareExtension == .enabled ? nil : "Open Extensions Settings…",
                    action: shareExtension == .enabled ? nil : {
                        if let url = OpenAction.extensionsSettings.url() {
                            NSWorkspace.shared.open(url)
                        }
                    }
                )
                Text("Adds Axon to the Share menu in Safari and other apps. Shared links and selections land in your inbox.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Section {
                Button("Run Setup Again…") {
                    openWindow(id: WindowID.onboarding)
                }
            }

            if let error = settings.lastError {
                Section {
                    Label(error, systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                        .font(.caption)
                }
            }
        }
        .formStyle(.grouped)
        .task {
            daemonStartsAtLogin = await settings.daemonServiceInstalled()
            shareExtension = await ShareExtensionProbe().state()
        }
    }

    private var shareExtensionLabel: String {
        switch shareExtension {
        case .enabled: "Enabled"
        case .registeredButDisabled: "Not enabled"
        case .notRegistered: "Not installed — launch Axon from /Applications once"
        case .unknown: "Unknown"
        }
    }
}

/// Success is never notified, so there is no toggle for it (CFR-71).
struct NotificationToggles: View {
    let settings: SettingsStore

    var body: some View {
        let prefs = settings.notifications
        Toggle("An automation fails", isOn: binding(\.automationFailed))
        Toggle("The budget guard pauses automations", isOn: binding(\.budgetGuardTripped))
        Toggle("AXON stops unexpectedly", isOn: binding(\.daemonStoppedUnexpectedly))
        Toggle("An AXON update is available", isOn: binding(\.updateAvailable))
        Text("Companion never notifies you about routine success.")
            .font(.caption)
            .foregroundStyle(.secondary)
            .accessibilityHidden(true)
        let _ = prefs
    }

    private func binding(_ keyPath: WritableKeyPath<NotificationPrefs, Bool>) -> Binding<Bool> {
        Binding(
            get: { settings.notifications[keyPath: keyPath] },
            set: { newValue in
                var prefs = settings.notifications
                prefs[keyPath: keyPath] = newValue
                settings.notifications = prefs
            }
        )
    }
}
