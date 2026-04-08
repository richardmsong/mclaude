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
    @State private var appState = AppState()
    @Environment(\.scenePhase) private var scenePhase

    init() {
        appState.registerBackgroundTask()
    }

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environment(appState)
                .onChange(of: scenePhase) { _, newPhase in
                    switch newPhase {
                    case .active:
                        guard appState.isSignedIn else { break }
                        appState.connectWebSocket()
                    case .background:
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
        if appState.isSignedIn {
            TabView {
                DashboardView()
                    .tabItem {
                        Label("Sessions", systemImage: "terminal")
                    }

                SettingsView()
                    .tabItem {
                        Label("Settings", systemImage: "gear")
                    }
            }
        } else {
            SignInView()
        }
    }
}

struct SignInView: View {
    @Environment(AppState.self) private var appState
    @State private var isSigningIn = false
    @State private var signInError: String?

    var body: some View {
        VStack(spacing: 32) {
            Spacer()

            Image(systemName: "terminal.fill")
                .font(.system(size: 64))
                .foregroundStyle(.tint)

            Text("MClaude")
                .font(.largeTitle.bold())

            Text("Sign in with your Google account to continue.")
                .font(.subheadline)
                .foregroundStyle(.secondary)
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
                .padding(.vertical, 12)
            }
            .buttonStyle(.borderedProminent)
            .disabled(isSigningIn)
            .padding(.horizontal, 40)

            if isSigningIn {
                ProgressView()
            }

            if let error = signInError {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .padding(.horizontal, 40)
            }

            Spacer()
        }
    }
}
