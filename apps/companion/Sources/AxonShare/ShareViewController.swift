import AppKit
import AxonKit
import SwiftUI

/// The share extension's principal class (CFR-96) — named verbatim in the
/// appex Info.plist as `AxonShare.ShareViewController`. Rename either and the
/// extension loads to a blank sheet.
///
/// It owns no logic: AxonKit reduces the shared items, the panel model runs the
/// rules, and this class only bridges the extension context.
final class ShareViewController: NSViewController {
    private var model: SharePanelModel?

    override func loadView() {
        let model = SharePanelModel(
            capture: { payload in
                try await DashboardClient().capture(
                    url: payload.url, title: payload.title, text: payload.text)
            },
            finish: { [weak self] in
                self?.extensionContext?.completeRequest(returningItems: [], completionHandler: nil)
            }
        )
        self.model = model

        let hosting = NSHostingController(rootView: SharePanel(model: model))
        addChild(hosting)
        view = hosting.view

        let items = (extensionContext?.inputItems as? [NSExtensionItem]) ?? []
        // Inherits this view controller's main-actor isolation; annotating the
        // task @MainActor would instead make `items` cross a boundary it
        // cannot (NSExtensionItem is not Sendable).
        Task {
            model.load(await ShareExtraction.payload(from: items))
        }
    }
}
