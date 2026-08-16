import AxonKit
import SwiftUI

/// The one surface most users ever see. Glanceable in under 2 s (PRD §4).
///
/// Reading order, top to bottom, is deliberate: who and how you are (header),
/// what it is costing (budget), what wants you (badges), what it has been doing
/// (tokens), where you want to go (destinations), and only then the controls
/// for the daemon itself.
struct StatusPopover: View {
    let controller: DaemonController
    let badges: BadgeCounts
    let sparkline: [TokenPoint]
    let vaultPath: String?
    let dataDir: String?

    @Environment(\.openWindow) private var openWindow

    /// Breathing room from the popover's own edge. The system draws no inset of
    /// its own, so without this the content sits flush against the chrome.
    private let edgeInset: CGFloat = 16

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            StatusHeader(state: controller.state, health: controller.health)

            switch controller.state {
            case .notInstalled:
                SetUpPrompt { open(WindowID.onboarding) }
            case .stopped, .unknown:
                StoppedBody(controller: controller)
            case .starting, .stopping, .runningWith:
                runningBody
            }

            if let error = controller.lastError {
                ErrorNote(message: error)
            }

            Divider().opacity(0.6)

            PopoverFooter(
                onDoctor: { open(WindowID.doctor) },
                onQuit: { NSApplication.shared.terminate(nil) }
            )
        }
        .padding(edgeInset)
        .frame(width: 328)
    }

    private func open(_ id: String) {
        openWindow(id: id)
        // An LSUIElement app is never "active", so a window it opens can land
        // behind whatever the user was looking at. Raise the app explicitly.
        NSApp.activate(ignoringOtherApps: true)
    }

    @ViewBuilder
    private var runningBody: some View {
        if let usage = controller.usage {
            BudgetBars(usage: usage)
        }

        BadgeRow(badges: badges)

        TokenTrendStrip(points: sparkline, dailyLimit: controller.usage?.dayLimit)

        // Destinations first and largest: opening one of these is why the
        // popover gets clicked.
        HStack(spacing: 9) {
            DestinationTile(title: "Dashboard", systemImage: "chart.bar.doc.horizontal", tint: .blue) {
                Opener.open(.dashboard)
            }
            DestinationTile(
                title: "Vault", systemImage: "book.closed.fill", tint: .purple,
                isDisabled: vaultPath == nil
            ) {
                if let vaultPath { Opener.open(.vaultInObsidian(vaultPath: vaultPath)) }
            }
            DestinationTile(title: "Insights", systemImage: "chart.xyaxis.line", tint: .teal) {
                open(WindowID.insights)
            }
        }

        LifecycleActions(
            state: controller.state,
            canControl: controller.canControlLifecycle,
            onStart: { Task { await controller.startDaemon() } },
            onStop: { Task { await controller.stopDaemon() } },
            onRestart: { Task { await controller.restartDaemon() } }
        )
    }
}

/// What the popover shows when the daemon is down: the one thing to do about it.
struct StoppedBody: View {
    let controller: DaemonController

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("AXON isn't running, so nothing is being indexed or automated.")
                .font(.callout)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            LifecycleActions(
                state: controller.state,
                canControl: controller.canControlLifecycle,
                onStart: { Task { await controller.startDaemon() } },
                onStop: {},
                onRestart: { Task { await controller.restartDaemon() } }
            )
        }
    }
}

// MARK: - header

struct StatusHeader: View {
    let state: DaemonState
    let health: AxonHealth?

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 8) {
                StatusDot(state: state)

                Text(health?.profile ?? "AXON")
                    .font(.system(.title3, design: .rounded).weight(.semibold))

                Spacer(minLength: 4)

                if let uptime = AxonFormat.uptime(health?.uptime(asOf: .now)) {
                    Label(uptime, systemImage: "clock")
                        .font(.caption2.monospacedDigit())
                        .foregroundStyle(.secondary)
                        .labelStyle(.titleAndIcon)
                        .accessibilityLabel("up for \(uptime)")
                }
            }

            HStack(spacing: 6) {
                Text(state.summary)
                    .foregroundStyle(state.needsAttention ? AnyShapeStyle(Color.orange) : AnyShapeStyle(.secondary))
                Text("·").foregroundStyle(.tertiary)
                Text(AxonFormat.version(health?.version))
                    .foregroundStyle(.tertiary)
            }
            .font(.caption)
            .accessibilityElement(children: .combine)
        }
    }
}

struct StatusDot: View {
    let state: DaemonState

    var body: some View {
        Circle()
            .fill(color.gradient)
            .frame(width: 9, height: 9)
            // A soft halo so the dot reads as a live indicator rather than a
            // bullet point.
            .shadow(color: color.opacity(0.6), radius: 3)
            .accessibilityHidden(true)
    }

    private var color: Color {
        switch state {
        case .runningWith(_, let reasons): reasons.isEmpty ? .green : .orange
        case .starting, .stopping, .unknown: .yellow
        case .stopped, .notInstalled: .secondary
        }
    }
}

/// The budget guard pauses **automations only** — interactive use is
/// unaffected — so the copy says that rather than "AXON is blocked".
struct GuardChip: View {
    let reason: String?

    var body: some View {
        Label("Automations paused — \(reason ?? "budget guard tripped")", systemImage: "pause.circle.fill")
            .font(.caption2)
            .foregroundStyle(.red)
            .padding(.horizontal, 8)
            .padding(.vertical, 5)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(.red.opacity(0.12), in: .rect(cornerRadius: 7))
            .fixedSize(horizontal: false, vertical: true)
    }
}

// MARK: - badges

struct BadgeCounts: Equatable {
    var review: Int?
    var actions: Int?

    var isEmpty: Bool { review == nil && actions == nil }
}

struct BadgeRow: View {
    let badges: BadgeCounts

    var body: some View {
        HStack(spacing: 9) {
            if let review = badges.review {
                BadgeCard(
                    title: "Review", count: review,
                    systemImage: "tray.full.fill", tint: .orange
                ) { Opener.open(.reviewTab) }
            }
            // Absent (not zero) when the profile disabled actions — the card
            // hides rather than showing a misleading 0.
            if let actions = badges.actions {
                BadgeCard(
                    title: "Actions", count: actions,
                    systemImage: "checklist", tint: .indigo
                ) { Opener.open(.actionsTab) }
            }
        }
    }
}

/// A queue that wants the user. Prominent by design: these are the two numbers
/// that mean "there is something for you to do".
struct BadgeCard: View {
    let title: String
    let count: Int
    let systemImage: String
    let tint: Color
    let action: () -> Void

    @State private var isHovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 9) {
                Image(systemName: systemImage)
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundStyle(tint.gradient)

                VStack(alignment: .leading, spacing: 0) {
                    Text("\(count)")
                        .font(.system(.title3, design: .rounded).weight(.bold).monospacedDigit())
                        .foregroundStyle(.primary)
                    Text(title)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                Spacer(minLength: 0)
            }
            .padding(.horizontal, 11)
            .padding(.vertical, 9)
            .frame(maxWidth: .infinity)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .background(tint.opacity(isHovering ? 0.20 : 0.12), in: .rect(cornerRadius: Glass.cornerRadius))
        .overlay(
            RoundedRectangle(cornerRadius: Glass.cornerRadius)
                .strokeBorder(tint.opacity(isHovering ? 0.5 : 0.25), lineWidth: 1)
        )
        .onHover { isHovering = $0 }
        .animation(.easeOut(duration: 0.12), value: isHovering)
        .accessibilityLabel("\(count) \(title.lowercased()) items. Opens the dashboard.")
    }
}

// MARK: - other states

struct SetUpPrompt: View {
    let onOpen: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("AXON isn't installed yet.")
                .font(.callout.weight(.medium))
            Text("Companion walks you through the prerequisites — it never installs anything itself.")
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Button("Set Up AXON…", action: onOpen)
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .frame(maxWidth: .infinity)
        }
    }
}

struct ErrorNote: View {
    let message: String

    var body: some View {
        Label(message, systemImage: "exclamationmark.triangle.fill")
            .font(.caption)
            .foregroundStyle(.orange)
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(9)
            .background(.orange.opacity(0.12), in: .rect(cornerRadius: 8))
    }
}

/// Doctor, Settings and Quit. Real controls rather than link-styled text —
/// the previous row read as leftover markup.
struct PopoverFooter: View {
    let onDoctor: () -> Void
    let onQuit: () -> Void

    var body: some View {
        HStack(spacing: 7) {
            FooterButton(title: "Doctor", systemImage: "stethoscope", action: onDoctor)

            SettingsLink {
                Label("Settings", systemImage: "gearshape.fill")
                    .font(.caption)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 6)
                    .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .foregroundStyle(.secondary)
            .background(.quaternary.opacity(0.5), in: .capsule)
            .simultaneousGesture(TapGesture().onEnded {
                NSApp.activate(ignoringOtherApps: true)
            })

            Spacer(minLength: 0)

            // ⌘Q quits Companion only — never the daemon.
            FooterButton(title: "Quit", systemImage: "power", action: onQuit)
                .accessibilityLabel("Quit Companion. AXON keeps running.")
        }
    }
}

struct FooterButton: View {
    let title: String
    let systemImage: String
    let action: () -> Void

    @State private var isHovering = false

    var body: some View {
        Button(action: action) {
            Label(title, systemImage: systemImage)
                .font(.caption)
                .padding(.horizontal, 10)
                .padding(.vertical, 6)
                .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .foregroundStyle(.secondary)
        .background(.quaternary.opacity(isHovering ? 0.9 : 0.5), in: .capsule)
        .onHover { isHovering = $0 }
        .animation(.easeOut(duration: 0.12), value: isHovering)
    }
}

/// Window identifiers, in one place so the popover and the app agree.
enum WindowID {
    static let insights = "insights"
    static let doctor = "doctor"
    static let onboarding = "onboarding"
}
