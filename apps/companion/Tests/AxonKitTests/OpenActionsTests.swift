import Foundation
import Testing

@testable import AxonKit

@Suite struct OpenActionTests {
    @Test func dashboardOpensTheRoot() {
        #expect(OpenAction.dashboard.url()?.absoluteString == "http://127.0.0.1:7777")
    }

    @Test func tabsDeepLinkThroughTheFragment() {
        #expect(OpenAction.reviewTab.url()?.absoluteString == "http://127.0.0.1:7777/#review")
        #expect(OpenAction.actionsTab.url()?.absoluteString == "http://127.0.0.1:7777/#actions")
    }

    @Test func tabLinksFollowANonDefaultPort() {
        let base = URL(string: "http://127.0.0.1:9999")!
        #expect(OpenAction.reviewTab.url(baseURL: base)?.absoluteString
            == "http://127.0.0.1:9999/#review")
    }

    @Test func emptyTabFallsBackToTheDashboardRoot() {
        #expect(OpenAction.dashboardTab("").url() == OpenAction.dashboard.url())
    }

    // MARK: Obsidian

    /// Real vault paths have spaces. An unencoded URL either fails to construct
    /// or opens the wrong note, and neither failure is visible in code review.
    @Test func obsidianPathsWithSpacesArePercentEncoded() {
        let url = try? #require(
            OpenAction.vaultInObsidian(vaultPath: "/Users/jandro/Notes/My Vault").url()
        )

        #expect(url?.scheme == "obsidian")
        #expect(url?.absoluteString.contains("My%20Vault") == true)
        #expect(url?.absoluteString.contains(" ") == false)
    }

    @Test func obsidianPathsWithNonASCIIAreEncoded() {
        let url = OpenAction.vaultInObsidian(vaultPath: "/Users/jandro/Notas/Diseño").url()

        #expect(url != nil)
        #expect(url?.absoluteString.contains("Dise") == true)
        #expect(url?.absoluteString.contains("ñ") == false)
    }

    /// Round-tripping proves the encoding is not merely "different", but right.
    @Test func obsidianPathRoundTripsToTheOriginal() throws {
        let path = "/Users/jandro/Notes/My Vault/03-Resources/Ada & Co.md"
        let url = try #require(OpenAction.obsidianURL(path: path))

        let components = try #require(URLComponents(url: url, resolvingAgainstBaseURL: false))
        let decoded = components.queryItems?.first { $0.name == "path" }?.value
        #expect(decoded == path)
    }

    @Test func emptyVaultPathYieldsNoURL() {
        #expect(OpenAction.vaultInObsidian(vaultPath: "").url() == nil)
    }

    // MARK: file locations

    @Test func logsFolderIsDerivedFromTheProfileDataDir() {
        let url = OpenAction.logsFolder(dataDir: "/Users/jandro/.axon/profiles/personal").url()

        #expect(url?.isFileURL == true)
        #expect(url?.path == "/Users/jandro/.axon/profiles/personal/logs")
    }

    @Test func emptyDataDirYieldsNoLogsURL() {
        #expect(OpenAction.logsFolder(dataDir: "").url() == nil)
    }

    @Test func revealInFinderProducesAFileURL() {
        let url = OpenAction.revealInFinder(path: "/Users/jandro/Notes/My Vault").url()

        #expect(url?.isFileURL == true)
        #expect(url?.path == "/Users/jandro/Notes/My Vault")
    }

    @Test func dailyNoteTargetsTheDaemonsPathConvention() throws {
        let url = try #require(
            OpenAction.todaysDailyNote(vaultPath: "/v/My Vault", day: "2026-08-16").url()
        )

        let components = try #require(URLComponents(url: url, resolvingAgainstBaseURL: false))
        let path = components.queryItems?.first { $0.name == "path" }?.value
        #expect(path == "/v/My Vault/Daily/2026-08-16.md")
    }

    @Test func fileBackedActionsAreFlaggedForFinderFallback() {
        #expect(OpenAction.logsFolder(dataDir: "/x").isFileURL)
        #expect(OpenAction.revealInFinder(path: "/x").isFileURL)
        #expect(!OpenAction.dashboard.isFileURL)
        #expect(!OpenAction.vaultInObsidian(vaultPath: "/x").isFileURL)
    }

    @Test func extensionsSettingsOpensTheSharingPane() throws {
        let url = try #require(OpenAction.extensionsSettings.url())
        #expect(url.absoluteString == "x-apple.systempreferences:com.apple.ExtensionsPreferences")
    }
}

// MARK: - formatting

@Suite struct FormattersTests {
    @Test func uptimeReadsInTheLargestUsefulUnit() {
        #expect(AxonFormat.uptime(45) == "45s")
        #expect(AxonFormat.uptime(90) == "1m")
        #expect(AxonFormat.uptime(3600) == "1h")
        #expect(AxonFormat.uptime(3600 * 5 + 60 * 30) == "5h 30m")
        #expect(AxonFormat.uptime(86400 * 2 + 3600 * 3) == "2d 3h")
    }

    @Test func uptimeIsNilWhenTheDaemonCannotSay() {
        #expect(AxonFormat.uptime(nil) == nil)
    }

    @Test func tokenCountsAreCompact() {
        #expect(AxonFormat.tokens(842) == "842")
        #expect(AxonFormat.tokens(3724) == "3.7K")
        #expect(AxonFormat.tokens(1_500_000) == "1.5M")
    }

    /// The version string is not semver-clean on dev builds; the display form
    /// must not mangle it (CONTRACT.md §0).
    @Test func versionsDisplayVerbatimWithoutADuplicateV() {
        #expect(AxonFormat.version("v1.3.2") == "v1.3.2")
        #expect(AxonFormat.version("1.3.2") == "v1.3.2")
        #expect(AxonFormat.version("v1.3.1-1-gec42a3a") == "v1.3.1-1-gec42a3a")
        #expect(AxonFormat.version("01b625917053-dirty") == "01b625917053-dirty")
        #expect(AxonFormat.version(nil) == "unknown")
    }
}
