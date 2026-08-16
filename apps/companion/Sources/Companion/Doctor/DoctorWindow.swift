import AppKit
import AxonKit
import SwiftUI

/// Reproduces `axon doctor` verbatim, including its remediation text (CFR-60).
///
/// Works with the daemon stopped — that is exactly when it is needed.
struct DoctorWindow: View {
    @Environment(AppModel.self) private var app
    @State private var model: DoctorModel?
    @State private var copied = false

    var body: some View {
        Group {
            if let model {
                content(model)
            } else {
                ProgressView()
            }
        }
        .task {
            if model == nil { model = app.makeDoctorModel() }
            await model?.run()
        }
    }

    @ViewBuilder
    private func content(_ model: DoctorModel) -> some View {
        VStack(spacing: 0) {
            if let error = model.error {
                Label(error, systemImage: "exclamationmark.triangle.fill")
                    .font(.callout)
                    .foregroundStyle(.orange)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(12)
            }

            if let report = model.report {
                DoctorSummary(report: report)
                    .padding(.horizontal, 14)
                    .padding(.top, 10)

                List(report.sortedBySeverity) { check in
                    DoctorRow(check: check)
                }
                .listStyle(.inset)
            } else if !model.running && model.error == nil {
                ContentUnavailableView(
                    "No report yet",
                    systemImage: "stethoscope",
                    description: Text("Run the checks to see prerequisite health.")
                )
            }
        }
        .toolbar {
            ToolbarItem {
                Button {
                    Task { await model.run() }
                } label: {
                    Label("Re-run", systemImage: "arrow.clockwise")
                }
                .disabled(model.running)
            }
            ToolbarItem {
                Button {
                    Task {
                        Opener.copy(await model.diagnosticsText())
                        copied = true
                        try? await Task.sleep(for: .seconds(2))
                        copied = false
                    }
                } label: {
                    Label(
                        copied ? "Copied" : "Copy Diagnostics",
                        systemImage: copied ? "checkmark" : "doc.on.doc"
                    )
                }
                .help("Copies a redacted report: versions, doctor output and /health. No vault content, no secrets.")
            }
        }
        .overlay(alignment: .bottom) {
            if model.running {
                ProgressView().controlSize(.small).padding(6)
            }
        }
    }
}

struct DoctorSummary: View {
    let report: DoctorReport

    var body: some View {
        HStack(spacing: 14) {
            StatusPill(
                systemImage: report.status == .ok ? "checkmark.circle.fill" : "xmark.circle.fill",
                tint: report.status == .ok ? .green : .red,
                text: report.status == .ok ? "All clear" : "Blocking issues"
            )
            if report.failCount > 0 { CountPill(count: report.failCount, label: "failed", tint: .red) }
            if report.warnCount > 0 { CountPill(count: report.warnCount, label: "warnings", tint: .orange) }
            CountPill(count: report.passCount, label: "passing", tint: .green)
            Spacer()
        }
        .accessibilityElement(children: .combine)
    }
}

struct StatusPill: View {
    let systemImage: String
    let tint: Color
    let text: String

    var body: some View {
        Label(text, systemImage: systemImage)
            .font(.callout.bold())
            .foregroundStyle(tint)
    }
}

struct CountPill: View {
    let count: Int
    let label: String
    let tint: Color

    var body: some View {
        Text("\(count) \(label)")
            .font(.caption)
            .padding(.horizontal, 7)
            .padding(.vertical, 2)
            .background(tint.opacity(0.15), in: .capsule)
            .foregroundStyle(tint)
    }
}

struct DoctorRow: View {
    let check: DoctorCheck
    @State private var copied = false

    var body: some View {
        HStack(alignment: .top, spacing: 9) {
            Image(systemName: glyph)
                .foregroundStyle(tint)
                .font(.body)
                .accessibilityHidden(true)

            VStack(alignment: .leading, spacing: 2) {
                Text(check.name)
                    .font(.body.monospaced())
                // The daemon folds its remediation into `detail`, so this IS
                // the remediation text — rendered verbatim and selectable so
                // a suggested command can be copied straight out.
                Text(check.detail)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }

            Spacer(minLength: 6)

            Button {
                Opener.copy("\(check.name): \(check.detail)")
                copied = true
                Task {
                    try? await Task.sleep(for: .seconds(2))
                    copied = false
                }
            } label: {
                Image(systemName: copied ? "checkmark" : "doc.on.doc")
            }
            .buttonStyle(.borderless)
            .help("Copy this check")
            .accessibilityLabel("Copy \(check.name)")
        }
        .padding(.vertical, 3)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("\(check.name), \(check.status.rawValue): \(check.detail)")
    }

    private var glyph: String {
        switch check.status {
        case .ok: "checkmark.circle.fill"
        case .warn: "exclamationmark.triangle.fill"
        case .fail: "xmark.circle.fill"
        }
    }

    private var tint: Color {
        switch check.status {
        case .ok: .green
        case .warn: .orange
        case .fail: .red
        }
    }
}
