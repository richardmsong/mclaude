# Multi-Laptop Support

**Status**: draft
**Status history**:
- 2026-04-10: accepted
- 2026-04-19: reverted to draft — retroactive accepted tag incorrect; implementation not confirmed, needs per-ADR review


## Overview

Allow multiple laptops to connect to a single relay simultaneously. Each laptop runs its own connector + mclaude-server. The web/iOS UI shows sessions from all laptops, filterable by host.

## Architecture

```
Browser/iOS ──> Relay (VM, port 80)
                    ├── tunnel "mbp16" ──> Connector (work laptop) ──> mclaude-server
                    └── tunnel "mbp14" ──> Connector (personal laptop) ──> mclaude-server
```

## Core Changes

### Relay: Tunnel Map

Replace single `tunnel *websocket.Conn` with `tunnels map[string]*tunnelEntry` keyed by hostname.

Each connector sends its hostname via `X-Hostname` header during WS handshake. Defaults to `os.Hostname()` on the connector — zero config needed.

Same `TUNNEL_TOKEN` for all connectors. Same laptop connecting again evicts only its own old tunnel.

### Session ID Namespacing

IDs would collide across laptops (both could have session `1`). The relay prefixes all session IDs with `{hostname}~` in responses and strips the prefix when routing requests.

- Browser sees: `mbp16~1`, `mbp14~mclaude:3`
- Relay strips prefix, routes to correct tunnel
- mclaude-server on each laptop is completely unchanged

### API Routing

- `GET /sessions` — fan out to ALL tunnels, merge, add `laptop` field, prefix IDs
- `GET /sessions/:id/events` — extract laptop from `~` prefix, route to that tunnel
- `POST /sessions/:id/input` — same, route by prefix
- `POST /sessions` — require `laptop` field in body, route to that tunnel
- `GET /projects` — fan out to all, merge with `laptop` field
- `GET /skills` — fan out, merge
- `GET /laptops` — new endpoint, relay answers directly with connected host list
- `POST /screenshots` — route by `X-Laptop-ID` header (session detail knows which laptop)

### WS Bridge

Phone connects via `/ws`. Relay sends `ws_connect` to ALL tunnels. Events from each tunnel get session IDs prefixed before forwarding to phone. One tunnel disconnecting doesn't break the phone WS.

### Data Model

Add `laptop: String?` to ClaudeSession (optional for backward compat):

```swift
public struct ClaudeSession: Codable, Identifiable, Sendable {
    // existing fields...
    public let laptop: String?
}
```

## Implementation Steps

### Step 1: Relay multi-tunnel (relay.go)

- Replace `tunnel *websocket.Conn` + `sendMu` with `tunnels map[string]*tunnelEntry`
- Each `tunnelEntry` has its own `conn`, `sendMu`, `pending map`, `laptopID`
- `HandleTunnel`: read `X-Hostname` header, store by hostname
- Evict only same-hostname tunnel on reconnect
- Per-tunnel disconnect cleanup (fail only that tunnel's pending requests)

### Step 2: Fan-out API (relay.go)

- `GET /sessions`: parallel request to all tunnels, parse JSON arrays, add `laptop` field, prefix `id` with `hostname~`, merge, return
- `GET /projects`: same fan-out pattern
- `GET /skills`: same fan-out pattern

### Step 3: Routed API (relay.go + main.go)

- For paths like `/sessions/mbp16~1/events`: split on `~`, extract hostname, strip prefix, route to that tunnel
- `POST /sessions`: read `laptop` from request body, route to that tunnel
- `POST /screenshots`: read `X-Laptop-ID` header, route accordingly

### Step 4: Multi-tunnel WS bridge (relay.go)

- `HandleClientWS` sends `ws_connect` to all tunnels
- Track per-client per-tunnel WS IDs
- Prefix session IDs in `sessions`, `output`, `event` messages from each tunnel
- On tunnel disconnect: send updated sessions list (without that tunnel's sessions) to all phone clients

### Step 5: Connector hostname (mclaude-connector/main.go)

- Get hostname via `os.Hostname()`
- Allow override via `CONNECTOR_NAME` env var
- Send as `X-Hostname` header during tunnel WS handshake
- No other changes

### Step 6: Web app (index.html)

- Add `laptop` field awareness to session model
- **Settings page with laptop selector** (dropdown of connected hosts + "All" option)
- Selected laptop stored in `localStorage` as `mc_laptop`
- When a laptop is selected, entire app is scoped to that laptop:
  - Dashboard only shows that laptop's sessions
  - Tmux filter operates within that laptop context
  - New Session creates on that laptop
  - Projects loaded from that laptop only
  - Screenshots/files routed to that laptop
- "All" option merges everything (session rows show laptop badge)
- Settings page also shows connection status per laptop
- All API calls use prefixed session IDs

### Step 7: iOS app

- Add `laptop: String?` to ClaudeSession in MClaude-Shared
- **Settings tab gets laptop picker** (same dropdown pattern)
- Selected laptop persisted in UserDefaults
- Full context switch: dashboard, projects, session creation all scoped to selected laptop
- "All" option for merged view with laptop badges on rows
- Connection indicator shows per-laptop status

### Step 8: Health + laptops endpoints (main.go)

- `GET /laptops`: returns `[{"id":"mbp16","connected":true,"since":"..."},...]`
- `GET /health`: enhanced with laptop list

## Backward Compatibility

- Connector without hostname awareness gets ID `"default"`
- Session IDs without `~` belong to default laptop
- `laptop` field is optional (nil for single-laptop setups)
- Single-connector setups work unchanged

## Design Decisions

- **Hostname**: Connector uses short hostname (truncated at first dot). Override via `CONNECTOR_NAME` env var.
- **Disconnect behavior**: Sessions removed immediately when a laptop's tunnel drops. Relay broadcasts updated session list to all phone WS clients.
- **"All Hosts" view**: Flat list with a small laptop badge on each session row (no section headers).
- **Implementation order**: Relay first (multi-tunnel support), then connector, then clients.

## Edge Cases

- **TUNNEL_STATIC**: Pinned to a single laptop. Relay env var `TUNNEL_STATIC_HOST` specifies which hostname serves static files (e.g. `TUNNEL_STATIC_HOST=mbp16`). Only static requests go to that tunnel. If the host disconnects, relay falls back to embedded files.
- **Screenshots/files**: Laptop-specific (file on that laptop's disk). Session detail view knows which laptop, routes accordingly.
- **Same hostname**: If two connectors send same hostname, second evicts first (same as current single-tunnel behavior).
- **CONNECTOR_NAME override**: For when hostname isn't descriptive enough.
