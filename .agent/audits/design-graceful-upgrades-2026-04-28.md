## Run: 2026-04-28T00:00:00Z

CLEAN — no blocking gaps found.

### Verification summary

Evaluated ADR-0043 against the existing codebase, spec-state-schema.md, spec-session-agent.md, and Claude Code source files at `/Users/rsong/work/collection-claude-code-source-code/`.

**Already fully implemented in the codebase (not pending work):**
- `StateUpdating` constant in `events.go`
- MCLAUDE_API JetStream stream creation in `NewAgent`
- Two durable pull consumers (cmd + ctl) in `createJetStreamConsumers()`
- JetStream fetch loop with `jetstream.Msg` → `*nats.Msg` adapter in `runConsumer()`
- SIGTERM drain: write "updating" to KV, stop cmd, drain subs, keep ctl, poll predicate, auto-interrupt `requires_action`, stop ctl, publish `session_upgrading`, exit — in `gracefulShutdown()`
- `inFlightBackgroundAgents` counter in `session.go`
- `shutdownPending` flag + KV write suppression in `session.go`
- Startup recovery (`pendingUpdatingIDs`, `clearUpdatingState()`) in `agent.go`
- ConfigMap watch and Recreate strategy in `reconciler.go`
- `terminationGracePeriodSeconds: 86400` in `charts/mclaude-worker/values.yaml`
- SPA: `onSessionAdded`, kvWatch DEL/PURGE handling, `createSession` publish+KV-watch+api_error, `deleteSession` fire-and-forget — all in `session-store.ts` and `session-list-vm.ts`
- SPA: `SessionState` includes `'updating'`, `LifecycleEvent.type` includes `'session_upgrading'`, `KVEntry.operation` typed — in `types.ts`
- SPA: `subjEventsApi` function in `subj.ts`
- SPA: `STATE_LABELS.updating` in both `DashboardScreen.tsx` and `SessionDetailScreen.tsx`
- SPA: `StatusDot` has `updating: 'var(--blue)'` in `STATE_COLORS` and in `PULSE_STATES`
- `publishAPIError` function in `agent.go`
- `RequestID` field in create/delete/restart request structs

**Pending implementation (clearly specified by the ADR, no ambiguity):**
- `inFlightShells` / `pendingShells` maps in `Session` struct
- Two-phase shell tracking in stdout router
- `CLAUDE_CODE_TMPDIR` env var and PVC subPath VolumeMount in `buildPodTemplate`
- Shell notification publish step in `gracefulShutdown` (between drain and ctl-stop)
- `sanitizePath` and `shellOutputPath` Go helpers
- "Updating..." banner UI in `SessionDetailScreen`

**Cross-spec consistency verified:**
- MCLAUDE_API stream subjects match spec-state-schema ✓
- Consumer naming convention (`sa-cmd-{uslug}-{pslug}`, `sa-ctl-{uslug}-{pslug}`) consistent ✓
- `sessions.input` payload (`{session_id, type, message}`) consistent ✓
- `api_error` event published to `events._api` subject — matches spec and existing code ✓
- Shell output path formula matches Claude Code source (`getProjectTempDir()` = `{CLAUDE_CODE_TMPDIR}/claude-{uid}/{sanitizePath(cwd)}/`) ✓
- `<tool-use-id>` is present in real task-notifications for background bash tasks (confirmed in `LocalShellTask.tsx:enqueueShellNotification`) ✓
- Agent task-notifications also include optional `<tool-use-id>` (confirmed in `LocalAgentTask.tsx:enqueueAgentNotification`) ✓

**Minor documentation gap (not blocking for implementation):**
- The `session_upgrading` lifecycle event payload is not listed in the spec-state-schema.md lifecycle events section, and the Impact section does not instruct adding it. A developer can infer the payload shape from existing lifecycle events (same `{type, sessionId, ts}` pattern). This does not block code implementation.
