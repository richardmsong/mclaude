import SwiftUI

/// A finding the Ops dispatcher posts into the Ops session (docs/plan-observability.md → Ops
/// session → Delivery: the Finding card):
/// `<mclaude-finding id="F-12" kind="rule" rule="composer_desync" at="…" trace="…" count="1">Summary.</mclaude-finding>`
/// followed by an "Investigate this finding with the ops skill." line, which is never shown.
struct FindingNotice: Identifiable, Equatable {
    let id: String          // "F-12"
    let kind: String?       // rule / stall / crash / model …
    let rule: String?
    let name: String?
    let at: Date?
    let trace: String?
    let count: Int
    let summary: String

    /// "composer_desync" for a rule; otherwise the kind plus any rule/name ("crash · mclaude.app.exception").
    var subject: String? {
        let detail = rule ?? name
        if kind == nil || kind == "rule" { return detail ?? kind }
        return [kind, detail].compactMap { $0 }.joined(separator: " · ")
    }

    var title: String { subject.map { "Finding \(id) · \($0)" } ?? "Finding \(id)" }
}

enum FindingParser {
    private static let block = try! NSRegularExpression(
        pattern: #"<mclaude-finding(\s[^>]*)?>((?:(?!<mclaude-finding).)*?)</mclaude-finding>"#, options: [.dotMatchesLineSeparators])
    /// The whole attribute section must be `name="value"` pairs (single or double quotes).
    private static let attrSection = try! NSRegularExpression(
        pattern: #"^(?:\s+[A-Za-z_][\w.-]*\s*=\s*(?:"[^"]*"|'[^']*'))*\s*$"#)
    private static let attr = try! NSRegularExpression(
        pattern: #"([A-Za-z_][\w.-]*)\s*=\s*(?:"([^"]*)"|'([^']*)')"#)
    /// The dispatcher's trailing instruction line.
    private static let instruction = try! NSRegularExpression(
        pattern: #"^[ \t]*Investigate (?:this finding|these findings) with the ops skill\.?[ \t]*$\n?"#,
        options: [.anchorsMatchLines, .caseInsensitive])

    static func containsFinding(_ text: String) -> Bool { text.contains("<mclaude-finding") }

    /// Well-formed findings in the text plus the text left once they and the instruction line are
    /// removed. Malformed blocks are left in the remainder; no findings means "render as usual".
    static func parse(_ text: String) -> (findings: [FindingNotice], remainder: String) {
        guard containsFinding(text) else { return ([], text) }
        let ns = text as NSString
        var findings: [FindingNotice] = []
        var rest = text
        // Remove matched blocks back to front so earlier ranges stay valid.
        var good: [NSRange] = []
        for match in block.matches(in: text, range: NSRange(location: 0, length: ns.length)) {
            let attrs = match.range(at: 1).location == NSNotFound ? "" : ns.substring(with: match.range(at: 1))
            guard let f = finding(attrs: attrs, body: ns.substring(with: match.range(at: 2))) else { continue }
            findings.append(f)
            good.append(match.range)
        }
        guard !findings.isEmpty else { return ([], text) }
        for range in good.reversed() {
            rest = (rest as NSString).replacingCharacters(in: range, with: "")
        }
        rest = instruction.stringByReplacingMatches(in: rest, range: NSRange(location: 0, length: (rest as NSString).length), withTemplate: "")
        return (findings, rest.trimmingCharacters(in: .whitespacesAndNewlines))
    }

    /// The findings and remainder, or nil when the text has no well-formed finding (render as usual).
    static func cards(in text: String) -> (findings: [FindingNotice], remainder: String)? {
        let parsed = parse(text)
        return parsed.findings.isEmpty ? nil : parsed
    }

    /// One-line text for places that show the raw message (pending bubble, queue item):
    /// "▲ Finding F-12 · composer_desync: Summary." per finding, instruction line dropped.
    static func compactText(_ text: String) -> String {
        let parsed = parse(text)
        guard !parsed.findings.isEmpty else { return text }
        let lines = parsed.findings.map { "▲ \($0.title): \($0.summary)" }
        return (lines + (parsed.remainder.isEmpty ? [] : [parsed.remainder])).joined(separator: "\n")
    }

    private static func finding(attrs: String, body: String) -> FindingNotice? {
        let ans = attrs as NSString
        guard attrSection.firstMatch(in: attrs, range: NSRange(location: 0, length: ans.length)) != nil else { return nil }
        var values: [String: String] = [:]
        for m in attr.matches(in: attrs, range: NSRange(location: 0, length: ans.length)) {
            let key = ans.substring(with: m.range(at: 1)).lowercased()
            let r = m.range(at: 2).location != NSNotFound ? m.range(at: 2) : m.range(at: 3)
            let value = decode(ans.substring(with: r)).trimmingCharacters(in: .whitespacesAndNewlines)
            if !value.isEmpty, values[key] == nil { values[key] = value }
        }
        let summary = decode(body).trimmingCharacters(in: .whitespacesAndNewlines)
        guard let id = values["id"], !summary.isEmpty else { return nil }
        return FindingNotice(
            id: id,
            kind: values["kind"],
            rule: values["rule"],
            name: values["name"],
            at: values["at"].flatMap(date),
            trace: values["trace"],
            count: values["count"].flatMap { Int($0) }.map { max($0, 1) } ?? 1,
            summary: summary)
    }

    private static func date(_ s: String) -> Date? {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let d = f.date(from: s) { return d }
        f.formatOptions = [.withInternetDateTime]
        return f.date(from: s)
    }

    private static func decode(_ s: String) -> String {
        guard s.contains("&") else { return s }
        return s.replacingOccurrences(of: "&lt;", with: "<")
            .replacingOccurrences(of: "&gt;", with: ">")
            .replacingOccurrences(of: "&quot;", with: "\"")
            .replacingOccurrences(of: "&apos;", with: "'")
            .replacingOccurrences(of: "&#39;", with: "'")
            .replacingOccurrences(of: "&amp;", with: "&")
    }
}

/// Raised card for a finding: ▲ title, local time, the summary, ×N, and Open → the System map's
/// finding sheet for its trace.
struct FindingCard: View {
    let finding: FindingNotice
    @Environment(AppState.self) private var appState: AppState?
    @State private var showSheet = false

    private var timeText: String? {
        finding.at.map { $0.formatted(date: .omitted, time: .shortened) }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline, spacing: 6) {
                Text("▲")
                    .font(.claudeCaptionSemibold)
                    .foregroundStyle(Theme.danger)
                Text(finding.title)
                    .font(.claudeCaptionSemibold)
                    .foregroundStyle(Theme.textPrimary)
                    .lineLimit(2)
                Spacer(minLength: 6)
                if finding.count > 1 {
                    Text("×\(finding.count)")
                        .font(.claudeCaptionSemibold)
                        .foregroundStyle(Theme.textSecondary)
                        .monospacedDigit()
                }
                if let timeText {
                    Text(timeText)
                        .font(.claudeFootnote)
                        .foregroundStyle(Theme.textMuted)
                        .monospacedDigit()
                }
            }
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(headerLabel)

            Text(finding.summary)
                .font(.claudeCaption)
                .foregroundStyle(Theme.textSecondary)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)

            if finding.trace != nil {
                HStack {
                    Spacer(minLength: 0)
                    Button { showSheet = true } label: {
                        Text("Open")
                            .font(.claudeCaptionSemibold)
                            .foregroundStyle(Theme.clay)
                            .padding(.horizontal, 4)
                            .padding(.vertical, 2)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Open finding \(finding.id)")
                    .accessibilityHint("Shows the finding's record from the System map")
                }
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .claudeCard()
        .padding(.vertical, 3)
        .accessibilityElement(children: .contain)
        .sheet(isPresented: $showSheet) {
            if let trace = finding.trace {
                FindingSheet(traceId: trace, nodeLabel: nil, client: ObsAPIClient(host: appState?.serverHost ?? ""))
            }
        }
    }

    private var headerLabel: String {
        var parts = ["Finding \(finding.id)"]
        if let s = finding.subject { parts.append(s) }
        if finding.count > 1 { parts.append("seen \(finding.count) times") }
        if let at = finding.at { parts.append(at.formatted(date: .abbreviated, time: .shortened)) }
        return parts.joined(separator: ", ")
    }
}
