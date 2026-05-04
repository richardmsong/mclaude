# Audit Round 9: Code vs Spec Gaps

**Date:** 2026-04-29
**Auditor:** factory-droid
**Scope:** All 6 spec files cross-referenced against production code
**Method:** Full read of all spec files and all listed code files; systematic cross-reference of env vars, NATS subjects, struct fields, Helm values, lifecycle events, default values, error handling paths, conditional logic

---

## Unflagged Gaps

### GAP 1: `session_created` lifecycle event missing `branch` field

| Field | Value |
|-------|-------|
| **Summary** | `session_created` lifecycle event payload omits the `branch` field required by the spec |
| **Code location** | `mclaude-session-agent/agent.go` → `handleCreate()` calls `a.publishLifecycle(sessionID, "session_created")` which uses the generic `publishLifecycle` emitting only `{type, sessionId, ts}` |
| **Spec location** | `docs/spec-state-schema.md` → Lifecycle Event Payloads → `session_created`: `{"type": "session_created", "sessionId": "...", "branch": "...", "ts": "RFC3339"}` |
| **Detail** | The spec defines `branch` as a required field in the `session_created` payload. The code uses the generic `publishLifecycle()` helper which only includes `type`, `sessionId`, and `ts`. Should use `publishLifecycleExtra()` with `{"branch": branch}` to include the branch. |

### GAP 2: `session_stopped` lifecycle event missing `exitCode` field

| Field | Value |
|-------|-------|
| **Summary** | `session_stopped` lifecycle event payload omits the `exitCode` field required by the spec |
| **Code location** | `mclaude-session-agent/agent.go` → `handleDelete()` calls `a.publishLifecycle(req.SessionID, "session_stopped")` emitting only `{type, sessionId, ts}` |
| **Spec location** | `docs/spec-state-schema.md` → Lifecycle Event Payloads → `session_stopped`: `{"type": "session_stopped", "sessionId": "...", "exitCode": 0, "ts": "RFC3339"}` |
| **Detail** | The spec defines `exitCode` as a required field. The exit code from `cmd.ProcessState.ExitCode()` is available after `stopAndWait` returns but is never captured or included in the event. |

### GAP 3: In-flight background shell tracking not implemented

| Field | Value |
|-------|-------|
| **Summary** | The two-phase in-flight shell tracking (`pendingShells` → `inFlightShells`) described in the spec is entirely absent from code |
| **Code location** | `mclaude-session-agent/session.go` → `Session` struct has `inFlightBackgroundAgents` (agent tracking) but no `pendingShells` or `inFlightShells` maps |
| **Spec location** | `docs/mclaude-session-agent/spec-session-agent.md` → Event Routing → "In-flight background shell tracking (K8s mode; two-phase)" |
| **Detail** | Spec describes: Phase 1 (Pending) — on `assistant` event with Bash `tool_use` where `run_in_background == true`, adds `pendingShell` entry. Phase 2 (Promoted) — on `user` event with matching `tool_result`, extracts `backgroundTaskId` and promotes to `inFlightShells`. Removal on `task-notification`. None of this code exists. The spec flags `CLAUDE_CODE_TMPDIR` as "not yet injected" but does NOT flag the absence of the tracking code itself. |

### GAP 4: Graceful shutdown shell-killed notifications not implemented

| Field | Value |
|-------|-------|
| **Summary** | Step 6 of the graceful shutdown sequence (shell-killed notifications) is not implemented |
| **Code location** | `mclaude-session-agent/agent.go` → `gracefulShutdown()` — goes directly from idle-drain polling (step 5) to cancelling the control consumer (step 6→7 in code, which is spec step 7→8) |
| **Spec location** | `docs/mclaude-session-agent/spec-session-agent.md` → Graceful Shutdown (SIGTERM) → Step 6 |
| **Detail** | Spec step 6 requires: for each session's `inFlightShells`, construct a `<task-notification>` XML message with `<task-id>`, `<tool-use-id>`, `<output-file>`, `<status>killed</status>`, `<summary>` and publish to JetStream `api.sessions.input`. This depends on GAP 3 (shell tracking) which is also absent. The spec flags `CLAUDE_CODE_TMPDIR` as "not yet injected" but does NOT flag the absence of the shutdown notification code. |

### GAP 5: `session_job_cancelled` lifecycle event not published by daemon DELETE

| Field | Value |
|-------|-------|
| **Summary** | Daemon's DELETE /jobs/{id} handler does not publish the `session_job_cancelled` lifecycle event |
| **Code location** | `mclaude-session-agent/daemon_jobs.go` → `handleJobByID()` DELETE case — publishes `sessions.delete` then sets `job.Status = "cancelled"` in KV but never publishes `session_job_cancelled` lifecycle event |
| **Spec location** | `docs/spec-state-schema.md` → Lifecycle Event Payloads → `session_job_cancelled`: "Published by: daemon dispatcher when handling DELETE /jobs/{id}, after publishing sessions.delete" |
| **Detail** | The spec says the dispatcher publishes `session_job_cancelled` after `sessions.delete`. The lifecycle subscriber is supposed to pick it up and write `Status = cancelled` to KV. Instead, the code writes `cancelled` directly to KV without the lifecycle event, meaning the SPA's lifecycle subscriber never receives the cancellation notification. |

### GAP 6: `session_job_paused` lifecycle event has wrong payload fields

| Field | Value |
|-------|-------|
| **Summary** | The `session_job_paused` event published by the dispatcher has incorrect fields vs spec |
| **Code location** | `mclaude-session-agent/daemon_jobs.go` → `processDispatch()` — publishes with fields `{type, sessionId, jobId, priority, u5, ts}` |
| **Spec location** | `docs/spec-state-schema.md` → Lifecycle Event Payloads → `session_job_paused`: `{type, sessionId, jobId, pausedVia, u5, r5, outputTokensSinceSoftMark, ts}` |
| **Detail** | Missing spec fields: `pausedVia` (should be `"quota_soft"` or `"quota_hard"`), `r5` (5h reset time from QuotaStatus, load-bearing for autoContinue), `outputTokensSinceSoftMark`. Extra non-spec field: `priority`. The `r5` field is load-bearing — the lifecycle subscriber uses it to set `JobEntry.ResumeAt` when `autoContinue` is true. Without it, autoContinue resume scheduling is broken. |

### GAP 7: QuotaMonitor publishes `session_quota_interrupted` instead of `session_job_paused`

| Field | Value |
|-------|-------|
| **Summary** | QuotaMonitor's `publishExitLifecycle` uses the superseded event name `session_quota_interrupted` instead of `session_job_paused` |
| **Code location** | `mclaude-session-agent/quota_monitor.go` → `publishExitLifecycle()` — when `stopReason == "quota"`, publishes `"session_quota_interrupted"` |
| **Spec location** | `docs/spec-state-schema.md` → Lifecycle Event Payloads → `session_job_paused`: "Published by: QuotaMonitor's publishExitLifecycle on soft-stop turn-end OR hard-stop interrupt" |
| **Detail** | The spec says `session_job_paused` supersedes `session_quota_interrupted`. The QuotaMonitor should publish `session_job_paused` with `pausedVia: "quota_soft"` or `"quota_hard"`. The lifecycle subscriber handles `session_quota_interrupted` as a separate event, but the spec's canonical event list does not include `session_quota_interrupted` — only `session_job_paused`. |

### GAP 8: Admin-created users don't get default 'local' host row

| Field | Value |
|-------|-------|
| **Summary** | Users created via `POST /admin/users` don't receive the default 'local' machine host row |
| **Code location** | `mclaude-control-plane/admin.go` → `adminCreateUser()` calls `db.CreateUser()` which only inserts into the `users` table |
| **Spec location** | `docs/spec-state-schema.md` → hosts → Default machine host: "On user creation, control-plane writes one row to hosts with slug='local', type='machine', role='owner'" |
| **Detail** | The schema migration DO block backfills default hosts for existing users on startup, but users created at runtime via the admin API don't get a default 'local' host until the next binary restart. This means admin-created users cannot create projects (which require a `host_id`) until after a restart. The `seedDev` path also doesn't call a separate host-creation function after `CreateUser`. |

### GAP 9: User slug creation has no reserved-word blocklist enforcement

| Field | Value |
|-------|-------|
| **Summary** | `computeUserSlug()` does not check against the reserved-word blocklist before assigning a slug |
| **Code location** | `mclaude-control-plane/db.go` → `computeUserSlug()` — applies regex `[^a-zA-Z0-9]+` → `-` and lowercases, but never checks against the blocklist |
| **Spec location** | `docs/spec-state-schema.md` → users → Slug charset: "excluding leading `_` and the reserved-word blocklist `{users, hosts, projects, sessions, clusters, api, events, lifecycle, quota, terminal}`" |
| **Detail** | A user with email `api@example.com` would get slug `api`, colliding with the reserved word. A user with email `users@corp.com` would get slug `users`. The spec says the blocklist applies and that a fallback `{prefix}-{6 base32}` kicks in on match, but this logic is not implemented. The SQL backfill in the migration also lacks blocklist enforcement. |

---

## Summary

**9 unflagged gaps found.** Breakdown by component:

| Component | Count | Gaps |
|-----------|-------|------|
| mclaude-session-agent | 5 | GAPs 1, 2, 3, 4, 7 |
| mclaude-session-agent (daemon) | 2 | GAPs 5, 6 |
| mclaude-control-plane | 2 | GAPs 8, 9 |

**Severity assessment:**
- **High impact:** GAP 6 (`r5` field missing breaks autoContinue resume scheduling), GAP 8 (admin-created users can't use the platform until restart)
- **Medium impact:** GAPs 1, 2, 5, 7, 9 (incorrect event payloads, missing lifecycle events, slug collision risk)
- **Low impact (blocked by other known gaps):** GAPs 3, 4 (shell tracking depends on `CLAUDE_CODE_TMPDIR` which is already flagged as not injected)
