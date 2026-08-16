import Foundation
import Testing

@testable import AxonKit

@Suite struct TokenSavingsTests {
    private func point(
        model: String = "claude-sonnet-5",
        input: Int64 = 0, output: Int64 = 0, cacheRead: Int64 = 0, cacheWrite: Int64 = 0
    ) throws -> TokenPoint {
        let json = #"""
        {"day":"2026-08-16","operation":"automation.x","model":"\#(model)",
         "input":\#(input),"output":\#(output),
         "cache_read":\#(cacheRead),"cache_write":\#(cacheWrite)}
        """#
        return try AxonJSON.decode(TokenPoint.self, from: Data(json.utf8))
    }

    /// The claim must be defensible: cache reads ARE billed, at roughly a tenth
    /// of fresh input, so only the other nine tenths were avoided. Reporting
    /// the whole cache-read total as "saved" would overstate it by 11%.
    @Test func cacheReadsCountAtTheirDiscountNotInFull() throws {
        let savings = TokenSavings(points: [try point(input: 100, cacheRead: 1000)])

        #expect(savings.cacheRead == 1000)
        #expect(savings.avoided == 900)
        #expect(savings.avoided < savings.cacheRead, "cache reads are not free")
    }

    /// Local-model work never reaches Claude, so it counts in full.
    @Test func localModelWorkCountsInFull() throws {
        let savings = TokenSavings(points: [
            try point(model: "codestral", input: 500, output: 200),
            try point(model: "claude-sonnet-5", input: 100, output: 50),
        ])

        #expect(savings.local == 700)
        #expect(savings.billed == 150)
        #expect(savings.avoided == 700)
    }

    /// A new Claude model name must not be mistaken for a local one — that
    /// would silently inflate the savings figure.
    @Test func unknownClaudeNamesAreNotCountedAsLocal() {
        #expect(!TokenSavings.isLocalModel("claude-fable-5"))
        #expect(!TokenSavings.isLocalModel("claude-something-unreleased"))
        #expect(!TokenSavings.isLocalModel("some-new-opus-variant"))
        #expect(TokenSavings.isLocalModel("codestral"))
        #expect(TokenSavings.isLocalModel("nomic-embed-text"))
        // An empty model name is not evidence of anything.
        #expect(!TokenSavings.isLocalModel(""))
    }

    @Test func fractionIsShareOfTheUnoptimisedTotal() throws {
        let savings = TokenSavings(cacheRead: 0, cacheWrite: 0, billed: 100, local: 900)

        #expect(savings.withoutAxon == 1000)
        #expect(abs(savings.fraction - 0.9) < 0.001)
    }

    @Test func nothingSavedIsReportedAsNothing() throws {
        let savings = TokenSavings(points: [try point(input: 10, output: 5)])

        #expect(savings.avoided == 0)
        #expect(savings.hasSavings == false)
        #expect(savings.fraction == 0)
        #expect(savings.explanation.contains("Nothing"))
    }

    @Test func fractionIsSafeWithNoData() {
        let savings = TokenSavings(points: [])
        #expect(savings.fraction == 0)
        #expect(savings.withoutAxon == 0)
    }

    /// Cache writes are a cost, not a saving, and must never be added in.
    @Test func cacheWritesAreNotCountedAsSavings() throws {
        let savings = TokenSavings(points: [try point(cacheWrite: 5000)])

        #expect(savings.cacheWrite == 5000)
        #expect(savings.avoided == 0)
    }

    /// The explanation is what a sceptical user reads to check the number, so
    /// it must state the discount rather than assert a round figure.
    @Test func explanationStatesHowTheNumberIsDerived() throws {
        let savings = TokenSavings(points: [try point(cacheRead: 1000)])

        #expect(savings.explanation.contains("still billed"))
        #expect(savings.explanation.contains("90%"))
    }

    /// Against the real fixture the numbers must be plausible and consistent.
    @Test func realFixtureProducesConsistentTotals() throws {
        let series = try AxonJSON.decode(TokenSeries.self, from: fixture("tokens"))
        let savings = TokenSavings(points: series.points)

        #expect(savings.billed + savings.local == series.total)
        #expect(savings.withoutAxon >= savings.billed)
        #expect(savings.fraction >= 0 && savings.fraction <= 1)
    }
}
