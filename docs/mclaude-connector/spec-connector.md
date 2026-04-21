# Spec: Connector

## Role

The connector is a laptop-side daemon that maintains a persistent WebSocket
tunnel to the mclaude-relay. It receives inbound HTTP requests, WebSocket
connections, and PTY session commands through the tunnel, fulfills them locally
(against mclaude-server or the filesystem), and sends responses back through
the same tunnel. This makes local services reachable from the public internet
without exposing any listening ports on the laptop.

## Deployment

The connector runs as a long-lived process on the developer's laptop.

| Env var | Required | Default | Purpose |
|---|---|---|---|
| `RELAY_URL` | yes | -- | Base URL of the relay (http/https) |
| `TUNNEL_TOKEN` | yes | -- | Bearer token for tunnel authentication |
| `MCLAUDE_URL` | no | `http://localhost:8377` | Local mclaude-server address |
| `SERVICE_TOKEN` | no | -- | Bearer token injected into all upstream requests to mclaude-server |
| `STATIC_DIR` | no | -- | Local directory to serve static files from; when set, non-API paths are served from disk instead of proxied |
| `TLS_SKIP_VERIFY` | no | `false` | Disables TLS certificate verification for mclaude-server (`1` or `true`) |
| `CONNECTOR_NAME` | no | short OS hostname | Human-readable name sent to the relay for multi-laptop identification |

## Interfaces

### Tunnel protocol

The connector dials `{RELAY_URL}/tunnel` as a WebSocket client, authenticating
with a Bearer token in the `Authorization` header and sending its hostname in
the `X-Hostname` header. All communication uses a single JSON message envelope
(`TunnelMsg`) with a `type` field that determines semantics.

### Message types

**HTTP proxy**

| Type | Direction | Purpose |
|---|---|---|
| `http_request` | relay -> connector | Inbound HTTP request (method, path, query, headers, base64-encoded body) |
| `http_response` | connector -> relay | HTTP response (status, headers, base64-encoded body) |

Every `http_request` produces exactly one `http_response` correlated by `id`.

**WebSocket bridge**

| Type | Direction | Purpose |
|---|---|---|
| `ws_connect` | relay -> connector | Open a new WebSocket to mclaude-server's `/ws` endpoint |
| `ws_message` | bidirectional | Relay a single WebSocket frame (base64-encoded data, binary flag) |
| `ws_close` | bidirectional | Close a bridged WebSocket (close code and reason) |

On `ws_connect`, the connector dials mclaude-server, authenticates with the
service token, and starts a read loop that forwards frames back through the
tunnel. Both sides can initiate `ws_close`.

**PTY sessions**

| Type | Direction | Purpose |
|---|---|---|
| `pty_connect` | relay -> connector | Start a new pseudo-terminal session |
| `pty_data` | bidirectional | Terminal I/O data (base64-encoded) |
| `pty_resize` | relay -> connector | Update terminal dimensions (rows, cols) |
| `pty_close` | bidirectional | Terminate the PTY session (reason string) |

On `pty_connect`, the connector spawns the user's default shell (or a tmux
command if specified in the `command` field -- only `tmux` prefixed commands are
allowed). The initial terminal size is set from the `rows`/`cols` fields if
present. PTY output is streamed back as `pty_data` messages. When the shell
process exits, the connector sends `pty_close`.

### HTTP proxy behavior

Incoming `http_request` messages are routed based on path:

- **API paths** (`/sessions`, `/projects`, `/skills`, `/screenshots`, `/files`,
  `/telemetry`, `/auth/`, `/ws`, `/tunnel`, `/health`, `/usage`, `/admin`,
  `/laptops`, `/tmux-sessions`) are always proxied to mclaude-server. The
  connector injects the `SERVICE_TOKEN` as a Bearer header and forwards the
  original request headers, method, query string, and body.
- **Non-API paths** (when `STATIC_DIR` is set) are served from the local
  filesystem. Path traversal is rejected. The content type is detected from the
  file extension. All static responses carry `Cache-Control: no-cache, no-store, must-revalidate` headers
  to prevent caching during live editing.
- **`/__static-version`** returns the mtime of `index.html` as JSON, enabling
  the web app to poll for changes and auto-reload.
- When `STATIC_DIR` is not set, all paths are proxied to mclaude-server.

Response headers `connection` and `transfer-encoding` are stripped from proxied
responses before forwarding through the tunnel.

## Internal Behavior

### Tunnel lifecycle

The connector runs an infinite reconnect loop. On each iteration it dials the
relay, enters a blocking read loop that dispatches incoming messages to handler
goroutines, and returns when the WebSocket connection drops. A fixed 2-second
delay separates reconnection attempts.

### Disconnect cleanup

When the tunnel connection drops, all active WebSocket bridges and PTY sessions
are torn down immediately. Bridged WebSocket connections are closed, and PTY
processes are killed. This prevents stale sessions from accumulating across
reconnects.

### Message dispatch

Each inbound tunnel message is dispatched to its handler in a new goroutine,
so a slow HTTP proxy call does not block PTY data flow. Writes to the tunnel
WebSocket are serialized with a mutex.

### Concurrency

WebSocket bridge entries and PTY session entries are tracked in maps guarded by
their own mutexes. Each bridged WebSocket connection has a per-entry write mutex
to prevent interleaved frames.

## Error Handling

- **Relay unreachable or tunnel drops:** The connector logs the error and
  reconnects after a 2-second delay. All in-flight sessions are cleaned up.
- **mclaude-server unreachable (HTTP):** Returns a 502 Bad Gateway response
  through the tunnel.
- **mclaude-server unreachable (WebSocket):** Sends a `ws_close` with code 1011
  and a descriptive reason back through the tunnel.
- **PTY spawn failure:** Sends a `pty_close` message with the error in the
  reason field.
- **Static file not found:** Returns a 404 through the tunnel.
- **Path traversal attempt:** Returns a 403 through the tunnel.
- **Malformed tunnel message:** Logged and skipped; the read loop continues.

## Dependencies

- **mclaude-relay** -- the connector maintains a persistent WebSocket to the
  relay and is non-functional without it.
- **mclaude-server** -- the local API server that handles proxied HTTP and
  WebSocket traffic (default `localhost:8377`).
- **A POSIX shell** -- PTY sessions spawn the user's `$SHELL` (falling back to
  `/bin/bash`). tmux must be installed if tmux commands are used.
