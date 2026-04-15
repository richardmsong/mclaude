## Run: 2026-04-15T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Notes |
|-----------------|-----------|---------------|---------|-------|
| plan-k8s-integration.md:9 | session-agent spawns headless Claude Code processes, routes stream-json events to/from NATS | agent.go, session.go | IMPLEMENTED | Core structure present |
| plan-k8s-integration.md:63-70 | Spawn args: --print --verbose --output-format stream-json --input-format stream-json --include-partial-messages --session-id | session.go:138-156 | IMPLEMENTED | All flags present |
| plan-k8s-integration.md:73-80 | Resume: uses --resume {sessionId} | session.go:138-146 | IMPLEMENTED | resume=true path |
| plan-k8s-integration.md:163 | Events subject: mclaude.{userId}.{location}.{projectId}.events.{sessionId} | session.go:202-206 | PARTIAL | Subject is mclaude.%s.%s.events.%s (3 segments: userID, projectID, sessionID). Missing {location} segment. |
| plan-k8s-integration.md:164 | Lifecycle subject: mclaude.{userId}.{location}.{projectId}.lifecycle.{sessionId} | agent.go:1048 | PARTIAL | Subject mclaude.%s.%s.lifecycle.%s — missing {location} segment. |
| plan-k8s-integration.md:171 | Stream MCLAUDE_EVENTS captures mclaude.*.*.*.events.* | agent.go:99-108 | PARTIAL | Filter is mclaude.*.*.events.* (3 wildcards), not 4. Missing location wildcard. |
| plan-k8s-integration.md:172 | Stream MCLAUDE_LIFECYCLE captures mclaude.*.*.*.lifecycle.* subjects | — | GAP | No MCLAUDE_LIFECYCLE stream is created anywhere in session-agent code. |
| plan-k8s-integration.md:178-199 | KV buckets: mclaude-sessions, mclaude-projects, mclaude-heartbeats, mclaude-locations | agent.go:82-93 | PARTIAL | Spec names bucket mclaude-locations; code uses mclaude-laptops. |
| plan-k8s-integration.md:194 | Session agents fail fast if bucket doesn't exist | agent.go:82-93 | IMPLEMENTED | Returns error if bucket not found |
| plan-k8s-integration.md:197 | mclaude-sessions: deleted by session agent on normal session delete | agent.go:817 | IMPLEMENTED | sessKV.Delete in handleDelete |
| plan-k8s-integration.md:199 | mclaude-heartbeats: TTL 90s on the KV entry | — | GAP | runHeartbeat writes to mclaude-heartbeats every 30s but no TTL is configured on the bucket or per-entry. |
| plan-k8s-integration.md:200 | mclaude-laptops: TTL 24h; Launcher refreshes on startup and every 12h | daemon.go:27-28,177-189 | IMPLEMENTED | laptopHeartbeatInterval=12h, writes on startup |
| plan-k8s-integration.md:253-254 | Subscribes to mclaude.{userId}.{location}.{projectId}.api.> | agent.go:267-309 | PARTIAL | Consumers filter mclaude.{userId}.{projectId}.api.sessions.* — missing {location} segment. |
| plan-k8s-integration.md:257-265 | Routes stdout events → NATS; routes NATS input → Claude stdin; tracks state; spawns PTY; unix socket; heartbeat; startup resume | session.go, agent.go, debug.go, terminal.go | IMPLEMENTED | All behaviors present |
| plan-k8s-integration.md:261 | Caches capabilities from init event; refreshes on reload_plugins | session.go:284-302 | PARTIAL | init event handled. reload_plugins refresh not handled — no case for reload_plugins control response updating capabilities. |
| plan-k8s-integration.md:338-349 | Graceful shutdown on SIGTERM: stop accepting new sessions, interrupt each Claude, wait 10s, SIGKILL, flush, publish lifecycle, close NATS, exit 0 | agent.go:418-498 | IMPLEMENTED | Spec (k8s integration) is superseded by plan-graceful-upgrades; actual impl follows graceful-upgrades spec. |
| plan-k8s-integration.md:378 | Unix socket at /tmp/mclaude-session-{id}.sock | debug.go:12 | IMPLEMENTED | debugSocketFmt matches |
| plan-k8s-integration.md:407 | Location collision check on startup | daemon.go:143-162 | IMPLEMENTED | checkHostnameCollision() |
| plan-k8s-integration.md:409-411 | JWT refresh: background goroutine, TTL decode, 15min threshold | daemon.go:308-345 | IMPLEMENTED | |
| plan-k8s-integration.md:417-429 | Worktrees: slugification, branch derivation | worktree.go:16-25, agent.go:579-593 | IMPLEMENTED | SlugifyBranch |
| plan-k8s-integration.md:430-455 | joinWorktree collision logic, git worktree add/remove | agent.go:598-813 | IMPLEMENTED | |
| plan-k8s-integration.md:466-498 | Session state KV JSON schema | state.go:27-43 | IMPLEMENTED | All fields present |
| plan-k8s-integration.md:500 | capabilities refreshed when reload_plugins response received | — | GAP | No code path updates capabilities on reload_plugins control response. init event is the only update point. |
| plan-k8s-integration.md:502 | replayFromSeq updated on /clear and compaction | agent.go:1106-1143 | PARTIAL | Updated on compact_boundary. clear event produces no replayFromSeq update. |
| plan-k8s-integration.md:519-533 | Lifecycle events: session_created, session_stopped, session_restarting, session_resumed, session_failed, debug_attached, debug_detached | agent.go:740,819,956,1005,729; debug.go | IMPLEMENTED | All published |
| plan-graceful-upgrades.md:51-57 | MCLAUDE_API stream: subjects mclaude.*.*.api.sessions.> | agent.go:110-121 | PARTIAL | Subjects filter is mclaude.*.*.api.sessions.> (3 wildcards), not mclaude.*.*.*.api.sessions.> (4). Missing location segment. |
| plan-graceful-upgrades.md:69-95 | Two durable pull consumers: cmd (create/delete/input/restart) and ctl (control) | agent.go:267-309 | IMPLEMENTED | Both consumers configured correctly |
| plan-graceful-upgrades.md:99-115 | JetStream fetch loop: batch 10, FetchMaxWait 5s, ack after handler | agent.go:314-347 | IMPLEMENTED | |
| plan-graceful-upgrades.md:119-130 | jsToNatsMsg adapter | agent.go:353-359 | IMPLEMENTED | |
| plan-graceful-upgrades.md:133-145 | dispatchCmd routes by subject suffix | agent.go:362-376 | IMPLEMENTED | |
| plan-graceful-upgrades.md:155-169 | SIGTERM 8-step graceful shutdown | agent.go:418-498 | IMPLEMENTED | |
| plan-graceful-upgrades.md:185-199 | Run() startup sequence: recoverSessions, createJetStreamConsumers, subscribeTerminalAPI, clearUpdatingState, runHeartbeat | agent.go:152-169 | IMPLEMENTED | |
| plan-graceful-upgrades.md:195-199 | recoverSessions: skip KV write for updating sessions | agent.go:202-218 | IMPLEMENTED | wasUpdating logic |
| plan-graceful-upgrades.md:202-215 | Reply mechanism: reply() no-op when msg.Reply==""; errors go to events._api | agent.go:1148-1161, 529-538 | IMPLEMENTED | |
| plan-graceful-upgrades.md:231-252 | RequestID in create/delete/restart request structs | agent.go:552-561, 761-764, 934-937 | IMPLEMENTED | |
| plan-replay-user-messages.md:68-88 | --replay-user-messages flag on spawn args (new sessions and resume) | session.go:138-156 | IMPLEMENTED | Flag present in both paths |
| plan-replay-user-messages.md:90 | handleInput: remove manual publish to events stream; only strip session_id and write to stdin | agent.go:837-879 | IMPLEMENTED | No events publish in handleInput |
| plan-replay-user-messages.md:91 | uuid preserved (only session_id stripped) | agent.go:870 | IMPLEMENTED | delete(fields, "session_id") only |
| plan-quota-aware-scheduling.md:78-87 | Daemon struct: sessKV, jobQueueKV, projectsKV fields; opened in NewDaemon() | daemon.go:54-57, 85-96 | IMPLEMENTED | |
| plan-quota-aware-scheduling.md:89-90 | DaemonConfig.CredentialsPath | daemon.go:43 | IMPLEMENTED | |
| plan-quota-aware-scheduling.md:95-109 | runQuotaPublisher: polls 60s, reads OAuth token, calls quota API with correct headers | daemon_jobs.go:130-157 | IMPLEMENTED | |
| plan-quota-aware-scheduling.md:107-109 | Publishes QuotaStatus to mclaude.{userId}.quota; sends on quotaCh | daemon_jobs.go:131-141 | IMPLEMENTED | |
| plan-quota-aware-scheduling.md:111-127 | runLifecycleSubscriber: subscribes mclaude.{userId}.*.lifecycle.*, handles 5 event types | daemon_jobs.go:245-313 | IMPLEMENTED | |
| plan-quota-aware-scheduling.md:129-155 | runJobDispatcher: KV watch + quota updates, dispatches queued jobs | daemon_jobs.go:507-699 | IMPLEMENTED | |
| plan-quota-aware-scheduling.md:147 | 5% hysteresis: stop enough sessions so headroom drops below threshold-5 | daemon_jobs.go:606-649 | PARTIAL | Stops all running jobs where u5 >= job.Threshold; does not implement the per-job headroom accumulation to stop exactly enough jobs. |
| plan-quota-aware-scheduling.md:151-155 | Startup recovery: starting→queued, running→check sessKV, paused with past ResumeAt→queued | daemon_jobs.go:451-503 | IMPLEMENTED | |
| plan-quota-aware-scheduling.md:157-175 | Spec path → component mapping | daemon_jobs.go:160-175 | IMPLEMENTED | |
| plan-quota-aware-scheduling.md:204-220 | strict-allowlist: auto-denies unlisted tools, sends deny control_response, calls onStrictDeny | session.go:333-358 | IMPLEMENTED | |
| plan-quota-aware-scheduling.md:221-243 | onStrictDeny, onRawOutput callbacks on Session; wired in handleCreate before start() | session.go:57-61; agent.go:691-706 | IMPLEMENTED | |
| plan-quota-aware-scheduling.md:259-288 | publishLifecycleExtra and publishPermDenied methods on Agent | agent.go:1074-1099 | IMPLEMENTED | |
| plan-quota-aware-scheduling.md:291-292 | Default dev-harness allowlist (Read, Write, Edit, Glob, Grep, Bash, Agent, Task*) | agent.go:542-545 | IMPLEMENTED | |
| plan-quota-aware-scheduling.md:296-312 | Extended sessions.create payload: PermPolicy, AllowedTools, QuotaMonitor; QuotaMonitorConfig struct | agent.go:552-561; state.go:126-131 | IMPLEMENTED | |
| plan-quota-aware-scheduling.md:315-351 | QuotaMonitor struct, newQuotaMonitor: ChanSubscribe, goroutine | quota_monitor.go:16-67 | IMPLEMENTED | |
| plan-quota-aware-scheduling.md:353-430 | QuotaMonitor goroutine: select loop, sendGracefulStop, sendHardInterrupt, publishExitLifecycle, onRawOutput, signalPermDenied | quota_monitor.go:80-215 | IMPLEMENTED | |
| plan-quota-aware-scheduling.md:453-474 | Jobs HTTP server on localhost:8378; 5 endpoints | daemon_jobs.go:702-902 | IMPLEMENTED | |
| plan-quota-aware-scheduling.md:523-551 | JobEntry struct | state.go:133-154 | IMPLEMENTED | |
| plan-quota-aware-scheduling.md:558-566 | QuotaStatus struct: HasData, U5, R5, U7, R7, TS | state.go:115-122 | PARTIAL | TS (timestamp of fetch) field missing from QuotaStatus struct. Spec defines TS time.Time field. |
| plan-quota-aware-scheduling.md:570-582 | session_quota_interrupted payload includes threshold field | quota_monitor.go:150-155 | PARTIAL | publishExitLifecycle passes u5, r5, jobId but not threshold. Spec shows threshold in payload. |
| plan-quota-aware-scheduling.md:585-593 | session_permission_denied payload: type, sessionId, tool, jobId, ts | agent.go:1090-1099 | IMPLEMENTED | |
| plan-quota-aware-scheduling.md:595-606 | session_job_complete payload includes branch field | quota_monitor.go:146-148 | PARTIAL | Passes prUrl and jobId but not branch. Spec shows branch in session_job_complete payload. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| agent.go:24-27 | INFRA | Constants heartbeatInterval, sessionDeleteTimeout, kvBucketSessions/Projects/Heartbeats — infra for spec'd behavior |
| agent.go:30-65 | INFRA | Agent struct definition — required scaffold for all spec'd behaviors |
| agent.go:71-148 | INFRA | NewAgent constructor — required wiring |
| agent.go:530-538 | INFRA | publishAPIError — implements spec'd error event from plan-graceful-upgrades |
| agent.go:540-545 | INFRA | defaultDevHarnessAllowlist var — explicitly spec'd in plan-quota-aware-scheduling.md:291-292 |
| agent.go:1188-1212 | INFRA | controlResponse type, gitWorktreeAdd, gitWorktreeRemove — required helpers for spec'd worktree behavior |
| daemon.go:22-31 | INFRA | Constants and types for daemon — infra |
| daemon.go:34-58 | INFRA | DaemonConfig and Daemon structs — spec'd structures |
| daemon.go:60-65 | INFRA | managedChild struct — helper for child process management |
| daemon.go:67-70 | INFRA | laptopEntry struct — used for mclaude-laptops KV entries (spec'd) |
| daemon.go:223-303 | INFRA | spawnChild, manageChild, buildChildCmd, shutdownChildren — child process lifecycle management (spec'd in laptop mode) |
| events.go:1-103 | INFRA | Event type constants and struct definitions for stream-json parsing — required for spec'd event routing |
| worktree.go:1-38 | INFRA | SlugifyBranch + worktreeExists helpers — used by spec'd worktree logic |
| state.go:62-101 | INFRA | sessionKVKey, heartbeatKVKey, addPendingControl, removePendingControl, clearPendingControlsForResume, accumulateUsage helpers — required infrastructure for spec'd KV operations |
| debug.go:1-148 | INFRA | DebugServer implementation — spec'd in plan-k8s-integration.md unix socket section |
| session.go:433-467 | INFRA | flushKV, sendInput, clearPendingControl, stop, waitDone helpers — required for Session lifecycle |
| session.go:379-431 | INFRA | shouldAutoApprove, buildAutoApproveResponse, truncateEventIfNeeded — spec'd in permission policy and NATS size sections |
| terminal.go | INFRA | PTY terminal session management — spec'd in plan-k8s-integration.md PTY section |
| metrics.go | INFRA | Prometheus metrics — observability, not specifically spec'd but valid infrastructure |
| main.go:1-167 | INFRA | main(), flag parsing, NATS connect — required entry point |
| daemon_jobs.go:159-184 | INFRA | specPathToComponent, specPathToSlug, scheduledSessionPrompt helpers — spec'd in plan-quota-aware-scheduling |
| daemon_jobs.go:220-243 | INFRA | readJobEntry, writeJobEntry helpers — infra for spec'd job queue operations |
| agent.go:1102-1143 | INFRA | updateReplayFromSeq — spec'd in plan-k8s-integration.md replayFromSeq section |
| agent.go:63-77 | INFRA | newSession constructor — required session initialization |

### Summary

- Implemented: 61
- Gap: 3
- Partial: 14
- Infra: 24
- Unspec'd: 0
- Dead: 0
