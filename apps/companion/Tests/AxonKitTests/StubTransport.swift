import Foundation

/// Thread-safe storage for one stubbed transport.
///
/// `URLProtocol` calls `startLoading()` on URLSession's own thread while the
/// test body writes routes from the test thread, so this genuinely needs a lock
/// — `nonisolated(unsafe)` statics alone are a data race, not just a Swift 6
/// annotation chore.
final class StubStore: @unchecked Sendable {
    struct Response: Sendable {
        var status: Int = 200
        var body: Data = Data()
        var headers: [String: String] = [:]
    }

    private let lock = NSLock()
    private var _routes: [String: Response] = [:]
    private var _failure: Error?
    private var _recorded: [URLRequest] = []

    var routes: [String: Response] {
        get { lock.withLock { _routes } }
        set { lock.withLock { _routes = newValue } }
    }

    var failure: Error? {
        get { lock.withLock { _failure } }
        set { lock.withLock { _failure = newValue } }
    }

    var recorded: [URLRequest] { lock.withLock { _recorded } }

    func record(_ request: URLRequest) {
        lock.withLock { _recorded.append(request) }
    }

    func reset() {
        lock.withLock {
            _routes = [:]
            _failure = nil
            _recorded = []
        }
    }
}

/// Serves fixture bytes per path so client tests never touch the network.
///
/// Each concrete subclass owns its own ``StubStore``. Suites that share one
/// store would interfere when Swift Testing runs them in parallel — which is
/// not hypothetical: a shared store deadlocked the SSE and dashboard suites
/// against each other. One store per subclass keeps the suites independent.
class StubURLProtocol: URLProtocol, @unchecked Sendable {
    /// Overridden by each concrete stub. Never called on the base.
    class var store: StubStore {
        fatalError("StubURLProtocol subclasses must provide a store")
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let store = Self.store
        store.record(request)

        if let failure = store.failure {
            client?.urlProtocol(self, didFailWithError: failure)
            return
        }
        guard let route = store.routes[request.url?.path ?? ""] else {
            client?.urlProtocol(self, didFailWithError: URLError(.unsupportedURL))
            return
        }
        let response = HTTPURLResponse(
            url: request.url!, statusCode: route.status,
            httpVersion: "HTTP/1.1", headerFields: route.headers
        )!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: route.body)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

/// Stub for `DashboardClient` tests.
final class DashboardStubProtocol: StubURLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static let shared = StubStore()
    override class var store: StubStore { shared }
}

/// Stub for `SSEClient` tests — deliberately a separate store.
final class SSEStubProtocol: StubURLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static let shared = StubStore()
    override class var store: StubStore { shared }
}

extension URLSession {
    /// An ephemeral session wired to one stub protocol.
    static func stubbed(_ protocolClass: StubURLProtocol.Type) -> URLSession {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [protocolClass]
        return URLSession(configuration: config)
    }
}
