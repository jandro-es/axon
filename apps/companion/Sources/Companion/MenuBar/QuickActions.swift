import AxonKit
import SwiftUI

/// A destination tile — Dashboard, Vault, Insights.
///
/// These are the primary actions: what people open the popover to do. They get
/// the icon size, the colour and the top row. Lifecycle controls sit below them
/// in a quieter form, because restarting the daemon is rare and stopping it is
/// rarer still.
struct DestinationTile: View {
    let title: String
    let systemImage: String
    let tint: Color
    var isDisabled = false
    let action: () -> Void

    @State private var isHovering = false

    var body: some View {
        Button(action: action) {
            VStack(spacing: 7) {
                Image(systemName: systemImage)
                    .font(.system(size: 19, weight: .semibold))
                    .foregroundStyle(tint.gradient)
                    .frame(height: 22)
                Text(title)
                    .font(.caption.weight(.medium))
                    .foregroundStyle(.primary)
                    .lineLimit(1)
                    .minimumScaleFactor(0.85)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 13)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .background(
            tint.opacity(isHovering && !isDisabled ? 0.18 : 0.10),
            in: .rect(cornerRadius: Glass.cornerRadius)
        )
        .overlay(
            RoundedRectangle(cornerRadius: Glass.cornerRadius)
                .strokeBorder(tint.opacity(isHovering && !isDisabled ? 0.5 : 0.22), lineWidth: 1)
        )
        .onHover { isHovering = $0 }
        .disabled(isDisabled)
        .opacity(isDisabled ? 0.4 : 1)
        .animation(.easeOut(duration: 0.12), value: isHovering)
        .accessibilityLabel(title)
    }
}

/// A lifecycle control. Deliberately smaller and quieter than a destination:
/// text beside a small glyph, no fill, no colour unless it is destructive.
struct LifecycleButton: View {
    let title: String
    let systemImage: String
    var role: ButtonRole?
    var isDisabled = false
    let action: () -> Void

    @State private var isHovering = false

    private var tint: Color { role == .destructive ? .red : .secondary }

    var body: some View {
        Button(action: action) {
            HStack(spacing: 5) {
                Image(systemName: systemImage).font(.system(size: 10, weight: .semibold))
                Text(title).font(.caption)
            }
            .foregroundStyle(role == .destructive ? AnyShapeStyle(Color.red) : AnyShapeStyle(.secondary))
            .padding(.horizontal, 10)
            .padding(.vertical, 6)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .background(tint.opacity(isHovering && !isDisabled ? 0.16 : 0.08), in: .capsule)
        .onHover { isHovering = $0 }
        .disabled(isDisabled)
        .opacity(isDisabled ? 0.4 : 1)
        .animation(.easeOut(duration: 0.12), value: isHovering)
        .accessibilityLabel(title)
    }
}

/// The lifecycle row: whichever controls the current state allows.
struct LifecycleActions: View {
    let state: DaemonState
    let canControl: Bool
    let onStart: () -> Void
    let onStop: () -> Void
    let onRestart: () -> Void

    @State private var confirmingStop = false

    var body: some View {
        HStack(spacing: 7) {
            if state.isRunning || state.isTransitioning {
                LifecycleButton(
                    title: state == .stopping ? "Stopping…" : "Restart",
                    systemImage: "arrow.clockwise",
                    isDisabled: !canControl || state.isTransitioning,
                    action: onRestart
                )
                LifecycleButton(
                    title: "Stop", systemImage: "stop.fill", role: .destructive,
                    isDisabled: !canControl || state.isTransitioning
                ) {
                    confirmingStop = true
                }
                Spacer(minLength: 0)
            } else {
                // Stopped is the one state where lifecycle IS the primary
                // action, so here it earns a full-width prominent button.
                Button(action: onStart) {
                    Label(state == .starting ? "Starting…" : "Start AXON", systemImage: "play.fill")
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 3)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(!canControl || state.isTransitioning)
            }
        }
        // Stopping is irreversible in the moment: anything mid-run is cut
        // short (CFR-12).
        .confirmationDialog("Stop AXON?", isPresented: $confirmingStop) {
            Button("Stop AXON", role: .destructive, action: onStop)
            Button("Cancel", role: .cancel) {}
        } message: {
            Text(
                """
                Any automation currently running is cut short, and scheduled \
                automations will not run until AXON is started again.

                If AXON is installed as a login service, macOS restarts it \
                automatically — use Settings to turn that off first.
                """
            )
        }
    }
}
