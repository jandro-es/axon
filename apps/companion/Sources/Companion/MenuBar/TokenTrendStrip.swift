import AxonKit
import Charts
import SwiftUI

/// Daily token spend plus what AXON avoided sending.
///
/// Replaces a flat blue silhouette that carried one fact (spend went up and
/// down) in a lot of space. The bars are coloured by how the day's spend sat
/// against the daily budget, so the strip answers "was any of this a problem?"
/// and not just "was there some?" — and the savings line gives the chart a
/// point of view rather than being a shape.
struct TokenTrendStrip: View {
    let points: [TokenPoint]
    let dailyLimit: Int64?

    private struct Day: Identifiable {
        let id: String
        let date: Date
        let total: Int64
    }

    private var days: [Day] {
        Dictionary(grouping: points, by: \.day)
            .compactMap { day, points -> Day? in
                guard let date = DayString.date(from: day) else { return nil }
                return Day(id: day, date: date, total: points.reduce(0) { $0 + $1.total })
            }
            .sorted { $0.date < $1.date }
    }

    private var savings: TokenSavings { TokenSavings(points: points) }
    private var total: Int64 { days.reduce(0) { $0 + $1.total } }

    /// Same green/amber/red vocabulary as the budget bars above, so a colour
    /// means one thing everywhere in the popover.
    private func tint(for day: Day) -> Color {
        guard let dailyLimit, dailyLimit > 0 else { return .accentColor }
        switch Double(day.total) / Double(dailyLimit) {
        case ..<0.6: return .green
        case ..<0.85: return .yellow
        case ..<0.95: return .orange
        default: return .red
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            HStack(alignment: .firstTextBaseline) {
                Label("Tokens", systemImage: "chart.bar.fill")
                    .font(.caption.weight(.medium))
                    .foregroundStyle(.secondary)
                Spacer()
                Text("\(AxonFormat.tokens(total)) in \(days.count)d")
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
            }

            if days.isEmpty {
                Text("No spend recorded yet.")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
                    .frame(maxWidth: .infinity, alignment: .center)
                    .frame(height: 44)
            } else {
                Chart(days) { day in
                    BarMark(
                        x: .value("Day", day.date, unit: .day),
                        y: .value("Tokens", day.total),
                        width: .ratio(0.62)
                    )
                    .foregroundStyle(tint(for: day).gradient)
                    .cornerRadius(2.5)
                }
                .chartXAxis(.hidden)
                .chartYAxis(.hidden)
                .chartLegend(.hidden)
                .frame(height: 44)
                .accessibilityLabel(
                    "Token spend over \(days.count) days, total \(AxonFormat.tokens(total))"
                )
            }

            if savings.hasSavings {
                SavingsLine(savings: savings)
            }
        }
    }
}

/// The savings claim, worded so a sceptic can check it.
struct SavingsLine: View {
    let savings: TokenSavings

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "leaf.fill")
                .font(.caption2)
                .foregroundStyle(.green)
                .accessibilityHidden(true)

            // "avoided", not "saved": cache reads are billed, just cheaply.
            // The exact wording is the difference between a true claim and a
            // flattering one.
            // One string LITERAL, not a concatenation: Text only parses
            // markdown when it can see a literal, so `a + b` renders the
            // asterisks verbatim.
            Text("**~\(AxonFormat.tokens(savings.avoided)) avoided** — \(AxonFormat.percent(savings.fraction)) of what AXON would have sent")
            .font(.caption2)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            Spacer(minLength: 0)
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 5)
        .background(.green.opacity(0.10), in: .rect(cornerRadius: 7))
        .help(savings.explanation)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Roughly \(AxonFormat.tokens(savings.avoided)) tokens avoided. \(savings.explanation)")
    }
}
