## Run: 2026-05-01T00:00:00Z

**ADR:** `docs/adr-0044-scheduling-and-quota.md` (draft)
**Specs evaluated:**
- `docs/spec-state-schema.md`
- `docs/spec-nats-payload-schema.md`
- `docs/spec-nats-activity.md`
- `docs/mclaude-session-agent/spec-session-agent.md`

### Phase 1 — ADR → Spec (forward pass)

| # | ADR text | Spec location | Verdict | Direction | Notes |
|---|----------|---------------|---------|-----------|-------|
| 1 | "Scheduled sessions are regular sessions with optional quota fields on `sessions.create`. Session KV is the single source of truth." + "Eliminates `mclaude-job-queue-{uslug}` KV bucket" | spec-state-schema.md §mclaude-job-queue | GAP | SPEC→FIX | spec-state-schema.md still documents the `mclaude-job-queue` KV bucket in full (schema, writers, readers). ADR-0044 eliminates it entirely. The section should be removed or marked as superseded. |
| 2 | "Eliminates… job dispatcher goroutine, `localhost:8378` HTTP API" | spec-session-agent.md §HTTP (Daemon Only), §Daemon: Job Dispatcher | GAP | SPEC→FIX | spec-session-agent.md still documents `localhost:8378` HTTP API (POST/GET /jobs, DELETE /jobs/{id}, GET /jobs/projects) and the "Daemon: Job Dispatcher" section. Both are eliminated by ADR-0044. |
| 3 | "Caller supplies a free-text `prompt` field on `sessions.create`; agent sends it verbatim as the initial `sessions.input` after CLI startup." | spec-nats-payload-schema.md §sessions.create | REFLECTED | — | `prompt` field documented as optional on sessions.create with correct description. |
| 4 | "all quota fields are **top-level**, not nested under a `quotaMonitor` object — this ADR supersedes the nested shape" | spec-nats-payload-schema.md §sessions.create | REFLECTED | — | Payload schema shows quota fields (softThreshold, hardHeadroomTokens, etc.) as top-level fields. |
| 5 | "all quota fields are **top-level**, not nested under a `quotaMonitor` object" | spec-state-schema.md §NATS Subjects, sessions.create row | GAP | SPEC→FIX | The NATS Subjects table in spec-state-schema.md still lists `quotaMonitor` as a field in the sessions.create payload description: `{name, branch, cwd, joinWorktree, extraFlags, permPolicy, allowedTools, quotaMonitor, requestId}`. Should list the individual top-level quota fields instead. |
| 6 | "all quota fields are **top-level**, not nested under a `quotaMonitor` object" | spec-session-agent.md §Session Lifecycle > Creation step 8 | GAP | SPEC→FIX | Step 8 says "Wire quota monitor if `quotaMonitor` config is present in the request." Should reference `softThreshold > 0` as the trigger, per ADR-0044. |
| 7 | "Daemon goroutine polling every 60 seconds; publishes `QuotaStatus`… Only the **designated** agent runs the publisher — CP assigns one agent per user on registration." | spec-nats-activity.md §8e, §9i | REFLECTED | — | Section 8e and 9i describe quota publisher designation, CP assignment, re-designation on disconnect. |
| 8 | "CP designates exactly one agent per user as the quota publisher. The designation is delivered via a `quotaPublisher: true` field in the CP's response to `mclaude.hosts.{hslug}.api.agents.register`" | spec-nats-activity.md §7b | REFLECTED | — | Section 7b shows `quotaPublisher:true` in the register reply. |
| 9 | "Designated agent" quota publisher model (not daemon-only) | spec-session-agent.md §NATS Subjects (Publish), §Quota Monitoring | GAP | SPEC→FIX | spec-session-agent.md §NATS Subjects (Publish) still says "Quota status to `mclaude.users.{uslug}.quota` (daemon only)". ADR-0044 moves quota publishing to the designated agent (any agent, not just daemon). The "(daemon only)" qualifier should be removed and replaced with "(designated agent only)". §Quota Monitoring still describes the old daemon-centric model. |
| 10 | "`softThreshold` (% 5h utilization) per session. On breach, agent injects `MCLAUDE_STOP: quota_soft` via `sessions.input`" | spec-nats-activity.md §9g Soft pause | REFLECTED | — | Section 9g shows soft pause with MCLAUDE_STOP: quota_soft injection. |
| 11 | "`hardHeadroomTokens` per session. After soft marker injection, QuotaMonitor counts output tokens; at budget, sends `control_request` interrupt directly on `s.stdinCh`" | spec-nats-activity.md §9g Hard interrupt | REFLECTED | — | Section 9g hard interrupt path described correctly. |
| 12 | "When `hardHeadroomTokens` is 0, the hard interrupt fires immediately after the soft marker is injected" | spec-nats-payload-schema.md §sessions.create | REFLECTED | — | Field table says "0 = immediate hard stop after soft marker." |
| 13 | "Soft-stop and hard-stop both leave the Claude Code subprocess running. Subprocess idles on stdin with in-memory conversation intact." | spec-nats-activity.md §9g | REFLECTED | — | "CLI subprocess stays alive — conversation context intact in memory." |
| 14 | "Agent sends a new user message via `sessions.input`. Uses caller-supplied `resumePrompt` if provided; otherwise platform defaults (different for soft vs hard)." | spec-nats-activity.md §9g Resume, §9k | REFLECTED | — | Resume path with default nudges and custom resumePrompt documented in 9g and 9k. |
| 15 | "Soft-paused: `Resuming — continue where you left off.` Hard-paused: `Your previous turn was interrupted mid-response. Check git status and recover state before continuing.`" | spec-nats-activity.md §9g, §9k | REFLECTED | — | Both default nudges documented verbatim. |
| 16 | "No stdout-marker scanning. Completion happens when Claude ends a turn naturally and the Stop hook allows the stop." | spec-session-agent.md §Quota Monitoring | GAP | SPEC→FIX | spec-session-agent.md QuotaMonitor section (step 4) still says "Scans assistant events for the `SESSION_JOB_COMPLETE:{prUrl}` marker." ADR-0044 eliminates stdout-marker scanning entirely. QuotaMonitor detects completion via the `result` event + `stopReason == ""`. |
| 17 | "Stop hook mechanism: Claude Code's native `hooks.Stop`… Not a platform primitive." | spec-nats-activity.md §9g Completion | REFLECTED | — | Completion path references Stop hook allowing the stop. |
| 18 | "Agent injects `MCLAUDE_STOP: quota_soft` as a user message. Caller's hook reads `transcript_path`, scans last user message for marker" | spec-nats-activity.md §9g | REFLECTED | — | Soft pause shows MCLAUDE_STOP injection as a sessions.input message. |
| 19 | "QuotaMonitor detects turn-end via stream-json `result` event (`EventTypeResult`)" | spec-session-agent.md §Quota Monitoring | GAP | SPEC→FIX | QuotaMonitor section doesn't describe turn-end detection via `result` events. Still uses the old model (30-minute hard-interrupt timer, SESSION_JOB_COMPLETE marker). Should describe `onRawOutput` callback, `EventTypeResult` detection, `handleTurnEnd()` dispatch. |
| 20 | "All sessions with quota config start immediately. Each session independently monitors `u5` against its own `softThreshold`." | spec-nats-activity.md §9g Startup | REFLECTED | — | Section 9g shows immediate startup and per-session independent monitoring. |
| 21 | "`strict-allowlist` mode: auto-approve allowlisted tools, auto-deny everything else." | spec-session-agent.md §Permission Policy | REFLECTED | — | strict-allowlist policy documented with correct behavior. |
| 22 | "If `allowedTools` is empty on a `strict-allowlist` session, the agent rejects the `sessions.create` request and publishes a `lifecycle.error` event. There is no default allowlist" | spec-session-agent.md §Permission Policy | GAP | SPEC→FIX | spec-session-agent.md says "When no explicit allowed-tools list is provided for `allowlist` or `strict-allowlist`, a default set is applied: Read, Write, Edit, Glob, Grep, Bash, Agent, TaskCreate, TaskUpdate, TaskGet, TaskList, TaskOutput, and TaskStop." ADR-0044 eliminates this default — empty `allowedTools` with `strict-allowlist` is rejected. The spec must remove the default-set paragraph and add rejection behavior. |
| 23 | "If `allowedTools` is empty on `strict-allowlist` session, the agent rejects" | spec-nats-activity.md §9l | REFLECTED | — | Section 9l "Empty allowedTools rejection" describes the rejection correctly. |
| 24 | "Branch `schedule/{branchSlug}`. If worktree exists, session attaches to it — same slug = shared worktree." | spec-nats-activity.md §9g Startup | REFLECTED | — | "Ensures worktree schedule/refactor-auth exists." |
| 25 | "`autoContinue` flag per session. When set and paused on quota, agent resumes at 5h reset time." | spec-nats-activity.md §9g | REFLECTED | — | Resume path checks resumeAt set from r5 when autoContinue=true. |
| 26 | "`sessions.delete` — same as any session. Agent interrupts subprocess and reaps. Emits `session_job_cancelled` lifecycle event." | spec-nats-activity.md §9g Cancellation | REFLECTED | — | Cancellation path shows delete → interrupt → session_job_cancelled → KV tombstone. |
| 27 | "Agent persists `claudeSessionID` to session KV. On session loss, respawns with `claude --resume <claudeSessionID>`." | spec-nats-activity.md §9g | REFLECTED | — | Prompt delivery captures claudeSessionID; degraded fallback mentioned in ADR. |
| 28 | "In-process Go channel from `Session.onStrictDeny` to `QuotaMonitor.permDeniedCh`" | spec-session-agent.md §Permission Policy | PARTIAL | SPEC→FIX | spec-session-agent.md mentions `onStrictDeny` callback publishes lifecycle event and "signals the quota monitor" but does not describe the `permDeniedCh` channel or the QuotaMonitor's response (graceful stop, transition to needs_spec_fix). |
| 29 | "Session callbacks: `onStrictDeny` and `onRawOutput`" | spec-session-agent.md §Event Routing | PARTIAL | SPEC→FIX | `onRawOutput` callback is mentioned in step 6 of the stdout router ("Notifies the quota monitor raw output callback") but the callback itself (`onRawOutput func(evType string, raw []byte)`) is not specified as a Session field. `onStrictDeny` is mentioned in Permission Policy. Neither is documented as a formal Session callback interface. |
| 30 | "The existing `State` field is **renamed to `Status`**… its value set is extended with: `pending`, `paused`, `completed`, `cancelled`, `needs_spec_fix`" | spec-state-schema.md §mclaude-sessions | GAP | SPEC→FIX | spec-state-schema.md mclaude-sessions KV still uses `"state": "idle \| running \| requires_action \| updating \| restarting \| failed \| plan_mode \| waiting_for_input \| unknown"`. Missing the `state`→`status` rename and the new status values (pending, paused, completed, cancelled, needs_spec_fix, stopped, error). |
| 31 | Session KV new fields: `softThreshold`, `hardHeadroomTokens`, `autoContinue`, `pausedVia`, `claudeSessionID`, `branchSlug`, `failedTool`, `resumeAt` | spec-state-schema.md §mclaude-sessions | GAP | SPEC→FIX | spec-state-schema.md mclaude-sessions value schema does not include any of the ADR-0044 quota-managed session fields. These must be added to the SessionState JSON schema. |
| 32 | Session KV new fields | spec-nats-payload-schema.md §KV_mclaude-sessions-{uslug} | REFLECTED | — | Payload schema's KV section includes all quota fields with correct types and descriptions. |
| 33 | Status enum: `pending \| running \| paused \| requires_action \| completed \| stopped \| cancelled \| needs_spec_fix \| failed \| error` | spec-nats-payload-schema.md §KV_mclaude-sessions-{uslug} | REFLECTED | — | Full status enum documented in payload schema. |
| 34 | `QuotaStatus` data model: `{U5, U7, R5, R7, HasData, TS}` on `mclaude.users.{uslug}.quota` | spec-state-schema.md §Quota Status | REFLECTED | — | QuotaStatus documented with matching fields and subject. |
| 35 | `QuotaStatus` data model | spec-nats-payload-schema.md §Quota Subject | REFLECTED | — | Quota subject payload matches ADR. |
| 36 | Lifecycle: `session_job_complete` with `{type, sessionId, branch, ts}` — no `jobId` | spec-state-schema.md §session_job_complete | GAP | SPEC→FIX | spec-state-schema.md lifecycle payload includes `jobId` and `prUrl` fields. ADR-0044 eliminates both (no job concept, no stdout-marker scanning for prUrl). |
| 37 | Lifecycle: `session_job_complete` — no `jobId` | spec-nats-payload-schema.md §session_job_complete | GAP | SPEC→FIX | spec-nats-payload-schema.md lifecycle payload includes `jobId` field. ADR-0044 eliminates the job concept — there is no jobId. |
| 38 | Lifecycle: `session_job_paused` with `{type, sessionId, pausedVia, u5, r5, outputTokensSinceSoftMark, ts}` — no `jobId` | spec-state-schema.md §session_job_paused | GAP | SPEC→FIX | Includes `jobId` field. ADR-0044 removes it. |
| 39 | Lifecycle: `session_job_paused` — no `jobId` | spec-nats-payload-schema.md §session_job_paused | GAP | SPEC→FIX | Includes `jobId` field. ADR-0044 removes it. |
| 40 | Lifecycle: `session_job_cancelled` with `{type, sessionId, ts}` — no `jobId` | spec-state-schema.md §session_job_cancelled | GAP | SPEC→FIX | Includes `jobId` field. ADR-0044 removes it. |
| 41 | Lifecycle: `session_job_cancelled` — no `jobId` | spec-nats-payload-schema.md §session_job_cancelled | GAP | SPEC→FIX | Includes `jobId` field. ADR-0044 removes it. |
| 42 | Lifecycle: `session_permission_denied` with `{type, sessionId, tool, ts}` — no `jobId` | spec-state-schema.md §session_permission_denied | GAP | SPEC→FIX | Includes `jobId` field. ADR-0044 removes it. |
| 43 | Lifecycle: `session_permission_denied` — no `jobId` | spec-nats-payload-schema.md §session_permission_denied | GAP | SPEC→FIX | Includes `jobId` field. ADR-0044 removes it. |
| 44 | Lifecycle: `session_job_failed` with `{type, sessionId, error, ts}` — no `jobId` | spec-state-schema.md §session_job_failed | GAP | SPEC→FIX | Includes `jobId` field. ADR-0044 removes it. |
| 45 | Lifecycle: `session_job_failed` — no `jobId` | spec-nats-payload-schema.md §session_job_failed | GAP | SPEC→FIX | Includes `jobId` field. ADR-0044 removes it. |
| 46 | "`handleDelete` uses the session's `SoftThreshold` field to distinguish interactive from quota-managed sessions… Quota-managed session: emit `session_job_cancelled`… worktree-prune exclusion for `schedule/` branches" | spec-session-agent.md §Session Lifecycle > Deletion | GAP | SPEC→FIX | Deletion section describes uniform behavior for all sessions. Does not mention the branching on `SoftThreshold > 0` (interactive vs quota-managed), the `session_job_cancelled` event for quota sessions, or the `schedule/` branch worktree-prune exclusion. |
| 47 | "Recovery does NOT use stream replay. The agent uses the existing KV-based recovery mechanism" | spec-nats-activity.md §9n | GAP | SPEC→FIX | Section 9n is titled "Agent restart recovery (stream replay)" and describes replaying the session stream to recover state. ADR-0044 explicitly says recovery is KV-based: iterate session KV entries, re-evaluate quota-managed sessions by status (pending → re-gate prompt, paused → wait for quota, running → attempt --resume). |
| 48 | Agent restart recovery: "For each session KV entry with `softThreshold > 0`… status: pending → respawn CLI, gate prompt… status: paused → check subprocess, start QuotaMonitor… status: running → attempt --resume" | spec-session-agent.md §Session Lifecycle > Resumption | PARTIAL | SPEC→FIX | Resumption section describes KV watch + resume with --resume for all sessions. Missing the ADR-0044 quota-managed recovery branching: pending (respawn + gate prompt), paused (check subprocess alive, start QuotaMonitor), running with dead subprocess (attempt --resume with claudeSessionID). |
| 49 | "`QuotaMonitor` goroutine with select-loop cases: `stopCh`, `permDeniedCh`, `quotaCh`, `turnEndedCh`, `doneCh`" | spec-session-agent.md §Quota Monitoring | GAP | SPEC→FIX | QuotaMonitor section describes a simpler model (subscribe to quota, send threshold message, 30-min timer, scan for marker). ADR-0044's QuotaMonitor is a full goroutine with 5 select-loop cases, handleTurnEnd(), handleSubprocessExit(), token counting via onRawOutput, and turnEndedCh priority over doneCh. |
| 50 | "Token counting uses a two-strategy approach: byte estimate `len(raw) / 4` during turn, authoritative `usage.output_tokens` on result event" | spec-session-agent.md §Quota Monitoring | GAP | SPEC→FIX | Not reflected. Spec describes no token counting strategy. |
| 51 | "`handleTurnEnd()` inspects `stopReason`: quota_soft → session_job_paused, quota_hard → session_job_paused, empty → session_job_complete" | spec-session-agent.md §Quota Monitoring | GAP | SPEC→FIX | Not reflected. handleTurnEnd() dispatch table is absent from spec. |
| 52 | "`handleSubprocessExit()`: if `terminalEventPublished` → no-op, otherwise → `session_job_failed`" | spec-session-agent.md §Quota Monitoring | GAP | SPEC→FIX | Not reflected. handleSubprocessExit() logic absent from spec. |
| 53 | "QuotaMonitor sends messages to NATS subject `sessions.{sslug}.input`… uses the same JSON envelope defined in spec-nats-payload-schema.md" | spec-session-agent.md §Quota Monitoring | GAP | SPEC→FIX | Spec doesn't describe QuotaMonitor publishing to sessions.input with the standard envelope. Still describes stdin messages. |
| 54 | "control_request interrupts (hard-stop) are written directly to `sess.stdinCh`… are NOT published to NATS" | spec-session-agent.md §Quota Monitoring | GAP | SPEC→FIX | Spec doesn't distinguish between NATS-published input (soft-stop, prompt, resume) and direct stdinCh writes (hard-stop control_request). |
| 55 | "`/schedule-feature` skill location: Lives in external plugin. Deleted from mclaude." | spec-session-agent.md | REFLECTED | — | No /schedule-feature skill documented in the session-agent spec. |
| 56 | "mclaude-job-queue KV bucket eliminated" | spec-session-agent.md §NATS KV Buckets | GAP | SPEC→FIX | spec-session-agent.md §NATS KV Buckets (Read-Only) still lists "`mclaude-job-queue` (read/write, daemon only) — Job entries for the scheduled job dispatcher." |
| 57 | "Lifecycle subscriber goroutine — agent updates session KV directly" (eliminated) | spec-session-agent.md §NATS Subjects (Subscribe) | GAP | SPEC→FIX | spec-session-agent.md still documents "Lifecycle events (daemon only) via core NATS wildcard… Updates job queue KV on terminal job events." The lifecycle subscriber that updates job-queue KV is eliminated. |
| 58 | Quota publisher: "publishes QuotaStatus to NATS subject mclaude.users.{uslug}.quota (core NATS, no JetStream retention)" | spec-session-agent.md §Quota Monitoring | GAP | SPEC→FIX | QuotaMonitor section (step 1) still uses UUID-based subject (`mclaude.users.{UUID}.quota`) per the "Known bug" annotation. ADR-0044 uses slug-based subjects. While this is a known code bug, the spec should document the correct slug-based subject as the target. |
| 59 | "QuotaMonitor… sends a `MCLAUDE_STOP: quota_soft` message via sessions.input" | spec-session-agent.md §Quota Monitoring | GAP | SPEC→FIX | Spec step 2 says "sends a `QUOTA_THRESHOLD_REACHED` message to Claude's stdin requesting graceful stop." ADR-0044 uses `MCLAUDE_STOP: quota_soft` as the marker, sent via `sessions.input` (NATS), not directly to stdin. |
| 60 | "30-minute hard-interrupt timer" eliminated; replaced by token-budget hard stop | spec-session-agent.md §Quota Monitoring | GAP | SPEC→FIX | Spec step 3 says "Starts a 30-minute hard-interrupt timer as a backstop." ADR-0044 replaces this with `hardHeadroomTokens` token-budget enforcement. |
| 61 | "sessions.create `sessions.{sslug}.input` format for QuotaMonitor messages" | spec-nats-payload-schema.md §sessions.{sslug}.input | REFLECTED | — | Input message format with `type: "message"` and `text` field matches QuotaMonitor's usage. |
| 62 | "No data (`hasData == false`): do not start pending sessions, do not pause running ones" | spec-nats-activity.md §9j | REFLECTED | — | Section 9j "QuotaMonitors see hasData=false. No action taken." |
| 63 | "Permission-denied path: Session KV → `status = needs_spec_fix`, `failedTool = toolName`. Session will not auto-restart." | spec-nats-activity.md §9g Permission denied | REFLECTED | — | Permission denied path with needs_spec_fix status documented. |
| 64 | "Startup: sessions.create arrives. Agent ensures worktree… Spawns CLI immediately. Extracts claudeSessionID. Session KV → status: pending. QuotaMonitor subscribes… evaluates u5 < softThreshold: send prompt" | spec-nats-activity.md §9g Startup, Prompt delivery | REFLECTED | — | Full startup flow with pending → gated prompt delivery documented. |

### Summary

- **Reflected**: 28
- **Gap**: 30
- **Partial**: 6

### Gaps by spec file

**`spec-state-schema.md`** (8 gaps):
- Session KV `state`→`status` rename not applied (#30)
- Session KV missing all quota-managed fields (#31)
- `mclaude-job-queue` KV bucket still documented (#1)
- `sessions.create` payload still lists nested `quotaMonitor` (#5)
- 5 lifecycle event payloads still include `jobId` field (#36, #38, #40, #42, #44)

**`spec-session-agent.md`** (16 gaps, 2 partial):
- `localhost:8378` HTTP API still documented (#2)
- Job Dispatcher still documented (#2)
- `mclaude-job-queue` KV still listed (#56)
- Lifecycle subscriber for job-queue still documented (#57)
- `quotaMonitor` nested field in creation step (#6)
- Default allowlist for strict-allowlist not removed (#22)
- handleDelete branching missing (#46)
- QuotaMonitor: still uses QUOTA_THRESHOLD_REACHED (#59), 30-min timer (#60), SESSION_JOB_COMPLETE marker (#16)
- QuotaMonitor: missing turn-end detection (#19), token counting (#50), handleTurnEnd (#51), handleSubprocessExit (#52), sessions.input publishing (#53), stdinCh vs NATS distinction (#54)
- Quota publisher still marked "daemon only" (#9)
- Quota subject uses UUID (#58)
- Agent restart recovery for quota-managed sessions partial (#48)
- Permission-denied → QuotaMonitor channel partial (#28)
- Session callbacks not formally specified (#29)

**`spec-nats-payload-schema.md`** (5 gaps):
- 5 lifecycle event payloads still include `jobId` field (#37, #39, #41, #43, #45)

**`spec-nats-activity.md`** (1 gap):
- Section 9n describes stream replay for recovery; ADR-0044 uses KV-based recovery (#47)
