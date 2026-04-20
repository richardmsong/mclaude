# Spec: Relay

## Role

The relay is a public-facing Go server that bridges browser and mobile clients to one or more laptop connectors running behind NAT. It accepts persistent WebSocket tunnels from connectors, multiplexes HTTP and WebSocket traffic through those tunnels, manages multi-user authentication with per-laptop access control, and serves the web UI's static files.

## Deployment

The relay runs as a single-replica container (or bare binary) listening on one HTTP port. Configuration is entirely via environment variables:

| Variable | Required | Description |
|---|---|---|
| `TUNNEL_TOKEN` | yes | Shared secret that connectors present to register a tunnel |
| `WEB_TOKEN` | no | Legacy single-token auth for all clients (bootstrap / backward compat) |
| `PORT` | no | Listen port (default `8080`) |
| `USERS_FILE` | no | Path to the JSON user-store file (default `/tmp/mclaude-users.json`) |
| `TUNNEL_STATIC` | no | When `true`, proxy static file requests through a tunnel instead of serving locally |
| `TUNNEL_STATIC_HOST` | no | Pin tunneled static serving to a specific laptop hostname (default: any available) |
| `STATIC_DIR` | no | Directory on disk to serve static files from (default `static`; ignored when `TUNNEL_STATIC` is enabled) |

On first boot with an empty user store and `WEB_TOKEN` set, the relay auto-creates an admin user seeded with that token.

## Interfaces

### HTTP Endpoints

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/health` | none | Returns JSON with relay status, tunnel count, and connected laptop hostnames |
| GET | `/laptops` | optional | Lists connected laptops; if authenticated, filters by the user's ACL |
| GET | `/auth/me` | bearer | Returns the authenticated user's profile (id, name, email, role, laptops) |
| OPTIONS | `*` | none | CORS preflight; returns permissive allow-origin/methods/headers |
| GET | `/sessions` | bearer | Fan-out: queries all accessible tunnels, merges session lists, prefixes session IDs with laptop hostname |
| GET | `/projects` | bearer | Fan-out: merges project lists from all accessible tunnels |
| GET | `/skills` | bearer | Fan-out: merges skill lists from all accessible tunnels |
| POST | `/sessions` | bearer | Routes to the tunnel identified by `X-Laptop-ID` header (or first available) |
| `*` | `/sessions/{prefixed-id}/**` | bearer | Strips the laptop prefix from the session ID and proxies to the owning tunnel |
| `*` | `/screenshots/**` | bearer | Proxies to the tunnel identified by `X-Laptop-ID` (or first available) |
| `*` | `/files/**` | bearer | Same routing as screenshots |
| `*` | `/telemetry/**` | bearer | Proxied as a generic API path |

### Admin Endpoints (require admin role)

| Method | Path | Description |
|---|---|---|
| GET | `/admin/users` | List all users (tokens redacted to last 8 chars) |
| POST | `/admin/users` | Create a user; returns the full generated token |
| GET | `/admin/users/{id}` | Get a single user (token redacted) |
| PUT | `/admin/users/{id}` | Update user fields (name, email, role, laptops, disabled) |
| DELETE | `/admin/users/{id}` | Delete a user |
| POST | `/admin/users/{id}/rotate-token` | Generate a new token for the user; returns the full new token |

### WebSocket Endpoints

| Path | Auth | Description |
|---|---|---|
| `/tunnel` | `TUNNEL_TOKEN` | Connector registers a persistent tunnel; identified by `X-Hostname` header |
| `/ws` | bearer | Client (browser/phone) connects; relay bridges frames to/from all accessible tunnels |
| `/ws/pty` | bearer | Browser terminal session; relay bridges binary PTY I/O to a single tunnel. Accepts `laptop`, `rows`, `cols`, `command` query params |

### Static File Serving

The relay serves the web UI through one of three strategies, evaluated in order:

1. **Tunneled** (`TUNNEL_STATIC=true`): proxies all non-API requests through a connector tunnel, optionally pinned to `TUNNEL_STATIC_HOST`.
2. **Disk** (`STATIC_DIR` or `./static` exists): serves files from the local filesystem with hot-reload.
3. **Embedded**: serves files compiled into the binary via Go `embed`.

Static file requests require no authentication.

## Internal Behavior

### Tunnel Management

Each connector opens a single WebSocket to `/tunnel`, authenticated by `TUNNEL_TOKEN` and identified by its `X-Hostname` header. The relay maintains a map of hostname to tunnel connection. If a connector reconnects with the same hostname, the old connection is evicted. Keepalive pings are sent every 30 seconds with a 60-second read deadline.

All traffic over the tunnel is multiplexed as JSON messages with a `type` field. Supported types: `http_request`/`http_response` for proxied REST calls, `ws_connect`/`ws_message`/`ws_close` for bridged WebSocket sessions, and `pty_connect`/`pty_data`/`pty_resize`/`pty_close` for terminal sessions. HTTP request and response bodies are base64-encoded.

### Client Routing

API requests that operate on a single session extract the target laptop from the session ID's hostname prefix (format: `hostname~original-id`). Fan-out endpoints (`GET /sessions`, `GET /projects`, `GET /skills`) query all tunnels the user can access in parallel, merge JSON array responses, and annotate each item with a `laptop` field.

The `X-Laptop-ID` request header explicitly targets a specific tunnel. When absent, the relay falls back to the first available tunnel the user can access.

### WebSocket Bridging

When a client connects to `/ws`, the relay opens a virtual WebSocket on each accessible tunnel (or a single tunnel if the `laptop` query param is set). Frames from the client are fanned out to all connected tunnels. Frames from tunnels are forwarded to the client after rewriting session IDs to include the laptop prefix. The `load_more` command is routed to the specific tunnel that owns the referenced session.

When a tunnel disconnects, the relay sends `ws_close` for that tunnel's virtual WebSocket IDs. The client connection stays open as long as at least one tunnel remains.

### PTY Bridging

A `/ws/pty` connection targets exactly one tunnel. The relay sends a `pty_connect` message to spawn a shell (or a custom command), then bridges binary frames bidirectionally. Text-mode JSON `resize` messages from the client are translated to `pty_resize` on the tunnel side. The PTY session is torn down when either side disconnects.

### User and ACL Management

Users are stored in a JSON file on disk, loaded at startup, and persisted on every mutation. Each user has an ID, name, email, role (`admin` or `user`), a bearer token (prefix `mcu_`, 32 random hex bytes), a laptop ACL list, and a disabled flag. The ACL entry `*` grants access to all laptops.

Authentication checks the user store first, then falls back to the legacy `WEB_TOKEN` (which maps to a synthetic admin with wildcard access). Disabled users are rejected at authentication time.

## Error Handling

- **No tunnel available**: API and static requests return 503 with a descriptive message.
- **Laptop not connected**: requests targeting a specific laptop by ID or session prefix return 503.
- **Access denied**: requests for a laptop outside the user's ACL return 403.
- **Unauthorized**: missing or invalid bearer token returns 401 on all authenticated endpoints.
- **Forbidden**: non-admin users calling `/admin/*` endpoints return 403.
- **Tunnel timeout**: HTTP-over-tunnel requests time out after 25 seconds; the pending request is cleaned up and the client receives 503.
- **Tunnel disconnect**: all pending HTTP requests for the disconnected tunnel receive a nil response (surfaced as 503). Connected WebSocket clients receive `ws_close` for that tunnel's sessions. PTY sessions on the disconnected tunnel are closed.
- **User store I/O failure**: load and save errors are logged but do not crash the process.

## Dependencies

- **gorilla/websocket**: WebSocket upgrade and framing for tunnel, client, and PTY connections.
- **Connector tunnels**: all API and real-time data flows through at least one connected connector; without tunnels the relay can only serve static files and the health endpoint.
- **Filesystem** (optional): user store JSON file for persistence; static file directory for disk-based serving.
