import SwiftUI

struct SessionRowView: View {
    let session: ClaudeSession
    @State private var tick = false

    var body: some View {
        let _ = tick  // force re-render on tick
        HStack(spacing: 12) {
            // Status indicator
            Image(systemName: session.status.icon)
                .foregroundStyle(session.status.color)
                .font(.title3)
                .frame(width: 28)

            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(session.projectName)
                        .font(.headline)

                    Spacer()

                    Text(session.status.label)
                        .font(.caption)
                        .fontWeight(.medium)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 2)
                        .background(session.status.backgroundColor)
                        .foregroundStyle(session.status.color)
                        .clipShape(Capsule())
                }

                HStack {
                    Text("W\(session.tmuxWindow)")
                        .font(.caption)
                        .foregroundStyle(.secondary)

                    if let laptop = session.laptop {
                        Text(laptop)
                            .font(.caption2)
                            .fontWeight(.medium)
                            .padding(.horizontal, 5)
                            .padding(.vertical, 1)
                            .background(.tint.opacity(0.12))
                            .foregroundStyle(.tint)
                            .clipShape(Capsule())
                    }

                    Text(session.cwd)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.head)

                    Spacer()

                    if let duration = session.statusDuration {
                        Text("\(session.status == .idle ? "idle" : session.status.label.lowercased()) \(duration)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    } else {
                        Text(session.uptime)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
        }
        .padding(.vertical, 4)
        .onReceive(Timer.publish(every: 1, on: .main, in: .common).autoconnect()) { _ in
            tick.toggle()
        }
    }
}
