import SwiftUI

/// A window shell that says what it will hold.
///
/// Deliberately explicit rather than an empty window: a blank window reads as a
/// bug, and these ship in intermediate commits.
struct WindowPlaceholder: View {
    let title: String
    let systemImage: String
    let detail: String

    var body: some View {
        ContentUnavailableView {
            Label(title, systemImage: systemImage)
        } description: {
            Text(detail)
        }
        .frame(minWidth: 420, minHeight: 280)
    }
}
