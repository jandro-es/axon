import AxonKit
import Charts
import SwiftUI

/// Automation runs as a timeline, coloured by outcome, with a success-rate rule.
struct RunsChart: View {
    let runs: [RunRecord]
    let successRate: Double?
    let range: MetricsStore.Range

    /// Fixed colours per outcome so the legend means the same thing here as in
    /// the Ingestion chart below it.
    static func color(for outcome: RunRecord.Outcome) -> Color {
        switch outcome {
        case .ok: .green
        case .skipped: .secondary
        case .failed: .red
        case .running: .blue
        case .unknown: .orange
        }
    }

    private struct Row: Identifiable {
        let id: Int64
        let automation: String
        let started: Date
        let outcome: RunRecord.Outcome
        let duration: TimeInterval
        let label: String
    }

    private var rows: [Row] {
        runs.compactMap { record in
            guard let started = record.startedAt else { return nil }
            return Row(
                id: record.id,
                automation: record.automation,
                started: started,
                outcome: record.outcome,
                duration: record.duration ?? 0,
                label: Self.outcomeLabel(record.outcome)
            )
        }
    }

    private static func outcomeLabel(_ outcome: RunRecord.Outcome) -> String {
        switch outcome {
        case .ok: "ok"
        case .skipped: "skipped"
        case .failed: "failed"
        case .running: "running"
        case .unknown: "unknown"
        }
    }

    private var summary: String {
        let failed = runs.count { $0.outcome == .failed }
        let skipped = runs.count { $0.outcome == .skipped }
        var parts = ["\(runs.count) runs"]
        if failed > 0 { parts.append("\(failed) failed") }
        if skipped > 0 { parts.append("\(skipped) skipped") }
        if let successRate {
            parts.append("\(AxonFormat.percent(successRate)) success")
        }
        return parts.joined(separator: " · ")
    }

    var body: some View {
        ChartCard(title: "Automation runs", subtitle: summary, exportDataset: "runs") {
            if rows.isEmpty {
                ChartEmptyState(message: "No automation runs in this range.")
            } else {
                Chart(rows) { row in
                    PointMark(
                        x: .value("Started", row.started),
                        y: .value("Automation", row.automation)
                    )
                    .foregroundStyle(by: .value("Outcome", row.label))
                    .symbolSize(row.outcome == .failed ? 90 : 45)
                }
                .chartForegroundStyleScale([
                    "ok": Color.green, "skipped": Color.secondary,
                    "failed": Color.red, "running": Color.blue, "unknown": Color.orange,
                ])
                .chartLegend(position: .bottom, spacing: 8)
                .chartYAxis {
                    AxisMarks(preset: .aligned, position: .leading)
                }
                // Height scales with the number of automations so rows never
                // collide; a fixed height turns 16 automations into a smear.
                .frame(height: max(180, CGFloat(Set(rows.map(\.automation)).count) * 22 + 60))
                .accessibilityLabel("Automation runs over the last \(range.title), coloured by outcome")
            }
        }
    }
}

/// Ingestion throughput: sources per day split by outcome, with embed queue depth.
struct IngestionChart: View {
    let buckets: [SourceBucket]
    let queueDepth: Int?
    let range: MetricsStore.Range

    private struct Bar: Identifiable {
        let id: String
        let date: Date
        let status: String
        let count: Int
    }

    private var bars: [Bar] {
        buckets.compactMap { bucket in
            guard let date = bucket.date else { return nil }
            return Bar(id: bucket.id, date: date, status: bucket.status, count: bucket.count)
        }
    }

    private var subtitle: String {
        let ok = buckets.filter { $0.status == "ok" }.reduce(0) { $0 + $1.count }
        let problems = buckets.filter { $0.status != "ok" }.reduce(0) { $0 + $1.count }
        var parts = ["\(ok) ingested"]
        if problems > 0 { parts.append("\(problems) failed or redacted") }
        if let queueDepth, queueDepth > 0 { parts.append("\(queueDepth) chunks awaiting embedding") }
        return parts.joined(separator: " · ")
    }

    var body: some View {
        ChartCard(title: "Ingestion", subtitle: subtitle, exportDataset: "ingestion") {
            if bars.isEmpty {
                // The daemon returns `series: null` on a vault with no sources,
                // which is a real and common state — not an error.
                ChartEmptyState(message: "Nothing ingested yet.")
            } else {
                Chart(bars) { bar in
                    BarMark(
                        x: .value("Day", bar.date, unit: .day),
                        y: .value("Sources", bar.count)
                    )
                    .foregroundStyle(by: .value("Status", bar.status))
                }
                .chartLegend(position: .bottom, spacing: 8)
                .frame(height: 180)
                .accessibilityLabel("Sources ingested per day, split by status")
            }
        }
    }
}

/// Vault growth over time, plus the current-value tiles.
struct VaultGrowthChart: View {
    let growth: [GrowthPoint]
    let stats: VaultCounts?
    let reviewPending: Int?
    let range: MetricsStore.Range

    private struct Point: Identifiable {
        let id: String
        let date: Date
        let series: String
        let value: Int
    }

    /// Notes and words only — there is **no** links series on `/api/vault`
    /// (CONTRACT.md §6). Links appear as a tile instead of being invented.
    private var points: [Point] {
        growth.flatMap { point -> [Point] in
            guard let date = point.date else { return [] }
            var out: [Point] = []
            if let notes = point.notes {
                out.append(Point(id: "\(point.day)|notes", date: date, series: "Notes", value: notes))
            }
            if let words = point.words {
                out.append(Point(id: "\(point.day)|words", date: date, series: "Words", value: words))
            }
            return out
        }
    }

    var body: some View {
        ChartCard(
            title: "Vault",
            subtitle: "Cumulative by note creation date",
            exportDataset: "vault"
        ) {
            HStack(spacing: 18) {
                StatTile(title: "Notes", value: stats?.notes)
                StatTile(title: "Links", value: stats?.links)
                StatTile(title: "Words", value: stats?.words)
                StatTile(title: "Inbox", value: stats?.inboxBacklog, highlightWhenNonZero: true)
                StatTile(title: "Review", value: reviewPending, highlightWhenNonZero: true)
                Spacer()
            }

            if points.isEmpty {
                ChartEmptyState(message: "No vault growth recorded in this range.")
            } else {
                Chart(points) { point in
                    LineMark(
                        x: .value("Day", point.date, unit: .day),
                        y: .value("Count", point.value)
                    )
                    .foregroundStyle(by: .value("Series", point.series))
                    .interpolationMethod(.monotone)
                }
                // Notes are in the hundreds and words in the hundreds of
                // thousands; on one linear axis the notes line is a flat zero.
                .chartYScale(type: .symmetricLog)
                .chartYAxis {
                    AxisMarks(format: IntegerFormatStyle<Int>.number.notation(.compactName))
                }
                .chartLegend(position: .bottom, spacing: 8)
                .frame(height: 180)
                .accessibilityLabel("Vault growth: notes and words over time, on a log scale")
            }
        }
    }
}

struct StatTile: View {
    let title: String
    let value: Int?
    var highlightWhenNonZero = false

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(title)
                .font(.caption2)
                .foregroundStyle(.secondary)
            Text(value.map { AxonFormat.tokens(Int64($0)) } ?? "—")
                .font(.title3.monospacedDigit())
                .foregroundStyle(
                    highlightWhenNonZero && (value ?? 0) > 0 ? AnyShapeStyle(.tint) : AnyShapeStyle(.primary)
                )
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("\(title): \(value.map(String.init) ?? "unknown")")
    }
}
