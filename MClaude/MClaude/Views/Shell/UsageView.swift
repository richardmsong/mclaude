import Charts
import SwiftUI

/// Usage page (docs/design-claude-uplift.md → Usage page): tokens and estimated list-price cost for
/// all Claude Code use on the laptop, in calendar windows (today, this week, this month).
struct UsageView: View {
    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss

    enum Window: String, CaseIterable, Identifiable {
        case today = "Today", week = "This week", month = "This month"
        var id: String { rawValue }
    }

    private enum LoadState: Equatable { case loading, loaded, failed(String) }

    @State private var window: Window = .today
    @State private var summary: UsageSummary?
    @State private var loadState: LoadState = .loading
    @State private var selectedDay: Date?
    @State private var showAllProjects = false

    private var calendar: Calendar { Calendar.current }

    private static func dayFormatter(_ cal: Calendar) -> DateFormatter {
        let f = DateFormatter()
        f.calendar = cal
        f.timeZone = cal.timeZone
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = "yyyy-MM-dd"
        return f
    }

    /// First day of the window, in the phone's calendar.
    private func start(of window: Window, now: Date = Date()) -> Date {
        switch window {
        case .today: return calendar.startOfDay(for: now)
        case .week: return calendar.dateInterval(of: .weekOfYear, for: now)?.start ?? calendar.startOfDay(for: now)
        case .month: return calendar.dateInterval(of: .month, for: now)?.start ?? calendar.startOfDay(for: now)
        }
    }

    private var windowDays: [UsageDay] {
        guard let summary else { return [] }
        let first = Self.dayFormatter(calendar).string(from: start(of: window))
        return summary.days.filter { $0.date >= first }   // yyyy-MM-dd sorts as text
    }

    private var totals: UsageTotals { windowDays.map(\.total).reduce(UsageTotals(), +) }

    private func merged(_ key: KeyPath<UsageDay, [String: UsageTotals]>) -> [(name: String, usage: UsageTotals)] {
        var out: [String: UsageTotals] = [:]
        for day in windowDays { for (k, v) in day[keyPath: key] { out[k] = (out[k] ?? UsageTotals()) + v } }
        return out.map { ($0.key, $0.value) }
            .sorted { ($0.usage.costUSD, $0.usage.totalTokens, $1.name) > ($1.usage.costUSD, $1.usage.totalTokens, $0.name) }
    }

    /// One bar per day of the window through today (zero-usage days included).
    private var chartDays: [(date: Date, usage: UsageTotals)] {
        let f = Self.dayFormatter(calendar)
        let byDate = Dictionary(windowDays.map { ($0.date, $0.total) }, uniquingKeysWith: +)
        var out: [(Date, UsageTotals)] = []
        var day = start(of: window)
        let today = calendar.startOfDay(for: Date())
        while day <= today {
            out.append((day, byDate[f.string(from: day)] ?? UsageTotals()))
            guard let next = calendar.date(byAdding: .day, value: 1, to: day) else { break }
            day = next
        }
        return out
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    Picker("Period", selection: $window) {
                        ForEach(Window.allCases) { Text($0.rawValue).tag($0) }
                    }
                    .pickerStyle(.segmented)
                    .onChange(of: window) { _, _ in selectedDay = nil; showAllProjects = false }

                    content
                }
                .padding(16)
            }
            .refreshable { await load() }
            .background(Theme.bgPage)
            .navigationTitle("Usage")
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

    @ViewBuilder
    private var content: some View {
        switch loadState {
        case .loading where summary == nil:
            ProgressView("Loading usage…")
                .tint(Theme.textMuted)
                .foregroundStyle(Theme.textMuted)
                .frame(maxWidth: .infinity)
                .padding(.top, 60)
        case .failed(let message) where summary == nil:
            VStack(spacing: 10) {
                Text("Couldn't load usage")
                    .font(.system(size: 20, design: .serif))
                    .foregroundStyle(Theme.textPrimary)
                Text(message)
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.textMuted)
                    .multilineTextAlignment(.center)
                Button("Retry") { Task { await load() } }
                    .font(.claudeUISemibold)
                    .foregroundStyle(Theme.clay)
            }
            .frame(maxWidth: .infinity)
            .padding(.top, 60)
        default:
            if windowDays.isEmpty {
                Text("No usage in this period")
                    .font(.system(size: 20, design: .serif))
                    .foregroundStyle(Theme.textPrimary)
                    .frame(maxWidth: .infinity)
                    .padding(.top, 60)
            } else {
                totalsCard
                if window != .today { chartCard }
                listCard("By model", rows: merged(\.byModel), mono: true, limit: nil)
                listCard("By project", rows: merged(\.byProject), mono: false, limit: showAllProjects ? nil : 8)
            }
            footnote
        }
    }

    // MARK: Cards

    private var totalsCard: some View {
        let t = totals
        return VStack(alignment: .leading, spacing: 12) {
            cardTitle(window.rawValue)
            HStack(alignment: .firstTextBaseline) {
                Text(SessionInfoView.tokens(t.totalTokens))
                    .font(.system(size: 30, weight: .regular, design: .serif))
                    .foregroundStyle(Theme.textPrimary)
                Text("tokens")
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.textMuted)
                Spacer()
                VStack(alignment: .trailing, spacing: 2) {
                    Text(SessionInfoView.cost(t.costUSD))
                        .font(.system(size: 22, weight: .regular, design: .serif))
                        .foregroundStyle(Theme.textPrimary)
                    Text("list price")
                        .font(.claudeFootnote)
                        .foregroundStyle(Theme.textMuted)
                }
            }
            LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible())], spacing: 10) {
                tile("Input", t.inputTokens)
                tile("Output", t.outputTokens)
                tile("Cache write", t.cacheWriteTokens)
                tile("Cache read", t.cacheReadTokens)
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .claudeCard()
    }

    private var chartCard: some View {
        let days = chartDays
        let selected = selectedDay.flatMap { sel in days.first { calendar.isDate($0.date, inSameDayAs: sel) } }
        return VStack(alignment: .leading, spacing: 10) {
            HStack {
                cardTitle("By day")
                Spacer()
                if let selected {
                    Text("\(selected.date.formatted(.dateTime.month(.abbreviated).day())): \(SessionInfoView.cost(selected.usage.costUSD)) · \(SessionInfoView.tokens(selected.usage.totalTokens)) tokens")
                        .font(.claudeFootnote)
                        .monospacedDigit()
                        .foregroundStyle(Theme.textSecondary)
                }
            }
            Chart(days, id: \.date) { day in
                BarMark(x: .value("Day", day.date, unit: .day), y: .value("Cost", day.usage.costUSD))
                    .foregroundStyle(selected == nil || calendar.isDate(day.date, inSameDayAs: selected!.date)
                                     ? Theme.clay : Theme.clay.opacity(0.35))
                    .cornerRadius(3)
            }
            .chartXSelection(value: $selectedDay)
            .chartXAxis {
                AxisMarks(values: .stride(by: .day, count: days.count <= 10 ? 1 : 5)) { _ in
                    AxisValueLabel(format: window == .week ? .dateTime.weekday(.narrow) : .dateTime.day(),
                                   centered: true)
                        .foregroundStyle(Theme.textMuted)
                }
            }
            .chartYAxis {
                AxisMarks(position: .leading) { value in
                    AxisGridLine().foregroundStyle(Theme.border)
                    AxisValueLabel {
                        if let v = value.as(Double.self) { Text(v >= 1 ? String(format: "$%.0f", v) : String(format: "$%.2f", v)) }
                    }
                    .foregroundStyle(Theme.textMuted)
                }
            }
            .frame(height: 150)
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .claudeCard()
    }

    private func listCard(_ title: String, rows: [(name: String, usage: UsageTotals)], mono: Bool, limit: Int?) -> some View {
        let shown = limit.map { Array(rows.prefix($0)) } ?? rows
        return VStack(alignment: .leading, spacing: 10) {
            cardTitle(title)
            ForEach(shown, id: \.name) { row in
                HStack {
                    Text(mono ? SessionInfoView.shortModel(row.name) : row.name)
                        .font(mono ? .claudeMono(12) : .claudeCaption)
                        .foregroundStyle(Theme.textSecondary)
                        .lineLimit(1)
                    Spacer()
                    Text(SessionInfoView.tokens(row.usage.totalTokens))
                        .font(.claudeCaption)
                        .monospacedDigit()
                        .foregroundStyle(Theme.textPrimary)
                    Text(SessionInfoView.cost(row.usage.costUSD))
                        .font(.claudeCaption)
                        .monospacedDigit()
                        .foregroundStyle(Theme.textMuted)
                        .frame(minWidth: 56, alignment: .trailing)
                }
            }
            if let limit, rows.count > limit {
                Button("\(rows.count - limit) more") { withAnimation(.snappy) { showAllProjects = true } }
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.clay)
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .claudeCard()
    }

    private var footnote: some View {
        var text = "All Claude Code use on this laptop, including terminal sessions. Cost is estimated from tokens at each provider's list price (Bedrock or Anthropic)."
        if let unpriced = summary?.unpricedModels, !unpriced.isEmpty {
            text += " Some models have no price: \(unpriced.map(SessionInfoView.shortModel).joined(separator: ", "))."
        }
        return Text(text)
            .font(.claudeFootnote)
            .foregroundStyle(Theme.textMuted)
            .fixedSize(horizontal: false, vertical: true)
            .padding(.horizontal, 4)
    }

    private func tile(_ label: String, _ value: Int) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(SessionInfoView.tokens(value))
                .font(.system(size: 18, weight: .semibold))
                .monospacedDigit()
                .foregroundStyle(Theme.textPrimary)
            Text(label)
                .font(.claudeFootnote)
                .foregroundStyle(Theme.textMuted)
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.bgSunken, in: RoundedRectangle(cornerRadius: Theme.radiusSmall, style: .continuous))
    }

    private func cardTitle(_ title: String) -> some View {
        Text(title)
            .font(.claudeCaptionSemibold)
            .foregroundStyle(Theme.textMuted)
    }

    // MARK: Loading

    private func load() async {
        if summary == nil { loadState = .loading }
        let from = min(start(of: .week), start(of: .month))
        do {
            summary = try await appState.client.fetchUsageSummary(
                from: Self.dayFormatter(calendar).string(from: from), timeZone: calendar.timeZone)
            loadState = .loaded
        } catch {
            if case APIError.http(404) = error {
                loadState = .failed("This server doesn't support the usage page yet.")
            } else {
                loadState = .failed(error.localizedDescription)
            }
        }
    }
}
