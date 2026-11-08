import XCTest
@testable import mclaude_server

final class McpConfigTests: XCTestCase {

    func testNameValidation() {
        XCTAssertTrue(McpConfig.isValidName("searxng"))
        XCTAssertTrue(McpConfig.isValidName("my_server.v2-beta"))
        XCTAssertFalse(McpConfig.isValidName(""))
        XCTAssertFalse(McpConfig.isValidName("has space"))
        XCTAssertFalse(McpConfig.isValidName("plugin:Notion:notion"))
        XCTAssertFalse(McpConfig.isValidName(String(repeating: "a", count: 65)))
    }

    func testSummary() {
        XCTAssertEqual(McpConfig.summary(["type": "stdio", "command": "npx", "args": ["-y", "mcp-searxng@2.10.0"]]),
                       "npx -y mcp-searxng@2.10.0")
        XCTAssertEqual(McpConfig.summary(["type": "http", "url": "https://mcp.notion.com/mcp"]), "https://mcp.notion.com/mcp")
        XCTAssertEqual(McpConfig.summary(nil), "")
    }

    func testEditable() {
        XCTAssertTrue(McpConfig.isEditable(["command": "node"]))       // type absent = stdio
        XCTAssertTrue(McpConfig.isEditable(["type": "sse", "url": "x"]))
        XCTAssertFalse(McpConfig.isEditable(["type": "ws", "url": "x"]))
    }

    func testMergeKeepsUnknownKeysAndRemovesCleared() {
        let old: [String: Any] = ["type": "stdio", "command": "npx", "args": ["a"], "env": ["K": "v"], "timeout": 30]
        let merged = McpConfig.merged(old: old, new: ["type": "stdio", "command": "node", "args": ["b", "c"], "env": [String: String]()])
        XCTAssertEqual(merged["command"] as? String, "node")
        XCTAssertEqual(merged["args"] as? [String], ["b", "c"])
        XCTAssertNil(merged["env"])
        XCTAssertEqual(merged["timeout"] as? Int, 30)
    }

    func testMergeSwitchingTransportDropsOtherFields() {
        let old: [String: Any] = ["type": "stdio", "command": "npx", "args": ["a"], "env": ["K": "v"]]
        let merged = McpConfig.merged(old: old, new: ["type": "http", "url": "https://x/mcp", "headers": ["Authorization": "Bearer t"]])
        XCTAssertEqual(merged["type"] as? String, "http")
        XCTAssertEqual(merged["url"] as? String, "https://x/mcp")
        XCTAssertNil(merged["command"]); XCTAssertNil(merged["args"]); XCTAssertNil(merged["env"])
    }

    func testGroup() {
        XCTAssertEqual(McpConfig.group(["scope": "user", "source": "user"]), "user")
        XCTAssertEqual(McpConfig.group(["scope": "dynamic", "source": "plugin"]), "plugin")
        XCTAssertEqual(McpConfig.group(["scope": "project"]), "project")
        XCTAssertEqual(McpConfig.group(["scope": "local"]), "project")
    }

    func testSessionServerShape() {
        let entry: [String: Any] = [
            "name": "playwright", "status": "connected", "transport": "stdio", "scope": "user", "source": "user",
            "serverInfo": ["name": "Playwright", "version": "1.65.0"],
            "config": ["type": "stdio", "command": "npx", "args": ["@playwright/mcp@latest"]],
            "tools": [["name": "browser_click"], ["name": "browser_navigate"]],
        ]
        let s = McpConfig.sessionServer(entry)
        XCTAssertEqual(s["version"] as? String, "1.65.0")
        XCTAssertEqual(s["group"] as? String, "user")
        XCTAssertEqual(s["summary"] as? String, "npx @playwright/mcp@latest")
        XCTAssertEqual(s["tools"] as? [String], ["browser_click", "browser_navigate"])
        XCTAssertNil(s["error"])
    }

    func testSortedByGroupThenName() {
        let sorted = McpConfig.sortedSessionServers([
            ["name": "plugin:Notion:notion", "status": "needs-auth", "source": "plugin", "scope": "dynamic"],
            ["name": "zeta", "status": "connected", "scope": "user"],
            ["name": "db", "status": "connected", "scope": "local"],
            ["name": "alpha", "status": "failed", "scope": "user", "error": "boom"],
        ])
        XCTAssertEqual(sorted.map { $0["name"] as? String }, ["alpha", "zeta", "db", "plugin:Notion:notion"])
        XCTAssertEqual(sorted[0]["error"] as? String, "boom")
    }

    func testGlobalServersAggregatesAndSkipsProjectAndRemoved() {
        let config: [String: [String: Any]] = ["searxng": ["command": "npx"], "gmail": ["command": "node"]]
        let reports: [(id: String, name: String, servers: [[String: Any]])] = [
            (id: "s1", name: "One", servers: [
                ["name": "searxng", "status": "connected", "scope": "user"],
                ["name": "old", "status": "connected", "scope": "user"],          // removed from config
                ["name": "db", "status": "connected", "scope": "project"],        // project scope: not global
                ["name": "plugin:Notion:notion", "status": "needs-auth", "source": "plugin"],
            ]),
            (id: "s2", name: "Two", servers: [
                ["name": "searxng", "status": "failed", "scope": "user", "error": "spawn ENOENT"],
            ]),
        ]
        let out = McpConfig.globalServers(config: config, reports: reports)
        XCTAssertEqual(out.map { $0["name"] as? String }, ["gmail", "searxng", "plugin:Notion:notion"])
        let searx = out[1]["sessions"] as? [[String: Any]] ?? []
        XCTAssertEqual(searx.count, 2)
        XCTAssertEqual(searx[1]["error"] as? String, "spawn ENOENT")
        XCTAssertEqual((out[0]["sessions"] as? [Any])?.count, 0)
        XCTAssertNil(out[2]["config"])
        XCTAssertEqual(out[2]["editable"] as? Bool, false)
    }
}
