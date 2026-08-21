import Foundation
import UniformTypeIdentifiers

/// Reduces the items macOS hands a share extension to a ``SharePayload``
/// (CFR-97).
///
/// Safari sends the page URL as an attachment, the page title as
/// `attributedTitle`, and any selection as `attributedContentText`. A text
/// selection from most other apps arrives as a plain-text attachment with no
/// title. Both shapes, and everything between, fold into the same three fields.
public enum ShareExtraction {
    public static func payload(from items: [NSExtensionItem]) async -> SharePayload {
        var payload = SharePayload()

        for item in items {
            if payload.title.isEmpty {
                payload.title = clean(item.attributedTitle?.string)
            }
            if payload.text.isEmpty {
                payload.text = clean(item.attributedContentText?.string)
            }

            for provider in item.attachments ?? [] {
                if payload.url.isEmpty,
                   provider.hasItemConformingToTypeIdentifier(UTType.url.identifier) {
                    payload.url = clean(await load(provider, as: UTType.url))
                }
                if payload.text.isEmpty,
                   provider.hasItemConformingToTypeIdentifier(UTType.plainText.identifier) {
                    payload.text = clean(await load(provider, as: UTType.plainText))
                }
            }
        }

        return payload
    }

    /// `loadItem` hands back whatever the sender boxed: a URL, a string, or
    /// raw bytes. Normalise all three rather than trusting one.
    private static func load(_ provider: NSItemProvider, as type: UTType) async -> String? {
        await withCheckedContinuation { continuation in
            provider.loadItem(forTypeIdentifier: type.identifier, options: nil) { item, _ in
                switch item {
                case let url as URL: continuation.resume(returning: url.absoluteString)
                case let string as String: continuation.resume(returning: string)
                case let data as Data: continuation.resume(returning: String(decoding: data, as: UTF8.self))
                default: continuation.resume(returning: nil)
                }
            }
        }
    }

    private static func clean(_ value: String?) -> String {
        value?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    }
}
