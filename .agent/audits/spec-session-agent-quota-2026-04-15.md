## Run: 2026-04-15T00:00:00Z

Component: session-agent (and daemon + skills)
Spec: docs/plan-quota-aware-scheduling.md
Round 3

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Notes |
|-----------------|-----------|---------------|---------|-------|
| plan-quota:11 | Quota data source: api.anthropic.com/api/oauth/usage via OAuth token | daemon_jobs.go:82-98 | IMPLEMENTED | URL, headers, response parsing all match |
| plan-quota:11 | OAuth token from ~/.claude/.credentials.json, field .claudeAiOauth.accessToken | daemon_jobs.go:34-73 | IMPLEMENTED | credentialsFile struct + readOAuthToken match |
| plan-quota:11 | Returns HasData:false if file missing or token empty | daemon_jobs.go:76-79 | IMPLEMENTED | err path returns QuotaStatus{HasData:false} |
| plan-quota:13 | Job persistence: mclaude-job-queue KV bucket | daemon.go:89-92, state.go:134-155 | IMPLEMENTED | Opened in NewDaemon; JobEntry struct defined |
| plan-quota:78-84 | Daemon struct: sessKV, jobQueueKV, projectsKV fields | daemon.go:54-57 | IMPLEMENTED | All three fields present with correct types |
| plan-quota:85-87 | All three KV handles opened in NewDaemon, fail-fast if not found | daemon.go:85-96 | IMPLEMENTED | Same js.KeyValue pattern as laptopsKV |
| plan-quota:89-91 | DaemonConfig.CredentialsPath field | daemon.go:43 | IMPLEMENTED | CredentialsPath string present |
| plan-quota:96-108 | runQuotaPublisher: polls every 60s, reads OAuth token, calls usage API, publishes to mclaude.{userId}.quota | daemon_jobs.go:131-158 | IMPLEMENTED | Publish-on-startup plus 60s ticker; correct subject |
| plan-quota:99-103 | API call headers: Authorization: Bearer, anthropic-beta: oauth-2025-04-20, Content-Type: application/json | daemon_jobs.go:86-88 | IMPLEMENTED | All three headers set |
| plan-quota:105 | Parses JSON {five_hour: {utilization, resets_at}, seven_day: {utilization, resets_at}} | daemon_jobs.go:40-49, 105-126 | IMPLEMENTED | quotaAPIResponse struct and parse logic match |
| plan-quota:107 | Publishes QuotaStatus to mclaude.{userId}.quota (core NATS, no JetStream) | daemon_jobs.go:131-158 | IMPLEMENTED | d.nc.Publish (not JetStream) |
| plan-quota:108 | Sends same QuotaStatus on internal chan QuotaStatus to runJobDispatcher | daemon.go:58, daemon_jobs.go:139-142 | IMPLEMENTED | quotaCh field; non-blocking send |
| plan-quota:110-126 | runLifecycleSubscriber: subscribes mclaude.{userId}.*.lifecycle.*, handles 5 event types | daemon_jobs.go:247-314 | IMPLEMENTED | |
| plan-quota:116 | session_job_complete: set status=completed, prUrl, completedAt | daemon_jobs.go:267-271 | IMPLEMENTED | |
| plan-quota:117 | session_quota_interrupted: autoContinue->paused+resumeAt; else->queued | daemon_jobs.go:273-284 | IMPLEMENTED | |
| plan-quota:118 | session_permission_denied: set status=needs_spec_fix, failedTool | daemon_jobs.go:285-288 | IMPLEMENTED | FailedTool = ev["tool"] |
| plan-quota:119 | session_job_failed: set status=failed, error | daemon_jobs.go:289-292 | IMPLEMENTED | |
| plan-quota:120 | session_job_paused: no-op, logged | daemon_jobs.go:293-299 | IMPLEMENTED | |
| plan-quota:124 | Unrecognised jobId: ignore silently | daemon_jobs.go:260-263 | IMPLEMENTED | |
| plan-quota:126 | Started by Daemon.Run() alongside quota publisher and job dispatcher | daemon.go:129-134 | IMPLEMENTED | All 4 goroutines started |
| plan-quota:129-155 | runJobDispatcher: receives quota updates, watches KV, dispatches queued jobs | daemon_jobs.go:508-706 | IMPLEMENTED | |
| plan-quota:134 | On new queued entry: derive branch schedule/{slug}-{shortId}, set starting, write to KV | daemon_jobs.go:320-333 | IMPLEMENTED | |
| plan-quota:136-137 | sessions.create to mclaude.{userId}.{projectId}.api.sessions.create with branch, permPolicy:strict-allowlist, quotaMonitor | daemon_jobs.go:337-352 | IMPLEMENTED | allowedTools omitted (uses agent default) which matches spec |
| plan-quota:138-139 | Poll sessKV for state=idle up to 30s at 500ms | daemon_jobs.go:370-408 | IMPLEMENTED | |
| plan-quota:139 | On 30s timeout: increment RetryCount, reset queued; after 3 failures: failed | daemon_jobs.go:387-408 | IMPLEMENTED | |
| plan-quota:140-141 | Send dev-harness prompt via sessions.input fire-and-forget with session_id | daemon_jobs.go:431-441 | IMPLEMENTED | |
| plan-quota:141 | Update job to running, set SessionID, StartedAt | daemon_jobs.go:444-448 | IMPLEMENTED | |
| plan-quota:142-147 | On quota threshold exceeded: collect running jobs, sort ascending priority, send graceful stop, publish session_job_paused, update paused | daemon_jobs.go:584-655 | IMPLEMENTED | |
| plan-quota:145 | Graceful stop payload includes top-level session_id field | daemon_jobs.go:629-637 | IMPLEMENTED | |
| plan-quota:148-150 | On quota recovery: reset paused jobs to queued, sort by priority descending | daemon_jobs.go:658-691 | IMPLEMENTED | |
| plan-quota:151-155 | Daemon startup recovery: starting->queued, running(no sessKV)->queued, paused past resumeAt->queued | daemon_jobs.go:452-503 | IMPLEMENTED | |
| plan-quota:157-169 | Spec path to component mapping table | daemon_jobs.go:161-176 | IMPLEMENTED | All entries match |
| plan-quota:172-198 | Scheduled session prompt content (exact format) | daemon_jobs.go:188-219 | IMPLEMENTED | |
| plan-quota:202-203 | PermissionPolicyStrictAllowlist: auto-deny tools not in allowlist | session.go:333-357, state.go:23 | IMPLEMENTED | |
| plan-quota:206-218 | Deny control_response, call clearPendingControl, call onStrictDeny | session.go:333-357 | IMPLEMENTED | |
| plan-quota:220-233 | Session.onStrictDeny and Session.onRawOutput callbacks; nil by default | session.go:57-61 | IMPLEMENTED | |
| plan-quota:234-243 | onRawOutput invoked in stdout router under s.mu.Lock() before NATS publish | session.go:235-241 | IMPLEMENTED | |
| plan-quota:245-256 | Agent sets both callbacks in handleCreate before sess.start() | agent.go:691-705 | IMPLEMENTED | |
| plan-quota:259-273 | a.publishLifecycleExtra method | agent.go:1074-1086 | IMPLEMENTED | |
| plan-quota:276-288 | a.publishPermDenied method | agent.go:1090-1100 | IMPLEMENTED | |
| plan-quota:291-292 | Default dev-harness allowlist: Read, Write, Edit, Glob, Grep, Bash, Agent, Task* | agent.go:542-545 | IMPLEMENTED | All 13 tools match |
| plan-quota:296-302 | Extended sessions.create payload: permPolicy, allowedTools, quotaMonitor optional fields | agent.go:556-560 | IMPLEMENTED | |
| plan-quota:306-312 | QuotaMonitorConfig struct fields: Threshold, Priority, JobID, AutoContinue | state.go:126-132 | IMPLEMENTED | |
| plan-quota:315-336 | QuotaMonitor struct fields | quota_monitor.go:16-32 | IMPLEMENTED | All fields present |
| plan-quota:339 | newQuotaMonitor creates struct, subscribes, starts goroutine, returns | quota_monitor.go:36-66 | IMPLEMENTED | |
| plan-quota:341-348 | NATS ChanSubscribe with channel capacity 16 | quota_monitor.go:43-47 | IMPLEMENTED | |
| plan-quota:351 | On goroutine exit, calls m.quotaSub.Unsubscribe() | quota_monitor.go:173 | IMPLEMENTED | defer m.quotaSub.Unsubscribe() |
| plan-quota:353-378 | Goroutine select loop: stopCh, permDeniedCh, quotaCh, stopTimeout, session.doneCh | quota_monitor.go:172-218 | IMPLEMENTED | |
| plan-quota:366 | On permDenied: stopReason="permDenied", sendGracefulStop(), 30-min timer | quota_monitor.go:183-189 | IMPLEMENTED | |
| plan-quota:367-370 | On quota msg: store u5/r5; if hasData && u5>=threshold -> stop | quota_monitor.go:191-205 | IMPLEMENTED | |
| plan-quota:372-373 | On stopTimeout: sendHardInterrupt() | quota_monitor.go:207-210 | IMPLEMENTED | |
| plan-quota:374-377 | On session.doneCh: publishExitLifecycle(), close(stopCh) | quota_monitor.go:212-215 | IMPLEMENTED | |
| plan-quota:380-383 | sendGracefulStop() queues exact JSON on s.stdinCh (no top-level session_id) | quota_monitor.go:117-129 | IMPLEMENTED | |
| plan-quota:386-389 | sendHardInterrupt() queues {"type":"control_request","request":{"subtype":"interrupt"}} | quota_monitor.go:133-138 | IMPLEMENTED | |
| plan-quota:392-396 | publishExitLifecycle() logic: completionPR->complete, quota->interrupted, permDenied->nothing, else->failed | quota_monitor.go:143-167 | IMPLEMENTED | |
| plan-quota:398-421 | onRawOutput scans for SESSION_JOB_COMPLETE:, extracts PR URL up to 200 bytes | quota_monitor.go:93-113 | IMPLEMENTED | |
| plan-quota:424-431 | signalPermDenied non-blocking send on permDeniedCh | quota_monitor.go:82-88 | IMPLEMENTED | |
| plan-quota:433-448 | Control-plane: ensureJobQueueKV creates mclaude-job-queue bucket | (control-plane component, not in scope) | N/A | Deferred to control-plane audit |
| plan-quota:452-474 | runJobsHTTP: HTTP server on localhost:8378 | daemon_jobs.go:708-728 | IMPLEMENTED | |
| plan-quota:460-465 | HTTP routes table: POST/GET /jobs, GET/DELETE /jobs/{id}, GET /jobs/projects | daemon_jobs.go:710-713 | IMPLEMENTED | All 5 routes registered |
| plan-quota:468 | Handlers scope KV to {d.cfg.UserID}/{jobId} | daemon_jobs.go:734, 814, 867 | IMPLEMENTED | Uses d.cfg.UserID from DaemonConfig |
| plan-quota:468 | No auth header required (loopback-only, daemon knows userId from config) | daemon_jobs.go:732-733 comment | IMPLEMENTED | No X-User-ID logic; matches simplified spec |
| plan-quota:470 | POST /jobs: generate UUID, status=queued, createdAt, write to KV | daemon_jobs.go:756-772 | IMPLEMENTED | |
| plan-quota:472 | DELETE /jobs/{id}: if running, publish sessions.delete; status=cancelled, soft delete | daemon_jobs.go:834-852 | IMPLEMENTED | |
| plan-quota:474 | GET /jobs/projects: reads mclaude-projects KV, returns [{id, name}] | daemon_jobs.go:859-901 | IMPLEMENTED | |
| plan-quota:476-493 | /schedule-feature skill: usage, arguments, 4-step behavior | .agent/skills/schedule-feature/SKILL.md | IMPLEMENTED | |
| plan-quota:495-513 | /job-queue skill: list/cancel/status subcommands, curl calls, table format | .agent/skills/job-queue/SKILL.md | IMPLEMENTED | |
| plan-quota:519-551 | JobEntry struct fields with all status values | state.go:136-155 | IMPLEMENTED | All fields and status enum values present |
| plan-quota:558-565 | QuotaStatus struct: U5, U7, R5, R7, HasData, TS | state.go:116-123 | IMPLEMENTED | |
| plan-quota:570-629 | Five new lifecycle event payloads (fields) | quota_monitor.go:143-167, agent.go:1090-1100, daemon_jobs.go:641-650 | IMPLEMENTED | All field sets match |
| plan-quota:633-643 | Error handling table: quota API fail, creds missing, session start fail, graceful stop timeout, out-of-allowlist | daemon_jobs.go:76-79, 387-408, quota_monitor.go:207-210 | IMPLEMENTED | |
| plan-quota:647 | OAuth token never written to NATS, KV, or logs | daemon_jobs.go:53-73 (token returned inline, not stored) | IMPLEMENTED | |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| daemon_jobs.go:1-31 | INFRA | Package declaration, imports, package-level constants for timeouts and addresses |
| daemon_jobs.go:34-49 | INFRA | credentialsFile and quotaAPIResponse structs — necessary for readOAuthToken and fetchQuotaStatus |
| daemon_jobs.go:221-245 | INFRA | readJobEntry/writeJobEntry helpers — necessary plumbing for all job CRUD operations |
| daemon_jobs.go:316-449 | INFRA | dispatchQueuedJob — helper called by runJobDispatcher; extracted for clarity |
| daemon_jobs.go:451-504 | INFRA | startupRecovery — helper called by runJobDispatcher; extracted for clarity |
| daemon_jobs.go:581-706 | INFRA | processDispatch — helper called by runJobDispatcher; extracted for clarity |
| daemon_jobs.go:780-807 | INFRA | GET /jobs response: watcher loop, sort by createdAt desc — necessary implementation detail for spec'd GET /jobs endpoint |
| quota_monitor.go:1-10 | INFRA | Package declaration, imports |
| quota_monitor.go:69-77 | INFRA | stop() method — safe helper to close stopCh idempotently; used by handleCreate on monitor setup failure |
| state.go:1-101 | INFRA | Existing state types and helpers (not new to this spec, pre-existing infrastructure) |
| agent.go:540-545 | INFRA | defaultDevHarnessAllowlist var — spec'd at plan-quota:291 |
| session.go:378-408 | INFRA | shouldAutoApprove, buildAutoApproveResponse — pre-existing helpers extended by strict-allowlist path |

### Summary

- Implemented: 68
- Gap: 0
- Partial: 0
- Infra: 12
- Unspec'd: 0
- Dead: 0
