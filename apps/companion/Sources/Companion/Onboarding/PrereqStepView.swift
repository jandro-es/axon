import AxonKit
import SwiftUI

/// One prerequisite page: what it is, the command to run, and a live check.
struct PrereqStepView: View {
    let step: OnboardingModel.Step
    let state: OnboardingModel.CheckState
    let isRechecking: Bool
    let onRecheck: () -> Void

    @State private var copied = false

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(spacing: 8) {
                CheckGlyph(state: state, isRechecking: isRechecking)
                Text(step.title).font(.title2.bold())
            }

            Text(step.explanation)
                .font(.callout)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            if let command = step.command {
                CommandBlock(command: command, copied: $copied)

                HStack(spacing: 8) {
                    Button("Copy") {
                        Opener.copy(command)
                        copied = true
                        Task {
                            try? await Task.sleep(for: .seconds(2))
                            copied = false
                        }
                    }
                    Button("Open Terminal") { Opener.openTerminal() }
                    Spacer()
                    Button("Check again", action: onRecheck)
                        .disabled(isRechecking)
                }
                .controlSize(.small)
            }

            if case .fail(let why) = state {
                Label(why, systemImage: "info.circle")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            if !step.isRequired, state != .pass, step != .welcome, step != .done {
                Text("This step is optional — you can continue without it.")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }

            Spacer(minLength: 0)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .contain)
    }
}

struct CommandBlock: View {
    let command: String
    @Binding var copied: Bool

    var body: some View {
        Text(command)
            .font(.system(.caption, design: .monospaced))
            .textSelection(.enabled)
            .padding(9)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(.quaternary.opacity(0.4), in: .rect(cornerRadius: 7))
            .overlay(alignment: .topTrailing) {
                if copied {
                    Text("Copied")
                        .font(.caption2)
                        .padding(.horizontal, 5)
                        .background(.tint, in: .capsule)
                        .foregroundStyle(.white)
                        .padding(4)
                }
            }
            .accessibilityLabel("Command to run: \(command)")
    }
}

struct CheckGlyph: View {
    let state: OnboardingModel.CheckState
    let isRechecking: Bool

    var body: some View {
        Group {
            if isRechecking, state != .pass {
                ProgressView().controlSize(.small)
            } else {
                Image(systemName: symbol).foregroundStyle(tint)
            }
        }
        .frame(width: 18, height: 18)
        .accessibilityLabel(label)
    }

    private var symbol: String {
        switch state {
        case .pass: "checkmark.circle.fill"
        case .fail: "circle.dashed"
        case .checking, .unknown: "circle"
        }
    }

    private var tint: Color {
        state == .pass ? .green : .secondary
    }

    private var label: String {
        switch state {
        case .pass: "ready"
        case .fail: "not ready yet"
        case .checking, .unknown: "checking"
        }
    }
}
