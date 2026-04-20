# Spec: Session Agent

## Role

The session agent is a per-project process supervisor that manages **multiple concurrent Claude Code sessions** within a single project. One Agent instance owns all sessions for a `(userId, projectId)` pair, holding them in an in-memory session map. It spawns and manages Claude Code child processes (one per session), bridges their stream-json I/O to NATS, maintains per-session state in NATS KV, manages git worktrees, handles permission policies, provides PTY-based terminal sessions, and exposes debug unix sockets for CLI attach. It operates in two modes: as a standalone per-project agent (K8s pod or single-project laptop) or as a laptop daemon that spawns one child agent per project and runs the job queue dispatcher.

## Deployment

### Standalone Mode (K8s)

Runs as a single-container Deployment per project in the `mclaude-{userId}` namespace. The pod entrypoint (`entrypoint.sh`) seeds the Nix store from a bootstrap tarball, consumes secrets from the mounted `user-secrets` Secret, seeds Claude Code config from the `user-config` ConfigMap, symlinks JSONL history to the project PVC, disables onboarding and permission dialogs, and then execs the `session-agent` binary with `--mode k8s`.

The container image is Alpine-based with Node.js 22, Claude Code (native binary), git, gh, glab, zsh, and Nix. A `pkg` shim translates `apt`/`brew` commands to `nix profile` operations. Two PVCs are mounted: the project PVC at `/data` (bare repo, worktrees, JSONL persistence, shared memory) and the Nix PVC at `/nix` (shared Nix store).

The Go binary runs credential helper setup and initial repo clone/init before entering the NATS session lifecycle.

### Daemon Mode (Laptop)

Runs as a long-lived process on the user's laptop with `--daemon`. The daemon spawns one supervised child agent per project, manages JWT credential refresh, writes laptop presence to KV, publishes quota status, dispatches scheduled jobs, and exposes a local HTTP API for job management.

### Configuration

| Knob | Source | Description |
|------|--------|-------------|
| `--nats-url` / `NATS_URL` | Flag or env | NATS server URL (default: `nats://localhost:4222`) |
| `--nats-creds` / `NATS_CREDS_FILE` | Flag or env | Path to NATS credentials file |
| `--user-id` / `USER_ID` | Flag or env | User UUID (required) |
| `--project-id` / `PROJECT_ID` | Flag or env | Project UUID (required in standalone) |
| `--user-slug` / `USER_SLUG` | Flag or env | User typed slug per ADR-0024 |
| `--project-slug` / `PROJECT_SLUG` | Flag or env | Project typed slug per ADR-0024 |
| `--claude-path` / `CLAUDE_PATH` | Flag or env | Path to Claude binary (default: `claude`) |
| `--data-dir` | Flag | Project PVC mount (default: `/data`) |
| `--mode` | Flag | `k8s` or `standalone` (default: `standalone`) |
| `--daemon` | Flag | Enable laptop daemon mode |
| `--hostname` / `HOSTNAME` | Flag or env | Hostname for collision detection (daemon only) |
| `--machine-id` / `MACHINE_ID` | Flag or env | Machine ID for collision detection (daemon only) |
| `--refresh-url` / `REFRESH_URL` | Flag or env | JWT refresh endpoint URL (daemon only) |
| `LOG_LEVEL` | Env | Zerolog level (default: `info`) |
| `METRICS_ADDR` | Env | Prometheus metrics listen address (default: `:9091`) |
| `GIT_URL` | Env | Git remote URL for initial clone (K8s mode) |
| `GIT_IDENTITY_ID` | Env | OAuth connection ID for credential identity selection (K8s mode) |

### Health and Readiness

The binary supports `--health` (immediate exit 0) and `--ready` (attempts a NATS connection, exits 0 on success) for K8s probe integration.

## Interfaces

### NATS JetStream Streams

The agent idempotently creates or updates two JetStream streams on startup:

- **MCLAUDE_EVENTS** -- The agent is authoritative for this stream. Publishes raw stream-json events from Claude Code stdout. See `spec-state-schema.md` section "JetStream Streams / MCLAUDE_EVENTS" for stream config and subject pattern.

- **MCLAUDE_API** -- Captures session API commands for at-least-once delivery. See `spec-state-schema.md` section "JetStream Streams / MCLAUDE_API" for stream config and subject pattern.

The agent creates two durable pull consumers on MCLAUDE_API:

- **Command consumer** (`sa-cmd-{uslug}-{pslug}`) -- Filters `create`, `delete`, `input`, and `restart` subjects. Explicit ack, 60s ack wait, max 5 deliveries.
- **Control consumer** (`sa-ctl-{uslug}-{pslug}`) -- Filters `control` subject. Same ack policy.

### NATS KV Buckets (Read/Write)

- **mclaude-sessions** (write) -- Writes session state on creation, every state change, usage accumulation, and compaction boundary. See `spec-state-schema.md` section "NATS KV Buckets / mclaude-sessions".
- **mclaude-heartbeats** (write) -- Writes project heartbeat every 30 seconds.

### NATS KV Buckets (Read-Only)

- **mclaude-projects** (read) -- Read by daemon for job dispatch context.
- **mclaude-sessions** (read) -- Read by daemon for startup recovery and session state polling.
- **mclaude-job-queue** (read/write, daemon only) -- Job entries for the scheduled job dispatcher. See `spec-state-schema.md` section "NATS KV Buckets / mclaude-job-queue".
- **mclaude-laptops** (read/write, daemon only) -- Laptop presence registration and hostname collision detection.

### NATS Subjects (Publish)

- **Lifecycle events** to `mclaude.users.{uslug}.projects.{pslug}.lifecycle.{sslug}` -- Published on session creation, stop, resume, restart, upgrade, failure, permission denial, quota interruption, and job completion. See `spec-state-schema.md` section "Lifecycle Event Payloads".
- **Stream-json events** to `mclaude.users.{uslug}.projects.{pslug}.events.{sslug}` -- Raw Claude Code stdout lines, forwarded to the MCLAUDE_EVENTS stream.
- **API error events** to `mclaude.users.{uslug}.projects.{pslug}.events._api` -- Published when a create/delete/restart handler encounters an error.
- **Quota status** to `mclaude.users.{uslug}.quota` (daemon only) -- Published every 60 seconds from Anthropic API polling. See `spec-state-schema.md` section "Quota Status".

### NATS Subjects (Subscribe)

All API subscriptions are at project scope — the Agent subscribes once per project and demultiplexes to the correct session internally. Messages include a `sessionId` field (or `sessionSlug`) that the Agent uses to route to the right `Session` in its map. Control requests without a specific session target are broadcast to all active sessions.

- **Session API commands** via JetStream pull consumers on MCLAUDE_API (create, delete, input, restart, control).
- **Terminal API** via core NATS subscriptions on `mclaude.users.{uslug}.projects.{pslug}.api.terminal.{create,delete,resize}` -- Latency-sensitive; stays on core NATS.
- **Project create** (daemon only) via core NATS on `mclaude.users.{uslug}.api.projects.create`.
- **Lifecycle events** (daemon only) via core NATS wildcard on `mclaude.users.{uslug}.projects.*.lifecycle.*` -- Updates job queue KV on terminal job events.
- **Quota status** (per-session QuotaMonitor) via core NATS on `mclaude.users.{uslug}.quota`.

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
2. For each session belonging to this project: clear pending controls, resume the Claude process with `--resume {id}`.
3. Sessions in `updating` state are resumed but their KV entry stays as `updating` until all JetStream consumers are attached, then cleared to `idle`.
4. Publish `session_resumed` lifecycle event per session.

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

The stdout router goroutine reads NDJSON lines from Claude Code's stdout and for each line:
1. Tracks in-flight background agents (increments on `Agent(run_in_background=true)` tool use, decrements on `task-notification` user events).
2. Truncates events exceeding the 8 MB NATS payload limit (removes `content` field, adds `truncated: true`).
3. Publishes to the session's NATS events subject.
4. Notifies the compact-boundary callback to update `replayFromSeq`.
5. Notifies the quota monitor raw output callback.
6. Broadcasts to connected debug clients.
7. Processes side effects (state changes, init, permission requests, usage accumulation).

The stdin serializer goroutine drains the stdin channel sequentially to prevent NDJSON line interleaving.

### Permission Policy

Four policies control how `control_request` events (tool-use permission prompts) are handled:

| Policy | Behavior |
|--------|----------|
| `managed` (default) | Forward to NATS client for human decision |
| `auto` | Auto-approve all tools |
| `allowlist` | Auto-approve listed tools; forward others to client |
| `strict-allowlist` | Auto-approve listed tools; auto-deny all others |

When no explicit allowed-tools list is provided for `allowlist` or `strict-allowlist`, a default set is applied: Read, Write, Edit, Glob, Grep, Bash, Agent, and Task* tools.

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
- Refreshes credentials before each git worktree operation.
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
5. Poll every 1 second: wait for all sessions to reach `idle` state with zero in-flight background agents. Auto-interrupt sessions stuck in `requires_action`.
6. Cancel the control consumer.
7. Publish `session_upgrading` lifecycle event per session.
8. Exit.

### Daemon: Child Process Supervision

The daemon spawns one child agent per project via `manageChild`, which restarts the child on crash with a 2-second delay. On daemon shutdown, all children receive SIGINT.

### Daemon: JWT Refresh

The daemon checks NATS credential file JWT TTL every 60 seconds. When remaining TTL falls below 15 minutes, it POSTs to the refresh URL with the current JWT and writes the new JWT back to the credentials file.

### Daemon: Job Dispatcher

The dispatcher watches `mclaude-job-queue` KV and quota updates:
- **Startup recovery:** Resets `starting` jobs to `queued`, checks if `running` jobs still have sessions, unpauses `paused` jobs whose resume time has passed.
- **Dispatch:** For each `queued` job, if quota allows (utilization below threshold), sends `sessions.create` with `strict-allowlist` policy and quota monitor config, waits for session to reach `idle` (30s poll), then sends the dev-harness prompt.
- **Quota preemption:** When any running job's threshold is exceeded, sends graceful stop messages to exceeded jobs in ascending priority order. Publishes `session_job_paused` lifecycle. Marks jobs as `paused`.
- **Quota recovery:** When utilization drops below all running thresholds, unpauses paused jobs in descending priority order.
- **Job status transitions:** The lifecycle subscriber updates job status in KV on `session_job_complete`, `session_quota_interrupted`, `session_permission_denied`, and `session_job_failed` events.

### Daemon: Laptop Presence

The daemon writes a laptop entry to `mclaude-laptops` KV on startup and refreshes every 12 hours. On startup, it checks for hostname collision (same hostname but different machine ID) and fails fast if detected.

### Terminal Sessions

Terminal sessions spawn a PTY shell and bridge I/O through core NATS subjects. The agent subscribes to terminal input and publishes terminal output in 4 KB chunks. Resize events are forwarded to the PTY. On delete, the PTY is closed and the process is killed.

## Error Handling

| Failure | Behavior |
|---------|----------|
| NATS connection lost | NATS client auto-reconnects; reconnection counter incremented |
| KV bucket not found on startup | Fatal: session agent exits (control-plane not started) |
| Session create — worktree collision | Error returned via NATS reply and published as `api_error` event |
| Session create — git worktree add fails | Error returned via NATS reply and published as `api_error` event |
| Session create — Claude process fails to start | `session_failed` lifecycle event published; error returned via NATS reply |
| Git auth error during initial clone (K8s) | `session_failed` lifecycle event with `provider_auth_failed` reason published; agent exits |
| Credential helper setup fails | Logged as warning; continues (non-fatal, SSH key auth may still work) |
| Session delete — process does not stop within 10s | Process is SIGKILLed; deletion proceeds |
| Graceful shutdown — session stuck in requires_action | Auto-interrupted so the turn aborts to idle |
| JetStream fetch error | Exponential backoff (100ms to 5s) with retry |
| Event exceeds 8 MB NATS payload | Content field stripped; `truncated: true` added |
| JWT refresh fails (daemon) | Logged as warning; children use current JWT until expiry |
| Hostname collision (daemon) | Fatal: daemon refuses to start |
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
