import Foundation
import Testing

@testable import AxonKit

/// The acceptance gate requires Insights to show the same numbers as the web
/// dashboard for the same range (PRD §8). Both read the same endpoints, so the
/// only place they can diverge is aggregation — which is what these pin down,
/// against the dashboard's actual arithmetic in `web/src/App.jsx`.
@Suite struct DashboardAgreementTests {
    /// `web/src/App.jsx:88` — `d.total += (b.input || 0) + (b.output || 0)`,
    /// with cache read/write shown as separate tiles (lines 1027-1028).
    /// Folding cache into the total would silently inflate every number.
    @Test func tokenTotalExcludesCacheColumns() throws {
        let data = Data(#"""
        [{"day":"2026-08-16","operation":"automation.briefing","model":"m",
          "input":100,"output":20,"cache_read":9000,"cache_write":5000}]
        """#.utf8)
        let series = try AxonJSON.decode(TokenSeries.self, from: data)

        #expect(series.points[0].total == 120)
        #expect(series.total == 120)
    }

    /// `web/src/App.jsx:67` — `shortOp` drops `automation.` and rewrites
    /// `ingest.` to `ingest:`. Legend entries must mean the same in both.
    @Test func labelsMirrorTheDashboardsShortOp() throws {
        let data = Data(#"""
        [{"day":"d","operation":"automation.briefing","model":"m"},
         {"day":"d","operation":"ingest.enrich","model":"m"},
         {"day":"d","operation":"something.else","model":"m"}]
        """#.utf8)
        let series = try AxonJSON.decode(TokenSeries.self, from: data)

        #expect(series.points[0].label == "briefing")
        #expect(series.points[1].label == "ingest:enrich")
        #expect(series.points[2].label == "something.else")
    }

    /// Missing token fields are absent, not zero, on some rows. Both surfaces
    /// coalesce to 0 (`b.input || 0`) rather than dropping the row.
    @Test func missingTokenFieldsCountAsZeroNotMissing() throws {
        let data = Data(#"[{"day":"d","operation":"o","model":"m","output":7}]"#.utf8)
        let series = try AxonJSON.decode(TokenSeries.self, from: data)

        #expect(series.points[0].total == 7)
    }

    /// The real fixture, summed both ways, must agree — the same data grouped
    /// by automation and by model has to total identically, or one of the two
    /// stackings is dropping rows.
    @Test func bothStackingsTotalIdentically() throws {
        let series = try AxonJSON.decode(TokenSeries.self, from: fixture("tokens"))

        let byAutomation = Dictionary(grouping: series.points, by: \.label)
            .values.reduce(Int64(0)) { sum, points in
                sum + points.reduce(Int64(0)) { $0 + $1.total }
            }
        let byModel = Dictionary(grouping: series.points, by: \.model)
            .values.reduce(Int64(0)) { sum, points in
                sum + points.reduce(Int64(0)) { $0 + $1.total }
            }

        #expect(byAutomation == series.total)
        #expect(byModel == series.total)
    }
}
