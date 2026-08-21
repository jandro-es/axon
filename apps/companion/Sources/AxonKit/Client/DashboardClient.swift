import Foundation

/// Everything that can go wrong talking to the dashboard API.
public enum DashboardError: Error, Equatable, Sendable {
    /// Nothing is listening, or the connection dropped. The common case: the
    /// daemon is stopped. Callers treat this as a state, not an error to show.
    case unreachable
    /// The daemon answered, but not with success.
    case badStatus(Int)
    /// The daemon answered with something we could not decode. Distinct from
    /// `unreachable` on purpose: this one means the *contract* broke.
    case decoding(String)
}

/// Read-only REST client for the daemon's dashboard API (CONTRACT.md §§2-8).
///
/// An `actor` because it is shared by the poll loop, the metrics store and the
/// popover; serialising its state costs nothing and removes a class of races.
/// Every request is loopback-only and short-lived — the SSE stream is separate.
public actor DashboardClient {
    /// Default dashboard address. Overridden from `axon config get dashboard.port`
    /// when the profile moves it.
    public static let defaultBaseURL = URL(string: "http://127.0.0.1:7777")!

    /// Polling reads must fail fast: a hung request would stall the icon's
    /// ≤5 s state detection (CFR-01).
    static let requestTimeout: TimeInterval = 3

    private let baseURL: URL
    private let session: URLSession

    public init(baseURL: URL = DashboardClient.defaultBaseURL, session: URLSession = .shared) {
        self.baseURL = baseURL
        self.session = session
    }

    // MARK: reads

    public func health() async throws -> AxonHealth {
        try await get(AxonHealth.self, path: "/health")
    }

    public func usage() async throws -> UsageSnapshot {
        try await get(UsageSnapshot.self, path: "/api/usage")
    }

    /// `days` maps to the daemon's `?days=` window (default 30).
    public func tokens(days: Int = 30) async throws -> TokenSeries {
        try await get(TokenSeries.self, path: "/api/tokens", query: ["days": String(days)])
    }

    /// `limit` is a **row count**, not a time range (CONTRACT.md §5).
    public func runs(limit: Int = 100) async throws -> [RunRecord] {
        try await get([RunRecord].self, path: "/api/runs", query: ["limit": String(limit)])
    }

    public func ingestion() async throws -> IngestionStats {
        try await get(IngestionStats.self, path: "/api/ingestion")
    }

    public func vault() async throws -> VaultStats {
        try await get(VaultStats.self, path: "/api/vault")
    }

    /// Pending review-queue items for the badge. `items` is ignored — it can be
    /// ~100 KB and the badge needs one integer.
    public func reviewCount() async throws -> Int? {
        try await get(ReviewMeta.self, path: "/api/review").pending
    }

    /// Open actions for the badge, or nil when the profile disabled actions
    /// (the daemon answers 404) — the badge hides rather than erroring.
    public func actionsCount() async throws -> Int? {
        do {
            let meta = try await get(
                ActionsMeta.self, path: "/api/actions",
                headers: ["X-Axon-Actions": "1"]
            )
            return meta.openCount
        } catch DashboardError.badStatus(404) {
            return nil
        }
    }

    // MARK: query (CFR-92…95 — the Siri/Shortcuts verbs)

    /// Hybrid search (CONTRACT.md §8b). Read-only, zero model spend. 404 means
    /// the profile disabled `dashboard.search_enabled` (or the daemon predates
    /// FR-198) — callers surface that as a state.
    public func search(_ q: String, topK: Int = 8) async throws -> [SearchHit] {
        try await get(
            SearchResponse.self, path: "/api/search",
            query: ["q": q, "top_k": String(topK)],
            headers: ["X-Axon-Search": "1"]
        ).hits
    }

    /// Grounded ask (ADR-023). Spends synthesis tokens through the daemon's
    /// chokepoint — always human-initiated from here. The synthesis call is
    /// slow by nature, hence the generous timeout.
    public func ask(_ question: String) async throws -> AskAnswer {
        try await post(
            AskAnswer.self, path: "/api/ask",
            body: ["question": question],
            headers: ["X-Axon-Ask": "1"],
            timeout: 90
        )
    }

    /// Non-destructive inbox capture (ADR-024): creates a note under
    /// `00-Inbox/`, never edits anything. Zero model spend.
    ///
    /// Empty fields are omitted rather than sent as `""`: the daemon renders
    /// `# Captured note` when it receives no title, and writes the URL on its
    /// own first line — which is what makes the capture automation fetch it.
    /// The default arguments keep the spoken-thought caller (CFR-95) reading
    /// as `capture(text:)`.
    public func capture(url: String = "", title: String = "", text: String = "") async throws {
        var body: [String: String] = [:]
        if !url.isEmpty { body["url"] = url }
        if !title.isEmpty { body["title"] = title }
        if !text.isEmpty { body["text"] = text }
        try await postIgnoringBody(
            path: "/api/capture",
            body: body,
            headers: ["X-Axon-Capture": "1"]
        )
    }

    // MARK: export (CFR-31)

    /// The daemon serialises every export; Companion only builds the URL.
    public nonisolated func exportURL(dataset: String, format: String) -> URL {
        var components = URLComponents(url: baseURL.appending(path: "/api/export"),
                                       resolvingAgainstBaseURL: false)!
        components.queryItems = [
            URLQueryItem(name: "dataset", value: dataset),
            URLQueryItem(name: "format", value: format),
        ]
        return components.url!
    }

    /// Mirrors the daemon's `Content-Disposition` filename so the save panel
    /// suggests the same name whether or not the header survives.
    public nonisolated func exportFilename(dataset: String, format: String, day: String) -> String {
        "axon-\(dataset)-\(day).\(format)"
    }

    /// The daemon 403s any request whose Host is not loopback (FR-63), so a
    /// non-loopback base URL can only ever fail. Callers check before adopting
    /// a configured host.
    public nonisolated static func isLoopback(_ url: URL) -> Bool {
        switch url.host()?.lowercased() {
        case "127.0.0.1", "localhost", "::1", "[::1]": true
        default: false
        }
    }

    // MARK: transport

    private func post<T: Decodable>(
        _ type: T.Type,
        path: String,
        body: [String: String],
        headers: [String: String],
        timeout: TimeInterval = DashboardClient.requestTimeout
    ) async throws -> T {
        let data = try await postRaw(path: path, body: body, headers: headers, timeout: timeout)
        do {
            return try AxonJSON.decode(type, from: data)
        } catch {
            throw DashboardError.decoding(String(describing: error))
        }
    }

    private func postIgnoringBody(
        path: String,
        body: [String: String],
        headers: [String: String]
    ) async throws {
        _ = try await postRaw(path: path, body: body, headers: headers,
                              timeout: Self.requestTimeout)
    }

    private func postRaw(
        path: String,
        body: [String: String],
        headers: [String: String],
        timeout: TimeInterval
    ) async throws -> Data {
        var request = URLRequest(url: baseURL.appending(path: path))
        request.httpMethod = "POST"
        request.timeoutInterval = timeout
        request.cachePolicy = .reloadIgnoringLocalCacheData
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        for (key, value) in headers {
            request.setValue(value, forHTTPHeaderField: key)
        }
        request.httpBody = try JSONEncoder().encode(body)

        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: request)
        } catch {
            throw DashboardError.unreachable
        }
        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            throw DashboardError.badStatus(http.statusCode)
        }
        return data
    }

    private func get<T: Decodable>(
        _ type: T.Type,
        path: String,
        query: [String: String] = [:],
        headers: [String: String] = [:]
    ) async throws -> T {
        var components = URLComponents(url: baseURL.appending(path: path),
                                       resolvingAgainstBaseURL: false)!
        if !query.isEmpty {
            components.queryItems = query
                .sorted { $0.key < $1.key }
                .map { URLQueryItem(name: $0.key, value: $0.value) }
        }

        var request = URLRequest(url: components.url!)
        request.timeoutInterval = Self.requestTimeout
        // Never serve a stale body for a liveness check.
        request.cachePolicy = .reloadIgnoringLocalCacheData
        for (key, value) in headers {
            request.setValue(value, forHTTPHeaderField: key)
        }

        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: request)
        } catch {
            // Any transport error means "cannot talk to the daemon". The
            // distinction between refused, timed out and cancelled is not
            // actionable for the user.
            throw DashboardError.unreachable
        }

        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            throw DashboardError.badStatus(http.statusCode)
        }

        do {
            return try AxonJSON.decode(type, from: data)
        } catch {
            throw DashboardError.decoding(String(describing: error))
        }
    }
}
