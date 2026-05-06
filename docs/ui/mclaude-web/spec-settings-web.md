# Spec: mclaude-web Settings

Settings screen layout, sections, and error-handling rules for the mclaude-web SPA.

## Screen: Settings

```
┌─────────────────────────────────┐
│ ‹ Back      Settings            │
├─────────────────────────────────┤
│                                 │
│  HOST                           │  section label
│  ┌─────────────────────────┐    │
│  │  Active Host  [select▾] │    │
│  └─────────────────────────┘    │
│                                 │
│  CONNECTED HOSTS                │
│  ┌─────────────────────────┐    │
│  │ ● macbook-pro   3 sess  │    │
│  │ ● macbook-air   1 sess  │    │
│  └─────────────────────────┘    │
│                                 │
│  CONNECTION                     │
│  ┌─────────────────────────┐    │
│  │  Status    ● Connected  │    │
│  │  Sessions  4            │    │
│  └─────────────────────────┘    │
│                                 │
│  CLAUDE ACCOUNT                 │  (ADR-0092)
│  ┌─────────────────────────┐    │
│  │  Status  ● Connected    │    │  green/red/gray dot
│  │  [Reconnect] [Disconnect]│   │
│  └─────────────────────────┘    │
│                                 │
│  ADMINISTRATION (admin only)    │
│  ┌─────────────────────────┐    │
│  │  User Management      › │    │
│  └─────────────────────────┘    │
│                                 │
│  ACCOUNT                        │
│  ┌─────────────────────────┐    │
│  │  Name       Richard     │    │
│  │  Role       admin       │    │
│  └─────────────────────────┘    │
│                                 │
│  ┌─────────────────────────┐    │
│  │        Sign Out         │    │  red text
│  └─────────────────────────┘    │
└─────────────────────────────────┘
```

Settings rows use a grouped card style (iOS Settings aesthetic):
- `--surf` card background
- `--border` dividers between rows within a card
- Row: label left, value/control right
- Status dot: 8px circle, green/gray/red

"Active Host" dropdown: selecting a host reconnects the WebSocket filtered to that host. "All Hosts" option shows sessions from all connected hosts.

**Error handling — general rule:**
Every section that loads data from the server must surface failures visibly. Silent catches that swallow errors and show an empty/default state are not acceptable — the user cannot distinguish "no data" from "failed to load."

Specific rules:
- **Git Providers section**: if `getMe()` or `getAdminProviders()` fails, show a red error line in the section (e.g. "Failed to load providers") instead of "No providers configured." Always `console.error` the underlying error for dev-tools debugging.
- **Any async load**: on failure, log to `console.error` with the error object. Show an inline error in the relevant section. Never silently fall back to an empty state.

## Section: CLAUDE ACCOUNT (ADR-0092)

Positioned between CONNECTION and ADMINISTRATION. Shows the user's Claude token status.

| State | Status line | Actions |
|-------|------------|---------|
| Connected | `● Connected` (green dot) | [Reconnect] [Disconnect] |
| Token invalid | `● Token invalid` (red dot) | [Reconnect] |
| Not connected | `○ Not connected` (gray dot) | [Connect] |

"Token invalid" is shown when any active session reports `status: "token_expired"` in session KV. No active sessions = presumed valid ("Connected").

- **[Connect] / [Reconnect]**: opens the Connect sheet (bottom sheet with OAuth popup + paste input).
- **[Disconnect]**: confirmation dialog ("Disconnect Claude account? All active sessions on N hosts will stop."). Deletes from KV + publishes token-revoke to all accepted-grant hosts. No Anthropic-side revocation — the token remains valid at Anthropic until natural expiry (~1 year). Users can manually revoke at `claude.ai/settings/claude-code`.

## Host Detail: CLAUDE ACCESS sub-section (ADR-0092)

Added to the host detail screen (see spec-host-picker.md) below the host info and above PROJECTS.

| State | Display | Action |
|-------|---------|--------|
| Authorized | `● Authorized` (green) | [Revoke] |
| Pending delivery | `○ Pending delivery` (yellow) | Auto-delivers when user-agent online |
| Awaiting acceptance | `○ Awaiting acceptance` (gray) | Auto-accepted for default platform |
| Revoked | `○ Revoked` (red) | [Re-authorize] |
| Host offline | `○ Host offline` (gray) | Queued for next visit |

[Revoke]: confirmation dialog with session impact count. On confirm: SPA publishes `token-revoke` request/reply (10s timeout). On NATS timeout/failure: SPA shows inline error "Could not reach {host} — token may still be active. Try again when the host is online."

[Re-authorize]: immediate silent re-delivery (no confirmation). SPA reads token from KV, wraps with user-agent X25519 pubkey, delivers via NATS request/reply. On success: host transitions to "Authorized." On failure (user-agent offline): SPA shows inline error "Host is offline — try again later" and host stays in "Revoked" state. Pending delivery is NOT written to localStorage (unlike first-run) — re-authorize is an explicit user action with immediate feedback, not a background retry.
