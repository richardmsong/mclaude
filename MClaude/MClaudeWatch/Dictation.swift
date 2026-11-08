import Foundation
#if os(watchOS)
import WatchKit
#endif

/// Apple's dictation on the watch (docs/plan-watch-app.md → Talk). SwiftUI has no way to open it
/// directly on watchOS; WatchKit's text input controller with no suggestions goes straight to it.
enum Dictation {
    /// Open dictation. `done` gets the text, or nil when cancelled. Returns false if it couldn't be shown.
    @MainActor
    static func present(_ done: @escaping (String?) -> Void) -> Bool {
        #if os(watchOS)
        let app = WKApplication.shared()
        guard let controller = app.visibleInterfaceController ?? app.rootInterfaceController else { return false }
        controller.presentTextInputController(withSuggestions: nil, allowedInputMode: .plain) { results in
            let text = (results ?? []).compactMap { $0 as? String }.joined(separator: " ")
            DispatchQueue.main.async { done(results == nil ? nil : text) }
        }
        return true
        #else
        return false
        #endif
    }
}
