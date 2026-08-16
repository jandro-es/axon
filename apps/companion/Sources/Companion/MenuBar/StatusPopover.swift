import AxonKit
import SwiftUI

/// The one surface most users ever see. Budget: glanceable in under 2 seconds
/// (PRD §4), so anything that needs reading goes to Insights or the dashboard.
struct StatusPopover: View {
    let controller: DaemonController
    let badges: BadgeCounts
    let sparkline: [TokenPoint]
    let vaultPath: String?
    let dataDir: String?

    @Environment(\.openWindow) private var openWindow

    var body: some View {
        VStack(alignment: .leading, spacing: Glass.tileSpacing) {
            AxonGlassGroup {
                VStack(alignment: .leading, spacing: Glass.tileSpacing) {
                    StatusHeader(state: controller.state, health: controller.health)

                    // Every state renders something actionable — never a bare
                    // "not running" with nothing to do about it.
                    switch controller.state {
                    case .notInstalled:
                        SetUpPrompt { openWindow(id: WindowID.onboarding) }
                    case .stopped, .unknown:
                        lifecycle
                    case .starting, .stopping, .runningWith:
                        runningBody
                    }
                }
            }

            if let error = controller.lastError {
                ErrorNote(message: error)
            }

            PopoverFooter(
                onDoctor: { openWindow(id: WindowID.doctor) },
                onQuit: { NSApplication.shared.terminate(nil) }
            )
        }
        .padding(Glass.tileSpacing)
        .frame(width: 300)
    }

    @ViewBuilder
    private var runningBody: some View {
        if let usage = controller.usage {
            BudgetGaugePair(usage: usage)
        }

        BadgeRow(badges: badges)

        if !sparkline.isEmpty {
            TokenSparkline(points: sparkline)
        }

        lifecycle

        HStack(spacing: Glass.tileSpacing) {
            QuickActionTile(title: "Dashboard", systemImage: "chart.bar.doc.horizontal") {
                Opener.open(.dashboard)
            }
            QuickActionTile(
                title: "Vault", systemImage: "book.closed",
                isDisabled: vaultPath == nil
            ) {
                if let vaultPath { Opener.open(.vaultInObsidian(vaultPath: vaultPath)) }
            }
            QuickActionTile(title: "Insights", systemImage: "chart.xyaxis.line") {
                openWindow(id: WindowID.insights)
            }
        }
    }

    private var lifecycle: some View {
        LifecycleActions(
            state: controller.state,
            canControl: controller.canControlLifecycle,
            onStart: { Task { await controller.startDaemon() } },
            onStop: { Task { await controller.stopDaemon() } },
            onRestart: { Task { await controller.restartDaemon() } }
        )
    }
}

// MARK: - header

struct StatusHeader: View {
    let state: DaemonState
    let health: AxonHealth?

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            StatusDot(state: state)

            Text(health?.profile ?? "AXON")
                .font(.headline)

            Spacer(minLength: 4)

            // The uptime pill is hidden — not zeroed — when the daemon cannot
            // say, which is any daemon predating the started_at seam.
            if let uptime = AxonFormat.uptime(health?.uptime(asOf: .now)) {
                Text(uptime)
                    .font(.caption2.monospacedDigit())
                    .padding(.horizontal, 5)
                    .padding(.vertical, 1)
                    .background(.quaternary, in: .capsule)
                    .accessibilityLabel("up for \(uptime)")
            }
        }

        HStack(spacing: 4) {
            Text(state.summary)
            Text("·")
            Text(AxonFormat.version(health?.version))
        }
        .font(.caption)
        .foregroundStyle(.secondary)
        .accessibilityElement(children: .combine)
    }
}

struct StatusDot: View {
    let state: DaemonState

    var body: some View {
        Circle()
            .fill(color)
            .frame(width: 7, height: 7)
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

// MARK: - budget

struct BudgetGaugePair: View {
    let usage: UsageSnapshot

    var body: some View {
        HStack(spacing: 14) {
            BudgetGauge(
                title: "Day", fraction: usage.dayFraction,
                used: usage.dayUsed, limit: usage.dayLimit
            )
            BudgetGauge(
                title: "Week", fraction: usage.weekFraction,
                used: usage.weekUsed, limit: usage.weekLimit
            )

            if usage.isGuardTripped {
                GuardChip(reason: usage.guardReason)
            }
            Spacer(minLength: 0)
        }
    }
}

struct BudgetGauge: View {
    let title: String
    let fraction: Double
    let used: Int64?
    let limit: Int64?

    var body: some View {
        Gauge(value: fraction) {
            Text(title)
        } currentValueLabel: {
            Text(AxonFormat.tokens(used))
                .font(.system(size: 9).monospacedDigit())
        }
        .gaugeStyle(.accessoryCircularCapacity)
        .scaleEffect(0.62)
        .frame(width: 46, height: 46)
        .tint(fraction > 0.9 ? .red : fraction > 0.75 ? .orange : .accentColor)
        .accessibilityLabel(
            "\(title) budget: \(AxonFormat.tokens(used)) of \(AxonFormat.tokens(limit)) tokens, "
                + AxonFormat.percent(fraction)
        )
    }
}

/// The guard pauses **automations only** — interactive use is unaffected — so
/// the copy says "paused automations", not "AXON is blocked".
struct GuardChip: View {
    let reason: String?

    var body: some View {
        Label("Automations paused", systemImage: "pause.circle.fill")
            .font(.caption2)
            .foregroundStyle(.red)
            .labelStyle(.titleAndIcon)
            .help(reason ?? "budget guard tripped")
            .accessibilityLabel("Budget guard paused automations. \(reason ?? "")")
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
        HStack(spacing: Glass.tileSpacing) {
            if let review = badges.review {
                BadgeButton(title: "Review", count: review, systemImage: "tray.full") {
                    Opener.open(.reviewTab)
                }
            }
            // Absent (not zero) when the profile disabled actions — the badge
            // hides rather than showing a misleading 0.
            if let actions = badges.actions {
                BadgeButton(title: "Actions", count: actions, systemImage: "checklist") {
                    Opener.open(.actionsTab)
                }
            }
            Spacer(minLength: 0)
        }
    }
}

struct BadgeButton: View {
    let title: String
    let count: Int
    let systemImage: String
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 4) {
                Image(systemName: systemImage).font(.caption2)
                Text(title).font(.caption)
                Text("\(count)")
                    .font(.caption.monospacedDigit().bold())
                    .padding(.horizontal, 5)
                    .background(.tint.opacity(0.18), in: .capsule)
            }
            .padding(.horizontal, 7)
            .padding(.vertical, 4)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .axonGlass(interactive: true, cornerRadius: 8)
        .accessibilityLabel("\(count) \(title.lowercased()) items. Opens the dashboard.")
    }
}

// MARK: - sparkline

/// 24 h of token spend. Sits directly under the budget gauges so the popover
/// tells one story — what you are spending — rather than two half-stories.
struct TokenSparkline: View {
    let points: [TokenPoint]

    private var totals: [(day: String, total: Int64)] {
        Dictionary(grouping: points, by: \.day)
            .map { (day: $0.key, total: $0.value.reduce(0) { $0 + $1.total }) }
            .sorted { $0.day < $1.day }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack {
                Text("Tokens").font(.caption2).foregroundStyle(.secondary)
                Spacer()
                Text(AxonFormat.tokens(totals.reduce(0) { $0 + $1.total }))
                    .font(.caption2.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            SparklineShape(values: totals.map { Double($0.total) })
                .fill(.tint.opacity(0.75))
                .frame(height: 22)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(
            "Token spend, last \(totals.count) days, "
                + "total \(AxonFormat.tokens(totals.reduce(0) { $0 + $1.total }))"
        )
    }
}

/// A filled area sparkline. Hand-drawn rather than a Swift Chart: the popover
/// wants a 22pt strip with no axes, legend or hit-testing, and a Chart brings
/// all three plus its layout cost.
struct SparklineShape: Shape {
    let values: [Double]

    func path(in rect: CGRect) -> Path {
        var path = Path()
        guard values.count > 1, let peak = values.max(), peak > 0 else {
            path.addRect(CGRect(x: 0, y: rect.maxY - 1, width: rect.width, height: 1))
            return path
        }

        let step = rect.width / CGFloat(values.count - 1)
        path.move(to: CGPoint(x: 0, y: rect.maxY))
        for (index, value) in values.enumerated() {
            let x = CGFloat(index) * step
            let y = rect.maxY - CGFloat(value / peak) * rect.height
            path.addLine(to: CGPoint(x: x, y: y))
        }
        path.addLine(to: CGPoint(x: rect.maxX, y: rect.maxY))
        path.closeSubpath()
        return path
    }
}

// MARK: - other states

struct SetUpPrompt: View {
    let onOpen: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("AXON isn't installed yet.")
                .font(.callout)
            Text("Companion guides you through the prerequisites — it never installs anything itself.")
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Button("Set Up AXON…", action: onOpen)
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
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
            .padding(6)
            .axonCard(cornerRadius: 8)
    }
}

struct PopoverFooter: View {
    let onDoctor: () -> Void
    let onQuit: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            Button("Doctor", action: onDoctor)
            SettingsLink { Text("Settings…") }
            Spacer()
            // ⌘Q quits Companion only — never the daemon.
            Button("Quit", action: onQuit)
                .accessibilityLabel("Quit Companion. AXON keeps running.")
        }
        .buttonStyle(.link)
        .font(.caption)
        .padding(.horizontal, 2)
    }
}

/// Window identifiers, in one place so the popover and the app agree.
enum WindowID {
    static let insights = "insights"
    static let doctor = "doctor"
    static let onboarding = "onboarding"
}
