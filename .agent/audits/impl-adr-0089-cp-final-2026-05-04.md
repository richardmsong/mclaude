# Implementation Audit: mclaude-control-plane (ADR-0089 final)

**Component:** mclaude-control-plane
**Component root:** mclaude-control-plane/
**Spec files:** docs/adr-0089-cp-integration-test-suite.md, docs/mclaude-control-plane/spec-control-plane.md
**ADR status:** accepted

## Run: 2026-05-04T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0089:13-14 | "Admin port removal — The loopback-only port (9091) and static ADMIN_TOKEN bearer are removed." | main.go:96-172 — no adminPort listener, no adminToken param, no AdminMux call; server.go:91 NewServer has no adminToken param | IMPLEMENTED | — | No trace of 9091, adminToken, adminPort, or AdminMux anywhere in production code |
| adr-0089:14 | "Admin routes (/admin/*, /metrics) move to the main port (8080). Admin routes are protected by an is_admin JWT middleware that checks the authenticated user's is_admin flag from Postgres." | server.go:130-133 mux.Handle("/admin/", s.isAdminMiddleware(...)); server.go:144-166 isAdminMiddleware impl | IMPLEMENTED | — | isAdminMiddleware wraps authMiddleware then checks user.IsAdmin from DB |
| adr-0089:15-16 | "Bootstrap admin password — A new BOOTSTRAP_ADMIN_PASSWORD Helm value lets the init-keys job set a password hash at deploy time, enabling CI to authenticate as the bootstrap admin via POST /auth/login without OAuth." | init_keys.go:37 bootstrapPassword := os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"); init_keys.go:128-188 bootstrapAdminUser; charts/mclaude-cp/values.yaml:115 bootstrapAdminPassword | IMPLEMENTED | — | Password read, hashed, stored; idempotent update for existing users |
| adr-0089:33 | "Admin route auth: Move /admin/* to main port (8080) behind isAdminMiddleware" | server.go:128-133 | IMPLEMENTED | — | |
| adr-0089:35 | "/metrics route: Move to main port (8080), unauthenticated" | server.go:135-138 mux.HandleFunc("/metrics", s.handleMetrics); no auth wrapper | IMPLEMENTED | — | |
| adr-0089:37 | "Bootstrap admin password: Add BOOTSTRAP_ADMIN_PASSWORD Helm value; init-keys job hashes and stores it at deploy time" | init_keys.go:37,113; charts/mclaude-cp/templates/init-keys-job.yaml:56-58 | IMPLEMENTED | — | Idempotent — does not overwrite existing password |
| adr-0089:39 | "ADMIN_TOKEN env var: Remove from CP binary, Helm chart, and all test infrastructure" | No occurrence of ADMIN_TOKEN in production .go files; no occurrence in charts/mclaude-cp/ | IMPLEMENTED | — | grep confirms complete removal |
| adr-0089:40 | "Two-phase CI gate: Phase 1: go test -tags integration must pass before Phase 2 starts" | integration_test.go:1 //go:build integration tag | IMPLEMENTED | — | Gate exists; tests are tagged correctly |
| adr-0089:42 | "Test approach: Unit-style httptest.NewRecorder() + direct handler calls; newIntegrationServer(t, db, nc) helper" | integration_test.go:770 func newIntegrationServer(t, db, nc); all ADR-0089 tests use httptest.NewRecorder() | IMPLEMENTED | — | ADR names helper newIntegrationServer; spec-control-plane.md:319 says newTestServer — minor naming discrepancy (spec should use newIntegrationServer per ADR) |
| adr-0089:151-153 | "main.go: Remove adminPort variable and 127.0.0.1:adminPort listener, remove adminToken parameter from NewServer call, remove srv.AdminMux() call" | main.go:96 NewServer(db, accountKP, natsURL, natsWsURL, jwtExpiry) — no adminToken; no AdminMux; single listener main.go:169 | IMPLEMENTED | — | |
| adr-0089:158-163 | "server.go: NewServer signature remove adminToken string parameter; adminAuthMiddleware replaced with isAdminMiddleware; Admin routes registered on main mux behind isAdminMiddleware; /metrics on main mux unauthenticated; AdminMux() method deleted" | server.go:91 NewServer signature has no adminToken; server.go:144 isAdminMiddleware; server.go:130-138; no AdminMux method exists | IMPLEMENTED | — | |
| adr-0089:166-170 | "admin.go: Remove Server.adminToken field; No handler changes" | admin.go:15-19 comment describes isAdminMiddleware on main port; Server struct in auth.go has no adminToken field | IMPLEMENTED | — | Fix #1 confirmed: comment correctly describes isAdminMiddleware on main port |
| adr-0089:172-176 | "init_keys.go: Read BOOTSTRAP_ADMIN_PASSWORD from env; Pass to bootstrapAdminUser; In bootstrapAdminUser: if password non-empty and existing user has no password hash, call HashPassword and db.UpdateUserPassword; if fresh user, call HashPassword and pass hash to CreateUser" | init_keys.go:37,113,128-188 | IMPLEMENTED | — | Fix #2 confirmed: seedDev entirely removed; BOOTSTRAP_ADMIN_PASSWORD flow correct |
| adr-0089:178-181 | "integration_test.go: Deleted test TestIntegration_SeedDev_WritesLocalHostKV" | integration_test.go:764 — tombstone comment only; no function definition for TestIntegration_SeedDev_WritesLocalHostKV | IMPLEMENTED | — | |
| adr-0089:183-190 | "New tests Phase 1 invariants: TestIntegration_Schema_NoSeedRows, TestIntegration_CreateUser_NoLocalHost" | integration_test.go:823-870, 874-894 | IMPLEMENTED | — | Both tests present and correctly implement the invariants |
| spec-cp:327 | "TestIntegration_BootstrapAdminUser_FreshWithPassword — bootstrapAdminUser with non-empty password on empty DB: user row created with non-empty password_hash, is_admin=true; login succeeds" | integration_test.go:900-947 | IMPLEMENTED | — | Fix #3a confirmed: test exists, verifies password_hash non-empty, is_admin=true, and login succeeds |
| spec-cp:328-329 | "TestIntegration_BootstrapAdminUser_ExistingUserGetsPassword — bootstrapAdminUser called on existing user with empty password_hash: hash is written, login succeeds after call" | integration_test.go:952-1005 | IMPLEMENTED | — | Fix #3b confirmed: test exists, verifies idempotent update path |
| adr-0089:192-200 | "New tests admin routes: TestIntegration_AdminCreateUser, TestIntegration_AdminListUsers, TestIntegration_AdminDeleteUser, TestIntegration_AdminMiddleware_RejectsNoToken, TestIntegration_AdminMiddleware_RejectsNonAdmin" | integration_test.go:1011-1187 | IMPLEMENTED | — | All 5 admin tests present and correct |
| adr-0089:202-213 | "New tests auth surface: TestIntegration_HandleLogin_InvalidCreds, TestIntegration_AuthMiddleware_Rejects_Unauthenticated, TestIntegration_AuthMiddleware_Accepts_ValidJWT, TestIntegration_HostRegister_HTTP, TestIntegration_NKeyChallenge_AndVerify, TestIntegration_DeviceCode_StartAndPoll, TestIntegration_HealthEndpoints" | integration_test.go:1193-1493 | IMPLEMENTED | — | All 7 auth surface tests present |
| adr-0089:215-224 | "New tests NATS subject handlers: TestIntegration_ManageGrant, TestIntegration_ManageRevokeAccess, TestIntegration_ManageDeregister, TestIntegration_ManageUpdate, TestIntegration_ProjectCreate_NATS, TestIntegration_CheckSlug_Available, TestIntegration_CheckSlug_Taken" | integration_test.go:1508-1880 | IMPLEMENTED | — | All 7 NATS subject tests present |
| adr-0089:226-231 | "cluster_test.go: TestCluster_DevUserProjectInKV → replaced by TestCluster_UserProjectInKV; uses admin JWT from POST /auth/login, not ADMIN_TOKEN; all ADMIN_TOKEN env var uses removed" | cluster_test.go:444-575 TestCluster_UserProjectInKV; cluster_test.go:418-442 adminJWT() uses CLUSTER_TEST_ADMIN_EMAIL + CLUSTER_TEST_ADMIN_PASSWORD; no ADMIN_TOKEN in file | IMPLEMENTED | — | |
| adr-0089:233-244 | "mclaude-web e2e/api.spec.ts: remove ADMIN_TOKEN fallback, use admin JWT via login" | No ADMIN_TOKEN in mclaude-web/e2e/ | IMPLEMENTED | — | |
| adr-0089:246-249 | "Helm chart: Remove adminToken, devSeed, ADMIN_TOKEN, ADMIN_PORT, DEV_SEED from values.yaml etc; Add bootstrapAdminPassword value" | charts/mclaude-cp/values.yaml:115 bootstrapAdminPassword present; no ADMIN_TOKEN/ADMIN_PORT/DEV_SEED found in charts | IMPLEMENTED | — | |
| adr-0089:251-253 | "scripts/register-platform-host.sh (new): Phase 2 CI entry point using BOOTSTRAP_ADMIN_EMAIL + BOOTSTRAP_ADMIN_PASSWORD" | scripts/register-platform-host.sh exists; uses CP_URL, BOOTSTRAP_ADMIN_EMAIL, BOOTSTRAP_ADMIN_PASSWORD | IMPLEMENTED | — | |
| spec-cp:11 | "Listens on a single port: the main API port (default 8080) for all routes — public, authenticated, and admin. Admin endpoints are protected by isAdminMiddleware" | main.go:169 http.ListenAndServe(":"+port, mux) — single listener; server.go:130 isAdminMiddleware | IMPLEMENTED | — | |
| spec-cp:17-18 | "Phase 1 Postgres state: users — one row (bootstrap admin, is_admin=true). hosts, projects, agent_credentials — all empty" | init_keys.go:112-118 bootstrapAdminUser conditionally creates admin when BOOTSTRAP_ADMIN_EMAIL set; TestIntegration_Schema_NoSeedRows verifies empty tables | IMPLEMENTED | — | |
| spec-cp:319 | "newTestServer(t, db, nc) *Server helper constructs a test server" | integration_test.go:770 func newIntegrationServer — named newIntegrationServer, not newTestServer | PARTIAL | SPEC→FIX | spec-cp:319 says newTestServer; code and ADR say newIntegrationServer. Spec should be updated to match actual function name |
| spec-cp:42-43 | "BOOTSTRAP_ADMIN_PASSWORD: Hashed with bcrypt by the init-keys Job and stored in users.password_hash. Required if CI must authenticate as the bootstrap admin via POST /auth/login. If empty, the bootstrap admin can only log in via OAuth." | init_keys.go:169-175 hashes password with HashPassword; TestIntegration_BootstrapAdminUser_FreshWithPassword verifies | IMPLEMENTED | — | |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| integration_test.go:764 | INFRA | Tombstone comment "TestIntegration_SeedDev_WritesLocalHostKV removed per ADR-0089 (seedDev removed)" — documents the deletion, no code |
| integration_test.go:766-817 | INFRA | newIntegrationServer and mustAdminJWT helpers — necessary infrastructure for ADR-0089 tests; fully spec'd in ADR-0089:180 |
| cluster_test.go:418-442 | INFRA | adminJWT() helper — necessary infrastructure for TestCluster_UserProjectInKV; fully described in ADR-0089:229 |
| breakglass_test.go:32-43 | INFRA | TestMetricsEndpoint_MainMux — verifies /metrics registered on main mux; aligns with ADR-0089:35 and spec-cp:129 |
| breakglass_test.go:47-188 | INFRA | Admin handler nil-DB guard tests — unit tests verifying error handling for spec'd admin endpoints without DB |
| init_keys.go:53-59 | INFRA | Secret idempotency check (Get before Create) — standard pattern to prevent duplicate init runs; aligns with spec-cp:214 "Idempotent: exits 0 if Secret already exists" |
| admin.go:1-19 | INFRA | Package declaration + imports + comment block describing isAdminMiddleware on main port |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0089 Fix #1 — admin.go comment | comment describes isAdminMiddleware on main port | breakglass_test.go:13-14 documents the change; server.go tested in integration | TestIntegration_AdminMiddleware_RejectsNoToken, _RejectsNonAdmin | TESTED |
| adr-0089 Fix #2 — seedDev removed | no seedDev, no DEV_SEED in production code | integration_test.go:764 tombstone; TestIntegration_Schema_NoSeedRows verifies Migrate adds no rows | integration_test.go:823 | TESTED |
| adr-0089 Fix #3a — TestIntegration_BootstrapAdminUser_FreshWithPassword | fresh user created with password_hash, is_admin=true, login succeeds | integration_test.go:900-947 | — | UNIT_ONLY |
| adr-0089 Fix #3b — TestIntegration_BootstrapAdminUser_ExistingUserGetsPassword | existing user gets password set, login succeeds | integration_test.go:952-1005 | — | UNIT_ONLY |
| adr-0089:183 TestIntegration_Schema_NoSeedRows | fresh Migrate leaves tables empty | integration_test.go:823-870 | — | UNIT_ONLY |
| adr-0089:184 TestIntegration_CreateUser_NoLocalHost | CreateUser does not insert slug='local' host | integration_test.go:874-894 | — | UNIT_ONLY |
| adr-0089:192 TestIntegration_AdminCreateUser | POST /admin/users creates user with {id,email,name,slug} | integration_test.go:1011-1050 | — | UNIT_ONLY |
| adr-0089:193 TestIntegration_AdminListUsers | GET /admin/users lists users | integration_test.go:1053-1093 | — | UNIT_ONLY |
| adr-0089:194 TestIntegration_AdminDeleteUser | DELETE /admin/users/{id} deletes user | integration_test.go:1096-1128 | — | UNIT_ONLY |
| adr-0089:195 TestIntegration_AdminMiddleware_RejectsNoToken | 401 on /admin/users without JWT | integration_test.go:1131-1146 | — | UNIT_ONLY |
| adr-0089:196 TestIntegration_AdminMiddleware_RejectsNonAdmin | 403 on /admin/users with non-admin JWT | integration_test.go:1149-1187 | — | UNIT_ONLY |
| adr-0089:202 TestIntegration_HandleLogin_InvalidCreds | 401 on wrong password | integration_test.go:1193-1217 | — | UNIT_ONLY |
| adr-0089:203 TestIntegration_AuthMiddleware_Rejects_Unauthenticated | 401 on /auth/me without JWT | integration_test.go:1221-1236 | — | UNIT_ONLY |
| adr-0089:204 TestIntegration_AuthMiddleware_Accepts_ValidJWT | 200 on /auth/me with valid JWT | integration_test.go:1239-1276 | — | UNIT_ONLY |
| adr-0089:205 TestIntegration_HostRegister_HTTP | POST /api/hosts/register creates host with public_key, returns JWT | integration_test.go:1279-1346 | — | UNIT_ONLY |
| adr-0089:206 TestIntegration_NKeyChallenge_AndVerify | full challenge/verify round-trip returns scoped JWT | integration_test.go:1350-1423 | — | UNIT_ONLY |
| adr-0089:207 TestIntegration_DeviceCode_StartAndPoll | device-code returns {deviceCode,userCode}; poll returns pending | integration_test.go:1426-1473 | — | UNIT_ONLY |
| adr-0089:208 TestIntegration_HealthEndpoints | /health, /healthz, /readyz return 200 with DB up | integration_test.go:1477-1493 | — | UNIT_ONLY |
| adr-0089:215 TestIntegration_ManageGrant | manage.grant inserts grantee in host_access | integration_test.go:1508-1572 | — | UNIT_ONLY |
| adr-0089:216 TestIntegration_ManageRevokeAccess | manage.revoke-access removes grant | integration_test.go:1575-1640 | — | UNIT_ONLY |
| adr-0089:217 TestIntegration_ManageDeregister | manage.deregister deletes host row | integration_test.go:1642-1692 | — | UNIT_ONLY |
| adr-0089:218 TestIntegration_ManageUpdate | manage.update updates host name | integration_test.go:1694-1746 | — | UNIT_ONLY |
| adr-0089:219 TestIntegration_ProjectCreate_NATS | projects.create NATS publishes project row | integration_test.go:1749-1797 | — | UNIT_ONLY |
| adr-0089:220 TestIntegration_CheckSlug_Available | check-slug returns {available:true} for unused | integration_test.go:1801-1833 | — | UNIT_ONLY |
| adr-0089:221 TestIntegration_CheckSlug_Taken | check-slug returns {available:false} for taken | integration_test.go:1836-1880 | — | UNIT_ONLY |
| adr-0089:226-231 cluster_test.go TestCluster_UserProjectInKV | creates user via admin JWT, publishes project, verifies KV | cluster_test.go:446-575 | cluster_test.go:446-575 (cluster-tagged = real cluster = E2E) | E2E_ONLY |

Note on test classification: All integration_test.go tests tagged `//go:build integration` use real Postgres + NATS via testcontainers. These are classified as UNIT_ONLY because they mock no actual HTTP clients and test in-process handlers; they do not run against a deployed cluster. The cluster_test.go tests (tagged `//go:build cluster`) run against a real k3d cluster and are classified as E2E.

The UNIT_ONLY classification for the bootstrap admin tests (Fix #3a, #3b) is appropriate — these tests call bootstrapAdminUser() directly with a real Postgres connection from testcontainers and verify the resulting DB state and login flow. No E2E cluster test for this path is required by the ADR (Phase 2 CI gate exercises it indirectly via the login flow).

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| 002-droid-shield-false-positives.md | Droid-Shield False Positives on Non-Secret Patterns | OPEN | Component: Droid-Shield pre-commit scanner — not mclaude-control-plane. Out of scope for this audit. |

No bugs with Component: mclaude-control-plane found in .agent/bugs/.

### Summary

- Implemented: 27
- Gap: 0
- Partial: 1
- Infra: 7
- Unspec'd: 0
- Dead: 0
- Tested: 1
- Unit only: 24
- E2E only: 1
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0 (1 is Droid-Shield scanner, unrelated component)

### PARTIAL items

PARTIAL [SPEC→FIX]: "newTestServer(t, db, nc) *Server helper constructs a test server" → spec-control-plane.md:319 names the helper newTestServer; the ADR (adr-0089:42, adr-0089:180) and the actual code both use newIntegrationServer. The spec should be updated to say newIntegrationServer. (spec-control-plane.md:319 vs integration_test.go:770)
