import SwiftUI

/// **The only file in Companion that names a Liquid Glass API** (CFR-91).
///
/// Everything else says `.axonGlass(...)`. That keeps two things to one place:
/// the Reduce Transparency fallback, which must be honoured everywhere or
/// nowhere, and whatever macOS 27 changes about the material — a one-file fix
/// instead of a hunt.
enum Glass {
    /// Corner radius shared by every glass surface, so adjacent tiles read as
    /// one system rather than a pile of unrelated cards.
    static let cornerRadius: CGFloat = 12
    /// Spacing between glass tiles. Must match the `GlassEffectContainer`
    /// spacing or the effect blends tiles that are not actually adjacent.
    static let tileSpacing: CGFloat = 8
}

private struct AxonGlassModifier: ViewModifier {
    /// Only tappable surfaces opt in — `.interactive()` on a static card
    /// animates in response to input the card does not handle.
    let interactive: Bool
    let cornerRadius: CGFloat

    /// Reduce Transparency is an accessibility setting, not a preference: a
    /// translucent surface over a busy desktop is unreadable for the people who
    /// enable it. Glass is replaced with an opaque fill, never merely dimmed.
    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency

    func body(content: Content) -> some View {
        if reduceTransparency {
            content.background(
                .background.secondary,
                in: .rect(cornerRadius: cornerRadius)
            )
        } else if interactive {
            content.glassEffect(.regular.interactive(), in: .rect(cornerRadius: cornerRadius))
        } else {
            content.glassEffect(.regular, in: .rect(cornerRadius: cornerRadius))
        }
    }
}

extension View {
    /// A glass card. Apply **after** layout and appearance modifiers.
    func axonGlass(
        interactive: Bool = false,
        cornerRadius: CGFloat = Glass.cornerRadius
    ) -> some View {
        modifier(AxonGlassModifier(interactive: interactive, cornerRadius: cornerRadius))
    }

    /// A quiet, opaque card — deliberately **not** glass.
    ///
    /// Charts go here. Translucency behind a plot makes gridlines and small
    /// series fight the desktop behind them, so PRD §4 rules glass out for
    /// chart backgrounds specifically.
    func axonCard(cornerRadius: CGFloat = Glass.cornerRadius) -> some View {
        background(.background.secondary, in: .rect(cornerRadius: cornerRadius))
    }
}

/// Groups adjacent glass tiles so they blend as one surface.
///
/// A thin wrapper for the same reason as the modifier: one place to change when
/// the container API moves.
struct AxonGlassGroup<Content: View>: View {
    var spacing: CGFloat = Glass.tileSpacing
    @ViewBuilder let content: Content

    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency

    var body: some View {
        if reduceTransparency {
            // No glass to blend — the container would only add cost.
            content
        } else {
            GlassEffectContainer(spacing: spacing) { content }
        }
    }
}
