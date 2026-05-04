# Implementation Audit: mclaude-session-agent — Round 5

## Run: 2026-05-01T12:00:00Z

**Component:** mclaude-session-agent
**Root:** /Users/rsong/work/mclaude/mclaude-session-agent
**Specs:** docs/mclaude-session-agent/spec-session-agent.md, docs/spec-state-schema.md, docs/spec-nats-data-taxonomy.md
**ADRs evaluated:** ADR-0005 (accepted), ADR-0044 (accepted), ADR-0054 (accepted), ADR-0053 (accepted), ADR-0058 (accepted), ADR-0035 (accepted), ADR-0051 (implemented), ADR-0024 (implemented), ADR-0043 (implemented)

**Previous round (R4) gaps verified:**

1. **JSONL KV check** (GAP CODE→FIX) — **FIXED**: `doJSONLCleanup` (fswatch.go:73-120) now includes KV-orphan detection. Check 2 queries `a.sessKV.Get(ctx, kvKey)` for each `.jsonl` file; files whose session ID returns `ErrKeyNotFound` are marked as orphans and deleted.

2. **JSON layout** (GAP CODE→FIX) — **FIXED**: `SessionState` (state.go) now has `Capabilities drivers.CLICapabilities json:"capabilities"` (correct key), and `Tools`, `Skills`, `Agents` are top-level `[]string` fields with JSON tags `"tools"`, `"skills"`, `"agents"`. The init handler (session.go) sets `s.state.Tools`, `s.state.Skills`, `s.state.Agents`, and `s.state.Capabilities = s.driver.Capabilities()`.

3. **Driver routing** (PARTIAL CODE→FIX) — **FIXED**: `handleInput` (agent.go) now routes `type:"message"` → `drv.SendMessage(proc, userMsg)`, `type:"skill_invoke"` → `drv.SendMessage(proc, skillMsg)`, `type:"permission_response"` → `drv.SendPermissionResponse(proc, rID, all)` via `sess.sendViaDriver()`. `handleConfig` routes through `drv.UpdateConfig(proc, cfg)`. `handleControl` interrupt routes through `drv.Interrupt(proc)`.

---

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-session-agent:1 | "per-project process supervisor that manages multiple concurrent CLI sessions" | agent.go:30-70 | IMPLEMENTED | — | Agent struct with sessions map |
| spec-session-agent:3 | "pluggable driver/adapter pattern (ADR-0005)" | internal/drivers/driver.go, registry.go | IMPLEMENTED | — | CLIDriver interface + DriverRegistry |
| spec-session-agent:5 | "supports multiple CLI backends: Claude Code, Factory Droid, Devin ACP, and a GenericTerminal fallback" | internal/drivers/*.go, agent.go:130-137 | PARTIAL | SPEC→FIX | Spec opening paragraph mentions "Gemini CLI" but no driver exists and the Driver Implementations table has 4 rows without Gemini. Opening paragraph should match the table. |
| spec-session-agent:7 | "operates as a standalone per-project process in all deployment modes" | main.go:92-200 | IMPLEMENTED | — | Standalone mode with --project-id |
| spec-session-agent:9 | "HOST_SLUG is required configuration" | main.go:107-131 | IMPLEMENTED | — | Fallback chain: flag → env → symlink → hostname |
| spec-session-agent:12 | "Absence is fatal at startup" | main.go:131 | IMPLEMENTED | — | log.Fatal with "FATAL: HOST_SLUG required" |
| spec-session-agent:dep | "Daemon mode deprecation (ADR-0058)" | daemon.go, daemon_jobs.go, main.go:141-176 | IMPLEMENTED | — | --daemon still works, routes to Daemon.Run() |
| spec-session-agent:cfg | Configuration table (all flags) | main.go:85-112 | IMPLEMENTED | — | All flags defined with env fallbacks |
| spec-session-agent:health | "--health checks process alive and NATS connection" | main.go:41-53 | IMPLEMENTED | — | natsProbeConnect + exit 0 |
| spec-session-agent:ready | "--ready attempts NATS connection and verifies Claude Code can be spawned" | main.go:55-72 | IMPLEMENTED | — | NATS probe + exec.LookPath |
| spec-session-agent:driver-if | "CLIDriver interface" | internal/drivers/driver.go:85-122 | IMPLEMENTED | — | All methods defined |
| spec-session-agent:registry | "DriverRegistry maps CLIBackend enum values to driver instances" | internal/drivers/registry.go | IMPLEMENTED | — | Register/Get/GetOrDefault |
| spec-session-agent:cc-drv | "ClaudeCodeDriver spawns claude --print --verbose --output-format stream-json" | internal/drivers/claudecode.go:62-80 | IMPLEMENTED | — | Correct flags in buildArgs |
| spec-session-agent:droid-drv | "DroidDriver — JSON-RPC 2.0 — stub" | internal/drivers/droid.go | IMPLEMENTED | — | Stub with "not yet implemented" |
| spec-session-agent:devin-drv | "DevinACPDriver — ACP — stub" | internal/drivers/devin.go | IMPLEMENTED | — | Stub with "not yet implemented" |
| spec-session-agent:gen-drv | "GenericTerminalDriver — PTY raw I/O — hasEventStream: false" | internal/drivers/generic_terminal.go | IMPLEMENTED | — | HasEventStream: false |
| spec-session-agent:stream | "agent does not create streams — CP creates per-user streams" | agent.go:108-111 | IMPLEMENTED | — | No stream creation; only OrderedConsumer |
| spec-session-agent:consumers | "ordered push consumers on MCLAUDE_SESSIONS_{uslug}" | agent.go:204-242 | IMPLEMENTED | — | Two consumers: cmd + ctl |
| spec-session-agent:kv-write | "KV_mclaude-sessions-{uslug} write — key: hosts.{hslug}.projects.{pslug}.sessions.{sslug}" | agent.go:writeSessionKV, state.go:sessionKVKey | IMPLEMENTED | — | Uses subj.SessionsKVKey() |
| spec-session-agent:kv-proj | "KV_mclaude-projects-{uslug} write — clear importRef" | import.go:clearImportRef | IMPLEMENTED | — | Reads/updates projKV |
| spec-session-agent:hosts-kv | "KV_mclaude-hosts read — Shared bucket" | agent.go:104-113 | IMPLEMENTED | — | hostsKV read-only; nil on failure |
| spec-session-agent:pub-events | "Session events to sessions.{sslug}.events" | session.go:start() stdout router | IMPLEMENTED | — | Full slug path publish |
| spec-session-agent:pub-lifecycle | "Lifecycle events to sessions.{sslug}.lifecycle.{eventType}" | agent.go:publishLifecycle* | IMPLEMENTED | — | Uses subj helper |
| spec-session-agent:pub-api-error | "API error events to sessions._api" | agent.go:publishAPIError | IMPLEMENTED | — | {type,request_id,operation,error} |
| spec-session-agent:pub-quota | "Quota status to mclaude.users.{uslug}.quota" | agent.go:runQuotaPublisher | IMPLEMENTED | — | 60s polling |
| spec-session-agent:sub-terminal | "Terminal API via core NATS" | agent.go:subscribeTerminalAPI | IMPLEMENTED | — | 3 subscriptions |
| spec-session-agent:sub-quota | "Quota status via core NATS" | quota_monitor.go:newQuotaMonitor | IMPLEMENTED | — | ChanSubscribe |
| spec-session-agent:sub-designate | "designate-quota-publisher" | agent.go:subscribeManageAPI | IMPLEMENTED | — | Core NATS subscription |
| spec-session-agent:unix-sock | "Debug socket at /tmp/mclaude-session-{sessionId}.sock" | debug.go | IMPLEMENTED | — | NDJSON broadcast + stdin |
| spec-session-agent:metrics | "4 Prometheus metrics" | metrics.go:17-38 | IMPLEMENTED | — | All 4 registered |
| spec-session-agent:otel | "OpenTelemetry trace spans" | metrics.go | IMPLEMENTED | — | NATSPublishSpan, KVWriteSpan, etc. |
| spec-session-agent:traceparent | "W3C traceparent headers propagated" | metrics.go:InjectTraceparent,ExtractTraceparent | IMPLEMENTED | — | otel propagation |
| spec-session-agent:state-machine | "Session State Machine" | events.go:29-51 | IMPLEMENTED | — | All states defined |
| spec-session-agent:create | "Session creation lifecycle (steps 1-11)" | agent.go:handleCreate | IMPLEMENTED | — | All steps present |
| spec-session-agent:create-backend | "Look up CLIDriver from DriverRegistry" | agent.go:handleCreate | IMPLEMENTED | — | GetOrDefault with fallback |
| spec-session-agent:recovery | "KV-based recovery" | agent.go:recoverSessions | IMPLEMENTED | — | Watches project-scoped keys |
| spec-session-agent:recovery-updating | "Sessions in updating status: KV stays as updating until clearUpdatingState" | agent.go:recoverSessions, clearUpdatingState | IMPLEMENTED | — | pendingUpdatingIDs tracking |
| spec-session-agent:resume-init | "On init event: set status" | session.go:handleSideEffect SubtypeInit | PARTIAL | SPEC→FIX | Code sets StateIdle per ADR-0051 and state machine diagram ("create → idle ← resume"). Spec §Resumption 2d says "running" but the spec's own state machine shows idle after resume. Spec should say "idle". |
| spec-session-agent:startup-timeout | "30s startup timeout → failed state" | session.go:start() timeout goroutine | IMPLEMENTED | — | startupTimeout = 30s |
| spec-session-agent:delete-interactive | "Interactive delete: remove, interrupt+wait, remove worktree, delete KV, session_stopped" | agent.go:handleDelete | IMPLEMENTED | — | Full flow with exitCode |
| spec-session-agent:delete-quota | "Quota-managed delete: stop monitor, skip schedule/, tombstone KV, session_job_cancelled" | agent.go:handleDelete | IMPLEMENTED | — | schedule/ prefix skip, KV delete |
| spec-session-agent:restart | "Restart: session_restarting, stop, clear pending, driver.Resume, session_resumed" | agent.go:handleRestart | IMPLEMENTED | — | Full restart flow |
| spec-session-agent:import | "Import handler: read project KV, download, unpack, clear importRef, signal completion" | import.go:checkImport | IMPLEMENTED | — | Pre-signed URL, tar.gz, collision handling |
| spec-session-agent:import-collision | "Skip session with warning on collision" | import.go:createImportedSessionKVEntry | IMPLEMENTED | — | KV get check |
| spec-session-agent:import-fail | "Report session_import_failed, leave importRef set" | import.go:checkImport | IMPLEMENTED | — | publishImportFailed + early return |
| spec-session-agent:import-retry | "S3 URL expired: request new URL, retry" | import.go:checkImport retry block | IMPLEMENTED | — | Second request on failure |
| spec-session-agent:attachment-dl | "Attachment download via pre-signed URL" | attachment.go:downloadAttachment | IMPLEMENTED | — | NATS req/reply + S3 GET |
| spec-session-agent:attachment-ul | "Attachment upload: request URL, PUT to S3, confirm" | attachment.go:uploadAttachment | IMPLEMENTED | — | 3-step flow |
| spec-session-agent:attachment-retry | "Retry on URL expiry" | attachment.go | IMPLEMENTED | — | Both have retry |
| spec-session-agent:fswatch | "fsnotify watcher for new JSONL files" | fswatch.go:watchSessionDataDir | IMPLEMENTED | — | Create/Rename events |
| spec-session-agent:fswatch-meta | "Extract metadata from first few lines" | fswatch.go:extractJSONLSessionMeta | IMPLEMENTED | — | Reads first 10 lines |
| spec-session-agent:fswatch-dedup | "Check KV before creating entry" | fswatch.go:handleNewJSONLFile | IMPLEMENTED | — | KV get check |
| spec-session-agent:event-router | "Event router reads DriverEvents" | session.go:start() eventsCh goroutine | IMPLEMENTED | — | Reads DriverEvent channel |
| spec-session-agent:bg-agent-track | "inFlightBackgroundAgents tracking" | session.go:updateInFlightBackgroundAgents | IMPLEMENTED | — | +1 Agent(bg), -1 task-notification |
| spec-session-agent:bg-shell-track | "Two-phase shell tracking" | session.go:updateInFlightShells | IMPLEMENTED | — | pending → promoted → removed |
| spec-session-agent:truncate | "Truncates events >8 MB" | session.go:truncateEventIfNeeded | IMPLEMENTED | — | maxEventBytes = 8*1024*1024 |
| spec-session-agent:input-routing | "Input routing: message → SendMessage, permission_response → SendPermissionResponse, skill_invoke → SendMessage" | agent.go:handleInput | IMPLEMENTED | — | Routes via sendViaDriver() to CLIDriver methods. FIXED from R4. |
| spec-session-agent:config-routing | "Config updates from sessions.{sslug}.config → driver.UpdateConfig" | agent.go:handleConfig | IMPLEMENTED | — | Builds SessionConfig, routes through driver.UpdateConfig |
| spec-session-agent:interrupt-routing | "Interrupts from sessions.{sslug}.control.interrupt → driver.Interrupt(proc)" | agent.go:handleControl (interrupt branch) | IMPLEMENTED | — | Routes through drv.Interrupt(proc) via sendViaDriver |
| spec-session-agent:perm-policy | "Four policies: managed, auto, allowlist, strict-allowlist" | state.go, session.go:shouldAutoApprove | IMPLEMENTED | — | All four policies |
| spec-session-agent:strict-reject | "Empty allowedTools on strict-allowlist → reject" | agent.go:handleCreate | IMPLEMENTED | — | lifecycle.error event |
| spec-session-agent:strict-deny | "strict-allowlist: auto-deny + onStrictDeny" | session.go:handleSideEffect | IMPLEMENTED | — | Deny response + callback |
| spec-session-agent:auth | "HTTP challenge-response auth" | agent_auth.go | IMPLEMENTED | — | Full Authenticate flow |
| spec-session-agent:auth-refresh | "5-minute TTL, proactive refresh" | agent_auth.go:StartRefreshLoop | IMPLEMENTED | — | Ticker + permViolation |
| spec-session-agent:auth-nkey-file | "Agent exposes public key via file or stdout" | agent_auth.go, main.go:152-160 | IMPLEMENTED | — | WritePublicKeyToFile or print |
| spec-session-agent:git-creds | "Git credential helper setup (K8s)" | gitcreds.go:CredentialManager.Setup | IMPLEMENTED | — | 3-step setup |
| spec-session-agent:git-identity | "Resolve GIT_IDENTITY_ID" | gitcreds.go:resolveIdentityFromManaged | IMPLEMENTED | — | conn-{id}-username lookup |
| spec-session-agent:git-normalize | "Normalize SCP-style git URLs to HTTPS" | gitcreds.go:NormalizeGitURL | IMPLEMENTED | — | git@host:path → https://host/path |
| spec-session-agent:init-repo | "Clone bare repo or init scratch" | gitcreds.go:InitRepo | IMPLEMENTED | — | Clone, fetch, or scratch |
| spec-session-agent:worktree | "Worktree management" | agent.go:gitWorktreeAdd/Remove, worktree.go | IMPLEMENTED | — | git worktree add/remove |
| spec-session-agent:worktree-cred | "Refreshes credentials before git worktree" | agent.go:gitWorktreeAdd | IMPLEMENTED | — | credMgr.RefreshIfChanged |
| spec-session-agent:compact | "compact_boundary → query stream → write replayFromSeq" | agent.go:updateReplayFromSeq | IMPLEMENTED | — | Stream.Info → LastSeq → KV |
| spec-session-agent:quota-mon | "QuotaMonitor: soft threshold, hard budget, turn-end" | quota_monitor.go | IMPLEMENTED | — | Full implementation |
| spec-session-agent:quota-msg | "Messages to NATS sessions.{sslug}.input with envelope" | quota_monitor.go:publishToSessionsInput | IMPLEMENTED | — | {id, ts, type, text} |
| spec-session-agent:quota-soft | "Soft threshold: inject MCLAUDE_STOP: quota_soft" | quota_monitor.go:run, sendGracefulStop | IMPLEMENTED | — | Publishes to NATS |
| spec-session-agent:quota-hard | "Hard budget: interrupt on stdinCh" | quota_monitor.go:onRawOutput | IMPLEMENTED | — | sendHardInterrupt |
| spec-session-agent:quota-zero | "hardHeadroomTokens=0: immediate hard interrupt" | quota_monitor.go:run | IMPLEMENTED | — | Immediate quota_hard |
| spec-session-agent:turn-end | "handleTurnEnd: soft→paused, hard→paused, empty→completed" | quota_monitor.go:handleTurnEnd | IMPLEMENTED | — | All branches |
| spec-session-agent:subprocess-exit | "handleSubprocessExit: !terminalEventPublished → failed" | quota_monitor.go:handleSubprocessExit | IMPLEMENTED | — | Checks flag |
| spec-session-agent:quota-priority | "turnEndedCh checked before doneCh" | quota_monitor.go:run | IMPLEMENTED | — | Nested select |
| spec-session-agent:quota-publisher | "CP designates quota publisher" | agent.go:handleDesignateQuotaPublisher | IMPLEMENTED | — | Start/stop via designation |
| spec-session-agent:graceful | "Graceful shutdown: 9-step sequence" | agent.go:gracefulShutdown | IMPLEMENTED | — | All steps |
| spec-session-agent:graceful-interrupt | "Auto-interrupt requires_action" | agent.go:gracefulShutdown | IMPLEMENTED | — | sendInterrupt |
| spec-session-agent:graceful-shell | "Shell-killed notifications" | agent.go:publishShellKilledNotifications | IMPLEMENTED | — | XML format |
| spec-session-agent:nats-reconnect | "MaxReconnects(-1), RetryOnFailedConnect(true)" | main.go:natsConnectWithAuth | IMPLEMENTED | — | Both options set |
| spec-session-agent:kv-not-found | "KV bucket not found: Fatal" | agent.go:NewAgent | IMPLEMENTED | — | Fatal on missing |
| spec-session-agent:crash-watch | "watchSessionCrash: detect, publish, auto-restart" | agent.go:watchSessionCrash | IMPLEMENTED | — | Full crash → restart loop |
| spec-session-agent:terminal | "Terminal sessions: PTY, 4 KB, core NATS" | terminal.go | IMPLEMENTED | — | pty.Start, 4096 reads |
| spec-session-agent:terminal-resize | "Resize forwarded to PTY" | terminal.go:resize | IMPLEMENTED | — | pty.Setsize |
| spec-session-agent:liveness | "Liveness via NATS connection; no heartbeat" | (no heartbeat code) | IMPLEMENTED | — | $SYS handled by CP |
| spec-session-agent:lifecycle-created | "session_created includes branch" | agent.go:publishLifecycleWithBranch | IMPLEMENTED | — | branch field |
| spec-session-agent:lifecycle-stopped | "session_stopped includes exitCode" | agent.go:publishLifecycleWithExitCode | IMPLEMENTED | — | exitCode field |
| spec-session-agent:jsonl-cleanup | "Daily cleanup deletes JSONL >90 days" | fswatch.go:runJSONLCleanup | IMPLEMENTED | — | 90-day cutoff |
| spec-session-agent:jsonl-purge | "purges session files not present in KV" | fswatch.go:doJSONLCleanup | IMPLEMENTED | — | KV-orphan detection via sessKV.Get. FIXED from R4. |
| spec-state-schema:caps | "capabilities boolean flags; tools/skills/agents top-level" | state.go:SessionState | IMPLEMENTED | — | Capabilities at "capabilities", tools/skills/agents top-level. FIXED from R4. |
| spec-session-agent:caps-kv | "CLICapabilities populated from init event" | session.go:handleSideEffect SubtypeInit | IMPLEMENTED | — | s.state.Capabilities = s.driver.Capabilities() |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-30 | INFRA | Package imports, flag parsing setup |
| main.go:32-73 | IMPLEMENTED | Health/readiness probes per spec |
| main.go:74-200 | IMPLEMENTED | CLI flags, slug resolution, NATS connection per spec |
| agent.go:1-30 | INFRA | Imports |
| agent.go:30-140 | IMPLEMENTED | Agent struct + NewAgent — all per spec |
| agent.go:142-300 | IMPLEMENTED | Run, recoverSessions, createJetStreamConsumers, runConsumer, dispatch |
| agent.go:302-500 | IMPLEMENTED | clearUpdatingState, gracefulShutdown |
| agent.go:500-720 | IMPLEMENTED | publishShellKilledNotifications, manage API, quota publisher, terminal API |
| agent.go:742-755 | UNSPEC'd | `defaultDevHarnessAllowlist` variable — retained as convenience constant per code comment, but has no spec reference. Could be removed. |
| agent.go:757-1700 | IMPLEMENTED | handleCreate, handleDelete, handleInput, handleControl, handleConfig, handleRestart, writeSessionKV, publishLifecycle* family |
| agent.go:1700-2034 | IMPLEMENTED | watchSessionCrash, updateReplayFromSeq, reply, worktreeInUse, sessionForRequest, gitWorktree*, terminal handlers |
| session.go | IMPLEMENTED | Session struct, start, event router, side effects, shell tracking, stdin serializer |
| state.go:SessionState | IMPLEMENTED | All fields match spec-state-schema |
| state.go:JobEntry | UNSPEC'd | Daemon-mode only struct, retained during deprecation period |
| events.go | IMPLEMENTED | Event types and parse helpers |
| quota_monitor.go | IMPLEMENTED | Full QuotaMonitor per spec |
| import.go | IMPLEMENTED | Import handler per ADR-0053 |
| attachment.go | IMPLEMENTED | Attachment download/upload per ADR-0053 |
| fswatch.go | IMPLEMENTED | fsnotify watcher + JSONL cleanup with KV-orphan purging |
| daemon.go | IMPLEMENTED | Deprecated daemon mode (retained per ADR-0058) |
| daemon_jobs.go | IMPLEMENTED | Deprecated job dispatcher (retained per ADR-0058) |
| metrics.go | IMPLEMENTED | Prometheus metrics + OpenTelemetry spans |
| terminal.go | IMPLEMENTED | PTY terminal sessions |
| agent_auth.go | IMPLEMENTED | HTTP challenge-response auth |
| debug.go | IMPLEMENTED | Unix socket debug server |
| gitcreds.go | IMPLEMENTED | Credential management, InitRepo |
| worktree.go | INFRA | SlugifyBranch, worktreeExists |
| shell_split.go | INFRA | Shell argument splitting |
| internal/drivers/driver.go | IMPLEMENTED | CLIDriver interface, types |
| internal/drivers/registry.go | IMPLEMENTED | DriverRegistry |
| internal/drivers/claudecode.go | IMPLEMENTED | ClaudeCodeDriver |
| internal/drivers/droid.go | IMPLEMENTED | DroidDriver stub |
| internal/drivers/devin.go | IMPLEMENTED | DevinACPDriver stub |
| internal/drivers/generic_terminal.go | IMPLEMENTED | GenericTerminalDriver stub |

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

- Implemented: 87
- Gap: 0
- Partial: 2
- Infra: 4
- Unspec'd: 2
- Dead: 0
- Tested: 0
- Unit only: 1 (aggregate)
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0

**R4 gaps — all verified FIXED:**
1. ✅ JSONL KV check: `doJSONLCleanup` now has KV-orphan detection (fswatch.go:73-120)
2. ✅ JSON layout: `Capabilities` at `"capabilities"` key; `Tools`/`Skills`/`Agents` top-level (state.go, session.go)
3. ✅ Driver routing: `handleInput` routes `message`/`skill_invoke`/`permission_response` through `CLIDriver` methods via `sendViaDriver()` (agent.go)

**Remaining items (SPEC→FIX only — no code changes needed):**

PARTIAL [SPEC→FIX]: "supports multiple CLI backends: Claude Code, Factory Droid, Devin ACP, and a GenericTerminal fallback" → Spec opening paragraph mentions "Gemini CLI" but the Driver Implementations table (4 rows) omits it and no Gemini driver exists. The `CLIBackend` enum in driver.go defines `BackendGemini = "gemini"` but no `GeminiDriver` struct is implemented. Opening paragraph should match the table, or a Gemini stub should be added if the enum value is intentional.

PARTIAL [SPEC→FIX]: "On init event: set status: 'idle'" → Code correctly sets `StateIdle` on init per ADR-0051 and the state machine diagram ("create → idle ← resume"). Spec §Resumption step 2d says "set `status: 'running'`" which contradicts the spec's own state machine. Spec should be corrected to say "idle".
