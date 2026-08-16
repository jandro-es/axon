import Foundation
import Testing

@testable import AxonKit

/// Loads a captured fixture. Every decoder test runs against the real bytes the
/// daemon produced (CONTRACT.md), not a hand-written approximation.
func fixture(_ name: String, ext: String = "json") throws -> Data {
    let url = try #require(
        Bundle.module.url(forResource: "Fixtures/\(name)", withExtension: ext),
        "missing fixture Fixtures/\(name).\(ext)"
    )
    return try Data(contentsOf: url)
}

// MARK: - /health

@Test func decodesHealthFixture() throws {
    let health = try AxonJSON.decode(AxonHealth.self, from: fixture("health"))

    #expect(!health.version.isEmpty)
    #expect(health.profile == "personal")
    #expect(health.status == "ok")
    #expect(health.isDegraded == false)
    #expect(health.db == true)
    #expect(health.updateAvailable == true)
    #expect(health.latestVersion == "1.3.2")
    #expect(health.embeddingsProvider == "ollama")
    #expect(health.actionsEnabled == true)
}

@Test func healthToleratesUnknownFields() throws {
    let data = Data(#"{"version":"9.9.9","some_future_field":{"x":1}}"#.utf8)
    let health = try AxonJSON.decode(AxonHealth.self, from: data)

    #expect(health.version == "9.9.9")
    // Everything beyond the identity fields is optional (CFR-82).
    #expect(health.latestVersion == nil)
    #expect(health.updateAvailable == nil)
    #expect(health.startedAt == nil)
}

@Test func healthDegradedIsDerivedFromStatus() throws {
    let data = Data(#"{"version":"1.3.2","status":"degraded","db":false}"#.utf8)
    let health = try AxonJSON.decode(AxonHealth.self, from: data)

    #expect(health.isDegraded)
    #expect(health.degradedComponents == ["db"])
}

@Test func healthParsesStartedAtAndDerivesUptime() throws {
    let data = Data(#"{"version":"1.3.2","started_at":"2026-08-16T16:41:02Z"}"#.utf8)
    let health = try AxonJSON.decode(AxonHealth.self, from: data)

    let started = try #require(health.startedAt)
    #expect(started.timeIntervalSince1970 == 1_786_898_462)
    // Uptime is derived at read time, never stored.
    let now = started.addingTimeInterval(3600)
    #expect(health.uptime(asOf: now) == 3600)
}

@Test func healthWithoutStartedAtHasNoUptime() throws {
    let health = try AxonJSON.decode(AxonHealth.self, from: fixture("health"))
    // The captured fixture predates the started_at seam — uptime must degrade
    // to nil rather than inventing a value (CFR-82 feature-by-feature).
    #expect(health.uptime(asOf: .now) == nil)
}

// MARK: - /api/usage

@Test func decodesUsageFixture() throws {
    let usage = try AxonJSON.decode(UsageSnapshot.self, from: fixture("usage"))

    #expect(usage.dayUsed == 3724)
    #expect(usage.dayLimit == 1_500_000)
    #expect(usage.weekUsed == 30729)
    #expect(usage.weekLimit == 8_000_000)
    #expect(usage.guardPaused == false)
    #expect(usage.guardReason == "")
}

@Test func usageFractionsAreZeroToOneNotPercent() throws {
    let usage = try AxonJSON.decode(UsageSnapshot.self, from: fixture("usage"))

    // The daemon reports *_pct as 0-100; SwiftUI Gauge needs 0-1.
    #expect(abs(usage.dayFraction - 0.00248) < 0.0001)
    #expect(abs(usage.weekFraction - 0.00384) < 0.0001)
}

@Test func usageFractionIsClampedAndSafeAtZeroLimit() throws {
    let over = Data(#"{"day_used":150,"day_limit":100,"week_used":0,"week_limit":0}"#.utf8)
    let usage = try AxonJSON.decode(UsageSnapshot.self, from: over)

    // Over budget must saturate, not overflow the gauge.
    #expect(usage.dayFraction == 1.0)
    // A zero limit means "unlimited", not "divide by zero".
    #expect(usage.weekFraction == 0)
}

@Test func usageCostFieldsAreOptional() throws {
    let usage = try AxonJSON.decode(UsageSnapshot.self, from: fixture("usage"))
    // week_cost_* are absent from the real payload outside api_key mode.
    #expect(usage.weekCostCap == nil)
    #expect(usage.dayCostCap == 0)
    #expect(usage.tracksCost == false)
}

// MARK: - /api/tokens

@Test func decodesTokensFixture() throws {
    let series = try AxonJSON.decode(TokenSeries.self, from: fixture("tokens"))

    #expect(series.points.count == 24)
    let first = try #require(series.points.first)
    #expect(first.day == "2026-08-09")
    #expect(first.model == "claude-sonnet-5")
    #expect(first.operation == "automation.briefing")
    // Charted total is input + output; cache columns are informational only.
    #expect(first.total == 122)
}

@Test func tokenPointShortensAutomationOperationNames() throws {
    let series = try AxonJSON.decode(TokenSeries.self, from: fixture("tokens"))
    let automation = try #require(series.points.first { $0.operation.hasPrefix("automation.") })

    #expect(!automation.label.hasPrefix("automation."))
    #expect(automation.label == String(automation.operation.dropFirst("automation.".count)))
}

@Test func tokenSeriesGroupsByAutomationAndByModel() throws {
    let series = try AxonJSON.decode(TokenSeries.self, from: fixture("tokens"))

    // Both CFR-30 stackings come from this one response — never a second fetch.
    #expect(series.models.count == 3)
    #expect(series.models.contains("claude-sonnet-5"))
    #expect(!series.automations.isEmpty)
    #expect(series.automations.allSatisfy { !$0.hasPrefix("automation.") })
}

@Test func tokenSeriesFiltersByDayWindow() throws {
    let series = try AxonJSON.decode(TokenSeries.self, from: fixture("tokens"))
    let days = Set(series.points.map(\.day))
    let newest = try #require(days.max())

    let narrowed = series.filtered(sinceDay: newest)
    #expect(narrowed.points.allSatisfy { $0.day == newest })
    #expect(narrowed.points.count < series.points.count)
}

// MARK: - /api/runs

@Test func decodesRunsFixture() throws {
    let runs = try AxonJSON.decode([RunRecord].self, from: fixture("runs"))

    #expect(runs.count == 19)
    #expect(Set(runs.map(\.status)) == ["ok", "skipped", "failed"])
}

@Test func runRecordDerivesDurationAndOutcome() throws {
    let runs = try AxonJSON.decode([RunRecord].self, from: fixture("runs"))
    let finished = try #require(runs.first { !$0.finishedAt.isEmpty })

    let duration = try #require(finished.duration)
    #expect(duration >= 0)

    let failed = try #require(runs.first { $0.status == "failed" })
    #expect(failed.outcome == .failed)
    #expect(runs.first { $0.status == "ok" }?.outcome == .ok)
    #expect(runs.first { $0.status == "skipped" }?.outcome == .skipped)
}

@Test func runRecordWithoutFinishTimeIsStillRunning() throws {
    let data = Data(#"""
    [{"id":1,"automation":"briefing","started_at":"2026-08-16T16:50:03Z",
      "finished_at":"","status":"running","skip_reason":"","tokens":0,"error":""}]
    """#.utf8)
    let runs = try AxonJSON.decode([RunRecord].self, from: data)

    #expect(runs[0].duration == nil)
    #expect(runs[0].outcome == .running)
}

@Test func runRecordToleratesUnknownStatus() throws {
    let data = Data(#"[{"id":1,"automation":"x","status":"teleported"}]"#.utf8)
    let runs = try AxonJSON.decode([RunRecord].self, from: data)

    // An unknown status must not crash or masquerade as success.
    #expect(runs[0].outcome == .unknown)
}

// MARK: - /api/ingestion

@Test func decodesIngestionFixtureWithNullSeries() throws {
    // The real daemon returns `series: null` (not []) on a vault with no
    // sources. This is the single most likely decoder crash — hence a fixture.
    let stats = try AxonJSON.decode(IngestionStats.self, from: fixture("ingestion"))

    #expect(stats.embeddingQueue == 0)
    #expect(stats.series == nil)
    #expect(stats.buckets.isEmpty)
}

@Test func decodesPopulatedIngestionSeries() throws {
    let data = Data(#"""
    {"embedding_queue":12,
     "series":[{"day":"2026-08-15","status":"ok","count":3},
               {"day":"2026-08-15","status":"failed","count":1},
               {"day":"2026-08-16","status":"redacted","count":2}]}
    """#.utf8)
    let stats = try AxonJSON.decode(IngestionStats.self, from: data)

    #expect(stats.embeddingQueue == 12)
    #expect(stats.buckets.count == 3)
    #expect(stats.successCount == 3)
    // Failed and redacted are both "did not land cleanly" for the chart.
    #expect(stats.problemCount == 3)
}

// MARK: - /api/vault

@Test func decodesVaultFixture() throws {
    let vault = try AxonJSON.decode(VaultStats.self, from: fixture("vault"))

    #expect(vault.stats?.notes == 165)
    #expect(vault.stats?.links == 548)
    #expect(vault.stats?.inboxBacklog == 0)
    #expect(vault.growth?.count == 4)
    #expect(vault.growth?.first?.day == "2026-07-11")
}

@Test func vaultGrowthHasNoLinksSeries() throws {
    // CONTRACT.md §6: growth points carry notes and words only. Assert the
    // shape so a future daemon that adds `links` is noticed by a failing test
    // rather than silently ignored.
    let raw = try JSONSerialization.jsonObject(with: fixture("vault")) as? [String: Any]
    let growth = try #require(raw?["growth"] as? [[String: Any]])
    #expect(growth.allSatisfy { $0["links"] == nil })
}

@Test func vaultGrowthFiltersByDayWindow() throws {
    let vault = try AxonJSON.decode(VaultStats.self, from: fixture("vault"))
    let narrowed = vault.growth(sinceDay: "2026-08-01")

    #expect(narrowed.count == 3)
    #expect(narrowed.allSatisfy { $0.day >= "2026-08-01" })
}

// MARK: - badge counts

@Test func decodesReviewPendingCount() throws {
    let meta = try AxonJSON.decode(ReviewMeta.self, from: fixture("review"))
    #expect(meta.pending == 160)
}

@Test func decodesActionsOpenCount() throws {
    let meta = try AxonJSON.decode(ActionsMeta.self, from: fixture("actions"))
    #expect(meta.counts?.open == 458)
    #expect(meta.openCount == 458)
}

@Test func actionsCountsAreOptional() throws {
    let meta = try AxonJSON.decode(ActionsMeta.self, from: Data("{}".utf8))
    #expect(meta.openCount == nil)
}
