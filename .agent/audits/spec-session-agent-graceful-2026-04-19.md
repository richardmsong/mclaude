## Run: 2026-04-19T00:00:00Z

Scope: session-agent component vs docs/plan-graceful-upgrades.md (full spec)
Focus: graceful shutdown drain predicate (SIGTERM handler section) + full spec sweep.

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| plan-graceful-upgrades.md:5 | "session-agent finishes its current Claude turn, queues incoming messages in JetStream, writes an 'Updating...' state to KV, and exits cleanly" | agent.go:432-534 gracefulShutdown | IMPLEMENTED | — | Full sequence implemented |
| plan-graceful-upgrades.md:51-64 | MCLAUDE_API stream: Name=MCLAUDE_API, Subjects=["mclaude.*.*.api.sessions.>"], Retention=LimitsPolicy, MaxAge=1h, Storage=FileStorage, Discard=DiscardOld | agent.go:119-128 | IMPLEMENTED | — | All fields match exactly |
| plan-graceful-upgrades.md:72-85 | Command consumer: Name=sa-cmd-{userId}-{projectId}, FilterSubjects=[create,delete,input,restart], AckPolicy=Explicit, AckWait=60s, MaxDeliver=5 | agent.go:276-294 | IMPLEMENTED | — | All consumer config fields match |
| plan-graceful-upgrades.md:87-95 | Control consumer: Name=sa-ctl-{userId}-{projectId}, FilterSubjects=[control], AckPolicy=Explicit, AckWait=60s, MaxDeliver=5 | agent.go:296-305 | IMPLEMENTED | — | All consumer config fields match |
| plan-graceful-upgrades.md:99-114 | JetStream fetch loop: runConsumer with Fetch(10, FetchMaxWait(5s)), ctx.Err() check, backoff on error | agent.go:323-356 | IMPLEMENTED | — | Exact pattern including backoff |
| plan-graceful-upgrades.md:119-131 | jsToNatsMsg adapter: wraps jetstream.Msg into *nats.Msg, .Reply is empty | agent.go:362-368 | IMPLEMENTED | — | Exact match |
| plan-graceful-upgrades.md:133-145 | dispatchCmd: routes by subject suffix to handleCreate/Delete/Input/Restart | agent.go:371-385 | IMPLEMENTED | — | Exact match |
| plan-graceful-upgrades.md:147 | Ack after handler returns (not before); panic = no ack = redeliver after AckWait | agent.go:349-354 | IMPLEMENTED | — | msg.Ack() called after dispatch(msg) |
| plan-graceful-upgrades.md:150 | Stopping consumer: cancel context → Fetch returns immediately | agent.go:310-316, 519-521 | IMPLEMENTED | — | cmdCancel/ctlCancel pattern |
| plan-graceful-upgrades.md:158-161 | SIGTERM step 1: Write state:"updating" + stateSince:now to session KV. Set sess.shutdownPending=true. Do NOT modify in-memory sess.state.State | agent.go:434-461 | IMPLEMENTED | — | KV write with StateUpdating, shutdownPending set, sess.state.State not mutated |
| plan-graceful-upgrades.md:162 | SIGTERM step 2: Cancel command consumer context | agent.go:463-466 | IMPLEMENTED | — | cmdCancel() called |
| plan-graceful-upgrades.md:163 | SIGTERM step 3: Drain core NATS subscriptions (terminal.create, terminal.delete, terminal.resize) | agent.go:468-479 | IMPLEMENTED | — | Drains a.subs |
| plan-graceful-upgrades.md:164 | SIGTERM step 4: Keep control consumer running (its context is NOT cancelled) | agent.go:481 comment + 519 | IMPLEMENTED | — | ctlCancel not called until step 6 after poll loop |
| plan-graceful-upgrades.md:165-167 | SIGTERM step 5: Poll loop 1s tick, evaluate drain predicate, break when ALL sessions satisfy it | agent.go:484-516 | IMPLEMENTED | — | ticker 1s, allDone logic |
| plan-graceful-upgrades.md:173-175 | Drain predicate: sess.getState().State == StateIdle AND sess.inFlightBackgroundAgents == 0 | agent.go:498-511 | IMPLEMENTED | — | Both conditions checked under sess.mu.Lock |
| plan-graceful-upgrades.md:177 | Pending permission prompts NOT blocking: sessions in StateRequiresAction get synthetic interrupt every poll tick | agent.go:503-507 | IMPLEMENTED | — | sendInterrupt() called for StateRequiresAction |
| plan-graceful-upgrades.md:179 | In-memory state is source of truth for poll, NOT the KV state | agent.go:497-499 | IMPLEMENTED | — | sess.state.State read directly (via mu.Lock), not from KV |
| plan-graceful-upgrades.md:168 | SIGTERM step 6: Cancel control consumer context | agent.go:519-521 | IMPLEMENTED | — | ctlCancel() |
| plan-graceful-upgrades.md:169 | SIGTERM step 7: Publish lifecycle event "session_upgrading" for each session | agent.go:524-526 | IMPLEMENTED | — | publishLifecycle(id, "session_upgrading") |
| plan-graceful-upgrades.md:170 | SIGTERM step 8: Exit(0) | agent.go:529-533 | IMPLEMENTED | — | doExit(0) (overridable in tests) |
| plan-graceful-upgrades.md:183-185 | inFlightBackgroundAgents counter: +1 on assistant message with Agent tool_use where run_in_background==true | session.go:323-353 | IMPLEMENTED | — | updateInFlightBackgroundAgents, EventTypeAssistant branch |
| plan-graceful-upgrades.md:185 | inFlightBackgroundAgents counter: -1 (floored at zero) on top-level user message with origin.kind=="task-notification" | session.go:355-372 | IMPLEMENTED | — | EventTypeUser branch, floored at 0 |
| plan-graceful-upgrades.md:183 | inFlightBackgroundAgents int field guarded by sess.mu | session.go:74 (field decl), 344-352 (incr), 366-370 (decr) | IMPLEMENTED | — | All accesses under sess.mu.Lock |
| plan-graceful-upgrades.md:189 | KV write suppression: SubtypeSessionStateChanged handler skips flushKV while shutdownPending==true but STILL updates in-memory state | session.go:403-419 | IMPLEMENTED | — | state.State updated, pending checked, flushKV skipped when pending |
| plan-graceful-upgrades.md:200-211 | Run() startup sequence: recoverSessions → createJetStreamConsumers → subscribeTerminalAPI → clearUpdatingState → runHeartbeat → <-ctx.Done() → gracefulShutdown | agent.go:161-177 | IMPLEMENTED | — | Exact order |
| plan-graceful-upgrades.md:213-218 | recoverSessions: "updating" sessions — treat as idle for resume, do NOT write KV yet; clearUpdatingState writes later | agent.go:210-230 | IMPLEMENTED | — | wasUpdating branch skips writeSessionKV, adds to pendingUpdatingIDs |
| plan-graceful-upgrades.md:222 | Reply mechanism: reply() is no-op when msg.Reply=="" | agent.go:1202-1205 | IMPLEMENTED | — | if msg.Reply == "" { return } |
| plan-graceful-upgrades.md:225-231 | Error reply mechanism: api_error published to mclaude.{userId}.{projectId}.events._api | agent.go:563-574 publishAPIError | IMPLEMENTED | — | Subject format matches spec |
| plan-graceful-upgrades.md:240-247 | api_error payload: {type, request_id, operation, error} | agent.go:567-572 | IMPLEMENTED | — | All four fields present |
| plan-graceful-upgrades.md:249-262 | handleCreate request struct: +RequestID field | agent.go:588-597 | IMPLEMENTED | — | RequestID string json:"requestId" present |
| plan-graceful-upgrades.md:263-266 | handleDelete request struct: +RequestID field | agent.go:800-804 | IMPLEMENTED | — | RequestID string json:"requestId" present |
| plan-graceful-upgrades.md:267-270 | handleRestart request struct: +RequestID field | agent.go:973-977 | IMPLEMENTED | — | RequestID string json:"requestId" present |
| plan-graceful-upgrades.md:344-350 | Helm values.yaml: terminationGracePeriodSeconds: 86400 | (helm chart — out of scope for this session-agent audit) | — | — | Covered by helm audit |
| plan-graceful-upgrades.md:568-579 | Session State Constants: StateUpdating = "updating" in events.go | events.go:29 | IMPLEMENTED | — | StateUpdating = "updating" present |
| plan-graceful-upgrades.md:580-593 | clearPendingControlsForResume handles "updating" as idle | state.go:90-93 | IMPLEMENTED | — | Unconditionally sets StateIdle; spec note says no code change needed |

### KEY FOCUS AREA: Drain Predicate Spec Detail-by-Detail Check

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| plan-graceful-upgrades.md:158 | "Step 1 writes state:'updating' to KV ONLY (not in-memory sess.state.State)" | agent.go:449-461: builds kvSt=st copy, sets kvSt.State=StateUpdating, writes kvSt; does NOT do sess.state.State=StateUpdating | IMPLEMENTED | — | sess.state.State is never assigned in step 1 |
| plan-graceful-upgrades.md:158 | "Step 1 sets sess.shutdownPending = true" | agent.go:455-457: sess.mu.Lock(); sess.shutdownPending=true; sess.mu.Unlock() | IMPLEMENTED | — | Guarded by mu |
| plan-graceful-upgrades.md:173 | "Drain predicate: sess.state.State == StateIdle" | agent.go:497: state := sess.state.State (via mu.Lock at 496) | IMPLEMENTED | — | |
| plan-graceful-upgrades.md:173 | "Drain predicate: sess.inFlightBackgroundAgents == 0" | agent.go:498: inFlight := sess.inFlightBackgroundAgents (via same mu.Lock) | IMPLEMENTED | — | Both in same critical section |
| plan-graceful-upgrades.md:177 | "Pending-control interrupt: on every poll tick, sessions in StateRequiresAction get a synthetic interrupt" | agent.go:503-507: if state==StateRequiresAction { sess.sendInterrupt(); allDone=false; continue } | IMPLEMENTED | — | |
| plan-graceful-upgrades.md:189 | "SubtypeSessionStateChanged handler skips flushKV while shutdownPending==true (but STILL updates in-memory state)" | session.go:406-419: s.state.State=ev.State; pending=s.shutdownPending; if !pending { s.flushKV(writeKV) } | IMPLEMENTED | — | In-memory update always happens; KV flush gated by !pending |
| plan-graceful-upgrades.md:183 | "Counter +1 on assistant message containing tool_use with name=='Agent' AND input.run_in_background==true" | session.go:326-353: checks block.Type=='tool_use' && block.Name=='Agent', parses input.RunInBackground, increments if true | IMPLEMENTED | — | |
| plan-graceful-upgrades.md:185 | "Counter -1 (floored at zero) on top-level user message with origin.kind=='task-notification'" | session.go:355-372: checks msg.Origin.Kind=='task-notification', if>0 decrements | IMPLEMENTED | — | Floor at 0 via if s.inFlightBackgroundAgents > 0 guard |

### Phase 1 — State Schema Cross-Check

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| plan-state-schema.md:93 | "state": "idle \| busy \| error" (mclaude-sessions KV value) | events.go:26-30: StateIdle="idle", StateRunning="running", StateRequiresAction="requires_action", StateUpdating="updating" | GAP | SPEC→FIX | State schema says "idle\|busy\|error" but code uses "idle\|running\|requires_action\|updating". The code's values are correct (they match Claude Code's emitted session_state_changed events). The state schema document is outdated and must be updated to reflect the real enum. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| agent.go:1-18 | INFRA | Package/imports — standard boilerplate |
| agent.go:20-26 | INFRA | Consts: heartbeatInterval, sessionDeleteTimeout, kvBucket names — infrastructure for spec'd behavior |
| agent.go:28-72 | INFRA | Agent struct fields — all fields implement spec'd capabilities (subs, cmdConsumer, ctlConsumer, doExit override for tests, writeSessionKVFn for tests, pendingUpdatingIDs for startup recovery, credMgr/gitIdentityID for github-oauth spec) |
| agent.go:74-157 | INFRA | NewAgent constructor: creates js, opens KV buckets, creates MCLAUDE_EVENTS and MCLAUDE_API streams, wires reconnect handler. All spec'd by plan-graceful-upgrades.md and plan-k8s-integration.md |
| agent.go:159-177 | INFRA | Run() method — implements startup sequence from plan-graceful-upgrades.md:200-211 |
| agent.go:180-265 | INFRA | recoverSessions — implements plan-graceful-upgrades.md:213-218 and plan-k8s-integration.md recovery |
| agent.go:267-319 | INFRA | createJetStreamConsumers — implements plan-graceful-upgrades.md:69-97 |
| agent.go:321-356 | INFRA | runConsumer — implements plan-graceful-upgrades.md:99-114 |
| agent.go:358-390 | INFRA | jsToNatsMsg, dispatchCmd, dispatchCtl — implements plan-graceful-upgrades.md:119-145 |
| agent.go:392-415 | INFRA | clearUpdatingState — implements plan-graceful-upgrades.md:213-218 startup recovery spec |
| agent.go:417-534 | INFRA | gracefulShutdown — implements plan-graceful-upgrades.md:154-191 |
| agent.go:536-561 | INFRA | subscribeTerminalAPI — implements plan-k8s-integration.md terminal API subscriptions |
| agent.go:563-574 | INFRA | publishAPIError — implements plan-graceful-upgrades.md:225-247 |
| agent.go:576-582 | INFRA | defaultDevHarnessAllowlist — referenced by handleCreate for permPolicy setup |
| agent.go:584-793 | INFRA | handleCreate — implements plan-graceful-upgrades.md:249-262 and plan-k8s-integration.md create handler |
| agent.go:795-870 | INFRA | handleDelete — implements plan-graceful-upgrades.md:263-266 and plan-k8s-integration.md delete handler |
| agent.go:872-919 | INFRA | handleInput — implements plan-k8s-integration.md input handler |
| agent.go:921-967 | INFRA | handleControl — implements plan-k8s-integration.md control handler |
| agent.go:969-1058 | INFRA | handleRestart — implements plan-graceful-upgrades.md:267-270 and plan-k8s-integration.md restart handler |
| agent.go:1060-1076 | INFRA | runHeartbeat — implements plan-k8s-integration.md heartbeat spec |
| agent.go:1078-1094 | INFRA | writeSessionKV — necessary infrastructure for all KV-writing spec behavior |
| agent.go:1096-1109 | INFRA | publishLifecycle — implements lifecycle event publishing (plan-state-schema.md lifecycle events) |
| agent.go:1111-1123 | INFRA | publishLifecycleFailed — implements session_failed lifecycle event |
| agent.go:1125-1140 | INFRA | publishLifecycleExtra — implements quota/job lifecycle events |
| agent.go:1142-1154 | INFRA | publishPermDenied — implements session_permission_denied lifecycle event |
| agent.go:1156-1197 | INFRA | updateReplayFromSeq — implements replayFromSeq tracking for plan-k8s-integration.md |
| agent.go:1199-1215 | INFRA | reply() — implements plan-graceful-upgrades.md:222 no-op when Reply=="" |
| agent.go:1217-1240 | INFRA | worktreeInUse, sessionForRequest — helper methods for handleCreate/handleControl |
| agent.go:1242-1246 | INFRA | controlResponse struct — necessary for handleControl |
| agent.go:1248-1272 | INFRA | gitWorktreeAdd/Remove — implements plan-github-oauth.md git operations |
| agent.go:1274-1384 | INFRA | handleTerminalCreate/Delete/Resize — implements plan-k8s-integration.md terminal API |
| session.go:1-13 | INFRA | Package/imports |
| session.go:14-18 | INFRA | startupTimeout/maxEventBytes consts — spec'd by plan-graceful-upgrades.md (8MB NATS limit) |
| session.go:20-75 | INFRA | Session struct — all fields implement spec'd behavior |
| session.go:77-92 | INFRA | newSession — constructor |
| session.go:94-99 | INFRA | getState — thread-safe state accessor used by drain predicate |
| session.go:101-144 | INFRA | stopAndWait — used by handleDelete/handleRestart |
| session.go:146-155 | INFRA | sendInterrupt — used by gracefulShutdown pending-control interrupt spec |
| session.go:157-315 | INFRA | start() — implements plan-k8s-integration.md Claude process lifecycle |
| session.go:317-373 | INFRA | updateInFlightBackgroundAgents — implements plan-graceful-upgrades.md:183-185 |
| session.go:375-483 | INFRA | handleSideEffect — implements state change → KV write, KV suppression during drain |
| session.go:485-515 | INFRA | shouldAutoApprove/buildAutoApproveResponse — implements plan-k8s-integration.md permission policy |
| session.go:517-538 | INFRA | truncateEventIfNeeded — implements plan-k8s-integration.md 8MB NATS limit |
| session.go:540-558 | INFRA | flushKV/sendInput/clearPendingControl — helper methods for spec'd side effects |
| session.go:560-579 | INFRA | stop/waitDone — used by other helpers |
| events.go:1-103 | INFRA | All event type constants, structs — spec'd in plan-graceful-upgrades.md and plan-k8s-integration.md |
| state.go:1-157 | INFRA | SessionState/UsageStats/etc. structs, helpers — all implement plan-state-schema.md |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| plan-graceful-upgrades.md:158 (step 1: KV only, not in-memory) | Write state:"updating" to KV only, set shutdownPending | jetstream_test.go:423 TestGracefulShutdownWritesUpdatingStateKVOnly | None (no full integration with real signal) | UNIT_ONLY |
| plan-graceful-upgrades.md:173 (drain predicate: idle AND inFlight==0) | Exit when all idle + no in-flight agents | jetstream_test.go:521 TestGracefulShutdownExitsWhenAllIdle | None | UNIT_ONLY |
| plan-graceful-upgrades.md:183-185 (inFlight counter blocks drain) | Blocks while inFlight>0, exits after it reaches 0 | jetstream_test.go:559 TestGracefulShutdownBlocksOnInFlightBackgroundAgents | None | UNIT_ONLY |
| plan-graceful-upgrades.md:177 (pending-control interrupt) | Auto-interrupt StateRequiresAction sessions each tick | jetstream_test.go:611 TestGracefulShutdownInterruptsRequiresAction | None | UNIT_ONLY |
| plan-graceful-upgrades.md:189 (KV suppression) | SubtypeSessionStateChanged skips flushKV when shutdownPending | jetstream_test.go:1130 TestSessionStateChangedSkipsKVFlushWhenShutdownPending | None | UNIT_ONLY |
| plan-graceful-upgrades.md:183 (+1 on Agent run_in_background) | Counter increments on Agent tool_use with run_in_background:true | jetstream_test.go:1186 TestUpdateInFlightBGAgentsIncrement | None | UNIT_ONLY |
| plan-graceful-upgrades.md:185 (-1 on task-notification) | Counter decrements on user message with origin.kind=="task-notification" | jetstream_test.go:1229 TestUpdateInFlightBGAgentsDecrement | None | UNIT_ONLY |
| plan-graceful-upgrades.md:51-64 (MCLAUDE_API stream) | Stream created with correct config | jetstream_test.go:183 TestMCLAUDE_APIStreamCreated | None | UNIT_ONLY |
| plan-graceful-upgrades.md:72-95 (two consumers) | Both consumers created with correct config | jetstream_test.go:237 TestJetStreamConsumersCreated | None | UNIT_ONLY |
| plan-graceful-upgrades.md:99-114 (fetch loop) | Messages dispatched via runConsumer | jetstream_test.go:337 TestRunConsumerDispatchesMessages | None | UNIT_ONLY |
| plan-graceful-upgrades.md:119-131 (jsToNatsMsg) | Adapter wraps fields correctly | jetstream_test.go:86 TestJsToNatsMsg | None | UNIT_ONLY |
| plan-graceful-upgrades.md:133-145 (dispatchCmd routing) | Routes by subject suffix | jetstream_test.go:127 TestDispatchCmdRouting | None | UNIT_ONLY |
| plan-graceful-upgrades.md:213-218 (recoverSessions skips KV write for updating) | KV not written for updating sessions during recovery | jetstream_test.go:792 TestRecoverSessionsSkipsKVWriteForUpdating | None | UNIT_ONLY |
| plan-graceful-upgrades.md:213-218 (clearUpdatingState) | Writes idle after consumers attached | jetstream_test.go:674 TestClearUpdatingState | None | UNIT_ONLY |
| plan-graceful-upgrades.md:225-247 (publishAPIError) | Correct payload and subject | jetstream_test.go:880 TestPublishAPIError | None | UNIT_ONLY |
| plan-graceful-upgrades.md:249-270 (RequestID in error events) | handleCreate/Delete/Restart echo requestId in errors | jetstream_test.go:945,999 TestHandleCreateErrorPublishesAPIError, TestHandleRestartErrorPublishesAPIError | None | UNIT_ONLY |
| plan-graceful-upgrades.md:222 (reply no-op) | reply() is no-op when Reply=="" | jetstream_test.go:1047 TestReplyNoOpWhenReplyEmpty | None | UNIT_ONLY |
| plan-graceful-upgrades.md:568-579 (StateUpdating constant) | StateUpdating=="updating" | jetstream_test.go:51 TestStateUpdatingConstant | None | UNIT_ONLY |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| 001 | New project with git URL crashes — no git credentials in pod | OPEN | Bug is about entrypoint.sh crash on git clone failure for private repos. The github-oauth spec (plan-github-oauth.md) is now implemented (session-agent InitRepo in gitcreds.go handles clone with credential setup). However, the bug says "control-plane OAuth endpoints not yet implemented (0/17 spec items)" — this is a control-plane gap, not session-agent. Session-agent side is fixed. Bug needs re-investigation at control-plane level. Leaving OPEN as the system-level symptom may still occur if control-plane hasn't provisioned credentials. |
| 004 | "Agent down: mclaude -- heartbeat stale" despite running pod | OPEN | heartbeat KV bucket TTL is still not configured (no TTL on mclaude-heartbeats bucket in agent.go:99); the session-agent runHeartbeat (agent.go:1060-1076) writes every 30s but bucket has no TTL. Bug remains open. |

### Summary

- Implemented: 35
- Gap: 1 (state schema "state" field enum mismatch — SPEC→FIX)
- Partial: 0
- Infra: 48
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 19
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 2
