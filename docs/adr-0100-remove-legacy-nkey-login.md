# ADR: Remove legacy server-generated NKey login path

**Status**: accepted
**Status history**:
- 2026-05-06: accepted

## Overview

Remove the legacy `IssueUserJWTLegacy` path from `POST /auth/login`. All clients (SPA, CLI) must generate their own NKey pair and send `nkey_public` in the login request body. The server never generates NKey seeds for clients. Additionally, the default-platform auto-grant must fire during the login handler (not only during challenge-response auth), so that users are immediately granted access to the default platform on login.

## Motivation

1. **Server should never hold client private keys.** The legacy path generates an NKey pair server-side and returns the seed to the client. This violates the NKey trust model — the client should be the sole owner of its private key material.

2. **Auto-grant doesn't fire on login.** The default-platform auto-grant (`UpsertHostGrant` + `grants.create` publish) lives in `issueJWTForNKey` (challenge-response path). The `POST /auth/login` handler calls `IssueUserJWT` directly — it never calls `issueJWTForNKey` — so users logging in via the SPA or CLI never trigger the auto-grant. This means the worker controller never receives `grants.create` and never provisions the user-agent namespace.

3. **The legacy path is marked DEPRECATED.** The comment at `auth.go:182` says "will be removed once all clients support nkey_public." Both the SPA and CLI now support NKey generation — the migration is complete.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Legacy NKey path | Remove `IssueUserJWTLegacy` and the `else` branch at `auth.go:180-191` | All clients generate their own NKeys. Server never generates seeds. |
| `nkey_public` in login | Required field. Return 400 if absent. | Enforces client-owned NKeys. |
| `nkeySeed` in login response | Remove from `LoginResponse`. Never returned. | Server doesn't have the seed. |
| Auto-grant in login handler | After issuing the JWT, run the same default-platform auto-grant logic as `issueJWTForNKey`: query `GetDefaultPlatformHostSlug`, `UpsertHostGrant`, publish `grants.create`. | Users get access to the default platform immediately on login. |
| Auto-grant in device-code poll | Same — when the device-code poll returns `authorized`, fire the auto-grant. | CLI login also needs auto-grant. |
| SPA login | SPA must generate an NKey pair before calling `POST /auth/login` and include `nkey_public` in the request body. Store the seed in localStorage alongside the JWT. | SPA already has the NKey generation code in auth-store.ts. |

## Component Changes

### `mclaude-control-plane`

**`server/auth.go`:**
- Remove the `else` branch at line 180-191 (`IssueUserJWTLegacy` call).
- Make `nkey_public` required in `LoginRequest`. Return 400 `{error: "nkey_public is required"}` if empty.
- Remove `NKeySeed` field from `LoginResponse`.
- After JWT issuance (line 174), add the default-platform auto-grant block (same as `challenge.go:257-269`): query `GetDefaultPlatformHostSlug`, `UpsertHostGrant(defaultHostSlug, user.Slug, "accepted")`, publish `mclaude.hosts.{defaultHostSlug}.grants.create` with `{uslug}`.
- Remove `IssueUserJWTLegacy` function from `auth/jwt.go` (or wherever it lives).

**`server/auth.go` — device-code poll handler:**
- When poll returns `status: "authorized"`, fire the same auto-grant logic after JWT issuance.

### `mclaude-web`

**`src/stores/auth-store.ts`:**
- Generate NKey pair before login: `const kp = nkeys.createUser(); const nkeyPublic = kp.getPublicKey(); const nkeySeed = kp.getSeed();`
- Include `nkey_public` in the `POST /auth/login` request body.
- Store `nkeySeed` in localStorage (alongside JWT) for NATS connection.
- Remove any code that reads `nkeySeed` from the login response (server no longer returns it).

### `mclaude-cli`

- Already generates NKey and sends `nkey_public` in device-code flow. No changes needed (verify only).

## Impact

- `docs/mclaude-control-plane/spec-control-plane.md`: update login handler to require `nkey_public`, remove `nkeySeed` from response, add auto-grant on login.
- `docs/spec-state-schema.md`: note that `users.nkey_public` is always populated after login (no NULL for active users).
- `docs/mclaude-web/spec-web.md`: update login flow to generate NKey client-side.
- `docs/mclaude-cli/spec-cli.md`: verify device-code flow sends `nkey_public` (already does).

## Error Handling

- **`nkey_public` omitted in login request:** 400 `{error: "nkey_public is required"}`.
- **Invalid `nkey_public` format:** 400 `{error: "invalid nkey_public format"}` (validate U-prefix, 56-char base32).

## Integration Test Cases

| Test case | What it verifies |
|-----------|------------------|
| Login with `nkey_public` triggers auto-grant | `POST /auth/login` with `nkey_public` → `host_grants` row created for default platform, `grants.create` published |
| Login without `nkey_public` returns 400 | `POST /auth/login` without `nkey_public` → 400 `{error: "nkey_public is required"}` |
| Login response has no `nkeySeed` | `POST /auth/login` response body has no `nkeySeed` field |
| Device-code poll triggers auto-grant | Authorized device-code poll → `host_grants` row created for default platform |
