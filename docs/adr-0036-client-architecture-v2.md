# ADR: Client Architecture v2

**Status**: accepted
**Status history**:
- 2026-04-26: draft
- 2026-04-26: accepted — paired with spec-cli.md, spec-conversation-events.md

> Supersedes `adr-0006-client-architecture.md`. The layered architecture (Transport → Store → View Model → Platform) and the conversation model accumulation algorithm are preserved. This ADR corrects the protocol contract (subjects, KV keys, event types, block types) to match the canonical state schema (ADR-0024 typed slugs + ADR-0004/ADR-0035 host scoping), acknowledges the CLI's unix-socket transport as a legitimate alternative to the full NATS stack, and adds block types and features that emerged since ADR-0006.

## Overview

Defines the client-side architecture for every mclaude platform (web SPA, mclaude-cli, future native apps). Clients follow a four-layer architecture: Transport → Store → View Model → Platform. The business logic (Store + View Model) is identical across platforms; only the Transport and Platform layers differ. This ADR is the single source of truth for the client protocol contract — NATS subjects, KV keys, JetStream streams, event types, conversation model blocks, input message formats, reconnection strategy, and cache handling.

## Motivation

ADR-0006 was written before ADR-0024 (typed slugs), ADR-0004 (BYOH host scoping), and ADR-0035 (unified host architecture). As a result, every protocol artifact in ADR-0006 is stale:

1. **Subject format**: ADR-0006 uses `mclaude.{userId}.{projectId}.events.{sessionId}`. The canonical format is `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events.{sslug}`.
2. **KV key format**: ADR-0006 uses `{userId}/{projectId}/{sessionId}` (slash-separated UUIDs). The canonical format is `{uslug}.{hslug}.{pslug}.{sslug}` (dot-separated slugs).
3. **SessionState schema**: ADR-0006 is missing `slug`, `userSlug`, `hostSlug`, `projectSlug`, `createdAt`, `joinWorktree`.
4. **CLI transport**: ADR-0006 says "every client follows the same layered architecture" with NATS transport. The CLI uses unix sockets to the session agent — a deliberate divergence that works well for its use case.
5. **Block types**: Two block types emerged in the web SPA that ADR-0006 doesn't cover: `SystemMessageBlock` (synthetic system notifications) and `UserImageBlock` (base64 image content in user messages).
6. **API subjects**: ADR-0006 lists `sessions.restart` but omits `sessions.resume`. Both exist: `restart` restarts the Claude Code process; `resume` resumes from a paused/crashed session. ADR-0006 also omits `events._api` (error events).

Additionally, the `/implement-features` development harness described in ADR-0006 was never built and its design is superseded by the spec-driven-dev workflow (ADR-0026). That section is dropped entirely.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Architecture layers | Preserve Transport → Store → View Model → Platform from ADR-0006 | Proven in production. Web SPA implements all four layers faithfully. |
| CLI transport exception | CLI uses unix-socket transport to session-agent, not NATS | CLI attaches to a single running session — it doesn't need KV watches, JetStream replay, or multi-session management. Unix socket is simpler and eliminates NATS dependency for local debugging. |
| CLI feature scope | CLI is a debug-attach tool, not a full client | CLI implements conversation + permissions in text mode. No project management, no terminal sessions, no voice. Feature matrix in the Feature List (feature-list.md) is the authority. |
| Subject format | ADR-0024 typed slugs + ADR-0035 host scoping | All project-scoped subjects use `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}...`. |
| Host scoping in subjects | `.hosts.{hslug}.` inserted between user and project in ALL project-scoped subjects (pending ADR-0035 implementation) | Current code (subj.ts, pkg/subj) uses ADR-0024 form without host scope. ADR-0035 adds host scope. This ADR documents the target state. |
| KV key format | Dot-separated slugs with host scope: `{uslug}.{hslug}.{pslug}.{sslug}` | Matches spec-state-schema.md. |
| Block types | ADR-0006 blocks + SystemMessageBlock + UserImageBlock | Both exist in production code and serve real use cases. |
| Event types | Align with what session-agent actually publishes (see Stream-JSON Event Types table) | ADR-0006 was aspirational; this ADR documents reality. |
| Development harness | Removed — superseded by spec-driven-dev workflow (ADR-0026) | The `/implement-features` skill and GHA automation from ADR-0006 were never built. The spec-driven-dev plugin (plan-feature → feature-change → dev-harness → implementation-evaluator) replaced them. |
| HeartbeatMonitor | Replace with `mclaude-hosts` KV watcher — standalone HostStatusStore that watches `mclaude-hosts` KV for per-host online/offline status. Replaces the old `mclaude-heartbeats` bucket watcher. | ADR-0035 removes `mclaude-heartbeats` and uses `$SYS` presence only; control-plane writes online/offline to `mclaude-hosts` KV. A standalone store keeps the same architecture pattern as other stores. |
| CapabilitiesCache | Not a standalone component — capabilities live on SessionKVState and EventStore | ADR-0006 listed it as a Store Layer component but it was never implemented as a separate class. The data comes from two sources: `capabilities` field in session KV (set by session-agent from `init` event) and `system/init` events. Both are handled by existing stores. |
| Reconnection strategy | Same algorithm as ADR-0006, plus `visibilitychange` for mobile browsers | Mobile Safari kills WebSocket on background; reconnect on foreground. |

## User Flow

No change from ADR-0006. Users interact with sessions through the platform layer (web components, CLI REPL, or future native UI). The layers below handle connection, state management, and conversation accumulation transparently.

## Component Changes

### All Clients (protocol alignment)

- NATS subjects updated to ADR-0024 + ADR-0035 form (see Protocol Contract below)
- KV keys updated to dot-separated slug form with host scope
- SessionState schema aligned with spec-state-schema.md
- Block types extended with SystemMessageBlock and UserImageBlock
- Event type handling aligned with what session-agent actually publishes

### mclaude-web

- Already implements the four-layer architecture faithfully
- `subj.ts` needs host-scope update when ADR-0035 lands (add `hslug` parameter to all project-scoped builders)
- HeartbeatMonitor replaced by HostStatusStore (watches `mclaude-hosts` KV per ADR-0035)
- SystemMessageBlock + UserImageBlock already implemented

### mclaude-cli

- Transport: unix socket to session-agent (no change — this is the correct design for CLI)
- Accumulator: handles core event types; omits ThinkingBlock rendering, SkillInvocationBlock parsing, SystemMessageBlock, and `clear` event handling (acceptable per CLI's debug-attach scope)
- No KV watches, no JetStream replay, no session management

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

Subscribes to JetStream event stream for a specific session. Accumulates events into a conversation model.

```
EventStore(natsClient, userSlug, hostSlug, projectSlug, sessionSlug)
  events: Event[]
  conversation: ConversationModel

  start(replayFromSeq?: number)
  stop()
  lastSequence: number
  addPendingMessage(uuid, text)

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
| `system` | `compact_boundary` | Reset ConversationModel, add CompactionBlock with summary |
| `stream_event` | `content_block_delta` | Append to StreamingTextBlock (live typing) |
| `assistant` | — | Finalize StreamingTextBlock, extract tool_use blocks, create ThinkingBlocks |
| `user` | (text, not tool_result) | Create user Turn (see user-message parsing rules below) |
| `user` | (tool_result) | Attach ToolResultBlock to matching ToolUseBlock by toolUseId |
| `control_request` | `can_use_tool` | Create ControlRequestBlock, show permission prompt |
| `tool_progress` | — | Update ToolUseBlock elapsed time |
| `result` | — | Turn complete, accumulate usage stats |
| `clear` | — | Reset ConversationModel (empty turns), update replayFromSeq |

Events the client may ignore (but must not break on):

| Event type | Notes |
|-----------|-------|
| `keep_alive` | Connection health, no UI action |
| `system` with subtypes: `api_retry`, `hook_started`, etc. | Log or ignore |

### User-Message Parsing Rules

When a `user` event arrives with text content (not tool_result):

1. If `isSynthetic` flag is set: create a `SystemMessageBlock` turn (system notification injected by the platform). Do not show as a user message.
2. If text starts with `"Base directory for this skill:"`: parse as `SkillInvocationBlock` — extract skill name from path segment after `skills/`, extract args from text after `"ARGUMENTS:"` line.
3. If text starts with `"[SYSTEM NOTIFICATION"`: discard entirely — do not create a turn.
4. Otherwise: dedup against pending messages — match by `event.uuid` (primary) or exact text content (fallback). On match: clear `pendingUuid` on the existing optimistic turn. No match: create a new user Turn with TextBlock(s).

For user messages containing image content blocks: create `UserImageBlock` with `mediaType` and base64 `data`.

### Input Message Formats (publish)

**User message:**
```json
{"type": "user", "message": {"role": "user", "content": "fix the bug"}, "session_id": "{sessionId}", "parent_tool_use_id": null}
```

**User message with image:**
```json
{"type": "user", "message": {"role": "user", "content": [{"type": "text", "text": "What's in this?"}, {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "..."}}]}, "session_id": "{sessionId}", "parent_tool_use_id": null}
```

**Skill invocation:**
```json
{"type": "user", "message": {"role": "user", "content": "/commit -m 'Fix bug'"}, "session_id": "{sessionId}", "parent_tool_use_id": null}
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
      → update replayFromSeq

    case "compact_boundary":
      → reset ConversationModel (turns = [])
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
      → apply user-message parsing rules (see above)

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

The CLI implements a simplified version of this logic over its unix-socket transport, without sequence tracking or dedup (the socket is a single persistent connection, so at-most-once delivery is inherent).

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
- `clear`: user clears conversation. Session agent publishes `clear` event, updates `replayFromSeq` in KV. Client resets to empty.
- `compact_boundary`: Claude Code compacts context. Session agent publishes `compact_boundary`, updates `replayFromSeq`. Client resets and shows compaction summary.

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

## Testing

Store and View Model layers are testable without any platform dependencies:
- Feed mock events into EventStore → assert ConversationModel state
- Feed mock KV updates into SessionStore → assert SessionKVState
- Call ConversationVM.sendMessage() → assert correct NATS publish call
- Simulate JWT expiry → assert AuthStore triggers refresh

Platform layer tested via browser automation (Playwright) or visual snapshot tests.

---

## Security

- JWT + NKey auth per connection (NATS credentials never exposed to JavaScript beyond memory)
- Per-user NATS permission scoping: `mclaude.users.{uslug}.>` (SPA), `mclaude.users.{uslug}.hosts.{hslug}.>` (per-host daemon)
- `localStorage` for JWT persistence — cleared on logout
- No secrets in URL paths or query params

## Impact

Specs updated in this commit:
- `docs/spec-state-schema.md` — no changes needed (already canonical)
- ADR-0006 marked as superseded

Components: `mclaude-web`, `mclaude-cli`.

## Scope

In v1 (what this ADR covers):
- Corrected protocol contract (subjects, KV keys, event types, blocks, input formats)
- CLI transport acknowledged as unix-socket (not NATS)
- Two new block types (SystemMessageBlock, UserImageBlock) added to the conversation model
- Development harness section removed (superseded by ADR-0026)
- Feature list (feature-list.md) remains the canonical source for platform support matrix

Deferred:
- CLI migration to NATS transport (if ever needed — unix socket works well for its use case)
- V1 voice/push-to-talk (V1 in feature list)
- M4 context meter (get_context_usage)
- X3 background reconnect (visibilitychange — specified but not yet implemented)

## Open questions

_All resolved — see Decisions table._

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| mclaude-web (`subj.ts` + host-scope migration) | ~200 | ~60k | Add `hslug` param to all project-scoped builders in `subj.ts`; update all call-sites in EventStore, LifecycleStore, TerminalVM, ConversationVM, SessionListVM, and App.tsx to pass `hostSlug` (when ADR-0035 lands) |
| mclaude-web (`types.ts` schema alignment) | ~30 | ~20k | Add `hostSlug`, `createdAt`, `joinWorktree` to `SessionKVState` in types.ts to match spec-state-schema.md |
| mclaude-web (HostStatusStore) | ~200 | ~60k | Create HostStatusStore; delete `heartbeat-monitor.ts`; update `App.tsx` to create HostStatusStore instead of HeartbeatMonitor; update `SessionListVM` constructor to accept HostStatusStore; update tests (`session-list-vm.test.ts`, `DashboardScreen.filter.test.tsx`); remove `kvKeyHeartbeatsForUser` from `subj.ts` |
| mclaude-cli (accumulator gaps) | ~80 | ~40k | Add `clear` event handling, parent_tool_use_id nesting |
| ADR-0006 supersession note | ~5 | — | Mechanical edit |

**Total estimated tokens:** ~180k
**Estimated wall-clock:** ~1.5h
