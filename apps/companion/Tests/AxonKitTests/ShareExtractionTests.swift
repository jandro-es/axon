import Foundation
import Testing
import UniformTypeIdentifiers

@testable import AxonKit

private func urlProvider(_ string: String) -> NSItemProvider {
    NSItemProvider(item: NSURL(string: string)!, typeIdentifier: UTType.url.identifier)
}

private func textProvider(_ string: String) -> NSItemProvider {
    NSItemProvider(item: string as NSString, typeIdentifier: UTType.plainText.identifier)
}

private func item(
    attachments: [NSItemProvider] = [],
    title: String? = nil,
    selection: String? = nil
) -> NSExtensionItem {
    let item = NSExtensionItem()
    if !attachments.isEmpty { item.attachments = attachments }
    if let title { item.attributedTitle = NSAttributedString(string: title) }
    if let selection { item.attributedContentText = NSAttributedString(string: selection) }
    return item
}

/// CFR-97 — what a macOS share reduces to.
@Suite
struct ShareExtractionTests {
    @Test func safariShapedShareCarriesURLTitleAndSelection() async {
        let payload = await ShareExtraction.payload(from: [
            item(attachments: [urlProvider("https://example.com/article")],
                 title: "An article",
                 selection: "the paragraph I highlighted")
        ])

        #expect(payload.url == "https://example.com/article")
        #expect(payload.title == "An article")
        #expect(payload.text == "the paragraph I highlighted")
        #expect(payload.isEmpty == false)
    }

    @Test func plainTextSelectionYieldsTextOnly() async {
        let payload = await ShareExtraction.payload(from: [
            item(attachments: [textProvider("a thought worth keeping")])
        ])

        #expect(payload.url.isEmpty)
        #expect(payload.title.isEmpty)
        #expect(payload.text == "a thought worth keeping")
    }

    @Test func bareURLYieldsURLWithEmptyTitle() async {
        let payload = await ShareExtraction.payload(from: [
            item(attachments: [urlProvider("https://example.com/a")])
        ])

        #expect(payload.url == "https://example.com/a")
        #expect(payload.title.isEmpty)
        #expect(payload.text.isEmpty)
    }

    @Test func whitespaceOnlySelectionIsNotText() async {
        let payload = await ShareExtraction.payload(from: [
            item(attachments: [urlProvider("https://example.com/a")], selection: "   \n  ")
        ])

        // "" not "   " — the daemon would otherwise write a blank body line.
        #expect(payload.text.isEmpty)
    }

    @Test func emptyShareIsRefusedBeforeAnyRoundTrip() async {
        let payload = await ShareExtraction.payload(from: [item()])
        #expect(payload.isEmpty)
    }

    @Test func noItemsIsEmpty() async {
        let payload = await ShareExtraction.payload(from: [])
        #expect(payload.isEmpty)
    }
}
