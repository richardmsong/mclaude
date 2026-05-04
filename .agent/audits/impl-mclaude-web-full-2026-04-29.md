# Full Implementation Audit: mclaude-web (SPA)

**Date:** 2026-04-29
**Scope:** All production code in `mclaude-web/src/` vs `docs/mclaude-web/spec-web.md` + `docs/spec-state-schema.md`
**Files read:** 70+ .ts/.tsx files across src/, stores/, viewmodels/, components/, lib/, transport/, hooks/, system/

---

## Gap Summary

| # | Area | Severity | Description |
|---|------|----------|-------------|
| G1 | JWT Refresh | MISMATCH | Threshold 5 min vs spec 15 min |
| G2 | Background Reconnect | PARTIAL | Empty JWT in visibilitychange; KV watches not restarted |
| G3 | Result Event Rendering | PARTIAL | `result` events ignored; cost from assistant events only |
| G4 | Routing | MISMATCH | Custom hash routing, not React Router v6 |
| G5 | Per-Project Cost Display | MISSING | No per-project cost grouping view |
| G6 | File Upload Size Limit | MISSING | No ~20MB validation on image uploads |
| G7 | createSession Payload | PARTIAL | Missing cwd, joinWorktree, permPolicy, quotaMonitor fields |
| G8 | deleteSession Payload | PARTIAL | Missing requestId field |
| G9 | restartSession Payload | PARTIAL | Missing requestId field |
| G10 | Session KV DEL Handling | KNOWN BUG | Deletes by slug but map keyed by UUID (documented) |
| G11 | createProject Subject | KNOWN BUG | User-scoped subject, missing hostSlug (documented) |
| G12 | Lifecycle Event Processing | PARTIAL | Events subscribed but no listener processes them |
| G13 | Project KV DEL Handling | PARTIAL | Not explicitly checked; relies on parse failure catch |

---

## Detailed Gaps

### G1 — JWT Refresh Threshold (MISMATCH)

**Spec says:**
> "The SPA checks JWT TTL every 60 seconds. When remaining TTL falls below 15 minutes, it calls POST /auth/refresh"

**Code does:**
In `stores/auth-store.ts` `_checkAndRefresh()`:
```ts
if (ttlMs > 5 * 60 * 1000) return // More than 5 min left — no action
```

The code refreshes when TTL is below 5 minutes, not 15 minutes as spec requires. The check interval (60s) is correct.

**Severity: MISMATCH** — 5 min vs 15 min threshold. With 8h JWT lifetime this creates a narrow refresh window where network hiccups could cause token expiry.

---

### G2 — Background Reconnect (PARTIAL)

**Spec says:**
> ```js
> document.addEventListener('visibilitychange', () => {
>   if (document.visibilityState !== 'visible') return;
>   nc.reconnect();
>   kv.watch(`{uslug}/>`);
> });
> ```

**Code does:**
In `components/App.tsx`:
```ts
const handler = () => {
  if (document.visibilityState !== 'visible') return
  if (!natsClient.isConnected()) {
    natsClient.reconnect('').catch(() => {})
  }
}
document.addEventListener('visibilitychange', handler)
```

Two issues:
1. **Empty JWT passed to reconnect:** `natsClient.reconnect('')` passes an empty string as the new JWT. In `nats-client.ts`, `reconnect(newJwt)` uses `{ ...this._opts, jwt: newJwt }` — so it would try to connect with an empty JWT, which will fail NATS auth. The error is silently swallowed.
2. **KV watches not restarted:** The spec says `kv.watch('{uslug}/>')` should be called after reconnect. The code's reconnect handler only restarts the EventStore. `SessionStore.startWatching()` and `HeartbeatMonitor.start()` are NOT called after reconnect — their KV watchers are tied to the old (closed) NATS connection and will be dead.

The NATS `onReconnect` callback in App.tsx restarts the EventStore but not KV watches:
```ts
natsClient.onReconnect(() => {
  setConnected(true)
  const store = eventStoreRef.current
  if (store) {
    const resumeSeq = Math.max(store.lastSequence + 1, store.replayFromSeq)
    store.stop()
    store.start(resumeSeq)
  }
  // ← SessionStore.startWatching() and HeartbeatMonitor.start() NOT called
})
```

**Severity: PARTIAL** — Mobile users returning to a backgrounded tab will see stale session/project/host data. EventStore replay works correctly on reconnect (when triggered by other paths), but the visibility-change path fails due to empty JWT.

---

### G3 — Result Event Rendering (PARTIAL)

**Spec says:**
> Event table: `result` → "Usage/cost display"

**Code does:**
In `stores/event-store.ts`:
```ts
case 'result':
case 'keep_alive':
  // No UI action needed
  break
```

The `result` event is completely ignored. The `ResultEvent` type includes `usage?: UsageStats` but this data is never extracted or displayed.

Per-turn cost IS displayed — but it comes from `assistant` event's `message.usage`, not from `result` events. Per-session aggregate cost comes from KV state. So cost tracking works via alternative paths, but the `result` event's explicit `usage` payload is discarded.

**Severity: PARTIAL** — Cost tracking works functionally but the `result` event rendering specified in the event table is not implemented.

---

### G4 — Routing (MISMATCH)

**Spec says:**
> "React Router v6 with parametric segments: `/api/users/{uslug}/projects/{pslug}/sessions/{sslug}`"
>
> Dependencies table: "React Router v6 — Client-side routing"

**Code does:**
`components/App.tsx` uses a custom hash-based router:
```ts
function getRoute(): { screen: string; sessionId?: string; hostSlug?: string } {
  const hash = window.location.hash.slice(1)
  // ... regex matching on hash patterns
}
```

Hash format: `#u/{uslug}/h/{hslug}/p/{pslug}/s/{sslug}` (with legacy fallbacks).

No React Router dependency is installed or used anywhere in the codebase. The `main.tsx` entry point renders `<App />` directly with no `<BrowserRouter>` or `<HashRouter>`.

**Severity: MISMATCH** — The spec lists React Router v6 as a dependency and describes path-based parametric routing. The code uses custom hash-based routing instead. Functionally equivalent but architecturally different.

---

### G5 — Per-Project Cost Display (MISSING)

**Spec says:**
> "The SPA displays per-session and per-project cost."

**Code does:**
- Per-session cost: ✓ Displayed in `SessionDetailScreen` usage overlay (from `session.usage` KV field)
- Per-project cost: ✗ NOT implemented

The `TokenUsage` component (`components/TokenUsage.tsx`) aggregates cost across ALL sessions for a time range. It does not group or filter by project. No other component displays per-project cost totals.

**Severity: MISSING** — No per-project cost display exists anywhere in the SPA.

---

### G6 — File Upload Size Limit (MISSING)

**Spec says:**
> "Files and images are sent as base64 in the user message content array (standard Anthropic content format). Max ~20MB per image."

**Code does:**
In `components/SessionDetailScreen.tsx`, images are read via `FileReader.readAsDataURL(file)` with no size check:
```ts
const stageImageFile = (file: File) => {
  const reader = new FileReader()
  reader.onload = (ev) => {
    // ... directly creates base64 data
  }
  reader.readAsDataURL(file)
}
```

No file size validation is performed. Users can attempt to upload images of any size, which could:
1. Cause browser memory issues with very large base64 strings
2. Exceed NATS max_payload (8MB per message) silently

**Severity: MISSING** — No size validation; spec says ~20MB max.

---

### G7 — createSession Payload (PARTIAL)

**Spec says (state-schema):**
> `api.sessions.create` payload: `{name, branch, cwd, joinWorktree, extraFlags, permPolicy, quotaMonitor, requestId}`

**Code does:**
In `viewmodels/session-list-vm.ts` `createSession()`:
```ts
const payload = {
  projectId,   // NOT in spec
  branch,
  name,
  requestId,
  ...(opts?.extraFlags !== undefined ? { extraFlags: opts.extraFlags } : {}),
}
```

Missing fields: `cwd`, `joinWorktree`, `permPolicy`, `quotaMonitor`
Extra field: `projectId` (not in spec — session-agent may use this for session creation)

**Severity: PARTIAL** — Core fields present (name, branch, requestId, extraFlags); optional fields for workspace join, permission policy, and quota monitoring are not sent.

---

### G8 — deleteSession Payload (PARTIAL)

**Spec says (state-schema):**
> `api.sessions.delete` payload: `{sessionSlug, requestId}`

**Code does:**
In `viewmodels/session-list-vm.ts` `deleteSession()`:
```ts
this.natsClient.publish(subject, new TextEncoder().encode(JSON.stringify({ sessionId })))
```

Missing: `requestId` — spec requires it for error correlation. Also sends `sessionId` (UUID) instead of `sessionSlug`, which is a documented known gap in spec-web.md.

**Severity: PARTIAL** — Fire-and-forget works, but without `requestId` any deletion errors are silently lost. The sessionId-vs-sessionSlug mismatch is documented.

---

### G9 — restartSession Payload (PARTIAL)

**Spec says (state-schema):**
> `api.sessions.restart` payload: `{sessionSlug, extraFlags?, requestId}`

**Code does:**
In `viewmodels/session-list-vm.ts` `restartSession()`:
```ts
const payload = {
  sessionId,
  ...(opts?.extraFlags !== undefined ? { extraFlags: opts.extraFlags } : {}),
}
```

Missing: `requestId` — spec says it's required. Without it, restart errors are silently lost (documented known gap in spec-web.md). Also sends `sessionId` (UUID) instead of `sessionSlug`.

**Severity: PARTIAL** — Documented known gap; restart errors have no correlation path back to the SPA.

---

### G10 — Session KV DEL/PURGE Handling (KNOWN BUG)

**Spec says:**
> "Known bug: the DEL handler extracts the session slug from the KV key (parts[parts.length - 1]), but the _sessions map is keyed by UUID (state.id). this._sessions.delete(sessionSlug) is a no-op — deleted sessions persist in the UI as ghosts until full page reload."

**Code confirms:**
In `stores/session-store.ts`:
```ts
if (entry.operation === 'DEL' || entry.operation === 'PURGE') {
  const parts = entry.key.split('.')
  const sessionId = parts[parts.length - 1] // ← this is the session SLUG
  if (sessionId) this._sessions.delete(sessionId)  // ← map keyed by UUID → no-op
```

**Severity: KNOWN BUG** — Documented in spec. Ghost sessions persist until page reload.

---

### G11 — createProject Subject (KNOWN BUG)

**Spec says (spec-web.md):**
> "Known bug: the payload does not include hostSlug — the control-plane's NATS handler cannot resolve the host for provisioning, so SPA-created projects get no session-agent pod."

**Code confirms:**
In `lib/subj.ts`, `subjProjectsCreate` is user-scoped (no host slug):
```ts
export function subjProjectsCreate(uslug: UserSlug): string {
  return `${PREFIX}.users.${uslug}.api.projects.create`
}
```

The state-schema spec says the create subject should be `mclaude.users.{uslug}.hosts.{hslug}.api.projects.create` (host-scoped). Both the subject and the payload are missing `hostSlug`.

**Severity: KNOWN BUG** — Documented in spec.

---

### G12 — Lifecycle Event Processing (PARTIAL)

**Spec says (state-schema):**
> MCLAUDE_LIFECYCLE stream subscribers include "SPA (session list updates)"

**Code does:**
In `components/App.tsx`, a `LifecycleStore` is created per session:
```ts
const lifecycle = new LifecycleStore(natsClient, authState.userId, resolvedProjectId, ...)
lifecycle.start()
```

But NO listener is ever registered on the lifecycle store. Events are subscribed to and parsed, but discarded. Session list updates come entirely from KV watches, not lifecycle events.

The `LifecycleStore` class supports `onLifecycleEvent()` listeners, but App.tsx never calls it. No component consumes lifecycle events.

**Severity: PARTIAL** — The subscription exists but is effectively a no-op. Session list updates work via KV watches, but faster lifecycle-driven updates (e.g., session_created, session_failed) are lost. This means the SPA relies solely on KV watch latency for session state updates rather than the immediate lifecycle pub/sub path.

---

### G13 — Project KV DEL Handling (PARTIAL)

**Spec says (state-schema):**
> KVEntry.operation may be 'PUT' | 'DEL' | 'PURGE'. The session store's kvWatch callback must handle DEL/PURGE.

**Code does:**
In `stores/session-store.ts`, the project watcher does NOT check `entry.operation`:
```ts
const unwatch2 = this.natsClient.kvWatch('mclaude-projects', projectKey, (entry) => {
  try {
    const state = JSON.parse(new TextDecoder().decode(entry.value)) as ProjectKVState
    this._projects.set(state.id, state)
    this._notifyProjects()
  } catch {
    const parts = entry.key.split('.')
    const projectId = parts[parts.length - 1]
    if (projectId) {
      this._projects.delete(projectId)
    }
    this._notifyProjects()
  }
})
```

DEL entries would have empty/undefined value → `JSON.parse` throws → catch block deletes the project. This works by accident rather than by design. Compare with the session watcher which explicitly checks `entry.operation === 'DEL' || entry.operation === 'PURGE'`.

**Severity: PARTIAL** — Works but fragile. Any other parse failure (malformed JSON) would also delete the project from the local map, which is incorrect behavior.

---

## Verified Correct Implementations

The following spec requirements are confirmed correctly implemented:

| Area | Status |
|------|--------|
| NATS Connection (WebSocket at /nats, JWT+NKey auth) | ✅ |
| Event Rendering: stream_event (streaming text) | ✅ |
| Event Rendering: assistant (complete message) | ✅ |
| Event Rendering: tool_use (collapsible ToolCard) | ✅ |
| Event Rendering: tool_progress (elapsed time) | ✅ |
| Event Rendering: control_request (approve/deny buttons) | ✅ |
| Event Rendering: session_state_changed (status indicator) | ✅ |
| Event Rendering: init (skills, tools, model) | ✅ |
| Event Rendering: parent_tool_use_id nesting (AgentGroup) | ✅ |
| KV Watch: mclaude-sessions | ✅ |
| KV Watch: mclaude-projects | ✅ |
| KV Watch: mclaude-hosts (HeartbeatMonitor) | ✅ |
| Event Replay: max(lastSeq+1, replayFromSeq) on reconnect | ✅ |
| Event Replay: dedup by JetStream sequence number | ✅ |
| JWT Refresh: 60s check interval | ✅ |
| JWT Refresh: POST /auth/refresh | ✅ |
| JWT Refresh: NATS re-auth with new JWT | ✅ |
| Model Switching: set_model control request | ✅ |
| Effort Switching: set_max_thinking_tokens control request | ✅ |
| Plugin Reload: reload_plugins control request | ✅ |
| Per-session cost display (usage overlay) | ✅ |
| File/Image uploads (base64 in content array) | ✅ |
| Hash routing with parametric slug segments | ✅ |
| TypeScript slug branded types (slug.ts) | ✅ |
| TypeScript subject builders (subj.ts) | ✅ |
| Slug validation + reserved words | ✅ |
| createSession: publish + KV-watch + 30s timeout | ✅ |
| createSession: api_error subscription on events._api | ✅ |
| SessionStore.onSessionAdded (project-filtered) | ✅ |
| deleteSession: fire-and-forget | ✅ |
| ConversationVM: session_id (UUID) in sessions.input | ✅ |
| Default hostSlug 'local' across all stores/VMs | ✅ |
| SessionState.state union includes "updating" | ✅ |
| DashboardScreen STATE_LABELS: updating → 'Updating...' | ✅ |
| SessionDetail: blue "Updating" banner when state=updating | ✅ |
| StatusDot: blue pulsing for updating state | ✅ |
| Message input enabled during updating state | ✅ |
| Desktop Notifications: requestPermission on first NATS connect | ✅ |
| Desktop Notifications: fire on requires_action while tab hidden | ✅ |
| Project Filter: filterProjectId persisted to localStorage | ✅ |
| Project Filter: resolveFilter auto-clears deleted projects | ✅ |
| Dead code: subjClusterProvision/Status, kvKeyUserClusters | ✅ documented |
| Session KV prefix: userSlug for sessions, userId for projects | ✅ |
| URL rewrite to slug format on session entry | ✅ |

---

## Notes

- **Documented known bugs** (G10, G11) are already flagged in spec-web.md and don't represent new findings.
- **G1 (JWT threshold)** and **G2 (background reconnect)** are the most impactful gaps for production reliability, especially on mobile browsers.
- **G5 (per-project cost)** is a feature gap rather than a correctness issue.
- **G4 (routing)** is an architectural choice that works correctly; the spec dependency list may simply be outdated.
