## Run: 2026-05-04T00:00:00Z (Round 3)

ADR: docs/adr-0089-cp-integration-test-suite.md
Status: accepted

Specs checked:
- docs/mclaude-control-plane/spec-control-plane.md
- docs/charts-mclaude/spec-helm.md
- docs/mclaude-cli/spec-cli.md
- docs/mclaude-web/spec-web.md
- docs/spec-state-schema.md

---

### Phase 1 — ADR → Spec forward pass

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 35 | "Admin port — Remove loopback port 9091 entirely" | spec-control-plane.md:11 "Listens on a single port: the main API port (default 8080) for all routes — public, authenticated, and admin." | REFLECTED | — | Single-port deployment explicitly stated |
| 36 | "Admin route auth — Move /admin/* to main port (8080) behind isAdminMiddleware — checks JWT via existing authMiddleware + user.IsAdmin == true from DB" | spec-control-plane.md:11 "Admin endpoints are protected by isAdminMiddleware, which validates the caller's JWT via the standard auth middleware and then checks that the authenticated user has is_admin=true in Postgres." | REFLECTED | — | |
| 37 | "/metrics route — Move to main port (8080), unauthenticated" | spec-control-plane.md:129 "GET /metrics — Prometheus metrics (unauthenticated — internal scraping)" in admin-only table; also spec-helm.md:59 "/metrics is served on the same port, unauthenticated." | REFLECTED | — | |
| 38 | "Bootstrap admin password — Add BOOTSTRAP_ADMIN_PASSWORD Helm value; init-keys job hashes and stores it at deploy time" | spec-control-plane.md:43 BOOTSTRAP_ADMIN_PASSWORD env var documented; spec-helm.md:115 bootstrapAdminPassword value documented | REFLECTED | — | |
| 39 | "Admin = user with a flag, not a user type — is_admin is a boolean on a normal user row; admin goes through the same registration and login flow as any other user" | spec-control-plane.md:232 "The bootstrap admin is a normal user with is_admin=true." spec-state-schema.md:24 is_admin column documented | REFLECTED | — | |
| 39 | "The OAuth-only admin row (no password, no OAuth connection) violates this — it's a user that cannot log in." | spec-control-plane.md:43-44 BOOTSTRAP_ADMIN_PASSWORD docs explain that if empty admin can only log in via OAuth; startup section at line 232 clarifies idempotent behavior | REFLECTED | — | |
| 40 | "ADMIN_TOKEN env var — Remove from CP binary, Helm chart, and all test infrastructure" | spec-control-plane.md env table: no ADMIN_TOKEN entry present; spec-helm.md pre-req secrets section line 157 "no longer requires an admin-token key"; spec-helm.md configuration table: no adminToken/ADMIN_TOKEN entry | REFLECTED | — | |
| 41 | "Two-phase CI gate — Phase 1: go test -tags integration must pass before Phase 2 starts" | spec-control-plane.md:29 "The go test -tags integration suite runs after Phase 1 as the gate before Phase 2 starts." | REFLECTED | — | |
| 42 | "CI Phase 2 auth — Bootstrap admin logs in via POST /auth/login with BOOTSTRAP_ADMIN_EMAIL + BOOTSTRAP_ADMIN_PASSWORD CI secrets" | spec-control-plane.md:27 "scripts/register-platform-host.sh authenticates as the bootstrap admin via POST /auth/login" | REFLECTED | — | |
| 43 | "Test approach — Unit-style httptest.NewRecorder() + direct handler calls; newTestServer(t, db, nc) helper" | spec-control-plane.md:319 "All tests use httptest.NewRecorder() + direct handler calls. The newTestServer(t, db, nc) *Server helper constructs a test server..." | REFLECTED | — | |
| 48-68 | Phase 1 — 5 running pods + 2 completed Jobs in mclaude-system | spec-control-plane.md:17 "the cluster contains exactly 5 running pods in mclaude-system: mclaude-cp-nats-0 (NATS hub), mclaude-cp-postgres-0 (Postgres), the CP pod, the SPA pod, and the MinIO pod. Two Jobs complete and exit: init-keys and minio-bucket-job." | REFLECTED | — | Pod names match ADR exactly |
| 104-108 | Phase 1 Postgres state: users one row (bootstrap admin is_admin=true), hosts/projects/agent_credentials all empty | spec-control-plane.md:19 "Phase 1 Postgres state: users — one row (bootstrap admin, is_admin=true). hosts, projects, agent_credentials — all empty." | REFLECTED | — | |
| 109-112 | Phase 1 NATS state: mclaude-hosts KV bucket exists and empty; no per-user KV buckets; no leafnode connections | spec-control-plane.md:21 "Phase 1 NATS state: mclaude-hosts KV bucket exists and is empty. No per-user KV buckets or streams (created lazily on first login). No leafnode connections." | REFLECTED | — | |
| 114-119 | What works at Phase 1: POST /auth/login, POST /admin/users, /readyz, NKey challenge/verify, device-code flow, SPA loads | spec-control-plane.md:23 "What works at Phase 1: POST /auth/login (bootstrap admin can authenticate, gets JWT), POST /admin/users (admin can create additional users), /readyz (200), NKey challenge/verify, device-code flow, SPA loads with empty dashboard." | REFLECTED | — | |
| 121-125 | What requires Phase 2: sessions, NATS provisioning requests, $SYS CONNECT events | spec-control-plane.md:25 "What requires Phase 2: Sessions (no controller to provision session-agent pods), NATS provisioning requests (published by CP but nothing subscribed), any $SYS.ACCOUNT.*.CONNECT events (no hosts have ever connected)." | REFLECTED | — | |
| 127-143 | CI Pipeline Structure: phase1-cp → phase2-host with needs: gate | spec-control-plane.md:29 mentions gate; spec does not reproduce the YAML pipeline structure — but this is CI config, not a behavior spec, so no spec update needed | REFLECTED | — | CI YAML is implementation, not spec content |
| 148-153 | mclaude-control-plane main.go: Remove adminPort variable, adminToken, AdminMux(), /metrics on main mux | spec-control-plane.md:11 single port; line 129 /metrics on main port unauthenticated; no AdminMux referenced | REFLECTED | — | |
| 157-160 | server.go: isAdminMiddleware replaces adminAuthMiddleware | spec-control-plane.md:125 "isAdminMiddleware — calls the standard JWT auth middleware first, then fetches the user from Postgres and checks is_admin = true" | REFLECTED | — | |
| 157-160 | server.go: Admin routes on main mux behind isAdminMiddleware; AdminMux() deleted | spec-control-plane.md:123-138 admin routes table headed "Admin-only (isAdminMiddleware)" on main port | REFLECTED | — | |
| 168-172 | init_keys.go: BOOTSTRAP_ADMIN_PASSWORD from env; hash and store; idempotent - does not overwrite existing password | spec-control-plane.md:42-43 BOOTSTRAP_ADMIN_PASSWORD env documented; line 232 "Idempotent — if the user already exists, promotes them to admin and updates the password if BOOTSTRAP_ADMIN_PASSWORD is provided." | REFLECTED | — | |
| 174-177 | integration_test.go: newTestServer helper; no admin token; admin tests use db.CreateUser + db.SetUserAdmin + JWT | spec-control-plane.md:319 "No static admin token — admin tests create an is_admin=true user via db.CreateUser + db.SetUserAdmin, log in via handleLogin, and use the resulting JWT." | REFLECTED | — | |
| 179 | Deleted test: TestIntegration_SeedDev_WritesLocalHostKV removed with seedDev | spec-control-plane.md:368-369 "Deleted tests (ADR-0089): TestIntegration_SeedDev_WritesLocalHostKV — removed with seedDev" | REFLECTED | — | |
| 182-186 | New tests — Phase 1 invariants: TestIntegration_Schema_NoSeedRows, TestIntegration_CreateUser_NoLocalHost | spec-control-plane.md:322-326 both tests listed with matching descriptions | REFLECTED | — | |
| 188-197 | New tests — admin routes: 5 tests (AdminCreateUser, AdminListUsers, AdminDeleteUser, AdminMiddleware_RejectsNoToken, AdminMiddleware_RejectsNonAdmin) | spec-control-plane.md:328-336 all 5 tests listed with matching method/path/verification | REFLECTED | — | |
| 199-208 | New tests — auth surface: 7 tests (HandleLogin_InvalidCreds, AuthMiddleware_Rejects_Unauthenticated, AuthMiddleware_Accepts_ValidJWT, HostRegister_HTTP, NKeyChallenge_AndVerify, DeviceCode_StartAndPoll, HealthEndpoints) | spec-control-plane.md:338-348 all 7 tests listed with matching details | REFLECTED | — | |
| 210-220 | New tests — NATS subject handlers: 7 tests (ManageGrant, ManageRevokeAccess, ManageDeregister, ManageUpdate, ProjectCreate_NATS, CheckSlug_Available, CheckSlug_Taken) | spec-control-plane.md:350-362 all 7 tests listed with matching subject/verification | REFLECTED | — | |
| 258 | "NATS subject tests use nc.Request with 3s timeout; t.Fatal on timeout" | spec-control-plane.md:352 "NATS subject tests use nc.Request with a 3s timeout; t.Fatal on timeout." | REFLECTED | — | Round 2 fix confirmed present |
| 222-227 | cluster_test.go: TestCluster_UserProjectInKV replaces TestCluster_DevUserProjectInKV; creates ephemeral user via POST /admin/users with admin JWT (not ADMIN_TOKEN); all ADMIN_TOKEN uses removed | spec-control-plane.md:364-366 "TestCluster_UserProjectInKV — replaces TestCluster_DevUserProjectInKV. Creates an ephemeral user via POST /admin/users using an admin JWT obtained from POST /auth/login (not ADMIN_TOKEN). Publishes a project-create NATS message, verifies the KV entry, then tears down. All uses of ADMIN_TOKEN env var removed from cluster_test.go." | REFLECTED | — | Round 2 fix confirmed present |
| 229-238 | mclaude-web e2e/api.spec.ts: remove || 'dev-admin-token' fallback; acquire admin JWT via loginAsAdmin | spec-web.md:253-259 "Admin operations in spec files: Spec files that need to call admin-only API endpoints must obtain an admin JWT by logging in as the bootstrap admin, using the loginAsAdmin helper... No spec file may use a static ADMIN_TOKEN string or the || 'dev-admin-token' fallback pattern." | REFLECTED | — | Round 2 fix confirmed present |
| 241-245 | Helm chart: Remove adminToken, devSeed, ADMIN_TOKEN, ADMIN_PORT, DEV_SEED from values.yaml etc.; Add bootstrapAdminPassword | spec-helm.md:114-115 bootstrapAdminPassword and bootstrapAdminEmail documented; line 157 "no longer requires an admin-token key"; no adminToken/ADMIN_TOKEN/ADMIN_PORT/DEV_SEED in configuration knobs table | REFLECTED | — | |
| 246-248 | scripts/register-platform-host.sh: Phase 2 CI entry point; uses BOOTSTRAP_ADMIN_EMAIL + BOOTSTRAP_ADMIN_PASSWORD from CI env | spec-control-plane.md:27 "scripts/register-platform-host.sh authenticates as the bootstrap admin via POST /auth/login, then runs mclaude host register to create the first host." | REFLECTED | — | |
| 256 | isAdminMiddleware: 401 if no JWT, 403 if JWT valid but is_admin = false | spec-control-plane.md:125 "Returns 401 if no JWT, 403 if JWT valid but user is not admin." | REFLECTED | — | |
| 257 | bootstrapAdminUser: if BOOTSTRAP_ADMIN_PASSWORD empty and user has no password, leaves password empty; does not fail | spec-control-plane.md:43-44 "If empty, the bootstrap admin can only log in via OAuth." | REFLECTED | — | Idempotent non-failure behavior implied by the env var docs |
| 258 | spec-state-schema.md: mclaude-hosts KV Writers remove DEV_SEED secondary path sentence | spec-state-schema.md:298-313 mclaude-hosts section; no DEV_SEED reference present | REFLECTED | — | |
| 274 | spec-cli.md: smoke test section: remove port-forward + ADMIN_TOKEN; use bootstrap admin login | spec-cli.md:218-225 smoke test env vars use BOOTSTRAP_ADMIN_EMAIL + BOOTSTRAP_ADMIN_PASSWORD; "No port-forward needed — admin routes are served on the main port." | REFLECTED | — | |

---

### Summary

- Reflected: 33
- Gap: 0
- Partial: 0
