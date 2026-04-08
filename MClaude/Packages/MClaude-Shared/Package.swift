// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "MClaude-Shared",
    platforms: [
        .iOS(.v17),
        .watchOS(.v10),
        .macOS(.v14)
    ],
    products: [
        .library(name: "MClaude-Shared", targets: ["MClaude-Shared"]),
    ],
    targets: [
        .target(name: "MClaude-Shared", path: "Sources/MClaude-Shared"),
    ]
)
