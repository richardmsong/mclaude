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
- Persists JWT, nkeySeed, userId, and natsUrl to `localStorage` on login; clears on logout
- On app startup, exposes `loadFromStorage()` so the app layer can restore a session without re-authenticating
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
  restoreTokens(tokens)   // rehydrate state from stored tokens, no network call
  
  // Internal: periodic check, refresh when TTL < threshold
  startRefreshLoop(checkIntervalMs: 60000)
```

Responsibilities:
- Monitors JWT expiry, triggers refresh when TTL falls below configured threshold
- On refresh success: reconnects NATS with new JWT
- On refresh failure: sets status to `expired`, upper layers show login screen
- Threshold and expiry are read from the JWT itself (control-plane configures them)
- `restoreTokens()` is called on app startup when `AuthClient.loadFromStorage()` returns tokens; if the subsequent NATS connect fails (expired/invalid), tokens are cleared and the login screen is shown

### SessionStore

Watches NATS KV for session and project state.

```
SessionStore(natsClient, userId)
  sessions: Map<sessionId, SessionState>
  projects: Map<projectId, ProjectState>
  
  // Start watching KV buckets
  startWatching()
  
  // Derived state
  getSessionsForProject(projectId) → SessionState[]
  
  onSessionChanged(callback)
  onProjectChanged(callback)
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
  state: "idle" | "running" | "requires_action" | "plan_mode" | "restarting" | "failed" | "updating" | "unknown" | "waiting_for_input"
  stateSince: timestamp
  model: string
  capabilities: { skills: string[], tools: string[], agents: string[] }
  pendingControls: Record<string, ControlRequest>  // map of requestId → ControlRequest
  usage: { inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, costUsd }
  replayFromSeq: number | null     // JetStream seq — start replay here, not 0
}
```

Responsibilities:
- Watches `mclaude-sessions` and `mclaude-projects` KV buckets for the user's keys
- Agent health is tracked via NATS `$SYS` presence events (control-plane writes health status to project KV entries)
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
      | ThinkingBlock | ControlRequestBlock | CompactionBlock | SkillInvocationBlock

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

SkillInvocationBlock {
  type: "skill_invocation"
  skillName: string    // e.g. "feature-change", extracted from "Base directory for this skill: .../skills/<name>"
  args: string         // content after the "ARGUMENTS:" line, trimmed; empty string if absent
  rawContent: string   // full original text for expand view
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
  - `user` (human text, not tool_result) → inspect content before creating a turn:
      - If text starts with `"Base directory for this skill:"`: parse as `SkillInvocationBlock` — extract skill name from the path segment after `.../skills/` and extract args from the text after the `"ARGUMENTS:"` line (trimmed). Create a user turn with this block instead of a TextBlock.
      - If text starts with `"[SYSTEM NOTIFICATION"`: discard entirely — do not create a turn.
      - Otherwise: dedup against pending messages — primary: match by `event.uuid`; fallback: if uuid absent, match by exact text content. On match: clear `pendingUuid` on the existing optimistic turn in-place (no new turn created). If no match: create a new user `Turn` with `TextBlock`s at current position. `addPendingMessage` on send inserts an optimistic turn immediately (with `pendingUuid` set) so the message appears at the correct conversation position before Claude echoes it back.
  - `control_request` → creates `ControlRequestBlock` with status `pending`
  - Events with `parent_tool_use_id` → nested under the parent `ToolUseBlock`'s turn
- On `clear` event: resets `ConversationModel` (empty turns), updates local `replayFromSeq`
- On `compact_boundary` event: resets `ConversationModel`, adds a `CompactionBlock` with the summary
- Tracks `lastSequence` for replay on reconnect
- On reconnect: re-subscribes from `max(lastSequence + 1, replayFromSeq)`, no data loss
- On fresh load: reads `replayFromSeq` from SessionStore KV — skips events before last clear/compaction
- **Deduplication**: delivery is at-least-once — session agent may re-publish events after a NATS reconnect. Skip any event whose JetStream sequence number is ≤ `lastSequence`

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

---

## View Model Layer

Responsible for: combining store data into view-ready models, handling user actions. Still no rendering — just data + actions that the platform layer binds to.

### SessionListVM

```
SessionListVM(sessionStore, lifecycleStore)
  // View-ready data
  projects: ProjectVM[]
  
  // Actions
  createProject(name, gitUrl)
  deleteProject(projectId)
  createSession(projectId, branch, name, opts?: { extraFlags?: string })
  deleteSession(sessionId)
  restartSession(sessionId, opts?: { extraFlags?: string })
```

```
ProjectVM {
  id: string
  name: string
  status: string
  healthy: boolean       // from project KV entry (set by control-plane via $SYS presence)
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
  extraFlags: string   // raw CLI flags string, empty if none
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

## Platform Layer

The only layer that touches native APIs. Renders view models and captures user input. Each platform implements the features marked in the [Feature List](feature-list.md) — the canonical source of truth for what every client implements. Reference features by ID (e.g., C3, T1, X1).

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
**Invalidated by**: NATS `$SYS` disconnect event (control-plane marks agent offline in project KV). Recovery sequence on reconnect rewrites all KV entries from fresh Claude Code state.

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

Two cache tiers:

| Path | `Cache-Control` | Why |
|------|----------------|-----|
| `index.html` | `no-cache` | Entry point that references hashed bundles. Browser must revalidate on every load so it picks up new bundle filenames after a deploy. (Still uses `ETag`/`Last-Modified` — a 304 is fine, but a stale 200 from heuristic cache is not.) |
| `/assets/*` | `public, max-age=31536000, immutable` | Content-hashed filenames from the build tool (e.g., `index-BAOWvUvJ.js`). Safe to cache forever — a new build produces a new filename. |

**Invalidated by**: content-hash filenames (assets) and `no-cache` revalidation (HTML entry point).

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

### Development Harness

A `/implement-features` skill that audits a client codebase against the [Client Feature List](feature-list.md) and implements missing features. Designed to run as N parallel mclaude sessions — one per platform — triggered automatically when the feature list changes.

#### The Skill

```
/implement-features <platform> [--audit-only] [--category <category>]
```

Where `<platform>` is `web`, `cli`, or `ios`. The skill:

1. **Read specs** — `docs/feature-list.md` (features + support matrix) and `docs/plan-client-architecture.md` (architecture, protocol, accumulation logic, cache handling)
2. **Identify platform root** — `mclaude-web/`, `mclaude-cli/`, `mclaude-ios/` (configurable)
3. **Audit** — scan the platform codebase, classify each applicable feature as `implemented`, `partial`, or `missing`
4. **Output gap report** — ordered by dependency (A1 before P1, C1 before C2), with specific files to create/modify
5. **Pick next category** — select the next category of missing features (Auth → Project → Conversation → etc.)
6. **Implement the category** — implement features in the category, following the architecture doc
7. **Verify** — run the build (`npm run build`, `go build`, `xcodebuild`). If it breaks, fix it.
8. **Commit and push** — one commit per category, push to the branch. This is the checkpoint.
9. **Re-audit and loop** — go back to step 3. Repeat until the audit returns zero missing features, then open a PR.

#### Convergence on a branch

The feature list is a spec. The skill only moves the codebase closer to the spec — it can't move it further away (the build check and re-audit prevent regressions). So no matter how many times you run it, no matter how many sessions die and restart, the branch converges monotonically toward feature-complete.

```
Session 1: audit → Auth ✓ → Project ✓ → Conversation ✓ → [dies]
  (3 commits pushed to branch)
Session 2: audit → Auth ✓ Project ✓ Conversation ✓ → Permissions ✓ → Skills ✓ → [dies]
  (2 more commits pushed)
Session 3: audit → all above ✓ → Model ✓ → Terminal ✓ → Voice ✓ → System ✓ → audit clean → opens PR
```

No coordination between sessions. No merge step in between. Each session pulls the branch, re-audits, implements the next missing category, pushes. The branch is the accumulator.

This means greenfield is just "keep spawning sessions on this branch until the PR opens." You can do it manually (`/implement-features web` a few times) or let the GHA loop handle it. Either way, you're reviewing the final PR, not babysitting the process.

**Verification**: build check after each category. Not UI testing — "does it compile." UI correctness is validated during PR review or by Playwright tests if they exist.

#### Trigger: Feature List Changes

When `docs/feature-list.md` is updated, a GitHub Action spins up N mclaude sessions (one per platform) on a `feature-sync` branch:

```yaml
# .github/workflows/implement-features.yml
name: Implement Feature List Changes
on:
  push:
    paths: ['docs/feature-list.md']
    branches: [main]
  workflow_dispatch:
    inputs:
      platform:
        description: 'Single platform (blank = all)'
        required: false

jobs:
  implement:
    strategy:
      matrix:
        platform: [web, cli, ios]
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Run /implement-features
        run: |
          SESSION=$(curl -s -X POST "$MCLAUDE_API/sessions" \
            -H "Authorization: Bearer $MCLAUDE_TOKEN" \
            -d '{"projectId": "${{ matrix.platform }}-client", "branch": "feature-sync/${{ matrix.platform }}"}' \
            | jq -r '.sessionId')

          curl -s -X POST "$MCLAUDE_API/sessions/$SESSION/input" \
            -H "Authorization: Bearer $MCLAUDE_TOKEN" \
            -d '{"text": "/implement-features ${{ matrix.platform }}"}'

          # Wait for idle — session loops internally until audit is clean or it dies
          while true; do
            STATE=$(curl -s "$MCLAUDE_API/sessions/$SESSION" \
              -H "Authorization: Bearer $MCLAUDE_TOKEN" \
              | jq -r '.state')
            [ "$STATE" = "idle" ] && break
            sleep 30
          done

          # If session died before audit was clean, re-trigger
          # (next session picks up from last push on the branch)
          MISSING=$(curl -s "$MCLAUDE_API/sessions/$SESSION/audit" \
            -H "Authorization: Bearer $MCLAUDE_TOKEN" \
            | jq -r '.missingCount')
          if [ "$MISSING" -gt 0 ]; then
            gh workflow run implement-features.yml -f platform=${{ matrix.platform }}
          fi
```

The GHA loop is the outer retry. The skill is the inner loop. Between them, the branch converges to feature-complete without human intervention.

#### Manual Use

```
/implement-features web                    # audit + implement until clean
/implement-features web --audit-only       # just the gap report, no changes
/implement-features web --category auth    # implement only the Auth category
```

#### Why This Works

- **Monotonic convergence** — the feature list is the spec, the skill only adds what's missing, the build check prevents regressions. Each session moves the branch strictly closer to the target. Can't diverge.
- **Branch is the accumulator** — commits are pushed, not held in session state. Sessions are disposable. Die and restart as many times as needed.
- **Feature list is the contract** — same IDs, same descriptions, same support matrix for all platforms. Update one document, all platforms converge.
- **Architecture doc is the spec** — exact interfaces, protocol messages, accumulation algorithm. The agent doesn't guess.
- **Categories are natural batch boundaries** — Auth is ~4 features, Conversation is ~11. Each fits in one context window.
- **N platforms, N branches** — each platform converges independently. Review the final PR when it opens.

### Testing

Store and View Model layers are testable without any platform dependencies:
- Feed mock events into EventStore → assert ConversationModel state
- Feed mock KV updates into SessionStore → assert SessionState
- Call ConversationVM.sendMessage() → assert correct NATS publish call
- Simulate JWT expiry → assert AuthStore triggers refresh

Platform layer tested via browser automation (Playwright) or visual snapshot tests.
