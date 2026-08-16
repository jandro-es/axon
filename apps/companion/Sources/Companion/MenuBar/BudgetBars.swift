import AxonKit
import SwiftUI

/// Day and week budget, as bars rather than rings.
///
/// The rings this replaces were unreadable at popover size: a 46pt circular
/// gauge at 0.2% draws an arc a couple of pixels long, so both dials looked
/// identical and empty whatever the real numbers were. A horizontal bar has the
/// full popover width to show a small fraction in, and — more importantly —
/// leaves room for the numbers themselves, which are what the user actually
/// wants: how much is left, not a shape.
struct BudgetBars: View {
    let usage: UsageSnapshot

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            BudgetBar(
                title: "Today",
                used: usage.dayUsed, limit: usage.dayLimit, fraction: usage.dayFraction
            )
            BudgetBar(
                title: "This week",
                used: usage.weekUsed, limit: usage.weekLimit, fraction: usage.weekFraction
            )

            if usage.isGuardTripped {
                GuardChip(reason: usage.guardReason)
            }
        }
    }
}

struct BudgetBar: View {
    let title: String
    let used: Int64?
    let limit: Int64?
    let fraction: Double

    /// Green well under budget, amber approaching it, red at the edge. The
    /// colour is the glanceable part — a user should not have to read the
    /// numbers to know whether anything is wrong.
    private var tint: Color {
        switch fraction {
        case ..<0.6: .green
        case ..<0.85: .yellow
        case ..<0.95: .orange
        default: .red
        }
    }

    private var remaining: String {
        guard let used, let limit, limit > used else { return "over budget" }
        return "\(AxonFormat.tokens(limit - used)) left"
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(alignment: .firstTextBaseline) {
                Text(title)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Spacer()
                Text(remaining)
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(tint)
            }

            GeometryReader { geometry in
                ZStack(alignment: .leading) {
                    Capsule()
                        .fill(.quaternary)
                    Capsule()
                        .fill(tint.gradient)
                        // A fraction of a percent still renders as a visible
                        // sliver rather than nothing at all, so "barely used"
                        // and "not connected" never look the same.
                        .frame(width: max(3, geometry.size.width * fraction))
                }
            }
            .frame(height: 6)

            Text("\(AxonFormat.tokens(used)) of \(AxonFormat.tokens(limit))")
                .font(.caption2.monospacedDigit())
                .foregroundStyle(.tertiary)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(
            "\(title): \(AxonFormat.tokens(used)) of \(AxonFormat.tokens(limit)) tokens used, "
                + "\(AxonFormat.percent(fraction)), \(remaining)"
        )
    }
}
