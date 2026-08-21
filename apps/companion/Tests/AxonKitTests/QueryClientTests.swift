import Foundation
import Testing

@testable import AxonKit

/// One store per suite (see StubTransport.swift): sharing DashboardStubProtocol
/// with the dashboard suite races when Swift Testing runs suites in parallel.
final class QueryStubProtocol: StubURLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static let shared = StubStore()
    override class var store: StubStore { shared }
}

private let stub = QueryStubProtocol.shared

private func makeClient() -> DashboardClient {
    DashboardClient(session: .stubbed(QueryStubProtocol.self))
}

/// The Siri/Shortcuts verbs (CFR-92…95): search / ask / capture against the
/// daemon contract (§8b search; ADR-023 ask; ADR-024 capture).
@Suite(.serialized)
struct QueryClientTests {
    @Test func searchDecodesHitsAndCarriesGuardHeader() async throws {
        stub.reset()
        stub.routes["/api/search"] = .init(body: try fixture("search"))

        let hits = try await makeClient().search("sqlite", topK: 5)

        #expect(hits.count == 2)
        #expect(hits[0].path.hasSuffix("sqlite-internals.md"))
        #expect(hits[0].score > hits[1].score)

        let request = try #require(stub.recorded.first)
        #expect(request.value(forHTTPHeaderField: "X-Axon-Search") == "1")
        let query = try #require(request.url?.query())
        #expect(query.contains("q=sqlite"))
        #expect(query.contains("top_k=5"))
    }

    @Test func searchDisabledSurfacesAs404() async throws {
        stub.reset()
        stub.routes["/api/search"] = .init(status: 404, body: Data("off".utf8))

        await #expect(throws: DashboardError.badStatus(404)) {
            _ = try await makeClient().search("x")
        }
    }

    @Test func askPostsQuestionAndDecodesAnswer() async throws {
        stub.reset()
        stub.routes["/api/ask"] = .init(body: try fixture("ask"))

        let a = try await makeClient().ask("what did we decide about sqlite?")

        #expect(a.refused == false)
        #expect(a.answer?.contains("SQLite") == true)
        #expect(a.citations?.isEmpty == false)

        let request = try #require(stub.recorded.first)
        #expect(request.httpMethod == "POST")
        #expect(request.value(forHTTPHeaderField: "X-Axon-Ask") == "1")
        let body = try #require(request.axonBodyData)
        let sent = try #require(try JSONSerialization.jsonObject(with: body) as? [String: String])
        #expect(sent["question"] == "what did we decide about sqlite?")
    }

    @Test func askRefusalIsAStateNotAnError() async throws {
        stub.reset()
        stub.routes["/api/ask"] = .init(
            body: Data(#"{"refused": true, "reason": "nothing relevant retrieved", "tokens": 0}"#.utf8))

        let a = try await makeClient().ask("who won the 1907 regatta?")
        #expect(a.refused)
        #expect(a.answer == nil)
    }

    @Test func capturePostsTextAndIgnoresResponseBody() async throws {
        stub.reset()
        stub.routes["/api/capture"] = .init(body: Data("{}".utf8))

        try await makeClient().capture(text: "call the notary about the deed")

        let request = try #require(stub.recorded.first)
        #expect(request.httpMethod == "POST")
        #expect(request.value(forHTTPHeaderField: "X-Axon-Capture") == "1")
        let body = try #require(request.axonBodyData)
        let sent = try #require(try JSONSerialization.jsonObject(with: body) as? [String: String])
        #expect(sent["text"] == "call the notary about the deed")
    }

    @Test func capturePostsURLTitleAndTextAndOmitsEmptyFields() async throws {
        stub.reset()
        stub.routes["/api/capture"] = .init(body: Data("{}".utf8))

        try await makeClient().capture(
            url: "https://example.com/a", title: "A page", text: "the bit I selected")

        let request = try #require(stub.recorded.first)
        #expect(request.httpMethod == "POST")
        #expect(request.value(forHTTPHeaderField: "X-Axon-Capture") == "1")
        #expect(request.value(forHTTPHeaderField: "Content-Type") == "application/json")
        let body = try #require(request.axonBodyData)
        let sent = try #require(try JSONSerialization.jsonObject(with: body) as? [String: String])
        #expect(sent["url"] == "https://example.com/a")
        #expect(sent["title"] == "A page")
        #expect(sent["text"] == "the bit I selected")
    }

    @Test func captureOmitsEmptyFieldsRatherThanSendingBlanks() async throws {
        stub.reset()
        stub.routes["/api/capture"] = .init(body: Data("{}".utf8))

        try await makeClient().capture(url: "https://example.com/a")

        let body = try #require(stub.recorded.first?.axonBodyData)
        let sent = try #require(try JSONSerialization.jsonObject(with: body) as? [String: String])
        #expect(sent["url"] == "https://example.com/a")
        // An empty title would make the daemon write a blank H1 instead of
        // falling back to "Captured note".
        #expect(sent["title"] == nil)
        #expect(sent["text"] == nil)
    }
}

// URLProtocol turns httpBody into a stream before the request is recorded;
// drain it back to bytes for assertions.
extension URLRequest {
    var axonBodyData: Data? {
        if let body = httpBody { return body }
        guard let stream = httpBodyStream else { return nil }
        stream.open()
        defer { stream.close() }
        var data = Data()
        let size = 4096
        let buffer = UnsafeMutablePointer<UInt8>.allocate(capacity: size)
        defer { buffer.deallocate() }
        while stream.hasBytesAvailable {
            let n = stream.read(buffer, maxLength: size)
            if n <= 0 { break }
            data.append(buffer, count: n)
        }
        return data
    }
}
