import AxonKit
import SwiftUI

/// A paged wizard that walks the machine to green (CFR-50/52).
struct OnboardingWindow: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var model: OnboardingModel?

    /// How often a visible step re-checks. Frequent enough that the wizard
    /// turns green moments after the user's command finishes, cheap enough
    /// that it costs nothing: every probe is a local file stat or a 2s
    /// loopback request.
    private static let recheckInterval: Duration = .seconds(3)

    var body: some View {
        Group {
            if let model {
                content(model)
            } else {
                ProgressView()
            }
        }
        .task {
            if model == nil { model = app.makeOnboardingModel() }
            await model?.recheck()
        }
    }

    @ViewBuilder
    private func content(_ model: OnboardingModel) -> some View {
        VStack(spacing: 0) {
            TabView(selection: Binding(get: { model.current }, set: { model.go(to: $0) })) {
                ForEach(OnboardingModel.Step.allCases, id: \.self) { step in
                    Group {
                        if step == .done {
                            CelebrationPage(model: model, app: app, onClose: { dismiss() })
                        } else {
                            PrereqStepView(
                                step: step,
                                state: model.state(of: step),
                                isRechecking: model.isRechecking,
                                onRecheck: { Task { await model.recheck() } }
                            )
                        }
                    }
                    .padding(22)
                    .tag(step)
                }
            }
            .tabViewStyle(.automatic)

            Divider()
            WizardFooter(model: model, onSkip: {
                model.dismiss()
                dismiss()
            })
        }
        // A visible step re-checks on a timer so the wizard turns green on its
        // own once the user runs the command — no "click to continue" dance.
        .task(id: model.current) {
            while !Task.isCancelled {
                try? await Task.sleep(for: Self.recheckInterval)
                if Task.isCancelled { return }
                await model.recheck()
            }
        }
    }
}

struct WizardFooter: View {
    let model: OnboardingModel
    let onSkip: () -> Void

    var body: some View {
        HStack {
            Button("Skip setup", action: onSkip)
                .buttonStyle(.link)
                .controlSize(.small)

            Spacer()

            StepDots(current: model.current, model: model)

            Spacer()

            if model.current != .welcome {
                Button("Back") { model.goBack() }
            }
            if !model.isLastStep {
                Button("Continue") { model.advance() }
                    .buttonStyle(.borderedProminent)
                    .disabled(!model.canAdvance)
                    .help(model.canAdvance ? "" : "This step is required before continuing")
            }
        }
        .padding(12)
    }
}

struct StepDots: View {
    let current: OnboardingModel.Step
    let model: OnboardingModel

    var body: some View {
        HStack(spacing: 5) {
            ForEach(OnboardingModel.Step.allCases, id: \.self) { step in
                Circle()
                    .fill(fill(for: step))
                    .frame(width: 6, height: 6)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(
            "Step \((OnboardingModel.Step.allCases.firstIndex(of: current) ?? 0) + 1) "
                + "of \(OnboardingModel.Step.allCases.count): \(current.title)"
        )
    }

    private func fill(for step: OnboardingModel.Step) -> Color {
        if step == current { return .accentColor }
        return model.state(of: step).isPass ? .green.opacity(0.6) : .secondary.opacity(0.35)
    }
}

/// The final page: start the daemon and open the dashboard (CFR-52).
struct CelebrationPage: View {
    let model: OnboardingModel
    let app: AppModel
    let onClose: () -> Void

    @State private var isStarting = false

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(spacing: 8) {
                Image(systemName: model.isReady ? "checkmark.seal.fill" : "exclamationmark.triangle.fill")
                    .font(.title)
                    .foregroundStyle(model.isReady ? .green : .orange)
                Text(model.isReady ? "You're set" : "Almost there")
                    .font(.title2.bold())
            }

            Text(
                model.isReady
                    ? OnboardingModel.Step.done.explanation
                    : "Some prerequisites aren't ready yet. You can finish them any time — "
                        + "Settings has a Run Setup Again button."
            )
            .font(.callout)
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)

            HStack(spacing: 8) {
                Button(isStarting ? "Starting…" : "Start AXON") {
                    Task {
                        isStarting = true
                        await model.finish()
                        isStarting = false
                    }
                }
                .buttonStyle(.borderedProminent)
                .disabled(isStarting || model.state(of: .daemon).isPass)

                Button("Open Dashboard") { Opener.open(.dashboard) }
                    .disabled(!model.state(of: .daemon).isPass)

                Spacer()
                Button("Done") {
                    model.dismiss()
                    onClose()
                }
            }

            Spacer(minLength: 0)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}
