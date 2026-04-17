## Audit: 2026-04-14T00:00:00Z

**Document:** docs/plan-quota-aware-scheduling.md

### Round 1

**Gaps found: 10**

1. `runJobDispatcher` has no source of `projectId`
2. `mclaude-job-queue` KV bucket uses wrong JetStream API (control-plane uses legacy `nats.JetStreamContext`)
3. `publishStrictDeny` method undefined — no receiver, signature, or `jobId` access path
4. `QuotaMonitor` has no mechanism to observe `session_permission_denied` — only subscribes to quota subject
5. `SESSION_JOB_COMPLETE` detection via `onEventPublished` callback is impossible — callback only gets `(evType, seq)`
6. MCP tools call unspecified API surface — design says "control-plane" but all existing tools call `http://localhost:8377` (mclaude-server)
7. `create_job` MCP schema missing `projectId` field
8. Graceful stop message includes top-level `session_id` field — wrong for direct `stdinCh` write
9. `cancel_job` has no mechanism to get `userId` for constructing NATS `sessions.delete` subject
10. Daemon startup recovery needs `mclaude-sessions` KV but `Daemon` struct never opens it

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1,7 | projectId source | Added `projectId: z.string()` to `create_job` schema; `/schedule-feature` skill calls `list_projects` to match CWD to projectId; `JobEntry.ProjectID` populated at creation time | factual |
| 2 | Wrong KV API | Changed control-plane snippet to use `nats.JetStreamContext` pattern (`js.CreateKeyValue(&nats.KeyValueConfig{...})`) matching projects.go:146-156 | factual |
| 3 | publishStrictDeny undefined | Added `Session.onStrictDeny func(toolName string)` callback; Agent sets it in handleCreate; defines `Agent.publishPermDenied()` with full signature | factual |
| 4 | QuotaMonitor perm-denial observation | Changed to in-process channel `QuotaMonitor.permDeniedCh chan string`; `onStrictDeny` calls `monitor.signalPermDenied(toolName)` which sends on channel; monitor selects on it | factual |
| 5 | SESSION_JOB_COMPLETE detection | Added `Session.onRawOutput func(evType string, raw []byte)` callback; invoked in stdout router before NATS publish; QuotaMonitor.onRawOutput scans assistant events for marker | factual |
| 6 | Wrong HTTP surface | Moved `/jobs` endpoints to mclaude-server (Swift/Hummingbird, localhost:8377); added `/jobs` to connector apiPrefixes; specified mclaude-server opens NATS connection for KV ops | factual |
| 8 | session_id in stdinCh message | Removed top-level `session_id` from graceful stop JSON; message goes directly to Claude stdin, not through handleInput routing | factual |
| 9 | cancel_job userId | Specified job HTTP endpoints require JWT auth; userId extracted from JWT claims (same as all other endpoints); scopes KV ops to authenticated userId | factual |
| 10 | Daemon missing sessKV | Added `sessKV jetstream.KeyValue` and `jobQueueKV jetstream.KeyValue` to Daemon struct; both opened in NewDaemon() | factual |

## Run: 2026-04-14T00:01:00Z

**Gaps found: 10**

1. **`runJobDispatcher` has no source of `projectId`** — The dispatcher runs inside the daemon, which manages children per `projectId` but has no `projectId` of its own in `DaemonConfig`. The design says the dispatcher sends `sessions.create` to `mclaude.{userId}.{projectId}.api.sessions.create`, but `DaemonConfig` (daemon.go:34-43) has no `ProjectID` field and `JobEntry` has a `ProjectID` field that is written by the MCP tool — yet the design never specifies how the MCP tool learns the correct `projectId` to populate `JobEntry.ProjectID` when `create_job` is called, nor how the dispatcher reads it.
   - **Doc**: "Sends request to `mclaude.{userId}.{projectId}.api.sessions.create`." (Component Changes → Daemon → runJobDispatcher)
   - **Code**: `DaemonConfig` (daemon.go:34) has no `ProjectID` field. `JobEntry.ProjectID` exists in the schema but the create_job tool spec does not include a `projectId` parameter, and the /schedule-feature skill does not pass one.

2. **`mclaude-job-queue` KV bucket uses wrong JetStream API** — The design uses the newer `jetstream` package API (`jetstream.CreateOrUpdateKeyValue`, `jetstream.KeyValueConfig`, `jetstream.FileStorage`) but the control-plane uses `nats.go v1.50.0` with the legacy `nats.JetStreamContext` API (`js.CreateKeyValue`, `nats.KeyValueConfig`) as seen in projects.go. The session-agent uses `nats.go v1.38.0` with the new `jetstream` sub-package. The design does not specify which package and API style the control-plane's new KV creation should use, and the two are incompatible call sites.
   - **Doc**: `js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{...})` (Component Changes → Control-Plane)
   - **Code**: control-plane/projects.go:146-156 uses `nats.JetStreamContext` / `nats.KeyValueConfig`. control-plane/go.mod uses nats.go v1.50.0 which does not import `jetstream` sub-package (that is the session-agent's v1.38.0 convention).

3. **`publishStrictDeny` method is not defined** — The design introduces a new method `a.publishStrictDeny(sessionID, toolName)` called from the `strict-allowlist` control path (session.go side-effect handler), but neither its signature, receiver (Agent vs Session), nor its NATS publish subject is specified. The existing `publishLifecycle` (agent.go:684) takes only `(sessionID, eventType string)` and publishes a minimal payload; `publishStrictDeny` needs to include `tool` and `jobId` fields but the design doesn't specify how `jobId` is accessible at the point where `shouldAutoApprove` / the deny path executes inside `Session.handleSideEffect`, which has no reference to the `QuotaMonitorConfig`.
   - **Doc**: "Call `a.publishStrictDeny(sessionID, toolName)` → emits `session_permission_denied` lifecycle event." (Component Changes → Session-Agent → strict-allowlist)
   - **Code**: `Session.handleSideEffect` (session.go:261) only has access to the session struct and a `writeKV` callback. No `Agent` reference or `jobId` is reachable from that call path.

4. **`QuotaMonitor` goroutine's observation of `session_permission_denied` is underspecified** — The design says the QuotaMonitor observes permission denials "via its NATS subscription" but the lifecycle subject is `mclaude.{userId}.{projectId}.lifecycle.{sessionId}` (core NATS, not JetStream). The QuotaMonitor is described as subscribing to `mclaude.{userId}.quota` (quota status), not to the lifecycle subject. There is no specified subscription to the lifecycle subject within `QuotaMonitor`, so the mechanism for the monitor to detect a `session_permission_denied` event and trigger graceful stop is undefined.
   - **Doc**: "The quota monitor goroutine (attached to this session) observes the lifecycle event via its NATS subscription and sends the graceful stop message." (Component Changes → Session-Agent → strict-allowlist); "On `strict-allowlist` permission denial (observed via `publishStrictDeny` callback):" (Component Changes → quota_monitor.go)
   - **Code**: `QuotaMonitor` struct only lists a subscription to `mclaude.{userId}.quota`. No lifecycle subscription is defined.

5. **`SESSION_JOB_COMPLETE` detection mechanism is unimplemented in the described callback** — The design states the QuotaMonitor "Detects `SESSION_JOB_COMPLETE:{prUrl}` in the session's output stream (via `onEventPublished` callback on assistant events)." But `onEventPublished` (session.go:54) is called with `(evType string, seq uint64)` — the event type string and sequence number only. The raw content of the assistant message is not passed. There is no mechanism described to get the full text content of an assistant event through this callback, making the PR URL extraction unimplementable as specified.
   - **Doc**: "Detects `SESSION_JOB_COMPLETE:{prUrl}` in the session's output stream (via `onEventPublished` callback on assistant events) and parses the PR URL." (Component Changes → quota_monitor.go)
   - **Code**: `onEventPublished func(evType string, seq uint64)` (session.go:54). The session stdout router calls `notify(evType, 0)` (session.go:222) without passing raw line content.

6. **MCP tools call the wrong API surface** — The existing MCP server (mclaude-mcp/src/index.ts) makes HTTP calls to `MCLAUDE_URL` which is the relay/connector URL (default `http://localhost:8377`), not directly to the control-plane. The design adds four tools (`create_job`, `list_jobs`, `get_job`, `cancel_job`) that call `/jobs` endpoints on the control-plane. The design does not specify whether these HTTP endpoints are routed through the relay, or whether the MCP server should call the control-plane directly, or what `MCLAUDE_URL` resolves to in this context (relay or control-plane). The existing tool implementations all call paths like `/sessions`, `/projects` that are either relay-proxied or control-plane paths — the design gives no indication of which host serves `/jobs`.
   - **Doc**: "Four new tools added" to `mclaude-mcp/src/index.ts`; "New HTTP endpoints (authenticated, same auth as existing session endpoints)" on control-plane. (Component Changes → mclaude-mcp and Control-Plane)
   - **Code**: All existing MCP tools call `api(path)` which prefixes `MCLAUDE_URL`. The control-plane server.go:RegisterRoutes only mounts auth endpoints. The design doesn't show `/jobs` being added to the relay proxy or to the MCP's base URL logic.

7. **`create_job` payload schema is missing `projectId`** — `JobEntry.ProjectID` is a required field (it is used in `sessions.create` targeting), but the `create_job` MCP tool schema only defines `specPath`, `priority`, `threshold`, and `autoContinue`. The skill `/schedule-feature` does not pass a `projectId` either. There is no specification for how `projectId` is determined at job creation time and populated into the `JobEntry`.
   - **Doc**: `create_job` schema (Component Changes → mclaude-mcp); `JobEntry.ProjectID string` (Data Model → mclaude-job-queue KV)
   - **Code**: n/a — the field exists in the schema but the tool input has no way to supply it.

8. **Graceful stop message format conflicts with `handleInput` contract** — The graceful stop message sent to `s.stdinCh` is shown as a JSON object with `type:"user"` and a nested `message` and `session_id` field. But `handleInput` (agent.go:508) expects the raw payload to contain a top-level `session_id` field and passes `msg.Data` directly to `sess.sendInput`. The session reads from `stdinCh` and writes to the Claude stdin pipe as-is. The actual Claude stream-json input format for a user message is `{"type":"user","message":{"role":"user","content":"..."}}` without a top-level `session_id`. If the graceful stop message includes `session_id` at top level it will be forwarded to Claude's stdin unmodified — that is consistent with `handleInput`, but the document shows `session_id` at the top level of the object sent to `stdinCh` directly (bypassing `handleInput`), which differs from the format Claude actually processes.
   - **Doc**: Graceful stop message format (Component Changes → quota_monitor.go): `{"type":"user","message":{"role":"user","content":"QUOTA_THRESHOLD_REACHED..."},"session_id":"{sessionID}"}`
   - **Code**: Claude stream-json input format for a user turn is `{"type":"user","message":{"role":"user","content":"..."}}`. The `session_id` at the top level is not part of the Claude input protocol; its presence or absence and effect is unspecified.

9. **`cancel_job` sends `sessions.delete` but the NATS subject requires `projectId`** — The design says `cancel_job` "sends `sessions.delete` to stop the session first" if the job is running. The `sessions.delete` NATS subject is `mclaude.{userId}.{projectId}.api.sessions.delete` (agent.go:248-257). The MCP tool only has access to the `jobId`; to send the delete it needs both `userId` and `projectId`, which come from the `JobEntry`. But the design does not specify how the MCP tool gets `userId` (it is not a parameter and the tool description provides no auth context propagation mechanism).
   - **Doc**: "If job is `running`, sends `sessions.delete` to stop the session first." (Component Changes → mclaude-mcp → cancel_job)
   - **Code**: `sessPrefix + "delete"` subscription (agent.go:258) uses `mclaude.{userId}.{projectId}.api.sessions.delete`. The MCP tool has no specified mechanism to obtain `userId` from the HTTP request context.

10. **Daemon restart recovery checks `mclaude-sessions` KV for active sessions but the daemon does not have a session-agent KV handle** — On startup, the dispatcher is described as checking `mclaude-sessions` KV to find which sessions are still active, then resetting orphaned jobs. But the `Daemon` struct (daemon.go:46-53) only holds `laptopsKV` (the laptops bucket). `mclaude-sessions` is opened by the `Agent` (agent.go:66), not the `Daemon`. The design does not specify how the dispatcher goroutine inside `Daemon.Run()` obtains access to the session KV bucket, or whether `Daemon` should open it during `NewDaemon`.
    - **Doc**: "Jobs in `running` or `starting` with no active session in session KV → reset to `queued`." (Component Changes → Daemon → runJobDispatcher → daemon startup)
    - **Code**: `Daemon` struct (daemon.go:46) has no KV field for `mclaude-sessions`. `NewDaemon` (daemon.go:69) only opens `mclaude-laptops` KV.

## Run: 2026-04-14T00:02:00Z

<evaluation against current design doc content>

**Gaps found: 7**

1. **Dispatcher graceful stop via `sessions.input` requires `session_id` in payload but design does not specify it** — The daemon's `runJobDispatcher` sends the graceful stop message to `mclaude.{userId}.{job.ProjectID}.api.sessions.input`. The existing `handleInput` (agent.go:508) parses `session_id` from the top-level of the payload and uses it to look up the session. The design does not show the graceful stop payload including a `session_id` field. Without it, `handleInput` logs a warning and drops the message. The graceful stop format shown (line 316) explicitly states "no top-level `session_id` field" and routes directly to `s.stdinCh` — but that is the `QuotaMonitor`'s path. The daemon dispatcher path sends through `sessions.input` (line 124), which is a different path requiring `session_id`. These two code paths use different formats and the design does not reconcile them.
   - **Doc**: "sends graceful stop to `mclaude.{userId}.{job.ProjectID}.api.sessions.input`" (line 124); graceful stop JSON format "Note: no top-level `session_id` field — this is written directly to Claude's stdin, not routed via `handleInput`." (lines 316-318)
   - **Code**: `handleInput` (agent.go:508-526) requires `session_id` in payload or drops the message silently.

2. **`mclaude-server` has no NATS connection** — The design specifies that the mclaude-server (Swift/Hummingbird) opens a NATS connection to read/write the `mclaude-job-queue` KV bucket for the `/jobs` REST endpoints. But the existing `mclaude-server/Sources/main.swift` has no NATS dependency, no `NATS_URL` env var, and no NATS connection code anywhere in the Swift package. There is no NATS Swift client library in the Package.swift dependencies. An implementer must add a NATS Swift client dependency, wire up connection code, and integrate it with the existing async actor pattern — none of which is specified by the design.
   - **Doc**: "The server opens a NATS connection (using `NATS_URL` env var, same as other components) to read/write the `mclaude-job-queue` KV bucket." (Component Changes → mclaude-server)
   - **Code**: `mclaude-server/Sources/main.swift` — no NATS import, no NATS URL, no NATS connection. `Package.swift` has no NATS dependency.

3. **`/schedule-feature` skill matches CWD against `list_projects` `cwd` field, but `list_projects` returns `path` not `cwd`** — The skill says "Call `mcp__mclaude__list_projects` and matches the current working directory (`cwd` field) to find the `projectId`." But the actual `list_projects` tool returns `ProjectInfo` objects with `name` and `path` fields (Models.swift:51-54). There is no `cwd` field on `ProjectInfo`. The skill cannot perform the described match.
   - **Doc**: "matches the current working directory (`cwd` field) to find the `projectId`" (Component Changes → /schedule-feature)
   - **Code**: `ProjectInfo` (mclaude-server/Sources/Models.swift:51) has `name: String` and `path: String`. No `cwd` field exists.

4. **`publishLifecycleExtra` does not exist** — The `QuotaMonitor` struct definition includes `publishLifec func(sessionID, evType string, extra map[string]string) // wraps a.publishLifecycleExtra`. But `agent.go` only defines `publishLifecycle(sessionID, eventType string)` and `publishLifecycleFailed(sessionID, errMsg string)`. There is no `publishLifecycleExtra` method on `Agent`. The new lifecycle events (`session_quota_interrupted`, `session_job_complete`, `session_permission_denied`, `session_job_paused`) all require extra fields beyond `type`/`sessionId`/`ts`, so the existing `publishLifecycle` signature cannot be used as-is. The implementer has no specified signature to follow.
   - **Doc**: `publishLifec func(sessionID, evType string, extra map[string]string) // wraps a.publishLifecycleExtra` (Component Changes → quota_monitor.go → QuotaMonitor struct)
   - **Code**: `agent.go:684` defines `publishLifecycle(sessionID, eventType string)` only. No `publishLifecycleExtra` exists anywhere in the session-agent package.

5. **`QuotaMonitor` subscribes to `mclaude.{userId}.quota` but no subscription subject or NATS connection ownership is specified** — The goroutine's select loop includes `case msg := <-quotaSub.Channel()`, implying `quotaSub` is a NATS subscription. But neither `newQuotaMonitor` nor the `QuotaMonitor` struct includes a subscription handle, NATS connection reference, or the mechanism by which `quotaSub` is created. The `QuotaMonitor` has `nc *nats.Conn` but the design never specifies where the subscription is set up (in `newQuotaMonitor`? in the goroutine before the loop?), what the exact subject is (`mclaude.{userId}.quota` with a literal `.`-separated subject?), or how the subscription is cleaned up when the monitor exits.
   - **Doc**: `case msg := <-quotaSub.Channel():` (Component Changes → quota_monitor.go → goroutine behavior); `nc *nats.Conn` in QuotaMonitor struct
   - **Code**: No existing pattern for NATS subscriptions within `QuotaMonitor` goroutines in the session-agent package.

6. **Dispatcher cannot determine `job.SessionID` for the `starting` → `queued` recovery path** — On startup, the dispatcher checks jobs in `running` or `starting` state by looking up `job.SessionID` in `d.sessKV`. But the design states the dispatcher only sets `SessionID` when it updates the job to `running` (line 120: "Updates job status to `running`, sets `SessionID`"). Jobs in `starting` status (sent `sessions.create` but not yet confirmed `idle`) would have no `SessionID` yet in `JobEntry`. The recovery logic says "look up `job.SessionID` in `d.sessKV`. If the session no longer exists → reset job to `queued`" — but if `SessionID` is empty (job never reached `running`), the lookup would use an empty key, which is undefined behavior. The design does not specify what to do when `SessionID` is empty on a `starting` job.
   - **Doc**: "Jobs in `running` or `starting` state: look up `job.SessionID` in `d.sessKV`. If the session no longer exists → reset job to `queued`." (Daemon → runJobDispatcher → daemon startup); "Updates job status to `running`, sets `SessionID` and `StartedAt`" (line 120)
   - **Code**: `JobEntry.SessionID` is described as "populated when status=running". No specification for what value it holds when status is `starting`.

7. **`DELETE /jobs/{id}` sends `sessions.delete` via NATS but mclaude-server has no NATS connection** — This is a specific consequence of gap 2: the `DELETE /jobs/{id}` handler is specified to publish `sessions.delete` to NATS (`mclaude.{userId}.{projectId}.api.sessions.delete`) when the job's status is `running`. This requires the mclaude-server Swift process to have an active NATS connection and know how to construct the correct subject. Since mclaude-server has no NATS capability, this path is entirely unimplementable without first resolving gap 2 — but it additionally requires the design to specify how the Swift process constructs the NATS subject (userId from JWT, projectId from the JobEntry in KV).
   - **Doc**: "If the entry's `status == 'running'`, the server also sends `sessions.delete` to NATS (`mclaude.{userId}.{projectId}.api.sessions.delete`) before deleting." (Component Changes → mclaude-server → DELETE /jobs/{id})
   - **Code**: No NATS client in mclaude-server. `sessions.delete` subject requires `mclaude.{userId}.{projectId}.api.sessions.delete` (agent.go:248).

## Run: 2026-04-14T00:03:00Z

**Gaps found: 2**

1. **Initial dev-harness prompt sent via `sessions.input` is missing `session_id` in the payload specification** — The dispatcher sends the dev-harness prompt to `mclaude.{userId}.{job.ProjectID}.api.sessions.input` (line 119), but `handleInput` (agent.go:508-514) requires the payload to contain a top-level `session_id` field and silently drops the message if it is absent. The design specifies the `session_id` field for the dispatcher's graceful stop message (line 124) but never specifies that the initial dev-harness prompt payload must also include `session_id`. The dispatcher has the session ID from the `sessions.create` reply. Without this field in the prompt payload, `handleInput` logs "sessions.input: missing session_id" and discards it; the session never receives the dev-harness prompt.
   - **Doc**: "Sends the dev-harness prompt to `mclaude.{userId}.{job.ProjectID}.api.sessions.input` (fire-and-forget, no reply)." (Component Changes → Daemon → runJobDispatcher, line 119). The Scheduled Session Prompt section (lines 153-176) shows only the message content, not a wrapper JSON structure with `session_id`.
   - **Code**: `handleInput` (agent.go:508-514) parses `json:"session_id"` and returns early if empty. The graceful stop path (line 124) correctly includes `session_id` in the wrapper, but the initial prompt has no specified wrapper at all.

2. **`session_job_paused` lifecycle event has no specified publisher** — The data model defines the `session_job_paused` event payload (lines 614-624) with `type`, `sessionId`, `priority`, `u5`, `jobId`, and `ts` fields. However, no component in the design is specified to publish this event. `publishExitLifecycle()` in `QuotaMonitor` only publishes `session_job_complete` or `session_quota_interrupted` (lines 359-361). The dispatcher's quota-threshold path (lines 121-125) updates the job to `paused` but specifies no lifecycle event. Any consumer of the lifecycle subject that watches for `session_job_paused` will never receive it, and the implementer has no specification for which code path should call `publishLifecycleExtra` with this event type.
   - **Doc**: `session_job_paused` payload defined (Data Model → New Lifecycle Event Payloads, lines 614-624). `publishExitLifecycle()` logic (lines 358-361) lists only two publish cases. Dispatcher quota-exceeded path (lines 121-125) says "updates job to `paused`" with no mention of publishing `session_job_paused`.
   - **Code**: No `session_job_paused` string appears anywhere in the session-agent codebase. `publishLifecycle` and `publishLifecycleFailed` are the only existing lifecycle publish methods (agent.go:684-707).

## Run: 2026-04-14T04:00:00Z

**Gaps found: 1**

1. **No component is specified to write job completion state transitions back to `mclaude-job-queue` KV after a session exits** — The user flow (line 53) says "Monitor publishes `session_job_complete` lifecycle event. Dispatcher marks job `completed`." Lines 7e-f say the monitor "marks job `paused`" or "marks job `queued`" after a quota-interrupted exit. But neither component has a specified mechanism to perform these writes. The `QuotaMonitor` struct (`quota_monitor.go`) has no KV handle — it holds `nc *nats.Conn`, `session *Session`, `cfg QuotaMonitorConfig`, `publishLifec`, and channel fields, but no `jobQueueKV jetstream.KeyValue`. The `runJobDispatcher` spec only describes four triggers (queued→start, threshold→pause, recovery→restart, startup→recover). No trigger is defined for "session exits naturally → read outcome from monitor, write completed/paused/queued/failed to KV." The `publishExitLifecycle` pseudocode (lines 358-362) calls `publishLifecycleExtra` for NATS events but specifies no KV write. An implementer has no specified path for these transitions and cannot know whether to: (a) add a KV handle to `QuotaMonitor` so it writes directly, (b) add a lifecycle subscription to `runJobDispatcher`, (c) wire a callback from `QuotaMonitor` to the dispatcher, or (d) some other mechanism.
   - **Doc**: "Monitor publishes `session_job_complete` lifecycle event. Dispatcher marks job `completed`." (User Flow, line 53). "Marks job `paused` and sets `resumeAt`" / "marks job `queued`" (User Flow, lines 60-61). `publishExitLifecycle()` logic (lines 358-362). `runJobDispatcher` spec (lines 110-134) — no lifecycle subscription described.
   - **Code**: `QuotaMonitor` struct fields (doc lines 293-307) — no KV handle. `runJobDispatcher` (daemon.go) — no existing lifecycle NATS subscription pattern in daemon.go. `d.jobQueueKV` is the only KV reference in `Daemon`; no code specifies who calls `d.jobQueueKV.Put(...)` to write `status=completed`.

## Run: 2026-04-14T05:00:00Z

**Gaps found: 1**

1. **PR failure leaves job permanently `running` — no specified code path to set `status=failed`** — The error handling table says: "PR creation fails (session outputs error) → Job → `failed` with error text from assistant output." But `publishExitLifecycle()` (lines 375-378) emits nothing when `m.completionPR == ""` and `stopInProgress == false` — i.e., a natural session exit where Claude ran, PR creation failed, and no quota stop was in progress. Without a lifecycle event, `runLifecycleSubscriber` receives nothing, and the job stays `status=running` forever. There is no specified 4th lifecycle event for "session exited without completion marker," no timeout on a `running` job in `runJobDispatcher`, and no fallback KV write anywhere. An implementer reading only the component specs cannot produce a working failure path.
   - **Doc**: "PR creation fails (session outputs error) | `m.completionPR` is never set; `SESSION_JOB_COMPLETE` marker not detected. On session exit: monitor publishes no `session_job_complete`. Job → `failed` with error text from assistant output." (Error Handling table, line 652). `publishExitLifecycle()` logic (lines 375-378): "Else: publishes no new event." `runLifecycleSubscriber` event table (lines 115-120): handles only `session_job_complete`, `session_quota_interrupted`, `session_permission_denied`, `session_job_paused` — none maps to "natural exit with no PR."
   - **Code**: No existing `session_failed` or equivalent lifecycle event consumed by a daemon KV writer. `publishLifecycleFailed` (agent.go:698) emits `session_failed` on session start failure only, not session exit.

## Run: 2026-04-14T06:00:00Z

**Gaps found: 1**

1. **Permission-denied path: `publishExitLifecycle` overwrites `needs_spec_fix` status with `paused`** — When a session uses the `strict-allowlist` policy and an out-of-allowlist tool is requested: (a) `a.publishPermDenied` immediately publishes `session_permission_denied`, which causes `runLifecycleSubscriber` to set `status=needs_spec_fix`; (b) `monitor.signalPermDenied` sets `stopInProgress = true` and sends the graceful stop. The `QuotaMonitor` goroutine continues running with `stopInProgress = true`. When the session eventually exits (either gracefully within 30 min, or forcibly via `sendHardInterrupt`), `session.doneCh` closes and `publishExitLifecycle()` runs — finding `m.completionPR == ""` and `stopInProgress == true`, it publishes `session_quota_interrupted`. The `runLifecycleSubscriber` receives `session_quota_interrupted` and overwrites the job's status: if `autoContinue`, sets `status=paused`; else sets `status=queued`. This destroys the `needs_spec_fix` status that was set earlier. An implementer following the design as written produces broken behavior for the permission-denied path — the job either auto-restarts (undoing the `needs_spec_fix` protection) or is reset to `queued` for manual restart, neither of which is the intended outcome. The design does not specify that `publishExitLifecycle` should check whether a `session_permission_denied` event was already published, nor does it specify a `permDeniedFired bool` flag on the monitor to suppress the exit lifecycle event in this case.
   - **Doc**: `publishExitLifecycle()` logic (lines 376-379): "Else if `stopInProgress`: publishes `session_quota_interrupted`." Goroutine behavior (lines 346-350): `permDeniedCh` sets `stopInProgress = true`. `runLifecycleSubscriber` table (lines 118-119): `session_quota_interrupted` → set `status=paused` or `queued`; `session_permission_denied` → set `status=needs_spec_fix`. No ordering or deduplication rule specified.
   - **Code**: No existing `QuotaMonitor` implementation — but the design's pseudocode unambiguously causes both events to fire sequentially for any permission-denied path, with the second event overwriting the first KV write.

## Run: 2026-04-14T07:00:00Z

**Gaps found: 1**

1. **`DELETE /jobs/{id}` behavior is contradictory — KV delete vs `cancelled` status** — The data model defines a `cancelled` status ("`cancelled` — user-cancelled via /job-queue cancel", Data Model section) and the error handling table says for `needs_spec_fix` jobs: "User updates spec, cancels with `/job-queue cancel`, re-queues with `/schedule-feature`" — implying the cancelled entry remains queryable so the user can inspect it. But the `DELETE /jobs/{id}` handler specification says "deleting the KV entry" (Daemon Jobs HTTP Server section), which removes the entry entirely. An implementer cannot determine which behavior to implement: if the entry is deleted, it disappears from `GET /jobs` responses; if status is set to `cancelled`, it remains visible. These produce different observable behaviors for `/job-queue list` and `/job-queue status`, and the design never reconciles the contradiction between defining the `cancelled` status and specifying entry deletion.
   - **Doc**: Data Model — `cancelled — user-cancelled via /job-queue cancel` (JobEntry Status values); `DELETE /jobs/{id}`: "reads the entry from `d.jobQueueKV`... before deleting the KV entry" (Daemon Jobs HTTP Server section).
   - **Code**: No existing `mclaude-job-queue` bucket — but the choice between `KV.Delete(key)` and `KV.Put(key, updatedEntry{status:"cancelled"})` determines whether `GET /jobs` ever returns the entry again.

## Run: 2026-04-14T08:00:00Z

**Gaps found: 2**

1. **`QuotaMonitor` struct has no fields to store last-known `u5`, `r5`, or `branch` — required by `publishExitLifecycle()`** — `publishExitLifecycle()` is described as publishing `session_quota_interrupted` "with last known `u5` and `r5`" (line 380), and `session_job_complete` with `prUrl` (line 379). The `session_quota_interrupted` payload (lines 599-610) requires `u5` and `r5` fields. The `session_job_complete` payload (lines 623-633) requires a `branch` field. But the `QuotaMonitor` struct (lines 310-323) has no fields to hold the last-observed `u5` (int) or `r5` (time.Time) from quota messages, and no `branch` field to populate the completion event. The select loop parses quota messages on each receipt but has no place to store the values for later use. When `session.doneCh` fires, `publishExitLifecycle()` cannot access these values. An implementer has no specified field names, types, or initialization path for this state.
   - **Doc**: `publishExitLifecycle()` description (line 380); `session_quota_interrupted` payload spec (lines 599-610) with `u5` and `r5` fields; `session_job_complete` payload spec (lines 623-633) with `branch` field. `QuotaMonitor` struct definition (lines 310-323).
   - **Code**: `QuotaMonitor` struct fields: `sessionID`, `userID`, `projectID`, `cfg`, `nc`, `session`, `publishLifec`, `permDeniedCh`, `quotaCh`, `quotaSub`, `completionPR`, `stopCh`. No `lastU5`, `lastR5`, `branch`, or equivalent fields.

2. **`runJobDispatcher` prompt payload references `{job.SessionID}` before it is set** — Line 136 specifies the dev-harness prompt payload as `{"type":"user","message":{"role":"user","content":"{prompt}"},"session_id":"{job.SessionID}"}`. But `job.SessionID` is not populated in the `JobEntry` until line 137 ("Updates job status to `running`, sets `SessionID` and `StartedAt`, writes back"). The session ID is returned in the `sessions.create` NATS reply (`{"id": sessionID}`), not read from `job.SessionID`. An implementer reading the design literally would attempt to read `job.SessionID` before writing it, getting an empty string. The design needs to specify that `session_id` in the prompt payload comes from the `sessions.create` reply, not from the `JobEntry.SessionID` field.
   - **Doc**: "Sends the dev-harness prompt to `mclaude.{userId}.{job.ProjectID}.api.sessions.input` ... The payload must include a top-level `session_id` field ... `{"type":"user","message":{"role":"user","content":"{prompt}"},"session_id":"{job.SessionID}"}`." (line 136). "Updates job status to `running`, sets `SessionID` and `StartedAt`" (line 137).
   - **Code**: `handleCreate` returns `{"id": sessionID}` as the NATS reply (agent.go:432). `JobEntry.SessionID` is set by the dispatcher only after the prompt is sent.

## Run: 2026-04-14T09:00:00Z

**Gaps found: 3**

1. **`JobEntry.Branch` has no specified creator** — The data model defines `Branch string` with the comment `"schedule/{slug}-{shortId}"` (Data Model → JobEntry). The user flow (step 4) says the dispatcher "Creates a git worktree branch `schedule/spa-{shortId}`", implying the dispatcher generates the branch name. But the `runJobDispatcher` component spec has no step for generating or writing `Branch` to the `JobEntry`. The `POST /jobs` handler spec says it only sets `id`, `status`, and `createdAt` — it does not generate a branch name. The dispatcher spec only says it passes `branch` in `sessions.create`, but never says it generates the name, stores it in `JobEntry.Branch`, or at what point the field gets written. An implementer cannot determine: (a) who generates the `schedule/{slug}-{shortId}` string, (b) whether it is written at job creation time or job start time, (c) what `{slug}` is derived from (the spec path?), and (d) what `{shortId}` is (first 8 chars of the job UUID?).
   - **Doc**: `Branch string `json:"branch"` // "schedule/{slug}-{shortId}"` (Data Model → JobEntry, line 574). User flow step 4: "Creates a git worktree branch `schedule/spa-{shortId}` on the target project." `runJobDispatcher` spec (lines 127-152) — no branch generation or `JobEntry.Branch` write step.
   - **Code**: `POST /jobs` handler spec sets only `id`, `status`, `createdAt` (line 460). `runJobDispatcher` queued-entry path (lines 131-138) has no branch generation step.

2. **User flow says daemon creates the git worktree but component spec doesn't, and session-agent already does it** — User flow step 4 says the daemon "Creates a git worktree branch `schedule/spa-{shortId}` on the target project" before sending `sessions.create`. But the `runJobDispatcher` component spec (lines 131-138) has no worktree creation step — the dispatcher only sends `sessions.create` with a `branch` field. The existing `handleCreate` in the session-agent already creates the git worktree for the specified branch (agent.go:337-346). A developer following the user flow would implement worktree creation in the daemon, then the session-agent would attempt to create the same worktree and return a "worktree already in use" error (agent.go:330-334). The design must resolve whether (a) worktree creation is daemon-side only and `handleCreate` should be modified to skip it, or (b) worktree creation is session-agent-side only (existing behavior) and the user flow description is wrong.
   - **Doc**: User flow step 4: "Creates a git worktree branch `schedule/spa-{shortId}` on the target project." (line 42). `runJobDispatcher` queued-entry path (lines 131-138): no worktree creation step.
   - **Code**: `handleCreate` (agent.go:336-346) creates the worktree unconditionally when `!collision`. There is no worktree creation code in daemon.go.

3. **mclaude-server auth mechanism misdescribed as JWT claims extraction** — The design specifies that the mclaude-server proxy routes for `/jobs` "handle JWT auth (existing middleware), extracts `userId` from JWT claims" (line 466-468). But the mclaude-server has no JWT parsing or claims extraction. The existing `requireAuth` helper (APIServer.swift:120-127) calls `store.authenticate(token: token)` which performs an opaque session token lookup against an in-memory dictionary (`sessionTokens: [String: String]`). The server issues its own random session tokens after Google OAuth, not JWTs. There is no JWT library in the Swift package, no `.claims` accessor, no signing key. An implementer following the design would look for JWT middleware that does not exist and cannot know they should instead call `requireAuth` (the existing function) to get `userId`.
   - **Doc**: "handles JWT auth (existing middleware), extracts `userId` from JWT claims" (Component Changes → mclaude-server, line 466).
   - **Code**: `requireAuth` (APIServer.swift:120-127) uses `store.authenticate(token: token)` → opaque token lookup. `SessionStore.authenticate` (SessionStore.swift:169) looks up `sessionTokens[token]`, not JWT claims. No JWT import anywhere in mclaude-server Swift sources.

## Run: 2026-04-14T10:00:00Z

**Gaps found: 1**

1. **`starting` status is never written by any specified step — startup recovery clause is dead code and double-start race is unprotected** — `JobEntry.Status` defines `"starting — sessions.create sent; waiting for session idle"` (Data Model). The startup recovery logic (Daemon → `runJobDispatcher` → daemon startup, line 151) handles jobs in `starting` state. But no step in `runJobDispatcher` ever writes `status=starting`. Line 133 writes `job.Branch` to KV with `status=queued` still set. Line 139 writes `status=running`. There is no specified transition to `starting`. A developer implementing the dispatcher cannot know: (a) whether to write `status=starting` before sending `sessions.create`, and (b) whether to do so in the same KV write as line 133 (when `Branch` is set). Without this write, the startup recovery clause for `starting` jobs never fires, and if the daemon crashes during the 30s session-idle poll window the job stays `queued` and will be retried — which can cause `RetryCount` to increment spuriously and ultimately reach 3 (the `failed` threshold) without three actual start failures.
   - **Doc**: `JobEntry.Status` values include `"starting — sessions.create sent; waiting for session idle"` (Data Model, line 567). Startup recovery (line 151): "Jobs in `starting` state: `SessionID` is empty... Reset directly to `queued` without any KV lookup." `runJobDispatcher` queued-entry path (lines 131-139): writes `Branch` at line 133, writes `status=running` at line 139 — no step writes `status=starting`.
   - **Code**: `daemon.go` — no existing `starting` status write anywhere. The `RetryCount` increment is specified only for "session fails to start (30s KV poll timeout)" (Error Handling table, line 666): "Dispatcher resets job to `queued`, increments `RetryCount`."

## Run: 2026-04-14T11:00:00Z

**Gaps found: 2**

1. **`list_projects` response has no `projectId` field — `/schedule-feature` cannot find a UUID projectId** — The `/schedule-feature` skill (step 2) calls `mcp__mclaude__list_projects` and "matches the current working directory against the `path` field of each `ProjectInfo` entry to find the `projectId`." But the `list_projects` tool calls `GET /projects` on mclaude-server, which returns `ProjectInfo` objects with only two fields: `name: String` (directory name, e.g. `"mclaude"`) and `path: String` (absolute filesystem path, e.g. `"~/mclaude"`). There is no `projectId` UUID field on `ProjectInfo`. The `projectId` UUID is the one stored in the control-plane database and used in NATS subjects (`mclaude.{userId}.{projectId}.api.sessions.create`). Matching CWD against `path` produces the directory name, not the UUID. An implementer following the skill spec cannot obtain the `projectId` to pass to `create_job`.
   - **Doc**: `/schedule-feature` skill behavior step 2 (doc line 524): "Match the current working directory against the `path` field of each `ProjectInfo` entry to find the `projectId`."
   - **Code**: `ProjectInfo` (mclaude-server/Sources/Models.swift:51-54) has `name: String` and `path: String` only. `GET /projects` handler (APIServer.swift:262-282) builds `ProjectInfo(name: entry, path: fullPath)` — no UUID anywhere in the response.

2. **`onRawOutput` and `onStrictDeny` callbacks are set after `sess.start()` but read in an already-running goroutine without mutex protection — data race** — The design says "The Agent sets both callbacks in `handleCreate` after calling `sess.start()`" (doc line 240). But `sess.start()` (session.go:129) spawns the stdout router goroutine immediately, which reads `s.onRawOutput` (as shown in doc lines 233-238: `if notify := s.onRawOutput; notify != nil { notify(evType, lineCopy) }`). If the goroutine runs before the main goroutine sets the callback, it observes nil — and more critically, the concurrent read and write of an unprotected struct field is a data race. The existing `onEventPublished` field avoids this by (a) being set before `start()` is called (agent.go:382 before agent.go:407) and (b) reading under `s.mu.Lock()` in the goroutine (session.go:219-224). The design specifies neither of these safeguards for the new callbacks. An implementer following the design literally would write a Go data race.
   - **Doc**: "The Agent sets both callbacks in `handleCreate` after calling `sess.start()`" (doc line 240). Goroutine placement for `onRawOutput` invocation (doc lines 233-238): no mutex shown.
   - **Code**: Existing `onEventPublished` set before `start()` at agent.go:382 (before agent.go:407). Read under `s.mu.Lock()` at session.go:219-224. The doc's proposed pattern for `onRawOutput`/`onStrictDeny` is inconsistent with both safeguards.

## Run: 2026-04-14T12:00:00Z

**Gaps found: 3**

1. **`DELETE /jobs/{id}` NATS publish has no specified payload format** — The handler specifies that when `status == "running"`, the daemon publishes a `sessions.delete` NATS message to `mclaude.{userId}.{job.ProjectID}.api.sessions.delete`. But the payload for this message is never specified. `handleDelete` (agent.go:438-442) unmarshals the payload as `{sessionId: string}` and returns "invalid request: missing sessionId" if the field is absent — silently dropping the delete. The daemon has `job.SessionID` from the `JobEntry`, so the correct payload is inferable, but the design never says so.
   - **Doc**: "`DELETE /jobs/{id}`: reads the entry from `d.jobQueueKV`. If `status == 'running'`, publishes a `sessions.delete` NATS message to `mclaude.{userId}.{job.ProjectID}.api.sessions.delete` before updating state." (Daemon Jobs HTTP Server → DELETE /jobs/{id}, line 470)
   - **Code**: `handleDelete` (agent.go:438-442) requires `{sessionId: string}` in the NATS payload or it returns an error and does nothing.

2. **`mclaude-sessions` KV key format never specified — dispatcher cannot poll for session idle state** — The dispatcher spec says "Polls `d.sessKV` for the new session to reach state `idle` (up to 30s, 500ms intervals)." A developer implementing this must look up the session by KV key. The KV key format is `{userId}.{projectId}.{sessionId}` (state.go:62-64), but this is not documented anywhere in the design document. The daemon has all three values (userId from cfg, projectId from job.ProjectID, sessionId from the sessions.create NATS reply), but without knowing the key format an implementer cannot write the poll loop.
   - **Doc**: "Polls `d.sessKV` for the new session to reach state `idle` (up to 30s, 500ms intervals)." (Daemon → runJobDispatcher, line 137); `sessKV jetstream.KeyValue // mclaude-sessions — read-only for startup recovery` (new Daemon struct fields)
   - **Code**: `sessionKVKey(userID, projectID, sessionID string) string` (state.go:62-64) returns `{userId}.{projectId}.{sessionId}`. Not referenced in the design document.

3. **`RetryCount` increment and 3-failure cutoff have no implementation path in `runJobDispatcher`** — The error handling table specifies: "Session fails to start (30s KV poll timeout) — Dispatcher resets job to `queued`, increments `RetryCount`. After 3 failures: marks `failed` with error message." But the `runJobDispatcher` component spec (lines 127-152) has no step for: (a) incrementing `RetryCount` when the 30s poll times out, (b) reading `RetryCount` before re-queuing, (c) checking if `RetryCount >= 3`, or (d) writing `status=failed` when the threshold is reached. A developer reading only the component spec implements a dispatcher that retries indefinitely. The behavior is defined only in the error table, which provides no code-level location or trigger for these writes.
   - **Doc**: "Session fails to start (30s KV poll timeout) | Dispatcher resets job to `queued`, increments `RetryCount`. After 3 failures: marks `failed` with error message." (Error Handling table). `runJobDispatcher` spec (lines 127-152): no RetryCount logic.
   - **Code**: `JobEntry.RetryCount int` defined in the data model but no code path writes to it in the dispatcher spec.

## Run: 2026-04-14T13:00:00Z

**Gaps found: 2**

1. **`GET /jobs/projects` handler reads `mclaude-projects` KV but Daemon has no handle for it** — The `GET /jobs/projects` daemon handler description says "reads all entries from `mclaude-projects` KV with key prefix `{userId}.`" The new Daemon struct fields specify only `sessKV jetstream.KeyValue // mclaude-sessions` and `jobQueueKV jetstream.KeyValue // mclaude-job-queue`. Neither is the `mclaude-projects` bucket. The existing `Daemon.NewDaemon()` only opens `mclaude-laptops`. An implementer cannot read the `mclaude-projects` bucket without a third handle — but neither the Daemon struct nor `NewDaemon()` is specified to include one. Whether to add a `projKV jetstream.KeyValue` field (opened in `NewDaemon` the same as the others), open an ad-hoc KV connection inside the handler, or reuse an existing handle is not stated anywhere in the design.
   - **Doc**: "`GET /jobs/projects`: reads all entries from `mclaude-projects` KV with key prefix `{userId}.`" (Daemon Jobs HTTP Server → runJobsHTTP). New Daemon struct fields (Component Changes → Daemon → New fields on Daemon struct): `sessKV` and `jobQueueKV` only.
   - **Code**: `NewDaemon` (daemon.go:69-88) opens only `mclaude-laptops` KV. `Daemon` struct (daemon.go:46-53) has only `laptopsKV`. The new fields add `sessKV` and `jobQueueKV` but not a `mclaude-projects` handle.

2. **`autoContinue=false` after quota interruption is described as "next manual start" but the specified `status=queued` means automatic restart** — The lifecycle subscriber table specifies that `session_quota_interrupted` with `autoContinue=false` sets `status=queued`. The dispatcher's "queued entry with u5 < threshold" branch picks up all queued jobs automatically — there is no distinction between a manually queued job and one set to `queued` by the lifecycle subscriber. The user flow (step 7e) describes this path as "sets `status=queued` for next manual start," but a developer implementing the dispatcher as specified produces automatic restart when quota drops below threshold. An implementer must decide: (a) add a new terminal status (e.g. `quota_stopped`) that the dispatcher never auto-starts, (b) add a `ManualOnly` flag to `JobEntry` checked by the dispatcher, or (c) accept that non-autoContinue jobs also auto-restart — none of which is specified.
   - **Doc**: User flow step 7e: "If not `autoContinue`: sets `status=queued` for next manual start." (line 60). Lifecycle subscriber table: "`session_quota_interrupted` | ... Else: set `status=queued`. Write back." `runJobDispatcher` queued-entry branch: "On **new or updated entry** with status `queued` AND latest quota `u5 < threshold`: [starts the session]." No `autoContinue` check in the dispatcher's queued-entry path.
   - **Code**: `JobEntry` has no `ManualOnly` or equivalent flag. The dispatcher branch condition is `status=queued AND u5 < threshold` only — no other discriminator is specified.

## Run: 2026-04-14T14:00:00Z

CLEAN — no blocking gaps found.

All gaps from prior rounds have been resolved in the current document. Verifications against codebase:

- `Daemon` struct now specifies `sessKV`, `jobQueueKV`, `projectsKV` — all opened in `NewDaemon()` using the correct `jetstream.KeyValue` API (matching daemon.go pattern).
- `ensureJobQueueKV` correctly uses `nats.JetStreamContext` / `nats.KeyValueConfig` (matching control-plane/projects.go:146-156).
- `publishLifecycleExtra` and `publishPermDenied` are now fully defined with signatures, subjects, and payload shapes.
- `onRawOutput` and `onStrictDeny` callbacks are set before `sess.start()` and read under `s.mu.Lock()` — matching the existing `onEventPublished` pattern in session.go:219-224.
- `QuotaMonitor` struct now has `lastU5 int`, `lastR5 time.Time`, `completionPR string`, `quotaSub *nats.Subscription`.
- `newQuotaMonitor` subscription setup is fully specified with `nc.ChanSubscribe` and cleanup on exit.
- `publishExitLifecycle()` has a 4th `else` branch that publishes `session_job_failed` — resolving the "no failure path" gap.
- `runLifecycleSubscriber` handles all five event types including `session_job_failed`.
- `session_job_paused` is now published by the daemon dispatcher in the quota-threshold path (line 146).
- `DELETE /jobs/{id}` payload specified as `{"sessionId":"{job.SessionID}"}`, matching `handleDelete` (agent.go:438-442).
- `status=starting` is now explicitly written in the dispatcher before `sessions.create` (line 134).
- `RetryCount` increment and 3-failure cutoff are now in the dispatcher spec (line 139).
- Prompt payload includes `session_id` from the `sessions.create` reply, not from `job.SessionID` (line 140).
- `GET /jobs/projects` reads from `d.projectsKV` — which is now a specified field on Daemon.
- `autoContinue=false` → `status=queued` is explicitly described as automatic restart (line 60), resolving the contradiction.
- `requireAuth(request:)` (APIServer.swift:120-127) is now cited correctly by name, removing the JWT misdescription.
- `/schedule-feature` calls `GET /jobs/projects` and matches `basename(CWD) == project.name`, avoiding the non-existent `cwd` field issue.
- `session_permission_denied` exits `publishExitLifecycle()` without publishing a second event (line 395) — resolving the `needs_spec_fix` overwrite issue.
- `Branch` field generation is now specified in the dispatcher at line 134 (slug + shortId derivation).

### Result

**CLEAN** after 14 rounds, 30 total gaps resolved (30 factual fixes, 0 design decisions).

## Run: 2026-04-16T00:00:00Z

CLEAN — no blocking gaps found.

All design claims verified against the implementation:

- `Daemon` struct fields (`sessKV`, `jobQueueKV`, `projectsKV`, `quotaCh`) — present in `daemon.go:47-58`
- `DaemonConfig.CredentialsPath` — present in `daemon.go:43`
- `runQuotaPublisher`, `runLifecycleSubscriber`, `runJobDispatcher`, `runJobsHTTP` goroutines — all started in `Daemon.Run()` at `daemon.go:131-134`
- `mclaude-job-queue` KV bucket creation — in `control-plane/projects.go:164-174` using `nats.JetStreamContext` (correct API)
- `JobEntry` schema — matches `state.go:136-155`
- `QuotaStatus` schema — matches `state.go:115-123`
- `QuotaMonitorConfig` — matches `state.go:126-132`
- `PermissionPolicyStrictAllowlist` — defined in `state.go:22`, implemented in `session.go:333-358`
- `Session.onStrictDeny` and `Session.onRawOutput` callbacks — present in `session.go:57-61`
- `QuotaMonitor` struct and goroutine — fully implemented in `quota_monitor.go`
- `Agent.publishLifecycleExtra` and `Agent.publishPermDenied` — present in `agent.go:1074-1099`
- `handleCreate` wires quota monitor before `sess.start()` — `agent.go:688-706`
- `handleJobsProjects` reads `ProjectState.Name` field — `ProjectKVState` (control-plane) and `ProjectState` (daemon) share the `"name"` JSON key; unmarshaling is safe
- `/schedule-feature` and `/job-queue` skill files — exist at `.agent/skills/schedule-feature/SKILL.md` and `.agent/skills/job-queue/SKILL.md` with complete algorithms
- HTTP endpoints (`POST /jobs`, `GET /jobs`, `GET /jobs/{id}`, `DELETE /jobs/{id}`, `GET /jobs/projects`) — all implemented in `daemon_jobs.go`
- `specPathToComponent` mapping table — implemented in `daemon_jobs.go:161-176`
- Startup recovery (`startupRecovery`) — implemented in `daemon_jobs.go:452-504`
- `sessionKVKey` format `{userId}.{projectId}.{sessionId}` — matches `state.go:64-66`

## Audit: 2026-04-16T04:20:00Z

**Document:** docs/plan-quota-aware-scheduling.md

### Round 1

CLEAN — no blocking gaps found.

### Result

**CLEAN** after 1 round, 0 gaps.
