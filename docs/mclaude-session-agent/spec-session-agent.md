# Spec: Session Agent

## Role

The session agent is a per-project process supervisor that manages **multiple concurrent Claude Code sessions** within a single project. One Agent instance owns all sessions for a `(userId, hostSlug, projectId)` triple, holding them in an in-memory session map. It spawns and manages Claude Code child processes (one per session), bridges their stream-json I/O to NATS, maintains per-session state in NATS KV, manages git worktrees, handles permission policies, provides PTY-based terminal sessions, and exposes debug unix sockets for CLI attach. It operates in two modes: as a standalone per-project agent (K8s pod or single-project laptop) or as a laptop daemon that spawns one child agent per project and runs the job queue dispatcher.

Per ADR-0035, every NATS subject the agent uses is host-scoped: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.…`. The host slug is required configuration — sourced from the `HOST_SLUG` env var in K8s pods and from the `--host` flag (or `~/.mclaude/active-host` symlink) on BYOH machines. Absence is fatal at startup.

## Deployment

### Standalone Mode (K8s)

Runs as a single-container Deployment per project in the `mclaude-{userId}` namespace. The pod entrypoint (`entrypoint.sh`) seeds the Nix store from a bootstrap tarball, consumes secrets from the mounted `user-secrets` Secret, seeds Claude Code config from the `user-config` ConfigMap, symlinks JSONL history to the project PVC, disables onboarding and permission dialogs, and then execs the `session-agent` binary with `--mode k8s`.

The container image is Alpine-based with Node.js 22, Claude Code (native binary), git, gh, glab, zsh, and Nix. A `pkg` shim translates `apt`/`brew` commands to `nix profile` operations. Two PVCs are mounted: the project PVC at `/data` (bare repo, worktrees, JSONL persistence, shared memory) and the Nix PVC at `/nix` (shared Nix store).

The Go binary runs credential helper setup and initial repo clone/init before entering the NATS session lifecycle.

### Pod Structure (Multi-Container)

Each project pod contains:

| Container | Image | Role |
|-----------|-------|------|
| `session-agent` | `mclaude-session-agent` | Primary — Claude process supervisor |
| `config-sync` | `mclaude-config-sync` | Sidecar — watches `~/.claude/settings.json` and `CLAUDE.md` via inotify, patches `user-config` ConfigMap on change. Image includes inotify-tools, kubectl, jq. |
| `dockerd-rootless` (optional) | `docker:dind-rootless` | Sidecar — per-project Docker daemon, enabled via project flag. Socket at `/var/run/docker.sock`. |

### Auto-Memory Sharing

Claude Code stores auto-memories per working directory at `~/.claude/projects/{encoded-cwd}/memory/`. Different worktrees have different cwds, creating separate memories by default. The entrypoint runs a background loop (every 5s) that symlinks each worktree's memory directory to a single shared location on the PVC (`/data/shared-memory/`), ensuring memories (feedback, project context) are shared across all branches.

### Managed Platform Config

**CLAUDE.md three-tier system:**

| Tier | Location | Controlled by | User override? |
|------|----------|--------------|----------------|
| Global (managed policy) | `/etc/claude-code/CLAUDE.md` | Platform (baked into session image) | No |
| User | `~/.claude/CLAUDE.md` | User (synced via ConfigMap + config-sync sidecar) | Yes |
| Project | `{worktree}/CLAUDE.md` | Repo (committed to git) | Yes |

**Guard hooks** at `/etc/claude-code/hooks/guard.sh` enforce platform constraints at the Bash tool execution level:
- Block `git checkout` / `git switch` (platform manages worktrees)
- Block `apt install` / `apt-get install` (use `pkg` shim instead)
- Block modification of `/etc/claude-code/` (managed platform config)
- Block `rm -rf` on critical paths (`/data/repo`, `/nix`, `/etc`)

### Registry Mirror System

Enterprise deployments configure package managers to pull from internal mirrors. The session image includes platform hooks that read from a `mirrors.json` file (mounted from a ConfigMap). If the file doesn't exist (personal laptop), hooks skip — tools use public defaults.

Supported mirror types: `npm` (→ `.npmrc`), `pypi` (→ `pip.conf`), `go` (→ `GOPROXY`), `nix` (→ `nix.conf`). Each entry supports `auth` (K8s Secret ref) and `tls` (corporate CA bundle).

### JSONL Cleanup Job

A daily cleanup job (cron or entrypoint background goroutine) deletes JSONL files older than 90 days from `/data/projects/` and purges session files for sessions not present in `mclaude-sessions` KV.

### `/upgrade-claude` Skill

Manages Claude Code version upgrades. Fetches changelog between current and target version, analyzes for breaking changes (stream-json events, CLI flags, control protocol, hooks), proposes patches if breaking changes found, and updates the pinned version in Dockerfile on an `upgrade/claude-{version}` branch with a PR.

### Daemon Mode (BYOH machine)

Runs as a long-lived process on the user's machine (laptop, desktop, VM) with `--daemon --host <hslug>`. The daemon spawns one supervised child agent per project, manages JWT credential refresh, publishes quota status, dispatches scheduled jobs, and exposes a local HTTP API for job management.

Liveness is reported by the hub NATS via `$SYS.ACCOUNT.*.CONNECT/DISCONNECT` events that the control-plane subscribes to. The daemon does **not** publish periodic heartbeats and does **not** write to `mclaude-hosts` or to any removed bucket (`mclaude-laptops`, `mclaude-heartbeats`).

### Configuration

| Knob | Source | Description |
|------|--------|-------------|
| `--nats-url` / `NATS_URL` | Flag or env | NATS server URL (default: `nats://localhost:4222`) |
| `--nats-creds` / `NATS_CREDS_FILE` | Flag or env | Path to NATS credentials file |
| `--user-id` / `USER_ID` | Flag or env | User UUID (required) |
| `--project-id` / `PROJECT_ID` | Flag or env | Project UUID (required in standalone) |
| `--user-slug` / `USER_SLUG` | Flag or env | User typed slug per ADR-0024 |
| `--host` / `HOST_SLUG` | Flag or env | **Required.** Host slug per ADR-0035 (`mclaude.users.{uslug}.hosts.{hslug}.…`). K8s pods read `HOST_SLUG` injected by `mclaude-controller-k8s`'s `buildPodTemplate`. BYOH daemons read `--host` (defaulting to the target of `~/.mclaude/active-host` if unset). Hard fail at startup on absence. |
| `--project-slug` / `PROJECT_SLUG` | Flag or env | Project typed slug per ADR-0024 |
| `--claude-path` / `CLAUDE_PATH` | Flag or env | Path to Claude binary (default: `claude`) |
| `--data-dir` | Flag | Project PVC mount (default: `/data`) |
| `--mode` | Flag | `k8s` or `standalone` (default: `standalone`) |
| `--daemon` | Flag | Enable BYOH daemon mode |
| `--refresh-url` / `REFRESH_URL` | Flag or env | JWT refresh endpoint URL (daemon only) |
| `LOG_LEVEL` | Env | Zerolog level (default: `info`) |
| `HOSTNAME` / `--hostname` | Flag or env | **Dead code** — daemon mode only. Stored in `DaemonConfig.Hostname` but never accessed. Remnant of the removed collision-detection flow. |
| `MACHINE_ID` / `--machine-id` | Flag or env | **Dead code** — daemon mode only. Stored in `DaemonConfig.MachineID` but never accessed. Remnant of the removed collision-detection flow. |
| `METRICS_ADDR` | Env | Prometheus metrics listen address (default: `:9091`) |
| `GIT_URL` | Env | Git remote URL for initial clone (K8s mode) |
| `GIT_IDENTITY_ID` | Env | OAuth connection ID for credential identity selection (K8s mode) |
| `CLAUDE_CODE_TMPDIR` | Env | Base directory for Claude Code temp files (K8s mode). Set to `/data/claude-tmp` and backed by the `project-data` PVC (`SubPath: claude-tmp`). Enables shell output files to survive pod restarts. When unset (BYOH/daemon mode), in-flight shell tracking is disabled. |

### Health and Readiness

The binary supports two probe commands for K8s integration:
- `--health` — checks process alive and NATS connection status. Exits 0 if both OK.
- `--ready` — attempts a NATS connection and verifies Claude Code can be spawned. Exits 0 on success.

## Interfaces

### NATS JetStream Streams

The agent idempotently creates or updates two JetStream streams on startup:

- **MCLAUDE_EVENTS** -- The agent is authoritative for this stream. Publishes raw stream-json events from Claude Code stdout. See `spec-state-schema.md` section "JetStream Streams / MCLAUDE_EVENTS" for stream config and subject pattern.

- **MCLAUDE_API** -- Captures session API commands for at-least-once delivery. See `spec-state-schema.md` section "JetStream Streams / MCLAUDE_API" for stream config and subject pattern.

Note: The agent does **not** currently create `MCLAUDE_LIFECYCLE` on startup, despite the spec-state-schema.md listing it as "Created by: session-agent". Lifecycle events are published to core NATS subjects but are not persisted without the stream. The test harness creates it for integration tests. This is a known gap.

The agent creates two durable pull consumers on MCLAUDE_API:

- **Command consumer** (`sa-cmd-{uslug}-{pslug}`) -- Filters `create`, `delete`, `input`, and `restart` subjects. Explicit ack, 60s ack wait, max 5 deliveries. Fetch loop: batch 10, FetchMaxWait 5s.
- **Control consumer** (`sa-ctl-{uslug}-{pslug}`) -- Filters `control` subject. Same ack policy and fetch parameters.

The fetch loop delivers `jetstream.Msg`, not `*nats.Msg`. A `jsToNatsMsg` adapter wraps the JetStream message before dispatching to handlers (which accept `*nats.Msg`). After a successful handler return, the message is acked; on handler error or after MaxDeliver exhaustion the message is dropped (indicates a handler bug — not retried by the application layer).

### NATS KV Buckets (Read/Write)

- **mclaude-sessions** (write) -- Writes session state on creation, every state change, usage accumulation, and compaction boundary. See `spec-state-schema.md` section "NATS KV Buckets / mclaude-sessions".

### NATS KV Buckets (Read-Only)

- **mclaude-projects** (read) -- Read by daemon for job dispatch context.
- **mclaude-sessions** (read) -- Read by daemon for startup recovery and session state polling.
- **mclaude-job-queue** (read/write, daemon only) -- Job entries for the scheduled job dispatcher. See `spec-state-schema.md` section "NATS KV Buckets / mclaude-job-queue".

The agent does not write to `mclaude-hosts` (single-writer is control-plane, sourced from `$SYS` events). The previous `mclaude-laptops` and `mclaude-heartbeats` buckets are removed entirely per ADR-0035.

### NATS Subjects (Publish)

All publishes use the host-scoped pattern per ADR-0035: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.…`.

- **Lifecycle events** to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}` -- Published on session creation, stop, resume, restart, upgrade, failure, permission denial, quota interruption, and job completion. See `spec-state-schema.md` section "Lifecycle Event Payloads".
- **Stream-json events** to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events.{sslug}` -- Raw Claude Code stdout lines, forwarded to the MCLAUDE_EVENTS stream. Note: the code has a fallback path that constructs event subjects using UUIDs (`userID`, `projectID`, `sessionID`) when slug fields are empty. This should not occur in normal operation but provides backward compatibility during transitions.
- **API error events** to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events._api` -- Published when a create/delete/restart handler encounters an error. Payload: `{type: "api_error", request_id: string, operation: "create" | "delete" | "restart", error: string}`.
- **Quota status** to `mclaude.users.{uslug}.quota` (daemon only) -- Published every 60 seconds from Anthropic API polling. The quota subject is user-scoped (no host segment) because quota is an account-wide signal. See `spec-state-schema.md` section "Quota Status".

### NATS Subjects (Subscribe)

All API subscriptions are at host-and-project scope — the Agent subscribes once per `(uslug, hslug, pslug)` triple and demultiplexes to the correct session internally. Messages include a `sessionId` field (or `sessionSlug`) that the Agent uses to route to the right `Session` in its map. Control requests without a specific session target are broadcast to all active sessions.

- **Session API commands** via JetStream pull consumers on MCLAUDE_API (create, delete, input, restart, control). Filter subjects are host-scoped: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.{create,delete,input,restart,control}`.
- **Terminal API** via core NATS subscriptions on `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.{create,delete,resize}` -- Latency-sensitive; stays on core NATS.
- **Lifecycle events** (daemon only) via core NATS wildcard on `mclaude.users.{uslug}.hosts.{hslug}.projects.*.lifecycle.*` -- Updates job queue KV on terminal job events.
- **Quota status** (per-session QuotaMonitor) via core NATS on `mclaude.users.{uslug}.quota`.

The daemon does **not** subscribe to project-creation requests; project provisioning per ADR-0035 is handled by `mclaude-controller-local` (BYOH) or `mclaude-controller-k8s` (cluster), each subscribing to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` (or the user-level wildcard variant for the cluster controller).

### Unix Sockets

Each session exposes a debug socket at `/tmp/mclaude-session-{sessionId}.sock`. Protocol is NDJSON: server broadcasts Claude Code stdout events to connected clients; clients send stream-json messages that are forwarded to Claude Code stdin. Publishes `debug_attached` and `debug_detached` lifecycle events on client connect/disconnect.

### HTTP (Daemon Only)

Loopback-only HTTP server on `localhost:8378`:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/jobs` | POST | Submit a new scheduled job |
| `/jobs` | GET | List all jobs for the user |
| `/jobs/{id}` | GET | Get a single job |
| `/jobs/{id}` | DELETE | Cancel a job (stops session if running) |
| `/jobs/projects` | GET | List projects from KV |

### Prometheus Metrics

Exposed at `/metrics` on the metrics address:

| Metric | Type | Description |
|--------|------|-------------|
| `mclaude_active_sessions` | Gauge | Currently active Claude Code sessions |
| `mclaude_events_published_total` | Counter (by event_type) | Stream-json events published to NATS |
| `mclaude_nats_reconnects_total` | Counter | NATS reconnection count |
| `mclaude_claude_restarts_total` | Counter | Claude process restart count |

OpenTelemetry trace spans are emitted for NATS publish, KV write, session lifecycle, and Claude process spawn operations. W3C traceparent headers are propagated via NATS message headers.

## Internal Behavior

### Session State Machine

Sessions track Claude Code's reported state plus two agent-managed states:

```
            ┌──────────┐
   create → │   idle   │ ← resume / restart / turn complete
            └────┬─────┘
                 │ user message
                 v
            ┌──────────┐
            │ running  │ ← tool approved / denied
            └────┬─────┘
                 │ permission prompt
                 v
            ┌───────────────┐
            │requires_action│ → user approves/denies → running
            └───────────────┘

Agent-managed states (not from Claude Code):
  updating   — pod is draining for graceful upgrade (written to KV only)
  restarting — transient during restart handler
```

State transitions flow from Claude Code's `session_state_changed` system events. The agent updates in-memory state and flushes to KV on every transition, except during graceful shutdown when KV writes are suppressed to preserve the `updating` banner.

### Session Lifecycle

**Creation:**
1. Receive `sessions.create` from JetStream consumer.
2. Generate session UUID and derive session slug.
3. Derive branch name (from request, slugified session name, or `session-{shortId}`).
4. Check for worktree collision; error if branch is in use and `joinWorktree` is false.
5. Create git worktree via `git worktree add` (refreshes credentials first if managed).
6. Write initial `SessionState` to KV with state `idle`.
7. Apply permission policy and allowed-tools set from request.
8. Wire quota monitor if `quotaMonitor` config is present in the request.
9. Start debug unix socket.
10. Spawn Claude Code process with `--print --verbose --output-format stream-json --input-format stream-json --include-partial-messages --replay-user-messages --session-id {id}`.
11. Publish `session_created` lifecycle event.

**Resumption (on pod restart):**
1. Watch all keys in `mclaude-sessions` KV for initial values.
2. Set all matching session KV entries to `state: "restarting"`, clear `pendingControls`. **Known bug:** `clearPendingControlsForResume()` sets state to `"idle"` instead of `"restarting"` — the SPA never shows the "Restarting..." indicator during recovery. Root cause: `StateRestarting` constant is not defined (only 4 of 9 state constants exist in code).
3. Publish `session_restarting` lifecycle event per session.
4. For each session belonging to this project: resume the Claude process with `--resume {id}`.
5. On `init` event: update KV with fresh state.
6. Sessions in `updating` state are resumed but their KV entry stays as `updating` until all JetStream consumers are attached, then cleared to `idle`.
7. Publish `session_resumed` lifecycle event per session.
8. Sessions that fail to start within 30s: mark `state: "failed"`, publish `session_failed` lifecycle event. **Known gap:** the code's 30s timer fires but does nothing (comment: "Don't kill the process; it's idle but valid") — sessions that don't receive an `init` event within 30s remain in their current state indefinitely rather than being marked failed.

**Deletion:**
1. Remove session from in-memory map.
2. Send interrupt to Claude stdin; wait up to 10 seconds, then SIGKILL.
3. Remove git worktree if this was the last session using the branch.
4. Delete session key from KV.
5. Publish `session_stopped` lifecycle event.

**Restart:**
1. Publish `session_restarting` lifecycle event.
2. Stop existing process (interrupt + timeout + kill).
3. Clear pending controls, update extra flags if provided.
4. Respawn with `--resume`.
5. Publish `session_resumed` lifecycle event.

### Event Routing

The stdout router goroutine reads NDJSON lines from Claude Code's stdout (using a 16 MB scanner buffer to handle large events before NATS-level truncation) and for each line:
1. **In-flight background agent tracking:** Increments `inFlightBackgroundAgents` on `assistant` events with an `Agent` tool_use where `input.run_in_background == true`. Decrements (floored at 0) on top-level `user` events with `origin.kind == "task-notification"`. Used by the drain predicate in graceful shutdown.
2. **In-flight background shell tracking (K8s mode; two-phase):**
   - **Phase 1 — Pending:** On `assistant` event with a `Bash` tool_use where `input.run_in_background == true`, adds a `pendingShell{toolUseId, command, startedAt}` entry to `sess.pendingShells` (keyed by `tool_use.id`).
   - **Phase 2 — Promoted:** On `user` event with a `tool_result` whose `tool_use_id` matches a pending entry, extracts `backgroundTaskId` from the result text (pattern: `"Command was manually backgrounded with ID: (\S+)"`), constructs `outputFilePath` as `{CLAUDE_CODE_TMPDIR}/claude-{uid}/{sanitizePath(cwd)}/{sessionId}/tasks/{taskId}.output`, and promotes the entry to `sess.inFlightShells map[string]*inFlightShell` (keyed by toolUseId). Fields: `{toolUseId, taskId, command, outputFilePath, startedAt}`. `sanitizePath` replaces every character outside `[a-zA-Z0-9]` with `-`; for paths > 200 chars appends a wyhash/djb2 suffix (in practice CWDs are under 200 chars so the hash branch is never reached).
   - **Removal:** On `user` event with `origin.kind == "task-notification"` referencing the shell's toolUseId (shell completed naturally).
   - Shell tracking is disabled when `CLAUDE_CODE_TMPDIR` is not set (daemon/BYOH mode).
3. Truncates events exceeding the 8 MB NATS payload limit (removes `content` field, adds `truncated: true`).
4. Publishes to the session's NATS events subject. User input messages are also published to the events stream so that replaying clients see the full conversation.
5. Notifies the compact-boundary callback to update `replayFromSeq`.
6. Notifies the quota monitor raw output callback.
7. Broadcasts to connected debug clients.
8. Processes side effects (state changes, init, permission requests, usage accumulation). **Known bug:** `handleSideEffect` only accumulates `inputTokens`, `outputTokens`, and `costUsd` from `result` events — `cacheReadTokens` and `cacheWriteTokens` are never accumulated (always 0 in KV). An `accumulateUsage()` helper that handles all 5 fields exists in `state.go` but is dead code (never called).

The stdin serializer goroutine drains the stdin channel sequentially to prevent NDJSON line interleaving.

### Permission Policy

Four policies control how `control_request` events (tool-use permission prompts) are handled:

| Policy | Behavior |
|--------|----------|
| `managed` (default) | Forward to NATS client for human decision |
| `auto` | Auto-approve all tools |
| `allowlist` | Auto-approve listed tools; forward others to client |
| `strict-allowlist` | Auto-approve listed tools; auto-deny all others |

When no explicit allowed-tools list is provided for `allowlist` or `strict-allowlist`, a default set is applied: Read, Write, Edit, Glob, Grep, Bash, Agent, TaskCreate, TaskUpdate, TaskGet, TaskList, TaskOutput, and TaskStop.

The `strict-allowlist` policy invokes an `onStrictDeny` callback on denial, which publishes a `session_permission_denied` lifecycle event and signals the quota monitor.

### Credential Management (K8s Mode)

On startup in K8s mode:
1. Symlink PVC config directory (`/data/.config`) to `~/.config`.
2. Read managed credentials from the `user-secrets` Secret mount (`gh-hosts.yml`, `glab-config.yml`).
3. Resolve `GIT_IDENTITY_ID` to a username and host via `conn-{id}-username` Secret keys.
4. Convert multi-account `gh-hosts.yml` to old single-account format with identity-selected token.
5. Merge managed tokens into PVC-persisted CLI configs (managed wins per host).
6. Register credential helpers via `gh auth setup-git` and `glab auth setup-git`.
7. Re-run merge and setup before each git operation if managed content has changed.

Initial repo setup: clone bare repo from `GIT_URL` (or init scratch repo with empty commit if no URL), normalize SCP-style git URLs to HTTPS for hosts with registered credential helpers.

### Worktree Management

Sessions are isolated via git worktrees under `{dataDir}/worktrees/{branchSlug}`. The agent:
- Creates worktrees with `git worktree add` on session creation.
- Refreshes credentials before each git worktree operation via `CredentialManager.RefreshIfChanged(gitIdentityID)` — selects the correct OAuth connection token based on the `GIT_IDENTITY_ID` env var.
- Checks for branch collision before creating (errors unless `joinWorktree` is true).
- Removes worktrees with `git worktree remove --force` when the last session using a branch is deleted.

### Compact Boundary Tracking

When a `compact_boundary` event is published, the agent queries the MCLAUDE_EVENTS stream for its last sequence number and writes it to the session's KV entry as `replayFromSeq`. New SPA subscribers use this to skip already-compacted history.

### Quota Monitoring

One `QuotaMonitor` goroutine per session, created when the `sessions.create` request includes a `quotaMonitor` config (used by scheduled jobs). The monitor:
1. Subscribes to `mclaude.users.{uslug}.quota` for quota status updates.
2. When 5-hour utilization reaches the threshold, sends a `QUOTA_THRESHOLD_REACHED` message to Claude's stdin requesting graceful stop.
3. Starts a 30-minute hard-interrupt timer as a backstop.
4. Scans assistant events for the `SESSION_JOB_COMPLETE:{prUrl}` marker.
5. On session exit, publishes the appropriate lifecycle event based on exit reason (completion, quota interruption, permission denial, or failure).

### Graceful Shutdown (SIGTERM)

The shutdown sequence preserves in-progress work and enables zero-downtime upgrades:
1. Write `state: "updating"` to KV for all sessions (SPA displays upgrade banner). Set `shutdownPending` flag to suppress further KV state flushes.
2. Cancel the command consumer (new commands queue in JetStream for the replacement pod).
3. Drain core NATS subscriptions (terminal API).
4. Keep the control consumer running (interrupts and permission responses still work).
5. Poll every 1 second (no wall-clock timeout — indefinite wait; `terminationGracePeriodSeconds: 86400` from values.yaml is K8s's last-resort backstop, not a policy limit): wait for all sessions to reach `idle` state with zero in-flight background agents. Auto-interrupt sessions stuck in `requires_action`.
6. Shell-killed notifications (K8s mode only, when `CLAUDE_CODE_TMPDIR` is set): for each session, for each entry in `inFlightShells`, construct a `<task-notification>` XML message with child elements `<task-id>`, `<tool-use-id>`, `<output-file>`, `<status>killed</status>`, and `<summary>` populated from the `inFlightShell` entry. Publish it as a session-input payload `{"session_id": "<id>", "type": "user", "message": {"role": "user", "content": "<xml>"}}` to the JetStream `api.sessions.input` subject. Messages queue in the durable cmd consumer for the replacement pod. (If the pod crashes before publishing, those notifications are lost — tolerated; Claude will notice unresolved shell tool_uses on the next BashOutput call.)
7. Cancel the control consumer.
8. Publish `session_upgrading` lifecycle event per session.
9. Exit.

### Daemon: JWT Refresh

The daemon checks its host JWT (`~/.mclaude/hosts/{hslug}/nats.creds`) TTL every 60 seconds. When remaining TTL falls below 15 minutes, it POSTs to the refresh URL with the current JWT and writes the new JWT back to the credentials file.

### Daemon: Job Dispatcher

The dispatcher watches `mclaude-job-queue` KV and quota updates:
- **Startup recovery:** Resets `starting` jobs to `queued`, checks if `running` jobs still have sessions, unpauses `paused` jobs whose resume time has passed.
- **Dispatch:** For each `queued` job, if quota allows (utilization below threshold), sends `sessions.create` with `strict-allowlist` policy and quota monitor config, waits for session to reach `idle` (30s poll), then sends the dev-harness prompt.
- **Quota preemption:** When any running job's threshold is exceeded, sends graceful stop messages to exceeded jobs in ascending priority order. Publishes `session_job_paused` lifecycle. Marks jobs as `paused`.
- **Quota recovery:** When utilization drops below all running thresholds, unpauses paused jobs in descending priority order.
- **Job status transitions:** The lifecycle subscriber updates job status in KV on `session_job_complete`, `session_quota_interrupted`, `session_permission_denied`, and `session_job_failed` events.

### Daemon: Liveness

Liveness is signalled by the NATS connection itself. When the daemon connects to hub NATS, the hub publishes `$SYS.ACCOUNT.{accountKey}.CONNECT`; control-plane subscribes and updates `hosts.last_seen_at` and the `mclaude-hosts` KV entry. On disconnect, control-plane sets `online=false`. There is no periodic heartbeat publish; the previous `mclaude-laptops` collision-detection flow is removed (slug uniqueness is enforced server-side at registration time).

### Terminal Sessions

Terminal sessions spawn a PTY shell and bridge I/O through core NATS subjects. The agent subscribes to terminal input and publishes terminal output in 4 KB chunks. Resize events are forwarded to the PTY. On delete, the PTY is closed and the process is killed.

## Error Handling

| Failure | Behavior |
|---------|----------|
| NATS connection lost | NATS client auto-reconnects; state changes buffered in memory during outage and flushed on reconnect. Claude processes continue running. Reconnection counter incremented. |
| KV bucket not found on startup | Fatal: session agent exits (control-plane not started) |
| Session create — worktree collision | `api_error` event published to `events._api` (no NATS reply — JetStream messages have no Reply field) |
| Session create — git worktree add fails | `api_error` event published to `events._api` (no NATS reply — JetStream messages have no Reply field) |
| Session create — Claude process fails to start | `session_failed` lifecycle event published to `lifecycle.{sslug}` |
| Session delete — handler error | `api_error` event published to `events._api` with `operation: "delete"` |
| Session restart — handler error | `api_error` event published to `events._api` with `operation: "restart"` |
| Claude process crash (unexpected exit) | Session agent detects exit, publishes `session_failed` lifecycle event, auto-restarts with `--resume {sessionId}`. Increments `mclaude_claude_restarts_total` counter. |
| Git auth error during initial clone (K8s) | `session_failed` lifecycle event with `provider_auth_failed` reason published; agent exits |
| Credential helper setup fails | Logged as warning; continues (non-fatal, SSH key auth may still work) |
| Session delete — process does not stop within 10s | Process is SIGKILLed; deletion proceeds |
| Graceful shutdown — session stuck in requires_action | Auto-interrupted so the turn aborts to idle |
| JetStream message exhausts MaxDeliver (5 deliveries) | Message dropped. Indicates a persistent handler bug — not retried by the application. |
| Pod crash (no SIGTERM — SIGKILL, OOM, node failure) | In-flight shell tracking (`inFlightShells`, `pendingShells`) is lost with the process. Durable JetStream consumers redeliver unacked messages. Sessions resume from KV. Dangling background-shell tool_uses remain in the transcript — Claude notices the unknown shell-id when it attempts `BashOutput` and can adjust. |
| JetStream fetch error | Exponential backoff (100ms to 5s) with retry |
| Event exceeds 8 MB NATS payload | Content field stripped; `truncated: true` added |
| JWT refresh fails (daemon) | Logged as warning; children use current JWT until expiry |
| `HOST_SLUG` / `--host` not provided | Fatal: agent or daemon refuses to start with `FATAL: HOST_SLUG required (set via env or --host flag)` (no fallback). |
| Host JWT signed for the wrong host | Hub NATS auth rejects publishes/subscribes; the agent surfaces this as a NATS auth error and exits or refuses the failing operation. |
| Job dispatch — session never reaches idle | Job retried up to 3 times, then marked `failed` |
| Debug socket start fails | Logged as warning; CLI attach disabled but sessions function normally |

## Dependencies

| Dependency | Purpose |
|------------|---------|
| NATS server | Messaging, JetStream streams, KV buckets |
| Control-plane | Must have created KV buckets before agent starts |
| Claude Code binary | Spawned as child process for each session |
| git | Bare repo clone, worktree management, fetch |
| gh CLI | Credential helper for GitHub hosts |
| glab CLI | Credential helper for GitLab hosts |
| Nix | Package manager for user-installed tools (K8s mode; PVC-backed store) |
| Anthropic OAuth API | Quota polling via `api.anthropic.com/api/oauth/usage` (daemon mode) |
| `~/.claude/.credentials.json` | OAuth token for quota API (daemon mode) |
| user-secrets K8s Secret | NATS creds, OAuth token, git CLI configs, connection tokens (K8s mode) |
| user-config K8s ConfigMap | Claude Code seed settings and hooks (K8s mode) |
| Project PVC at `/data` | Bare repo, worktrees, JSONL persistence, config (K8s mode) |
| Nix PVC at `/nix` | Shared Nix store (K8s mode) |
