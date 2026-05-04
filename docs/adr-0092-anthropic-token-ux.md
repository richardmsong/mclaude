# ADR: Anthropic Token UX — Connect, Authorize, Revoke

**Status**: draft
**Status history**:
- 2026-05-04: draft

## Overview

User-facing flows for connecting, authorizing, and revoking the per-user Anthropic token introduced in ADR-0091. Covers first-run onboarding, Settings UI, host-level authorization, and admin controls.

## Depends on

- ADR-0091 — per-user Anthropic OAuth token (mechanism)

## Decisions to make

| Decision | Options | Notes |
|----------|---------|-------|
| First-run gate | Gate session creation on token; show connect screen first | |
| Token expiry UX | Inline session-detail prompt vs. Settings-only | ADR-0091 defers silent refresh; user must reconnect manually |
| Default platform auto-delivery | Auto-deliver K_wrapped on first-run OAuth (no user action) | |
| Owner-registered host | Auto-deliver K_wrapped silently once user-agent ready | |
| Granted host | Require explicit [Authorize] click in host detail | |
| Revoke per-host | Available in host detail screen | Removes K_wrapped; terminates active sessions on that host |
| Admin: no default platform | Per-user toggle at creation/edit time | Opt out of default platform assignment |

## UX Flows

### First-run (new user, default platform)

_To be designed._

User connects Claude account once via OAuth or token paste. K_wrapped is auto-delivered silently to the default platform user-agent. Session creation proceeds immediately. No host-level authorization step is visible to the user.

### Settings — Claude Account section

_To be designed._

Status (connected / not connected), Connect, Reconnect, Disconnect actions.

### Settings — Host detail

_To be designed._

Per-host Claude authorization status. [Authorize] for granted hosts awaiting K_wrapped. [Revoke] to remove K_wrapped and terminate sessions on that host.

### Admin: per-user default platform assignment

_To be designed._

Toggle in User Management to skip default platform assignment for specific users.

## Impact

- `docs/ui/spec-first-run-flow.md`: add token gate before session creation.
- `docs/ui/mclaude-web/spec-settings-web.md`: add Claude Account section; extend host detail screen.
- `docs/mclaude-control-plane/spec-control-plane.md`: `GET /api/users/{uslug}/hosts` returns `owned: bool` per host.
- `docs/mclaude-web/spec-web.md`: ownership-based K_wrapped auto-delivery logic.
