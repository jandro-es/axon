import AppKit
import AxonKit
import UserNotifications

/// The only place in Companion that touches `UNUserNotificationCenter`.
///
/// The router decides *what* deserves a notification and is pure; this decides
/// nothing and only delivers. Keeping the split means every rule in CFR-70/71
/// is testable without user permission or a signed bundle.
///
/// Not `@MainActor`: `UNUserNotificationCenterDelegate` hands back non-Sendable
/// types, so the callbacks must stay nonisolated and hop to the main actor only
/// for the AppKit handoff.
final class NotificationDelivery: NSObject, UNUserNotificationCenterDelegate, @unchecked Sendable {
    private let lock = NSLock()
    /// Deep-link targets keyed by notification id, so a click lands somewhere
    /// useful. Held here rather than in the request's userInfo so the payload
    /// stays free of anything but display text.
    private var actions: [String: OpenAction] = [:]
    private var didRequestAuthorization = false

    override init() {
        super.init()
        UNUserNotificationCenter.current().delegate = self
    }

    /// Asks for permission lazily — on the first notification worth showing,
    /// not at launch. A menu bar app that demands notification permission
    /// before it has done anything has not earned it.
    func deliver(_ notification: PlannedNotification) {
        lock.withLock { actions[notification.id] = notification.action }

        Task {
            guard await ensureAuthorized() else { return }

            let content = UNMutableNotificationContent()
            content.title = notification.title
            content.body = notification.body
            // A failure is worth a badge, not a chime. Companion interrupts as
            // little as it can and still be useful.
            content.sound = nil

            let request = UNNotificationRequest(
                identifier: notification.id, content: content, trigger: nil
            )
            try? await UNUserNotificationCenter.current().add(request)
        }
    }

    private func ensureAuthorized() async -> Bool {
        let center = UNUserNotificationCenter.current()
        let settings = await center.notificationSettings()

        switch settings.authorizationStatus {
        case .authorized, .provisional, .ephemeral:
            return true
        case .denied:
            return false
        case .notDetermined:
            // Ask once per launch. Repeatedly prompting a user who declined is
            // the fastest way to have the app muted permanently.
            let shouldAsk = lock.withLock {
                if didRequestAuthorization { return false }
                didRequestAuthorization = true
                return true
            }
            guard shouldAsk else { return false }
            return (try? await center.requestAuthorization(options: [.alert, .badge])) ?? false
        @unknown default:
            return false
        }
    }

    // MARK: UNUserNotificationCenterDelegate

    /// Show the banner even when Companion is frontmost — the whole point is
    /// that the user is looking at something else.
    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        completionHandler([.banner, .list])
    }

    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse,
        withCompletionHandler completionHandler: @escaping () -> Void
    ) {
        let id = response.notification.request.identifier
        let action = lock.withLock { actions[id] }
        if let action {
            Task { @MainActor in Opener.open(action) }
        }
        completionHandler()
    }
}
