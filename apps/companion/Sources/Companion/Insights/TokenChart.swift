import AxonKit
import Charts
import SwiftUI

/// Tokens per day, stacked. The reference implementation the other charts copy.
///
/// Both stackings come from the same `/api/tokens` response — grouping happens
/// client-side, never a second fetch (CONTRACT.md §4).
struct TokenChart: View {
    let points: [TokenPoint]
    let range: MetricsStore.Range

    enum Stacking: String, CaseIterable, Identifiable {
        case automation, model
        var id: String { rawValue }
        var title: String {
            switch self {
            case .automation: "By automation"
            case .model: "By model"
            }
        }
    }

    @State private var stacking: Stacking = .automation

    /// One bar segment: a day, a series name, and its token total.
    private struct Segment: Identifiable {
        let id: String
        let date: Date
        let series: String
        let tokens: Int64
    }

    private var segments: [Segment] {
        // Pre-grouped rather than filtered inline in the Chart body: the body
        // re-runs on every layout pass, and this is O(n) work per pass.
        let keyed = Dictionary(grouping: points) { point -> String in
            switch stacking {
            case .automation: point.label
            case .model: point.model
            }
        }
        return keyed.flatMap { series, points in
            Dictionary(grouping: points, by: \.day).compactMap { day, dayPoints -> Segment? in
                guard let date = DayString.date(from: day) else { return nil }
                let total = dayPoints.reduce(Int64(0)) { $0 + $1.total }
                guard total > 0 else { return nil }
                return Segment(id: "\(day)|\(series)", date: date, series: series, tokens: total)
            }
        }
        .sorted { $0.date < $1.date }
    }

    var body: some View {
        ChartCard(
            title: "Tokens",
            subtitle: "\(AxonFormat.tokens(points.reduce(0) { $0 + $1.total })) over the last \(range.title)",
            exportDataset: "tokens"
        ) {
            Picker("Stack", selection: $stacking) {
                ForEach(Stacking.allCases) { option in
                    Text(option.title).tag(option)
                }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()

            if segments.isEmpty {
                ChartEmptyState(message: "No tokens spent in this range.")
            } else {
                Chart(segments) { segment in
                    BarMark(
                        x: .value("Day", segment.date, unit: .day),
                        y: .value("Tokens", segment.tokens)
                    )
                    .foregroundStyle(by: .value(stacking.title, segment.series))
                }
                .chartLegend(position: .bottom, spacing: 8)
                .chartYAxis {
                    AxisMarks(format: IntegerFormatStyle<Int64>.number.notation(.compactName))
                }
                .chartXAxis {
                    AxisMarks(values: .stride(by: .day, count: range == .month ? 5 : 1))
                }
                .frame(height: 210)
                .accessibilityLabel("Tokens per day, stacked \(stacking.title.lowercased())")
            }
        }
    }
}

/// Day/week budget gauges plus the guard chip.
struct BudgetGauges: View {
    let usage: UsageSnapshot?

    var body: some View {
        ChartCard(title: "Budget", subtitle: "Same numbers as `axon status`") {
            if let usage {
                HStack(spacing: 28) {
                    BudgetDial(
                        title: "Day", fraction: usage.dayFraction,
                        used: usage.dayUsed, limit: usage.dayLimit
                    )
                    BudgetDial(
                        title: "Week", fraction: usage.weekFraction,
                        used: usage.weekUsed, limit: usage.weekLimit
                    )
                    Spacer()
                    if usage.isGuardTripped {
                        GuardBanner(reason: usage.guardReason)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            } else {
                ChartEmptyState(message: "Budget unavailable.")
            }
        }
    }
}

struct BudgetDial: View {
    let title: String
    let fraction: Double
    let used: Int64?
    let limit: Int64?

    var body: some View {
        VStack(spacing: 4) {
            Gauge(value: fraction) {
                Text(title)
            } currentValueLabel: {
                Text(AxonFormat.percent(fraction))
                    .font(.caption2.monospacedDigit())
            }
            .gaugeStyle(.accessoryCircularCapacity)
            .tint(fraction > 0.9 ? .red : fraction > 0.75 ? .orange : .accentColor)

            Text("\(AxonFormat.tokens(used)) / \(AxonFormat.tokens(limit))")
                .font(.caption2.monospacedDigit())
                .foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(
            "\(title) budget: \(AxonFormat.tokens(used)) of \(AxonFormat.tokens(limit)) tokens, "
                + AxonFormat.percent(fraction)
        )
    }
}

/// The guard pauses automations only — interactive use is unaffected. The copy
/// says so, because "AXON is blocked" would be wrong and alarming.
struct GuardBanner: View {
    let reason: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Label("Automations paused", systemImage: "pause.circle.fill")
                .font(.callout.bold())
                .foregroundStyle(.red)
            Text(reason ?? "The budget guard tripped.")
                .font(.caption)
                .foregroundStyle(.secondary)
            Text("Interactive use is unaffected.")
                .font(.caption2)
                .foregroundStyle(.tertiary)
        }
        .padding(10)
        .background(.red.opacity(0.1), in: .rect(cornerRadius: 8))
        .accessibilityElement(children: .combine)
    }
}
