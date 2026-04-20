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
