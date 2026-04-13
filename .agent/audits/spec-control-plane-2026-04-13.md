## Run: 2026-04-13T00:00:00Z

### GAPS

**SPEC**: "Endpoints" section lists "GET  /auth/sso/{provider}       initiate SSO (Entra, Okta)" and "GET  /auth/sso/{provider}/cb    SSO callback → NATS JWT"
**CODE**: SSO endpoints are not implemented. No handlers for /auth/sso/* in server.go or any other files.

**SPEC**: "Endpoints" section lists SCIM 2.0 endpoints: "POST   /scim/v2/Users", "PUT    /scim/v2/Users/{id}", "DELETE /scim/v2/Users/{id}", "GET    /scim/v2/Users"
**CODE**: SCIM endpoints are not implemented. No handlers for /scim/* in any files.

**SPEC**: Break-glass admin endpoints should include "POST   /admin/projects           create project Deployment + PVC" and "DELETE /admin/projects/{id}      delete project" and "GET    /admin/projects           list projects for user (reads Postgres, not NATS KV)"
**CODE**: Only /admin/users and /admin/sessions/stop are implemented in handleAdminUsers(). No /admin/projects endpoints exist (mclaude-control-plane/admin.go lines 44-58).

**SPEC**: User provisioning flow step "Generate NATS NKey credentials for session agent" and "Store NATS creds in K8s Secret user-secrets in namespace" are part of the flow.
**CODE**: The adminCreateUser endpoint (mclaude-control-plane/admin.go lines 89-131) creates a user but does not generate NKey credentials or populate the user-secrets Secret in the namespace. No K8s integration for credential generation.

**SPEC**: "Publish mclaude.admin.users.created to NATS (fire-and-forget, non-fatal)" as part of user provisioning flow.
**CODE**: No publication of mclaude.admin.users.created event found in any files. User creation does not trigger NATS events.

**SPEC**: User deprovision flow: "Revoke user's NATS JWT: add user NKey to account JWT revocations, push updated account JWT to NATS server"
**CODE**: adminDeleteUser (mclaude-control-plane/admin.go lines 133-147) only calls db.DeleteUser(). No JWT revocation logic is implemented. No account JWT manipulation or NATS server updates.

**SPEC**: User deprovision flow: "kubectl delete namespace mclaude-{userId}" should be called during user deletion.
**CODE**: adminDeleteUser does not call K8s to delete the namespace. Only database deletion is performed.

**SPEC**: Postgres schema includes "CREATE TABLE nats_credentials (user_id TEXT REFERENCES users(id) ON DELETE CASCADE, nkey_seed TEXT NOT NULL, created_at TIMESTAMPTZ)"
**CODE**: The schema in db.go (lines 151-168) only defines users and projects tables. The nats_credentials table is not created. NKey seeds are not stored in Postgres.

**SPEC**: Postgres schema includes users table with "display_name TEXT NOT NULL, password_hash TEXT, google_id TEXT UNIQUE, ... last_login_at TIMESTAMPTZ"
**CODE**: The users table schema (db.go line 154) has "name" not "display_name", and lacks "google_id" and "last_login_at" columns. Schema does not match spec exactly.

**SPEC**: Line 235: "JWT expiry duration and refresh threshold are configurable in control-plane (env vars `JWT_EXPIRY_SECONDS` default 28800, `JWT_REFRESH_THRESHOLD_SECONDS` default 900)"
**CODE**: JWT_EXPIRY_SECONDS is parsed in main.go (lines 35-40) with default 8*time.Hour (28800 seconds) ✓. But JWT_REFRESH_THRESHOLD_SECONDS is never parsed or used anywhere in the codebase. This env var is not implemented.

**SPEC**: "control-plane creates all four buckets idempotently on startup (`nats.KeyValueStoreOrCreate`)"
**CODE**: Only mclaude-projects KV bucket is created in projects.go via ensureProjectsKV (line 139-142). No initialization of mclaude-sessions, mclaude-heartbeats, or mclaude-laptops buckets. The four KV buckets specified in the spec are not all initialized on startup.

**SPEC**: Project provisioning flow: "Write Project JSON to NATS KV mclaude-projects/{userId}/{projectId}"
**CODE**: projects.go correctly writes to KV (lines 88-100), but the KV key format uses "." separator (line 96: "userID+"."+id"). The spec shows forward slash in examples like `{userId}/{projectId}`, though the code comment (line 126-127) notes that "." is used for NATS token separator. This is technically correct but the spec examples show "/" notation.

**SPEC**: Line 1085: "Login endpoints return 503 while Postgres is unreachable."
**CODE**: handleLogin (auth.go lines 62-113) returns 503 when s.db == nil (lines 76-79) ✓. But handleRefresh (lines 115-153) does not check if database is available before trying to decode JWT. If Postgres is down but NATS JWT is valid, refresh may succeed, which is acceptable. However, the spec says "Login endpoints return 503 while Postgres is unreachable" — refresh is listed as /auth/refresh and should also return 503 if DB is unavailable for consistency.

**SPEC**: Line 1126: "control-plane polls pod status and reflects `PROJECT_STATUS_FAILED` in NATS KV."
**CODE**: No polling of pod status is implemented. Projects.go creates projects and writes to KV, but there is no reconciliation loop, pod status monitoring, or updating project status to PROJECT_STATUS_FAILED on pod failures.

**SPEC**: "Health probes - `/health` for liveness, `/ready` for readiness. `/health` never checks NATS."
**CODE**: server.go registers /health, /healthz, and /readyz (lines 16-26), all returning 200 ✓. However, the implementation is stub endpoints with no actual health checks. They don't check Postgres connectivity, NATS connectivity, or readiness state. The spec says /ready should check Postgres, and /health should NOT check NATS — but current code checks neither.

**SPEC**: "Break-glass admin (not exposed via nginx)... Bind to separate port (`:9090`)."
**CODE**: main.go line 28 sets adminPort default to "9091", not "9090" as specified in the spec. The spec states it should bind to `:9090`.

**SPEC**: "Projects: POST   /api/projects           create project Deployment + PVC, DELETE /api/projects/{id}      delete project, GET    /api/projects           list projects for user (reads NATS KV), GET    /api/projects/{id}      get project status"
**CODE**: Projects are created via NATS request/reply (projects.go StartProjectsSubscriber), not HTTP endpoints. No HTTP endpoints for /api/projects are registered in server.go. The NATS subject is mclaude.{userId}.api.projects.create, which is correct, but the spec also lists these as Endpoints suggesting HTTP routes should exist. GET /api/projects endpoints for listing/reading are completely missing.

**SPEC**: "Generate NATS NKey credentials for session agent" during user provisioning.
**CODE**: While IssueUserJWT (nkeys.go lines 92-118) generates user JWTs, there is no code to generate session-agent-specific credentials. SessionAgentSubjectPermissions (nkeys.go lines 31-40) defines permissions but they are never used to issue a separate JWT for the session agent to use. The spec implies session agents get separate long-lived credentials issued during provisioning.

**SPEC**: Line 595: "Pod starts, session-agent connects to NATS, begins subscriptions"
**CODE**: control-plane provisions K8s resources via ProvisionProject (provision.go), but it does NOT wait for or verify that the pod actually starts successfully. The "reply with projectId" happens immediately after K8s resources are created, not after session-agent connectivity is confirmed.

**SPEC**: "Clients may operate on their own mclaude.{userId}.> namespace" per NATS permissions in nkeys.go.
**CODE**: Correct — UserSubjectPermissions (nkeys.go lines 21-28) and SessionAgentSubjectPermissions (lines 31-40) implement the right namespacing ✓.

