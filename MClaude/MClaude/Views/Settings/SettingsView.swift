import SwiftUI

struct SettingsView: View {
    @Environment(AppState.self) private var appState
    @State private var isChecking = false
    @State private var connectionResult: Bool?
    @State private var isSigningIn = false
    @State private var signInError: String?

    var body: some View {
        @Bindable var state = appState

        NavigationStack {
            Form {
                Section("Server") {
                    HStack {
                        Text("Host")
                        Spacer()
                        TextField("IP or hostname", text: $state.serverHost)
                            .multilineTextAlignment(.trailing)
                            .font(.system(.body, design: .monospaced))
                            .autocorrectionDisabled()
                            .textInputAutocapitalization(.never)
                    }

                    HStack {
                        Text("Port")
                        Spacer()
                        TextField("Port", value: $state.serverPort, format: .number.grouping(.never))
                            .multilineTextAlignment(.trailing)
                            .font(.system(.body, design: .monospaced))
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
                                Image(systemName: result ? "checkmark.circle.fill" : "xmark.circle.fill")
                                    .foregroundStyle(result ? .green : .red)
                            }
                        }
                    }
                }

                Section {
                    if appState.isSignedIn {
                        HStack {
                            VStack(alignment: .leading, spacing: 4) {
                                Text(appState.userName.isEmpty ? "Signed In" : appState.userName)
                                    .font(.headline)
                                if !appState.userEmail.isEmpty {
                                    Text(appState.userEmail)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                }
                            }
                            Spacer()
                            Image(systemName: "checkmark.circle.fill")
                                .foregroundStyle(.green)
                        }

                        Button("Sign Out", role: .destructive) {
                            appState.signOut()
                        }
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
                                .font(.caption)
                                .foregroundStyle(.red)
                        }
                    }
                } header: {
                    Text("Account")
                } footer: {
                    Text(appState.isSignedIn
                         ? "Sessions are tied to your Google account."
                         : "Sign in to access your sessions across devices.")
                }

                Section("About") {
                    HStack {
                        Text("MClaude")
                        Spacer()
                        Text("v\(Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "?")")
                            .foregroundStyle(.secondary)
                    }
                }
            }
            .navigationTitle("Settings")
        }
    }
}
