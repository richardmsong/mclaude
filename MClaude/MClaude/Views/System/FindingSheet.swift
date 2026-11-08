import SwiftUI

/// A rule break from the map's events strip (docs/plan-observability.md → Map screen → Finding
/// sheet): rule, time and node, the screenshot if there is one (loaded through mclaude-server's
/// /screenshots endpoint), and the record's JSON as label–value rows, raw JSON collapsed.
struct FindingSheet: View {
    let traceId: String
    let nodeLabel: String?
    let client: ObsAPIClient
    @Environment(\.dismiss) private var dismiss

    private enum LoadState { case loading, loaded(ObsFinding), failed }
    @State private var state: LoadState = .loading
    @State private var showRaw = false

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    content
                }
                .padding(16)
            }
            .background(Theme.bgPage)
            .navigationTitle("Finding")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                        .foregroundStyle(Theme.textSecondary)
                }
            }
        }
        .tint(Theme.clay)
        .presentationDetents([.large])
        .presentationBackground(Theme.bgPage)
        .task { await load() }
    }

    private func load() async {
        state = .loading
        do { state = .loaded(try await client.finding(traceId: traceId)) } catch { state = .failed }
    }

    @ViewBuilder
    private var content: some View {
        switch state {
        case .loading:
            ProgressView("Loading finding…")
                .tint(Theme.textMuted)
                .foregroundStyle(Theme.textMuted)
                .frame(maxWidth: .infinity)
                .padding(.top, 60)
        case .failed:
            VStack(spacing: 10) {
                Text("Can't reach observability (\(client.hostLabel))")
                    .font(.system(size: 20, design: .serif))
                    .foregroundStyle(Theme.textPrimary)
                    .multilineTextAlignment(.center)
                Button("Retry") { Task { await load() } }
                    .font(.claudeUISemibold)
                    .foregroundStyle(Theme.clay)
            }
            .frame(maxWidth: .infinity)
            .padding(.top, 60)
        case .loaded(let f):
            header(f)
            if let path = f.screenshot {
                RemoteImageView(path: path, contentMode: .fit)
                    .frame(maxWidth: .infinity, maxHeight: 420)
                    .clipShape(RoundedRectangle(cornerRadius: Theme.radiusCard, style: .continuous))
                    .claudeHairline(radius: Theme.radiusCard)
                    .accessibilityLabel("Screenshot at the rule break")
            }
            let attrs = f.attributes.filter { !["event.name", "rule"].contains($0.key) }
            if !attrs.isEmpty {
                LabelValueCard(title: "Attributes", rows: ObsJSON.object(attrs).summaryRows())
            }
            if let body = f.body {
                LabelValueCard(title: "Record", rows: body.summaryRows(title: "Body"))
                rawJSON(body)
            }
        }
    }

    private func header(_ f: ObsFinding) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Text("▲").foregroundStyle(Theme.danger)
                Text(f.rule)
                    .foregroundStyle(Theme.textPrimary)
            }
            .font(.system(size: 22, design: .serif))
            Text([f.time.map { $0.formatted(date: .abbreviated, time: .standard) },
                  f.node.map { nodeLabel ?? $0 } ?? nodeLabel]
                .compactMap { $0 }.joined(separator: " · "))
                .font(.claudeCaption)
                .foregroundStyle(Theme.textMuted)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .combine)
    }

    private func rawJSON(_ body: ObsJSON) -> some View {
        DisclosureGroup(isExpanded: $showRaw) {
            ScrollView(.horizontal, showsIndicators: false) {
                Text(body.prettyJSON)
                    .font(.claudeMono(11))
                    .foregroundStyle(Theme.textSecondary)
                    .textSelection(.enabled)
                    .padding(.top, 8)
            }
        } label: {
            Text("Raw JSON")
                .font(.claudeCaptionSemibold)
                .foregroundStyle(Theme.textMuted)
        }
        .claudeCard(padding: 14)
    }
}
