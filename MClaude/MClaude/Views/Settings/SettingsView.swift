import SwiftUI

struct SettingsView: View {
    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss
    @State private var testPushResult: String?
    @State private var isChecking = false
    @State private var connectionResult: Bool?
    @State private var isSigningIn = false
    @State private var signInError: String?
    /// `GET /mcp` answered 403: not the laptop owner, so the MCP servers row is hidden.
    @State private var mcpForbidden = false

    var body: some View {
        @Bindable var state = appState

        NavigationStack {
            Form {
                Section {
                    HStack {
                        Text("Host")
                        Spacer()
                        TextField("IP or hostname", text: $state.serverHost)
                            .multilineTextAlignment(.trailing)
                            .font(.claudeMono(15))
                            .foregroundStyle(Theme.textSecondary)
                            .autocorrectionDisabled()
                            .textInputAutocapitalization(.never)
                    }

                    HStack {
                        Text("Port")
                        Spacer()
                        TextField("Port", value: $state.serverPort, format: .number.grouping(.never))
                            .multilineTextAlignment(.trailing)
                            .font(.claudeMono(15))
                            .foregroundStyle(Theme.textSecondary)
                            .keyboardType(.numberPad)
                    }

                    Button {
                        Task {
                            isChecking = true
                            connectionResult = await appState.client.healthCheck()
                            isChecking = false
                        }
                    } label: {
                        HStack {
                            Text("Test Connection")
                            Spacer()
                            if isChecking {
                                ProgressView()
                            } else if let result = connectionResult {
                                Image(systemName: result ? "checkmark.circle" : "xmark.circle")
                                    .foregroundStyle(result ? Theme.success : Theme.danger)
                            }
                        }
                    }
                } header: {
                    sectionHeader("Server")
                }
                .listRowBackground(Theme.bgRaised)

                Section {
                    if appState.isSignedIn {
                        HStack {
                            VStack(alignment: .leading, spacing: 4) {
                                Text(appState.userName.isEmpty ? "Signed In" : appState.userName)
                                    .font(.claudeUISemibold)
                                    .foregroundStyle(Theme.textPrimary)
                                if !appState.userEmail.isEmpty {
                                    Text(appState.userEmail)
                                        .font(.claudeCaption)
                                        .foregroundStyle(Theme.textMuted)
                                }
                            }
                            Spacer()
                            Image(systemName: "checkmark.circle")
                                .foregroundStyle(Theme.success)
                        }

                        Button("Sign Out", role: .destructive) {
                            appState.signOut()
                        }
                        .foregroundStyle(Theme.danger)
                    } else {
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
                                Spacer()
                                if isSigningIn {
                                    ProgressView()
                                }
                            }
                        }
                        .disabled(isSigningIn)

                        if let error = signInError {
                            Text(error)
                                .font(.claudeCaption)
                                .foregroundStyle(Theme.danger)
                        }
                    }
                } header: {
                    sectionHeader("Account")
                } footer: {
                    Text(appState.isSignedIn
                         ? "Sessions are tied to your Google account."
                         : "Sign in to access your sessions across devices.")
                        .font(.claudeFootnote)
                        .foregroundStyle(Theme.textMuted)
                }
                .listRowBackground(Theme.bgRaised)

                Section {
                    Button {
                        Task {
                            testPushResult = nil
                            do {
                                try await appState.client.sendTestPush()
                                testPushResult = "Sent: check your notifications"
                            } catch {
                                testPushResult = "Couldn't send. Allow notifications for MClaude, then try again."
                            }
                        }
                    } label: {
                        Text("Send test notification")
                            .foregroundStyle(Theme.clay)
                    }
                    if let testPushResult {
                        Text(testPushResult)
                            .font(.claudeCaption)
                            .foregroundStyle(Theme.textMuted)
                    }
                } header: {
                    sectionHeader("Notifications")
                } footer: {
                    Text("MClaude notifies you when a turn that ran 5 minutes or longer finishes, with Claude's last reply.")
                        .font(.claudeFootnote)
                        .foregroundStyle(Theme.textMuted)
                }
                .listRowBackground(Theme.bgRaised)

                if !mcpForbidden {
                    Section {
                        NavigationLink {
                            McpSettingsList(onOpenSession: openSession)
                        } label: {
                            Label {
                                Text("MCP servers")
                            } icon: {
                                Image(systemName: "puzzlepiece.extension")
                                    .foregroundStyle(Theme.textSecondary)
                            }
                        }
                    }
                    .listRowBackground(Theme.bgRaised)
                }

                Section {
                    HStack {
                        Text("MClaude")
                        Spacer()
                        Text("v\(Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "?") (\(Bundle.main.infoDictionary?["CFBundleVersion"] as? String ?? "?"))")
                            .foregroundStyle(Theme.textMuted)
                    }
                } header: {
                    sectionHeader("About")
                }
                .listRowBackground(Theme.bgRaised)
            }
            .font(.claudeUI)
            .foregroundStyle(Theme.textPrimary)
            .scrollContentBackground(.hidden)
            .background(Theme.bgPage)
            .navigationTitle("Settings")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                        .fontWeight(.semibold)
                        .foregroundStyle(Theme.clay)
                }
            }
        }
        .tint(Theme.clay)
        .presentationBackground(Theme.bgPage)
        .task {
            do {
                _ = try await appState.client.fetchMcp()
            } catch {
                if McpText.status(of: error) == 403 { mcpForbidden = true }
            }
        }
    }

    /// A session row in MCP servers → detail: close Settings, then switch to that session.
    private func openSession(_ id: String) {
        dismiss()
        Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(350))
            appState.openSessionRequest = id
        }
    }

    private func sectionHeader(_ title: String) -> some View {
        Text(title)
            .font(.claudeCaptionSemibold)
            .foregroundStyle(Theme.textMuted)
    }
}
