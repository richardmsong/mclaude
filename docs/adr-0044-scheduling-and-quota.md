# ADR: Session Scheduling and Quota Management

**Status**: draft
**Status history**:
- 2026-04-28: draft

> **Co-requisite:** `adr-0054-nats-jetstream-permission-tightening.md` — ADR-0044 depends on ADR-0054's `state`→`status` rename, per-user KV buckets (`mclaude-sessions-{uslug}`), and per-user session stream (`MCLAUDE_SESSIONS_{uslug}`). Both ADRs must be deployed together as a clean cut-over (all components simultaneously, no rolling upgrade). Implementing ADR-0044 without ADR-0054 will break any component that reads the `state` field from session KV.

> Supersedes:
> - `adr-0009-quota-aware-scheduling.md` — folded in: QuotaMonitor goroutine, real-time Anthropic API quota monitoring via OAuth usage endpoint, strict-allowlist permission policy with auto-deny, unattended session lifecycle events, permission-denied → needs_spec_fix flow, error handling, security model. **Eliminated**: mclaude-job-queue KV bucket, job dispatcher goroutine, lifecycle subscriber goroutine, localhost:8378 HTTP API — replaced by quota fields on `sessions.create` + per-session QuotaMonitor.
> - `adr-0034-generic-scheduler-prompt.md` — folded in: methodology-agnostic free-text prompt field, two-tier quota (soft threshold + hard token budget), paused sessions stay alive, resume via new user message in same conversation, completion via Claude Code Stop hook (not stdout marker), MCLAUDE_STOP: quota_soft marker, ClaudeSessionID for --resume fallback, pausedVia lineage, /schedule-feature moved to external plugin, Stop hook authoring guide.
>
> The two ADRs above are marked `superseded` by this ADR in their status history.

## Overview

Enables unattended Claude sessions with real-time Anthropic API quota monitoring. There is no separate job queue — a scheduled session is just a regular session with quota configuration. The `sessions.create` payload carries optional quota fields (`prompt`, `softThreshold`, `hardHeadroomTokens`, etc.); the session-agent handles the lifecycle internally. This means one abstraction (session), one KV (sessions), one stream, and one set of NATS permissions.

Quota enforcement uses two tiers: a soft threshold that injects a cooperative stop marker (`MCLAUDE_STOP: quota_soft`) and waits for Claude to end its turn naturally, and a hard token budget that sends a `control_request` interrupt when output tokens exceed the budget. Paused sessions stay alive — the subprocess idles on stdin with its in-memory conversation intact, so resume is just a new user message rather than a restart.

## Motivation

mclaude sessions need to run unattended — started, paused on quota pressure, and resumed — without human intervention. The Anthropic API enforces a 5-hour rolling utilization window; sessions must respect this limit and yield gracefully when quota is tight, then resume automatically when quota recovers.

The scheduler primitive must be content-agnostic. Callers supply a free-text prompt and the platform passes it verbatim. Methodology-specific prompt composition (e.g., SDD `/dev-harness` invocations), Stop hook configuration, and completion criteria belong in caller-side plugins, not in the platform.

**Why no separate job queue:** A job was a session with extra fields (prompt, quota thresholds). The job queue KV duplicated state already tracked by the session KV. The dispatcher was a middleman between the caller and the session-agent. Merging jobs into sessions eliminates the `mclaude-job-queue-{uslug}` KV bucket, the dispatcher goroutine, and the `localhost:8378` HTTP API. The session-agent already manages session lifecycle — it's the natural place for quota enforcement.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| No separate job queue | Scheduled sessions are regular sessions with optional quota fields on `sessions.create`. Session KV is the single source of truth. | Eliminates `mclaude-job-queue-{uslug}` KV bucket, job dispatcher goroutine, `localhost:8378` HTTP API, and all job-queue NATS permissions. One abstraction instead of two. |
| Prompt shape | Caller supplies a free-text `prompt` field on `sessions.create`; agent sends it verbatim as the initial `sessions.input` after CLI startup. | Methodology-agnostic. Platform never parses, rewrites, or prepends content. |
| Quota data source | `api.anthropic.com/api/oauth/usage` via OAuth token from `~/.claude/.credentials.json` | No dependency on claude-pace or any shell plugin. Works wherever the daemon runs as long as credentials exist. |
| Quota publisher | Daemon goroutine polling every 60 seconds; publishes `QuotaStatus` to NATS subject `mclaude.users.{uslug}.quota` (core NATS, no JetStream retention). Only the **designated** agent runs the publisher — CP assigns one agent per user on registration. If the designated agent goes offline (heartbeat timeout), CP re-designates another. | Prevents duplicate API calls and inconsistent quota decisions when a user has agents on multiple hosts. |
| Quota — soft threshold | `softThreshold` (% 5h utilization) per session. On breach, agent injects `MCLAUDE_STOP: quota_soft` via `sessions.input` and waits for Claude to end its turn. | Cooperative stop — Claude saves state before yielding. |
| Quota — hard budget | `hardHeadroomTokens` per session. After soft marker injection, QuotaMonitor counts output tokens; at budget, sends `control_request` interrupt directly on `s.stdinCh`. When `hardHeadroomTokens` is 0, the hard interrupt fires immediately after the soft marker is injected (zero tolerance — no output tokens allowed past the soft mark). | More precise than a percentage. Token counter lives in session-agent (already reads stream-json). Interrupts bypass Stop hooks per Claude Code. 0 = immediate hard stop after soft marker; use when no additional output is acceptable. |
| Paused sessions stay alive | Soft-stop and hard-stop both leave the Claude Code subprocess running. Subprocess idles on stdin with in-memory conversation intact. | Eliminates restart complexity — no `--resume`, no prompt idempotency requirement, no context loss. |
| Resume from pause | Agent sends a new user message via `sessions.input`. Uses caller-supplied `resumePrompt` if provided; otherwise platform defaults (different for soft vs hard). | Resume is just another turn in the same conversation. |
| Default resume nudges | Soft-paused: `"Resuming — continue where you left off."` Hard-paused: `"Your previous turn was interrupted mid-response. Check git status and recover state before continuing."` | Hard-stopped sessions may have inconsistent state; different nudge prompts recovery. |
| Completion signalling | No stdout-marker scanning. Completion happens when Claude ends a turn naturally and the Stop hook allows the stop. Session record persists — `sessions.delete` is only issued on explicit user action. | Platform stays out of content. Stop hook is the authoritative "is this done?" decider. Session KV retains the record for SPA review. |
| Stop hook mechanism | Claude Code's native `hooks.Stop` (in caller's `settings.json`). Not a platform primitive. | Reuses Claude Code hook infrastructure. Hook fires on natural turn-end; can block to force another turn. |
| Stop-reason signalling (platform → hook) | Agent injects `MCLAUDE_STOP: quota_soft` as a user message. Caller's hook reads `transcript_path`, scans last user message for marker, and allows stop when present. | Transcript is the one surface both sides can see. Only cooperative stop uses the marker; hard-stop and cancel bypass hooks. |
| Turn-end detection | QuotaMonitor detects turn-end via stream-json `result` event (`EventTypeResult`). `handleTurnEnd()` inspects `stopReason` state to distinguish paused-vs-completed. | `result` event is Claude Code's canonical end-of-turn marker. |
| Parallelism model | All sessions with quota config start immediately. Each session independently monitors `u5` against its own `softThreshold`. | `softThreshold` encodes urgency: higher threshold = more aggressive (session runs until quota is nearly exhausted, finishes sooner); lower threshold = conservative (pauses early, yields to others, finishes whenever). No cross-session coordination needed. |
| Permission policy for unattended sessions | `strict-allowlist` mode: auto-approve allowlisted tools, auto-deny everything else. Caller passes `permPolicy` and `allowedTools` on `sessions.create`. | Prevents blocking on out-of-allowlist tool prompts. Forces every caller to declare tool scope. |
| Worktree per session | Branch `schedule/{branchSlug}`. If worktree exists, session attaches to it — same slug = shared worktree. | Isolated worktrees prevent file conflicts. Shared-slug attach lets sessions collaborate. |
| Worktree cleanup | Platform does not prune worktrees. Caller's PR/merge workflow owns branch lifecycle. | Avoids cross-session race conditions; platform stays out of git state beyond creation/attach. |
| Auto-continuation | `autoContinue` flag per session. When set and paused on quota, agent resumes at 5h reset time. | Without the flag, sessions resume whenever quota recovers (no wait for reset). |
| Cancel | `sessions.delete` — same as any session. Agent interrupts subprocess and reaps. Emits `session_job_cancelled` lifecycle event. | Uniform mechanism. Stop hook doesn't fire because interrupts bypass it. |
| Degraded fallback: session loss | Agent persists `claudeSessionID` to session KV. On session loss (pod eviction, crash), respawns with `claude --resume <claudeSessionID>`. | Normal path never uses this. Only covers actual infra failure. |
| Permission-denied signaling | In-process Go channel from `Session.onStrictDeny` to `QuotaMonitor.permDeniedCh`; lifecycle event published. | Avoids a NATS round-trip for an in-process event. |
| `/schedule-feature` skill location | Lives in external plugin (e.g., `spec-driven-dev` plugin). Deleted from mclaude. | Skill composes methodology-specific prompts + configures Stop hooks; those are caller concerns, not platform. |
| Time-based scheduling (`--at`) | Deferred. | Priority is throughput, not specific time windows. Sessions start as quota allows. |

## User Flow

### Creating a Scheduled Session

1. Caller assembles a free-text prompt. The prompt may optionally include instructions for Claude on how to respond to `MCLAUDE_STOP: quota_soft` (e.g., "commit in-progress work, output a one-line status, stop"). If the prompt is silent on this, Claude will typically wrap up on its own; if it doesn't, the hard-token budget catches it.
2. Caller (optionally) configures a Stop hook in `settings.json`. The hook can block turn-ends to keep Claude running (course-correction). To avoid fighting with the platform on quota stops, the hook should check whether the last user message in `transcript_path` contains `MCLAUDE_STOP: quota_soft` and NOT block when it does. Without a hook, every natural turn-end results in `completed`.
3. Caller publishes `sessions.create` with quota fields (all quota fields are **top-level**, not nested under a `quotaMonitor` object — this ADR supersedes the nested shape in `spec-nats-payload-schema.md`):
   ```json
   {
     "id": "01JTRK...",
     "ts": 1714470060000,
     "sessionSlug": "sched-001",
     "backend": "claude_code",
     "prompt": "<free text>",
     "branchSlug": "refactor-auth-middleware",
     "softThreshold": 75,
     "hardHeadroomTokens": 50000,
     "autoContinue": true,
     "permPolicy": "strict-allowlist",
     "allowedTools": ["Read", "Write", "Edit", "Glob", "Grep", "Bash"],
     "resumePrompt": ""
   }
   ```
4. Agent starts the session immediately (see "Session Startup" below).

### Session Startup

1. `sessions.create` arrives. Agent ensures the worktree `schedule/{branchSlug}` exists on the host (creates if absent; attaches if present).
2. Agent spawns the Claude Code subprocess immediately. Extracts `claudeSessionID` from the first stream-json `init` event. Persists to session KV. Session KV → `status: pending` (process alive, no work yet).
3. QuotaMonitor subscribes to `mclaude.users.{uslug}.quota` and evaluates:
   - `u5 < softThreshold`: send `prompt` immediately as the initial user message via `sessions.input`. Session KV → `status: running`.
   - `u5 >= softThreshold`: hold the prompt. CLI process is warm and idle on stdin — no tokens consumed. When a quota update arrives with `u5 < softThreshold`, send the prompt. Session KV → `status: running`.

The CLI subprocess is always started immediately (warm, ready to go). Only the prompt delivery is gated by quota. This avoids the "start working then immediately soft-stop" pattern — no tokens are wasted.

### Soft-Stop Path

1. Quota update arrives: `u5 >= softThreshold` for this session.
2. QuotaMonitor for this session injects `MCLAUDE_STOP: quota_soft` via `sessions.input`.
3. QuotaMonitor sets `stopReason = "quota_soft"`, captures `outputTokensAtSoftMark = <current cumulative output tokens>`, and resets `outputTokensSinceSoftMark = 0`. Token counting begins.
4. Claude processes the marker. Caller's prompt (or default Claude behavior) wraps up.
5. Claude ends its turn, emitting a stream-json `result` event. QuotaMonitor's `handleTurnEnd()` publishes `session_job_paused` with `pausedVia: "quota_soft"` and `r5`. `stopReason` resets.
6. Agent updates session KV: `status = paused`, `pausedVia = "quota_soft"`. If `autoContinue` and `r5` is set, `resumeAt = r5`.

### Hard-Stop Path

1. While `stopReason == "quota_soft"`, QuotaMonitor counts output tokens on every `onRawOutput` event.
2. When `outputTokensSinceSoftMark >= hardHeadroomTokens`, QuotaMonitor sets `stopReason = "quota_hard"` and queues a `control_request` interrupt on `s.stdinCh` directly.
3. Claude's current turn ends mid-response (interrupted). Stop hook is NOT fired (interrupts bypass it).
4. QuotaMonitor's `handleTurnEnd()` publishes `session_job_paused` with `pausedVia: "quota_hard"`.
5. Agent updates session KV: `status = paused`, `pausedVia = "quota_hard"`. CLI subprocess stays alive.

### Resume Path

1. Quota update: `u5` drops below `softThreshold` for this session.
2. For `autoContinue` sessions with `resumeAt` set: wait until that time passes.
3. Agent verifies session is still alive (subprocess running). If lost → degraded-fallback path.
4. Agent picks the resume nudge: `resumePrompt` if non-empty, otherwise platform default based on `pausedVia`.
5. Agent sends the nudge via `sessions.input` (same subprocess, same conversation, no context loss).
6. Agent updates session KV: `status = running`, `pausedVia = ""`, `resumeAt = null`.

### Natural Completion Path

1. Claude ends a turn with no platform-injected marker preceding it. Stream-json `result` event fires.
2. Stop hook fires. Hook does not block (either no hook configured, or caller's hook judged this a genuine completion).
3. QuotaMonitor's `handleTurnEnd()` sees `stopReason == ""` and publishes `session_job_complete`.
4. Agent updates session KV: `status = completed`. CLI subprocess exits naturally. Session record persists — the user can review the full conversation in the SPA (stream replay), check the branch, or re-run. `sessions.delete` is only issued when the user explicitly deletes the session.

### Permission-Denied Path

1. Session requests a tool outside the allowlist.
2. Agent auto-denies via `control_response`. Calls `sess.onStrictDeny(toolName)`.
3. `onStrictDeny` publishes `session_permission_denied` lifecycle event AND sends `toolName` on `monitor.permDeniedCh`.
4. QuotaMonitor sends graceful stop message immediately.
5. Agent updates session KV: `status = needs_spec_fix`, `failedTool = toolName`. Session will not auto-restart.

### Cancel Path

1. Caller publishes `sessions.delete` — same mechanism as any session.
2. Agent interrupts subprocess via `stopAndWait`, publishes `session_job_cancelled` lifecycle event.
3. Agent tombstones the session KV entry (KV delete). The user explicitly chose to remove this session — there is no persistent `cancelled` record.

### Degraded Fallback: Session Loss

If a paused session's subprocess is gone when the agent tries to resume (pod eviction, node reboot, crash):

1. Agent constructs a new `sessions.create` internally with `resumeClaudeSessionID` set from session KV.
2. Agent spawns `claude --resume <claudeSessionID>` instead of a fresh session.
3. Agent updates session KV with new subprocess handle, sends resume nudge.
4. If `--resume` fails, `status = failed`.

## Component Changes

### Daemon (`mclaude-session-agent/daemon.go`)

One new goroutine: `runQuotaPublisher` (on the designated agent only). The job dispatcher and lifecycle subscriber are removed — the session-agent handles quota management per-session via `QuotaMonitor`. The `localhost:8378` HTTP API is removed — callers publish `sessions.create` directly.

#### Quota Publisher Designation

CP designates exactly one agent per user as the quota publisher. The designation is delivered via a `quotaPublisher: true` field in the CP's response to `mclaude.hosts.{hslug}.api.agents.register` (the agent registration reply, step 7 in the activity spec). In K8s mode, where the host controller mediates registration, the host controller relays the `quotaPublisher` flag to the agent via the local agent management channel. On disconnect of the designated agent (detected via `$SYS.ACCOUNT.*.DISCONNECT`), CP re-designates the next online agent for that user and notifies it via `manage.designate-quota-publisher`. Non-designated agents only subscribe to the quota subject — they never poll the Anthropic API.

#### `runQuotaPublisher(ctx context.Context)`

Only runs on the designated agent. Stops if the agent receives a de-designation signal (another agent took over).

- Polls every 60 seconds.
- Reads OAuth bearer token from `~/.claude/.credentials.json` (field `.claudeAiOauth.accessToken`). Returns `HasData: false` if missing.
- Calls `GET https://api.anthropic.com/api/oauth/usage` with `Authorization: Bearer {token}` and `anthropic-beta: oauth-2025-04-20`.
- Parses JSON response: `{five_hour: {utilization, resets_at}, seven_day: {utilization, resets_at}}`. The API returns `utilization` as a float in the range `0.0`–`1.0`. The publisher converts to an integer percentage: `U5 = int(apiResp.FiveHour.Utilization * 100)` (e.g., `0.76` → `76`). `softThreshold` values on `sessions.create` use the same 0–100 integer scale, so comparisons are `u5 >= softThreshold` with both sides in integer percent.
- Marshals into `QuotaStatus` and publishes to NATS subject `mclaude.users.{uslug}.quota` (core NATS, no JetStream retention).
- On HTTP/parse error: publishes `QuotaStatus{HasData: false}`.

### Session-Agent (`agent.go`, `session.go`)

#### Extended `sessions.create` Payload

New optional fields (backward-compatible; absent = interactive session):

```go
Prompt                string              `json:"prompt"`
BranchSlug            string              `json:"branchSlug"`
SoftThreshold         int                 `json:"softThreshold"`
HardHeadroomTokens    int                 `json:"hardHeadroomTokens"`
AutoContinue          bool                `json:"autoContinue"`
ResumePrompt          string              `json:"resumePrompt"`
PermPolicy            string              `json:"permPolicy"`
AllowedTools          []string            `json:"allowedTools"`
ResumeClaudeSessionID string              `json:"resumeClaudeSessionID"`
```

When `softThreshold > 0`, the agent treats this as a quota-managed session and starts a `QuotaMonitor`.

#### Permission Policy: `strict-allowlist`

In `handleSideEffect`, when policy is `strict-allowlist` and `shouldAutoApprove` returns false:
1. Build a deny `control_response` with `{"behavior": "deny"}`.
2. Clear pending control.
3. Call `s.onStrictDeny(toolName)` if non-nil.

**`allowedTools` validation:** If `allowedTools` is empty on a `strict-allowlist` session, the agent rejects the `sessions.create` request and publishes a `lifecycle.error` event. There is no default allowlist — callers must explicitly declare their tool scope.

**Breaking change from prior behavior:** The existing code (`agent.go`) and `spec-session-agent.md` apply a `defaultDevHarnessAllowlist` (Read, Write, Edit, Glob, Grep, Bash, Agent, TaskCreate, TaskUpdate, TaskGet, TaskList, TaskOutput, TaskStop) when `allowedTools` is empty on `allowlist` or `strict-allowlist` sessions. This ADR eliminates that default. **Migration:** All existing callers that rely on the implicit default must be updated to pass `allowedTools` explicitly on `sessions.create`. The `defaultDevHarnessAllowlist` constant remains available in code for callers to reference, but the agent will no longer apply it automatically. `spec-session-agent.md` must be updated to remove the "default set is applied" paragraph and instead state that empty `allowedTools` with `strict-allowlist` is rejected.

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

One goroutine per quota-managed session; started from `handleCreate` when `softThreshold > 0`.

```go
type QuotaMonitor struct {
    sessionSlug              string
    userSlug                 string
    hostSlug                 string
    projectSlug              string
    softThreshold            int
    hardHeadroomTokens       int
    autoContinue             bool
    prompt                   string // initial prompt; held until quota allows delivery
    resumePrompt             string
    nc                       *nats.Conn
    session                  *Session
    sessKV                   jetstream.KeyValue
    permDeniedCh             chan string
    quotaCh                  chan *nats.Msg
    quotaSub                 *nats.Subscription
    lastU5                   int
    lastR5                   time.Time
    stopReason               string  // "" | "quota_soft" | "quota_hard"
    outputTokensAtSoftMark   int     // cumulative outputTokens snapshot when soft marker injected
    outputTokensSinceSoftMark int
    turnEndedCh              chan struct{} // 1-buffered; fires on result event
    terminalEventPublished   bool
    stopCh                   chan struct{}
}
```

**Goroutine select-loop cases:**

- `<-m.stopCh`: exit cleanly.
- `toolName := <-m.permDeniedCh`: if `stopReason == ""`, set `stopReason = "permDenied"`, send graceful stop.
- `msg := <-m.quotaCh`: update cached `QuotaStatus`. Four triggers:
  - **Soft threshold breached** (`u5 >= softThreshold` and `stopReason == ""`): set `stopReason = "quota_soft"`, inject `MCLAUDE_STOP: quota_soft` via `sessions.input`, capture `outputTokensAtSoftMark = <current cumulative output tokens>`, reset `outputTokensSinceSoftMark = 0`.
  - **Quota available for pending session** (session is `pending`, `u5 < softThreshold`): send `m.prompt` as the initial user message via `sessions.input`, update session KV → `running`.
  - **Quota recovered** (session is `paused`, `u5 < softThreshold`): check `resumeAt` if `autoContinue`, send resume nudge via `sessions.input`, update session KV → `running`.
  - **No data** (`hasData == false`): do not start pending sessions, do not pause running ones.
- `<-m.turnEndedCh`: dispatch to `handleTurnEnd()`. **Priority: `turnEndedCh` is checked before `doneCh` in the select.** Before entering the main select loop, the QuotaMonitor drains all pending `turnEndedCh` messages. In the natural completion case, the `result` event fires first (Claude finishes), then the subprocess exits (`doneCh` closes). Go's `select` picks non-deterministically among ready cases, so the implementation uses a nested `select` with `default` fallthrough: first try `turnEndedCh`; only if empty, fall through to the full `select` including `doneCh`. This ensures `handleTurnEnd` sets `terminalEventPublished` before `handleSubprocessExit` can observe `doneCh`, preventing false `session_job_failed` events on successful completions.
- `<-session.doneCh`: dispatch to `handleSubprocessExit()`.

Each QuotaMonitor independently evaluates `u5 >= softThreshold` for its own session. No cross-session coordination. `softThreshold` encodes urgency: higher = more aggressive (keeps running longer), lower = conservative (pauses earlier).

**`sessions.input` message format:** QuotaMonitor publishes messages to the NATS subject `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.input` (captured by `MCLAUDE_SESSIONS_{uslug}` stream for replay). Messages use the same `sessions.{sslug}.input` JSON envelope defined in `spec-nats-payload-schema.md`:

```json
{
  "id": "01JTRK...",
  "ts": 1714470080000,
  "type": "message",
  "text": "MCLAUDE_STOP: quota_soft"
}
```

All three QuotaMonitor-generated message types use this format:
- **Initial prompt delivery:** `"text": "<caller-supplied prompt>"` — sent when `u5 < softThreshold` and session is `pending`.
- **Soft-stop marker:** `"text": "MCLAUDE_STOP: quota_soft"` — sent when `u5 >= softThreshold` during a running session.
- **Resume nudge:** `"text": "Resuming — continue where you left off."` (or `resumePrompt` if provided) — sent when quota recovers and session is `paused`.

The `id` field is a ULID generated by the QuotaMonitor. The `ts` field is the current Unix millisecond timestamp. This is distinct from `control_request` interrupts (hard-stop), which are written directly to `sess.stdinCh` in Claude Code's stream-json stdin format (`{"type":"control_request","request":{"subtype":"interrupt"}}`) and are NOT published to NATS.

**Token-budget check** (in `onRawOutput`): when `outputTokensSinceSoftMark >= hardHeadroomTokens` and `stopReason == "quota_soft"`, set `stopReason = "quota_hard"` and queue `control_request` interrupt on `s.stdinCh`.

**`onRawOutput(evType, raw)`** handles:
- **Token counting**: Only the final `result` event carries a cumulative `usage` object with `usage.output_tokens`. Mid-turn `assistant` events (text deltas) do NOT include `usage` — they are streaming fragments without token metadata. Therefore, token counting uses a two-strategy approach:
  - **Primary (byte estimate):** For every `EventTypeAssistant` event while `stopReason != ""`, increment `outputTokensSinceSoftMark` by `len(raw) / 4` (approximate tokens from raw byte count). This provides real-time progress tracking during the turn.
  - **Authoritative (result event):** When `EventTypeResult` arrives with `usage.output_tokens`, replace the running estimate with the authoritative cumulative count: `outputTokensSinceSoftMark = resultUsage.OutputTokens - outputTokensAtSoftMark`. This corrects any estimation drift at turn-end.
  - **Hard budget check:** After each increment (from either strategy), if `outputTokensSinceSoftMark >= hardHeadroomTokens` and `stopReason == "quota_soft"`, fire the hard interrupt immediately. Because the byte estimate is approximate (~±25%), `hardHeadroomTokens` should be set with this margin in mind — the interrupt may fire slightly before or after the exact token count.
- **Turn-end detection**: if `evType == EventTypeResult`, send non-blocking on `turnEndedCh`.

**`handleTurnEnd()`** fires on every stream-json `result` event received by `onRawOutput`. It checks `stopReason` to distinguish pause vs completion. A `result` event with `stopReason == ""` means Claude ended its turn naturally — if the Stop hook also allowed it, this is a genuine completion.

**Clarification on intermediate turns:** `onRawOutput` does NOT unconditionally produce `handleTurnEnd` invocations for every Claude turn. The Claude Code stream-json protocol emits a `result` event when a turn completes, but the Stop hook intercepts turn-end processing *before* the `result` event is emitted to the stream. When the Stop hook blocks a turn-end (returning "continue" to force another turn), Claude Code does not emit the `result` event for that intermediate turn — the hook suppresses it and a new turn begins immediately. Therefore, only *final* turn-ends (where the Stop hook allows the stop or no hook is configured) produce `result` events that reach `onRawOutput` and fire `turnEndedCh`. In the unattended session model, each turn runs to completion and the Stop hook is the authoritative decider: if it allows the stop and `stopReason == ""`, that IS a genuine completion.

`handleTurnEnd()` inspects `stopReason`:

| `stopReason` | Event published | Next step |
|--------------|-----------------|-----------|
| `"quota_soft"` | `session_job_paused` with `pausedVia: "quota_soft"` + `r5` | Reset `stopReason`; update session KV → `paused`; subprocess stays alive. |
| `"quota_hard"` | `session_job_paused` with `pausedVia: "quota_hard"` + `r5` + `outputTokensSinceSoftMark` | Reset `stopReason`; update session KV → `paused`; subprocess stays alive. |
| `""` | `session_job_complete` | Update session KV → `completed`. CLI subprocess exits naturally. Session record persists for user review. |

**`handleSubprocessExit()`** (on `doneCh` close):
- If `terminalEventPublished` → no-op (expected cleanup after completion or cancellation).
- Otherwise → publish `session_job_failed` with `error: "subprocess exited without turn-end signal"`. Update session KV → `failed`.

**Agent restart recovery:** Recovery does NOT use stream replay. The agent uses the existing KV-based recovery mechanism (matching `spec-session-agent.md` § Resumption and current code `agent.go:240-310`):

1. Agent iterates all entries in the session KV bucket (`mclaude-sessions-{uslug}`) for its host and project scope.
2. For each session KV entry with `softThreshold > 0` (quota-managed):
   - **`status: pending`**: CLI subprocess was warm but prompt was never sent. Agent respawns the CLI subprocess, starts a new QuotaMonitor, subscribes to quota updates, and gates prompt delivery on the next quota update (same as initial startup).
   - **`status: paused`**: Session was paused on quota. Agent checks if the subprocess is alive. If alive: start a new QuotaMonitor and wait for quota recovery. If dead: attempt `--resume` with `claudeSessionID` from KV (degraded fallback). If `autoContinue` and `resumeAt` has passed, attempt resume immediately on next favorable quota update.
   - **`status: running`**: Session was mid-execution. CLI subprocess is dead (agent restarted). Agent reads `claudeSessionID` from KV and attempts `claude --resume <claudeSessionID>`. On success, starts a new QuotaMonitor. On failure, updates KV → `status: failed`.
3. Interactive sessions (no quota fields) follow the existing recovery path unchanged.

This approach reuses the proven KV-watch recovery mechanism. No temporary stream consumers or replay infrastructure is needed — session KV is the recovery source of truth.

### `handleDelete` Branching: Interactive vs Quota-Managed

`handleDelete` uses the session's `SoftThreshold` field to distinguish interactive from quota-managed sessions. The branching condition is `sess.getState().SoftThreshold > 0`:

- **Interactive session** (`SoftThreshold == 0`): existing behavior — emit `session_stopped` lifecycle event, delete KV entry, remove worktree if last user.
- **Quota-managed session** (`SoftThreshold > 0`): emit `session_job_cancelled` lifecycle event, tombstone the KV entry (KV delete — session record disappears). If the session has an active `QuotaMonitor`, call `monitor.stop()` before `sess.stopAndWait()`.

**Worktree handling:** If the session's `Branch` field starts with the `schedule/` prefix (checked via `strings.HasPrefix(st.Branch, "schedule/")`), skip worktree removal regardless of session type. The `Branch` field (not the slugified `Worktree` field) is the check target because the caller controls branch naming and the `schedule/` prefix is the caller's signal that the worktree should persist for potential re-use.

### Control-Plane (`mclaude-control-plane`)

No changes. The `mclaude-job-queue` KV bucket is removed. Session KV (`mclaude-sessions-{uslug}`) already exists and tracks all session state.

### Session KV Extensions

The session KV gains new fields. The existing `State` field is **renamed to `Status`** (`json:"status"`) and its value set is extended with the new quota-managed states: `pending`, `paused`, `completed`, `cancelled`, `needs_spec_fix`. The full `status` enum is documented in `spec-nats-payload-schema.md` (Session KV section). The `state`→`status` rename is deployed as part of the ADR-0054 clean cut-over (all components deployed simultaneously, no rolling upgrade). No backward-compatibility shim or dual-read fallback is needed.

New optional fields for quota-managed sessions:

```go
// Renamed field (was State string `json:"state"`)
Status                string     `json:"status"`              // pending | running | paused | requires_action | completed | stopped | cancelled | needs_spec_fix | failed | error

// Added fields (zero values omitted for interactive sessions)
SoftThreshold         int        `json:"softThreshold,omitempty"`
HardHeadroomTokens    int        `json:"hardHeadroomTokens,omitempty"`
AutoContinue          bool       `json:"autoContinue,omitempty"`
PausedVia             string     `json:"pausedVia,omitempty"`
ClaudeSessionID       string     `json:"claudeSessionID,omitempty"`
BranchSlug            string     `json:"branchSlug,omitempty"`
FailedTool            string     `json:"failedTool,omitempty"`
ResumeAt              *time.Time `json:"resumeAt,omitempty"`
```

Interactive sessions omit the quota fields. The SPA can distinguish quota-managed sessions by checking `softThreshold > 0`.

## Data Model

### `QuotaStatus` (NATS subject: `mclaude.users.{uslug}.quota`)

Published by the designated agent's quota publisher every 60 seconds:

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

**Authoritative subject pattern (ADR-0054):** `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.lifecycle.{eventType}` — captured by the per-user `MCLAUDE_SESSIONS_{uslug}` stream (filter: `mclaude.users.{uslug}.hosts.*.projects.*.sessions.>`). This is the ADR-0054 target pattern where lifecycle events live under the session namespace.

**Note on spec divergence:** The pre-ADR-0054 pattern in `spec-state-schema.md` is `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}` (captured by the shared `MCLAUDE_LIFECYCLE` stream). The current code (`agent.go:publishLifecycle`, `subj.UserHostProjectLifecycle`) uses this pre-ADR-0054 pattern. The migration to the ADR-0054 pattern (`.sessions.{sslug}.lifecycle.{eventType}`) is part of the ADR-0054 co-requisite deployment. Until then, implementations should use the current `subj.UserHostProjectLifecycle` helper, which will be updated as part of ADR-0054.

Lifecycle event payloads:

**`session_job_complete`**:
```json
{
  "type": "session_job_complete",
  "sessionId": "...",
  "branch": "schedule/refactor-auth",
  "ts": "..."
}
```

**`session_job_paused`**:
```json
{
  "type": "session_job_paused",
  "sessionId": "...",
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
  "ts": "..."
}
```

**`session_permission_denied`**:
```json
{
  "type": "session_permission_denied",
  "sessionId": "...",
  "tool": "mcp__gmail__send_email",
  "ts": "..."
}
```

**`session_job_failed`**:
```json
{
  "type": "session_job_failed",
  "sessionId": "...",
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
| Quota API call fails | `QuotaStatus{HasData: false}` published. QuotaMonitor does NOT trigger stop or start. Retry at next 60s interval. |
| Credentials file missing or token absent | Designated agent's quota publisher logs warning; publishes `{HasData: false}`. Does not crash daemon. |
| Session fails to start (CLI process crash on spawn) | Agent updates session KV → `failed`. Publishes `lifecycle.error`. |
| Prompt ignores `MCLAUDE_STOP: quota_soft` | Token counter fires hard interrupt at `hardHeadroomTokens`. Session paused with `pausedVia: "quota_hard"`. |
| Stop hook blocks on quota_soft (caller bug) | Same as above. `pausedVia: "quota_hard"` surfaces the issue. |
| Out-of-allowlist tool request | Auto-denied via `control_response`. `session_permission_denied` published. Graceful stop sent. Session KV → `needs_spec_fix`. |
| `needs_spec_fix` sessions | Stay in session KV. Agent will not auto-resume. User cancels and re-creates. |
| Agent restart mid-session | Agent replays session stream. Quota-managed sessions in `pending`/`paused` state: re-evaluate against current quota. `running` sessions with no live subprocess: attempt `--resume` fallback. |
| Session lost while paused | Agent attempts `--resume` with `claudeSessionID` from session KV. If that fails, session KV → `failed`. |
| `claude --resume` fails | Agent updates session KV → `failed`. Publishes `session_job_failed`. |
| `claudeSessionID` missing from session KV | Normal resume works (subprocess alive). Degraded `--resume` fallback will fail (no ID to resume from). |
| `usage.output_tokens` missing from stream-json `assistant` event | Expected — mid-turn `assistant` events never carry `usage`. QuotaMonitor uses byte-count estimate `len(raw) / 4` during the turn. Authoritative count arrives on `result` event. |
| `allowedTools` empty on `strict-allowlist` session | Agent rejects `sessions.create` — publishes `lifecycle.error` with message. |
| Shared worktree: concurrent sessions | Both write concurrently. Platform does not lock files. Caller designs prompts for collaboration. |

## Security

- OAuth token read from `~/.claude/.credentials.json` at runtime; never written to NATS, KV, or logs.
- Session KV entries are per-user (`mclaude-sessions-{uslug}`). NATS auth enforces user isolation.
- `strict-allowlist` sessions cannot access external services. Any attempt auto-denied and surfaced as `session_permission_denied`.
- Caller-supplied `prompt` passed verbatim — platform does not interpret, sanitize, or escape it.
- `MCLAUDE_STOP:` is a platform-owned marker prefix. Platform never reads it back; only the caller's Stop hook does. A prompt containing the prefix does not confuse the platform.
- `branchSlug` regex (`[a-z0-9-]+`) prevents path-traversal branch names.
- `ClaudeSessionID` in session KV points to a Claude Code transcript — same trust boundary as the rest of `mclaude-sessions-{uslug}` (user-scoped keys, NATS auth).
- Scheduled sessions use isolated worktree branches. Allowlist does not include force-push; merging to `main` requires PR review.

## Scope

**In scope:**
- Daemon: `runQuotaPublisher` goroutine on designated agent only (polls Anthropic OAuth usage endpoint, publishes to `mclaude.users.{uslug}.quota`)
- Session-agent: `strict-allowlist` permission policy with auto-deny
- Session-agent: `Session.onStrictDeny` and `Session.onRawOutput` callbacks
- Session-agent: `QuotaMonitor` per-session goroutine (`quota_monitor.go`) with two-tier quota (soft threshold + hard token budget)
- Session-agent: `resumeClaudeSessionID` in `sessions.create` for `--resume` fallback
- Session-agent: `handleDelete` worktree-prune exclusion for `schedule/` branches
- Session-agent: agent restart recovery (KV-based, re-evaluate pending/paused quota-managed sessions)
- Five lifecycle event types: `session_job_complete`, `session_job_paused` (with `pausedVia`), `session_job_cancelled`, `session_permission_denied`, `session_job_failed`
- Session KV extensions: `softThreshold`, `hardHeadroomTokens`, `autoContinue`, `pausedVia`, `claudeSessionID`, `branchSlug`, `failedTool`, `resumeAt`

**Eliminated (from prior ADR-0009/0034):**
- `mclaude-job-queue` KV bucket — session KV is the single source of truth
- Job dispatcher goroutine — agent handles quota evaluation per-session
- `localhost:8378` HTTP API — callers publish `sessions.create` directly
- Lifecycle subscriber goroutine — agent updates session KV directly

**Out of scope / deferred:**
- `--at` time-based scheduling
- Hardware capacity monitoring (CPU, RAM)
- Session dependencies (run A only after B completes)
- Automatic spec-fix session on `needs_spec_fix`
- Plugin's `/schedule-feature` skill (lives in external plugin repo)
- Plugin's SDD Stop hook configuration (lives in external plugin repo)
- Subprocess liveness probe / stuck-session handling beyond hard interrupt
- Worktree pruning (caller's PR/merge workflow owns branch lifecycle)
