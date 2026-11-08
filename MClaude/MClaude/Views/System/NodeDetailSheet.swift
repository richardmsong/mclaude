import SwiftUI

/// A node of the system map: its state, value and reason, and its `details` as label–value rows
/// (docs/plan-observability.md → Map screen → Tapping a box). Follows the live map while open.
struct NodeDetailSheet: View {
    let node: ObsNode
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    VStack(alignment: .leading, spacing: 8) {
                        HStack(spacing: 8) {
                            Circle().fill(node.stateColor).frame(width: 9, height: 9)
                            Text(node.stateWord)
                                .font(.system(size: 22, design: .serif))
                                .foregroundStyle(node.stateColor)
                        }
                        if let value = node.value, !value.isEmpty {
                            Text(value)
                                .font(.claudeMono(13))
                                .foregroundStyle(Theme.textSecondary)
                        }
                        if let reason = node.reason, !reason.isEmpty {
                            Text(reason)
                                .font(.claudeUI)
                                .foregroundStyle(Theme.textPrimary)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .claudeCard(padding: 14)
                    .accessibilityElement(children: .combine)

                    if let details = node.details, !details.isEmpty {
                        LabelValueCard(title: "Details", rows: details.sorted { $0.key < $1.key }
                            .map { (ObsJSON.humanize($0.key), $0.value.display) })
                    }
                }
                .padding(16)
            }
            .background(Theme.bgPage)
            .navigationTitle(node.label)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                        .foregroundStyle(Theme.textSecondary)
                }
            }
        }
        .tint(Theme.clay)
        .presentationDetents([.medium, .large])
        .presentationBackground(Theme.bgPage)
    }
}

/// A raised card of label–value rows with hairline dividers.
struct LabelValueCard: View {
    let title: String
    let rows: [(String, String)]

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text(title)
                .font(.claudeCaptionSemibold)
                .foregroundStyle(Theme.textMuted)
                .padding(.bottom, 6)
            ForEach(rows.indices, id: \.self) { i in
                if i > 0 { Rectangle().fill(Theme.border).frame(height: 0.5) }
                row(rows[i].0, rows[i].1)
                    .padding(.vertical, 8)
                    .accessibilityElement(children: .combine)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .claudeCard(padding: 14)
    }

    /// Short values sit on the right; long ones go under their label.
    @ViewBuilder
    private func row(_ label: String, _ value: String) -> some View {
        let labelText = Text(label).font(.claudeUI).foregroundStyle(Theme.textSecondary)
        let valueText = Text(value).font(.claudeMono(value.count > 32 ? 12 : 13)).foregroundStyle(Theme.textPrimary)
            .textSelection(.enabled)
        if value.count > 32 {
            VStack(alignment: .leading, spacing: 4) {
                labelText
                valueText.fixedSize(horizontal: false, vertical: true)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        } else {
            HStack(alignment: .firstTextBaseline, spacing: 12) {
                labelText
                Spacer(minLength: 8)
                valueText.multilineTextAlignment(.trailing)
            }
        }
    }
}
