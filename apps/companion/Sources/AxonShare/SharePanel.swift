import SwiftUI

/// The compose panel (CFR-97). Deliberately plain SwiftUI: `Glass.swift` lives
/// in the Companion executable target and AxonKit may not import SwiftUI, and a
/// share panel is system chrome with a small custom body.
struct SharePanel: View {
    @Bindable var model: SharePanelModel
    @FocusState private var noteFocused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(model.payload.title.isEmpty ? "Capture to Axon" : model.payload.title)
                .font(.headline)
                .lineLimit(2)

            if !model.payload.url.isEmpty {
                Text(model.payload.url)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }

            TextEditor(text: $model.note)
                .font(.body)
                .scrollContentBackground(.hidden)
                .padding(4)
                .frame(minHeight: 96)
                .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(.separator))
                .focused($noteFocused)
                .accessibilityLabel("Note")

            if let failure = model.failure {
                Label(failure, systemImage: "exclamationmark.triangle")
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
            }

            HStack {
                Spacer()
                Button("Cancel") { model.cancelPressed() }
                    .keyboardShortcut(.cancelAction)
                Button(model.busy ? "Capturing…" : "Capture") { model.capturePressed() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(!model.canCapture)
            }
        }
        .padding(16)
        .frame(width: 420)
        .onAppear { noteFocused = true }
    }
}
