import Foundation

/// What a macOS share carries, reduced to the three fields
/// `POST /api/capture` stores (ADR-024).
///
/// Lives in AxonKit rather than the extension target so the reduction is unit
/// tested: `NSExtensionItem` and `NSItemProvider` are constructible in a test,
/// a running share extension is not.
public struct SharePayload: Equatable, Sendable {
    public var url: String
    public var title: String
    public var text: String

    public init(url: String = "", title: String = "", text: String = "") {
        self.url = url
        self.title = title
        self.text = text
    }

    /// The daemon answers an all-empty capture with 400; refusing here saves a
    /// pointless round trip and gives the panel something to disable on.
    public var isEmpty: Bool {
        url.isEmpty && title.isEmpty && text.isEmpty
    }
}
