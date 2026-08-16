import Foundation

/// Finds the `axon` binary (CONTRACT.md §10).
///
/// Deliberately does **not** consult `$SHELL -c 'which axon'`: a login shell
/// would run the user's rc files, which is both slow and a way for a broken
/// profile to make Companion look broken.
public enum BinaryLocator {
    /// Search order. `/usr/local/bin` first because that is where the daemon's
    /// own `install.sh` puts it; Homebrew second.
    public static let defaultSearchPaths = [
        "/usr/local/bin",
        "/opt/homebrew/bin",
        "/usr/bin",
    ]

    /// - Parameter inheritedPath: the `PATH` to fall back to. Injected so tests
    ///   can search a controlled set of directories — with the process PATH
    ///   always consulted, a test asserting "found nothing" would find the
    ///   developer's own installed `axon`.
    public static func locate(
        explicit: String? = nil,
        searchPaths: [String] = defaultSearchPaths,
        names: [String] = ["axon"],
        inheritedPath: String? = ProcessInfo.processInfo.environment["PATH"]
    ) -> URL? {
        let fileManager = FileManager.default

        // An explicit setting wins — but a stale one must fall through to
        // discovery rather than bricking the app.
        if let explicit, !explicit.isEmpty {
            let url = URL(fileURLWithPath: explicit)
            if fileManager.isExecutableFile(atPath: url.path) { return url }
        }

        for directory in searchPaths {
            for name in names {
                let url = URL(fileURLWithPath: directory).appending(path: name)
                if fileManager.isExecutableFile(atPath: url.path) { return url }
            }
        }

        // Last resort: the inherited PATH. Present when Companion was launched
        // from a terminal; usually absent for a Finder launch, which is exactly
        // why it is the fallback rather than the primary strategy.
        guard let inheritedPath else { return nil }
        for directory in inheritedPath.split(separator: ":").map(String.init) {
            for name in names {
                let url = URL(fileURLWithPath: directory).appending(path: name)
                if fileManager.isExecutableFile(atPath: url.path) { return url }
            }
        }
        return nil
    }
}
