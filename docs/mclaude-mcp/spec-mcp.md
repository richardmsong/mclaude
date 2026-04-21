# Spec: MCP Server

## Role

The MCP server is a stdio-based Model Context Protocol bridge that lets any MCP-capable client manage Claude Code sessions running in tmux. It translates MCP tool calls into REST requests against the mclaude-server HTTP API, exposing session lifecycle, I/O, and project discovery as first-class MCP tools.

## Deployment

Runs as a stdio MCP server (stdin/stdout transport). A host process (e.g., Claude Desktop, another Claude Code instance) launches it directly.

| Variable | Default | Purpose |
|---|---|---|
| `MCLAUDE_URL` | `http://localhost:8377` | Base URL of the mclaude-server HTTP API |

## Interfaces

All tools return JSON-formatted text content.

### Session discovery

| Tool | Parameters | Behavior |
|---|---|---|
| `list_sessions` | none | Returns all active Claude Code sessions with status and working directory. |
| `get_session` | `id` (string) | Returns details for one session including its current status and detected prompt. |

### Session output

| Tool | Parameters | Behavior |
|---|---|---|
| `get_session_output` | `id` (string) | Returns the current terminal output from a session's tmux pane. |
| `get_session_events` | `id` (string) | Returns recent structured events (tool use, text, thinking) from the session's JSONL log. |

### Session lifecycle

| Tool | Parameters | Behavior |
|---|---|---|
| `create_session` | `cwd` (string), `prompt?` (string) | Creates a new Claude Code session in a tmux window at the given directory. If `prompt` is provided, waits for the session to become idle (up to 20 seconds), pauses 2 seconds for initialization, then sends the prompt as initial input. |
| `send_input` | `id` (string), `text` (string) | Sends text input to an active session. |
| `approve_session` | `id` (string) | Approves a pending permission prompt by sending Enter. |
| `cancel_session` | `id` (string) | Cancels the current operation by sending Escape. |

### Project discovery

| Tool | Parameters | Behavior |
|---|---|---|
| `list_projects` | none | Lists available project directories. |

## Dependencies

- **mclaude-server** -- the HTTP API at `MCLAUDE_URL` that actually manages tmux sessions; every tool call proxies to it.
- **Node.js** -- runtime (ES2022 target).
- **@modelcontextprotocol/sdk** -- provides `McpServer` and `StdioServerTransport`.
- **zod** -- schema validation for MCP tool parameters.
