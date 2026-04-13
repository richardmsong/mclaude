## Run: 2026-04-13T22:45:00Z

GAP: "Exposes unix socket for `mclaude-cli` debug attach" → No unix socket implementation found in mclaude-session-agent. Terminal.go has PTY support but no unix socket listener for debug attach at /tmp/mclaude-session-{id}.sock.

GAP: "For auto-approve workflows (CI, batch jobs), the session agent can be configured with a permission policy that auto-responds to `control_request` events" → No permissionPolicy config implementation found. Agent.go does not parse or support permissionPolicy (auto/managed/allowlist) configuration.

GAP: "permissionPolicy: \"auto\"  # auto-approve all tools, permissionPolicy: \"managed\"  # forward to client (default), permissionPolicy: \"allowlist\"  # auto-approve listed tools, forward rest, allowedTools: [\"Bash\", \"Read\", \"Edit\", \"Write\", \"Glob\", \"Grep\"]" → No config parsing or auto-response logic implemented for permission policies.

GAP: "On session delete: Scan KV bucket for all sessions in this projectId — if no other session has the same `worktree` slug: `git -C /data/repo worktree remove /data/worktrees/{branchSlug}`" → handleDelete() in agent.go does not execute git worktree remove command when no other sessions use the worktree.

GAP: "On session create: if not found → `git -C /data/repo worktree add /data/worktrees/{branchSlug} {branch}`" → handleCreate() in agent.go does not execute git worktree add command.

GAP: "On SIGTERM (pod termination): Stop accepting new sessions" → gracefulShutdown() in agent.go terminates active sessions but does not stop subscribeAPI() or prevent new requests from being processed.

GAP: "`replayFromSeq` is the JetStream sequence number from which clients should start replaying events. Updated by the session agent on `/clear` (conversation reset) and compaction (context compacted)." → handleSideEffect() in session.go has comment "replayFromSeq will be updated by the agent with the JetStream seq" but no implementation to capture and store JetStream sequence numbers.

GAP: "Session agent lifecycle events published on `mclaude.{userId}.{projectId}.lifecycle.{sessionId}` include: session_failed" → publishLifecycle() in agent.go does not publish session_failed events when sessions fail to start or encounter errors.

GAP: "Session agent lifecycle events published on `mclaude.{userId}.{projectId}.lifecycle.{sessionId}` include: debug_attached, debug_detached" → No implementation of debug attach/detach lifecycle events.

GAP: "Exposes unix socket for `mclaude-cli` debug attach... Text REPL wraps input as stream-json user messages, displays assistant text, prompts on control_requests. ~150 lines of Go." → No debug attach REPL implementation found. Terminal.go provides PTY routing but not the unix socket REPL interface.

GAP: "`/health` (liveness) never checks NATS — the pod must stay alive and port-forwardable even when NATS is down" and "command: [\"session-agent\", \"--health\"]  # checks process alive + NATS connection" → No --health flag or /health endpoint implementation in main.go or agent.go.

GAP: "Sessions that fail to start within 30s: mark state: \"failed\", publish session_failed" → No timeout handling or session_failed state management in handleCreate(). If Claude fails to start, the error is replied but no "failed" state is persisted to KV.

## Run: 2026-04-13T13:37:00Z

GAP: Spec line 182 "Session agents and launchers do not create buckets — they fail fast if a bucket doesn't exist" → agent.go line 51-62 uses CreateOrUpdateKeyValue() which creates buckets instead of failing fast. Should use KeyValue() with error handling to fail on missing buckets.

GAP: Spec line 347 "Session operations: `…api.sessions.create`" with step "if not found → `git -C /data/repo worktree add /data/worktrees/{branchSlug} {branch}`" → agent.go handleCreate() does not execute git worktree add command.

GAP: Spec line 436-438 "On session delete: Scan KV bucket for all sessions in this projectId — if no other session has the same `worktree` slug: `git -C /data/repo worktree remove /data/worktrees/{branchSlug}`" → agent.go handleDelete() does not execute git worktree remove command.

GAP: Spec line 1051-1059 "Health Probes: livenessProbe: command: [\"session-agent\", \"--health\"], readinessProbe: command: [\"session-agent\", \"--ready\"]" → main.go does not handle --health or --ready CLI flags. No implementation of health check endpoints or flag parsing.

GAP: Spec line 315-323 "Permission handling: session agent can be configured with a permission policy that auto-responds to `control_request` events without forwarding to NATS: permissionPolicy: 'auto'/'managed'/'allowlist' and allowedTools: [...]" → No permission policy configuration parsing or auto-response logic implemented in agent.go or session.go.

GAP: Spec line 365-381 "Debug attach (mclaude-cli): Session agent exposes a unix socket per session at `/tmp/mclaude-session-{id}.sock`" and "Text REPL wraps input as stream-json user messages, displays assistant text, prompts on control_requests. ~150 lines of Go." → No unix socket listener or REPL implementation in any source file.

GAP: Spec line 329-339 "Graceful shutdown: 1. Stop accepting new sessions..." → agent.go gracefulShutdown() (line 156) stops active sessions but does not unsubscribe from NATS API subjects, allowing new requests to continue being processed after shutdown begins.

GAP: Spec line 295-296 "Core loop: case 'clear', 'compact_boundary': updateReplayFromSeq(line, jetStreamSeq)" → session.go handleSideEffect() line 228 only has comment "replayFromSeq will be updated by the agent" but does not capture JetStream sequence numbers or update replayFromSeq when compact_boundary events are received.

GAP: Spec line 486 "Updated by the session agent on `/clear` (conversation reset) and compaction" → session.go does not handle "clear" event type at all. EventTypeClear constant is missing, and no side effect handler for clearing replayFromSeq.

GAP: Spec line 1099-1109 "Recovery sequence on startup: Sessions that fail to start within 30s: mark state: 'failed', publish session_failed" → agent.go handleCreate() line 267-270 does not implement 30-second timeout, does not set session state to "failed", and does not publish session_failed lifecycle event on startup failure.

GAP: Spec line 507-514 "Session agent lifecycle events: session_failed, debug_attached, debug_detached" → agent.go publishLifecycle() does not support or publish session_failed, debug_attached, or debug_detached event types. Only publishes: session_created, session_stopped, session_restarting, session_resumed.

GAP: Spec line 463-467 "Capabilities: skills, tools, agents" → events.go initEvent struct (line 48-56) is missing skills and agents fields. Only captures tools. session.go handleSideEffect() line 192 only populates Tools in Capabilities, ignoring skills and agents from the init event.

GAP: Spec line 162 "If a single event still exceeds 8MB, the session agent truncates the `content` field and sets a `truncated: true` flag" → session.go line 167 publishes events to NATS without checking event size or implementing truncation for oversized messages.

GAP: Spec line 1087 "NATS unavailability: buffer state changes in memory, flush on reconnect... Events published just before disconnect may be re-published on reconnect" → session.go and agent.go have no event buffering mechanism if NATS connection drops. Events are published directly without buffering.

