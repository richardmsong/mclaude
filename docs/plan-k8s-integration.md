# mclaude Platform Architecture

## Overview

Two services + infrastructure. The relay, connector, server, and controller collapse into simpler, well-scoped components. The session agent communicates with Claude Code via the structured stream-json protocol (the same protocol used by VS Code and JetBrains IDE extensions), eliminating tmux, JSONL tailing, and screen scraping entirely.

| Component | Language | Role |
|-----------|----------|------|
| `mclaude-session-agent` | Go | Spawns headless Claude Code processes, routes stream-json events to/from NATS. Same binary on laptop and in K8s pod. |
| `mclaude-control-plane` | Go | Platform control plane. Auth, SSO, SCIM, user/project provisioning, K8s namespace + Deployment management, NATS JWT issuance. |
| `mclaude-cli` | Go | Debug attach tool. Thin text REPL over unix socket to session agent. |
| NATS JetStream | — | Event bus, state (KV), routing between all components and clients. |
| Postgres | — | Users table only. Lives in `mclaude-system`. |
| nginx ingress | — | Dumb reverse proxy. No routing logic, no auth. |
| SPA | TypeScript | Web client. Mobile browser first. |

**Gone:**

| Old component | Replaced by |
|---------------|-------------|
| `mclaude-relay` | nginx ingress |
| `mclaude-connector` | session agent connects to NATS directly |
| `mclaude-server` | collapsed into session agent |
| `mclaude-controller` | merged into control-plane |
| Per-namespace Postgres | central Postgres (users only) + NATS KV (state) |
| Per-namespace mclaude-server | session agent runs inside each project pod |
| tmux | Claude Code runs headless via `--print --output-format stream-json` |
| JSONL tailing | stream-json stdout provides real-time events |
| Screen scraping / capture-pane | `session_state_changed` events + `control_request` protocol |
| Protobuf event schema | Claude Code's stream-json IS the canonical event schema |

---

## Architecture

```
                    nginx ingress (mclaude-system)
                    /auth  /api  /scim → control-plane
                    /nats             → NATS WebSocket proxy
                    /*                → SPA static files
                           │
           ┌───────────────┼───────────────┐
           ▼               ▼               ▼
   control-plane       NATS            SPA
   (Postgres)       (JetStream + KV)
                          │
          ┌───────────────┼───────────────┐
          ▼               ▼               ▼
   session-agent    session-agent   session-agent
   (K8s pod)        (K8s pod)       (laptop daemon)
     │                │                │
     ▼                ▼                ▼
   claude             claude           claude
   (headless          (headless        (headless
    stream-json)       stream-json)     stream-json)
```

Clients (browser, laptop agent) connect to NATS via `/nats` WebSocket proxy through nginx. K8s session agents connect to NATS directly (in-cluster). NATS subject-based permissions enforce tenant isolation — users can only pub/sub on `mclaude.{userId}.>`.

### Claude Code Integration

The session agent spawns Claude Code headless:

```bash
claude --print --verbose \
  --output-format stream-json \
  --input-format stream-json \
  --include-partial-messages \
  --session-id {sessionId}
```

For session resume after pod restart:

```bash
claude --print --verbose \
  --output-format stream-json \
  --input-format stream-json \
  --include-partial-messages \
  --resume {sessionId}
```

`--print` disables the interactive TUI and trust dialog. Auth is handled via `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` env vars — no keychain needed. Hooks, LSP, auto-memory, and plugin discovery all run normally (needed for guard hooks, code intelligence, and CLAUDE.md discovery). `--include-partial-messages` enables token-by-token streaming of Claude's text (see Terminal section).

Claude Code still writes JSONL internally (its own persistence for `--resume`). The session agent never reads JSONL.

**Claude CLI installation**: native binary via `claude install --version {pinned-version}` in the session-agent Dockerfile. No Node.js/npm dependency required. Version is pinned — updates go through `/upgrade-claude` (see below).

### Stream-JSON Protocol

**Output (stdout)** — Claude Code emits NDJSON events:

```json
{"type": "system", "subtype": "init", "skills": [...], "tools": [...], "agents": [...], "model": "..."}
{"type": "system", "subtype": "session_state_changed", "state": "idle"}
{"type": "system", "subtype": "session_state_changed", "state": "running"}
{"type": "system", "subtype": "session_state_changed", "state": "requires_action"}
{"type": "assistant", "content": [...], "model": "...", "usage": {...}}
{"type": "stream_event", "event": {"type": "content_block_delta", ...}}
{"type": "user", "message": {...}}
{"type": "control_request", "request_id": "abc", "request": {"subtype": "can_use_tool", "tool_name": "Bash", ...}}
{"type": "tool_progress", "tool_use_id": "...", "tool_name": "Bash", "elapsed_time_seconds": 30}
{"type": "result", "subtype": "success", "usage": {...}, "duration_ms": 1234}
{"type": "clear"}
{"type": "compact_boundary"}
```

**Input (stdin)** — session agent writes:

```json
{"type": "user", "message": {"role": "user", "content": "fix the bug"}}
{"type": "user", "message": {"role": "user", "content": "/commit -m 'Fix bug'"}}
{"type": "user", "message": {"role": "user", "content": [{"type": "text", "text": "What's in this image?"}, {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "iVBOR..."}}]}}
{"type": "control_response", "response": {"subtype": "success", "request_id": "abc", "response": {"behavior": "allow"}}}
{"type": "control_request", "request": {"subtype": "interrupt"}}
{"type": "control_request", "request": {"subtype": "reload_plugins"}}
{"type": "control_request", "request": {"subtype": "set_model", "model": "claude-opus-4-6"}}
{"type": "control_request", "request": {"subtype": "set_max_thinking_tokens", "max_thinking_tokens": 10000}}
```

Skills work via plain text `/commit` messages in user content. Capabilities are queryable at runtime via `reload_plugins` control request. Images/files sent via standard Anthropic content arrays with base64-encoded data.

### Subagent Events

When Claude spawns a subagent (Explore, Plan, etc.), the subagent's events appear **flat** on the parent's stdout — not nested. Each event carries `parent_tool_use_id` linking it to the Agent tool_use that spawned it:

```json
{"type": "assistant", "content": [{"type": "tool_use", "id": "tu_1", "name": "Agent", ...}]}
{"type": "assistant", "content": [...], "parent_tool_use_id": "tu_1"}
{"type": "assistant", "content": [{"type": "tool_use", "name": "Grep", ...}], "parent_tool_use_id": "tu_1"}
{"type": "user", "message": {"content": [{"type": "tool_result", ...}]}, "parent_tool_use_id": "tu_1"}
```

The SPA uses `parent_tool_use_id` to render subagent events nested under the parent Agent tool block. Events with `parent_tool_use_id: null` are top-level. The session agent publishes all events verbatim regardless of nesting depth — `parent_tool_use_id` is valid at any depth (Agent → Agent → Agent). Rendering strategy for deep nesting is deferred to `plan-client-architecture.md`.

---

## NATS Subject Structure

### Location segment

All session-scoped subjects include a `{location}` segment identifying where the session runs. Location is a user-assigned name set during cluster or laptop registration (e.g., `macbook`, `home-lab`, `work-k8s`). Must be unique per user. Stored in the `mclaude-locations` KV bucket as `{userId}/{location}` → `{"type": "k8s"|"laptop", "machineId": "...", "ts": "..."}`.

This enables multi-cluster and hybrid (K8s + laptop) deployments under a single user namespace. The SPA subscribes with a wildcard on location to receive events from all locations: `mclaude.{userId}.*.{projectId}.events.>`.

### API (request/reply)

```
mclaude.{userId}.{location}.{projectId}.api.sessions.create
mclaude.{userId}.{location}.{projectId}.api.sessions.delete
mclaude.{userId}.{location}.{projectId}.api.sessions.input
mclaude.{userId}.{location}.{projectId}.api.sessions.control    → permission responses, interrupts
mclaude.{userId}.{location}.{projectId}.api.sessions.restart
mclaude.{userId}.api.projects.create    → control-plane (global, not location-scoped)
mclaude.{userId}.api.projects.update    → control-plane
mclaude.{userId}.api.projects.delete    → control-plane
mclaude.{userId}.api.projects.list      → control-plane
mclaude.{userId}.api.projects.updated   → control-plane broadcasts (project state changed)
```

### Events (JetStream, append-only)

```
mclaude.{userId}.{location}.{projectId}.events.{sessionId}       → Claude Code stream-json events
mclaude.{userId}.{location}.{projectId}.lifecycle.{sessionId}     → session agent lifecycle events
```

`events` carries raw stream-json objects from Claude Code stdout — no envelope, the subject encodes the routing metadata. `lifecycle` carries session agent's own events (created, stopped, resumed, debug attached/detached).

Separate subjects so clients can subscribe to one or both. Stream-json events are high-volume; lifecycle events are low-volume state transitions.

Stream `MCLAUDE_EVENTS` captures all `mclaude.*.*.*.events.*` subjects. Retained 30 days.
Stream `MCLAUDE_LIFECYCLE` captures all `mclaude.*.*.*.lifecycle.*` subjects. Retained 30 days.

**NATS message size**: default limit is 1MB. Large tool results (file reads, big diffs) can exceed this. Set `max_payload: 8388608` (8MB) in NATS server config. If a single event still exceeds 8MB, the session agent truncates the `content` field and sets a `truncated: true` flag — the full content is in Claude's JSONL if needed.

### State (KV)

```
KV bucket: mclaude-sessions
  key: {userId}/{location}/{projectId}/{sessionId}  → Session JSON (see below)

KV bucket: mclaude-projects
  key: {userId}/{projectId}              → Project JSON (see below)

KV bucket: mclaude-heartbeats
  key: {userId}/{location}/{projectId}  → {"ts": "..."}

KV bucket: mclaude-locations
  key: {userId}/{location}              → {"type": "k8s"|"laptop", "machineId": "...", "ts": "..."}
```

Watching a KV key gives real-time state updates to any subscriber. Clients watch their own user's keys.

**Bucket initialization**: control-plane creates all four buckets idempotently on startup (`nats.KeyValueStoreOrCreate`). Session agents and launchers do not create buckets — they fail fast if a bucket doesn't exist (indicates control-plane hasn't started yet).

**Entry lifetime**:
- `mclaude-sessions`: deleted by session agent on normal session delete. Orphaned entries (ungraceful shutdown) are swept by the daily JSONL cleanup job — any KV entry whose sessionId has no corresponding JSONL file on PVC older than 7 days is purged.
- `mclaude-projects`: deleted by control-plane on project delete.
- `mclaude-heartbeats`: TTL 90s on the KV entry itself (NATS KV native TTL). Expires automatically if agent stops writing.
- `mclaude-laptops`: TTL 24h. Launcher refreshes on startup and every 12h.

---

## NATS Authentication

**Operator**: platform root NKey — generated once at install, held by control-plane.

**Login flow**:

```
1. Client POST /auth/login → control-plane
2. Validate credentials against Postgres
3. Issue NATS User JWT scoped to mclaude.{userId}.>
4. Return { natsUrl, jwt, nkeySeed } to client
5. Client connects to wss://mclaude.example.com/nats using JWT
6. NATS broker validates JWT, enforces subject permissions
```

**Per-user JWT** (8h expiry, re-issued on refresh):

```json
{
  "nats": {
    "pub": { "allow": ["mclaude.{userId}.>", "_INBOX.>"] },
    "sub": { "allow": ["mclaude.{userId}.>", "_INBOX.>"] },
    "expires": 28800
  }
}
```

**Per-session-agent credentials** (provisioned by control-plane, long-lived):

```json
{
  "nats": {
    "pub": { "allow": ["mclaude.{userId}.>", "_INBOX.>"] },
    "sub": { "allow": ["mclaude.{userId}.>", "_INBOX.>"] }
  }
}
```

Enforced at the NATS broker. A client with alice's JWT cannot subscribe to `mclaude.bob.>` — the broker rejects it cryptographically. No application-level auth checks needed in pub/sub paths.

`_INBOX.>` permission is required on all clients for NATS request/reply responses.

**JWT refresh**: the SPA decodes the JWT `exp` claim and checks remaining TTL periodically (every 60s). When TTL falls below a refresh threshold, it calls `POST /auth/refresh`. On success, reconnects NATS with the new JWT seamlessly. On failure (session expired, SSO revoked), redirects to login. The NATS client library (`nats.ws`) supports `reconnect` with updated credentials without dropping subscriptions. JWT expiry duration and refresh threshold are configurable in control-plane (env vars `JWT_EXPIRY_SECONDS` default 28800, `JWT_REFRESH_THRESHOLD_SECONDS` default 900) — enterprise deployments may require shorter session lifetimes.

---

## mclaude-session-agent

Single Go binary. Runs as a container inside each K8s project pod, or as a standalone daemon on a laptop. Identical code path — the only difference is how it connects to NATS.

### What it does

- Subscribes to `mclaude.{userId}.{location}.{projectId}.api.>` — handles session CRUD, input, and control messages
- Spawns Claude Code as child processes with `--print --verbose --output-format stream-json --input-format stream-json`
- Routes stdout JSON events → NATS JetStream (raw, no envelope — subject encodes userId/projectId/sessionId)
- Routes NATS input/control messages → Claude stdin
- Publishes all stdout events to NATS unfiltered — clients decide what to render
- Tracks session state from `session_state_changed` events, writes to NATS KV
- Caches capabilities from `init` event (skills, tools, agents, model) in NATS KV, refreshes on `reload_plugins`
- Spawns terminal (PTY) sessions via `creack/pty`, routes raw I/O through NATS
- Exposes unix socket for `mclaude-cli` debug attach
- Writes heartbeat to NATS KV every 30s (staleness detection for clients)
- On startup, reads NATS KV for existing sessions → relaunches with `--resume`

No tmux. No JSONL tailing. No screen scraping. No HTTP server.

### Core loop (simplified)

```go
cmd := exec.Command("claude",
    "--print", "--verbose",
    "--output-format", "stream-json",
    "--input-format", "stream-json",
    "--include-partial-messages",
    "--session-id", sessionID)

stdin, _ := cmd.StdinPipe()
stdout, _ := cmd.StdoutPipe()

// Stdin serialization — multiple NATS messages must not interleave JSON lines.
// All writes go through a channel; a single goroutine drains it to the pipe.
stdinCh := make(chan []byte, 64)
go func() {
    for msg := range stdinCh {
        stdin.Write(msg)
        stdin.Write([]byte("\n"))
    }
}()

// stdout → NATS
go func() {
    scanner := bufio.NewScanner(stdout)
    scanner.Buffer(make([]byte, 0), 16*1024*1024) // 16MB buffer for large events
    for scanner.Scan() {
        line := scanner.Bytes()
        // Passthrough — publish all events, clients decide what to render
        nats.Publish("session."+id+".events", line)
        // Side effects: update local state from specific event types
        if eventType := parseEventType(line); eventType != "" {
            switch eventType {
            case "session_state_changed": updateKV(line)
            case "control_request": updatePendingControl(line)
            case "result": accumulateUsage(line)
            case "clear", "compact_boundary": updateReplayFromSeq(line, jetStreamSeq)
            }
        }
    }
}()

// NATS → stdin (via serialization channel)
go func() {
    sub := nats.Subscribe("session."+id+".input")
    for msg := range sub.Chan() {
        stdinCh <- msg.Data
    }
}()
```

### Permission handling

`control_request` events (subtype `can_use_tool`) are always emitted on stdout regardless of permission mode. The session agent publishes them to NATS. The client (SPA, mclaude-cli) responds with a `control_response` via the `.api.sessions.control` subject. The session agent routes it to stdin.

For auto-approve workflows (CI, batch jobs), the session agent can be configured with a permission policy that auto-responds to `control_request` events without forwarding to NATS:

```yaml
# session-agent config
permissionPolicy: "auto"  # auto-approve all tools
# permissionPolicy: "managed"  # forward to client (default)
# permissionPolicy: "allowlist"  # auto-approve listed tools, forward rest
# allowedTools: ["Bash", "Read", "Edit", "Write", "Glob", "Grep"]
```

### Graceful shutdown

On SIGTERM (pod termination):

```
1. Stop accepting new sessions
2. For each active Claude process:
   a. Send {"type": "control_request", "request": {"subtype": "interrupt"}} to stdin
   b. Wait up to 10s for process exit
   c. SIGKILL if still running
3. Flush buffered events to NATS
4. Publish lifecycle events (session_stopped for each session)
5. Close NATS connection
6. Exit 0
```

Set `terminationGracePeriodSeconds: 30` in pod spec to give enough time.

### Session operations

| NATS subject | Action |
|--------------|--------|
| `…api.sessions.create` | `exec.Command("claude", "--print", "--verbose", "--output-format", "stream-json", "--input-format", "stream-json", "--session-id", id, "-w", cwd)` |
| `…api.sessions.delete` | Send interrupt control request → wait for exit → kill if timeout |
| `…api.sessions.input` | Write user message JSON to stdin pipe |
| `…api.sessions.control` | Write control_response JSON to stdin pipe (permission approvals, interrupts, model changes) |
| `…api.sessions.restart` | Kill process → relaunch with `--resume {sessionId}` |

### Startup / recovery

```
1. Read NATS KV for all sessions with this projectId
2. For each session with a sessionId:
   claude --print --verbose --output-format stream-json --input-format stream-json --resume {sessionId}
3. Begin NATS subscriptions
4. Begin reading stdout from all child processes
```

No HTTP polling. No dependency on another service being up. Session state is in NATS KV — always available.

### Debug attach (mclaude-cli)

Session agent exposes a unix socket per session at `/tmp/mclaude-session-{id}.sock`. The `mclaude-cli` tool connects and provides a text REPL:

```bash
$ mclaude-cli attach abc-123
[session abc-123, state: idle]
> fix the failing tests
[state: running]
[assistant: I'll look at the test failures...]
[tool_use: Bash "npm test"]
[control_request: Allow Bash "npm test"? (y/n)] y
[tool_result: 3 tests passing, 1 failing...]
[assistant: The issue is in...]
> /commit -m "Fix test"
[skill expanding...]
```

Text REPL wraps input as stream-json user messages, displays assistant text, prompts on control_requests. ~150 lines of Go.

For K8s: `kubectl exec -it pod -- mclaude-cli attach {sessionId}`

### Laptop mode

On a laptop, **one session-agent per project** — same scoping as K8s. A lightweight launcher manages the per-project agents:

- `mclaude-launcher` runs as a launchd/systemd daemon
- On `projects.create` NATS message (or `mclaude start <project>` CLI), launcher spawns a session-agent for that project
- Each session-agent connects to NATS via `wss://mclaude.example.com/nats` (outbound, works behind NAT/firewall)
- Each subscribes to `mclaude.{userId}.{location}.{projectId}.api.>` — same subject structure as K8s (location is the user-assigned name for this laptop)
- NATS JWT issued by control-plane on first setup, stored in `~/.config/mclaude/creds` (shared by all agents)
- Launcher monitors child agents, restarts on crash

**Location collision**: on startup, launcher checks NATS KV for an existing `mclaude-locations/{userId}/{location}` entry. If one exists with a different machine ID, launcher exits with an error: `location "{location}" is already registered to another machine — set a unique name with: mclaude config location <name>`. If the entry matches this machine ID (e.g. crash recovery), launcher proceeds normally and overwrites the entry.

**JWT refresh**: launcher runs a background goroutine that decodes the `exp` claim from `~/.config/mclaude/creds` every 60s. When TTL falls below 15 minutes, it calls `POST /auth/refresh` and writes the new credential file. All child session-agents share the same file and reload it on their next NATS reconnect. If refresh fails (network down, server error), launcher retries with exponential backoff and logs a warning — child agents continue with the existing JWT until it expires, then reconnect with whatever credential is current.

The browser connects to the same NATS and subscribes to `mclaude.{userId}.*.{projectId}.events.>` — wildcard on location to receive events from any cluster or laptop.

### Worktrees

Branch slugification: `feature/auth` → `feature-auth` (replace `/` and non-alphanumeric with `-`, lowercase).

Session create request payload:
```json
{
  "name": "Fix auth bug",
  "branch": "feature/auth",
  "cwd": "packages/api",
  "joinWorktree": false
}
```

`branch` is optional. If omitted, the session agent derives it from `name` via slugification (`"Fix auth bug"` → `fix-auth-bug`). If both `name` and `branch` are omitted, the agent generates a default: `session-{shortId}`. Git-savvy users can specify `branch` explicitly; the SPA hides it by default and only shows the `name` field.

`joinWorktree` controls behaviour when a worktree for the branch already exists (git only allows one worktree per branch):

| `joinWorktree` | Worktree exists? | Behaviour |
|----------------|-----------------|-----------|
| `false` (default) | No | Create worktree, spawn Claude |
| `false` (default) | Yes | **Error**: "branch already has an active session — set joinWorktree: true to share the worktree" |
| `true` | No | Create worktree, spawn Claude (same as default) |
| `true` | Yes | Skip `git worktree add`, reuse `/data/worktrees/{branchSlug}`, spawn Claude |

On session create:
1. Derive branch: if `branch` is empty, slugify `name`; if `name` is also empty, use `session-{shortId}`
2. Compute `branchSlug = slugify(branch)`
3. Scan KV for any session in this projectId with the same `worktree` slug
4. If found and `joinWorktree: false` → reply with error
5. If found and `joinWorktree: true` → skip to step 7
6. If not found → `git -C /data/repo worktree add /data/worktrees/{branchSlug} {branch}`
7. Set cwd to `/data/worktrees/{branchSlug}/{cwd}`
8. Write to NATS KV with `branch` (raw), `worktree` (slug), `joinWorktree` (bool)

Every project has a bare repo at `/data/repo` — the entrypoint initializes one via `git init --bare` for scratch projects (no GIT_URL) and `git clone --bare` for git-backed projects. The session agent does not need to check whether the repo exists; it always does.

On session delete:
1. Send interrupt → wait for Claude exit
2. Scan KV bucket for all sessions in this projectId — if no other session has the same `worktree` slug: `git -C /data/repo worktree remove /data/worktrees/{branchSlug}`
3. Delete from NATS KV

---

## Event Schema

Claude Code's stream-json protocol is the canonical event schema. No protobuf translation layer. Events flow from Claude Code → NATS → clients unchanged.

Events are published as raw stream-json bytes — no envelope. The NATS subject (`mclaude.{userId}.{location}.{projectId}.events.{sessionId}`) encodes all routing metadata. JetStream adds its own timestamp. Clients parse the raw stream-json directly.

### Session state (NATS KV)

```json
{
  "id": "abc-123",
  "projectId": "proj-1",
  "branch": "feature/auth",
  "worktree": "feature-auth",
  "cwd": "/data/worktrees/feature-auth",
  "name": "Fix auth bug",
  "state": "idle",
  "stateSince": "2026-04-11T10:00:00Z",
  "createdAt": "2026-04-11T09:00:00Z",
  "model": "claude-sonnet-4-6",
  "capabilities": {
    "skills": ["commit", "review-pr", "init"],
    "tools": ["Bash", "Read", "Edit", "Write", "Glob", "Grep"],
    "agents": ["general-purpose", "Explore", "Plan"]
  },
  "pendingControls": {},
  "usage": {
    "inputTokens": 12500,
    "outputTokens": 3200,
    "cacheReadTokens": 8000,
    "cacheWriteTokens": 4500,
    "costUsd": 0.042
  },
  "replayFromSeq": 1042
}
```

`state` maps directly from stream-json `session_state_changed` events: `"idle"`, `"running"`, `"requires_action"`.

`pendingControls` is a map of `request_id → control_request` — all unanswered permission prompts for this session. The session agent adds an entry when a `control_request` is received and removes it when the `control_response` is sent. Claude Code may emit multiple simultaneous `control_request` events (parallel tool calls) — the agent forwards all of them to NATS immediately and updates KV for each. The client renders all pending prompts simultaneously. No timeout — a prompt stays open until answered, regardless of how long that takes.

`capabilities` is populated from the `init` event on session start. Refreshed when `reload_plugins` control request response is received. Client reads from KV — one read, no stream replay needed for the skills picker.

`replayFromSeq` is the JetStream sequence number from which clients should start replaying events. Updated by the session agent on `/clear` (conversation reset) and compaction (context compacted). Clients read this before subscribing — avoids replaying thousands of now-irrelevant events from before the clear/compaction boundary. If null/absent, replay from the beginning.

### Project state (NATS KV)

```json
{
  "id": "proj-1",
  "name": "mclaude",
  "gitUrl": "git@github.com:org/mclaude.git",
  "status": "running",
  "sessionCount": 2,
  "worktrees": ["main", "feature-auth"],
  "createdAt": "2026-04-01T00:00:00Z",
  "lastActiveAt": "2026-04-11T10:00:00Z"
}
```

### Session agent lifecycle events

Published on `mclaude.{userId}.{location}.{projectId}.lifecycle.{sessionId}` — separate from the stream-json event stream:

```json
{"type": "session_created", "sessionId": "abc-123", "ts": "..."}
{"type": "session_stopped", "sessionId": "abc-123", "exitCode": 0, "ts": "..."}
{"type": "session_restarting", "sessionId": "abc-123", "ts": "..."}
{"type": "session_resumed", "sessionId": "abc-123", "ts": "..."}
{"type": "session_failed", "sessionId": "abc-123", "error": "...", "ts": "..."}
{"type": "debug_attached", "sessionId": "abc-123", "ts": "..."}
{"type": "debug_detached", "sessionId": "abc-123", "ts": "..."}
```

Clients subscribe to lifecycle for session list updates (new sessions appearing, sessions dying). Subscribe to events for the active conversation view. Keeps the high-volume Claude output separate from low-volume state transitions.

---

## mclaude-control-plane

Single Go service in `mclaude-system`. The platform control plane — owns user identity, NATS credential management, and K8s resource provisioning via a kubebuilder reconciliation controller. ClusterRole for cross-namespace operations. Owns Postgres (users only). Issues NATS JWTs.

### Reconciliation Controller

The control-plane runs a controller-runtime Manager with a reconciler that continuously ensures K8s resources match the desired state in Postgres. This replaces fire-and-forget imperative provisioning — if a resource is deleted, drifted, or missing, the reconciler recreates it.

**CRD: `MCProject`** (`mclaude.io/v1alpha1`)

```yaml
apiVersion: mclaude.io/v1alpha1
kind: MCProject
metadata:
  name: {projectId}
  namespace: mclaude-system
spec:
  userId: "user-abc"
  projectId: "proj-1"
  gitUrl: "git@github.com:org/repo.git"   # empty for scratch projects
status:
  phase: "Ready"           # Pending | Provisioning | Ready | Failed
  userNamespace: "mclaude-user-abc"
  conditions:
    - type: NamespaceReady
      status: "True"
    - type: RBACReady
      status: "True"
    - type: SecretsReady
      status: "True"
    - type: DeploymentReady
      status: "True"
  lastReconciledAt: "2026-04-13T10:00:00Z"
```

**Reconcile loop** — on any MCProject create/update/delete, or on any owned resource change:

```
1. Read MCProject spec (userId, projectId, gitUrl)
2. Ensure user namespace mclaude-{userId} exists with labels
3. Ensure ServiceAccount, Role, RoleBinding in user namespace
4. Ensure user-config ConfigMap in user namespace
5. Ensure user-secrets Secret with NATS creds in user namespace
6. Ensure imagePullSecrets copied from control-plane namespace
7. Ensure project PVC (project-{projectId}) in user namespace
8. Ensure nix PVC (nix-store) in user namespace (shared across all projects)
9. Ensure project-config ConfigMap (project-{projectId}-config) with GIT_URL from spec.gitUrl
10. Ensure Deployment (project-{projectId}) in user namespace with correct spec (mounts project-config ConfigMap at /etc/mclaude/config)
11. Update MCProject status conditions and phase
```

Each step is idempotent. The reconciler **owns** all resources it creates via `controllerutil.SetControllerReference` — when an MCProject is deleted, owned resources in the user namespace are garbage-collected (except PVCs, which require explicit `?purge=true`).

**Watches**: the controller watches MCProject CRs directly. It also watches Deployments, Secrets, ConfigMaps, and ServiceAccounts in user namespaces (filtered by owner reference) — any drift triggers re-reconciliation.

**Resync**: controller-runtime's informer cache resyncs every 10 minutes by default, catching any missed events.

**Startup**: the control-plane Manager starts alongside the HTTP server. On first boot, the reconciler processes all existing MCProject CRs — this handles crash recovery and catches any resources that drifted while the control-plane was down.

**Integration with NATS API**: when `projects.create` fires, the handler creates the MCProject CR (instead of calling `ProvisionProject` directly). The reconciler picks it up and provisions resources. The NATS handler replies immediately with the projectId — it does not wait for provisioning to complete. The SPA shows project status from the MCProject status conditions.

**Integration with dev-seed**: `seedDev` creates MCProject CRs for seed projects. The reconciler provisions them.

### Session Pod Upgrades (future work)

When helm deploys a new chart, the `session-agent-template` ConfigMap gets the new image tag. The reconciler must propagate that to existing session-agent Deployments in user namespaces.

**Planned approach:**

1. Reconciler watches the `session-agent-template` ConfigMap in the control-plane namespace. On change, re-enqueues all MCProject CRs.
2. `reconcileDeployment` compares the template image against the live Deployment image. On mismatch, updates the Deployment spec. K8s handles the rolling restart.
3. Restarts are effectively free — all state lives on PVC (JSONL history, git repo, worktrees) and NATS KV (session metadata). The new pod's entrypoint reseeds `.claude` from the ConfigMap/PVC, and any interrupted session resumes via `claude --resume`.
4. `terminationGracePeriodSeconds` gives the current Claude turn time to finish before SIGTERM kills the process.

**Version pinning (future work):**

Users may want to pin a specific session-agent image (and therefore Claude Code version) per project. Planned: add `spec.imageOverride` to the MCProject CRD. When set, the reconciler uses it instead of the fleet-wide template. When empty, follows the template. The SPA exposes this as "Pin Claude version" in project settings.

### Endpoints

**Auth**

```
POST /auth/login                local credentials → NATS JWT + nkey seed
POST /auth/refresh              refresh NATS JWT
GET  /auth/sso/{provider}       initiate SSO (Entra, Okta)
GET  /auth/sso/{provider}/cb    SSO callback → NATS JWT
```

**Users (admin)**

```
POST   /users         create user + provision K8s namespace
GET    /users         list users
DELETE /users/{id}    deprovision user + delete namespace
```

**Projects**

```
POST   /api/projects           create project Deployment + PVC
DELETE /api/projects/{id}      delete project (PVC retained unless ?purge=true)
GET    /api/projects           list projects for user (reads NATS KV)
GET    /api/projects/{id}      get project status (reads NATS KV)
```

**SCIM 2.0** (enterprise IdP provisioning)

```
POST   /scim/v2/Users          IdP provisions user → create + provision namespace
PUT    /scim/v2/Users/{id}     IdP updates user
DELETE /scim/v2/Users/{id}     IdP deprovisions user → delete namespace
GET    /scim/v2/Users          IdP syncs user list
```

**Break-glass admin (not exposed via nginx)**

HTTP endpoints that mirror the NATS project API. Used only when NATS is down — normal operation goes through NATS request/reply. Access via `kubectl port-forward`:

```bash
kubectl port-forward -n mclaude-system deploy/mclaude-control-plane 9090:9090
```

```
POST   /admin/projects           create project Deployment + PVC (same as NATS projects.create)
DELETE /admin/projects/{id}      delete project
GET    /admin/projects           list projects for user (reads Postgres, not NATS KV)
POST   /admin/sessions/{id}/stop kill session (sends SIGTERM to pod)
```

These endpoints bind to a separate port (`:9090`) that is never referenced in the nginx config. No ingress rule, no external exposure. Authenticated via a static bearer token in a K8s Secret (`mclaude-admin-token` in `mclaude-system`), independent of NATS JWTs.

### User provisioning flow

```
1. User created (local POST /users, SSO first login, or SCIM push)
2. INSERT into Postgres users table
3. (User namespace, RBAC, secrets are created by the reconciler when the first MCProject CR is created for this user)
```

User-level K8s resources (namespace, ServiceAccount, Role, RoleBinding, user-config, user-secrets) are provisioned by the reconciliation controller as a side effect of reconciling the user's first MCProject. This avoids creating empty namespaces for users who never create a project.

### User deprovision flow

```
1. DELETE /users/{id} (or SCIM DELETE)
2. Revoke user's NATS JWT: add user NKey to account JWT revocations, push updated account JWT to NATS server
3. NATS broker terminates all active connections for that user immediately
4. DELETE from Postgres users table (cascades to nats_credentials)
5. Delete all MCProject CRs for the user — reconciler garbage-collects owned resources
6. kubectl delete namespace mclaude-{userId}  ← catches anything the reconciler didn't own
```

JWT revocation is NATS-native: control-plane holds the account NKey and can push a signed revocation to the NATS server at any time. Existing connections are dropped within one server tick. No 8h window.

### Project provisioning flow

```
1. Client publishes mclaude.{userId}.api.projects.create
2. control-plane receives via NATS request/reply
3. Create MCProject CR in mclaude-system namespace
4. Write Project JSON to NATS KV mclaude-projects/{userId}/{projectId}
5. Reply with projectId immediately (don't wait for provisioning)
6. Reconciler picks up MCProject CR → creates namespace, RBAC, secrets, PVCs, Deployment
7. Pod starts, session-agent connects to NATS, begins subscriptions
```

The SPA can watch MCProject status conditions (via NATS KV or direct status) to show provisioning progress.

### Postgres schema

```sql
CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    display_name  TEXT NOT NULL,
    email         TEXT UNIQUE,
    password_hash TEXT,              -- null for SSO-only users
    google_id     TEXT UNIQUE,
    created_at    TIMESTAMPTZ DEFAULT now(),
    last_login_at TIMESTAMPTZ
);

CREATE TABLE nats_credentials (
    user_id    TEXT REFERENCES users(id) ON DELETE CASCADE,
    nkey_seed  TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now()
);
```

Schema migrations managed by dbmate init container on control-plane Deployment.

---

## Pod Structure (one per project)

```
Pod: project-{projectId}            namespace: mclaude-{userId}
├── container: session-agent
│   image: mclaude-session-agent:{version}
│   ├── project PVC      → /data/              (RW) repo, worktrees, shared-memory
│   ├── nix-store PVC    → /nix/               (RWX) shared Nix store (per-namespace)
│   ├── claude-home      → ~/.claude/           (RW) emptyDir, ephemeral
│   ├── user-config      → ~/.claude-seed/      (RO) ConfigMap seed
│   └── user-secrets     → ~/.user-secrets/     (RO) Secret
├── container: config-sync
│   image: mclaude-config-sync:{version}
│   watches ~/.claude/ → patches user-config ConfigMap on change
└── container: dockerd-rootless     (optional — per-project flag)
    image: docker:dind-rootless
    # Validate rootless Docker works on target AKS nodes before enabling.
    # Default: disabled.
```

`/data/` layout:

```
/data/
  repo/             bare git repo
  worktrees/
    main/
    feature-auth/
  shared-memory/    auto-memory symlinked across all worktrees
  projects/         symlinked to ~/.claude/projects/ (JSONL history — Claude's own, for --resume)
```

---

## 3-Tier Storage

| Tier | Scope | Storage | Contents |
|------|-------|---------|----------|
| **User** | Per namespace | ConfigMap + Secret | CLAUDE.md, settings.json, skills, commands, credentials |
| **Project** | Per project | PVC (RWO, managed-csi-premium) | Bare git repo, worktrees, JSONL (Claude's own persistence), shared memory |
| **Session** | Per session | NATS KV | id, branch, worktree, cwd, state, capabilities, pendingControls |
| **Home** | Per pod | emptyDir | Seeded from ConfigMap + Secret on boot. Ephemeral. |

---

## Home Directory + Config Sync

`$HOME` is an emptyDir — fresh on every pod start, writable, not persisted. Credentials belong in K8s Secrets, not on browsable storage.

**On boot**, entrypoint seeds `$HOME` from:
- K8s Secret: SSH keys, OAuth token, `.gitconfig`
- ConfigMap: `settings.json`, `CLAUDE.md`, commands, skills

**config-sync sidecar** watches `~/.claude/settings.json` and `CLAUDE.md` for writes via inotify. On change, patches the `user-config` ConfigMap. Survives pod restarts via re-seeding.

`mclaude-config-sync` is a **dedicated image** with inotify-tools, kubectl, and jq pre-installed. Do not use runtime `apk add` — it fails in air-gapped environments.

---

## Managed Platform Config

Global CLAUDE.md at `/etc/claude-code/CLAUDE.md` — loaded before all user config, cannot be excluded.

```markdown
# MClaude Platform

## Environment
You are running in a Kubernetes pod.
- `/data/repo/` — bare git repo
- `/data/worktrees/{branch}/` — git worktrees
- `/data/shared-memory/` — auto-memory shared across worktrees
- `$HOME` is ephemeral — rebuilt on every pod restart

## Git
Branch switching is managed by the platform. Do not use `git checkout` or `git switch`.
The bare repo is at `/data/repo/`. Do not modify it directly.

## Tool Installation
Use `pkg install <package>`. Do not use `apt install` or `apt-get`.
Tools are cached in the shared Nix store and persist across pod restarts.

## Shell
- `~/.zshrc.local` for ephemeral shell additions (not synced)
- `~/.env.secrets` for credentials (sourced by .zshrc, written by entrypoint)
- Do not write secrets to `~/.zshrc` — it syncs to ConfigMap

## Docker
Docker is available via `DOCKER_HOST` if enabled for this project.
```

Platform hooks enforce constraints at execution time:

```bash
#!/bin/bash
# /etc/claude-code/hooks/guard.sh
COMMAND=$(cat | jq -r '.input.command // empty')

if echo "$COMMAND" | grep -qE '^\s*git\s+(checkout|switch)\s'; then
    echo "BLOCK: Branch switching is managed by the platform." >&2
    exit 2
fi
if echo "$COMMAND" | grep -qE '(^|\s|/)(apt-get|apt)\s+install'; then
    echo "BLOCK: Use 'pkg install <package>' instead." >&2
    exit 2
fi
if echo "$COMMAND" | grep -qE '/etc/claude-code/'; then
    echo "BLOCK: Managed platform config cannot be modified." >&2
    exit 2
fi
if echo "$COMMAND" | grep -qE 'rm\s+(-rf|-fr)\s+/(data/repo|nix|etc)\b'; then
    echo "BLOCK: Cannot delete platform-managed directories." >&2
    exit 2
fi
exit 0
```

---

## Tool Installation (Nix)

Nix store (`/nix/`) lives on an Azure Files PVC (RWX) — one per namespace, shared across all project pods. Install a tool in any pod and it's immediately available in all pods for that user.

```bash
# /usr/local/bin/pkg — shim
if [ "$1" = "install" ]; then
    shift; for p in "$@"; do nix profile install "nixpkgs#$p"; done
elif [ "$1" = "remove" ]; then
    shift; for p in "$@"; do nix profile remove "$p"; done
fi
```

`apt` and `brew` are shimmed to `pkg`. Users who want devbox, mise, etc. install via `pkg install devbox`.

---

## Entrypoint (session-agent container)

```bash
#!/bin/bash
set -e

# Consume secrets
[ -f "/home/node/.user-secrets/id_rsa" ] && {
    mkdir -p "$HOME/.ssh" && chmod 700 "$HOME/.ssh"
    cp /home/node/.user-secrets/id_rsa "$HOME/.ssh/id_rsa"
    chmod 600 "$HOME/.ssh/id_rsa"
    ssh-keyscan github.com >> "$HOME/.ssh/known_hosts" 2>/dev/null || true
}
[ -f "/home/node/.user-secrets/.gitconfig" ] && \
    cp /home/node/.user-secrets/.gitconfig "$HOME/.gitconfig"
[ -f "/home/node/.user-secrets/oauth-token" ] && \
    export CLAUDE_CODE_OAUTH_TOKEN=$(cat /home/node/.user-secrets/oauth-token)

# Seed user config (emptyDir is fresh each boot — always copy)
for f in CLAUDE.md settings.json; do
    [ -f "/home/node/.claude-seed/$f" ] && \
        cp "/home/node/.claude-seed/$f" "$HOME/.claude/$f"
done
for d in commands skills; do
    [ -d "/home/node/.claude-seed/$d" ] && \
        cp -r "/home/node/.claude-seed/$d" "$HOME/.claude/$d"
done

# Link JSONL history to PVC (Claude's own persistence for --resume)
mkdir -p /data/projects
ln -sf /data/projects "$HOME/.claude/projects"

# Skip onboarding. bypassPermissions disables Claude Code's built-in permission dialogs —
# guard hooks (platform-level enforcement) are the permission layer in pods, not Claude Code's UI prompts.
echo '{"hasCompletedOnboarding":true,"bypassPermissions":true}' > "$HOME/.claude.json"

# Git setup (bare repo — worktrees created by session agent)
# Every project gets a bare repo. Git-backed projects clone from GIT_URL;
# scratch projects (no GIT_URL) get an empty bare repo initialized in place.
# This means the session agent's worktree machinery works uniformly for all projects.
if [ ! -d "/data/repo/HEAD" ]; then
    if [ -n "$GIT_URL" ]; then
        git clone --bare "$GIT_URL" /data/repo || {
            echo "[entrypoint] Git clone failed — exiting for restart"
            exit 1
        }
    else
        git init --bare /data/repo
        # Create an initial empty commit so worktrees have something to branch from.
        git -C /data/repo commit --allow-empty -m "init" \
            --author="mclaude <mclaude@local>" 2>/dev/null || true
    fi
else
    if [ -n "$GIT_URL" ]; then
        git -C /data/repo fetch --all --prune || true
    fi
fi
mkdir -p /data/worktrees

# Shared memory — symlink each worktree's memory dir to /data/shared-memory/
mkdir -p /data/shared-memory
(while true; do
    for dir in "$HOME/.claude/projects"/*/; do
        [ -d "$dir" ] && [ ! -L "${dir}memory" ] && {
            rm -rf "${dir}memory"
            ln -s /data/shared-memory "${dir}memory"
        }
    done
    sleep 5
done) &

# Wait for dockerd if enabled
[ "${DOCKER_ENABLED}" = "true" ] && \
    while [ ! -S /var/run/docker.sock ]; do sleep 0.5; done

# Hand off to session agent — no tmux, spawns Claude as child processes
exec session-agent \
    --nats-url    "${NATS_URL}" \
    --nats-creds  "/home/node/.user-secrets/nats-creds" \
    --user-id     "${USER_ID}" \
    --project-id  "${PROJECT_ID}" \
    --data-dir    /data \
    --mode        k8s
```

The `session-agent` binary handles everything after setup: NATS subscription, session recovery from KV, Claude process management, event routing.

---

## Web SPA / Client

Mobile browser first — enterprise constraint requires the client to work in a mobile browser (native apps cannot reach the VPN).

- **iOS Safari, Android Chrome** — primary
- **Desktop browser** — same SPA
- No iOS app, no Electron, no Flutter (deferred)

**Framework**: React 18 (chosen).

**PTT**: WebSpeech API. On iOS Safari this calls `SFSpeechRecognizer` natively — same quality as a native iOS app. On Android Chrome, Google's speech recognition. Requires HTTPS + mic permission.

**Real-time events**: client connects to NATS via `/nats` WebSocket proxy. Subscribes to `mclaude.{userId}.*.{projectId}.events.>` (wildcard on location). Events are raw stream-json from Claude Code — the client renders them directly.

**Rendering**: the SPA consumes stream-json event types:
- `stream_event` (content_block_delta) → live streaming text as Claude types (token-by-token)
- `assistant` → complete message (final, replaces streamed deltas)
- `tool_use` → collapsible block with tool name and input summary
- `tool_progress` → elapsed time indicator on running tools ("Bash running… 30s")
- `control_request` → approve/deny buttons (permission prompt)
- `system.session_state_changed` → status indicator (idle/running/requires_action)
- `system.init` → populate skills picker, tool list, model info
- `result` → turn complete, show usage/cost
- Events with `parent_tool_use_id` → render nested under parent Agent block

**Model/effort switching**: SPA can send `set_model` and `set_max_thinking_tokens` control requests mid-session. Model picker in session header reads available models from `init` event.

**Cost tracking**: `result` events include `usage` (input/output tokens, cache read/write, cost). Session agent accumulates these in NATS KV session state. SPA shows per-session and per-project cost in the UI.

**File/image uploads**: SPA sends images as base64 in the user message content array: `[{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "..."}}]`. Max ~20MB per image (Anthropic API limit). Screenshots from clipboard paste supported.

**State**: client watches NATS KV buckets `mclaude-sessions` and `mclaude-projects` for live updates.

**Event replay**: on reconnect or tab foreground, client re-subscribes from `max(lastSeenSeq + 1, replayFromSeq)`. On fresh load, reads `replayFromSeq` from session KV and subscribes from there — avoids replaying events from before the last `/clear` or compaction. No stale cache — client always knows its position in the stream.

**Deduplication**: delivery is at-least-once — a session agent reconnecting to NATS after an outage may re-publish events the client already received. Client must deduplicate by JetStream sequence number: skip any event whose sequence number is ≤ `lastSeenSeq`.

**Skills picker**: populated from the `init` event's `skills` array (cached in NATS KV session state). Refreshed via `reload_plugins` when skills change mid-session.

**Background reconnect** (mobile browser):
```js
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState !== 'visible') return;
  // iOS silently kills NATS WebSocket when tab backgrounds
  nc.reconnect();
  // Re-watch KV to catch missed state updates
  kv.watch(`{userId}/>`);
});
```

---

## Terminal Access

### Interactive terminal (PTY) sessions

The session agent manages two types of sessions:

1. **Claude sessions** — headless stream-json, JSON on stdin/stdout
2. **Terminal sessions** — interactive PTY, raw bytes on stdin/stdout

Terminal sessions are spawned via the same NATS API:

```
mclaude.{userId}.{location}.{projectId}.api.terminal.create    → spawn shell
mclaude.{userId}.{location}.{projectId}.api.terminal.delete     → kill terminal
mclaude.{userId}.{location}.{projectId}.api.terminal.resize     → resize PTY
```

The session agent spawns a shell using `creack/pty` (Go PTY library):

```go
cmd := exec.Command("/bin/zsh")
cmd.Dir = "/data/worktrees/" + branch
cmd.Env = append(os.Environ(), "TERM=xterm-256color")
ptmx, _ := pty.Start(cmd)

// PTY output → NATS (raw bytes)
go func() {
    buf := make([]byte, 4096)
    for {
        n, _ := ptmx.Read(buf)
        nats.Publish("terminal."+id+".output", buf[:n])
    }
}()

// NATS → PTY input (raw bytes)
go func() {
    sub := nats.Subscribe("terminal."+id+".input")
    for msg := range sub.Chan() {
        ptmx.Write(msg.Data)
    }
}()
```

NATS subjects for terminal I/O:

```
mclaude.{userId}.{location}.{projectId}.terminal.{termId}.output    → raw PTY output bytes
mclaude.{userId}.{location}.{projectId}.terminal.{termId}.input     → raw keyboard input bytes
```

These are **not** JetStream — raw terminal I/O is ephemeral, no replay needed. Use core NATS pub/sub for low latency.

**Message size**: the read buffer is 4096 bytes, so each NATS pub is at most 4KB — nowhere near the 8MB `max_payload` limit. High-throughput output (e.g. `cat bigfile`) produces many small messages; since the subject is ephemeral (not persisted), this is fine.

The web SPA renders terminal sessions with **xterm.js**:
- Subscribes to `.terminal.{termId}.output` → feeds bytes to xterm.js
- Captures keyboard input from xterm.js → publishes to `.terminal.{termId}.input`
- Sends resize events on window resize → session agent calls `pty.Setsize()`

Users can:
- Open a terminal tab alongside Claude session tabs in the SPA
- Run manual commands (`npm test`, `git log`, inspect files)
- Debug issues that Claude can't solve
- Run interactive tools (`vim`, `ssh`, `top`)

Terminal sessions share the same filesystem as Claude sessions — same worktree, same `/data/`, same Nix tools.

`kubectl exec` remains available as a fallback but is not the primary interface.

### Real-time Claude Output

In headless stream-json mode, there is no terminal showing Claude's TUI. The implications:

### What streams in real-time

| Event type | Latency | Content |
|-----------|---------|---------|
| `stream_event` (content_block_delta) | Token-by-token | Claude's text as it types — live streaming |
| `tool_use` | Instant | Tool name + input (shows immediately when Claude decides to use a tool) |
| `tool_progress` | Periodic (~5s) | Elapsed time only ("Bash running… 30s") — **no stdout** |
| `tool_result` | After completion | Full stdout/stderr from Bash, file contents from Read, etc. |
| `control_request` | Instant | Permission prompt (approve/deny) |
| `session_state_changed` | Instant | State transitions |

### The gap: long-running Bash commands

When Claude runs a 5-minute build, the user sees:
1. `tool_use: Bash "npm run build"` — instant
2. `tool_progress: elapsed 5s… 10s… 30s…` — heartbeats, no output
3. `tool_result: <full build output>` — after 5 minutes

The build output appears all at once, not streaming. This matches Claude Code's TUI behavior (the TUI also shows a spinner during Bash execution, not live output). But it's a worse UX than a raw terminal.

### Mitigations

**For the SPA**: show an animated elapsed-time indicator from `tool_progress` events. When `tool_result` arrives, render the full output with syntax highlighting. For very long outputs, render collapsed with expand-on-click.

**For mclaude-cli (debug attach)**: same behavior — show elapsed time, then full output. Users who need live streaming can `kubectl exec` into the pod and run commands directly (the session agent doesn't prevent this — it just won't see that activity).

**Future**: if Claude Code adds stdout streaming to `tool_progress` events (or a new `tool_output` event type), the architecture supports it immediately — it's just another JSON event on stdout that flows through NATS to the client. No architectural change needed.

**Context usage**: the `get_context_usage` control request returns current context window utilization. The SPA can poll this periodically and show a context meter (useful for long sessions approaching the limit).

---

## nginx Ingress

```nginx
location /nats {
    proxy_pass         http://nats.mclaude-system:8080;
    proxy_http_version 1.1;
    proxy_set_header   Upgrade    $http_upgrade;
    proxy_set_header   Connection "upgrade";
    proxy_read_timeout 3600s;
}
location /auth { proxy_pass http://mclaude-control-plane:8080; }
location /api  { proxy_pass http://mclaude-control-plane:8080; }
location /scim { proxy_pass http://mclaude-control-plane:8080; }
location /     { proxy_pass http://mclaude-spa:80; }
```

No auth logic. No routing decisions. Bytes in, bytes out.

---

## Image Build Pipeline

All images tagged with semver. Never `:latest` in production. Push to main → build → push to Artifactory with git SHA + semver tag. Semver bump triggers promotion.

| Image | Contents |
|-------|----------|
| `mclaude-session-agent:{v}` | session-agent binary, mclaude-cli binary, Claude CLI, git, Nix, zsh, pkg shim, guard hooks |
| `mclaude-control-plane:{v}` | control-plane binary, kubectl, dbmate |
| `mclaude-config-sync:{v}` | inotify-tools, kubectl, jq — pre-installed, no runtime package installs |

Note: tmux is no longer in the session-agent image.

---

## Health Probes

All pods have liveness + readiness probes. Kubernetes restarts on failure.

**session-agent pod:**
```yaml
livenessProbe:
  exec:
    command: ["session-agent", "--health"]  # checks process alive + NATS connection
  initialDelaySeconds: 10
  periodSeconds: 30

readinessProbe:
  exec:
    command: ["session-agent", "--ready"]  # checks NATS connected + can spawn claude
  initialDelaySeconds: 5
  periodSeconds: 10
```

**control-plane pod:**
```yaml
livenessProbe:
  httpGet:
    path: /health
    port: 8080
  periodSeconds: 15

readinessProbe:
  httpGet:
    path: /ready  # checks Postgres connection only — NATS outage must not mark pod unready
    port: 8080
  periodSeconds: 10
```

`/health` (liveness) never checks NATS — the pod must stay alive and port-forwardable even when NATS is down, so the break-glass admin port (`:9090`) remains reachable.

**NATS pod:** use the official NATS Helm chart — includes probes by default.

---

## Reliability

**Postgres unavailability** (control-plane): retry with exponential backoff. Login endpoints return 503 while Postgres is unreachable. NATS JWTs already issued remain valid.

**NATS unavailability** (session-agent): buffer state changes in memory, flush on reconnect. Claude processes continue running — sessions are not affected by NATS downtime. Stdout events are buffered and published when connection restores. Delivery is **at-least-once** — events published just before disconnect may be re-published on reconnect (JetStream re-delivery from last ack). Clients deduplicate by JetStream sequence number.

**Pod restart — graceful** (session-agent): graceful shutdown runs (see above). On startup, read NATS KV for existing sessions, relaunch with `--resume`. Claude Code reads its own JSONL from PVC to restore conversation context. No dependency on any other service being up.

**Pod restart — ungraceful** (SIGKILL, node failure, OOM): graceful shutdown does NOT run. Stale state left behind:
- NATS KV session entries still show old state (e.g. `running`)
- `pendingControls` may contain stale permission prompts
- No `session_stopped` lifecycle events published
- Buffered but unpublished events lost
- Terminal (PTY) sessions are gone (ephemeral, no recovery)
- JSONL on PVC is fine (persistent storage)

Recovery sequence on startup after ungraceful termination:

```
1. Read KV for all sessions with this projectId
2. Set all session KV entries to state: "restarting", clear pendingControls
3. Publish session_restarting lifecycle events
4. For each session: claude --resume {sessionId}
5. On init event received: update KV with fresh state from Claude
6. Publish session_resumed lifecycle events
7. Sessions that fail to start within 30s: mark state: "failed", publish session_failed
```

Clearing `pendingControls` in step 2 is safe: on `--resume`, Claude Code sees the interrupted tool call in its conversation history and re-emits the `control_request` naturally. The client receives a fresh prompt and responds normally.

This is the same sequence as a graceful restart — the agent doesn't distinguish between the two. It always re-derives state from Claude Code on startup.

**Staleness detection** (heartbeats): the session agent writes a heartbeat to NATS KV every 30s:

```
KV bucket: mclaude-heartbeats
  key: {userId}/{projectId}  →  {"ts": "2026-04-11T10:00:30Z"}
```

Clients check `now - lastHeartbeat > 60s` → agent is dead or unreachable, show "reconnecting" in the UI. When heartbeats resume after pod restart, client clears the warning and re-reads session state from KV.

**Claude process crash**: session-agent detects child process exit, publishes lifecycle event, updates NATS KV state. Auto-restart with `--resume` if exit was unexpected (non-zero, no interrupt signal).

**Git clone failure**: entrypoint exits non-zero. Deployment restart policy retries. control-plane polls pod status and reflects `PROJECT_STATUS_FAILED` in NATS KV. Client shows error.

**Image tagging**: semver. A bad image push does not auto-deploy. Rollback is `kubectl set image` to previous semver tag.

**JSONL cleanup**: Claude Code accumulates JSONL files on the project PVC (`/data/projects/`). The session agent runs a daily cleanup job: delete JSONL files older than 90 days, delete session files for sessions not in NATS KV. Monitor PVC usage and alert at 80% capacity.

**Claude Code version pinning**: pin `@anthropic-ai/claude-code@{version}` in the session-agent Dockerfile. Test stream-json protocol compatibility before upgrading. The protocol is used by IDE extensions (VS Code, JetBrains) so it's likely stable, but breaking changes are possible across major versions.

---

## Testing

### Local development

k3d cluster with NATS, Postgres, nginx. Session agent runs locally against the k3d NATS. Claude Code runs on the dev machine (not in k3d — needs API key). Test the full event flow: spawn session → send input → receive events.

### Integration tests (CI)

```
1. Deploy NATS + Postgres to test namespace
2. Deploy control-plane, create test user + project
3. Deploy session-agent with mock Claude process
   (mock: reads stdin JSON, emits canned stream-json events on stdout)
4. Run test suite:
   - Session CRUD (create, list, delete via NATS)
   - Event routing (mock emits events → verify they arrive on NATS subject)
   - Permission flow (mock emits control_request → test client responds → verify control_response reaches mock stdin)
   - State tracking (mock emits session_state_changed → verify NATS KV updated)
   - Recovery (kill mock process → verify session-agent restarts it)
   - Lifecycle events (verify created/stopped events on lifecycle subject)
5. Teardown
```

The mock Claude process is a ~50-line Go program that replays a canned stream-json transcript. This tests the session agent without needing a real Claude API key.

### E2E tests

Full stack with real Claude Code (requires API key, run manually or in a gated CI stage):
- Create session → send "echo hello" → verify tool_use + control_request + tool_result events
- Resume session → verify conversation context restored
- Skills: send "/commit" → verify skill expansion
- Concurrent sessions on same project

---

## Observability

OTEL stack is already on the cluster. All components export to it.

**Metrics** (Prometheus/OTEL):
- Per-user: active sessions, events/sec, project count
- NATS: message rate, stream lag, KV operations
- control-plane: auth latency, provisioning latency, SCIM sync rate
- Session agent: Claude process count, restart count, event throughput

**Logging**: structured JSON to stdout. Labels: `userId`, `projectId`, `sessionId`.

**FinOps**:
- Compute cost per user: CPU/memory × time
- Storage per user: PVC GiB
- Alert on idle PVCs (no sessions for >7 days, PVC still allocated)

**Cost estimate per user** (2 active projects):

| Resource | Monthly cost |
|----------|-------------|
| Project pod ×2 (200m CPU, 512Mi request / 4 CPU, 8Gi limit) | ~$12 |
| Project PVC ×2 (20Gi premium) | ~$6 |
| Nix store PVC (20Gi Azure Files) | ~$1.20 |
| NATS share (per-user estimate) | ~$1 |
| Postgres share (per-user estimate) | ~$0.50 |
| **Total** | **~$21/month** |

---

## Artifactory / Registry Configuration

Enterprise deployments pull all images and packages through Artifactory. Session-agent reads a `registry-mirrors` ConfigMap published by the platform and runs hook scripts to configure each package manager.

```json
// mirrors.json key in ConfigMap
[
  {
    "origin": "https://registry.npmjs.org",
    "mirror": "https://npm.artifactory.example.com/",
    "type": "npm",
    "auth": { "secretRef": { "name": "artifactory-creds", "key": "token" } }
  }
]
```

Hook scripts in `/etc/mclaude/hooks.d/` read `mirrors.json` and write tool-specific config on pod start. On personal laptop, env vars are empty → hooks skip → public defaults used.

---

## Kubernetes Resources

### User namespace (applied by control-plane on provisioning)

```yaml
# Namespace
apiVersion: v1
kind: Namespace
metadata:
  name: mclaude-{userId}
---
# ServiceAccount for project pods
apiVersion: v1
kind: ServiceAccount
metadata:
  name: mclaude-sa
  namespace: mclaude-{userId}
---
# Role: project pods only need to read their own namespace secrets/configmaps
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: mclaude-role
  namespace: mclaude-{userId}
rules:
  - apiGroups: [""]
    resources: ["configmaps"]
    resourceNames: ["user-config"]
    verbs: ["get", "watch", "patch"]
  - apiGroups: [""]
    resources: ["secrets"]
    resourceNames: ["user-secrets"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: mclaude-role
  namespace: mclaude-{userId}
subjects:
  - kind: ServiceAccount
    name: mclaude-sa
roleRef:
  kind: Role
  name: mclaude-role
  apiGroup: rbac.authorization.k8s.io
```

### Project Deployment (applied by control-plane)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: project-{projectId}
  namespace: mclaude-{userId}
  labels:
    app: mclaude-project
    project: "{projectId}"
spec:
  replicas: 1
  selector:
    matchLabels:
      app: mclaude-project
      project: "{projectId}"
  template:
    spec:
      serviceAccountName: mclaude-sa
      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        fsGroup: 1000
      volumes:
        - name: project-data
          persistentVolumeClaim:
            claimName: project-{projectId}
        - name: claude-home
          emptyDir: {}
        - name: user-config
          configMap:
            name: user-config
        - name: user-secrets
          secret:
            secretName: user-secrets
        - name: nix-store
          persistentVolumeClaim:
            claimName: nix-store
      containers:
        - name: session-agent
          image: mclaude-session-agent:{version}
          env:
            - name: GIT_URL
              value: "{gitUrl}"
            - name: USER_ID
              value: "{userId}"
            - name: PROJECT_ID
              value: "{projectId}"
            - name: NATS_URL
              value: "nats://nats.mclaude-system:4222"
            - name: HTTPS_PROXY
              value: "{proxyUrl}"   # omit if no egress restriction
          volumeMounts:
            - name: project-data
              mountPath: /data
            - name: nix-store
              mountPath: /nix
            - name: claude-home
              mountPath: /home/node/.claude
            - name: user-config
              mountPath: /home/node/.claude-seed
              readOnly: true
            - name: user-secrets
              mountPath: /home/node/.user-secrets
              readOnly: true
          resources:
            requests:
              cpu: 200m
              memory: 512Mi
            limits:
              cpu: 4000m
              memory: 8Gi
          livenessProbe:
            exec:
              command: ["session-agent", "--health"]
            initialDelaySeconds: 10
            periodSeconds: 30
          readinessProbe:
            exec:
              command: ["session-agent", "--ready"]
            initialDelaySeconds: 5
            periodSeconds: 10
        - name: config-sync
          image: mclaude-config-sync:{version}
          # Dedicated image — inotify-tools + kubectl + jq pre-installed.
          # Never use runtime apk installs (fails in air-gapped environments).
          command: ["/bin/sh", "-c"]
          args:
            - |
              echo "[config-sync] Watching /claude-home/ for changes..."
              while true; do
                inotifywait -qq -r -e close_write /claude-home/ 2>/dev/null || sleep 30
                sleep 1
                PATCH="{\"data\":{"
                SEP=""
                for f in settings.json CLAUDE.md; do
                  if [ -f "/claude-home/$f" ]; then
                    PATCH="${PATCH}${SEP}\"$f\":$(jq -Rs . < "/claude-home/$f")"
                    SEP=","
                  fi
                done
                PATCH="${PATCH}}}"
                kubectl patch configmap user-config -n "$NAMESPACE" -p "$PATCH" 2>/dev/null && \
                  echo "[config-sync] Synced" || echo "[config-sync] Sync failed (will retry)"
              done
          env:
            - name: NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
          volumeMounts:
            - name: claude-home
              mountPath: /claude-home
              readOnly: true
          resources:
            requests:
              cpu: 10m
              memory: 32Mi
            limits:
              cpu: 100m
              memory: 64Mi
        # dockerd-rootless is optional — enabled per-project via DOCKER_ENABLED env var.
        # Omit this container for projects that don't need Docker.
        # Validate rootless Docker works on target AKS nodes before enabling.
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: project-{projectId}
  namespace: mclaude-{userId}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: managed-csi-premium
  resources:
    requests:
      storage: 20Gi
```

### Nix store PVC (one per namespace)

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: nix-store
  namespace: mclaude-{userId}
spec:
  accessModes: [ReadWriteMany]
  storageClassName: azurefile-csi
  resources:
    requests:
      storage: 20Gi
```

### Network policy

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: deny-cross-namespace
  namespace: mclaude-{userId}
spec:
  podSelector: {}
  policyTypes: [Ingress]
  ingress:
    - from:
        - podSelector: {}
    - from:
        - namespaceSelector:
            matchLabels:
              name: mclaude-system
```

---

## control-plane ClusterRole

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: mclaude-control-plane
rules:
  - apiGroups: ["mclaude.io"]
    resources: ["mcprojects", "mcprojects/status"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: [""]
    resources: ["namespaces"]
    verbs: ["get", "list", "create", "delete"]
  - apiGroups: [""]
    resources: ["secrets", "configmaps", "serviceaccounts", "persistentvolumeclaims"]
    verbs: ["get", "list", "create", "update", "delete"]
  - apiGroups: ["apps"]
    resources: ["deployments", "deployments/scale"]
    verbs: ["get", "list", "create", "update", "patch", "delete"]
  - apiGroups: ["rbac.authorization.k8s.io"]
    resources: ["roles", "rolebindings"]
    verbs: ["get", "list", "create", "update", "delete"]
  - apiGroups: ["networking.k8s.io"]
    resources: ["networkpolicies"]
    verbs: ["get", "list", "create", "update", "delete"]
```

---

## HTTPS Proxy (if cluster egress is blocked)

If the cluster cannot reach `api.anthropic.com` directly:

```bash
sudo dnf install -y squid
sudo tee /etc/squid/squid.conf << 'EOF'
acl allowed_dst dstdomain api.anthropic.com
acl CONNECT method CONNECT
http_access allow CONNECT allowed_dst
http_access deny all
http_port 3128
EOF
sudo systemctl enable --now squid
```

All project pods set `HTTPS_PROXY` env var. Claude Code respects it natively. Omit the env var for environments with direct egress.

---

## Implementation Order

**Phase 1 — Infrastructure:**
1. NATS cluster (3-node StatefulSet in `mclaude-system`, JetStream enabled, NATS Helm chart)
2. Postgres in `mclaude-system` (users table only, dbmate migrations)
3. nginx ingress with routing rules

**Phase 2 — mclaude-control-plane:**
4. Local auth — login, NATS JWT issuance, NATS Operator + Account setup
5. User CRUD + K8s namespace provisioning
6. Project Deployment + PVC provisioning
7. SSO — Entra/Okta OIDC
8. SCIM 2.0

**Phase 3 — mclaude-session-agent:**
9. Build session image (Dockerfile: Claude CLI, git, Nix, guard hooks — no tmux)
10. Claude process management (spawn headless, stdin/stdout pipes, lifecycle tracking)
11. NATS subscription + event routing (stdout → JetStream, NATS → stdin)
12. NATS KV state management (from session_state_changed events)
13. Session recovery on pod restart (read KV → relaunch with --resume)
14. Debug attach unix socket + mclaude-cli
15. Laptop mode (standalone daemon, same binary)

**Phase 4 — Web SPA:**
16. ~~Framework decision~~ (React 18 — decided)
17. Auth flow — login → NATS JWT
18. NATS direct subscription — stream-json events + KV state watches
19. Stream-json renderer (assistant text, tool use, control requests, thinking)
20. Skills picker (from init event capabilities)
21. PTT — WebSpeech API

**Phase 5 — Hardening:**
22. Semver CI/CD pipeline (GitHub Actions on GHES)
23. Network policies
24. Registry mirrors (Artifactory hooks in session image)
25. Observability — OTEL export, FinOps dashboard
26. `hostUsers: false` — validate on AKS nodes, add to pod spec if confirmed

---

## Verification

**Provisioning:**
1. `POST /users` → namespace, RBAC, NATS credentials created
2. `POST /api/projects` → Deployment + PVC in user namespace, Project in NATS KV
3. SCIM push from IdP → user provisioned automatically

**Session lifecycle:**
4. `mclaude.{userId}.{location}.{projectId}.api.sessions.create` → Claude process spawned, Session in NATS KV, `init` event received
5. Send input via NATS → appears as user message in stream-json output
6. `session_state_changed` events flow through NATS → client updates status
7. `control_request` for tool approval → client shows approve/deny → `control_response` sent → tool executes
8. Kill project pod → Deployment restarts, session-agent reads KV, relaunches with `--resume`, conversation restored
9. Delete session → Claude process interrupted and exited, worktree cleaned up if last session on branch

**Skills:**
10. Send `/commit -m "test"` as user message → skill expands, tool permissions flow through control protocol
11. Send `reload_plugins` control request → response includes updated skills list

**Multi-session:**
12. Two sessions on same branch with `joinWorktree: true` → share `/data/worktrees/{slug}/`
13. Claude learns feedback in session A → memory at `/data/shared-memory/` → available in session B on different branch

**Security:**
14. User alice's JWT → cannot subscribe to `mclaude.bob.>` (NATS broker rejects)
15. User alice's JWT → can subscribe to `mclaude.alice.>` (allowed)

**Debug:**
16. `kubectl exec -it pod -- mclaude-cli attach {sessionId}` → interactive text REPL, can send messages and approve tools
17. `mclaude-cli` detach → session continues, lifecycle event published

**Laptop:**
18. Laptop session-agent connects via `/nats` proxy, same event flow as K8s
19. Browser subscribes to laptop session events directly via NATS

**Reliability:**
20. NATS connection drops → session-agent buffers, reconnects, flushes
21. Claude process crashes → session-agent detects, publishes lifecycle event, auto-restarts with --resume
22. Bad image deployed → semver rollback with `kubectl set image`

---

## Critical Files

```
mclaude-session-agent/
  main.go               NATS subscriber, Claude process manager, event router
  session.go            Claude process lifecycle (spawn, stdin/stdout pipes, restart)
  router.go             Stream-json event routing (stdout → NATS, NATS → stdin)
  state.go              NATS KV state tracking (from session_state_changed events)
  terminal.go           PTY session management (creack/pty, raw I/O via core NATS)
  worktree.go           Git worktree create/join/remove, branch slugification
  debug.go              Unix socket for mclaude-cli attach
  entrypoint.sh         Pod startup script
  Dockerfile

mclaude-cli/
  main.go               Debug attach REPL — connects to session agent unix socket

mclaude-control-plane/
  main.go               Auth, user CRUD, controller-runtime Manager
  auth.go               Login, JWT issuance, SSO, SCIM
  reconciler.go         MCProject reconciler — namespace, RBAC, secrets, PVCs, Deployment
  api/v1alpha1/         MCProject CRD types
  migrations/           dbmate SQL files
  Dockerfile
```

---

## `/upgrade-claude` Skill

Manages Claude Code version upgrades. Never update the pin manually — always go through this skill.

```
/upgrade-claude [--version <target>]
```

Without `--version`, targets the latest published release. With `--version`, targets a specific version.

### What it does

```
1. Fetch changelog between current pinned version and target version
   (github.com/anthropics/claude-code releases — or npm changelog)
2. Read current plan docs and session-agent source to understand what protocol
   features and CLI flags we depend on
3. Analyze changelog for:
   - Breaking changes to stream-json event types, subtypes, or field names
   - Removed or renamed CLI flags (--print, --output-format, --input-format,
     --include-partial-messages, --resume, --session-id, --bare)
   - Changes to control_request/control_response protocol
   - Changes to permission model or hook behavior
4. If breaking changes found:
   - Surface a detailed impact report: what breaks, what needs updating
   - Propose patches to session-agent, client-architecture, and plan docs
   - Require explicit approval before proceeding
5. If no breaking changes (or after approval):
   - Update pinned version in session-agent Dockerfile
   - Update pinned version in plan docs
   - Commit and push on a branch: upgrade/claude-{version}
   - Open a PR with the changelog analysis as the PR body
6. After PR merges: existing pods continue running old version until redeployed
   (kubectl rollout restart deployment -n mclaude-{userId})
```

The skill is the gatekeeper. A Claude Code release is never deployed to pods until this skill has reviewed it and a human has approved the PR.

---

## Open Questions

- ~~**Web UI framework**~~: React 18 (decided).
- **Idle scale-to-zero**: deferred. Project pods stay running when idle. Relay replaced by nginx so the original tunnel wake-up problem is gone — revisit with NATS request/reply as the wake mechanism.
- **Config change UX**: auto-restart sessions on user config change, or prompt? Leaning toward auto-restart with toast (--resume preserves conversation).
- **PVC resize**: 20Gi default. `managed-csi-premium` supports online expansion — add monitoring alert.
- **GHES repo browser**: search/autocomplete in "Clone repo" dialog. Details TBD.
- **OpenBao**: credential seed scripts for tool-specific secrets. Community repo contract: read from Bao, write to `$HOME`, exit 0 if missing.
- **`hostUsers: false` on AKS**: omitted from pod manifests — needs test pod to confirm.
- **Bash stdout streaming**: `tool_progress` events only include elapsed time, not stdout. If Claude Code adds real-time tool output events in the future, the architecture supports them immediately. Monitor Claude Code changelogs.
- **OAuth token refresh in pods**: Claude Code's OAuth token may expire during long sessions. Entrypoint sets `CLAUDE_CODE_OAUTH_TOKEN` from Secret, but long-running sessions may need a refresh mechanism. Consider `apiKeyHelper` script that reads from a refreshable source. Long-term: enterprise SSO users on a paid Anthropic plan may be able to have OAuth tokens managed directly via the Anthropic API, eliminating this problem entirely — revisit when that capability exists.

---

## Acceptance Criteria

Complete when all verification steps pass AND these future plans are written:

| Plan | Scope |
|------|-------|
| `plan-client-architecture.md` | ✅ Done — layered architecture, stores, view models, protocol contract, accumulation algorithm, feature list |
| `docs/feature-list.md` | ✅ Done — canonical feature list with platform support matrix |
| `plan-entra-sso.md` | Entra OIDC integration (blocked on corporate Entra admin approval) |
| `plan-openbao-integration.md` | OpenBao deployment, K8s auth, seed script framework |
| `plan-laptop-worktrees.md` | Worktree-per-session on laptop (parity with K8s) |
| `plan-finops-dashboard.md` | Per-user cost tracking, idle resource alerts |
| `plan-idle-scaledown.md` | Project pod idle scale-to-zero design (NATS request/reply wake mechanism) |
| `plan-ghes-repo-browser.md` | GHES API integration for Clone repo dialog |
