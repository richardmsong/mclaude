# Control-Plane Full Implementation Audit

**Date:** 2026-04-29
**Component:** `mclaude-control-plane/`
**Specs evaluated:**
- `docs/mclaude-control-plane/spec-control-plane.md`
- `docs/spec-state-schema.md`

**Files reviewed:** All 27 `.go` files in `mclaude-control-plane/` (production + test + testutil).

---

## NEW GAPS (not already documented as known bugs in the spec)

### GAP-CP-01: DATABASE_DSN empty does not fatal — MISMATCH

**Spec says:**
> `DATABASE_URL` / `DATABASE_DSN` | Yes | (none -- exits on startup if empty)
>
> Postgres unavailable at startup: fatal exit.

**Code does:**
```go
if databaseDSN != "" {
    db, err = ConnectDB(ctx, databaseDSN)
    // ... fatal on connect error
} else {
    logger.Warn().Msg("DATABASE_DSN not set — running without persistence")
}
```
The code warns and continues without persistence instead of fatal-exiting.

**Severity:** MISMATCH — the control-plane runs in a degraded state silently. All HTTP handlers check `s.db == nil` and return 503, but the process stays alive and accepting connections. A misconfigured Helm deploy could result in a control-plane pod that appears healthy but rejects every request.

---

### GAP-CP-02: Provisioning failure does not roll back DB/KV or mark project failed

**Spec says:**
> If the controller times out or replies with an error, control-plane returns 503 Service Unavailable with `{error: "host {hslug} unreachable"}` and rolls back the Postgres + KV writes (or marks `projects.status = 'failed'` — whichever the implementation chooses).

**Code does (projects.go NATS handler):**
```go
provReply, err := nc.Request(provSubject, provData, ProvisionTimeoutSeconds*time.Second)
if err != nil {
    log.Error().Err(err).Msg("provisioning request timed out — host unreachable")
}
// ... falls through to reply with project ID regardless
reply, _ := json.Marshal(map[string]string{"id": id})
_ = msg.Respond(reply)
```
On provisioning timeout or error, the code logs the error but:
1. Does NOT roll back the Postgres row
2. Does NOT roll back the KV write
3. Does NOT mark `projects.status = 'failed'`
4. Replies with the project ID as if everything succeeded

**Severity:** MISSING — projects can be created in DB/KV with no backing infrastructure on the host. SPA shows the project but the user cannot use it.

---

### GAP-CP-03: No NATS publish to `api.projects.update` after OAuth connection changes

**Spec says:**
> On successful callback, the control-plane stores connection metadata in the `oauth_connections` Postgres table and publishes a NATS message on `mclaude.users.{uslug}.hosts.{hslug}.api.projects.update` so `mclaude-controller-k8s` can refresh the per-user `user-secrets` Secret with the new tokens.
>
> After any connection change (connect, disconnect, token refresh), the control-plane re-publishes the update message so `gh-hosts.yml` and `glab-config.yml` reconciliation continues to land on the correct host.

**Code does:**
- `patchUserSecret()` is a no-op (correct per ADR-0035 — K8s ops moved to controller)
- `reconcileUserCLIConfig()` is a no-op (correct per ADR-0035)
- But NO NATS message is published to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.update` to trigger the controller to do the Secret updates

This affects:
- `handleOAuthCallback` — no NATS publish after OAuth connect
- `handleAddPAT` — no NATS publish after PAT add
- `handleDeleteConnection` — no NATS publish after disconnect
- `refreshGitLabToken` — no NATS publish after token refresh

**Severity:** MISSING — controllers never learn about credential changes. Git credentials on cluster hosts become stale. BYOH machines never receive new credential files.

---

### GAP-CP-04: No NATS publish to `api.projects.delete` on project/host deletion

**Spec says (NATS Publishes table):**
> `mclaude.users.{uslug}.hosts.{hslug}.api.projects.delete` | Project delete | Asks the controller to tear down per-project resources.

**Code does:**
No code anywhere publishes to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.delete`. When `DELETE /api/users/{uslug}/hosts/{hslug}` is called, the hosts row is deleted (cascading to projects via FK), but no NATS message is sent to the controller to clean up Kubernetes resources (namespaces, Deployments, PVCs, RBAC).

**Severity:** MISSING — orphaned K8s resources persist on the worker cluster after host/project deletion.

---

### GAP-CP-05: PATCH /api/projects/{id} does not publish NATS update to controller

**Spec says (NATS Publishes table):**
> `mclaude.users.{uslug}.hosts.{hslug}.api.projects.update` | `PATCH /api/projects/{id}` | Asks the controller to apply project metadata changes.

**Code does (handlePatchProject):**
Writes updated state to `mclaude-projects` KV bucket for SPA visibility, but does NOT publish a NATS request to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.update`. The controller never learns about git identity changes.

**Severity:** MISSING — when the user changes `gitIdentityId` via PATCH, the cluster controller doesn't update the `user-secrets` Secret with the new credential. The session-agent continues using the old identity.

---

### GAP-CP-06: Provisioning subject is `.api.projects.create` instead of `.api.projects.provision`

**Spec says (control-plane NATS Publishes table):**
> `mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision` | `POST /api/users/{uslug}/projects`

**Code does (projects.go, both NATS handler and seedDev):**
```go
provSubject := "mclaude.users." + userSlug + ".hosts." + req.HostSlug + ".api.projects.create"
```

The code uses `.api.projects.create` instead of `.api.projects.provision`. The state schema lists BOTH subjects, but the control-plane spec specifically says it publishes to `.provision` for provisioning requests triggered by project creation.

**Severity:** MISMATCH — if the controller subscribes to `.provision` as the spec says, provisioning requests from the control-plane will never be received. (If the controller subscribes to `.api.projects.>` wildcard, this is harmless but still inconsistent.)

---

### GAP-CP-07: Duplicate cluster slug returns 500 instead of 409

**Spec says (Error Handling):**
> Duplicate cluster slug (admin tries to register a slug already in use): returns 409 Conflict.

**Code does (adminRegisterCluster):**
```go
if err != nil {
    log.Error().Err(err).Msg("create cluster host row")
    http.Error(w, "failed to create cluster", http.StatusInternalServerError)
    return
}
```
No check for unique constraint violation — all DB errors return 500.

**Severity:** MISMATCH — admin gets a confusing 500 error instead of a clear 409 when re-registering an existing cluster slug.

---

### GAP-CP-08: `slugify()` in hosts.go lacks validation (blocklist, length, charset)

**Spec says (Error Handling):**
> Slug validation failure at ingress: returns HTTP 400 with `{code: "invalid_slug", reason: "reserved_word|charset|length", field: "slug"}`.
> Reserved-word match in slugify: fallback kicks in automatically (deterministic `{prefix}-{6 base32}`).

**Code does (hosts.go `slugify`):**
```go
func slugify(name string) string {
    s := strings.ToLower(name)
    var b strings.Builder
    for _, c := range s { /* basic char filter */ }
    return strings.Trim(b.String(), "-")
}
```
No reserved-word blocklist check, no length validation (spec: `[a-z0-9][a-z0-9-]{0,62}`), no fallback generation. A host named "api" becomes slug "api" which collides with the reserved word. A host named with 200 characters creates an overly long slug.

The spec documents the `computeUserSlug()` blocklist bug, but this identical issue in `slugify()` for host registration is undocumented.

**Severity:** MISSING — host slugs can collide with reserved words or exceed length limits, potentially breaking NATS subject routing.

---

### GAP-CP-09: `handleCreateHost` does not return JWT/seed in response

**Spec says:**
> POST /api/users/{uslug}/hosts — Creates a machine host directly (`{slug, name, publicKey?}`). Bypasses the device-code flow. Inserts a `hosts` row with `type='machine'`, `role='owner'`, mints a per-host user JWT, returns host response.

**Code does:**
The code mints a JWT via `IssueHostJWT()` and stores it in the DB, but the response only contains `HostResponse{ID, Slug, Name, Type, Role, CreatedAt}` — no JWT or NKey seed. The caller has no way to use the host after creating it (no credentials to connect to NATS).

**Severity:** PARTIAL — the host is created and the JWT is stored in the DB, but the API consumer cannot retrieve credentials for NATS connection. (The device-code flow returns credentials correctly.)

---

### GAP-CP-10: `mclaude.users.{uslug}.api.projects.updated` broadcast never published

**Spec says (NATS Subjects — User-level subjects):**
> `mclaude.users.{uslug}.api.projects.updated` | control-plane | SPA | Broadcast notification that project state has changed. **Not yet published by code.**

**Code does:** No code anywhere publishes to this subject. Project changes are communicated only through KV watches.

**Severity:** MISSING (documented in state-schema spec as "not yet published by code" but not flagged in the control-plane spec).

---

### GAP-CP-11: OAuth callback does not set `users.oauth_id` on first login

**Spec says (users table):**
> `oauth_id` TEXT NULL — Provider's stable user identifier from the linked OAuth identity. NULL for unlinked rows (e.g. bootstrap-admin row created before first login). On first OAuth callback whose email matches the row, control-plane sets this to the provider's `id`.

**Code does:** The OAuth callback handler (`handleOAuthCallback`) stores the connection in `oauth_connections` but never updates `users.oauth_id`. There is no code path that sets `oauth_id` on the users table after the initial row creation (which always sets it to NULL).

**Severity:** MISSING — the `users.oauth_id` column is always NULL for all users. The spec says it should be populated on first OAuth login matching the user's email.

---

### GAP-CP-12: `IssueUserJWT` does not set `claims.IssuerAccount`

**Spec says:**
> issues a user JWT signed by the account signing key

**Code does (`nkeys.go`):**
```go
func IssueUserJWT(...) {
    claims := natsjwt.NewUserClaims(userKP.PublicKey)
    claims.Name = userID
    claims.Expires = expirySecs
    // ... no claims.IssuerAccount = ...
    encoded, err := claims.Encode(accountKP)
}
```
Contrast with `generateNATSUserCreds` which explicitly sets `claims.IssuerAccount, _ = accountKP.PublicKey()`. The `IssuerAccount` field tells the NATS server which account this user belongs to. Without it, the JWT may fail validation on servers that require it.

Note: `Encode(accountKP)` may implicitly set the `Issuer` field from the signing key, but `IssuerAccount` is a separate field. The same omission exists in `IssueHostJWT` and `IssueSessionAgentJWT`.

**Severity:** PARTIAL — may cause JWT validation failures on strict NATS configurations. Works in practice if the NATS server infers the account from the resolver preload.

---

## CONFIRMED KNOWN BUGS/GAPS (already documented in the spec)

These are gaps the spec explicitly flags as "Known bug" or "Known gap". Included for completeness.

### KNOWN-01: `IssueUserJWT` double expiry
**Spec:** "Known bug: `IssueUserJWT` is called with `expiresAt + expirySecs` where `expiresAt` is already `now + 8h`, resulting in `claims.Expires = now + 16h`."
**Code confirms:** `IssueUserJWT(user.ID, user.Slug, s.accountKP, expiresAt+expirySecs)` in both `handleLogin` and `handleRefresh`.

### KNOWN-02: `handleRefresh` returns `s.natsURL` instead of `s.natsWsURL`
**Spec:** "Known bug: returns `s.natsURL` (internal broker URL) instead of `s.natsWsURL`."
**Code confirms:** `NATSUrl: s.natsURL` in handleRefresh response.

### KNOWN-03: `readyz` always returns 200
**Spec:** "Known bug: currently returns 200 unconditionally. Should check Postgres connectivity."
**Code confirms:** `w.WriteHeader(http.StatusOK)` unconditionally.

### KNOWN-04: `IssueHostJWT` called with UUID instead of slug
**Spec:** "Known bug: `IssueHostJWT(userID, ...)` passes UUID instead of slug."
**Code confirms:** All call sites pass `userID` (UUID) or `user.ID` (UUID) as the first argument:
- `handleCreateHost`: `IssueHostJWT(userID, req.Slug, ...)`
- `handleHostRegister`: `IssueHostJWT(entry.UserID, slug, ...)`
- `adminGrantCluster`: `IssueHostJWT(user.ID, cslug, ...)`

### KNOWN-05: `adminGrantCluster` calls `GetUserByEmail` instead of `GetUserBySlug`
**Spec:** "Known bug: handler calls `GetUserByEmail(req.UserSlug)` instead of `GetUserBySlug(req.UserSlug)`."
**Code confirms:** `user, err := s.db.GetUserByEmail(r.Context(), req.UserSlug)`

### KNOWN-06: `adminRegisterCluster` user_id is arbitrary
**Spec:** "Known bug: `hosts` row `user_id` is set to `(SELECT id FROM users LIMIT 1)` (arbitrary)."
**Code confirms:** `VALUES ($1, (SELECT id FROM users LIMIT 1), ...)`

### KNOWN-07: `adminRegisterCluster` response omits accountJwt/operatorJwt
**Spec:** "Known bug: response omits `accountJwt`/`operatorJwt` (fields exist but unpopulated)."
**Code confirms:** Response sets `AccountJWT: ""` (zero value) and `OperatorJWT: ""` (zero value).

### KNOWN-08: `adminStopSession` queries non-existent sessions table
**Spec:** "Known bug: handler executes `UPDATE sessions SET status = 'stopped'` against a `sessions` table that does not exist in the schema."
**Code confirms:** `UPDATE sessions SET status = 'stopped', stopped_at = NOW() WHERE ...` — the `sessions` table is not in the schema DDL.

### KNOWN-09: `POST /admin/users` lacks `isAdmin` field
**Spec:** "Known gap: spec says optional `isAdmin` field, but `AdminUserRequest` struct has no `IsAdmin` field."
**Code confirms:** `AdminUserRequest` has `ID, Email, Name, Password` only.

### KNOWN-10: `POST /admin/users/{uslug}/promote` not wired
**Spec:** "Not yet implemented — DB method `SetUserAdmin` exists but HTTP handler is not wired."
**Code confirms:** `SetUserAdmin` exists in `db.go`; no handler in `admin.go handleAdminRoutes`.

### KNOWN-11: `DELETE /admin/users` does not revoke NATS JWT or notify controllers
**Spec:** "Known gap: does not revoke the user's NATS JWT and does not publish NATS delete requests to controllers."
**Code confirms:** `adminDeleteUser` only calls `db.DeleteUser()` — no NATS operations.

### KNOWN-12: `computeUserSlug` does not check reserved-word blocklist
**Spec:** "Known bug: `computeUserSlug()` in `db.go` does not check against the blocklist."
**Code confirms:** `computeUserSlug` does basic slugification only — no blocklist check.

### KNOWN-13: `CreateProjectWithIdentity` does not set slug or host_id
**Spec:** "Known bug: `CreateProjectWithIdentity` does not compute or set the `slug` column on INSERT. Known bug: `host_id` column is nullable and `CreateProjectWithIdentity` does not set `host_id`."
**Code confirms:** INSERT omits both `slug` and `host_id`.

### KNOWN-14: `host_id` column is nullable despite spec requiring NOT NULL
**Spec:** "Known bug: `host_id` column is nullable in the DDL despite the spec requiring NOT NULL."
**Code confirms:** DDL says `host_id TEXT REFERENCES hosts(id) ON DELETE CASCADE` (no NOT NULL).

### KNOWN-15: Device-code pending response does not include `expiresAt`
**Spec:** "Known gap: pending response does not include `expiresAt`."
**Code confirms:** `DeviceCodeStatusResponse` has Status, Slug, JWT, HubURL — no ExpiresAt.

### KNOWN-16: `POST /admin/users` and `seedDev` do not create default host row
**Spec:** "Known bug: `POST /admin/users` and `seedDev` `CreateUser` do not create the default host row — only the startup migration backfill does."
**Code confirms:** `adminCreateUser` calls `db.CreateUser()` which does not insert a hosts row. The schema migration backfill does it on next restart.

### KNOWN-17: Login response is flat (missing hosts[], projects[])
**Spec (state-schema):** "Spec target (not yet implemented): The ADR-0035 target includes a richer response with nested user object, hosts[] array, and projects[] array."
**Code confirms:** `LoginResponse` has `NATSUrl, JWT, NKeySeed, UserID, UserSlug, ExpiresAt` only.

### KNOWN-18: `PROVISION_TIMEOUT_SECONDS` hardcoded, not read from env
**Spec:** "Note: currently a hardcoded constant in code (`const ProvisionTimeoutSeconds = 10`), not yet read from env."
**Code confirms:** `const ProvisionTimeoutSeconds = 10`.

### KNOWN-19: HTTP project CRUD endpoints not implemented
**Spec:** "HTTP project CRUD — not yet implemented."
**Code confirms:** No handlers for `POST/GET/DELETE /api/users/{uslug}/projects[/{pslug}]`.

### KNOWN-20: SCIM 2.0 endpoints not implemented
**Spec:** "SCIM 2.0 (IdP user provisioning) — not yet implemented."
**Code confirms:** No SCIM handlers.

### KNOWN-21: `DELETE /admin/clusters/{cslug}` not implemented
**Spec:** "Not yet implemented — deferred per ADR-0035."
**Code confirms:** No handler for DELETE cluster in `handleAdminRoutes`.

### KNOWN-22: Auth middleware lacks access boundary enforcement
**Spec:** "Known gap: the spec-described access boundary enforcement (cross-user 403 when JWT `sub` doesn't match URL `{uslug}`, cross-host 403) is not implemented."
**Code confirms:** `authMiddleware` only extracts user ID; no URL parameter checking.

---

## SUMMARY

| Category | New Gaps | Confirmed Known | Total |
|----------|----------|-----------------|-------|
| Database schema | 0 | 3 (KNOWN-12,13,14) | 3 |
| Auth / JWT | 2 (CP-01,12) | 3 (KNOWN-01,02,22) | 5 |
| NATS subjects | 2 (CP-06,10) | 1 (KNOWN-18) | 3 |
| NATS publishing | 3 (CP-03,04,05) | 0 | 3 |
| Provisioning | 1 (CP-02) | 0 | 1 |
| API behavior | 3 (CP-07,08,09) | 6 (KNOWN-03,05,06,07,15,16) | 9 |
| Missing endpoints | 0 | 4 (KNOWN-10,19,20,21) | 4 |
| Admin ops | 0 | 3 (KNOWN-08,09,11) | 3 |
| Login response | 0 | 1 (KNOWN-17) | 1 |
| OAuth integration | 1 (CP-11) | 0 | 1 |
| **Total** | **12** | **22** | **34** |

### Critical new gaps (highest impact):
1. **GAP-CP-03** — No NATS publish for credential changes → stale git credentials on all hosts
2. **GAP-CP-02** — No rollback on provisioning failure → phantom projects in DB/KV
3. **GAP-CP-04** — No NATS publish for project/host deletion → orphaned K8s resources
4. **GAP-CP-05** — PATCH project doesn't notify controller → stale git identity on hosts
5. **GAP-CP-06** — Provisioning uses wrong NATS subject (`.create` vs `.provision`)
