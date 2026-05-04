# Implementation Audit: mclaude-session-agent — Round 4

## Run: 2026-05-01T00:00:00Z

**Component:** mclaude-session-agent
**Root:** /Users/rsong/work/mclaude/mclaude-session-agent
**Specs:** docs/mclaude-session-agent/spec-session-agent.md, docs/spec-state-schema.md, docs/spec-nats-data-taxonomy.md
**ADRs evaluated:** ADR-0005 (accepted), ADR-0044 (accepted), ADR-0054 (accepted), ADR-0053 (accepted), ADR-0058 (accepted), ADR-0035 (accepted), ADR-0051 (implemented), ADR-0024 (implemented), ADR-0043 (implemented)

**Previous round (R3) gaps verified:**
1. CLICapabilities in KV — **FIXED**: `DriverCapabilities drivers.CLICapabilities` field added to SessionState (state.go:52-56), populated on init event in handleSideEffect (session.go) via `s.state.DriverCapabilities = s.driver.Capabilities()`.
2. JSONL cleanup — **FIXED**: `runJSONLCleanup` goroutine added (fswatch.go:17-48), runs daily, deletes files >90 days.
3. Stub drivers — **FIXED**: DroidDriver (droid.go), DevinACPDriver (devin.go), GenericTerminalDriver (generic_terminal.go) all implemented as stubs with proper Capabilities() returning backend-specific boolean flags.

---

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-session-agent:1 | "per-project process supervisor that manages multiple concurrent CLI sessions" | agent.go:30-70 (Agent struct with sessions map) | IMPLEMENTED | — | Agent holds map[string]*Session |
| spec-session-agent:3 | "pluggable driver/adapter pattern (ADR-0005)" | internal/drivers/driver.go, registry.go | IMPLEMENTED | — | CLIDriver interface + DriverRegistry |
| spec-session-agent:5 | "supports multiple CLI backends: Claude Code, Factory Droid, Devin ACP, Gemini CLI, and a GenericTerminal fallback" | internal/drivers/*.go, agent.go:130-137 | PARTIAL | SPEC→FIX | Gemini CLI mentioned in opening paragraph but no driver stub exists. The Driver Implementations table (4 rows) omits Gemini. Opening paragraph should match the table. |
| spec-session-agent:7 | "operates as a standalone per-project process in all deployment modes" | main.go:92-200 | IMPLEMENTED | — | Standalone mode with --project-id |
| spec-session-agent:9 | "HOST_SLUG is required configuration — sourced from HOST_SLUG env var" | main.go:107-131 | IMPLEMENTED | — | Fallback chain: flag → env → symlink → hostname |
| spec-session-agent:12 | "Absence is fatal at startup" | main.go:131 | IMPLEMENTED | — | `log.Fatal().Msg("FATAL: HOST_SLUG required")` |
| spec-session-agent:dep | "Daemon mode deprecation (ADR-0058)" | daemon.go, daemon_jobs.go, main.go:141-176 | IMPLEMENTED | — | --daemon flag still works, routes to Daemon.Run() |
| spec-session-agent:cfg | Configuration table (--nats-url, --user-id, etc.) | main.go:85-112 | IMPLEMENTED | — | All flags defined with env fallbacks |
| spec-session-agent:health | "--health checks process alive and NATS connection" | main.go:41-53 | IMPLEMENTED | — | natsProbeConnect + exit 0 |
| spec-session-agent:ready | "--ready attempts NATS connection and verifies Claude Code can be spawned" | main.go:55-72 | IMPLEMENTED | — | NATS probe + exec.LookPath |
| spec-session-agent:driver-if | "CLIDriver interface with Backend/DisplayName/Capabilities/Launch/Resume/SendMessage/SendPermissionResponse/UpdateConfig/Interrupt/ReadEvents" | internal/drivers/driver.go:85-122 | IMPLEMENTED | — | All methods defined |
| spec-session-agent:registry | "DriverRegistry maps CLIBackend enum values to driver instances" | internal/drivers/registry.go | IMPLEMENTED | — | Register/Get/GetOrDefault |
| spec-session-agent:cc-drv | "ClaudeCodeDriver spawns claude --print --verbose --output-format stream-json" | internal/drivers/claudecode.go:62-80 | IMPLEMENTED | — | buildArgs constructs correct flags |
| spec-session-agent:droid-drv | "DroidDriver — JSON-RPC 2.0 — stub" | internal/drivers/droid.go | IMPLEMENTED | — | Stub returns "not yet implemented" |
| spec-session-agent:devin-drv | "DevinACPDriver — ACP — stub" | internal/drivers/devin.go | IMPLEMENTED | — | Stub returns "not yet implemented" |
| spec-session-agent:gen-drv | "GenericTerminalDriver — PTY raw I/O — hasEventStream: false" | internal/drivers/generic_terminal.go | IMPLEMENTED | — | HasEventStream: false in Capabilities() |
| spec-session-agent:stream | "agent does not create streams — CP creates per-user streams" | agent.go:108-111 (comment only) | IMPLEMENTED | — | No stream creation code; only OrderedConsumer calls |
| spec-session-agent:consumers | "ordered push consumers on MCLAUDE_SESSIONS_{uslug}" | agent.go:204-242 | IMPLEMENTED | — | Two consumers: cmdMsgs (create/input/delete/config), ctlMsgs (control.>) |
| spec-session-agent:kv-write | "KV_mclaude-sessions-{uslug} (write) — key: hosts.{hslug}.projects.{pslug}.sessions.{sslug}" | agent.go:writeSessionKV, state.go:sessionKVKey | IMPLEMENTED | — | Uses subj.SessionsKVKey() |
| spec-session-agent:kv-proj | "KV_mclaude-projects-{uslug} (write) — Updates project state (e.g., clear importRef)" | import.go:clearImportRef | IMPLEMENTED | — | Reads/updates projKV |
| spec-session-agent:hosts-kv | "KV_mclaude-hosts (read) — Shared bucket" | agent.go:104-113 | IMPLEMENTED | — | hostsKV opened read-only; nil on STREAM.INFO failure |
| spec-session-agent:pub-events | "Session events to sessions.{sslug}.events" | session.go:start()→stdout router | IMPLEMENTED | — | Publishes to eventSubject with full slug path |
| spec-session-agent:pub-lifecycle | "Lifecycle events to sessions.{sslug}.lifecycle.{eventType}" | agent.go:publishLifecycle* family | IMPLEMENTED | — | Uses subj.UserHostProjectSessionsLifecycle |
| spec-session-agent:pub-api-error | "API error events to sessions._api" | agent.go:publishAPIError | IMPLEMENTED | — | Publishes {type,request_id,operation,error} |
| spec-session-agent:pub-quota | "Quota status to mclaude.users.{uslug}.quota" | agent.go:runQuotaPublisher | IMPLEMENTED | — | 60s polling, publishes QuotaStatus |
| spec-session-agent:sub-terminal | "Terminal API via core NATS on api.terminal.{create,delete,resize}" | agent.go:subscribeTerminalAPI | IMPLEMENTED | — | 3 core NATS subscriptions |
| spec-session-agent:sub-quota | "Quota status via core NATS on mclaude.users.{uslug}.quota" | quota_monitor.go:newQuotaMonitor | IMPLEMENTED | — | nc.ChanSubscribe to quota subject |
| spec-session-agent:sub-designate | "designate-quota-publisher via core NATS" | agent.go:subscribeManageAPI | IMPLEMENTED | — | Subscribes to manage.designate-quota-publisher |
| spec-session-agent:unix-sock | "Debug socket at /tmp/mclaude-session-{sessionId}.sock" | debug.go | IMPLEMENTED | — | NDJSON protocol, broadcast + stdin forwarding |
| spec-session-agent:metrics | "mclaude_active_sessions, mclaude_events_published_total, mclaude_nats_reconnects_total, mclaude_claude_restarts_total" | metrics.go:17-38 | IMPLEMENTED | — | All 4 metrics registered |
| spec-session-agent:otel | "OpenTelemetry trace spans emitted for NATS publish, KV write, session lifecycle, Claude process spawn" | metrics.go:NATSPublishSpan,KVWriteSpan,SessionSpan,ClaudeSpawnSpan | IMPLEMENTED | — | Spans created with proper attributes |
| spec-session-agent:traceparent | "W3C traceparent headers propagated via NATS message headers" | metrics.go:InjectTraceparent,ExtractTraceparent | IMPLEMENTED | — | Uses otel propagation.TraceContext |
| spec-session-agent:state-machine | "Session State Machine — idle/running/requires_action + KV status enum" | events.go:29-51 | IMPLEMENTED | — | All states defined as constants |
| spec-session-agent:create | "Session creation lifecycle (steps 1-11)" | agent.go:handleCreate | IMPLEMENTED | — | All 11 steps present |
| spec-session-agent:create-backend | "Look up CLIDriver from DriverRegistry based on backend field (default: claude_code)" | agent.go:handleCreate (driver lookup) | IMPLEMENTED | — | GetOrDefault with fallback |
| spec-session-agent:recovery | "KV-based recovery mechanism" | agent.go:recoverSessions | IMPLEMENTED | — | Watches project-scoped keys, resumes each |
| spec-session-agent:recovery-updating | "Sessions in updating status are resumed but KV entry stays as updating" | agent.go:recoverSessions (wasUpdating), clearUpdatingState | IMPLEMENTED | — | pendingUpdatingIDs tracks, clearUpdatingState writes idle |
| spec-session-agent:resume-init | "On init event: set status: 'running'" | session.go:handleSideEffect SubtypeInit | PARTIAL | SPEC→FIX | Code sets StateIdle per ADR-0051 comment and state machine diagram ("create → idle ← resume"). Spec §Resumption 2d says "running" but conflicts with its own state machine which shows idle after resume. Spec should say "idle". |
| spec-session-agent:startup-timeout | "Sessions that fail to start within 30s: mark status: 'failed'" | session.go:start() (go func with startupTimeout) | IMPLEMENTED | — | 30s timeout → StateFailed |
| spec-session-agent:delete-interactive | "Interactive session delete: remove from map, interrupt+wait 10s, remove worktree, delete KV, publish session_stopped" | agent.go:handleDelete | IMPLEMENTED | — | Full flow with exitCode |
| spec-session-agent:delete-quota | "Quota-managed delete: stop monitor, interrupt+wait, skip schedule/ worktree, tombstone KV, publish session_job_cancelled" | agent.go:handleDelete (isQuotaManaged branch) | IMPLEMENTED | — | schedule/ prefix skip, KV delete, cancelled event |
| spec-session-agent:restart | "Restart: publish session_restarting, stop, clear pending, respawn via driver.Resume, publish session_resumed" | agent.go:handleRestart | IMPLEMENTED | — | Full restart flow |
| spec-session-agent:import | "Import handler: read project KV, download archive, unpack, clear importRef, signal completion" | import.go:checkImport | IMPLEMENTED | — | Pre-signed URL, tar.gz unpack, KV collision handling |
| spec-session-agent:import-collision | "If SessionState KV entry already exists, skip the session with a warning log" | import.go:createImportedSessionKVEntry | IMPLEMENTED | — | KV get check before put |
| spec-session-agent:import-fail | "On unpack fail: report session_import_failed, leave importRef set" | import.go:checkImport | IMPLEMENTED | — | publishImportFailed + early return |
| spec-session-agent:import-retry | "Import S3 download URL expired: request new URL, retry" | import.go:checkImport (retry block) | IMPLEMENTED | — | Second request + download on failure |
| spec-session-agent:attachment-dl | "Attachment download: request pre-signed URL from CP, download from S3" | attachment.go:downloadAttachment | IMPLEMENTED | — | NATS request/reply + S3 GET |
| spec-session-agent:attachment-ul | "Attachment upload: request pre-signed upload URL, upload to S3, confirm" | attachment.go:uploadAttachment | IMPLEMENTED | — | 3-step: upload URL → PUT → confirm |
| spec-session-agent:attachment-retry | "Attachment download/upload retry on URL expiry" | attachment.go:downloadAttachment,uploadAttachment | IMPLEMENTED | — | Both have retry paths |
| spec-session-agent:fswatch | "fsnotify watcher on session data directory for new JSONL files" | fswatch.go:watchSessionDataDir | IMPLEMENTED | — | Create/Rename events → handleNewJSONLFile |
| spec-session-agent:fswatch-meta | "Extract session metadata from first few lines of JSONL" | fswatch.go:extractJSONLSessionMeta | IMPLEMENTED | — | Reads first 10 lines for session_id, model, branch |
| spec-session-agent:fswatch-dedup | "Check if SessionState KV entry already exists — skip if so" | fswatch.go:handleNewJSONLFile | IMPLEMENTED | — | KV get check before creating |
| spec-session-agent:event-router | "Event router reads DriverEvents from driver.ReadEvents() channel" | session.go:start() (eventsCh goroutine) | IMPLEMENTED | — | Reads from drivers.DriverEvent channel |
| spec-session-agent:bg-agent-track | "inFlightBackgroundAgents: +1 on Agent(run_in_background), -1 on task-notification" | session.go:updateInFlightBackgroundAgents | IMPLEMENTED | — | Both increment and decrement paths |
| spec-session-agent:bg-shell-track | "Two-phase shell tracking: pending → promoted → removed" | session.go:updateInFlightShells | IMPLEMENTED | — | Phase 1 (pendingShells), Phase 2 (inFlightShells), removal |
| spec-session-agent:truncate | "Truncates events exceeding 8 MB NATS payload" | session.go:truncateEventIfNeeded | IMPLEMENTED | — | maxEventBytes = 8*1024*1024 |
| spec-session-agent:input-routing | "Input routing: type 'message' → driver.SendMessage, 'permission_response' → driver.SendPermissionResponse, 'skill_invoke' → driver.SendMessage" | agent.go:handleInput | PARTIAL | CODE→FIX | Input is forwarded via sess.sendInput (raw stdinCh write), not through CLIDriver.SendMessage/SendPermissionResponse. Works for claude_code but bypasses the driver interface. Config and interrupt routing similarly bypass driver.UpdateConfig/Interrupt. |
| spec-session-agent:perm-policy | "Four policies: managed, auto, allowlist, strict-allowlist" | state.go:PermissionPolicy*, session.go:shouldAutoApprove | IMPLEMENTED | — | All four policies with correct behavior |
| spec-session-agent:strict-reject | "If allowedTools is empty on strict-allowlist, reject sessions.create" | agent.go:handleCreate (toolList check) | IMPLEMENTED | — | Rejects with lifecycle.error event |
| spec-session-agent:strict-deny | "strict-allowlist: auto-deny + onStrictDeny callback → lifecycle event + permDeniedCh" | session.go:handleSideEffect (strict-allowlist branch) | IMPLEMENTED | — | Sends deny response, calls onStrictDeny |
| spec-session-agent:auth | "HTTP challenge-response: NKey pair, challenge, verify, JWT" | agent_auth.go | IMPLEMENTED | — | Full Authenticate flow with retry loop |
| spec-session-agent:auth-refresh | "5-minute TTL, proactive refresh via HTTP challenge-response" | agent_auth.go:StartRefreshLoop | IMPLEMENTED | — | Ticker at TTL-60s, permViolation immediate refresh |
| spec-session-agent:auth-nkey-file | "Agent exposes public key via file or stdout" | agent_auth.go:WritePublicKeyToFile, main.go:152-160 | IMPLEMENTED | — | File write or stdout print |
| spec-session-agent:git-creds | "Git credential helper setup (K8s mode): symlink PVC, merge managed tokens, register helpers" | gitcreds.go:CredentialManager.Setup | IMPLEMENTED | — | Full 3-step setup |
| spec-session-agent:git-identity | "Resolve GIT_IDENTITY_ID to username/host via Secret mount" | gitcreds.go:resolveIdentityFromManaged | IMPLEMENTED | — | Reads conn-{id}-username, finds host |
| spec-session-agent:git-normalize | "Normalize SCP-style git URLs to HTTPS for hosts with registered credential helpers" | gitcreds.go:NormalizeGitURL | IMPLEMENTED | — | git@host:path → https://host/path |
| spec-session-agent:init-repo | "Clone bare repo from GIT_URL or init scratch repo" | gitcreds.go:InitRepo | IMPLEMENTED | — | Clone, fetch, or initScratchRepo |
| spec-session-agent:worktree | "Worktree management: create, check collision, remove" | agent.go:gitWorktreeAdd,gitWorktreeRemove, worktree.go | IMPLEMENTED | — | git worktree add/remove --force |
| spec-session-agent:worktree-cred | "Refreshes credentials before each git worktree operation" | agent.go:gitWorktreeAdd (credMgr.RefreshIfChanged) | IMPLEMENTED | — | Called before git worktree add |
| spec-session-agent:compact | "compact_boundary event → query stream for last seq → write replayFromSeq to KV" | agent.go:updateReplayFromSeq | IMPLEMENTED | — | Stream.Info → LastSeq → KV write |
| spec-session-agent:quota-mon | "QuotaMonitor: soft threshold, hard budget, turn-end detection" | quota_monitor.go | IMPLEMENTED | — | Full implementation with all 5 select cases |
| spec-session-agent:quota-msg | "Messages published to NATS sessions.{sslug}.input with standard envelope" | quota_monitor.go:publishToSessionsInput | IMPLEMENTED | — | {id, ts, type, text} envelope |
| spec-session-agent:quota-soft | "Soft threshold breached: inject 'MCLAUDE_STOP: quota_soft'" | quota_monitor.go:run (soft threshold branch) | IMPLEMENTED | — | sendGracefulStop publishes to NATS |
| spec-session-agent:quota-hard | "Hard budget: interrupt on stdinCh when outputTokensSinceSoftMark >= hardHeadroomTokens" | quota_monitor.go:onRawOutput (hard budget check) | IMPLEMENTED | — | sendHardInterrupt to sess.stdinCh |
| spec-session-agent:quota-zero | "When hardHeadroomTokens is 0, hard interrupt fires immediately after soft marker" | quota_monitor.go:run (hardHeadroomTokens == 0 check) | IMPLEMENTED | — | Immediate quota_hard + interrupt |
| spec-session-agent:turn-end | "handleTurnEnd: quota_soft → paused, quota_hard → paused, empty → completed" | quota_monitor.go:handleTurnEnd | IMPLEMENTED | — | All 3 branches with lifecycle events |
| spec-session-agent:subprocess-exit | "handleSubprocessExit: if !terminalEventPublished → session_job_failed" | quota_monitor.go:handleSubprocessExit | IMPLEMENTED | — | Checks flag, publishes failed |
| spec-session-agent:quota-priority | "turnEndedCh is checked before doneCh via nested select" | quota_monitor.go:run (priority drain) | IMPLEMENTED | — | Non-blocking select on turnEndedCh before full select |
| spec-session-agent:quota-publisher | "CP designates one agent per user as quota publisher" | agent.go:handleDesignateQuotaPublisher, startQuotaPublisher | IMPLEMENTED | — | Start/stop via designation message |
| spec-session-agent:graceful | "Graceful shutdown: write updating, stop cmd consumer, drain subs, poll drain, shell-killed, stop ctl, publish upgrading, exit" | agent.go:gracefulShutdown | IMPLEMENTED | — | All 9 steps present |
| spec-session-agent:graceful-interrupt | "Auto-interrupt sessions stuck in requires_action" | agent.go:gracefulShutdown (step 5 loop) | IMPLEMENTED | — | sendInterrupt on StateRequiresAction |
| spec-session-agent:graceful-shell | "Shell-killed notifications: <task-notification status=killed> XML" | agent.go:publishShellKilledNotifications | IMPLEMENTED | — | XML format with task-id, tool-use-id, output-file, status, summary |
| spec-session-agent:nats-reconnect | "NATS client auto-reconnects; MaxReconnects(-1), RetryOnFailedConnect(true)" | main.go:natsConnectWithAuth | IMPLEMENTED | — | Both options set |
| spec-session-agent:kv-not-found | "KV bucket not found on startup: Fatal" | agent.go:NewAgent (sessKV, projKV error paths) | IMPLEMENTED | — | Fatal error on missing buckets |
| spec-session-agent:crash-watch | "watchSessionCrash detects exit via doneCh, publishes session_failed, auto-restarts" | agent.go:watchSessionCrash | IMPLEMENTED | — | Full crash → restart → new crash watcher loop |
| spec-session-agent:terminal | "Terminal sessions: PTY shell, 4 KB chunks, core NATS" | terminal.go | IMPLEMENTED | — | pty.Start, 4096 byte reads, NATS pub/sub |
| spec-session-agent:terminal-resize | "Resize events forwarded to PTY" | terminal.go:resize, agent.go:handleTerminalResize | IMPLEMENTED | — | pty.Setsize |
| spec-session-agent:liveness | "Liveness signalled by NATS connection itself; no periodic heartbeat" | agent.go (no heartbeat code) | IMPLEMENTED | — | No heartbeat publish, $SYS handled by CP |
| spec-session-agent:lifecycle-created | "session_created payload includes branch field" | agent.go:publishLifecycleWithBranch | IMPLEMENTED | — | branch field included |
| spec-session-agent:lifecycle-stopped | "session_stopped payload includes exitCode field" | agent.go:publishLifecycleWithExitCode | IMPLEMENTED | — | exitCode field included |
| spec-session-agent:jsonl-cleanup | "Daily cleanup deletes JSONL files older than 90 days" | fswatch.go:runJSONLCleanup, doJSONLCleanup | IMPLEMENTED | — | 90-day cutoff, daily ticker |
| spec-session-agent:jsonl-purge | "purges session files for sessions not present in KV_mclaude-sessions-{uslug}" | fswatch.go:doJSONLCleanup | GAP | CODE→FIX | Code only does age-based deletion. Does not check KV for orphaned session files. |
| spec-state-schema:caps | "capabilities contains boolean flags (hasThinking, etc.); tools, skills, agents are top-level fields" | state.go:SessionState | GAP | CODE→FIX | Code keeps tools/skills/agents nested under `capabilities` struct (JSON: `capabilities.tools`). Spec + state schema require them as top-level JSON fields. DriverCapabilities (boolean flags) is under `driverCapabilities` but spec says `capabilities`. |
| spec-session-agent:caps-kv | "CLICapabilities is populated from the init event" | session.go:handleSideEffect SubtypeInit | IMPLEMENTED | — | `s.state.DriverCapabilities = s.driver.Capabilities()` |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-30 | INFRA | Package declaration, imports, flag parsing setup |
| main.go:32-73 | IMPLEMENTED | Health/readiness probes per spec |
| main.go:74-200 | IMPLEMENTED | CLI flags, slug resolution, NATS connection, agent creation per spec |
| agent.go:1-30 | INFRA | Imports |
| agent.go:30-70 | IMPLEMENTED | Agent struct fields — all documented in spec |
| agent.go:73-140 | IMPLEMENTED | NewAgent: KV bucket access, driver registry setup |
| agent.go:142-200 | IMPLEMENTED | Run: import check, recovery, consumers, subscriptions, watcher, cleanup |
| agent.go:200-260 | IMPLEMENTED | createJetStreamConsumers: two ordered push consumers |
| agent.go:262-300 | IMPLEMENTED | runConsumer, jsToNatsMsg, dispatchCmd, dispatchCtl |
| agent.go:302-330 | IMPLEMENTED | clearUpdatingState |
| agent.go:332-500 | IMPLEMENTED | gracefulShutdown: full 9-step sequence |
| agent.go:500-560 | IMPLEMENTED | publishShellKilledNotifications |
| agent.go:560-690 | IMPLEMENTED | subscribeManageAPI, handleDesignateQuotaPublisher, startQuotaPublisher, runQuotaPublisher |
| agent.go:690-720 | IMPLEMENTED | subscribeTerminalAPI |
| agent.go:720-740 | IMPLEMENTED | publishAPIError |
| agent.go:742-755 | UNSPEC'd | `defaultDevHarnessAllowlist` variable — retained as convenience constant per comment but no spec reference for retaining it. Could be removed. |
| agent.go:757-1000 | IMPLEMENTED | handleCreate: full session creation lifecycle |
| agent.go:1000-1100 | IMPLEMENTED | handleDelete: interactive and quota-managed paths |
| agent.go:1100-1300 | IMPLEMENTED | handleInput: routing, session_id strip, control_response clearing |
| agent.go:1300-1400 | IMPLEMENTED | handleControl: control_response routing, broadcast to all sessions |
| agent.go:1400-1460 | IMPLEMENTED | handleConfig: config updates forwarded to session |
| agent.go:1460-1480 | IMPLEMENTED | sessionBySlug |
| agent.go:1480-1600 | IMPLEMENTED | handleRestart |
| agent.go:1600-1700 | IMPLEMENTED | writeSessionKV with slug resolution |
| agent.go:1700-1914 | IMPLEMENTED | publishLifecycle* family, watchSessionCrash, updateReplayFromSeq, reply, worktreeInUse, sessionForRequest, gitWorktreeAdd/Remove, terminal handlers |
| session.go:1-70 | INFRA | Type definitions for pendingShell, inFlightShell, backgroundTaskIdRe, sanitizePath, shellOutputPath |
| session.go:70-200 | IMPLEMENTED | Session struct, newSession, getState, stopAndWait |
| session.go:200-370 | IMPLEMENTED | start(): driver resolution, spawn, stdin serializer, stdout router, startup timeout |
| session.go:370-460 | IMPLEMENTED | updateInFlightBackgroundAgents |
| session.go:460-640 | IMPLEMENTED | updateInFlightShells: two-phase tracking |
| session.go:640-730 | IMPLEMENTED | removeShellByTaskNotification |
| session.go:730-870 | IMPLEMENTED | handleSideEffect: init, state_changed, control_request, result, compact_boundary |
| session.go:870-920 | IMPLEMENTED | shouldAutoApprove, buildAutoApproveResponse, truncateEventIfNeeded |
| session.go:920-960 | IMPLEMENTED | flushKV, sendInput, clearPendingControl, stop, waitDone, exitCode |
| state.go | IMPLEMENTED | SessionState struct, Capabilities, UsageStats, sessionKVKey, accumulateUsage, clearPendingControlsForResume |
| state.go:JobEntry | UNSPEC'd | JobEntry struct — daemon-mode only, retained during deprecation period. Matches daemon spec section. |
| events.go | IMPLEMENTED | Event type constants, parse helpers, event structs |
| quota_monitor.go | IMPLEMENTED | Full QuotaMonitor implementation |
| import.go | IMPLEMENTED | Import handler: checkImport, unpackImportArchive, createImportedSessionKVEntry |
| attachment.go | IMPLEMENTED | Attachment download/upload with S3 pre-signed URLs |
| fswatch.go | IMPLEMENTED | fsnotify watcher + JSONL cleanup |
| daemon.go | IMPLEMENTED | Deprecated daemon mode (retained per ADR-0058 Phase 1-2) |
| daemon_jobs.go | IMPLEMENTED | Deprecated job dispatcher (retained per ADR-0058) |
| metrics.go | IMPLEMENTED | Prometheus metrics + OpenTelemetry spans |
| terminal.go | IMPLEMENTED | PTY terminal sessions |
| agent_auth.go | IMPLEMENTED | HTTP challenge-response auth with NKey pairs |
| debug.go | IMPLEMENTED | Unix socket debug server |
| gitcreds.go | IMPLEMENTED | Credential management, InitRepo |
| worktree.go | INFRA | SlugifyBranch, worktreeExists helper |
| shell_split.go | INFRA | Shell argument splitting |
| internal/drivers/driver.go | IMPLEMENTED | CLIDriver interface, CLICapabilities, Process, LaunchOptions, DriverEvent |
| internal/drivers/registry.go | IMPLEMENTED | DriverRegistry with GetOrDefault |
| internal/drivers/claudecode.go | IMPLEMENTED | ClaudeCodeDriver: spawn, ReadEvents, buildArgs |
| internal/drivers/droid.go | IMPLEMENTED | DroidDriver stub with correct Capabilities |
| internal/drivers/devin.go | IMPLEMENTED | DevinACPDriver stub with correct Capabilities |
| internal/drivers/generic_terminal.go | IMPLEMENTED | GenericTerminalDriver stub with HasEventStream:false |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| All spec lines | Session agent implementation | Test files exist (*_test.go) | No integration tests with real NATS/JetStream | UNIT_ONLY | No integration test infrastructure observed for session-agent. Unit tests mock NATS. |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| BUG-002 | PTT race condition | N/A | Component: spa — not mclaude-session-agent |
| BUG-003 | Initial message injection race | N/A | Component: spa — not mclaude-session-agent |
| BUG-005 | Getting Started canned text | N/A | Component: spa — not mclaude-session-agent |

No bugs filed for mclaude-session-agent.

### Summary

- Implemented: 68
- Gap: 2
- Partial: 3
- Infra: 6
- Unspec'd: 2
- Dead: 0
- Tested: 0
- Unit only: 1 (aggregate)
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0

**Previous R3 gaps — all verified FIXED:**
1. ✅ CLICapabilities in KV: `DriverCapabilities` field added and populated on init
2. ✅ JSONL cleanup: `runJSONLCleanup` goroutine runs daily with 90-day cutoff
3. ✅ Stub drivers: All three stubs exist with proper Capabilities()

**Remaining gaps:**

GAP [CODE→FIX]: "purges session files for sessions not present in KV_mclaude-sessions-{uslug}" → JSONL cleanup (fswatch.go:doJSONLCleanup) only does age-based deletion; does not check KV for orphaned sessions.

GAP [CODE→FIX]: "capabilities contains boolean flags; tools, skills, agents are top-level fields" → Code keeps tools/skills/agents nested under `capabilities` struct (JSON `capabilities.tools`). `DriverCapabilities` is at `driverCapabilities` instead of being part of `capabilities`. State schema and spec both require a different JSON layout.

PARTIAL [SPEC→FIX]: "supports multiple CLI backends: Claude Code, Factory Droid, Devin ACP, Gemini CLI, and a GenericTerminal fallback" → Gemini CLI mentioned in opening paragraph but no driver stub exists. The Driver Implementations table (4 rows) omits Gemini. Opening paragraph should match the table.

PARTIAL [CODE→FIX]: "Input routing: type 'message' → driver.SendMessage, 'permission_response' → driver.SendPermissionResponse" → Input/config/interrupt routing bypasses CLIDriver methods, writing raw JSON to stdinCh instead. Works for claude_code but doesn't use the driver interface.

PARTIAL [SPEC→FIX]: "On init event: set status: 'running'" → Code sets StateIdle per ADR-0051 and the state machine diagram ("create → idle ← resume"). Spec §Resumption 2d should say "idle" to match.
