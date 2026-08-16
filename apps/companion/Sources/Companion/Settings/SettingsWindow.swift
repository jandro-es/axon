import AxonKit
import SwiftUI

/// Panes land in Task 11; this is the scene so ⌘, works from Task 8 onward.
struct SettingsWindow: View {
    let settings: SettingsStore
    let app: AppModel

    var body: some View {
        WindowPlaceholder(
            title: "Settings",
            systemImage: "gearshape",
            detail: "General, Daemon, Automations and About panes arrive here."
        )
        .frame(width: 520, height: 380)
    }
}
