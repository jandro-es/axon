import AxonKit
import SwiftUI

@main
struct CompanionApp: App {
    var body: some Scene {
        // Placeholder presence. Real icon states and the popover land in Task 7;
        // this exists so the build/package/launch loop is provable from Task 2.
        MenuBarExtra("Axon", systemImage: "brain") {
            VStack(alignment: .leading, spacing: 4) {
                Text("AXON").font(.headline)
                Text(DaemonState.unknown.summary)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .padding()
        }
        .menuBarExtraStyle(.window)
    }
}
