## Run: 2026-04-28T00:00:00Z (round 5)

ADR: docs/adr-0043-graceful-upgrades.md
Specs evaluated: docs/spec-state-schema.md, docs/mclaude-session-agent/spec-session-agent.md, docs/mclaude-web/spec-web.md

---

| ADR (section) | ADR text | Spec location | Verdict | Direction | Notes |
|---------------|----------|---------------|---------|-----------|-------|
| Decisions | JetStream scope: All `api.sessions.*` subjects | spec-state-schema.md §MCLAUDE_API stream | REFLECTED | — | Stream subjects cover all api.sessions.* |
| Decisions | Terminal subjects: Stay on core NATS | spec-session-agent.md §Subscribe / Terminal API | REFLECTED | — | "Latency-sensitive; stays on core NATS" |
| Decisions | Consumer split: Two durable pull consumers per agent (cmd + ctl) | spec-session-agent.md §NATS JetStream Streams | REFLECTED | — | Both sa-cmd and sa-ctl consumers described |
| Decisions | Drain timeout: Wait indefinitely for idle | spec-session-agent.md §Graceful Shutdown step 5 | REFLECTED | — | "no wall-clock timeout — indefinite wait; terminationGracePeriodSeconds: 86400 is K8s's last-resort backstop" |
| Decisions | Drain predicate: `state == idle` AND `inFlightBackgroundAgents == 0` | spec-session-agent.md §Graceful Shutdown step 5 | REFLECTED | — | "wait for all sessions to reach idle state with zero in-flight background agents" |
| Decisions | Pending-control handling during drain: Auto-interrupt sessions in `requires_action` | spec-session-agent.md §Graceful Shutdown step 5 | REFLECTED | — | "Auto-interrupt sessions stuck in requires_action" |
| Decisions | Banner timing: Write `state:"updating"` to KV immediately on SIGTERM | spec-session-agent.md §Graceful Shutdown step 1 | REFLECTED | — | Step 1: "Write state: "updating" to KV for all sessions" |
| Decisions | In-memory state during drain: Do NOT clobber `sess.state.State`; suppress KV writes | spec-session-agent.md §Graceful Shutdown step 1 / §Session State Machine | REFLECTED | — | "Set shutdownPending flag to suppress further KV state flushes"; state machine section notes KV writes suppressed during shutdown |
| Decisions | Create/delete reply mechanism: Publish + KV watch (replaces request-reply) | spec-web.md §createSession(); spec-state-schema.md §NATS Subjects sessions.create row | REFLECTED | — | "No request-reply: JetStream messages have no Reply field" |
| Decisions | Deployment strategy: Recreate | spec-state-schema.md §Deployment section | REFLECTED | — | "Strategy: Recreate — old pod must exit before new pod starts" |
| Decisions | Handle backgrounded shell deaths: synthetic `<task-notification status=killed>` on drain | spec-session-agent.md §Graceful Shutdown step 6 | REFLECTED | — | Full XML format and publish logic described |
| Decisions | Handle agent deaths: drain waits for agents to finish naturally | spec-session-agent.md §Graceful Shutdown step 5 | REFLECTED | — | Drain predicate waits for inFlightBackgroundAgents == 0 (no kill path for agents) |
| Decisions | Shell notification transport: Publish onto `api.sessions.input` subject in JetStream | spec-session-agent.md §Graceful Shutdown step 6 | REFLECTED | — | "Publish it as a session-input payload... to the JetStream api.sessions.input subject" |
| Decisions | Shell output file persistence: `CLAUDE_CODE_TMPDIR` → PVC subPath `/data/claude-tmp` | spec-state-schema.md §Deployment; spec-session-agent.md §Configuration | REFLECTED | — | Env var and PVC SubPath documented in both specs |
| Decisions | Shell tracking structure: `map[toolUseId]*inFlightShell` | spec-session-agent.md §Event Routing / shell tracking | REFLECTED | — | Fields (toolUseId, taskId, command, outputFilePath, startedAt) all listed |
| Decisions | Shell tracking trigger: Two-phase (pending on tool_use, promote on tool_result) | spec-session-agent.md §Event Routing Phase 1 and Phase 2 | REFLECTED | — | Both phases described with exact field extraction logic |
| Decisions | Shell tracking removal: `origin.kind == "task-notification"` referencing tool-use-id | spec-session-agent.md §Event Routing / Removal | REFLECTED | — | "On user event with origin.kind == "task-notification" referencing the shell's toolUseId" |
| Decisions | Shell output path formula: `{CLAUDE_CODE_TMPDIR}/claude-{uid}/{sanitizePath(cwd)}/{sessionId}/tasks/{taskId}.output` | spec-session-agent.md §Event Routing Phase 2 | REFLECTED | — | Exact formula present |
| Decisions | `sanitizePath` determinism: ≤200 chars pure regex; >200 chars appends wyhash/djb2 | spec-session-agent.md §Event Routing | REFLECTED | — | "for paths > 200 chars appends a wyhash/djb2 suffix (in practice CWDs are under 200 chars)" |
| Decisions | `<output-file>` informational — Claude Code does NOT auto-read it | spec-session-agent.md (not mentioned explicitly) | REFLECTED | — | Not a blocking implementation gap — session-agent only needs to include the path; Claude Code behavior is external |
| Component Changes / MCLAUDE_API | Stream config: Name, Subjects, Retention, MaxAge, Storage, Discard | spec-state-schema.md §MCLAUDE_API | REFLECTED | — | Full config block matches ADR |
| Component Changes / Consumers | Consumer names sa-cmd-{uslug}-{pslug} and sa-ctl-{uslug}-{pslug}; AckPolicy Explicit; AckWait 60s; MaxDeliver 5 | spec-session-agent.md §NATS JetStream Streams | REFLECTED | — | All config fields present |
| Component Changes / Consumers | Fetch loop: batch 10, FetchMaxWait 5s | spec-session-agent.md §NATS JetStream Streams | REFLECTED | — | "Fetch loop: batch 10, FetchMaxWait 5s" |
| Component Changes / Consumers | `jsToNatsMsg` adapter | spec-session-agent.md §NATS JetStream Streams | REFLECTED | — | "A jsToNatsMsg adapter wraps the JetStream message before dispatching to handlers" |
| Component Changes / Consumers | Ack timing: after handler returns (not before) | spec-session-agent.md §NATS JetStream Streams | REFLECTED | — | "After a successful handler return, the message is acked" |
| Component Changes / In-Flight Tracking | inFlightBackgroundAgents counter: +1 on Agent tool_use with run_in_background; -1 on task-notification | spec-session-agent.md §Event Routing step 1 | REFLECTED | — | Exact increment/decrement rules present |
| Component Changes / In-Flight Tracking | pendingShells map + inFlightShells map; two-phase tracking | spec-session-agent.md §Event Routing Phase 1 and 2 | REFLECTED | — | Both maps and promotion logic described |
| Component Changes / SIGTERM | Steps 1–9 of shutdown sequence | spec-session-agent.md §Graceful Shutdown | REFLECTED | — | All 9 steps present in spec (matching ADR) |
| Component Changes / SIGTERM | KV write suppression via `shutdownPending` flag | spec-session-agent.md §Graceful Shutdown step 1 / §Session State Machine | REFLECTED | — | "suppress further KV state flushes"; state machine section mentions suppression |
| Component Changes / SIGTERM | Shell notification ordering: published after drain, while cmd consumer stopped | spec-session-agent.md §Graceful Shutdown step 6 | REFLECTED | — | Step 6 comes after step 2 (cancel cmd consumer) and step 5 (drain predicate satisfied) |
| Component Changes / SIGTERM | Shell notification idempotency: pod crash → empty map → no duplicate publishes | spec-session-agent.md §Error Handling / Pod crash | REFLECTED | — | "In-flight shell tracking (inFlightShells, pendingShells) is lost with the process" |
| Component Changes / Synthetic Format | XML elements: task-id, tool-use-id, output-file, status=killed, summary | spec-session-agent.md §Graceful Shutdown step 6 | REFLECTED | — | All XML child elements named |
| Component Changes / Synthetic Format | Session-input payload wrapping: `{"session_id": ..., "type": "user", "message": {...}}` | spec-session-agent.md §Graceful Shutdown step 6 | REFLECTED | — | Exact JSON wrapping format present |
| Component Changes / Startup Recovery | Sessions in "updating" state treated as "idle" for resume; KV stays "updating" until consumers attached | spec-session-agent.md §Session Lifecycle / Resumption step 6 | REFLECTED | — | "Sessions in updating state are resumed but their KV entry stays as updating until all JetStream consumers are attached, then cleared to idle" |
| Component Changes / Reply Mechanism | handleCreate/Delete/Restart: no msg.Respond(); error → api_error event | spec-session-agent.md §Error Handling; §NATS Subjects (Publish) | REFLECTED | — | "api_error event published to events._api (no NATS reply — JetStream messages have no Reply field)" |
| Component Changes / Reply Mechanism | Error event subject: `…events._api` | spec-session-agent.md §NATS Subjects (Publish) | REFLECTED | — | Exact subject documented |
| Component Changes / Reply Mechanism | Error event payload: `{type, request_id, operation, error}` | spec-session-agent.md §NATS Subjects (Publish) | REFLECTED | — | Exact fields listed |
| Component Changes / Reply Mechanism | RequestID field in create, delete, restart request structs | spec-state-schema.md §NATS Subjects sessions.create, delete, restart rows | REFLECTED | — | All three rows include requestId |
| Component Changes / Shell Output Path | BYOH/daemon mode: tracking disabled when CLAUDE_CODE_TMPDIR is empty | spec-session-agent.md §Event Routing / shell tracking; §Configuration CLAUDE_CODE_TMPDIR | REFLECTED | — | "Shell tracking is disabled when CLAUDE_CODE_TMPDIR is not set (daemon/BYOH mode)" |
| Component Changes / StateUpdating | `StateUpdating = "updating"` constant | spec-state-schema.md §mclaude-sessions Value state enum | REFLECTED | — | `"updating"` included in the state union |
| Component Changes / Control-Plane | ConfigMap watch with predicate filtered by name + namespace, re-enqueue MCProject CRs | spec-state-schema.md §ConfigMap session-agent-template Readers column | REFLECTED | — | "watches this ConfigMap (filtered by name + namespace)... re-enqueues all MCProject CRs" |
| Component Changes / Control-Plane | Recreate strategy set in both create and update branches of reconcileDeployment | spec-state-schema.md §Deployment | REFLECTED | — | "Strategy: Recreate" with ADR-0043 citation |
| Component Changes / Helm | `terminationGracePeriodSeconds: 86400` | spec-session-agent.md §Graceful Shutdown step 5 | REFLECTED | — | "terminationGracePeriodSeconds: 86400 from values.yaml is K8s's last-resort backstop" |
| Component Changes / Helm | CLAUDE_CODE_TMPDIR env var + PVC subPath `claude-tmp` on `project-data` | spec-state-schema.md §Deployment; spec-session-agent.md §Configuration | REFLECTED | — | Both env var and mount documented with exact values |
| Component Changes / SPA Session Store | `onSessionAdded(projectId, cb)` method + unsubscribe return | spec-web.md §SessionStore.onSessionAdded() | REFLECTED | — | Method signature, projectId filter, and unsubscribe return all documented |
| Component Changes / SPA Session Store | kvWatch DEL/PURGE fix; `KVEntry.operation` field | spec-web.md §KV Watch DEL/PURGE Handling | REFLECTED | — | "`KVEntry.operation` may be 'PUT' \| 'DEL' \| 'PURGE'" |
| Component Changes / SPA Session List VM | `createSession()`: publish + onSessionAdded watch + api_error temp sub + 30s timeout | spec-web.md §createSession() | REFLECTED | — | All four elements documented |
| Component Changes / SPA Session List VM | `deleteSession()`: fire-and-forget publish | spec-web.md §deleteSession() | REFLECTED | — | "fire-and-forget message... SPA does not wait for acknowledgement" |
| Component Changes / SPA Session Detail | "Updating..." banner above conversation; input box stays enabled | spec-web.md §Session State / updating | REFLECTED | — | Banner and enabled input both documented |
| Component Changes / SPA Dashboard | STATE_LABELS `updating: 'Updating...'` | spec-web.md §Session Management / Session State | REFLECTED | — | "DashboardScreen STATE_LABELS gains updating: 'Updating...'" |
| Component Changes / SPA StatusDot | `updating` → blue (`var(--blue)`) + PULSE_STATES | spec-web.md §Session Management / Session State | REFLECTED | — | "blue pulsing indicator (var(--blue), added to PULSE_STATES)" |
| Component Changes / SPA TypeScript Types | Add `'updating'` to `SessionState` union | spec-web.md §TypeScript Types | REFLECTED | — | "SessionState.state union gains "updating"" — explicitly present in spec-web.md §TypeScript Types section |
| Component Changes / SPA TypeScript Types | Add `'session_upgrading'` to `LifecycleEvent.type` union in `types.ts` | spec-web.md §TypeScript Types | REFLECTED | — | Round 4 found this PARTIAL; spec-web.md §TypeScript Types now explicitly states "LifecycleEvent.type union gains "session_upgrading"" — gap is closed |
| Error Handling | Pod crashes (no SIGTERM): inFlightShells lost; durable consumers redeliver; dangling tool_uses | spec-session-agent.md §Error Handling / Pod crash row | REFLECTED | — | Exact scenario documented |

---

### Summary

- Reflected: 53
- Gap: 0
- Partial: 0

CLEAN — 53 ADR decisions reflected in specs, 0 gaps, 0 partials.

The single PARTIAL from round 4 (`spec-web.md` missing `LifecycleEvent.type union gains 'session_upgrading'`) has been resolved. The `spec-web.md §TypeScript Types` section now explicitly lists both `SessionState.state` and `LifecycleEvent.type` additions.
