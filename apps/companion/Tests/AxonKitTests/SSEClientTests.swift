import Foundation
import Testing

@testable import AxonKit

@Suite struct SSEParserTests {
    /// Feed the captured stream through the parser one byte-chunk at a time to
    /// prove framing does not depend on how the network splits the bytes.
    private func parseFixture(chunkSize: Int = 64) throws -> [AxonEvent] {
        let data = try fixture("events", ext: "sse")
        var parser = SSEParser()
        var events: [AxonEvent] = []

        var index = data.startIndex
        while index < data.endIndex {
            let end = data.index(index, offsetBy: chunkSize, limitedBy: data.endIndex) ?? data.endIndex
            events.append(contentsOf: parser.consume(data[index..<end]))
            index = end
        }
        events.append(contentsOf: parser.finish())
        return events
    }

    @Test func parsesEveryWellFormedFrameInTheFixture() throws {
        let events = try parseFixture()

        // 17 `event:` frames in the fixture, one of which is deliberately
        // truncated and must be dropped rather than crash the stream.
        #expect(events.count == 16)
        #expect(!events.contains { $0.kind == "malformed.frame" })
    }

    @Test func framingIsIndependentOfChunkBoundaries() throws {
        let byOne = try parseFixture(chunkSize: 1)
        let byChunk = try parseFixture(chunkSize: 4096)

        #expect(byOne.count == byChunk.count)
        #expect(byOne.map(\.kind) == byChunk.map(\.kind))
    }

    @Test func decodesRealDaemonFields() throws {
        let events = try parseFixture()
        let run = try #require(events.first { $0.kind == "automation.run" })

        #expect(run.level == "info")
        #expect(!run.message.isEmpty)
        #expect(run.date != nil)
        // SSE timestamps carry a local offset and fractional seconds, unlike
        // the REST endpoints' `Z` form.
        #expect(run.automationName != nil)
    }

    @Test func skipsCommentsAndHeartbeats() throws {
        let events = try parseFixture()
        // `: connected` and `: ping` must never surface as events.
        #expect(!events.contains { $0.kind.isEmpty })
        #expect(!events.contains { $0.message.contains("connected") && $0.kind.isEmpty })
    }

    @Test func concatenatesMultiLineDataPayloads() throws {
        let events = try parseFixture()
        let multi = try #require(events.first { $0.message == "multiline: failed" })

        // The two `data:` lines must join into one decodable JSON document.
        #expect(multi.kind == "automation.fail")
        #expect(multi.automationName == "multiline")
    }

    @Test func toleratesUnknownKinds() throws {
        let events = try parseFixture()
        let future = try #require(
            events.first { $0.kind == "future.kind.axon.does.not.emit.yet" }
        )

        // The kind union is open (CONTRACT.md §9) — an unknown kind must
        // survive as data, not break the stream.
        #expect(future.level == "info")
    }

    @Test func malformedFrameIsSkippedNotFatal() throws {
        let events = try parseFixture()

        // The frames after the truncated one must still arrive.
        #expect(events.contains { $0.kind == "automation.run" })
        #expect(events.last?.kind == "automation.run")
    }

    @Test func readsTokenDenyReasonForNotificationBodies() throws {
        let events = try parseFixture()
        let deny = try #require(events.first { $0.kind == "token.deny" })

        #expect(deny.level == "warn")
        #expect(deny.reason == "daily budget exhausted")
    }

    @Test func classifiesKindsIntoRefreshTopics() throws {
        #expect(AxonEvent.Topic(kind: "automation.fail") == .automations)
        #expect(AxonEvent.Topic(kind: "automation.run") == .automations)
        #expect(AxonEvent.Topic(kind: "token.ledger") == .tokens)
        #expect(AxonEvent.Topic(kind: "token.deny") == .tokens)
        #expect(AxonEvent.Topic(kind: "ingest.done") == .ingestion)
        #expect(AxonEvent.Topic(kind: "ingest.embed.fail") == .ingestion)
        // Unknown and irrelevant kinds must not trigger refreshes.
        #expect(AxonEvent.Topic(kind: "ask.refused") == nil)
        #expect(AxonEvent.Topic(kind: "future.kind") == nil)
    }

    @Test func emptyStreamYieldsNoEvents() {
        var parser = SSEParser()
        #expect(parser.consume(Data()).isEmpty)
        #expect(parser.finish().isEmpty)
    }

    @Test func handlesCRLFLineEndings() {
        var parser = SSEParser()
        let frame = "event: token.ledger\r\ndata: {\"kind\":\"token.ledger\",\"message\":\"x\"}\r\n\r\n"
        let events = parser.consume(Data(frame.utf8))

        #expect(events.count == 1)
        #expect(events.first?.kind == "token.ledger")
    }

    /// The `event:` line and the `kind` inside `data:` are two independent
    /// sources of the same fact. Prefer the payload, fall back to the frame.
    @Test func fallsBackToFrameNameWhenPayloadOmitsKind() {
        var parser = SSEParser()
        let frame = "event: automation.run\ndata: {\"message\":\"no kind field\"}\n\n"
        let events = parser.consume(Data(frame.utf8))

        #expect(events.first?.kind == "automation.run")
    }
}

// MARK: - reconnect behaviour

@Suite struct SSEBackoffTests {
    @Test func backoffGrowsExponentiallyAndCaps() {
        var backoff = SSEBackoff()

        #expect(backoff.next() == .seconds(1))
        #expect(backoff.next() == .seconds(2))
        #expect(backoff.next() == .seconds(4))
        #expect(backoff.next() == .seconds(8))
        #expect(backoff.next() == .seconds(16))
        #expect(backoff.next() == .seconds(30))
        // Capped at 30s — a dead daemon must not push retries to minutes.
        #expect(backoff.next() == .seconds(30))
    }

    @Test func successResetsBackoff() {
        var backoff = SSEBackoff()
        _ = backoff.next()
        _ = backoff.next()
        _ = backoff.next()

        backoff.reset()
        #expect(backoff.next() == .seconds(1))
    }
}

@Suite(.serialized)
struct SSEClientTests {
    private let stub = SSEStubProtocol.shared

    private func makeClient(sleep: @escaping @Sendable (Duration) async throws -> Void)
        -> SSEClient
    {
        SSEClient(
            url: URL(string: "http://127.0.0.1:7777/events")!,
            session: .stubbed(SSEStubProtocol.self),
            sleep: sleep
        )
    }

    @Test func streamEmitsConnectedThenEventsThenDisconnected() async throws {
        stub.reset()
        stub.routes["/events"] = .init(
            body: try fixture("events", ext: "sse"),
            headers: ["Content-Type": "text/event-stream"]
        )

        // No real waiting: the injected sleep returns immediately, and we stop
        // consuming after the first disconnect.
        let client = makeClient(sleep: { _ in })
        var states: [String] = []

        for await state in await client.stream() {
            switch state {
            case .connected: states.append("connected")
            case .event(let event): states.append("event:\(event.kind)")
            case .disconnected: states.append("disconnected")
            }
            if states.count > 1, states.last == "disconnected" { break }
        }

        #expect(states.first == "connected")
        #expect(states.last == "disconnected")
        #expect(states.contains("event:automation.run"))
    }

    @Test func reconnectsAfterTheStreamDrops() async throws {
        stub.reset()
        stub.routes["/events"] = .init(
            body: Data("event: token.ledger\ndata: {\"kind\":\"token.ledger\"}\n\n".utf8),
            headers: ["Content-Type": "text/event-stream"]
        )

        let slept = Slept()
        let client = makeClient(sleep: { await slept.record($0) })

        var connects = 0
        for await state in await client.stream() {
            if case .connected = state { connects += 1 }
            // Two connects proves it reconnected after the first drop.
            if connects == 2 { break }
        }

        #expect(connects == 2)
        // It waited before retrying rather than hot-looping the daemon.
        #expect(await slept.durations.first == .seconds(1))
    }

    @Test func unreachableDaemonStillReportsDisconnected() async throws {
        stub.reset()
        stub.failure = URLError(.cannotConnectToHost)

        let client = makeClient(sleep: { _ in })
        var sawDisconnected = false

        for await state in await client.stream() {
            if case .disconnected = state {
                sawDisconnected = true
                break
            }
        }

        // Stream liveness is a health signal — a refused connection must report
        // disconnected, not stall silently (CFR-01 fast path).
        #expect(sawDisconnected)
    }
}

/// Records injected sleeps so backoff can be asserted without real waiting.
actor Slept {
    var durations: [Duration] = []
    func record(_ duration: Duration) { durations.append(duration) }
}
