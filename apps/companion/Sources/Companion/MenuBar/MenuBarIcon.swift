import AxonKit
import SwiftUI

/// The menu bar glyph.
///
/// Menu bar images must be **template** images: the system tints them for the
/// current appearance, wallpaper and highlight state. That means state cannot
/// be carried by colour in the glyph itself — only by shape — which is why
/// attention is a badge and "stopped" is the unfilled variant.
struct MenuBarIcon: View {
    let state: DaemonState

    var body: some View {
        // Monochrome, not hierarchical or multicolour: the menu bar renders
        // its label as a template image and tints it for the current
        // appearance, so any colour in the glyph is discarded anyway.
        Image(systemName: symbolName)
            .symbolRenderingMode(.monochrome)
            .accessibilityLabel(accessibilityLabel)
    }

    private var symbolName: String {
        switch state {
        case .runningWith(_, let reasons):
            reasons.isEmpty ? "brain.fill" : "brain.filled.head.profile"
        case .starting, .stopping, .unknown:
            "brain"
        case .stopped:
            "brain"
        case .notInstalled:
            "brain"
        }
    }

    private var accessibilityLabel: String {
        "AXON — \(state.summary)"
    }
}

/// The label shown in the menu bar, including the attention badge.
///
/// `MenuBarExtra`'s label is rendered as a template, so the badge is drawn as a
/// separate overlay that the system tints along with the glyph. Shape, not
/// colour, is what survives — the badge reads as "something is different"
/// regardless of the wallpaper behind it.
struct MenuBarLabel: View {
    let state: DaemonState

    var body: some View {
        MenuBarIcon(state: state)
            .overlay(alignment: .topTrailing) {
                if state.needsAttention {
                    Circle()
                        .frame(width: 5, height: 5)
                        .offset(x: 2, y: -1)
                } else if state == .notInstalled {
                    Image(systemName: "questionmark")
                        .font(.system(size: 7, weight: .bold))
                        .offset(x: 3, y: -1)
                }
            }
            // Stopped and not-installed read as inactive. Opacity is the one
            // visual dimension a template image keeps.
            .opacity(isInactive ? 0.55 : 1)
    }

    private var isInactive: Bool {
        switch state {
        case .stopped, .notInstalled, .unknown: true
        case .starting, .stopping, .runningWith: false
        }
    }
}
