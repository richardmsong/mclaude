# mclaude Client Architecture

## Principle

Every client — web SPA, mclaude-cli, future native apps — follows the same layered architecture. The business logic is identical; only the rendering and native API bindings change.

```
┌─────────────────────────────────────────────┐
│  Platform Layer (differs per client)        │
│  React/Solid components, xterm.js,          │
│  SwiftUI views, Go text REPL, etc.          │
├─────────────────────────────────────────────┤
│  View Model Layer                           │
│  Conversation, SessionList, Terminal,       │
│  SkillsPicker, PermissionPrompt, CostMeter  │
├─────────────────────────────────────────────┤
│  Store Layer                                │
│  SessionStore, EventStore, AuthStore,       │
│  HeartbeatMonitor, CapabilitiesCache        │
├─────────────────────────────────────────────┤
│  Transport Layer                            │
│  NATSClient, AuthClient                     │
└─────────────────────────────────────────────┘
```

Each layer depends only on the layer below it. The Platform Layer is the only part that touches native APIs. Everything below it is pure business logic that can be ported across languages with the same structure.

---

## Transport Layer

Responsible for: NATS connection lifecycle, HTTP auth calls, raw message send/receive.

### NATSClient

Wraps the NATS client library for the platform (`nats.ws` for browser, `nats.go` for Go, `swift-nats` for Swift, etc.).

```
NATSClient
  connect(url, jwt, nkeySeed) → Connection
  reconnect(newJwt)
  subscribe(subject, callback)
  publish(subject, data)
  request(subject, data, timeout) → reply
  kvWatch(bucket, key, callback)
  kvGet(bucket, key) → value
  onDisconnect(callback)
  onReconnect(callback)
```

Responsibilities:
- Manages connection state (connected, reconnecting, disconnected)
- Handles automatic reconnection with backoff
- Exposes raw message bytes — no parsing
- Reports connection health to upper layers

### AuthClient

HTTP client for control-plane auth endpoints.

```
AuthClient
  login(email, password) → { jwt, nkeySeed, userId }
  loginSSO(provider) → redirect URL
  refresh() → { jwt }
  logout()
```

Responsibilities:
- Stores JWT and nkeySeed (in-memory or platform secure storage)
- Decodes JWT for userId and expiry
- No refresh logic here — that's in AuthStore

---

## Store Layer

Responsible for: state management, event accumulation, protocol interpretation. Pure business logic — no rendering, no native APIs.

### AuthStore

```
AuthStore(authClient, natsClient)
  state: { userId, jwt, expiry, status: "authenticated" | "refreshing" | "expired" }
  
  login(email, password)
  loginSSO(provider)
  logout()
  
  // Internal: periodic check, refresh when TTL < threshold
  startRefreshLoop(checkIntervalMs: 60000)
```

Responsibilities:
- Monitors JWT expiry, triggers refresh when TTL falls below configured threshold
- On refresh success: reconnects NATS with new JWT
- On refresh failure: sets status to `expired`, upper layers show login screen
- Threshold and expiry are read from the JWT itself (control-plane configures them)

### SessionStore

Watches NATS KV for session and project state.

```
SessionStore(natsClient, userId)
  sessions: Map<sessionId, SessionState>
  projects: Map<projectId, ProjectState>
  heartbeats: Map<projectId, { ts, healthy }>
  
  // Start watching KV buckets
  startWatching()
  
  // Derived state
  getSessionsForProject(projectId) → SessionState[]
  isAgentHealthy(projectId) → boolean
  
  onSessionChanged(callback)
  onProjectChanged(callback)
  onHealthChanged(callback)
```

Where `SessionState` mirrors the NATS KV schema:

```
SessionState {
  id: string
  projectId: string
  branch: string
  worktree: string
  cwd: string
  name: string
  state: "idle" | "running" | "requires_action" | "restarting" | "failed"
  stateSince: timestamp
  model: string
  capabilities: { skills: string[], tools: string[], agents: string[] }
  pendingControl: ControlRequest | null
  usage: { inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, costUsd }
  replayFromSeq: number | null     // JetStream seq — start replay here, not 0
}
```

Responsibilities:
- Watches `mclaude-sessions` and `mclaude-projects` KV buckets for the user's keys
- Watches `mclaude-heartbeats` for agent health
- Marks agent as unhealthy when `now - heartbeat.ts > 60s`
- Emits change events for upper layers to react to
- No rendering — just data

### EventStore

Subscribes to JetStream event stream for a specific session. Accumulates events into a conversation model.

```
EventStore(natsClient, userId, projectId, sessionId)
  events: Event[]
  conversation: ConversationModel
  
  // Subscribe and begin accumulating
  // Reads replayFromSeq from SessionStore KV, subscribes from there (not 0)
  start(replayFromSeq?: number)
  stop()
  
  // Get last sequence for replay on reconnect
  lastSequence: number
  
  onEvent(callback)
  onConversationChanged(callback)
```

Where `ConversationModel` is the accumulated, renderable conversation:

```
ConversationModel {
  turns: Turn[]
}

Turn {
  id: string
  type: "user" | "assistant" | "system"
  blocks: Block[]
  model?: string
  usage?: Usage
  parentToolUseId?: string    // non-null = subagent turn
}

Block = TextBlock | StreamingTextBlock | ToolUseBlock | ToolResultBlock
      | ThinkingBlock | ControlRequestBlock | CompactionBlock

TextBlock {
  type: "text"
  text: string
}

StreamingTextBlock {
  type: "streaming_text"
  chunks: string[]         // accumulated from stream_event deltas
  complete: boolean        // true when final assistant message arrives
}

ToolUseBlock {
  type: "tool_use"
  id: string
  name: string
  inputSummary: string
  fullInput?: string
  elapsed?: number         // from tool_progress events
  result?: ToolResultBlock // attached when tool_result arrives
}

ToolResultBlock {
  type: "tool_result"
  toolUseId: string
  content: string
  isError: boolean
}

ThinkingBlock {
  type: "thinking"
  text: string
}

ControlRequestBlock {
  type: "control_request"
  requestId: string
  toolName: string
  input: any
  status: "pending" | "approved" | "denied"
}

CompactionBlock {
  type: "compaction"
  summary: string
}
```

Responsibilities:
- Subscribes to `mclaude.{userId}.{projectId}.events.{sessionId}`
- Parses raw stream-json events into typed `Event` objects
- Accumulates events into `ConversationModel`:
  - `stream_event` deltas → appended to current `StreamingTextBlock.chunks`
  - `assistant` message → finalizes `StreamingTextBlock` (sets `complete: true`, replaces chunks with final text)
  - `tool_use` → creates `ToolUseBlock`
  - `tool_progress` → updates `ToolUseBlock.elapsed`
  - `tool_result` → attaches to matching `ToolUseBlock` by toolUseId
  - `control_request` → creates `ControlRequestBlock` with status `pending`
  - Events with `parent_tool_use_id` → nested under the parent `ToolUseBlock`'s turn
- On `clear` event: resets `ConversationModel` (empty turns), updates local `replayFromSeq`
- On `compact_boundary` event: resets `ConversationModel`, adds a `CompactionBlock` with the summary
- Tracks `lastSequence` for replay on reconnect
- On reconnect: re-subscribes from `max(lastSequence + 1, replayFromSeq)`, no data loss
- On fresh load: reads `replayFromSeq` from SessionStore KV — skips events before last clear/compaction

### LifecycleStore

Subscribes to lifecycle events for all sessions in a project.

```
LifecycleStore(natsClient, userId, projectId)
  start()
  stop()
  
  onLifecycleEvent(callback)
```

Responsibilities:
- Subscribes to `mclaude.{userId}.{projectId}.lifecycle.>`
- Forwards lifecycle events (session_created, session_stopped, session_restarting, etc.)
- SessionStore uses these to supplement KV watches (faster notification than KV propagation)

### HeartbeatMonitor

Checks agent health from NATS KV heartbeats.

```
HeartbeatMonitor(natsClient, userId)
  health: Map<projectId, { ts, healthy }>
  
  start(checkIntervalMs: 5000)
  stop()
  
  isHealthy(projectId) → boolean
  onHealthChanged(callback)
```

Responsibilities:
- Watches `mclaude-heartbeats` KV bucket
- Every check interval, evaluates `now - ts > threshold` for each project
- Emits health change events (healthy → unhealthy, unhealthy → healthy)
- Threshold is configurable (default 60s)

---

## View Model Layer

Responsible for: combining store data into view-ready models, handling user actions. Still no rendering — just data + actions that the platform layer binds to.

### SessionListVM

```
SessionListVM(sessionStore, lifecycleStore, heartbeatMonitor)
  // View-ready data
  projects: ProjectVM[]
  
  // Actions
  createProject(name, gitUrl)
  deleteProject(projectId)
  createSession(projectId, branch, name)
  deleteSession(sessionId)
```

```
ProjectVM {
  id: string
  name: string
  status: string
  healthy: boolean       // from heartbeat monitor
  sessions: SessionVM[]
}

SessionVM {
  id: string
  name: string
  state: string
  model: string
  branch: string
  costUsd: number
  hasPendingPermission: boolean
}
```

### ConversationVM

```
ConversationVM(eventStore, sessionStore, natsClient)
  // View-ready data
  turns: TurnVM[]
  state: "idle" | "running" | "requires_action"
  model: string
  skills: string[]
  usage: Usage
  
  // Actions
  sendMessage(text: string)
  sendMessageWithImage(text: string, imageBase64: string, mimeType: string)
  approvePermission(requestId: string)
  denyPermission(requestId: string)
  interrupt()
  switchModel(model: string)
  invokeSkill(skillName: string, args?: string)
  reloadPlugins()
```

```
TurnVM {
  id: string
  type: "user" | "assistant" | "system"
  blocks: BlockVM[]
  isSubagent: boolean
  subagentDescription?: string
  collapsed: boolean           // UI state: subagent turns default collapsed
}
```

Responsibilities:
- Maps `ConversationModel` turns/blocks to `TurnVM`/`BlockVM` with UI-relevant metadata
- `sendMessage` → publishes `{"type": "user", "message": {...}}` to `.api.sessions.input`
- `approvePermission` → publishes `{"type": "control_response", ...}` to `.api.sessions.control`
- `interrupt` → publishes interrupt control request
- Tracks streaming state (is Claude currently typing?)
- Manages subagent nesting/collapsing

### TerminalVM

```
TerminalVM(natsClient, userId, projectId)
  terminals: TerminalInstance[]
  
  // Actions
  createTerminal(cwd?: string) → terminalId
  deleteTerminal(terminalId)
  sendInput(terminalId, data: Uint8Array)
  resize(terminalId, rows, cols)
  
  // Events
  onOutput(terminalId, callback: (data: Uint8Array) => void)
```

Responsibilities:
- Creates terminal sessions via `.api.terminal.create`
- Subscribes to `.terminal.{termId}.output` for raw PTY bytes
- Publishes to `.terminal.{termId}.input` for keyboard input
- Publishes resize events
- No rendering — the platform layer (xterm.js, or a Go terminal lib) handles display

### PermissionPromptVM

```
PermissionPromptVM(conversationVM)
  // View-ready data
  pending: PendingPermission | null
  
  // Actions
  approve()
  deny()
```

```
PendingPermission {
  requestId: string
  toolName: string
  inputSummary: string
  fullInput?: string
}
```

Extracted from ConversationVM for platforms that show permission prompts as system-level notifications (mobile push, desktop notification) rather than inline.

### SkillsPickerVM

```
SkillsPickerVM(sessionStore, conversationVM)
  skills: string[]
  
  // Actions
  invoke(skillName: string, args?: string)
  refresh()
```

Responsibilities:
- Reads skills from SessionStore capabilities (from KV)
- `invoke` → calls `conversationVM.invokeSkill()`
- `refresh` → sends `reload_plugins` control request, SessionStore updates from KV

---

## Feature List

Canonical list of features every client implements. Platform-specific docs should say "implement the feature list" rather than re-describing behavior. Each platform marks features as supported, not supported, or deferred.

Future: Figma designs linked per feature.

### Auth

| # | Feature | Description |
|---|---------|-------------|
| A1 | Login | Email/password or SSO login flow |
| A2 | Session persistence | Secure JWT + nkeySeed storage, auto-reconnect on app reopen |
| A3 | Token refresh | Background JWT refresh before expiry, graceful re-auth on failure |
| A4 | Logout | Clear credentials, disconnect NATS |

### Project & Session Management

| # | Feature | Description |
|---|---------|-------------|
| P1 | Project list | List projects with health status (from heartbeat monitor) |
| P2 | Session list | List sessions per project with state, model, branch, cost |
| P3 | Create session | Specify project, branch, optional name |
| P4 | Delete session | Stop Claude process, clean up worktree |
| P5 | Session state indicator | Live idle/running/requires_action/restarting/failed badge |
| P6 | Agent health banner | Show warning when heartbeat is stale (agent down) |

### Conversation

| # | Feature | Description |
|---|---------|-------------|
| C1 | Send message | Text input, submit to session |
| C2 | Streaming text | Live token-by-token rendering from `stream_event` deltas |
| C3 | Markdown rendering | Render assistant text as formatted markdown |
| C4 | Syntax highlighting | Code blocks in assistant text and tool results |
| C5 | Tool use display | Show tool name, input summary, elapsed time, result |
| C6 | Tool result display | Render tool output, collapsed for large results, expand-on-click |
| C7 | Thinking blocks | Show/hide extended thinking content |
| C8 | Subagent nesting | Nest subagent turns under parent Agent tool block, default collapsed |
| C9 | Compaction indicator | Visual divider when context is compacted |
| C10 | Image upload | Send images (base64) in user messages, clipboard paste |
| C11 | Interrupt | Stop Claude mid-turn |

### Permissions

| # | Feature | Description |
|---|---------|-------------|
| R1 | Permission prompt | Show tool name, input, approve/deny buttons on `control_request` |
| R2 | Permission notification | System-level notification when permission is needed (mobile push, desktop notification) |

### Skills

| # | Feature | Description |
|---|---------|-------------|
| S1 | Skills picker | List available skills from capabilities, invoke by name |
| S2 | Skills refresh | `reload_plugins` to pick up mid-session changes |

### Model & Cost

| # | Feature | Description |
|---|---------|-------------|
| M1 | Model switcher | Change model mid-session via `set_model` control request |
| M2 | Effort switcher | Change thinking budget via `set_max_thinking_tokens` |
| M3 | Cost display | Per-session token usage and cost (input, output, cache read/write) |
| M4 | Context meter | Context window utilization from `get_context_usage` |

### Terminal

| # | Feature | Description |
|---|---------|-------------|
| T1 | Terminal session | Open interactive PTY shell alongside Claude sessions |
| T2 | Terminal resize | Sync terminal dimensions on window resize |
| T3 | Multiple terminals | Manage multiple terminal tabs per project |

### Voice

| # | Feature | Description |
|---|---------|-------------|
| V1 | Push-to-talk | Voice input via platform speech recognition API |

### System

| # | Feature | Description |
|---|---------|-------------|
| X1 | Cache reset | Button to clear all client-side caches (ConversationModel, capabilities, session state) and re-subscribe from scratch |
| X2 | Reconnection | Auto-reconnect on network loss, JWT expiry, tab foreground — gap-free event replay |
| X3 | Background reconnect | Mobile: reconnect on `visibilitychange`, re-watch KV |

### Platform Support Matrix

|   | Web SPA | mclaude-cli | iOS (future) |
|---|---------|-------------|--------------|
| A1–A4 | All | All | All |
| P1–P6 | All | P2, P5 only (single-session focus) | All |
| C1–C11 | All | C1, C5, C6, C11 (text-only) | All |
| R1 | Inline | `y/n` prompt | Inline + push notification (R2) |
| R2 | Desktop notification | — | Push notification |
| S1–S2 | All | S1 (numbered list) | All |
| M1–M4 | All | M1 only | All |
| T1–T3 | All (xterm.js) | — (`kubectl exec`) | All (SwiftTerm) |
| V1 | WebSpeech API | — | SFSpeechRecognizer |
| X1–X3 | All | X2 only | All |

---

## Platform Layer

The only layer that touches native APIs. Renders view models and captures user input. Each platform implements the features marked in the support matrix above.

### Web SPA (React / Solid / Svelte)

Implements all features. Markdown via marked/remark, syntax highlighting via highlight.js/Shiki, terminal via xterm.js, voice via WebSpeech API.

### mclaude-cli (Go)

Text REPL. Implements the conversation and permission features in text mode. ~300 lines total. No terminal sessions (use `kubectl exec`), no voice, no project management.

### Future: Native iOS (SwiftUI)

Implements all features. Terminal via SwiftTerm, voice via SFSpeechRecognizer + AVAudioEngine.

---

## Protocol Contract

Every client, regardless of platform, implements the same protocol.

### NATS Subjects (subscribe)

```
mclaude.{userId}.{projectId}.events.{sessionId}       → stream-json events (JetStream)
mclaude.{userId}.{projectId}.lifecycle.{sessionId}     → lifecycle events (JetStream)
mclaude.{userId}.{projectId}.terminal.{termId}.output  → PTY output bytes (core NATS)
```

### NATS Subjects (publish)

```
mclaude.{userId}.{projectId}.api.sessions.create       → request/reply
mclaude.{userId}.{projectId}.api.sessions.delete       → request/reply
mclaude.{userId}.{projectId}.api.sessions.input        → fire-and-forget
mclaude.{userId}.{projectId}.api.sessions.control      → fire-and-forget
mclaude.{userId}.{projectId}.api.sessions.restart      → request/reply
mclaude.{userId}.{projectId}.api.terminal.create       → request/reply
mclaude.{userId}.{projectId}.api.terminal.delete       → request/reply
mclaude.{userId}.{projectId}.api.terminal.resize       → fire-and-forget
mclaude.{userId}.{projectId}.terminal.{termId}.input   → fire-and-forget
```

### NATS KV (watch)

```
mclaude-sessions:  {userId}/{projectId}/{sessionId}    → SessionState JSON
mclaude-projects:  {userId}/{projectId}                → ProjectState JSON
mclaude-heartbeats: {userId}/{projectId}               → { ts: ISO8601 }
```

### Stream-JSON Event Types (parse)

Every client must handle these event types from the events subject:

| Event type | Subtype | Client action |
|-----------|---------|--------------|
| `system` | `init` | Cache capabilities, update session model/tools |
| `system` | `session_state_changed` | Update state indicator (also comes via KV) |
| `stream_event` | `content_block_delta` | Append to streaming text (live typing) |
| `assistant` | — | Finalize text, extract tool_use blocks |
| `user` | — | Show user message |
| `control_request` | `can_use_tool` | Show permission prompt |
| `tool_progress` | — | Update elapsed time on running tool |
| `result` | — | Turn complete, accumulate usage |

Events the client may ignore (but should not break on):

| Event type | Notes |
|-----------|-------|
| `keep_alive` | Connection health, no UI action |
| `system` with other subtypes | `api_retry`, `hook_started`, `compact_boundary`, etc. |

### Input Message Formats (publish)

**User message:**
```json
{"type": "user", "message": {"role": "user", "content": "fix the bug"}, "session_id": "", "parent_tool_use_id": null}
```

**User message with image:**
```json
{"type": "user", "message": {"role": "user", "content": [{"type": "text", "text": "What's in this?"}, {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "..."}}]}, "session_id": "", "parent_tool_use_id": null}
```

**Skill invocation:**
```json
{"type": "user", "message": {"role": "user", "content": "/commit -m 'Fix bug'"}, "session_id": "", "parent_tool_use_id": null}
```

**Permission approval:**
```json
{"type": "control_response", "response": {"subtype": "success", "request_id": "abc", "response": {"behavior": "allow"}}}
```

**Permission denial:**
```json
{"type": "control_response", "response": {"subtype": "success", "request_id": "abc", "response": {"behavior": "deny"}}}
```

**Interrupt:**
```json
{"type": "control_request", "request": {"subtype": "interrupt"}}
```

**Model switch:**
```json
{"type": "control_request", "request": {"subtype": "set_model", "model": "claude-opus-4-6"}}
```

**Reload plugins (refresh skills):**
```json
{"type": "control_request", "request": {"subtype": "reload_plugins"}}
```

---

## Reconnection Strategy

All clients implement the same reconnection logic:

```
1. NATS disconnects (network, JWT expiry, tab backgrounded)
2. Transport layer: auto-reconnect with backoff
3. AuthStore: check JWT expiry
   a. If expired: call refresh() → reconnect with new JWT
   b. If refresh fails: status = expired → show login
4. EventStore: re-subscribe from max(lastSequence + 1, replayFromSeq) (JetStream replay)
5. SessionStore: re-watch KV (catches any missed state changes)
6. HeartbeatMonitor: resume health checks
7. TerminalVM: terminal sessions are dead — prompt user to reopen
   (PTY sessions are ephemeral, no replay)
```

Mobile browser specific (iOS Safari kills WebSocket on background):
```js
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState !== 'visible') return;
  natsClient.reconnect();
});
```

---

## Conversation Model Accumulation

The core logic every client implements for building the conversation from events:

```
on event:
  switch event.type:
    case "clear":
      → reset ConversationModel (turns = [])
      → update replayFromSeq to this event's JetStream sequence
    
    case "compact_boundary":
      → reset ConversationModel (turns = [])
      → add CompactionBlock with summary text
      → update replayFromSeq to this event's JetStream sequence
    
    case "system" where subtype == "init":
      → cache capabilities (skills, tools, agents, model)
    
    case "system" where subtype == "session_state_changed":
      → update conversation state
    
    case "stream_event":
      → if no current StreamingTextBlock: create one
      → append delta to StreamingTextBlock.chunks
    
    case "assistant":
      → finalize any StreamingTextBlock (complete = true)
      → for each content block:
          "text" → TextBlock
          "thinking" → ThinkingBlock
          "tool_use" → ToolUseBlock (pending result)
    
    case "user" where content contains tool_result:
      → find matching ToolUseBlock by toolUseId
      → attach ToolResultBlock to it
    
    case "user" where content is text:
      → UserTurn with TextBlock
    
    case "control_request" where subtype == "can_use_tool":
      → create ControlRequestBlock (status: pending)
      → also set on ConversationVM.pendingPermission
    
    case "tool_progress":
      → find matching ToolUseBlock by tool_use_id
      → update elapsed time
    
    case "result":
      → turn complete, accumulate usage stats

  if event.parent_tool_use_id != null:
    → nest under parent ToolUseBlock's agent turn
  else:
    → top-level turn
```

This logic is identical across all clients. The platform layer just renders the resulting `ConversationModel`.

---

## Cache Handling

Several caches exist in the system. Each has different staleness characteristics and invalidation mechanisms.

### NATS KV (server-side materialized state)

Session state, capabilities, usage, pending control requests. Write-through — the session agent updates KV on every relevant event, so KV is always current while the agent is alive.

**Goes stale when**: session agent crashes (KV retains last written value).
**Invalidated by**: heartbeat staleness detection (>60s without heartbeat). Recovery sequence rewrites all KV entries from fresh Claude Code state.

### ConversationModel (client-side in-memory accumulation)

Built by replaying JetStream events from `replayFromSeq` and accumulating into typed turns/blocks.

**Goes stale when**: NATS disconnects (network, JWT expiry, tab backgrounded on mobile).
**Invalidated by**: re-subscribe from `max(lastSequence + 1, replayFromSeq)` on reconnect. JetStream guarantees gap-free replay.

**Reset events**:
- **`/clear`**: user resets the conversation. Session agent publishes `clear` event, updates `replayFromSeq` in KV to the clear event's JetStream sequence. Client resets `ConversationModel` to empty.
- **Compaction**: Claude Code compacts its context. Session agent publishes `compact_boundary` event, updates `replayFromSeq`. Client resets `ConversationModel` and shows a compaction summary. Events before the boundary are still in JetStream but no longer represent the active conversation.
- **Session resume after crash**: `init` event re-emitted. Old events before the crash are still in JetStream. `replayFromSeq` is updated on resume to avoid re-rendering stale pre-crash state.

`replayFromSeq` is the key mechanism: clients read it from session KV before subscribing. On fresh load, this skips potentially thousands of irrelevant events. On reconnect mid-session, the client uses `max(lastSequence + 1, replayFromSeq)` — whichever is further ahead.

### Capabilities cache (client-side, from KV)

Skills, tools, agents, model — populated from the `capabilities` field in session KV (which the session agent populates from the `init` event).

**Goes stale when**: user installs a new MCP server, adds a custom skill, or modifies plugin config mid-session.
**Invalidated by**: `reload_plugins` control request → Claude Code re-emits capabilities → session agent updates KV → client gets KV watch notification. This is a **manual** bust — there is no automatic detection that capabilities have changed. The skills picker should expose a refresh button.

### SessionStore (client-side mirror of KV)

In-memory mirror of NATS KV, kept in sync via KV watch.

**Goes stale when**: missed KV updates during NATS disconnect.
**Invalidated by**: re-watch on reconnect. KV watch delivers the latest value immediately on (re-)subscribe — no replay needed.

### JWT

Cached in-memory with decoded expiry.

**Invalidated by**: periodic check (60s interval). When TTL falls below the configured threshold, `AuthStore` calls refresh. On failure, status becomes `expired` and upper layers show login.

### SPA static assets

Standard browser HTTP cache for JS/CSS bundles.

**Invalidated by**: content-hash filenames from the build tool (e.g., `main.a3f2b1.js`). No manual busting needed.

---

## Implementation Notes

### Web SPA

The Store and View Model layers are TypeScript modules with no framework dependency. They use a simple observable/subscription pattern (or framework-specific reactivity: Solid signals, Svelte stores, React context + useSyncExternalStore).

```
src/
  transport/
    nats-client.ts        NATSClient wrapper around nats.ws
    auth-client.ts        HTTP client for /auth endpoints
  stores/
    auth-store.ts         JWT management, refresh loop
    session-store.ts      KV watches, session/project state
    event-store.ts        JetStream subscription, conversation accumulation
    lifecycle-store.ts    Lifecycle event subscription
    heartbeat-monitor.ts  Agent health checks
  viewmodels/
    session-list-vm.ts    Project + session list
    conversation-vm.ts    Conversation view model + actions
    terminal-vm.ts        PTY session management
    skills-picker-vm.ts   Skills list + invocation
  components/             Framework-specific (React/Solid/Svelte)
    ProjectList.*
    SessionCard.*
    Conversation.*
    Turn.*
    TextBlock.*
    ToolUseBlock.*
    PermissionPrompt.*
    Terminal.*
    SkillsPicker.*
    PTTButton.*
    LoginPage.*
    HealthBanner.*
```

### mclaude-cli (Go)

Same layers, simpler implementation:

```
mclaude-cli/
  transport/
    nats.go              nats.go client wrapper
  stores/
    session.go           KV watch (single session)
    events.go            Event accumulation (simplified — text-only rendering)
  main.go               Text REPL: readline → sendMessage, permission prompt → y/n
```

### Testing

Store and View Model layers are testable without any platform dependencies:
- Feed mock events into EventStore → assert ConversationModel state
- Feed mock KV updates into SessionStore → assert SessionState
- Call ConversationVM.sendMessage() → assert correct NATS publish call
- Simulate JWT expiry → assert AuthStore triggers refresh

Platform layer tested via browser automation (Playwright) or visual snapshot tests.
