// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "mclaude-server",
    platforms: [
        .macOS(.v14)
    ],
    dependencies: [
        .package(url: "https://github.com/hummingbird-project/hummingbird.git", from: "2.0.0"),
        .package(url: "https://github.com/hummingbird-project/hummingbird-websocket.git", from: "2.0.0"),
        .package(url: "https://github.com/apple/swift-async-algorithms.git", exact: "1.0.4"),
    ],
    targets: [
        .executableTarget(
            name: "mclaude-server",
            dependencies: [
                .product(name: "Hummingbird", package: "hummingbird"),
                .product(name: "HummingbirdTLS", package: "hummingbird"),
                .product(name: "HummingbirdWebSocket", package: "hummingbird-websocket"),
            ],
            path: "Sources"
        ),
        .testTarget(
            name: "mclaude-server-tests",
            dependencies: ["mclaude-server"],
            path: "Tests"
        ),
    ]
)
