# ADR: Remove Admin HTTP Routes and SCIM Stubs

**Status**: implemented
**Status history**:
- 2026-05-06: draft
- 2026-05-06: accepted
- 2026-05-06: implemented — mclaude-control-plane CLEAN

## Overview

Remove all `/admin/*` HTTP routes and `/scim/v2/*` stubs from the control plane. User creation and promotion are handled entirely by the `BOOTSTRAP_ADMIN_EMAIL`/`BOOTSTRAP_ADMIN_PASSWORD` startup upsert in `init-keys` — there is no scenario where an operator needs the HTTP admin routes that does not already imply direct DB or environment access. The SCIM routes were never implemented. Removing both surfaces reduces attack surface and eliminates dead code.

## Motivation

1. **Circular dependency.** All admin routes require `isAdminMiddleware` (JWT + `is_admin=true`). The only way to get `is_admin=true` is the `init-keys` Job, which does a direct DB upsert from `BOOTSTRAP_ADMIN_EMAIL`/`BOOTSTRAP_ADMIN_PASSWORD` env vars. Anyone who can deploy the Helm chart can already create admin users without HTTP. The HTTP surface adds no capability and only adds attack surface.

2. **SCIM was never implemented.** `scim.go` (308 lines) returns stub responses but never persists any data. It was scaffolded in anticipation of IdP integration that has not materialized. Dead code and spec drift.

3. **`POST /admin/sessions/stop`** publishes a NATS `sessions.delete` message. This was the one potentially-useful break-glass op, but the user has decided it is not worth the attack surface.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Remove /admin/* routes | Yes — all of them | Superseded by bootstrap upsert; circular dependency makes them unreachable without DB access anyway |
| Remove /scim/v2/* stubs | Yes — delete scim.go entirely | Never implemented; spec drift; IdP integration not planned |
| Remove `isAdminMiddleware` | Yes | No routes remain behind it |
| `is_admin` DB column | Retain | Still gates `defaultPlatform=true` in host registration (ADR-0097) and admin-as-operator bypass in NATS lifecycle handlers |
| Admin bypass in auth middleware slug-match (auth.go:381) | Remove | Vestigial with no admin routes; widens blast radius of compromised admin account |
| `requireSelfOrAdmin` in hosts.go (grants/accept/pubkey endpoints) | Remove | Consistent with removing HTTP admin bypass entirely. Admins who need cross-user access should use NATS directly (wider NATS grant — deferred to future ADR) rather than an HTTP bypass path |
| `is_admin` gates in NATS lifecycle handlers (manage.deregister, manage.revoke, manage.update) | Retain | These are operator safety valves: an admin can deregister/revoke/update any host regardless of ownership. Retained as platform operator tools distinct from the removed HTTP admin surface. |
| Integration tests for removed routes | Delete test cases; add regression tests asserting 404 | Per ADR-0097 pattern (TestAdminClusterEndpoints_Removed) |

## User Flow

There is no user-visible change. Operators who previously used `POST /admin/users` to create users must use `BOOTSTRAP_ADMIN_EMAIL`/`BOOTSTRAP_ADMIN_PASSWORD` env vars in the Helm values instead (this was already the correct path).

## Component Changes

### mclaude-control-plane

**Files deleted:**
- `server/admin.go` — all admin HTTP handlers and types (`AdminUserRequest`, `AdminUserResponse`, `AdminSessionStopRequest`, `handleAdminRoutes`, `adminListUsers`, `adminCreateUser`, `adminDeleteUser`, `adminStopSession`, `adminPromoteUser`)
- `server/scim.go` — all SCIM types and handlers (`SCIMUser`, `SCIMName`, `SCIMEmail`, `SCIMMeta`, `SCIMListResponse`, `SCIMError`, `handleSCIMRoutes`, `scimListUsers`, `scimGetUser`, `scimCreateUser`, `scimUpdateUser`, `scimDeleteUser`, `scimError`)
- `server/breakglass_test.go` — not deleted wholesale; `TestMetricsEndpoint_MainMux` (lines 32–43) has no `admin.go` dependency and must be retained. All other tests in the file call `admin.go` symbols and must be deleted. Action: remove `TestAdminCreateUser_MissingFields`, `TestAdminDeleteUser_NilDB`, `TestAdminStopSession_MissingFields`, `TestAdminStopSession_ValidRequest`, `TestAdminRoutes_UnknownRoute`, `TestAdminClusterEndpoints_Removed`, `TestAdminUserResponse_JSONShape` from `breakglass_test.go`; keep `TestMetricsEndpoint_MainMux` in place.
- `server/admin_device_test.go` — not deleted wholesale; file contains 10 tests, only 2 depend on `AdminUserResponse` (`TestAdminUserResponse_SlugField` line 23, `TestAdminUserResponse_SlugEmpty` line 58). The 8 remaining tests (`TestCLIDeviceCodeCreate_*`, `TestCLIDeviceCodeVerify_*`, `TestProjectResponse_*`) have no `admin.go` dependency and must be preserved. Action: remove the 2 `AdminUserResponse` tests from `admin_device_test.go` (or delete those test functions); the remaining 8 tests stay in place.
- `server/device_auth_integration_test.go` — remove call to `srv.adminCreateUser` (line 410) and reference to `AdminUserResponse` (line 416); replace with direct `db.CreateUser` + `db.SetUserAdmin` calls for test-user seeding.

**Files modified:**
- `server/server.go` — remove `mux.Handle("/admin/", ...)`, `mux.Handle("/scim/v2/", ...)`, and `isAdminMiddleware` method
- `server/auth.go` — remove admin bypass block (~line 381): the `if !user.IsAdmin { return }` check inside the `/api/users/{uslug}/...` slug-match enforcement
- `server/hosts.go` — remove `requireSelfOrAdmin` function (hosts.go:661–682) and update its three callers (`handleListGrants`, `handleGrantAccept`, `handleUserAgentPubkey`) to use self-only access enforcement
- `server/cluster_test.go` — remove `AdminUserRequest`/`AdminUserResponse` usages (lines 457–475) and replace live HTTP `POST /admin/users` call with direct `db.CreateUser` + `db.SetUserAdmin` calls for test-user seeding (same pattern as `integration_test.go`); also remove line 450 (`jwt := adminJWT(t)`) whose only use is inside the removed block — leaving it causes a compile error (unused variable)

**Tests to delete:**
- `server/integration_test.go`: `TestIntegration_AdminCreateUser`, `TestIntegration_AdminListUsers`, `TestIntegration_AdminDeleteUser`, `TestIntegration_AdminMiddleware_RejectsNoToken`, `TestIntegration_AdminMiddleware_RejectsNonAdmin`, `TestIntegration_ADR0097_AdminCluster_Returns404` (calls `srv.handleAdminRoutes` directly — superseded by `TestAdminRoutes_AllReturn404`)
- `server/auth_test.go`: `TestIsAdminMiddleware_RejectsNoToken` (calls `srv.isAdminMiddleware` directly — method is deleted)
- `server/device_auth_integration_test.go`: `TestIntegration_AdminCreateUser_SlugInResponse` (lines 393–433, tested slug derivation via admin HTTP response; `AdminUserResponse` type deleted with `admin.go`)
- Any SCIM integration tests

**Tests to add** (all in `server/integration_test.go`):
- `TestAdminRoutes_AllReturn404` — verifies that GET `/admin/users`, POST `/admin/users`, DELETE `/admin/users/x`, POST `/admin/users/x/promote`, POST `/admin/sessions/stop`, GET `/scim/v2/Users` all return 404 (routes not registered)
- `TestAuthMiddleware_AdminBypassRemoved` — verifies that a user with `is_admin=true` gets 403 on `/api/users/{other-uslug}/...`, same as non-admin
- `TestRequireSelfOrAdmin_AdminCannotAccessOtherUserGrants` — verifies that a user with `is_admin=true` gets 403 on `GET /api/users/{other-uslug}/grants`

## Data Model

No schema changes. The `users.is_admin` column is retained.

## Error Handling

Requests to `/admin/*` or `/scim/v2/*` fall through to the default mux handler and return 404. No special handling needed.

## Security

Removing the admin HTTP surface and the admin bypass in `authMiddleware` reduces the blast radius of a compromised `is_admin=true` account. Such an account can still register a default-platform host (the only remaining `is_admin` gate), but can no longer read or modify arbitrary users' data via the API.

## Impact

**Specs updated in this commit:**
- `docs/mclaude-control-plane/spec-control-plane.md` — remove admin routes table, isAdminMiddleware description, SCIM table, related test matrix entries, and error/failure mode entries for admin routes
- `docs/spec-state-schema.md` — update `is_admin` column description (no longer gates HTTP admin routes; gates NATS lifecycle admin bypass and defaultPlatform registration); update `nkey_public` column description (remove reference to `POST /admin/users`); update Writers/Readers annotations

**Components implementing the change:**
- `mclaude-control-plane`

## Scope

**In scope:**
- Delete `server/admin.go` and `server/scim.go`
- Remove route registrations and `isAdminMiddleware` from `server/server.go`
- Remove admin bypass from `server/auth.go`
- Update tests

**Explicitly deferred / out of scope:**
- No replacement admin UI is planned
- No alternative break-glass session-stop mechanism is planned
- SCIM/IdP integration is not planned

## Open questions

(none)

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| `TestAdminRoutes_AllReturn404` | GET `/admin/users`, POST `/admin/users`, DELETE `/admin/users/x`, POST `/admin/users/x/promote`, POST `/admin/sessions/stop`, GET `/scim/v2/Users` all return 404 | None (no auth needed — routes not registered) | control-plane HTTP mux |
| `TestAuthMiddleware_AdminBypassRemoved` | A user with `is_admin=true` cannot access `/api/users/{other-uslug}/...` (gets 403, same as non-admin) | Create two users, set one `is_admin=true`, authenticate as admin, attempt to read other user's resource | control-plane auth middleware |
| `TestRequireSelfOrAdmin_AdminCannotAccessOtherUserGrants` | A user with `is_admin=true` gets 403 on GET `/api/users/{other-uslug}/grants` | Create two users, set one `is_admin=true`, authenticate as admin, call grants endpoint for other user | control-plane hosts.go handlers |

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| mclaude-control-plane | −600 lines (delete admin.go + scim.go) + ~20 lines changed (server.go, auth.go) + ~30 lines test additions | ~60k | Pure deletion + regression tests |

**Total estimated tokens:** ~60k
**Estimated wall-clock:** 0.5h
