## Run: 2026-04-14T00:00:00Z

**Gaps found: 12**

1. **`onSessionAdded` and `eventStore.onApiError` do not exist** — The proposed SPA `createSession()` code calls `this.sessionStore.onSessionAdded(projectId, cb)` and `this.eventStore.onApiError(requestId, cb)`. Neither method exists anywhere in the codebase. `SessionStore` exposes only `onSessionChanged` (fires on any change) and `onProjectChanged`. `EventStore` has no `onApiError` method and does not handle any `api_error` event type in `_applyEvent`. A developer must invent both APIs from scratch, and neither the callback signature nor the event routing subject are specified for `onApiError`.
   - **Doc**: "const unsub = this.sessionStore.onSessionAdded(projectId, (session) => {...})" and "const unsubErr = this.eventStore.onApiError(requestId, (error) => {...})"
   - **Code**: `SessionStore` (`mclaude-web/src/stores/session-store.ts`) has no `onSessionAdded`; `EventStore` (`mclaude-web/src/stores/event-store.ts`) has no `onApiError` and its `_applyEvent` switch has no `api_error` case.

2. **`api_error` event subject is ambiguous** — The doc says errors are published to `mclaude.{userId}.{projectId}.events.{sessionId}` "or a project-level subject". For `sessions.create`, there is no sessionId at the time of failure. The doc does not specify which subject is used when there is no sessionId, nor how the SPA subscribes to catch the error. Without a concrete subject, the SPA's `onApiError` watcher cannot be implemented.
   - **Doc**: "published to `mclaude.{userId}.{projectId}.events.{sessionId}` or a project-level subject"
   - **Code**: `handleCreate` in `agent.go` currently publishes `session_failed` on `mclaude.{userId}.{projectId}.lifecycle.{sessionId}` — not on the events stream.

3. **`StateUpdating` is defined in `events.go`, not `state.go`** — The doc says add `StateUpdating = "updating"` to `state.go`. The existing state constants (`StateIdle`, `StateRunning`, `StateRequiresAction`) live in `events.go` (line 26–29), not `state.go`. `state.go` contains `SessionState` struct and `clearPendingControlsForResume` which hard-codes `st.State = StateIdle`. A developer will be confused about where to add the constant and whether `clearPendingControlsForResume` needs to handle `"updating"` as an alias for idle.
   - **Doc**: "Add `StateUpdating` to the state enum in `state.go`"
   - **Code**: State constants are in `mclaude-session-agent/events.go:26-29`; `state.go` contains the `SessionState` struct.

4. **`MCLAUDE_API` stream subject pattern contradicts the existing NATS subject format** — The doc specifies `Subjects: ["mclaude.*.*.api.sessions.>"]`. The existing stream `MCLAUDE_EVENTS` uses `mclaude.*.*.events.*`. But the session-agent's actual subscription subjects (from `subscribeAPI`) are `mclaude.{userId}.{projectId}.api.sessions.{op}` and `mclaude.{userId}.{projectId}.api.terminal.{op}`. The stream filter `mclaude.*.*.api.sessions.>` would also capture terminal subjects if they started with `api.sessions`, which they do not — but more critically, when the agent creates consumers with per-`{userId}.{projectId}` filter subjects, those concrete subjects must be subsets of the stream's subject filter. The doc does not confirm that the concrete consumer subjects (e.g. `mclaude.abc.xyz.api.sessions.create`) satisfy the stream wildcard. Also, existing NATS ACL policies (if any) need updating — not mentioned.
   - **Doc**: "Subjects: [\"mclaude.*.*.api.sessions.>\"]"
   - **Code**: `mclaude-session-agent/agent.go:248` — subscribeAPI uses `mclaude.{userId}.{projectId}.api.sessions.*` subjects; NATS configmap at `charts/mclaude/templates/nats-configmap.yaml` (not read) may have ACL constraints.

5. **JetStream `Fetch` loop dispatch mechanics are completely unspecified** — The doc says "The dispatch layer changes from `nc.Subscribe` callbacks to a JetStream `Fetch` loop that routes messages to the same handlers." No details are given: What batch size? What timeout per fetch? How many goroutines? How does the loop terminate when the consumer is stopped? How are messages acked — before or after the handler returns? What happens if the handler panics? What is the interface between the JetStream message type and the existing `*nats.Msg` that all handlers accept? The handlers currently receive `*nats.Msg` (core NATS); JetStream messages are `jetstream.Msg`, a different type. The doc does not specify any adaptation layer.
   - **Doc**: "a JetStream `Fetch` loop that routes messages to the same handlers"
   - **Code**: `handleCreate`, `handleDelete`, etc. all accept `*nats.Msg` (core NATS). `jetstream.Msg` is a distinct interface with `Ack()`, `Nak()`, etc.

6. **`recoverSessions()` startup recovery: "updating" → idle write timing is unspecified** — The doc says after all sessions are recovered and consumers attached, write `state:"idle"` for any session that was `"updating"`. But `recoverSessions()` currently writes KV eagerly (via `clearPendingControlsForResume` + `writeSessionKV`) for each session individually before consumers are attached. The doc does not specify when consumers are "attached" and does not describe what new code structure makes that a defined synchronization point. The order — recover all sessions, then attach consumers, then write idle — is not implemented by any existing code structure.
   - **Doc**: "After all sessions recovered and consumers attached: write state:\"idle\" to session KV for any session that was \"updating\""
   - **Code**: `recoverSessions()` (`agent.go:138-202`) iterates and starts sessions one by one; `subscribeAPI()` (now renamed to consumer attach) is called after. There is no "after all recovered and consumers attached" hook.

7. **SIGTERM drain step 5 race: re-writing `state:"updating"` after stdout router sets idle** — The drain loop says "If a session transitions idle → write state:\"updating\" to KV (in case it was set to idle by the stdout router)". This requires observing per-session state transitions in a polling loop. The doc does not specify how the new SIGTERM handler knows a session transitioned to idle: does it poll `sess.getState().State`? Does it hook into `writeKV`? The existing `gracefulShutdown` uses `sess.stopAndWait(timeout)` which blocks on `doneCh`. A polling-based approach must know the full set of sessions (some may be created during drain if the cmd consumer isn't stopped fast enough) and must handle the case where a session is started by the new pod's consumer during the old pod's drain. Nothing in the doc specifies the data structure or goroutine that drives this poll.
   - **Doc**: "Loop: check all sessions every 1s — If all sessions are in state \"idle\" → break — If a session transitions idle → write state:\"updating\" to KV"
   - **Code**: `gracefulShutdown()` at `agent.go:209-243` — no session-state polling; uses blocking `stopAndWait`.

8. **Step 7 of SIGTERM handler (lifecycle event) contradicts step 5 of startup recovery** — The SIGTERM sequence publishes `"session_upgrading"` lifecycle events for each session (step 7), after all sessions are idle (step 6). Startup recovery clears `"updating"` state and writes `"idle"`. If the SPA or any subscriber receives `"session_upgrading"` it would need to handle it, but `LifecycleEvent` in `types.ts` only defines `'session_created' | 'session_stopped' | 'session_restarting' | 'session_failed'`. The doc does not specify whether `"session_upgrading"` must be added to the SPA type, and if so, what the SPA does with it.
   - **Doc**: "7. Publish lifecycle event \"session_upgrading\" for each session"
   - **Code**: `mclaude-web/src/types.ts:307` — `LifecycleEvent.type` is a closed union that does not include `"session_upgrading"`.

9. **`Recreate` strategy must be set on existing Deployments, not just new ones** — Both `reconcileDeployment` in `reconciler.go` (lines 313-317) and `ensureDeployment` in `provision.go` (lines 411-414) update existing Deployments by patching only the container image. Neither sets `.Spec.Strategy`. For existing Deployments already deployed with the default `RollingUpdate` strategy, the strategy will never be changed to `Recreate` unless the update path explicitly sets it. The doc says "ensureDeployment (both provision.go and reconciler.go) sets the Deployment strategy to Recreate" but the update code path in both files does not touch the strategy field.
   - **Doc**: "ensureDeployment (both provision.go and reconciler.go) sets the Deployment strategy to Recreate"
   - **Code**: `reconciler.go:313-317` only patches `Containers[0].Image`; `provision.go:412-414` does the same. Neither existing update branch sets `.Spec.Strategy`.

10. **ConfigMap watch in `SetupWithManager` uses `EnqueueRequestsFromMapFunc` but the object namespace for cross-namespace ConfigMaps is unspecified** — The doc shows pseudocode that checks `obj.Name != releaseName+"-session-agent-template"`. But the ConfigMap lives in the control-plane namespace (`mclaude-system`), while MCProject CRs live in the control-plane namespace too (per `mcproject-crd.yaml`). The controller-runtime `Watches` call must also be scoped to the correct namespace — either via a `WithPredicates` filter or by ensuring the cache watches the right namespace. The doc does not specify this, and the current `SetupWithManager` uses `enqueueForOwner` (owner-reference based), which does not work for the session-agent-template ConfigMap because it has no owner reference pointing at MCProject CRs.
    - **Doc**: "Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(...))"
    - **Code**: `reconciler.go:560-573` — existing `SetupWithManager` uses `enqueueForOwner` for ConfigMaps; no cross-namespace watch is implemented. The cache scope and namespace filter are not specified.

11. **`terminationGracePeriodSeconds` Helm template uses `| quote` but the existing template already has it** — The doc says add `terminationGracePeriodSeconds: {{ .Values.sessionAgent.terminationGracePeriodSeconds | quote }}` to `session-agent-pod-template.yaml`. But looking at the existing template (`charts/mclaude/templates/session-agent-pod-template.yaml:16`), that line is already present verbatim. The doc's instruction implies it doesn't exist, leading a developer to add a duplicate. The real change is only in `values.yaml` (changing 30 → 86400). The doc should not describe the template line as new.
    - **Doc**: "session-agent-pod-template.yaml — add terminationGracePeriodSeconds"
    - **Code**: `charts/mclaude/templates/session-agent-pod-template.yaml:16` — line already exists exactly as specified.

12. **`SessionState` TypeScript type does not include `"updating"`** — The doc specifies adding `"updating"` to `STATE_LABELS` in `SessionDetailScreen.tsx` and to `StatusDot` styling, but the `SessionState` union type in `types.ts` (lines 78-87) does not include `"updating"`. The type is used throughout the codebase as a closed union (e.g., `EventStore._sessionState: SessionState`, `SessionDetailScreen` props). If `"updating"` is not added to the union, TypeScript will reject the `sessionState === 'updating'` checks and the `STATE_LABELS` entry as type errors. The doc does not mention updating `types.ts`.
    - **Doc**: "Add `updating` state to `STATE_LABELS` and `StatusDot`"
    - **Code**: `mclaude-web/src/types.ts:78-87` — `SessionState` is a closed union with no `"updating"` member; `mclaude-web/src/stores/event-store.ts:34` holds `_sessionState: SessionState`.

## Run: 2026-04-14T10:00:00Z

**Second-round audit — all 12 gaps from the first round have been addressed in the current document. Reviewing for remaining blocking issues.**

**Gaps found: 7**

1. **`MCLAUDE_EVENTS` stream subject filter does not capture the `_api` error subject** — The doc specifies the `api_error` event is published to `mclaude.{userId}.{projectId}.events._api`. The existing `MCLAUDE_EVENTS` stream has `Subjects: ["mclaude.*.*.events.*"]`. The `*` wildcard in NATS matches exactly one token. `_api` is a single token, so `mclaude.*.*.events._api` does match the stream filter — the `*` in `mclaude.*.*.events.*` will match `_api`. This is not a gap. However, the doc states the `EventStore` must subscribe to the `_api` subject "in addition to per-session event subjects" and that "this subscription is set up when the store initializes for a project." The `EventStore` constructor today is session-scoped (takes `sessionId`) and subscribes to `mclaude.{userId}.{projectId}.events.{sessionId}` only. A project-level subscription for `_api` errors cannot be added to the current `EventStore` (which is one-per-session) without either restructuring it or creating a separate subscriber. The doc does not specify which class or component owns the `_api` subscription, what its lifetime is, and how it is started. A developer cannot implement `onApiError` without knowing where the subscription is created.
   - **Doc**: "The `EventStore` must subscribe to the `_api` subject in addition to per-session event subjects. This subscription is set up when the store initializes for a project."
   - **Code**: `EventStore` (`mclaude-web/src/stores/event-store.ts`) accepts `sessionId` as a constructor option and subscribes only to the per-session subject. There is no project-scoped store that could hold the `_api` subscription.

2. **`StatusDot` styling specification contradicts the existing implementation** — The doc says to add `updating: 'bg-blue-400 animate-pulse'` to `StatusDot`, implying Tailwind CSS class-based styling. The actual `StatusDot.tsx` uses CSS custom properties (`'var(--orange)'`, `'var(--green)'`, etc.) in a `STATE_COLORS` record with an inline `background` style, and a separate `PULSE_STATES` set. A developer following the doc's Tailwind snippet will produce non-compiling or visually inconsistent code. The correct format for `StatusDot` is a CSS color string entry in `STATE_COLORS` and adding `'updating'` to `PULSE_STATES`.
   - **Doc**: "Add `updating` state styling — `updating: 'bg-blue-400 animate-pulse'`"
   - **Code**: `mclaude-web/src/components/StatusDot.tsx:10-27` — uses `STATE_COLORS: Record<string, string>` with CSS variable strings and `PULSE_STATES: Set<string>`, not Tailwind classes.

3. **`SessionState` type name mismatch — doc introduces `SessionStateValue` but codebase uses `SessionState`** — The doc says to add `'updating'` to `SessionStateValue`. The actual union type in `types.ts` is named `SessionState` (line 78), not `SessionStateValue`. The doc's invented name `SessionStateValue` does not exist anywhere in the codebase. A developer will be confused about which type to edit.
   - **Doc**: "Add `'updating'` to the `SessionStateValue` union type in `types.ts`"
   - **Code**: `mclaude-web/src/types.ts:78` — `export type SessionState = ...`; no `SessionStateValue` exists.

4. **`LifecycleEvent` type change method is unspecified — the type in `types.ts` is a concrete interface, not a union alias** — The doc says to add `'session_upgrading'` to `LifecycleEventType`. No type named `LifecycleEventType` exists. The actual type is the `LifecycleEvent` interface at `types.ts:306` which has `type: 'session_created' | 'session_stopped' | 'session_restarting' | 'session_failed'` as an inline union on the `type` property. A developer must expand this inline union directly on the interface's `type` field, not a separate type alias. The doc's invented name `LifecycleEventType` doesn't tell a developer where to make the edit.
   - **Doc**: "Add `'session_upgrading'` to the `LifecycleEventType` type union"
   - **Code**: `mclaude-web/src/types.ts:306-311` — `LifecycleEvent` interface with `type: 'session_created' | 'session_stopped' | 'session_restarting' | 'session_failed'`; no separate `LifecycleEventType` type alias exists.

5. **`builder.WithPredicates` and `predicate` packages are not imported in `reconciler.go` and are not in the control-plane's imports** — The doc's pseudocode for `SetupWithManager` uses `builder.WithPredicates(predicate.NewPredicateFuncs(...))`. Neither `sigs.k8s.io/controller-runtime/pkg/builder` nor `sigs.k8s.io/controller-runtime/pkg/predicate` is currently imported in `reconciler.go`. The existing code imports `ctrl`, `client`, `ctrlutil`, `handler`, and `reconcile`, but not `builder` or `predicate`. A developer must add these imports, and the correct package paths are not confirmed in the doc.
   - **Doc**: "builder.WithPredicates(predicate.NewPredicateFuncs(...))"
   - **Code**: `mclaude-control-plane/reconciler.go:9-31` — import block does not include `builder` or `predicate` packages.

6. **`DashboardScreen.tsx` also has `STATE_LABELS` and a cast that must be updated for `'updating'`** — The doc specifies adding `'updating'` to `STATE_LABELS` in `SessionDetailScreen.tsx`. But `DashboardScreen.tsx` has its own separate `STATE_LABELS` record (line 72) and a hard-coded type cast at line 294: `state={session.state as 'idle' | 'running' | 'requires_action' | 'restarting' | 'failed'}`. Without updating `DashboardScreen.tsx`, the dashboard will display a raw `"updating"` string (or `undefined` for the status dot) and TypeScript will not accept `'updating'` in the cast. The doc does not mention `DashboardScreen.tsx`.
   - **Doc**: "Add `updating` state to `STATE_LABELS`" (only mentions `SessionDetailScreen`)
   - **Code**: `mclaude-web/src/components/DashboardScreen.tsx:72` — separate `STATE_LABELS`; line 294 — hard-coded state type cast that excludes `'updating'`.

7. **`handleCreate` in `provision.go` path is not covered by Recreate strategy update** — The doc says both `ensureDeployment` paths (in `provision.go` and `reconciler.go`) must set `Recreate` strategy. The doc's code snippet for the update path shows `existing.Spec.Strategy = ...` alongside the image update. But `provision.go`'s `ensureDeployment` update path (lines 411-414) is a completely separate function from `reconciler.go`'s `reconcileDeployment` update path (lines 313-317). The doc only shows one code snippet for the update path and does not clarify that this same change must be independently applied in both files. More importantly, `provision.go` is called by `K8sProvisioner.ProvisionProject()` (the imperative provisioner, not the reconciler). After this feature ships, `ProvisionProject` and `ensureDeployment` in `provision.go` may still be invoked in some paths. The doc does not specify whether `provision.go`'s update path should also be changed or whether `provision.go` is being retired. If it is still active, it must also be updated — if it is being retired, the doc should say so.
   - **Doc**: "Both `ensureDeployment` paths (in `provision.go` and `reconciler.go`) set the Deployment strategy to `Recreate`"
   - **Code**: `mclaude-control-plane/provision.go:411-414` and `mclaude-control-plane/reconciler.go:313-317` — two independent update code paths, both currently only patching `Containers[0].Image`.

## Run: 2026-04-14T18:00:00Z

**Third-round audit — reviewing document after all 7 second-round gaps were addressed.**

**Gaps found: 4**

1. **SPA `subscribe` API mismatch in error listener code** — Both the "SPA: API Error Listener" and "SPA: Session List VM" sections show code that calls `this.natsClient.subscribe(apiSubject)` and then does `for await (const msg of sub)` and `errSub.unsubscribe()`. But `INATSClient.subscribe` (types.ts:26) takes a callback and returns `() => void` — an unsubscribe function, not an async iterable. The pattern `for await (const msg of sub)` and `errSub.unsubscribe()` is the raw nats.ws API, not the `INATSClient` wrapper API. As written, the code won't compile: `for await` requires an async iterable, and calling `.unsubscribe()` on a `() => void` function will throw at runtime. A developer must translate the design snippet into the callback-based API, but the design gives no guidance on how to do this (including how to stop the subscription on `cleanup()`).
   - **Doc**: "const sub = this.natsClient.subscribe(apiSubject) / for await (const msg of sub)" and "errSub.unsubscribe()"
   - **Code**: `mclaude-web/src/types.ts:26` — `subscribe(subject: string, callback: (msg: NATSMessage) => void): () => void`; `mclaude-web/src/transport/nats-client.ts:58-79` — subscribe returns a plain unsubscribe function.

2. **`_previousKeys` mechanism in `onSessionAdded` has no implementable "initial load complete" signal** — The design's `onSessionAdded` relies on `this._previousKeys: Set<string>` populated on "initial KV watch load." But the existing `INATSClient.kvWatch` (and its implementation in `nats-client.ts:138-176`) fires the callback continuously for both initial and subsequent entries, with no sentinel to mark the end of initial values (unlike the Go server-side `WatchAll` which sends a `nil` entry). The design provides no mechanism — no API change to `kvWatch`, no alternative approach — for `SessionStore` to know when initial load is complete. A developer cannot implement `_previousKeys` correctly without either modifying the `kvWatch` contract (not mentioned) or accepting that every existing session will trigger `onSessionAdded` during startup.
   - **Doc**: "The store tracks `_previousKeys: Set<string>` — populated on initial KV watch load"
   - **Code**: `mclaude-web/src/transport/nats-client.ts:138-176` — `kvWatch` has no end-of-initial-values signal; `mclaude-web/src/stores/session-store.ts` has no `_previousKeys` field and no mechanism to distinguish initial load from new entries.

3. **`handleCreate` (and `handleDelete`, `handleRestart`) must parse `requestId` from the request payload, but the design doesn't say so** — The design specifies the `api_error` event payload includes `request_id` echoed from the original request. The SPA sends `requestId` in the create payload (`{ projectId, branch, name, requestId }`). For the session-agent to echo it, `handleCreate`'s request struct must include a `RequestID string` field. The current struct (agent.go:284-289) only has `Name`, `Branch`, `CWD`, and `JoinWorktree`. The design never explicitly instructs developers to add `RequestID` to the handler request structs — the Go handler side of the `requestId` round-trip is entirely absent from the design.
   - **Doc**: `{"type":"api_error","request_id":"<client-generated UUID>",...}` — implies the agent echoes the ID back
   - **Code**: `mclaude-session-agent/agent.go:284-289` — `handleCreate` request struct has no `RequestID` field; `handleDelete` (agent.go:439-445) and `handleRestart` (agent.go:603-609) likewise have no `RequestID` field.

4. **Startup recovery ordering contradiction: "updating" → "idle" KV write happens at step 1, not step 4** — The design's `Run()` startup sequence says `clearUpdatingState()` at step 4 "writes state:\"idle\" to KV for any session still in \"updating\"" — implying the banner clears only after JetStream consumers attach at step 2. But step 1 (`recoverSessions()`) calls `clearPendingControlsForResume(&st)` (state.go:87-90) which unconditionally sets `st.State = StateIdle`, then immediately calls `a.writeSessionKV(st)` (agent.go:166). By the time step 4 runs, every session has already been written as "idle" in KV — `clearUpdatingState()` will find nothing to do. The design's stated goal (banner disappears only after consumers are attached) is contradicted by the existing `recoverSessions()` code path. A developer implementing this as documented will get the wrong sequencing unless `recoverSessions()` is also modified to skip the KV write for "updating" sessions — but the design does not specify that change.
   - **Doc**: "Step 4: clearUpdatingState() — writes state:\"idle\" to KV for any session still in \"updating\"" and "The clearUpdatingState() in step 4 handles any sessions that were \"updating\" but not yet cleared"
   - **Code**: `mclaude-session-agent/agent.go:164-166` — `recoverSessions()` calls `clearPendingControlsForResume` then `writeSessionKV` for every session unconditionally (including those in "updating"); `mclaude-session-agent/state.go:87-90` — `clearPendingControlsForResume` sets `st.State = StateIdle` with no condition.

## Run: 2026-04-14T22:00:00Z

**Fourth-round audit — reviewing document after all 4 third-round gaps were addressed.**

**Gaps found: 2**

1. **`NATSClient.kvWatch` silently drops DEL operations — `deleteSession` fire-and-forget success signal will never arrive** — The doc redesigns `deleteSession()` as fire-and-forget: "Session disappears from KV (KV deletion observed by existing watcher)." The session-store's `kvWatch` callback has a `catch` block intended to detect deletions (comment: "Deleted key or malformed"), but `NATSClient.kvWatch` (`nats-client.ts:158`) explicitly skips all DEL and PURGE operations before calling the callback (`if (entry.operation === 'DEL' || entry.operation === 'PURGE') continue`). The callback is never invoked for deletions, so `_sessions.delete(sessionId)` is never called, and the session never disappears from the SPA's view. The doc does not address this gap — it assumes KV deletion is observable from the browser, but the existing transport layer makes it invisible. A developer implementing `deleteSession` as documented will produce a broken UX where deleted sessions persist in the list indefinitely.
   - **Doc**: "Session disappears from KV. Error → publish `api_error` event." (Reply Mechanism Change table, handleDelete row)
   - **Code**: `mclaude-web/src/transport/nats-client.ts:158` — `if (entry.operation === 'DEL' || entry.operation === 'PURGE') continue` — callback is never invoked for deletions; `mclaude-web/src/stores/session-store.ts:39-47` — catch block for deletion detection is dead code because the callback is never called for DEL entries.

2. **`onSessionAdded` requires `_addListeners` to be called from the KV watch callback, but the doc does not specify the invocation point or what constitutes a "new" key at the store level** — The doc specifies the `onSessionAdded` method and states "The existing KV watch callback in `SessionStore` must invoke `_addListeners` when a new key is observed." However, the doc does not specify: (a) what precise condition in the KV watch callback triggers calling `_addListeners` (a key that was not in `_sessions` before the current update?), and (b) in what order `_sessions.set(state.id, state)` and the `_addListeners` call happen (the handlers use `this._sessions.keys()` snapshot taken at registration time to determine novelty, so `_sessions` must NOT yet contain the new key at call time, or must already contain it — the doc is silent on this ordering). The two possible orderings produce different behavior: if `_sessions` is updated before `_addListeners` is called, the KV watch callback itself cannot determine novelty (it would need its own tracking set); if `_sessions` is not yet updated, the `onSessionAdded` handler receives a key not in `_sessions`, which matches the snapshot check. A developer reading only the doc cannot determine the required ordering.
   - **Doc**: "The existing KV watch callback in `SessionStore` must invoke `_addListeners` when a new key is observed (alongside the existing `_listeners` for general changes)."
   - **Code**: `mclaude-web/src/stores/session-store.ts:33-48` — the KV callback does `_sessions.set(state.id, state)` then `_notifySessions()`; there is no `_addListeners` field, no novelty-detection logic, and no specified ordering for the new call.

## Run: 2026-04-14T23:00:00Z

**Fifth-round audit — reviewing document after both fourth-round gaps were addressed.**

CLEAN — no blocking gaps found.

All 12 gaps from rounds 1–4 have been addressed in the current document. Specific verification against the codebase:

- **kvWatch DEL fix**: Document now specifies the exact change to `nats-client.ts:158` (remove the skip), the `operation?` field addition to `KVEntry` in `types.ts`, and the full updated `session-store.ts` callback. The fix is unambiguous and directly implementable.

- **`onSessionAdded` invocation ordering**: Document now specifies the `knownAtRegistration` snapshot approach, the `_addListeners` array field on `SessionStore`, `_notifyAddListeners(id, state)` method called after `_sessions.set()` and `_notifySessions()`, and the full method implementation. The ordering is explicit: `_sessions.set()` first, then `_notifyAddListeners()`. Novelty detection is handled by each listener's own snapshot — not by the store — which is correct and self-consistent.

- **SPA `subscribe` API**: Document now uses the correct `INATSClient.subscribe(subject, callback)` callback-based pattern (not `for await`) with the returned `() => void` unsubscribe function called via `cleanup()`. Matches `types.ts:26` and `nats-client.ts:58-79`.

- **`api_error` event subject**: Clearly specified as `mclaude.{userId}.{projectId}.events._api` with a project-scoped temporary subscription in `createSession()`, not via `EventStore`.

- **Handler `RequestID` fields**: Document explicitly specifies adding `RequestID string \`json:"requestId"\`` to `handleCreate`, `handleDelete`, and `handleRestart` request structs.

- **`StateUpdating` location**: Document now correctly says `events.go` (where `StateIdle`, `StateRunning`, `StateRequiresAction` live at lines 26–29).

- **Startup recovery ordering**: Document now explicitly calls out that `recoverSessions()` must skip the KV write for sessions in "updating" state, or set in-memory state to idle without persisting, so that `clearUpdatingState()` at step 4 is what actually writes `state:"idle"` to KV.

- **Both `ensureDeployment` paths**: Document specifies both `reconciler.go` and `provision.go` update paths must set `Spec.Strategy = Recreate`, with explicit code for both the create and update branches.

- **ConfigMap watch**: Document specifies the `builder.WithPredicates`/`predicate.NewPredicateFuncs` approach with the required new imports (`sigs.k8s.io/controller-runtime/pkg/builder` and `sigs.k8s.io/controller-runtime/pkg/predicate`).

- **`DashboardScreen.tsx`**: Document now covers both the `STATE_LABELS` addition and the type cast at line 294.

- **`StatusDot`**: Document now specifies `var(--blue)` in `STATE_COLORS` and adding `'updating'` to `PULSE_STATES` — matching the CSS-variable-based pattern in `StatusDot.tsx:10-27`.

- **`SessionState` and `LifecycleEvent` types**: Document now correctly names the types (`SessionState` at `types.ts:78`, inline union on `LifecycleEvent.type` at `types.ts:307`) and specifies the exact additions.
