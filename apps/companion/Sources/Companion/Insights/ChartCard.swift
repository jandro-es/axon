import AppKit
import AxonKit
import SwiftUI

/// The shared frame every chart sits in: title, range-aware subtitle, export
/// menu, and a **quiet opaque background**.
///
/// Explicitly not glass (PRD §4): translucency behind a plot makes gridlines
/// and thin series fight whatever is on the desktop, and a chart that is hard
/// to read is worse than one that is plain.
struct ChartCard<Content: View>: View {
    let title: String
    var subtitle: String?
    /// The `/api/export` dataset name, or nil for a card with nothing to export.
    var exportDataset: String?
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 1) {
                    Text(title).font(.headline)
                    if let subtitle {
                        Text(subtitle).font(.caption).foregroundStyle(.secondary)
                    }
                }
                Spacer()
                if let exportDataset {
                    ExportMenu(dataset: exportDataset)
                }
            }
            content
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .axonCard()
    }
}

/// Downloads a chart's series through the daemon's own `/api/export`.
///
/// Companion adds no serialisation of its own (CFR-31) — the CSV the user gets
/// is byte-identical to the dashboard's.
struct ExportMenu: View {
    let dataset: String
    @State private var isExporting = false
    @State private var error: String?

    var body: some View {
        Menu {
            Button("Export as CSV…") { export(format: "csv") }
            Button("Export as JSON…") { export(format: "json") }
        } label: {
            Image(systemName: isExporting ? "arrow.down.circle.dotted" : "square.and.arrow.down")
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .disabled(isExporting)
        .help(
            // The export covers the endpoint's own window, which is not the
            // on-screen range picker. Saying so beats a silently different file.
            "Exports the full series the daemon holds, not the selected range"
        )
        .accessibilityLabel("Export \(dataset)")
        .alert("Export failed", isPresented: .constant(error != nil)) {
            Button("OK") { error = nil }
        } message: {
            Text(error ?? "")
        }
    }

    private func export(format: String) {
        let client = DashboardClient()
        let day = DayFormatter.today()
        let panel = NSSavePanel()
        panel.nameFieldStringValue = client.exportFilename(
            dataset: dataset, format: format, day: day
        )
        panel.canCreateDirectories = true
        panel.isExtensionHidden = false

        guard panel.runModal() == .OK, let destination = panel.url else { return }
        isExporting = true

        Task {
            defer { isExporting = false }
            do {
                let (data, response) = try await URLSession.shared.data(
                    from: client.exportURL(dataset: dataset, format: format)
                )
                if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
                    // The daemon returns a plain-text reason on 400.
                    error = String(decoding: data, as: UTF8.self)
                    return
                }
                try data.write(to: destination)
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}

enum DayFormatter {
    static func today() -> String {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter.string(from: .now)
    }
}

/// What a chart shows when it has no data — and why, which is the part that
/// matters. "No data" and "the daemon is stopped" need different responses.
struct ChartEmptyState: View {
    let message: String
    var actionTitle: String?
    var action: (() -> Void)?

    var body: some View {
        VStack(spacing: 8) {
            Text(message)
                .font(.callout)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            if let actionTitle, let action {
                Button(actionTitle, action: action)
                    .controlSize(.small)
            }
        }
        .frame(maxWidth: .infinity)
        .frame(height: 140)
    }
}
