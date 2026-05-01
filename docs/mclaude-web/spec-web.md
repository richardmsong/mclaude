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
- `mclaude-hosts` — host liveness and online/offline state (via `HeartbeatMonitor`)

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

The SPA sends `set_model`, `set_max_thinking_tokens`, and `reload_plugins` control requests mid-session. `set_model` and `set_max_thinking_tokens` change the Claude model or thinking effort without restarting. `reload_plugins` refreshes the cached capabilities (skills, tools) from Claude Code without restarting the session — triggered from the skills picker refresh button.

## Cost Tracking

`result` events include `usage` (input/output/cache tokens, cost). The session agent accumulates usage in NATS KV. The SPA displays per-session and per-project cost.

## Device-Code Verification Page

The SPA serves a device-code verification page at `/auth/device-code/verify`. This is the page users visit when `mclaude login` displays a verification URL. The page:

1. Accepts the user code (pre-filled from the URL query parameter `?code=...` or entered manually)
2. Authenticates the user (reuses the existing SPA auth session — if not logged in, redirects to login first)
3. Calls `POST /api/auth/device-code/poll` (or a dedicated approval endpoint) to bind the device code to the authenticated user
4. Displays a success screen confirming the CLI is now authorized

The page is minimal: a code-entry field, an "Approve" button, and success/error states. No navigation chrome — it's a standalone flow.

## File/Image Uploads (Attachments)

Files and images are uploaded via S3 pre-signed URLs and referenced in messages as `AttachmentRef` (ADR-0053). The flow:

1. User selects a file (file picker) or pastes an image (paste handler)
2. SPA requests a pre-signed upload URL from the control-plane: `POST /api/attachments/upload-url` with `{filename, mimeType, sizeBytes, projectSlug, hostSlug}`
3. CP returns `{id, uploadUrl}` — the upload URL is scoped to a single S3 object, valid for 5 minutes
4. SPA uploads the file directly to S3 using the signed URL
5. SPA confirms the upload: `POST /api/attachments/{id}/confirm`
6. SPA attaches an `AttachmentRef` (`{id, filename, mimeType, sizeBytes}`) to the outgoing user message — no binary data flows through NATS

Max file size: 50MB (enforced by CP when generating upload URLs).

## Attachment Rendering

When session events contain `AttachmentRef` content blocks (user-uploaded or agent-generated attachments), the SPA resolves them to renderable content:

1. SPA encounters an `attachment_ref` content block in a session event
2. SPA requests a pre-signed download URL from the control-plane: `GET /api/attachments/{id}` — CP validates the user owns the project and returns `{id, filename, mimeType, sizeBytes, downloadUrl}`
3. SPA renders the attachment based on MIME type:
   - Images (`image/*`): inline `<img>` with the download URL as `src`
   - Other files: download link with filename and size

Download URLs are time-limited (5 minutes). The SPA re-requests a fresh URL if the previous one has expired.

## Imported Sessions

Imported sessions (created via `mclaude import`) are treated identically to native sessions in the SPA. No "imported" badge, no visual distinction. They appear in the session list and project views like any other session. Import metadata is stored internally but not surfaced in the UI (ADR-0053).

## Routing

React Router v6 with parametric segments: `/api/users/{uslug}/projects/{pslug}/sessions/{sslug}`. Display names render in UI; slugs in the URL path. The device-code verification page is served at `/auth/device-code/verify`.

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

### createProject()

`SessionListVM.createProject()` publishes to `subjProjectsCreate(uslug)` with `{name, gitUrl?}`. **Known bug:** the payload does not include `hostSlug` — the control-plane's NATS handler cannot resolve the host for provisioning, so SPA-created projects get no session-agent pod.

### deleteSession()

`SessionListVM.deleteSession()` publishes a fire-and-forget message to `subjSessionsDelete(uslug, hslug, pslug)` with `{sessionId}`. **Known gap:** spec says payload field is `sessionSlug` but code sends `sessionId` (UUID). The session-agent handles this because it looks up sessions by UUID internally.

### restartSession()

`SessionListVM.restartSession()` publishes to `subjSessionsRestart(uslug, hslug, pslug)` with `{sessionId}`. **Known gaps:** (1) spec says payload field is `sessionSlug` but code sends `sessionId` (UUID). (2) Spec requires `requestId` in the payload for error correlation, but it is omitted — restart errors are silently lost.

### SessionStore.onSessionAdded()

```ts
onSessionAdded(projectId: string, callback: (session: Session) => void): () => void
```

Registers a one-shot listener that fires when a new session belonging to `projectId` appears in the KV watcher snapshot. Filters on `session.projectId === projectId` so that concurrent creates in other projects do not resolve the wrong promise. Returns an unsubscribe function. Used by `createSession()` to detect when the session-agent has written the new session entry to KV.

### SessionStore KV Watch Prefixes

`SessionStore` watches two KV buckets with different key prefixes (ADR-0050 D13):

- **`mclaude-sessions`**: watched with `userSlug` prefix (`kvKeySessionsForUser(userSlug)`). Session KV keys are slug-based (`{uslug}.{hslug}.{pslug}.{sslug}`), written by the session-agent.
- **`mclaude-projects`**: watched with `userId` prefix (`kvKeyProjectsForUser(userId)`). Project KV keys are still UUID-based (`{userId}.{projectId}`), written by the control-plane. The project KV key format has not been migrated to slugs yet.

`App.tsx` passes `authState.userSlug ?? authState.userId` as the `userSlug` constructor param and `authState.userId` as the `userId` param.

### ConversationVM Session ID Resolution

`ConversationVM` includes `session_id` (the session's UUID) in every `sessions.input` NATS payload. The session-agent looks up sessions by UUID, not by slug. `App.tsx` resolves the UUID from the session store via `session?.id ?? route.sessionId` before constructing the ConversationVM, so that slug-format route URLs (e.g. `#u/dev/h/local/p/default-project/s/new-session`) produce the correct UUID in NATS messages (ADR-0050 D15).

### Default Host Slug

Throughout the SPA, `hostSlug` defaults to `'local'` when not available from the project, session, or route. This fallback appears in `ConversationVM`, `SessionListVM`, `EventStore`, `LifecycleStore`, and `TerminalVM` — all have `hostSlug` constructor parameters defaulting to `'local'`. This means sessions on projects without a known host will silently use `'local'` as the host slug for all NATS subjects, which is correct for single-host dev deployments but will need explicit host resolution for multi-cluster production.

### KV Watch DEL/PURGE Handling

`KVEntry.operation` may be `'PUT' | 'DEL' | 'PURGE'`. The session store's `kvWatch` callback must handle `DEL` (and `PURGE`) by removing the corresponding entry from the `_sessions` map, not inserting it. The DEL handler extracts the session slug from the KV key and performs a slug→UUID lookup to find and remove the correct entry from the `_sessions` map (which is keyed by UUID).

## Desktop Notifications

The SPA requests browser notification permission (`Notification.requestPermission()`) on first NATS connection. When a session transitions to `requires_action` while the tab is not visible (`document.visibilityState !== 'visible'`), a desktop notification is fired with title "MClaude — Permission needed" and body "A session needs your approval". No notification is sent when the tab is in the foreground.

## Project Filter

`SessionListVM` supports filtering the dashboard to a single project via `filterProjectId`. The filter is persisted to `localStorage` key `mclaude.filterProjectId`. `resolveFilter()` validates the stored project still exists in the KV store and auto-clears the filter if the project was deleted. The `DashboardScreen` UI exposes this as a project filter selector.

## Dead Code

The following helpers in `src/lib/subj.ts` reference removed infrastructure and are never called from production code:
- `subjClusterProvision()` / `subjClusterStatus()` — produce `mclaude.clusters.{cslug}.*` subjects that no longer exist per ADR-0035.
- `kvKeyUserClusters()` — references the removed `mclaude-clusters` KV bucket.

These should be cleaned up in a future commit.

## Dependencies

| Dependency | Purpose |
|------------|---------|
| React 18 | UI framework |
| NATS WebSocket client | Real-time event streaming and KV state |
| xterm.js | Terminal rendering |
| React Router v6 | Client-side routing |
