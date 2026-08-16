import AxonKit
import SwiftUI

/// One tile in the popover's action grid.
struct QuickActionTile: View {
    let title: String
    let systemImage: String
    var isProminent = false
    var isDisabled = false
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 4) {
                Image(systemName: systemImage)
                    .font(.system(size: 15, weight: .medium))
                    .frame(height: 18)
                Text(title)
                    .font(.caption2)
                    .lineLimit(1)
                    .minimumScaleFactor(0.8)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 9)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .foregroundStyle(isProminent ? AnyShapeStyle(.tint) : AnyShapeStyle(.primary))
        // Interactive glass only on tiles that actually respond to input.
        .axonGlass(interactive: !isDisabled, cornerRadius: 10)
        .disabled(isDisabled)
        .opacity(isDisabled ? 0.45 : 1)
        .accessibilityLabel(title)
    }
}

/// The lifecycle row: Start, Stop or Restart, whichever the state allows.
struct LifecycleActions: View {
    let state: DaemonState
    let canControl: Bool
    let onStart: () -> Void
    let onStop: () -> Void
    let onRestart: () -> Void

    @State private var confirmingStop = false

    var body: some View {
        HStack(spacing: Glass.tileSpacing) {
            if state.isRunning || state.isTransitioning {
                QuickActionTile(
                    title: "Restart", systemImage: "arrow.clockwise",
                    isDisabled: !canControl || state.isTransitioning, action: onRestart
                )
                QuickActionTile(
                    title: "Stop", systemImage: "stop.fill",
                    isDisabled: !canControl || state.isTransitioning
                ) {
                    confirmingStop = true
                }
            } else {
                QuickActionTile(
                    title: state == .starting ? "Starting…" : "Start",
                    systemImage: "play.fill",
                    isProminent: true,
                    isDisabled: !canControl || state.isTransitioning,
                    action: onStart
                )
            }
        }
        // Stopping is destructive-ish and irreversible in the moment: anything
        // mid-run is cut short (CFR-12).
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
