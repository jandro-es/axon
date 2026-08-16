import Foundation

/// Display formatting shared by the popover, Insights and Settings.
///
/// In AxonKit rather than the app target so the rules are unit-tested — a
/// wrong uptime or a doubled "v" is the kind of thing only a test notices.
public enum AxonFormat {
    /// Uptime in the largest useful unit, e.g. `5h 30m`.
    ///
    /// Returns nil when the daemon cannot say (an older daemon has no
    /// `started_at`), so callers hide the pill instead of showing "0s".
    public static func uptime(_ seconds: TimeInterval?) -> String? {
        guard let seconds, seconds >= 0 else { return nil }
        let total = Int(seconds)
        let days = total / 86400
        let hours = (total % 86400) / 3600
        let minutes = (total % 3600) / 60

        if days > 0 { return hours > 0 ? "\(days)d \(hours)h" : "\(days)d" }
        if hours > 0 { return minutes > 0 ? "\(hours)h \(minutes)m" : "\(hours)h" }
        if minutes > 0 { return "\(minutes)m" }
        return "\(total)s"
    }

    /// Compact token counts: `3.7K`, `1.5M`. Budgets run to millions, and the
    /// popover has no room for `1,500,000`.
    public static func tokens(_ count: Int64?) -> String {
        guard let count else { return "—" }
        let value = Double(count)
        switch abs(count) {
        case 1_000_000...:
            return trim(value / 1_000_000) + "M"
        case 1_000...:
            return trim(value / 1_000) + "K"
        default:
            return "\(count)"
        }
    }

    private static func trim(_ value: Double) -> String {
        let rounded = (value * 10).rounded() / 10
        return rounded == rounded.rounded()
            ? String(Int(rounded))
            : String(format: "%.1f", rounded)
    }

    /// A version for display. Dev builds are not semver — `v1.3.1-1-gec42a3a`
    /// and `01b625917053-dirty` are both real — so this only adds a missing
    /// leading `v` to a plain dotted version and otherwise passes through.
    public static func version(_ raw: String?) -> String {
        guard let raw, !raw.isEmpty else { return "unknown" }
        if raw.hasPrefix("v") { return raw }
        // Only a leading digit-and-dot looks like a version worth prefixing;
        // a bare commit hash must stay untouched.
        return raw.first?.isNumber == true && raw.contains(".") ? "v" + raw : raw
    }

    /// Percentage for a 0...1 fraction, e.g. `0.2%`.
    public static func percent(_ fraction: Double) -> String {
        let value = fraction * 100
        return value > 0 && value < 0.1
            ? "<0.1%"
            : String(format: value < 10 ? "%.1f%%" : "%.0f%%", value)
    }

    /// Relative time for run timestamps, e.g. "2 minutes ago".
    public static func relative(_ date: Date?, asOf now: Date = .now) -> String {
        guard let date else { return "never" }
        let formatter = RelativeDateTimeFormatter()
        formatter.unitsStyle = .full
        return formatter.localizedString(for: date, relativeTo: now)
    }
}
