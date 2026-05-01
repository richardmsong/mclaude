# Implementation Audit: mclaude-control-plane

## Run: 2026-05-01T00:00:00Z

**Component:** mclaude-control-plane
**Directory:** /Users/rsong/work/mclaude/mclaude-control-plane
**Primary spec:** docs/mclaude-control-plane/spec-control-plane.md
**ADR:** docs/adr-0054-nats-jetstream-permission-tightening.md (status: draft — evaluated via spec references only)
**State schema:** docs/spec-state-schema.md

Note: ADR-0054 has status `draft` so it is not directly authoritative. However, spec-control-plane.md incorporates ADR-0054 decisions extensively and is the primary evaluation target.

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-cp: Role | "authenticates all identity types (users, hosts, agents) via a unified HTTP NKey challenge-response protocol" | challenge.go:67-170 | IMPLEMENTED | | handleAuthChallenge + handleAuthVerify implement the unified flow |
| spec-cp: Role | "issues scoped NATS JWTs (signed by the deployment-level account key)" | nkeys.go:200-240 | IMPLEMENTED | | IssueUserJWT, IssueHostJWT, IssueSessionAgentJWT all sign with accountKP |
| spec-cp: Role | "manages user / project / host records in Postgres" | db.go (entire) | IMPLEMENTED | | Full CRUD for users, projects, hosts, host_access, agent_credentials, attachments |
| spec-cp: Role | "publishes provisioning requests over NATS to the appropriate controller" | projects.go:175-215 | IMPLEMENTED | | Publishes to host-scoped fan-out subject per ADR-0054 |
| spec-cp: Role | "manages host lifecycle (registration, access grants/revocation, deregistration, emergency credential revocation)" | lifecycle.go (entire) | IMPLEMENTED | | All manage.* handlers present |
| spec-cp: Role | "handles OAuth provider integrations (GitHub/GitLab)" | providers.go | IMPLEMENTED | | Full OAuth flow, PAT support, GitLab refresh |
| spec-cp: Role | "tracks host liveness via $SYS events" | sys_subscriber.go | IMPLEMENTED | | CONNECT/DISCONNECT handlers for both Client and Leafnode |
| spec-cp: Role | "manages S3 pre-signed URLs for binary data (imports and attachments)" | providers.go (S3 section not found) | GAP | CODE→FIX | No S3/attachment HTTP handlers found (POST /api/attachments/upload-url, /api/attachments/{id}/confirm, GET /api/attachments/{id}). The Postgres schema for attachments exists but no HTTP route handlers are wired. NATS import/attachment handlers also missing. |
| spec-cp: Role | "provisions per-user JetStream resources (KV buckets and sessions streams)" | projects.go:300-380 (ensureUserResources) | IMPLEMENTED | | ensurePerUserSessionsKV, ensurePerUserProjectsKV, ensurePerUserSessionsStream |
| spec-cp: Role | "exposes admin endpoints for cluster registration and access grants" | admin.go | IMPLEMENTED | | POST/GET /admin/clusters, POST /admin/clusters/{cslug}/grants |
| spec-cp: Role | "all identity types generate their own NKeys — the control-plane never generates NKey pairs or handles private key material" | auth.go:120-125, challenge.go:160 | PARTIAL | SPEC→FIX | CP still has legacy mode (IssueUserJWTLegacy, IssueHostJWTLegacy) where it generates NKey pairs for backward compat. The spec says "never" but code retains generation for old clients. |
| spec-cp: Deployment | "Listens on two ports: the main API port (default 8080)... and a loopback-only admin port (default 9091, bound to 127.0.0.1)" | main.go:128-135 | IMPLEMENTED | | Main on :PORT, admin on 127.0.0.1:ADMIN_PORT |
| spec-cp: Env | "EXTERNAL_URL... exits on startup if empty" | main.go:67-70 | IMPLEMENTED | | Fatal exit if empty |
| spec-cp: Env | "DATABASE_URL / DATABASE_DSN... exits on startup if empty" | main.go:57-64 | IMPLEMENTED | | Fatal exit if empty |
| spec-cp: Env | "NATS_ACCOUNT_SEED... If not set, generates an ephemeral account key (dev-only)" | main.go:77-80 (loadOrGenerateAccountKey) | IMPLEMENTED | | |
| spec-cp: Env | "OPERATOR_KEYS_PATH... provides operatorSeed and sysAccountSeed" | main.go (not read) | GAP | CODE→FIX | Spec says CP loads operatorSeed and sysAccountSeed from OPERATOR_KEYS_PATH at startup. Code has SetRevocationCredentials() but it's never called in main.go — the revocation credentials are never loaded from the operator-keys secret at runtime. |
| spec-cp: Env | "NATS_SYS_ACCOUNT_SEED... If set as an env var, takes precedence" | main.go (not read) | GAP | CODE→FIX | Not read from env in main.go |
| spec-cp: Env | "S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY" | (not read) | GAP | CODE→FIX | No S3 configuration loading in main.go. No S3 client initialized. |
| spec-cp: Env | "PROVISION_TIMEOUT_SECONDS... currently a hardcoded constant" | projects.go:60-68 | IMPLEMENTED | | provisionTimeoutSeconds() reads from env with 10s default |
| spec-cp: HTTP /auth/login | "Request body includes nkey_public... Returns the Login Response" | auth.go:100-175 | IMPLEMENTED | | Handles both nkey_public and legacy modes |
| spec-cp: HTTP /auth/login | "CP stores the public key in users.nkey_public" | auth.go:120-124 | IMPLEMENTED | | SetUserNKeyPublic called |
| spec-cp: HTTP /auth/refresh | "Exchange a valid per-host JWT from the Authorization header for a new JWT" | auth.go:178-240 | IMPLEMENTED | | |
| spec-cp: HTTP /api/auth/challenge | "Request: {nkey_public}. CP looks up the public key across users/hosts/agent_credentials" | challenge.go:67-120 | IMPLEMENTED | | isNKeyRegistered checks all three tables |
| spec-cp: HTTP /api/auth/challenge | "Returns {challenge} (random nonce, single-use, 30s TTL, stored in-memory)" | challenge.go:97-115 | IMPLEMENTED | | 32-byte nonce, 30s expiry |
| spec-cp: HTTP /api/auth/challenge | "Error: NOT_FOUND if public key is unknown" | challenge.go:104-110 | IMPLEMENTED | | Returns NOT_FOUND code |
| spec-cp: HTTP /api/auth/verify | "Request: {nkey_public, challenge, signature}" | challenge.go:120-170 | IMPLEMENTED | | |
| spec-cp: HTTP /api/auth/verify | "CP verifies the Ed25519 signature... resolves current permissions... signs a JWT... returns {ok, jwt}" | challenge.go:155-170, issueJWTForNKey | IMPLEMENTED | | Full flow works |
| spec-cp: HTTP /api/auth/verify | "Errors: UNAUTHORIZED (invalid signature), FORBIDDEN (host revoked), EXPIRED (challenge expired)" | challenge.go:135-155 | PARTIAL | CODE→FIX | UNAUTHORIZED and EXPIRED are returned, but FORBIDDEN for revoked host is not explicitly checked — issueJWTForNKey issues JWTs even for revoked hosts |
| spec-cp: HTTP /api/auth/device-code | "Initiate device-code login flow for CLI. Returns {deviceCode, userCode, verificationUrl, expiresIn, interval}. 15-minute TTL." | device_auth.go:85-120 | IMPLEMENTED | | All fields returned, 15-min TTL |
| spec-cp: HTTP /api/auth/device-code/poll | "Returns {status: 'pending'} while waiting, or {jwt, userSlug} once completed. Returns 410 Gone if expired." | device_auth.go:122-160 | IMPLEMENTED | | |
| spec-cp: HTTP /api/auth/device-code/verify | "Web UI endpoint where user enters the device code and authenticates" | device_auth.go:162-230 | IMPLEMENTED | | Serves HTML verification page |
| spec-cp: HTTP /version | "Returns minClientVersion and serverVersion" | version.go | IMPLEMENTED | | |
| spec-cp: HTTP /health | "Returns 200 OK (process alive check — never checks NATS)" | server.go:21-23 | IMPLEMENTED | | |
| spec-cp: HTTP /healthz | "Kubernetes liveness probe (same as /health — never checks NATS)" | server.go:28-30 | IMPLEMENTED | | |
| spec-cp: HTTP /readyz | "Checks Postgres connectivity — returns 503 when DB is unreachable" | server.go:31-38 | IMPLEMENTED | | Pings DB, 503 on failure |
| spec-cp: HTTP /auth/me | "Returns authenticated user info and connected OAuth providers" | auth.go:243-280 | IMPLEMENTED | | |
| spec-cp: HTTP /api/attachments/upload-url | "Request pre-signed S3 upload URL for an attachment" | (not found) | GAP | CODE→FIX | No HTTP handler for attachment upload URL generation |
| spec-cp: HTTP /api/attachments/{id}/confirm | "Confirm attachment upload" | (not found) | GAP | CODE→FIX | No HTTP handler for attachment confirmation |
| spec-cp: HTTP /api/attachments/{id} | "Get attachment download URL" | (not found) | GAP | CODE→FIX | No HTTP handler for attachment download |
| spec-cp: HTTP POST /api/users/{uslug}/projects | "Creates a project on a specified host" | project_http.go:55-130 | IMPLEMENTED | | Full flow: Postgres + KV + provisioning + broadcast |
| spec-cp: HTTP GET /api/users/{uslug}/projects | "Lists all projects for the user" | project_http.go:40-54 | IMPLEMENTED | | |
| spec-cp: HTTP GET /api/users/{uslug}/projects/{pslug} | "Gets a single project by slug" | project_http.go:132-150 | IMPLEMENTED | | |
| spec-cp: HTTP DELETE /api/users/{uslug}/projects/{pslug} | "Deletes a project" | project_http.go:152-195 | IMPLEMENTED | | With NATS notifications |
| spec-cp: HTTP POST /api/users/{uslug}/hosts | "Creates a machine host directly" | hosts.go:148-195 | IMPLEMENTED | | |
| spec-cp: HTTP POST /api/users/{uslug}/hosts/code | "Generates a 6-character device code for BYOH host registration" | hosts.go:225-255 | IMPLEMENTED | | |
| spec-cp: HTTP GET /api/users/{uslug}/hosts/code/{code} | "Polls device-code status" | hosts.go:258-295 | IMPLEMENTED | | Includes expiresAt (KNOWN-15 fixed) |
| spec-cp: HTTP POST /api/hosts/register | "Redeems a device code with {code, name}" | hosts.go:300-370 | IMPLEMENTED | | |
| spec-cp: HTTP PUT /api/users/{uslug}/hosts/{hslug} | "Updates host display name" | hosts.go:198-222 | IMPLEMENTED | | |
| spec-cp: HTTP DELETE /api/users/{uslug}/hosts/{hslug} | "Removes a host (cascades to its projects + sessions)" | hosts.go:224-260 | IMPLEMENTED | | With NATS project delete notifications |
| spec-cp: Admin POST /admin/users | "Creates a user (id, email, name, optional password)" | admin.go:100-140 | IMPLEMENTED | | IsAdmin field now in AdminUserRequest struct (KNOWN-09 fixed) |
| spec-cp: Admin POST /admin/users/{uslug}/promote | "Sets users.is_admin = true" | admin.go:285-305 | IMPLEMENTED | | Handler wired and functional (KNOWN-10 fixed) |
| spec-cp: Admin GET /admin/users | "Lists all users" | admin.go:75-98 | IMPLEMENTED | | |
| spec-cp: Admin DELETE /admin/users/{id} | "Deletes a user... Known gap: does not revoke the user's NATS JWT" | admin.go:142-195 | IMPLEMENTED | | Documented gap — JWT revocation not functional |
| spec-cp: Admin POST /admin/sessions/stop | "Break-glass session stop" | admin.go:197-245 | IMPLEMENTED | | Now publishes to sessions.delete subject (CP-3 fixed) |
| spec-cp: Admin POST /admin/clusters | "Registers a new cluster" | admin.go:248-310 | IMPLEMENTED | | With owner user lookup fix (KNOWN-06) |
| spec-cp: Admin GET /admin/clusters | "Lists registered clusters" | admin.go:313-345 | IMPLEMENTED | | |
| spec-cp: Admin POST /admin/clusters/{cslug}/grants | "Grants user access" | admin.go:348-395 | IMPLEMENTED | | Uses GetUserBySlug (KNOWN-05 fixed) |
| spec-cp: Admin DELETE /admin/clusters/{cslug} | "Removes the cluster" | admin.go:398-420 | IMPLEMENTED | | Now implemented (KNOWN-21 fixed) |
| spec-cp: NATS mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create | "Fan-out provisioning" | projects.go:175-215, project_http.go:85-125 | IMPLEMENTED | | Both NATS and HTTP paths publish fan-out |
| spec-cp: NATS mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.delete | "Asks the controller to tear down per-project resources" | projects.go:420-440 (publishProjectsDeleteToHost) | IMPLEMENTED | | |
| spec-cp: NATS $SYS.ACCOUNT.{accountKey}.CONNECT | "Per-connection event... update last_seen_at, upsert mclaude-hosts KV" | sys_subscriber.go | IMPLEMENTED | | Handles Client (machine) and Leafnode (cluster) |
| spec-cp: NATS $SYS.ACCOUNT.{accountKey}.DISCONNECT | "Same lookup logic; sets mclaude-hosts KV online=false" | sys_subscriber.go | IMPLEMENTED | | read-modify-write preserves lastSeenAt |
| spec-cp: NATS mclaude.hosts.{hslug}.api.agents.register | "Agent public key registration... validates host access + project ownership + host assignment" | lifecycle.go:106-165 | IMPLEMENTED | | Full validation chain |
| spec-cp: NATS mclaude.users.*.hosts._.register | "Host registration via NATS... Returns {ok, slug} — no JWT" | lifecycle.go:170-230 | IMPLEMENTED | | |
| spec-cp: NATS mclaude.users.*.hosts.*.manage.grant | "Host access grant... inserts into host_access, revokes grantee's JWT" | lifecycle.go:236-295 | IMPLEMENTED | | |
| spec-cp: NATS mclaude.users.*.hosts.*.manage.revoke-access | "Host access revocation... deletes from host_access, revokes grantee JWT + agent JWTs" | lifecycle.go:300-365 | IMPLEMENTED | | Revokes user + agent JWTs |
| spec-cp: NATS mclaude.users.*.hosts.*.manage.deregister | "Host deregistration... drains projects, revokes host, cleans up" | lifecycle.go:370-430 | IMPLEMENTED | | All 6 steps executed |
| spec-cp: NATS mclaude.users.*.hosts.*.manage.revoke | "Emergency credential revocation... host JWT + agent JWTs" | lifecycle.go:435-490 | IMPLEMENTED | | Marks host as revoked in KV |
| spec-cp: NATS mclaude.users.*.hosts.*.manage.rekey | "Rotate host NKey public key" | lifecycle.go:495-545 | IMPLEMENTED | | Revokes old JWT, stores new key |
| spec-cp: NATS mclaude.users.*.hosts.*.manage.update | "Update host display name or type metadata" | lifecycle.go:550-600 | IMPLEMENTED | | Updates DB and KV |
| spec-cp: NATS import/attachment subjects | "mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.request/confirm/download/complete" | (not found) | GAP | CODE→FIX | No NATS handlers for import lifecycle or attachment NATS subjects |
| spec-cp: NATS check-slug | "mclaude.users.{uslug}.hosts.{hslug}.projects.check-slug" | lifecycle.go:600-660 | IMPLEMENTED | | Checks uniqueness per user, returns suggestion |
| spec-cp: KV mclaude-hosts | "Shared KV bucket; created by ensureHostsKV" | projects.go:340-348 (ensureHostsKV) | IMPLEMENTED | | Created at startup |
| spec-cp: KV mclaude-sessions-{uslug} | "Per-user KV bucket for session state. Created by CP on user registration." | projects.go:310-320 (ensurePerUserSessionsKV) | IMPLEMENTED | | History=64 |
| spec-cp: KV mclaude-projects-{uslug} | "Per-user KV bucket for project state. Created by CP on user registration." | projects.go:300-310 (ensurePerUserProjectsKV) | IMPLEMENTED | | History=1 |
| spec-cp: Stream MCLAUDE_SESSIONS_{uslug} | "Per-user JetStream stream... LimitsPolicy, MaxAge: 30d, FileStorage" | projects.go:350-370 | IMPLEMENTED | | Correct config |
| spec-cp: KV key format | "Key format: hosts.{hslug}.projects.{pslug}" for projects KV | projects.go:275 | IMPLEMENTED | | "hosts." + hostSlug + ".projects." + proj.Slug |
| spec-cp: Postgres host_access table | "(host_id, user_id) composite PK" | db.go schema | IMPLEMENTED | | PRIMARY KEY (host_id, user_id) |
| spec-cp: Postgres agent_credentials | "(user_id, host_slug, project_slug) → nkey_public" | db.go schema | IMPLEMENTED | | UNIQUE (user_id, host_slug, project_slug) |
| spec-cp: Postgres attachments | "Attachment metadata for S3-stored binary data" | db.go schema | IMPLEMENTED | | Table created with all columns per spec |
| spec-cp: hosts table | "hosts are globally unique by slug — there is one row per host" | db.go schema | PARTIAL | CODE→FIX | Schema has UNIQUE(user_id, slug) not UNIQUE(slug). The spec says "one row per host" but the DB allows multiple rows per slug (one per user for cluster grants). GetHostBySlug queries for role='owner' LIMIT 1 to handle this. The model doesn't match the spec's "one row per host" claim. |
| spec-cp: hosts.owner_id | "Registering user (permanent owner). Set at registration, immutable." | db.go schema | PARTIAL | SPEC→FIX | Column is named `user_id` in schema, not `owner_id`. The spec says owner_id but code uses user_id. Both refer to the same concept. |
| spec-cp: Startup | "Connects to Postgres (fatal exit on failure)" | main.go:57-64 | IMPLEMENTED | | |
| spec-cp: Startup | "Loads the account signing key from NATS_ACCOUNT_SEED" | main.go:77-80 | IMPLEMENTED | | |
| spec-cp: Startup | "Also loads the operator seed and system account seed from OPERATOR_KEYS_PATH for JWT revocation support" | main.go (not present) | GAP | CODE→FIX | main.go does NOT load operator/sysAccount seeds. SetRevocationCredentials() exists but is never called. |
| spec-cp: Startup | "Caches the current account JWT in memory for revocation modifications" | main.go (not present) | GAP | CODE→FIX | Account JWT is not cached at startup |
| spec-cp: Startup | "Ensures the shared mclaude-hosts KV bucket exists" | projects.go:83-90 (StartProjectsSubscriber) | IMPLEMENTED | | |
| spec-cp: Startup | "Per-user KV buckets... created on user registration / first login, not at startup" | projects.go:360-380 (ensureUserResources) | PARTIAL | CODE→FIX | ensureUserResources exists but is only called from seedDev, not from the login/registration flow. No code path calls it on first user login. |
| spec-cp: Startup | "Subscribes to $SYS... Also subscribes to host lifecycle subjects, agent registration, and import/attachment NATS handlers" | main.go:100-115 | PARTIAL | CODE→FIX | Subscribes to $SYS, lifecycle, agent registration. But does NOT subscribe to import/attachment NATS handlers. |
| spec-cp: Startup | "Starts the GitLab token refresh goroutine (every 15 minutes)" | main.go:117-119 | IMPLEMENTED | | |
| spec-cp: Startup | "Optionally seeds a dev user, a default local machine host..." | main.go:121-130 | IMPLEMENTED | | |
| spec-cp: Auth | "User login: Login validates email and bcrypt password hash" | auth.go:100-115 | IMPLEMENTED | | |
| spec-cp: Auth | "Loads the calling user's hosts: owned + granted hosts" | auth.go:125-130, getUserHostSlugs | IMPLEMENTED | | GetHostAccessSlugs does owned + granted UNION |
| spec-cp: Auth | "Issues a user JWT... Permissions per ADR-0054 Full Permission Specifications" | nkeys.go:60-100 (UserSubjectPermissions) | IMPLEMENTED | | Explicit per-user-resource allow-lists |
| spec-cp: Auth | "JWT lifetime is JWT_EXPIRY_SECONDS (default 8h)" | auth.go:112, main.go:50-55 | IMPLEMENTED | | |
| spec-cp: Auth | "Challenge-response auth... lookup order: users.nkey_public → hosts.public_key → agent_credentials.nkey_public" | challenge.go:180-220 (issueJWTForNKey) | IMPLEMENTED | | Correct lookup order |
| spec-cp: Auth | "Agent JWT issuance... Host controllers no longer hold the account signing key" | nkeys.go:225-240 (IssueSessionAgentJWT) | IMPLEMENTED | | Only CP signs |
| spec-cp: Auth | "Host JWT issuance... zero JetStream access. TTL: 5 minutes" | nkeys.go:210-220 (IssueHostJWT), nkeys.go:130-140 (HostSubjectPermissions) | IMPLEMENTED | | 5-min TTL, no $JS.* subjects |
| spec-cp: Auth | "Per-user resource provisioning: On user registration (first login), CP creates per-user KV buckets" | projects.go:360-380 | PARTIAL | CODE→FIX | ensureUserResources function exists but is NOT called during login or user creation. Only called from seedDev path. |
| spec-cp: Auth middleware | "the spec-described access boundary enforcement (cross-user 403 when JWT sub doesn't match URL {uslug})" | auth.go:275-310 | IMPLEMENTED | | KNOWN-22 fixed — checks URL uslug vs JWT user slug, admins bypass |
| spec-cp: JWT Revocation | "CP loads the operator seed from OPERATOR_KEYS_PATH" | (not loaded) | GAP | CODE→FIX | main.go never reads OPERATOR_KEYS_PATH |
| spec-cp: JWT Revocation | "CP adds the target identity's NKey public key to the account JWT's Revocations map" | lifecycle.go:585-610 (revokeNKeyJWT) | GAP | CODE→FIX | revokeNKeyJWT is a stub — logs intent but does not perform actual revocation. The comment says "implemented in revocation.go" but no revocation.go file exists. |
| spec-cp: JWT Revocation | "CP publishes the updated account JWT to $SYS.REQ.CLAIMS.UPDATE" | (not implemented) | GAP | CODE→FIX | No $SYS.REQ.CLAIMS.UPDATE publish anywhere in codebase |
| spec-cp: hosts KV key format | "Key format: {hslug} (flat, no user prefix — hosts are globally unique per ADR-0054)" | sys_subscriber.go:78 | PARTIAL | CODE→FIX | Machine host CONNECT uses flat `hslug` key correctly. But seedDev in main.go uses `user.Slug + "." + localHostSlug` (prefixed with user slug) — inconsistent. |
| spec-cp: hosts KV value | "Spec says {slug, type, name, online, lastSeenAt}" | sys_subscriber.go:14-21 (HostKVState) | PARTIAL | SPEC→FIX | HostKVState struct includes a `Role` field not in the spec's KV value definition. Code writes Role, spec doesn't mention it. |
| spec-cp: NATS resolver | "resolver: nats (full NATS resolver — replaces MEMORY for JWT revocation support)" | (deployment config, not CP code) | N/A | | Not evaluable at code level; this is NATS server config |
| spec-cp: Project Creation Flow step 5 | "Writes the project to the per-user mclaude-projects-{uslug} KV bucket (key hosts.{hslug}.projects.{pslug})" | projects.go:260-280 (writeProjectKV) | IMPLEMENTED | | Correct hierarchical key format |
| spec-cp: Project Creation Flow step 6 | "Publishes to mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create" | projects.go:190-210 | IMPLEMENTED | | Uses subj.HostUserProjectsCreate |
| spec-cp: SCIM endpoints | "POST /scim/v2/Users... Not yet implemented" | scim.go | IMPLEMENTED | | SCIM endpoints are implemented (KNOWN-20 resolved) |
| spec-cp: Metrics | "GET /metrics on admin port" | server.go:115-120 | IMPLEMENTED | | |
| spec-cp: Error | "Postgres unavailable at startup: fatal exit" | main.go:57-64 | IMPLEMENTED | | |
| spec-cp: Error | "NATS connection failure: retries indefinitely" | main.go:89-95 | IMPLEMENTED | | MaxReconnects(-1) |
| spec-cp: Error | "Device-code expired (>10 min): returns 410 Gone" | hosts.go:320-325 | IMPLEMENTED | | |
| spec-cp: Error | "Device-code already redeemed: returns 409 Conflict" | hosts.go:330-333 | IMPLEMENTED | | |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-20 | INFRA | Package declaration, imports |
| main.go:30-55 | INFRA | Env var parsing, logger setup |
| main.go:82-95 | INFRA | NATS connection setup with retry |
| auth.go:1-60 | INFRA | Type declarations for LoginRequest/Response |
| auth.go:245-280 | INFRA | connectedProviderEntry type for /auth/me |
| auth.go:305-330 | INFRA | bearerToken, checkPassword, HashPassword helpers |
| context.go | INFRA | Context key helpers |
| version.go | INFRA | /version handler infrastructure |
| tracing.go | INFRA | OpenTelemetry tracer setup |
| metrics.go | INFRA | Prometheus metrics registry and handlers |
| nkeys.go:140-190 | INFRA | GenerateOperatorNKey, GenerateAccountNKey, GenerateUserNKey helpers |
| nkeys.go:245-310 | INFRA | IssueUserJWTLegacy, IssueHostJWTLegacy — backward compat |
| nkeys.go:315-340 | INFRA | DecodeUserJWT, VerifyNKeySignature helpers |
| nkeys.go:345-370 | UNSPEC'd | UserSubjectPermissionsLegacy, permContains, permHasPrefix — test/debug helpers in production code |
| db.go:310-330 | INFRA | OAuthConnection type and CRUD |
| db.go:430-500 | INFRA | GetExpiringGitLabConnections, UpdateTokenExpiry |
| hosts.go:75-98 | INFRA | deviceCodeStore, generateDeviceCode |
| challenge.go:195-210 | UNSPEC'd | cleanupExpiredChallenges — defined but never called (lazy cleanup mentioned in comment but no caller) |
| lifecycle.go:610-650 | INFRA | NATS reply helpers (replyNATSOK, replyNATSError, etc.) |
| init_keys.go | INFRA | Helm pre-install Job subcommand (documented in spec) |
| gen_leaf_creds.go | INFRA | Helm pre-install Job subcommand (documented in spec) |
| providers.go | INFRA | OAuth provider integration (documented in spec) |
| scim.go | INFRA | SCIM 2.0 endpoints (documented in spec) |
| device_auth.go | INFRA | CLI device-code auth flow (documented in spec) |
| project_http.go | INFRA | HTTP project CRUD handlers (documented in spec) |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-cp: challenge-response auth | "POST /api/auth/challenge + /api/auth/verify" | Not found | Not found | UNTESTED | No tests for challenge.go handlers |
| spec-cp: IssueUserJWT | "Issues scoped NATS user JWT with per-user permissions" | nkeys_test.go:229-305 | Not found | UNIT_ONLY | TestIssueUserJWT_ADR0054_ClaimsRoundTrip, TestIssueUserJWT_ADR0054_ScopedPermissions |
| spec-cp: UserSubjectPermissions | "Per-user scoped permissions" | nkeys_test.go:17-76 | Not found | UNIT_ONLY | TestUserSubjectPermissions_ADR0054, _NoHostSlugs, _CrossUserIsolation |
| spec-cp: IssueHostJWT | "Host JWT with zero JetStream, 5-min TTL" | nkeys_test.go:307-352 | Not found | UNIT_ONLY | TestIssueHostJWT_ADR0054 |
| spec-cp: SessionAgentSubjectPermissions | "Per-project scoped agent permissions" | nkeys_test.go:77-126 | Not found | UNIT_ONLY | TestSessionAgentSubjectPermissions_ADR0054, _CrossUserIsolation |
| spec-cp: IssueSessionAgentJWT | "Per-project scoped agent JWT" | nkeys_test.go:354-410 | Not found | UNIT_ONLY | TestIssueSessionAgentJWT_ADR0054 |
| spec-cp: HostSubjectPermissions | "Host-scoped subjects, zero JetStream" | nkeys_test.go:127-160 | Not found | UNIT_ONLY | TestHostSubjectPermissions_ADR0054, _ConstantSize |
| spec-cp: VerifyNKeySignature | "Ed25519 signature verification" | nkeys_test.go:415-455 | Not found | UNIT_ONLY | Valid sig, wrong key, tampered challenge |
| spec-cp: challenge-response auth | "POST /api/auth/challenge + /api/auth/verify" | Not found | Not found | UNTESTED | No tests for challenge.go handlers — critical auth flow |
| spec-cp: host lifecycle (manage.*) | "grant/revoke-access/deregister/revoke/rekey" | Not found | Not found | UNTESTED | No tests for lifecycle.go handlers — critical auth-adjacent flow |
| spec-cp: agent registration | "mclaude.hosts.{hslug}.api.agents.register" | Not found | Not found | UNTESTED | No tests for agent registration — critical for credential issuance |
| spec-cp: per-user resource provisioning | "ensureUserResources on user registration" | Not found | Not found | UNTESTED | Function exists but never called from login/registration flow |
| spec-cp: $SYS subscriber | "CONNECT/DISCONNECT for host liveness" | Not found | Not found | UNTESTED | |
| spec-cp: project creation NATS | "handleProjectCreate" | Not found | Not found | UNTESTED | |
| spec-cp: DB schema | "Migrate() applies schema" | db_test.go (compile check only) | Not found | UNIT_ONLY | db_test.go has compile-time checks for ensurePerUser* functions but no behavioral tests |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (no .agent/bugs/ directory found) | N/A | N/A | No bugs filed for this component |

### Summary

- Implemented: 55
- Gap: 10
- Partial: 7
- Infra: 25
- Unspec'd: 2
- Dead: 0
- Tested: 0
- Unit only: 8
- E2E only: 0
- Untested: 6
- Bugs fixed: 0
- Bugs open: 0

### Key Findings

**GAPS (CODE→FIX):**

1. **S3/attachment handlers missing entirely** — spec defines HTTP endpoints (`/api/attachments/upload-url`, `/api/attachments/{id}/confirm`, `/api/attachments/{id}`) and NATS handlers for import/attachment lifecycle. No code exists for any of these. DB schema exists (attachments table) but no route handlers.

2. **Import NATS handlers missing** — spec defines `import.request`, `import.confirm`, `import.download`, `import.complete` NATS subjects. No handlers subscribed.

3. **OPERATOR_KEYS_PATH not loaded at startup** — spec says CP loads operatorSeed and sysAccountSeed from OPERATOR_KEYS_PATH for JWT revocation. `SetRevocationCredentials()` exists but is never called in main.go.

4. **JWT revocation is a stub** — `revokeNKeyJWT()` logs intent but does not perform actual revocation. No `$SYS.REQ.CLAIMS.UPDATE` publish. The code comments reference a "revocation.go" file that doesn't exist. All manage.grant/revoke-access/deregister/revoke handlers call this stub.

5. **Per-user resources not created on login/registration** — spec says "On user registration (first login), CP creates per-user KV buckets and sessions stream." `ensureUserResources()` exists but is never called during login or user creation (only from seedDev). The projects KV is lazily created via `writeProjectKV`, but sessions KV and sessions stream are NOT.

6. **`/api/auth/verify` doesn't check for revoked hosts** — spec says FORBIDDEN error for revoked hosts; code issues JWT regardless.

7. **hosts KV key inconsistent in seedDev** — seedDev writes `user.Slug + "." + localHostSlug` (prefixed) but $SYS subscriber and spec require flat `hslug` key.

**GAPS (SPEC→FIX):**

8. **HostKVState includes Role field** — code writes `Role` to hosts KV but spec's KV value definition doesn't include it.

9. **hosts table schema has UNIQUE(user_id, slug) not UNIQUE(slug)** — spec says "one row per host, globally unique by slug" but schema allows multiple rows per slug (one per user for cluster grants). This is a fundamental model difference.

10. **Legacy NKey generation retained** — spec says CP "never generates NKey pairs" but code retains IssueUserJWTLegacy/IssueHostJWTLegacy for backward compat.

**UNTESTED (critical):**

- Challenge-response HTTP auth (challenge.go) — no tests at all for the primary authentication mechanism
- Host lifecycle handlers (lifecycle.go) — no tests for grant, revoke, deregister, rekey
- Agent registration — no tests for credential registration flow
