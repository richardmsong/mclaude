# ADR: Two-Phase Deployment, Admin Port Removal, and Complete CP Test Suite

**Status**: implemented
**Status history**:
- 2026-05-04: draft
- 2026-05-04: accepted
- 2026-05-04: implemented — all scope CLEAN

## Overview

This ADR establishes three tightly coupled decisions:

1. **Two-phase deployment model** — Phase 1 deploys the control plane only (clean DB, no hosts, no seed data). Phase 2 registers the first platform host. These are separate CI stages with a gate between them.

2. **Admin port removal** — The loopback-only port (`9091`) and static `ADMIN_TOKEN` bearer are removed. Admin routes (`/admin/*`, `/metrics`) move to the main port (`8080`). Admin routes are protected by an `is_admin` JWT middleware that checks the authenticated user's `is_admin` flag from Postgres. No static token.

3. **Bootstrap admin password** — `init-keys` currently creates the bootstrap admin with no password (OAuth-only). A new `BOOTSTRAP_ADMIN_PASSWORD` Helm value lets the init-keys job set a password hash at deploy time, enabling CI to authenticate as the bootstrap admin via `POST /auth/login` without OAuth.

Together these make the CP independently deployable and testable via standard auth flows — no special ports, no static tokens, no seed data.

## Motivation

**Admin port confusion:** The admin port requires a port-forward to reach from outside the cluster, a static `ADMIN_TOKEN` that must be distributed as a CI secret, and a separate mux that duplicates routing logic. It provides no meaningful security advantage over `is_admin` JWT auth on the main port — both require a secret to be set up in advance, but the admin port also requires network configuration. The static token carries no user identity, which is why `adminRegisterCluster` has the known bug of setting `user_id = (SELECT id FROM users LIMIT 1)`.

**Bootstrap admin in an invalid state:** `init_keys.go:153` creates the admin row with no password (`""`), explicitly "SSO-only" in the comment. This creates a user with no OAuth connection and no password — a state no normal user is ever in, because normal users complete either an OAuth flow or a password registration before they can log in. The admin user cannot log in at all until they happen to hit the OAuth callback with the right email. CI cannot do OAuth (requires a browser). `BOOTSTRAP_ADMIN_PASSWORD` fixes this: the bootstrap admin is a normal password-based user who also has `is_admin=true`.

**Deployment coupling:** Today CI cannot verify the CP is healthy before registering a host, because `DEV_SEED=true` blurs the two steps together. Removing seedDev (ADR-0088) and the bare `local` host row (ADR-0076) creates a clean Phase 1 state. An explicit Phase 1 gate (`go test -tags integration`) verifies it.

**Test coverage gaps:** The existing `integration_test.go` (836 lines, 18 tests) does not cover admin routes, host registration, NKey challenge/verify, device-code flow, NATS lifecycle subjects, auth middleware, or the clean-boot invariant. Without these, Phase 1 has no automated confidence gate.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Admin port | Remove loopback port 9091 entirely | Static token + port-forward adds complexity without meaningful security gain; breaks user identity on admin requests |
| Admin route auth | Move `/admin/*` to main port (8080) behind `isAdminMiddleware` — checks JWT via existing `authMiddleware` + `user.IsAdmin == true` from DB | Same auth path as all other protected routes; admin identity is tracked; no static token |
| `/metrics` route | Move to main port (8080), unauthenticated | Internal k8s scraping does not use JWT; acceptable since the port is behind the cluster ingress |
| Bootstrap admin password | Add `BOOTSTRAP_ADMIN_PASSWORD` Helm value; `init-keys` job hashes and stores it at deploy time | Enables CI to authenticate as bootstrap admin via `POST /auth/login`; idempotent — does not overwrite an existing password |
| Admin = user with a flag, not a user type | `is_admin` is a boolean on a normal user row; admin goes through the same registration and login flow as any other user | No user, admin included, should be in a state a normal user isn't in. The OAuth-only admin row (no password, no OAuth connection) violates this — it's a user that cannot log in. |
| `ADMIN_TOKEN` env var | Remove from CP binary, Helm chart, and all test infrastructure | Replaced by JWT auth; no backwards compat needed (internal only) |
| Two-phase CI gate | Phase 1: `go test -tags integration` must pass before Phase 2 starts | Tests run against testcontainers (no cluster needed); fast enough to be a blocking gate |
| CI Phase 2 auth | Bootstrap admin logs in via `POST /auth/login` with `BOOTSTRAP_ADMIN_EMAIL` + `BOOTSTRAP_ADMIN_PASSWORD` CI secrets | Same flow a human operator would use; no special mechanism |
| Test approach | Unit-style `httptest.NewRecorder()` + direct handler calls; `newIntegrationServer(t, db, nc)` helper | Mirrors existing pattern; testcontainers provide real DB + NATS; no extra Docker overhead |

## Deployment Flow

### Phase 1 — Control Plane

```
CI: helm upgrade --install mclaude-cp charts/mclaude-cp/
    (injects BOOTSTRAP_ADMIN_EMAIL, BOOTSTRAP_ADMIN_PASSWORD, NATS_ACCOUNT_SEED, etc.)

    → init-keys Job runs:
        creates operator/account NKeys → k8s Secret
        bootstrapAdminUser: creates user row (email, password hash, is_admin=true)

    → CP pod starts:
        Migrate() — schema up, tables empty
        StartProjectsSubscriber() — KV buckets ensured
        /readyz returns 200

CI gate: go test -tags integration ./...
    → TestIntegration_Schema_NoSeedRows       (DB clean after Migrate)
    → TestIntegration_CreateUser_NoLocalHost  (no bare local host row)
    → TestIntegration_AdminCreateUser         (admin route works on main port)
    → TestIntegration_AuthMiddleware_*        (JWT auth enforced)
    → TestIntegration_HealthEndpoints         (/readyz 200)
    → ... (full integration suite)
```

At end of Phase 1: CP healthy, DB clean, admin API reachable via JWT on main port. No hosts.

### Phase 2 — First Platform Host

```
CI: scripts/register-platform-host.sh
    → POST /auth/login {email: BOOTSTRAP_ADMIN_EMAIL, password: BOOTSTRAP_ADMIN_PASSWORD}
      → adminJWT (is_admin=true user)
    → GET /auth/me (adminJWT) → userSlug
    → generate NKey pair (Ed25519) → publicKey, privateSeed
    → POST /api/users/{userSlug}/hosts/code {publicKey} (adminJWT)
      → {code, expiresAt}
    → POST /api/hosts/register {code, name: HOST_NAME} (no auth — code is credential)
      → {slug, jwt, hubUrl}
    → outputs HOST_SLUG, HOST_JWT, HOST_HUB_URL for Helm chart use
    → host row in DB: public_key + user_jwt populated
    → $SYS CONNECT event fires → CP writes KV entry (online=true)

CI gate: go test -tags cluster ./...
         playwright test
```

### Phase 1 End State

After `helm upgrade --install mclaude-cp` completes and the Phase 1 CI gate passes, the cluster contains exactly **5 running pods** and **2 completed Jobs** in `mclaude-system`:

| Resource | Kind | Count | Purpose |
|----------|------|-------|---------|
| `mclaude-cp-nats-0` | StatefulSet pod | 1 | NATS hub |
| `mclaude-cp-postgres-0` | StatefulSet pod | 1 | Postgres |
| `mclaude-cp-control-plane-*` | Deployment pod | 1 | CP binary |
| `mclaude-cp-spa-*` | Deployment pod | 1 | Web SPA |
| `mclaude-cp-minio-*` | Deployment pod | 1 | S3-compatible object store |
| `init-keys` Job | Completed | — | Generated NKeys + bootstrap admin user |
| `minio-bucket-job` Job | Completed | — | Created S3 bucket |

No controller pods. No session agents. No worker namespaces. No NATS leafnodes.

**Postgres state:**
- `users`: one row — bootstrap admin (email, password hash, `is_admin=true`)
- `hosts`: empty
- `projects`: empty
- `agent_credentials`: empty

**NATS state:**
- `mclaude-hosts` KV bucket: exists, empty
- No per-user KV buckets (created lazily on first login)
- No leafnode connections

**What works at this state:**
- `POST /auth/login` — bootstrap admin can authenticate, gets JWT
- `POST /admin/users` — admin can create additional users
- `/readyz` — 200 (DB up)
- NKey challenge/verify, device-code flow — all auth flows functional
- SPA loads, shows empty dashboard

**What requires Phase 2:**
- Sessions — no controller to provision session-agent pods
- NATS provisioning requests — published by CP but nothing subscribed
- Any `$SYS.ACCOUNT.*.CONNECT` events — no hosts have ever connected

### CI Pipeline Structure

```yaml
jobs:
  phase1-cp:
    steps:
      - deploy CP (helm upgrade --install)
      - run integration tests (go test -tags integration)
      - assert /readyz 200

  phase2-host:
    needs: phase1-cp
    steps:
      - register platform host (scripts/register-platform-host.sh)
      - run cluster tests (go test -tags cluster)
      - run e2e tests (playwright test)
```

## Component Changes

### mclaude-control-plane `main.go`

- Remove `adminPort` variable and `"127.0.0.1:" + adminPort` listener
- Remove `adminToken` parameter from `NewServer` call
- Remove `srv.AdminMux()` call
- `/metrics` handler registered on main mux instead of admin mux

### mclaude-control-plane `server.go`

- `NewServer` signature: remove `adminToken string` parameter
- `adminAuthMiddleware`: replace with `isAdminMiddleware` — calls `authMiddleware` first (parses + validates JWT), then fetches user from DB and checks `user.IsAdmin`
- Admin routes (`/admin/*`, `/scim/v2/`) registered on main mux behind `isAdminMiddleware`
- `/metrics` registered on main mux, unauthenticated
- `AdminMux()` method deleted

### mclaude-control-plane `admin.go`

- Remove `Server.adminToken` field (if it exists on the struct)
- No handler changes — handlers are unaffected; only the middleware wrapping them changes

### mclaude-control-plane `init_keys.go`

- Read `BOOTSTRAP_ADMIN_PASSWORD` from env
- Pass to `bootstrapAdminUser`
- In `bootstrapAdminUser`: if password non-empty and existing user has no password hash, call `HashPassword` and `db.UpdateUserPassword`; if user is being created fresh, call `HashPassword` and pass hash to `CreateUser`
- Comment "no password, SSO-only" removed

### mclaude-control-plane `integration_test.go`

All new tests use `httptest.NewRecorder()` + direct handler call. `newIntegrationServer(t, db, nc) *Server` helper constructs the server with a test `accountKP` and a real DB + NATS from testcontainers. No admin token needed — admin tests create an `is_admin=true` user via `db.CreateUser` + `db.SetUserAdmin`, log in via `handleLogin`, and use the resulting JWT.

**Deleted tests:**
- `TestIntegration_SeedDev_WritesLocalHostKV` — removed with seedDev

**New tests — Phase 1 invariants:**

| Test name | What it verifies |
|-----------|-----------------|
| `TestIntegration_Schema_NoSeedRows` | Fresh `Migrate()` on empty DB: `hosts`, `projects`, `users` tables all empty |
| `TestIntegration_CreateUser_NoLocalHost` | After `CreateUser`, no row in `hosts` with `slug='local'` for that user (ADR-0076) |

**New tests — admin routes (main port, is_admin JWT):**

| Test name | Method / Path | What it verifies |
|-----------|--------------|-----------------|
| `TestIntegration_AdminCreateUser` | `POST /admin/users` | Creates user; response has `{id, email, name, slug}` |
| `TestIntegration_AdminListUsers` | `GET /admin/users` | Lists created users |
| `TestIntegration_AdminDeleteUser` | `DELETE /admin/users/{id}` | Deletes user; subsequent DB fetch returns nil |
| `TestIntegration_AdminMiddleware_RejectsNoToken` | `GET /admin/users` (no auth) | 401 without JWT |
| `TestIntegration_AdminMiddleware_RejectsNonAdmin` | `GET /admin/users` (non-admin JWT) | 403 with valid JWT for a non-admin user |

**New tests — auth surface:**

| Test name | Method / Path | What it verifies |
|-----------|--------------|-----------------|
| `TestIntegration_HandleLogin_InvalidCreds` | `POST /auth/login` | 401 on wrong password |
| `TestIntegration_AuthMiddleware_Rejects_Unauthenticated` | `GET /auth/me` | 401 without JWT |
| `TestIntegration_AuthMiddleware_Accepts_ValidJWT` | `GET /auth/me` | 200 with valid JWT from login |
| `TestIntegration_HostRegister_HTTP` | `POST /api/hosts/register` | Creates host row with `public_key`; response contains JWT |
| `TestIntegration_NKeyChallenge_AndVerify` | `POST /api/auth/challenge` + verify | Full round-trip; returns scoped JWT |
| `TestIntegration_DeviceCode_StartAndPoll` | `POST /api/auth/device-code` + poll | Returns `{deviceCode, userCode}`; poll returns `pending` |
| `TestIntegration_HealthEndpoints` | `GET /health`, `/healthz`, `/readyz` | All return 200 with DB up |

**New tests — NATS subject handlers:**

| Test name | Subject | What it verifies |
|-----------|---------|-----------------|
| `TestIntegration_ManageGrant` | `mclaude.users.*.hosts.*.manage.grant` | Grantee appears in host access list after publish |
| `TestIntegration_ManageRevokeAccess` | `mclaude.users.*.hosts.*.manage.revoke-access` | Grant removed from DB |
| `TestIntegration_ManageDeregister` | `mclaude.users.*.hosts.*.manage.deregister` | Host row deleted from DB |
| `TestIntegration_ManageUpdate` | `mclaude.users.*.hosts.*.manage.update` | Host name updated in DB |
| `TestIntegration_ProjectCreate_NATS` | `mclaude.users.*.api.projects.create` | Project row created; KV entry written |
| `TestIntegration_CheckSlug_Available` | `mclaude.users.*.hosts.*.projects.check-slug` | `{available:true}` for unused slug |
| `TestIntegration_CheckSlug_Taken` | `mclaude.users.*.hosts.*.projects.check-slug` | `{available:false}` for existing slug |

### mclaude-control-plane `cluster_test.go`

- `TestCluster_DevUserProjectInKV` → replaced by `TestCluster_UserProjectInKV`
  - Creates ephemeral user via `POST /admin/users` with admin JWT (not `ADMIN_TOKEN`)
  - Publishes project-create NATS message, verifies KV entry, tears down
- All uses of `ADMIN_TOKEN` env var removed; tests use admin JWT from login

### mclaude-web `e2e/api.spec.ts`

Line 574: remove `|| 'dev-admin-token'` fallback. The admin token concept is gone — E2E tests that need admin operations must log in as the bootstrap admin user and use the resulting JWT.

```typescript
// Before:
const ADMIN_TOKEN = process.env['ADMIN_TOKEN'] || 'dev-admin-token'

// After: acquire admin JWT via login
// loginAsAdmin(baseURL, email, password) — direct fetch(), no Playwright page
const adminJWT = await loginAsAdmin(ADMIN_URL, process.env['BOOTSTRAP_ADMIN_EMAIL']!, process.env['BOOTSTRAP_ADMIN_PASSWORD']!)
```

### Helm chart (`charts/mclaude-cp/`)

- Remove `adminToken`, `devSeed`, `ADMIN_TOKEN`, `ADMIN_PORT`, `DEV_SEED` from `values.yaml`, `values-dev.yaml`, `values-k3d-ghcr.yaml`, and `templates/control-plane-deployment.yaml`
- Add `bootstrapAdminPassword` value (required); injected as `BOOTSTRAP_ADMIN_PASSWORD` env var on the init-keys Job

### `scripts/register-platform-host.sh` (new)

Phase 2 CI entry point. Uses `BOOTSTRAP_ADMIN_EMAIL` + `BOOTSTRAP_ADMIN_PASSWORD` from CI env to log in, then runs `mclaude host register`. Not a dev-only script — same flow an operator runs manually.

## Data Model

No schema changes. `is_admin` column already exists in `users` table.

## Error Handling

- `isAdminMiddleware`: 401 if no JWT, 403 if JWT valid but `is_admin = false`
- `bootstrapAdminUser`: if `BOOTSTRAP_ADMIN_PASSWORD` is empty and user has no password, leaves password empty (no change); does not fail — existing OAuth-only admins continue to work
- NATS subject tests use `nc.Request` with 3s timeout; `t.Fatal` on timeout

## Security

- Admin routes on main port behind `isAdminMiddleware` — same TLS termination and ingress as all other routes
- `is_admin` checked against DB on every admin request (not a JWT claim) — cannot be spoofed by modifying a token
- `/metrics` unauthenticated on main port — acceptable since the port is behind the cluster ingress (not externally reachable without auth at the ingress level)
- `BOOTSTRAP_ADMIN_PASSWORD` is a Helm value stored as a k8s Secret; never logged

## Impact

Specs updated in this commit:
- `docs/mclaude-control-plane/spec-control-plane.md` — remove loopback port section; update admin routes to main port with `is_admin` middleware; document two-phase deployment; add `BOOTSTRAP_ADMIN_PASSWORD` env var; remove `ADMIN_TOKEN`, `ADMIN_PORT`, `DEV_SEED` env vars
- `docs/charts-mclaude/spec-helm.md` — remove `adminToken`, `devSeed`, `ADMIN_PORT`/`DEV_SEED` values; add `bootstrapAdminPassword`; single-port Service
- `docs/mclaude-cli/spec-cli.md` — smoke test section: remove port-forward + `ADMIN_TOKEN`; use bootstrap admin login to obtain `adminJWT`
- `docs/mclaude-web/spec-web.md` — global-setup/teardown: replace `ADMIN_TOKEN` with bootstrap admin login; update env var list and step sequence
- `docs/spec-state-schema.md` — `mclaude-hosts` KV Writers: remove DEV_SEED secondary path sentence

## Scope

**v1 (this ADR):**
- Admin port removal (main.go, server.go, admin.go)
- `isAdminMiddleware` replacing `adminAuthMiddleware`
- `BOOTSTRAP_ADMIN_PASSWORD` in init-keys
- All new integration tests
- `TestCluster_UserProjectInKV` replacing dev-user cluster test
- Remove `TestIntegration_SeedDev_WritesLocalHostKV`
- Remove `ADMIN_TOKEN` fallback in E2E tests
- Helm chart cleanup (`ADMIN_TOKEN`, `ADMIN_PORT`, `DEV_SEED` removed)
- `scripts/register-platform-host.sh`

**Deferred:**
- Import NATS subject tests — require MinIO container
- Attachment NATS subject tests — same dependency
- SCIM endpoint tests
- `manage.revoke`, `manage.rekey` — NATS operator mode; cluster-test scope

## Integration Test Cases

The Phase 1 gate (`go test -tags integration`) is the integration test case for this ADR. The full test table in Component Changes is the specification.

## Implementation Plan

| Component | New/changed lines (est.) | Notes |
|-----------|--------------------------|-------|
| `mclaude-control-plane/main.go` | ~15 removed | Remove admin listener, adminToken |
| `mclaude-control-plane/server.go` | ~25 changed | `isAdminMiddleware`, route rewiring, remove `AdminMux` |
| `mclaude-control-plane/admin.go` | ~5 | Remove struct field if present |
| `mclaude-control-plane/init_keys.go` | ~20 | `BOOTSTRAP_ADMIN_PASSWORD` read + hash + store |
| `mclaude-control-plane/integration_test.go` | ~650 | 22 new tests, 1 deleted, helper |
| `mclaude-control-plane/cluster_test.go` | ~70 | Replace 1 test, remove ADMIN_TOKEN |
| `mclaude-web/e2e/api.spec.ts` | ~10 | Remove fallback, add admin login helper |
| `charts/mclaude-cp/` | ~20 | Remove ADMIN_TOKEN/ADMIN_PORT/DEV_SEED, add bootstrapAdminPassword |
| `scripts/register-platform-host.sh` | ~50 | Phase 2 registration script |

**Total estimated tokens:** ~160k
**Estimated wall-clock:** ~40 min
