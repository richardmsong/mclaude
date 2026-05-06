# ADR: Anthropic Token UX — Connect, Authorize, Revoke

**Status**: implemented
**Status history**:
- 2026-05-04: draft
- 2026-05-06: accepted — paired with spec-first-run-flow.md, spec-settings-web.md, spec-host-picker.md, spec-web.md, spec-state-schema.md, spec-nats-data-taxonomy.md, spec-session-agent.md, spec-user-agent.md, spec-cli.md, spec-control-plane.md
- 2026-05-06: implemented — all scope CLEAN (mclaude-web, mclaude-session-agent, mclaude-user-agent, mclaude-cli, mclaude-control-plane)

## Overview

User-facing flows for connecting, authorizing, and revoking the per-user Anthropic token introduced in ADR-0091. Covers first-run onboarding, Settings UI, host-level authorization, and disconnect/revoke.

The Anthropic token is a long-lived OAuth access token (`sk-ant-oat01-...`, ~1-year TTL) obtained via PKCE OAuth against claude.ai — the same flow as `claude setup-token`. It is stored as a plaintext string in NATS KV (per ADR-0091) and delivered to hosts via X25519 sealed-box wrapping.

## Depends on

- ADR-0091 — per-user Anthropic OAuth token (mechanism)

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| First-run gate | Connect sheet before everything (before project/session creation) | Simplest mental model: nothing works without a token. K_wrapped auto-delivery and first-run flow happen after connect. |
| Token entry | OAuth PKCE primary ("Sign in with Claude" button); manual paste collapsed under "Or enter a token manually" | OAuth is the happy path (matches `claude setup-token` / `claude login` flow). Paste is the escape hatch for restricted environments. |
| Connect UI | Bottom sheet (consistent with New Session, New Project); OAuth opens popup window, main SPA stays open | User keeps their page context. Popup handles redirect round-trip. postMessage signals completion. |
| Default platform auto-delivery | Auto-deliver K_wrapped silently on first-run after OAuth/paste completes | No user action needed for the default platform; delivery happens between connect and session creation. |
| Token failure UX | Inline banner at point of failure (session view) + state in Settings | User is never more than one tap from fixing it. Session-agent reports `status: "token_expired"` via KV. Rare for 1-year tokens but covers revocation at claude.ai. |
| Settings placement | CLAUDE ACCOUNT section between CONNECTION and ADMINISTRATION | Groups with "how you're connected to services" — host above, Claude here. |
| Host authorization | Always automatic — all hosts get K_wrapped auto-delivered after grant acceptance | No explicit [Authorize] step. Trust is established at grant-accept time (X25519 fingerprint check). Token delivery is a consequence of trust, not a separate decision. |
| Revoke per-host | [Revoke] in host detail with confirmation dialog showing session impact | Destructive action with clear impact disclosure. Re-authorize available to re-deliver. |
| Re-delivery on reconnect | SPA auto-re-delivers to all accepted-grant hosts after new token written | User-agents offline at reconnect time get re-delivered on next SPA visit. No background retry. |
| Disconnect scope | KV + all hosts. No Anthropic-side revocation (no public endpoint exists). | Anthropic does not expose RFC 7009 revocation. Token is long-lived (~1y). Users can manually revoke at claude.ai/settings. |
| Token KV format | Plain string (unchanged from ADR-0091) | Token is a long-lived OAuth access token (`sk-ant-oat01-...`). No refresh token, no expiry tracking needed. The 1-year TTL makes proactive expiry warnings unnecessary. |
| Admin: default platform opt-out | Not needed — default platform is universal by definition | Access control is at user-account level, not platform-grant level. No admin toggle. |

## UX Flows

### First-run (new user, default platform)

The token gate is the very first screen after login — before project creation, before any dashboard rendering. The flow:

1. User logs in (SSO / device-code / password — existing auth).
2. SPA hydrates from `mclaude-user-info-{uslug}` KV. If `tokens.anthropic` is absent → open the Connect sheet.
3. **Connect sheet** (bottom sheet with scrim — same pattern as New Session, New Project):

```
┌──────────────────────────────────┐
│ Connect Claude Account       [✕] │
├──────────────────────────────────┤
│  MClaude uses your Claude account│
│  to power coding sessions.       │
│                                  │
│  ┌────────────────────────────┐  │
│  │   Sign in with Claude      │  │  primary button, --blue
│  └────────────────────────────┘  │
│                                  │
│  Or enter a token manually  ▶    │  collapsed <details>
│                                  │
│  (expanded:)                     │
│  ┌────────────────────────────┐  │
│  │ ●●●●●●●●●●●●●●●●          │  │  password input, write-only
│  └────────────────────────────┘  │
│        [Connect]                 │  secondary button
└──────────────────────────────────┘
```

- **"Sign in with Claude"** opens a **popup window** for the PKCE OAuth flow (redirects to claude.ai in the popup, not the main SPA window). The main SPA stays on the current page. Popup completes → parent window detects success via `postMessage` → sheet updates to show "Connected" → auto-dismisses after 1s.
- **"Or enter a token manually"** expands a `<details>` section with a write-only password input and a [Connect] button. On submit, writes the pasted token to KV. Input cleared immediately. Sheet updates inline — no popup needed.
- Error states: OAuth popup blocked → inline warning ("Allow popups for this site and try again"). OAuth failure → inline red error below the button ("Sign-in failed — try again or enter a token manually"). Paste failure → inline error below the input.
- On first-run, the sheet auto-opens over an empty/dimmed background (no dashboard behind it). On reconnect from Settings or session banner, it opens over the current page.
- The [✕] close button is present but disabled during first-run (token is mandatory to proceed). On reconnect, [✕] is enabled — user can cancel and return to whatever they were doing.

4. After token is written to KV, SPA auto-delivers K_wrapped to the default platform silently (user-agent must be online; if not, retry on next login — see "Token delivery failure" below).
5. Existing first-run flow proceeds: create Default project → create Getting Started session → navigate to session → onboarding message.

**Token delivery failure during first-run**: if the default platform user-agent is offline when the SPA tries to deliver K_wrapped, the SPA stores the delivery as pending in `localStorage` at key `mclaude.pendingDeliveries` (value: JSON array of `{ hslug, provider }` objects). On next login or on NATS reconnect, the SPA retries delivery for each entry and removes it on success. The session creation is blocked until delivery succeeds — the user sees a spinner with "Setting up your environment…" and a timeout message after 30s:

```
┌──────────────────────────────────┐
│  Your host isn't responding.     │
│                                  │
│          [Try again]             │
└──────────────────────────────────┘
```

After the 30s timeout: the spinner stops, the error message renders with a [Try again] button that retries delivery immediately (resets the 30s timer). The Connect sheet remains open behind the error — the user can dismiss and see the empty dashboard, but the first-run flow (project + session creation) does not proceed until delivery succeeds. If the user dismisses the Connect sheet (navigates away), the pending delivery stays in `localStorage` and retries on next login.

### Settings — Claude Account section

New section in Settings, positioned between CONNECTION and ADMINISTRATION (admin only). Groups with "how you're connected to services" — host connection above, Claude connection here.

```
┌─────────────────────────────────┐
│ ...                             │
│  CONNECTION                     │
│  ┌─────────────────────────┐    │
│  │  Status    ● Connected  │    │
│  │  Sessions  4            │    │
│  └─────────────────────────┘    │
│                                 │
│  CLAUDE ACCOUNT                 │  new section
│  ┌─────────────────────────┐    │
│  │  Status  ● Connected    │    │  green dot
│  │  [Reconnect] [Disconnect]│   │
│  └─────────────────────────┘    │
│                                 │
│  ADMINISTRATION (admin only)    │
│  ...                            │
└─────────────────────────────────┘
```

**States:**

| State | Status line | Actions |
|-------|------------|---------|
| Connected | `● Connected` (green dot) | [Reconnect] [Disconnect] |
| Token invalid | `● Token invalid` (red dot) | [Reconnect] |
| Not connected | `○ Not connected` (gray dot) | [Connect] |

**"Token invalid" detection**: Settings shows "Token invalid" when ANY active session reports `status: "token_expired"` in session KV. The SPA already watches all session KV entries; if any session's status is `token_expired`, the Settings section reflects this. If no sessions are active (nothing to detect against), Settings shows "Connected" — the token is presumed valid until proven otherwise. There is no proactive token validation call to Anthropic from the SPA.

- **[Connect]** / **[Reconnect]**: opens the Connect sheet (bottom sheet with OAuth button + collapsed paste input — same component used in first-run). After success, sheet auto-dismisses and Settings updates.
- **[Disconnect]**: confirmation dialog showing impact ("Disconnect your Claude account? All active sessions on N hosts will stop."). On confirm:
  1. Delete `tokens.anthropic` from `mclaude-user-info-{uslug}` KV.
  2. Publish `token-revoke` to all accepted-grant hosts (same as per-host [Revoke] but for every host). User-agents delete from Secret; active sessions stop.
  3. No Anthropic-side token revocation — Anthropic does not expose a public revocation endpoint. The token remains valid at Anthropic until its natural expiry (~1 year). Users who want to revoke at Anthropic's side can do so manually at `claude.ai/settings/claude-code`.

### Token failure — inline prompt

When a session fails due to an invalid or revoked token, the SPA shows an inline banner at the top of the session view:

```
┌──────────────────────────────────┐
│ ⚠ Your Claude token is invalid   │
│          [Reconnect]             │
└──────────────────────────────────┘
```

- Banner uses `--warning` background, `--text` foreground.
- [Reconnect] opens the Connect sheet (same bottom sheet as Settings → Reconnect and first-run).
- After reconnect, SPA auto-re-delivers K_wrapped to all trusted hosts. Banner disappears once the session pod confirms token availability.
- Detection: session-agent reports token failure via session state in KV (`status: "token_expired"`). SPA watches session KV and renders the banner when this status appears.
- This is expected to be rare — tokens are ~1-year lived. Primary triggers: user revoked at claude.ai, Anthropic invalidated the token, or subscription lapsed.

### Settings — Host detail (Claude authorization)

Extends the existing host detail screen (spec-host-picker.md § Settings → Hosts → host detail) with Claude authorization status.

All hosts (owned and granted) receive K_wrapped automatically after grant acceptance — no explicit [Authorize] step. The X25519 fingerprint verification from ADR-0091 Phase 2 happens at the grant-accept level, not at the token-delivery level. Once the grant is accepted, all subsequent token deliveries are automatic.

```
┌──────────────────────────────────┐
│ ← macbook-pro                    │
├──────────────────────────────────┤
│  Status: ● Online                │
│  Type:   🖥️ Machine (you)        │
│  Projects: 3                     │
│                                  │
│  CLAUDE ACCESS                   │  new sub-section
│  ┌────────────────────────────┐  │
│  │  ● Authorized              │  │  green dot
│  │             [Revoke]       │  │  red text, destructive
│  └────────────────────────────┘  │
│                                  │
│  PROJECTS                        │
│  ...                             │
└──────────────────────────────────┘
```

**Claude Access states:**

| State | Display | Action |
|-------|---------|--------|
| Authorized | `● Authorized` (green) | [Revoke] |
| Pending delivery (grant accepted, token not yet delivered) | `○ Pending delivery` (yellow) | Auto-delivers on next SPA visit when user-agent is online |
| Awaiting acceptance (grant pending) | `○ Awaiting acceptance` (gray) | N/A — grant auto-accepted for default platform; manual accept for non-default (deferred) |
| Revoked | `○ Revoked` (red) | [Re-authorize] — see below |
| Host offline | `○ Host offline` (gray) | Delivery queued; auto-delivered when host comes back |

**[Revoke] behavior:**

Confirmation dialog showing impact:

```
┌──────────────────────────────────┐
│ Revoke Claude access on          │
│ macbook-pro?                     │
│                                  │
│ 2 active sessions will be stopped│
│                                  │
│      [Cancel]  [Revoke]          │
└──────────────────────────────────┘
```

- [Revoke] is red/destructive style.
- On confirm: SPA publishes revoke request to user-agent via NATS (`mclaude.users.{uslug}.hosts.{hslug}.agent.token-revoke` with `{ "provider": "anthropic" }`). User-agent deletes the provider key from `user-tokens-{uslug}` Secret. Active session pods lose their token mount on next kubelet sync and stop.
- Host enters "Revoked" state in the UI. [Re-authorize] button appears to re-deliver.

**[Re-authorize] behavior**: immediate silent re-delivery, no confirmation. On click: SPA reads token from KV, reads user-agent X25519 pubkey from CP, wraps, delivers via NATS request/reply. On success: host transitions to "Authorized." On failure (user-agent offline): SPA shows inline error "Host is offline — try again later" and the host stays in "Revoked" state. The pending delivery is NOT written to localStorage (unlike first-run) — re-authorize is an explicit user action with immediate feedback, not a background retry.

### Token reconnect — re-delivery to all hosts

When the user reconnects (via inline session banner, Settings → [Reconnect], or first-run), after the new token is written to KV, the SPA automatically re-delivers K_wrapped to **every host with an accepted grant**:

1. SPA reads the list of accepted grants from CP: `GET /api/users/{uslug}/grants` (filters to `status === "accepted"` client-side).
2. For each accepted grant, SPA reads the user-agent's X25519 pubkey from CP: `GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey`.
3. SPA wraps the new token and delivers via NATS request/reply to each user-agent.
4. SPA shows a brief progress indicator during delivery: "Updating N hosts…" → "Done" (or "N of M hosts updated — N offline hosts will receive the token on your next visit").

User-agents that are offline at reconnect time retain the old token in their Secret. On the user's next SPA visit (or login from any device), the SPA detects the stale token state and re-delivers. There is no background retry — delivery requires an online SPA and an online user-agent simultaneously.

**How the SPA detects stale tokens on hosts:** on login hydration, the SPA reads the token from KV and reads each host's authorization status from CP. If a host's grant is accepted but the user-agent reports no token (or an older token version), the SPA re-delivers automatically. The user-agent exposes token availability via a new NATS subject `mclaude.users.{uslug}.hosts.{hslug}.agent.status` with payload `{ "hasToken": true, "tokenVersion": "<sha256-prefix>" }`. The SPA compares the reported `tokenVersion` (first 8 hex chars of `sha256(token)`) with its own token's hash. Mismatch → re-deliver.

### Admin: per-user default platform assignment

**Not needed.** The default platform is universal by definition — all users get auto-granted. If an admin doesn't want a user on the platform, the user shouldn't have an mclaude account at all. Per-user opt-out would contradict the meaning of "default."

No admin toggle, no invite-time checkbox. The default platform grants all users unconditionally. Access control happens at the user-account level (don't create the account), not at the platform-grant level.

## Open questions

_(none — all resolved)_

## User Flow

### First-time user (end to end)

1. User logs in via SSO / device-code.
2. SPA hydrates KV → no `tokens.anthropic` → Connect sheet auto-opens (over empty background, [✕] disabled).
3. User clicks "Sign in with Claude" → popup opens to claude.ai OAuth PKCE → user authorizes → popup returns code → SPA exchanges for access token → writes plaintext token to KV.
4. SPA reads default platform grant from CP → reads user-agent X25519 pubkey → wraps token → delivers via NATS.
5. Spinner: "Setting up your environment…" (30s timeout).
6. User-agent unseals → writes to Secret → ready.
7. SPA creates Default project → creates Getting Started session → navigates to session → onboarding message.

### Returning user (token valid)

1. Login → hydrate KV → `tokens.anthropic` exists → dashboard renders normally.
2. On login hydration, SPA checks each accepted-grant host's token version. If stale → auto-re-delivers.

### Returning user (token invalid)

1. Login → hydrate KV → `tokens.anthropic` exists → dashboard renders.
2. User opens session → session-agent reports `token_expired` → inline banner: "Your Claude token is invalid [Reconnect]."
3. User clicks [Reconnect] → Connect sheet → OAuth → new token → re-delivered → banner clears, session resumes.

### Disconnect

1. User opens Settings → CLAUDE ACCOUNT → [Disconnect].
2. Confirmation: "Disconnect Claude account? All active sessions on N hosts will stop."
3. On confirm: delete from KV → revoke from all hosts → sessions stop → Settings shows "Not connected."

### Per-host revoke

1. User opens Settings → Hosts → host detail → CLAUDE ACCESS → [Revoke].
2. Confirmation: "Revoke Claude access on {host}? N active sessions will be stopped."
3. On confirm: publish token-revoke → user-agent deletes from Secret → sessions stop → host shows "Revoked."

## Component Changes

### mclaude-web (SPA)

- **Connect sheet**: new bottom sheet component (reused for first-run, Settings reconnect, session banner reconnect). OAuth popup via PKCE, manual paste, error states.
- **OAuth popup flow**: The SPA uses the same `client_id` as the CLI (`mclaude-cli`). Hardcoded as a constant in the SPA source (e.g., `const OAUTH_CLIENT_ID = "mclaude-cli"`). No build-time injection or Helm knob needed — the client_id is not a secret and is identical across environments. If a deployment needs a different client_id in the future, it can be moved to a Vite `define` entry at that time. The `code_verifier` stays in the parent window's memory — no cross-window storage needed. The token exchange goes through a CP proxy to avoid CORS issues. The flow:
  1. **Parent window** generates PKCE `code_verifier` (32 random bytes, base64url), `code_challenge` (SHA256 S256), and `state` (32 random bytes, base64url). Keeps `code_verifier` and `state` in JavaScript variables (not storage — they don't leave the parent's memory).
  2. **Parent window** opens popup to `claude.ai/oauth/authorize?response_type=code&client_id={CLIENT_ID}&redirect_uri={SPA_ORIGIN}/auth/oauth/callback&code_challenge={CHALLENGE}&code_challenge_method=S256&state={STATE}`. No `scope` parameter — matches the CLI implementation, which relies on server defaults.
  3. **Popup** redirects to claude.ai, user authorizes, redirects back to `{SPA_ORIGIN}/auth/oauth/callback?code={CODE}&state={STATE}`.
  4. **Callback page** (`/auth/oauth/callback`) is a **static HTML page** served by nginx outside the SPA's HashRouter. It is minimal: extracts `code` and `state` from URL query params and posts them to the parent window:
  ```
  window.opener.postMessage({
    type: "mclaude:oauth-callback",
    code: "<authorization_code>",
    state: "<state>"
  }, window.location.origin)
  window.close()
  ```
  5. **Parent window** listens for `message` events. Validates `origin === window.location.origin` and `data.type === "mclaude:oauth-callback"`. Verifies `data.state === expectedState` (CSRF check — `expectedState` is in the parent's memory from step 1). If mismatch → display error in sheet.
  6. **Token exchange**: Parent calls CP proxy `POST /api/auth/oauth/exchange` with `{ code, code_verifier, redirect_uri, client_id }`. CP forwards to `POST claude.ai/oauth/token` server-side (no CORS issues) and returns `{ access_token }`. This avoids the browser making a cross-origin POST to claude.ai directly.
  7. **Parent** writes the access token to KV, updates the Connect sheet to "Connected", auto-dismisses.
- **New CP endpoint: `POST /api/auth/oauth/exchange`** — proxies the OAuth token exchange to `claude.ai/oauth/token`. Request: `{ "code": "...", "code_verifier": "...", "redirect_uri": "...", "client_id": "..." }`. CP adds `grant_type=authorization_code` and forwards as a form-encoded POST to the Anthropic token endpoint. Returns `{ "access_token": "..." }` on success, `{ "error": "...", "error_description": "..." }` on failure. Auth: requires valid mclaude user JWT (existing auth middleware). CP does not store the token — it passes through. Added to Impact section.
- **First-run gate**: after login hydration, if `tokens.anthropic` absent in KV → open Connect sheet (auto-open, [✕] disabled). After connect → auto-deliver to default platform → proceed to existing first-run flow.
- **Settings → CLAUDE ACCOUNT**: new section between CONNECTION and ADMINISTRATION. Shows token status (Connected/Token invalid/Not connected) with [Connect]/[Reconnect]/[Disconnect] actions.
- **Settings → Host detail → CLAUDE ACCESS**: new sub-section showing per-host authorization state with [Revoke]/[Re-authorize].
- **Token failure banner**: renders on `token_expired` session status in KV. [Reconnect] opens Connect sheet.
- **Re-delivery on reconnect**: after new token written to KV, iterate accepted grants → wrap → deliver to each user-agent. Progress indicator: "Updating N hosts…"
- **Stale token detection on login**: compare KV token hash with each user-agent's reported `tokenVersion`. Auto-re-deliver if stale.
- **Disconnect flow**: delete from KV + publish token-revoke to all accepted-grant hosts.
- **Pending delivery retry**: `localStorage` key `mclaude.pendingDeliveries` (JSON array of `{ hslug, provider }`). Retried on login/reconnect, removed on success, cleared on logout.
- **libsodium-wrappers**: for X25519 sealed-box wrapping (already planned by ADR-0091; this ADR specifies when wrapping is triggered).

### mclaude-session-agent

- Report `status: "token_expired"` to session KV when the mounted token fails (Anthropic API returns 401/403). This is a new session status value surfaced to the SPA.
- **Recovery**: session-agent watches the mounted Secret directory (`/secrets/tokens/`) via `fsnotify` (or polls every 30s). When the token file is updated (user-agent writes new token after re-delivery), session-agent restarts the session process via `driver.Resume` with the new token injected as an env override. The session transitions to `idle` on the init event (then `running` on the next user message). This is a process restart (not an in-process retry) — the running process has the old token baked into its environment. No user action required.

### mclaude-user-agent

- New NATS subject: `mclaude.users.{uslug}.hosts.{hslug}.agent.token-revoke`. Request/reply. SPA sends `{ "provider": "anthropic" }`, user-agent replies `{ "ok": true }` on success or `{ "ok": false, "error": "..." }` on failure. On success: user-agent deletes the provider key from `user-tokens-{uslug}` Secret. SPA uses `nc.Request()` with a 10s timeout.
- New NATS subject: `mclaude.users.{uslug}.hosts.{hslug}.agent.status`. Request/reply. SPA sends `{}` (empty request), user-agent replies with `{ "hasToken": bool, "tokenVersion": "<first-8-hex-of-sha256>" }`. SPA uses `nc.Request()` with a 5s timeout per host during login hydration. User-agent also publishes unsolicited on this subject on startup and on token change (for any SPA that happens to be subscribed), but the SPA's stale-detection flow uses request/reply, not subscribe-and-wait.

### mclaude-cli

- `mclaude auth anthropic --revoke`: extend to publish token-revoke to all accepted-grant hosts (currently KV-only). Behavior: delete token from KV first (always succeeds), then iterate accepted-grant hosts and send `token-revoke` request/reply to each (10s timeout per host). Continue on per-host failure — print warning `"Warning: could not revoke on {hslug}: {error}"` to stderr. Exit code: `ExitAuthSuccess` if KV delete succeeded (even if some hosts failed). Print summary: `"Token removed. Revoked on N/M hosts."` If all hosts fail: `"Token removed from account. Warning: N hosts could not be reached — token may still be cached on those hosts."`

## Data Model

### KV format: `tokens.anthropic` (unchanged)

Plain string — the OAuth access token (`sk-ant-oat01-...`) or a manually pasted API key. No schema change from ADR-0091.

### New NATS subjects

**`mclaude.users.{uslug}.hosts.{hslug}.agent.token-revoke`** — request/reply. SPA/CLI publishes request `{ "provider": "anthropic" }`. User-agent replies `{ "ok": true }` on success or `{ "ok": false, "error": "..." }` on failure. On success, user-agent deletes the provider key from the `user-tokens-{uslug}` Secret. SPA uses `nc.Request()` with 10s timeout. Falls under the existing `agent.>` wildcard in user JWT permissions.

**`mclaude.users.{uslug}.hosts.{hslug}.agent.status`** — request/reply. SPA publishes `{}` (empty request). User-agent replies:

```json
{
  "hasToken": true,
  "tokenVersion": "a1b2c3d4"
}
```

`tokenVersion` is the first 8 hex chars of `sha256(token)`. Used by SPA to detect stale tokens without transmitting the token itself. SPA uses `nc.Request()` with 5s timeout per host during login hydration. User-agent also publishes unsolicited on this subject on startup and on token change. Falls under the existing `agent.>` wildcard in both user-agent and user JWT permissions.

### New session status value

`token_expired` — reported by session-agent when the mounted Anthropic token fails (401/403). SPA watches for this status in session KV and renders the failure banner. Added to the canonical `SessionState.status` enum in `spec-state-schema.md`.

### localStorage schema

**`mclaude.pendingDeliveries`** — JSON array of pending token deliveries. Each entry: `{ "hslug": "string", "provider": "string" }`. Written when delivery fails during first-run or reconnect. Retried on login/NATS reconnect. Entry removed on successful delivery. Entire key cleared on logout.

## Error Handling

| Error | Where detected | User sees | Recovery |
|-------|---------------|-----------|----------|
| OAuth popup blocked | SPA (popup.closed + no postMessage) | "Allow popups for this site and try again" | User allows popups, retries |
| OAuth failure (code exchange fails) | SPA (popup postMessage with error) | "Sign-in failed — try again or enter a token manually" | Retry or use paste |
| Paste token invalid (empty/whitespace) | SPA (client validation) | "Please enter a valid token" | Re-paste |
| K_wrapped delivery fails (user-agent offline) | SPA (NATS request timeout) | First-run: spinner → 30s timeout → "Your host isn't responding — try again later or contact your admin". Reconnect: "N of M hosts updated — N offline hosts will receive the token on your next visit" | Retry on next login/visit (pending delivery in localStorage) |
| Token invalid mid-session | Session-agent (Anthropic API 401/403) | Inline banner: "Your Claude token is invalid [Reconnect]" | User reconnects via Connect sheet |
| Disconnect with active sessions | SPA (pre-action check) | Confirmation: "N active sessions on M hosts will stop" | User confirms or cancels |
| Revoke with active sessions | SPA (pre-action check) | Confirmation: "N active sessions will be stopped" | User confirms or cancels |
| User-agent token-revoke fails | SPA (NATS request timeout) | "Could not reach {host} — token may still be active. Try again when the host is online." | Retry when host online |

## Security

- **Token in KV**: protected by NATS RBAC (user JWT scoped to own bucket) + JetStream at-rest encryption. Plaintext string — same security model as ADR-0091.
- **Token version (`sha256` prefix)**: user-agent reports 8-char hex prefix of `sha256(token)`. This is not reversible — SPA can compare versions without the user-agent ever transmitting the token.
- **OAuth popup**: popup window is same-origin; `postMessage` validates origin. No cross-origin token leakage.
- **No Anthropic-side revocation**: after Disconnect, the token remains valid at Anthropic until its natural expiry (~1 year). Users concerned about this can manually revoke at `claude.ai/settings/claude-code`. This is a known limitation, not a vulnerability — the token was already trusted to Anthropic.
- **Pending deliveries in localStorage**: contains only `{ hslug, provider }` — no token data. The token itself is in NATS KV (RBAC-protected).

## Impact

Specs updated in this commit:

- `docs/ui/spec-first-run-flow.md`: add token gate (Connect sheet) before project/session creation.
- `docs/ui/mclaude-web/spec-settings-web.md`: add CLAUDE ACCOUNT section; update host detail with CLAUDE ACCESS sub-section.
- `docs/mclaude-web/spec-web.md`: add Connect sheet component, OAuth popup, token failure banner, re-delivery logic, disconnect/revoke flows, stale token detection, pending delivery retry.
- `docs/spec-state-schema.md`: add `token_expired` to `SessionState.status` enum; add `mclaude.pendingDeliveries` localStorage key.
- `docs/spec-nats-data-taxonomy.md`: add `agent.token-revoke` and `agent.status` subjects under `agent.>` hierarchy.
- `docs/mclaude-session-agent/spec-session-agent.md`: add `token_expired` status reporting.
- `docs/mclaude-user-agent/spec-user-agent.md`: add `token-revoke` handler; add `status` subject.
- `docs/mclaude-cli/spec-cli.md`: extend `--revoke` to publish to hosts.
- `docs/mclaude-control-plane/spec-control-plane.md`: add `POST /api/auth/oauth/exchange` proxy endpoint.

## Scope

**In v1:**
- First-run token gate (Connect sheet before project/session creation)
- Connect sheet: OAuth PKCE popup + manual paste, reused from all entry points
- Settings → CLAUDE ACCOUNT section (Connected/Token invalid/Not connected)
- Settings → Host detail → CLAUDE ACCESS (Authorized/Pending/Revoked)
- Reactive token failure detection (session-agent reports `token_expired`)
- Auto-delivery to all hosts after grant acceptance (no [Authorize] step)
- Per-host [Revoke] with session impact confirmation
- [Disconnect] = KV + all-host revoke (no Anthropic-side revocation)
- Re-delivery to all hosts on reconnect
- Stale token detection on login via `tokenVersion`
- New NATS subjects: `agent.token-revoke`, `agent.status`
- New session status: `token_expired`
- Pending delivery retry via localStorage

**Deferred:**
- Anthropic-side token revocation (pending public revocation endpoint)
- Non-default host manual accept UX (ADR-0091 deferred)
- Silent token refresh (if Anthropic adds shorter-lived tokens with refresh support)

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| First-run gate blocks session creation | New user login → Connect sheet appears → no project/session until token written | Create user, login | SPA, KV |
| OAuth popup completes and writes token | Click "Sign in with Claude" → popup → token string in KV | Login, trigger connect | SPA, OAuth (mock), KV |
| Manual paste writes token | Paste token → KV has plain string | Login, trigger connect | SPA, KV |
| First-run auto-delivers to default platform | After connect → K_wrapped delivered to default platform user-agent → session creation succeeds | User + default platform host + user-agent running | SPA, CP, user-agent, NATS |
| Delivery failure shows timeout | User-agent offline → 30s spinner → "host isn't responding" message → pendingDeliveries written | User + default platform host, user-agent down | SPA, localStorage |
| Pending delivery retried on login | pendingDeliveries has entry → login → delivery retried → entry removed on success | Pre-set localStorage, restart user-agent | SPA, NATS, user-agent |
| Reactive token failure detection | Session-agent sets status: token_expired → SPA shows "token is invalid" banner | Running session, invalidate token on host | SPA, session-agent, KV |
| Settings shows CLAUDE ACCOUNT | Login with token → Settings shows "Connected" with Reconnect/Disconnect | Login with token | SPA, KV |
| Disconnect revokes all hosts | Click Disconnect → KV cleared + token-revoke published to all hosts | Login + 2 hosts with accepted grants | SPA, KV, NATS, user-agents |
| Per-host revoke stops sessions | Click Revoke on host → token deleted from Secret → sessions stop | Login + host with active sessions | SPA, NATS, user-agent |
| Re-authorize after revoke | Revoke → Re-authorize → K_wrapped re-delivered → host authorized | Login + revoked host | SPA, NATS, user-agent |
| Reconnect re-delivers to all hosts | Token invalid → Reconnect → new token delivered to all accepted-grant hosts | Login + 2 hosts | SPA, KV, NATS, user-agents |
| Stale token detection on login | Host has old tokenVersion → SPA auto-re-delivers on login | Login + host with stale token | SPA, CP, NATS, user-agent |

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| mclaude-web (SPA) | ~200 lines | 150k | Connect sheet, OAuth popup, token failure banner, re-delivery, disconnect/revoke, stale detection, host detail CLAUDE ACCESS, pending delivery |
| mclaude-session-agent | ~15 lines | 30k | token_expired status reporting on 401/403 |
| mclaude-user-agent | ~50 lines | 60k | token-revoke handler, status subject |
| mclaude-cli | ~10 lines | 30k | --revoke host broadcast |

**Total estimated tokens:** 270k
**Estimated wall-clock:** 3-4h
