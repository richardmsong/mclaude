# Replay User Messages

## Overview

Enable Claude Code's `--replay-user-messages` flag so that all user messages — including mid-turn redirects queued between tool calls — are echoed on stdout and flow through the events stream. The SPA renders pending messages at the bottom of the chat in a dimmed state, then moves them inline to their correct position when Claude accepts them. This replaces the manual `handleInput` publish and the optimistic `addUserTurn()` approach.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Optimistic UX | Pending → inline promotion | User sees message instantly (dimmed, bottom of chat). When Claude echoes it back, message moves inline to where Claude accepted it. Matches claude.ai webapp behavior. |
| Mid-turn rendering | Inline where they occurred | Mid-turn user messages appear at the exact position in the conversation where Claude drained them from its queue — between tool results. |
| Publish source | Claude's replay only | Remove manual `handleInput` publish. `--replay-user-messages` makes Claude the single source of truth for user messages on the events stream. |
| Synthetic messages | Show as system messages | Replays with `isSynthetic: true` (task notifications, coordinator messages) render as gray system-style messages for power-user visibility. |
| UUID format | Top-level field on stdin JSON | SPA generates uuid, includes in NATS payload. Session-agent preserves it (only strips `session_id`). Claude echoes it back in the replay. |
| Batched messages | Collapse pending on replay | When replays arrive, remove all matching pending messages and show replay content inline. Claude emits individual replays per uuid even when batching. |
| Turn-start messages | Same flow as mid-turn | All messages go through pending → inline, including turn-starting ones. Uniform code path; the pending flash is imperceptible for idle-state messages. |

## User Flow

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

### Session-agent

**Spawn args**: Add `--replay-user-messages` to the Claude Code exec.Command:

```
claude --print --verbose --output-format stream-json --input-format stream-json --replay-user-messages --session-id {id} -w {cwd}
```

Also add to `--resume` invocations:

```
claude --print --verbose --output-format stream-json --input-format stream-json --replay-user-messages --resume {sessionId}
```

**handleInput**: Remove the manual `js.Publish()` to the events stream. Claude's replay echo flows through the existing stdout scanner → `publish(eventSubject, lineCopy)` path. `handleInput` only strips `session_id` and writes to stdin — all other fields (including `uuid`) are preserved.

**Stdout scanner**: No changes — the existing scanner publishes all stdout lines to the events stream verbatim. Claude's new user replay events are just another event type flowing through.

**Claude stdout shape with `--replay-user-messages`**: When this flag is enabled, Claude emits user messages on stdout as NDJSON lines with the following shape:

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

### SPA — Types

Add optional fields to the existing `UserEvent` interface:

```typescript
export interface UserEvent extends BaseEvent {
  type: 'user'
  message: {
    role: 'user'
    content: string | ContentBlock[]
  }
  uuid?: string         // round-tripped from stdin; present on --replay-user-messages echoes
  isReplay?: boolean    // true for all echoed messages
  isSynthetic?: boolean // true for task-notifications and coordinator messages
}
```

Add `SystemMessageBlock` to the `Block` union:

```typescript
export interface SystemMessageBlock {
  type: 'system_message'
  text: string
}

export type Block =
  | TextBlock
  | StreamingTextBlock
  | ToolUseBlock
  | ToolResultBlock
  | ThinkingBlock
  | ControlRequestBlock
  | CompactionBlock
  | SystemMessageBlock
```

Add pending message type:

```typescript
interface PendingMessage {
  uuid: string
  content: string | Array<{ type: string; text?: string }>
  sentAt: number  // Date.now() for ordering
}
```

### SPA — EventStore

**New state**: `_pendingMessages: PendingMessage[]` — messages sent by the user but not yet echoed by Claude.

**addPendingMessage(uuid, content)**: Replaces `addUserTurn()`. Adds to `_pendingMessages` and notifies listeners. Does NOT add to `_conversation.turns`.

**Modified `case 'user'` handler in `_applyEvent()`**:

1. If event content is a tool_result: attach to matching ToolUseBlock and return early (existing behavior, unchanged). Tool results are auto-generated by Claude and never carry a user-generated `uuid`, so no pending matching is needed for them.
2. If event has `uuid` and a matching pending message exists: remove from `_pendingMessages`
3. If event has `isSynthetic: true`: create a system turn with a `SystemMessageBlock` (type `'system_message'`, text field contains the message content)
4. Otherwise: create a normal user turn inline at current position
5. Remove the text-matching dedup logic (uuid matching replaces it)

**Getter**: `get pendingMessages(): PendingMessage[]` — exposes pending list for the UI.

### SPA — ConversationVM

Add `pendingMessages` to `ConversationVMState` so the UI layer accesses it through the existing `conversationVM.state` pattern:

```typescript
export interface ConversationVMState {
  turns: ConversationModel['turns']
  pendingMessages: PendingMessage[]  // new
  state: SessionState
  model: string
  skills: string[]
  isStreaming: boolean
}
```

`ConversationVM.state` getter reads `this.eventStore.pendingMessages` and includes it in the returned state.

**sendMessage(text)**:
1. Generate uuid (`crypto.randomUUID()`)
2. Call `eventStore.addPendingMessage(uuid, text)`
3. Publish to NATS with uuid: `{"type": "user", "session_id": "...", "uuid": "<uuid>", "message": {"role": "user", "content": "<text>"}}`
4. No `sending` guard — the function is synchronous (NATS publish is fire-and-forget) and the send button stays enabled so users can queue multiple messages

**sendMessageWithImage(text, imageBase64, mimeType)**:
Same pattern — generate uuid, add pending, publish with uuid.

### SPA — UI (SessionDetailScreen)

**Pending messages**: Render `conversationVM.state.pendingMessages` at the bottom of the message list, after all conversation turns. Styled with reduced opacity (0.5) and a subtle "sending..." indicator.

**Send button**: Remove the `sending` state guard and `setSending` calls from `handleSend`. The send button stays enabled at all times (except when NATS is disconnected). Users can queue multiple messages while Claude is busy — each appears as a pending message at the bottom.

**Inline user messages**: No change to existing user turn rendering — they appear at the correct position in the `conversation.turns` array.

**System messages from synthetic replays**: `EventList` renders `SystemMessageBlock` (type `'system_message'`) as gray, smaller text — the same style used for `CompactionBlock` summaries.

## Data Model

### NATS payload change

Input subject `api.sessions.input` payload adds optional `uuid` field:

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

`uuid` is optional for backward compatibility. If absent, pending matching falls back to text comparison.

### Events stream

User replay events on the events stream (published by stdout scanner):

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

## Error Handling

| Failure | Behavior |
|---------|----------|
| Message sent but no replay arrives within 30s | Pending message stays visible with "sending..." indicator. No timeout — user can send another message or interrupt. |
| Claude process crashes mid-turn with pending messages | Pending messages remain visible. On session restart/resume, they won't be replayed (they were never processed). User sees them stuck as pending and can resend. |
| uuid missing from replay (older Claude version) | Fall back to text-matching dedup against pending messages. If no match, create inline turn normally. |
| JetStream replay delivers user events from before clear/compact | Handled by existing `replayFromSeq` logic — events before the boundary are skipped. |

## Scope

### In v1

- `--replay-user-messages` flag on session-agent spawn args
- Remove manual `handleInput` publish to events stream
- uuid generation and round-trip (SPA → stdin → stdout → events → SPA)
- Pending message state in EventStore
- Pending → inline promotion on uuid match
- Inline rendering of mid-turn user messages
- Synthetic replay rendering as system messages
- Remove old text-matching dedup and `addUserTurn()`

### Deferred

- Animation for pending → inline transition (v1 just re-renders)
- Pending message editing (user edits a pending message before Claude accepts it)
- Pending message cancellation (user cancels a pending mid-turn redirect)
- Priority control (letting user mark a message as 'now' priority to interrupt immediately)
