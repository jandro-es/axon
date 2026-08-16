import Foundation
import Testing

@testable import AxonKit

private let stub = DashboardStubProtocol.shared

private func makeClient() -> DashboardClient {
    DashboardClient(session: .stubbed(DashboardStubProtocol.self))
}

// MARK: - tests

@Suite(.serialized)
struct DashboardClientTests {
    @Test func healthDecodesFromTransport() async throws {
        stub.reset()
        stub.routes["/health"] = .init(body: try fixture("health"))

        let health = try await makeClient().health()
        #expect(health.profile == "personal")
    }

    @Test func connectionFailureSurfacesAsUnreachable() async throws {
        stub.reset()
        stub.failure = URLError(.cannotConnectToHost)

        await #expect(throws: DashboardError.unreachable) {
            _ = try await makeClient().health()
        }
    }

    @Test func nonSuccessStatusSurfacesTheCode() async throws {
        stub.reset()
        stub.routes["/health"] = .init(status: 503, body: Data("nope".utf8))

        await #expect(throws: DashboardError.badStatus(503)) {
            _ = try await makeClient().health()
        }
    }

    @Test func malformedBodySurfacesAsDecodingError() async throws {
        stub.reset()
        stub.routes["/health"] = .init(body: Data("{not json".utf8))

        await #expect(throws: DashboardError.self) {
            _ = try await makeClient().health()
        }
    }

    /// `/api/actions` is 403 without its guard header (CONTRACT.md §1). This is
    /// the single easiest thing to get wrong and it fails silently as a badge
    /// that never appears — so assert the header on the wire.
    @Test func actionsRequestCarriesItsGuardHeader() async throws {
        stub.reset()
        stub.routes["/api/actions"] = .init(body: try fixture("actions"))

        _ = try await makeClient().actionsCount()

        let request = try #require(stub.recorded.first)
        #expect(request.value(forHTTPHeaderField: "X-Axon-Actions") == "1")
    }

    /// Everything except /api/actions must NOT send a guard header — sending
    /// one where the daemon does not expect it is harmless today but encodes a
    /// wrong belief about the contract.
    @Test func plainReadsSendNoGuardHeader() async throws {
        stub.reset()
        stub.routes["/api/review"] = .init(body: try fixture("review"))

        _ = try await makeClient().reviewCount()

        let request = try #require(stub.recorded.first)
        #expect(request.value(forHTTPHeaderField: "X-Axon-Actions") == nil)
        #expect(request.value(forHTTPHeaderField: "X-Axon-Review") == nil)
    }

    /// A 404 means the profile disabled actions — the badge hides, it is not an
    /// error the user should see (CONTRACT.md §7).
    @Test func disabledActionsReturnNilRatherThanThrowing() async throws {
        stub.reset()
        stub.routes["/api/actions"] = .init(status: 404, body: Data())

        let count = try await makeClient().actionsCount()
        #expect(count == nil)
    }

    @Test func tokensRequestPassesTheDayWindow() async throws {
        stub.reset()
        stub.routes["/api/tokens"] = .init(body: try fixture("tokens"))

        _ = try await makeClient().tokens(days: 7)

        let url = try #require(stub.recorded.first?.url)
        #expect(url.query()?.contains("days=7") == true)
    }

    @Test func runsRequestPassesTheRowLimit() async throws {
        stub.reset()
        stub.routes["/api/runs"] = .init(body: try fixture("runs"))

        _ = try await makeClient().runs(limit: 250)

        let url = try #require(stub.recorded.first?.url)
        #expect(url.query()?.contains("limit=250") == true)
    }

    @Test func everyReadDecodesItsFixtureThroughTheTransport() async throws {
        stub.reset()
        stub.routes = [
            "/health": .init(body: try fixture("health")),
            "/api/usage": .init(body: try fixture("usage")),
            "/api/tokens": .init(body: try fixture("tokens")),
            "/api/runs": .init(body: try fixture("runs")),
            "/api/ingestion": .init(body: try fixture("ingestion")),
            "/api/vault": .init(body: try fixture("vault")),
            "/api/review": .init(body: try fixture("review")),
            "/api/actions": .init(body: try fixture("actions")),
        ]
        let client = makeClient()

        #expect(try await client.usage().dayUsed == 3724)
        #expect(try await client.tokens().points.count == 24)
        #expect(try await client.runs().count == 19)
        #expect(try await client.ingestion().embeddingQueue == 0)
        #expect(try await client.vault().stats?.notes == 165)
        #expect(try await client.reviewCount() == 160)
        #expect(try await client.actionsCount() == 458)
    }

    // MARK: export URLs (CFR-31)

    @Test func exportURLIsBuiltFromDatasetAndFormat() {
        let url = DashboardClient().exportURL(dataset: "tokens", format: "csv")

        #expect(url.host() == "127.0.0.1")
        #expect(url.port == 7777)
        #expect(url.path() == "/api/export")
        let query = try? #require(url.query())
        #expect(query?.contains("dataset=tokens") == true)
        #expect(query?.contains("format=csv") == true)
    }

    @Test func exportSuggestedFilenameMatchesTheDaemonPattern() {
        let client = DashboardClient()
        let name = client.exportFilename(dataset: "runs", format: "json", day: "2026-08-16")

        // Mirrors the daemon's Content-Disposition (CONTRACT.md §8).
        #expect(name == "axon-runs-2026-08-16.json")
    }

    // MARK: base URL

    @Test func baseURLHonoursANonDefaultPort() {
        let client = DashboardClient(baseURL: URL(string: "http://127.0.0.1:9999")!)
        #expect(client.exportURL(dataset: "vault", format: "csv").port == 9999)
    }

    /// The daemon's Host guard 403s anything that is not loopback, so a
    /// non-loopback base URL is a configuration bug worth refusing early.
    @Test func nonLoopbackBaseURLIsRejected() {
        #expect(DashboardClient.isLoopback(URL(string: "http://127.0.0.1:7777")!))
        #expect(DashboardClient.isLoopback(URL(string: "http://localhost:7777")!))
        #expect(DashboardClient.isLoopback(URL(string: "http://[::1]:7777")!))
        #expect(!DashboardClient.isLoopback(URL(string: "http://example.com")!))
        #expect(!DashboardClient.isLoopback(URL(string: "http://192.168.1.4:7777")!))
    }
}
