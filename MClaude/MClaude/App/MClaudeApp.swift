import SwiftUI
import UIKit

// Ensure the interactive pop gesture (swipe-back) is never blocked by subviews
extension UINavigationController: @retroactive UIGestureRecognizerDelegate {
    override open func viewDidLoad() {
        super.viewDidLoad()
        interactivePopGestureRecognizer?.delegate = self
    }

    public func gestureRecognizerShouldBegin(_ gestureRecognizer: UIGestureRecognizer) -> Bool {
        return viewControllers.count > 1
    }

    public func gestureRecognizer(_ gestureRecognizer: UIGestureRecognizer, shouldRecognizeSimultaneouslyWith other: UIGestureRecognizer) -> Bool {
        return true
    }
}

@main
struct MClaudeApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @State private var appState = AppState()
    @Environment(\.scenePhase) private var scenePhase

    init() {
        CrashReporter.shared.start()
        appState.registerBackgroundTask()
    }

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environment(appState)
                .onAppear {
                    appDelegate.appState = appState
                    if appState.isSignedIn { AppDelegate.requestPermissionAndRegister() }
                }
                .onChange(of: appState.isSignedIn) { _, signedIn in
                    if signedIn { AppDelegate.requestPermissionAndRegister() }
                }
                .onChange(of: scenePhase) { _, newPhase in
                    switch newPhase {
                    case .active:
                        if let id = appState.visibleSessionId { SeenStore.shared.markSeen(id) }
                        guard appState.isSignedIn else { break }
                        appState.connectWebSocket()
                    case .background:
                        if let id = appState.visibleSessionId { SeenStore.shared.markSeen(id) }
                        appState.scheduleBackgroundRefresh()
                    default:
                        break
                    }
                }
        }
    }
}

struct ContentView: View {
    @Environment(AppState.self) private var appState

    var body: some View {
        Group {
            if appState.isSignedIn {
                RootView()
            } else {
                SignInView()
            }
        }
        .tint(Theme.clay)
    }
}

struct SignInView: View {
    @Environment(AppState.self) private var appState
    @State private var isSigningIn = false
    @State private var signInError: String?

    var body: some View {
        VStack(spacing: 32) {
            Spacer()

            MClaudeMark(size: 64)

            Text("MClaude")
                .font(.system(size: 34, weight: .semibold, design: .serif))
                .foregroundStyle(Theme.textPrimary)

            Text("Sign in with your Google account to continue.")
                .font(.claudeUI)
                .foregroundStyle(Theme.textMuted)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 40)

            Button {
                Task {
                    isSigningIn = true
                    signInError = nil
                    do {
                        try await appState.signInWithGoogle()
                    } catch {
                        signInError = error.localizedDescription
                    }
                    isSigningIn = false
                }
            } label: {
                HStack {
                    Image(systemName: "person.badge.key")
                    Text("Sign in with Google")
                }
                .frame(maxWidth: .infinity)
                .padding(.vertical, 4)
            }
            .buttonStyle(ClayCapsuleButtonStyle(enabled: !isSigningIn))
            .disabled(isSigningIn)
            .padding(.horizontal, 40)

            if isSigningIn {
                ProgressView()
            }

            if let error = signInError {
                Text(error)
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.danger)
                    .padding(.horizontal, 40)
            }

            Spacer()
        }
        .frame(maxWidth: .infinity)
        .background(Theme.bgPage.ignoresSafeArea())
    }
}
