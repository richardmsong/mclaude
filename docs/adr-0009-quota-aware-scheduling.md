# Quota-Aware Session Scheduling

**Status**: accepted
**Status history**:
- 2026-04-14: accepted


> **Note:** Sections of this ADR that describe slug derivation from the `docs/plan-` filename prefix are superseded by `adr-0021-docs-plan-spec-refactor.md`. The scheduler still takes a `specPath` but the prefix is now `docs/adr-YYYY-MM-DD-` or `docs/spec-` — the code derives the slug by stripping the leading `docs/` and trailing `.md`, then additionally stripping any `adr-YYYY-MM-DD-` or `spec-` prefix.

## Overview

Enables unattended dev-harness implementation sessions that run as remote mclaude sessions, managed by a priority-aware job queue, with real-time quota monitoring that gracefully stops low-priority work before the 5-hour Anthropic API usage window is exhausted. Users queue jobs by spec path via `/schedule-feature`; the daemon starts sessions immediately as capacity allows, monitors API utilization by polling the Anthropic OAuth usage endpoint, and pauses lower-priority sessions when quota is tight. On successful completion the session creates a GitHub PR for human review.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Quota data source | `api.anthropic.com/api/oauth/usage` via OAuth token | No dependency on claude-pace or any shell plugin. Same endpoint claude-pace's API fallback uses. Works wherever the daemon runs as long as `~/.claude/.credentials.json` exists. |
| Quota publisher | Daemon goroutine | Daemon (laptop) already has credentials and HTTP client. Single publisher per user; K8s session-agents subscribe to NATS. |
| Job persistence | New `mclaude-job-queue` KV bucket | Consistent with existing KV patterns; survives daemon restarts; each entry is inspectable and cancellable by key. |
| Parallelism model | All queued jobs start immediately; priority-based preemption when threshold hit | Maximize throughput by default. User-assigned priorities determine which sessions survive quota pressure. |
| Permission policy for unattended sessions | New `strict-allowlist` mode: auto-approve allowlisted tools, auto-deny everything else | Prevents blocking on out-of-allowlist tool prompts. Denial signals a spec gap and triggers fail-fast with a lifecycle event. |
| Time-based scheduling (`--at`) | Deferred to v2 | User priority is throughput, not specific time windows. Jobs start as quota allows. |
| Priority preemption mechanism | Graceful stop, lowest-priority first | Lets Claude finish its current commit before stopping; no work is lost. |
| Worktree per job | Branch `schedule/{slug}-{shortId}` | Each job gets an isolated worktree; concurrent jobs for different specs cannot conflict via shared files. Git conflicts between parallel sessions can be resolved by Claude because each session is aware of all concurrently running jobs. |
| Auto-continuation | Optional flag per job | Re-queues at the 5h reset time when `--auto-continue` is set. |
| PR creation | Claude creates the PR in the session as its final step | Keeps PR logic inside the dev-harness prompt; surfaces work for human review before merge. |
| Job HTTP endpoints | Daemon exposes local HTTP server on localhost:8378; skills call it directly | Loopback-only, no auth needed (daemon knows userId from config). V1 components (mclaude-server, connector, MCP) are not modified; proxy chain deferred to v2 for BYOM compatibility. |
| Permission-denied signaling | In-process Go channel from `Session.onStrictDeny` to `QuotaMonitor.permDeniedCh`; lifecycle event published via `Agent.publishPermDenied` | Avoids a NATS round-trip for an in-process event; simpler than subscribing the monitor to the lifecycle subject. |
| Completion detection | New `Session.onRawOutput` callback scans assistant events for `SESSION_JOB_COMPLETE:` marker | Extends the existing callback pattern; lets the QuotaMonitor parse the PR URL from Claude's assistant text without modifying the NATS event router. |

## User Flow

1. User finishes a spec at `docs/adr-YYYY-MM-DD-spa.md` and wants it implemented unattended.

2. User runs:
   ```
   /schedule-feature docs/adr-YYYY-MM-DD-spa.md [--priority 7] [--threshold 75] [--auto-continue]
   ```

3. The `/schedule-feature` skill:
   - Verifies the spec path exists.
   - Calls `GET /jobs/projects` and matches `basename(CWD) == project.name` to find `projectId`. If no match, asks user to pick.
   - Calls `mcp__mclaude__create_job` with spec path, priority, threshold, autoContinue, and projectId.
   - The skill POSTs to `http://localhost:8378/jobs`, which writes a `JobEntry` to the `mclaude-job-queue` KV bucket with status `queued`.
   - Responds with the job ID and current queue status.

4. The daemon's job dispatcher sees the new `queued` entry. If current 5h utilization (`u5`) < threshold, it:
   - Generates branch name `schedule/spa-{shortId}` (slug from spec path, shortId from first 8 chars of job ID). Stores in `job.Branch`.
   - Sends `sessions.create` to `mclaude.{userId}.{projectId}.api.sessions.create` with:
     - `branch: "schedule/spa-{shortId}"` — `handleCreate` in the session-agent creates the git worktree for this branch (existing behavior)
     - `permPolicy: "strict-allowlist"`, `allowedTools: [default dev-harness list]`
     - `quotaMonitor: {threshold, priority, jobId, autoContinue}`
   - Polls `mclaude-sessions` KV for this session's state = `idle` (up to 30s).
   - Sends the dev-harness prompt via `mclaude.{userId}.{projectId}.api.sessions.input`.
   - Updates job to `running`.

5. The session runs unattended. The `QuotaMonitor` goroutine in the session-agent subscribes to `mclaude.{userId}.quota` and watches for threshold breaches.

6. **Normal completion path**: Session runs `/dev-harness` for the component, exhausts all spec gaps, achieves a CLEAN audit-only, then runs `gh pr create --base main --head schedule/spa-{shortId}` as its final step and outputs `SESSION_JOB_COMPLETE:{prUrl}`. The `QuotaMonitor`'s `onRawOutput` callback detects this marker and records the PR URL. Session exits. Monitor publishes `session_job_complete` lifecycle event. Daemon's `runLifecycleSubscriber` receives the event and marks the job `completed` in `jobQueueKV`.

7. **Quota-interrupted path**: When 5h utilization hits threshold:
   a. The daemon's `runJobDispatcher` identifies running jobs, sorts by priority ascending (lowest first), and sends the graceful stop NATS message (with `session_id`) to the lowest-priority session(s). The job status is updated to `paused` in `jobQueueKV` and `session_job_paused` is published.
   b. Independently, the session's `QuotaMonitor` also detects `u5 >= threshold` via its `mclaude.{userId}.quota` subscription and sends a graceful stop directly to `s.stdinCh`.
   c. Claude finishes its current task and commits. The session process exits (`s.doneCh` closes).
   d. `QuotaMonitor.publishExitLifecycle()` publishes `session_quota_interrupted` (includes `u5`, `r5`, `jobId`).
   e. `runLifecycleSubscriber` receives the event. If `autoContinue`: confirms `status=paused`, sets `resumeAt=r5`. The dispatcher resets `paused` jobs to `queued` after `r5` passes. If not `autoContinue`: sets `status=queued` immediately. The dispatcher picks it up automatically when `u5 < threshold` (no manual action required — `autoContinue` controls only whether to wait until the 5h reset time, not whether to auto-restart).

8. **Permission-denied path**: If the session requests a tool outside the allowlist:
   a. Session-agent auto-denies; calls `sess.onStrictDeny(toolName)`.
   b. `onStrictDeny` publishes `session_permission_denied` lifecycle event AND sends `toolName` on `monitor.permDeniedCh`.
   c. QuotaMonitor receives on `permDeniedCh` and sends the graceful stop message immediately (does not wait for quota threshold).
   d. `runLifecycleSubscriber` receives `session_permission_denied`, sets job `status=needs_spec_fix`, `failedTool`. Job remains in queue but will not restart automatically. User must update the spec and re-queue.

9. User can inspect the queue at any time with `/job-queue`.

## Component Changes

### Daemon (`mclaude-session-agent/daemon.go`)

Two new goroutines added to `Daemon.Run()`. Two new KV handles added to `Daemon` and opened in `NewDaemon()`.

#### New fields on `Daemon` struct

```go
type Daemon struct {
    // ...existing fields...
    sessKV       jetstream.KeyValue  // mclaude-sessions — read-only for startup recovery
    jobQueueKV   jetstream.KeyValue  // mclaude-job-queue — read/write for dispatcher
    projectsKV   jetstream.KeyValue  // mclaude-projects — read-only for GET /jobs/projects
}
```

All three are opened in `NewDaemon()` using `js.KeyValue(ctx, bucketName)` (same pattern as `laptopsKV`). The daemon fails fast if any bucket does not exist (same as `NewAgent`).

#### New field in `DaemonConfig`

```go
CredentialsPath string // path to ~/.claude/.credentials.json; default "$HOME/.claude/.credentials.json"
```

#### `runQuotaPublisher(ctx context.Context)`

- Polls every 60 seconds.
- Reads the OAuth bearer token from the file at `cfg.CredentialsPath` (field `.claudeAiOauth.accessToken`). Returns `HasData: false` if the file is missing or the token is empty — quota monitoring is best-effort.
- Calls `GET https://api.anthropic.com/api/oauth/usage` with:
  ```
  Authorization: Bearer {token}
  anthropic-beta: oauth-2025-04-20
  Content-Type: application/json
  ```
- Parses JSON response: `{five_hour: {utilization, resets_at}, seven_day: {utilization, resets_at}}`.
- Marshals into `QuotaStatus` and publishes to NATS subject `mclaude.{userId}.quota` (core NATS, no JetStream retention).
- On HTTP or parse error: publishes `QuotaStatus{HasData: false}`.
- Sends the same `QuotaStatus` value on an internal `chan QuotaStatus` consumed by `runJobDispatcher`.

#### `runLifecycleSubscriber(ctx context.Context)`

Subscribes to `mclaude.{userId}.*.lifecycle.*` (NATS wildcard, core NATS). Handles the four job lifecycle event types emitted by QuotaMonitor goroutines in child session-agents, and updates `d.jobQueueKV` accordingly. This is the single code path that writes terminal job state back to KV; the QuotaMonitor only publishes NATS events.

Event handling:

| Event type | Action |
|-----------|--------|
| `session_job_complete` | Read job by `jobId`. Set `status=completed`, `prUrl`, `completedAt=now()`. Write back. |
| `session_quota_interrupted` | Read job by `jobId`. If `autoContinue`: set `status=paused`, `resumeAt=r5` (dispatcher will reset to `queued` after reset time). Else: set `status=queued` directly (dispatcher auto-restarts when quota recovers). Write back. |
| `session_permission_denied` | Read job by `jobId`. Set `status=needs_spec_fix`, `failedTool`. Write back. |
| `session_job_failed` | Read job by `jobId`. Set `status=failed`, `error`. Write back. |
| `session_job_paused` | No-op — state is already written by the dispatcher in the quota-exceeded path. Logged for observability. |

For any unrecognized `jobId` (e.g., lifecycle event for a non-scheduled session): ignore silently.

Started by `Daemon.Run()` alongside the quota publisher and job dispatcher.

#### `runJobDispatcher(ctx context.Context)`

- Receives `QuotaStatus` updates from the quota publisher channel.
- Watches `d.jobQueueKV` via `WatchAll` for KV changes.
- On **new or updated entry** with status `queued` AND latest quota `u5 < threshold`:
  - Reads `job.ProjectID` from the entry (set at job creation time by the MCP tool).
  - Derives `{slug}` from `specPath` (same stripping logic as [spec path → component mapping](#spec-path--component-mapping): strip `docs/plan-` prefix and `.md` suffix). Derives `{shortId}` = first 8 characters of `job.ID`. Sets `job.Branch = "schedule/{slug}-{shortId}"`. Sets `job.Status = "starting"`. Writes updated entry to `d.jobQueueKV` — this persists the `starting` status so daemon-restart recovery can detect mid-flight jobs.
  - Derives component from `specPath` using the [spec path → component mapping](#spec-path--component-mapping).
  - Constructs the sessions.create request and sends to `mclaude.{userId}.{job.ProjectID}.api.sessions.create` (NATS request/reply, 10s timeout).
  - Polls `d.sessKV` for the new session to reach state `idle` (up to 30s, 500ms intervals).
  - Reads the `sessionID` from the `sessions.create` NATS reply (field `id`). This is the value used for all subsequent NATS messages and KV writes for this job.
  - Polls `d.sessKV` for the new session to reach state `idle` (up to 30s, 500ms intervals). KV key format: `{userId}.{projectId}.{sessionID}` (matching `sessionKVKey` in state.go). If the poll times out after 30s: increment `job.RetryCount`, reset `job.Status = "queued"`, and write back to KV. If `RetryCount >= 3`: set `job.Status = "failed"`, `job.Error = "session failed to start after 3 attempts"`, and stop retrying.
  - Sends the dev-harness prompt to `mclaude.{userId}.{job.ProjectID}.api.sessions.input` (fire-and-forget, no reply). The payload uses the reply-returned `sessionID`: `{"type":"user","message":{"role":"user","content":"{prompt}"},"session_id":"{sessionID}"}`.
  - Updates job status to `running`, sets `SessionID = sessionID` (from the reply) and `StartedAt`, writes back to `d.jobQueueKV`.
- On **quota threshold exceeded** (latest `QuotaStatus` has `u5 >= threshold`):
  - Reads all jobs in `running` state from `d.jobQueueKV`.
  - Sorts by `Priority` ascending (lowest first).
  - For each, sends graceful stop via NATS to `mclaude.{userId}.{job.ProjectID}.api.sessions.input`; updates job to `paused`. The payload must include a top-level `session_id` field so `handleInput` can route it to the correct session: `{"type":"user","message":{"role":"user","content":"QUOTA_THRESHOLD_REACHED..."},"session_id":"{job.SessionID}"}`. The content string is identical to the QuotaMonitor's graceful stop message.
  - Publishes `session_job_paused` lifecycle event to `mclaude.{userId}.{job.ProjectID}.lifecycle.{job.SessionID}` using `d.nc.Publish` with the payload from the data model (includes `priority`, `u5`, `jobId`).
  - Stops all running jobs whose individual threshold is exceeded. (No hysteresis needed — the 5h usage window is monotonically increasing and resets to zero; oscillation is impossible.)
- On **quota recovery** (latest `u5 < threshold` — only happens after the 5h window resets):
  - Reads all `paused` jobs; sorts by `Priority` descending (highest first).
  - Resets each to `queued`. The watch loop picks them up and starts them.
- On **daemon startup** (called once before entering the watch loop):
  - Reads all entries from `d.jobQueueKV`.
  - Jobs in `starting` state: `SessionID` is empty (not yet assigned — the session create request was sent but the job never reached `running`). Reset directly to `queued` without any KV lookup.
  - Jobs in `running` state: look up `job.SessionID` in `d.sessKV`. If the session no longer exists → reset job to `queued`.
  - Jobs in `paused` state with `ResumeAt != nil && ResumeAt.Before(now)` → reset to `queued`.

#### Spec Path → Component Mapping

The dispatcher strips the `docs/plan-` prefix and `.md` suffix from `specPath`, then maps:

| `specPath` | Component |
|-----------|-----------|
| `docs/adr-YYYY-MM-DD-spa.md` | `spa` |
| `docs/adr-YYYY-MM-DD-session-agent.md` | `session-agent` |
| `docs/adr-0003-k8s-integration.md` | `control-plane` |
| `docs/adr-0006-client-architecture.md` | `spa` |
| `docs/adr-0007-github-oauth.md` | `control-plane` |
| `docs/adr-0009-quota-aware-scheduling.md` | `all` |
| Any unrecognized `plan-*.md` | `all` |

The component is passed as the argument to `/dev-harness <component>` in the session prompt.

#### Scheduled Session Prompt

The dispatcher sends this as the initial user message via `sessions.input`:

```
You are running as an unattended scheduled dev-harness session.

Spec: {specPath}
Component: {component}
Priority: {priority}
Branch: {branch}

Concurrent sessions also running:
{list of other running/queued job specPaths, or "none"}

Instructions:
1. Run: /dev-harness {component}
2. When your work is complete (all spec gaps closed, all tests passing), run:
   gh pr create --base main --head {branch} --title "feat({component}): scheduled dev-harness [auto]" --body "Auto-created by scheduled dev-harness session for {specPath}."
   Then output on its own line: SESSION_JOB_COMPLETE:{prUrl}
3. If you receive a message starting with QUOTA_THRESHOLD_REACHED, immediately:
   a. Finish your current task and commit.
   b. Run: /dev-harness {component} --audit-only
   c. Output the full gap report.
   d. Stop without starting new work.
```

### Session-Agent (`agent.go`, `session.go`)

#### New Permission Policy: `strict-allowlist`

`PermissionPolicyStrictAllowlist` — like `PermissionPolicyAllowlist` but auto-denies tools not in the allowlist instead of forwarding to the client.

In `handleSideEffect` (session.go), when the policy is `strict-allowlist` and `shouldAutoApprove` returns false:
1. Build a deny `control_response`:
   ```go
   resp, _ := json.Marshal(map[string]any{
       "type": "control_response",
       "response": map[string]any{
           "subtype":    "success",
           "request_id": cr.RequestID,
           "response":   map[string]string{"behavior": "deny"},
       },
   })
   s.stdinCh <- resp
   ```
2. Call `s.clearPendingControl(cr.RequestID, writeKV)`.
3. Read `s.onStrictDeny` under `s.mu.Lock()`, then call it outside the lock if non-nil (same pattern as `onEventPublished` reads in the stdout router).

**New callbacks on `Session` struct** (both nil by default; set by Agent after session creation):

```go
// onStrictDeny, if non-nil, is called when a strict-allowlist session
// auto-denies a control_request. Receives the tool name from the request.
onStrictDeny func(toolName string)

// onRawOutput, if non-nil, is called for every raw stdout line from Claude
// (in the stdout router goroutine) before the line is published to NATS.
// Used by QuotaMonitor to scan assistant events for the SESSION_JOB_COMPLETE marker.
onRawOutput func(evType string, raw []byte)
```

The `onRawOutput` callback is invoked in `session.start()`'s stdout router goroutine, after `evType` is parsed and before the NATS publish. It is read under `s.mu.Lock()` (same as `onEventPublished`) to avoid a data race:
```go
s.mu.Lock()
notify := s.onRawOutput
s.mu.Unlock()
if notify != nil {
    notify(evType, lineCopy)
}
```

The Agent sets both callbacks in `handleCreate` **before** calling `sess.start()` (same requirement as `onEventPublished`). The stdout router goroutine reads these fields immediately after `start()`, so they must be set while the session is still single-threaded:
```go
jobID := req.QuotaMonitor.JobID
monitor := newQuotaMonitor(...)
sess.onStrictDeny = func(toolName string) {
    a.publishPermDenied(sessionID, toolName, jobID)
    monitor.signalPermDenied(toolName)
}
sess.onRawOutput = monitor.onRawOutput
// onEventPublished is also set before start() — see existing pattern in handleCreate.
// Now start the process:
if err := sess.start(...); err != nil { ... }
```

**`a.publishLifecycleExtra(sessionID, eventType string, extra map[string]string)`** — new method on `Agent` used by `QuotaMonitor` for events that carry additional fields beyond `type`/`sessionId`/`ts`:
```go
func (a *Agent) publishLifecycleExtra(sessionID, eventType string, extra map[string]string) {
    subject := fmt.Sprintf("mclaude.%s.%s.lifecycle.%s", a.userID, a.projectID, sessionID)
    payload := map[string]string{
        "type":      eventType,
        "sessionId": sessionID,
        "ts":        time.Now().UTC().Format(time.RFC3339),
    }
    for k, v := range extra {
        payload[k] = v
    }
    out, _ := json.Marshal(payload)
    _ = a.nc.Publish(subject, out)
}
```

**`a.publishPermDenied(sessionID, toolName, jobID string)`** — new method on `Agent`:
```go
func (a *Agent) publishPermDenied(sessionID, toolName, jobID string) {
    subject := fmt.Sprintf("mclaude.%s.%s.lifecycle.%s", a.userID, a.projectID, sessionID)
    payload, _ := json.Marshal(map[string]string{
        "type":      "session_permission_denied",
        "sessionId": sessionID,
        "tool":      toolName,
        "jobId":     jobID,
        "ts":        time.Now().UTC().Format(time.RFC3339),
    })
    _ = a.nc.Publish(subject, payload)
}
```

**Default dev-harness allowlist** (used when `AllowedTools` is empty in a `strict-allowlist` session):
`Read`, `Write`, `Edit`, `Glob`, `Grep`, `Bash`, `Agent`, `TaskCreate`, `TaskUpdate`, `TaskGet`, `TaskList`, `TaskOutput`, `TaskStop`

#### Extended `sessions.create` Payload

New optional fields parsed in `handleCreate` (backward-compatible; absent = existing behavior):

```go
// Additional fields in the anonymous request struct in handleCreate:
PermPolicy   string              `json:"permPolicy"`   // "managed" | "auto" | "allowlist" | "strict-allowlist"
AllowedTools []string            `json:"allowedTools"` // tool names; empty = use default for policy
QuotaMonitor *QuotaMonitorConfig `json:"quotaMonitor"` // nil = no monitor goroutine
```

`QuotaMonitorConfig`:
```go
type QuotaMonitorConfig struct {
    Threshold    int    `json:"threshold"`    // % 5h utilization; 0 = disabled
    Priority     int    `json:"priority"`     // 1–10; affects preemption order in dispatcher
    JobID        string `json:"jobId"`        // KV key suffix for the job entry ({userId}/{jobId})
    AutoContinue bool   `json:"autoContinue"` // if true, dispatcher re-queues at 5h reset time
}
```

#### `QuotaMonitor` Goroutine (new file: `quota_monitor.go`)

One goroutine per session; started from `handleCreate` when `QuotaMonitor` config is non-nil.

```go
type QuotaMonitor struct {
    sessionID    string
    userID       string
    projectID    string
    branch       string           // git branch for the job (e.g. "schedule/spa-abc12345"); used in session_job_complete payload
    cfg          QuotaMonitorConfig
    nc           *nats.Conn
    session      *Session
    publishLifec func(sessionID, evType string, extra map[string]string) // wraps a.publishLifecycleExtra
    permDeniedCh chan string       // receives toolName when strict-allowlist denies a tool
    quotaCh      chan *nats.Msg   // receives quota status updates from NATS
    quotaSub     *nats.Subscription // subscription to mclaude.{userID}.quota; unsubscribed on exit
    lastU5       int              // last observed 5h utilization %; updated on each quota message
    lastR5       time.Time        // last observed 5h reset time; updated on each quota message
    completionPR string            // set by onRawOutput when SESSION_JOB_COMPLETE is detected
    stopCh       chan struct{}      // closed when the QuotaMonitor should exit (session process done)
}
```

**`newQuotaMonitor`** creates the struct, creates the NATS subscription, starts the goroutine, and returns the monitor. Called from `handleCreate`.

Subscription setup in `newQuotaMonitor`:
```go
quotaCh := make(chan *nats.Msg, 16)
subject := fmt.Sprintf("mclaude.%s.quota", userID)
quotaSub, err := nc.ChanSubscribe(subject, quotaCh)
if err != nil {
    return nil, fmt.Errorf("quota subscribe: %w", err)
}
```

The `quotaSub` is stored in the struct. When the goroutine exits (via `m.stopCh` close or `session.doneCh` close), it calls `m.quotaSub.Unsubscribe()` before returning.

**Goroutine behavior** (single `select` loop):
```
stopReason = ""   // "quota" | "permDenied" | ""

loop:
  select:
    case <-m.stopCh:
      exit
    case toolName := <-m.permDeniedCh:
      if stopReason == "":
        stopReason = "permDenied"
        sendGracefulStop()
        startStopTimeout(30min)
    case msg := <-m.quotaCh:
      parse QuotaStatus
      store m.lastU5 = qs.U5; m.lastR5 = qs.R5
      if hasData && u5 >= threshold && stopReason == "":
        stopReason = "quota"
        sendGracefulStop()
        startStopTimeout(30min)
    case <-stopTimeout (30min after graceful stop sent):
      sendHardInterrupt()
    case <-session.doneCh:
      publishExitLifecycle()
      close(stopCh)
```

**`sendGracefulStop()`** queues this JSON on `s.stdinCh`:
```json
{"type":"user","message":{"role":"user","content":"QUOTA_THRESHOLD_REACHED: The 5-hour API quota threshold has been reached. Please finish your current task and commit all changes, run --audit-only to generate a gap report and output the full results, then stop. Do not start any new tasks."}}
```
Note: no top-level `session_id` field — this is written directly to Claude's stdin, not routed via `handleInput`.

**`sendHardInterrupt()`** queues this on `s.stdinCh`:
```json
{"type":"control_request","request":{"subtype":"interrupt"}}
```
(Same format used by `session.stopAndWait`.)

**`publishExitLifecycle()`** — on `session.doneCh` close, determines exit reason:
- If `m.completionPR != ""`: publishes `session_job_complete` with `prUrl`.
- Else if `stopReason == "quota"`: publishes `session_quota_interrupted` with last known `u5` and `r5`.
- Else if `stopReason == "permDenied"`: publishes nothing — `session_permission_denied` was already published synchronously by `onStrictDeny` before the stop was initiated. Publishing a second event would overwrite `needs_spec_fix` status in KV.
- Else: publishes `session_job_failed` with `error: "session exited without completion marker"`. This covers PR creation failures, Claude crashes, and any other unexpected exit path.

**`onRawOutput(evType string, raw []byte)`** — scans for completion marker:
```go
func (m *QuotaMonitor) onRawOutput(evType string, raw []byte) {
    if evType != EventTypeAssistant {
        return
    }
    const marker = "SESSION_JOB_COMPLETE:"
    idx := bytes.Index(raw, []byte(marker))
    if idx == -1 {
        return
    }
    // Parse the PR URL: everything after the marker until whitespace or end of text value.
    // The raw bytes are NDJSON; the marker appears inside a string field.
    // Extract conservatively: take up to 200 bytes after marker, trim at whitespace or '"'.
    rest := raw[idx+len(marker):]
    end := bytes.IndexAny(rest, " \t\n\r\"}")
    if end == -1 {
        end = len(rest)
    }
    if end > 200 {
        end = 200
    }
    m.completionPR = string(rest[:end])
}
```

**`signalPermDenied(toolName string)`** — non-blocking send on `m.permDeniedCh`:
```go
select {
case m.permDeniedCh <- toolName:
default:
    // stop already in progress; drop signal
}
```

### Control-Plane (`mclaude-control-plane/projects.go` or new `jobs.go`)

**New KV bucket** created on startup alongside `mclaude-projects`. Uses the existing `nats.JetStreamContext` pattern:
```go
// ensureJobQueueKV creates the mclaude-job-queue KV bucket if it doesn't exist.
func ensureJobQueueKV(js nats.JetStreamContext) (nats.KeyValue, error) {
    kv, err := js.KeyValue("mclaude-job-queue")
    if err == nil {
        return kv, nil
    }
    return js.CreateKeyValue(&nats.KeyValueConfig{
        Bucket:  "mclaude-job-queue",
        History: 1,
    })
}
```

Called in `StartProjectsSubscriber` alongside `ensureProjectsKV`.

### Daemon Jobs HTTP Server (`mclaude-session-agent/daemon.go`)

The daemon starts a local HTTP server on `localhost:8378` (loopback-only, not accessible from outside the machine). This is the direct endpoint for `/jobs` REST operations. Skills call it directly via `curl`/`fetch`; no proxy chain is involved.

#### `runJobsHTTP(ctx context.Context)`

Started by `Daemon.Run()` alongside the quota publisher and job dispatcher. Serves:

| Method | Path | Request | Response |
|--------|------|---------|----------|
| `POST` | `/jobs` | `{specPath, priority, threshold, autoContinue, projectId}` + header `X-User-ID` | `{id, status:"queued"}` |
| `GET` | `/jobs` | header `X-User-ID` | `[JobEntry, ...]` sorted by `createdAt` desc |
| `GET` | `/jobs/{id}` | header `X-User-ID` | `JobEntry` |
| `DELETE` | `/jobs/{id}` | header `X-User-ID` | `{}` |
| `GET` | `/jobs/projects` | header `X-User-ID` | `[{id, name}, ...]` |

All handlers scope KV operations to `{d.cfg.UserID}/{jobId}` keys in `d.jobQueueKV`. Since the server is loopback-only and the daemon already knows the userId from `DaemonConfig`, no auth header is required.

`POST /jobs` generates a UUID `id`, sets `status = "queued"`, `createdAt = now()`, and writes the `JobEntry` to `d.jobQueueKV`.

`DELETE /jobs/{id}`: reads the entry from `d.jobQueueKV`. If `status == "running"`, publishes a `sessions.delete` NATS message to `mclaude.{userId}.{job.ProjectID}.api.sessions.delete` with payload `{"sessionId":"{job.SessionID}"}` (required by `handleDelete` in agent.go) before updating state. Sets `status=cancelled` and writes back to KV (soft delete — the entry remains visible in `GET /jobs` so users can inspect it before re-queuing). The daemon has `nc *nats.Conn`, so the NATS publish is direct, not an HTTP call.

`GET /jobs/projects`: reads all entries from `mclaude-projects` KV with key prefix `{userId}.`. Returns `[{id, name}]` — the project name is the human-readable name (e.g., `"mclaude"`); no local path is included since the daemon does not track project paths. The skill uses this to find the `projectId` by matching `basename(CWD) == project.name`.

### New Skill: `/schedule-feature` (`.agent/skills/schedule-feature/SKILL.md`)

**Usage**:
```
/schedule-feature <spec-path> [--priority N] [--threshold N] [--auto-continue]
```

**Arguments**:
- `spec-path` — relative path to spec doc (e.g., `docs/adr-0009-quota-aware-scheduling.md`). Must exist.
- `--priority N` — integer 1–10; default 5. Higher = survives quota pressure longer.
- `--threshold N` — integer 1–99; default 75. 5h utilization % at which to trigger graceful stop.
- `--auto-continue` — flag; if set, job re-queues at the 5h reset time after being stopped.

**Behavior**:
1. Verify `spec-path` exists (Glob or Read).
2. Call `GET http://localhost:8378/jobs/projects` (via Bash `curl`) to get `[{id, name}]`. Match `basename(CWD) == project.name` to find the `projectId`. If no match, display the project list and ask the user to pick by name.
3. Call `POST http://localhost:8378/jobs` (via Bash `curl`) with `{specPath, projectId, priority, threshold, autoContinue}`.
4. Display: job ID, spec path, priority, threshold, auto-continue, current queue depth.

### New Skill: `/job-queue` (`.agent/skills/job-queue/SKILL.md`)

**Usage**:
```
/job-queue [list|cancel <jobId>|status <jobId>]
```

Default (no subcommand): same as `list`.

**`list`**: Calls `GET http://localhost:8378/jobs` (via Bash `curl`). Displays a table:
```
ID         SPEC                     PRI  STATUS           SESSION
abc12345   docs/adr-YYYY-MM-DD-spa.md         7    running          sess-xyz
def67890   docs/adr-...-k8s-integration.md   5    queued           -
```

**`cancel <jobId>`**: Calls `DELETE http://localhost:8378/jobs/{jobId}` (via Bash `curl`). Confirms cancellation.

**`status <jobId>`**: Calls `GET http://localhost:8378/jobs/{jobId}` (via Bash `curl`). Displays full entry including PR URL, error, branch, failedTool.

## Data Model

### `mclaude-job-queue` KV

Key: `{userId}/{jobId}`

Value:
```go
type JobEntry struct {
    ID           string     `json:"id"`           // UUID v4
    UserID       string     `json:"userId"`
    ProjectID    string     `json:"projectId"`    // project the session runs under
    SpecPath     string     `json:"specPath"`     // e.g. "docs/adr-YYYY-MM-DD-spa.md"
    Priority     int        `json:"priority"`     // 1–10; 5 = default
    Threshold    int        `json:"threshold"`    // % 5h utilization; 75 = default
    AutoContinue bool       `json:"autoContinue"`
    Status       string     `json:"status"`
    // Status values:
    //   queued          — waiting to start
    //   starting        — sessions.create sent; waiting for session idle
    //   running         — session active
    //   paused          — stopped due to quota; will restart at ResumeAt if AutoContinue
    //   completed       — CLEAN audit-only; PR created
    //   failed          — unrecoverable error (e.g. session crash, 3 start failures)
    //   needs_spec_fix  — out-of-allowlist tool requested; spec update required before restart
    //   cancelled       — user-cancelled via /job-queue cancel
    SessionID    string     `json:"sessionId"`    // populated when status=running
    Branch       string     `json:"branch"`       // "schedule/{slug}-{shortId}"
    PRUrl        string     `json:"prUrl"`        // populated when status=completed
    FailedTool   string     `json:"failedTool"`   // populated when status=needs_spec_fix
    Error        string     `json:"error"`        // populated when status=failed
    RetryCount   int        `json:"retryCount"`   // incremented on each queued→starting failure
    ResumeAt     *time.Time `json:"resumeAt"`     // set when status=paused with AutoContinue
    CreatedAt    time.Time  `json:"createdAt"`
    StartedAt    *time.Time `json:"startedAt"`
    CompletedAt  *time.Time `json:"completedAt"`
}
```

### NATS Subject: `mclaude.{userId}.quota`

Published by the daemon quota publisher every 60 seconds (core NATS, no JetStream retention):
```go
type QuotaStatus struct {
    U5      int       `json:"u5"`      // 5h window utilization %
    U7      int       `json:"u7"`      // 7d window utilization %
    R5      time.Time `json:"r5"`      // 5h window reset time (UTC)
    R7      time.Time `json:"r7"`      // 7d window reset time (UTC)
    HasData bool      `json:"hasData"` // false when API call failed; monitor must not trigger
    TS      time.Time `json:"ts"`      // timestamp of this fetch
}
```

### New Lifecycle Event Payloads

Published on `mclaude.{userId}.{projectId}.lifecycle.{sessionId}` (same subject as existing lifecycle events, core NATS):

**`session_quota_interrupted`**:
```json
{
  "type": "session_quota_interrupted",
  "sessionId": "...",
  "threshold": 75,
  "u5": 76,
  "r5": "2026-04-14T08:00:00Z",
  "jobId": "...",
  "ts": "..."
}
```

**`session_permission_denied`**:
```json
{
  "type": "session_permission_denied",
  "sessionId": "...",
  "tool": "mcp__gmail__send_email",
  "jobId": "...",
  "ts": "..."
}
```

**`session_job_complete`**:
```json
{
  "type": "session_job_complete",
  "sessionId": "...",
  "branch": "schedule/spa-abc12345",
  "prUrl": "https://github.com/owner/mclaude/pull/42",
  "jobId": "...",
  "ts": "..."
}
```

**`session_job_paused`**:
```json
{
  "type": "session_job_paused",
  "sessionId": "...",
  "priority": 3,
  "u5": 76,
  "jobId": "...",
  "ts": "..."
}
```

**`session_job_failed`**:
```json
{
  "type": "session_job_failed",
  "sessionId": "...",
  "jobId": "...",
  "error": "session exited without completion marker",
  "ts": "..."
}
```

## Error Handling

| Failure | Handling |
|---------|----------|
| Quota API call fails | `QuotaStatus{HasData: false}` published. Monitor does NOT trigger stop. Dispatcher does not start new jobs. Retry at next 60s interval. |
| Credentials file missing or token absent | Quota publisher logs warning; publishes `{HasData: false}`. Does not crash daemon. |
| Session fails to start (30s KV poll timeout) | Dispatcher resets job to `queued`, increments `RetryCount`. After 3 failures: marks `failed` with error message. |
| Graceful stop timeout (30 min) | `sendHardInterrupt()` queued to `s.stdinCh`. Session process expected to exit within `sessionDeleteTimeout` (10s) thereafter. Job marked `paused` (if quota-triggered) or `failed` (if unexpected exit). |
| Out-of-allowlist tool request | Auto-denied via `control_response`. `session_permission_denied` published. Graceful stop sent via `permDeniedCh`. Job → `needs_spec_fix`. Other running jobs unaffected. |
| PR creation fails (session outputs error) | `m.completionPR` is never set; `SESSION_JOB_COMPLETE` marker not detected. `publishExitLifecycle()` publishes `session_job_failed`. `runLifecycleSubscriber` sets `status=failed`. |
| Daemon restarts mid-job | On startup, dispatcher scans `jobQueueKV`. Jobs in `running`/`starting` with no matching `SessionID` in `sessKV` → reset to `queued`. Jobs `paused` with past `ResumeAt` → reset to `queued`. |
| `needs_spec_fix` jobs | Stay in KV with that status. Dispatcher never auto-starts them. User updates spec, cancels with `/job-queue cancel`, re-queues with `/schedule-feature`. |
| Concurrent sessions on shared files | Each session is aware of other running jobs (prompt lists them). Claude resolves conflicts via git workflow. No automatic file locking. |

## Security

- OAuth token is read from `~/.claude/.credentials.json` at runtime; never written to NATS, KV, or logs.
- Job entries in `mclaude-job-queue` are keyed `{userId}/...`. NATS auth enforces userId isolation (existing behavior). Daemon HTTP endpoints are loopback-only (localhost:8378); no auth needed since the daemon knows its userId from config.
- `strict-allowlist` sessions cannot access external services (email, browser, web APIs). Any attempt is auto-denied and surfaces as `session_permission_denied`.
- Scheduled sessions use a fresh worktree branch (`schedule/{slug}-{shortId}`). The allowlist does not include force-push; merging to `main` requires the PR review workflow.
- The 30-minute stop timeout ensures no runaway session exhausts quota silently; `sendHardInterrupt()` is the guaranteed backstop.

## Operational Plan

### Scheduling Parameters

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| Model for dev-harness jobs | Sonnet | ~2x more work per quota unit vs Opus (Opus burns ~$245/session vs Sonnet ~$112 against shared pool) |
| Daytime threshold (9am–5pm) | 75% | Leaves headroom for interactive Claude Code use during work hours |
| Overnight threshold (midnight–7am) | 95% | Maximize throughput; no interactive use expected |
| Train commute (7–9am) | Interactive Opus only | User does spec work on the train; no scheduled jobs |
| Evening (5pm–midnight) | Interactive only | Personal projects; scheduled jobs paused |
| `--auto-continue` | All jobs | Re-queues at 5h reset time automatically |
| Parallelism | Sequential (one job at a time) | Avoids git conflicts; simpler quota management |

### Job Queue Priority

Jobs are ordered by dependency (enabling components first), gap count (quick wins first), and user-facing value.

| Priority | Component | Spec(s) | Gaps | Design Audit Status | Notes |
|----------|-----------|---------|------|---------------------|-------|
| 1 | session-agent | plan-k8s-integration, plan-graceful-upgrades | 3 GAP + ~6 PARTIAL | k8s: Round 1 only | Quick win — mostly adding `{location}` segment to NATS subjects. Enables everything else. |
| 2 | spa | plan-client-architecture, ui-spec, plan-replay-user-messages | 9 GAP + 11 PARTIAL | client-arch: none; replay: CLEAN | Highest daily-use value. xterm.js terminal, table rendering, budget bar, reconnect logic. |
| 3 | control-plane | plan-core-containers, plan-github-oauth, plan-k8s-integration | 17 GAP | github-oauth: CLEAN; core-containers: none | Large scope — OAuth endpoints, KV buckets, SCIM, project HTTP API. Consider splitting essentials vs OAuth/SCIM. |
| 4 | helm | plan-core-containers | 7 GAP | None | Chart templates. |
| 5 | cli | plan-client-architecture | 5 GAP | None | Client tool. |

### Execution Approach

1. Queue **session-agent** first (quick win, spec mostly clean).
2. Queue **spa** second (highest daily interaction value).
3. Run **design-audit** on plan-core-containers and plan-client-architecture in parallel while jobs 1–2 execute — reduces rework risk from spec ambiguities.
4. Queue **control-plane** and **helm** after design audits resolve ambiguities.
5. Queue **cli** last.

### Quota Budget Context (Max 5x, $100/mo)

- Single unified utilization percentage (0–100%) across a 5-hour rolling window (~88K Haiku-equivalent billable tokens).
- 7-day rolling window is the real weekly limit — usage rolls off continuously, not in fixed blocks.
- Opus burns ~2x faster than Sonnet against the same pool (based on observed $245 vs $112 sessions).
- Cache reads dominate volume (97–278M per session) at 10% billing rate.
- Physical ceiling: 4.8 five-hour windows per day = ~33 windows/week.
- Goal: hit ~100% weekly utilization right as it rolls over.

## Scope

**In scope**:
- Daemon: `runQuotaPublisher` goroutine (polls `api.anthropic.com/api/oauth/usage`)
- Daemon: `runJobDispatcher` goroutine (start/pause/restart jobs from KV)
- Daemon: `runLifecycleSubscriber` goroutine (writes terminal job state back to KV on lifecycle events)
- Daemon: `runJobsHTTP` goroutine (localhost:8378 job CRUD)
- Daemon: `sessKV`, `jobQueueKV`, and `projectsKV` handles
- Session-agent: `strict-allowlist` permission policy with auto-deny
- Session-agent: `Session.onStrictDeny` and `Session.onRawOutput` callbacks
- Session-agent: `QuotaMonitor` per-session goroutine (`quota_monitor.go`)
- Session-agent: `Agent.publishPermDenied` method
- Five new lifecycle event types (`session_quota_interrupted`, `session_permission_denied`, `session_job_complete`, `session_job_paused`, `session_job_failed`)
- `mclaude-job-queue` KV bucket (created by control-plane using `nats.JetStreamContext` API)
- `/schedule-feature` Claude Code skill
- `/job-queue` Claude Code skill

**Deferred**:
- `--at "2am"` time-based scheduling
- Hardware capacity monitoring (CPU, RAM)
- Job dependencies (run job A only after job B completes)
- Automatic spec-fix session on `needs_spec_fix` status
- K8s-only mode without a running laptop daemon (quota publisher requires daemon)
- BYOM integration: v1 components (mclaude-server, mclaude-connector, mclaude-mcp) are not modified; proxy chain and MCP tools deferred for BYOM compatibility
