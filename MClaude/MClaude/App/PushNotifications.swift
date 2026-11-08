import SwiftUI
import UIKit
import UserNotifications

/// Push notifications: the server pushes when a long turn (5+ min) finishes. This delegate asks
/// for permission, hands the APNs device token to the server, and routes taps to the session.
final class AppDelegate: NSObject, UIApplicationDelegate, UNUserNotificationCenterDelegate {
    /// Set by MClaudeApp once the app state exists.
    weak var appState: AppState? {
        didSet { if let token = pendingToken { register(token) } }
    }
    private var pendingToken: String?

    func application(_ application: UIApplication,
                     didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        UNUserNotificationCenter.current().delegate = self
        // Early, so a watch refresh that wakes the app in the background gets answered.
        WatchRelay.shared.activate()
        return true
    }

    /// Ask once (iOS remembers the answer) and register with APNs.
    static func requestPermissionAndRegister() {
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge]) { granted, _ in
            guard granted else { return }
            DispatchQueue.main.async { UIApplication.shared.registerForRemoteNotifications() }
        }
    }

    func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        let token = deviceToken.map { String(format: "%02x", $0) }.joined()
        if appState == nil { pendingToken = token } else { register(token) }
    }

    func application(_ application: UIApplication, didFailToRegisterForRemoteNotificationsWithError error: Error) {
        print("[push] registration failed: \(error.localizedDescription)")
    }

    private func register(_ token: String) {
        pendingToken = nil
        guard let appState else { return }
        #if DEBUG
        let environment = "sandbox"       // Xcode builds talk to the APNs sandbox
        #else
        let environment = "production"    // TestFlight / App Store
        #endif
        Task { try? await appState.client.registerDevice(token: token, environment: environment) }
    }

    // Banner even in the foreground, unless the user is already looking at that session.
    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        let sessionId = notification.request.content.userInfo["sessionId"] as? String
        let viewing = sessionId != nil && sessionId == appState?.visibleSessionId
        completionHandler(viewing ? [] : [.banner, .sound, .list])
    }

    // Tap → open that session.
    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                withCompletionHandler completionHandler: @escaping () -> Void) {
        if let sessionId = response.notification.request.content.userInfo["sessionId"] as? String, !sessionId.isEmpty {
            DispatchQueue.main.async { self.appState?.openSessionRequest = sessionId }
        }
        completionHandler()
    }
}
