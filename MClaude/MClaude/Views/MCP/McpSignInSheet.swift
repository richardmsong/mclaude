import SwiftUI
import WebKit

/// What the sign-in sheet needs: the session whose process started the sign-in, and where to go.
struct McpSignInTarget: Identifiable {
    let sessionId: String
    let serverName: String
    let authUrl: URL
    let callbackPort: Int
    var id: String { "\(sessionId)/\(serverName)/\(authUrl.absoluteString)" }
}

/// "Sign in to <name>": an in-app web view that catches the provider's localhost callback, with
/// "Open in browser" (the phone's default browser) and a paste-the-address fallback.
/// docs/plan-mcp-servers.md → Sign-in sheet.
struct McpSignInSheet: View {
    let target: McpSignInTarget
    /// Called with the fresh session list after Claude accepts the callback.
    let onSuccess: (SessionMcpList) -> Void

    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss
    @State private var pasteMode = false
    @State private var pasteText = ""
    @State private var finishing = false
    @State private var error: String?
    @State private var reloadToken = 0
    @FocusState private var fieldFocused: Bool

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                if let error {
                    errorBanner(error)
                }
                if pasteMode {
                    pasteState
                } else {
                    webState
                }
            }
            .background(Theme.bgPage)
            .navigationTitle("Sign in to \(mcpDisplayName(target.serverName))")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                        .foregroundStyle(Theme.textSecondary)
                }
                ToolbarItem(placement: .primaryAction) {
                    Button("Open in browser") {
                        UIApplication.shared.open(target.authUrl)
                        error = nil
                        pasteMode = true
                    }
                    .foregroundStyle(Theme.clay)
                    .disabled(finishing)
                }
            }
        }
        .presentationDetents([.large])
        .presentationBackground(Theme.bgPage)
        .interactiveDismissDisabled(finishing)
    }

    // MARK: States

    private var webState: some View {
        McpAuthWebView(url: target.authUrl, callbackPort: target.callbackPort, reloadToken: reloadToken) { url in
            submit(url.absoluteString)
        }
        .overlay {
            if finishing {
                ZStack {
                    Theme.bgPage.opacity(0.92)
                    finishingLabel
                }
            }
        }
    }

    private var finishingLabel: some View {
        HStack(spacing: 10) {
            ProgressView().tint(Theme.textMuted)
            Text("Finishing sign-in…")
                .font(.claudeUI)
                .foregroundStyle(Theme.textSecondary)
        }
    }

    private var pasteState: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                Text("After you approve, your browser lands on a page that can't load. Copy that page's address and paste it here.")
                    .font(.claudeBody)
                    .foregroundStyle(Theme.textPrimary)
                    .fixedSize(horizontal: false, vertical: true)

                PasteButton(payloadType: String.self) { strings in
                    let text = strings.first ?? ""
                    Task { @MainActor in
                        pasteText = text
                        submit(text)
                    }
                }
                .buttonBorderShape(.capsule)
                .tint(Theme.clay)
                .disabled(finishing)

                TextField("http://localhost:…/callback?code=…", text: $pasteText, axis: .horizontal)
                    .font(.claudeMono(13))
                    .foregroundStyle(Theme.textPrimary)
                    .keyboardType(.URL)
                    .textContentType(.URL)
                    .autocorrectionDisabled()
                    .textInputAutocapitalization(.never)
                    .submitLabel(.go)
                    .focused($fieldFocused)
                    .onSubmit { submit(pasteText) }
                    .disabled(finishing)
                    .padding(12)
                    .background(Theme.bgRaised, in: RoundedRectangle(cornerRadius: Theme.radiusSmall, style: .continuous))
                    .claudeHairline(radius: Theme.radiusSmall)

                if finishing {
                    finishingLabel
                }
            }
            .padding(16)
        }
    }

    private func errorBanner(_ text: String) -> some View {
        HStack(alignment: .top, spacing: 8) {
            Text(text)
                .font(.claudeCaption)
                .foregroundStyle(Theme.danger)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 8)
            if !pasteMode {
                Button("Reload") {
                    self.error = nil
                    reloadToken += 1
                }
                .font(.claudeCaptionSemibold)
                .foregroundStyle(Theme.textSecondary)
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 9)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.danger.opacity(0.10))
    }

    // MARK: Callback

    private func submit(_ raw: String) {
        guard !finishing else { return }
        let text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let comps = URLComponents(string: text), comps.scheme != nil, comps.host != nil,
              comps.queryItems?.contains(where: { $0.name == "code" }) == true else {
            error = "That doesn't look like the sign-in link."
            return
        }
        error = nil
        finishing = true
        fieldFocused = false
        Task {
            do {
                let list = try await appState.client.finishMcpAuth(sessionId: target.sessionId,
                                                                   name: target.serverName, callbackUrl: text)
                finishing = false
                onSuccess(list)
                dismiss()
            } catch {
                finishing = false
                self.error = McpText.message(for: error)
            }
        }
    }
}

/// WKWebView that loads the provider's sign-in page and hands back (instead of loading) any
/// navigation to `localhost` / `127.0.0.1` on the callback port.
struct McpAuthWebView: UIViewRepresentable {
    let url: URL
    let callbackPort: Int
    let reloadToken: Int
    let onCallback: (URL) -> Void

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeUIView(context: Context) -> WKWebView {
        let webView = WKWebView(frame: .zero, configuration: WKWebViewConfiguration())
        webView.navigationDelegate = context.coordinator
        webView.uiDelegate = context.coordinator
        webView.allowsBackForwardNavigationGestures = true
        context.coordinator.lastReloadToken = reloadToken
        webView.load(URLRequest(url: url))
        return webView
    }

    func updateUIView(_ webView: WKWebView, context: Context) {
        context.coordinator.parent = self
        if context.coordinator.lastReloadToken != reloadToken {
            context.coordinator.lastReloadToken = reloadToken
            context.coordinator.handled = false
            webView.load(URLRequest(url: url))
        }
    }

    final class Coordinator: NSObject, WKNavigationDelegate, WKUIDelegate {
        var parent: McpAuthWebView
        var lastReloadToken = 0
        /// The callback for the current load has already been handed back.
        var handled = false

        init(_ parent: McpAuthWebView) { self.parent = parent }

        func isCallback(_ url: URL?) -> Bool {
            guard let url, let host = url.host?.lowercased() else { return false }
            return (host == "localhost" || host == "127.0.0.1") && url.port == parent.callbackPort
        }

        private func deliver(_ url: URL) {
            guard !handled else { return }
            handled = true
            let callback = parent.onCallback
            DispatchQueue.main.async { callback(url) }
        }

        func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction,
                     decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void) {
            if let url = navigationAction.request.url, isCallback(url) {
                decisionHandler(.cancel)
                deliver(url)
                return
            }
            // A new page (e.g. the provider again after a rejected callback): the next callback counts.
            if navigationAction.targetFrame?.isMainFrame != false { handled = false }
            decisionHandler(.allow)
        }

        func webView(_ webView: WKWebView, didReceiveServerRedirectForProvisionalNavigation navigation: WKNavigation!) {
            if let url = webView.url, isCallback(url) {
                webView.stopLoading()
                deliver(url)
            }
        }

        /// Pop-ups (`target=_blank`, window.open) load in this web view instead.
        func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                     for navigationAction: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
            if navigationAction.targetFrame == nil { webView.load(navigationAction.request) }
            return nil
        }

        /// Fallback: a redirect to the callback that slipped past the policy check fails to load.
        func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
            let failing = (error as NSError).userInfo[NSURLErrorFailingURLErrorKey] as? URL
            if isCallback(failing), let failing { deliver(failing) }
        }
    }
}
