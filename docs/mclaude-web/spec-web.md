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

The SPA watches NATS KV buckets for live updates. Per ADR-0054, sessions and projects use per-user buckets (user slug encoded in the bucket name):

- `mclaude-sessions-{uslug}` — per-user session state (capabilities, pendingControls, usage, status). Key format: `hosts.{hslug}.projects.{pslug}.sessions.{sslug}`.
- `mclaude-projects-{uslug}` — per-user project list and status. Key format: `hosts.{hslug}.projects.{pslug}`.
- `mclaude-hosts` — host liveness and online/offline state (via `HeartbeatMonitor`). Shared bucket, key format: `{hslug}`.

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

`SessionListVM.createSession()` publishes to `subjSessionsCreate(uslug, hslug, pslug)` and waits for the new session to appear in the `mclaude-sessions-{uslug}` KV watcher (via `SessionStore.onSessionAdded()`). Timeout: 30 seconds. On error the session-agent publishes an `api_error` event on `subjEventsApi(uslug, hslug, pslug)`; the SPA subscribes and surfaces the error to the user.

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

`SessionStore` watches two per-user KV buckets (ADR-0054):

- **`mclaude-sessions-{uslug}`**: watched with `>` wildcard (all keys in the per-user bucket). Session KV keys: `hosts.{hslug}.projects.{pslug}.sessions.{sslug}`, written by the session-agent.
- **`mclaude-projects-{uslug}`**: watched with `>` wildcard (all keys in the per-user bucket). Project KV keys: `hosts.{hslug}.projects.{pslug}`, written by the control-plane.

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

## E2E Test Infrastructure

Playwright tests run against the live k3d cluster at `https://dev.mclaude.richardmcsong.com`. A global setup script creates a fresh test user before each invocation; a global teardown script deletes it after.

### Running tests against k3d

The admin API binds to `127.0.0.1:9091` inside the pod and is not exposed via the public Ingress. A `kubectl port-forward` is required to reach it:

```bash
# Step 1 — forward the admin port (run in a separate terminal or background):
kubectl port-forward -n mclaude-system svc/mclaude-cp-control-plane 9091:9091 &

# Step 2 — run tests:
BASE_URL=https://dev.mclaude.richardmcsong.com \
ADMIN_URL=http://localhost:9091 \
ADMIN_TOKEN=dev-admin-token \
npx playwright test --project=chromium
```

`ADMIN_TOKEN` is the break-glass admin bearer token stored in the `mclaude-control-plane` K8s Secret (`admin-token` field). For the k3d dev cluster the value is `dev-admin-token`.

To run tests without a port-forward (skip user creation, use hardcoded dev defaults):
```bash
BASE_URL=https://dev.mclaude.richardmcsong.com npx playwright test --project=chromium
```

### Test user isolation

**Global setup** (`e2e/global-setup.ts`):
1. Reads `ADMIN_URL`, `ADMIN_TOKEN` (default `dev-admin-token`), and `BASE_URL` from env.
2. If `DEV_EMAIL` and `DEV_TOKEN` env vars are both set, skips creation: writes `{skipped: true}` to `.test-user.json` and returns.
3. If `ADMIN_URL` is not set, skips creation: writes `{skipped: true}` and returns — spec files fall back to their hardcoded dev defaults.
4. Generates `userId = crypto.randomUUID()` and calls `POST {ADMIN_URL}/admin/users` with `Authorization: Bearer {ADMIN_TOKEN}`, body `{id: userId, email: "e2e-{Date.now()}@mclaude.local", name: "E2E Test User", password: "<random 16-char hex>"}`. The `id` field is required by the CP `AdminUserRequest` struct.
5. Computes `userSlug = slugify(email)` (lowercase, replace non-`[a-z0-9]` runs with `-`, trim, truncate to 63 chars). Sets `process.env['DEV_EMAIL'] = email`, `process.env['DEV_TOKEN'] = token`, and `process.env['DEV_USER_SLUG'] = userSlug` so spec files inherit the test user credentials and slug.
6. **Session seeding** — calls `POST {BASE_URL}/auth/login` with `{email, password: token}` (no `nkey_public` — legacy mode) to receive `{jwt, nkeySeed, natsUrl, userSlug}`. Derives the NATS WebSocket URL: uses `natsUrl` from the response if non-empty; otherwise replaces `https://` with `wss://` in `BASE_URL` and appends `/nats`. Connects to NATS: `connect({ servers: [natsWsUrl], authenticator: jwtAuthenticator(jwt, seed) })` where `seed = new TextEncoder().encode(nkeySeed)`.
7. **Project seeding** — publishes `{name: "e2e-default", hostSlug: "local"}` to `mclaude.users.{uslug}.hosts.local.api.projects.create` via `nc.request()` (30 s timeout). Throws if the response contains an error. Waits up to 30 s for the project to appear in `mclaude-projects-{uslug}` KV (key prefix `hosts.local.projects.`). Parses the KV entry JSON and reads the `slug` field as `projectSlug`. Throws `"global-setup: timed out waiting for project KV entry"` if not found. Sets `process.env['DEV_PROJECT_SLUG'] = projectSlug`.
8. **Session seeding** — publishes `{}` to `mclaude.users.{uslug}.hosts.local.projects.{pslug}.sessions.create`. Waits up to 60 s for a session to appear in `mclaude-sessions-{uslug}` KV (key prefix `hosts.local.projects.{pslug}.sessions.`). Reads `sessionSlug` from the last `.`-delimited segment of the KV key. Throws `"global-setup: timed out waiting for session KV entry — check kubectl get pods -n mclaude-system for Pending pods"` if not found. Closes the NATS connection. Sets `process.env['DEV_SESSION_SLUG'] = sessionSlug`.
9. Writes `{userId, email, token, projectSlug, sessionSlug}` to `e2e/.test-user.json`.
10. On login failure, throws `"global-setup: login failed {status}: {body}"`. On any other failure, throws — all tests abort cleanly.

**Global teardown** (`e2e/global-teardown.ts`):
1. Reads `e2e/.test-user.json`. If missing or `skipped: true`, returns immediately.
2. Deletes `e2e/.test-user.json` eagerly before the API call (prevents re-read on a subsequent crashed run).
3. Calls `DELETE {ADMIN_URL}/admin/users/{userId}` with `Authorization: Bearer {ADMIN_TOKEN}`.
4. Logs a warning on deletion failure (does not throw — teardown runs after tests complete regardless).

**`playwright.config.ts`** declares `globalSetup: './e2e/global-setup.ts'` and `globalTeardown: './e2e/global-teardown.ts'`.

### Fixture credentials and spec file pattern

`e2e/fixtures.ts` resolves credentials in priority order:
1. `DEV_EMAIL` / `DEV_TOKEN` / `DEV_USER_SLUG` / `DEV_PROJECT_SLUG` / `DEV_SESSION_SLUG` environment variables (set by global setup or CI).
2. `e2e/.test-user.json` (written by global setup for the current run).
3. Hardcoded defaults.

Spec files that define local credential or slug constants must use `process.env` with hardcoded fallbacks — not string literals — so that global setup's `process.env` assignments take effect:
- `process.env['DEV_EMAIL'] || 'dev@mclaude.local'`
- `process.env['DEV_TOKEN'] || 'dev'`
- `process.env['DEV_USER_SLUG'] || 'dev-mclaude-local'` (for user-scoped API URL paths)
- `process.env['DEV_PROJECT_SLUG'] || 'default-project'` (for project-scoped API URL paths)
- `process.env['DEV_SESSION_SLUG'] || ''` (empty means no seeded session; tests skip if required)

`e2e/.test-user.json` is gitignored and ephemeral — it exists only during a `npx playwright test` invocation.

### Credential file schema

```json
{
  "userId": "<uuid>",
  "email": "e2e-1746123456789@mclaude.local",
  "token": "a3f8c2d1e9b4f7a2",
  "projectSlug": "e2e-default",
  "sessionSlug": "new-session"
}
```

Or when env-var override is active: `{"skipped": true}`.

## Dependencies

| Dependency | Purpose |
|------------|---------|
| React 18 | UI framework |
| NATS WebSocket client | Real-time event streaming and KV state |
| xterm.js | Terminal rendering |
| React Router v6 | Client-side routing |
