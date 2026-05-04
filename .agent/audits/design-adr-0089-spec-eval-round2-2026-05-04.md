## Run: 2026-05-04T00:00:00Z

# Spec Alignment Audit — ADR-0089 (Round 2)

ADR: `docs/adr-0089-cp-integration-test-suite.md`
Status: accepted

Specs checked:
- `docs/mclaude-control-plane/spec-control-plane.md`
- `docs/charts-mclaude/spec-helm.md`
- `docs/mclaude-cli/spec-cli.md`
- `docs/mclaude-web/spec-web.md`
- `docs/spec-state-schema.md`

---

## Phase 1 — ADR → Spec Forward Pass

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 34–35 | Admin port (9091) removed entirely; admin routes move to main port (8080) behind `isAdminMiddleware` | spec-control-plane.md:11–12 "Listens on a single port: the main API port (default 8080)… Admin endpoints are protected by `isAdminMiddleware`" | REFLECTED | — | |
| 36 | `/metrics` moved to main port (8080), unauthenticated | spec-control-plane.md:129; spec-helm.md:59 | REFLECTED | — | |
| 37–38 | `BOOTSTRAP_ADMIN_PASSWORD` Helm value; `init-keys` hashes and stores it; idempotent | spec-control-plane.md:43; spec-helm.md:115 | REFLECTED | — | |
| 39 | `is_admin` is a boolean on a normal user row; admin goes through same registration and login flow | spec-control-plane.md:258 "Admin = a normal user with `is_admin=true` — no separate token or port." | REFLECTED | — | |
| 40 | Remove `ADMIN_TOKEN` env var from CP binary, Helm chart, and all test infrastructure | spec-helm.md:157 "no longer requires an `admin-token` key"; no `ADMIN_TOKEN` in env table | REFLECTED | — | |
| 41 | Phase 1 CI gate: `go test -tags integration` must pass before Phase 2 starts | spec-control-plane.md:29 | REFLECTED | — | |
| 42 | CI Phase 2 auth: bootstrap admin logs in via `POST /auth/login` with `BOOTSTRAP_ADMIN_EMAIL` + `BOOTSTRAP_ADMIN_PASSWORD` | spec-control-plane.md:27; spec-cli.md:219–223 | REFLECTED | — | |
| 43 | Test approach: `httptest.NewRecorder()` + direct handler calls; `newTestServer(t, db, nc)` helper | spec-control-plane.md:319 | REFLECTED | — | |
| 48–68 | Two-phase deployment with explicit CI gate between phases | spec-control-plane.md:13–29 | REFLECTED | — | |
| 91–101 | Phase 1 end state: exactly 5 running pods + 2 completed Jobs | spec-control-plane.md:17 | REFLECTED | — | |
| 104–108 | Phase 1 Postgres state: users (1 row), hosts/projects/agent_credentials all empty | spec-control-plane.md:19 | REFLECTED | — | |
| 110–113 | Phase 1 NATS state: `mclaude-hosts` KV exists+empty; no per-user KV/streams; no leafnode connections | spec-control-plane.md:21 | REFLECTED | — | |
| 115–121 | What works at Phase 1 (POST /auth/login, POST /admin/users, /readyz, NKey challenge/verify, device-code flow, SPA loads) | spec-control-plane.md:23 | REFLECTED | — | |
| 123–125 | What requires Phase 2 (sessions, NATS provisioning, $SYS CONNECT events) | spec-control-plane.md:25 | REFLECTED | — | |
| 148–153 | `main.go` changes: remove adminPort, admin listener, adminToken param, AdminMux(), /metrics on main mux | spec-control-plane.md Deployment section — single port, no AdminMux | REFLECTED | — | |
| 154–160 | `server.go`: `isAdminMiddleware` replaces `adminAuthMiddleware`; admin routes on main mux; `AdminMux()` deleted | spec-control-plane.md:123–125 | REFLECTED | — | |
| 168–172 | `init_keys.go`: read `BOOTSTRAP_ADMIN_PASSWORD`, hash + store; idempotent | spec-control-plane.md:232; spec-helm.md:28 | REFLECTED | — | |
| 176 | Deleted test: `TestIntegration_SeedDev_WritesLocalHostKV` | spec-control-plane.md:362 | REFLECTED | — | |
| 182–186 | New tests — Phase 1 invariants: `TestIntegration_Schema_NoSeedRows`, `TestIntegration_CreateUser_NoLocalHost` | spec-control-plane.md:325–327 | REFLECTED | — | |
| 188–197 | New tests — admin routes: 5 tests (AdminCreateUser, AdminListUsers, AdminDeleteUser, AdminMiddleware_RejectsNoToken, AdminMiddleware_RejectsNonAdmin) | spec-control-plane.md:329–337 | REFLECTED | — | |
| 199–209 | New tests — auth surface: 7 tests (HandleLogin_InvalidCreds, AuthMiddleware_Rejects_Unauthenticated, AuthMiddleware_Accepts_ValidJWT, HostRegister_HTTP, NKeyChallenge_AndVerify, DeviceCode_StartAndPoll, HealthEndpoints) | spec-control-plane.md:339–349 | REFLECTED | — | |
| 211–221 | New tests — NATS subject handlers: 7 tests (ManageGrant, ManageRevokeAccess, ManageDeregister, ManageUpdate, ProjectCreate_NATS, CheckSlug_Available, CheckSlug_Taken) | spec-control-plane.md:351–361 | REFLECTED | — | |
| 222–227 | `cluster_test.go`: `TestCluster_DevUserProjectInKV` → `TestCluster_UserProjectInKV`; uses admin JWT (not `ADMIN_TOKEN`); ephemeral user via `POST /admin/users` | spec-control-plane.md has no mention of `cluster_test.go`, `TestCluster_UserProjectInKV`, or the `ADMIN_TOKEN` removal in cluster tests | GAP | SPEC→FIX | The Integration Tests section only covers `integration_test.go`. A developer reading the spec would not know the cluster test was renamed and its auth updated. The spec should document the cluster_test.go change alongside the integration_test.go changes. |
| 229–238 | `mclaude-web/e2e/api.spec.ts` line 574: remove `\|\| 'dev-admin-token'` fallback; E2E tests needing admin operations must log in as bootstrap admin user and use resulting JWT (via `loginAsAdmin`) | spec-web.md describes global-setup using bootstrap admin login; describes `BOOTSTRAP_ADMIN_EMAIL`/`BOOTSTRAP_ADMIN_PASSWORD` env vars; does NOT describe per-test `loginAsAdmin` pattern or the specific `api.spec.ts` fallback removal | PARTIAL | SPEC→FIX | spec-web.md reflects that the admin token concept is gone from global-setup/teardown, but does not state the pattern for per-test admin operations in `api.spec.ts` (i.e. that each spec file needing admin ops must call `loginAsAdmin(page, BOOTSTRAP_ADMIN_EMAIL, BOOTSTRAP_ADMIN_PASSWORD)` rather than using a static `ADMIN_TOKEN` env var). A developer implementing E2E tests from the spec alone would know about global-setup but might still reach for `ADMIN_TOKEN` for inline per-test admin calls. |
| 241–245 | Helm chart: remove adminToken/devSeed/ADMIN_TOKEN/ADMIN_PORT/DEV_SEED; add bootstrapAdminPassword | spec-helm.md:114–115 shows bootstrapAdminEmail/bootstrapAdminPassword; no adminToken/devSeed/ADMIN_PORT/DEV_SEED entries; line 157 notes no admin-token key | REFLECTED | — | |
| 246–248 | `scripts/register-platform-host.sh` (new): Phase 2 CI entry point | spec-control-plane.md:27 | REFLECTED | — | |
| 256–257 | Error handling: `isAdminMiddleware` 401 if no JWT, 403 if JWT valid but is_admin=false | spec-control-plane.md:123–125, 315 | REFLECTED | — | |
| 258–259 | Error handling: `bootstrapAdminUser` — if password empty and user has no password, leaves empty; does not fail | spec-control-plane.md:43 | REFLECTED | — | |
| 260 | NATS subject tests: `nc.Request` with 3s timeout; `t.Fatal` on timeout | spec-control-plane.md has no NATS test timeout documented | GAP | SPEC→FIX | The 3s timeout is a behaviorally meaningful test convention (determines how long tests wait before declaring a NATS subject handler broken). A developer writing the NATS test cases from spec alone has no guidance on the timeout value. However this is a test-internal implementation choice with no user-visible consequence — borderline. Including as a gap since the ADR explicitly decided it. |

---

### Summary

- Reflected: 27
- Gap: 2
- Partial: 1
