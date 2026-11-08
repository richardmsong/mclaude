import Foundation

/// Optional hook around every API request (docs/plan-observability.md → System map → Phone).
/// The iPhone app sets `start` to record an HTTP client span; the returned `traceparent` is sent
/// as a header so mclaude-server's request span joins the same trace. Unset (the watch app,
/// tests), requests go out unchanged.
public enum NetworkObserver {
    public typealias Finish = @Sendable (_ status: Int?, _ error: Error?) -> Void
    nonisolated(unsafe) public static var start: (@Sendable (_ method: String, _ path: String) -> (traceparent: String, finish: Finish))?
}
