# Graceful Session-Agent Pod Upgrades

**Status**: draft
**Status history**:
- 2026-04-14: accepted
- 2026-04-19: reverted to draft — retroactive accepted tag incorrect; implementation not confirmed, needs per-ADR review


## Overview

When Helm deploys a new session-agent image, the running pod finishes its current Claude turn, queues incoming messages in JetStream, writes an "Updating..." state to KV, and exits cleanly. The new pod starts, resumes sessions, drains queued messages, and clears the banner. No user messages are lost. The UI shows an "Updating..." banner during the transition.

This feature also moves all session API subjects from core NATS (fire-and-forget) to JetStream (persistent, at-least-once delivery). Terminal I/O stays on core NATS.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| JetStream scope | All `api.sessions.*` subjects | Ensures no messages lost during restart. Create/delete results delivered via existing KV watchers. |
| Terminal subjects | Stay on core NATS | Ephemeral, latency-sensitive. Stale terminal input after restart is meaningless. |
| Drain timeout | Wait indefinitely | User sends interrupt if stuck. K8s 24h grace period is a safety net, not a policy. |
| Banner timing | Immediately on SIGTERM | User sees upgrade is in progress while current turn finishes. More transparent than waiting. |
| Create/delete reply mechanism | Publish + KV watch | SPA already watches session KV. Session appears → create succeeded. Error → event on events stream. |
| Deployment strategy | Recreate | Old pod exits before new pod starts. Prevents two pods consuming from the same JetStream consumer simultaneously. |
| Consumer split | Two consumers (cmd + ctl) | During drain, stop cmd consumer (queue new work) but keep ctl consumer (interrupts, permission responses still work). |

## Upgrade Flow

End-to-end sequence when Helm deploys a new session-agent image:

```
1. Helm upgrade → ConfigMap `session-agent-template` updated with new image tag
2. Reconciler detects ConfigMap change → re-enqueues all MCProject CRs
3. reconcileDeployment sees image mismatch → updates Deployment spec (including Recreate strategy)
4. K8s sends SIGTERM to old pod (Recreate strategy: old dies first)
5. Session-agent receives SIGTERM:
   a. Writes state:"updating" to session KV for ALL sessions (for SPA banner). Does NOT clobber the in-memory `sess.state.State` field — that keeps reflecting Claude's live state so the drain predicate in step e can detect real idle-vs-running transitions.
   b. Stops command consumer (create/delete/input/restart queue in JetStream)
   c. Drains core NATS subscriptions (terminal API)
   d. Keeps control consumer running (interrupts, permission responses)
   e. Waits for every session to satisfy the drain predicate — Claude main turn is `idle` AND no in-flight background agents. Sessions stuck in `requires_action` (awaiting a permission response) are interrupted so the user isn't a blocker for the upgrade; they re-send after resume if they still want the tool to run. No wall-clock timeout.
   f. Stops control consumer
   g. Publishes lifecycle event "session_upgrading" for each session
   h. Exits cleanly
6. K8s terminates old pod, creates new pod
7. New pod starts:
   a. Entrypoint runs (seeds config, sets up repo)
   b. Session-agent recovers sessions from KV
   c. Attaches to existing durable JetStream consumers
   d. Drains queued messages (creates, inputs, etc.)
   e. Writes state:"idle" to session KV for all sessions that were "updating"
   f. UI banner disappears
```

## Component Changes

### JetStream Stream: MCLAUDE_API

New stream created by session-agent on startup (idempotent, same pattern as `MCLAUDE_EVENTS`):

```
Name:      MCLAUDE_API
Subjects:  ["mclaude.*.*.api.sessions.>"]
Retention: LimitsPolicy
MaxAge:    1h
Storage:   FileStorage
Discard:   DiscardOld
```

One hour retention — API commands older than 1h are stale and should not be processed.

**NATS ACL note:** The session-agent's per-user JWT already permits `pub` and `sub` on `mclaude.{userId}.>`, and the existing `MCLAUDE_EVENTS` stream (created by the same agent with the same JWT) proves JetStream API access works. `MCLAUDE_API` follows the same pattern — the stream captures messages on matching subjects regardless of which user published them, and each agent's consumer filters to its own user/project prefix.

### Session-Agent: JetStream Consumers

Two durable pull consumers per session-agent, created on startup:

**Command consumer** — handles new work (sessions.create, sessions.delete, sessions.input, sessions.restart):

```
Name:           sa-cmd-{userId}-{projectId}
FilterSubjects: [
  "mclaude.{userId}.{projectId}.api.sessions.create",
  "mclaude.{userId}.{projectId}.api.sessions.delete",
  "mclaude.{userId}.{projectId}.api.sessions.input",
  "mclaude.{userId}.{projectId}.api.sessions.restart"
]
AckPolicy:      Explicit
AckWait:        60s
MaxDeliver:     5
```

**Control consumer** — handles interrupts and permission responses (must stay active during drain):

```
Name:           sa-ctl-{userId}-{projectId}
FilterSubjects: ["mclaude.{userId}.{projectId}.api.sessions.control"]
AckPolicy:      Explicit
AckWait:        60s
MaxDeliver:     5
```

Both consumers are durable — they survive pod restarts. When the new pod attaches, it picks up from the last acknowledged position. Unacked messages from the old pod are redelivered.

### Session-Agent: JetStream Fetch Loop

Each consumer runs in its own goroutine with a pull-based fetch loop:

```
func (a *Agent) runConsumer(ctx context.Context, cons jetstream.Consumer, dispatch func(jetstream.Msg)) {
    for {
        msgs, err := cons.Fetch(10, jetstream.FetchMaxWait(5s))
        if ctx.Err() != nil { return }  // consumer stopped
        if err != nil { backoff; continue }
        for msg := range msgs.Messages() {
            dispatch(msg)
            msg.Ack()
        }
    }
}
```

**Batch size:** 10 messages per fetch. **Fetch timeout:** 5s (returns early if messages arrive sooner).

**Type adaptation:** JetStream delivers `jetstream.Msg`, not `*nats.Msg`. The existing handler functions (`handleCreate`, `handleInput`, etc.) accept `*nats.Msg`. Rather than rewriting every handler, wrap the JetStream message:

```go
// jsToNatsMsg wraps a jetstream.Msg into a *nats.Msg for handler compatibility.
// The wrapped msg has .Data, .Subject, and .Header populated.
// .Reply is empty — handlers must not call msg.Respond() (see Reply Mechanism Change).
func jsToNatsMsg(jm jetstream.Msg) *nats.Msg {
    return &nats.Msg{
        Subject: jm.Subject(),
        Data:    jm.Data(),
        Header:  jm.Headers(),
    }
}
```

The dispatch function routes by subject suffix:

```go
func (a *Agent) dispatchCmd(jm jetstream.Msg) {
    msg := jsToNatsMsg(jm)
    switch {
    case strings.HasSuffix(jm.Subject(), ".create"):  a.handleCreate(msg)
    case strings.HasSuffix(jm.Subject(), ".delete"):  a.handleDelete(msg)
    case strings.HasSuffix(jm.Subject(), ".input"):   a.handleInput(msg)
    case strings.HasSuffix(jm.Subject(), ".restart"): a.handleRestart(msg)
    }
}
```

**Ack timing:** After the handler returns (not before). If the handler panics, the message is not acked and will be redelivered after AckWait (60s).

**Stopping a consumer:** Cancel the consumer's context. The `Fetch` call returns immediately with `ctx.Err()`. The goroutine exits. The durable consumer remains on the server — the new pod re-attaches to it.

### Session-Agent: SIGTERM Handler

Replace the current `gracefulShutdown()` (which interrupts Claude and exits in 10s) with:

```
On SIGTERM / context cancellation:
1. For each session:
    - Write state:"updating" + stateSince:now to session KV (for SPA banner).
    - Set sess.shutdownPending = true (in-memory flag).
    - Do NOT modify the in-memory sess.state.State field — it must keep tracking Claude's live state so the drain predicate works.
2. Cancel command consumer context (stops cmd fetch loop; messages queue in JetStream).
3. Drain core NATS subscriptions (terminal.create, terminal.delete, terminal.resize).
4. Keep control consumer running (its context is NOT cancelled).
5. Poll loop (1s tick):
    - For each session: evaluate the drain predicate (see below).
    - If ALL sessions satisfy the predicate → break.
6. Cancel control consumer context (stops ctl fetch loop).
7. Publish lifecycle event "session_upgrading" for each session.
8. Exit(0).
```

**Drain predicate** — a session is ready to exit when BOTH of:
- `sess.getState().State == StateIdle` — Claude's main turn is not active.
- `sess.inFlightBackgroundAgents == 0` — no async `Agent(run_in_background=true)` tool calls are awaiting their `task-notification`.

Pending permission prompts are NOT a blocking condition. If a session has outstanding controls (`state == StateRequiresAction`) the drain loop interrupts the turn rather than waiting for the user to respond — see Pending-control interrupt below.

The in-memory state field is the source of truth for the poll, NOT the KV state. KV state is set to `"updating"` in step 1 for the SPA banner and must not be read back to decide drain completion (that would be tautological).

**Pending-control interrupt** — a session may be stuck in `StateRequiresAction` because the user is offline or asleep. Waiting indefinitely would block the upgrade for that session forever. Therefore, on every poll tick, for each session whose state is `StateRequiresAction`, the drain loop sends a synthetic interrupt through the same code path as `handleControl` — the turn aborts, the pending tool_use is cancelled, the session transitions to `StateIdle`, and the drain predicate becomes satisfiable. After the new pod resumes the conversation with `--resume`, the user's last request is still in the transcript; they can re-send to retry, and Claude re-issues the tool_use (triggering a fresh permission prompt) if it still wants to proceed.

**In-flight background agent counter** — each session tracks `inFlightBackgroundAgents int` guarded by `sess.mu`. The stdout router updates it:
- `+1` when it observes an `assistant` message whose `message.content[*]` contains a `tool_use` block where `name == "Agent"` AND the input includes `run_in_background: true`.
- `-1` (floored at zero) when it observes a top-level `user` message with `origin.kind == "task-notification"`.

The counter is best-effort — if the session-agent was killed mid-flight and relaunched, the counter starts at 0 on the new pod. The main-session JSONL still contains the unresolved Agent tool_use, but Claude's internal task-notification machinery was lost with the old pod. This is by design: the NEW pod's drain logic only needs to protect agents launched after its own startup.

**KV write suppression during drain** — while `sess.shutdownPending == true`, the `SubtypeSessionStateChanged` handler updates in-memory `sess.state.State` as usual but MUST NOT flush state to KV. If it did, a post-step-1 Claude transition from `running` → `idle` would overwrite the `"updating"` banner state, and the SPA banner would disappear mid-upgrade.

The poll loop uses `sess.getState().State` (already thread-safe via mutex) — not `stopAndWait`, which sends an interrupt. The point is to wait passively, not to interrupt.

The control consumer stays active during the poll loop so the user can:
- Send an interrupt to unblock a stuck turn
- Respond to a pending permission prompt

Without this, a busy session waiting for a permission response would block the upgrade forever.

### Session-Agent: Startup Recovery

Extend `recoverSessions()` and the `Run()` startup sequence:

```
Run():
  1. recoverSessions()          // resumes sessions from KV with --resume
  2. createJetStreamConsumers() // attaches to durable consumers (creates if first time)
  3. subscribeTerminalAPI()     // core NATS subs for terminal.*
  4. clearUpdatingState()       // writes state:"idle" to KV for any session still in "updating"
  5. runHeartbeat()
  6. <-ctx.Done()
  7. gracefulShutdown()
```

In `recoverSessions()`, when a session's state is `"updating"`:
- Treat it as `"idle"` for purposes of resume (the session was idle when the old pod wrote "updating")
- **Do NOT write state to KV yet** — keep the session as "updating" in KV so the UI banner stays visible during startup
- The existing `clearPendingControlsForResume(&st)` unconditionally sets `st.State = StateIdle` and the recovery loop calls `writeSessionKV(st)`. This must be changed: skip the KV write for sessions in "updating" state, or set the in-memory state to idle (for `sess.start()`) without persisting it to KV
- The `clearUpdatingState()` in step 4 is what actually writes `state:"idle"` to KV for these sessions — only after consumers are attached and the agent is ready to process messages

### Session-Agent: Reply Mechanism Change

Handlers that currently use `msg.Respond()` (request-reply) switch to writing results via existing side channels. The `reply()` helper becomes a no-op when `msg.Reply == ""` (which it always is for JetStream messages since the wrapped `*nats.Msg` has no Reply field).

| Handler | Old reply | New reply |
|---------|----------|-----------|
| `handleCreate` | `msg.Respond({id: sessionId})` | Session appears in KV (SPA watches KV). Error → publish `api_error` event. |
| `handleDelete` | `msg.Respond({})` | Session disappears from KV. Error → publish `api_error` event. |
| `handleRestart` | `msg.Respond({})` | Session state transitions through `restarting` in KV. Error → publish `api_error` event. |
| `handleInput` | (none — fire-and-forget) | No change. |
| `handleControl` | (none — fire-and-forget) | No change. |

**Error event subject:** `mclaude.{userId}.{projectId}.events._api`

This is a project-level subject (no sessionId) because some errors (e.g., create failures) occur before a session exists. The `_api` suffix distinguishes it from session event subjects (`events.{sessionId}`). The `MCLAUDE_EVENTS` stream captures it via the existing `mclaude.*.*.events.*` filter.

**Error event payload:**

```json
{
  "type": "api_error",
  "request_id": "<client-generated UUID>",
  "operation": "create | delete | restart",
  "error": "<error message>"
}
```

The `request_id` is included in the original request payload by the SPA. The session-agent echoes it back in the error event so the SPA can correlate.

**Handler request struct changes** — add `RequestID string` to each handler's request struct so it can be echoed in error events:

```go
// handleCreate request struct:
var req struct {
    Name         string `json:"name"`
    Branch       string `json:"branch"`
    CWD          string `json:"cwd"`
    JoinWorktree bool   `json:"joinWorktree"`
    RequestID    string `json:"requestId"`  // ← new
}

// handleDelete request struct:
var req struct {
    SessionID string `json:"sessionId"`
    RequestID string `json:"requestId"`  // ← new
}

// handleRestart request struct:
var req struct {
    SessionID string `json:"sessionId"`
    RequestID string `json:"requestId"`  // ← new
}
```

When a handler encounters an error, it publishes `api_error` with `req.RequestID` (if non-empty) to the `events._api` subject instead of calling `msg.Respond()`.

### Control-Plane: Reconciler ConfigMap Watch

Add a watch on the `session-agent-template` ConfigMap to `SetupWithManager`. When the ConfigMap changes, re-enqueue all MCProject CRs so `reconcileDeployment` compares the new template image against each Deployment.

The watch uses `handler.EnqueueRequestsFromMapFunc` with a predicate that filters by name and namespace to avoid firing on every ConfigMap change cluster-wide.

New imports required in `reconciler.go`:
- `"sigs.k8s.io/controller-runtime/pkg/builder"`
- `"sigs.k8s.io/controller-runtime/pkg/predicate"`

```
SetupWithManager:
  For(&MCProject{}).
  Owns(&appsv1.Deployment{}).
  ...existing watches...
  Watches(&corev1.ConfigMap{},
    handler.EnqueueRequestsFromMapFunc(
      func(ctx, obj) []reconcile.Request {
        if obj.GetName() != releaseName+"-session-agent-template" { return nil }
        if obj.GetNamespace() != controlPlaneNs { return nil }
        var mcpList MCProjectList
        r.client.List(ctx, &mcpList)
        var reqs []reconcile.Request
        for _, mcp := range mcpList.Items {
          reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{
            Name: mcp.Name, Namespace: mcp.Namespace,
          }})
        }
        return reqs
      }
    ),
    builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
      return obj.GetName() == releaseName+"-session-agent-template" &&
             obj.GetNamespace() == controlPlaneNs
    })),
  )
```

### Control-Plane: Deployment Strategy

Both `ensureDeployment` implementations set the Deployment strategy to `Recreate`. There are two active provisioning paths — both must be updated:

1. **`reconciler.go` — `MCProjectReconciler.reconcileDeployment`** (controller-runtime, declarative). The primary path. `SetupWithManager` drives reconciliation on MCProject CR changes.
2. **`provision.go` — `K8sProvisioner.ensureDeployment`** (raw `kubernetes.Interface`, imperative). Still active as the fallback path used by `seedDev` and the NATS `projects.create` handler when the controller-runtime client is unavailable.

Both paths must be updated in both the create branch (new Deployment) and the update branch (existing Deployment with image mismatch):

**Create path** — set strategy on the Deployment spec before creating:

```go
deploy.Spec.Strategy = appsv1.DeploymentStrategy{
    Type: appsv1.RecreateDeploymentStrategyType,
}
```

**Update path** — set strategy alongside the image update:

```go
existing.Spec.Template.Spec.Containers[0].Image = tpl.image
existing.Spec.Strategy = appsv1.DeploymentStrategy{
    Type: appsv1.RecreateDeploymentStrategyType,
}
```

This ensures existing Deployments (which defaulted to RollingUpdate) are migrated to Recreate on the next reconcile.

### Helm Chart

**values.yaml** — increase default `terminationGracePeriodSeconds`:

```yaml
sessionAgent:
  terminationGracePeriodSeconds: 86400  # 24h — pod waits indefinitely for idle; this is a K8s safety net
```

The template at `session-agent-pod-template.yaml` already renders `terminationGracePeriodSeconds` from this value — no template change needed.

### SPA: Session Store — New `onSessionAdded` Method

Add `onSessionAdded(projectId, callback)` to `SessionStore`. Fires when a new session key appears in the KV watcher for the given project. Returns an unsubscribe function.

The `kvWatch` API has no "initial load complete" sentinel (unlike Go's `WatchAll` which sends `nil`). Instead, `onSessionAdded` snapshots `this._sessions.keys()` at registration time. Any session key that appears in a subsequent KV watch callback and was NOT in the snapshot is "new" and triggers the listener:

```typescript
onSessionAdded(projectId: string, cb: (session: SessionKVState) => void): () => void {
  const knownAtRegistration = new Set(this._sessions.keys())
  const handler = (id: string, session: SessionKVState) => {
    if (session.projectId === projectId && !knownAtRegistration.has(id)) {
      cb(session)
    }
  }
  this._addListeners.push(handler)
  return () => { this._addListeners = this._addListeners.filter(l => l !== handler) }
}
```

**Invocation:** The KV watch callback calls `_notifyAddListeners(id, state)` on every PUT event, after `_sessions.set()` and `_notifySessions()`. Each listener's `knownAtRegistration` snapshot (captured once at registration time) handles its own filtering — no novelty detection needed at the store level. The updated KV watch callback code is shown in the "SPA: Session Store — kvWatch DEL fix" section below.

### SPA: API Error Listener

`api_error` events are published to `mclaude.{userId}.{projectId}.events._api` — a project-level subject, not a session-level one. The existing `EventStore` is session-scoped (constructed with a `sessionId`) and cannot own this subscription.

Instead, the `createSession()` method in `SessionListVM` creates a temporary core NATS subscription via the existing `INATSClient.subscribe(subject, callback)` API (which takes a callback and returns an unsubscribe function):

```typescript
// Inside createSession():
const apiSubject = `mclaude.${this.userId}.${projectId}.events._api`
const unsubErr = this.natsClient.subscribe(apiSubject, (msg) => {
  const event = JSON.parse(new TextDecoder().decode(msg.data))
  if (event.type === 'api_error' && event.request_id === requestId) {
    cleanup()
    reject(new Error(event.error))
  }
})
```

This subscription is cleaned up when the promise resolves (success from KV watcher, error from this subscription, or 30s timeout). No changes to `EventStore` needed.

### SPA: Session List VM

**`createSession()`** — switch from request-reply to publish + KV watch:

```typescript
async createSession(projectId: string, branch: string, name: string): Promise<string> {
  const requestId = crypto.randomUUID()
  const subject = `mclaude.${this.userId}.${projectId}.api.sessions.create`
  const payload = { projectId, branch, name, requestId }
  this.natsClient.publish(subject, encode(JSON.stringify(payload)))

  return new Promise((resolve, reject) => {
    const cleanup = () => { clearTimeout(timer); unsubKV(); unsubErr() }
    const timer = setTimeout(() => { cleanup(); reject(new Error('Create session timed out')) }, 30_000)

    // Success: session appears in KV (already watched by session-store)
    const unsubKV = this.sessionStore.onSessionAdded(projectId, (session) => {
      cleanup()
      resolve(session.id)
    })

    // Error: temporary core NATS sub on project-level _api subject
    const unsubErr = this.natsClient.subscribe(
      `mclaude.${this.userId}.${projectId}.events._api`,
      (msg) => {
        const event = JSON.parse(new TextDecoder().decode(msg.data))
        if (event.type === 'api_error' && event.request_id === requestId) {
          cleanup()
          reject(new Error(event.error))
        }
      }
    )
  })
}
```

**`deleteSession()`** — fire-and-forget (KV deletion observed by watcher):

```typescript
async deleteSession(sessionId: string): Promise<void> {
  const session = this.sessionStore.sessions.get(sessionId)
  if (!session) return
  const subject = `mclaude.${this.userId}.${session.projectId}.api.sessions.delete`
  this.natsClient.publish(subject, encode(JSON.stringify({ sessionId })))
}
```

**Prerequisite: `kvWatch` must pass DEL entries to the callback.** Currently `nats-client.ts:158` skips DEL/PURGE operations entirely (`if (entry.operation === 'DEL' || entry.operation === 'PURGE') continue`). This means session-store never observes KV deletions — the catch block at `session-store.ts:39-46` is unreachable.

Fix `kvWatch` to include DEL entries by adding an `operation` field to `KVEntry`:

```typescript
// In types.ts, extend KVEntry:
export interface KVEntry {
  key: string
  value: Uint8Array
  revision: number
  operation?: 'PUT' | 'DEL' | 'PURGE'  // ← new
}

// In nats-client.ts kvWatch loop, replace the skip:
callback({
  key: entry.key,
  value: entry.value,
  revision: entry.revision,
  operation: entry.operation as 'PUT' | 'DEL' | 'PURGE',
})
```

Then update `session-store.ts` to check `entry.operation` instead of relying on a catch block:

```typescript
const unwatch1 = this.natsClient.kvWatch('mclaude-sessions', sessionKey, (entry) => {
  if (entry.operation === 'DEL' || entry.operation === 'PURGE') {
    const parts = entry.key.split('.')
    const sessionId = parts[parts.length - 1]
    if (sessionId) this._sessions.delete(sessionId)
    this._notifySessions()
    return
  }
  const state = JSON.parse(new TextDecoder().decode(entry.value)) as SessionKVState
  this._sessions.set(state.id, state)
  this._notifySessions()
  this._notifyAddListeners(state.id, state)  // for onSessionAdded
})
```

### SPA: Session Detail Screen

Add `updating` state to `STATE_LABELS`:

```typescript
const STATE_LABELS: Record<string, string> = {
  running: 'Working',
  requires_action: 'Needs permission',
  plan_mode: 'Plan mode',
  idle: 'Idle',
  updating: 'Updating...',     // ← new
  restarting: 'Restarting',
  failed: 'Failed',
  unknown: 'Unknown',
  waiting_for_input: 'Waiting for input',
}
```

When `state === 'updating'`, show a banner above the conversation:

```
┌──────────────────────────────────────────────┐
│  ↻ Updating — your session will resume       │
│    shortly. Messages are queued.             │
└──────────────────────────────────────────────┘
```

The input box stays enabled — user can still type and send. Messages queue in JetStream and are processed when the new pod starts.

### SPA: Dashboard Screen

`DashboardScreen.tsx` has its own `STATE_LABELS` (line 72) and a hard-coded type cast (line 294). Both must be updated:

Add to `STATE_LABELS`:
```typescript
updating: 'Updating...',
```

Update the type cast at line 294 to include `'updating'`:
```typescript
<StatusDot state={session.state as 'idle' | 'running' | 'requires_action' | 'restarting' | 'failed' | 'updating'} size={12} />
```

### SPA: StatusDot

`StatusDot.tsx` uses `STATE_COLORS: Record<string, string>` with CSS variable values and a `PULSE_STATES: Set<string>` for the pulse animation.

Add `updating` to both:

```typescript
// In STATE_COLORS:
updating: 'var(--blue)',

// In PULSE_STATES:
const PULSE_STATES = new Set(['working', 'running', 'connecting', 'updating'])
```

This gives the updating state a blue pulsing dot — distinct from orange/running and red/error.

### SPA: TypeScript Types

In `types.ts`, add `'updating'` to the `SessionState` union (line 78):

```typescript
export type SessionState =
  | 'idle'
  | 'running'
  | 'requires_action'
  | 'plan_mode'
  | 'restarting'
  | 'failed'
  | 'updating'           // ← new
  | 'unknown'
  | 'waiting_for_input'
```

In `types.ts`, add `'session_upgrading'` to the inline union on `LifecycleEvent.type` (line 307):

```typescript
export interface LifecycleEvent {
  type: 'session_created' | 'session_stopped' | 'session_restarting' | 'session_failed' | 'session_upgrading'
  sessionId: string
  projectId: string
  timestamp: string
}
```

### Session State Constants

Add `StateUpdating` to the state constants in `events.go` (where the existing state constants live):

```go
const (
  StateIdle           = "idle"
  StateRunning        = "running"
  StateRequiresAction = "requires_action"
  StateUpdating       = "updating"  // ← new
)
```

In `clearPendingControlsForResume()` (in `state.go`), handle `"updating"` as an incoming state — treat it like `"idle"`:

```go
func clearPendingControlsForResume(st *SessionState) {
  st.PendingControls = map[string]any{}
  if st.State == StateUpdating {
    st.State = StateIdle
  } else {
    st.State = StateIdle
  }
}
```

(In practice, `clearPendingControlsForResume` already sets state to idle unconditionally, so `"updating"` is handled by default. No code change needed — just documenting the behavior.)

## Error Handling

| Scenario | Behavior |
|----------|----------|
| SIGTERM while all sessions idle AND no in-flight background agents | Write "updating" to KV, stop cmd consumer, drain terminal subs, evaluate predicate → already satisfied → stop ctl consumer, exit. |
| SIGTERM while a session is mid-turn (`state == running`) | Write "updating" to KV (but do NOT clobber in-memory state). Stop cmd consumer, keep ctl consumer. Poll until Claude emits `state == idle`, then check for background agents. |
| SIGTERM while a session has in-flight background agents | Write "updating" to KV. Keep ctl consumer running. Poll until each async Agent's `task-notification` has been observed (counter hits 0) before exit. |
| User sends interrupt during drain | Control consumer processes it. Session goes idle. Pod exits. |
| User sends permission response during drain | Control consumer processes it. Turn continues/completes. Pod exits when the drain predicate is satisfied (possibly still waiting on background agents). |
| User does NOT respond to permission prompt during drain | Drain loop sends a synthetic interrupt on the next poll tick. Turn aborts, session transitions to idle, pod exits. User's last request remains in transcript and they can re-send after the new pod resumes. |
| New user message during drain | Queues in JetStream. Processed by new pod after restart. |
| Create request during restart | Queues in JetStream. New pod processes it. SPA waits for KV (30s timeout). |
| Pod crashes (no SIGTERM) | K8s recreates pod. Durable consumer has unacked messages → redelivered. Sessions resume from KV. |
| Turn never finishes | User sends interrupt via control subject. K8s kills at 24h as last resort. |
| Second Helm deploy during drain | K8s updates Deployment spec. Current drain continues. New pod starts with latest image. |
| Message redelivered 5 times (MaxDeliver) | Message dropped. Indicates a bug — the handler is consistently failing. |

## Scope

**v1 (this feature):**
- `MCLAUDE_API` JetStream stream for all session API subjects
- Two durable pull consumers per session-agent (cmd + ctl)
- JetStream fetch loop with `jetstream.Msg` → `*nats.Msg` adapter
- SIGTERM graceful drain: write `"updating"` to KV only (not in-memory state) → stop cmd → poll drain predicate (state == idle AND in-flight bg agents == 0) → auto-interrupt any session in `requires_action` so pending permission prompts don't block upgrade → stop ctl → exit
- Per-session `inFlightBackgroundAgents` counter maintained by the stdout router (`+1` on `Agent` tool_use with `run_in_background: true`, `-1` on user message with `origin.kind == "task-notification"`)
- KV write suppression in `SubtypeSessionStateChanged` handler while `shutdownPending` is set (in-memory state still updates)
- Reconciler watches ConfigMap (filtered by name + namespace), re-enqueues MCProject CRs on image change
- `Recreate` deployment strategy for session-agent pods (both create and update paths)
- `terminationGracePeriodSeconds: 86400` (values.yaml change only — template already renders it)
- SPA: create/delete switch from request-reply to publish + KV watch
- SPA: `SessionStore.onSessionAdded()` new method; temporary NATS sub for `api_error` in `createSession()`
- SPA: `SessionState` type union and `LifecycleEvent.type` inline union updated in `types.ts`
- SPA: `DashboardScreen.tsx` STATE_LABELS and type cast updated
- SPA: "Updating..." banner and blue pulsing StatusDot
- `api_error` events on `mclaude.{userId}.{projectId}.events._api` for failed create/delete/restart
- `StateUpdating` constant in `events.go`

**Deferred:**
- Version pinning per project (`spec.imageOverride` on MCProject CRD)
- Canary rollouts (gradual upgrade across user pods)
- Pre-pull new image before drain (minimize restart window)
- Dead letter queue for messages that exceed MaxDeliver
