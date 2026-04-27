# Spec: CLI

## Role

mclaude-cli is a terminal client for the mclaude platform. It attaches to running session agents over unix sockets, provides an interactive REPL for sending messages and approving tool-use permission requests, and lists sessions for a given user/project. It reads default slug values from a local context file (`~/.mclaude/context.json`) so users do not need to pass identity flags on every invocation.

## Deployment

Installed as a standalone Go binary (`mclaude-cli`). No container, no daemon -- invoked directly from the shell. Requires a running session agent exposing a unix socket to attach to.

## Interfaces

### Commands

#### `mclaude-cli attach <session-id>`

Connects to a session agent's unix socket and starts an interactive REPL. Events from the agent (streaming text, tool use, permission requests, progress, results, state changes, compaction boundaries) are rendered as human-readable terminal output. User input is sent as messages; when a permission prompt is pending, `y`/`n` input is sent as an allow/deny control response instead.

Flags:

| Flag | Description | Default |
|------|-------------|---------|
| `--socket <path>` | Override unix socket path | `/tmp/mclaude-session-{id}.sock` |
| `--log-machine` | Emit structured JSON logs to stderr | off (pretty console logs) |
| `--log-level <level>` | Log verbosity: `debug`, `info`, `warn`, `error` | `info` |

#### `mclaude-cli session list`

Resolves user and project slugs (from flags or context file), validates them, and prints the NATS KV key prefix that would be used to query sessions. Does not make network calls -- outputs the resolved parameters only.

Flags:

| Flag | Description | Default |
|------|-------------|---------|
| `-u <uslug>` | User slug | Value from `~/.mclaude/context.json` |
| `-p <pslug>` | Project slug (accepts `@pslug` short form) | Value from `~/.mclaude/context.json` |

#### `mclaude host register [--name <name>]`

Device-code registration flow for BYOH machines. Prompts for a hostname (default = `hostname` output, slugified). Generates an NKey pair locally — the private seed never leaves the machine, written to `~/.mclaude/hosts/{hslug}/nkey.seed` (mode 0600). Calls `POST /api/users/{uslug}/hosts/code` with `{publicKey}` to get a 6-character device code, then prints instructions for the user to open the dashboard and enter the code. Polls `GET /api/users/{uslug}/hosts/code/{code}` until the status changes from `pending` to `completed`. On completion, writes `~/.mclaude/hosts/{hslug}/{nats.creds, config.json}` from the returned JWT + the locally-stored seed, and symlinks `~/.mclaude/active-host → {hslug}`.

Flags:

| Flag | Description | Default |
|------|-------------|---------|
| `--name <name>` | Display name for the host | `hostname` output, slugified |

#### `mclaude host list`

Lists all hosts the authenticated user owns or has been granted access to. Calls `GET /api/users/{uslug}/hosts` and prints a table of slug, name, type, role, and online status.

#### `mclaude host use <hslug>`

Sets the active host by symlinking `~/.mclaude/active-host → ~/.mclaude/hosts/{hslug}/`. Subsequent commands that require a host slug (e.g. `mclaude daemon`) read from this symlink when `--host` is not provided.

#### `mclaude host rm <hslug>`

Removes a host registration. Calls `DELETE /api/users/{uslug}/hosts/{hslug}` and removes the local `~/.mclaude/hosts/{hslug}/` directory. If the removed host is the active host, the `active-host` symlink is also removed.

#### `mclaude cluster register`

Admin-only. Registers a new K8s worker cluster. Calls `POST /admin/clusters`.

Flags:

| Flag | Description | Default |
|------|-------------|---------|
| `--slug <cslug>` | Cluster slug (required; becomes the `hosts.slug` for all granted users) | (none) |
| `--name <display>` | Display name | Defaults to slug |
| `--jetstream-domain <jsd>` | JetStream domain for the worker NATS | (required) |
| `--leaf-url <url>` | Worker NATS leaf-node URL (e.g. `nats-leaf://hub:7422`) | (required) |
| `--direct-nats-url <wss>` | Externally-reachable WebSocket URL for SPA direct-to-worker | (optional) |

Returns `{slug, leafJwt, leafSeed, accountJwt, operatorJwt, jsDomain, directNatsUrl}` for the admin to drop into the worker cluster's NATS Secret + `mclaude-worker` Helm values.

#### `mclaude cluster grant <cluster-slug> <uslug>`

Admin-only. Grants a user access to a cluster. Calls `POST /admin/clusters/{cluster-slug}/grants` with `{userSlug}`. Control-plane creates a new `hosts` row for that user with the cluster-shared fields copied from the existing cluster host row, and mints a per-user JWT.

#### `mclaude daemon --host <hslug>`

Starts the BYOH local controller daemon. Reads `--host` from the flag or from `~/.mclaude/active-host` symlink if unset. Connects to hub NATS using the host's credentials from `~/.mclaude/hosts/{hslug}/nats.creds`, subscribes to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>`, and starts session-agent subprocesses for each provisioned project. Intended to run as a launchd / systemd service.

Flags:

| Flag | Description | Default |
|------|-------------|---------|
| `--host <hslug>` | Host slug | Read from `~/.mclaude/active-host` symlink |

### Context file

`~/.mclaude/context.json` stores `userSlug`, `projectSlug`, and `hostSlug` defaults. The path is overridable via the `MCLAUDE_CONTEXT_FILE` environment variable. If the file does not exist, all fields default to empty.

### Wire protocol

The attach REPL communicates over a newline-delimited JSON (JSONL) protocol on the unix socket. Outbound messages are either `user` (chat message) or `control_response` (permission allow/deny). Inbound events include types: `system`, `stream_event`, `assistant`, `user`, `control_request`, `tool_progress`, `result`, and `clear`.

### Accumulator behavior

The CLI accumulates inbound events into a conversation model for rendering. Block types rendered: `TextBlock`, `StreamingTextBlock`, `ToolUseBlock`, `ToolResultBlock`, `ControlRequestBlock`, `CompactionBlock`. Block types silently discarded: `ThinkingBlock`, `SkillInvocationBlock`, `SystemMessageBlock`.

When an event carries a non-null `parent_tool_use_id`, the resulting turn is nested under the parent ToolUseBlock's agent turn (subagent nesting).

On `clear` event: the accumulator resets all turns to empty (no divider rendered, just a blank conversation state).

On `compact_boundary` system event: the accumulator resets all turns and renders a `--- context compacted ---` divider.

### Reconnection

No reconnection. If the unix socket drops, the CLI exits immediately. The user re-runs `mclaude-cli attach <session-id>` to reconnect.

## Dependencies

- **Session agent unix socket** -- the attach command connects to a session agent's socket at `/tmp/mclaude-session-{id}.sock` (or a custom path).
- **`~/.mclaude/context.json`** -- optional; provides default user/project/host slugs.
- **mclaude-common (`mclaude.io/common`)** -- shared slug validation (`pkg/slug`) and NATS subject construction (`pkg/subj`).
- **zerolog** -- structured logging.
