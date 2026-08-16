import AxonKit
import SwiftUI

/// Replaced by the real check list in Task 12.
struct DoctorWindow: View {
    var body: some View {
        WindowPlaceholder(
            title: "Doctor",
            systemImage: "stethoscope",
            detail: "`axon doctor` output with per-check remediation arrives here."
        )
    }
}
