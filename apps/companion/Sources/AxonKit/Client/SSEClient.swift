import Foundation

/// Incremental server-sent-events framer.
///
/// Written as a byte-level state machine rather than "split the whole body on
/// blank lines" because the body never ends — the daemon holds the connection
/// open indefinitely, delivering bytes in arbitrary chunks that routinely split
/// a frame down the middle.
struct SSEParser {
    private var buffer = Data()

    /// Feed the next chunk; returns every complete frame it completed.
    mutating func consume(_ chunk: some DataProtocol) -> [AxonEvent] {
        buffer.append(contentsOf: chunk)
        var events: [AxonEvent] = []

        while let range = buffer.firstRange(of: Data("\n\n".utf8))
            ?? buffer.firstRange(of: Data("\r\n\r\n".utf8))
        {
            let frame = buffer[buffer.startIndex..<range.lowerBound]
            buffer.removeSubrange(buffer.startIndex..<range.upperBound)
            if let event = Self.parse(frame: frame) {
                events.append(event)
            }
        }
        return events
    }

    /// Flush a trailing frame that was never terminated by a blank line — the
    /// case when the connection closes mid-stream.
    mutating func finish() -> [AxonEvent] {
        defer { buffer.removeAll() }
        guard !buffer.isEmpty, let event = Self.parse(frame: buffer) else { return [] }
        return [event]
    }

    /// Parse one frame's worth of lines. Returns nil for comment-only frames
    /// (`: connected`, `: ping`) and for payloads that fail to decode — a
    /// malformed frame is skipped, never fatal (CONTRACT.md §9).
    private static func parse(frame: some DataProtocol) -> AxonEvent? {
        guard let raw = String(data: Data(frame), encoding: .utf8) else { return nil }
        // Swift collapses "\r\n" into a SINGLE Character (an extended grapheme
        // cluster), so splitting on Character "\n" silently never splits at a
        // CRLF boundary. Normalise before touching lines at all.
        let text = raw.replacingOccurrences(of: "\r\n", with: "\n")

        var eventName: String?
        var payload = ""

        for rawLine in text.split(separator: "\n", omittingEmptySubsequences: false) {
            let line = String(rawLine)
            if line.isEmpty || line.hasPrefix(":") { continue }

            guard let colon = line.firstIndex(of: ":") else { continue }
            let field = String(line[line.startIndex..<colon])
            var value = String(line[line.index(after: colon)...])
            // A single leading space after the colon is part of the framing.
            if value.hasPrefix(" ") { value.removeFirst() }

            switch field {
            case "event":
                eventName = value
            case "data":
                // Consecutive data: lines concatenate into one document.
                payload += value
            default:
                continue
            }
        }

        guard !payload.isEmpty else { return nil }
        guard var event = try? AxonJSON.decode(AxonEvent.self, from: Data(payload.utf8)) else {
            return nil
        }
        // The payload's own `kind` wins; the frame name is the fallback.
        if event.kind.isEmpty, let eventName {
            event = AxonEvent(
                kind: eventName, level: event.level, message: event.message,
                ts: event.ts, data: event.data
            )
        }
        return event.kind.isEmpty ? nil : event
    }
}

/// Exponential reconnect backoff, 1s doubling to a 30s cap.
///
/// Capped low on purpose: the daemon is a local process the user may restart at
/// any moment, and a Companion that waited minutes to notice would break the
/// ≤5s state promise for the whole session after one blip.
struct SSEBackoff {
    private var attempt = 0
    private let base: Duration = .seconds(1)
    private let cap: Duration = .seconds(30)

    mutating func next() -> Duration {
        let multiplier = 1 << min(attempt, 16)
        attempt += 1
        let delay = base * multiplier
        return delay > cap ? cap : delay
    }

    mutating func reset() { attempt = 0 }
}

/// What the stream is doing. `connected` / `disconnected` matter as much as the
/// events: `DaemonController` uses stream liveness as a fast health signal, so
/// a drop triggers an immediate poll instead of waiting for the next tick.
public enum SSEClientState: Sendable {
    case connected
    case event(AxonEvent)
    case disconnected
}

/// Live event stream client with automatic reconnect.
public actor SSEClient {
    private let url: URL
    private let session: URLSession
    /// Injected so backoff can be asserted without real waiting.
    private let sleep: @Sendable (Duration) async throws -> Void

    public init(
        url: URL = DashboardClient.defaultBaseURL.appending(path: "/events"),
        session: URLSession = .shared,
        sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }
    ) {
        self.url = url
        self.session = session
        self.sleep = sleep
    }

    /// An endless stream of state changes. Cancelling the consuming task, or
    /// breaking out of the `for await`, tears the connection down.
    public func stream() -> AsyncStream<SSEClientState> {
        AsyncStream { continuation in
            let task = Task { [url, session, sleep] in
                var backoff = SSEBackoff()

                while !Task.isCancelled {
                    var connected = false
                    do {
                        var request = URLRequest(url: url)
                        request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
                        // The stream is meant to hang open — no request timeout.
                        request.timeoutInterval = .infinity
                        request.cachePolicy = .reloadIgnoringLocalCacheData

                        let (bytes, response) = try await session.bytes(for: request)
                        if let http = response as? HTTPURLResponse,
                           !(200..<300).contains(http.statusCode)
                        {
                            throw DashboardError.badStatus(http.statusCode)
                        }

                        connected = true
                        backoff.reset()
                        continuation.yield(.connected)

                        var parser = SSEParser()
                        for try await chunk in bytes.chunks {
                            for event in parser.consume(chunk) {
                                continuation.yield(.event(event))
                            }
                        }
                        for event in parser.finish() {
                            continuation.yield(.event(event))
                        }
                    } catch {
                        // Connect failures and mid-stream drops are the same
                        // fact to a consumer: the daemon is not talking.
                    }

                    _ = connected
                    continuation.yield(.disconnected)
                    if Task.isCancelled { break }

                    do {
                        try await sleep(backoff.next())
                    } catch {
                        break
                    }
                }
                continuation.finish()
            }

            continuation.onTermination = { _ in task.cancel() }
        }
    }
}

// MARK: - byte chunking

extension URLSession.AsyncBytes {
    /// Regroup the byte-at-a-time sequence into buffered chunks.
    ///
    /// `URLSession.AsyncBytes` yields one `UInt8` per iteration; feeding the
    /// parser a byte at a time would mean a `firstRange(of:)` scan per byte.
    var chunks: AsyncStream<Data> {
        AsyncStream { continuation in
            let task = Task {
                var buffer = Data()
                buffer.reserveCapacity(4096)
                do {
                    for try await byte in self {
                        buffer.append(byte)
                        // Flush on a frame terminator so events surface with no
                        // added latency, and on size so a chatty stream cannot
                        // grow the buffer without bound.
                        if byte == UInt8(ascii: "\n") || buffer.count >= 4096 {
                            continuation.yield(buffer)
                            buffer.removeAll(keepingCapacity: true)
                        }
                    }
                } catch {
                    // Fall through: the drop is reported by the caller.
                }
                if !buffer.isEmpty { continuation.yield(buffer) }
                continuation.finish()
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }
}
