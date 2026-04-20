# Spec: Server

## Role

mclaude-server is a Swift HTTP/WebSocket daemon that monitors Claude Code sessions running in tmux windows (and optionally as Kubernetes pods), exposes their state and terminal output over a REST API, streams real-time updates to connected WebSocket clients, and provides multi-user authentication via Google OAuth. It is the single backend that the iOS app, web UI, and connector talk to.

## Deployment

The server runs as a macOS launchd daemon. It listens on a configurable host and port (default `0.0.0.0:8377`). When TLS certificate and key files exist at `~/mclaude-certs/`, it serves HTTPS and WSS; otherwise it falls back to plain HTTP and WebSocket.

A self-health-check loop hits `GET /health` every 30 seconds (after a 10-second startup grace period). Three consecutive failures cause the process to `exit(1)`, relying on launchd to restart it.

### Environment Variables

| Variable | Default | Description |
|---|---|---|
| `MCLAUDE_HOST` | `0.0.0.0` | Bind address |
| `MCLAUDE_PORT` | `8377` | Listen port |
| `MCLAUDE_TMUX_TARGET` | `mclaude` | Default tmux session name to monitor |
| `MCLAUDE_POLL_INTERVAL` | `1` | Seconds between tmux/K8s poll cycles |
| `MCLAUDE_SIGNAL_RECIPIENT` | (none) | Phone number for Signal CLI notifications; omit to disable |

### Configuration File

`~/mclaude-config.json` stores Google OAuth credentials (`googleClientId`, `googleClientSecret`, `googleRedirectUri`) and the `ownerUserId` (first authenticated user becomes server owner).

## Interfaces

### HTTP Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/health` | Returns `{"status":"ok"}` (used by self-health-check) |
| GET | `/auth/google/login` | Returns `{"url":"..."}` — the Google OAuth authorization URL |
| GET | `/auth/google/callback` | Exchanges Google auth code, creates/finds user, redirects to `mclaude://auth?token=...` |
| GET | `/auth/me` | Returns the authenticated user's id, name, and email |
| GET | `/sessions` | Lists all sessions visible to the authenticated user (tmux + K8s merged) |
| GET | `/sessions/:id` | Returns a single session by composite ID |
| GET | `/sessions/:id/output` | Returns the cached terminal output for a session |
| GET | `/sessions/:id/events` | Returns the last 200 parsed JSONL events for a session |
| GET | `/sessions/:id/usage` | Returns aggregated token usage (input, output, cache read/create, turn count, model) |
| GET | `/sessions/:id/plan` | Returns the most recently modified `.md` plan file from the session's project |
| POST | `/sessions` | Creates a new Claude Code session in tmux or K8s (accepts `cwd`, optional `runtime`, `tmuxSession`, `windowName`, `token`) |
| POST | `/sessions/:id/input` | Sends text to a session's tmux pane (with optional `sendEnter` flag, default true) |
| POST | `/sessions/:id/approve` | Sends Enter to a session (quick-approve a permission prompt) |
| POST | `/sessions/:id/cancel` | Sends Escape to a session (cancel the current operation) |
| GET | `/skills` | Lists available slash commands (builtins + global + per-project skills) |
| GET | `/projects` | Lists subdirectories under `~/work/` as known projects |
| GET | `/tmux-sessions` | Lists monitored tmux session names |
| GET | `/usage/timeline` | Returns per-turn token usage data points across all sessions (accepts `?hours=N`, default 24, max 720) |
| POST | `/screenshots` | Accepts raw PNG body (up to 10 MB), saves to `/tmp/mclaude-screenshots/`, returns `{"path":"..."}` |
| POST | `/files` | Accepts raw file body (up to 50 MB) with `X-Filename` header, saves to `/tmp/mclaude-files/`, returns `{"path":"..."}` |
| POST | `/telemetry` | Receives error reports from clients; logs and returns `{"status":"received"}` |

### Authentication

Requests with a `Bearer` token in the `Authorization` header are authenticated against session tokens (issued after Google OAuth) or legacy Claude OAuth token hashes. Requests without a token are treated as the server owner (local/connector bypass). All session-scoped endpoints enforce ownership: users only see and control sessions they created. Unowned sessions belong to the server owner.

### WebSocket Protocol

Clients connect to `/ws` with an optional `?token=<session-token>` query parameter. On connect, the server immediately sends the current session list and cached output for all sessions the user owns.

**Server-to-client message format:**

```json
{"type": "<message_type>", "data": "<JSON-encoded payload>"}
```

| Message Type | Payload | Trigger |
|---|---|---|
| `sessions` | Array of session objects | Session list changes (status, prompt, add/remove) |
| `output` | `{"id":"...","output":"..."}` | Terminal output changes for a session |
| `more_output` | `{"id":"...","output":"..."}` | Response to a client `load_more` command |
| `event` | `{"id":"...","event":{...}}` | New JSONL structured event (user message, assistant text, tool use, etc.) |

**Client-to-server commands:**

| Command | Fields | Description |
|---|---|---|
| `load_more` | `id`, `lines` (default 160) | Request more scrollback history for a session |

Messages are filtered per-user: each client only receives data for sessions it owns.

## Internal Behavior

### Tmux Monitoring

The poll loop runs every `MCLAUDE_POLL_INTERVAL` seconds. Each cycle:

1. Auto-discovers all tmux sessions (adds new ones, removes stale ones from the monitored set).
2. For each monitored tmux session, lists panes and matches pane PIDs to Claude Code session files in `~/.claude/sessions/` (JSON files containing pid, sessionId, cwd, startedAt).
3. Captures the last 80 lines of each matched pane in parallel.
4. Detects session status by analyzing ANSI escape codes and text patterns in the pane content.
5. Detects interactive prompts (lines starting with `?` followed by numbered options).
6. Compares output hashes and session state hashes against the previous cycle; fires change callbacks only on differences.

### Session Status Detection

Status is derived from the terminal tail content using these rules (evaluated in order):

| Status | Detection |
|---|---|
| `plan_mode` | Tail contains "plan and is ready to execute", "Plan is ready", or "execute this plan" |
| `needs_permission` | Tail contains "Do you want to", "Allow this", "Approve?", "(y/n)", or "(Y/n)" |
| `working` | ANSI spinner colors (38;5;174/216/180/210) present, or working-indicator text ("Running...", "Thinking...", "Compacting conversation", etc.), or JSONL events indicate mid-turn |
| `idle` | Bottom 5 lines contain prompt indicators ("bypass permissions", "shift+tab to cycle", "tab to cycle") |
| `working` (fallback) | None of the above matched |

JSONL working state overrides tmux-detected idle: if the JSONL tailer reports mid-turn activity, a tmux-idle session is upgraded to working. JSONL idle-since timestamps are used for accurate statusSince values.

### Kubernetes Session Management

The K8s session manager creates pods in the `mclaude` namespace labeled `app=mclaude-session`. Each pod runs Claude Code inside tmux. Session interaction (send keys, capture pane) uses `kubectl exec`. The poll loop queries running pods and captures their pane output in parallel, applying the same status detection logic as tmux sessions. K8s session IDs are prefixed with `k8s-`.

### JSONL Event Tailing

For each active session, a file watcher tails `~/.claude/projects/{encoded-cwd}/{sessionId}.jsonl` by polling every 500ms for new bytes appended past the last-read offset.

Parsed event types: `user`, `text`, `thinking`, `tool_use`, `tool_result`, `system`, `compaction`.

Queue-operation events (mid-turn user messages injected via `/queue`) are deduplicated against the real user event and repositioned to the dequeue timestamp for correct chronological ordering.

The tailer also tracks subagents: when an `Agent` tool_use event appears, it polls for a new JSONL file in the `subagents/` subdirectory, starts a watcher for it, and tags emitted events with the subagent's metadata (agent ID, type, description, parent tool use ID). The subagent watcher stops on receiving a `turn_duration` system event.

### WebSocket Broadcasting

The broadcaster maintains a registry of connected clients, each associated with a userId. When sending, it calls a per-client filter function that returns a message only for clients who own the relevant session. Sends have a 2-second timeout; clients that fail to receive within the timeout are disconnected.

### Skills Scanning

The `/skills` endpoint aggregates three sources: a hardcoded list of built-in Claude Code commands, global skills from `~/.claude/skills/` (each subdirectory with a `SKILL.md`), and per-project skills from each active session's `.claude/skills/` directory. Skill metadata (name, description) is parsed from YAML frontmatter in `SKILL.md`.

### Signal Notifications

When `MCLAUDE_SIGNAL_RECIPIENT` is set, the server sends Signal messages via `signal-cli` on status transitions to `waiting_for_input`, `plan_mode`, or `idle`. Other transitions are silent.

## Error Handling

| Failure | Behavior |
|---|---|
| TLS certs missing | Falls back to plain HTTP/WS; logs the missing cert path |
| Health check fails 3 times consecutively | Process exits with code 1 (launchd restarts it) |
| Tmux session disappears | Removed from the monitored set on next discovery cycle; sessions in it stop appearing |
| Claude session file missing for a pane PID | That pane is silently skipped (no session object created) |
| JSONL file not found for a session | Logs the missing path; no watcher is started |
| WebSocket send timeout (>2s) | Client is removed from the broadcaster |
| Google OAuth code exchange fails | Returns 401 to the callback endpoint |
| Pod fails to reach Running within 60s | Returns error response from POST `/sessions` |
| kubectl command fails | K8s session manager returns cached sessions instead of updated data |

## Dependencies

- **tmux** (`/opt/homebrew/bin/tmux`) for session monitoring and interaction
- **Claude Code** CLI (`claude`) for creating new sessions (resolved via `which` or known paths)
- **Hummingbird** (Swift) for HTTP server, TLS, and WebSocket support
- **macOS launchd** for process supervision and restart-on-crash
- **signal-cli** (`/opt/homebrew/bin/signal-cli`, optional) for push notifications
- **kubectl** (`/usr/local/bin/kubectl`, optional) for Kubernetes pod management
- **Google OAuth** (accounts.google.com, optional) for multi-user authentication
- **`~/.claude/sessions/`** directory containing per-process JSON session metadata files
- **`~/.claude/projects/`** directory containing per-session JSONL event logs
