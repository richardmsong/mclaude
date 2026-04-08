import Foundation

public struct ServerConfig: Sendable {
    public let host: String
    public let port: Int
    public let token: String?

    public init(host: String = "100.90.85.91", port: Int = 8377, token: String? = nil) {
        self.host = host
        self.port = port
        self.token = token
    }

    public var baseURL: URL {
        var components = URLComponents()
        components.scheme = "http"
        components.host = host
        components.port = port
        guard let url = components.url else {
            fatalError("Invalid server config: host=\(host) port=\(port)")
        }
        return url
    }

    public func url(path: String) -> URL? {
        var components = URLComponents()
        components.scheme = "http"
        components.host = host
        components.port = port
        components.path = path
        return components.url
    }
}
