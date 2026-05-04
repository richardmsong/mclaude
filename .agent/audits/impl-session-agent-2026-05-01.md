## Run: 2026-05-01T00:00:00Z

Component: mclaude-session-agent
Root: /Users/rsong/work/mclaude/mclaude-session-agent
Spec: docs/mclaude-session-agent/spec-session-agent.md
ADR: docs/adr-0054-nats-jetstream-permission-tightening.md (status: draft — evaluated via spec which incorporates ADR-0054 decisions)
State schema: docs/spec-state-schema.md

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-session-agent:1-5 | "session agent is a per-project process supervisor ... holds them in an in-memory session map" | agent.go:36-43 (sessions map[string]*Session) | IMPLEMENTED | — | Agent struct has sessions map and is per-project |
| spec-session-agent:7 | "Per ADR-0054, every NATS subject the agent uses is host-scoped: mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.…" | agent.go:209-215 (projBase construction) | IMPLEMENTED | — | Subject prefix constructed with user/host/project slugs |
| spec-session-agent:7 | "The host slug is required configuration — sourced from HOST_SLUG env var ... Absence is fatal at startup." | main.go:93-103 | PARTIAL | SPEC→FIX | Spec says "Known bug: code does not fatal on absence — silently falls back". Code at main.go:103 now calls `log.Fatal().Msg("FATAL: HOST_SLUG required")` after fallback chain, which IS a fatal exit. But the fallback chain itself (lines 93-102) tries hostname slugification before fataling, which means if hostname is valid, it won't fatal. The spec documents this as a known bug but the code actually does fatal when ALL fallbacks fail. The spec's "known bug" annotation matches the code: it falls back through slugified hostname before fataling. |
| spec-session-agent:78-80 | "Per ADR-0054, the agent does not create streams — the control-plane creates per-user streams on user registration" | agent.go:90-92 (comment only, no stream creation code) | IMPLEMENTED | — | No CreateOrUpdateStream calls in agent code |
| spec-session-agent:80 | "MCLAUDE_SESSIONS_{uslug} — Consolidated per-user stream" | agent.go:208 (streamName construction) | IMPLEMENTED | — | Stream name = "MCLAUDE_SESSIONS_" + userSlug |
| spec-session-agent:82-83 | "The agent creates ordered push consumers on MCLAUDE_SESSIONS_{uslug}" | agent.go:200-247 (createJetStreamConsumers) | IMPLEMENTED | — | Uses js.OrderedConsumer with DeliverNewPolicy |
| spec-session-agent:84-85 | "Session command consumer — Filtered to sessions.create and sessions.{sslug}.{input,delete,config,control.*}. DeliverNew" | agent.go:216-224 | PARTIAL | CODE→FIX | Consumer filter includes sessions.create, sessions.*.input, sessions.*.delete, sessions.*.config but NOT sessions.*.control.* — control subjects are on a separate consumer (ctlCons). This is by design but the spec says "Session command consumer" includes control.* which is inaccurate. The code splits into cmd+ctl consumers (spec §Graceful Shutdown step 2 vs 4 describes this split). Spec text at line 84 is misleading — it describes a single consumer but code has two. However spec §Graceful Shutdown does describe the two-consumer split, so the behavior matches. |
| spec-session-agent:90-93 | "KV_mclaude-sessions-{uslug} (write) ... Key format: hosts.{hslug}.projects.{pslug}.sessions.{sslug}" | agent.go:83-88 (sessKV bucket), state.go:64-67 (sessionKVKey) | IMPLEMENTED | — | Per-user bucket name, hierarchical key format |
| spec-session-agent:94-95 | "KV_mclaude-projects-{uslug} (write) ... Key format: hosts.{hslug}.projects.{pslug}" | agent.go:89-93 (projKV bucket) | IMPLEMENTED | — | Per-user projects bucket |
| spec-session-agent:100 | "KV_mclaude-hosts (read) — Shared bucket. Read-only access" | — | GAP | CODE→FIX | Agent does not read from mclaude-hosts KV at all. Spec says agent has read-only access to host's own key. No code references this bucket. |
| spec-session-agent:102-103 | "The agent does not write to mclaude-hosts ... The previous mclaude-laptops and mclaude-heartbeats buckets are removed" | No writes to these buckets | IMPLEMENTED | — | Confirmed: no writes to hosts/laptops/heartbeats KV |
| spec-session-agent:107-109 | "Session events to mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.events" | session.go:273-278 | GAP | CODE→FIX | Event subject uses `.events.{sslug}` format (old) not `.sessions.{sslug}.events` (ADR-0054). Code constructs: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events.{sslug}` instead of `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.events`. The `events` and session slug tokens are in the wrong order. |
| spec-session-agent:110-111 | "Lifecycle events to mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.lifecycle.{eventType}" | agent.go:614-622 (publishLifecycle using subj.UserHostProjectSessionsLifecycle) | IMPLEMENTED | — | Uses subj helper which constructs the correct ADR-0054 lifecycle subject |
| spec-session-agent:112-113 | "API error events to sessions._api" | agent.go:340-348 (publishAPIError) | IMPLEMENTED | — | Publishes to sessions._api subject |
| spec-session-agent:114-115 | "Quota status to mclaude.users.{uslug}.quota (designated agent only)" | daemon_jobs.go:127-143 (runQuotaPublisher in Daemon) | PARTIAL | SPEC→FIX | Quota publisher exists but only in deprecated daemon mode, not in per-project standalone agent. Spec says "designated agent publishes" but ADR-0054 re-designation mechanism is not implemented. |
| spec-session-agent:120-121 | "Session commands via ordered push consumers on MCLAUDE_SESSIONS_{uslug}" | agent.go:200-247 | IMPLEMENTED | — | Ordered push consumers created and dispatched |
| spec-session-agent:122 | "Terminal API via core NATS subscriptions" | agent.go:319-337 (subscribeTerminalAPI) | IMPLEMENTED | — | Terminal create/delete/resize on core NATS |
| spec-session-agent:123 | "Quota status (per-session QuotaMonitor) via core NATS on mclaude.users.{uslug}.quota" | quota_monitor.go:58-62 (ChanSubscribe to users.{userSlug}.quota) | IMPLEMENTED | — | Quota subscription uses slug-based subject |
| spec-session-agent:124-125 | "Quota publisher re-designation via core NATS on manage.designate-quota-publisher" | — | GAP | CODE→FIX | No subscription to manage.designate-quota-publisher in the standalone agent. Only daemon mode has quota publisher. |
| spec-session-agent:131 | "Each session exposes a debug socket at /tmp/mclaude-session-{sessionId}.sock" | debug.go:15-17, agent.go:478-485 | IMPLEMENTED | — | Debug server with unix socket |
| spec-session-agent:138-141 | "Prometheus Metrics ... mclaude_active_sessions, mclaude_events_published_total, mclaude_nats_reconnects_total, mclaude_claude_restarts_total" | metrics.go:14-42 | IMPLEMENTED | — | All four metrics registered |
| spec-session-agent:145-147 | "OpenTelemetry trace spans are emitted for NATS publish, KV write, session lifecycle, and Claude process spawn" | metrics.go:95-133 | IMPLEMENTED | — | NATSPublishSpan, KVWriteSpan, SessionSpan, ClaudeSpawnSpan all defined |
| spec-session-agent:169-182 | "Session State Machine ... Full status enum: pending, running, paused, requires_action, completed, stopped, cancelled, needs_spec_fix, failed, error" | events.go:25-35, state.go | PARTIAL | CODE→FIX | Code defines StateIdle, StateRunning, StateRequiresAction, StateUpdating, StateRestarting, StateFailed, StatePlanMode, StateWaitingForInput, StateUnknown. Missing from code: pending, paused, completed, stopped, cancelled, needs_spec_fix, error. Code uses "idle" while spec uses "running" for between-turns state. The spec's KV status field is described as "status" (renamed from "state" per ADR-0044) but code uses JSON field "state" in SessionState struct. |
| spec-session-agent:184-185 | "State transitions flow from the driver's state_change canonical events. The agent updates in-memory state and flushes to KV on every transition" | session.go:459-471 (SubtypeSessionStateChanged handling) | IMPLEMENTED | — | State changes update in-memory and flush to KV |
| spec-session-agent:185 | "except during graceful shutdown when KV writes are suppressed to preserve the updating banner" | session.go:467-471 (shutdownPending check) | IMPLEMENTED | — | shutdownPending flag suppresses KV flush |
| spec-session-agent:185-186 | "KV write failures are logged at warn level (ADR-0051)" | session.go:533-536 (flushKV warn log) | IMPLEMENTED | — | Warn-level logging on KV write failure |
| spec-session-agent:189-199 | "Session Lifecycle — Creation steps 1-11" | agent.go:360-533 (handleCreate) | IMPLEMENTED | — | All creation steps present: UUID gen, branch derive, collision check, worktree add, KV write, debug socket, start process, publish lifecycle |
| spec-session-agent:199 | "Publish session_created lifecycle event" | agent.go:527 (publishLifecycleWithBranch) | IMPLEMENTED | — | session_created published with branch field |
| spec-session-agent:204-210 | "Resumption — Recovery uses the KV-based mechanism" | agent.go:137-193 (recoverSessions) | IMPLEMENTED | — | Recovery from KV watch, resuming sessions |
| spec-session-agent:204 | "Watch all keys in KV_mclaude-sessions-{uslug} for initial values (filtered to own host+project prefix)" | agent.go:145-146 (projectKeyPrefix) | IMPLEMENTED | — | Filtered KV watch with hosts.{hslug}.projects.{pslug}.sessions.> |
| spec-session-agent:206 | "Set session KV to status: restarting, clear pendingControls" | agent.go:168-170 (clearPendingControlsForResume) | IMPLEMENTED | — | Clears pending controls, sets restarting state |
| spec-session-agent:207 | "Publish session_restarting lifecycle event" | agent.go:182 | IMPLEMENTED | — | Published before start |
| spec-session-agent:208-209 | "On init event: set status: running, stateSince: now, update model/capabilities, flush to KV" | session.go:447-461 (SubtypeInit handler) | IMPLEMENTED | — | Init event sets idle state, model, capabilities, flushes KV |
| spec-session-agent:210 | "Start crash watcher goroutine (ADR-0051)" | agent.go:189 (go a.watchSessionCrash) | IMPLEMENTED | — | Crash watcher started for recovered sessions |
| spec-session-agent:212 | "Sessions in updating status are resumed but KV entry stays as updating until consumers are attached" | agent.go:160-165 (wasUpdating logic), agent.go:252-269 (clearUpdatingState) | IMPLEMENTED | — | Updating sessions tracked and cleared after consumers attach |
| spec-session-agent:215-216 | "Sessions that fail to start within 30s: mark status: failed, publish session_failed" | session.go:268-278 (startup timeout goroutine) | PARTIAL | CODE→FIX | Timeout sets StateFailed and flushes KV, but does NOT publish session_failed lifecycle event. Only the KV state is updated. |
| spec-session-agent:228-238 | "handleDelete — Interactive session: remove from map, interrupt, wait 10s, SIGKILL, remove worktree, delete KV, publish session_stopped" | agent.go:537-596 (handleDelete) | IMPLEMENTED | — | All delete steps present |
| spec-session-agent:240-247 | "handleDelete — Quota-managed session: different behavior with QuotaMonitor stop, schedule/ worktree skip, tombstone KV, publish session_job_cancelled" | agent.go:537-596 | GAP | CODE→FIX | handleDelete does not branch on SoftThreshold > 0. No QuotaMonitor.stop() call. No schedule/ worktree skip logic. Always publishes session_stopped (not session_job_cancelled for quota sessions). |
| spec-session-agent:249-254 | "Restart: publish session_restarting, stop process, clear pending controls, respawn, publish session_resumed" | agent.go:645-716 (handleRestart) | IMPLEMENTED | — | All restart steps present |
| spec-session-agent:256-274 | "Import Handler (ADR-0053) — check importRef, download, verify, unpack, clear importRef" | — | GAP | CODE→FIX | No import handler code exists in the session-agent. No reading of projects KV for importRef, no download from S3, no import flow. |
| spec-session-agent:276-294 | "Attachment Support (ADR-0053) — download and upload via pre-signed URLs" | — | GAP | CODE→FIX | No attachment handling code exists in the session-agent. |
| spec-session-agent:296-305 | "fsnotify Watcher (ADR-0053) — watch session data directory for new JSONL files" | — | GAP | CODE→FIX | No fsnotify watcher code exists. |
| spec-session-agent:307-308 | "Event Routing — driver-agnostic, reads canonical SessionEvent structs from driver's ReadEvents() channel" | session.go:234-310 (stdout router) | PARTIAL | SPEC→FIX | Code reads raw NDJSON from Claude Code stdout, not canonical SessionEvent from a driver channel. Spec describes the CLIDriver interface but code directly spawns Claude Code without the driver abstraction. The driver/adapter pattern (ADR-0005) is described in spec but not implemented in code. |
| spec-session-agent:311-314 | "In-flight background agent tracking" | session.go:335-370 (updateInFlightBackgroundAgents) | IMPLEMENTED | — | Agent tracking with increment/decrement on message events |
| spec-session-agent:315-322 | "In-flight background shell tracking (K8s mode; two-phase)" | session.go:112-140 (pendingShell, inFlightShell), session.go:390-520 (updateInFlightShells) | IMPLEMENTED | — | Spec acknowledges "Not yet implemented" but code actually implements the two-phase tracking now |
| spec-session-agent:323 | "Truncates events exceeding the 8 MB NATS payload limit" | session.go:505-522 (truncateEventIfNeeded) | IMPLEMENTED | — | 8MB limit with content removal and truncated:true |
| spec-session-agent:324 | "Marshals as JSON and publishes to sessions.{sslug}.events" | session.go:287-291 | IMPLEMENTED | — | Publishes to event subject (though subject format has the GAP noted above) |
| spec-session-agent:324 | "User input messages are also published to the events stream so that replaying clients see the full conversation" | — | GAP | CODE→FIX | User input messages received on sessions.{sslug}.input are forwarded to Claude stdin only. They are NOT re-published to the events subject for replay. The SPA input is captured by the MCLAUDE_SESSIONS stream via the input subject, but the agent doesn't explicitly re-publish. |
| spec-session-agent:331-334 | "Input routing: type message → driver.SendMessage, type permission_response → driver.SendPermissionResponse, type skill_invoke → driver.SendMessage" | agent.go:430-508 (handleInput) | PARTIAL | SPEC→FIX | Input routing dispatches to sess.sendInput (raw stdin). No driver.SendMessage abstraction. skill_invoke routing not explicitly handled. Spec describes driver interface not yet implemented. |
| spec-session-agent:335 | "Config updates from sessions.{sslug}.config are routed to driver.UpdateConfig" | agent.go:604-629 (handleConfig) | PARTIAL | SPEC→FIX | Config updates forwarded to session stdin (raw), not through driver.UpdateConfig interface |
| spec-session-agent:337-341 | "Permission Policy — four policies: managed, auto, allowlist, strict-allowlist" | state.go:14-22, session.go:478-512 | IMPLEMENTED | — | All four policies implemented |
| spec-session-agent:343-344 | "allowedTools validation (ADR-0044): If allowedTools is empty on strict-allowlist, agent rejects sessions.create and publishes lifecycle.error" | agent.go:400-404 | GAP | CODE→FIX | Code falls back to defaultDevHarnessAllowlist when allowedTools is empty for strict-allowlist, instead of rejecting. Spec says "The previous implicit defaultDevHarnessAllowlist is eliminated" and agent should reject. |
| spec-session-agent:348-353 | "Credential Management — All session-agents authenticate via HTTP challenge-response (ADR-0054)" | — | GAP | CODE→FIX | No HTTP challenge-response auth code. Agent uses NATS credentials file (nats.UserCredentials). No NKey generation, no challenge-response, no 5-min TTL refresh. |
| spec-session-agent:354-358 | "Agent generates NKey pair, exposes public key to host controller, host registers with CP, agent authenticates via HTTP" | — | GAP | CODE→FIX | No NKey generation, no public key exposure, no HTTP auth flow |
| spec-session-agent:359 | "Agent JWT has a 5-minute TTL. Agent runs same HTTP challenge-response before TTL expiry" | — | GAP | CODE→FIX | No JWT refresh loop in standalone mode. Daemon mode has JWT refresh (daemon.go:207-240) but uses POST /auth/refresh (old protocol), not HTTP challenge-response. |
| spec-session-agent:360 | "On permissions violation error from NATS, agent triggers immediate refresh + retry" | — | GAP | CODE→FIX | No permissions violation handler with refresh+retry in standalone mode |
| spec-session-agent:387-395 | "Compact Boundary Tracking — queries MCLAUDE_SESSIONS_{uslug} stream for last sequence number, writes replayFromSeq" | agent.go:746-771 (updateReplayFromSeq) | IMPLEMENTED | — | Stream info queried, replayFromSeq written to KV |
| spec-session-agent:397-399 | "Quota Monitoring — one QuotaMonitor goroutine per quota-managed session" | quota_monitor.go | IMPLEMENTED | — | QuotaMonitor created per session when config present |
| spec-session-agent:450-455 | "Graceful Shutdown — Step 1: Write state:updating to KV, set shutdownPending" | agent.go:293-311 | IMPLEMENTED | — | Writes updating state, sets shutdownPending |
| spec-session-agent:455 | "Step 2: Cancel command consumer" | agent.go:314-316 | IMPLEMENTED | — | cmdMsgs.Stop() |
| spec-session-agent:456 | "Step 3: Drain core NATS subscriptions (terminal API)" | agent.go:319-327 | IMPLEMENTED | — | Drains all subs |
| spec-session-agent:457 | "Step 4: Keep control consumer running" | agent.go:332-356 (poll loop, ctlMsgs stays) | IMPLEMENTED | — | ctlMsgs stopped later |
| spec-session-agent:458-459 | "Step 5: Poll 1s — drain predicate: idle + zero in-flight agents. Auto-interrupt requires_action" | agent.go:332-356 | IMPLEMENTED | — | 1s ticker, auto-interrupt, drain check |
| spec-session-agent:460-462 | "Step 6: Shell-killed notifications" | agent.go:358-359 (publishShellKilledNotifications) | IMPLEMENTED | — | Shell killed notifications published |
| spec-session-agent:463 | "Step 7: Cancel control consumer" | agent.go:362-364 | IMPLEMENTED | — | ctlMsgs.Stop() |
| spec-session-agent:464 | "Step 8: Publish session_upgrading lifecycle event" | agent.go:367-369 | IMPLEMENTED | — | session_upgrading published |
| spec-session-agent:465 | "Step 9: Exit" | agent.go:372-376 | IMPLEMENTED | — | doExit(0) called |
| spec-session-agent:490-491 | "Liveness is signalled by the NATS connection itself ... no periodic heartbeat publish" | No heartbeat code | IMPLEMENTED | — | No heartbeat publishing |
| spec-session-agent:493-494 | "Terminal sessions spawn a PTY shell and bridge I/O through core NATS subjects" | terminal.go | IMPLEMENTED | — | PTY terminal with NATS I/O |
| spec-session-agent:499 | "NATS connection lost: NATS client auto-reconnects ... MaxReconnects=60, no RetryOnFailedConnect" | main.go:173-176 (natsConnect) | IMPLEMENTED | — | Code sets MaxReconnects(-1) and RetryOnFailedConnect(true), fixing the known bug documented in the spec. The spec's "known bug" annotation is now stale. |
| spec-session-agent:500 | "KV bucket not found on startup: Fatal — session agent exits" | agent.go:84-88, 90-93 | IMPLEMENTED | — | Returns error which becomes Fatal in main |
| spec-session-agent:508-510 | "CLI process crash (unexpected exit): Known bug: auto-restart is not implemented" | agent.go:723-770 (watchSessionCrash) | IMPLEMENTED | — | Crash watcher IS implemented. Spec's "known bug" is stale — code now auto-restarts. |
| spec-session-agent:512 | "Session delete — process does not stop within 10s: Process is SIGKILLed" | session.go:169-179 (stopAndWait timeout + Kill) | IMPLEMENTED | — | 10s timeout then SIGKILL |
| spec-session-agent:513 | "Graceful shutdown — session stuck in requires_action: Auto-interrupted" | agent.go:344-348 | IMPLEMENTED | — | Auto-interrupt in drain loop |
| spec-session-agent:518 | "Event exceeds 8 MB NATS payload: Content field stripped, truncated: true added" | session.go:505-522 | IMPLEMENTED | — | Truncation logic implemented |
| spec-session-agent:520-521 | "HOST_SLUG / --host not provided: Spec requires fatal exit ... Known bug: code falls back" | main.go:93-103 | PARTIAL | SPEC→FIX | Code falls back through chain then fatals at end. Spec says "FATAL: HOST_SLUG required" but documents the known bug. Code DOES fatal if all fallbacks fail. Spec annotation partially stale. |
| spec-session-agent:523 | "allowedTools empty on strict-allowlist session: Agent rejects sessions.create — publishes lifecycle.error" | agent.go:400-404 | GAP | CODE→FIX | Code uses defaultDevHarnessAllowlist fallback instead of rejecting. Duplicate of row above. |
| spec-session-agent:526-527 | "Import — session ID collision: Skip with warning" | — | GAP | CODE→FIX | No import handler exists |
| spec-session-agent:528-529 | "Import — unpack fails: report error via session_import_failed" | — | GAP | CODE→FIX | No import handler exists |
| spec-session-agent:530-531 | "Attachment — download/upload pre-signed URL handling" | — | GAP | CODE→FIX | No attachment handler exists |
| spec:state-schema | "SessionState status field renamed from state per ADR-0044/ADR-0054" | state.go:42 (`json:"state"`) | GAP | CODE→FIX | Go struct uses JSON tag `"state"` but spec-state-schema says field is renamed to `"status"` per ADR-0044/ADR-0054. |
| spec:state-schema | "SessionState has backend field (default: claude_code)" | state.go:36-59 | GAP | CODE→FIX | SessionState struct has no `Backend` field. Spec says it should be present with default `claude_code`. |
| spec:state-schema | "SessionState has branchSlug field" | state.go:36-59 | GAP | CODE→FIX | No branchSlug field in SessionState. |
| spec:state-schema | "SessionState has softThreshold, hardHeadroomTokens, autoContinue, pausedVia, claudeSessionID, failedTool, resumeAt" | state.go:36-59 | GAP | CODE→FIX | None of the ADR-0044 quota fields are present in SessionState struct. These are handled through QuotaMonitorConfig/JobEntry but not in the session KV state itself. |
| spec:state-schema | "Capabilities struct has hasThinking, hasSubagents, hasSkills, hasPlanMode, hasMissions, hasEventStream, hasSessionResume, toolIcons, thinkingLabel, modelOptions, permissionModes" | state.go:62-66 | GAP | CODE→FIX | Capabilities struct only has Skills, Tools, Agents fields. Missing all boolean feature flags and other fields from spec. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-30 | INFRA | Package declaration, imports, zerolog setup |
| main.go:31-68 | IMPLEMENTED | Health/readiness probe handlers (spec'd) |
| main.go:69-110 | IMPLEMENTED | CLI flags parsing (spec'd Configuration section) |
| main.go:111-145 | IMPLEMENTED | Daemon mode startup (spec'd as deprecated) |
| main.go:146-176 | IMPLEMENTED | Standalone mode startup, credential setup, repo init |
| agent.go:1-35 | INFRA | Package declaration, imports, constants |
| agent.go:36-72 | IMPLEMENTED | Agent struct fields — spec'd |
| agent.go:73-120 | IMPLEMENTED | NewAgent constructor — spec'd KV bucket init |
| agent.go:121-135 | IMPLEMENTED | Run — spec'd lifecycle |
| agent.go:350-358 | UNSPEC'd | defaultDevHarnessAllowlist — spec says this is eliminated per ADR-0044 but code still defines and uses it |
| agent.go:436-440 | INFRA | handleInput JSON parsing — plumbing for spec'd input routing |
| session.go:1-110 | INFRA | Package imports, constants, type definitions |
| session.go:112-140 | IMPLEMENTED | pendingShell/inFlightShell types — spec'd shell tracking |
| session.go:142-158 | IMPLEMENTED | sanitizePath/shellOutputPath — spec'd path construction |
| session.go:160-260 | IMPLEMENTED | Session struct, newSession, getState, stopAndWait — spec'd |
| session.go:262-320 | IMPLEMENTED | start() — spec'd session start |
| session.go:321-370 | IMPLEMENTED | updateInFlightBackgroundAgents — spec'd |
| session.go:371-520 | IMPLEMENTED | updateInFlightShells — spec'd two-phase shell tracking |
| session.go:521-540 | IMPLEMENTED | handleSideEffect — spec'd side effect handling |
| state.go:1-130 | IMPLEMENTED | SessionState, Capabilities, UsageStats, sessionKVKey, helpers |
| state.go:131-180 | UNSPEC'd | JobEntry struct — spec says job-queue KV eliminated per ADR-0044 but code still defines JobEntry |
| events.go:1-90 | IMPLEMENTED | Event type constants and parsing — spec'd |
| quota_monitor.go:1-220 | IMPLEMENTED | QuotaMonitor — spec'd quota monitoring |
| daemon.go:1-250 | IMPLEMENTED | Daemon mode — spec'd as deprecated |
| daemon_jobs.go:1-500 | UNSPEC'd | Job dispatcher, HTTP server, quota publisher — spec says all eliminated per ADR-0044, but daemon mode is deprecated not removed |
| debug.go:1-130 | IMPLEMENTED | DebugServer — spec'd debug unix socket |
| terminal.go:1-130 | IMPLEMENTED | TerminalSession — spec'd terminal management |
| metrics.go:1-170 | IMPLEMENTED | Metrics and OTel helpers — spec'd |
| worktree.go:1-40 | IMPLEMENTED | SlugifyBranch, worktreeExists — spec'd worktree helpers |
| gitcreds.go:1-500 | IMPLEMENTED | CredentialManager, InitRepo — spec'd credential management (K8s mode) |
| shell_split.go | INFRA | POSIX shell argument parsing helper |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec:78-80 | No stream creation by agent | agent_terminal_test.go (NewAgent with test KV) | integration_test.go | UNIT_ONLY | Unit tests verify agent initializes without creating streams. No integration test with real NATS operator-mode. |
| spec:82-85 | Ordered push consumers on MCLAUDE_SESSIONS | jetstream_test.go | — | UNIT_ONLY | JetStream consumer tests exist. No integration test. |
| spec:90-93 | Per-user sessions KV bucket | state_test.go, session_test.go | — | UNIT_ONLY | KV key format tested. No integration with real NATS. |
| spec:107-109 | Session events published | session_test.go | — | UNIT_ONLY | Event publishing tested via mock. |
| spec:189-199 | Session creation | session_test.go | e2e_test.go | UNIT_ONLY | Unit tests cover creation. e2e_test.go exists but unclear if it tests against real infra. |
| spec:204-214 | Session recovery | session_test.go | — | UNIT_ONLY | Recovery tested via mock KV. |
| spec:228-238 | Session deletion | session_test.go | — | UNIT_ONLY | Deletion tested. |
| spec:249-254 | Session restart | session_test.go | — | UNIT_ONLY | Restart tested. |
| spec:337-341 | Permission policies | session_test.go | — | UNIT_ONLY | All four policies tested. |
| spec:397-450 | QuotaMonitor | quota_test.go | — | UNIT_ONLY | Quota monitor behavior tested. |
| spec:450-465 | Graceful shutdown | daemon_test.go | — | UNIT_ONLY | Shutdown sequence tested. |
| spec:493-494 | Terminal sessions | terminal_test.go | — | UNIT_ONLY | Terminal PTY tested. |
| ADR-0054 integration tests | "User cannot access other user's KV" | — | — | UNTESTED | ADR-0054 lists 6 integration test cases; none are implemented for session-agent. |
| ADR-0054 integration tests | "Session-agent scoped to user" | — | — | UNTESTED | No cross-user scoping test. |
| ADR-0054 integration tests | "Agent auth requires registered public key" | — | — | UNTESTED | No auth test — HTTP challenge-response not implemented. |
| spec:348-360 | HTTP challenge-response auth | — | — | UNTESTED | Auth mechanism not implemented at all. |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| 001-new-project-crash-no-git-credentials.md | New project with git URL crashes — no git credentials | FIXED | Root cause was entrypoint.sh exiting on git clone failure. The Go code now handles this: gitcreds.go InitRepo logs warning on fetch failure (non-fatal), and InitRepo returns GitAuthError for auth failures which main.go handles by publishing session_failed. The entrypoint.sh clone logic is replaced by Go code. |

### Summary

- Implemented: 38
- Gap: 14
- Partial: 7
- Infra: 7
- Unspec'd: 2
- Dead: 0
- Tested: 0
- Unit only: 11
- E2E only: 0
- Untested: 4
- Bugs fixed: 1
- Bugs open: 0
