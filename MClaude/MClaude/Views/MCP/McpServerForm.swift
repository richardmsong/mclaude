import SwiftUI

enum McpFormMode {
    case add
    case edit(name: String, config: McpConfig)
}

/// "Add MCP server" / "Edit <name>": a form for user-scope stdio, http and sse servers (no JSON
/// editor). docs/plan-mcp-servers.md → Global screen → Add/Edit form.
struct McpServerForm: View {
    let mode: McpFormMode
    /// The fresh `GET /mcp` body when the server returned one (nil: reload it).
    let onSaved: (GlobalMcpList?) -> Void

    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss

    private struct ArgRow: Identifiable, Equatable { let id = UUID(); var value = "" }
    private struct PairRow: Identifiable, Equatable { let id = UUID(); var key = ""; var value = ""; var revealed = false }

    @State private var name = ""
    @State private var type = "stdio"
    // stdio
    @State private var command = ""
    @State private var args: [ArgRow] = []
    @State private var env: [PairRow] = []
    // http / sse
    @State private var url = ""
    @State private var headers: [PairRow] = []

    @State private var saving = false
    @State private var error: String?
    @State private var editMode: EditMode = .inactive

    private static let types = ["stdio", "http", "sse"]

    init(mode: McpFormMode, onSaved: @escaping (GlobalMcpList?) -> Void) {
        self.mode = mode
        self.onSaved = onSaved
        if case .edit(let name, let config) = mode {
            let t = config.type ?? "stdio"
            _name = State(initialValue: name)
            _type = State(initialValue: Self.types.contains(t) ? t : "stdio")
            _command = State(initialValue: config.command ?? "")
            _args = State(initialValue: config.args.map { ArgRow(value: $0) })
            _env = State(initialValue: config.env.keys.sorted().map { PairRow(key: $0, value: config.env[$0] ?? "") })
            _url = State(initialValue: config.url ?? "")
            _headers = State(initialValue: config.headers.keys.sorted().map { PairRow(key: $0, value: config.headers[$0] ?? "") })
        }
    }

    private var isAdd: Bool { if case .add = mode { return true } else { return false } }

    private var title: String {
        if case .edit(let name, _) = mode { return "Edit \(name)" }
        return "Add MCP server"
    }

    private var nameValid: Bool { McpText.isValidName(name) }

    private var isValid: Bool {
        if isAdd && !nameValid { return false }
        if type == "stdio" { return !command.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
        return McpText.isValidURL(url)
    }

    var body: some View {
        NavigationStack {
            Form {
                if let error {
                    Section {
                        Text(error)
                            .font(.claudeCaption)
                            .foregroundStyle(Theme.danger)
                            .textSelection(.enabled)
                    }
                    .listRowBackground(Theme.bgRaised)
                }

                if isAdd {
                    Section {
                        TextField("my-server", text: $name)
                            .font(.claudeMono(15))
                            .autocorrectionDisabled()
                            .textInputAutocapitalization(.never)
                        if !name.isEmpty && !nameValid {
                            Text("Letters, numbers, dot, dash and underscore only.")
                                .font(.claudeCaption)
                                .foregroundStyle(Theme.danger)
                        }
                    } header: { header("Name") }
                    .listRowBackground(Theme.bgRaised)
                }

                Section {
                    Picker("Type", selection: $type) {
                        ForEach(Self.types, id: \.self) { Text($0).tag($0) }
                    }
                    .pickerStyle(.segmented)
                    .listRowBackground(Color.clear)
                    .listRowInsets(EdgeInsets())
                } header: { header("Type") }

                if type == "stdio" {
                    stdioSections
                } else {
                    urlSections
                }
            }
            .environment(\.editMode, $editMode)
            .font(.claudeUI)
            .foregroundStyle(Theme.textPrimary)
            .scrollContentBackground(.hidden)
            .background(Theme.bgPage)
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                        .foregroundStyle(Theme.textSecondary)
                        .disabled(saving)
                }
                ToolbarItem(placement: .confirmationAction) {
                    if saving {
                        ProgressView()
                    } else {
                        Button("Save") { Task { await save() } }
                            .fontWeight(.semibold)
                            .foregroundStyle(isValid ? Theme.clay : Theme.textMuted)
                            .disabled(!isValid)
                    }
                }
            }
        }
        .tint(Theme.clay)
        .presentationBackground(Theme.bgPage)
        .interactiveDismissDisabled(saving)
    }

    // MARK: Sections

    @ViewBuilder
    private var stdioSections: some View {
        Section {
            TextField("npx", text: $command)
                .font(.claudeMono(15))
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)
        } header: { header("Command") }
        .listRowBackground(Theme.bgRaised)

        Section {
            ForEach($args) { $arg in
                TextField("Argument", text: $arg.value)
                    .font(.claudeMono(15))
                    .autocorrectionDisabled()
                    .textInputAutocapitalization(.never)
            }
            .onDelete { args.remove(atOffsets: $0) }
            .onMove { args.move(fromOffsets: $0, toOffset: $1) }
            addRow("Add argument") { args.append(ArgRow()) }
        } header: {
            HStack {
                header("Arguments")
                Spacer()
                if args.count > 1 {
                    // Drag handles for reordering (long-press drag works too)
                    Button(editMode.isEditing ? "Done" : "Reorder") {
                        withAnimation { editMode = editMode.isEditing ? .inactive : .active }
                    }
                    .font(.claudeCaptionSemibold)
                    .textCase(nil)
                }
            }
        }
        .listRowBackground(Theme.bgRaised)

        pairSection("Environment", rows: $env, keyPlaceholder: "KEY", addTitle: "Add variable")
    }

    @ViewBuilder
    private var urlSections: some View {
        Section {
            TextField("https://example.com/mcp", text: $url)
                .font(.claudeMono(15))
                .keyboardType(.URL)
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)
            if !url.isEmpty && !McpText.isValidURL(url) {
                Text("Enter an http:// or https:// URL.")
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.danger)
            }
        } header: { header("URL") }
        .listRowBackground(Theme.bgRaised)

        pairSection("Headers", rows: $headers, keyPlaceholder: "Header", addTitle: "Add header")
    }

    private func pairSection(_ title: String, rows: Binding<[PairRow]>, keyPlaceholder: String,
                             addTitle: String) -> some View {
        Section {
            ForEach(rows) { $row in
                HStack(spacing: 8) {
                    TextField(keyPlaceholder, text: $row.key)
                        .font(.claudeMono(14))
                        .autocorrectionDisabled()
                        .textInputAutocapitalization(.never)
                        .frame(maxWidth: 130)
                    Divider()
                    Group {
                        if row.revealed {
                            TextField("Value", text: $row.value)
                        } else {
                            SecureField("Value", text: $row.value)
                        }
                    }
                    .font(.claudeMono(14))
                    .autocorrectionDisabled()
                    .textInputAutocapitalization(.never)
                    Button {
                        row.revealed.toggle()
                    } label: {
                        Image(systemName: row.revealed ? "eye.slash" : "eye")
                            .font(.system(size: 14))
                            .foregroundStyle(Theme.textSecondary)
                            .frame(width: 28, height: 28)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel(row.revealed ? "Hide value" : "Show value")
                }
            }
            .onDelete { rows.wrappedValue.remove(atOffsets: $0) }
            addRow(addTitle) { rows.wrappedValue.append(PairRow()) }
        } header: { header(title) }
        .listRowBackground(Theme.bgRaised)
    }

    private func addRow(_ title: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Label(title, systemImage: "plus")
                .foregroundStyle(Theme.clay)
        }
    }

    private func header(_ title: String) -> some View {
        Text(title)
            .font(.claudeCaptionSemibold)
            .foregroundStyle(Theme.textMuted)
    }

    // MARK: Save

    /// Only the selected type's keys. Cleared args/env/headers go as empty values so the server
    /// removes them; keys the form doesn't know stay in the server's merge.
    private var configBody: [String: JSONValue] {
        func map(_ rows: [PairRow]) -> JSONValue {
            var out: [String: JSONValue] = [:]
            for row in rows {
                let key = row.key.trimmingCharacters(in: .whitespacesAndNewlines)
                if !key.isEmpty { out[key] = .string(row.value) }
            }
            return .object(out)
        }
        if type == "stdio" {
            return [
                "type": .string("stdio"),
                "command": .string(command.trimmingCharacters(in: .whitespacesAndNewlines)),
                "args": .array(args.filter { !$0.value.isEmpty }.map { .string($0.value) }),
                "env": map(env),
            ]
        }
        return [
            "type": .string(type),
            "url": .string(url.trimmingCharacters(in: .whitespacesAndNewlines)),
            "headers": map(headers),
        ]
    }

    private func save() async {
        guard isValid, !saving else { return }
        saving = true
        error = nil
        defer { saving = false }
        do {
            let fresh: GlobalMcpList?
            switch mode {
            case .add:
                fresh = try await appState.client.addMcpServer(name: name, config: configBody)
            case .edit(let name, _):
                fresh = try await appState.client.updateMcpServer(name: name, config: configBody)
            }
            onSaved(fresh)
            dismiss()
        } catch {
            self.error = McpText.message(for: error)
        }
    }
}
