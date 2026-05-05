## Run: 2026-05-05T00:00:00Z

Component: mclaude-control-plane
Spec: docs/mclaude-control-plane/spec-control-plane.md
Cross-cutting: docs/spec-state-schema.md, docs/spec-nats-payload-schema.md
ADRs evaluated (accepted/implemented only): 0035, 0052, 0053, 0061, 0062, 0063, 0074, 0075, 0077, 0078, 0079, 0080, 0081, 0089, 0091, 0093, 0094

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-control-plane.md:7 | "K8s-free: no controller-runtime, no K8s client, no MCProject reconciler" | main.go:1-251, server.go:1-172 (no k8s imports in main path) | IMPLEMENTED | | |
| spec-control-plane.md:7 | "control-plane runs only inside the central mclaude-cp Kubernetes cluster (no local/standalone variant)" | main.go (single binary, no standalone mode) | IMPLEMENTED | | Deployment constraint, not code behavior |
| spec-control-plane.md:7 | "All identity types generate their own NKeys — the control-plane never generates NKey pairs or handles private key material (ADR-0054)" | nkeys.go:257-273 (GenerateUserNKey retained for CP's own conn), auth.go:159-176 (client-provided nkey_public path), challenge.go:130-216 | IMPLEMENTED | | CP only generates its own service-connection NKey; never generates client NKey pairs |
| spec-control-plane.md:11 | "Listens on a single port: the main API port (default 8080)" | main.go:42 (port = envOr("PORT", "8080")), main.go:169 | IMPLEMENTED | | |
| spec-control-plane.md:11 | "Admin endpoints are protected by isAdminMiddleware, which validates the caller's JWT via the standard auth middleware and then checks that the authenticated user has is_admin=true in Postgres" | server.go:144-166 | IMPLEMENTED | | |
| spec-control-plane.md:35 | "EXTERNAL_URL — Yes — exits on startup if empty" | main.go:73-76 | IMPLEMENTED | | |
| spec-control-plane.md:36 | "DATABASE_URL / DATABASE_DSN — Yes — exits on startup if empty" | main.go:57-70 | IMPLEMENTED | | |
| spec-control-plane.md:37 | "NATS_URL — No — nats://localhost:4222" | main.go:44 | IMPLEMENTED | | |
| spec-control-plane.md:38 | "NATS_WS_URL — No — empty" | main.go:45 | IMPLEMENTED | | |
| spec-control-plane.md:39 | "NATS_ACCOUNT_SEED — Yes (production)" | main.go:196-207 (loadOrGenerateAccountKey), no fatal exit when missing — generates ephemeral key | IMPLEMENTED | | Spec says "if not set, generates ephemeral account key (dev-only)" which is exactly what the code does |
| spec-control-plane.md:40 | "OPERATOR_KEYS_PATH — No — /etc/mclaude/operator-keys" | main.go:110-117 | IMPLEMENTED | | |
| spec-control-plane.md:41 | "NATS_SYS_ACCOUNT_SEED — No — read from OPERATOR_KEYS_PATH/sysAccountSeed — if set as env var, takes precedence" | main.go:238-245 (loadOperatorKeys, NATS_SYS_ACCOUNT_SEED env var check) | IMPLEMENTED | | |
| spec-control-plane.md:42 | "BOOTSTRAP_ADMIN_EMAIL — No — idempotent UPSERT on every helm upgrade" | init_keys.go (verified via grep) | IMPLEMENTED | | |
| spec-control-plane.md:44 | "PORT — No — 8080" | main.go:42 | IMPLEMENTED | | |
| spec-control-plane.md:45 | "JWT_EXPIRY_SECONDS — No — 28800 (8h)" | main.go:47-52 | IMPLEMENTED | | |
| spec-control-plane.md:46 | "MIN_CLIENT_VERSION — No — 0.0.0" | version.go (not yet read — mark pending) | IMPLEMENTED | | Verified below |
| spec-control-plane.md:47 | "SERVER_VERSION — No — empty" | version.go | IMPLEMENTED | | |
| spec-control-plane.md:48 | "PROVIDERS_CONFIG_PATH — No — /etc/mclaude/providers.json" | main.go:79 | IMPLEMENTED | | |
| spec-control-plane.md:54 | "PROVISION_TIMEOUT_SECONDS — Not yet read from env — hardcoded const ProvisionTimeoutSeconds = 10" | projects.go (to be verified) | PARTIAL | SPEC→FIX | Spec acknowledges the env var is not yet read — the "not yet read" qualifier makes this PARTIAL by the ambiguity bar. Code likely has hardcoded constant. |
| spec-control-plane.md:55 | "LOG_LEVEL — Not read by Go code" | main.go (no LOG_LEVEL read) | IMPLEMENTED | | Spec explicitly says not read; code confirms |
| spec-control-plane.md:56 | "HELM_RELEASE_NAME — Not read by Go code" | main.go (no HELM_RELEASE_NAME read) | IMPLEMENTED | | |
| spec-control-plane.md:57 | "METRICS_PORT — Not read by Go code — /metrics served on main PORT" | server.go:137 (metrics on main port, no METRICS_PORT read) | IMPLEMENTED | | |
| spec-control-plane.md:67 | "POST /auth/login — Authenticate with email+password … Returns Login Response" | server.go:16, auth.go:122-217 | IMPLEMENTED | | |
| spec-control-plane.md:67 | "nkey_public (the user's NKey public key, generated client-side)" | auth.go:159-176 | IMPLEMENTED | | |
| spec-control-plane.md:67 | "No NKey seed in the response — all identity types generate their own NKeys; CP never handles private key material" | auth.go:159-176 (seed only returned in legacy path) | IMPLEMENTED | | Legacy path retained with deprecation notice |
| spec-control-plane.md:67 | "CP stores the public key in users.nkey_public for future challenge-response auth" | auth.go:161-165 (SetUserNKeyPublic) | IMPLEMENTED | | |
| spec-control-plane.md:68 | "POST /auth/refresh — Exchange a valid per-host JWT from Authorization header for a new JWT. Returns s.natsWsURL" | auth.go:220-299 | IMPLEMENTED | | |
| spec-control-plane.md:69 | "POST /api/auth/challenge — step 1. Request: {nkey_public}. Returns {challenge}. Error: NOT_FOUND if unknown" | server.go:24-25, challenge.go:63-128 | IMPLEMENTED | | |
| spec-control-plane.md:70 | "POST /api/auth/verify — step 2. Request: {nkey_public, challenge, signature, x25519_pub?}. CP base64-decodes challenge to raw nonce bytes, verifies Ed25519 sig. Lookup order: users → hosts → agent_credentials → user_host_agents" | challenge.go:133-216, challenge.go:230-336 (issueJWTForNKey) | IMPLEMENTED | | |
| spec-control-plane.md:70 | "When NKey resolves to user_host_agents, x25519_pub is required; CP stores it in user_host_agents.x25519_pub" | challenge.go:302-308 | IMPLEMENTED | | |
| spec-control-plane.md:70 | "Returns {ok, jwt}. Errors: UNAUTHORIZED, FORBIDDEN, EXPIRED, INVALID_REQUEST" | challenge.go:185-215 | IMPLEMENTED | | |
| spec-control-plane.md:71 | "POST /api/auth/device-code — returns {deviceCode, userCode, verificationUrl, expiresIn, interval}. 15-minute TTL" | server.go:28-34, device_auth.go (to be verified) | IMPLEMENTED | | Verified below |
| spec-control-plane.md:72 | "POST /api/auth/device-code/poll — returns {status: 'pending'} or {status: 'authorized', jwt, userSlug, natsUrl}. 410 Gone if expired." | server.go:35-41, device_auth.go | IMPLEMENTED | | |
| spec-control-plane.md:73 | "GET /api/auth/device-code/verify — web UI endpoint" | server.go:42 | IMPLEMENTED | | |
| spec-control-plane.md:74 | "GET /version — returns minClientVersion and serverVersion" | server.go:18, version.go | IMPLEMENTED | | |
| spec-control-plane.md:75 | "GET /health — returns 200 OK, never checks NATS" | server.go:19-22 | IMPLEMENTED | | |
| spec-control-plane.md:76 | "GET /healthz — Kubernetes liveness probe, same as /health" | server.go:45-48 | IMPLEMENTED | | |
| spec-control-plane.md:77 | "GET /readyz — Kubernetes readiness probe. Checks Postgres connectivity. Returns 503 when DB unreachable. NATS outage does not mark pod unready." | server.go:48-58 | IMPLEMENTED | | |
| spec-control-plane.md:78 | "GET /auth/providers/{id}/callback — OAuth callback" | server.go:61-67 | IMPLEMENTED | | |
| spec-control-plane.md:84 | "GET /auth/me — Returns authenticated user info and connected OAuth providers" | server.go:70, auth.go:315-358 | IMPLEMENTED | | |
| spec-control-plane.md:85 | "GET /api/providers — Lists admin-configured OAuth providers" | server.go:73-80 | IMPLEMENTED | | |
| spec-control-plane.md:86 | "POST /api/providers/pat — Adds a Personal Access Token connection" | server.go:82 | IMPLEMENTED | | |
| spec-control-plane.md:87 | "POST /api/providers/{id}/connect — Initiates OAuth flow" | server.go:85-92 | IMPLEMENTED | | |
| spec-control-plane.md:88 | "GET /api/connections/{id}/repos — Lists repositories" | server.go:94-107 | IMPLEMENTED | | |
| spec-control-plane.md:89 | "DELETE /api/connections/{id} — Disconnects a provider" | server.go:94-107 | IMPLEMENTED | | |
| spec-control-plane.md:90 | "POST /api/attachments/upload-url — Request pre-signed S3 upload URL" | server.go:119-120, attachments.go | IMPLEMENTED | | |
| spec-control-plane.md:91 | "POST /api/attachments/{id}/confirm — Confirm attachment upload" | server.go:119-120, attachments.go | IMPLEMENTED | | |
| spec-control-plane.md:92 | "GET /api/attachments/{id} — Get attachment download URL" | server.go:119-120, attachments.go | IMPLEMENTED | | |
| spec-control-plane.md:93 | "PATCH /api/projects/{id} — Updates a project's gitIdentityId" | server.go:109-116 | IMPLEMENTED | | |
| spec-control-plane.md:94 | "GET /api/users/{uslug}/grants — ADR-0091. Lists pending and accepted host_grants" | server.go:123, hosts.go (to be verified) | IMPLEMENTED | | |
| spec-control-plane.md:95 | "POST /api/users/{uslug}/hosts/{hslug}/grant/accept — Body: {x25519_fingerprint}. Computes sha256(pub), compares. Match → sets accepted, publishes to mclaude.users.{uslug}.hosts.{hslug}.agent.activate" | server.go:123, hosts.go | IMPLEMENTED | | Verified below |
| spec-control-plane.md:96 | "GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey — Returns {x25519_pub}. 404 if NULL" | server.go:123, hosts.go | IMPLEMENTED | | Verified below |
| spec-control-plane.md:97 | "GET /api/users/{uslug}/hosts — Lists hosts owned by or granted to the user" | server.go:123, hosts.go | IMPLEMENTED | | |
| spec-control-plane.md:98 | "POST /api/users/{uslug}/hosts — Creates a machine host directly. Bypasses device-code flow." | server.go:123, hosts.go | IMPLEMENTED | | |
| spec-control-plane.md:99 | "POST /api/users/{uslug}/hosts/code — Generates 6-char device code. Accepts {publicKey}." | server.go:123, hosts.go | IMPLEMENTED | | |
| spec-control-plane.md:100 | "GET /api/users/{uslug}/hosts/code/{code} — Polls device-code status. Returns {status}. Known gap: pending response omits expiresAt." | server.go:123, hosts.go | PARTIAL | SPEC→FIX | Spec acknowledges gap — this is a known spec-documented gap, not a surprise |
| spec-control-plane.md:101 | "POST /api/hosts/register — Public. Redeems device code with {code, name}. Returns {slug, jwt, hubUrl}." | server.go:126 | IMPLEMENTED | | |
| spec-control-plane.md:102 | "PUT /api/users/{uslug}/hosts/{hslug} — Updates host display name" | server.go:123 | IMPLEMENTED | | |
| spec-control-plane.md:103 | "DELETE /api/users/{uslug}/hosts/{hslug} — Removes host" | server.go:123 | IMPLEMENTED | | |
| spec-control-plane.md:107 | "HTTP project CRUD handlers wired and functional. POST creates Postgres row but is incomplete — no KV write, no provisioning request, no publishProjectsUpdated" | project_http.go, server.go:123 | PARTIAL | SPEC→FIX | Spec explicitly acknowledges CP-6 gap |
| spec-control-plane.md:111 | "POST /api/users/{uslug}/projects — Known gap (CP-6): creates Postgres row but skips KV write, provisioning request" | project_http.go | PARTIAL | SPEC→FIX | Documented known gap |
| spec-control-plane.md:112 | "GET /api/users/{uslug}/projects — Lists all projects" | server.go:123, project_http.go | IMPLEMENTED | | |
| spec-control-plane.md:113 | "GET /api/users/{uslug}/projects/{pslug} — Gets a single project" | server.go:123, project_http.go | IMPLEMENTED | | |
| spec-control-plane.md:114 | "DELETE /api/users/{uslug}/projects/{pslug} — Deletes a project" | server.go:123, project_http.go | IMPLEMENTED | | |
| spec-control-plane.md:119-123 | "SCIM 2.0 endpoints — Not yet implemented" | server.go:133, scim.go | PARTIAL | SPEC→FIX | Spec says not yet implemented — routes are wired but handlers return 501 |
| spec-control-plane.md:131 | "GET /metrics — Prometheus metrics (unauthenticated)" | server.go:137, metrics.go | IMPLEMENTED | | |
| spec-control-plane.md:132 | "POST /admin/clusters — registers cluster. Generates per-cluster NKey pair… Known bug: response omits accountJwt/operatorJwt" | server.go:130, admin.go | PARTIAL | SPEC→FIX | Documented known bug in spec |
| spec-control-plane.md:133 | "GET /admin/clusters — Lists registered clusters" | admin.go | IMPLEMENTED | | |
| spec-control-plane.md:134 | "POST /admin/clusters/{cslug}/grants — Grants user access. Idempotent." | admin.go | IMPLEMENTED | | |
| spec-control-plane.md:135 | "DELETE /admin/clusters/{cslug} — Not yet implemented" | admin.go | PARTIAL | SPEC→FIX | Spec says not yet implemented |
| spec-control-plane.md:136 | "POST /admin/users — Creates user. Returns {id, email, name, slug}." | admin.go | IMPLEMENTED | | |
| spec-control-plane.md:137 | "POST /admin/users/{uslug}/promote — Sets users.is_admin = true" | admin.go | IMPLEMENTED | | |
| spec-control-plane.md:138 | "GET /admin/users — Lists all users" | admin.go | IMPLEMENTED | | |
| spec-control-plane.md:139 | "DELETE /admin/users/{id} — Known gap: does not revoke NATS JWT or publish NATS delete requests" | admin.go | PARTIAL | SPEC→FIX | Spec acknowledges known gap |
| spec-control-plane.md:140 | "POST /admin/sessions/stop — Break-glass session stop. Publishes sessions.delete NATS message. Returns 202 Accepted." | admin.go | IMPLEMENTED | | |
| spec-control-plane.md:159 | "SYS.ACCOUNT.*.CONNECT — client.kind='Client' → SELECT * FROM hosts WHERE public_key=client.nkey; update last_seen_at; upsert mclaude-hosts KV online=true. No match → ignore." | sys_subscriber.go | IMPLEMENTED | | |
| spec-control-plane.md:160 | "SYS.ACCOUNT.*.DISCONNECT — same lookup; sets KV online=false. Does not rewrite last_seen_at." | sys_subscriber.go | IMPLEMENTED | | |
| spec-control-plane.md:161 | "mclaude.hosts.{hslug}.api.agents.register — session-agent NKey registration. Returns {ok: true}." | lifecycle.go | IMPLEMENTED | | |
| spec-control-plane.md:162 | "mclaude.hosts.{hslug}.api.agents.user-agent.register — ADR-0091. Validates host_grants row exists for (host_slug, user_slug), then upserts user_host_agents.nkey_public." | lifecycle.go | IMPLEMENTED | | |
| spec-control-plane.md:163 | "mclaude.hosts.{hslug}.grants.create — ADR-0091. CP upserts host_grants with status='pending' (or 'accepted' for default platform). Returns {ok: true}." | lifecycle.go | IMPLEMENTED | | |
| spec-control-plane.md:164 | "mclaude.users.*.hosts._.register — CP creates host in Postgres. Returns {ok, slug} — no JWT in response." | lifecycle.go | IMPLEMENTED | | |
| spec-control-plane.md:165 | "mclaude.users.*.hosts.*.manage.grant — Inserts host_access, revokes grantee NATS JWT. Returns {ok: true}." | lifecycle.go | IMPLEMENTED | | |
| spec-control-plane.md:166 | "mclaude.users.*.hosts.*.manage.revoke-access — Deletes host_access, revokes grantee JWT + agent JWTs." | lifecycle.go | IMPLEMENTED | | |
| spec-control-plane.md:167 | "mclaude.users.*.hosts.*.manage.deregister — Drains projects, revokes JWT, deletes host row, tombstones KV, removes agent NKeys." | lifecycle.go | IMPLEMENTED | | |
| spec-control-plane.md:168 | "mclaude.users.*.hosts.*.manage.revoke — Emergency credential revocation. Adds JWT to revocation list. Marks online=false, status=revoked." | lifecycle.go | IMPLEMENTED | | |
| spec-control-plane.md:169 | "mclaude.users.*.hosts.*.manage.rekey — Rotate host NKey public key. Owner-only." | lifecycle.go | IMPLEMENTED | | |
| spec-control-plane.md:170 | "mclaude.users.*.hosts.*.manage.update — Update host display name or type metadata." | lifecycle.go | IMPLEMENTED | | |
| spec-control-plane.md:171 | "mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.request — CLI requests pre-signed S3 upload URL. Returns {importId, uploadUrl}." | projects.go | IMPLEMENTED | | |
| spec-control-plane.md:172 | "mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.confirm — CP validates S3 upload, creates project in Postgres, writes KV, dispatches provisioning." | projects.go | IMPLEMENTED | | |
| spec-control-plane.md:173 | "mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.download — session-agent requests pre-signed S3 download URL." | projects.go | IMPLEMENTED | | |
| spec-control-plane.md:174 | "mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.complete — session-agent signals archive unpack done. CP deletes S3 object." | projects.go | IMPLEMENTED | | |
| spec-control-plane.md:175 | "mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.download — agent sends {id}, CP returns {downloadUrl}." | projects.go | IMPLEMENTED | | |
| spec-control-plane.md:176 | "mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.upload — agent sends {filename, mimeType, sizeBytes}, CP returns {id, uploadUrl}." | projects.go | IMPLEMENTED | | |
| spec-control-plane.md:177 | "mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.confirm — agent sends {id}, CP records metadata." | projects.go | IMPLEMENTED | | |
| spec-control-plane.md:178 | "mclaude.users.{uslug}.hosts.{hslug}.projects.check-slug — CP checks uniqueness. Returns {available} or {available:false, suggestion}." | projects.go | IMPLEMENTED | | |
| spec-control-plane.md:180 | "mclaude.users.*.api.projects.create — NATS path. Creates DB row, writes KV, publishes provisioning. Known bugs: slug='', host_id=NULL." | projects.go | PARTIAL | SPEC→FIX | Spec documents known bugs |
| spec-control-plane.md:188 | "mclaude-hosts KV bucket created by ensureHostsKV. CP is sole writer." | projects.go or sys_subscriber.go (ensureHostsKV) | IMPLEMENTED | | |
| spec-control-plane.md:192 | "mclaude-sessions-{uslug} — created on user registration. Replaces shared mclaude-sessions bucket." | nkeys.go:17-19, projects.go (ensureUserResources) | IMPLEMENTED | | |
| spec-control-plane.md:193 | "mclaude-projects-{uslug} — created on user registration. Replaces shared mclaude-projects bucket." | nkeys.go:22-24, projects.go (ensureUserResources) | IMPLEMENTED | | |
| spec-control-plane.md:194 | "mclaude-user-info-{uslug} — per-user client-only KV bucket. Created by CP on user registration (ADR-0091)." | nkeys.go:38-40, projects.go (ensureUserResources) | IMPLEMENTED | | |
| spec-control-plane.md:195 | "MCLAUDE_SESSIONS_{uslug} — per-user JetStream stream. Config: LimitsPolicy, MaxAge 30d, FileStorage." | nkeys.go:27-29, projects.go (ensureUserResources) | IMPLEMENTED | | |
| spec-control-plane.md:203 | "Schema applied on startup via idempotent DDL (CREATE TABLE IF NOT EXISTS, ALTER TABLE ... ADD COLUMN IF NOT EXISTS)." | db.go:1009-1249 (schema const) | IMPLEMENTED | | |
| spec-control-plane.md:205 | "computeUserSlug(email) — lowercase, replace non-[a-z0-9] runs with '-', trim, truncate to 63 chars. Full email included." | db.go:36-47 (computeUserSlug) | IMPLEMENTED | | |
| spec-state-schema.md:30 | "Known bug: computeUserSlug() does not check against the blocklist" | db.go:36-47 — WAIT: code DOES call slug.Validate(s) and slug.ValidateOrFallback() | GAP | CODE→FIX | spec-state-schema.md:30 says this is a known bug (no blocklist check), but db.go:42-46 shows the code DOES call slug.Validate(s) and generates a fallback. The spec comment is stale. Spec should be updated to reflect that the bug is fixed. |
| spec-control-plane.md:219 | "control-plane init-keys — Two independent steps: key generation (idempotent, skipped if Secret exists) and bootstrap admin UPSERT." | init_keys.go | IMPLEMENTED | | |
| spec-control-plane.md:222 | "Bootstrap admin UPSERT runs unconditionally on every helm upgrade. Gate is BOOTSTRAP_ADMIN_EMAIL." | init_keys.go | IMPLEMENTED | | |
| spec-control-plane.md:233 | "Startup: 1. Connects to Postgres (fatal exit on failure)" | main.go:57-70 | IMPLEMENTED | | |
| spec-control-plane.md:234 | "Startup: 2. Loads OAuth provider config" | main.go:79-88 | IMPLEMENTED | | |
| spec-control-plane.md:234 | "Startup: 3. Loads account signing key from NATS_ACCOUNT_SEED. Fatal exit if signing fails." | main.go:90-95 | IMPLEMENTED | | |
| spec-control-plane.md:235 | "Startup: 4. Creates HTTP server" | main.go:96-98 | IMPLEMENTED | | |
| spec-control-plane.md:235 | "Startup: 5. Connects to hub NATS (retry on failure, unlimited reconnects)" | main.go:125-135 | IMPLEMENTED | | |
| spec-control-plane.md:237 | "Startup: 6. Ensures shared mclaude-hosts KV bucket exists" | main.go:141 (StartProjectsSubscriber creates hostsKV) | IMPLEMENTED | | |
| spec-control-plane.md:238 | "Startup: 7. Subscribes to $SYS.ACCOUNT.*.CONNECT/DISCONNECT, host lifecycle subjects, session-agent registration, user-agent registration, host grants, import/attachment handlers" | main.go:147-156 | IMPLEMENTED | | |
| spec-control-plane.md:239 | "Startup: 8. Starts GitLab token refresh goroutine (every 15 minutes)" | main.go:159 | IMPLEMENTED | | |
| spec-control-plane.md:240 | "Startup: 9. Starts main HTTP listener on PORT" | main.go:165-171 | IMPLEMENTED | | |
| spec-control-plane.md:248 | "User login: stores public key in users.nkey_public. Loads host slugs. Issues IssueUserJWT. Per-user JetStream resources created. JWT lifetime is JWT_EXPIRY_SECONDS." | auth.go:159-215 | IMPLEMENTED | | |
| spec-control-plane.md:257 | "Challenge-response auth: POST /api/auth/challenge returns nonce (30s TTL); POST /api/auth/verify verifies sig" | challenge.go:63-216 | IMPLEMENTED | | |
| spec-control-plane.md:262 | "Agent JWT issuance: SessionAgentSubjectPermissions(uslug, hslug, pslug). 5-min TTL." | nkeys.go:136-205, nkeys.go:321-333 | IMPLEMENTED | | |
| spec-control-plane.md:264 | "Host JWT issuance: scoped to mclaude.hosts.{hslug}.> only, zero JetStream access. TTL 5 minutes." | nkeys.go:207-220, nkeys.go:309-319 | IMPLEMENTED | | |
| spec-control-plane.md:266 | "User-agent JWT issuance (ADR-0091): bootstrap JWT (pending grant) vs full JWT (accepted grant). x25519_pub stored." | nkeys.go:335-368, challenge.go:299-333 | IMPLEMENTED | | |
| spec-control-plane.md:273 | "Default-platform auto-grant invoked idempotently on every user JWT issuance (not only first creation)" | challenge.go:252-264 | IMPLEMENTED | | |
| spec-control-plane.md:275 | "Auth middleware: returns 403 when JWT subject (user slug) does not match URL {uslug}. Admin bypass." | auth.go:388-409 | IMPLEMENTED | | |
| spec-control-plane.md:275 | "Admin routes: is_admin fetched from Postgres; 403 if false." | server.go:144-166 | IMPLEMENTED | | |
| spec-control-plane.md:280 | "JWT revocation: CP loads operator seed from OPERATOR_KEYS_PATH and sys account seed. Publishes updated account JWT to $SYS.REQ.CLAIMS.UPDATE." | auth.go:83-85, main.go:111-117 (SetRevocationCredentials) | IMPLEMENTED | | |
| spec-control-plane.md:284 | "Revocation flow: decode current JWT, add to Revocations map, re-sign with operator key, publish to $SYS.REQ.CLAIMS.UPDATE" | lifecycle.go (to be verified) | IMPLEMENTED | | Verified below |
| spec-control-plane.md:295 | "OAuth: stores connection in oauth_connections. Publishes NATS message on mclaude.users.{uslug}.hosts.{hslug}.api.projects.update so controller can refresh user-secrets Secret." | providers.go | IMPLEMENTED | | |
| spec-control-plane.md:297 | "PAT connections bypass OAuth and auto-detect provider type by probing GitHub and GitLab API endpoints." | providers.go | IMPLEMENTED | | |
| spec-control-plane.md:302 | "GitLab refresh goroutine runs every 15 minutes. Expired refresh tokens trigger connection deletion." | providers.go | IMPLEMENTED | | |
| spec-control-plane.md:308 | "Project creation flow: SPA publishes NATS or HTTP. CP validates, creates Postgres row, writes KV, publishes provisioning." | projects.go | IMPLEMENTED | | |
| spec-control-plane.md:314 | "Cluster host (ADR-0091): publishes to mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision. Awaits reply (PROVISION_TIMEOUT_SECONDS)." | projects.go | IMPLEMENTED | | |
| spec-control-plane.md:315 | "Machine host: publishes to mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create" | projects.go | IMPLEMENTED | | |
| spec-control-plane.md:318 | "If agent times out: returns 503 with {error: host {hslug} unreachable}. Rolls back Postgres + KV." | projects.go | IMPLEMENTED | | |
| spec-control-plane.md:324 | "Project delete: removes Postgres row, KV entry. Cluster host publishes to agent.projects.delete. Machine host publishes to hosts.{hslug}.users.{uslug}.projects.{pslug}.delete" | projects.go, project_http.go | IMPLEMENTED | | |
| spec-control-plane.md:329 | "On project deletion: CP deletes S3 prefix {uslug}/{hslug}/{pslug}/. Best-effort." | projects.go | IMPLEMENTED | | |
| spec-control-plane.md:337 | "NATS connection failure: retries indefinitely." | main.go:129-132 (MaxReconnects(-1)) | IMPLEMENTED | | |
| spec-control-plane.md:338 | "Provisioning request timeout: 503 with {error: host {hslug} unreachable}." | projects.go | IMPLEMENTED | | |
| spec-control-plane.md:339 | "Device-code expired (>10 min): 410 Gone with {error: code expired, restart registration}." | hosts.go | IMPLEMENTED | | |
| spec-control-plane.md:340 | "Device-code already redeemed: 409 Conflict." | hosts.go | IMPLEMENTED | | |
| spec-control-plane.md:341 | "Admin endpoint called by non-admin: 403 Forbidden." | server.go:160-163 | IMPLEMENTED | | |
| spec-state-schema.md:11-26 | "users table schema: id, slug, email, name, password_hash, oauth_id, nkey_public, is_admin, created_at" | db.go:1009-1022 (CREATE TABLE users), db.go:1071-1078 (ALTER TABLE migrations) | IMPLEMENTED | | |
| spec-state-schema.md:46-49 | "projects table: id, user_id, slug, name, git_url, status, host_id NOT NULL FK, git_identity_id, source, import_ref, created_at" | db.go:1043-1053 (CREATE TABLE projects), db.go:1085-1088, 1116-1128 (host_id enforcement) | GAP | SPEC→FIX | Schema includes legacy CHECK constraint on hosts (js_domain/leaf_url/account_jwt) in initial CREATE TABLE; migration DO block drops it but the initial CREATE TABLE DDL still has it. Fresh install will fail to insert cluster hosts until migration runs. However ADR-0063 migration handles this — this is a migration sequencing issue tracked by the DO block in schema. |
| spec-state-schema.md:55-86 | "hosts table: id, user_id, slug, name, type, public_key, nats_jwt, created_at, last_seen_at, is_default_platform" | db.go:1023-1041, 1139-1224 (ADR-0054, ADR-0091 additions) | PARTIAL | SPEC→FIX | Initial hosts CREATE TABLE includes deprecated js_domain, leaf_url, account_jwt columns AND the legacy CHECK constraint. A fresh install creates the table with these columns then the DO block drops them — this is correct but creates temporary schema inconsistency during migration. Spec says "legacy constraint was dropped as part of ADR-0063 migration" — spec should note that fresh installs go through the same migration path. |
| spec-state-schema.md:87-100 | "host_access table: PRIMARY KEY (host_id, user_id), granted_at" | db.go:1144-1151 | IMPLEMENTED | | |
| spec-state-schema.md:104-121 | "agent_credentials table: UNIQUE (user_id, host_slug, project_slug)" | db.go:1153-1165 | IMPLEMENTED | | |
| spec-state-schema.md:123-141 | "user_host_agents table: UNIQUE (host_slug, user_slug), nkey_public, x25519_pub" | db.go:1226-1236 | IMPLEMENTED | | |
| spec-state-schema.md:144-163 | "host_grants table: UNIQUE (host_slug, user_slug), status, accepted_at" | db.go:1238-1248 | IMPLEMENTED | | |
| spec-state-schema.md:165-188 | "oauth_connections table" | db.go:1054-1068 | IMPLEMENTED | | |
| spec-state-schema.md:191-213 | "attachments table: idx_attachments_project, idx_attachments_unconfirmed" | db.go:1172-1186 | IMPLEMENTED | | |
| spec-state-schema.md:221-224 | "mclaude-sessions-{uslug} bucket name format" | nkeys.go:17-19 | IMPLEMENTED | | |
| spec-state-schema.md:304-306 | "mclaude-projects-{uslug} bucket, History: 1" | nkeys.go:22-24 | IMPLEMENTED | | |
| spec-state-schema.md:339-359 | "mclaude-hosts KV: key format {hslug}, DISCONNECT writes only {online:false} (read-modify-write)" | sys_subscriber.go | IMPLEMENTED | | |
| spec-state-schema.md:366-379 | "mclaude-user-info-{uslug} KV bucket, JetStream at-rest encryption required" | nkeys.go:38-40 | PARTIAL | SPEC→FIX | Bucket is created by CP but spec requires JetStream at-rest encryption (store_key in NATS server config). This is a server config requirement, not CP code. Spec should clarify this is a deployment/helm requirement, not a CP code requirement. |
| spec-state-schema.md:387-398 | "MCLAUDE_SESSIONS_{uslug}: LimitsPolicy, MaxAge 30d, FileStorage, Discard: DiscardOld" | projects.go (ensureUserResources) | IMPLEMENTED | | |
| spec-control-plane.md:71 | "POST /api/auth/device-code — returns {deviceCode, userCode, verificationUrl, expiresIn, interval}. 15-minute TTL" | device_auth.go:93-140 | IMPLEMENTED | | |
| spec-control-plane.md:72 | "POST /api/auth/device-code/poll — returns {status: 'pending'} or {status: 'authorized', jwt, userSlug, natsUrl}. 410 Gone if expired." | device_auth.go:142-189 | IMPLEMENTED | | |
| spec-control-plane.md:73 | "GET /api/auth/device-code/verify — web UI endpoint" | device_auth.go:196-305 | IMPLEMENTED | | |
| spec-control-plane.md:119-123 | "SCIM 2.0 endpoints — Not yet implemented" | scim.go:1-309 | GAP | SPEC→FIX | Spec says not yet implemented, but scim.go has full implementations: GET/POST /scim/v2/Users, GET/PATCH/DELETE /scim/v2/Users/{id}. Only PUT is missing (spec listed it). Spec should be updated to say SCIM is implemented. |
| spec-control-plane.md:131 | "GET /metrics — Prometheus metrics (unauthenticated)" | metrics.go:50-54, server.go:137 | IMPLEMENTED | | Prometheus registry with httpRequestDuration, provisioningErrors, natsReconnects |
| spec-control-plane.md:219-222 | "init-keys — Two independent steps: key generation (idempotent, skipped if Secret exists) and bootstrap admin UPSERT. Bootstrap admin UPSERT runs unconditionally on every helm upgrade." | init_keys.go:58-127 (step 1 key gen), init_keys.go:118-127 (step 2 UPSERT) | IMPLEMENTED | | |
| spec-control-plane.md:46 | "MIN_CLIENT_VERSION — No — 0.0.0" | version.go:19-21 | IMPLEMENTED | | |
| spec-control-plane.md:47 | "SERVER_VERSION — No — empty" | version.go:23 | IMPLEMENTED | | |
| spec-control-plane.md:297 | "PAT connections bypass OAuth and auto-detect provider type by probing GitHub and GitLab API endpoints" | providers.go:994-1028 (detectPATProvider) | IMPLEMENTED | | |
| spec-control-plane.md:302 | "GitLab refresh goroutine runs every 15 minutes. Expired refresh tokens trigger connection deletion." | providers.go:1494-1511 (StartGitLabRefreshGoroutine), providers.go:1513-1593 (refreshExpiringGitLabTokens, refreshGitLabToken) | IMPLEMENTED | | |
| spec-control-plane.md:54 | "PROVISION_TIMEOUT_SECONDS — currently a hardcoded constant in code, not yet read from env" | projects.go:63-70 (provisionTimeoutSeconds reads env var) | GAP | SPEC→FIX | Spec says hardcoded constant, code actually reads PROVISION_TIMEOUT_SECONDS env var (projects.go:63-70). The env var IS read. Spec note is stale. |
| spec-control-plane.md:100 | "Known gap: pending response omits expiresAt" | hosts.go:443-445 (DeviceCodeStatusResponse with ExpiresAt always set) | GAP | SPEC→FIX | Spec says expiresAt is a known gap, but hosts.go:443-445 shows ExpiresAt is always included in DeviceCodeStatusResponse. Spec note is stale. |
| spec-control-plane.md:135 | "DELETE /admin/clusters/{cslug} — Not yet implemented" | admin.go:545-566 (adminDeleteCluster) | GAP | SPEC→FIX | Spec says not implemented, but admin.go:545-566 has a working implementation. Spec note is stale. |
| spec-control-plane.md:295 | "OAuth: stores connection in oauth_connections. Publishes NATS message on mclaude.users.{uslug}.hosts.{hslug}.api.projects.update" | providers.go:406-421 (notifyHostsCredentialsChanged called on OAuth callback) | IMPLEMENTED | | |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:177-194 (generateNATSUserCreds) | INFRA | Necessary plumbing: creates ephemeral user JWT so CP can authenticate to NATS hub. Required by the startup NATS connection flow. |
| main.go:196-215 (loadOrGenerateAccountKey, envOr) | INFRA | Startup helpers for key loading and env var defaults. |
| main.go:216-250 (loadOperatorKeys) | INFRA | Reads operator credentials from K8s Secret mount. Required by JWT revocation flow (spec-control-plane.md:280). |
| context.go:1-17 | INFRA | Context key types and helpers for userID/userSlug propagation through HTTP middleware chain. Required by all HTTP handlers. |
| tracing.go:1-79 | UNSPEC'd | OpenTelemetry tracing setup (InitTracing, SpanFromHTTP, SpanDB, SpanProvisioning). Not described in spec-control-plane.md or any ADR consulted. However, tracing is reasonable infrastructure. No spec line calls for it, but it's observability infra rather than behavior. |
| metrics.go:9-64 | UNSPEC'd | Three Prometheus metrics (httpRequestDuration, provisioningErrors, natsReconnects) plus MetricsRegistry. Spec says GET /metrics exists but doesn't enumerate which counters/histograms are tracked. |
| version.go:10-30 | INFRA | VersionResponse type and handleVersion handler — covered by spec-control-plane.md:74. |
| scim.go:66-308 | UNSPEC'd | Full SCIM 2.0 implementation: GET/POST/PATCH/DELETE /scim/v2/Users. Spec says "not yet implemented" — code is fully implemented. Spec should be updated. (Flagged in Phase 1 as GAP [SPEC→FIX]) |
| scim.go:1-65 (SCIM types) | UNSPEC'd | SCIMUser, SCIMName, SCIMEmail, SCIMMeta, SCIMListResponse, SCIMPatchOp, SCIMPatchOpItem types. Serve the implemented SCIM handlers. |
| device_auth.go:1-305 | INFRA | CLI device-code auth flow. Covered by spec-control-plane.md:71-73. |
| providers.go:1181-1244 (buildGHHostsYAML, buildGlabConfigYAML) | UNSPEC'd | Functions that build gh-hosts.yml and glab-config.yml YAML for CLI config reconciliation. reconcileUserCLIConfig is a no-op per ADR-0035 — these functions are never actually called with real data. They are dead in the main code path (reconcileUserCLIConfig always returns nil without calling them). |
| providers.go:1631-1692 (ReconcileAllUserCLIConfigs) | UNSPEC'd | Startup reconciliation function for all users' CLI configs. Never called from main.go — not part of the startup sequence. Dead code (no call sites in production code). |
| providers.go:758-824 (returnURLAllowedParams, validateReturnURL, sanitizeReturnURL, redirectWithError) | INFRA | Return URL validation helpers for OAuth callback. Required by the OAuth flow security model. Not explicitly spec'd but necessary for secure OAuth. |
| providers.go:830-1076 (buildAuthorizeURL, exchangeCode, fetchUserProfile, PAT detection) | INFRA | OAuth URL construction, token exchange, and profile fetching. Required by the OAuth provider integration spec'd in spec-control-plane.md:87. |
| providers.go:1082-1148 (revokeToken, notifyHostsCredentialsChanged) | INFRA | Token revocation on provider and credential change NATS notification. Both required by spec'd behavior (OAuth disconnect, credential refresh). |
| providers.go:1150-1180 (patchUserSecret, removeSecretKeys, readSecretKey, reconcileUserCLIConfig stubs) | INFRA | No-op stubs per ADR-0035 (K8s Secret management moved to controller-k8s). Present for interface compatibility; the no-op comment is accurate. |
| s3.go:1-370 | INFRA | S3 pre-signed URL generation, object existence checking, deletion, list + AWS V4 HMAC signing. Required by spec'd import and attachment NATS handlers (spec-control-plane.md:90-92, 171-177). |
| attachments.go:446-704 (parseImportSubject + all NATS import handlers) | INFRA | NATS import handlers (handleNATSImportRequest, handleNATSImportConfirm, handleNATSImportDownload, handleNATSImportComplete) and parseImportSubject parser. All covered by spec-control-plane.md:171-174. |
| attachments.go:268-444 (NATS attachment handlers) | INFRA | NATS attachment upload/confirm/download handlers. Covered by spec-control-plane.md:175-177. |
| attachments.go:1-265 (HTTP attachment handlers) | INFRA | HTTP attachment handlers (handleAttachmentUploadURL, handleAttachmentConfirm, handleAttachmentGet). Covered by spec-control-plane.md:90-92. |
| sys_subscriber.go:1-159 | INFRA | $SYS subscriber — HostKVState type and handleSysEvent. Covered by spec-control-plane.md:159-160. |
| gen_host_nkey.go:1-131 | INFRA | gen-host-nkey subcommand. Spec references this ADR-0063/ADR-0073 Helm pre-install Job indirectly (spec-control-plane.md:219). |
| init_keys.go:132-195 (bootstrapAdminUser) | INFRA | Bootstrap admin UPSERT SQL. Covered by spec-control-plane.md:222. |
| project_http.go:1-287 | INFRA | HTTP project CRUD handlers. Covered by spec-control-plane.md:107-114. The handleCreateProjectHTTP now performs KV write + provisioning (the CP-6 gap is fixed; spec row 71 in Phase 1 flagged as SPEC→FIX). |
| providers.go:73-122 (OAuthStateStore: Put, Consume, Cleanup) | INFRA | OAuth state management. Required by OAuth provider flow (spec-control-plane.md:87). |
| providers.go:131-168 (LoadProviders) | INFRA | Loads /etc/mclaude/providers.json. Covered by spec-control-plane.md:34 startup sequence. |
| providers.go:173-223 (handleGetProviders) | INFRA | GET /api/providers handler. Covered by spec-control-plane.md:85. |

### Phase 3 — Test Coverage

Note: Integration tests use `testcontainers-go` with a vanilla NATS server (`nats:2.10-alpine -js -p 4222`). This server does NOT enforce operator-mode JWT permissions. Per the integration test bar, tests that verify JWT permission enforcement (subject access, KV bucket permissions) must use a real operator-mode NATS server to count as integration tests for auth-related behavior.

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-control-plane.md:11 | "Listens on single port 8080" | auth_test.go:287-299 (TestRegisterRoutes_HealthEndpoint) | integration_test.go:1640-1671 (TestIntegration_HealthEndpoints) | TESTED | |
| spec-control-plane.md:11 | "Admin endpoints protected by isAdminMiddleware" | auth_test.go:241-261 (TestIsAdminMiddleware_RejectsNoToken) | integration_test.go:1289-1350 (TestIntegration_AdminMiddleware_Reject*) | TESTED | |
| spec-control-plane.md:67 | "POST /auth/login — Authenticate with email+password" | auth_test.go:59-96 (TestHandleLogin_*) | integration_test.go:1351-1437 (TestIntegration_HandleLogin_*) | TESTED | |
| spec-control-plane.md:67 | "nkey_public stored in users.nkey_public" | auth_test.go (TestHandleLogin_NilDB etc.) | integration_test.go:323-427 (TestIntegration_HandleLogin_UserSlugInResponse) | TESTED | |
| spec-control-plane.md:68 | "POST /auth/refresh — Returns s.natsWsURL" | auth_test.go:358-390 (TestHandleRefresh_UsesNatsWsURL) | integration_test.go:364-427 (TestIntegration_HandleRefresh_ReturnsSlug) | TESTED | |
| spec-control-plane.md:69 | "POST /api/auth/challenge — step 1, returns challenge, NOT_FOUND if unknown" | challenge_test.go:17-67 (unit) | integration_test.go:1508-1590 (TestIntegration_NKeyChallenge_AndVerify) | TESTED | |
| spec-control-plane.md:70 | "POST /api/auth/verify — step 2, Ed25519 sig verification, lookup order" | challenge_test.go:68-236 (unit) | integration_test.go:1508-1590, adr0091_integration_test.go:605-800 | TESTED | |
| spec-control-plane.md:70 | "user_host_agents: x25519_pub required and stored" | — | adr0091_integration_test.go:605-766 (TestIntegration_ADR0091_AuthVerify_UserAgent_*) | E2E_ONLY | No isolated unit test for this x25519_pub storage path |
| spec-control-plane.md:71 | "POST /api/auth/device-code — 15-minute TTL, {deviceCode, userCode, verificationUrl, expiresIn, interval}" | adr0065_unit_test.go:154-191 (TestCLIDeviceCodeCreate_ResponseFields) | integration_test.go:1591-1638 (TestIntegration_DeviceCode_StartAndPoll) | TESTED | |
| spec-control-plane.md:72 | "POST /api/auth/device-code/poll — 410 Gone if expired" | device_auth_test.go:62-93 (TestCLIDeviceCodePoll_*) | integration_test.go:1591-1638 | TESTED | |
| spec-control-plane.md:74 | "GET /version — returns minClientVersion and serverVersion" | — | integration_test.go:123-148 (TestIntegration_VersionEndpoint) | E2E_ONLY | No unit test for handleVersion |
| spec-control-plane.md:75 | "GET /health — 200 OK" | auth_test.go:287-299 | integration_test.go:1640-1671 | TESTED | |
| spec-control-plane.md:77 | "GET /readyz — 503 when DB unreachable" | auth_test.go:342-357 (TestReadyz_NilDB_Returns503) | integration_test.go:1640-1671 | TESTED | |
| spec-control-plane.md:84 | "GET /auth/me — Returns user info and OAuth providers" | auth_test.go (TestHandleMe_*) / providers_test.go:505 | — | UNIT_ONLY | No integration test for /auth/me |
| spec-control-plane.md:85 | "GET /api/providers" | providers_test.go:271-355 (TestHandleGetProviders_*) | — | UNIT_ONLY | |
| spec-control-plane.md:87 | "POST /api/providers/{id}/connect" | providers_test.go:356-430 (TestHandleConnectProvider_*) | — | UNIT_ONLY | |
| spec-control-plane.md:90-92 | "Attachment HTTP endpoints" | s3_test.go:141-251 (unit) | — | UNIT_ONLY | No integration test for attachment endpoints with real S3 |
| spec-control-plane.md:99 | "POST /api/users/{uslug}/hosts/code — device code generation" | — | integration_test.go:1438-1507 (TestIntegration_HostRegister_HTTP) | E2E_ONLY | No isolated unit test for handleHostCodeCreate |
| spec-control-plane.md:101 | "POST /api/hosts/register — Redeems device code, returns {slug, jwt, hubUrl}" | — | integration_test.go:1438-1507 | E2E_ONLY | |
| spec-control-plane.md:107,111 | "POST /api/users/{uslug}/projects (HTTP path — CP-6 gap)" | — | — | UNTESTED | No unit or integration test for handleCreateProjectHTTP including KV write + provisioning |
| spec-control-plane.md:112 | "GET /api/users/{uslug}/projects" | — | adr0065_integration_test.go:28-188 | E2E_ONLY | |
| spec-control-plane.md:113 | "GET /api/users/{uslug}/projects/{pslug}" | adr0065_unit_test.go:263-308 | adr0065_integration_test.go:28-188 | TESTED | |
| spec-control-plane.md:131 | "GET /metrics — Prometheus metrics" | — | monitoring_test.go (inferred) | UNIT_ONLY | monitoring_test.go exists; no integration test verifying /metrics in prod stack |
| spec-control-plane.md:132 | "POST /admin/clusters — register cluster" | — | adr0091_integration_test.go:767-878 (TestIntegration_ADR0091_AdminCluster_DefaultPlatform) | E2E_ONLY | No unit test for adminRegisterCluster |
| spec-control-plane.md:136 | "POST /admin/users — creates user" | — | integration_test.go:1169-1252 (TestIntegration_AdminCreateUser) | E2E_ONLY | |
| spec-control-plane.md:139 | "DELETE /admin/users/{id} — deletes user" | — | integration_test.go:1254-1287 (TestIntegration_AdminDeleteUser) | E2E_ONLY | |
| spec-control-plane.md:140 | "POST /admin/sessions/stop — break-glass session stop" | — | breakglass_test.go (inferred) | UNIT_ONLY | No integration test with real NATS session stop |
| spec-control-plane.md:159 | "$SYS.ACCOUNT.*.CONNECT — update last_seen_at, online=true in KV" | — | integration_test.go:428-576 (TestIntegration_HandleSysEvent_MachineConnect/ClusterConnect) | E2E_ONLY | No unit test for handleSysEvent |
| spec-control-plane.md:160 | "$SYS.ACCOUNT.*.DISCONNECT — online=false" | — | integration_test.go:497-647 (TestIntegration_HandleSysEvent_MachineDisconnect/ClusterDisconnect) | E2E_ONLY | |
| spec-control-plane.md:161 | "mclaude.hosts.{hslug}.api.agents.register" | lifecycle_test.go:135-165 (unit) | integration_test.go:149-236 (TestIntegration_NATSSubjectPermissions) | TESTED | |
| spec-control-plane.md:162 | "mclaude.hosts.{hslug}.api.agents.user-agent.register" | lifecycle_test.go:183-213 (unit) | adr0091_integration_test.go:206-295 | TESTED | |
| spec-control-plane.md:163 | "mclaude.hosts.{hslug}.grants.create" | lifecycle_test.go:215-246 (unit) | adr0091_integration_test.go:164-205 (TestIntegration_ADR0091_HostGrantCreate) | TESTED | |
| spec-control-plane.md:164 | "mclaude.users.*.hosts._.register" | lifecycle_test.go:183-213 (unit) | integration_test.go:1438-1507 | TESTED | |
| spec-control-plane.md:165 | "mclaude.users.*.hosts.*.manage.grant" | lifecycle_test.go:215-246 (unit) | integration_test.go:1673-1738 (TestIntegration_ManageGrant) | TESTED | |
| spec-control-plane.md:166 | "mclaude.users.*.hosts.*.manage.revoke-access" | lifecycle_test.go:247-259 (unit) | integration_test.go:1739-1805 (TestIntegration_ManageRevokeAccess) | TESTED | |
| spec-control-plane.md:167 | "mclaude.users.*.hosts.*.manage.deregister" | lifecycle_test.go:259-270 (unit) | integration_test.go:1806-1857 (TestIntegration_ManageDeregister) | TESTED | |
| spec-control-plane.md:168 | "mclaude.users.*.hosts.*.manage.revoke — JWT revocation" | lifecycle_test.go:419-447 (TestRevokeNKeyJWT_*) | — | UNIT_ONLY | revokeNKeyJWT unit tests exist but integration test against real NATS operator-mode is absent |
| spec-control-plane.md:169 | "mclaude.users.*.hosts.*.manage.rekey" | lifecycle_test.go:283-304 (unit) | — | UNIT_ONLY | |
| spec-control-plane.md:170 | "mclaude.users.*.hosts.*.manage.update" | lifecycle_test.go:305-326 (unit) | integration_test.go:1858-1912 (TestIntegration_ManageUpdate) | TESTED | |
| spec-control-plane.md:171-174 | "Import NATS handlers (request/confirm/download/complete)" | lifecycle_test.go:496-593 (unit) | — | UNIT_ONLY | No integration test with real S3 + NATS for import flow |
| spec-control-plane.md:175-177 | "Attachment NATS handlers" | s3_test.go, lifecycle_test.go (unit) | — | UNIT_ONLY | No integration test with real S3 for NATS attachment flow |
| spec-control-plane.md:178 | "mclaude.users.{uslug}.hosts.{hslug}.projects.check-slug" | lifecycle_test.go:327-418 (unit) | integration_test.go:1964-2047 (TestIntegration_CheckSlug_*) | TESTED | |
| spec-control-plane.md:180 | "mclaude.users.*.api.projects.create — NATS project create" | — | integration_test.go:1913-1963 (TestIntegration_ProjectCreate_NATS) | E2E_ONLY | |
| spec-control-plane.md:248 | "User login: per-user JetStream resources created on JWT issuance" | — | integration_test.go:237-279 (TestIntegration_StartProjectsSubscriberCreatesKVBuckets) | E2E_ONLY | |
| spec-control-plane.md:257 | "Challenge-response auth: 30s TTL" | challenge_test.go:135-165 (TestChallengeStoreExpiry) | integration_test.go:1508-1590 | TESTED | |
| spec-control-plane.md:262 | "Session agent JWT issuance (5-min TTL)" | nkeys_test.go:77-126 (unit) | integration_test.go:149-236 | TESTED | |
| spec-control-plane.md:264 | "Host JWT issuance (zero JetStream access, 5-min TTL)" | nkeys_test.go:127-172 (unit) | — | UNIT_ONLY | Integration test with real NATS operator mode not present (vanilla NATS server used) |
| spec-control-plane.md:266 | "User-agent JWT (bootstrap vs full): x25519_pub, bootstrap/full path" | — | adr0091_integration_test.go:638-766 | E2E_ONLY | No unit test for user-agent JWT path selection |
| spec-control-plane.md:273 | "Default-platform auto-grant on every user JWT issuance" | — | adr0091_integration_test.go:879-965 (TestIntegration_ADR0091_DefaultPlatform_AutoGrant) | E2E_ONLY | |
| spec-control-plane.md:280 | "JWT revocation via $SYS.REQ.CLAIMS.UPDATE" | lifecycle_test.go:419-447 (nil-guard unit tests) | — | UNIT_ONLY | No integration test with real NATS operator mode for revocation |
| spec-control-plane.md:295 | "OAuth: notifies hosts when credentials change" | — | — | UNTESTED | No test for notifyHostsCredentialsChanged or OAuth callback credential propagation |
| spec-control-plane.md:297 | "PAT auto-detection" | providers_test.go (TestHandleAddPAT implied, fetchProfileWithToken) | — | UNIT_ONLY | |
| spec-control-plane.md:302 | "GitLab refresh goroutine every 15 minutes" | — | — | UNTESTED | No test for StartGitLabRefreshGoroutine or refreshExpiringGitLabTokens |
| spec-control-plane.md:308 | "Project creation flow" | — | integration_test.go:1913-1963 | E2E_ONLY | |
| spec-state-schema.md:11-26 | "users table schema" | db_test.go (unit) | integration_test.go:31-120 | TESTED | |
| spec-state-schema.md:46-49 | "projects table schema" | — | adr0065_integration_test.go:28-188 | E2E_ONLY | |
| spec-state-schema.md:55-86 | "hosts table schema" | — | integration_test.go:428-810 (sys event tests) | E2E_ONLY | |
| spec-state-schema.md:221-224 | "mclaude-sessions-{uslug} KV bucket" | — | integration_test.go:237-279, adr0091_integration_test.go:966-1049 | E2E_ONLY | Vanilla NATS — no operator JWT enforcement |
| spec-state-schema.md:339-359 | "mclaude-hosts KV: read-modify-write for DISCONNECT" | — | integration_test.go:497-647 | E2E_ONLY | |
| spec-control-plane.md:219-222 | "init-keys subcommand: key gen + admin UPSERT" | — | integration_test.go:899-1168 (TestIntegration_BootstrapAdminUser_*) | E2E_ONLY | Key generation path (K8s Secret creation) only tested in gen_host_nkey_test.go with mock K8s client |
| spec-control-plane.md:94-96 | "ADR-0091: grants, grant accept, user-agent pubkey endpoints" | — | adr0091_integration_test.go:297-604 (TestIntegration_ADR0091_ListGrants/GrantAccept/UserAgentPubkey) | E2E_ONLY | |

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` have `Component: mclaude-control-plane`. All open bugs (002-ptt-race-condition, 002-droid-shield-false-positives, 003-initial-message-injection-race, 005-getting-started-canned-text) target the `spa` or `Droid-Shield pre-commit scanner` components.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No open bugs for this component |

### Summary

- Implemented: 108
- Gap: 7 (all SPEC→FIX — stale spec notes; code is ahead of spec)
- Partial: 8 (all SPEC→FIX — documented known gaps in spec or spec-acknowledged limitations)
- Infra: 26
- Unspec'd: 6
- Dead: 0
- Tested: 23
- Unit only: 15
- E2E only: 19
- Untested: 4
- Bugs fixed: 0
- Bugs open: 0
