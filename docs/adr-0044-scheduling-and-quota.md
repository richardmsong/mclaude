# ADR: Session Scheduling and Quota Management

**Status**: draft
**Status history**:
- 2026-04-28: draft

> Supersedes:
> - `adr-0009-quota-aware-scheduling.md` — folded in: priority job queue, QuotaMonitor goroutine, real-time Anthropic API quota monitoring via OAuth usage endpoint, strict-allowlist permission policy with auto-deny, unattended session lifecycle events, mclaude-job-queue KV bucket, daemon goroutines (quota publisher, job dispatcher, lifecycle subscriber), localhost:8378 HTTP API, permission-denied → needs_spec_fix flow, error handling, security model
> - `adr-0034-generic-scheduler-prompt.md` — folded in: methodology-agnostic free-text prompt field, two-tier quota (soft threshold + hard token budget), paused sessions stay alive in session-agent pod, resume via new user message in same conversation, completion via Claude Code Stop hook (not stdout marker), MCLAUDE_STOP: quota_soft marker, hostSlug for multi-laptop, ClaudeSessionID for --resume fallback, pausedVia lineage, session_job_cancelled event, /schedule-feature moved to external plugin, Stop hook authoring guide
>
> The two ADRs above are marked `superseded` by this ADR in their status history.

## Overview

Enables unattended Claude sessions managed by a priority-aware job queue with real-time quota monitoring. Callers POST a free-text prompt to the platform; the platform spawns a session on an isolated worktree, enforces Anthropic API quota via two tiers (soft percentage threshold + hard output-token budget), and preserves session state across quota pauses by keeping the Claude Code subprocess alive. The platform is methodology-agnostic — it never interprets prompt content. Completion is determined by Claude Code's native Stop hook; paused sessions resume with a new user message in the same conversation (no restart, no context loss). The `/schedule-feature` SDD-flavored skill lives in an external plugin; the platform exposes only the generic `/job-queue` skill.

## Motivation

mclaude sessions need to run unattended — queued, started, paused on quota pressure, and resumed — without human intervention. The Anthropic API enforces a 5-hour rolling utilization window; scheduled sessions must respect this limit and yield gracefully when quota is tight, then resume automatically when quota recovers.

The scheduler primitive must be content-agnostic. Callers supply a free-text prompt and the platform passes it verbatim. Methodology-specific prompt composition (e.g., SDD `/dev-harness` invocations), Stop hook configuration, and completion criteria belong in caller-side plugins, not in the platform. This separation lets mclaude serve arbitrary unattended workloads — refactors, code generation, doc updates, or any long-running Claude task — without coupling to any single development methodology.

Quota enforcement uses two tiers: a soft threshold that injects a cooperative stop marker (`MCLAUDE_STOP: quota_soft`) and waits for Claude to end its turn naturally, and a hard token budget that sends a `control_request` interrupt when output tokens exceed the budget after soft-mark injection. Paused sessions stay alive inside the session-agent pod — the subprocess idles on stdin with its in-memory conversation intact, so resume is just a new user message rather than a restart.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Prompt shape | Caller supplies a free-text `prompt` field; platform passes it verbatim as the initial user message. | Methodology-agnostic. Platform never parses, rewrites, or prepends content. |
| Job entry fields | `prompt` (required), `title` (display label for `/job-queue`), `branchSlug` (required — worktree branch derivation). No `specPath`. | Predictable top-level fields. Caller-provided slug keeps branch names readable without the platform parsing prompt content. |
| Quota data source | `api.anthropic.com/api/oauth/usage` via OAuth token from `~/.claude/.credentials.json` | No dependency on claude-pace or any shell plugin. Works wherever the daemon runs as long as credentials exist. |
| Quota publisher | Daemon goroutine polling every 60 seconds; publishes `QuotaStatus` to NATS subject `mclaude.{userSlug}.quota` (core NATS, no JetStream retention). | Single publisher per user; K8s session-agents subscribe to NATS. Same `QuotaStatus` value sent on internal channel consumed by job dispatcher. |
| Quota — soft threshold | `softThreshold` (% 5h utilization) per job. On breach, dispatcher injects `MCLAUDE_STOP: quota_soft` via `sessions.input` and waits for Claude to end its turn. | Cooperative stop — Claude saves state before yielding. Dispatcher is the sole marker injector (QuotaMonitor only updates local state). |
| Quota — hard budget | `hardHeadroomTokens` per job. After soft marker injection, session-agent's QuotaMonitor counts output tokens; at budget, sends `control_request` interrupt directly on `s.stdinCh`. | More precise than a percentage. Token counter lives in session-agent (already reads stream-json). Interrupts bypass Stop hooks per Claude Code. |
| Paused sessions stay alive | Soft-stop and hard-stop both leave the Claude Code subprocess running inside the session-agent pod. Subprocess idles on stdin with in-memory conversation intact. | Eliminates restart complexity — no `--resume`, no prompt idempotency requirement, no context loss. |
| Resume from pause | Dispatcher sends a new user message via `sessions.input`. Uses caller-supplied `resumePrompt` if provided; otherwise platform defaults (different for soft vs hard). | Resume is just another turn in the same conversation. |
| Default resume nudges | Soft-paused: `"Resuming — continue where you left off."` Hard-paused: `"Your previous turn was interrupted mid-response. Check git status and recover state before continuing."` | Hard-stopped sessions may have inconsistent state; different nudge prompts recovery. |
| Completion signalling | No stdout-marker scanning. Completion happens when Claude ends a turn naturally and the Stop hook allows the stop. Platform then issues `sessions.delete`. | Platform stays out of content. Stop hook is the authoritative "is this job done?" decider. |
| Stop hook mechanism | Claude Code's native `hooks.Stop` (in caller's `settings.json`). Not a platform primitive. | Reuses Claude Code hook infrastructure. Hook fires on natural turn-end; can block to force another turn. |
| Stop-reason signalling (platform → hook) | Platform injects `MCLAUDE_STOP: quota_soft` as a user message. Caller's hook reads `transcript_path`, scans last user message for marker, and allows stop when present. | Transcript is the one surface both sides can see. Only cooperative stop uses the marker; hard-stop and cancel bypass hooks. |
| Turn-end detection | QuotaMonitor detects turn-end via stream-json `result` event (`EventTypeResult`). `handleTurnEnd()` inspects `stopReason` state to distinguish paused-vs-completed. | `result` event is Claude Code's canonical end-of-turn marker. `session.doneCh` only fires on subprocess exit, not on turn-end while subprocess stays alive. |
| Job persistence | `mclaude-job-queue` KV bucket (JetStream). Key: `{userSlug}.{jobId}`. | Consistent with existing KV patterns; survives daemon restarts; each entry inspectable and cancellable by key. |
| Parallelism model | All queued jobs start immediately when quota allows; priority-based preemption when soft threshold hit. | Maximize throughput by default. User-assigned priorities determine which sessions survive quota pressure. |
| Priority preemption mechanism | Dispatcher sorts running jobs by priority ascending (lowest first), injects `MCLAUDE_STOP: quota_soft` to lowest-priority sessions first. | Lets Claude finish current task before stopping; no work lost. |
| Permission policy for unattended sessions | `strict-allowlist` mode: auto-approve allowlisted tools, auto-deny everything else. Required on POST — caller must pass `permPolicy` and `allowedTools`. | Prevents blocking on out-of-allowlist tool prompts. Denial signals a spec gap and triggers fail-fast with a lifecycle event. Forces every caller to declare tool scope. |
| Worktree per job | Branch `schedule/{branchSlug}`. If worktree exists, session attaches to it — same slug = shared worktree. | Isolated worktrees prevent file conflicts between concurrent jobs. Shared-slug attach lets scheduled sessions collaborate. |
| Worktree cleanup | Platform does not prune worktrees. Caller's PR/merge workflow owns branch lifecycle. `handleDelete` skips worktree removal for `schedule/` prefix branches. | Avoids cross-job race conditions; platform stays out of git state beyond creation/attach. |
| Auto-continuation | `autoContinue` flag per job. When set and paused on quota, dispatcher sets `ResumeAt` to 5h reset time and auto-resumes. | Re-queues at the 5h reset window. Without the flag, jobs resume whenever quota recovers (no wait for reset). |
| Cancel | Single verb: `DELETE /jobs/{id}`. Dispatcher publishes `sessions.delete` (causes `handleDelete` to interrupt + reap subprocess via `stopAndWait`), then publishes `session_job_cancelled` lifecycle event. No cooperative variant. | If the user pulled the job from the queue, they want it gone. Stop hook doesn't fire because interrupts bypass it. |
| Degraded fallback: session loss | Dispatcher persists `ClaudeSessionID` (Claude Code's own session ID) to `JobEntry`. On session loss (pod eviction, daemon crash), spawns new session with `claude --resume <ClaudeSessionID>`. | Normal path never uses this. Only covers actual infra failure. If `--resume` fails, job marked `failed`. |
| `hostSlug` on `JobEntry` | Required on POST. Identifies which host the scheduled session runs on. Used for 4-part session KV key and host-scoped NATS subjects per ADR-0004. | Multi-laptop setups need explicit host routing. Subject routing and session KV lookups fail without it. |
| Job HTTP endpoints | Daemon exposes local HTTP server on localhost:8378; skills call it directly. | Loopback-only, no auth needed (daemon knows userId from config). |
| Permission-denied signaling | In-process Go channel from `Session.onStrictDeny` to `QuotaMonitor.permDeniedCh`; lifecycle event published via `Agent.publishPermDenied`. | Avoids a NATS round-trip for an in-process event. |
| `/schedule-feature` skill location | Lives in external plugin (e.g., `spec-driven-dev` plugin). Deleted from mclaude. | Skill composes methodology-specific prompts + configures Stop hooks; those are caller concerns, not platform. |
| `/job-queue` skill | Stays in mclaude. Displays `TITLE` column (not spec path). Status detail shows first 200 chars of prompt, `SoftThreshold`/`HardHeadroomTokens`, `PausedVia`. | Pure platform UX; no methodology vocabulary. |
| NATS subject format | All session-scoped subjects use host-slug-inclusive form: `mclaude.users.{userSlug}.hosts.{hostSlug}.projects.{projectSlug}...` | Aligns with ADR-0004. Fixes pre-existing routing bug from ADR-0009 that omitted `.hosts.*.` segment. |
| Time-based scheduling (`--at`) | Deferred. | Priority is throughput, not specific time windows. Jobs start as quota allows. |

## User Flow

### POST a Job

1. Caller assembles a free-text prompt. The prompt may optionally include instructions for Claude on how to respond to `MCLAUDE_STOP: quota_soft` (e.g., "commit in-progress work, output a one-line status, stop"). If the prompt is silent on this, Claude will typically wrap up on its own; if it doesn't, the hard-token budget catches it.
2. Caller (optionally) configures a Stop hook. The hook can block turn-ends to keep Claude running (course-correction). To avoid fighting with the platform on quota stops, the hook should check whether the last user message in `transcript_path` contains `MCLAUDE_STOP: quota_soft` and NOT block when it does. Without a hook, every natural turn-end results in `completed`.
3. Caller POSTs to `http://localhost:8378/jobs`:
   ```json
   {
     "prompt": "<free text>",
     "title": "refactor-auth-middleware",
     "branchSlug": "refactor-auth-middleware",
     "projectId": "...",
     "hostSlug": "laptop-1",
     "priority": 5,
     "softThreshold": 75,
     "hardHeadroomTokens": 50000,
     "autoContinue": true,
     "permPolicy": "strict-allowlist",
     "allowedTools": ["Read", "Write", "Edit", "Glob", "Grep", "Bash"],
     "resumePrompt": ""
   }
   ```
4. Platform writes a `JobEntry` with status `queued`. Dispatcher picks it up when quota allows.

### First Dispatch

1. Dispatcher reads `job.BranchSlug` and `job.HostSlug`, ensures the worktree `schedule/{branchSlug}` exists on the target host (creates if absent; attaches if present).
2. Dispatcher calls `sessions.create` with `permPolicy`, `allowedTools`, and the worktree branch — sending to `subj.UserHostProjectAPISessionsCreate(userSlug, hostSlug, projectSlug)`.
3. Session-agent spawns the Claude Code subprocess. Extracts `session_id` from the first stream-json `system`/`init` event and includes it in the `sessions.create` NATS reply as `claudeSessionID`.
4. Dispatcher persists the returned `claudeSessionID` to `JobEntry.ClaudeSessionID`.
5. Dispatcher sends `job.Prompt` verbatim as the initial user message via `sessions.input`. Status → `running`.

### Soft-Stop Path

1. Quota publisher reports `u5 >= job.SoftThreshold`. Both the dispatcher and every per-session QuotaMonitor observe this simultaneously.
2. Dispatcher (sole marker injector): publishes `sessions.input` with content `"MCLAUDE_STOP: quota_soft"`. QuotaMonitor does NOT inject the marker — the dispatcher is the single source of truth for quota-level decisions (priority ordering, etc.).
3. QuotaMonitor sets `stopReason = "quota_soft"` and resets `outputTokensSinceSoftMark = 0`. State update only — no stdin write. Token counting begins.
4. Claude processes the marker. Caller's prompt (or default Claude behavior) wraps up.
5. Claude ends its turn, emitting a stream-json `result` event. QuotaMonitor's `handleTurnEnd()` publishes `session_job_paused` with `pausedVia: "quota_soft"` and `r5` from cached `QuotaStatus`. `stopReason` resets to `""`.
6. `runLifecycleSubscriber` writes `JobEntry.Status = paused`, `PausedVia = "quota_soft"`. If `AutoContinue` and `r5` is set, `ResumeAt = r5`.

### Hard-Stop Path

1. While `stopReason == "quota_soft"`, QuotaMonitor counts output tokens on every `onRawOutput` event.
2. When `outputTokensSinceSoftMark >= job.HardHeadroomTokens`, QuotaMonitor sets `stopReason = "quota_hard"` and queues a `control_request` interrupt on `s.stdinCh` directly.
3. Claude's current turn ends mid-response (interrupted). Stop hook is NOT fired (interrupts bypass it). Claude emits a `result` event.
4. QuotaMonitor's `handleTurnEnd()` publishes `session_job_paused` with `pausedVia: "quota_hard"`, `r5`, and `outputTokensSinceSoftMark`. `stopReason` resets to `""`.
5. `runLifecycleSubscriber` writes `JobEntry.Status = paused`, `PausedVia = "quota_hard"`. Same `AutoContinue`/`ResumeAt` logic as soft-stop. Session stays alive awaiting stdin.

### Resume Path

1. Quota publisher reports `u5` below all paused jobs' `SoftThreshold`. Dispatcher sorts paused jobs by `Priority` descending (highest first), resumes one at a time. Jobs with future `ResumeAt` are skipped until that time passes.
2. Dispatcher verifies the session is still alive via `sessKV.Get`. If missing → degraded-fallback path (see below).
3. Dispatcher picks the resume nudge: `job.ResumePrompt` if non-empty, otherwise platform default based on `PausedVia`.
4. Dispatcher sends the nudge via `sessions.input` (same session, same live subprocess, same conversation).
5. Updates `JobEntry`: `Status = running`, `PausedVia = ""`, `ResumeAt = nil`.

### Natural Completion Path

1. Claude ends a turn with no platform-injected marker preceding it. Stream-json `result` event fires.
2. Stop hook fires. Hook does not block (either no hook configured, or caller's hook judged this a genuine completion).
3. QuotaMonitor's `handleTurnEnd()` sees `stopReason == ""` and publishes `session_job_complete`.
4. `runLifecycleSubscriber` writes `JobEntry.Status = completed` AND publishes `sessions.delete` to reap the subprocess.

### Permission-Denied Path

1. Session requests a tool outside the allowlist.
2. Session-agent auto-denies via `control_response`. Calls `sess.onStrictDeny(toolName)`.
3. `onStrictDeny` publishes `session_permission_denied` lifecycle event AND sends `toolName` on `monitor.permDeniedCh`.
4. QuotaMonitor receives on `permDeniedCh` and sends graceful stop message immediately.
5. `runLifecycleSubscriber` receives `session_permission_denied`, sets `status = needs_spec_fix`, `failedTool`. Job remains in queue but will not restart automatically.

### Cancel Path

1. User calls `DELETE /jobs/{id}`.
2. Dispatcher publishes `sessions.delete` (causes `handleDelete` to interrupt + reap subprocess via `stopAndWait`), then publishes `session_job_cancelled` lifecycle event.
3. `runLifecycleSubscriber` writes `JobEntry.Status = cancelled`.

### Degraded Fallback: Session Loss

If a paused session's KV entry is missing when the dispatcher tries to resume (pod eviction, node reboot, daemon crash):

1. Dispatcher constructs `sessions.create` with `resumeClaudeSessionID = job.ClaudeSessionID`.
2. Session-agent spawns `claude --resume <claudeSessionID>` instead of a fresh session.
3. Dispatcher updates `JobEntry.ClaudeSessionID` and `SessionID`, sends resume nudge.
4. If `--resume` fails, job marked `failed`.

## Component Changes

### Daemon (`mclaude-session-agent/daemon.go`)

Four new goroutines added to `Daemon.Run()`: `runQuotaPublisher`, `runJobDispatcher`, `runLifecycleSubscriber`, `runJobsHTTP`.

#### New Fields on `Daemon` Struct

```go
type Daemon struct {
    // ...existing fields...
    sessKV       jetstream.KeyValue  // mclaude-sessions — read-only for startup recovery
    jobQueueKV   jetstream.KeyValue  // mclaude-job-queue — read/write for dispatcher
    projectsKV   jetstream.KeyValue  // mclaude-projects — read-only for GET /jobs/projects
}
```

#### `runQuotaPublisher(ctx context.Context)`

- Polls every 60 seconds.
- Reads OAuth bearer token from `~/.claude/.credentials.json` (field `.claudeAiOauth.accessToken`). Returns `HasData: false` if missing.
- Calls `GET https://api.anthropic.com/api/oauth/usage` with `Authorization: Bearer {token}` and `anthropic-beta: oauth-2025-04-20`.
- Parses JSON response: `{five_hour: {utilization, resets_at}, seven_day: {utilization, resets_at}}`.
- Marshals into `QuotaStatus` and publishes to NATS subject `mclaude.{userSlug}.quota` (core NATS).
- Sends the same `QuotaStatus` on an internal `chan QuotaStatus` consumed by `runJobDispatcher`.
- On HTTP/parse error: publishes `QuotaStatus{HasData: false}`.

#### `runLifecycleSubscriber(ctx context.Context)`

Subscribes to `mclaude.users.{userSlug}.hosts.*.projects.*.lifecycle.*` (NATS wildcard). Handles lifecycle event types and updates `d.jobQueueKV`:

| Event type | Action |
|-----------|--------|
| `session_job_complete` | Set `status=completed`, `completedAt=now()`. Publish `sessions.delete` to reap subprocess. |
| `session_job_paused` | Set `status=paused`, `pausedVia=ev["pausedVia"]`. If `autoContinue` and `ev["r5"]` present, set `ResumeAt=r5`. |
| `session_job_cancelled` | Set `status=cancelled`. |
| `session_permission_denied` | Set `status=needs_spec_fix`, `failedTool=ev["tool"]`. |
| `session_job_failed` | Set `status=failed`, `error=ev["error"]`. |

For unrecognized `jobId` (non-scheduled sessions): ignore silently.

#### `runJobDispatcher(ctx context.Context)`

- Receives `QuotaStatus` updates from the quota publisher channel.
- Watches `d.jobQueueKV` via `WatchAll` for KV changes.
- On **new `queued` entry** with `u5 < softThreshold`:
  - Sets `job.Branch = "schedule/{job.BranchSlug}"`. If worktree exists, session attaches.
  - Sends `sessions.create` with `permPolicy`, `allowedTools`, `quotaMonitor` config.
  - Captures `claudeSessionID` from reply, persists to `JobEntry.ClaudeSessionID`.
  - Polls `sessKV` for session state `idle` (up to 30s, 500ms intervals). Timeout → increment `RetryCount`, reset to `queued`. After 3 failures → `failed`.
  - Sends `job.Prompt` verbatim via `sessions.input`.
  - Status → `running`, sets `SessionID`, `StartedAt`.
- On **quota threshold exceeded** (`u5 >= softThreshold`):
  - Sorts running jobs by `Priority` ascending (lowest first).
  - For each exceeded job: injects `"MCLAUDE_STOP: quota_soft"` via `sessions.input`.
  - Publishes `session_job_paused` lifecycle event. (QuotaMonitor also observes threshold and sets local state.)
- On **quota recovery** (`u5 < softThreshold` after reset):
  - Sorts paused jobs by `Priority` descending (highest first).
  - For each: verifies session alive in `sessKV`, picks resume nudge based on `PausedVia`, sends via `sessions.input`.
  - If session lost: falls back to `sessions.create` with `resumeClaudeSessionID`.
- On **daemon startup**:
  - Jobs in `starting` state: reset to `queued`.
  - Jobs in `running` state: verify `SessionID` in `sessKV`. If missing → reset to `queued`.
  - Jobs in `paused` with past `ResumeAt` → reset to `queued`.

#### `runJobsHTTP(ctx context.Context)`

Local HTTP server on `localhost:8378` (loopback-only):

| Method | Path | Request | Response |
|--------|------|---------|----------|
| `POST` | `/jobs` | `{prompt, title, branchSlug, projectId, hostSlug, priority, softThreshold, hardHeadroomTokens, autoContinue, permPolicy, allowedTools, resumePrompt}` | `{id, status:"queued"}` |
| `GET` | `/jobs` | | `[JobEntry, ...]` sorted by `createdAt` desc |
| `GET` | `/jobs/{id}` | | `JobEntry` |
| `DELETE` | `/jobs/{id}` | | `{}` — publishes `sessions.delete` + `session_job_cancelled` |
| `GET` | `/jobs/projects` | | `[{id, name}, ...]` |

Validation: 400 if any required field is empty/zero. `branchSlug` and `hostSlug` must match `^[a-z0-9][a-z0-9-]*$`. `allowedTools` must be non-empty.

### Session-Agent (`agent.go`, `session.go`)

#### Permission Policy: `strict-allowlist`

In `handleSideEffect`, when policy is `strict-allowlist` and `shouldAutoApprove` returns false:
1. Build a deny `control_response` with `{"behavior": "deny"}`.
2. Clear pending control.
3. Call `s.onStrictDeny(toolName)` if non-nil.

**Default dev-harness allowlist** (used when `AllowedTools` is empty in a `strict-allowlist` session):
`Read`, `Write`, `Edit`, `Glob`, `Grep`, `Bash`, `Agent`, `TaskCreate`, `TaskUpdate`, `TaskGet`, `TaskList`, `TaskOutput`, `TaskStop`

#### Extended `sessions.create` Payload

New optional fields (backward-compatible; absent = existing behavior):

```go
PermPolicy            string              `json:"permPolicy"`
AllowedTools          []string            `json:"allowedTools"`
QuotaMonitor          *QuotaMonitorConfig `json:"quotaMonitor"`
ResumeClaudeSessionID string              `json:"resumeClaudeSessionID"` // for --resume fallback
```

`sessions.create` reply carries both `id` (mclaude session UUID) and `claudeSessionID` (Claude Code's own session ID extracted from the first stream-json `system`/`init` event).

#### Session Callbacks

```go
// onStrictDeny — called when strict-allowlist auto-denies a control_request.
onStrictDeny func(toolName string)

// onRawOutput — called for every raw stdout line from Claude before NATS publish.
// Used by QuotaMonitor for token counting and turn-end detection.
onRawOutput func(evType string, raw []byte)
```

Both set by Agent in `handleCreate` before calling `sess.start()`.

#### `QuotaMonitor` Goroutine (`quota_monitor.go`)

One goroutine per scheduled session; started from `handleCreate` when `QuotaMonitor` config is non-nil.

```go
type QuotaMonitor struct {
    sessionID                string
    userSlug                 string
    hostSlug                 string
    projectSlug              string
    cfg                      QuotaMonitorConfig
    nc                       *nats.Conn
    session                  *Session
    publishLifec             func(sessionID, evType string, extra map[string]string)
    permDeniedCh             chan string
    quotaCh                  chan *nats.Msg
    quotaSub                 *nats.Subscription
    lastU5                   int
    lastR5                   time.Time
    stopReason               string  // "" | "quota_soft" | "quota_hard"
    outputTokensSinceSoftMark int
    turnEndedCh              chan struct{} // 1-buffered; fires on result event
    terminalEventPublished   bool
    stopCh                   chan struct{}
}
```

**`QuotaMonitorConfig`**:
```go
type QuotaMonitorConfig struct {
    SoftThreshold      int    `json:"softThreshold"`
    HardHeadroomTokens int    `json:"hardHeadroomTokens"`
    JobID              string `json:"jobId"`
    AutoContinue       bool   `json:"autoContinue"`
}
```

**Goroutine select-loop cases:**

- `<-m.stopCh`: exit cleanly.
- `toolName := <-m.permDeniedCh`: if `stopReason == ""`, set `stopReason = "permDenied"`, send graceful stop.
- `msg := <-m.quotaCh`: update cached `QuotaStatus`. If `u5 >= SoftThreshold` and `stopReason == ""`: set `stopReason = "quota_soft"`, reset `outputTokensSinceSoftMark = 0`. State-only — no stdin write (dispatcher is sole marker injector).
- `<-m.turnEndedCh`: dispatch to `handleTurnEnd()`.
- `<-session.doneCh`: dispatch to `handleSubprocessExit()`.

**Token-budget check** (in `onRawOutput`): when `outputTokensSinceSoftMark >= HardHeadroomTokens` and `stopReason == "quota_soft"`, set `stopReason = "quota_hard"` and queue `control_request` interrupt on `s.stdinCh`.

**`onRawOutput(evType, raw)`** handles:
- **Token counting**: if `evType == EventTypeAssistant` or `EventTypeResult` with `usage.output_tokens` present, increments `outputTokensSinceSoftMark` (only while `stopReason != ""`). Fallback to `len(raw) / 4` estimate when `usage` absent.
- **Turn-end detection**: if `evType == EventTypeResult`, send non-blocking on `turnEndedCh`.

**`handleTurnEnd()`** inspects `stopReason`:

| `stopReason` | Event published | Next step |
|--------------|-----------------|-----------|
| `"quota_soft"` | `session_job_paused` with `pausedVia: "quota_soft"` + `r5` | Reset `stopReason`; subprocess stays alive. |
| `"quota_hard"` | `session_job_paused` with `pausedVia: "quota_hard"` + `r5` + `outputTokensSinceSoftMark` | Reset `stopReason`; subprocess stays alive. |
| `""` | `session_job_complete` | Lifecycle subscriber writes `completed` and issues `sessions.delete`. |

**`handleSubprocessExit()`** (on `doneCh` close):
- If `terminalEventPublished` → no-op (expected cleanup after completion or cancellation).
- Otherwise → publish `session_job_failed` with `error: "subprocess exited without turn-end signal"`.

### Control-Plane (`mclaude-control-plane`)

New KV bucket `mclaude-job-queue` created on startup (History: 1). Created alongside `mclaude-projects` using `nats.JetStreamContext`.

### Worktree Handling in `handleDelete`

If the branch name matches the `schedule/` prefix, skip worktree removal. All other `handleDelete` behavior (lifecycle-event emission, subprocess termination via `stopAndWait`) unchanged.

## Data Model

### `JobEntry` (in `mclaude-job-queue` KV)

Key: `{userSlug}.{jobId}`

```go
type JobEntry struct {
    ID                  string     `json:"id"`                   // UUID v4
    UserID              string     `json:"userId"`
    UserSlug            string     `json:"userSlug"`
    ProjectID           string     `json:"projectId"`
    ProjectSlug         string     `json:"projectSlug"`
    SessionID           string     `json:"sessionId"`            // mclaude session UUID
    SessionSlug         string     `json:"sessionSlug"`
    Prompt              string     `json:"prompt"`               // free-text initial user message
    Title               string     `json:"title"`                // display label; falls back to BranchSlug
    BranchSlug          string     `json:"branchSlug"`           // worktree branch = "schedule/{branchSlug}"
    ResumePrompt        string     `json:"resumePrompt"`         // caller-supplied nudge on resume; empty = platform default
    HostSlug            string     `json:"hostSlug"`             // required on POST; feeds host-scoped NATS subjects
    Priority            int        `json:"priority"`             // 1–10; 5 = default
    SoftThreshold       int        `json:"softThreshold"`        // % 5h utilization
    HardHeadroomTokens  int        `json:"hardHeadroomTokens"`   // output tokens past soft mark before hard interrupt
    AutoContinue        bool       `json:"autoContinue"`
    PermPolicy          string     `json:"permPolicy"`           // required on POST
    AllowedTools        []string   `json:"allowedTools"`         // required on POST
    Status              string     `json:"status"`               // queued | starting | running | paused | completed | cancelled | failed | needs_spec_fix
    PausedVia           string     `json:"pausedVia"`            // "quota_soft" | "quota_hard" | "" when not paused
    ClaudeSessionID     string     `json:"claudeSessionID"`      // Claude Code's own session ID for --resume fallback
    Branch              string     `json:"branch"`               // "schedule/{branchSlug}"
    FailedTool          string     `json:"failedTool"`           // populated when status=needs_spec_fix
    Error               string     `json:"error"`                // populated when status=failed
    RetryCount          int        `json:"retryCount"`
    ResumeAt            *time.Time `json:"resumeAt"`             // set if autoContinue + paused on quota
    CreatedAt           time.Time  `json:"createdAt"`
    StartedAt           *time.Time `json:"startedAt"`
    CompletedAt         *time.Time `json:"completedAt"`
}
```

### `QuotaStatus` (NATS subject: `mclaude.{userSlug}.quota`)

Published by the daemon quota publisher every 60 seconds:

```go
type QuotaStatus struct {
    U5      int       `json:"u5"`      // 5h window utilization %
    U7      int       `json:"u7"`      // 7d window utilization %
    R5      time.Time `json:"r5"`      // 5h window reset time (UTC)
    R7      time.Time `json:"r7"`      // 7d window reset time (UTC)
    HasData bool      `json:"hasData"` // false when API call failed
    TS      time.Time `json:"ts"`
}
```

### Lifecycle Event Payloads

Published on `mclaude.users.{userSlug}.hosts.{hostSlug}.projects.{projectSlug}.lifecycle.{sessionSlug}` (core NATS):

**`session_job_complete`**:
```json
{
  "type": "session_job_complete",
  "sessionId": "...",
  "jobId": "...",
  "branch": "schedule/refactor-auth",
  "ts": "..."
}
```

**`session_job_paused`**:
```json
{
  "type": "session_job_paused",
  "sessionId": "...",
  "jobId": "...",
  "pausedVia": "quota_soft",
  "u5": 76,
  "r5": "2026-04-28T08:00:00Z",
  "outputTokensSinceSoftMark": 0,
  "ts": "..."
}
```

**`session_job_cancelled`**:
```json
{
  "type": "session_job_cancelled",
  "sessionId": "...",
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

**`session_job_failed`**:
```json
{
  "type": "session_job_failed",
  "sessionId": "...",
  "jobId": "...",
  "error": "subprocess exited without turn-end signal",
  "ts": "..."
}
```

## Stop Hook Authoring Guide

Callers are responsible for two things: (1) the **prompt** must teach Claude how to respond to `MCLAUDE_STOP: quota_soft`, and (2) the **Stop hook** (optional but recommended) must distinguish platform-initiated stops from Claude-initiated ones.

### Prompt Obligations

The prompt should include explicit handling for `MCLAUDE_STOP: quota_soft`. Minimum viable guidance:

```
If you receive a user message starting with "MCLAUDE_STOP: quota_soft":
  1. Save any in-progress work (git add + commit, or write to a scratchpad file).
  2. Output a one-line summary of where you are.
  3. End your turn. Do not start new tasks.
```

If the prompt is silent and Claude keeps working past the marker, the hard-token budget (`HardHeadroomTokens`) catches it and the session ends up `paused` with `PausedVia: "quota_hard"`. That's the safety net, not the intended path.

### Stop Hook Responsibilities

**Scenario A — Platform-initiated stop (quota_soft).** The last user message is `MCLAUDE_STOP: quota_soft`. Claude has saved state. The hook should **allow the stop**. The platform will mark the job `paused` and resume later.

**Scenario B — Claude-initiated stop (natural turn-end).** No `MCLAUDE_STOP:` marker in recent messages. Claude thinks it's done. The hook should **evaluate**: is the work actually complete? If caller has work-done criteria (all tests passing, PR created, etc.), check them. If not met, block with steering:

```json
{ "decision": "block", "reason": "Tests still failing — investigate and fix before stopping." }
```

### Reference Hook Skeleton

```bash
#!/usr/bin/env bash
INPUT=$(cat)
TRANSCRIPT=$(jq -r .transcript_path <<< "$INPUT")

LAST_USER=$(tac "$TRANSCRIPT" | jq -r 'select(.type=="user") | .message.content' | head -1)

# Scenario A: platform asked us to stop. Allow.
if [[ "$LAST_USER" == MCLAUDE_STOP:* ]]; then
  exit 0
fi

# Scenario B: Claude-initiated. Check caller-specific "am I done?" criteria.
if unfinished_work_exists; then
  jq -n '{decision: "block", reason: "There are still open tasks. Continue working."}'
  exit 0
fi

# Genuinely done. Allow the stop.
exit 0
```

### Anti-Patterns

- **Hook blocks even when `MCLAUDE_STOP: quota_soft` is present.** Session runs past marker; hard budget interrupts; `PausedVia: "quota_hard"` on a soft breach signals miscoded hook.
- **Hook has no Scenario-A path.** Platform stops still work (hook allows by default), but work-done logic may misfire on `MCLAUDE_STOP:` messages. Always short-circuit on the marker first.
- **No hook and no work-done criteria.** Every natural turn-end becomes `completed` — fine for single-shot tasks, problematic for long-running sprints.

## Error Handling

| Failure | Handling |
|---------|----------|
| Quota API call fails | `QuotaStatus{HasData: false}` published. Monitor does NOT trigger stop. Dispatcher does not start new jobs. Retry at next 60s interval. |
| Credentials file missing or token absent | Quota publisher logs warning; publishes `{HasData: false}`. Does not crash daemon. |
| Session fails to start (30s KV poll timeout) | Dispatcher resets job to `queued`, increments `RetryCount`. After 3 failures: marks `failed`. |
| Prompt ignores `MCLAUDE_STOP: quota_soft` | Token counter fires hard interrupt at `HardHeadroomTokens`. Session paused with `PausedVia: "quota_hard"`. |
| Stop hook blocks on quota_soft (caller bug) | Same as above. `PausedVia: "quota_hard"` surfaces the issue. |
| Out-of-allowlist tool request | Auto-denied via `control_response`. `session_permission_denied` published. Graceful stop sent. Job → `needs_spec_fix`. Other running jobs unaffected. |
| Daemon restarts mid-job | Dispatcher scans `jobQueueKV`. `running`/`starting` with no matching session → reset to `queued`. `paused` with past `ResumeAt` → reset to `queued`. |
| `needs_spec_fix` jobs | Stay in KV. Dispatcher never auto-starts. User cancels and re-queues after fixing. |
| Session lost while paused | Dispatcher's resume falls back to `sessions.create` with `resumeClaudeSessionID`. If that fails, `Status = failed`. |
| `claude --resume` fails | Session-agent returns error; dispatcher marks job `failed`. |
| `sessions.create` reply missing `claudeSessionID` | Dispatcher logs warning, persists empty string. Normal resume works (session alive). Degraded `--resume` fallback will fail (no ID). |
| `usage.output_tokens` missing from stream-json event | QuotaMonitor falls back to byte-count estimate: `len(raw) / 4`. |
| Shared worktree: concurrent sessions | Both write concurrently. Platform does not lock files. Caller designs prompts for collaboration. |
| POST `/jobs` missing required field | 400 Bad Request with `{"error": "<field> required"}`. |
| `branchSlug`/`hostSlug` regex mismatch | 400 with `{"error": "<field> must match ^[a-z0-9][a-z0-9-]*$"}`. |
| `allowedTools` empty | 400 with `{"error": "allowedTools must not be empty"}`. |
| Existing in-flight jobs under old schema | Clean break — cancel and re-queue. Dispatcher skips entries with empty `Prompt` or `BranchSlug`. |

## Security

- OAuth token read from `~/.claude/.credentials.json` at runtime; never written to NATS, KV, or logs.
- Job entries in `mclaude-job-queue` keyed `{userSlug}/...`. NATS auth enforces user isolation. Daemon HTTP endpoints loopback-only (localhost:8378).
- `strict-allowlist` sessions cannot access external services. Any attempt auto-denied and surfaced as `session_permission_denied`.
- Caller-supplied `prompt` passed verbatim — platform does not interpret, sanitize, or escape it.
- `MCLAUDE_STOP:` is a platform-owned marker prefix. Platform never reads it back; only the caller's Stop hook does. A prompt containing the prefix does not confuse the platform.
- `BranchSlug` regex prevents path-traversal branch names.
- Required `permPolicy` + `allowedTools` on POST forces every caller to declare tool scope.
- `ClaudeSessionID` in KV points to a Claude Code transcript — same trust boundary as the rest of `mclaude-job-queue` (user-scoped keys, NATS auth).
- Scheduled sessions use isolated worktree branches. Allowlist does not include force-push; merging to `main` requires PR review.

## Scope

**In scope:**
- Daemon: `runQuotaPublisher` goroutine (polls Anthropic OAuth usage endpoint)
- Daemon: `runJobDispatcher` goroutine (start/pause/resume/restart jobs from KV)
- Daemon: `runLifecycleSubscriber` goroutine (writes terminal job state back to KV on lifecycle events)
- Daemon: `runJobsHTTP` goroutine (localhost:8378 job CRUD)
- Daemon: `sessKV`, `jobQueueKV`, `projectsKV` handles
- Session-agent: `strict-allowlist` permission policy with auto-deny
- Session-agent: `Session.onStrictDeny` and `Session.onRawOutput` callbacks
- Session-agent: `QuotaMonitor` per-session goroutine (`quota_monitor.go`) with two-tier quota (soft threshold + hard token budget)
- Session-agent: `ResumeClaudeSessionID` in `sessions.create` for `--resume` fallback
- Session-agent: `claudeSessionID` in `sessions.create` reply
- Session-agent: `handleDelete` worktree-prune exclusion for `schedule/` branches
- Six lifecycle event types: `session_job_complete`, `session_job_paused` (with `pausedVia`), `session_job_cancelled`, `session_permission_denied`, `session_job_failed`
- `mclaude-job-queue` KV bucket (created by control-plane)
- `/job-queue` skill (platform-level, methodology-agnostic)
- Host-scoped NATS subjects and 4-part session KV keys per ADR-0004

**Out of scope / deferred:**
- `--at` time-based scheduling
- Hardware capacity monitoring (CPU, RAM)
- Job dependencies (run A only after B completes)
- Automatic spec-fix session on `needs_spec_fix`
- K8s-only mode without running laptop daemon
- BYOM proxy chain for `/jobs` endpoint
- Plugin's `/schedule-feature` skill (lives in external plugin repo)
- Plugin's SDD Stop hook configuration (lives in external plugin repo)
- Migration for in-flight jobs (manual cancel + re-queue)
- Subprocess liveness probe / stuck-session handling beyond hard interrupt
- Worktree pruning (caller's PR/merge workflow owns branch lifecycle)
