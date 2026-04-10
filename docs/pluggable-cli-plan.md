# Pluggable CLI Backend Architecture

## Overview

Make mclaude support multiple CLI backends (Claude Code, Devin CLI, Droid CLI, Gemini CLI) through a driver/adapter pattern. Each CLI becomes a "driver" that implements a standard interface. The server already has a de-facto split (TmuxMonitor for local, K8sSessionManager for k8s). This formalizes that pattern.

## Current Coupling Points

There are 7 places where Claude Code is hardcoded:

1. **Process discovery** — `ps` scan for "claude" process name
2. **Session file loading** — `~/.claude/sessions/*.json`
3. **Status detection** — Pattern-matching Claude-specific terminal strings
4. **Prompt detection** — "?" prefix lines with numbered options
5. **JSONL parsing** — Claude's specific JSONL schema
6. **Event log path** — `~/.claude/projects/{cwd}/{sessionId}.jsonl`
7. **Session creation** — `claude --dangerously-skip-permissions`

## CLIDriver Protocol

```swift
protocol CLIDriver: Actor {
    var backend: CLIBackend { get }
    var displayName: String { get }
    var capabilities: DriverCapabilities { get }

    func claimProcess(panePid: Int) async -> Int?
    func loadSessionInfo(pid: Int) async -> DriverSessionInfo?
    func detectStatus(from paneContent: String) -> SessionStatus
    func detectPrompt(from paneContent: String) -> DetectedPrompt?
    func eventLogPath(sessionId: String, cwd: String) -> String?
    func parseEventLine(_ line: String) -> SessionEvent?
    var supportsEventStream: Bool { get }
    func launchCommand(cwd: String, token: String?) -> (String, [String], [String: String])
}
```

## DriverCapabilities

Tells the UI what features each backend supports:

```swift
struct DriverCapabilities: Codable, Sendable {
    let hasThinking: Bool
    let hasSubagents: Bool
    let hasSkills: Bool
    let hasPlanMode: Bool
    let toolIcons: [String: String]
    let thinkingLabel: String
    let idlePromptPatterns: [String]
}
```

## Data Model Changes

- Add `CLIBackend` enum: `claude_code`, `devin_cli`, `droid_cli`, `gemini_cli`, `generic`
- Add `backend: CLIBackend` field to `ClaudeSession` (optional, defaults to `claude_code`)
- Add `backend` to `SessionEvent` for UI adaptation
- Typealias `ClaudeSession = CLISession` during migration

## Implementation Phases

### Phase 1: Protocol and Model Changes
- Add `CLIBackend` enum to `Models.swift`
- Add `backend` field to `ClaudeSession`
- Define `CLIDriver` protocol and `DriverCapabilities`
- **Size**: Small

### Phase 2: Extract ClaudeCodeDriver
- Move all Claude-specific logic from `TmuxMonitor`, `JSONLParser`, `JSONLTailer` into `Drivers/ClaudeCodeDriver.swift`
- Process map, session file loading, status detection, prompt detection, JSONL parsing, event log path, launch command
- **Size**: Medium (refactoring, not rewriting)

### Phase 3: Refactor TmuxMonitor
- Accept `[CLIDriver]` array in init
- For each tmux pane, iterate drivers calling `claimProcess()` — first match wins
- Delegate `detectStatus()` and `detectPrompt()` to the claiming driver
- **Size**: Medium

### Phase 4: Refactor K8sSessionManager
- Delegate status/prompt detection to driver instead of duplicating logic
- Accept driver parameter (default: ClaudeCodeDriver)
- **Size**: Small

### Phase 5: DriverRegistry and Wiring
- Create `DriverRegistry` actor that holds registered drivers
- Update `main.swift` to register drivers and pass to TmuxMonitor
- JSONLTailer uses `driver.eventLogPath()` and `driver.parseEventLine()`
- **Size**: Small

### Phase 6: API Capabilities Endpoint
- Add `GET /capabilities` returning each backend's capabilities
- Include `backend` field in session list and WS messages
- Send capabilities on WS connect
- **Size**: Small

### Phase 7: Shared Models Update (iOS)
- Add `CLIBackend` and `DriverCapabilities` to MClaude-Shared package
- Add optional `backend` to `ClaudeSession` (backward compat)
- **Size**: Small

### Phase 8: UI Adaptation
- Tool icons driven by capabilities instead of hardcoded
- Thinking label from capabilities
- Feature toggles (subagents, skills, plan mode) per backend
- Backend badge on session rows
- **Size**: Medium

### Phase 9: Additional Drivers
- Each new CLI requires: process name, status patterns, event log format (if any), launch command
- `GenericTerminalDriver` as fallback (heuristic idle/working detection)
- **Size**: Small-medium per driver

## Dependency Graph

```
Phase 1 ──> Phase 2 ──> Phase 3 ──> Phase 5
                    └──> Phase 4 ──┘
Phase 1 ──> Phase 7
Phase 5 ──> Phase 6 ──> Phase 8
Phase 5 ──> Phase 9
```

Phases 1-5 are the critical path and can be done as one coordinated change.

## Key Design Decisions

- **CLIs without session files**: Driver returns nil from `loadSessionInfo`, server derives identity from tmux window + PID
- **CLIs without event logs**: `supportsEventStream = false`, UI shows only Terminal tab
- **Backward compat**: `backend` field optional, defaults to `claude_code` — old clients ignore it
- **GenericTerminalDriver**: Detects idle from "no output for N seconds" — works for any CLI

## Critical Files

- `mclaude-server/Sources/Models.swift` — data model changes
- `mclaude-server/Sources/TmuxMonitor.swift` — refactor to use drivers
- `mclaude-server/Sources/JSONLParser.swift` — move into driver
- `mclaude-server/Sources/JSONLTailer.swift` — accept driver for path + parsing
- `mclaude-server/Sources/main.swift` — registry wiring
- `mclaude-server/Sources/Drivers/ClaudeCodeDriver.swift` — new
- `mclaude-server/Sources/CLIDriver.swift` — new
- `mclaude-server/Sources/DriverRegistry.swift` — new
