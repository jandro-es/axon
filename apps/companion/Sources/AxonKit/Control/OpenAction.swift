import Foundation

/// Somewhere Companion can send the user (CFR-20).
///
/// Lives in AxonKit rather than the app target so URL construction — the part
/// that is easy to get subtly wrong with spaces and unicode in vault paths — is
/// unit-tested without launching a UI.
public enum OpenAction: Equatable, Sendable {
    case dashboard
    /// A dashboard tab, deep-linked through the URL fragment.
    case dashboardTab(String)
    case capturePage
    case vaultInObsidian(vaultPath: String)
    case revealInFinder(path: String)
    case logsFolder(dataDir: String)
    case todaysDailyNote(vaultPath: String, day: String)

    public static let reviewTab = OpenAction.dashboardTab("review")
    public static let actionsTab = OpenAction.dashboardTab("actions")

    /// The URL to open, or nil when the action needs a path Companion does not
    /// have yet (no profile read, no vault configured).
    public func url(baseURL: URL = DashboardClient.defaultBaseURL) -> URL? {
        switch self {
        case .dashboard:
            return baseURL

        case .dashboardTab(let tab):
            // Fragment routing, added to the SPA for exactly this (CONTRACT §11).
            // An unknown fragment falls back to overview daemon-side, so this
            // can never land the dashboard on a blank page.
            guard !tab.isEmpty else { return baseURL }
            return URL(string: baseURL.absoluteString + "/#" + tab)

        case .capturePage:
            return baseURL.appending(path: "capture")

        case .vaultInObsidian(let vaultPath):
            return Self.obsidianURL(path: vaultPath)

        case .revealInFinder(let path):
            guard !path.isEmpty else { return nil }
            return URL(fileURLWithPath: path)

        case .logsFolder(let dataDir):
            guard !dataDir.isEmpty else { return nil }
            return URL(fileURLWithPath: dataDir).appending(path: "logs")

        case .todaysDailyNote(let vaultPath, let day):
            guard !vaultPath.isEmpty else { return nil }
            // The daemon writes daily notes as Daily/<YYYY-MM-DD>.md.
            let notePath = URL(fileURLWithPath: vaultPath)
                .appending(path: "Daily")
                .appending(path: "\(day).md")
            return Self.obsidianURL(path: notePath.path)
        }
    }

    /// `obsidian://open?path=<percent-encoded absolute path>`.
    ///
    /// Percent-encoding here is not optional: real vault paths contain spaces
    /// ("Notes/My Vault") and non-ASCII characters, and an unencoded URL either
    /// fails to construct or opens the wrong note.
    static func obsidianURL(path: String) -> URL? {
        guard !path.isEmpty else { return nil }
        var components = URLComponents()
        components.scheme = "obsidian"
        components.host = "open"
        components.queryItems = [URLQueryItem(name: "path", value: path)]
        return components.url
    }

    /// Whether opening this needs a real file to exist. Callers fall back to
    /// revealing the parent folder in Finder when it does not.
    public var isFileURL: Bool {
        switch self {
        case .revealInFinder, .logsFolder: true
        default: false
        }
    }
}
