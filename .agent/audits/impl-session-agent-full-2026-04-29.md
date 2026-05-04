# Full Implementation Audit: mclaude-session-agent

**Date:** 2026-04-29
**Specs reviewed:** `docs/mclaude-session-agent/spec-session-agent.md`, `docs/spec-state-schema.md`
**Code reviewed:** All `.go` files in `mclaude-session-agent/` (35 files including tests, 18 production files)

---

## NEW Gaps (Not Previously Flagged in Spec)

### GAP-SA-N1: `--health` probe does not check NATS connection status
- **Severity:** PARTIAL
- **Spec says:** "checks process alive and NATS connection status. Exits 0 if both OK."
- **Code does:** `case "--health": os.Exit(0)` — immediately exits 0 without checking NATS connection. Only checks that the process is alive (implicitly, by running).
- **File:** `main.go:36`

### GAP-SA-N2: `--ready` probe does not verify Claude Code binary
- **Severity:** PARTIAL
- **Spec says:** "attempts a NATS connection and verifies Claude Code can be spawned. Exits 0 on success."
- **Code does:** Connects to NATS and exits 0, but does NOT verify that the Claude Code binary exists or can be spawned (e.g., `exec.LookPath(claudePath)`).
- **File:** `main.go:38-47`

### GAP-SA-N3: CLI flag name `--host-slug` vs spec's `--host`
- **Severity:** MISMATCH
- **Spec says:** "`--host` / `HOST_SLUG` — Flag or env — Required. Host slug per ADR-0035… BYOH daemons read `--host`"
- **Code does:** `flag.String("host-slug", os.Getenv("HOST_SLUG"), ...)` — the flag is named `--host-slug`, not `--host`. The env var `HOST_SLUG` is correct, but CLI callers must use `--host-slug` instead of the spec-prescribed `--host`.
- **File:** `main.go:76`

### GAP-SA-N4: No `~/.mclaude/active-host` symlink fallback for host slug
- **Severity:** MISSING
- **Spec says:** "BYOH daemons read `--host` (defaulting to the target of `~/.mclaude/active-host` symlink if unset)."
- **Code does:** Falls back through `slugify(hostname)` → `slugify(os.Hostname())` → `"h-unknown"`. No code reads `~/.mclaude/active-host`. The spec's known bug note about the fallback chain mentions this, but the active-host symlink read was the INTENDED default before the fallback chain, and it is completely absent.
- **File:** `main.go:83-92`

### GAP-SA-N5: `session_restarting` lifecycle event not published during pod restart recovery
- **Severity:** MISSING
- **Spec says:** "3. Publish `session_restarting` lifecycle event per session." (under Resumption on pod restart)
- **Code does:** `recoverSessions()` publishes only `session_resumed` after successful resume. It never publishes `session_restarting` before starting the resume. The explicit `handleRestart` handler correctly publishes `session_restarting` before stop + resume, but the recovery path skips it.
- **File:** `agent.go:192`

### GAP-SA-N6: Daemon lifecycle subscriber uses `hosts.*` wildcard instead of `hosts.{hslug}`
- **Severity:** MISMATCH
- **Spec says:** "Lifecycle events (daemon only) via core NATS wildcard on `mclaude.users.{uslug}.hosts.{hslug}.projects.*.lifecycle.*`"
- **Code does:** `"mclaude.users." + string(d.cfg.UserSlug) + ".hosts.*.projects.*.lifecycle.*"` — subscribes with `hosts.*` (all hosts) instead of `hosts.{hslug}` (daemon's own host only). This means the daemon receives lifecycle events from ALL hosts, not just its own. While this may work, it could cause the daemon to process job lifecycle events triggered by different hosts' sessions.
- **File:** `daemon_jobs.go:191`

---

## Known Bugs (Already Flagged in Spec — Confirmed in Code)

### GAP-SA-K1: `HOST_SLUG` / `--host` not fatal on absence
- **Severity:** MISMATCH
- **Spec says:** "Spec requires fatal exit with `FATAL: HOST_SLUG required`. Known bug: code falls back to slugified hostname or `h-unknown`."
- **Code confirms:** `main.go:83-92` — falls through `slugify(hostname)` → `"h-unknown"`.

### GAP-SA-K2: `clearPendingControlsForResume` sets state to `"idle"` instead of `"restarting"`
- **Severity:** MISMATCH
- **Spec says:** "Set all matching session KV entries to `state: 'restarting'`"
- **Code confirms:** `state.go:93` — `st.State = StateIdle` instead of `"restarting"`.
- **Root cause:** `StateRestarting` constant is not defined.

### GAP-SA-K3: Only 4 of 9 state constants defined
- **Severity:** PARTIAL
- **Spec says:** Session state can be: `idle`, `running`, `requires_action`, `updating`, `restarting`, `failed`, `plan_mode`, `waiting_for_input`, `unknown`.
- **Code confirms:** Only `StateIdle`, `StateRunning`, `StateRequiresAction`, `StateUpdating` are defined in `events.go:22-26`. Missing: `StateRestarting`, `StateFailed`, `StatePlanMode`, `StateWaitingForInput`, `StateUnknown`.

### GAP-SA-K4: 30s startup timeout does nothing
- **Severity:** MISMATCH
- **Spec says:** "Sessions that fail to start within 30s: mark `state: 'failed'`, publish `session_failed` lifecycle event."
- **Code confirms:** `session.go` startup monitoring goroutine has comment: "Don't kill the process; it's idle but valid" — does nothing on timeout. Session stays in current state indefinitely.

### GAP-SA-K5: `accumulateUsage()` dead code — `cacheReadTokens` and `cacheWriteTokens` always 0
- **Severity:** MISMATCH
- **Spec says:** "`handleSideEffect` only accumulates `inputTokens`, `outputTokens`, and `costUsd` from `result` events — `cacheReadTokens` and `cacheWriteTokens` are never accumulated."
- **Code confirms:** `session.go` `handleSideEffect` `EventTypeResult` case: `s.state.Usage.InputTokens += r.Usage.InputTokens; s.state.Usage.OutputTokens += r.Usage.OutputTokens; s.state.Usage.CostUSD += r.TotalCostUSD`. The `accumulateUsage()` function in `state.go:97` handles all 5 fields but is never called.

### GAP-SA-K6: Two-phase shell tracking (`pendingShells`, `inFlightShells`) not implemented
- **Severity:** MISSING
- **Spec says:** Full two-phase shell tracking with pending/promoted maps, output path formula, sanitizePath, removal on task-notification.
- **Code confirms:** No `pendingShells`, `inFlightShells`, `sanitizePath`, or background task ID extraction code exists anywhere in the codebase. Only `inFlightBackgroundAgents` (agent tracking) is implemented.

### GAP-SA-K7: Shell-killed notifications not implemented (graceful shutdown step 6)
- **Severity:** MISSING
- **Spec says:** For each entry in `inFlightShells`, construct a synthetic `<task-notification>` XML message and publish to JetStream `api.sessions.input`.
- **Code confirms:** `gracefulShutdown()` goes directly from drain poll (step 5) to cancel ctl consumer (step 6 in code, step 7 in spec). No shell-killed notification code exists. Depends on GAP-SA-K6.

### GAP-SA-K8: `MCLAUDE_LIFECYCLE` stream not created on startup
- **Severity:** MISSING
- **Spec says:** "The agent does not currently create MCLAUDE_LIFECYCLE on startup."
- **Code confirms:** `NewAgent` only creates `MCLAUDE_EVENTS` and `MCLAUDE_API`. Lifecycle events are published to core NATS but not persisted.

### GAP-SA-K9: `session_created` lifecycle payload missing `branch` field
- **Severity:** PARTIAL
- **Spec says (spec-state-schema.md):** `session_created` payload: `{ "type": "session_created", "sessionId": "...", "branch": "...", "ts": "RFC3339" }`
- **Code confirms:** `publishLifecycle` emits only `{type, sessionId, ts}`. No branch field.

### GAP-SA-K10: `session_stopped` lifecycle payload missing `exitCode` field
- **Severity:** PARTIAL
- **Spec says (spec-state-schema.md):** `session_stopped` payload: `{ "type": "session_stopped", "sessionId": "...", "exitCode": 0, "ts": "RFC3339" }`
- **Code confirms:** `publishLifecycle` emits only `{type, sessionId, ts}`. No exitCode field.

### GAP-SA-K11: QuotaMonitor subscribes to UUID-based subject instead of slug-based
- **Severity:** MISMATCH
- **Spec says:** "`newQuotaMonitor()` receives `userID` (UUID) and builds the subscription subject as `mclaude.users.{UUID}.quota`, but the daemon publishes to `mclaude.users.{userSlug}.quota` (slug-based). These never match."
- **Code confirms:** `quota_monitor.go:53` — `subject := "mclaude.users." + userID + ".quota"` uses UUID. Daemon's `runQuotaPublisher` uses `subj.UserQuota(d.cfg.UserSlug)` (slug-based). Quota signals never reach QuotaMonitor.

### GAP-SA-K12: `session_quota_interrupted` used instead of `session_job_paused`
- **Severity:** MISMATCH
- **Spec says (spec-state-schema.md):** QuotaMonitor should publish `session_job_paused`. Code publishes superseded `session_quota_interrupted`.
- **Code confirms:** `quota_monitor.go:120` — `m.publishLifec(m.sessionID, "session_quota_interrupted", ...)`.

### GAP-SA-K13: `session_job_paused` event missing spec-required fields
- **Severity:** PARTIAL
- **Spec says (spec-state-schema.md):** `session_job_paused` requires: `pausedVia`, `r5`, `outputTokensSinceSoftMark`. Should not include `priority`.
- **Code confirms:** `daemon_jobs.go` processDispatch publishes: `{type, sessionId, jobId, priority, u5, ts}`. Missing: `pausedVia`, `r5`, `outputTokensSinceSoftMark`. Includes non-spec `priority`.

### GAP-SA-K14: DELETE `/jobs/{id}` does not publish `session_job_cancelled` lifecycle event
- **Severity:** MISSING
- **Spec says (spec-state-schema.md):** "Published by daemon dispatcher after sessions.delete." Daemon should publish `session_job_cancelled` lifecycle event.
- **Code confirms:** `daemon_jobs.go` `handleJobByID` DELETE case publishes `sessions.delete` and writes `Status = cancelled` directly to KV but does NOT publish `session_job_cancelled`.

### GAP-SA-K15: `natsConnect()` uses NATS defaults (MaxReconnects=60, no RetryOnFailedConnect)
- **Severity:** MISMATCH
- **Spec says:** "On BYOH machines, the daemon permanently loses NATS after 60 failed reconnects instead of retrying indefinitely. Control-plane and controller-k8s both explicitly configure unlimited reconnects."
- **Code confirms:** `main.go` `natsConnect` only adds `UserCredentials` option. No `MaxReconnects(-1)` or `RetryOnFailedConnect(true)`.

### GAP-SA-K16: Auto-restart on Claude process crash not implemented
- **Severity:** MISSING
- **Spec says:** "No goroutine watches session `doneCh` for unexpected exits. A crashed Claude process leaves the session dead in memory."
- **Code confirms:** No crash watcher goroutine. `doneCh` is closed when the stdout scanner returns, but nothing acts on it except the quota monitor and the graceful shutdown drain.

### GAP-SA-K17: `JobEntry` struct still carries ADR-0009 schema
- **Severity:** MISMATCH
- **Spec says (spec-state-schema.md):** ADR-0034 target adds fields: `prompt`, `title`, `branchSlug`, `resumePrompt`, `softThreshold`, `hardHeadroomTokens`, `permPolicy`, `allowedTools`, `claudeSessionID`, `pausedVia`.
- **Code confirms:** `state.go` `JobEntry` has ADR-0009 fields (`SpecPath`, `Threshold`, `PRUrl`) but none of the ADR-0034 additions.

### GAP-SA-K18: Job KV keys use UUID instead of slug
- **Severity:** MISMATCH
- **Spec says (spec-state-schema.md):** Key format should be `{uslug}.{jobId}`.
- **Code confirms:** `readJobEntry` and `writeJobEntry` call `subj.JobQueueKVKey(slug.UserSlug(userID), jobID)` — casts UUID to UserSlug. Internally consistent but deviates from spec.

### GAP-SA-K19: `dispatchQueuedJob` uses `ProjectID` (UUID) as `ProjectSlug`
- **Severity:** MISMATCH
- **Spec says (spec-state-schema.md):** Dispatcher should use slug fields for NATS subject construction.
- **Code confirms:** `daemon_jobs.go:153` — `slug.ProjectSlug(job.ProjectID)` casts UUID to ProjectSlug.

---

## Verified Correct (Spec Matches Code)

| Section | Status |
|---------|--------|
| **MCLAUDE_EVENTS stream** — subjects, retention, MaxAge, storage, discard | ✅ Match |
| **MCLAUDE_API stream** — subjects, retention, MaxAge, storage, discard | ✅ Match |
| **sa-cmd consumer** — name, filter subjects, AckPolicy, AckWait, MaxDeliver | ✅ Match |
| **sa-ctl consumer** — name, filter, same ack config | ✅ Match |
| **Fetch loop** — batch 10, FetchMaxWait 5s | ✅ Match |
| **jsToNatsMsg adapter** — wraps JetStream msg for handler compatibility | ✅ Match |
| **API error events** — subject pattern, payload schema | ✅ Match |
| **Lifecycle events** — subject pattern, all event types published | ✅ Match |
| **Stream-json events** — subject pattern with slug fallback to UUID | ✅ Match |
| **Quota status publish** — subject `mclaude.users.{uslug}.quota`, 60s interval | ✅ Match |
| **Terminal API subscriptions** — create, delete, resize on core NATS | ✅ Match |
| **KV bucket reads** — sessions, projects, job-queue | ✅ Match |
| **KV bucket writes** — sessions (all state changes), job-queue (daemon) | ✅ Match |
| **No writes to mclaude-hosts** — daemon confirmed read-only | ✅ Match |
| **Session creation flow** — all 11 steps match | ✅ Match |
| **Session deletion flow** — all 5 steps match (interrupt, wait, worktree, KV, lifecycle) | ✅ Match |
| **Session restart flow** — lifecycle, stop, clear controls, respawn --resume | ✅ Match |
| **Updating state recovery** — pendingUpdatingIDs, clearUpdatingState after consumers | ✅ Match |
| **In-flight background agent tracking** — +1 on Agent run_in_background, -1 on task-notification | ✅ Match |
| **Event truncation at 8MB** — content removed, truncated:true added | ✅ Match |
| **Compact boundary tracking** — onEventPublished → updateReplayFromSeq | ✅ Match |
| **Graceful shutdown** — all implemented steps correct (1-5, 6-8 mapped to spec 1-5, 7-9) | ✅ Match |
| **KV write suppression during shutdown** — shutdownPending flag | ✅ Match |
| **Auto-interrupt requires_action during shutdown** — sendInterrupt in poll loop | ✅ Match |
| **No wall-clock timeout on drain** — infinite poll with 1s ticker | ✅ Match |
| **Permission policies** — managed, auto, allowlist, strict-allowlist | ✅ Match |
| **Default allowlist** — Read, Write, Edit, Glob, Grep, Bash, Agent, Task* | ✅ Match |
| **onStrictDeny callback** — publishes session_permission_denied | ✅ Match |
| **Claude spawn flags** — --print --verbose --output-format stream-json etc. | ✅ Match |
| **Credential management** — symlink, merge, convert, setup-git, RefreshIfChanged | ✅ Match |
| **SCP→HTTPS normalization** — NormalizeGitURL with registered hosts check | ✅ Match |
| **Git auth error detection** — exit code 128 + pattern matching | ✅ Match |
| **Initial repo setup** — clone --bare or init scratch repo | ✅ Match |
| **Worktree management** — add, remove --force, collision check, joinWorktree | ✅ Match |
| **Terminal sessions** — PTY spawn, 4KB output chunks, resize, delete/kill | ✅ Match |
| **Debug unix sockets** — /tmp/mclaude-session-{sessionId}.sock, NDJSON | ✅ Match |
| **Debug lifecycle events** — debug_attached, debug_detached | ✅ Match |
| **Metrics** — all 4 metrics registered with correct types/labels | ✅ Match |
| **OpenTelemetry spans** — NATS publish, KV write, session lifecycle, Claude spawn | ✅ Match |
| **W3C traceparent propagation** — InjectTraceparent/ExtractTraceparent | ✅ Match |
| **JetStream fetch error backoff** — exponential 100ms to 5s | ✅ Match |
| **KV bucket not found** — fatal exit | ✅ Match |
| **Credential helper setup fails** — warn, non-fatal | ✅ Match |
| **Debug socket start fails** — warn, non-fatal | ✅ Match |
| **Session delete — 10s timeout then SIGKILL** | ✅ Match |
| **JWT refresh** — 60s check interval, 15min threshold, POST to refresh URL | ✅ Match |
| **Job dispatcher** — startup recovery, dispatch, quota preemption/recovery | ✅ Match |
| **Job retries** — 3 attempts then failed | ✅ Match |
| **HTTP API** — POST/GET /jobs, GET/DELETE /jobs/{id}, GET /jobs/projects | ✅ Match |
| **Quota publisher** — 60s poll, Anthropic API, credentials file read | ✅ Match |
| **Quota monitor** — QUOTA_THRESHOLD_REACHED message, 30min hard interrupt | ✅ Match |
| **SESSION_JOB_COMPLETE marker** — extraction from assistant events | ✅ Match |
| **LOG_LEVEL** — zerolog level parsing from env | ✅ Match |
| **METRICS_ADDR** — defaults to :9091 | ✅ Match |
| **Configuration env vars** — all spec entries present (NATS_URL, USER_ID, etc.) | ✅ Match |
| **HOSTNAME/MACHINE_ID dead code** — stored in DaemonConfig, never accessed | ✅ Match |
| **Daemon child management** — spawn, restart on crash, shutdown | ✅ Match |
| **Daemon no heartbeats** — no periodic heartbeat publish | ✅ Match |

---

## Summary

| Category | Count |
|----------|-------|
| **NEW gaps (not previously flagged)** | 6 |
| **Known bugs (confirmed, already in spec)** | 19 |
| **Verified correct** | 50+ sections |

### Critical NEW gaps requiring spec update:
1. **GAP-SA-N5** — `session_restarting` not published during recovery (breaks SPA UI indicator)
2. **GAP-SA-N3** — `--host-slug` flag name mismatch (CLI compatibility issue)
3. **GAP-SA-N4** — No `~/.mclaude/active-host` fallback (daemon UX gap)

### Most impactful known bugs (blocking features):
1. **GAP-SA-K11** — QuotaMonitor UUID/slug mismatch (quota enforcement completely broken)
2. **GAP-SA-K16** — No auto-restart on Claude crash (sessions die silently)
3. **GAP-SA-K6/K7** — Shell tracking not implemented (graceful upgrade loses shell context)
4. **GAP-SA-K15** — NATS reconnect limit (daemon disconnects permanently after 60 failures)
