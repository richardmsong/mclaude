# ADR: Client Architecture

**Status**: draft
**Status history**:
- 2026-04-28: draft

> Supersedes:
> - `adr-0036-client-architecture-v2.md` — folded in: four-layer architecture (Transport, Store, View Model, Platform), protocol contract aligned with typed slugs (ADR-0024) and host scoping (ADR-0035), HostStatusStore replacing HeartbeatMonitor, CLI unix-socket transport, conversation model accumulation, cache handling, reconnection strategy
> - `adr-0012-replay-user-messages.md` — folded in: `--replay-user-messages` flag, optimistic rendering with UUID-based dedup, pending message lifecycle (addPendingMessage → inline promotion), SystemMessageBlock and UserImageBlock types, synthetic message handling, text-matching fallback dedup
>
> The two ADRs above are marked `superseded` by this ADR in their status history.

## Overview

Defines the client-side architecture for every mclaude platform (web SPA, mclaude-cli, future native apps). Clients follow a four-layer architecture: Transport → Store → View Model → Platform. The business logic (Store + View Model) is identical across platforms; only the Transport and Platform layers differ. This ADR is the single source of truth for the client protocol contract — NATS subjects, KV keys, JetStream streams, event types, conversation model blocks, input message formats, reconnection strategy, cache handling, and the optimistic rendering model for user messages.

## Motivation

This ADR consolidates the client architecture into a single canonical reference. The prior documents evolved independently:

1. **Protocol staleness (from ADR-0036)**: ADR-0006 was written before ADR-0024 (typed slugs), ADR-0004 (BYOH host scoping), and ADR-0035 (unified host architecture). Every protocol artifact — subjects, KV keys, SessionState schema — was stale. ADR-0036 corrected them but existed as a separate document.

2. **User message model (from ADR-0012)**: The `--replay-user-messages` feature changed how user messages flow through the system. Claude becomes the single source of truth for user messages on the events stream, enabling optimistic rendering with UUID-based dedup, mid-turn user message positioning, and synthetic message handling. This is integral to the conversation model, not a separate concern.

3. **Block type expansion**: `SystemMessageBlock` (synthetic system notifications) and `UserImageBlock` (base64 image content in user messages) emerged in the web SPA and are now part of the canonical block type set.

4. **CLI transport**: The CLI uses unix sockets to the session agent — a deliberate divergence from the full NATS stack that works well for its use case. This is an architectural decision, not an exception.

5. **Development harness removal**: The `/implement-features` development harness described in ADR-0006 was never built and is superseded by the spec-driven-dev workflow (ADR-0026). That section is dropped entirely.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Architecture layers | Preserve Transport → Store → View Model → Platform from ADR-0006 | Proven in production. Web SPA implements all four layers faithfully. |
| CLI transport exception | CLI uses unix-socket transport to session-agent, not NATS | CLI attaches to a single running session — it doesn't need KV watches, JetStream replay, or multi-session management. Unix socket is simpler and eliminates NATS dependency for local debugging. |
| CLI feature scope | CLI is a debug-attach tool, not a full client | CLI implements conversation + permissions in text mode. No project management, no terminal sessions, no voice. Feature matrix in the Feature List (feature-list.md) is the authority. |
| Subject format | ADR-0024 typed slugs + ADR-0035 host scoping | All project-scoped subjects use `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}...`. |
| Host scoping in subjects | `.hosts.{hslug}.` inserted between user and project in ALL project-scoped subjects (pending ADR-0035 implementation) | Current code (subj.ts, pkg/subj) uses ADR-0024 form without host scope. ADR-0035 adds host scope. This ADR documents the target state. |
| KV key format | Dot-separated slugs with host scope: `{uslug}.{hslug}.{pslug}.{sslug}` | Matches spec-state-schema.md. |
| Block types | ADR-0006 blocks + SystemMessageBlock + UserImageBlock | Both exist in production code and serve real use cases. SystemMessageBlock renders synthetic system notifications; UserImageBlock renders base64 image content in user messages. |
| Event types | Align with what session-agent actually publishes (see Stream-JSON Event Types table) | ADR-0006 was aspirational; this ADR documents reality. |
| Development harness | Removed — superseded by spec-driven-dev workflow (ADR-0026) | The `/implement-features` skill and GHA automation from ADR-0006 were never built. The spec-driven-dev plugin (plan-feature → feature-change → dev-harness → implementation-evaluator) replaced them. |
| HeartbeatMonitor | Replace with `mclaude-hosts` KV watcher — standalone HostStatusStore that watches `mclaude-hosts` KV for per-host online/offline status. Replaces the old `mclaude-heartbeats` bucket watcher. | ADR-0035 removes `mclaude-heartbeats` and uses `$SYS` presence only; control-plane writes online/offline to `mclaude-hosts` KV. A standalone store keeps the same architecture pattern as other stores. |
| CapabilitiesCache | Not a standalone component — capabilities live on SessionKVState and EventStore | ADR-0006 listed it as a Store Layer component but it was never implemented as a separate class. The data comes from two sources: `capabilities` field in session KV (set by session-agent from `init` event) and `system/init` events. Both are handled by existing stores. |
| Reconnection strategy | Same algorithm as ADR-0006, plus `visibilitychange` for mobile browsers | Mobile Safari kills WebSocket on background; reconnect on foreground. |
| User message source of truth | Claude's `--replay-user-messages` echo is the single source of truth for user messages on the events stream | Remove manual `handleInput` publish. Claude echoes all user messages — including mid-turn redirects — on stdout, and the session-agent's stdout scanner publishes them to the events stream. This ensures message ordering matches Claude's actual processing order. |
| Optimistic rendering | Pending → inline promotion with UUID-based dedup | User sees message instantly (dimmed, at bottom of chat). When Claude echoes it back, the pending turn is spliced to its correct inline position. Matches claude.ai webapp behavior. |
| UUID round-trip | Top-level `uuid` field on stdin JSON, round-tripped by Claude | SPA generates uuid via `crypto.randomUUID()`, includes in NATS payload. Session-agent preserves it (only strips `session_id`). Claude echoes it back in the replay. |
| Synthetic messages | Show as SystemMessageBlock turns | Replays with `isSynthetic: true` (task notifications, coordinator messages) render as gray system-style messages for power-user visibility. |
| Mid-turn user messages | Inline where they occurred | Mid-turn user messages appear at the exact position in the conversation where Claude drained them from its queue — between tool results. |
| Multiple pending messages | Collapse pending on replay | When replays arrive, remove all matching pending messages and show replay content inline. Claude emits individual replays per uuid even when batching. |
| Text-matching fallback dedup | Retained as defensive measure for missing uuid | If Claude's echo arrives without a uuid (older Claude version), fall back to exact text comparison against pending messages. Only fires when `event.uuid` is absent — uuid matching always takes priority. |

## User Flow

No change from ADR-0006. Users interact with sessions through the platform layer (web components, CLI REPL, or future native UI). The layers below handle connection, state management, and conversation accumulation transparently.

### Sending a message (idle state)

1. User types message, presses Enter
2. Message appears immediately at bottom of chat, dimmed (pending state)
3. SPA publishes to `api.sessions.input` with generated `uuid`
4. Claude echoes the message on stdout within ~100ms (turn hasn't started yet)
5. Session-agent publishes echo to events stream
6. SPA receives the user event, matches uuid, removes pending, creates inline turn at current position
7. Pending flash is imperceptible — message appears to render instantly

### Sending a message mid-turn (Claude is busy)

1. Claude is making tool calls (running state)
2. User types "do it like this", presses Enter
3. Message appears at bottom of chat, dimmed (pending state) — below the streaming assistant content
4. SPA publishes to `api.sessions.input` with generated `uuid`
5. Claude's command queue holds the message until the next tool-call boundary
6. Between tool calls, Claude drains the queue and emits the replay on stdout
7. Session-agent publishes to events stream — the event arrives between tool result events
8. SPA receives the user event with matching uuid
9. Pending message disappears from bottom; inline user turn appears at the correct position in the conversation (between the tool results where Claude accepted it)

### Multiple messages while busy

1. User sends 3 messages quickly while Claude is working
2. Three dimmed pending messages stack at the bottom of the chat
3. Claude may batch them into one prompt, but emits individual replays per uuid
4. As each replay arrives, the matching pending message is removed and an inline turn is created
5. All three replays arrive at roughly the same time, so all pending messages disappear together

### Synthetic messages

1. A background agent completes a task, emitting a task-notification
2. Claude drains it between tool calls and emits a replay with `isSynthetic: true`
3. SPA renders it as a gray system message inline (not as a user bubble)

### Page refresh

1. User refreshes the page
2. JetStream replays all events from `replayFromSeq`
3. User replay events (with `isReplay: true`) arrive and create inline turns — no pending state needed
4. Conversation renders with user messages in their correct positions

## Component Changes

### All Clients (protocol alignment)

- NATS subjects updated to ADR-0024 + ADR-0035 form (see Protocol Contract below)
- KV keys updated to dot-separated slug form with host scope
- SessionState schema aligned with spec-state-schema.md
- Block types extended with SystemMessageBlock and UserImageBlock
- Event type handling aligned with what session-agent actually publishes

### Session-agent

**Spawn args**: Add `--replay-user-messages` to the args slice in `session.go start()`. Working directory is set via `cmd.Dir`, not a CLI flag. The full args for new sessions:

```
"--print", "--verbose",
"--output-format", "stream-json",
"--input-format", "stream-json",
"--include-partial-messages",
"--replay-user-messages",
"--session-id", {id}
```

For resume:

```
"--print", "--verbose",
"--output-format", "stream-json",
"--input-format", "stream-json",
"--include-partial-messages",
"--replay-user-messages",
"--resume", {sessionId}
```

**handleInput**: Remove the manual `js.Publish()` to the events stream. Claude's replay echo flows through the existing stdout scanner → `publish(eventSubject, lineCopy)` path. `handleInput` only strips `session_id` and writes to stdin — all other fields (including `uuid`) are preserved.

**Stdout scanner**: No changes — the existing scanner publishes all stdout lines to the events stream verbatim. Claude's user replay events are just another event type flowing through.

### mclaude-web

- Already implements the four-layer architecture faithfully
- `subj.ts` needs host-scope update when ADR-0035 lands (add `hslug` parameter to all project-scoped builders)
- HeartbeatMonitor replaced by HostStatusStore (watches `mclaude-hosts` KV per ADR-0035)
- SystemMessageBlock + UserImageBlock already implemented
- Optimistic rendering with pending message state in EventStore
- UUID-based dedup on user message echo

### mclaude-cli

- Transport: unix socket to session-agent (no change — this is the correct design for CLI)
- Accumulator: handles core event types; omits ThinkingBlock rendering, SkillInvocationBlock parsing, SystemMessageBlock, and `clear` event handling (acceptable per CLI's debug-attach scope)
- No KV watches, no JetStream replay, no session management
- No pending message model (unix socket provides at-most-once delivery; CLI sends synchronously)

## Architecture

```
┌─────────────────────────────────────────────┐
│  Platform Layer (differs per client)        │
│  React components, xterm.js (web)           │
│  Go text REPL (CLI)                         │
│  SwiftUI views (future iOS)                 │
├─────────────────────────────────────────────┤
│  View Model Layer                           │
│  SessionListVM, ConversationVM, TerminalVM, │
│  PermissionPromptVM, SkillsPickerVM         │
├─────────────────────────────────────────────┤
│  Store Layer                                │
│  SessionStore, EventStore, AuthStore,       │
│  LifecycleStore, HostStatusStore            │
├─────────────────────────────────────────────┤
│  Transport Layer                            │
│  NATSClient + AuthClient (web, iOS)         │
│  Unix socket (CLI)                          │
└─────────────────────────────────────────────┘
```

Each layer depends only on the layer below it. The Platform Layer is the only part that touches native APIs. Everything below it is pure business logic.

**CLI exception**: the CLI collapses Transport + Store into a single unix-socket reader that feeds events directly into an accumulator. It has no SessionStore, no KV watches, no AuthStore. This is deliberate — the CLI attaches to one session at a time and doesn't need the full stack.

---

## Transport Layer

### NATSClient (web, iOS)

Wraps the NATS client library for the platform (`nats.ws` for browser, `swift-nats` for Swift).

```
NATSClient
  connect(url, jwt, nkeySeed) → Connection
  reconnect(newJwt)
  subscribe(subject, callback)
  publish(subject, data)
  request(subject, data, timeout) → reply
  jsSubscribe(stream, subject, startSeq, callback) → JetStream ordered consumer
  kvWatch(bucket, key, callback)
  kvGet(bucket, key) → value
  onDisconnect(callback)
  onReconnect(callback)
  isConnected() → boolean
  close()
```

Responsibilities:
- Manages connection state (connected, reconnecting, disconnected)
- Handles automatic reconnection with backoff
- Exposes JetStream ordered consumer subscription (`jsSubscribe`) for event replay — this is the primary mechanism for conversation streaming
- Reports connection health to upper layers

### AuthClient (web, iOS)

HTTP client for control-plane auth endpoints.

```
AuthClient
  login(email, password) → { user, jwt, nkeySeed, hubUrl, hosts, projects }
  loginSSO(provider) → redirect URL
  refresh() → { jwt }
  logout()
  loadFromStorage() → tokens | null
  storeTokens(tokens)
  clearTokens()
  getMe() → UserInfo
```

Responsibilities:
- Persists JWT, nkeySeed, userSlug, and natsUrl to `localStorage` on login; clears on logout
- On app startup, exposes `loadFromStorage()` to restore a session without re-authenticating
- Decodes JWT for userSlug and expiry

### Unix Socket Client (CLI only)

```
UnixSocketClient
  connect(socketPath) → Connection   // default: /tmp/mclaude-session-{id}.sock
  read() → Event (newline-delimited JSON)
  write(message)                      // newline-delimited JSON
  close()
```

The CLI sends the same message formats (user message, control_response) as the NATS clients, just over JSONL on a unix socket instead of NATS subjects.

---

## Store Layer

### AuthStore (web, iOS)

```
AuthStore(authClient, natsClient)
  state: { userSlug, jwt, expiry, status: "unauthenticated" | "authenticated" | "refreshing" | "expired" }

  login(email, password)
  loginSSO(provider)
  logout()
  restoreTokens(tokens)

  startRefreshLoop(checkIntervalMs: 60000)
```

Responsibilities:
- Monitors JWT expiry, triggers refresh when TTL falls below threshold
- On refresh success: reconnects NATS with new JWT
- On refresh failure: sets status to `expired`, upper layers show login screen
- Extracts `userSlug` from JWT `slug` claim (per ADR-0024)

### SessionStore (web, iOS)

Watches NATS KV for session and project state.

```
SessionStore(natsClient, userSlug)
  sessions: Map<sessionKey, SessionKVState>
  projects: Map<projectKey, ProjectKVState>

  startWatching()
  getSessionsForProject(projectSlug) → SessionKVState[]
  getSessionBySlug(sessionSlug) → SessionKVState | null
  resolveSession(idOrSlug) → SessionKVState | null
  onSessionChanged(callback)
  onProjectChanged(callback)
  onSessionAdded(callback)
```

Where `SessionKVState` mirrors the NATS KV schema from `spec-state-schema.md`:

```
SessionKVState {
  id: string
  slug: string
  userSlug: string
  hostSlug: string
  projectSlug: string
  projectId: string
  branch: string
  worktree: string
  cwd: string
  name: string
  state: "idle" | "running" | "requires_action" | "updating" | "restarting" | "failed" | "plan_mode" | "waiting_for_input" | "unknown"
  stateSince: timestamp
  createdAt: timestamp
  model: string
  capabilities: { skills: string[], tools: string[], agents: string[] }
  pendingControls: Record<string, ControlRequest>
  usage: { inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, costUsd }
  replayFromSeq: number | null
  joinWorktree: boolean
}
```

KV key format: `{uslug}.{hslug}.{pslug}.{sslug}` (dot-separated, per spec-state-schema.md)

### EventStore (web, iOS; simplified accumulator in CLI)

Subscribes to JetStream event stream for a specific session. Accumulates events into a conversation model. Manages pending user messages for optimistic rendering.

```
EventStore(natsClient, userSlug, hostSlug, projectSlug, sessionSlug)
  events: Event[]
  conversation: ConversationModel
  pendingMessages: PendingMessage[]

  start(replayFromSeq?: number)
  stop()
  lastSequence: number
  addPendingMessage(uuid, content)

  onEvent(callback)
  onConversationChanged(callback)
```

Where `PendingMessage`:
```
PendingMessage {
  uuid: string
  content: string | Array<{ type: string; text?: string }>
  sentAt: number  // Date.now() for ordering
}
```

**Pending message lifecycle**:

1. `addPendingMessage(uuid, content)` — called by ConversationVM on send. Adds to `pendingMessages` AND immediately inserts an optimistic user turn into `conversation.turns` (with `pendingUuid` set, styled as dimmed/pending). When `content` is an array, the optimistic turn's `blocks` include both `TextBlock` (for `{type: 'text', ...}` entries) and `UserImageBlock` (for `{type: 'image', source: {type: 'base64', media_type, data}}` entries with `dataUrl = "data:{media_type};base64,{data}"`).

2. On user event with matching uuid (or text-matching fallback when uuid is absent): remove from `pendingMessages`, splice the optimistic turn out of `conversation.turns`, clear `pendingUuid`, and re-insert at the correct inline position — before the fresh streaming assistant turn under the same `parentToolUseId` if one exists, otherwise append at the end. Still-pending optimistic turns always remain at the tail of `conversation.turns`.

3. On `clear` or `compact_boundary` events: clear `pendingMessages = []` — no replay will arrive for in-flight pending messages after context truncation.

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
  pendingUuid?: string        // non-null = optimistic turn awaiting echo confirmation
}

Block = TextBlock | StreamingTextBlock | ToolUseBlock | ToolResultBlock
      | ThinkingBlock | ControlRequestBlock | CompactionBlock
      | SkillInvocationBlock | SystemMessageBlock | UserImageBlock

TextBlock {
  type: "text"
  text: string
}

StreamingTextBlock {
  type: "streaming_text"
  chunks: string[]
  complete: boolean
}

ToolUseBlock {
  type: "tool_use"
  id: string
  name: string
  inputSummary: string
  fullInput?: string
  elapsed?: number
  result?: ToolResultBlock
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
  skillName: string
  args: string
  rawContent: string
}

SystemMessageBlock {
  type: "system_message"
  text: string
}

UserImageBlock {
  type: "user_image"
  mimeType: string     // e.g. "image/png"
  dataUrl: string      // full data URL: "data:{mimeType};base64,{data}"
}
```

### LifecycleStore (web, iOS)

```
LifecycleStore(natsClient, userSlug, hostSlug, projectSlug)
  start()
  stop()
  onLifecycleEvent(callback)
```

Subscribes to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.>`.

### HostStatusStore (web; replaces HeartbeatMonitor)

```
HostStatusStore(natsClient, userSlug)
  hosts: Map<hostSlug, HostStatus>

  startWatching()
  isOnline(hostSlug) → boolean
  onHostStatusChanged(callback)
```

Where `HostStatus`:
```
HostStatus {
  slug: string
  name: string
  type: "machine" | "cluster"
  role: "owner" | "user"
  online: boolean
  lastSeenAt: timestamp
}
```

Watches `mclaude-hosts` KV bucket with key pattern `{uslug}.*`. Control-plane writes online/offline based on `$SYS.ACCOUNT.*.CONNECT`/`DISCONNECT` events (per ADR-0035). Replaces the old HeartbeatMonitor that watched `mclaude-heartbeats` — that bucket no longer exists.

---

## View Model Layer

### SessionListVM

```
SessionListVM(sessionStore, hostStatusStore, natsClient, userSlug)
  projects: ProjectVM[]

  createProject(name, hostSlug, gitUrl, gitIdentityId?)
  createSession(projectSlug, branch, name, opts?)
  deleteSession(sessionSlug)
  restartSession(sessionSlug, opts?)
```

### ConversationVM

```
ConversationVM(eventStore, sessionStore, natsClient)
  turns: TurnVM[]
  pendingMessages: PendingMessage[]
  state: "idle" | "running" | "requires_action"
  model: string
  skills: string[]
  usage: Usage

  sendMessage(text)
  sendMessageWithImage(text, imageBase64, mimeType)
  approvePermission(requestId)
  denyPermission(requestId)
  interrupt()
  switchModel(model)
  switchEffort(budget)                 // set_max_thinking_tokens with budget field
  invokeSkill(skillName, args?)
  reloadPlugins()
```

**sendMessage(text)**:
1. Generate uuid (`crypto.randomUUID()`)
2. Call `eventStore.addPendingMessage(uuid, text)` — optimistic turn appears immediately
3. Publish to NATS with uuid: `{"type": "user", "session_id": "...", "uuid": "<uuid>", "message": {"role": "user", "content": "<text>"}}`
4. No `sending` guard — NATS publish is fire-and-forget and the send button stays enabled so users can queue multiple messages

**sendMessageWithImage(text, imageBase64, mimeType)**:
Same pattern — generate uuid, add pending (with array content producing TextBlock + UserImageBlock), publish with uuid.

**ConversationVMState** exposes pending messages:
```
ConversationVMState {
  turns: ConversationModel['turns']
  pendingMessages: PendingMessage[]
  state: SessionState
  model: string
  skills: string[]
  isStreaming: boolean
}
```

### TerminalVM

```
TerminalVM(natsClient, userSlug, hostSlug, projectSlug)
  terminals: TerminalInstance[]

  createTerminal(cwd?) → terminalId
  deleteTerminal(terminalId)
  sendInput(terminalId, data)
  resize(terminalId, rows, cols)
  onOutput(terminalId, callback)
```

### PermissionPromptVM

```
PermissionPromptVM(conversationVM)
  pending: PendingPermission | null
  allPending: PendingPermission[]     // multiple pending supported

  approve()
  deny()
```

Desktop Notification API integration: when `control_request` arrives and document is not focused, fire a desktop notification.

### SkillsPickerVM

```
SkillsPickerVM(sessionStore, conversationVM)
  skills: string[]

  invoke(skillName, args?)
  refresh()                           // sends reload_plugins control request
```

---

## Protocol Contract

### NATS Subjects (subscribe)

```
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events.{sslug}       → stream-json events (JetStream via MCLAUDE_EVENTS)
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events._api          → error/API events (JetStream via MCLAUDE_EVENTS)
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.>           → lifecycle events (JetStream via MCLAUDE_LIFECYCLE)
```

### NATS Subjects (publish)

```
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.create  → request/reply
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.delete  → request/reply
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.restart → request/reply
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.resume  → request/reply
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.input   → fire-and-forget (via MCLAUDE_API stream)
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.control → fire-and-forget (via MCLAUDE_API stream)
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.*       → terminal I/O
mclaude.users.{uslug}.hosts.{hslug}.api.projects.create                     → request/reply (host-scoped per ADR-0035; current code still uses user-level form pending migration)
```

### NATS KV (watch)

```
mclaude-sessions:  {uslug}.{hslug}.{pslug}.{sslug}  → SessionState JSON
mclaude-projects:  {uslug}.{hslug}.{pslug}           → ProjectState JSON
mclaude-hosts:     {uslug}.{hslug}                    → HostState JSON
```

### Stream-JSON Event Types (parse)

Every client must handle these event types from the events subject:

| Event type | Subtype | Client action |
|-----------|---------|--------------|
| `system` | `init` | Cache capabilities (skills, tools, agents, model) |
| `system` | `session_state_changed` | Update state indicator |
| `system` | `compact_boundary` | Reset ConversationModel, add CompactionBlock with summary, clear pendingMessages |
| `stream_event` | `content_block_delta` | Append to StreamingTextBlock (live typing) |
| `assistant` | — | Finalize StreamingTextBlock, extract tool_use blocks, create ThinkingBlocks |
| `user` | (text, not tool_result) | Apply user-message parsing rules (pending dedup, synthetic detection, inline positioning — see below) |
| `user` | (tool_result) | Attach ToolResultBlock to matching ToolUseBlock by toolUseId |
| `control_request` | `can_use_tool` | Create ControlRequestBlock, show permission prompt |
| `tool_progress` | — | Update ToolUseBlock elapsed time |
| `result` | — | Turn complete, accumulate usage stats |
| `clear` | — | Reset ConversationModel (empty turns), clear pendingMessages, update replayFromSeq |

Events the client may ignore (but must not break on):

| Event type | Notes |
|-----------|-------|
| `keep_alive` | Connection health, no UI action |
| `system` with subtypes: `api_retry`, `hook_started`, etc. | Log or ignore |

### User-Message Parsing Rules

When a `user` event arrives with text content (not tool_result):

1. If event content is a tool_result: attach to matching ToolUseBlock and return early (existing behavior, unchanged). Tool results are auto-generated by Claude and never carry a user-generated `uuid`, so no pending matching is needed.
2. If `isSynthetic` flag is set: create a `SystemMessageBlock` turn (type `"system"`, system notification injected by the platform). Do not show as a user message.
3. If text starts with `"Base directory for this skill:"`: parse as `SkillInvocationBlock` — extract skill name from path segment after `skills/`, extract args from text after `"ARGUMENTS:"` line.
4. If text starts with `"[SYSTEM NOTIFICATION"`: discard entirely — do not create a turn.
5. Otherwise: dedup against pending messages — match by `event.uuid` (primary) or exact text content (fallback when `event.uuid` is absent). On match: remove from `pendingMessages`, splice the optimistic turn out, clear `pendingUuid`, re-insert at correct inline position (before the fresh streaming assistant turn under the same `parentToolUseId` if one exists, otherwise append). No match: create a new user Turn with TextBlock(s) and/or UserImageBlock(s).

For user messages containing image content blocks: create `UserImageBlock` with `mediaType` and base64 `data` (as `dataUrl = "data:{mediaType};base64,{data}"`).

### Claude Stdout Shape with `--replay-user-messages`

When this flag is enabled, Claude emits user messages on stdout as NDJSON lines:

```json
{"type":"user","message":{"role":"user","content":"fix the bug"},"session_id":"...","parent_tool_use_id":null,"uuid":"550e8400-...","isReplay":true}
```

For mid-turn queued messages drained between tool calls:

```json
{"type":"user","message":{"role":"user","content":"do it like this"},"session_id":"...","parent_tool_use_id":null,"uuid":"abc-123","timestamp":"...","isReplay":true}
```

For synthetic messages (task notifications, coordinator):

```json
{"type":"user","message":{"role":"user","content":"A background agent completed a task:\n..."},"session_id":"...","parent_tool_use_id":null,"uuid":"...","isReplay":true,"isSynthetic":true}
```

The `uuid` field is round-tripped from stdin — whatever uuid the session-agent writes to stdin, Claude echoes it back in the replay. The `isReplay` field is always `true` on echoed messages. The stdout scanner does not parse or transform these — they pass through to the events stream as-is.

### Input Message Formats (publish)

**User message:**
```json
{"type": "user", "session_id": "{sessionId}", "uuid": "{uuid}", "message": {"role": "user", "content": "fix the bug"}, "parent_tool_use_id": null}
```

**User message with image:**
```json
{"type": "user", "session_id": "{sessionId}", "uuid": "{uuid}", "message": {"role": "user", "content": [{"type": "text", "text": "What's in this?"}, {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "..."}}]}, "parent_tool_use_id": null}
```

**Skill invocation:**
```json
{"type": "user", "session_id": "{sessionId}", "uuid": "{uuid}", "message": {"role": "user", "content": "/commit -m 'Fix bug'"}, "parent_tool_use_id": null}
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

**Effort switch:**
```json
{"type": "control_request", "request": {"subtype": "set_max_thinking_tokens", "budget": 8000}}
```

**Reload plugins (refresh skills):**
```json
{"type": "control_request", "request": {"subtype": "reload_plugins"}}
```

---

## Reconnection Strategy

### Web / iOS (NATS clients)

```
1. NATS disconnects (network, JWT expiry, tab backgrounded)
2. Transport layer: auto-reconnect with backoff
3. AuthStore: check JWT expiry
   a. If expired: call refresh() → reconnect with new JWT
   b. If refresh fails: status = expired → show login
4. EventStore: re-subscribe from max(lastSequence + 1, replayFromSeq) (JetStream replay)
5. SessionStore: re-watch KV (catches any missed state changes)
6. HostStatusStore: resume KV watch (catches any host status changes)
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

### CLI (unix socket)

No reconnection — if the socket drops, the CLI exits. The user re-runs `mclaude-cli attach <session-id>`.

---

## Conversation Model Accumulation

The core logic every NATS-connected client implements:

```
on event:
  if event.seq <= lastSequence: skip (dedup)
  lastSequence = event.seq

  switch event.type:
    case "clear":
      → reset ConversationModel (turns = [])
      → clear pendingMessages = []
      → update replayFromSeq

    case "compact_boundary":
      → reset ConversationModel (turns = [])
      → clear pendingMessages = []
      → add CompactionBlock with summary text
      → update replayFromSeq

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
      → attach ToolResultBlock

    case "user" where content is text:
      → apply user-message parsing rules:
        1. isSynthetic? → SystemMessageBlock turn
        2. skill invocation pattern? → SkillInvocationBlock turn
        3. system notification pattern? → discard
        4. uuid match in pendingMessages? → splice optimistic turn to inline position, clear pendingUuid
        5. text match in pendingMessages (fallback, only when uuid absent)? → same as step 4
        6. no match → create new user Turn with TextBlock(s) and/or UserImageBlock(s)

    case "control_request" where subtype == "can_use_tool":
      → create ControlRequestBlock (status: pending)

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

The CLI implements a simplified version of this logic over its unix-socket transport, without sequence tracking, dedup, or pending message handling (the socket is a single persistent connection, so at-most-once delivery is inherent).

---

## Cache Handling

### NATS KV (server-side materialized state)

Session state, capabilities, usage, pending control requests. Write-through — the session agent updates KV on every relevant event.

**Goes stale when**: session agent crashes.
**Invalidated by**: `$SYS` disconnect event (control-plane marks agent offline). Recovery rewrites KV from fresh state.

### ConversationModel (client-side in-memory)

Built by replaying JetStream events from `replayFromSeq`.

**Goes stale when**: NATS disconnects.
**Invalidated by**: re-subscribe from `max(lastSequence + 1, replayFromSeq)`. JetStream guarantees gap-free replay.

**Reset events**:
- `clear`: user clears conversation. Session agent publishes `clear` event, updates `replayFromSeq` in KV. Client resets to empty and clears pending messages.
- `compact_boundary`: Claude Code compacts context. Session agent publishes `compact_boundary`, updates `replayFromSeq`. Client resets, shows compaction summary, and clears pending messages.

### Capabilities (client-side, from KV)

Skills, tools, agents, model — from `capabilities` field in session KV.

**Goes stale when**: user installs new MCP server or modifies plugins mid-session.
**Invalidated by**: `reload_plugins` control request → Claude Code re-emits capabilities → session agent updates KV → client gets KV watch notification.

### JWT

Cached in-memory with decoded expiry. Periodic check (60s). Refresh when TTL falls below threshold.

### SPA static assets

| Path | `Cache-Control` | Why |
|------|----------------|-----|
| `index.html` | `no-cache` | Must revalidate to pick up new bundle filenames. |
| `/assets/*` | `public, max-age=31536000, immutable` | Content-hashed filenames — safe to cache forever. |

---

## Platform Layer

The only layer that touches native APIs. Renders view models and captures user input. Each platform implements the features marked in the [Feature List](feature-list.md) — the canonical source of truth for what every client supports. Reference features by ID (e.g., C3, T1, X1).

### Web SPA (React)

Implements all features. Markdown via remark, syntax highlighting via Shiki, terminal via xterm.js, voice via WebSpeech API (future).

**Pending message rendering**: `conversationVM.state.pendingMessages` rendered at the bottom of the message list, after all conversation turns. Styled with reduced opacity (0.5) and a subtle "sending..." indicator. Send button stays enabled at all times (except when NATS is disconnected) — users can queue multiple messages.

**System message rendering**: `SystemMessageBlock` rendered as gray, smaller text — same style as `CompactionBlock` summaries.

File layout:
```
src/
  transport/          NATSClient, AuthClient
  stores/             AuthStore, SessionStore, EventStore, LifecycleStore, HostStatusStore
  viewmodels/         SessionListVM, ConversationVM, TerminalVM, PermissionPromptVM, SkillsPickerVM
  lib/                subj.ts (subject builders), slug.ts (typed slugs), pricing.ts
  components/         React components
    events/           Event rendering (AssistantText, ToolCard, ThinkingBlock, etc.)
  hooks/              React hooks (useVersionPoller, etc.)
  types.ts            All TypeScript interfaces
```

### mclaude-cli (Go)

Text REPL. Attaches to a single session via unix socket. Implements conversation display and permission prompts in text mode.

File layout:
```
mclaude-cli/
  main.go             Entry point, attach + session subcommands
  cmd/session.go      Session list (slug resolution)
  context/context.go  ~/.mclaude/context.json management
  events/types.go     Event struct definitions
  events/parse.go     JSON parsing helpers
  events/accumulator.go  Conversation model accumulation
  renderer/renderer.go   Terminal text rendering
  repl/repl.go        Interactive REPL (readline, permission prompts)
```

### Future: Native iOS (SwiftUI)

Implements all features. Terminal via SwiftTerm, voice via SFSpeechRecognizer + AVAudioEngine.

---

## Error Handling

| Failure | Behavior |
|---------|----------|
| Message sent but no replay arrives within 30s | Pending message stays visible with "sending..." indicator. No timeout — user can send another message or interrupt. |
| Claude process crashes mid-turn with pending messages | Pending messages remain visible. On session restart/resume, they won't be replayed (they were never processed). User sees them stuck as pending and can resend. |
| uuid missing from replay (older Claude version) | Text-matching fallback fires: if pending message text exactly matches the echo content, the optimistic turn is confirmed in-place (pendingUuid cleared). If text doesn't match either, a new inline turn is created and the orphaned optimistic turn stays dimmed until the user refreshes. In practice uuid is always round-tripped with `--replay-user-messages`. |
| JetStream replay delivers user events from before clear/compact | Handled by existing `replayFromSeq` logic — events before the boundary are skipped. |

---

## Testing

Store and View Model layers are testable without any platform dependencies:
- Feed mock events into EventStore → assert ConversationModel state
- Feed mock KV updates into SessionStore → assert SessionKVState
- Call ConversationVM.sendMessage() → assert correct NATS publish call with uuid
- Simulate JWT expiry → assert AuthStore triggers refresh
- Send message → assert pending turn appears → feed back user event with matching uuid → assert pending cleared and turn positioned inline
- Send message mid-turn → feed back user event between tool results → assert inline positioning
- Feed synthetic user event → assert SystemMessageBlock turn created
- Feed `clear` event with pending messages → assert pendingMessages cleared

Platform layer tested via browser automation (Playwright) or visual snapshot tests.

---

## Security

- JWT + NKey auth per connection (NATS credentials never exposed to JavaScript beyond memory)
- Per-user NATS permission scoping: `mclaude.users.{uslug}.>` (SPA), `mclaude.users.{uslug}.hosts.{hslug}.>` (per-host daemon)
- `localStorage` for JWT persistence — cleared on logout
- No secrets in URL paths or query params

## Data Model

### NATS payload — user message with uuid

Input subject `api.sessions.input` payload includes `uuid` field:

```json
{
  "type": "user",
  "session_id": "abc-123",
  "uuid": "550e8400-e29b-41d4-a716-446655440000",
  "message": {
    "role": "user",
    "content": "fix the bug"
  }
}
```

`uuid` is optional for backward compatibility. If absent, the text-matching fallback fires.

### Events stream — user replay events

User replay events published by stdout scanner:

```json
{
  "type": "user",
  "message": {"role": "user", "content": "fix the bug"},
  "uuid": "550e8400-e29b-41d4-a716-446655440000",
  "session_id": "abc-123",
  "isReplay": true,
  "parent_tool_use_id": null
}
```

Synthetic replay:

```json
{
  "type": "user",
  "message": {"role": "user", "content": "A background agent completed a task:\n..."},
  "isSynthetic": true,
  "isReplay": true,
  "parent_tool_use_id": null
}
```

## Impact

Specs updated in this commit:
- `docs/spec-state-schema.md` — no changes needed (already canonical)
- ADR-0006 marked as superseded (by prior ADR-0036)
- ADR-0036 marked as superseded by this ADR
- ADR-0012 marked as superseded by this ADR

Components: `mclaude-web`, `mclaude-cli`, `mclaude-session-agent`.

## Scope

In v1 (what this ADR covers):
- Corrected protocol contract (subjects, KV keys, event types, blocks, input formats)
- CLI transport acknowledged as unix-socket (not NATS)
- SystemMessageBlock and UserImageBlock as canonical block types in the conversation model
- `--replay-user-messages` flag on session-agent spawn args
- Optimistic rendering with UUID-based pending → inline promotion
- Mid-turn user message positioning at correct inline location
- Synthetic message handling as SystemMessageBlock turns
- Text-matching fallback dedup for backward compatibility
- Development harness section removed (superseded by ADR-0026)
- Feature list (feature-list.md) remains the canonical source for platform support matrix

Deferred:
- CLI migration to NATS transport (if ever needed — unix socket works well for its use case)
- V1 voice/push-to-talk (V1 in feature list)
- M4 context meter (get_context_usage)
- X3 background reconnect (visibilitychange — specified but not yet implemented)
- Animation for pending → inline transition (v1 just re-renders)
- Pending message editing (user edits a pending message before Claude accepts it)
- Pending message cancellation (user cancels a pending mid-turn redirect)
- Priority control (letting user mark a message as 'now' priority to interrupt immediately)

## Open questions

_All resolved — see Decisions table._

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| mclaude-web (`subj.ts` + host-scope migration) | ~200 | ~60k | Add `hslug` param to all project-scoped builders in `subj.ts`; update all call-sites |
| mclaude-web (`types.ts` schema alignment) | ~30 | ~20k | Add `hostSlug`, `createdAt`, `joinWorktree` to `SessionKVState` |
| mclaude-web (HostStatusStore) | ~200 | ~60k | Create HostStatusStore; delete `heartbeat-monitor.ts`; update `App.tsx` and tests |
| mclaude-session-agent (`--replay-user-messages`) | ~20 | ~15k | Add flag to spawn args, remove manual `handleInput` publish |
| mclaude-web (EventStore pending messages) | ~150 | ~50k | `addPendingMessage`, uuid dedup, inline positioning, pending state on Turn |
| mclaude-web (ConversationVM uuid flow) | ~40 | ~20k | uuid generation, `sendMessage`/`sendMessageWithImage` pending integration |
| mclaude-web (UI pending rendering) | ~60 | ~25k | Dimmed pending messages, remove send-button guard, SystemMessageBlock styling |
| mclaude-cli (accumulator gaps) | ~80 | ~40k | Add `clear` event handling, parent_tool_use_id nesting |
| ADR-0006/0036/0012 supersession notes | ~15 | — | Mechanical edits |

**Total estimated tokens:** ~290k
**Estimated wall-clock:** ~2.5h
