# MClaude Functional Test Matrix

Comprehensive test matrix covering every user-facing behavior described in the spec files. Each test case includes a unique ID, description, preconditions, steps, expected result, and deployment feasibility notes.

---

## 1. SPA User Stories (Testable via Browser)

### 1.1 Authentication

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| SPA-AUTH-01 | Login with email/password | User exists in Postgres, SPA loaded | 1. Enter email and password on login form. 2. Submit. | SPA receives JWT, NKey pair generated in browser localStorage, NATS connection established. User redirected to dashboard. | Yes |
| SPA-AUTH-02 | Login generates NKey pair client-side | SPA loaded, no prior session | 1. Open SPA. 2. Log in. 3. Inspect localStorage. | NKey seed exists in localStorage. Login request includes `nkey_public`. Response contains JWT but no `nkeySeed`. | Yes |
| SPA-AUTH-03 | JWT refresh before expiry | User logged in, JWT near expiry (<15 min remaining) | 1. Wait or manipulate time. 2. Observe background refresh. | SPA calls `POST /api/auth/challenge` + `POST /api/auth/verify`, receives new JWT, NATS connection re-authenticated. | Yes (may need mock/accelerated TTL) |
| SPA-AUTH-04 | Session persistence across page reload | User logged in | 1. Reload browser tab. | SPA reconnects to NATS using stored credentials from localStorage. Dashboard state restored via KV watches. | Yes |
| SPA-AUTH-05 | Logout clears credentials | User logged in | 1. Log out. | localStorage cleared (JWT, NKey seed). NATS connection closed. User redirected to login page. | Yes |
| SPA-AUTH-06 | Invalid credentials rejected | SPA loaded | 1. Enter wrong email/password. 2. Submit. | Error message displayed. No JWT issued. No NATS connection. | Yes |
| SPA-AUTH-07 | OAuth provider login | OAuth provider configured (GitHub/GitLab) | 1. Click OAuth provider button. 2. Complete OAuth flow. | User authenticated, JWT issued, redirected to dashboard. | Requires OAuth provider config |

### 1.2 NATS Connection & Reconnection

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| SPA-NATS-01 | Initial NATS WebSocket connection | User authenticated with JWT | 1. Login. | SPA connects to `/nats` WebSocket proxy. NATS connection established with JWT auth. | Yes |
| SPA-NATS-02 | Background reconnect on tab visibility | NATS connection active, tab backgrounded | 1. Background the tab (switch apps on mobile / minimize on desktop). 2. Wait >30s. 3. Return to tab. | `visibilitychange` handler fires. NATS reconnects. KV watches re-established. | Yes (manual or via Playwright) |
| SPA-NATS-03 | Desktop notification on requires_action | Tab not visible, session enters `requires_action` | 1. Background the tab. 2. Trigger a permission prompt in a session. | Browser notification fires: "MClaude — Permission needed". | Yes (Notification API must be granted) |
| SPA-NATS-04 | No notification when tab is foreground | Tab visible, session enters `requires_action` | 1. Keep tab in foreground. 2. Trigger a permission prompt. | No desktop notification fired. Permission prompt shown inline. | Yes |

### 1.3 Session List / Dashboard

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| SPA-DASH-01 | View session list | User logged in, ≥1 session exists | 1. Navigate to dashboard. | Sessions listed with name, status, project, cost. KV watch active. | Yes |
| SPA-DASH-02 | Session status indicator colors | Sessions in various states | 1. View dashboard. | `running` → green dot, `requires_action` → orange pulse, `updating` → blue pulse, `idle` → gray, `paused` → dimmed. | Yes |
| SPA-DASH-03 | Create new session | User logged in, project exists | 1. Click "New Session". 2. Fill session name, select project. 3. Submit. | SPA publishes to `sessions.create` NATS subject. Wait up to 30s for session to appear in KV. Session visible in list. | Yes |
| SPA-DASH-04 | Create session timeout handling | Agent not responding | 1. Attempt to create session when agent is down. | After 30s timeout, error surfaced to user. `api_error` event received. | Requires agent to be down |
| SPA-DASH-05 | Delete session | Session exists, not in critical state | 1. Click delete on a session. | SPA publishes to `sessions.{sslug}.delete`. Session disappears from list after KV tombstone received. | Yes |
| SPA-DASH-06 | Filter sessions by project | ≥2 projects exist, sessions in each | 1. Select project filter dropdown. 2. Choose a specific project. | Only sessions for selected project shown. Filter persisted in `localStorage` key `mclaude.filterProjectId`. | Yes |
| SPA-DASH-07 | Project filter persists across reload | Project filter active | 1. Set a project filter. 2. Reload page. | Filter still active after reload (read from localStorage). | Yes |
| SPA-DASH-08 | Project filter auto-clears on deleted project | Project filter set, filtered project deleted | 1. Set filter to project A. 2. Delete project A. | Filter auto-clears. All sessions shown. | Yes |
| SPA-DASH-09 | Lifecycle notifications on dashboard | Dashboard open | 1. Create/stop/error sessions. | Dashboard receives lifecycle events (`session_started`, `session_stopped`, `session_failed`) via stream consumer. Real-time status updates. | Yes |
| SPA-DASH-10 | Session list shows cost/token usage | Sessions with usage data | 1. View dashboard. | Per-session cost (USD) and token counts visible from KV state. | Yes |
| SPA-DASH-11 | Updating state banner | Session in `updating` state (graceful shutdown) | 1. Trigger graceful shutdown on agent pod. 2. View dashboard. | Session shows "Updating..." label. Blue pulsing status dot. | Requires K8s pod restart |
| SPA-DASH-12 | Create project from SPA | User logged in | 1. Click "New Project". 2. Enter name, optionally git URL. 3. Submit. | SPA publishes to `subjProjectsCreate`. Project appears in KV. **Known bug**: payload may not include `hostSlug`. | Yes (but project may not get agent pod due to known bug) |
| SPA-DASH-13 | Imported sessions appear in list | Imported sessions exist via `mclaude import` | 1. View dashboard. | Imported sessions appear like native sessions. No "imported" badge. | Requires prior import |

### 1.4 Session Detail / Chat View

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| SPA-CHAT-01 | View session conversation | Active session exists | 1. Click on session in list. | Ordered push consumer created for `sessions.{sslug}.>` with DeliverAll. Full conversation history replayed, then live delivery. | Yes |
| SPA-CHAT-02 | Streaming text display | Session running, Claude responding | 1. Send message. 2. Watch response. | `text_delta` events render token-by-token in real-time. | Yes |
| SPA-CHAT-03 | Tool use collapsible block | Claude uses a tool | 1. Send a message that triggers tool use. | `tool_call` → collapsible block with tool name. `tool_progress` → elapsed time. `tool_result` → result inside block. | Yes |
| SPA-CHAT-04 | Permission request approve/deny | Session in `requires_action` | 1. Claude requests permission (e.g., Write). | Approve/deny buttons displayed. Clicking approve sends `permission_response` with `allowed: true` to NATS. | Yes |
| SPA-CHAT-05 | Permission behavior options | Permission prompt displayed | 1. Approve with "Always allow" option. | `permission_response` sent with `behavior: "allow_always"`. Subsequent same-tool prompts auto-approved. | Yes |
| SPA-CHAT-06 | Send chat message | Session idle | 1. Type message in input box. 2. Submit. | `SessionInput` with `type: "message"` published to `sessions.{sslug}.input`. Message appears in conversation. | Yes |
| SPA-CHAT-07 | Message input enabled during updating | Session in `updating` state | 1. Observe input box during graceful shutdown. | Input box remains enabled. Messages queued in JetStream for replacement pod. | Requires K8s pod restart |
| SPA-CHAT-08 | Subagent nesting | Claude uses a sub-agent | 1. Trigger sub-agent use. | Messages with `parentToolUseId` render nested under the parent Agent tool_use block. | Yes |
| SPA-CHAT-09 | Event replay on reconnect | Session has history, NATS connection drops | 1. Disconnect NATS. 2. Reconnect. | SPA replays from `max(lastSeenSeq + 1, replayFromSeq)`. Deduplication by JetStream sequence number. | Yes |
| SPA-CHAT-10 | Compact boundary handling | Session has been compacted | 1. Open session with compact history. | Events before `replayFromSeq` are skipped. No stale content rendered. | Yes |
| SPA-CHAT-11 | Interrupt session | Session running | 1. Click interrupt button. | SPA publishes to `sessions.{sslug}.control.interrupt`. Claude turn ends. `turn_complete` event received. | Yes |
| SPA-CHAT-12 | Restart session | Session exists | 1. Click restart button. | SPA publishes to `sessions.{sslug}.control.restart`. New `init` event received. Session state reset. | Yes |
| SPA-CHAT-13 | Skill invocation | Session idle, skills available | 1. Select a skill from skills picker. | `SessionInput` with `type: "skill_invoke"` sent. Skill name and args included. | Yes |
| SPA-CHAT-14 | Reload plugins (refresh capabilities) | Session active | 1. Click refresh button on skills picker. | `reload_plugins` control request sent. Cached capabilities refreshed from Claude Code. | Yes |
| SPA-CHAT-15 | Model switching mid-session | Session active | 1. Select different model from dropdown. | `set_model` control request sent. `config` message published. Session KV updated with new model. | Yes |
| SPA-CHAT-16 | Thinking effort switching | Session active | 1. Change thinking effort slider. | `set_max_thinking_tokens` control request sent. Takes effect next turn. | Yes |
| SPA-CHAT-17 | Error event rendering | Session encounters error | 1. Trigger an error (e.g., rate limit). | `error` event rendered inline with message and code. `retryable` flag shown. | Yes |
| SPA-CHAT-18 | Init event populates UI | Session starts | 1. Observe session startup. | `init` event populates skills picker, tool list, model info, capabilities in session KV. | Yes |
| SPA-CHAT-19 | Result/usage display | Turn completes | 1. Complete a turn. | `turn_complete` event shows input/output tokens, cost, duration. Cumulative usage in session KV. | Yes |
| SPA-CHAT-20 | KV DEL/PURGE handling | Session deleted while viewing | 1. Delete a session from another client. | KV watch receives DEL operation. Session removed from `_sessions` map. UI reflects deletion. | Yes |
| SPA-CHAT-21 | Backend-specific event rendering | Session uses Droid backend | 1. Create session with `backend: "droid"`. | `backend_specific` events rendered per-backend (e.g., mission notifications). | Requires Droid backend |

### 1.5 Terminal View

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| SPA-TERM-01 | Open terminal session | Session active, terminal supported | 1. Click Terminal tab. | SPA publishes to `api.terminal.create`. PTY shell spawned. xterm.js renders output. | Yes |
| SPA-TERM-02 | Terminal input/output | Terminal session open | 1. Type commands. | Input sent via `api.terminal.{termId}.input`. Output received via `api.terminal.{termId}.output` (4KB chunks). | Yes |
| SPA-TERM-03 | Terminal resize | Terminal session open | 1. Resize browser window. | SPA publishes to `api.terminal.resize`. PTY dimensions updated. | Yes |
| SPA-TERM-04 | Close terminal | Terminal session open | 1. Click close. | SPA publishes to `api.terminal.delete`. PTY closed, process killed. | Yes |

### 1.6 Attachments / File Upload

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| SPA-ATT-01 | Upload file via file picker | Session active, S3 configured | 1. Click file picker. 2. Select file. | SPA requests `POST /api/attachments/upload-url`. Receives `{id, uploadUrl}`. Uploads directly to S3. Confirms via `POST /api/attachments/{id}/confirm`. `AttachmentRef` attached to message. | Requires S3 |
| SPA-ATT-02 | Paste image upload | Session active, S3 configured | 1. Paste image from clipboard. | Same flow as SPA-ATT-01 but triggered by paste handler. | Requires S3 |
| SPA-ATT-03 | File size limit enforcement | S3 configured | 1. Attempt to upload file >50MB. | CP rejects at `upload-url` step. Error surfaced to user. | Requires S3 |
| SPA-ATT-04 | Render inline image attachment | Event contains `attachment_ref` with image MIME type | 1. View message with image attachment. | SPA requests `GET /api/attachments/{id}`. Renders inline `<img>` with download URL as src. | Requires S3 |
| SPA-ATT-05 | Render file attachment as download link | Event contains `attachment_ref` with non-image MIME | 1. View message with file attachment. | Download link rendered with filename and size. | Requires S3 |
| SPA-ATT-06 | Download URL expiry refresh | Download URL >5 min old | 1. Wait for URL to expire. 2. Click attachment. | SPA re-requests fresh download URL from CP. | Requires S3 |

### 1.7 Device-Code Verification Page

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| SPA-DEV-01 | Verify device code page loads | SPA deployed, device code pending | 1. Navigate to `/auth/device-code/verify`. | Minimal page with code-entry field and "Approve" button. | Yes |
| SPA-DEV-02 | Code pre-filled from URL param | Device code generated | 1. Navigate to `/auth/device-code/verify?code=ABCD`. | Code field pre-filled with "ABCD". | Yes |
| SPA-DEV-03 | Manual code entry | SPA loaded at verify page | 1. Type device code manually. 2. Click Approve. | Code submitted. On success, "CLI is now authorized" displayed. | Yes |
| SPA-DEV-04 | Unauthenticated user redirected | User not logged in | 1. Navigate to verify page without being logged in. | Redirected to login first, then back to verify page. | Yes |
| SPA-DEV-05 | Expired device code | Code >15 min old | 1. Enter expired code. 2. Approve. | Error state displayed: code expired. | Yes |

### 1.8 Settings / Provider Management

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| SPA-SET-01 | View connected providers | User logged in, ≥1 provider connected | 1. Navigate to settings. | Connected providers listed (GitHub/GitLab) with display names. | Yes |
| SPA-SET-02 | Connect OAuth provider | OAuth provider configured | 1. Click connect for a provider. 2. Complete OAuth flow. | Provider appears in connected list. Credentials stored. | Requires OAuth config |
| SPA-SET-03 | Add Personal Access Token | Provider supports PAT | 1. Enter PAT. 2. Submit. | Auto-detection of provider type (GitHub/GitLab). Connection created. | Yes |
| SPA-SET-04 | Disconnect provider | Provider connected | 1. Click disconnect. | `DELETE /api/connections/{id}`. Token revoked, secret removed, DB row deleted. | Yes |
| SPA-SET-05 | List repos for connected provider | Provider connected | 1. Navigate to repo listing for a connection. | `GET /api/connections/{id}/repos` returns accessible repos. | Requires OAuth connection |

### 1.9 Host Management (SPA)

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| SPA-HOST-01 | View host list and online status | User has ≥1 host | 1. View dashboard. | Hosts listed from `mclaude-hosts` KV. Online/offline indicator updated in real-time. | Yes |
| SPA-HOST-02 | Host goes offline reflected in SPA | Host online, controller disconnects | 1. Stop host controller. 2. Observe SPA. | KV watch delivers `online: false`. Host shows offline. | Yes |
| SPA-HOST-03 | Host goes online reflected in SPA | Host offline, controller connects | 1. Start host controller. | KV watch delivers `online: true`. Host shows online. | Yes |

### 1.10 Routing

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| SPA-ROUTE-01 | Parametric routing | Session exists | 1. Navigate to `/api/users/{uslug}/projects/{pslug}/sessions/{sslug}`. | Correct session detail page rendered. Display names in UI, slugs in URL. | Yes |
| SPA-ROUTE-02 | Deep link to session | Valid session URL | 1. Open deep link directly. | Session loads with full replay. | Yes |
| SPA-ROUTE-03 | Device-code route | N/A | 1. Navigate to `/auth/device-code/verify`. | Verification page rendered (standalone, no navigation chrome). | Yes |

### 1.11 Quota-Managed Session UI

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| SPA-QUOTA-01 | Display paused sessions | Quota-managed sessions exist, some paused | 1. View dashboard. | Paused sessions show pause icon. `pausedVia` label visible (e.g., "quota_soft"). | Yes |
| SPA-QUOTA-02 | Display needs_spec_fix status | Session in `needs_spec_fix` state | 1. View session in dashboard. | Shows "blocked on: {failedTool}" from KV. | Yes |
| SPA-QUOTA-03 | Display resume countdown | Session with `autoContinue` and `resumeAt` set | 1. View session. | Countdown timer showing time until `resumeAt`. | Yes |
| SPA-QUOTA-04 | Distinguish quota-managed sessions | Mix of interactive and quota-managed sessions | 1. View dashboard. | Quota-managed sessions identifiable by `softThreshold > 0`. Grouped/dimmed if paused. | Yes |

---

## 2. API User Stories (Testable via curl/HTTP)

### 2.1 Public Endpoints (No Auth)

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| API-PUB-01 | Health check | CP running | `curl GET /health` | HTTP 200 OK. Never checks NATS. | Yes |
| API-PUB-02 | Liveness probe | CP running | `curl GET /healthz` | HTTP 200 OK. Same as health. | Yes |
| API-PUB-03 | Readiness probe (healthy) | CP + Postgres running | `curl GET /readyz` | HTTP 200 OK. Postgres connectivity checked. | Yes |
| API-PUB-04 | Readiness probe (DB down) | CP running, Postgres down | `curl GET /readyz` | HTTP 503 Service Unavailable. | Requires Postgres outage |
| API-PUB-05 | Version endpoint | CP running | `curl GET /version` | Returns `{minClientVersion, serverVersion}`. | Yes |
| API-PUB-06 | Login | User exists | `curl POST /auth/login` with `{email, password, nkey_public}` | Returns JWT, userId, userSlug, natsUrl, expiresAt. No `nkeySeed` in response. | Yes |
| API-PUB-07 | Login invalid credentials | CP running | `curl POST /auth/login` with wrong password | HTTP 401 Unauthorized. | Yes |
| API-PUB-08 | JWT refresh | Valid JWT in Authorization header | `curl POST /auth/refresh` with JWT | New JWT returned. `natsWsURL` included. | Yes |
| API-PUB-09 | Challenge-response auth step 1 | NKey registered in DB | `curl POST /api/auth/challenge` with `{nkey_public}` | Returns `{challenge}` (random nonce, 30s TTL). | Yes |
| API-PUB-10 | Challenge-response auth step 2 | Valid challenge obtained | `curl POST /api/auth/verify` with `{nkey_public, challenge, signature}` | Returns `{ok: true, jwt}`. | Yes |
| API-PUB-11 | Challenge expired | Challenge >30s old | `curl POST /api/auth/verify` with expired challenge | Returns `{ok: false, code: "EXPIRED"}`. | Yes |
| API-PUB-12 | Challenge invalid signature | Valid challenge | `curl POST /api/auth/verify` with bad signature | Returns `{ok: false, code: "UNAUTHORIZED"}`. | Yes |
| API-PUB-13 | Challenge unknown public key | No such key in DB | `curl POST /api/auth/challenge` with unknown key | Returns `{ok: false, code: "NOT_FOUND"}`. | Yes |
| API-PUB-14 | Device code initiation | CP running | `curl POST /api/auth/device-code` with `{publicKey}` | Returns `{deviceCode, userCode, verificationUrl, expiresIn, interval}`. | Yes |
| API-PUB-15 | Device code poll (pending) | Device code generated, not approved | `curl POST /api/auth/device-code/poll` with `{deviceCode}` | Returns `{status: "pending"}`. | Yes |
| API-PUB-16 | Device code poll (approved) | Device code approved by user | `curl POST /api/auth/device-code/poll` | Returns `{jwt, userSlug}`. | Yes |
| API-PUB-17 | Device code poll (expired) | Device code >15 min old | `curl POST /api/auth/device-code/poll` | HTTP 410 Gone. | Yes |

### 2.2 Protected Endpoints (JWT Required)

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| API-AUTH-01 | Get authenticated user info | Valid JWT | `curl GET /auth/me` with JWT | Returns user info and connected OAuth providers. | Yes |
| API-AUTH-02 | List OAuth providers | Valid JWT, providers configured | `curl GET /api/providers` | Returns admin-configured providers list. | Yes |
| API-AUTH-03 | Add PAT connection | Valid JWT | `curl POST /api/providers/pat` with PAT | Auto-detects GitHub/GitLab. Connection created. | Yes |
| API-AUTH-04 | Connect OAuth provider | Valid JWT, provider configured | `curl POST /api/providers/{id}/connect` | Returns `{redirectUrl}`. | Requires OAuth config |
| API-AUTH-05 | List repos for connection | Valid JWT, connection exists | `curl GET /api/connections/{id}/repos` | Returns repo list from provider. | Requires OAuth connection |
| API-AUTH-06 | Disconnect provider | Valid JWT, connection exists | `curl DELETE /api/connections/{id}` | Connection removed. Token revoked. | Yes |
| API-AUTH-07 | Update project git identity | Valid JWT, project exists | `curl PATCH /api/projects/{id}` with `{gitIdentityId}` | Project's `gitIdentityId` updated. | Yes |

### 2.3 Attachment HTTP Endpoints

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| API-ATT-01 | Request upload URL | Valid JWT, project exists, S3 configured | `curl POST /api/attachments/upload-url` with `{filename, mimeType, sizeBytes, projectSlug, hostSlug}` | Returns `{id, uploadUrl}`. Upload URL valid for 5 min. | Requires S3 |
| API-ATT-02 | Upload URL — size limit exceeded | S3 configured | `curl POST /api/attachments/upload-url` with `sizeBytes > 50MB` | HTTP 400 Bad Request. Size limit enforced. | Requires S3 |
| API-ATT-03 | Confirm upload | Attachment uploaded to S3 | `curl POST /api/attachments/{id}/confirm` | Attachment marked `confirmed=true` in Postgres. | Requires S3 |
| API-ATT-04 | Get download URL | Attachment confirmed | `curl GET /api/attachments/{id}` | Returns `{id, filename, mimeType, sizeBytes, downloadUrl}`. | Requires S3 |
| API-ATT-05 | Download URL — wrong project | JWT for user A, attachment in user B's project | `curl GET /api/attachments/{id}` | HTTP 403 Forbidden. | Requires S3 |

### 2.4 Project CRUD (HTTP)

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| API-PROJ-01 | Create project (HTTP) | Valid JWT, host exists | `curl POST /api/users/{uslug}/projects` with `{slug, hostSlug, ...}` | Postgres row created. **Known gap**: KV write + provisioning may not fire. | Yes |
| API-PROJ-02 | List projects | Valid JWT | `curl GET /api/users/{uslug}/projects` | Returns array of projects for user. | Yes |
| API-PROJ-03 | Get single project | Valid JWT, project exists | `curl GET /api/users/{uslug}/projects/{pslug}` | Returns project details. | Yes |
| API-PROJ-04 | Delete project | Valid JWT, project exists | `curl DELETE /api/users/{uslug}/projects/{pslug}` | Project deleted from Postgres. S3 prefix cleaned up. | Yes |

### 2.5 Host Management (HTTP)

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| API-HOST-01 | List user's hosts | Valid JWT | `curl GET /api/users/{uslug}/hosts` | Returns owned + granted hosts. | Yes |
| API-HOST-02 | Create host directly (bypass device-code) | Valid JWT | `curl POST /api/users/{uslug}/hosts` with `{slug, name, publicKey?}` | Host row created. JWT minted. | Yes |
| API-HOST-03 | Generate host device code | Valid JWT | `curl POST /api/users/{uslug}/hosts/code` with `{publicKey}` | Returns `{code, expiresAt}`. 10-min TTL. | Yes |
| API-HOST-04 | Poll host device code (pending) | Code generated, not redeemed | `curl GET /api/users/{uslug}/hosts/code/{code}` | Returns `{status: "pending"}`. | Yes |
| API-HOST-05 | Redeem host device code | Code generated by authenticated user | `curl POST /api/hosts/register` with `{code, name}` | Host created. Returns `{slug, jwt, hubUrl}`. | Yes |
| API-HOST-06 | Redeem expired host code | Code >10 min old | `curl POST /api/hosts/register` with expired code | HTTP 410 Gone. | Yes |
| API-HOST-07 | Redeem already-used host code | Code already redeemed | `curl POST /api/hosts/register` with used code | HTTP 409 Conflict. | Yes |
| API-HOST-08 | Update host name | Valid JWT, user owns host | `curl PUT /api/users/{uslug}/hosts/{hslug}` | Display name updated. | Yes |
| API-HOST-09 | Delete host | Valid JWT, user owns host | `curl DELETE /api/users/{uslug}/hosts/{hslug}` | Host removed. Cascades to projects + sessions. | Yes |

### 2.6 Admin Endpoints (Loopback Port 9091)

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| API-ADMIN-01 | Prometheus metrics | CP running, ADMIN_TOKEN set | `curl GET localhost:9091/metrics` | Prometheus metrics returned. | Yes |
| API-ADMIN-02 | Register cluster | ADMIN_TOKEN set | `curl POST localhost:9091/admin/clusters` with Bearer token, `{slug, name, jsDomain, leafUrl}` | Cluster host created. Returns `{slug, leafJwt, leafSeed, jsDomain}`. | Yes |
| API-ADMIN-03 | List clusters | ADMIN_TOKEN set | `curl GET localhost:9091/admin/clusters` | Returns registered clusters (deduplicated). | Yes |
| API-ADMIN-04 | Grant cluster access | ADMIN_TOKEN set, cluster exists | `curl POST localhost:9091/admin/clusters/{cslug}/grants` with `{userSlug}` | New host row for user. Per-user JWT minted. | Yes |
| API-ADMIN-05 | Create admin user | ADMIN_TOKEN set | `curl POST localhost:9091/admin/users` with `{id, email, name, password?}` | User created. **Known gap**: no `isAdmin` field — always non-admin. | Yes |
| API-ADMIN-06 | List users (admin) | ADMIN_TOKEN set | `curl GET localhost:9091/admin/users` | All users returned. | Yes |
| API-ADMIN-07 | Delete user (admin) | ADMIN_TOKEN set, user exists | `curl DELETE localhost:9091/admin/users/{id}` | User deleted. Cascades to hosts, projects, connections. **Known gap**: NATS JWT not revoked. | Yes |
| API-ADMIN-08 | Admin endpoint without token | ADMIN_TOKEN set | `curl POST localhost:9091/admin/clusters` without Bearer token | HTTP 401/403. | Yes |
| API-ADMIN-09 | Duplicate cluster slug | Cluster with slug exists | `curl POST localhost:9091/admin/clusters` with existing slug | HTTP 409 Conflict. | Yes |
| API-ADMIN-10 | Duplicate user email | User with email exists | `curl POST localhost:9091/admin/users` with existing email | HTTP 409 Conflict. | Yes |

---

## 3. NATS User Stories (Testable via nats CLI or Code)

### 3.1 Session Create / Delete / Restart

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| NATS-SESS-01 | Create interactive session | Agent running, project provisioned | Publish to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.create` with `{sessionSlug, backend: "claude_code", model: "sonnet"}` | Session KV entry created with `status: idle`. `session_created` lifecycle event published. CLI process spawned. | Yes |
| NATS-SESS-02 | Create session with backend selection | Agent running, multiple drivers registered | Publish `sessions.create` with `backend: "droid"` | Session uses DroidDriver. KV shows `backend: "droid"`. Capabilities reflect Droid features. | Requires Droid binary |
| NATS-SESS-03 | Delete interactive session | Session exists | Publish to `sessions.{sslug}.delete` with `{}` | CLI process interrupted (10s grace, then SIGKILL). KV entry tombstoned. `session_stopped` lifecycle event. Worktree removed if last session on branch. | Yes |
| NATS-SESS-04 | Restart session | Session exists | Publish to `sessions.{sslug}.control.restart` with `{}` | Existing process killed. New process spawned (with `--resume` if `claudeSessionID` available). `session_restarting` then `session_resumed` lifecycle events. | Yes |
| NATS-SESS-05 | Session input (message) | Session active | Publish to `sessions.{sslug}.input` with `{type: "message", text: "hello"}` | Agent receives via stream consumer. Message forwarded to CLI process via `driver.SendMessage`. | Yes |
| NATS-SESS-06 | Session input (permission response) | Permission prompt pending | Publish to `sessions.{sslug}.input` with `{type: "permission_response", requestId, allowed: true}` | Agent resolves permission via `driver.SendPermissionResponse`. `permission` event with `resolved: true` published. | Yes |
| NATS-SESS-07 | Session config update (model switch) | Session active | Publish to `sessions.{sslug}.config` with `{model: "opus"}` | Agent calls `driver.UpdateConfig`. Session KV updated with new model. | Yes |
| NATS-SESS-08 | Session interrupt | Session running | Publish to `sessions.{sslug}.control.interrupt` with `{}` | Agent sends SIGINT to CLI. `turn_complete` event published. State → idle. | Yes |
| NATS-SESS-09 | Session create — worktree collision | Branch already in use by another session | Publish `sessions.create` with conflicting branch, `joinWorktree: false` | `api_error` event published to `sessions._api` with `operation: "create"`. | Yes |
| NATS-SESS-10 | Session create — strict-allowlist with empty allowedTools | Agent running | Publish `sessions.create` with `permPolicy: "strict-allowlist", allowedTools: []` | Agent rejects. `lifecycle.error` published: "strict-allowlist requires non-empty allowedTools". No KV entry. | Yes |

### 3.2 Event Streaming

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| NATS-EVT-01 | Session events captured by stream | Session active | Publish message to session, observe events | All `sessions.{sslug}.events` messages captured by `MCLAUDE_SESSIONS_{uslug}` stream. Replayed by consumers. | Yes |
| NATS-EVT-02 | Event types: init | Session starts | Observe events | `init` event published with backend, model, tools, skills, agents, capabilities. | Yes |
| NATS-EVT-03 | Event types: text_delta | Claude responding | Observe events | `text_delta` events with `messageId`, `blockIndex`, `text`. | Yes |
| NATS-EVT-04 | Event types: tool_call + tool_result | Claude uses tool | Observe events | `tool_call` with `toolUseId`, `toolName`, `input`. `tool_result` with `content`, `isError`. | Yes |
| NATS-EVT-05 | Event types: turn_complete | Turn finishes | Observe events | `turn_complete` with `inputTokens`, `outputTokens`, `costUsd`, `durationMs`. | Yes |
| NATS-EVT-06 | Event types: state_change | Session state changes | Observe events | `state_change` events with `state`: `idle`, `running`, `requires_action`. | Yes |
| NATS-EVT-07 | Event truncation >8MB payload | Very large tool result | Observe events | Content field stripped. `truncated: true` added to event. | Hard to trigger naturally |
| NATS-EVT-08 | Lifecycle events captured by stream | Session lifecycle changes | Create/stop/restart sessions | `lifecycle.started`, `lifecycle.stopped`, `lifecycle.error` captured by `MCLAUDE_SESSIONS_{uslug}`. | Yes |
| NATS-EVT-09 | Input messages captured by stream | User sends input | Publish to `sessions.{sslug}.input` | Input messages captured by `MCLAUDE_SESSIONS_{uslug}` (subject matches `sessions.>` filter). | Yes |

### 3.3 KV State Reads/Writes

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| NATS-KV-01 | Session KV initial write on create | Agent running | Create a session | KV entry at `hosts.{hslug}.projects.{pslug}.sessions.{sslug}` with status, backend, model, capabilities. | Yes |
| NATS-KV-02 | Session KV state transitions | Session active | Trigger state changes (send message, permission prompt) | KV updated on every transition: `idle` → `running` → `requires_action` → `running` → `idle`. | Yes |
| NATS-KV-03 | Session KV usage accumulation | Complete a turn | Observe KV after turn_complete | `usage` fields updated: `inputTokens`, `outputTokens`, `cacheReadTokens`, `cacheWriteTokens`, `costUsd`. | Yes |
| NATS-KV-04 | Session KV replayFromSeq updated on compaction | Compaction boundary event | Observe KV | `replayFromSeq` updated to current stream sequence. | Yes |
| NATS-KV-05 | Project KV written on create | Project created | Read `mclaude-projects-{uslug}` KV | Entry at `hosts.{hslug}.projects.{pslug}` with id, slug, name, gitUrl, status. | Yes |
| NATS-KV-06 | Host KV updated on CONNECT | Host controller connects to NATS | Read `mclaude-hosts` KV key `{hslug}` | Entry with `online: true`, `lastSeenAt` updated. | Yes |
| NATS-KV-07 | Host KV updated on DISCONNECT | Host controller disconnects | Read `mclaude-hosts` KV key `{hslug}` | `online: false`. `lastSeenAt` NOT updated (preserved from last CONNECT). | Yes |
| NATS-KV-08 | Per-user KV bucket isolation | Multiple users | User A reads user B's KV bucket | NATS permission violation. User A's JWT scoped to own buckets only. | Requires multi-user setup |
| NATS-KV-09 | Agent KV write scope | Agent running | Agent attempts to write to another project's session key | NATS permission violation. Agent JWT scoped to own project only. | Requires multi-project setup |
| NATS-KV-10 | Session KV deletion on session stop | Session deleted | Read KV after deletion | KV entry tombstoned (DEL operation). Entry removed from store. | Yes |

### 3.4 Lifecycle Events

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| NATS-LIFE-01 | session_created | Session created | Subscribe to `sessions.{sslug}.lifecycle.*` | `session_created` event received with `sessionId`, `ts`. | Yes |
| NATS-LIFE-02 | session_stopped | Session deleted | Subscribe to lifecycle | `session_stopped` event with `sessionId`, `ts`. | Yes |
| NATS-LIFE-03 | session_restarting | Session restarted | Subscribe to lifecycle | `session_restarting` event before restart. | Yes |
| NATS-LIFE-04 | session_resumed | Session restarted successfully | Subscribe to lifecycle | `session_resumed` event after restart. | Yes |
| NATS-LIFE-05 | session_failed | CLI process crashes | Subscribe to lifecycle | `session_failed` event with `error` field. | Yes |
| NATS-LIFE-06 | session_upgrading | Graceful shutdown (SIGTERM) | Subscribe to lifecycle | `session_upgrading` event per session after all reach idle. | Requires pod restart |
| NATS-LIFE-07 | debug_attached / debug_detached | CLI attach to debug socket | Subscribe to lifecycle | `debug_attached` on connect, `debug_detached` on disconnect. | Yes |
| NATS-LIFE-08 | api_error on create failure | Create session with invalid params | Subscribe to `sessions._api` | `api_error` event with `operation: "create"`, `error` description. | Yes |
| NATS-LIFE-09 | api_error on delete failure | Delete non-existent session | Subscribe to `sessions._api` | `api_error` event with `operation: "delete"`. | Yes |

### 3.5 Quota-Managed Sessions (NATS)

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| NATS-QUOTA-01 | Quota-managed session pending | Agent running, quota status available | Create session with `softThreshold: 75`, initial `u5: 80%` | Session KV → `status: pending`. CLI subprocess alive but prompt not sent. | Requires Anthropic API (or mock) |
| NATS-QUOTA-02 | Quota-managed session prompt delivery | Quota recovers below threshold | Quota update arrives with `u5: 40%` (< 75%) | Prompt delivered via `sessions.{sslug}.input`. KV → `status: running`. | Requires quota source |
| NATS-QUOTA-03 | Soft pause on quota pressure | Running session, quota exceeds threshold | Quota update with `u5: 76%` (≥ 75%) | `MCLAUDE_STOP: quota_soft` injected. After turn-end: `session_job_paused` lifecycle event. KV → `status: paused`, `pausedVia: quota_soft`. | Requires quota source |
| NATS-QUOTA-04 | Hard interrupt on token budget | Soft-stopped but Claude keeps generating | Output tokens exceed `hardHeadroomTokens` | `control_request` interrupt sent on `stdinCh`. `session_job_paused` with `pausedVia: quota_hard`. | Requires quota source |
| NATS-QUOTA-05 | Auto-resume on quota recovery | Paused session with `autoContinue: true` | Quota update with `u5 < softThreshold` | Resume nudge sent. KV → `status: running`, `pausedVia: ""`. | Requires quota source |
| NATS-QUOTA-06 | Session completion | Claude ends naturally | No stop reason | `session_job_complete` lifecycle event. KV → `status: completed`. | Yes |
| NATS-QUOTA-07 | Session cancellation | User deletes quota-managed session | Publish `sessions.{sslug}.delete` | `session_job_cancelled` lifecycle event. KV tombstoned. | Yes |
| NATS-QUOTA-08 | Permission denied (strict-allowlist) | Out-of-allowlist tool requested | Claude requests unlisted tool | `session_permission_denied` lifecycle event with `tool` name. KV → `status: needs_spec_fix`, `failedTool` set. | Yes |
| NATS-QUOTA-09 | Subprocess exit without turn-end | CLI crashes mid-session | Kill CLI subprocess | `session_job_failed` lifecycle event. KV → `status: failed`. | Yes |
| NATS-QUOTA-10 | QuotaStatus with hasData: false | API call fails | Publish `mclaude.users.{uslug}.quota` with `hasData: false` | No action taken. Running sessions continue. Paused sessions stay paused. Pending sessions wait. | Yes |
| NATS-QUOTA-11 | Quota publisher designation | Agent registers | Observe `api.agents.register` reply | Reply includes `quotaPublisher: true/false`. Designated agent starts `runQuotaPublisher`. | Yes |
| NATS-QUOTA-12 | Quota publisher re-designation | Designated agent disconnects | CP publishes to `manage.designate-quota-publisher` with `{quotaPublisher: true}` | New agent starts `runQuotaPublisher`. Old agent (if reconnected) stops its publisher. | Requires multi-agent setup |

### 3.6 Import Subjects (NATS)

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| NATS-IMP-01 | Check slug availability | User authenticated via NATS | Request `mclaude.users.{uslug}.hosts.{hslug}.projects.check-slug` with `{slug}` | Reply: `{available: true}` or `{available: false, suggestion: "slug-2"}`. | Yes |
| NATS-IMP-02 | Request import upload URL | User authenticated, S3 configured | Request `import.request` with `{slug, sizeBytes}` | Reply: `{importId, uploadUrl}` (5-min pre-signed S3 PUT URL). | Requires S3 |
| NATS-IMP-03 | Confirm import upload | Archive uploaded to S3 | Publish `import.confirm` with `{importId}` | CP creates project in Postgres with `source: "import"`. Writes project KV with `importRef`. Dispatches provisioning. | Requires S3 |
| NATS-IMP-04 | Agent downloads import | Agent starts with `importRef` in project KV | Agent requests `import.download` with `{importId}` | Reply: `{downloadUrl}` (5-min pre-signed S3 GET URL). Agent unpacks archive. | Requires S3 |
| NATS-IMP-05 | Agent signals import complete | Archive unpacked successfully | Agent publishes `import.complete` with `{id, ts}` | CP receives signal, deletes S3 object. Agent clears `importRef` from project KV. | Requires S3 |
| NATS-IMP-06 | Import size limit | S3 configured | Request `import.request` with `sizeBytes > 500MB` | CP rejects. Error reply returned. | Requires S3 |

### 3.7 Attachment Subjects (NATS — Agent Side)

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| NATS-ATT-01 | Agent downloads attachment | User message contains `AttachmentRef` | Agent requests `attachments.download` with `{attachmentId}` | Reply: `{downloadUrl}`. Agent downloads from S3. | Requires S3 |
| NATS-ATT-02 | Agent uploads attachment | Agent generates binary output | Agent requests `attachments.upload` with `{filename, mimeType, sizeBytes}` | Reply: `{id, uploadUrl}`. Agent uploads to S3. Confirms via `attachments.confirm`. | Requires S3 |
| NATS-ATT-03 | Agent attachment ownership validation | Agent requests attachment from another project | Agent sends `attachments.download` for foreign attachment | CP validates project scope against agent JWT. Returns error. | Requires S3 + multi-project |

### 3.8 Host Management Subjects (NATS)

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| NATS-HOST-01 | Register host | User authenticated | Request `mclaude.users.{uslug}.hosts._.register` with `{name, type: "machine", nkeyPublic}` | Reply: `{ok: true, slug}`. Host created in Postgres. | Yes |
| NATS-HOST-02 | Grant host access | Host owner | Request `manage.grant` with `{userSlug}` | Reply: `{ok: true}`. `host_access` row inserted. Grantee's JWT revoked. | Requires multi-user |
| NATS-HOST-03 | Revoke host access | Host owner, access granted | Request `manage.revoke-access` with `{userSlug}` | Reply: `{ok: true}`. `host_access` row deleted. Grantee's JWT + agent JWTs revoked. | Requires multi-user |
| NATS-HOST-04 | Deregister host | Host owner | Request `manage.deregister` | Projects drained. Host KV tombstoned. Postgres row deleted. NKey public key removed. | Yes |
| NATS-HOST-05 | Emergency credential revocation | Host owner | Request `manage.revoke` | Host JWT added to NATS revocation list. Immediate disconnect. Host marked `revoked`. | Yes |
| NATS-HOST-06 | Rekey host | Host owner | Request `manage.rekey` with `{nkeyPublic: "UNEW..."}` | Public key updated in Postgres. Old JWT useless (nonce challenge fails). | Yes |
| NATS-HOST-07 | Update host metadata | Host owner | Request `manage.update` with `{name: "New Name"}` | Host name updated in Postgres + KV. | Yes |

### 3.9 Agent Registration (NATS)

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| NATS-AGENT-01 | Register agent public key | Host controller, project provisioned | Request `mclaude.hosts.{hslug}.api.agents.register` with `{userSlug, projectSlug, nkeyPublic}` | Reply: `{ok: true, quotaPublisher}`. Public key stored in `agent_credentials`. | Yes |
| NATS-AGENT-02 | Register agent — project not found (race) | Project create not yet processed | Request `api.agents.register` | Reply: `{ok: false, code: "NOT_FOUND"}`. Host controller retries with backoff. | Yes |
| NATS-AGENT-03 | Register agent — unauthorized host | Host controller for wrong host | Request from unauthorized host | Reply: `{ok: false, code: "FORBIDDEN"}`. | Requires multi-host |

---

## 4. CLI User Stories (Testable via mclaude Binary)

### 4.1 mclaude login

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| CLI-LOGIN-01 | Device-code login flow | CP running, user exists | 1. Run `mclaude login`. 2. Copy verification URL. 3. Open in browser, enter code. | NKey pair generated. Credentials written to `~/.mclaude/auth.json` (mode 0600). JWT + NKey seed + userSlug stored. | Yes |
| CLI-LOGIN-02 | Login with custom server | CP at custom URL | Run `mclaude login --server https://custom.url` | Device-code request sent to custom server URL. | Yes |
| CLI-LOGIN-03 | Re-login overwrites credentials | Previously logged in | Run `mclaude login` again | New NKey pair generated. `auth.json` overwritten with new JWT + seed. | Yes |
| CLI-LOGIN-04 | Login server from context file | `~/.mclaude/context.json` has `server` key | Run `mclaude login` without `--server` | Server URL read from context file. | Yes |

### 4.2 mclaude import

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| CLI-IMP-01 | Import from CWD | Logged in, host registered, Claude Code data at `~/.claude/projects/{encoded-cwd}/` | 1. `cd /path/to/project`. 2. Run `mclaude import`. | CWD encoded, session data discovered (JSONL, subagents, memory). Archive created. Slug checked via NATS. Upload URL obtained. Archive uploaded to S3. Confirmed. | Requires S3 + Claude Code data |
| CLI-IMP-02 | Import — slug taken | Logged in, slug already exists | 1. Run `mclaude import`. | Slug check returns `available: false`. User prompted for new name. Re-checks until available. | Requires existing project |
| CLI-IMP-03 | Import — no Claude Code data | Logged in, no data at derived path | 1. `cd /nonexistent/project`. 2. Run `mclaude import`. | CLI errors with hint, lists available encoded directories. | Yes |
| CLI-IMP-04 | Import with host override | Multiple hosts | 1. Run `mclaude import --host other-host`. | Import directed to specified host. | Requires multiple hosts |
| CLI-IMP-05 | Import — not logged in | No auth.json | 1. Run `mclaude import`. | Error: not logged in. Instructions to run `mclaude login`. | Yes |

### 4.3 mclaude attach

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| CLI-ATT-01 | Attach to session | Session agent running, unix socket at `/tmp/mclaude-session-{id}.sock` | Run `mclaude-cli attach <session-id>` | Interactive REPL. Events rendered as human-readable text. User input sent as messages. | Yes |
| CLI-ATT-02 | Attach with custom socket path | Session agent running at custom path | Run `mclaude-cli attach <id> --socket /custom/path.sock` | Connects to specified socket path. | Yes |
| CLI-ATT-03 | Permission handling in REPL | Permission prompt pending | 1. Attach. 2. See permission prompt. 3. Type `y`. | `y`/`n` input sent as allow/deny control response (not chat message). | Yes |
| CLI-ATT-04 | Streaming text rendering | Session running, Claude responding | 1. Attach. 2. Send message. | `text_delta` events render token-by-token. `message` events render complete messages. Tool use blocks rendered. | Yes |
| CLI-ATT-05 | Compact boundary rendering | Compact event received | 1. Attach during or after compaction. | Accumulator resets. `--- context compacted ---` divider rendered. | Yes |
| CLI-ATT-06 | Socket disconnect | Socket drops mid-session | 1. Kill session agent while attached. | CLI exits immediately. No reconnection attempt. | Yes |
| CLI-ATT-07 | Subagent nesting in REPL | Claude spawns sub-agent | 1. Observe events with `parentToolUseId`. | Nested turns rendered under parent agent block. | Yes |
| CLI-ATT-08 | Machine-readable logging | N/A | Run `mclaude-cli attach <id> --log-machine --log-level debug` | Structured JSON logs on stderr. Debug-level messages included. | Yes |

### 4.4 mclaude session list

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| CLI-LIST-01 | List sessions with defaults | `context.json` has userSlug + projectSlug | Run `mclaude-cli session list` | Resolved user and project slugs printed. KV key prefix outputted. | Yes |
| CLI-LIST-02 | List sessions with flags | N/A | Run `mclaude-cli session list -u alice -p @myproject` | Overrides context defaults. `@pslug` short form accepted. | Yes |
| CLI-LIST-03 | List sessions — no context file | No `~/.mclaude/context.json` | Run `mclaude-cli session list` without flags | Error or empty defaults. User informed that flags are required. | Yes |

### 4.5 mclaude host commands

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| CLI-HOST-01 | Register host | Logged in, CP running | Run `mclaude host register --name "My Laptop"` | NKey pair generated locally. Device code obtained from CP. User prompted to enter code in dashboard. On approval: `nats.creds`, `config.json` written to `~/.mclaude/hosts/{hslug}/`. Active-host symlink set. | Yes |
| CLI-HOST-02 | Register host with default name | Logged in | Run `mclaude host register` (no --name) | Hostname used as default name (slugified). | Yes |
| CLI-HOST-03 | List hosts | Logged in | Run `mclaude host list` | Table printed: slug, name, type, role, online status. | Yes |
| CLI-HOST-04 | Set active host | ≥2 hosts registered | Run `mclaude host use <hslug>` | `~/.mclaude/active-host` symlink updated to point at `~/.mclaude/hosts/{hslug}/`. | Yes |
| CLI-HOST-05 | Remove host | Host registered | Run `mclaude host rm <hslug>` | `DELETE /api/users/{uslug}/hosts/{hslug}` called. Local `~/.mclaude/hosts/{hslug}/` removed. Active-host symlink removed if was active. | Yes |

### 4.6 mclaude daemon

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| CLI-DAEMON-01 | Start daemon | Host registered, active-host set | Run `mclaude daemon` | Connects to hub NATS. Subscribes to `mclaude.hosts.{hslug}.>`. Starts session-agent subprocesses for provisioned projects. | Yes |
| CLI-DAEMON-02 | Daemon with explicit host | Multiple hosts | Run `mclaude daemon --host specific-host` | Controller uses specified host slug (not active-host). | Yes |
| CLI-DAEMON-03 | Daemon — no host available | No hosts registered, no active-host | Run `mclaude daemon` | Fatal exit: `FATAL: HOST_SLUG required`. | Yes |

### 4.7 mclaude cluster commands (Admin)

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| CLI-CLUSTER-01 | Register cluster | Admin credentials | Run `mclaude cluster register --slug us-east --name "US East" --jetstream-domain us-east --leaf-url nats-leaf://hub:7422` | Calls `POST /admin/clusters`. Returns credentials for worker cluster setup. | Requires admin access |
| CLI-CLUSTER-02 | Grant cluster access | Admin, cluster exists | Run `mclaude cluster grant us-east bob` | Calls `POST /admin/clusters/us-east/grants`. New host row for bob. | Requires admin + multi-user |

### 4.8 Context File

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| CLI-CTX-01 | Context file provides defaults | `~/.mclaude/context.json` exists with `userSlug`, `projectSlug`, `hostSlug`, `server` | Run commands without explicit flags | Values read from context file. | Yes |
| CLI-CTX-02 | MCLAUDE_CONTEXT_FILE env override | Custom context file path | `export MCLAUDE_CONTEXT_FILE=/tmp/ctx.json && mclaude session list` | Context read from custom path. | Yes |
| CLI-CTX-03 | Missing context file | No `~/.mclaude/context.json` | Run commands | All fields default to empty. Flags required. | Yes |

---

## 5. Controller User Stories (Testable via NATS Provisioning)

### 5.1 Project Create Fan-out

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| CTRL-CREATE-01 | K8s controller creates MCProject on provision | K8s controller running, cluster registered | CP publishes to `mclaude.hosts.{cslug}.users.{uslug}.projects.{pslug}.create` | `MCProject` CR created. Reconciler creates namespace, RBAC, secrets, PVCs, deployment. Status → `Ready`. Reply: `{ok: true}`. | Requires K8s cluster |
| CTRL-CREATE-02 | Local controller creates project directory | Local controller running, BYOH host | CP publishes to `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create` | Project directory created at `{data-dir}/{uslug}/{pslug}/worktree/`. Git URL cloned if provided. Session-agent subprocess started. Agent NKey registered with CP. | Yes |
| CTRL-CREATE-03 | Local controller — git clone fails | Invalid git URL | CP publishes create with bad gitUrl | Reply: `{ok: false, code: "git_clone_failed"}`. CP returns 503 to SPA. | Yes |
| CTRL-CREATE-04 | Provision timeout | Controller not running or unresponsive | CP publishes create with 10s timeout | CP receives no reply within `PROVISION_TIMEOUT_SECONDS`. Returns 503 to SPA: `"host {hslug} unreachable"`. | Yes |

### 5.2 Project Delete Fan-out

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| CTRL-DELETE-01 | K8s controller deletes MCProject | MCProject exists | CP publishes to `mclaude.hosts.{cslug}.users.{uslug}.projects.{pslug}.delete` | `MCProject` CR deleted. Cascaded namespace/RBAC/PVCs/Deployment removed. | Requires K8s cluster |
| CTRL-DELETE-02 | Local controller stops agent and cleans up | Agent running for project | CP publishes delete | Agent subprocess stopped (SIGINT, 10s grace, SIGKILL). Data directory removed. Reply: `{ok: true}`. | Yes |
| CTRL-DELETE-03 | Delete idempotent (project already gone) | Project doesn't exist | CP publishes delete for non-existent project | Reply: `{ok: true}`. No error. | Yes |

### 5.3 Agent Credential Registration

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| CTRL-CRED-01 | Agent NKey registration | Controller spawned agent, agent generated NKey | Controller publishes `mclaude.hosts.{hslug}.api.agents.register` with `{userSlug, projectSlug, nkeyPublic}` | CP stores credential. Reply: `{ok: true, quotaPublisher}`. | Yes |
| CTRL-CRED-02 | Registration race — retry | Project create not yet processed | Controller publishes register before CP processes create | `NOT_FOUND` reply. Controller retries with exponential backoff (100ms → 5s max, 10 attempts). | Yes |
| CTRL-CRED-03 | Agent HTTP challenge-response auth | Agent NKey registered | Agent calls `POST /api/auth/challenge` + `/api/auth/verify` | Per-project JWT issued. 5-min TTL. Scoped to one user/host/project. | Yes |

### 5.4 Host Credential Refresh

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| CTRL-REFRESH-01 | Host JWT refresh before expiry | Host controller running, JWT near expiry | Controller calls HTTP challenge-response | New JWT issued. NATS connection re-authenticated in-flight. | Yes |
| CTRL-REFRESH-02 | Host JWT expired | Controller didn't refresh in time | JWT expires | NATS rejects operations. Controller re-authenticates via HTTP. | Yes |
| CTRL-REFRESH-03 | Permissions violation triggers immediate refresh | Controller gets NATS auth error | Controller receives `permissions violation` | Immediate refresh triggered (not waiting for TTL). Retry operation. | Yes |

### 5.5 K8s Reconciler Specifics

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| CTRL-K8S-01 | Namespace creation | MCProject CR created | Observe K8s namespace | `mclaude-{userId}` namespace with labels `mclaude.io/user-id`, `mclaude.io/managed`. | Requires K8s |
| CTRL-K8S-02 | RBAC resources created | MCProject reconciled | Observe K8s RBAC | ServiceAccount, Role (get/watch/patch ConfigMap, get Secret), RoleBinding. | Requires K8s |
| CTRL-K8S-03 | User secrets created | MCProject reconciled | Observe K8s Secret | `user-secrets` with NATS creds, OAuth tokens, CLI configs. | Requires K8s |
| CTRL-K8S-04 | PVC creation | MCProject reconciled | Observe K8s PVCs | `project-{projectId}` and `nix-{projectId}` PVCs with configured sizes. | Requires K8s |
| CTRL-K8S-05 | Deployment creation | MCProject reconciled | Observe K8s Deployment | `project-{projectId}` with Recreate strategy, correct volumes, env vars (`USER_ID`, `USER_SLUG`, `HOST_SLUG`, `PROJECT_ID`, `PROJECT_SLUG`). | Requires K8s |
| CTRL-K8S-06 | Template ConfigMap change triggers re-enqueue | Update session-agent-template ConfigMap | Observe all MCProject reconciles | All MCProjects re-enqueued. Pod templates updated with new image/resources. | Requires K8s |
| CTRL-K8S-07 | MCProject status transitions | MCProject CR lifecycle | Observe status | Phase: `Pending → Provisioning → Ready` (or `Failed`). Conditions: `NamespaceReady`, `RBACReady`, `SecretsReady`, `DeploymentReady`. | Requires K8s |
| CTRL-K8S-08 | Corporate CA injection | `corporateCAEnabled: "true"` in template CM | Observe pod template | `corporate-ca` volume + mount at `/etc/ssl/certs/corporate-ca-certificates.crt`. `NODE_EXTRA_CA_CERTS` env var. Pod annotation with CA hash. | Requires K8s + trust-manager |
| CTRL-K8S-09 | ImagePullSecrets copied | Controller namespace has pull secrets | Observe user namespace pod | imagePullSecrets copied from controller's namespace. | Requires K8s |

### 5.6 Local Controller Process Supervision

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| CTRL-LOCAL-01 | Agent subprocess crash recovery | Agent subprocess crashes | 1. Kill agent process. | Controller restarts child with 2s delay. | Yes |
| CTRL-LOCAL-02 | Controller graceful shutdown | Controller receives SIGINT/SIGTERM | 1. Send SIGINT to controller. | SIGINT forwarded to all children (10s grace, then SIGKILL). Wait for all children to exit. Controller exits. | Yes |
| CTRL-LOCAL-03 | Controller without host slug | No --host flag, no active-host | Run controller without host | Fatal exit: `FATAL: HOST_SLUG required`. | Yes |

### 5.7 Liveness Detection

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| CTRL-LIVE-01 | Host online via $SYS.CONNECT | Host controller connects to NATS | Observe CP behavior | CP receives `$SYS.ACCOUNT.*.CONNECT`. Looks up host by NKey public key. Updates `hosts.last_seen_at`. KV: `online: true`. | Yes |
| CTRL-LIVE-02 | Host offline via $SYS.DISCONNECT | Host controller disconnects | Observe CP behavior | CP receives `$SYS.ACCOUNT.*.DISCONNECT`. KV: `online: false`. `last_seen_at` NOT updated. | Yes |
| CTRL-LIVE-03 | Cluster online via leafnode CONNECT | K8s cluster NATS leaf connects | Observe CP behavior | CP receives `$SYS` with `client.kind: "Leafnode"`. All user rows for that cluster slug updated. KV: `online: true`. | Requires K8s cluster |
| CTRL-LIVE-04 | Unknown NKey ignored | SPA or CP connects | Observe CP behavior | No match in `hosts.nkey_public`. Event ignored. No error logged. | Yes |

---

## 6. Cross-Cutting / Integration Tests

| ID | Description | Preconditions | Steps | Expected Result | Testable? |
|----|-------------|---------------|-------|-----------------|-----------|
| INT-01 | End-to-end: Login → Create Project → Create Session → Chat → Delete | Full stack running | 1. Login via SPA. 2. Create project. 3. Create session. 4. Send message. 5. Receive response. 6. Delete session. 7. Delete project. | All steps complete. Events flow through NATS. KV state consistent. | Yes |
| INT-02 | JWT revocation on host access change | Multi-user, host sharing | 1. Alice grants bob access to laptop-a. 2. Bob's SPA reconnects with new JWT. 3. Alice revokes bob's access. 4. Bob's SPA disconnects. | JWT revocation propagated via `$SYS.REQ.CLAIMS.UPDATE`. Bob's sessions terminated. | Requires multi-user |
| INT-03 | Agent restart recovery (KV-based) | Session running, agent crashes | 1. Session active. 2. Kill agent process. 3. Agent restarts. | Agent reads KV for session state. Interactive sessions resumed via `--resume`. Quota-managed sessions resume from KV state. | Yes |
| INT-04 | Graceful shutdown zero-downtime | K8s deployment, session active | 1. Session active. 2. Trigger rolling update (new image). | Agent writes `updating` to KV. Drains to idle. Publishes `session_upgrading`. New pod resumes sessions from KV. | Requires K8s |
| INT-05 | Multi-backend session support | Multiple CLI backends available | 1. Create session with `claude_code`. 2. Create session with `droid`. 3. Create session with `generic_terminal`. | Each session uses the correct driver. Capabilities differ per backend. SPA adapts UI accordingly. | Requires all backends |
| INT-06 | Import → Session discovery → Dashboard | S3 configured, Claude Code data exists | 1. `mclaude login`. 2. `mclaude import`. 3. Open SPA. | Imported sessions appear in dashboard. No "imported" badge. JSONL data accessible. | Requires S3 + Claude Code data |
| INT-07 | OAuth provider credential rotation | GitLab OAuth connected, token near expiry | 1. Wait for token refresh goroutine (15 min cycle). | Token refreshed. NATS message published to controller. Per-user secret updated. | Requires GitLab OAuth |
| INT-08 | S3 cleanup on project deletion | Project with imports + attachments, S3 configured | 1. Delete project. | S3 prefix `{uslug}/{hslug}/{pslug}/` cleaned up (best-effort). | Requires S3 |
| INT-09 | Slug validation at all ingress points | Various | 1. Attempt creation with invalid slugs (reserved words, bad charset, too long). | HTTP 400 with `{code: "invalid_slug", reason, field}`. NATS error reply. | Yes |
| INT-10 | Session agent fsnotify watcher | Agent running, new JSONL file appears | 1. Place JSONL file in session data directory. | Agent discovers file. Extracts session metadata. Creates `SessionState` KV entry with `status: "completed"`. | Yes |

---

## Summary Statistics

| Category | Count |
|----------|-------|
| SPA User Stories | 67 |
| API User Stories | 37 |
| NATS User Stories | 56 |
| CLI User Stories | 26 |
| Controller User Stories | 23 |
| Cross-Cutting / Integration | 10 |
| **Total** | **219** |

### Deployment Feasibility Notes

- **Fully testable in current deployment**: ~150 tests (basic auth, session lifecycle, KV operations, CLI commands, NATS messaging, API endpoints)
- **Requires S3/MinIO**: ~25 tests (import, attachment flows)
- **Requires K8s cluster**: ~15 tests (reconciler, PVC, RBAC, graceful shutdown)
- **Requires multi-user setup**: ~10 tests (host access grants/revocation, JWT revocation)
- **Requires OAuth provider**: ~8 tests (GitHub/GitLab OAuth flows, token refresh)
- **Requires Anthropic API / quota mock**: ~12 tests (quota monitoring, pause/resume)
- **Requires specific CLI backends**: ~3 tests (Droid, Devin, GenericTerminal)
