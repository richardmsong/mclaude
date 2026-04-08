import Foundation

@Observable
final class DashboardViewModel {
    var isLoading = true

    func startListening(appState: AppState) {
        appState.connectWebSocket()
        isLoading = false
    }

    func stopListening(appState: AppState) {
        // Don't disconnect — WS should stay alive for session detail views
    }
}
