import SwiftUI

struct SettingsView: View {
    @Environment(AppState.self) private var appState
    @State private var isChecking = false
    @State private var connectionResult: Bool?

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
                    SecureField("Claude Code OAuth Token", text: $state.authToken)
                        .font(.system(.body, design: .monospaced))
                        .autocorrectionDisabled()
                        .textInputAutocapitalization(.never)
                } header: {
                    Text("Account")
                } footer: {
                    Text("Optional. Run `claude setup-token` to get a token from your Claude Pro/Max subscription. Leave empty to use the server owner's subscription.")
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
