# Spec: mclaude-web Host Picker

The host picker is how users select which **host** a project is created on, and how they switch between hosts when viewing existing projects. Per ADR-0035 every project is owned by exactly one host; the SPA surfaces the host throughout the navigation, the URL scheme, and the project-level UI.

## Where it appears

### A. New Project sheet — Host field

When the user opens the New Project sheet (from the dashboard overflow menu), the form gains a required **Host** field above the existing Name and Git URL fields:

```
┌─────────────────────────────────┐
│  New Project                    │
│                                 │
│  Host                           │
│  [ MBP16 (you) ▾ ]              │  segmented dropdown
│                                 │
│  Name                           │
│  [ ____________________ ]       │
│                                 │
│  Git URL  (optional)            │
│  [ ____________________ ]       │
│                                 │
│            [Cancel] [Create]    │
└─────────────────────────────────┘
```

- Default selection: the user's first registered host (stored in localStorage `mclaude.defaultHostSlug`). If no hosts are registered, the host picker is disabled and the UI shows an empty state prompting the user to register a host (ADR-0095).
- Dropdown lists every host in the login response's `hosts[]` array, ordered alphabetically by name.
- Each row in the dropdown shows: host display name + slug + online indicator.
- **Disabled** rows (not selectable) for hosts where `online === false`. Tapping shows a tooltip "{name} is offline — wait for it to reconnect or pick another host."

### B. Dashboard project header — host pill

The project header row (rendered when ≥1 project is visible) gets a host pill on the right:

```
MCLAUDE                                   [🖥️ MBP16]
●  working-session
   Working · ~/work/mclaude
```

- Pill uses the host's display name; tooltip shows the slug.
- Pill is **clickable** — opens the [Settings → Hosts](#screen-settings--hosts) screen scrolled to that host.

### C. Settings → Hosts

A new screen reachable from the existing Settings overview (after the existing Account row, before Token Usage):

```
┌─────────────────────────────────┐
│ ←  Hosts                        │
├─────────────────────────────────┤
│  MBP16                          │
│      Online · 2 projects        │
│                              ›  │
│                                 │
│  Garage VM                      │
│      Offline (2h ago) · 0       │
│                              ›  │
│                                 │
│  us-east                        │
│      Online · 1 proj            │
│                              ›  │
│                                 │
│  [+ Register a new host]        │
└─────────────────────────────────┘
```

- Each row tappable → host detail screen with name, slug, online state, last seen, CLAUDE ACCESS sub-section (see `spec-settings-web.md` § Host Detail: CLAUDE ACCESS, ADR-0092), list of projects, and a "Remove host" destructive action.
- "+ Register a new host" launches the device-code flow:
  1. SPA calls `POST /api/users/{uslug}/hosts/code` to obtain `{code, expiresAt}`.
  2. Renders a modal with the 6-character code, a copy button, and the CLI hint: "Open a terminal on the new machine and run `mclaude host register` then enter this code." Reading the dashboard URL in the CLI is also supported.
  3. Polls `GET /api/users/{uslug}/hosts` (the host-list endpoint) every 3 seconds (max 10 minutes); when a new host with `slug` not previously seen appears, dismiss the modal and reveal the new row. The SPA does **not** poll the code-status endpoint `GET /api/users/{uslug}/hosts/code/{code}` — that path is reserved for the CLI on the new machine, which polls it to receive the JWT once redemption completes. The two polling strategies are not interchangeable: the dashboard watches for host inventory changes, the CLI watches for credential issuance.
  4. On expiry, the modal collapses to "Code expired — Try again."

## Routes

URLs include the host slug for any project-scoped page per ADR-0035:

- `/u/{uslug}/h/{hslug}/p/{pslug}` — project detail (sessions list).
- `/u/{uslug}/h/{hslug}/p/{pslug}/s/{sslug}` — session detail.
- `/u/{uslug}/hosts` — Settings → Hosts.
- `/u/{uslug}/hosts/{hslug}` — host detail.

The dashboard at `/u/{uslug}` aggregates across hosts (no `hslug` in the URL).

## Selection model

- A single user-level "default host" preference is stored client-side (localStorage `mclaude.defaultHostSlug`). New Project pre-selects this.
- The dashboard always shows projects across **all** hosts. There is no global "viewing host" filter — the host pill on each project row is the only host indicator.
- Switching to a project on a different host is implicit: opening the project detail navigates to a URL with that host's slug; the SessionStore opens the right per-host KV watches.

## Connection strategy (ties into EventStore / SessionStore)

The SPA always maintains a connection to **hub NATS** (`hubUrl` from login response). All hosts connect hub-direct — the hub connection is used uniformly.

## Connection / liveness indicators

- A small dot (8px) next to each host row indicates `online`: green / gray.
- The dashboard's existing global connection dot continues to reflect the hub connection only.
- "Last seen" rendered on offline hosts uses `lastSeenAt` from the login response; since `$SYS` events update `mclaude-hosts` KV in real time and the SPA watches that bucket, this updates without a page refresh.

## Empty / error states

| State | Display |
|-------|---------|
| User has zero hosts | Settings → Hosts shows an empty state: "No hosts registered. Run `mclaude host register` on your machine to get started." and the `+ Register a new host` button. New Project button is disabled. (ADR-0095) |
| User has exactly one host | New Project's Host field is non-disclosed (auto-selected, hidden) until a second host exists. |
| All hosts offline at New Project time | All dropdown rows disabled. Banner at top: "No hosts online. Contact an admin to check the host controller." |
| Device-code expired before registration completes | Modal collapses to "Code expired — Try again." |
| Device-code already redeemed (409) | Modal shows "This code was already used. Generate a new one." |
| Host removal while it has active sessions | Confirm dialog: "{name} has N active sessions that will be stopped. Continue?" Destructive action calls `DELETE /api/users/{uslug}/hosts/{hslug}`. |
| Host shows offline mid-session | Toast: "{name} disconnected — reconnecting." Existing session views switch to a "stale" appearance (faded, no live updates) until reconnect. |
| Per-host JWT signed for the wrong host | Hub NATS auth rejects the publish/subscribe; SPA receives a connection error. Host picker surfaces "credentials invalid for this host." Common cause: host removed and re-registered with the same slug after the SPA loaded — login again to refresh JWTs. |

## Dependencies

- Login response shape per `docs/spec-state-schema.md` — Login Response Shape.
- AuthStore extension: `getHosts(): Host[]` and `getDefaultHostSlug(): string | null` (reads localStorage; returns `null` if no hosts registered — callers must handle the no-host case).
- SessionStore extension: per-host JetStream KV watches.
- Control-plane endpoints `POST /api/users/{uslug}/hosts/code`, `POST /api/hosts/register`, `GET /api/users/{uslug}/hosts`, `DELETE /api/users/{uslug}/hosts/{hslug}` (see `docs/mclaude-control-plane/spec-control-plane.md`).
