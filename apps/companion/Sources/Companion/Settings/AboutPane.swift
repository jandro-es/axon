import AxonKit
import SwiftUI

struct AboutPane: View {
    let app: AppModel

    private var companionVersion: String {
        let info = Bundle.main.infoDictionary
        let short = info?["CFBundleShortVersionString"] as? String ?? "0.0.0"
        let build = info?["CFBundleVersion"] as? String ?? "0"
        return "\(short) (\(build))"
    }

    var body: some View {
        Form {
            Section {
                HStack(spacing: 12) {
                    Image(systemName: "brain.fill")
                        .font(.system(size: 34))
                        .foregroundStyle(.tint)
                    VStack(alignment: .leading, spacing: 1) {
                        Text("Axon Companion").font(.title3.bold())
                        Text("An optional menu bar companion for AXON.")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                .padding(.vertical, 4)
            }

            Section("Versions") {
                ReadOnlyRow(title: "Companion", value: companionVersion)
                ReadOnlyRow(title: "AXON daemon", value: AxonFormat.version(app.controller.health?.version))
                if let latest = app.controller.health?.latestVersion,
                   app.controller.health?.updateAvailable == true {
                    ReadOnlyRow(title: "Latest AXON", value: latest)
                }
            }

            Section("Updates") {
                Toggle(
                    "Check for Companion updates automatically",
                    isOn: Binding(
                        get: { app.updater.automaticallyChecks },
                        set: { app.updater.automaticallyChecks = $0 }
                    )
                )
                HStack {
                    Button("Check Now") { app.updater.checkForUpdates() }
                        .disabled(!app.updater.canCheck)
                    Spacer()
                    if let last = app.updater.lastCheck {
                        Text("Last checked \(AxonFormat.relative(last))")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                // The one piece of network egress Companion adds beyond
                // loopback, disclosed rather than implied (CFR-81).
                Text("Checks \(app.updater.feedURL). Companion and the AXON daemon update independently — this never touches the daemon.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            Section("Open") {
                Button("Dashboard") { Opener.open(.dashboard) }
                Button("Vault in Obsidian") {
                    if let path = app.vaultPath { Opener.open(.vaultInObsidian(vaultPath: path)) }
                }
                .disabled(app.vaultPath == nil)
                Button("Logs") {
                    if let dir = app.dataDir { Opener.open(.logsFolder(dataDir: dir)) }
                }
                .disabled(app.dataDir == nil)
            }

            // Everything Companion does has a CLI or dashboard equivalent, and
            // saying so is the complementarity promise in writing (PRD §1).
            Section("Companion is optional") {
                Text(
                    "AXON is fully functional without this app: `axon status`, "
                        + "`axon doctor` and the dashboard do everything here. "
                        + "Removing Companion leaves no trace in the daemon."
                )
                .font(.caption)
                .foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
    }
}
