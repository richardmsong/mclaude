# Backgrounded Shell Survival Across Pod Restarts

## Overview

When a session-agent pod is upgraded (Helm rollout, Recreate strategy), any `Bash(run_in_background=true)` shells that the session's Claude launched are killed with the pod. The resumed Claude on the new pod has no way to know a shell died — the `tool_use` is dangling in the transcript, and any future `BashOutput` call on the shell-id would reference a process that no longer exists.

This plan specifies how session-agent synthesizes a `task-notification` message for each killed shell on SIGTERM, so the resumed Claude sees the death in its event log and can adapt (re-run the shell with adjusted intent, inform the user, or move on).

**Related work:** `docs/adr-2026-04-14-graceful-upgrades.md` covers the SIGTERM drain flow for the main turn and in-flight background **agents** (subagents). That plan deliberately does not handle backgrounded shells — they're covered here. Agents are expensive (context + tokens) so drain waits for them to finish naturally; shells are cheap to restart but have side effects, so this plan handles their death explicitly.

## Claude Code's native pattern (reference)

Discovered in `/Users/rsong/work/collection-claude-code-source-code/claude-code-source-code/`:

**Task-notification XML shape** (emitted by `LocalShellTask.tsx:105-172` and `LocalAgentTask.tsx:200-262`):

```xml
<task-notification>
  <task-id>shell-abc123</task-id>
  <tool-use-id>toolu_01ABC...</tool-use-id>
  <output-file>/tmp/claude-{uid}/.../sessionId/tasks/{taskId}.output</output-file>
  <status>completed|failed|killed</status>
  <summary>"command" was stopped / completed with exit N</summary>
</task-notification>
```

- `killed` is a first-class status — Claude Code emits this when a shell is terminated externally (not just clean exit).
- The XML lives in a synthetic **user message** injected into the conversation via `enqueuePendingNotification()` at `src/utils/messageQueueManager.ts:142-149` with priority `later`.
- The resumed Claude sees it on its next turn and reacts naturally.

**Output file path** (`src/utils/task/diskOutput.ts:72-74` + `src/utils/permissions/filesystem.ts:331-346`):

```
$CLAUDE_CODE_TMPDIR/claude-{uid}/{sanitized-cwd}/{sessionId}/tasks/{taskId}.output
```

- `CLAUDE_CODE_TMPDIR` env var overrides the base `/tmp`.
- The `{taskId}.output` file holds stdout/stderr (interleaved) for the shell.

**No external injection API:** Claude Code has no stdin JSON, file-drop, or HTTP endpoint for an orchestrator to inject synthetic task-notifications. They all flow through in-process `enqueuePendingNotification()`. Our path is to publish onto the NATS session input subject — which session-agent already forwards to Claude's stdin as a user message — with the XML as the message content.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Handle shell deaths? | Yes, synthetic notification on drain | Shells are cheap to restart but have side effects; Claude needs to know to adjust intent on resume. |
| Handle agent deaths the same way? | No — drain waits for agents | Agents have context + token cost; can't meaningfully "retry". Covered in `adr-2026-04-14-graceful-upgrades.md`. |
| Notification transport | Publish onto session input subject in JetStream | Reuses existing durable path. No new storage, no authz to bypass (session-agent is the trusted consumer of its own subjects). |
| Output file persistence | Set `CLAUDE_CODE_TMPDIR` → PVC subPath | Scopes persistence to Claude-owned files, reuses existing session PVC, no new volume. |
| Tracking structure | `map[shellId]ShellMeta` per session, keyed by Claude-emitted shell-id | Need to iterate in-flight shells at drain time; count alone isn't enough. |
| What triggers tracking | stream-json parse: `Bash` tool_use with `run_in_background: true` | Same pattern as the in-flight agent counter in `adr-2026-04-14-graceful-upgrades.md`. |
| What removes tracking | User message with `origin.kind == "task-notification"` referencing the shell's tool-use-id | Real task-notification arrived (shell completed naturally during normal operation). |

## Flow

### Normal operation (no pod restart)

```
1. User asks Claude to run a long command in the background.
2. Claude emits Bash tool_use with run_in_background=true.
3. Session-agent stream-json router observes the tool_use:
     sess.inFlightShells[toolUseId] = {toolUseId, shellId, command, startedAt}
4. Shell runs. Output written to $CLAUDE_CODE_TMPDIR/claude-{uid}/.../tasks/{taskId}.output.
5. Shell exits naturally. Claude emits a task-notification user message.
6. Session-agent router observes origin.kind=="task-notification" → removes the entry from inFlightShells.
7. Claude's next turn processes the notification and responds.
```

### SIGTERM during pod upgrade

```
1. Session-agent receives SIGTERM (see adr-2026-04-14-graceful-upgrades.md for main flow).
2. After the main-turn drain predicate is satisfied (state == idle, no in-flight agents),
   and BEFORE stopping the control consumer and exiting:
3. For each session, for each entry in sess.inFlightShells:
     a. Construct task-notification XML with status=killed:
        <task-notification>
          <tool-use-id>{entry.toolUseId}</tool-use-id>
          <output-file>{entry.outputFilePath}</output-file>
          <status>killed</status>
          <summary>Shell "{entry.command}" was killed during server upgrade</summary>
        </task-notification>
     b. Publish a normal session-input message to
        mclaude.{userId}.{projectId}.api.sessions.input
        with payload {sessionId, content: <xml above>}.
     c. JetStream persists it in MCLAUDE_API.
4. Continue with adr-2026-04-14-graceful-upgrades.md steps (stop ctl consumer, lifecycle event, exit).
5. New pod starts, attaches to the durable cmd consumer.
6. Consumer delivers the queued synthetic input messages.
7. handleInput forwards each one to the resumed Claude's stdin as a normal user message.
8. Claude sees the task-notification XML, can read the output-file from the PVC
   to understand what the shell had done, and decides what to do next.
```

## Component Changes

### Session-Agent: shell tracking

Add to `Session` struct (next to `inFlightBackgroundAgents`):

```go
type inFlightShell struct {
    toolUseId      string  // "toolu_..."
    shellId        string  // Claude's internal shell-id (if surfaced in tool_use)
    command        string  // for the killed-notification summary
    outputFilePath string  // absolute path on the PVC
    startedAt      time.Time
}

// In Session:
inFlightShells map[string]*inFlightShell  // keyed by toolUseId, guarded by sess.mu
```

**Stdout router updates** (same file/function as the `inFlightBackgroundAgents` counter):

- On `assistant` message with a `tool_use` block where `name == "Bash"` AND `input.run_in_background == true`:
  - Construct an `inFlightShell` with toolUseId from the block, command from `input.command`, outputFilePath = `$CLAUDE_CODE_TMPDIR/claude-{uid}/{sanitized-cwd}/{sessionId}/tasks/{taskId}.output`.
  - Note: `taskId` here refers to the shell-task id Claude Code assigns; it may need to be parsed from a later event or derived from the toolUseId. (Open question — see below.)
  - Insert under `sess.mu`.
- On `user` message with `origin.kind == "task-notification"` that references a known toolUseId:
  - Remove from `inFlightShells` under `sess.mu`.

### Session-Agent: drain handler

In `gracefulShutdown()`, after the main drain predicate is satisfied and before stopping the control consumer:

```go
for _, sess := range a.sessions {
    sess.mu.Lock()
    shells := maps.Clone(sess.inFlightShells)
    sess.mu.Unlock()

    for _, shell := range shells {
        xml := fmt.Sprintf(`<task-notification>
  <tool-use-id>%s</tool-use-id>
  <output-file>%s</output-file>
  <status>killed</status>
  <summary>Shell %q was killed during server upgrade</summary>
</task-notification>`, shell.toolUseId, shell.outputFilePath, shell.command)

        payload, _ := json.Marshal(map[string]any{
            "sessionId": sess.id,
            "content":   xml,
        })
        subject := fmt.Sprintf("mclaude.%s.%s.api.sessions.input", a.userId, sess.projectId)
        a.jsPublish(subject, payload)  // publishes into MCLAUDE_API stream
    }
}
```

**Ordering:** this must happen *after* the main-turn drain completes (so we don't publish into an active session that would see the notification twice — once from our publish, once when Claude emits its own). It runs while the cmd consumer is already stopped, so the messages queue for the new pod.

**Idempotency:** if the pod crashes between publishing some notifications and stopping, the crashed pod re-runs drain on next start — but it won't have the `inFlightShells` in memory (new process, empty map), so no duplicate publishes. The new pod consumes the already-published notifications from the durable consumer.

### Helm Chart: PVC mount for Claude temp dir

Session-agent pod template (`charts/mclaude/templates/session-agent-pod-template.yaml`):

```yaml
env:
  - name: CLAUDE_CODE_TMPDIR
    value: /data/claude-tmp
volumeMounts:
  - name: session-data      # existing PVC
    mountPath: /data/claude-tmp
    subPath: claude-tmp     # scopes to a subdirectory of the shared PVC
```

Reuses the existing session PVC via `subPath`. No new volume declaration.

**Path expansion:**
- `CLAUDE_CODE_TMPDIR=/data/claude-tmp`
- `getClaudeTempDir()` → `/data/claude-tmp/claude-{uid}`
- `getProjectTempDir()` → `/data/claude-tmp/claude-{uid}/{sanitized-cwd}`
- Output file → `/data/claude-tmp/claude-{uid}/{sanitized-cwd}/{sessionId}/tasks/{taskId}.output`

This path survives pod restart. When the new pod mounts the same subPath, Claude resumes with `--resume <sessionId>` and can read the old output files when it processes the synthetic task-notifications.

### Session-Agent: outputFilePath construction

The synthetic notification needs to reference the same path Claude used. The session-agent knows:
- `CLAUDE_CODE_TMPDIR` (passed from env, default `/tmp`)
- `{uid}` (process uid on the pod; same for all sessions since they share the pod)
- `{sanitized-cwd}` — Claude Code sanitizes the CWD path for filesystem safety. Algorithm: `src/utils/permissions/filesystem.ts` (check `sanitizePath`)
- `{sessionId}` — per session
- `{taskId}` — *unknown without parsing more stream-json events* (see open questions)

A helper in session-agent:

```go
func shellOutputPath(tmpDir, sanitizedCwd, sessionId, taskId string) string {
    uid := os.Getuid()
    return filepath.Join(tmpDir, fmt.Sprintf("claude-%d", uid),
        sanitizedCwd, sessionId, "tasks", taskId+".output")
}
```

## Error Handling

| Scenario | Behavior |
|----------|----------|
| Shell completes naturally during drain | Real task-notification arrives; router removes from `inFlightShells` before the drain publish loop runs. No synthetic message published. |
| Shell is still running at drain time | Synthetic `killed` notification published to JetStream. New pod replays to resumed Claude. |
| Pod crashes mid-drain, after publishing some notifications | Published messages are durable in JetStream; new pod consumes them. The unpublished in-flight shells are lost (counter was in memory only) — resumed Claude sees some dangling tool_uses. Accepted tradeoff; could be mitigated by persisting `inFlightShells` to KV (deferred). |
| Resumed Claude's output-file read fails (PVC mount issue) | Claude sees the killed notification but can't read partial output; decides based on summary alone. Log volume-mount failures as operator errors. |
| Shell was launched by the pod that's currently resuming (i.e., the old pod died before it could publish) | Current pod has no record of the shell. Dangling tool_use in transcript. Claude will notice the absence when it tries `BashOutput` on an unknown shell-id. Accepted — this is the crashed-before-drain path. |

## Open questions

1. **Where does the `{taskId}` come from?**
   Claude Code's `BashTool` assigns a task-id that becomes part of the output file path. Is this surfaced in the `tool_use` block's input, or in a subsequent event? The session-agent router needs to know the taskId to construct the output-file path for the synthetic notification. Possible answers:
   - It's emitted in a `tool_result` for the Bash tool_use (when `run_in_background=true`, the result returns the shell-id / task-id synchronously).
   - It's derivable from the toolUseId via some convention.
   - It's in a separate stream-json event we're not currently parsing.
   → **Investigation needed** before implementing: run a live Claude with `Bash(run_in_background=true)` and inspect the stream-json events.

2. **Does Claude Code read `<output-file>` from an injected task-notification, or only from ones it emitted itself?**
   The XML tag is part of Claude's own prompt format. The LLM sees it as context regardless of who injected it — it'll read the file if it decides the content helps. → **Likely yes**, but confirm by test.

3. **What if `sanitizePath` differs between pods?**
   The sanitization algorithm must be deterministic on the same CWD across pods. Since Claude's CWD is set by session-agent (same for both old and new pod for the same session), this should be stable. → **Read `src/utils/permissions/filesystem.ts sanitizePath`** to confirm.

4. **Does the shell-id stay stable across resume?**
   Probably not — Claude Code assigns shell-ids per-process, so the resumed Claude has a fresh shell-id space. The synthetic notification's `tool-use-id` is what matters (stable across resume because it's in the transcript). The resumed Claude uses the tool-use-id to correlate, not shell-id.

5. **What if a user sends a message while the synthetic task-notifications are queued?**
   Both messages are in the cmd consumer. They're delivered in publish order. The user's message will be processed after the synthetic notifications, giving Claude a chance to process the killed shells before seeing the new user turn. → Desired behavior; no action needed.

6. **Does `CLAUDE_CODE_TMPDIR` affect other Claude tmp behavior in harmful ways?**
   `getClaudeTempDir()` is used for bundled skills, scratchpad, permission-filesystem checks. All of these are fine on a PVC. → No issue expected.

## Scope

**v1 (this plan):**
- Track in-flight shells per session via stream-json router (`map[toolUseId]*inFlightShell`).
- Set `CLAUDE_CODE_TMPDIR=/data/claude-tmp` in session-agent pod env.
- Mount session PVC at `/data/claude-tmp` via `subPath: claude-tmp`.
- On SIGTERM, after main drain, publish synthetic `<task-notification status=killed>` onto `mclaude.{userId}.{projectId}.api.sessions.input` for each outstanding shell.
- Resolve open question #1 (taskId source) before implementation.

**Deferred:**
- Persist `inFlightShells` to KV so pod crashes don't lose the record.
- Auto-restart with adjusted intent (the prompt `<task-notification status=killed>` is sufficient — Claude decides; no special SDK needed).
- Extending the same pattern to other in-pod side-effect processes (long-running hooks, file watchers).

## References

- Native Claude Code task-notification emission: `LocalShellTask.tsx:105-172`, `LocalAgentTask.tsx:200-262`
- Notification queue: `src/utils/messageQueueManager.ts:142-149`, `src/utils/task/framework.ts:255-269`
- Output path: `src/utils/task/diskOutput.ts:50-74`, `src/utils/permissions/filesystem.ts:307-378`
- Env var: `CLAUDE_CODE_TMPDIR` in `getClaudeTempDir()` at `src/utils/permissions/filesystem.ts:331-346`
- Source checkout: `/Users/rsong/work/collection-claude-code-source-code/`
- Related spec: `docs/adr-2026-04-14-graceful-upgrades.md` (main drain flow, in-flight agent handling)
