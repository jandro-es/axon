import AxonKit
import SwiftUI

/// A single scroll of chart cards with one range picker (CFR-30).
///
/// Sidebar-less on purpose: five cards do not earn navigation, and a scroll
/// keeps every number visible at once for cross-checking against the dashboard.
struct InsightsWindow: View {
    @Environment(AppModel.self) private var app

    var body: some View {
        @Bindable var metrics = app.metrics

        ScrollView {
            LazyVStack(alignment: .leading, spacing: 14) {
                if case .failed(let message) = metrics.loadState {
                    DegradedBanner(
                        message: message,
                        canStart: app.controller.state == .stopped
                            && app.controller.canControlLifecycle,
                        onStart: { Task { await app.controller.startDaemon() } },
                        onRetry: { Task { await metrics.refreshAll() } }
                    )
                }

                TokenChart(points: metrics.tokenPoints(), range: metrics.range)
                BudgetGauges(usage: metrics.usage)
                RunsChart(
                    runs: metrics.runs(within: metrics.range),
                    successRate: metrics.successRate(within: metrics.range),
                    range: metrics.range
                )
                IngestionChart(
                    buckets: metrics.ingestionBuckets(),
                    queueDepth: metrics.ingestion?.embeddingQueue,
                    range: metrics.range
                )
                VaultGrowthChart(
                    growth: metrics.vaultGrowth(),
                    stats: metrics.vault?.stats,
                    reviewPending: app.badges.review,
                    range: metrics.range
                )
            }
            .padding(16)
        }
        .toolbar {
            ToolbarItem(placement: .principal) {
                Picker("Range", selection: $metrics.range) {
                    ForEach(MetricsStore.Range.allCases, id: \.self) { range in
                        Text(range.title).tag(range)
                    }
                }
                .pickerStyle(.segmented)
                .fixedSize()
                .accessibilityLabel("Chart range")
            }
            ToolbarItem {
                Button {
                    Task { await metrics.refreshAll() }
                } label: {
                    Label("Refresh", systemImage: "arrow.clockwise")
                }
                .disabled(metrics.loadState == .loading)
            }
        }
        .task { await metrics.refreshAll() }
        // A range change refetches: /api/tokens is the one read whose window is
        // server-side, so a client-side filter alone would show a truncated
        // month view built from a week of data.
        .task(id: metrics.range) { await metrics.refreshAll() }
    }
}

/// Shown above the charts when the last refresh failed. Stale data stays
/// visible below it — the banner explains, it does not replace.
struct DegradedBanner: View {
    let message: String
    let canStart: Bool
    let onStart: () -> Void
    let onRetry: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(.orange)
            Text(message)
                .font(.callout)
                .fixedSize(horizontal: false, vertical: true)
            Spacer()
            if canStart {
                Button("Start AXON", action: onStart)
            }
            Button("Retry", action: onRetry)
        }
        .controlSize(.small)
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.orange.opacity(0.12), in: .rect(cornerRadius: 10))
    }
}
