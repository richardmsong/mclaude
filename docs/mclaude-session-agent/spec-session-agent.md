# Spec: Session Agent

## Role

The session agent is a per-project process supervisor that manages **multiple concurrent CLI sessions** within a single project through a pluggable driver/adapter pattern (ADR-0005). One Agent instance owns all sessions for a `(userId, hostSlug, projectId)` triple, holding them in an in-memory session map. It spawns and manages CLI backend processes (one per session) via the `CLIDriver` interface, translates their native protocol events into canonical `SessionEvent` structs that flow through NATS, maintains per-session state in NATS KV, manages git worktrees, handles permission policies, provides PTY-based terminal sessions, and exposes debug unix sockets for CLI attach.

The agent supports multiple CLI backends: Claude Code, Factory Droid, Devin ACP, and a GenericTerminal fallback. Each backend is implemented as a driver in the `internal/drivers/` package. The `DriverRegistry` maps `CLIBackend` enum values to driver instances; session creation specifies the backend (defaulting to `claude_code`).

The agent operates as a standalone per-project process in all deployment modes: as a K8s pod (one per project) or as a subprocess of `mclaude-controller-local` on BYOH hosts (one per project, per ADR-0058).

> **Daemon mode deprecation (ADR-0058):** The previous `--daemon` mode (single-process, cross-project) is deprecated and will be removed. The `mclaude daemon` CLI command now launches `mclaude-controller-local` which spawns per-project agents. See the "Daemon Mode" section for the deprecation plan.

Per ADR-0054, every NATS subject the agent uses is host-scoped: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.…`. The host slug is required configuration — sourced from the `HOST_SLUG` env var in K8s pods and from the `--host` flag (or `~/.mclaude/active-host` symlink) on BYOH machines. Absence is fatal at startup.

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

A daily cleanup job (cron or entrypoint background goroutine) deletes JSONL files older than 90 days from `/data/projects/` and purges session files for sessions not present in `KV_mclaude-sessions-{uslug}`.

### `/upgrade-claude` Skill

Manages Claude Code version upgrades. Fetches changelog between current and target version, analyzes for breaking changes (stream-json events, CLI flags, control protocol, hooks), proposes patches if breaking changes found, and updates the pinned version in Dockerfile on an `upgrade/claude-{version}` branch with a PR.

### BYOH Mode (Per-Project Agent)

On BYOH hosts, the agent runs as a standalone per-project subprocess spawned by `mclaude-controller-local` (ADR-0058). Each project gets its own agent process with its own per-project JWT (ADR-0054). The host controller manages agent lifecycle; the agent manages session lifecycle within its project.

Liveness is reported by the hub NATS via `$SYS.ACCOUNT.*.CONNECT/DISCONNECT` events that the control-plane subscribes to. The agent does **not** publish periodic heartbeats and does **not** write to `mclaude-hosts` or to any removed bucket (`mclaude-laptops`, `mclaude-heartbeats`).

### Daemon Mode (Deprecated)

> **Deprecated per ADR-0058.** The `--daemon` flag is deprecated. The `mclaude daemon` CLI command now launches `mclaude-controller-local` instead of `mclaude-session-agent --daemon`. Existing launchd/systemd units pointing at `mclaude-session-agent --daemon` will stop working when ADR-0054's permission tightening is deployed (host JWTs lose JetStream access, agent JWTs become per-project scoped). No migration of in-flight sessions — sessions running under daemon mode at cut-over are terminated.
>
> **Phase 1 (parallel):** `mclaude daemon` redirects to `mclaude-controller-local`. Old `--daemon` still works.
> **Phase 2 (hard cut-over):** ADR-0054 deployment makes cross-project JetStream access impossible. All BYOH hosts must use controller + per-project agents.
> **Phase 3 (code removal):** Remove `daemon.go`, `daemon_jobs.go`, `--daemon` flag, and daemon-specific goroutines (`runJWTRefresh`, `runLifecycleSubscriber`, `runJobDispatcher`, `runJobsHTTP`). The `runQuotaPublisher` goroutine has been moved to the per-project standalone agent (`agent.go`). Per-project agents subscribe to `manage.designate-quota-publisher` on startup and start/stop `runQuotaPublisher` on CP designation signals (ADR-0044). The daemon copy of `runQuotaPublisher` in `daemon_jobs.go` is retained during the daemon-deprecated period.

### Configuration

| Knob | Source | Description |
|------|--------|-------------|
| `--nats-url` / `NATS_URL` | Flag or env | NATS server URL (default: `nats://localhost:4222`) |
| `--nats-creds` / `NATS_CREDS_FILE` | Flag or env | Path to NATS credentials file |
| `--user-id` / `USER_ID` | Flag or env | User UUID (required) |
| `--project-id` / `PROJECT_ID` | Flag or env | Project UUID (required in standalone) |
| `--user-slug` / `USER_SLUG` | Flag or env | User typed slug per ADR-0024 |
| `--host` / `HOST_SLUG` | Flag or env | **Required.** Host slug per ADR-0035 (`mclaude.users.{uslug}.hosts.{hslug}.…`). K8s pods read `HOST_SLUG` injected by `mclaude-controller-k8s`'s `buildPodTemplate`. BYOH daemons read `--host` (defaulting to the target of `~/.mclaude/active-host` if unset). If no valid slug is derivable from any source, the agent exits fatally with `FATAL: HOST_SLUG required`. |
| `--project-slug` / `PROJECT_SLUG` | Flag or env | Project typed slug per ADR-0024 |
| `--claude-path` / `CLAUDE_PATH` | Flag or env | Path to Claude binary (default: `claude`) |
| `--data-dir` | Flag | Project PVC mount (default: `/data`) |
| `--mode` | Flag | `k8s` or `standalone` (default: `standalone`) |
| `--daemon` | Flag | **(Deprecated — ADR-0058)** Enable BYOH daemon mode. Use `mclaude-controller-local` instead. |
| `--refresh-url` / `REFRESH_URL` | Flag or env | **(Deprecated — ADR-0058)** JWT refresh endpoint URL (daemon only). Agents now use HTTP challenge-response. |
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

### CLIDriver Interface (ADR-0005)

The session agent uses a pluggable driver/adapter pattern. Each CLI backend implements the `CLIDriver` interface:

```go
type CLIDriver interface {
    Backend() CLIBackend
    DisplayName() string
    Capabilities() CLICapabilities
    Launch(ctx context.Context, opts LaunchOptions) (*Process, error)
    Resume(ctx context.Context, sessionID string, opts LaunchOptions) (*Process, error)
    SendMessage(proc *Process, msg UserMessage) error
    SendPermissionResponse(proc *Process, requestID string, allow bool) error
    UpdateConfig(proc *Process, cfg SessionConfig) error
    Interrupt(proc *Process) error
    ReadEvents(proc *Process, out chan<- DriverEvent) error
}
```

`ReadEvents` blocks, reading the process stdout and emitting `DriverEvent` structs on the channel until the process exits. Each driver translates its native protocol into driver events that the session agent's event router processes.

### DriverRegistry

The `DriverRegistry` holds registered drivers, keyed by `CLIBackend` enum:

```go
registry := NewDriverRegistry()
registry.Register(NewClaudeCodeDriver())
registry.Register(NewDroidDriver())
registry.Register(NewDevinDriver())
registry.Register(NewGenericTerminalDriver())
```

Session create requests specify the `backend` field. The registry looks up the driver. If no backend specified, default to `claude_code`.

### Driver Implementations (`internal/drivers/`)

All driver implementations live in the `internal/drivers/` package:

| Driver | Backend | Protocol | Key behavior |
|--------|---------|----------|-------------|
| `ClaudeCodeDriver` | `claude_code` | NDJSON (stream-json) | Spawns `claude --print --verbose --output-format stream-json`. Maps `system.init`, `stream`, `assistant`, `result`, `sdk_control_request` etc. to canonical events. |
| `DroidDriver` | `droid` | JSON-RPC 2.0 | Spawns `droid exec --input-format stream-jsonrpc`. Maps 22 notification types. `StreamStateTracker` for `turn_complete` synthesis. Mission events pass through as `backend_specific`. |
| `DevinACPDriver` | `devin_acp` | ACP (JSON-RPC 2.0) | Spawns `devin acp`. Maps `session/update` notifications. State inferred (no explicit state events). Implements client-side capabilities (`fs/read_text_file`, `terminal/create`). |
| `GenericTerminalDriver` | `generic_terminal` | PTY raw I/O | Fallback for unrecognized CLIs. `hasEventStream: false` — SPA shows Terminal tab only. Heuristic idle detection. Raw PTY bytes published as `text_delta` with `encoding: "plain"` or `"base64"`. Uses `creack/pty`. |

### NATS JetStream Streams

Per ADR-0054, the agent **does not create streams** — the control-plane creates per-user streams on user registration. The agent connects to the pre-existing per-user session stream:

- **MCLAUDE_SESSIONS_{uslug}** — Consolidated per-user stream (ADR-0054). Captures all session activity under `mclaude.users.{uslug}.hosts.*.projects.*.sessions.>` — events, commands, lifecycle, input, config, control. Replaces the previous separate `MCLAUDE_EVENTS`, `MCLAUDE_API`, and `MCLAUDE_LIFECYCLE` streams.

The agent creates **ordered push consumers** on `MCLAUDE_SESSIONS_{uslug}` (replacing the previous durable pull consumers per ADR-0054):

- **Session command consumer** — Filtered to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.create` and `sessions.{sslug}.{input,delete,config,control.*}`. DeliverNew — agent processes commands as they arrive, no replay.

Messages are dispatched to handlers based on subject suffix. After a successful handler return, the message is acked; on handler error or after MaxDeliver exhaustion the message is dropped (indicates a handler bug — not retried by the application layer).

### NATS KV Buckets (Read/Write)

Per ADR-0054, all KV buckets are per-user. The agent's JWT is scoped to one host and one project within the user's buckets.

- **KV_mclaude-sessions-{uslug}** (write) — Writes session state on creation, every state change, usage accumulation, and compaction boundary. Key format: `hosts.{hslug}.projects.{pslug}.sessions.{sslug}`. Agent JWT scopes writes to its own host+project prefix.
- **KV_mclaude-projects-{uslug}** (write) — Updates project state (e.g., clear importRef). Key format: `hosts.{hslug}.projects.{pslug}`. Agent JWT scopes writes to its own project key.

### NATS KV Buckets (Read-Only)

- **KV_mclaude-sessions-{uslug}** (read) — Read on startup recovery and session state polling. JWT-scoped to own project's session keys.
- **KV_mclaude-projects-{uslug}** (read) — Read for project context. JWT-scoped to own project key.
- **KV_mclaude-hosts** (read) — Shared bucket. Read-only access to the host's own key (`{hslug}`), scoped per-host in JWT.

The agent does not write to `mclaude-hosts` (single-writer is control-plane, sourced from `$SYS` events). The previous `mclaude-laptops` and `mclaude-heartbeats` buckets are removed entirely per ADR-0035. The `mclaude-job-queue` bucket is eliminated per ADR-0044 — quota-managed sessions use the session KV with extended fields.

### NATS Subjects (Publish)

All publishes use the host-scoped pattern per ADR-0054: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.…`. The agent's JWT is scoped to one host and one project (per-project scoping).

- **Session events** to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.events` — Canonical `SessionEvent` structs (translated from the CLI backend's native protocol by the driver). Captured by the `MCLAUDE_SESSIONS_{uslug}` stream.
- **Lifecycle events** to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.lifecycle.{eventType}` — Published on session creation, stop, resume, restart, upgrade, failure, permission denial, quota interruption, and completion. Captured by the `MCLAUDE_SESSIONS_{uslug}` stream.
- **API error events** to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions._api` — Published when a create/delete/restart handler encounters an error. Payload: `{type: "api_error", request_id: string, operation: "create" | "delete" | "restart", error: string}`.
- **Quota status** to `mclaude.users.{uslug}.quota` (designated agent only) — Published every 60 seconds from Anthropic API polling by the designated quota publisher agent (ADR-0044). CP assigns one agent per user as the quota publisher on registration. The quota subject is user-scoped (no host segment) because quota is an account-wide signal.

### NATS Subjects (Subscribe)

All API subscriptions are at host-and-project scope — the Agent subscribes once per `(uslug, hslug, pslug)` triple and demultiplexes to the correct session internally. Messages include a `sessionSlug` that the Agent uses to route to the right `Session` in its map. The agent's JWT is per-project scoped (ADR-0054).

- **Session commands** via ordered push consumers on `MCLAUDE_SESSIONS_{uslug}` — filtered to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.>` (create, delete, input, config, control.interrupt, control.restart).
- **Terminal API** via core NATS subscriptions on `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.{create,delete,resize}` — Latency-sensitive; stays on core NATS.
- **Quota status** (per-session QuotaMonitor) via core NATS on `mclaude.users.{uslug}.quota`.
- **Quota publisher re-designation** via core NATS on `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.manage.designate-quota-publisher` — CP publishes when re-designating the quota publisher after the previously designated agent goes offline (ADR-0044). On `quotaPublisher: true`, agent starts `runQuotaPublisher`; on `quotaPublisher: false`, agent stops it.

The agent does **not** subscribe to project-creation requests; project provisioning per ADR-0054 is handled by `mclaude-controller-local` (BYOH) or `mclaude-controller-k8s` (cluster), each subscribing to `mclaude.hosts.{hslug}.>` (host-scoped, ADR-0054).

### Unix Sockets

Each session exposes a debug socket at `/tmp/mclaude-session-{sessionId}.sock`. Protocol is NDJSON: server broadcasts Claude Code stdout events to connected clients; clients send stream-json messages that are forwarded to Claude Code stdin. Publishes `debug_attached` and `debug_detached` lifecycle events on client connect/disconnect.

### HTTP (Removed)

> **Eliminated per ADR-0044.** The `localhost:8378` HTTP API for job management is removed. Callers publish `sessions.create` directly with quota fields. The `mclaude-job-queue` KV bucket, job dispatcher goroutine, and all job-related HTTP endpoints are eliminated.

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

Sessions track the CLI driver's reported state via canonical `state_change` events, plus agent-managed states for quota enforcement (ADR-0044):

**Driver-reported states** (fine-grained, for SPA real-time rendering):
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
```

**KV `status` field** (coarser lifecycle state, ADR-0044 renamed from `state`):
```
pending → running → paused → running → completed
                  → requires_action → running
                  → needs_spec_fix
                  → stopped | cancelled | failed | error
```

Full status enum: `pending`, `running`, `paused`, `requires_action`, `completed`, `cancelled`, `needs_spec_fix`, `failed`. Note: `stopped` and `error` appear in the KV schema but are not used in practice — sessions are deleted from KV on stop (not tombstoned), and `error` is subsumed by `failed` with a failure reason.

State transitions flow from the driver's `state_change` canonical events. The agent updates in-memory state and flushes to KV on every transition, except during graceful shutdown when KV writes are suppressed to preserve the `updating` banner. KV write failures are logged at warn level (ADR-0051) — the write is fire-and-forget but operators must have visibility into failures.

### Session Lifecycle

**Creation:**
1. Receive `sessions.create` from JetStream consumer.
2. Generate session UUID and derive session slug.
3. Derive branch name (from request, slugified session name, or `session-{shortId}`).
4. Check for worktree collision; error if branch is in use and `joinWorktree` is false.
5. Create git worktree via `git worktree add` (refreshes credentials first if managed).
6. Write initial `SessionState` to KV with state `idle`.
7. Apply permission policy and allowed-tools set from request.
8. Wire `QuotaMonitor` if `softThreshold > 0` in the request (quota-managed session per ADR-0044). Set `onStrictDeny` and `onRawOutput` session callbacks.
9. Start debug unix socket.
10. Look up the `CLIDriver` from the `DriverRegistry` based on the `backend` field (default: `claude_code`). Launch via `driver.Launch(ctx, opts)`. Start `driver.ReadEvents(proc, events)` goroutine for the event loop.
11. Publish `session_created` lifecycle event.

**Resumption (on agent restart):**

Recovery uses the KV-based mechanism (ADR-0044) — no stream replay needed. Session KV is the recovery source of truth.

1. Watch all keys in `KV_mclaude-sessions-{uslug}` for initial values (filtered to own host+project prefix).
2. For **interactive sessions** (no quota fields / `softThreshold == 0`):
   a. Set session KV to `status: "restarting"`, clear `pendingControls`.
   b. Publish `session_restarting` lifecycle event.
   c. Resume via `driver.Resume(ctx, sessionID, opts)`.
   d. On `init` event: set `status: "idle"`, `stateSince: now`, update model/capabilities, flush to KV (ADR-0051).
   e. Start crash watcher goroutine (ADR-0051).
   f. Sessions in `updating` status are resumed but KV entry stays as `updating` until consumers are attached, then cleared to `running`.
   g. Publish `session_resumed` lifecycle event.
   h. Sessions that fail to start within 30s: mark `status: "failed"`, publish `session_failed`.
3. For **quota-managed sessions** (`softThreshold > 0`, per ADR-0044):
   - **`status: pending`**: CLI subprocess was warm but prompt was never sent. Agent respawns the CLI subprocess via the driver, starts a new `QuotaMonitor`, subscribes to quota updates, and gates prompt delivery on the next quota update (same as initial startup).
   - **`status: paused`**: Session was paused on quota. Agent checks if subprocess is alive. If alive: start a new `QuotaMonitor` and wait for quota recovery. If dead: attempt `driver.Resume()` with `claudeSessionID` from KV (degraded fallback). If `autoContinue` and `resumeAt` has passed, attempt resume immediately on next favorable quota update.
   - **`status: running`**: Session was mid-execution; subprocess is dead (agent restarted). Agent reads `claudeSessionID` from KV and attempts resume. On success: start a new `QuotaMonitor`. On failure: update KV → `status: failed`.
   - Interactive sessions follow path (2) unchanged.

**Deletion:**

`handleDelete` branches on `sess.getState().SoftThreshold > 0` (ADR-0044):

**Interactive session** (`SoftThreshold == 0`):
1. Remove session from in-memory map.
2. Send interrupt via `driver.Interrupt(proc)`; wait up to 10 seconds, then SIGKILL.
3. Remove git worktree if this was the last session using the branch.
4. Delete session key from KV.
5. Publish `session_stopped` lifecycle event.

**Quota-managed session** (`SoftThreshold > 0`):
1. If session has an active `QuotaMonitor`, call `monitor.stop()`.
2. Remove session from in-memory map.
3. Send interrupt via `driver.Interrupt(proc)`; wait up to 10 seconds, then SIGKILL.
4. If the session's `Branch` starts with `schedule/`, skip worktree removal (worktree persists for potential re-use). Otherwise, remove worktree if last session on that branch.
5. Tombstone the session KV entry (KV delete — session record disappears).
6. Publish `session_job_cancelled` lifecycle event.

**Restart:**
1. Publish `session_restarting` lifecycle event.
2. Stop existing process via `driver.Interrupt(proc)` + timeout + kill.
3. Clear pending controls, update extra flags if provided.
4. Respawn via `driver.Resume(ctx, sessionID, opts)`.
5. Publish `session_resumed` lifecycle event.

### Import Handler (ADR-0053)

On startup, after NATS connection and KV bucket initialization, the agent checks for a pending session import:

1. Read project state from `KV_mclaude-projects-{uslug}` (key: `hosts.{hslug}.projects.{pslug}`).
2. If `importRef` is set (e.g. `imp-001`), initiate the import flow:
   a. Request a pre-signed download URL from the control-plane via NATS request/reply on `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.download`, sending `{importId}`.
   b. Download the tar.gz archive directly from S3 using the pre-signed URL.
   c. Verify archive integrity: validate `metadata.json` schema, verify SHA-256 checksum if present, validate JSONL line format.
   d. Unpack JSONL session transcripts, subagent data, and memory files into the session data directory (PVC `/data/projects/` on K8s, `~/.claude/projects/{encoded-cwd}/` on BYOH).
   e. For each session ID in the archive: if a `SessionState` KV entry already exists for that session ID, **skip** the session with a warning log and continue importing remaining sessions (session ID collision handling).
   f. On successful unpack: clear `importRef` from project KV state (`KV_mclaude-projects-{uslug}`), publish `import.complete` via NATS to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.complete` with standard envelope payload `{"id":"...","ts":...}`. The control-plane receives the signal, resolves the import ID from project KV, and deletes the S3 object.
   g. Report import completion via `session_import_complete` lifecycle event.
3. If unpack fails (disk full, permissions, corrupt archive): report error via `session_import_failed` lifecycle event. Leave `importRef` set in project KV so the import can be retried on the next agent restart.
4. After import completes (or if no `importRef` was set), proceed with normal session recovery.

### Attachment Support (ADR-0053)

The agent handles binary attachments (images, screenshots, files) via S3 pre-signed URLs. NATS messages carry lightweight `AttachmentRef` references (id, filename, mimeType, sizeBytes) — never raw bytes.

#### Attachment Download

When processing a user input message containing `AttachmentRef` content blocks:

1. For each `AttachmentRef` in the message, request a pre-signed download URL from the control-plane via NATS request/reply on `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.download`, sending `{id: "<attachment-id>"}`.
2. The control-plane validates that the agent's JWT matches the project owning the attachment (uslug/hslug/pslug from JWT must match the attachment's S3 key prefix).
3. The control-plane returns `{downloadUrl: "https://s3.../...?X-Amz-Signature=..."}`.
4. The agent downloads the file directly from S3 using the pre-signed URL.
5. The downloaded file is passed to the CLI driver as image/file input for the current session.

#### Attachment Upload (Agent-Generated)

When the agent generates binary output (images, diagrams, files):

1. Request a pre-signed upload URL from the control-plane via NATS request/reply on `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.upload`, sending `{filename, mimeType, sizeBytes}`.
2. The control-plane returns `{id: "<attachment-id>", uploadUrl: "https://s3.../...?X-Amz-Signature=..."}`.
3. The agent uploads the file directly to S3 using the pre-signed URL.
4. The agent confirms the upload via NATS request/reply on `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.confirm`, sending `{id: "<attachment-id>"}`.
5. The agent includes an `AttachmentRef` (id, filename, mimeType, sizeBytes) in the session event content blocks published to NATS.

### fsnotify Watcher (ADR-0053)

The agent runs an `fsnotify` file watcher on the session data directory to discover new JSONL session files — both from import unpacking and any future file-based session discovery:

1. On startup, begin watching the session data directory for new `.jsonl` files.
2. When a new JSONL file is detected (created or moved into the directory):
   a. Extract session metadata from the first few lines of the JSONL file: session ID, timestamps, branch, model.
   b. Check if a `SessionState` KV entry already exists for the extracted session ID. If so, skip with a warning (duplicate).
   c. Create a `SessionState` KV entry in `KV_mclaude-sessions-{uslug}` with `status: "completed"` (imported sessions are historical, read-only).
3. The watcher runs for the lifetime of the agent process, enabling live discovery of sessions placed into the directory by any mechanism (import, manual copy, etc.).

### Event Routing

The session agent's core event loop is **driver-agnostic** (ADR-0005). Instead of directly parsing NDJSON from Claude Code's stdout, the agent reads canonical `SessionEvent` structs from the driver's `ReadEvents()` channel:

```
CLIDriver.ReadEvents(proc) → chan DriverEvent → NATS publish + side effects
```

The event router goroutine reads from the driver's event channel and for each `SessionEvent`:
1. **In-flight background agent tracking:** Increments `inFlightBackgroundAgents` on `message` events with an `Agent` tool_use where `input.run_in_background == true`. Decrements (floored at 0) on top-level `message` events with `origin.kind == "task-notification"`. Used by the drain predicate in graceful shutdown.
2. **In-flight background shell tracking (K8s mode; two-phase):**
   - **Phase 1 — Pending:** On `tool_call` event with a `Bash` tool where `input.run_in_background == true`, adds a `pendingShell{toolUseId, command, startedAt}` entry to `sess.pendingShells`.
   - **Phase 2 — Promoted:** On `tool_result` event whose `toolUseId` matches a pending entry, extracts `backgroundTaskId` from the result text (pattern: `"Command was manually backgrounded with ID: (\S+)"`), constructs `outputFilePath` as `{CLAUDE_CODE_TMPDIR}/claude-{uid}/{sanitizePath(cwd)}/{sessionId}/tasks/{taskId}.output`, and promotes the entry to `sess.inFlightShells`. `sanitizePath` replaces every character outside `[a-zA-Z0-9]` with `-`.
   - **Removal:** On `message` event with `origin.kind == "task-notification"` referencing the shell's toolUseId (shell completed naturally).
   - Shell tracking is disabled when `CLAUDE_CODE_TMPDIR` is not set (BYOH mode).
3. Truncates events exceeding the 8 MB NATS payload limit (removes `content` field, adds `truncated: true`).
4. Marshals as JSON and publishes to the session's NATS events subject (`sessions.{sslug}.events`). Note: user input messages arrive on the `sessions.{sslug}.input` subject which is already captured by the `MCLAUDE_SESSIONS_{uslug}` stream's `sessions.>` filter — no separate republish to the events subject is needed.
5. Notifies the compact-boundary callback to update `replayFromSeq`.
6. Notifies the quota monitor raw output callback (`onRawOutput`).
7. Broadcasts to connected debug clients.
8. Processes side effects (state changes, init, permission requests, usage accumulation — including `cacheReadTokens` and `cacheWriteTokens`).

**Input routing** (NATS → driver): A separate goroutine reads from the `sessions.{sslug}.input` subject and dispatches to the driver:
- `type: "message"` → `driver.SendMessage(proc, msg)`
- `type: "permission_response"` → `driver.SendPermissionResponse(proc, requestID, allow)`
- `type: "skill_invoke"` → `driver.SendMessage(proc, skillMessage)` (skill invocations sent as `/skill-name` prefixed messages)

Config updates from `sessions.{sslug}.config` are routed to `driver.UpdateConfig(proc, cfg)`. Interrupts from `sessions.{sslug}.control.interrupt` are routed to `driver.Interrupt(proc)`.

The stdin serializer goroutine drains the stdin channel sequentially to prevent line interleaving.

### Permission Policy

Four policies control how `permission` events (tool-use permission prompts) are handled:

| Policy | Behavior |
|--------|----------|
| `managed` (default) | Forward to NATS client for human decision |
| `auto` | Auto-approve all tools |
| `allowlist` | Auto-approve listed tools; forward others to client |
| `strict-allowlist` | Auto-approve listed tools; auto-deny all others |

**`allowedTools` validation (ADR-0044):** If `allowedTools` is empty on a `strict-allowlist` session, the agent **rejects** the `sessions.create` request and publishes a `lifecycle.error` event. There is no default allowlist — callers must explicitly declare their tool scope. The previous implicit `defaultDevHarnessAllowlist` is eliminated.

The `strict-allowlist` policy invokes an `onStrictDeny` callback on denial, which publishes a `session_permission_denied` lifecycle event and signals the quota monitor via `permDeniedCh` (in-process Go channel). The `QuotaMonitor` responds by sending a graceful stop message and transitioning the session to `status: needs_spec_fix` with `failedTool` set to the denied tool name.

### Credential Management

#### Authentication (All Modes — ADR-0054)

All session-agents authenticate via HTTP challenge-response to the control-plane (ADR-0054 unified credential protocol):

1. Agent generates its own NKey pair at startup (private seed never leaves the agent process).
2. Agent exposes its public key to the host controller via local IPC (stdout/file on BYOH, shared volume on K8s).
3. Host controller registers the public key with CP via `mclaude.hosts.{hslug}.api.agents.register` (NATS request/reply).
4. Agent authenticates itself via HTTP: `POST /api/auth/challenge {nkey_public}` → `POST /api/auth/verify {nkey_public, challenge, signature}` → receives per-project JWT.
5. Agent JWT has a **5-minute TTL**. The agent runs the same HTTP challenge-response flow before TTL expiry (proactive refresh).
6. On `permissions violation` error from NATS, the agent triggers an immediate refresh + retry.

No credential handoff between controller and agent — the controller never touches the agent's JWT or private key. The agent's JWT is per-project scoped (ADR-0054): `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.>` for NATS subjects, plus scoped KV and stream permissions.

#### Git Credentials (K8s Mode)

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

When a `compact_boundary` event is published, the agent queries the `MCLAUDE_SESSIONS_{uslug}` stream for its last sequence number and writes it to the session's KV entry as `replayFromSeq`. New SPA subscribers use this to skip already-compacted history.

### Quota Monitoring

Per ADR-0044, one `QuotaMonitor` goroutine per quota-managed session, created when `softThreshold > 0` on `sessions.create`. The monitor uses two-tier quota enforcement: a soft threshold that injects a cooperative stop marker and a hard token budget that sends an interrupt.

#### Session Callbacks

The agent sets two callbacks on each session before starting:

```go
onStrictDeny func(toolName string)   // called when strict-allowlist auto-denies a permission
onRawOutput func(evType string, raw []byte)  // called for every raw event from the driver before NATS publish
```

`onStrictDeny` publishes `session_permission_denied` lifecycle event AND sends `toolName` on `monitor.permDeniedCh`. `onRawOutput` is used by the QuotaMonitor for token counting and turn-end detection.

#### QuotaMonitor Goroutine

The `QuotaMonitor` is a goroutine with a select-loop over five cases:

- `<-m.stopCh`: exit cleanly.
- `toolName := <-m.permDeniedCh`: if `stopReason == ""`, set `stopReason = "permDenied"`, send graceful stop. Session KV → `status: needs_spec_fix`, `failedTool: toolName`.
- `msg := <-m.quotaCh`: update cached `QuotaStatus`. Four triggers:
  - **Soft threshold breached** (`u5 >= softThreshold` and `stopReason == ""`): set `stopReason = "quota_soft"`, inject `MCLAUDE_STOP: quota_soft` via `sessions.{sslug}.input` (NATS), capture `outputTokensAtSoftMark`, reset `outputTokensSinceSoftMark = 0`.
  - **Quota available for pending session** (session is `pending`, `u5 < softThreshold`): send `m.prompt` as the initial user message via `sessions.{sslug}.input`, update session KV → `status: running`.
  - **Quota recovered** (session is `paused`, `u5 < softThreshold`): check `resumeAt` if `autoContinue`, send resume nudge via `sessions.{sslug}.input`, update session KV → `status: running`.
  - **No data** (`hasData == false`): do not start pending sessions, do not pause running ones.
- `<-m.turnEndedCh`: dispatch to `handleTurnEnd()`. **Priority: `turnEndedCh` is checked before `doneCh`** in the select using a nested `select` with `default` fallthrough. This ensures `handleTurnEnd` sets `terminalEventPublished` before `handleSubprocessExit` can observe `doneCh`.
- `<-session.doneCh`: dispatch to `handleSubprocessExit()`.

#### Message Format

QuotaMonitor publishes messages to NATS subject `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.input` (captured by `MCLAUDE_SESSIONS_{uslug}` stream). Messages use the standard `sessions.{sslug}.input` JSON envelope:

```json
{"id": "01JTRK...", "ts": 1714470080000, "type": "message", "text": "MCLAUDE_STOP: quota_soft"}
```

Three message types:
- **Initial prompt delivery:** `"text": "<caller-supplied prompt>"` — sent when `u5 < softThreshold` and session is `pending`.
- **Soft-stop marker:** `"text": "MCLAUDE_STOP: quota_soft"` — sent when `u5 >= softThreshold` during a running session.
- **Resume nudge:** `"text": "Resuming — continue where you left off."` (or `resumePrompt` if provided) — sent when quota recovers and session is `paused`.

Hard-stop `control_request` interrupts are written directly to `sess.stdinCh` in the CLI backend's native format and are **NOT** published to NATS.

#### Token Counting

Token counting uses a two-strategy approach via the `onRawOutput` callback:

- **Primary (byte estimate):** For every assistant event while `stopReason != ""`, increment `outputTokensSinceSoftMark` by `len(raw) / 4`. Provides real-time progress tracking during the turn.
- **Authoritative (turn-complete event):** When a `turn_complete` event arrives with `outputTokens`, replace the running estimate: `outputTokensSinceSoftMark = turnComplete.OutputTokens - outputTokensAtSoftMark`.
- **Hard budget check:** After each increment, if `outputTokensSinceSoftMark >= hardHeadroomTokens` and `stopReason == "quota_soft"`, set `stopReason = "quota_hard"` and fire `control_request` interrupt on `sess.stdinCh` immediately.

When `hardHeadroomTokens` is 0, the hard interrupt fires immediately after the soft marker is injected (zero tolerance).

#### Turn-End Detection (`handleTurnEnd`)

Turn-end is detected via the `turn_complete` canonical event (fired by `onRawOutput`). `handleTurnEnd()` inspects `stopReason`:

| `stopReason` | Event published | Next step |
|--------------|-----------------|-----------|
| `"quota_soft"` | `session_job_paused` with `pausedVia: "quota_soft"` + `r5` | Reset `stopReason`; session KV → `status: paused`; subprocess stays alive. |
| `"quota_hard"` | `session_job_paused` with `pausedVia: "quota_hard"` + `r5` + `outputTokensSinceSoftMark` | Reset `stopReason`; session KV → `status: paused`; subprocess stays alive. |
| `""` (empty) | `session_job_complete` | Session KV → `status: completed`. CLI subprocess exits naturally. Session record persists for user review. |

#### Subprocess Exit (`handleSubprocessExit`)

On `doneCh` close:
- If `terminalEventPublished` → no-op (expected cleanup after completion or cancellation).
- Otherwise → publish `session_job_failed` with `error: "subprocess exited without turn-end signal"`. Session KV → `status: failed`.

#### Quota Publisher Designation

CP designates exactly one agent per user as the quota publisher (ADR-0044). The initial designation is delivered via a `quotaPublisher: true` field in CP's response to `mclaude.hosts.{hslug}.api.agents.register`. The designated agent's `runQuotaPublisher` goroutine polls `api.anthropic.com/api/oauth/usage` every 60 seconds and publishes `QuotaStatus` to `mclaude.users.{uslug}.quota` (core NATS, slug-based subject). Non-designated agents only subscribe to the quota subject.

**Re-designation:** If the designated agent goes offline (detected by CP via `$SYS.ACCOUNT.*.DISCONNECT`), CP selects the next online agent for that user and publishes a re-designation message to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.manage.designate-quota-publisher` with `{quotaPublisher: true}`. The agent subscribes to this subject on startup (core NATS). On receipt of `quotaPublisher: true`, the agent starts `runQuotaPublisher` if not already running. On receipt of `quotaPublisher: false`, the agent stops `runQuotaPublisher` (another agent has taken over). The previously designated agent — if it reconnects after a transient disconnect — will also receive `quotaPublisher: false` to stop its stale publisher.

### Graceful Shutdown (SIGTERM)

The shutdown sequence preserves in-progress work and enables zero-downtime upgrades:
1. Write `state: "updating"` to KV for all sessions (SPA displays upgrade banner). Set `shutdownPending` flag to suppress further KV state flushes.
2. Cancel the command consumer (new commands queue in JetStream for the replacement pod).
3. Drain core NATS subscriptions (terminal API).
4. Keep the control consumer running (interrupts and permission responses still work).
5. Poll every 1 second (no wall-clock timeout — indefinite wait; `terminationGracePeriodSeconds: 86400` from values.yaml is K8s's last-resort backstop, not a policy limit): wait for all sessions to reach `idle` state with zero in-flight background agents. Auto-interrupt sessions stuck in `requires_action`.
6. Shell-killed notifications (K8s mode only, when `CLAUDE_CODE_TMPDIR` is set): for each session, for each entry in `inFlightShells`, construct a `<task-notification>` XML message with child elements `<task-id>`, `<tool-use-id>`, `<output-file>`, `<status>killed</status>`, and `<summary>` populated from the `inFlightShell` entry. Publish it as a session-input payload to the `sessions.{sslug}.input` subject. Messages queue in the consumer for the replacement pod. (If the pod crashes before publishing, those notifications are lost — tolerated; Claude will notice unresolved shell tool_uses on the next BashOutput call.)
7. Cancel the control consumer.
8. Publish `session_upgrading` lifecycle event per session.
9. Exit.

### Daemon: JWT Refresh (Deprecated)

> **Deprecated per ADR-0058.** Agents now use HTTP challenge-response for credential refresh (see Credential Management section). The daemon JWT refresh mechanism is replaced by the per-agent 5-minute TTL proactive refresh.

### Daemon: Job Dispatcher (Removed)

> **Eliminated per ADR-0044.** The job dispatcher goroutine, `mclaude-job-queue` KV bucket, and `localhost:8378` HTTP API are all removed. Callers publish `sessions.create` with quota fields directly. The session-agent handles quota management per-session via `QuotaMonitor`.

### Liveness

Liveness is signalled by the NATS connection itself. When an agent connects to hub NATS, the hub publishes `$SYS.ACCOUNT.{accountKey}.CONNECT`; control-plane subscribes and updates `hosts.last_seen_at` and the `mclaude-hosts` KV entry. On disconnect, control-plane sets `online=false`. There is no periodic heartbeat publish; slug uniqueness is enforced server-side at registration time.

### Terminal Sessions

Terminal sessions spawn a PTY shell and bridge I/O through core NATS subjects. The agent subscribes to terminal input and publishes terminal output in 4 KB chunks. Resize events are forwarded to the PTY. On delete, the PTY is closed and the process is killed.

## Error Handling

| Failure | Behavior |
|---------|----------|
| NATS connection lost | NATS client auto-reconnects; state changes buffered in memory during outage and flushed on reconnect. CLI processes continue running. Reconnection counter incremented. `natsConnect()` sets `MaxReconnects(-1)` (unlimited) and `RetryOnFailedConnect(true)` — BYOH agents reconnect indefinitely after NATS outages. |
| KV bucket not found on startup | Fatal: session agent exits (control-plane has not created per-user buckets yet) |
| Session create — worktree collision | `api_error` event published to `sessions._api` |
| Session create — git worktree add fails | `api_error` event published to `sessions._api` |
| Session create — CLI process fails to start | `session_failed` lifecycle event published to `sessions.{sslug}.lifecycle.error` |
| Session delete — handler error | `api_error` event published to `sessions._api` with `operation: "delete"` |
| Session restart — handler error | `api_error` event published to `sessions._api` with `operation: "restart"` |
| CLI process crash (unexpected exit) | `watchSessionCrash` goroutine detects exit via `doneCh`, publishes `session_failed`, auto-restarts the session, increments `mclaude_claude_restarts_total`. |
| Git auth error during initial clone (K8s) | `session_failed` lifecycle event with `provider_auth_failed` reason published; agent exits |
| Credential helper setup fails | Logged as warning; continues (non-fatal, SSH key auth may still work) |
| Session delete — process does not stop within 10s | Process is SIGKILLed; deletion proceeds |
| Graceful shutdown — session stuck in requires_action | Auto-interrupted so the turn aborts to idle |
| JetStream message exhausts MaxDeliver (5 deliveries) | Message dropped. Indicates a persistent handler bug — not retried by the application. |
| Pod crash (no SIGTERM — SIGKILL, OOM, node failure) | In-flight shell tracking (`inFlightShells`, `pendingShells`) is lost with the process. JetStream consumers redeliver unacked messages. Sessions resume from KV. Dangling background-shell tool_uses remain in the transcript — the CLI notices the unknown shell-id when it attempts `BashOutput` and can adjust. |
| JetStream fetch error | Exponential backoff (100ms to 5s) with retry |
| Event exceeds 8 MB NATS payload | Content field stripped; `truncated: true` added |
| JWT refresh fails (agent) | Logged as warning; agent uses current JWT until expiry. On `permissions violation` error, triggers immediate refresh + retry. |
| `HOST_SLUG` / `--host` not provided | Agent tries fallback sources in order: `--host` flag → `HOST_SLUG` env → `~/.mclaude/active-host` symlink → `HOSTNAME` env → `os.Hostname()`. If no valid slug is found after all fallbacks, agent exits with `FATAL: HOST_SLUG required`. |
| Host JWT signed for the wrong host | Hub NATS auth rejects publishes/subscribes; the agent surfaces this as a NATS auth error and exits or refuses the failing operation. |
| `allowedTools` empty on `strict-allowlist` session | Agent rejects `sessions.create` — publishes `lifecycle.error` with message (ADR-0044). |
| Debug socket start fails | Logged as warning; CLI attach disabled but sessions function normally |
| Import — session ID collision (re-import) | Skip the conflicting session with a warning log, import remaining sessions. Existing `SessionState` KV entry is not overwritten. |
| Import — unpack fails (disk full, permissions, corrupt archive) | Report error via `session_import_failed` lifecycle event. Leave `importRef` set in project KV so the import can be retried on the next agent restart. |
| Import — S3 download URL expired | Request a new pre-signed URL from CP via `import.download` request/reply, retry download. |
| Import — archive integrity check fails | Treat as unpack failure: report error via lifecycle event, leave `importRef` for retry. |
| Attachment — download pre-signed URL expired | Request a new URL from CP via `attachments.download`, retry. |
| Attachment — upload fails mid-stream | Retry from beginning with a new pre-signed URL from CP via `attachments.upload`. |

## Dependencies

| Dependency | Purpose |
|------------|---------|
| NATS server | Messaging, JetStream streams, KV buckets (agents connect directly to hub NATS per ADR-0054) |
| Control-plane | Must have created per-user KV buckets and streams before agent starts. Issues agent JWTs via HTTP challenge-response. |
| CLI backend binary | Spawned as child process for each session via `CLIDriver` (Claude Code, Factory Droid, Devin CLI, or generic terminal) |
| git | Bare repo clone, worktree management, fetch |
| gh CLI | Credential helper for GitHub hosts |
| glab CLI | Credential helper for GitLab hosts |
| Nix | Package manager for user-installed tools (K8s mode; PVC-backed store) |
| Anthropic OAuth API | Quota polling via `api.anthropic.com/api/oauth/usage` (designated agent only, ADR-0044) |
| `~/.claude/.credentials.json` | OAuth token for quota API (designated agent only) |
| `mclaude-controller-local` | Spawns per-project agents on BYOH hosts, registers agent NKeys with CP (ADR-0058) |
| user-secrets K8s Secret | NATS creds, OAuth token, git CLI configs, connection tokens (K8s mode) |
| user-config K8s ConfigMap | Claude Code seed settings and hooks (K8s mode) |
| Project PVC at `/data` | Bare repo, worktrees, JSONL persistence, config (K8s mode) |
| Nix PVC at `/nix` | Shared Nix store (K8s mode) |
