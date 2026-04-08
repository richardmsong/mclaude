import SwiftUI

struct TerminalOutputView: View {
    let output: String
    let sessionId: String
    let onLoadMore: () -> Void

    @State private var parsed = AttributedString()
    @State private var lastOutput = ""
    @State private var isLoadingMore = false
    @State private var hasAppeared = false

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                VStack(spacing: 0) {
                    // Load more trigger at the top
                    Color.clear
                        .frame(height: 1)
                        .id("top")
                        .onAppear {
                            guard hasAppeared, !isLoadingMore, !output.isEmpty else { return }
                            isLoadingMore = true
                            onLoadMore()
                            DispatchQueue.main.asyncAfter(deadline: .now() + 1) {
                                isLoadingMore = false
                            }
                        }

                    Text(parsed)
                        .font(.system(size: 11, design: .monospaced))
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(8)

                    Color.clear
                        .frame(height: 1)
                        .id("bottom")
                }
            }
            .defaultScrollAnchor(.bottom)
            .background(.black)
            .onChange(of: output) {
                parseOutput()
            }
            .onAppear {
                parseOutput()
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                    hasAppeared = true
                }
            }
        }
    }

    private func parseOutput() {
        guard output != lastOutput else { return }
        lastOutput = output
        let text = output
        Task.detached {
            let result = ANSIParser.parse(text)
            await MainActor.run {
                parsed = result
            }
        }
    }
}
