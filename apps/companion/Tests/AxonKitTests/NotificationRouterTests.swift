import Foundation
import Testing

@testable import AxonKit

/// Collects what the router decided to post.
final class PostedNotifications: @unchecked Sendable {
    private let lock = NSLock()
    private var items: [PlannedNotification] = []

    var all: [PlannedNotification] { lock.withLock { items } }
    var count: Int { all.count }

    func append(_ notification: PlannedNotification) {
        lock.withLock { items.append(notification) }
    }
}

/// A clock the test moves by hand, so dedup windows are asserted without waiting.
final class MovableClock: @unchecked Sendable {
    private let lock = NSLock()
    private var instant = Date(timeIntervalSince1970: 1_787_000_000)

    var now: Date { lock.withLock { instant } }
    func advance(_ seconds: TimeInterval) {
        lock.withLock { instant = instant.addingTimeInterval(seconds) }
    }
}

private func makeRouter(
    prefs: NotificationPrefs = NotificationPrefs()
) -> (NotificationRouter, PostedNotifications, MovableClock) {
    let posted = PostedNotifications()
    let clock = MovableClock()
    let router = NotificationRouter(
        prefs: { prefs },
        post: { posted.append($0) },
        now: { clock.now }
    )
    return (router, posted, clock)
}

private func failEvent(automation: String) -> AxonEvent {
    try! AxonJSON.decode(
        AxonEvent.self,
        from: Data(#"""
        {"kind":"automation.fail","level":"error","message":"\#(automation): failed",
         "data":{"outcome":{"automation":"\#(automation)","status":"failed"}}}
        """#.utf8)
    )
}

private func denyEvent(reason: String = "daily budget exhausted") -> AxonEvent {
    try! AxonJSON.decode(
        AxonEvent.self,
        from: Data(#"""
        {"kind":"token.deny","level":"warn","message":"automation.briefing: \#(reason)",
         "data":{"reason":"\#(reason)","decision":"deny"}}
        """#.utf8)
    )
}

private func health(version: String = "1.3.2") -> AxonHealth {
    try! AxonJSON.decode(AxonHealth.self, from: Data(#"{"version":"\#(version)"}"#.utf8))
}

@Suite struct NotificationRouterTests {
    // MARK: what is never notified

    /// The single hardest rule in the spec (CFR-71). Success is noise, and
    /// noise is what makes people turn notifications off entirely.
    @Test func routineSuccessIsNeverNotified() {
        let (router, posted, _) = makeRouter()

        for kind in ["automation.run", "automation.skip", "ingest.done", "token.ledger",
                     "review.accept", "action.done", "ask.refused"] {
            router.handle(event: AxonEvent(kind: kind, message: "all good"))
        }

        #expect(posted.count == 0)
    }

    @Test func disabledPreferencesSilenceTheirKind() {
        var prefs = NotificationPrefs()
        prefs.automationFailed = false
        let (router, posted, _) = makeRouter(prefs: prefs)

        router.handle(event: failEvent(automation: "briefing"))

        #expect(posted.count == 0)
    }

    // MARK: automation failures

    @Test func anAutomationFailureNotifiesOnce() {
        let (router, posted, _) = makeRouter()
        router.handle(event: failEvent(automation: "briefing"))

        #expect(posted.count == 1)
        let notification = posted.all[0]
        #expect(notification.title.contains("briefing"))
        #expect(notification.action == .dashboardTab("automations"))
    }

    /// A failing automation on a short schedule would otherwise produce a
    /// notification every run, which trains the user to ignore all of them.
    @Test func repeatedFailuresAreDedupedWithinTheHour() {
        let (router, posted, clock) = makeRouter()

        router.handle(event: failEvent(automation: "briefing"))
        clock.advance(60)
        router.handle(event: failEvent(automation: "briefing"))
        clock.advance(600)
        router.handle(event: failEvent(automation: "briefing"))

        #expect(posted.count == 1)
    }

    @Test func failuresNotifyAgainAfterTheWindow() {
        let (router, posted, clock) = makeRouter()

        router.handle(event: failEvent(automation: "briefing"))
        clock.advance(NotificationRouter.automationFailWindow + 1)
        router.handle(event: failEvent(automation: "briefing"))

        #expect(posted.count == 2)
    }

    /// Dedup is per automation: two different automations failing is two
    /// distinct pieces of news.
    @Test func differentAutomationsDedupeIndependently() {
        let (router, posted, _) = makeRouter()

        router.handle(event: failEvent(automation: "briefing"))
        router.handle(event: failEvent(automation: "compaction"))

        #expect(posted.count == 2)
    }

    // MARK: budget guard

    /// A tripped guard denies every automation that tries afterwards, so the
    /// episode notifies once — not once per denied call.
    @Test func aGuardEpisodeNotifiesOnce() {
        let (router, posted, _) = makeRouter()

        router.handle(event: denyEvent())
        router.handle(event: denyEvent())
        router.handle(event: denyEvent(reason: "weekly budget exhausted"))

        #expect(posted.count == 1)
        #expect(posted.all[0].body.contains("daily budget exhausted"))
    }

    @Test func aRestartOpensAFreshGuardEpisode() {
        let (router, posted, _) = makeRouter()
        router.handle(event: denyEvent())

        // A fresh daemon is a fresh budget window.
        router.handle(transition: DaemonTransition(from: .stopped, to: .running(health())))
        router.handle(event: denyEvent())

        #expect(posted.count == 2)
    }

    @Test func everyGuardKindTripsTheSameEpisode() {
        for kind in ["token.deny", "token.defer", "token.downgrade"] {
            let (router, posted, _) = makeRouter()
            router.handle(event: AxonEvent(kind: kind, level: "warn", message: "x"))
            #expect(posted.count == 1, "kind \(kind) did not notify")
        }
    }

    // MARK: unexpected stop

    @Test func anUnexpectedStopNotifies() {
        let (router, posted, _) = makeRouter()

        router.handle(transition: DaemonTransition(from: .running(health()), to: .stopped))

        #expect(posted.count == 1)
        #expect(posted.all[0].title.contains("unexpectedly"))
    }

    /// Clicking Stop in the popover must not produce "AXON stopped
    /// unexpectedly" — the single most obviously wrong notification possible.
    @Test func aUserInitiatedStopIsSilent() {
        let (router, posted, _) = makeRouter()

        router.noteUserInitiatedStop()
        router.handle(transition: DaemonTransition(from: .running(health()), to: .stopped))

        #expect(posted.count == 0)
    }

    /// After a deliberate stop and a restart, a later crash is news again.
    @Test func theUserStopFlagDoesNotPersistPastARestart() {
        let (router, posted, _) = makeRouter()

        router.noteUserInitiatedStop()
        router.handle(transition: DaemonTransition(from: .running(health()), to: .stopped))
        router.handle(transition: DaemonTransition(from: .stopped, to: .running(health())))
        router.handle(transition: DaemonTransition(from: .running(health()), to: .stopped))

        #expect(posted.count == 1)
    }

    @Test func otherTransitionsAreSilent() {
        let (router, posted, _) = makeRouter()

        router.handle(transition: DaemonTransition(from: .unknown, to: .running(health())))
        router.handle(transition: DaemonTransition(from: .stopped, to: .starting))
        router.handle(transition: DaemonTransition(from: .stopped, to: .notInstalled))

        #expect(posted.count == 0)
    }

    @Test func stopNotificationsRespectTheirPreference() {
        var prefs = NotificationPrefs()
        prefs.daemonStoppedUnexpectedly = false
        let (router, posted, _) = makeRouter(prefs: prefs)

        router.handle(transition: DaemonTransition(from: .running(health()), to: .stopped))

        #expect(posted.count == 0)
    }

    // MARK: updates

    @Test func anAvailableUpdateNotifiesOncePerVersion() {
        var prefs = NotificationPrefs()
        prefs.updateAvailable = true
        let (router, posted, _) = makeRouter(prefs: prefs)

        router.handle(updateAvailable: "1.4.0")
        router.handle(updateAvailable: "1.4.0")
        router.handle(updateAvailable: "1.4.1")

        #expect(posted.count == 2)
    }

    /// Off by default: an update is not urgent, and the menu bar badge already
    /// says so without interrupting anyone.
    @Test func updateNotificationsAreOffByDefault() {
        let (router, posted, _) = makeRouter()
        router.handle(updateAvailable: "1.4.0")

        #expect(posted.count == 0)
    }

    // MARK: identity

    /// Ids are stable per subject so a repeat replaces rather than stacks in
    /// Notification Center.
    @Test func notificationIdsAreStablePerSubject() {
        let (router, posted, clock) = makeRouter()

        router.handle(event: failEvent(automation: "briefing"))
        clock.advance(NotificationRouter.automationFailWindow + 1)
        router.handle(event: failEvent(automation: "briefing"))

        #expect(posted.all[0].id == posted.all[1].id)
    }

    @Test func everyNotificationDeepLinksSomewhereUseful() {
        var prefs = NotificationPrefs()
        prefs.updateAvailable = true
        let (router, posted, _) = makeRouter(prefs: prefs)

        router.handle(event: failEvent(automation: "briefing"))
        router.handle(event: denyEvent())
        router.handle(transition: DaemonTransition(from: .running(health()), to: .stopped))
        router.handle(updateAvailable: "1.4.0")

        #expect(posted.count == 4)
        #expect(posted.all.allSatisfy { $0.action.url() != nil })
    }
}
