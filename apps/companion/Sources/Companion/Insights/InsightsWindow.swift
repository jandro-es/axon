import AxonKit
import SwiftUI

/// Chart cards land in Task 10; this is the shell so the popover's Insights
/// tile opens something real from Task 7 onward.
struct InsightsWindow: View {
    var body: some View {
        WindowPlaceholder(
            title: "Insights",
            systemImage: "chart.xyaxis.line",
            detail: "Live token, run, ingestion and vault charts arrive here."
        )
    }
}
