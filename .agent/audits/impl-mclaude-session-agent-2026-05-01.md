# Implementation Audit: mclaude-session-agent

## Run: 2026-05-01T00:00:00Z

**Component:** mclaude-session-agent
**Component root:** /Users/rsong/work/mclaude/mclaude-session-agent
**Spec:** docs/mclaude-session-agent/spec-session-agent.md
**Cross-cutting specs:** docs/spec-state-schema.md, docs/spec-nats-data-taxonomy.md
**ADRs evaluated:** ADR-0005 (pluggable CLI), ADR-0044 (scheduling/quota), ADR-0051 (resume KV state), ADR-0053 (session import), ADR-0054 (NATS permission tightening), ADR-0058 (BYOH redesign)

### Spec fix verification

1. "Gemini CLI" removed from opening paragraph: **CONFIRMED** — spec lists "Claude Code, Factory Droid, Devin ACP, and a GenericTerminal fallback"
2. Resumption 2d says "idle" not "running": **CONFIRMED** — spec says `status: "idle"`

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec:§Role ¶1 | "per-project process supervisor that manages multiple concurrent CLI sessions…pluggable driver/adapter pattern (ADR-0005)" | agent.go:Agent struct + driverRegistry field | IMPLEMENTED | — | Agent struct holds sessions map keyed by sessionID, driverRegistry maps backends to drivers |
| spec:§Role ¶2 | "agent supports multiple CLI backends: Claude Code, Factory Droid, Devin ACP, and a GenericTerminal fallback" | agent.go:NewAgent, internal/drivers/*.go | IMPLEMENTED | — | Four drivers registered: ClaudeCodeDriver, DroidDriver, DevinACPDriver, GenericTerminalDriver |
| spec:§Role ¶3 | "operates as a standalone per-project process in all deployment modes" | main.go (standalone + daemon paths) | IMPLEMENTED | — | Standalone is default; daemon is deprecated path |
| spec:§Deployment/Standalone | "single-container Deployment per project" | main.go:standalone mode path | IMPLEMENTED | — | K8s mode with --mode k8s flag |
| spec:§Configuration | All knobs: --nats-url, --nats-creds, --user-id, --project-id, --user-slug, --project-slug, --host, --claude-path, --data-dir, --mode, --daemon, --refresh-url, LOG_LEVEL, METRICS_ADDR, etc. | main.go:flag declarations | IMPLEMENTED | — | All flags and env vars present; HOST_SLUG fallback chain implemented (flag→env→active-host symlink→HOSTNAME→os.Hostname→fatal) |
| spec:§Configuration | "HOSTNAME / --hostname: Dead code — daemon mode only" | main.go:flagHostname | IMPLEMENTED | — | Present but only used in daemon mode DaemonConfig |
| spec:§Configuration | "MACHINE_ID / --machine-id: Dead code — daemon mode only" | main.go:flagMachineID | IMPLEMENTED | — | Same as above |
| spec:§Health and Readiness | "--health checks process alive and NATS connection status" | main.go:--health case | IMPLEMENTED | — | Connects to NATS, exits 0 on success |
| spec:§Health and Readiness | "--ready attempts NATS connection and verifies Claude Code can be spawned" | main.go:--ready case | IMPLEMENTED | — | NATS connect + exec.LookPath(claudeBin) |
| spec:§CLIDriver Interface | Interface with Backend(), DisplayName(), Capabilities(), Launch(), Resume(), SendMessage(), SendPermissionResponse(), UpdateConfig(), Interrupt(), ReadEvents() | internal/drivers/driver.go | IMPLEMENTED | — | All methods present |
| spec:§CLIDriver Interface | "ReadEvents blocks, reading process stdout and emitting canonical SessionEvent structs on the channel" | internal/drivers/driver.go:ReadEvents signature uses `chan<- DriverEvent` | PARTIAL | SPEC→FIX | Spec says `chan<- SessionEvent`, code uses `chan<- DriverEvent`. Types serve same purpose; spec should use actual type name `DriverEvent` |
| spec:§DriverRegistry | "registry.Register(NewClaudeCodeDriver()); registry.Register(NewDroidDriver()); registry.Register(NewDevinDriver()); registry.Register(NewGenericTerminalDriver())" | agent.go:NewAgent | IMPLEMENTED | — | All four registered; spec uses `NewDevinDriver()` but code uses `NewDevinACPDriver()` — trivial naming |
| spec:§Driver Implementations | ClaudeCodeDriver: "Spawns `claude --print --verbose --output-format stream-json`" | internal/drivers/claudecode.go:buildArgs | IMPLEMENTED | — | Exact flags match |
| spec:§Driver Implementations | DroidDriver, DevinACPDriver, GenericTerminalDriver: stub implementations | internal/drivers/droid.go, devin.go, generic_terminal.go | IMPLEMENTED | — | All return "not yet implemented" from Launch/Resume per ADR-0005 phased rollout |
| spec:§NATS JetStream | "agent does not create streams — control-plane creates per-user streams" | agent.go:NewAgent comment + createJetStreamConsumers | IMPLEMENTED | — | Agent only creates ordered push consumers |
| spec:§NATS JetStream | "ordered push consumers on MCLAUDE_SESSIONS_{uslug}" | agent.go:createJetStreamConsumers | IMPLEMENTED | — | Two consumers: cmdMsgs (sessions.create + per-session commands), ctlMsgs (sessions.*.control.>) |
| spec:§NATS JetStream | "DeliverNew — agent processes commands as they arrive, no replay" | agent.go:DeliverPolicy: jetstream.DeliverNewPolicy | IMPLEMENTED | — | Both consumers use DeliverNew |
| spec:§KV Buckets (RW) | "KV_mclaude-sessions-{uslug} — writes session state on creation, every state change" | agent.go:writeSessionKV, state.go:sessionKVKey | IMPLEMENTED | — | Per-user bucket naming, key format: hosts.{hslug}.projects.{pslug}.sessions.{sslug} |
| spec:§KV Buckets (RW) | "KV_mclaude-projects-{uslug} — updates project state (e.g., clear importRef)" | import.go:clearImportRef | IMPLEMENTED | — | Used in import flow |
| spec:§KV Buckets (RO) | "KV_mclaude-hosts — shared bucket, read-only" | agent.go:hostsKV field + comment about nil fallback | IMPLEMENTED | — | Non-fatal if unavailable (JWT may lack STREAM.INFO) |
| spec:§NATS Subjects (Pub) | "Session events to mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.events" | session.go:stdout router goroutine | IMPLEMENTED | — | Event subject uses slug fields |
| spec:§NATS Subjects (Pub) | "Lifecycle events to sessions.{sslug}.lifecycle.{eventType}" | agent.go:publishLifecycle + variants | IMPLEMENTED | — | Uses subj.UserHostProjectSessionsLifecycle |
| spec:§NATS Subjects (Pub) | "API error events to sessions._api" | agent.go:publishAPIError | IMPLEMENTED | — | Publishes on create/delete/restart errors |
| spec:§NATS Subjects (Pub) | "Quota status to mclaude.users.{uslug}.quota" | agent.go:runQuotaPublisher | IMPLEMENTED | — | Uses subj.UserQuota, publishes every 60s |
| spec:§NATS Subjects (Sub) | "Terminal API via core NATS subscriptions on api.terminal.{create,delete,resize}" | agent.go:subscribeTerminalAPI | IMPLEMENTED | — | Core NATS (not JetStream) |
| spec:§NATS Subjects (Sub) | "Quota publisher re-designation via manage.designate-quota-publisher" | agent.go:subscribeManageAPI + handleDesignateQuotaPublisher | IMPLEMENTED | — | Starts/stops runQuotaPublisher on designation change |
| spec:§Unix Sockets | "Each session exposes a debug socket at /tmp/mclaude-session-{sessionId}.sock" | debug.go:debugSocketFmt + NewDebugServer | IMPLEMENTED | — | NDJSON protocol, broadcasts stdout, forwards client input |
| spec:§Unix Sockets | "Publishes debug_attached and debug_detached lifecycle events" | debug.go:onAttach/onDetach callbacks → agent.go:publishLifecycle | IMPLEMENTED | — | |
| spec:§Prometheus Metrics | "mclaude_active_sessions (Gauge), mclaude_events_published_total (Counter by event_type), mclaude_nats_reconnects_total, mclaude_claude_restarts_total" | metrics.go:NewMetrics | IMPLEMENTED | — | All four metrics registered |
| spec:§Prometheus Metrics | "OpenTelemetry trace spans emitted for NATS publish, KV write, session lifecycle, Claude process spawn" | metrics.go:NATSPublishSpan, KVWriteSpan, SessionSpan, ClaudeSpawnSpan | IMPLEMENTED | — | W3C traceparent propagation via natsHeaderCarrier |
| spec:§Session State Machine | Driver-reported states: idle, running, requires_action | events.go:StateIdle, StateRunning, StateRequiresAction | IMPLEMENTED | — | |
| spec:§Session State Machine | KV status enum: pending, running, paused, requires_action, completed, stopped, cancelled, needs_spec_fix, failed, error | events.go + state.go constants | PARTIAL | SPEC→FIX | `stopped` and `error` not defined as Go constants. `stopped` is never set in KV (entries are deleted on stop). `error` is listed in enum but never used. Spec should clarify these are theoretical values or remove from enum. |
| spec:§Session Lifecycle/Creation | Steps 1-11 (generate UUID, derive branch, check collision, create worktree, write KV, apply policy, wire QuotaMonitor, start debug socket, look up driver, launch, publish lifecycle) | agent.go:handleCreate | IMPLEMENTED | — | All 11 steps present; quota fields top-level per ADR-0044 |
| spec:§Session Lifecycle/Creation | "initial lifecycle state: quota-managed sessions start as 'pending', interactive sessions start as 'idle'" | agent.go:handleCreate initialState logic | IMPLEMENTED | — | `StateIdle` or `StatusPending` based on SoftThreshold |
| spec:§Session Lifecycle/Resumption | "KV-based mechanism (ADR-0044) — no stream replay needed" | agent.go:recoverSessions | IMPLEMENTED | — | Watches KV for initial values, resumes each session |
| spec:§Session Lifecycle/Resumption 2a | "Set session KV to status: 'restarting', clear pendingControls" | agent.go:recoverSessions → clearPendingControlsForResume | IMPLEMENTED | — | |
| spec:§Session Lifecycle/Resumption 2b | "Publish session_restarting lifecycle event" | agent.go:recoverSessions → publishLifecycle(id, "session_restarting") | IMPLEMENTED | — | |
| spec:§Session Lifecycle/Resumption 2c | "Resume via driver.Resume(ctx, sessionID, opts)" | agent.go:recoverSessions → sess.start(claudePath, true, ...) | IMPLEMENTED | — | resume=true triggers driver.Resume |
| spec:§Session Lifecycle/Resumption 2d | "On init event: set status: 'idle', stateSince: now, update model/capabilities, flush to KV" | session.go:handleSideEffect SubtypeInit case | IMPLEMENTED | — | Sets StateIdle, model, tools, skills, agents, capabilities from driver |
| spec:§Session Lifecycle/Resumption 2e | "Start crash watcher goroutine (ADR-0051)" | agent.go:recoverSessions → go a.watchSessionCrash(st.ID, sess) | IMPLEMENTED | — | |
| spec:§Session Lifecycle/Resumption 2f | "Sessions in updating status are resumed but KV entry stays as updating until consumers are attached" | agent.go:recoverSessions wasUpdating + pendingUpdatingIDs + clearUpdatingState | IMPLEMENTED | — | |
| spec:§Session Lifecycle/Resumption 2g | "Publish session_resumed lifecycle event" | agent.go:recoverSessions → publishLifecycle(id, "session_resumed") | IMPLEMENTED | — | |
| spec:§Session Lifecycle/Resumption 2h | "Sessions that fail to start within 30s: mark status: 'failed', publish session_failed" | session.go:startup monitor goroutine (startupTimeout = 30s) | IMPLEMENTED | — | |
| spec:§Session Lifecycle/Deletion | handleDelete branches on SoftThreshold (interactive vs quota-managed) | agent.go:handleDelete isQuotaManaged branching | IMPLEMENTED | — | |
| spec:§Session Lifecycle/Deletion | "Interactive: Remove session, interrupt, wait 10s, SIGKILL, remove worktree, delete KV, publish session_stopped" | agent.go:handleDelete interactive path | IMPLEMENTED | — | sessionDeleteTimeout = 10s |
| spec:§Session Lifecycle/Deletion | "Quota-managed: stop monitor, remove, interrupt, skip worktree if schedule/, tombstone KV, publish session_job_cancelled" | agent.go:handleDelete quota path | IMPLEMENTED | — | schedule/ prefix check for worktree skip |
| spec:§Session Lifecycle/Restart | "Publish session_restarting, stop, clear pending controls, update extraFlags, respawn with --resume, publish session_resumed" | agent.go:handleRestart | IMPLEMENTED | — | |
| spec:§Import Handler | "check for pending session import on startup, before recovery" | agent.go:Run → checkImport before recoverSessions | IMPLEMENTED | — | |
| spec:§Import Handler | Steps: read project KV, check importRef, request download URL, download tar.gz, validate archive, unpack, create KV entries, clear importRef, publish import.complete | import.go:checkImport + unpackImportArchive | IMPLEMENTED | — | Full flow with retry on expired URL |
| spec:§Import Handler | "session ID collision: skip with warning, import remaining" | import.go:createImportedSessionKVEntry | IMPLEMENTED | — | Checks KV before writing, logs warning |
| spec:§Import Handler | "If unpack fails: report error via session_import_failed, leave importRef set" | import.go:checkImport error paths | IMPLEMENTED | — | publishImportFailed + return error |
| spec:§Attachment Support | "downloadAttachment: request pre-signed URL, download from S3, retry on expired URL" | attachment.go:downloadAttachment | IMPLEMENTED | — | Full download with retry |
| spec:§Attachment Support | "uploadAttachment: request upload URL, upload to S3, confirm via NATS" | attachment.go:uploadAttachment | IMPLEMENTED | — | Three-step flow (request, upload, confirm) |
| spec:§Attachment Download | "When processing a user input message containing AttachmentRef content blocks, download and pass to CLI driver" | attachment.go:processInputAttachments (not called from handleInput) | GAP | CODE→FIX | processInputAttachments exists but is never called from the message handling path in handleInput. The download/upload plumbing is complete but the integration point in the input handler is missing. |
| spec:§fsnotify Watcher | "On startup, begin watching session data directory for new .jsonl files" | agent.go:Run → go a.watchSessionDataDir(ctx) | IMPLEMENTED | — | |
| spec:§fsnotify Watcher | "When new JSONL detected: extract metadata, check KV, create completed entry" | fswatch.go:handleNewJSONLFile + extractJSONLSessionMeta | IMPLEMENTED | — | |
| spec:§Event Routing | "In-flight background agent tracking: +1 on Agent tool_use with run_in_background, -1 on task-notification" | session.go:updateInFlightBackgroundAgents | IMPLEMENTED | — | |
| spec:§Event Routing | "In-flight background shell tracking: Phase 1 pending, Phase 2 promoted, removal on task-notification" | session.go:updateInFlightShells | IMPLEMENTED | — | Two-phase tracking with pendingShells/inFlightShells maps |
| spec:§Event Routing | "Shell tracking disabled when CLAUDE_CODE_TMPDIR not set" | session.go:updateInFlightShells checks os.Getenv("CLAUDE_CODE_TMPDIR") | IMPLEMENTED | — | |
| spec:§Event Routing | "Truncates events exceeding 8 MB NATS payload limit" | session.go:truncateEventIfNeeded (maxEventBytes = 8*1024*1024) | IMPLEMENTED | — | |
| spec:§Event Routing | "Input routing: type:'message' → driver.SendMessage, type:'permission_response' → driver.SendPermissionResponse, type:'skill_invoke' → /skill-name prefixed" | agent.go:handleInput switch on msgType | IMPLEMENTED | — | All three routes + legacy passthrough |
| spec:§Event Routing | "Config updates from sessions.{sslug}.config → driver.UpdateConfig" | agent.go:handleConfig | IMPLEMENTED | — | |
| spec:§Event Routing | "Interrupts from sessions.{sslug}.control.interrupt → driver.Interrupt" | agent.go:handleControl | IMPLEMENTED | — | |
| spec:§Permission Policy | "Four policies: managed, auto, allowlist, strict-allowlist" | state.go:PermissionPolicy constants | IMPLEMENTED | — | |
| spec:§Permission Policy | "allowedTools empty on strict-allowlist → reject sessions.create, publish lifecycle.error" | agent.go:handleCreate validation | IMPLEMENTED | — | |
| spec:§Permission Policy | "strict-allowlist auto-deny → onStrictDeny callback → permDeniedCh" | session.go:handleSideEffect strict-allowlist path | IMPLEMENTED | — | |
| spec:§Credential Management | "HTTP challenge-response: generate NKey, expose public key, authenticate via POST /api/auth/challenge + /api/auth/verify, 5-minute TTL refresh" | agent_auth.go:AgentAuth | IMPLEMENTED | — | Full challenge-response flow with StartRefreshLoop |
| spec:§Credential Management | "On permissions violation: immediate refresh + retry" | agent_auth.go:StartRefreshLoop permViolationCh case | IMPLEMENTED | — | |
| spec:§Git Credentials (K8s) | "Symlink PVC config, read managed credentials, resolve GIT_IDENTITY_ID, convert multi→single account, merge, register helpers" | gitcreds.go:CredentialManager.Setup + mergeAndSetup | IMPLEMENTED | — | |
| spec:§Git Credentials (K8s) | "Re-run merge and setup before each git operation if managed content has changed" | gitcreds.go:RefreshIfChanged | IMPLEMENTED | — | |
| spec:§Worktree Management | "Creates worktrees, checks collision, removes when last session deleted" | agent.go:gitWorktreeAdd, gitWorktreeRemove, handleCreate collision check, handleDelete removal | IMPLEMENTED | — | |
| spec:§Worktree Management | "Refreshes credentials before each git worktree operation" | agent.go:gitWorktreeAdd calls credMgr.RefreshIfChanged | IMPLEMENTED | — | |
| spec:§Compact Boundary | "queries stream for last sequence number, writes to KV as replayFromSeq" | agent.go:updateReplayFromSeq | IMPLEMENTED | — | |
| spec:§Quota Monitoring | "One QuotaMonitor per quota-managed session, created when softThreshold > 0" | quota_monitor.go:newQuotaMonitor, agent.go:handleCreate wiring | IMPLEMENTED | — | |
| spec:§Quota Monitoring | "Select-loop over five cases: stopCh, permDeniedCh, quotaCh, turnEndedCh, doneCh" | quota_monitor.go:run() | IMPLEMENTED | — | Priority: turnEndedCh checked before full select |
| spec:§Quota Monitoring | "Soft threshold: inject MCLAUDE_STOP: quota_soft via sessions.input" | quota_monitor.go:sendGracefulStop + publishToSessionsInput | IMPLEMENTED | — | NATS publish to sessions.input, not direct stdinCh |
| spec:§Quota Monitoring | "Hard budget: outputTokensSinceSoftMark >= hardHeadroomTokens → control_request interrupt on stdinCh" | quota_monitor.go:onRawOutput + sendHardInterrupt | IMPLEMENTED | — | |
| spec:§Quota Monitoring | "Token counting: byte estimate + authoritative from turn_complete" | quota_monitor.go:onRawOutput (EventTypeAssistant + EventTypeResult) | IMPLEMENTED | — | |
| spec:§Quota Monitoring | "handleTurnEnd: quota_soft→paused, quota_hard→paused, empty→completed, permDenied→no-op" | quota_monitor.go:handleTurnEnd | IMPLEMENTED | — | |
| spec:§Quota Monitoring | "handleSubprocessExit: if !terminalEventPublished → session_job_failed" | quota_monitor.go:handleSubprocessExit | IMPLEMENTED | — | |
| spec:§Quota Monitoring | "hardHeadroomTokens == 0 → hard interrupt fires immediately after soft marker" | quota_monitor.go:run() quotaCh case | IMPLEMENTED | — | |
| spec:§Quota Monitoring | "Message format: standard sessions.input JSON envelope with id, ts, type, text" | quota_monitor.go:publishToSessionsInput | IMPLEMENTED | — | Uses uuid + time.Now().UnixMilli() |
| spec:§Quota Publisher | "runQuotaPublisher polls api.anthropic.com/api/oauth/usage every 60s" | agent.go:runQuotaPublisher + daemon_jobs.go:fetchQuotaStatus | IMPLEMENTED | — | quotaPollInterval = 60s |
| spec:§Quota Publisher | "CP designates one agent per user; agent subscribes to manage.designate-quota-publisher" | agent.go:handleDesignateQuotaPublisher | IMPLEMENTED | — | |
| spec:§Graceful Shutdown | "Step 1: write updating to KV, set shutdownPending" | agent.go:gracefulShutdown step 1 | IMPLEMENTED | — | |
| spec:§Graceful Shutdown | "Step 2: cancel command consumer" | agent.go:gracefulShutdown cmdMsgs.Stop() | IMPLEMENTED | — | |
| spec:§Graceful Shutdown | "Step 3: drain core NATS subscriptions" | agent.go:gracefulShutdown sub.Drain() | IMPLEMENTED | — | |
| spec:§Graceful Shutdown | "Step 5: poll 1s, indefinite wait, auto-interrupt requires_action" | agent.go:gracefulShutdown ticker loop | IMPLEMENTED | — | No timeout; auto-interrupts StateRequiresAction |
| spec:§Graceful Shutdown | "Step 6: shell-killed notifications (K8s mode)" | agent.go:publishShellKilledNotifications | IMPLEMENTED | — | XML task-notification format |
| spec:§Graceful Shutdown | "Step 7-8: cancel control consumer, publish session_upgrading" | agent.go:gracefulShutdown steps 7-8 | IMPLEMENTED | — | |
| spec:§JSONL Cleanup | "daily cleanup deletes JSONL files >90 days and purges KV-orphaned files" | fswatch.go:runJSONLCleanup + doJSONLCleanup | IMPLEMENTED | — | jsonlCleanupMaxAge = 90 days, KV-orphan detection |
| spec:§Daemon Mode | "deprecated per ADR-0058" | daemon.go + daemon_jobs.go | IMPLEMENTED | — | Full daemon code retained during deprecation period |
| spec:§Error Handling | "NATS connection lost: MaxReconnects(-1), RetryOnFailedConnect(true)" | main.go:natsConnectWithAuth | IMPLEMENTED | — | |
| spec:§Error Handling | "KV bucket not found on startup: Fatal" | agent.go:NewAgent | IMPLEMENTED | — | fmt.Errorf + log.Fatal in main.go |
| spec:§Error Handling | "JetStream fetch error: exponential backoff" | N/A (ordered push consumers handle this internally) | IMPLEMENTED | — | NATS client library handles backoff for ordered consumers |
| spec:§Error Handling | "HOST_SLUG not provided: fallback chain → FATAL" | main.go:HOST_SLUG resolution | IMPLEMENTED | — | flag→env→active-host→HOSTNAME→os.Hostname→fatal |
| spec:§Error Handling | "allowedTools empty on strict-allowlist: reject + lifecycle.error" | agent.go:handleCreate | IMPLEMENTED | — | |
| spec:§Error Handling | "Debug socket start fails: logged as warning, sessions function normally" | agent.go:handleCreate debug socket error path | IMPLEMENTED | — | |
| spec:§Terminal Sessions | "spawn PTY, bridge I/O through NATS, 4 KB chunks, resize forwarding" | terminal.go:startTerminal | IMPLEMENTED | — | 4096 byte read buffer |
| spec:§Liveness | "Signalled by NATS connection itself, no periodic heartbeat" | No heartbeat code in agent | IMPLEMENTED | — | Agent does not publish heartbeats; $SYS events handle liveness |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-50 | INFRA | Package imports, log setup, main() entry point |
| main.go:50-80 | IMPLEMENTED | Health/readiness probes (spec §Health and Readiness) |
| main.go:80-160 | IMPLEMENTED | CLI flag definitions (spec §Configuration) |
| main.go:160-260 | INFRA | Slug validation fallbacks, metrics setup |
| main.go:260-310 | IMPLEMENTED | AgentAuth NKey pair generation and HTTP challenge-response (spec §Credential Management) |
| main.go:310-350 | IMPLEMENTED | NATS connection with auth (spec §Error Handling) |
| main.go:350-400 | IMPLEMENTED | Daemon mode path (spec §Daemon Mode deprecated) |
| main.go:400-460 | IMPLEMENTED | K8s credential setup and repo init (spec §Git Credentials) |
| agent.go (entire) | IMPLEMENTED | Core agent: NewAgent, Run, recoverSessions, createJetStreamConsumers, handleCreate/Delete/Input/Config/Control/Restart, gracefulShutdown, publishLifecycle*, writeSessionKV, terminal handlers — all spec'd |
| agent_auth.go (entire) | IMPLEMENTED | HTTP challenge-response auth (spec §Credential Management) |
| session.go (entire) | IMPLEMENTED | Session process management, event routing, side effects, shell tracking (all spec'd) |
| events.go (entire) | IMPLEMENTED | Event type constants and parsing (spec §Session State Machine, §Event Routing) |
| state.go (entire) | IMPLEMENTED | SessionState, UsageStats, PermissionPolicy, KV helpers (spec §Session State Machine, §Permission Policy) |
| quota_monitor.go (entire) | IMPLEMENTED | QuotaMonitor goroutine (spec §Quota Monitoring) |
| daemon.go (entire) | IMPLEMENTED | Daemon mode — deprecated per ADR-0058, retained during deprecation (spec §Daemon Mode) |
| daemon_jobs.go (entire) | IMPLEMENTED | Daemon job dispatcher, quota publisher, HTTP API — deprecated per ADR-0058 (spec §Daemon Mode) |
| debug.go (entire) | IMPLEMENTED | Unix socket debug server (spec §Unix Sockets) |
| terminal.go (entire) | IMPLEMENTED | PTY terminal sessions (spec §Terminal Sessions) |
| metrics.go (entire) | IMPLEMENTED | Prometheus metrics + OTel tracing (spec §Prometheus Metrics) |
| gitcreds.go (entire) | IMPLEMENTED | Git credential management (spec §Credential Management, §Git Credentials) |
| worktree.go (entire) | IMPLEMENTED | Branch slugification and worktree helpers (spec §Worktree Management) |
| import.go (entire) | IMPLEMENTED | Session import handler (spec §Import Handler) |
| attachment.go (entire) | IMPLEMENTED | Attachment download/upload via S3 pre-signed URLs (spec §Attachment Support) |
| fswatch.go (entire) | IMPLEMENTED | fsnotify watcher + JSONL cleanup (spec §fsnotify Watcher, §JSONL Cleanup) |
| shell_split.go (entire) | INFRA | POSIX shell splitting utility for --extraFlags parsing |
| internal/drivers/driver.go | IMPLEMENTED | CLIDriver interface + types (spec §CLIDriver Interface) |
| internal/drivers/registry.go | IMPLEMENTED | DriverRegistry (spec §DriverRegistry) |
| internal/drivers/claudecode.go | IMPLEMENTED | ClaudeCodeDriver (spec §Driver Implementations) |
| internal/drivers/droid.go | IMPLEMENTED | DroidDriver stub (spec §Driver Implementations) |
| internal/drivers/devin.go | IMPLEMENTED | DevinACPDriver stub (spec §Driver Implementations) |
| internal/drivers/generic_terminal.go | IMPLEMENTED | GenericTerminalDriver stub (spec §Driver Implementations) |
| internal/drivers/driver.go:BackendGemini | UNSPEC'd | `BackendGemini CLIBackend = "gemini"` constant defined but no Gemini driver is registered or mentioned in spec-session-agent.md. Present in spec-state-schema.md backend enum. Harmless unused constant. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec:§Session Lifecycle/Creation | handleCreate full flow | Not evaluated | Not evaluated | — | Test files excluded per instructions |
| spec:§Session Lifecycle/Resumption | recoverSessions KV-based recovery | Not evaluated | Not evaluated | — | Test files excluded per instructions |
| spec:§Graceful Shutdown | gracefulShutdown drain + upgrade | Not evaluated | Not evaluated | — | Test files excluded per instructions |
| spec:§Quota Monitoring | QuotaMonitor two-tier enforcement | Not evaluated | Not evaluated | — | Test files excluded per instructions |
| spec:§Credential Management | HTTP challenge-response auth | Not evaluated | Not evaluated | — | Test files excluded per instructions |

*Note: Test coverage evaluation skipped — test files are excluded from evaluation scope per the production-code-only audit directive.*

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| 002-ptt-race-condition.md | PTT race condition | N/A | Component: spa — not session-agent |
| 003-initial-message-injection-race.md | Initial message injection race | N/A | Component: spa — not session-agent |
| 005-getting-started-canned-text.md | Getting started canned text | N/A | Component: spa — not session-agent |

No open bugs for the session-agent component.

### Summary

- Implemented: 74
- Gap: 1
- Partial: 2
- Infra: 4
- Unspec'd: 1
- Dead: 0
- Tested: N/A (test files excluded)
- Unit only: N/A
- E2E only: N/A
- Untested: N/A
- Bugs fixed: 0
- Bugs open: 0

### Non-clean findings

**GAP [CODE→FIX]:** "When processing a user input message containing AttachmentRef content blocks" → `processInputAttachments` exists in attachment.go but is never called from `handleInput` in agent.go. The download/upload plumbing is complete but the integration point in the message handling path is missing. (attachment.go:processInputAttachments, agent.go:handleInput)

**PARTIAL [SPEC→FIX]:** "ReadEvents(proc *Process, out chan<- SessionEvent) error" → Code uses `chan<- DriverEvent` not `chan<- SessionEvent`. The `DriverEvent` type serves the same purpose. Spec interface definition should use the actual type name `DriverEvent`. (internal/drivers/driver.go:ReadEvents)

**PARTIAL [SPEC→FIX]:** "KV status enum: pending, running, paused, requires_action, completed, stopped, cancelled, needs_spec_fix, failed, error" → `stopped` and `error` not defined as Go constants. `stopped` is never set in KV (entries are deleted on stop). `error` is listed in enum but never used in agent code. Spec should clarify these are theoretical values or note that `stopped` means KV deletion. (events.go, state.go)

**UNSPEC'd:** `BackendGemini CLIBackend = "gemini"` constant in internal/drivers/driver.go — defined but no Gemini driver registered. Present in spec-state-schema.md backend enum but not in spec-session-agent.md. Harmless. (internal/drivers/driver.go:27)
