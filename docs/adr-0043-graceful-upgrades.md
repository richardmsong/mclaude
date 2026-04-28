# ADR: Graceful Session-Agent Upgrades

**Status**: accepted
**Status history**:
- 2026-04-28: draft → accepted

> Supersedes:
> - `adr-0008-graceful-upgrades.md` — folded in: SIGTERM drain flow, JetStream migration for session API subjects, durable consumers (cmd + ctl), KV-based reply mechanism, Recreate deployment strategy, SPA "Updating..." banner, startup recovery sequence
> - `adr-0019-backgrounded-shells.md` — folded in: in-flight backgrounded shell tracking, synthetic `<task-notification status=killed>` on drain, PVC-backed `CLAUDE_CODE_TMPDIR` for output file persistence across pod restarts
>
> The two ADRs above are marked `superseded` by this ADR in their status history.

## Overview

When Helm deploys a new session-agent image, the running pod finishes its current Claude turn, queues incoming messages in JetStream, tracks and notifies backgrounded shells, writes an "Updating..." state to KV, and exits cleanly. The new pod starts, resumes sessions, drains queued messages (including synthetic shell-killed notifications), and clears the banner. No user messages are lost; no backgrounded shell deaths go unnoticed.

This feature moves all session API subjects from core NATS (fire-and-forget) to JetStream (persistent, at-least-once delivery). Terminal I/O stays on core NATS. It also sets `CLAUDE_CODE_TMPDIR` to a PVC-backed path so shell output files survive pod restarts, enabling resumed Claude instances to read partial output from killed shells.

## Motivation

Session-agent pods are long-lived — they host active Claude conversations, run background shells, and maintain in-memory state. A naive pod replacement (kill old, start new) causes:

1. **Lost messages** — user inputs sent during the restart window vanish on core NATS (fire-and-forget).
2. **Dangling tool_uses** — backgrounded `Bash(run_in_background=true)` shells die with the pod. The resumed Claude has no way to know the shell was killed; the `tool_use` is dangling in the transcript.
3. **No user visibility** — the SPA has no indication that an upgrade is in progress.
4. **Abrupt termination** — mid-turn Claude responses are cut off without completing the thought.

A graceful upgrade flow addresses all four: JetStream durability for messages, synthetic task-notifications for killed shells, an "Updating..." banner in the SPA, and a drain-until-idle strategy for active turns.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| JetStream scope | All `api.sessions.*` subjects | Ensures no messages lost during restart. Create/delete results delivered via existing KV watchers. |
| Terminal subjects | Stay on core NATS | Ephemeral, latency-sensitive. Stale terminal input after restart is meaningless. |
| Consumer split | Two durable pull consumers per agent (cmd + ctl) | During drain, stop cmd consumer (queue new work) but keep ctl consumer active (interrupts, permission responses still work). |
| Drain timeout | Wait indefinitely for idle | User sends interrupt if stuck. K8s 24h `terminationGracePeriodSeconds` is a safety net, not a policy. |
| Drain predicate | `state == idle` AND `inFlightBackgroundAgents == 0` | Main turn must finish and all async Agent subagents must complete before exit. |
| Pending-control handling during drain | Auto-interrupt sessions in `requires_action` | User may be offline/asleep; waiting indefinitely would block the upgrade. After resume, user can re-send and Claude re-issues the tool_use. |
| Banner timing | Write `state:"updating"` to KV immediately on SIGTERM | User sees upgrade is in progress while current turn finishes. More transparent than waiting. |
| In-memory state during drain | Do NOT clobber `sess.state.State`; suppress KV writes | In-memory state must track Claude's live transitions for the drain predicate. KV writes suppressed to keep "updating" banner visible. |
| Create/delete reply mechanism | Publish + KV watch (replaces request-reply) | JetStream messages have no Reply field. SPA already watches session KV; session appears → create succeeded. Error → `api_error` event. |
| Deployment strategy | Recreate | Old pod exits before new pod starts. Prevents two pods consuming from the same JetStream consumer simultaneously. |
| Handle backgrounded shell deaths | Yes, synthetic `<task-notification status=killed>` on drain | Shells are cheap to restart but have side effects; Claude needs to know to adjust intent on resume. |
| Handle agent deaths the same way as shells | No — drain waits for agents to finish naturally | Agents have context + token cost; can't meaningfully "retry". Shells are cheap to restart. |
| Shell notification transport | Publish onto `api.sessions.input` subject in JetStream | Reuses existing durable path. No new storage, no authz to bypass. |
| Shell output file persistence | Set `CLAUDE_CODE_TMPDIR` → PVC subPath `/data/claude-tmp` | Scopes persistence to Claude-owned temp files, reuses existing session PVC, no new volume. |
| Shell tracking structure | `map[toolUseId]*inFlightShell` per session | Need to iterate in-flight shells at drain time; a count alone isn't enough — need toolUseId, command, outputFilePath for the synthetic notification. |
| Shell tracking trigger | Two-phase: (1) `assistant` message with `tool_use` name=Bash + `input.run_in_background:true` → record pending entry; (2) `user` message with matching `tool_result` → extract `backgroundTaskId` from result text, compute `outputFilePath`, promote to `inFlightShells` | `taskId` (e.g. "b3f7x2a9") is Claude Code's internal random ID — NOT `tool_use.id`. It appears in the tool_result text: "Command was manually backgrounded with ID: {backgroundTaskId}". |
| Shell tracking removal | User message with `origin.kind == "task-notification"` referencing the shell's tool-use-id | Real task-notification arrived (shell completed naturally). |
| Shell output path formula | `{CLAUDE_CODE_TMPDIR}/claude-{uid}/{sanitizePath(cwd)}/{sessionId}/tasks/{taskId}.output` | `getClaudeTempDir()` always appends `claude-{uid}` via `getClaudeTempDirName()` (filesystem.ts:345), even when `CLAUDE_CODE_TMPDIR` is set. |
| `sanitizePath` determinism | Deterministic for paths ≤ 200 chars (pure regex replace); for paths > 200 chars appends a wyhash (`Bun.hash()`) or djb2 fallback — consistent within the same Claude Code binary (Bun) | Consistent across pod restarts provided same cwd and same Claude Code binary version. CWD in normal operation is well under 200 chars so the hash branch is not exercised. |
| `<output-file>` in synthetic notification | Informational — Claude Code does NOT auto-read it; the LLM sees the XML and may call Read/Bash tool to inspect the file | No special session-agent handling needed beyond including the path in the XML |

## Upgrade Flow

End-to-end sequence when Helm deploys a new session-agent image:

```
1.  Helm upgrade → ConfigMap `session-agent-template` updated with new image tag
2.  Reconciler detects ConfigMap change → re-enqueues all MCProject CRs
3.  reconcileDeployment sees image mismatch → updates Deployment spec (including Recreate strategy)
4.  K8s sends SIGTERM to old pod (Recreate strategy: old dies first)
5.  Session-agent receives SIGTERM:
      a. Writes state:"updating" + stateSince:now to session KV for ALL sessions (SPA banner).
         Does NOT clobber in-memory sess.state.State — that keeps tracking Claude's live state
         so the drain predicate can detect real idle-vs-running transitions.
      b. Stops command consumer (create/delete/input/restart queue in JetStream).
      c. Drains core NATS subscriptions (terminal.create, terminal.delete, terminal.resize).
      d. Keeps control consumer running (interrupts, permission responses).
      e. Polls (1s tick) until every session satisfies the drain predicate:
         - sess.getState().State == StateIdle — Claude's main turn is not active
         - sess.inFlightBackgroundAgents == 0 — no async Agent(run_in_background=true) calls pending
         Sessions stuck in requires_action are auto-interrupted (synthetic interrupt via control
         path) so pending permission prompts don't block the upgrade. No wall-clock timeout.
      f. For each session, for each entry in sess.inFlightShells:
         - Constructs a <task-notification status=killed> XML message
         - Publishes it as a session-input message to the JetStream cmd consumer subject
         - Messages queue for the new pod (cmd consumer is already stopped)
      g. Stops control consumer.
      h. Publishes lifecycle event "session_upgrading" for each session.
      i. Exits cleanly (exit 0).
6.  K8s terminates old pod, creates new pod
7.  New pod starts:
      a. Entrypoint runs (seeds config, sets up repo)
      b. Session-agent recovers sessions from KV (sessions in "updating" state treated as "idle"
         for resume purposes; KV stays as "updating" so the banner persists during startup)
      c. Attaches to existing durable JetStream consumers
      d. Drains queued messages — including synthetic shell-killed notifications from step 5f
      e. handleInput forwards each synthetic notification to resumed Claude's stdin as a user message
      f. Claude sees <task-notification status=killed> XML, can read the output-file from the PVC
         to understand what the shell had done, and decides what to do next
      g. Writes state:"idle" to session KV for all sessions that were "updating"
      h. UI banner disappears
```

## Component Changes

### JetStream Stream: MCLAUDE_API

New stream created by session-agent on startup (idempotent, same pattern as `MCLAUDE_EVENTS`):

```
Name:      MCLAUDE_API
Subjects:  ["mclaude.users.*.hosts.*.projects.*.api.sessions.>"]
Retention: LimitsPolicy
MaxAge:    1h
Storage:   FileStorage
Discard:   DiscardOld
```

One hour retention — API commands older than 1h are stale and should not be processed.

**NATS ACL note:** The session-agent's per-user JWT already permits `pub` and `sub` on `mclaude.{userId}.>`, and the existing `MCLAUDE_EVENTS` stream (created by the same agent with the same JWT) proves JetStream API access works. `MCLAUDE_API` follows the same pattern.

### Session-Agent: JetStream Consumers

Two durable pull consumers per session-agent, created on startup:

**Command consumer** — handles new work (sessions.create, sessions.delete, sessions.input, sessions.restart):

```
Name:           sa-cmd-{uslug}-{pslug}
FilterSubjects: [
  "mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.create",
  "mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.delete",
  "mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.input",
  "mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.restart"
]
AckPolicy:      Explicit
AckWait:        60s
MaxDeliver:     5
```

**Control consumer** — handles interrupts and permission responses (must stay active during drain):

```
Name:           sa-ctl-{uslug}-{pslug}
FilterSubjects: ["mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.control"]
AckPolicy:      Explicit
AckWait:        60s
MaxDeliver:     5
```

Both consumers are durable — they survive pod restarts. When the new pod attaches, it picks up from the last acknowledged position. Unacked messages from the old pod are redelivered.

### Session-Agent: JetStream Fetch Loop

Each consumer runs in its own goroutine with a pull-based fetch loop:

```go
func (a *Agent) runConsumer(ctx context.Context, cons jetstream.Consumer, dispatch func(jetstream.Msg)) {
    for {
        msgs, err := cons.Fetch(10, jetstream.FetchMaxWait(5*time.Second))
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

**Type adaptation:** JetStream delivers `jetstream.Msg`, not `*nats.Msg`. The existing handler functions accept `*nats.Msg`. Wrap the JetStream message:

```go
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
    case strings.HasSuffix(jm.Subject(), ".sessions.create"):  a.handleCreate(msg)
    case strings.HasSuffix(jm.Subject(), ".sessions.delete"):  a.handleDelete(msg)
    case strings.HasSuffix(jm.Subject(), ".sessions.input"):   a.handleInput(msg)
    case strings.HasSuffix(jm.Subject(), ".sessions.restart"): a.handleRestart(msg)
    }
}
```

**Ack timing:** After the handler returns (not before). If the handler panics, the message is not acked and will be redelivered after AckWait (60s).

**Stopping a consumer:** Cancel the consumer's context. The `Fetch` call returns immediately with `ctx.Err()`. The goroutine exits. The durable consumer remains on the server — the new pod re-attaches to it.

### Session-Agent: In-Flight Tracking

Two tracking structures per session, both guarded by `sess.mu`:

**Background agent counter** — `inFlightBackgroundAgents int`:
- `+1` when the stdout router observes an `assistant` message containing a `tool_use` block where `name == "Agent"` AND `input.run_in_background == true`.
- `-1` (floored at zero) when it observes a top-level `user` message with `origin.kind == "task-notification"`.

The counter is best-effort — if the session-agent was killed mid-flight and relaunched, the counter starts at 0. The new pod's drain logic only protects agents launched after its own startup.

**Background shell map** — `inFlightShells map[string]*inFlightShell` (keyed by toolUseId):

```go
type inFlightShell struct {
    toolUseId      string    // "toolu_..." — Claude API tool_use.id
    taskId         string    // "b3f7x2a9" — Claude Code internal random ID
    command        string    // for the killed-notification summary
    outputFilePath string    // absolute path on the PVC
    startedAt      time.Time
}
```

Shell tracking uses two maps per session:

```go
pendingShells  map[string]pendingShell   // keyed by toolUseId; awaiting tool_result
inFlightShells map[string]*inFlightShell // keyed by toolUseId; fully promoted entries
```

```go
type pendingShell struct {
    toolUseId string
    command   string
    startedAt time.Time
}
```

Shell tracking is **two-phase**:

1. **Phase 1 — Pending**: On `assistant` message with a `tool_use` block where `name == "Bash"` AND `input.run_in_background == true`: record a pending entry (keyed by toolUseId) with `command` captured from `input.command`. The `taskId` and `outputFilePath` are not yet known.

2. **Phase 2 — Promoted**: On `user` message with a `tool_result` block where `tool_use_id` matches a pending entry: parse the result text to extract `backgroundTaskId` (pattern: `"Command was manually backgrounded with ID: (\S+)"`). Construct `outputFilePath` as `filepath.Join(os.Getenv("CLAUDE_CODE_TMPDIR"), fmt.Sprintf("claude-%d", os.Getuid()), sanitizePath(session.cwd), session.id, "tasks", taskId+".output")`. Promote the entry to `inFlightShells`.

**Pending entries that never produce a matching tool_result** (e.g. the Bash tool_use was cancelled before execution) are simply discarded — they never enter `inFlightShells` and won't produce synthetic notifications.

- **Remove from `inFlightShells`:** On `user` message with `origin.kind == "task-notification"` referencing the shell's tool-use-id (real task-notification arrived — shell completed naturally).

### Session-Agent: SIGTERM Handler

Replace the current `gracefulShutdown()` with:

```
On SIGTERM / context cancellation:
1. For each session:
    - Write state:"updating" + stateSince:now to session KV (for SPA banner).
    - Set sess.shutdownPending = true (in-memory flag).
    - Do NOT modify in-memory sess.state.State.
2. Cancel command consumer context (stops cmd fetch loop; messages queue in JetStream).
3. Drain core NATS subscriptions (terminal.create, terminal.delete, terminal.resize).
4. Keep control consumer running (its context is NOT cancelled).
5. Poll loop (1s tick):
    - For each session: evaluate drain predicate:
      - sess.getState().State == StateIdle
      - sess.inFlightBackgroundAgents == 0
    - For sessions in StateRequiresAction: send synthetic interrupt (pending-control interrupt).
    - If ALL sessions satisfy the predicate → break.
6. For each session, for each entry in sess.inFlightShells:
    - Construct <task-notification> XML with status=killed (see Synthetic Task-Notification Format below).
    - Wrap in a full session-input payload: `{"session_id": "{sess.id}", "type": "user", "message": {"role": "user", "content": "{xml}"}}`.
    - Publish onto `subj.UserHostProjectAPISessionsInput(a.userSlug, a.hostSlug, a.projectSlug)`.
    - JetStream persists it in MCLAUDE_API. On the new pod, `handleInput` routes by `session_id` and forwards to Claude's stdin.
7. Cancel control consumer context (stops ctl fetch loop).
8. Publish lifecycle event "session_upgrading" for each session.
9. Exit(0).
```

**Pending-control interrupt** — on every poll tick, for each session in `StateRequiresAction`, the drain loop sends a synthetic interrupt through the same code path as `handleControl`. The turn aborts, the pending tool_use is cancelled, the session transitions to `StateIdle`, and the drain predicate becomes satisfiable. After the new pod resumes with `--resume`, the user can re-send and Claude re-issues the tool_use.

**KV write suppression during drain** — while `sess.shutdownPending == true`, the `SubtypeSessionStateChanged` handler updates in-memory `sess.state.State` as usual but MUST NOT flush state to KV. If it did, a Claude transition from `running` → `idle` would overwrite the `"updating"` banner state.

**Shell notification ordering** — synthetic shell-killed notifications are published (step 6) *after* the main-turn drain completes (step 5) so we don't publish into an active session that would see the notification twice. They are published while the cmd consumer is already stopped, so messages queue for the new pod.

**Shell notification idempotency** — if the pod crashes between publishing some notifications and stopping, the new pod starts with an empty `inFlightShells` map, so no duplicate publishes. Already-published notifications are consumed by the new pod from the durable consumer.

### Session-Agent: Drain — Synthetic Task-Notification Format

The synthetic notification follows Claude Code's native `<task-notification>` XML format:

```xml
<task-notification>
  <task-id>{entry.taskId}</task-id>
  <tool-use-id>{entry.toolUseId}</tool-use-id>
  <output-file>{entry.outputFilePath}</output-file>
  <status>killed</status>
  <summary>Shell "{entry.command}" was killed during server upgrade</summary>
</task-notification>
```

- `killed` is a first-class status in Claude Code's native task-notification schema (emitted by `LocalShellTask.tsx` when a shell is terminated externally).
- The XML is published as the `content` field of a normal session-input message. `handleInput` on the new pod forwards it to Claude's stdin as a user message.
- The resumed Claude sees it in its event log and can read the output-file from the PVC to understand what the shell had done before deciding what to do next.

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
- Treat it as `"idle"` for resume purposes (the session was idle when the old pod wrote "updating").
- **Do NOT write state to KV yet** — keep "updating" in KV so the UI banner stays visible during startup.
- `clearUpdatingState()` in step 4 writes `state:"idle"` only after consumers are attached and the agent is ready to process messages.

### Session-Agent: Reply Mechanism Change

Handlers switch from `msg.Respond()` (request-reply) to writing results via existing side channels. The `reply()` helper becomes a no-op when `msg.Reply == ""` (always true for JetStream messages since the wrapped `*nats.Msg` has no Reply field).

| Handler | Old reply | New reply |
|---------|----------|-----------|
| `handleCreate` | `msg.Respond({id: sessionId})` | Session appears in KV (SPA watches KV). Error → publish `api_error` event. |
| `handleDelete` | `msg.Respond({})` | Session disappears from KV. Error → publish `api_error` event. |
| `handleRestart` | `msg.Respond({})` | Session state transitions through `restarting` in KV. Error → publish `api_error` event. |
| `handleInput` | (none — fire-and-forget) | No change. |
| `handleControl` | (none — fire-and-forget) | No change. |

**Error event subject:** `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events._api`

Project-level subject (no sessionId) because some errors (e.g., create failures) occur before a session exists. The `_api` suffix distinguishes it from session event subjects. The `MCLAUDE_EVENTS` stream captures it via the existing wildcard filter.

**Error event payload:**

```json
{
  "type": "api_error",
  "request_id": "<client-generated UUID>",
  "operation": "create | delete | restart",
  "error": "<error message>"
}
```

**Handler request struct changes** — add `RequestID string` field to create, delete, and restart request structs so it can be echoed in error events.

### Session-Agent: Shell Output Path Construction

The synthetic notification needs to reference the same output path Claude used. Helper:

```go
func sanitizePath(p string) string {
    // Matches Claude Code's TypeScript sanitizePath (sessionStoragePortable.ts).
    // Replace any char that is not [a-zA-Z0-9] with '-'.
    // For paths > 200 chars, append a Bun.hash() suffix — but this branch is never
    // reached in practice (typical CWDs are well under 200 chars).
    result := []byte(p)
    for i, c := range result {
        if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
            result[i] = '-'
        }
    }
    return string(result)
}

func shellOutputPath(tmpDir, sanitizedCwd, sessionId, taskId string) string {
    uid := os.Getuid()
    return filepath.Join(tmpDir, fmt.Sprintf("claude-%d", uid),
        sanitizedCwd, sessionId, "tasks", taskId+".output")
}
// Called as: shellOutputPath(os.Getenv("CLAUDE_CODE_TMPDIR"), sanitizePath(cwd), sessionId, taskId)
```

Path expansion: `CLAUDE_CODE_TMPDIR=/data/claude-tmp` → `/data/claude-tmp/claude-{uid}/{sanitized-cwd}/{sessionId}/tasks/{taskId}.output`

**Note:** `getClaudeTempDir()` in Claude Code (`filesystem.ts:345`) always appends `claude-{uid}` (via `getClaudeTempDirName()`) to the base temp directory, even when `CLAUDE_CODE_TMPDIR` is set. So the full path always includes the `claude-{uid}` component regardless of whether the env var is set.

**BYOH/daemon mode:** When `CLAUDE_CODE_TMPDIR` is not set (e.g. BYOH mode, local controller-local sessions), `os.Getenv("CLAUDE_CODE_TMPDIR")` returns `""` and `filepath.Join("", ...)` produces a relative path that does not match Claude Code's actual temp dir. Shell tracking (pending and promoted phases) and the synthetic notification publish are **disabled** when `CLAUDE_CODE_TMPDIR` is empty — the SIGTERM drain loop skips step 6 and session-agent exits without publishing shell notifications. This is a K8s-only feature.

### Session State Constants

Add `StateUpdating` to state constants in `events.go`:

```go
const (
  StateIdle           = "idle"
  StateRunning        = "running"
  StateRequiresAction = "requires_action"
  StateUpdating       = "updating"  // ← new
)
```

`clearPendingControlsForResume()` already sets state to idle unconditionally, so `"updating"` is handled by default.

### Control-Plane: Reconciler ConfigMap Watch

Add a watch on the `session-agent-template` ConfigMap to `SetupWithManager`. When the ConfigMap changes, re-enqueue all MCProject CRs so `reconcileDeployment` compares the new template image against each Deployment.

Uses `handler.EnqueueRequestsFromMapFunc` with a predicate filtered by name and namespace to avoid firing on every ConfigMap change cluster-wide.

### Control-Plane: Deployment Strategy

The Deployment strategy is set to `Recreate` in `reconciler.go` — `MCProjectReconciler.reconcileDeployment` (controller-runtime). This is the sole Deployment provisioning path.

Both create and update branches set:

```go
deploy.Spec.Strategy = appsv1.DeploymentStrategy{
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

The template at `session-agent-pod-template.yaml` already renders this value — no template change needed.

**PVC mount for Claude temp dir** — implemented in `buildPodTemplate()` in `mclaude-controller-k8s/reconciler.go` (the pod spec is built in Go, not in the Helm template YAML):

```go
// Env var
corev1.EnvVar{Name: "CLAUDE_CODE_TMPDIR", Value: "/data/claude-tmp"}

// VolumeMount — reuses the existing `project-data` volume (already mounted at /data)
corev1.VolumeMount{Name: "project-data", MountPath: "/data/claude-tmp", SubPath: "claude-tmp"}
```

Reuses the existing `project-data` PVC volume via `SubPath`. No new volume declaration. This path survives pod restart — when the new pod mounts the same subPath, Claude resumes with `--resume` and can read old output files when processing synthetic task-notifications.

### SPA: Session Store — `onSessionAdded` Method

New method fires when a new session key appears in the KV watcher for the given project. Returns an unsubscribe function.

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

The KV watch callback calls `_notifyAddListeners(id, state)` on every PUT event, after `_sessions.set()` and `_notifySessions()`.

### SPA: Session Store — kvWatch DEL Fix

**Prerequisite:** `kvWatch` must pass DEL entries to the callback. Currently `nats-client.ts` skips DEL/PURGE operations entirely. Fix:

- Extend `KVEntry` in `types.ts` with `operation?: 'PUT' | 'DEL' | 'PURGE'`.
- In `nats-client.ts` kvWatch loop, include DEL entries with the operation field.
- Update `session-store.ts` to check `entry.operation` and handle deletions (remove from `_sessions` map, notify listeners).

### SPA: API Error Listener

`api_error` events are published to a project-level subject. The existing `EventStore` is session-scoped and cannot own this subscription.

Instead, `createSession()` in `SessionListVM` creates a temporary core NATS subscription:

```typescript
// hslug, pslug resolved from project store (same as in createSession)
const apiSubject = subjEventsApi(this.userSlug as UserSlug, hslug, pslug)
const unsubErr = this.natsClient.subscribe(apiSubject, (msg) => {
  const event = JSON.parse(new TextDecoder().decode(msg.data)) as { type?: string; request_id?: string; error?: string }
  if (event.type === 'api_error' && event.request_id === requestId) {
    cleanup()
    reject(new Error(event.error ?? 'api_error'))
  }
})
```

Cleaned up when the promise resolves (success, error, or 30s timeout).

### SPA: Session List VM

**`createSession()`** — switch from request-reply to publish + KV watch:

```typescript
async createSession(projectId: string, branch: string, name: string): Promise<string> {
  const requestId = crypto.randomUUID()
  const project = this.sessionStore.projects.get(projectId)
  const pslug = (project?.slug ?? projectId) as ProjectSlug
  const hslug = (project?.hostSlug ?? 'local') as HostSlug
  const subject = subjSessionsCreate(this.userSlug as UserSlug, hslug, pslug)
  const payload = { projectId, branch, name, requestId }
  this.natsClient.publish(subject, new TextEncoder().encode(JSON.stringify(payload)))

  return new Promise((resolve, reject) => {
    const cleanup = () => { clearTimeout(timer); unsubKV?.(); unsubErr?.() }
    const timer = setTimeout(() => { cleanup(); reject(new Error('Create session timed out')) }, 30_000)
    const unsubKV = this.sessionStore.onSessionAdded(projectId, (session) => {
      cleanup(); resolve(session.id)
    })
    const unsubErr = this.natsClient.subscribe(
      subjEventsApi(this.userSlug as UserSlug, hslug, pslug), (msg) => {
        const event = JSON.parse(new TextDecoder().decode(msg.data)) as { type?: string; request_id?: string; error?: string }
        if (event.type === 'api_error' && event.request_id === requestId) {
          cleanup(); reject(new Error(event.error ?? 'api_error'))
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
  const project = this.sessionStore.projects.get(session.projectId)
  const pslug = (project?.slug ?? session.projectId) as ProjectSlug
  const hslug = (project?.hostSlug ?? session.hostSlug ?? 'local') as HostSlug
  const subject = subjSessionsDelete(this.userSlug as UserSlug, hslug, pslug)
  this.natsClient.publish(subject, new TextEncoder().encode(JSON.stringify({ sessionId })))
}
```

### SPA: Session Detail Screen

Add `updating` state to `STATE_LABELS`:

```typescript
const STATE_LABELS: Record<string, string> = {
  // ...existing states...
  updating: 'Updating...',
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

Add `updating: 'Updating...'` to `DashboardScreen.tsx` `STATE_LABELS`. Update the `StatusDot` type cast to include `'updating'`.

### SPA: StatusDot

Add `updating` to `STATE_COLORS` (blue) and `PULSE_STATES`:

```typescript
updating: 'var(--blue)',
const PULSE_STATES = new Set(['working', 'running', 'connecting', 'updating'])
```

Blue pulsing dot — distinct from orange/running and red/error.

### SPA: TypeScript Types

Add `'updating'` to the `SessionState` union and `'session_upgrading'` to the `LifecycleEvent.type` union in `types.ts`.

## Impact

**Specs updated in this commit:**
- `docs/spec-state-schema.md` — fix `MCLAUDE_API` subject pattern (`resume` → `restart`); update `sessions.input` payload (`{type, message, sessionSlug}` → `{session_id, type, message}`); update `sessions.create` row (request/reply → publish+KV-watch, add `requestId`); add `requestId` to `sessions.delete` and `sessions.restart` payloads; update `sessions.control` JetStream delivery note; add `StateUpdating` constant
- `docs/mclaude-session-agent/spec-session-agent.md` — MCLAUDE_API stream, durable consumers, SIGTERM drain flow, shell tracking (two-phase pending/inFlight), startup recovery, reply mechanism changes, `api_error` payload schema, `CLAUDE_CODE_TMPDIR` config entry, pod-crash error entry
- `docs/mclaude-web/spec-web.md` — Session Management section: `createSession()` publish+KV-watch, `deleteSession()` fire-and-forget, `SessionStore.onSessionAdded()`, KV DEL/PURGE handling, `updating` session state visual treatment

**Components implementing the change:**
- `mclaude-session-agent` — primary; JetStream consumers, SIGTERM handler, shell tracking, startup recovery
- `mclaude-controller-k8s` — ConfigMap watch, Recreate strategy
- `mclaude-web` — SPA session store, session-list VM, StatusDot, Dashboard/Detail screens, types
- `charts/mclaude-worker` — `terminationGracePeriodSeconds`, `CLAUDE_CODE_TMPDIR` PVC mount

## Error Handling

| Scenario | Behavior |
|----------|----------|
| SIGTERM while all sessions idle, no in-flight agents or shells | Write "updating" to KV, stop cmd consumer, drain terminal subs, predicate satisfied immediately, no shell notifications needed, stop ctl consumer, exit. |
| SIGTERM while a session is mid-turn (`state == running`) | Write "updating" to KV (not in-memory). Stop cmd consumer, keep ctl consumer. Poll until Claude finishes (state → idle), then check agents/shells. |
| SIGTERM while a session has in-flight background agents | Write "updating" to KV. Keep ctl consumer. Poll until each async Agent's `task-notification` is observed (counter hits 0). |
| SIGTERM while backgrounded shells are running | After main-turn drain + agent drain, publish synthetic `<task-notification status=killed>` for each shell in `inFlightShells`. Messages queue in JetStream for the new pod. |
| Shell completes naturally during drain (before shell publish step) | Real task-notification arrives; router removes from `inFlightShells` before the publish loop. No synthetic message published. |
| User sends interrupt during drain | Control consumer processes it. Session goes idle. Pod exits. |
| User sends permission response during drain | Control consumer processes it. Turn continues/completes. Pod exits when drain predicate is satisfied. |
| User does NOT respond to permission prompt during drain | Drain loop sends a synthetic interrupt on the next poll tick. Turn aborts, session transitions to idle, pod exits. User's last request remains in transcript. |
| New user message during drain | Queues in JetStream. Processed by new pod after restart. |
| Create request during restart | Queues in JetStream. New pod processes it. SPA waits for KV (30s timeout). |
| Pod crashes (no SIGTERM) | K8s recreates pod. Durable consumer has unacked messages → redelivered. Sessions resume from KV. In-flight shells are lost (counter was in memory only); dangling tool_uses in transcript — Claude notices when it tries `BashOutput` on unknown shell-id. |
| Pod crashes mid-drain, after publishing some shell notifications | Published messages are durable in JetStream; new pod consumes them. Unpublished in-flight shells are lost (empty map on new process). |
| Resumed Claude's output-file read fails (PVC mount issue) | Claude sees the killed notification but can't read partial output; decides based on summary alone. Volume-mount failures logged as operator errors. |
| Turn never finishes | User sends interrupt via control subject. K8s kills at 24h as last resort. |
| Second Helm deploy during drain | K8s updates Deployment spec. Current drain continues. New pod starts with latest image. |
| Message redelivered 5 times (MaxDeliver) | Message dropped. Indicates a bug — the handler is consistently failing. |

## Open Questions

(None — all questions resolved. See Decisions table.)

## Scope

**v1 (this ADR):**
- `MCLAUDE_API` JetStream stream for all session API subjects
- Two durable pull consumers per session-agent (cmd + ctl)
- JetStream fetch loop with `jetstream.Msg` → `*nats.Msg` adapter
- SIGTERM graceful drain: write `"updating"` to KV only (not in-memory state) → stop cmd → poll drain predicate (state == idle AND in-flight bg agents == 0) → auto-interrupt any session in `requires_action` → publish synthetic shell-killed notifications → stop ctl → exit
- Per-session `inFlightBackgroundAgents` counter maintained by stdout router
- Per-session `inFlightShells` map maintained by stdout router (keyed by toolUseId)
- Synthetic `<task-notification status=killed>` for each outstanding shell, published to JetStream on drain
- `CLAUDE_CODE_TMPDIR=/data/claude-tmp` env var; PVC subPath mount for output file persistence
- KV write suppression in `SubtypeSessionStateChanged` handler while `shutdownPending` is set
- Reconciler watches ConfigMap (filtered by name + namespace), re-enqueues MCProject CRs on image change
- `Recreate` deployment strategy for session-agent pods (both create and update paths in `reconcileDeployment`)
- `terminationGracePeriodSeconds: 86400` (values.yaml)
- SPA: create/delete switch from request-reply to publish + KV watch
- SPA: `SessionStore.onSessionAdded()` method; kvWatch DEL fix; temporary NATS sub for `api_error`
- SPA: `SessionState` type union and `LifecycleEvent.type` union updated
- SPA: "Updating..." banner, blue pulsing StatusDot, Dashboard/Detail screen updates
- `api_error` events on `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events._api` for failed create/delete/restart
- `StateUpdating` constant in `events.go`
- Two-phase shell tracking: pending entry on tool_use, promoted on tool_result with backgroundTaskId

**Deferred:**
- Version pinning per project (`spec.imageOverride` on MCProject CRD)
- Canary rollouts (gradual upgrade across user pods)
- Pre-pull new image before drain (minimize restart window)
- Dead letter queue for messages that exceed MaxDeliver
- Persist `inFlightShells` to KV so pod crashes don't lose the record
- Auto-restart shells with adjusted intent (Claude decides via the killed notification)
- Extending shell survival pattern to other in-pod side-effect processes (hooks, file watchers)

## References

- Native Claude Code task-notification emission: `LocalShellTask.tsx:105-172`, `LocalAgentTask.tsx:200-262`
- Notification queue: `src/utils/messageQueueManager.ts:142-149`, `src/utils/task/framework.ts:255-269`
- Output path: `src/utils/task/diskOutput.ts:50-74`, `src/utils/permissions/filesystem.ts:307-378`
- Env var: `CLAUDE_CODE_TMPDIR` in `getClaudeTempDir()` at `src/utils/permissions/filesystem.ts:331-346`
- Source checkout: `/Users/rsong/work/collection-claude-code-source-code/`
