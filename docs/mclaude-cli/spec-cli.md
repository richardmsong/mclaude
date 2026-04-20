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

Resolves user and project slugs (from flags or context file), validates them, and prints the NATS KV key prefix and events subject that would be used to query sessions. Does not make network calls -- outputs the resolved parameters only.

Flags:

| Flag | Description | Default |
|------|-------------|---------|
| `-u <uslug>` | User slug | Value from `~/.mclaude/context.json` |
| `-p <pslug>` | Project slug (accepts `@pslug` short form) | Value from `~/.mclaude/context.json` |

### Context file

`~/.mclaude/context.json` stores `userSlug`, `projectSlug`, and `hostSlug` defaults. The path is overridable via the `MCLAUDE_CONTEXT_FILE` environment variable. If the file does not exist, all fields default to empty.

### Wire protocol

The attach REPL communicates over a newline-delimited JSON (JSONL) protocol on the unix socket. Outbound messages are either `user` (chat message) or `control_response` (permission allow/deny). Inbound events include types: `system`, `stream_event`, `assistant`, `user`, `control_request`, `tool_progress`, and `result`.

## Dependencies

- **Session agent unix socket** -- the attach command connects to a session agent's socket at `/tmp/mclaude-session-{id}.sock` (or a custom path).
- **`~/.mclaude/context.json`** -- optional; provides default user/project/host slugs.
- **mclaude-common (`mclaude.io/common`)** -- shared slug validation (`pkg/slug`) and NATS subject construction (`pkg/subj`).
- **zerolog** -- structured logging.
