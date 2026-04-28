# Spec: Web SPA

## Role

The web SPA is the primary user interface for mclaude. It is a React 18 single-page application designed mobile browser first (iOS Safari, Android Chrome) — enterprise constraint: must work in mobile browsers where native apps can't reach VPN. Desktop browsers use the same SPA.

## Deployment

Built as static files served by nginx. Image: `ghcr.io/richardmsong/mclaude-spa`. Served at `/*` behind nginx ingress (all non-API paths).

## NATS Connection

The SPA connects to NATS via the `/nats` WebSocket proxy endpoint. Authentication uses the per-host user JWT and NKey seed returned by `POST /auth/login`. The connection is scoped to `mclaude.users.{uslug}.hosts.{hslug}.>` (wildcard on host per ADR-0035).

## Event Rendering

The SPA consumes raw stream-json events from NATS JetStream (`MCLAUDE_EVENTS` stream):

| Event type | Rendering |
|-----------|-----------|
| `stream_event` (content_block_delta) | Live streaming text token-by-token |
| `assistant` | Complete message (replaces streamed deltas) |
| `tool_use` | Collapsible block |
| `tool_progress` | Elapsed time indicator |
| `control_request` | Approve/deny buttons |
| `session_state_changed` | Status indicator |
| `init` | Populate skills picker, tool list, model info |
| `result` | Usage/cost display |
| Events with `parent_tool_use_id` | Render nested under parent Agent block |

## State Management

The SPA watches NATS KV buckets for live updates:

- `mclaude-sessions` — session state (capabilities, pendingControls, usage, state)
- `mclaude-projects` — project list and status

## Event Replay

On reconnect or initial load, the SPA replays events from `max(lastSeenSeq + 1, replayFromSeq)` where `replayFromSeq` is read from the session's KV entry (updated on `/clear` and compaction). Clients deduplicate by JetStream sequence number.

## Background Reconnect (Mobile Browser)

When the browser tab becomes visible after being backgrounded:

```js
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState !== 'visible') return;
  nc.reconnect();
  kv.watch(`{uslug}/>`);
});
```

This handles iOS/Android browsers that suspend WebSocket connections when backgrounded.

## JWT Refresh

JWT expiry is 8h (configurable server-side via `JWT_EXPIRY_SECONDS`). The SPA checks JWT TTL every 60 seconds. When remaining TTL falls below 15 minutes, it calls `POST /auth/refresh` with the current JWT to obtain a fresh one. The NATS connection is re-authenticated with the new JWT.

## Model/Effort Switching

The SPA sends `set_model` and `set_max_thinking_tokens` control requests mid-session to change the Claude model or thinking effort without restarting the session.

## Cost Tracking

`result` events include `usage` (input/output/cache tokens, cost). The session agent accumulates usage in NATS KV. The SPA displays per-session and per-project cost.

## File/Image Uploads

Files and images are sent as base64 in the user message content array (standard Anthropic content format). Max ~20MB per image.

## Routing

React Router v6 with parametric segments: `/api/users/{uslug}/projects/{pslug}/sessions/{sslug}`. Display names render in UI; slugs in the URL path.

## TypeScript Slug/Subject Mirrors

- `src/lib/slug.ts` — mirrors `mclaude-common/pkg/slug` (Slugify + Validate + Fallback) for display consistency.
- `src/lib/subj.ts` — mirrors `mclaude-common/pkg/subj`. Publishes via typed helpers only. Runtime assertion in dev builds.

## Session Management

### TypeScript Types

- `SessionState.state` union gains `"updating"`.
- `LifecycleEvent.type` union gains `"session_upgrading"`.

### Session State

The `SessionState.state` union includes `"updating"` (set by the session-agent on SIGTERM). When a session enters `"updating"` state:

- `DashboardScreen` `STATE_LABELS` gains `updating: 'Updating...'`.
- Session Detail Screen shows a persistent blue "Updating..." banner above the conversation while `state === 'updating'`.
- The `StatusDot` renders as a blue pulsing indicator (`var(--blue)`, added to `PULSE_STATES`).
- The message input box remains enabled (user can queue messages; they will be delivered to the new pod).

### createSession()

`SessionListVM.createSession()` publishes to `subjSessionsCreate(uslug, hslug, pslug)` and waits for the new session to appear in the `mclaude-sessions` KV watcher (via `SessionStore.onSessionAdded()`). Timeout: 30 seconds. On error the session-agent publishes an `api_error` event on `subjEventsApi(uslug, hslug, pslug)`; the SPA subscribes and surfaces the error to the user.

No request-reply: JetStream `api.sessions.create` messages have no Reply field. Success is signalled by the session key appearing in KV.

### deleteSession()

`SessionListVM.deleteSession()` publishes a fire-and-forget message to `subjSessionsDelete(uslug, hslug, pslug)`. The SPA does not wait for an acknowledgement. Session removal is detected via the KV watcher receiving a `DEL` operation for the session key.

### SessionStore.onSessionAdded()

```ts
onSessionAdded(projectId: string, callback: (session: Session) => void): () => void
```

Registers a one-shot listener that fires when a new session belonging to `projectId` appears in the KV watcher snapshot. Filters on `session.projectId === projectId` so that concurrent creates in other projects do not resolve the wrong promise. Returns an unsubscribe function. Used by `createSession()` to detect when the session-agent has written the new session entry to KV.

### KV Watch DEL/PURGE Handling

`KVEntry.operation` may be `'PUT' | 'DEL' | 'PURGE'`. The session store's `kvWatch` callback must handle `DEL` (and `PURGE`) by removing the corresponding entry from the `_sessions` map, not inserting it. Failure to handle `DEL` causes ghost sessions to appear in the UI after deletion.

## Dependencies

| Dependency | Purpose |
|------------|---------|
| React 18 | UI framework |
| NATS WebSocket client | Real-time event streaming and KV state |
| xterm.js | Terminal rendering |
| React Router v6 | Client-side routing |
