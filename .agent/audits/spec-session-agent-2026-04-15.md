## Run: 2026-04-15T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Notes |
|-----------------|-----------|---------------|---------|-------|
| plan-github-oauth.md:370 | "entire credential setup and initial clone happen inside the Go session-agent binary, NOT in entrypoint.sh" | gitcreds.go:1-684, main.go:149-186 | IMPLEMENTED | gitcreds.go contains all credential logic; main.go invokes it before NewAgent |
| plan-github-oauth.md:370 | "entrypoint.sh remains minimal: SSH key setup, env vars, home directory, then exec session-agent. The existing git clone/init block moves entirely to Go." | entrypoint.sh:1-63 | IMPLEMENTED | entrypoint.sh has no git clone/init block; comment at line 35-37 confirms it moved to Go |
| plan-github-oauth.md:376 | "Step 1: Symlink PVC config: Remove any pre-existing ~/.config/ ... symlink /data/.config/ → ~/.config/" | gitcreds.go:324-346 | IMPLEMENTED | symlinkPVCConfig() does exactly this: RemoveAll, MkdirAll /data/.config/, then Symlink |
| plan-github-oauth.md:379 | "Read gh-hosts.yml from Secret mount (/home/node/.user-secrets/gh-hosts.yml)" | gitcreds.go:353-358, gitcreds.go:18 | IMPLEMENTED | ReadSecretFile("gh-hosts.yml") reads from secretMountPath=/home/node/.user-secrets |
| plan-github-oauth.md:380-381 | "Merge strategy: For each host in Secret's gh-hosts.yml, add/update the managed accounts. Do NOT remove accounts only in existing file. Managed token wins on same username." | gitcreds.go:121-186 | IMPLEMENTED | MergeGHHostsYAML implements exactly this logic with per-username merge |
| plan-github-oauth.md:382 | "Same merge for glab-config.yml → ~/.config/glab-cli/config.yml" | gitcreds.go:188-219, gitcreds.go:390-405 | IMPLEMENTED | MergeGLabConfigYAML + mergeAndSetup writes to glab-cli/config.yml |
| plan-github-oauth.md:384-385 | "Register credential helpers: Run gh auth setup-git ... Run glab auth setup-git" | gitcreds.go:409-419 | IMPLEMENTED | Both commands run with non-fatal error handling |
| plan-github-oauth.md:388-389 | "Switch to project identity: If GIT_IDENTITY_ID set, parse hosts.yml to find host for username. Run gh auth switch --user {username} --hostname {host}" | gitcreds.go:432-467 | IMPLEMENTED | switchProjectIdentity reads conn-{id}-username key, finds host, runs gh auth switch |
| plan-github-oauth.md:393 | "conn-{GIT_IDENTITY_ID}-username keys ... session-agent reads conn-{GIT_IDENTITY_ID}-username to resolve UUID to username" | gitcreds.go:469-476 | IMPLEMENTED | resolveUsername reads fmt.Sprintf("conn-%s-username", connectionID) from Secret mount |
| plan-github-oauth.md:397-402 | "Before each git operation: Re-read gh-hosts.yml and glab-config.yml from Secret mount. If changed, re-merge and re-run setup-git. Ensure correct account active." | gitcreds.go:282-298, gitcreds.go:636-659 | IMPLEMENTED | RefreshIfChanged() + RunGitOpWithCredsRefresh() implement this pattern |
| plan-github-oauth.md:403-404 | "SSH→HTTPS normalization: if URL is SCP-style (git@{host}:{path}) and credential helper registered for that host, normalize to HTTPS. Only SCP-style, not ssh:// scheme." | gitcreds.go:52-86 | IMPLEMENTED | NormalizeGitURL correctly handles git@ prefix, excludes ssh://, checks registeredHosts |
| plan-github-oauth.md:406 | "No GIT_ASKPASS, no custom credential provider interface, no hostname matching, no per-provider username mapping." | gitcreds.go:1-684 | IMPLEMENTED | No GIT_ASKPASS implementation exists; credential helpers handle everything |
| plan-github-oauth.md:408 | "Manual auth within sessions ... survives pod restarts (PVC persistence) and is not overwritten by merge step" | gitcreds.go:130-133, gitcreds.go:149-175 | IMPLEMENTED | MergeGHHostsYAML adds managed accounts without removing existing ones |
| plan-github-oauth.md:410 | "Error handling: git fails with auth error (exit code 128 + stderr matching: 'Authentication failed', 'HTTP Basic: Access denied', 'Invalid username or password', 'could not read Username')" | gitcreds.go:27-48 | IMPLEMENTED | gitAuthErrPatterns matches all four, IsGitAuthError checks exitCode==128 |
| plan-github-oauth.md:410 | "session-agent publishes a session_failed event with reason provider_auth_failed" | main.go:169-184 | IMPLEMENTED | GitAuthError detected in main.go, publishes session_failed with provider_auth_failed |
| plan-github-oauth.md:413-425 | "Dockerfile changes: Add gh and glab to session-agent image. gh via apk (github-cli), glab via binary download (not via Nix). ARG TARGETARCH; specific glab version 1.46.0" | Dockerfile:4-17 | IMPLEMENTED | Dockerfile adds github-cli via apk and glab_1.46.0 via curl; ARG TARGETARCH=arm64; curl deleted at end |
| plan-github-oauth.md:415 | "Note: curl is already installed for Claude CLI setup. The glab download must happen BEFORE the existing apk del curl cleanup step." | Dockerfile:9-17 | IMPLEMENTED | glab download is before apk del curl in the same RUN layer |
| plan-github-oauth.md:427 | "gh requires version 2.40+ for multi-account users: map. Alpine github-cli package in node:22-alpine ships gh 2.49+." | Dockerfile:3,9 | IMPLEMENTED | Uses node:22-alpine; installs github-cli package without version pin |
| plan-github-oauth.md:549 | "gh auth setup-git fails: Non-zero exit code at session start. Session-agent logs the error, proceeds without credential helper. Not session-fatal." | gitcreds.go:410-413 | IMPLEMENTED | logs Warn and continues; non-fatal per spec |
| plan-github-oauth.md:550 | "gh auth switch fails: Username not found in hosts.yml. Session-agent logs warning, uses default active account." | gitcreds.go:271-276 | IMPLEMENTED | logs Warn with 'using default active account (non-fatal)' |
| plan-github-oauth.md:277 | "GIT_IDENTITY_ID env var ... session-agent checks os.Getenv('GIT_IDENTITY_ID') != '' to decide whether to switch accounts" | main.go:158-159, gitcreds.go:260-277 | IMPLEMENTED | main.go reads GIT_IDENTITY_ID; Setup() passes to switchProjectIdentity which is no-op when empty |
| plan-k8s-integration.md:267-268 | "Startup/recovery: Read NATS KV for all sessions with this projectId. For each session with sessionId: claude --resume {sessionId}" | agent.go:171-256 | IMPLEMENTED | recoverSessions() watches KV and calls sess.start(claudePath, true, ...) for each |
| plan-k8s-integration.md:263-270 | "What session-agent does: Subscribes to api.>; Spawns Claude as child processes; Routes stdout events → NATS; Routes NATS → stdin; Publishes all stdout events; Tracks session state; Caches capabilities; Spawns PTY sessions; Exposes unix socket; Writes heartbeat; On startup reads KV → resumes" | agent.go:1-1326, session.go:1-473 | IMPLEMENTED | All behaviors present |
| plan-k8s-integration.md:357-361 | "Session operations table: create, delete, input, control, restart" | agent.go:547-1013 | IMPLEMENTED | handleCreate, handleDelete, handleInput, handleControl, handleRestart all implemented |
| plan-k8s-integration.md:340-350 | "Graceful shutdown sequence: Stop accepting new sessions, interrupt each Claude, wait 10s, SIGKILL if running, flush events, publish lifecycle, close NATS, exit 0" | agent.go:418-498 | PARTIAL | gracefulShutdown writes 'updating' state and drains subscriptions, but does NOT send interrupt control_request to each Claude process and wait 10s for exit. Instead it polls for idle/updating state. The spec says 'Send interrupt control_request to stdin' and 'Wait up to 10s for process exit' for each session, but gracefulShutdown() doesn't call sess.stopAndWait(). |
| plan-k8s-integration.md:415 | "Slugification: feature/auth → feature-auth (replace / and non-alphanumeric with -, lowercase)" | worktree.go:8-25 | IMPLEMENTED | SlugifyBranch handles all cases correctly |
| plan-k8s-integration.md:418-446 | "Session create request: name, branch, cwd, joinWorktree. Branch derivation rules. Worktree collision detection. git worktree add." | agent.go:551-754 | IMPLEMENTED | Full create logic matching spec |
| plan-k8s-integration.md:451-456 | "Session delete: interrupt → wait for Claude exit. Remove worktree if last session on branch. Delete from KV." | agent.go:760-830 | IMPLEMENTED | stopAndWait, gitWorktreeRemove, KV delete all present |
| plan-k8s-integration.md:463-502 | "Session state KV schema: id, projectId, branch, worktree, cwd, name, state, stateSince, createdAt, model, capabilities, pendingControls, usage, replayFromSeq" | state.go:27-43 | IMPLEMENTED | All fields present with correct json tags |
| plan-k8s-integration.md:493-494 | "replayFromSeq updated on /clear and compaction. Clients read this before subscribing." | agent.go:1106-1143, session.go:678-686 | IMPLEMENTED | updateReplayFromSeq on compact_boundary event |
| plan-k8s-integration.md:264 | "Writes heartbeat to NATS KV every 30s" | agent.go:1016-1031 | IMPLEMENTED | runHeartbeat with 30s ticker |
| plan-k8s-integration.md:376-378 | "Debug attach unix socket per session at /tmp/mclaude-session-{id}.sock" | debug.go:12-13, debug.go:41-67 | IMPLEMENTED | debugSocketFmt pattern and Start() implementation |
| plan-k8s-integration.md:1016-1064 | "Terminal sessions: spawn via creack/pty, routes raw I/O through NATS; terminal create/delete/resize" | terminal.go:1-133, agent.go:1214-1325 | IMPLEMENTED | startTerminal, NATSTermPubSub, handleTerminalCreate/Delete/Resize all present |
| plan-k8s-integration.md:380-394 | "Startup recovery after ungraceful termination: set all sessions to restarting, clear pendingControls, publish session_restarting, claude --resume each, mark failed after 30s" | agent.go:171-256 | PARTIAL | Code clears pendingControls and resumes sessions, but does NOT set state to "restarting" for ungraceful recovery, does NOT publish "session_restarting" lifecycle events during recovery (only "session_resumed"), and does NOT enforce a 30s timeout for sessions that fail to start |
| plan-k8s-integration.md:1217-1224 | "Staleness detection: heartbeat to mclaude-heartbeats KV, key: {userId}/{projectId}" | agent.go:1016-1031, state.go:71-73 | IMPLEMENTED | heartbeatKVKey uses userId.projectId format |
| plan-state-schema.md:79-113 | "mclaude-sessions KV: key format {userId}.{projectId}.{sessionId}" | state.go:64-66 | IMPLEMENTED | sessionKVKey uses dot-separated format |
| plan-state-schema.md:143-148 | "mclaude-heartbeats KV: key format {userId}.{projectId}" | state.go:70-72 | IMPLEMENTED | heartbeatKVKey uses userId.projectId |
| plan-state-schema.md:86-88 | "SessionState: state field values idle | busy | error" | state.go:27-43, events.go:24-30 | GAP | Spec schema says state: 'idle | busy | error'; code defines StateIdle='idle', StateRunning='running', StateRequiresAction='requires_action', StateUpdating='updating'. The value 'busy' and 'error' from plan-state-schema.md are not used; code uses 'running' and 'requires_action'. plan-k8s-integration.md says idle/running/requires_action (matching code). plan-state-schema.md is inconsistent with plan-k8s-integration.md. |
| plan-state-schema.md:209-237 | "MCLAUDE_EVENTS stream: subjects mclaude.*.*.events.* (3 wildcards)" | agent.go:99-108 | GAP | Code creates stream with subjects []string{"mclaude.*.*.events.*"} which is 3 wildcards. plan-state-schema.md defines pattern as mclaude.{userId}.{projectId}.events.{sessionId} (no location segment). But plan-k8s-integration.md shows mclaude.{userId}.{location}.{projectId}.events.{sessionId} (4 segments). Code uses 3-segment pattern matching state schema. |
| plan-state-schema.md:229-244 | "MCLAUDE_API stream: subjects mclaude.*.*.api.sessions.>" | agent.go:112-121 | GAP | Code creates MCLAUDE_API with subjects mclaude.*.*.api.sessions.> (3-segment). plan-k8s-integration.md defines 4-segment subjects including {location}. Same inconsistency as EVENTS stream. |
| plan-k8s-integration.md:460-461 | "Projects state KV: gitUrl, status, sessionCount, worktrees, createdAt, lastActiveAt" | state.go:103-113 | IMPLEMENTED | ProjectState struct matches |
| plan-k8s-integration.md:261 | "Caches capabilities from init event in NATS KV, refreshes on reload_plugins" | session.go:281-301 | PARTIAL | init event updates capabilities in KV. But reload_plugins control request handling: the code broadcasts all control_request subtypes to stdin but doesn't specifically refresh capabilities from the new init event that reload_plugins would generate. |
| plan-k8s-integration.md:262 | "Spawns terminal (PTY) sessions via creack/pty, routes raw I/O through NATS" | terminal.go:1-133 | IMPLEMENTED | Uses creack/pty |
| plan-github-oauth.md:34 | "gh and glab baked into session image as system dependencies. Not via Nix — /nix/ PVC mount hides image-layer packages." | Dockerfile:9-17 | IMPLEMENTED | Both installed in base image layer |
| plan-github-oauth.md:35 | "PVC-backed ~/.config/: Symlink /data/.config/ → ~/.config/ so gh auth login and glab auth login survive pod restarts" | gitcreds.go:324-346 | IMPLEMENTED | symlinkPVCConfig() implements this |
| plan-github-oauth.md:36 | "Config merge strategy: Merge, not overwrite. Session-agent adds managed tokens without removing entries from manual gh auth login." | gitcreds.go:121-186 | IMPLEMENTED | MergeGHHostsYAML preserves existing entries |
| plan-k8s-integration.md:380-394 | "Recovery after ungraceful termination: Set all session KV entries to state: restarting, clear pendingControls, publish session_restarting lifecycle events" | agent.go:171-256 | GAP | recoverSessions() clears pendingControls and sets state to idle (not 'restarting'). It publishes 'session_resumed' not 'session_restarting'. The spec explicitly says step 2 = 'Set all session KV entries to state: "restarting"' and step 3 = 'Publish session_restarting lifecycle events'. These are missing from the recovery path (they exist in handleRestart but not in recoverSessions). |
| plan-k8s-integration.md:390-392 | "Recovery step 7: Sessions that fail to start within 30s: mark state: 'failed', publish session_failed" | agent.go:171-256 | GAP | No 30-second timeout is implemented in recoverSessions(). Sessions that fail to start during recovery just get a log.Warn and are skipped; they are not marked failed in KV and no session_failed lifecycle event is published for them. |
| plan-k8s-integration.md:340-350 | "Graceful shutdown: For each active Claude process: Send interrupt control_request to stdin, Wait up to 10s for process exit, SIGKILL if still running" | agent.go:418-498 | GAP | gracefulShutdown() does NOT call sess.stopAndWait() for running sessions. It writes 'updating' state and then polls for idle/updating, but it does not actually send an interrupt to Claude processes or kill them. Sessions left running are not stopped. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:19-50 | INFRA | main() setup: log level parsing, health/readiness probe mode (--health, --ready flags) are spec'd in plan-k8s-integration.md health probes section |
| main.go:51-143 | INFRA | CLI flag parsing, daemon mode dispatch, observability setup — all infrastructure for spec'd behavior |
| main.go:144-198 | INFRA | Standalone mode: credential setup, InitRepo, NewAgent, Run — implements spec'd session-agent startup sequence |
| main.go:200-208 | INFRA | natsConnect helper — necessary infrastructure for NATS connection |
| gitcreds.go:636-659 | INFRA | RunGitOpWithCredsRefresh — helper for future worktree git ops with cred refresh; referenced by spec "Before each git operation" section |
| gitcreds.go:661-683 | INFRA | runCmd and equalBytes helpers — necessary infrastructure for spec'd commands |
| agent.go:1-65 | INFRA | Agent struct, constants — necessary for spec'd session management |
| agent.go:71-148 | INFRA | NewAgent, JetStream setup, stream creation — spec'd in "Bucket initialization" and agent startup sections |
| agent.go:349-359 | INFRA | jsToNatsMsg — adapter bridging JetStream to NATS message type; necessary infrastructure |
| agent.go:527-538 | INFRA | publishAPIError — error response mechanism for API operations; needed infrastructure |
| agent.go:540-545 | UNSPEC'd | defaultDevHarnessAllowlist — hardcoded list of tools for strict-allowlist sessions. plan-k8s-integration.md doesn't specify this default; it's only referenced by the quota-aware-scheduling feature |
| agent.go:1148-1162 | INFRA | reply() method — NATS reply helper for request/reply pattern |
| session.go:379-408 | INFRA | shouldAutoApprove, buildAutoApproveResponse — permission policy auto-approve infrastructure for spec'd permissionPolicy feature |
| session.go:414-430 | INFRA | truncateEventIfNeeded — spec says events >8MB are truncated with truncated:true flag; IMPLEMENTED |
| session.go:453-473 | INFRA | stop, waitDone helpers on Session — internal lifecycle helpers |
| state.go:74-101 | INFRA | addPendingControl, removePendingControl, clearPendingControlsForResume, accumulateUsage — state manipulation helpers for spec'd behaviors |
| state.go:115-155 | INFRA | QuotaStatus, QuotaMonitorConfig, JobEntry types — defined for quota-aware-scheduling spec (plan-quota-aware-scheduling.md) |
| worktree.go:30-37 | INFRA | worktreeExists helper — used by agent for worktree collision detection |
| events.go:1-103 | INFRA | Event type constants and struct definitions — necessary for spec'd event routing |
| debug.go:1-148 | INFRA | DebugServer — implements spec'd unix socket debug attach |
| terminal.go:1-133 | INFRA | TerminalSession, startTerminal, NATSTermPubSub — implements spec'd PTY terminal sessions |
| metrics.go:1-192 | INFRA | Prometheus metrics + OTEL tracing helpers — spec'd in Observability section of plan-k8s-integration.md |
| daemon.go:1-473 | INFRA | Daemon, laptop mode, JWT refresh, laptop KV heartbeat — implements spec'd laptop daemon mode |
| daemon_jobs.go:1-903 | INFRA | Job queue dispatcher, quota publisher, lifecycle subscriber, jobs HTTP server — implements plan-quota-aware-scheduling.md |
| quota_monitor.go:1-219 | INFRA | QuotaMonitor — implements per-session quota threshold monitoring per plan-quota-aware-scheduling.md |
| worktree.go:1-37 | INFRA | SlugifyBranch and worktreeExists — implements spec'd branch slugification |

### Summary

- Implemented: 35
- Gap: 5
- Partial: 3
- Infra: 24
- Unspec'd: 1
- Dead: 0
