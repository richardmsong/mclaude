## Audit: 2026-05-06T00:00:00Z

**Document:** docs/adr-0101-remove-admin-http-routes.md

## Run: 2026-05-06T12:00:00Z

**Gaps found: 5**

1. **`requireSelfOrAdmin` admin bypass not listed for removal** — The ADR only calls out the bypass at `auth.go:381–390` (the `isAdminMiddleware` slug-match block). But `hosts.go` contains a separate `requireSelfOrAdmin` function (hosts.go:661–682) that independently checks `user.IsAdmin` and returns nil (allowing access). This function is called from three HTTP handlers: `handleGetGrants` (hosts.go:504), `handleGrantAccept` (hosts.go:559), and `handleGetUserAgentPubkey` (hosts.go:632). The ADR's Security section claims removing the bypass "reduces blast radius" and prevents `is_admin=true` users from accessing other users' data — but `requireSelfOrAdmin` preserves exactly that capability for three endpoints. The ADR does not mention this function or direct the implementer to modify it.
   - **Doc**: "Admin bypass in auth middleware slug-match — Remove — The bypass at auth.go:381 allows any is_admin=true user to access any /api/users/{uslug}/... path."
   - **Code**: `hosts.go:661–682` — `requireSelfOrAdmin` independently grants admin bypass on `GET /api/users/{uslug}/grants`, `POST /api/users/{uslug}/hosts/{hslug}/grant/accept`, and `GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey`.

2. **`is_admin` gate in NATS host lifecycle handlers not scoped** — `lifecycle.go` uses `requester.IsAdmin` as an alternative to host ownership in three NATS handlers: `manage.deregister` (lifecycle.go:547), `manage.revoke` (lifecycle.go:637), and `manage.update` (lifecycle.go:786). The ADR's Security section says `is_admin=true` accounts "can still register a default-platform host (the only remaining is_admin gate)" — but these three NATS handlers are additional gates that survive the change. The implementer has no guidance on whether to retain or remove these admin-as-operator bypass paths.
   - **Doc**: "Security — Such an account can still register a default-platform host (the only remaining is_admin gate)"
   - **Code**: `lifecycle.go:547`, `lifecycle.go:637`, `lifecycle.go:786` — three additional `is_admin` gates remain in NATS handlers for deregister, revoke, and update.

3. **`breakglass_test.go` tests call deleted functions directly; no deletion instruction** — The ADR instructs deleting `admin.go` and its functions (`adminCreateUser`, `adminDeleteUser`, `adminStopSession`, `adminPromoteUser`, `handleAdminRoutes`). However, `breakglass_test.go` calls these methods directly (lines 68, 82, 108, 124, 136, 167) and references `AdminUserResponse` (line 179). Deleting `admin.go` will cause a compilation failure on `breakglass_test.go`. The ADR says "replace admin handler tests with `TestAdminRoutes_AllReturn404` regression test" — but it does not name `breakglass_test.go` as a file to delete or rewrite, and it does not list any of the `breakglass_test.go` tests in the "Tests to delete" list.
   - **Doc**: "Files modified — server/breakglass_test.go (or equivalent) — replace admin handler tests with TestAdminRoutes_AllReturn404 regression test"
   - **Code**: `breakglass_test.go:47–191` — tests `TestAdminCreateUser_MissingFields`, `TestAdminDeleteUser_NilDB`, `TestAdminStopSession_MissingFields`, `TestAdminStopSession_ValidRequest`, `TestAdminRoutes_UnknownRoute`, `TestAdminClusterEndpoints_Removed`, `TestAdminUserResponse_JSONShape` all call functions or reference types defined only in `admin.go`.

4. **`POST /admin/users/{uslug}/promote` not listed in any deletion or 404 regression test** — `admin.go:70–73` implements a promote endpoint (`POST /admin/users/{uslug}/promote`). The ADR lists this in the "Files deleted" types (`adminPromoteUser`) but never mentions the endpoint path `/admin/users/{uslug}/promote` in the integration test table. The `TestAdminRoutes_AllReturn404` test table only lists five paths: `GET /admin/users`, `POST /admin/users`, `DELETE /admin/users/x`, `POST /admin/sessions/stop`, `GET /scim/v2/Users`. Whether the regression test should also cover `POST /admin/users/{uslug}/promote` is unspecified. Additionally, `spec-state-schema.md` still describes `POST /admin/users/{uslug}/promote` as the mechanism for admin promotion in the `is_admin` column description.
   - **Doc**: "Tests to add — TestAdminRoutes_AllReturn404 — verifies that GET/POST/DELETE on /admin/users, /admin/sessions/stop, /scim/v2/Users all return 404"
   - **Code**: `admin.go:70–73` — `POST /admin/users/{uslug}/promote` is a distinct endpoint not covered by the listed test paths; `docs/spec-state-schema.md:24` still lists this endpoint as canonical.

5. **`spec-state-schema.md` stale after this ADR — no update instruction** — The `is_admin` column description in `spec-state-schema.md` (line 24) states: "Gate for /admin/* endpoints (user management) ... subsequent admin promotion via POST /admin/users/{uslug}/promote." The `nkey_public` column description (line 23) states: "NULL only for users created via POST /admin/users who have never logged in." The ADR's Impact section only lists `docs/mclaude-control-plane/spec-control-plane.md` as a spec to update; it does not mention `docs/spec-state-schema.md`. A developer following the ADR will leave the canonical state schema document inconsistent with the implemented behavior.
   - **Doc**: "Specs updated in this commit: docs/mclaude-control-plane/spec-control-plane.md — remove admin routes table, isAdminMiddleware description, SCIM table, related test matrix entries"
   - **Code**: `docs/spec-state-schema.md:23–24` — two column descriptions reference the now-deleted admin HTTP paths.

## Run: 2026-05-06T20:45:00Z

**Gaps found: 2**

1. **`spec-state-schema.md` auth.json description not updated — stale `mclaude admin users` reference** — The ADR's Impact section says to update `docs/spec-state-schema.md` for the `is_admin` column, `nkey_public` column, and Writers/Readers annotations. The `is_admin` column (line 24) and `nkey_public` column (line 23) are already correct in the current file. However, the "User-level credentials" section (line 842) still reads: "used for NATS-based CLI operations (`mclaude host register`, `mclaude import`, `mclaude admin users …`)". The `mclaude admin users` subcommand depends on admin HTTP routes being present. After this ADR removes those routes, this description is inaccurate and the state schema is the authoritative reference. The ADR's scope for spec-state-schema.md updates does not include this stale line, so a developer following the ADR exactly will leave the state schema inconsistent.
   - **Doc**: "Specs updated — docs/spec-state-schema.md — update `is_admin` column description ... update `nkey_public` column description ... update Writers/Readers annotations"
   - **Code**: `docs/spec-state-schema.md:842` — "NATS-based CLI operations (`mclaude host register`, `mclaude import`, `mclaude admin users …`)" references a subcommand that has no HTTP backend after this ADR

2. **`breakglass_test.go` listed as "Files modified" but should be deleted — action is ambiguous** — The ADR's Component Changes section lists `server/breakglass_test.go` under "Files modified" with the note "replace admin handler tests with `TestAdminRoutes_AllReturn404` in integration tests." However, every test in `breakglass_test.go` directly calls handler functions (`srv.adminCreateUser`, `srv.adminDeleteUser`, `srv.adminStopSession`, `srv.handleAdminRoutes`) and references types (`AdminUserResponse`) that are defined only in `admin.go`. Deleting `admin.go` will cause a compilation failure. The instruction "replace admin handler tests" does not tell the developer whether to (a) delete the entire `breakglass_test.go` file, or (b) rewrite it to contain `TestAdminRoutes_AllReturn404`. The tests to delete list does not include any `breakglass_test.go` test names. The replacement test `TestAdminRoutes_AllReturn404` is placed "in integration tests" — it is unclear whether that means it goes into `integration_test.go` or into the rewritten `breakglass_test.go`.
   - **Doc**: "server/breakglass_test.go — entire file deleted; all tests call functions defined only in admin.go ... Replaced by TestAdminRoutes_AllReturn404 in integration tests" (Component Changes, Files deleted section)
   - **Code**: `breakglass_test.go:47–191` — all seven test functions call `srv.adminCreateUser`, `srv.adminDeleteUser`, `srv.adminStopSession`, `srv.handleAdminRoutes`, or reference `AdminUserResponse` directly

   *Note: On re-read, the Files deleted section at line 43 does list `server/breakglass_test.go` as a deleted file. The ambiguity is whether "Replaced by TestAdminRoutes_AllReturn404 in integration tests" means the test goes into `integration_test.go` or a new file. This is the one sub-decision that would require a developer to stop and ask.*

## Run: 2026-05-06T22:30:00Z

**Gaps found: 2**

1. **`admin_device_test.go` and `cluster_test.go` reference deleted symbols but are not listed in the ADR's scope** — Deleting `admin.go` removes `adminCreateUser`, `AdminUserResponse`, and `AdminUserRequest`. Two test files outside the ADR's listed scope directly call or reference these symbols:
   - `server/admin_device_test.go` (build tag: `integration`) calls `srv.adminCreateUser(rec, req)` (line 410) and references `AdminUserResponse` (line 416) in `TestAdminCreateUser_SlugDerived`. This file is not listed under "Files deleted" or "Files modified."
   - `server/cluster_test.go` (build tag: `cluster`) uses `AdminUserRequest` (line 457) and `AdminUserResponse` (line 475) to create a test user via the admin HTTP endpoint. This file is not listed anywhere in the ADR.
   Deleting `admin.go` without also updating or deleting these two files will cause a compilation failure for both the `integration` and `cluster` build tags.
   - **Doc**: "Files deleted: server/admin.go ... server/breakglass_test.go" — neither `admin_device_test.go` nor `cluster_test.go` appear in the deleted or modified file lists.
   - **Code**: `server/admin_device_test.go:409–416`, `server/cluster_test.go:457–475`

2. **`cluster_test.go` uses `/admin/users` HTTP endpoint via live cluster call — replacement strategy unspecified** — `cluster_test.go:463` makes a real HTTP POST to `cluster.CPURL+"/admin/users"` to create a test user before running the project-creation integration test. After this ADR removes the `/admin/users` route, that HTTP call will return 404 and the test setup will fail. The ADR specifies a replacement regression test (`TestAdminRoutes_AllReturn404`) that verifies 404, but does not address how cluster tests that use admin HTTP to seed test data should create users in the future. The decision table says "Operators who previously used POST /admin/users must use BOOTSTRAP_ADMIN_EMAIL/BOOTSTRAP_ADMIN_PASSWORD env vars" — but that is not a viable strategy for dynamic test-user creation inside an integration test. A developer implementing this change must decide how to rewrite the test setup but has no guidance from the ADR.
   - **Doc**: "User Flow — Operators who previously used POST /admin/users must use BOOTSTRAP_ADMIN_EMAIL/BOOTSTRAP_ADMIN_PASSWORD env vars instead."
   - **Code**: `server/cluster_test.go:451–478` — creates a test user via `POST /admin/users` and depends on `AdminUserRequest`/`AdminUserResponse` types, which will be deleted.

## Run: 2026-05-06T23:45:00Z

**Gaps found: 2**

1. **Wrong file name for `srv.adminCreateUser` line 410 / `AdminUserResponse` line 416** — The ADR instructs: "server/admin_device_test.go — remove the call to `srv.adminCreateUser` (line 410) and reference to `AdminUserResponse` (line 416); replace with direct `db.CreateUser` + `db.SetUserAdmin` calls." But `server/admin_device_test.go` is only 307 lines and contains no call to `srv.adminCreateUser` and no `AdminUserResponse` at line 416. Those exact lines exist in `server/device_auth_integration_test.go` (confirmed at lines 410 and 416). A developer following the ADR would edit the wrong file and leave `device_auth_integration_test.go` with broken symbol references after `admin.go` is deleted.
   - **Doc**: "server/admin_device_test.go — remove the call to `srv.adminCreateUser` (line 410) and reference to `AdminUserResponse` (line 416)" (`Component Changes → Files modified`)
   - **Code**: `server/device_auth_integration_test.go:410` — `srv.adminCreateUser(rec, req)`; `server/device_auth_integration_test.go:416` — `var resp AdminUserResponse`. `server/admin_device_test.go` is 307 lines, no such call exists.

2. **`server/admin_device_test.go` uses `AdminUserResponse` as a struct literal — not addressed** — After `admin.go` is deleted, `AdminUserResponse` no longer exists. `server/admin_device_test.go` references `AdminUserResponse` at lines 23 and 58 in `TestAdminUserResponse_SlugField` and `TestAdminUserResponse_SlugEmpty`. These tests will fail to compile. The ADR neither deletes `admin_device_test.go` nor lists it in "Files modified" for the type-reference fix. The "Files deleted" and "Files modified" sections both omit this file entirely.
   - **Doc**: "Files deleted: server/admin.go ... server/breakglass_test.go" — `server/admin_device_test.go` appears nowhere in the deleted or modified lists.
   - **Code**: `server/admin_device_test.go:23` — `resp := AdminUserResponse{...}`; `server/admin_device_test.go:58` — `resp := AdminUserResponse{...}`. Both reference the type defined only in `admin.go`.

## Run: 2026-05-06T23:59:00Z

**Gaps found: 1**

1. **`admin_device_test.go` contains 8 additional unrelated tests that would be silently lost** — The ADR states "server/admin_device_test.go — entire file deleted; contains only `TestAdminUserResponse_SlugField` and `TestAdminUserResponse_SlugEmpty` which reference `AdminUserResponse` struct literals." This is factually incorrect. The file contains 10 test functions (306 lines), of which 8 have no dependency on `AdminUserResponse` or any symbol in `admin.go`:
   - `TestCLIDeviceCodeCreate_PublicKeyStoredInEntry` (line 72)
   - `TestCLIDeviceCodeCreate_EmptyBodyAccepted` (line 122)
   - `TestCLIDeviceCodeCreate_ResponseFields` (line 154)
   - `TestCLIDeviceCodeVerify_NilDB_Returns503` (line 193)
   - `TestCLIDeviceCodeVerify_MissingFields_Returns400` (line 210)
   - `TestCLIDeviceCodeVerify_GetServesHTML` (line 239)
   - `TestProjectResponse_ImportRefField` (line 263)
   - `TestProjectResponse_ImportRefNull` (line 288)

   None of these functions are covered elsewhere in the test suite (confirmed: no duplicate names in any other `server/*.go` file). A developer who follows "delete the entire file" destroys these 8 tests permanently with no disposition.
   - **Doc**: "server/admin_device_test.go — entire file deleted; contains only `TestAdminUserResponse_SlugField` and `TestAdminUserResponse_SlugEmpty`"
   - **Code**: `server/admin_device_test.go:72–306` — 8 test functions covering device-code creation/verification and `ProjectResponse.ImportRef` JSON shape that do not reference `AdminUserResponse` or any `admin.go` symbol.

## Run: 2026-05-06T20:45:02Z

**Gaps found: 2**

1. **`TestIsAdminMiddleware_RejectsNoToken` in `auth_test.go` calls deleted method — not listed for deletion** — The ADR removes `isAdminMiddleware` from `server.go`. However, `server/auth_test.go:258` calls `srv.isAdminMiddleware(...)` directly in `TestIsAdminMiddleware_RejectsNoToken`. Deleting `isAdminMiddleware` from `server.go` will cause a compilation failure for this test. The ADR's "Tests to delete" section lists `TestIntegration_AdminMiddleware_RejectsNoToken` (from `integration_test.go`) but does not list `TestIsAdminMiddleware_RejectsNoToken` (from `auth_test.go`). A developer following the ADR exactly will have a broken build.
   - **Doc**: "Files modified — server/server.go — remove ... `isAdminMiddleware` method"; "Tests to delete — TestIntegration_AdminMiddleware_RejectsNoToken, TestIntegration_AdminMiddleware_RejectsNonAdmin"
   - **Code**: `server/auth_test.go:258` — `handler := srv.isAdminMiddleware(http.HandlerFunc(...))` — not listed in any deletion or modification instruction

2. **`TestIntegration_ADR0097_AdminCluster_Returns404` in `integration_test.go` calls deleted `handleAdminRoutes` — not listed for deletion** — `server/integration_test.go:2684` calls `srv.handleAdminRoutes(rec, req)` directly inside `TestIntegration_ADR0097_AdminCluster_Returns404`. Deleting `admin.go` removes `handleAdminRoutes`, causing a compilation failure for this integration test. The ADR's "Tests to delete" section lists `TestAdminClusterEndpoints_Removed` (which is in `breakglass_test.go`, already being deleted) as "superseded by broader test," but does not list `TestIntegration_ADR0097_AdminCluster_Returns404` for deletion. A developer following the ADR exactly will have a broken build.
   - **Doc**: "Tests to delete — TestAdminClusterEndpoints_Removed (superseded by broader test)"
   - **Code**: `server/integration_test.go:2665–2687` — `TestIntegration_ADR0097_AdminCluster_Returns404` calls `srv.handleAdminRoutes` directly; not listed in the deleted or modified file lists

## Run: 2026-05-06T23:59:59Z

**Gaps found: 2**

1. **`breakglass_test.go` deletion claim is factually wrong for `TestMetricsEndpoint_MainMux`** — The ADR states "entire file deleted; all tests call functions defined only in `admin.go`." However, `TestMetricsEndpoint_MainMux` (breakglass_test.go:32–43) calls only `newTestMux` and `mux.ServeHTTP` — no `admin.go` symbols. The test verifies that `/metrics` is registered on the main mux (not 404). This test is not duplicated elsewhere: `monitoring_test.go` verifies metric content but the comment at breakglass_test.go:40 explicitly notes "Content verified in monitoring category," meaning this test covers only registration. Deleting the file loses the registration guard with no replacement. The ADR provides no disposition (keep, move, or replace) for this test.
   - **Doc**: "server/breakglass_test.go — entire file deleted; all tests call functions defined only in admin.go" (Component Changes, Files deleted section, line 43)
   - **Code**: `server/breakglass_test.go:32–43` — `TestMetricsEndpoint_MainMux` has no dependency on `admin.go` or any deleted symbol; `server/monitoring_test.go:159–176` covers metrics content but not route registration

2. **"Tests to add" lists only one test but Integration Test Cases table lists three** — The "Tests to add" bullet (line 58) names only `TestAdminRoutes_AllReturn404`. However, the Integration Test Cases table (lines 103–105) lists three tests to add: `TestAdminRoutes_AllReturn404`, `TestAuthMiddleware_AdminBypassRemoved`, and `TestRequireSelfOrAdmin_AdminCannotAccessOtherUserGrants`. The spec-control-plane.md update (lines 351–352) also lists two tests. A developer following only the "Tests to add" bullet — the implementation instruction — would add one of three tests and leave the admin-bypass and self-or-admin regressions unimplemented. The Integration Test Cases table is the authoritative list but is not cross-referenced from the "Tests to add" section.
   - **Doc**: "Tests to add — TestAdminRoutes_AllReturn404 — verifies that GET /admin/users, POST /admin/users, DELETE /admin/users/x, POST /admin/users/x/promote, POST /admin/sessions/stop, GET /scim/v2/Users all return 404" (line 58–59); vs Integration Test Cases table listing three tests (lines 103–105)
   - **Code**: No existing implementation of `TestAuthMiddleware_AdminBypassRemoved` or `TestRequireSelfOrAdmin_AdminCannotAccessOtherUserGrants` anywhere in `server/*.go`

## Run: 2026-05-06T23:59:01Z

**Gaps found: 2**

1. **`cluster_test.go` line 450 `jwt` variable becomes unused — compile error** — The ADR instructs removing "AdminUserRequest/AdminUserResponse usages (lines 457–475)" from `server/cluster_test.go` and replacing with `db.CreateUser` + `db.SetUserAdmin` calls. Line 465 (`req.Header.Set("Authorization", "Bearer "+jwt)`) is inside the 457–475 range and is the only consumer of the `jwt` variable declared at line 450 (`jwt := adminJWT(t)`). After removing 457–475, the `jwt` variable is declared but never read — a compile error in Go. The ADR does not mention removing line 450 or the `adminJWT` function call. A developer following the ADR exactly will produce a build failure.
   - **Doc**: "server/cluster_test.go — remove AdminUserRequest/AdminUserResponse usages (lines 457–475) and replace live HTTP POST /admin/users call with direct db.CreateUser + db.SetUserAdmin calls for test-user seeding" (`Component Changes → Files modified`)
   - **Code**: `server/cluster_test.go:450` — `jwt := adminJWT(t)` is declared outside the 457–475 range; its only use is at line 465 inside that range. Removing 457–475 leaves `jwt` unused → compile error.

2. **`TestIntegration_AdminCreateUser_SlugInResponse` replacement logic unspecified** — The ADR instructs replacing `srv.adminCreateUser` (line 410) and `AdminUserResponse` (line 416) in `server/device_auth_integration_test.go` with `db.CreateUser` + `db.SetUserAdmin` calls "for test-user seeding." However, `TestIntegration_AdminCreateUser_SlugInResponse` (device_auth_integration_test.go:393) is not a seeding test — its sole purpose is to verify that slug derivation from email works correctly, with three assertions: `resp.Slug` (line 424), `resp.ID` (line 427), and `resp.Email` (line 430). After replacing the HTTP call with `db.CreateUser`, the `AdminUserResponse` struct is gone and the assertion targets must change (to `user.Slug`, `user.ID`, `user.Email` from the DB return value). The ADR does not say whether to preserve the slug-derivation assertion (rewriting it against the DB return value) or delete the test entirely. A developer cannot determine the correct disposition without asking.
   - **Doc**: "server/device_auth_integration_test.go — remove call to srv.adminCreateUser (line 410) and reference to AdminUserResponse (line 416); replace with direct db.CreateUser + db.SetUserAdmin calls for test-user seeding" (`Component Changes → Files modified`)
   - **Code**: `server/device_auth_integration_test.go:393–433` — `TestIntegration_AdminCreateUser_SlugInResponse` has three slug/ID/email assertions against `AdminUserResponse` fields that cannot be compiled after `admin.go` is deleted; replacement assertion targets are unspecified.

## Run: 2026-05-06T15:30:00Z

**Gaps found: 1**

1. **Wrong function names for `requireSelfOrAdmin` callers in hosts.go** — The ADR says to "update its three callers (`handleGetGrants`, `handleGrantAccept`, `handleGetUserAgentPubkey`) to use self-only access enforcement." Neither `handleGetGrants` nor `handleGetUserAgentPubkey` exists in the codebase. The actual function names are `handleListGrants` (hosts.go:479) and `handleUserAgentPubkey` (hosts.go:607). A developer following the ADR and searching for `handleGetGrants` will not find it and will not know which handler to modify.
   - **Doc**: "server/hosts.go — remove `requireSelfOrAdmin` function (hosts.go:661–682) and update its three callers (`handleGetGrants`, `handleGrantAccept`, `handleGetUserAgentPubkey`) to use self-only access enforcement" (`Component Changes → Files modified`, line 50)
   - **Code**: `server/hosts.go:479` — `func (s *Server) handleListGrants(...)`; `server/hosts.go:607` — `func (s *Server) handleUserAgentPubkey(...)`. No function named `handleGetGrants` or `handleGetUserAgentPubkey` exists anywhere in `server/*.go`.

## Run: 2026-05-06T23:59:02Z

CLEAN — no blocking gaps found.

The current version of the ADR resolves all gaps reported in prior runs:

- `breakglass_test.go`: correctly specified as not deleted wholesale; `TestMetricsEndpoint_MainMux` is retained in place; the 6 remaining tests that call `admin.go` symbols are listed for deletion.
- `admin_device_test.go`: correctly specified as not deleted wholesale; the 2 `AdminUserResponse` tests are removed; the 8 remaining tests (`TestCLIDeviceCodeCreate_*`, `TestCLIDeviceCodeVerify_*`, `TestProjectResponse_*`) are preserved.
- `device_auth_integration_test.go`: correctly named (not `admin_device_test.go`); `TestIntegration_AdminCreateUser_SlugInResponse` is listed for deletion (not rewrite), resolving prior ambiguity.
- `cluster_test.go`: line 450 (`jwt := adminJWT(t)`) is explicitly listed for removal to avoid unused-variable compile error.
- `auth_test.go`: `TestIsAdminMiddleware_RejectsNoToken` is listed for deletion.
- `integration_test.go`: `TestIntegration_ADR0097_AdminCluster_Returns404` is listed for deletion.
- `hosts.go`: correct function names used — `handleListGrants` and `handleUserAgentPubkey`.
- `requireSelfOrAdmin` and its callers: explicitly in scope for removal.
- Three regression tests listed consistently in both "Tests to add" and "Integration Test Cases" table.
- `spec-state-schema.md` `is_admin` and `nkey_public` columns already reflect the ADR's changes in the current file.
