## Run: 2026-04-28T00:00:00Z

# Spec Alignment Evaluation — ADR-0043 (Graceful Session-Agent Upgrades) — Round 2

**ADR:** `docs/adr-0043-graceful-upgrades.md` (status: draft)
**Specs checked:**
- `docs/spec-state-schema.md`
- `docs/mclaude-session-agent/spec-session-agent.md`
- `docs/mclaude-web/spec-web.md`

---

## Phase 1 — ADR → Spec Forward Pass

| ADR (section) | ADR text | Spec location | Verdict | Direction | Notes |
|---------------|----------|---------------|---------|-----------|-------|
| Decisions / JetStream scope | "All `api.sessions.*` subjects" — ensures no messages lost during restart | spec-state-schema.md: MCLAUDE_API stream `Subjects: mclaude.users.*.hosts.*.projects.*.api.sessions.>` | REFLECTED | — | Stream subject pattern matches ADR decision |
| Decisions / Terminal subjects | "Stay on core NATS — Ephemeral, latency-sensitive." | spec-session-agent.md: "**Terminal API** via core NATS subscriptions on `…api.terminal.{create,delete,resize}` — Latency-sensitive; stays on core NATS." | REFLECTED | — | |
| Decisions / Consumer split | "Two durable pull consumers per agent (cmd + ctl)" | spec-session-agent.md: "The agent creates two durable pull consumers on MCLAUDE_API: **Command consumer** (`sa-cmd-{uslug}-{pslug}`) … **Control consumer** (`sa-ctl-{uslug}-{pslug}`)" | REFLECTED | — | Ack policy (explicit, 60s, max 5) also reflected |
| Decisions / Drain timeout | "Wait indefinitely for idle … K8s 24h `terminationGracePeriodSeconds` is a safety net, not a policy." | spec-session-agent.md Graceful Shutdown step 5: "no wall-clock timeout — indefinite wait; `terminationGracePeriodSeconds: 86400` from values.yaml is K8s's last-resort backstop, not a policy limit" | REFLECTED | — | |
| Decisions / Drain predicate | "`state == idle` AND `inFlightBackgroundAgents == 0`" | spec-session-agent.md Graceful Shutdown step 5: "wait for all sessions to reach `idle` state with zero in-flight background agents." | REFLECTED | — | |
| Decisions / Pending-control during drain | "Auto-interrupt sessions in `requires_action`" | spec-session-agent.md Graceful Shutdown step 5: "Auto-interrupt sessions stuck in `requires_action`." | REFLECTED | — | |
| Decisions / Banner timing | "Write `state:\"updating\"` to KV immediately on SIGTERM" | spec-session-agent.md Graceful Shutdown step 1: "Write `state: \"updating\"` to KV for all sessions (SPA displays upgrade banner). Set `shutdownPending` flag." | REFLECTED | — | |
| Decisions / In-memory state during drain | "Do NOT clobber `sess.state.State`; suppress KV writes" | spec-session-agent.md: "The agent updates in-memory state and flushes to KV on every transition, **except during graceful shutdown when KV writes are suppressed to preserve the `updating` banner**." | REFLECTED | — | |
| Decisions / Create/delete reply mechanism | "Publish + KV watch (replaces request-reply)" | spec-state-schema.md sessions.create row: "JetStream, cmd consumer; publish + KV-watch, no reply"; spec-web.md createSession(): "No request-reply … Success is signalled by the session key appearing in KV." | REFLECTED | — | |
| Decisions / Deployment strategy | "Recreate — Old pod exits before new pod starts." | spec-state-schema.md Deployment section: **no mention of Recreate strategy**; only describes volumes, env vars, and writers. | GAP | SPEC→FIX | A developer reading state-schema Deployment section would default to RollingUpdate; spec-state-schema.md Deployment section should state `Strategy: Recreate` with rationale. |
| Decisions / Handle backgrounded shell deaths | "Yes, synthetic `<task-notification status=killed>` on drain" | spec-session-agent.md Graceful Shutdown step 6: "Shell-killed notifications (K8s mode only … construct a `<task-notification status=killed>` XML message and publish it as a session-input payload…)" | REFLECTED | — | |
| Decisions / Handle agent deaths | "No — drain waits for agents to finish naturally" | spec-session-agent.md Event Routing item 1: "Decrements (floored at 0) on top-level `user` events with `origin.kind == \"task-notification\"`. Used by the drain predicate in graceful shutdown." Spec does not say agents get synthetic notifications, only that counter is used in predicate. | REFLECTED | — | Implicit in the drain predicate design |
| Decisions / Shell notification transport | "Publish onto `api.sessions.input` subject in JetStream" | spec-session-agent.md Graceful Shutdown step 6: "publish it as a session-input payload … to the JetStream `api.sessions.input` subject." | REFLECTED | — | |
| Decisions / Shell output file persistence | "Set `CLAUDE_CODE_TMPDIR` → PVC subPath `/data/claude-tmp`" | spec-session-agent.md Configuration: `CLAUDE_CODE_TMPDIR` "Set to `/data/claude-tmp` and backed by the `project-data` PVC (`SubPath: claude-tmp`)." | REFLECTED | — | |
| Decisions / Shell tracking structure | "`map[toolUseId]*inFlightShell` per session" with fields: toolUseId, taskId, command, outputFilePath, startedAt | spec-session-agent.md Event Routing phase 2: "promotes the entry to `sess.inFlightShells map[string]*inFlightShell` (keyed by toolUseId). Fields: `{toolUseId, taskId, command, outputFilePath, startedAt}`." | REFLECTED | — | |
| Decisions / Shell tracking trigger | "Two-phase: (1) assistant+tool_use Bash+run_in_background → pending; (2) user+tool_result → extract backgroundTaskId, compute outputFilePath, promote" | spec-session-agent.md Event Routing: "Phase 1 — Pending" and "Phase 2 — Promoted" subsections | REFLECTED | — | |
| Decisions / Shell tracking removal | "User message with `origin.kind == \"task-notification\"` referencing the shell's tool-use-id" | spec-session-agent.md Event Routing: "**Removal:** On `user` event with `origin.kind == \"task-notification\"` referencing the shell's toolUseId (shell completed naturally)." | REFLECTED | — | |
| Decisions / Shell output path formula | "`{CLAUDE_CODE_TMPDIR}/claude-{uid}/{sanitizePath(cwd)}/{sessionId}/tasks/{taskId}.output`" | spec-session-agent.md Event Routing Phase 2: "constructs `outputFilePath` as `{CLAUDE_CODE_TMPDIR}/claude-{uid}/{sanitizePath(cwd)}/{sessionId}/tasks/{taskId}.output`" | REFLECTED | — | |
| Decisions / sanitizePath determinism | "Deterministic for paths ≤ 200 chars (pure regex replace); for > 200 chars appends wyhash/djb2" | spec-session-agent.md does not describe sanitizePath details | REFLECTED | — | Implementation detail; not a spec-level behavioral gap |
| Decisions / `<output-file>` informational | "Informational — Claude Code does NOT auto-read it" | spec-session-agent.md doesn't describe this explicitly, but the drain step describes the XML only as a message Claude sees | REFLECTED | — | Implicit from context |
| Component Changes / MCLAUDE_API stream config | Name, Subjects, MaxAge 1h, Storage FileStorage, Discard DiscardOld | spec-state-schema.md MCLAUDE_API section: all fields present and matching | REFLECTED | — | |
| Component Changes / Consumer names and filter subjects | `sa-cmd-{uslug}-{pslug}` with create/delete/input/restart filters; `sa-ctl-{uslug}-{pslug}` with control filter | spec-session-agent.md consumer descriptions match | REFLECTED | — | |
| Component Changes / JetStream fetch loop | Batch 10, 5s FetchMaxWait, jsToNatsMsg adapter, dispatch by subject suffix, Ack after handler, stop by context cancel | spec-session-agent.md does not describe batch size or fetch details | REFLECTED | — | These are implementation details; not a spec-level gap |
| Component Changes / inFlightBackgroundAgents counter | +1 on assistant/Agent/run_in_background, -1 on user/task-notification, best-effort | spec-session-agent.md Event Routing item 1: full description present | REFLECTED | — | |
| Component Changes / pendingShells + inFlightShells maps | Two separate maps; pendingShells keyed by toolUseId; inFlightShells keyed by toolUseId; struct fields defined | spec-session-agent.md Event Routing items 1-2: both maps and struct fields covered | REFLECTED | — | |
| Component Changes / SIGTERM step-by-step sequence | 9-step sequence including updating KV, stop cmd, drain terminal, keep ctl, poll drain, shell notifications, stop ctl, session_upgrading event, exit | spec-session-agent.md Graceful Shutdown section: all 9 steps present | REFLECTED | — | |
| Component Changes / KV write suppression (shutdownPending) | "while `sess.shutdownPending == true`, MUST NOT flush state to KV" | spec-session-agent.md: "except during graceful shutdown when KV writes are suppressed to preserve the `updating` banner" | REFLECTED | — | |
| Component Changes / Auto-interrupt requires_action during drain | Drain loop sends synthetic interrupt through handleControl code path | spec-session-agent.md Graceful Shutdown step 5: "Auto-interrupt sessions stuck in `requires_action`" | REFLECTED | — | |
| Component Changes / Shell notification format | `<task-notification>` XML with task-id, tool-use-id, output-file, status=killed, summary | spec-session-agent.md Graceful Shutdown step 6: references "XML message" but does not spell out all XML fields | PARTIAL | SPEC→FIX | Spec-session-agent describes the outer payload wrapper and references the XML, but does not enumerate the XML element names (task-id, tool-use-id, output-file, summary). A developer implementing from the spec would not know the exact XML schema. Should add the XML format to the spec. |
| Component Changes / Startup Recovery — Run() ordering | recoverSessions → createJetStreamConsumers → subscribeTerminalAPI → clearUpdatingState → runHeartbeat | spec-session-agent.md Resumption step 6: "Sessions in `updating` state are resumed but their KV entry stays as `updating` until all JetStream consumers are attached, then cleared to `idle`." | REFLECTED | — | Behavioral outcome captured; exact function-call ordering is an implementation detail |
| Component Changes / "updating" treated as "idle" on resume | "Treat it as `idle` for resume purposes; do NOT write state to KV yet" | spec-session-agent.md Resumption step 6: "Sessions in `updating` state are resumed but their KV entry stays as `updating` until all JetStream consumers are attached, then cleared to `idle`." | REFLECTED | — | |
| Component Changes / Reply mechanism → no-op | "`reply()` helper becomes a no-op when `msg.Reply == \"\"` (always true for JetStream messages); errors signalled via `api_error` events" | spec-session-agent.md Error Handling rows: "Session create — worktree collision: **Error returned via NATS reply and published as `api_error` event**"; "Session create — Claude process fails to start: **error returned via NATS reply**" | GAP | SPEC→FIX | The error table still references the old NATS request-reply path. After ADR-0043, JetStream messages have no Reply field; `reply()` is a no-op. Errors must be signalled exclusively via `api_error` events. The spec should remove "via NATS reply" from all create/delete/restart error rows. |
| Component Changes / api_error event subject | `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events._api` | spec-session-agent.md NATS Subjects Publish: "**API error events** to `…events._api`"; spec-state-schema.md MCLAUDE_EVENTS stream: "Subject pattern: `…events.{sslug|_api}`" | REFLECTED | — | Both specs cover this; subjects table in state-schema doesn't have a dedicated row but stream description covers it |
| Component Changes / api_error payload schema | `{type, request_id, operation, error}` | spec-session-agent.md: "Payload: `{type: \"api_error\", request_id: string, operation: \"create\" \| \"delete\" \| \"restart\", error: string}`" | REFLECTED | — | |
| Component Changes / RequestID in create/delete/restart | Add `RequestID string` to request structs | spec-state-schema.md subject table: sessions.create payload includes `requestId`; sessions.delete `{sessionSlug, requestId}`; sessions.restart `{sessionSlug, extraFlags?, requestId}` | REFLECTED | — | |
| Component Changes / BYOH disable shell tracking | "disabled when `CLAUDE_CODE_TMPDIR` is empty" | spec-session-agent.md Configuration: "When unset (BYOH/daemon mode), in-flight shell tracking is disabled." | REFLECTED | — | |
| Component Changes / StateUpdating constant | `StateUpdating = "updating"` added to events.go | spec-session-agent.md State Machine: "Agent-managed states: `updating` — pod is draining for graceful upgrade (written to KV only)"; spec-state-schema.md sessions KV state enum includes `updating` | REFLECTED | — | |
| Component Changes / Reconciler ConfigMap watch | Watch `session-agent-template` ConfigMap, re-enqueue MCProject CRs | ADR Impact does not list spec-control-plane as a spec to update; not in the three checked specs | REFLECTED | — | Intentionally scoped to a different spec (spec-control-plane) not in evaluation scope |
| Component Changes / Deployment Recreate — both create and update | "Both create and update branches set `deploy.Spec.Strategy = Recreate`" | spec-state-schema.md Kubernetes Resources / Deployment: lists volumes, env vars, replicas, restart policy; **no mention of Recreate strategy** | GAP | SPEC→FIX | Spec-state-schema.md is the canonical reference for K8s resource shapes. The absence of Recreate strategy means a developer would implement RollingUpdate (K8s default), allowing two pods to share durable consumers simultaneously. ADR Impact doesn't flag this update but the state-schema Deployment section is where it belongs. |
| Component Changes / terminationGracePeriodSeconds 86400 | Default in values.yaml | spec-session-agent.md Graceful Shutdown: "terminationGracePeriodSeconds: 86400 from values.yaml is K8s's last-resort backstop" | REFLECTED | — | |
| Component Changes / CLAUDE_CODE_TMPDIR + PVC SubPath | `CLAUDE_CODE_TMPDIR=/data/claude-tmp`; VolumeMount project-data SubPath claude-tmp | spec-session-agent.md Configuration: "Set to `/data/claude-tmp` and backed by the `project-data` PVC (`SubPath: claude-tmp`)." | REFLECTED | — | |
| SPA / onSessionAdded signature | `onSessionAdded(projectId: string, cb: (session: SessionKVState) => void): () => void` — filters by projectId | spec-web.md: `onSessionAdded(callback: (session: Session) => void): () => void` — **missing `projectId` parameter** | GAP | SPEC→FIX | The spec drops the `projectId` parameter, so a developer implementing from the spec would produce a project-unscoped listener that fires for sessions from any project. The ADR implementation explicitly filters `session.projectId === projectId`. This is a behavioral correctness gap. |
| SPA / kvWatch DEL/PURGE handling | `KVEntry.operation?: 'PUT' \| 'DEL' \| 'PURGE'`; DEL entries remove from _sessions | spec-web.md: "`KVEntry.operation` may be `'PUT' \| 'DEL' \| 'PURGE'`. The session store's `kvWatch` callback must handle `DEL` (and `PURGE`) by removing the corresponding entry from the `_sessions` map." | REFLECTED | — | |
| SPA / createSession() publish+KV-watch | Publish to subjSessionsCreate; wait for onSessionAdded; 30s timeout; subscribe api_error; cleanup on success/error/timeout | spec-web.md createSession() section covers all these behaviors | REFLECTED | — | |
| SPA / deleteSession() fire-and-forget | Publish to subjSessionsDelete; no wait; removal detected via KV DEL watcher | spec-web.md deleteSession(): "publishes a fire-and-forget message … The SPA does not wait for an acknowledgement. Session removal is detected via the KV watcher receiving a `DEL` operation." | REFLECTED | — | |
| SPA / updating state visual | Blue "Updating..." banner on session detail; input box enabled; blue pulsing StatusDot | spec-web.md Session State section: "The session detail view shows a blue 'Updating...' banner … `StatusDot` renders as a blue pulsing indicator (`var(--blue)`, added to `PULSE_STATES`) … message input box remains enabled." | REFLECTED | — | |
| SPA / Dashboard STATE_LABELS | Add `updating: 'Updating...'` to DashboardScreen.tsx STATE_LABELS | spec-web.md describes updating state treatment in session detail and StatusDot; Dashboard-specific STATE_LABELS update not mentioned | PARTIAL | SPEC→FIX | Minor: the Dashboard STATE_LABELS update is implicit in the overall "updating state visual" treatment. A developer reading the spec would likely apply it consistently, but the Dashboard-specific mention from the ADR is absent. Low risk. |
| SPA / TypeScript SessionState union | Add `'updating'` to SessionState type | spec-web.md: "The `SessionState.state` union includes `\"updating\"`" | REFLECTED | — | |
| SPA / TypeScript LifecycleEvent.type union | Add `'session_upgrading'` | spec-web.md does not have a dedicated TypeScript types section for lifecycle events; spec-state-schema.md covers the lifecycle event payload | REFLECTED | — | Implementation-level type annotation; the behavioral event itself is covered in spec-state-schema.md |
| Error Handling / Pod crash | "In-flight shells are lost (counter in memory only); dangling tool_uses in transcript — Claude notices via BashOutput" | spec-session-agent.md Error Handling: "Pod crash … In-flight shell tracking (`inFlightShells`, `pendingShells`) is lost with the process … Dangling background-shell tool_uses remain in the transcript — Claude notices the unknown shell-id when it attempts `BashOutput` and can adjust." | REFLECTED | — | |
| Error Handling / Pod crash mid-drain | "Published messages are durable; unpublished in-flight shells are lost (empty map on new process)" | spec-session-agent.md Error Handling: pod-crash row covers durable consumer redelivery but does not call out the mid-drain partial publish case | PARTIAL | SPEC→FIX | Minor: the mid-drain crash scenario (some notifications published, some not) is an edge case. Spec could note that already-published shell notifications survive via durable consumer; not yet published ones are simply lost. However this is low-impact for implementers. |

---

### Summary

- Reflected: 39
- Gap: 3
- Partial: 3

---

## Gaps and Partials

### GAPs (blocking)

**GAP [SPEC→FIX] 1 — spec-session-agent.md error table still says "NATS reply"**

> ADR: "The `reply()` helper becomes a no-op when `msg.Reply == \"\"` (always true for JetStream messages since the wrapped `*nats.Msg` has no Reply field)."

The error handling table in spec-session-agent.md still reads:
- "Session create — worktree collision | Error returned via **NATS reply** and published as `api_error` event"
- "Session create — git worktree add fails | Error returned via **NATS reply** and published as `api_error` event"
- "Session create — Claude process fails to start | `session_failed` lifecycle event published; error returned via **NATS reply**"

After ADR-0043, JetStream delivery means no Reply field exists; errors are signalled exclusively via `api_error` events on `events._api`. The "via NATS reply" text is incorrect and should be removed from all create/delete/restart error rows.

**Spec file:** `docs/mclaude-session-agent/spec-session-agent.md` — Error Handling table

---

**GAP [SPEC→FIX] 2 — spec-web.md `onSessionAdded()` signature missing `projectId` parameter**

> ADR: `onSessionAdded(projectId: string, cb: (session: SessionKVState) => void): () => void` — implementation filters `session.projectId === projectId`.

spec-web.md shows:
```ts
onSessionAdded(callback: (session: Session) => void): () => void
```

The `projectId` parameter is absent. A developer implementing from the spec would produce a project-unscoped listener that fires for any newly added session, causing `createSession()` to resolve on sessions belonging to other projects.

**Spec file:** `docs/mclaude-web/spec-web.md` — Session Management / SessionStore.onSessionAdded()

---

**GAP [SPEC→FIX] 3 — spec-state-schema.md Deployment section silent on Recreate strategy**

> ADR: "Recreate deployment strategy for session-agent pods (both create and update paths in `reconcileDeployment`). Prevents two pods consuming from the same JetStream consumer simultaneously."

The Kubernetes Resources / Deployment section in spec-state-schema.md lists replicas, volumes, env vars, and restart policy, but says nothing about the deployment strategy. A developer referencing this spec to implement `reconcileDeployment` would default to `RollingUpdate`, allowing a brief overlap period where two pods read from the same durable JetStream consumers (double-delivery, state corruption).

**Spec file:** `docs/spec-state-schema.md` — Kubernetes Resources / Deployment

---

### PARTIALs

**PARTIAL [SPEC→FIX] 4 — spec-session-agent.md Graceful Shutdown: synthetic XML format not enumerated**

> ADR: Specifies exact XML element names: `<task-notification>`, `<task-id>`, `<tool-use-id>`, `<output-file>`, `<status>killed</status>`, `<summary>`.

spec-session-agent.md Graceful Shutdown step 6 references "a `<task-notification status=killed>` XML message" but does not enumerate the child elements (`task-id`, `tool-use-id`, `output-file`, `summary`). A developer implementing the notification synthesis from the spec alone would not know the exact XML schema required for Claude Code compatibility.

**Spec file:** `docs/mclaude-session-agent/spec-session-agent.md` — Graceful Shutdown (SIGTERM), step 6

---

**PARTIAL [SPEC→FIX] 5 — spec-web.md: Dashboard STATE_LABELS `updating` entry not explicitly mentioned**

> ADR: "Add `updating: 'Updating...'` to `DashboardScreen.tsx` `STATE_LABELS`. Update the `StatusDot` type cast to include `'updating'`."

spec-web.md Session State section covers the session detail banner and StatusDot but does not explicitly call out the Dashboard screen's STATE_LABELS update. Low risk (a careful developer would apply it consistently), but the spec is silent on it.

**Spec file:** `docs/mclaude-web/spec-web.md` — Session Management / Session State

---

**PARTIAL [SPEC→FIX] 6 — spec-session-agent.md: mid-drain crash scenario not described**

> ADR Error Handling: "Pod crashes mid-drain, after publishing some shell notifications | Published messages are durable in JetStream; new pod consumes them. Unpublished in-flight shells are lost (empty map on new process)."

spec-session-agent.md's pod-crash error row covers the general crash scenario but does not distinguish the mid-drain partial-publish case. This is a minor gap; the general crash behavior is correct, but the nuance that partially-published shell notifications survive while unissued ones are silently dropped is missing.

**Spec file:** `docs/mclaude-session-agent/spec-session-agent.md` — Error Handling table
