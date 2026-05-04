## Run: 2026-04-28T00:00:00Z

# Spec Alignment Evaluation: ADR-0043 (Graceful Session-Agent Upgrades)

**ADR**: `docs/adr-0043-graceful-upgrades.md`  
**Status**: draft  
**Specs checked**:
- `docs/spec-state-schema.md`
- `docs/mclaude-session-agent/spec-session-agent.md`

Additional specs consulted (not in Impact): `docs/mclaude-controller/spec-controller.md`, `docs/mclaude-web/spec-web.md`

---

## Phase 1 — ADR → Spec Forward Pass

| # | ADR (section) | ADR text | Spec location | Verdict | Direction | Notes |
|---|---------------|----------|---------------|---------|-----------|-------|
| 1 | Decisions: JetStream scope | "All `api.sessions.*` subjects" | spec-state-schema.md §MCLAUDE_API; spec-session-agent.md §NATS JetStream Streams | REFLECTED | — | MCLAUDE_API stream with `api.sessions.>` subject filter documented in both specs |
| 2 | Decisions: Terminal subjects | "Stay on core NATS" | spec-state-schema.md §NATS Subjects; spec-session-agent.md §NATS Subjects Subscribe | REFLECTED | — | Terminal subjects listed as core NATS in both specs |
| 3 | Decisions: Consumer split | "Two durable pull consumers per agent (cmd + ctl)" | spec-session-agent.md §NATS JetStream Streams | REFLECTED | — | Both cmd and ctl consumers named, described, and their filter subjects listed |
| 4 | Decisions: Drain timeout | "Wait indefinitely for idle… K8s 24h `terminationGracePeriodSeconds` is a safety net, not a policy" | spec-session-agent.md §Graceful Shutdown (step 5) | PARTIAL | SPEC→FIX | Spec says "Poll every 1 second: wait for all sessions to reach idle state" but omits: (a) no wall-clock timeout (indefinite wait) and (b) 24h terminationGracePeriodSeconds as K8s safety net |
| 5 | Decisions: Drain predicate | "`state == idle` AND `inFlightBackgroundAgents == 0`" | spec-session-agent.md §Graceful Shutdown (step 5) | REFLECTED | — | "wait for all sessions to reach `idle` state with zero in-flight background agents" |
| 6 | Decisions: Pending-control handling | "Auto-interrupt sessions in `requires_action`" | spec-session-agent.md §Graceful Shutdown (step 5) | REFLECTED | — | "Auto-interrupt sessions stuck in `requires_action`" |
| 7 | Decisions: Banner timing | "Write `state:"updating"` to KV immediately on SIGTERM" | spec-session-agent.md §Graceful Shutdown (step 1) | REFLECTED | — | Step 1 says "Write `state: "updating"` to KV for all sessions (SPA displays upgrade banner)" |
| 8 | Decisions: In-memory state during drain | "Do NOT clobber `sess.state.State`; suppress KV writes" | spec-session-agent.md §Session State Machine; §Graceful Shutdown | REFLECTED | — | "except during graceful shutdown when KV writes are suppressed to preserve the `updating` banner"; `shutdownPending` flag in step 1 |
| 9 | Decisions: Create/delete reply mechanism | "Publish + KV watch (replaces request-reply)" | spec-state-schema.md §NATS Subjects sessions.create row | REFLECTED | — | Row says "publish + KV-watch, no reply; error: `api_error` event on `events._api`" |
| 10 | Decisions: Deployment strategy | "Recreate" | spec-controller.md §Reconciler Loop step 7 (not in listed Impact specs) | REFLECTED | — | Controller spec already says "Ensures the session-agent Deployment with `Recreate` strategy". ADR Impact omits spec-controller.md but the spec is already correct. No gap in listed specs. |
| 11 | Decisions: Handle backgrounded shell deaths | "Yes, synthetic `<task-notification status=killed>` on drain" | spec-session-agent.md §Graceful Shutdown | GAP | SPEC→FIX | Graceful Shutdown section has 8 steps but no step for "for each session, for each inFlightShell, construct and publish synthetic task-notification". Missing entirely. |
| 12 | Decisions: Shell notification transport | "Publish onto `api.sessions.input` subject in JetStream" | spec-session-agent.md §Graceful Shutdown | GAP | SPEC→FIX | Shell-killed notifications are published on the JetStream cmd consumer subject after drain — not mentioned in the spec's shutdown sequence. |
| 13 | Decisions: Shell output file persistence | "Set `CLAUDE_CODE_TMPDIR` → PVC subPath `/data/claude-tmp`" | spec-session-agent.md §Configuration; spec-state-schema.md §Deployment | GAP | SPEC→FIX | `CLAUDE_CODE_TMPDIR` env var is absent from the Configuration table in spec-session-agent.md. The PVC subPath at `/data/claude-tmp` and the `project-data` VolumeMount SubPath are absent from spec-state-schema.md Deployment section. |
| 14 | Decisions: Shell tracking structure | "`map[toolUseId]*inFlightShell` per session" | spec-session-agent.md §Event Routing; §Internal Behavior | GAP | SPEC→FIX | Event Routing only mentions "Tracks in-flight background agents"; no mention of `inFlightShells` map, its structure (`toolUseId`, `taskId`, `command`, `outputFilePath`), or the `pendingShells` staging map. |
| 15 | Decisions: Shell tracking trigger | "Two-phase: (1) assistant message with Bash tool_use + run_in_background:true → pending; (2) user message with tool_result → extract backgroundTaskId → promote to inFlightShells" | spec-session-agent.md §Event Routing | GAP | SPEC→FIX | Two-phase shell tracking lifecycle is entirely absent from the spec. A developer implementing from the spec alone would have no idea about pending vs promoted entries or the backgroundTaskId extraction pattern. |
| 16 | Decisions: Shell tracking removal | "User message with `origin.kind == "task-notification"` referencing the shell's tool-use-id" | spec-session-agent.md | GAP | SPEC→FIX | Removal criterion for inFlightShells entries not described anywhere in the spec. |
| 17 | BYOH mode (Component Changes: Shell Output Path Construction) | "Shell tracking (pending and promoted phases) and the synthetic notification publish are disabled when `CLAUDE_CODE_TMPDIR` is empty — this is a K8s-only feature" | spec-session-agent.md | GAP | SPEC→FIX | BYOH behavior (shell tracking disabled when env var unset) is a runtime behavioral decision not mentioned in the spec. |
| 18 | Component Changes: MCLAUDE_API stream definition | "Name: MCLAUDE_API, Subjects: mclaude.users.*.hosts.*.projects.*.api.sessions.>, Retention: LimitsPolicy, MaxAge: 1h, Storage: FileStorage, Discard: DiscardOld" | spec-state-schema.md §MCLAUDE_API | REFLECTED | — | Exact match in spec-state-schema.md |
| 19 | Component Changes: JetStream consumers | "sa-cmd-{uslug}-{pslug} (cmd), sa-ctl-{uslug}-{pslug} (ctl); AckPolicy: Explicit, AckWait: 60s, MaxDeliver: 5" | spec-session-agent.md §NATS JetStream Streams | REFLECTED | — | Both consumers named with exact config parameters |
| 20 | Component Changes: SIGTERM Handler — step 6 shell notification | "For each session, for each entry in sess.inFlightShells: Constructs <task-notification status=killed> XML... Publishes it as a session-input message to JetStream" | spec-session-agent.md §Graceful Shutdown | GAP | SPEC→FIX | (Same as row 11 — missing step in spec's shutdown sequence) |
| 21 | Component Changes: Reply Mechanism — handleCreate | "`msg.Respond({id: sessionId})` → Session appears in KV. Error → publish `api_error` event" | spec-state-schema.md §NATS Subjects sessions.create; spec-session-agent.md §NATS Subjects Publish | REFLECTED | — | KV-watch + api_error path reflected in both specs |
| 22 | Component Changes: Reply Mechanism — api_error event payload | "`{type, request_id, operation, error}`" | spec-state-schema.md; spec-session-agent.md | GAP | SPEC→FIX | Neither spec defines the api_error event payload structure. spec-session-agent.md only mentions the subject (`events._api`). spec-state-schema.md NATS Subjects table mentions "error: `api_error` event" but contains no payload schema. |
| 23 | Component Changes: Handler request struct changes | "Add `RequestID string` field to create, delete, and restart request structs" | spec-state-schema.md §NATS Subjects (sessions.create, sessions.delete, sessions.restart rows) | PARTIAL | SPEC→FIX | sessions.create payload already includes `requestId`. But sessions.delete payload shows `{sessionSlug}` and sessions.restart shows `{sessionSlug, extraFlags?}` — neither includes `requestId`. The ADR adds it to all three. |
| 24 | Component Changes: Startup Recovery — clearUpdatingState | "4. clearUpdatingState() — writes state:'idle' to KV for any session still in 'updating' after consumers are attached" | spec-session-agent.md §Session Lifecycle / Resumption (step 6) | REFLECTED | — | "Sessions in `updating` state are resumed but their KV entry stays as `updating` until all JetStream consumers are attached, then cleared to `idle`" |
| 25 | Component Changes: StateUpdating constant | "Add `StateUpdating` to state constants in `events.go`" | spec-state-schema.md §mclaude-sessions SessionState.state union | REFLECTED | — | `"updating"` is present in the state union |
| 26 | Component Changes: session_upgrading lifecycle event | "Publish lifecycle event 'session_upgrading' for each session" | spec-state-schema.md §Lifecycle Event Payloads / session_upgrading | REFLECTED | — | session_upgrading payload fully defined in spec |
| 27 | Component Changes: SPA SessionState union | "Add 'updating' to the SessionState union" | spec-web.md (not in Impact) | GAP | SPEC→FIX | spec-web.md does not mention SessionState type values. The ADR Impact section omits spec-web.md entirely despite significant SPA changes. |
| 28 | Component Changes: SPA — createSession() → publish + KV watch | "switch from request-reply to publish + KV watch; 30s timeout" | spec-web.md (not in Impact) | GAP | SPEC→FIX | spec-web.md contains no description of session creation flow or API semantics. The ADR Impact section omits spec-web.md. |
| 29 | Component Changes: SPA — SessionStore.onSessionAdded() | "New method fires when a new session key appears in the KV watcher" | spec-web.md (not in Impact) | GAP | SPEC→FIX | spec-web.md contains no description of SessionStore methods. The ADR Impact section omits spec-web.md. |
| 30 | Component Changes: SPA — kvWatch DEL fix | "kvWatch must pass DEL entries to the callback; extend KVEntry with operation field" | spec-web.md (not in Impact) | GAP | SPEC→FIX | spec-web.md has no description of KV watch behavior for deletions. The ADR Impact section omits spec-web.md. |
| 31 | Component Changes: SPA — updating banner + StatusDot | "Show 'Updating...' banner; blue pulsing StatusDot; input box stays enabled" | spec-web.md (not in Impact) | GAP | SPEC→FIX | spec-web.md contains no description of session state visual indicators. The ADR Impact section omits spec-web.md. |
| 32 | Component Changes: Reconciler ConfigMap watch | "Add watch on `session-agent-template` ConfigMap; re-enqueue all MCProject CRs on change" | spec-controller.md §Reconciler loop; §Kubernetes resources | REFLECTED | — | Controller spec already says "Watched: `{release}-session-agent-template` ConfigMap in `mclaude-system`...changes re-enqueue all `MCProject` CRs". ADR Impact omits spec-controller.md but spec is already correct. |
| 33 | Error Handling: Pod crashes (no SIGTERM) | "In-flight shells are lost (counter was in memory only)" | spec-session-agent.md §Error Handling | GAP | SPEC→FIX | Error handling table has no entry for "pod crash — in-flight shell tracking state lost, dangling tool_uses". The ADR explicitly decides the behavior here. |

---

### Summary

- Reflected: 17
- Gap: 13
- Partial: 2 (rows 4, 23)

---

## Gaps and Partials

**GAP [SPEC→FIX]** (row 11/12/20): `"synthetic <task-notification status=killed> on drain"` → spec-session-agent.md §Graceful Shutdown must add a step after the drain predicate: "For each session, for each entry in `inFlightShells`: construct `<task-notification status=killed>` XML and publish it as a session-input message to the JetStream `api.sessions.input` subject. Messages queue in the durable cmd consumer for the replacement pod."

**GAP [SPEC→FIX]** (row 13): `"Set CLAUDE_CODE_TMPDIR → PVC subPath /data/claude-tmp"` → (a) spec-session-agent.md Configuration table must add `CLAUDE_CODE_TMPDIR` env var. (b) spec-state-schema.md Deployment section must note the `project-data` PVC subPath mount at `/data/claude-tmp`.

**GAP [SPEC→FIX]** (row 14): `"map[toolUseId]*inFlightShell per session"` → spec-session-agent.md §Event Routing or §Internal Behavior must describe the `inFlightShells` map (keyed by toolUseId), its fields (`toolUseId`, `taskId`, `command`, `outputFilePath`, `startedAt`), and the `pendingShells` staging map.

**GAP [SPEC→FIX]** (row 15): `"Two-phase shell tracking trigger"` → spec-session-agent.md §Event Routing must describe Phase 1 (pending on `assistant` Bash tool_use with `run_in_background:true`) and Phase 2 (promotion on matching `tool_result` with `backgroundTaskId` extraction).

**GAP [SPEC→FIX]** (row 16): `"Shell tracking removal: origin.kind == task-notification referencing the shell's tool-use-id"` → spec-session-agent.md must state that a real `task-notification` user message removes the corresponding entry from `inFlightShells`.

**GAP [SPEC→FIX]** (row 17): `"Shell tracking disabled when CLAUDE_CODE_TMPDIR is empty"` → spec-session-agent.md must note that shell tracking and synthetic notifications are a K8s-only feature, disabled (no-op) when `CLAUDE_CODE_TMPDIR` is unset (BYOH/daemon mode).

**GAP [SPEC→FIX]** (row 22): `"api_error payload: {type, request_id, operation, error}"` → spec-state-schema.md must add an `api_error` event payload schema (analogous to the lifecycle event payload entries), or spec-session-agent.md §NATS Subjects Publish must document the payload structure.

**GAP [SPEC→FIX]** (row 27): `"Add 'updating' to the SessionState union"` → spec-web.md (omitted from ADR Impact) must be added to the Impact list and updated with the `updating` session state and its visual treatment ("Updating..." banner, blue pulsing dot, enabled input).

**GAP [SPEC→FIX]** (rows 28–31): SPA behavioral decisions (createSession publish+KV-watch, deleteSession fire-and-forget, SessionStore.onSessionAdded(), kvWatch DEL fix) → spec-web.md is entirely absent from the ADR Impact section despite describing significant behavioral changes. It should be added to Impact and updated with the new SPA API semantics.

**GAP [SPEC→FIX]** (row 33): `"Pod crashes: in-flight shells lost"` → spec-session-agent.md §Error Handling table should add an entry: "Pod crash (no SIGTERM): in-flight shell tracking lost; dangling tool_uses in transcript — Claude notices when it attempts BashOutput on an unknown shell-id."

**PARTIAL [SPEC→FIX]** (row 4): `"Wait indefinitely for idle; K8s 24h terminationGracePeriodSeconds is a safety net, not a policy"` → spec-session-agent.md §Graceful Shutdown step 5 should note that there is no wall-clock timeout on the drain poll, and that `terminationGracePeriodSeconds: 86400` (from values.yaml) acts as K8s's last-resort backstop rather than an intended policy timeout.

**PARTIAL [SPEC→FIX]** (row 23): `"Add RequestID to create, delete, and restart request structs"` → spec-state-schema.md sessions.delete payload (`{sessionSlug}`) and sessions.restart payload (`{sessionSlug, extraFlags?}`) must both gain a `requestId` field. (sessions.create already has it.)
