## Run: 2026-05-01T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| ADR-0062:Decisions:Row1 | User slug derivation: `slugify(full-email)`: lowercase, replace all non-`[a-z0-9]` runs with `-`, trim leading/trailing `-`, truncate to 63 chars. | db.go:36-47 (`computeUserSlug`) | IMPLEMENTED | — | `computeUserSlug` calls `slug.Slugify(email)` which matches the ADR-0062 algorithm exactly. Reserved-word blocklist check and fallback also implemented. |
| ADR-0062:Decisions:Row2 | SQL backfill: `UPDATE users SET slug = lower(regexp_replace(email, '[^a-zA-Z0-9]+', '-', 'g'))` trimmed of leading/trailing `-`. Idempotent — runs on every migration. | db.go:896 (schema const) | IMPLEMENTED | — | SQL `UPDATE users SET slug = trim(both '-' from lower(regexp_replace(email, '[^a-zA-Z0-9]+', '-', 'g'))) WHERE slug = '';` matches exactly. Runs idempotently on every startup via `Migrate()`. |
| ADR-0062:IntegrationTests:Row1 | `computeUserSlug("dev@mclaude.local")` returns `dev-mclaude-local` | db.go:36-47 | IMPLEMENTED | — | `slug.Slugify("dev@mclaude.local")` → `dev-mclaude-local`. The `@` and `.` both become `-`. |
| ADR-0062:IntegrationTests:Row2 | `computeUserSlug("richard@rbc.com")` ≠ `computeUserSlug("richard@gmail.com")` — collision resistance | db.go:36-47 | IMPLEMENTED | — | Full email slugification produces distinct slugs: `richard-rbc-com` vs `richard-gmail-com`. |
| spec-CP:Postgres:UserSlugDerivation | `computeUserSlug(email)` slugifies the full email — lowercase, replace all non-`[a-z0-9]` runs with `-`, trim leading/trailing `-`, truncate to 63 chars | db.go:36-47 | IMPLEMENTED | — | Matches spec. |
| spec-CP:Postgres:UserSlugBlocklist | Known bug: `computeUserSlug()` in `db.go` does not check against the blocklist — a user with email `api@example.com` gets slug `api` | db.go:39-46 | IMPLEMENTED | SPEC→FIX | Spec documents this as a known bug, but code now validates against blocklist via `slug.Validate(s)` and generates fallback via `slug.ValidateOrFallback` on collision. The "known bug" note in spec-state-schema is stale — code is correct. |
| spec-CP:Deployment:EnvVars | `EXTERNAL_URL` required, exits on startup if empty | main.go:76-78 | IMPLEMENTED | — | `logger.Fatal().Msg(...)` if `EXTERNAL_URL == ""` |
| spec-CP:Deployment:EnvVars | `DATABASE_URL`/`DATABASE_DSN` required, exits on startup if empty | main.go:57-72 | IMPLEMENTED | — | `logger.Fatal().Msg("DATABASE_DSN required")` when both empty |
| spec-CP:Deployment:EnvVars | `NATS_URL` default `nats://localhost:4222` | main.go:58 | IMPLEMENTED | — | `envOr("NATS_URL", "nats://localhost:4222")` |
| spec-CP:Deployment:EnvVars | `NATS_WS_URL` default empty | main.go:59 | IMPLEMENTED | — | `envOr("NATS_WS_URL", "")` |
| spec-CP:Deployment:EnvVars | `NATS_ACCOUNT_SEED` loaded from env; ephemeral if not set | main.go (loadOrGenerateAccountKey) | IMPLEMENTED | — | Reads `NATS_ACCOUNT_SEED` env, falls back to ephemeral `nkeys.CreateAccount()` |
| spec-CP:Deployment:EnvVars | `OPERATOR_KEYS_PATH` default `/etc/mclaude/operator-keys` | main.go:103 | IMPLEMENTED | — | `envOr("OPERATOR_KEYS_PATH", "/etc/mclaude/operator-keys")` |
| spec-CP:Deployment:EnvVars | `NATS_SYS_ACCOUNT_SEED` env var takes precedence over file | main.go:loadOperatorKeys | IMPLEMENTED | — | Checks `os.Getenv("NATS_SYS_ACCOUNT_SEED")` before reading file |
| spec-CP:Deployment:EnvVars | `ADMIN_TOKEN` static bearer token for admin port | main.go:60, server.go:adminAuthMiddleware | IMPLEMENTED | — | Loaded from env and checked in `adminAuthMiddleware` |
| spec-CP:Deployment:EnvVars | `PORT` default `8080` | main.go:56 | IMPLEMENTED | — | `envOr("PORT", "8080")` |
| spec-CP:Deployment:EnvVars | `ADMIN_PORT` default `9091`, bound to `127.0.0.1` | main.go:57,156 | IMPLEMENTED | — | `envOr("ADMIN_PORT", "9091")` and `http.ListenAndServe("127.0.0.1:"+adminPort, ...)` |
| spec-CP:Deployment:EnvVars | `JWT_EXPIRY_SECONDS` default 28800 (8h) | main.go:62-66 | IMPLEMENTED | — | Defaults to `8 * time.Hour`, reads env var |
| spec-CP:Deployment:EnvVars | `DEV_SEED` creates dev user, default host, default project | main.go:seedDev | IMPLEMENTED | — | seedDev creates user `dev@mclaude.local`, default local host (via CreateUser), default project |
| spec-CP:Deployment:EnvVars | `S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY` | s3.go:loadS3Config | IMPLEMENTED | — | All four env vars read; returns nil if any missing |
| spec-CP:Deployment:EnvVars | `PROVIDERS_CONFIG_PATH` default `/etc/mclaude/providers.json` | main.go:79 | IMPLEMENTED | — | `envOr("PROVIDERS_CONFIG_PATH", "/etc/mclaude/providers.json")` |
| spec-CP:Deployment:EnvVars | `PROVISION_TIMEOUT_SECONDS` default 10 | projects.go:provisionTimeoutSeconds | IMPLEMENTED | — | Reads env, defaults to 10 |
| spec-CP:Deployment:EnvVars | `MIN_CLIENT_VERSION` default `0.0.0` | version.go:handleVersion | IMPLEMENTED | — | `os.Getenv("MIN_CLIENT_VERSION")`, defaults to `"0.0.0"` |
| spec-CP:Deployment:EnvVars | `SERVER_VERSION` | version.go:handleVersion | IMPLEMENTED | — | `os.Getenv("SERVER_VERSION")` |
| spec-CP:Deployment:EnvVars | `BOOTSTRAP_ADMIN_EMAIL` — init-keys Job creates admin user | init_keys.go:bootstrapAdminUser | IMPLEMENTED | — | Reads env, creates user with `is_admin=true`, idempotent |
| spec-CP:Deployment:EnvVars | `LOG_LEVEL` — **Not read by Go code** | — | IMPLEMENTED | — | Correctly not consumed by binary (spec documents this) |
| spec-CP:HTTP:Public | `POST /auth/login` — email+password, NKey public key, returns LoginResponse | auth.go:handleLogin | IMPLEMENTED | — | Full implementation: validates credentials, stores nkey_public, issues scoped JWT, returns hosts/projects |
| spec-CP:HTTP:Public | `POST /auth/refresh` — exchange valid JWT for new one | auth.go:handleRefresh | IMPLEMENTED | — | Decodes old JWT, looks up user, issues new JWT with current host slugs |
| spec-CP:HTTP:Public | `POST /api/auth/challenge` — NKey challenge step 1 | challenge.go:handleAuthChallenge | IMPLEMENTED | — | Verifies NKey is registered, generates 32-byte random nonce, 30s TTL, single-use |
| spec-CP:HTTP:Public | `POST /api/auth/verify` — NKey challenge step 2 | challenge.go:handleAuthVerify | IMPLEMENTED | — | Verifies signature, resolves identity type (user→host→agent), issues scoped JWT |
| spec-CP:HTTP:Public | `POST /api/auth/device-code` — CLI device-code flow initiation | device_auth.go:handleCLIDeviceCodeCreate | IMPLEMENTED | — | Returns deviceCode, userCode, verificationUrl, expiresIn (15 min), interval (5s) |
| spec-CP:HTTP:Public | `POST /api/auth/device-code/poll` — CLI polls for completion | device_auth.go:handleCLIDeviceCodePoll | IMPLEMENTED | — | Returns pending or authorized+jwt+userSlug. 410 Gone on expiry. |
| spec-CP:HTTP:Public | `GET /api/auth/device-code/verify` — web UI verification page | device_auth.go:handleCLIDeviceCodeVerify | IMPLEMENTED | — | Serves HTML form for GET, processes auth for POST |
| spec-CP:HTTP:Public | `GET /version` — returns minClientVersion and serverVersion | version.go:handleVersion | IMPLEMENTED | — | |
| spec-CP:HTTP:Public | `GET /health` — returns 200 (never checks NATS) | server.go:RegisterRoutes | IMPLEMENTED | — | Simple `w.WriteHeader(http.StatusOK)` |
| spec-CP:HTTP:Public | `GET /healthz` — Kubernetes liveness (never checks NATS) | server.go:RegisterRoutes | IMPLEMENTED | — | Simple 200 OK |
| spec-CP:HTTP:Public | `GET /readyz` — Kubernetes readiness, checks Postgres | server.go:RegisterRoutes | IMPLEMENTED | — | Pings DB, returns 503 if unavailable |
| spec-CP:HTTP:Protected | `GET /auth/me` — returns user info + connected providers | auth.go:handleMe | IMPLEMENTED | — | Returns userId, email, name, connectedProviders array |
| spec-CP:HTTP:Protected | `GET /api/providers` — lists admin OAuth providers | providers.go:handleGetProviders | IMPLEMENTED | — | Returns array of providers from Helm config |
| spec-CP:HTTP:Protected | `POST /api/providers/pat` — adds PAT connection | providers.go:handleAddPAT | IMPLEMENTED | — | Auto-detects GitHub/GitLab, stores connection |
| spec-CP:HTTP:Protected | `POST /api/providers/{id}/connect` — initiates OAuth flow | providers.go:handleConnectProvider | IMPLEMENTED | — | Generates state token, builds authorize URL |
| spec-CP:HTTP:Protected | `GET /api/connections/{id}/repos` — lists repos | providers.go:handleGetConnectionRepos | IMPLEMENTED | — | Supports GitHub and GitLab repo listing with search |
| spec-CP:HTTP:Protected | `DELETE /api/connections/{id}` — disconnects provider | providers.go:handleDeleteConnection | IMPLEMENTED | — | Revokes token, removes secrets, deletes DB row, notifies controllers |
| spec-CP:HTTP:Protected | `POST /api/attachments/upload-url` — request pre-signed upload URL | attachments.go:handleAttachmentUploadURL | IMPLEMENTED | — | Validates ownership, generates S3 key, creates attachment row, 5-min TTL |
| spec-CP:HTTP:Protected | `POST /api/attachments/{id}/confirm` — confirm upload | attachments.go:handleAttachmentConfirm | IMPLEMENTED | — | Verifies S3 object exists, sets confirmed=true |
| spec-CP:HTTP:Protected | `GET /api/attachments/{id}` — download URL | attachments.go:handleAttachmentGet | IMPLEMENTED | — | Validates ownership+confirmed, returns pre-signed download URL |
| spec-CP:HTTP:Protected | `PATCH /api/projects/{id}` — updates gitIdentityId | providers.go:handlePatchProject | IMPLEMENTED | — | Validates connection hostname match, updates DB, writes KV, notifies controller |
| spec-CP:HTTP:Protected | `GET /api/users/{uslug}/hosts` — lists hosts | hosts.go:handleListHosts | IMPLEMENTED | — | Lists hosts for user ordered by created_at |
| spec-CP:HTTP:Protected | `POST /api/users/{uslug}/hosts` — creates machine host | hosts.go:handleCreateHost | IMPLEMENTED | — | Creates host row, issues JWT (legacy or NKey-based) |
| spec-CP:HTTP:Protected | `POST /api/users/{uslug}/hosts/code` — generates device code for BYOH | hosts.go:handleHostCodeCreate | IMPLEMENTED | — | 6-char hex code, 10-min TTL, stores publicKey |
| spec-CP:HTTP:Protected | `GET /api/users/{uslug}/hosts/code/{code}` — polls device code status | hosts.go:handleHostCodeStatus | IMPLEMENTED | — | Returns pending/completed with expiresAt |
| spec-CP:HTTP:Protected | `POST /api/hosts/register` — redeems device code | hosts.go:handleHostRegister | IMPLEMENTED | — | Creates host row, mints JWT, marks code completed |
| spec-CP:HTTP:Protected | `PUT /api/users/{uslug}/hosts/{hslug}` — updates host name | hosts.go:handleUpdateHost | IMPLEMENTED | — | Simple name update |
| spec-CP:HTTP:Protected | `DELETE /api/users/{uslug}/hosts/{hslug}` — removes host | hosts.go:handleDeleteHost | IMPLEMENTED | — | Publishes delete notifications for each project + S3 cleanup, then deletes |
| spec-CP:HTTP:ProjectCRUD | `POST /api/users/{uslug}/projects` — creates project | project_http.go:handleCreateProjectHTTP | IMPLEMENTED | — | Creates Postgres row, writes KV, sends provisioning request, broadcasts updated |
| spec-CP:HTTP:ProjectCRUD | `GET /api/users/{uslug}/projects` — lists projects | project_http.go:handleListProjectsHTTP | IMPLEMENTED | — | |
| spec-CP:HTTP:ProjectCRUD | `GET /api/users/{uslug}/projects/{pslug}` — gets single project | project_http.go:handleGetProjectHTTP | IMPLEMENTED | — | |
| spec-CP:HTTP:ProjectCRUD | `DELETE /api/users/{uslug}/projects/{pslug}` — deletes project | project_http.go:handleDeleteProjectHTTP | IMPLEMENTED | — | Publishes delete notification, S3 cleanup, broadcasts updated |
| spec-CP:HTTP:SCIM | `POST /scim/v2/Users` — IdP provisions user | scim.go:scimCreateUser | IMPLEMENTED | — | Creates user via SCIM 2.0 protocol |
| spec-CP:HTTP:SCIM | `GET /scim/v2/Users` — IdP syncs user list | scim.go:scimListUsers | IMPLEMENTED | — | Supports filter=userName eq "email" |
| spec-CP:HTTP:SCIM | `GET /scim/v2/Users/{id}` | scim.go:scimGetUser | IMPLEMENTED | — | |
| spec-CP:HTTP:SCIM | `PATCH /scim/v2/Users/{id}` | scim.go:scimPatchUser | IMPLEMENTED | — | Supports replace on active and displayName |
| spec-CP:HTTP:SCIM | `DELETE /scim/v2/Users/{id}` | scim.go:scimDeleteUser | IMPLEMENTED | — | |
| spec-CP:HTTP:AdminPort | `GET /metrics` — Prometheus metrics | server.go:handleMetrics | IMPLEMENTED | — | Served on admin port via MetricsRegistry |
| spec-CP:HTTP:Admin | `POST /admin/clusters` — register cluster | admin.go:adminRegisterCluster | IMPLEMENTED | — | Creates host row with cluster type, generates NKey pair, issues JWT |
| spec-CP:HTTP:Admin | `GET /admin/clusters` — list clusters | admin.go:adminListClusters | IMPLEMENTED | — | DISTINCT by slug from hosts table |
| spec-CP:HTTP:Admin | `POST /admin/clusters/{cslug}/grants` — grant user access | admin.go:adminGrantCluster | IMPLEMENTED | — | Looks up user by slug (not email), creates host row for user |
| spec-CP:HTTP:Admin | `DELETE /admin/clusters/{cslug}` | admin.go:adminDeleteCluster | IMPLEMENTED | — | Deletes all host rows with matching slug+type=cluster |
| spec-CP:HTTP:Admin | `POST /admin/users` — creates user | admin.go:adminCreateUser | IMPLEMENTED | — | Supports optional isAdmin field |
| spec-CP:HTTP:Admin | `POST /admin/users/{uslug}/promote` — set is_admin=true | admin.go:adminPromoteUser | IMPLEMENTED | — | Looks up by slug, sets is_admin=true |
| spec-CP:HTTP:Admin | `GET /admin/users` — lists users | admin.go:adminListUsers | IMPLEMENTED | — | |
| spec-CP:HTTP:Admin | `DELETE /admin/users/{id}` — deletes user | admin.go:adminDeleteUser | IMPLEMENTED | — | Cascades to hosts/projects; publishes delete notifications; NATS JWT not revoked (known gap documented) |
| spec-CP:HTTP:Admin | `POST /admin/sessions/stop` — break-glass stop | admin.go:adminStopSession | IMPLEMENTED | — | Publishes to sessions.delete subject (fixed from the non-functional sessions.stop) |
| spec-CP:NATS:Subscribes | `$SYS.ACCOUNT.{accountKey}.CONNECT` — host presence | sys_subscriber.go:handleSysEvent | IMPLEMENTED | — | Machine: updates last_seen_at + KV online=true. Cluster (Leafnode): updates all rows with slug, KV online=true. |
| spec-CP:NATS:Subscribes | `$SYS.ACCOUNT.{accountKey}.DISCONNECT` — host offline | sys_subscriber.go:handleSysEvent | IMPLEMENTED | — | Sets KV online=false via read-modify-write. Does NOT rewrite last_seen_at (per spec). |
| spec-CP:NATS:Subscribes | `mclaude.hosts.{hslug}.api.agents.register` — agent registration | lifecycle.go:handleAgentRegister | IMPLEMENTED | — | Validates host access + project ownership + host assignment, upserts agent_credentials |
| spec-CP:NATS:Subscribes | `mclaude.users.*.hosts._.register` — host registration via NATS | lifecycle.go:handleNATSHostRegister | IMPLEMENTED | — | Creates host row, returns {ok, slug}, no JWT |
| spec-CP:NATS:Subscribes | `mclaude.users.*.hosts.*.manage.grant` — host access grant | lifecycle.go:handleManageGrant | IMPLEMENTED | — | Validates ownership, inserts host_access, revokes grantee's JWT |
| spec-CP:NATS:Subscribes | `mclaude.users.*.hosts.*.manage.revoke-access` — access revocation | lifecycle.go:handleManageRevokeAccess | IMPLEMENTED | — | Validates ownership, deletes host_access, revokes grantee + agent JWTs |
| spec-CP:NATS:Subscribes | `mclaude.users.*.hosts.*.manage.deregister` — host deregistration | lifecycle.go:handleManageDeregister | IMPLEMENTED | — | Drains projects (delete + S3), revokes host + agents, deletes DB + KV |
| spec-CP:NATS:Subscribes | `mclaude.users.*.hosts.*.manage.revoke` — emergency revocation | lifecycle.go:handleManageRevoke | IMPLEMENTED | — | Revokes host + agent JWTs, sets KV online=false |
| spec-CP:NATS:Subscribes | `mclaude.users.*.hosts.*.manage.rekey` — rotate host NKey | lifecycle.go:handleManageRekey | IMPLEMENTED | — | Owner-only, revokes old JWT, stores new public key |
| spec-CP:NATS:Subscribes | `mclaude.users.*.hosts.*.manage.update` — update host metadata | lifecycle.go:handleManageUpdate | IMPLEMENTED | — | Updates name in DB and KV |
| spec-CP:NATS:Subscribes | `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.request` | attachments.go:handleNATSImportRequest | IMPLEMENTED | — | Generates import ID + S3 key, presigns upload URL |
| spec-CP:NATS:Subscribes | `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.confirm` | attachments.go:handleNATSImportConfirm | IMPLEMENTED | — | Verifies S3, creates project with source=import, dispatches provisioning |
| spec-CP:NATS:Subscribes | `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.download` | attachments.go:handleNATSImportDownload | IMPLEMENTED | — | Returns pre-signed download URL for import archive |
| spec-CP:NATS:Subscribes | `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.complete` | attachments.go:handleNATSImportComplete | IMPLEMENTED | — | Deletes S3 object, clears import_ref |
| spec-CP:NATS:Subscribes | `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.upload` | attachments.go:handleNATSAttachmentUpload | IMPLEMENTED | — | |
| spec-CP:NATS:Subscribes | `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.confirm` | attachments.go:handleNATSAttachmentConfirm | IMPLEMENTED | — | |
| spec-CP:NATS:Subscribes | `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.download` | attachments.go:handleNATSAttachmentDownload | IMPLEMENTED | — | |
| spec-CP:NATS:Subscribes | `mclaude.users.{uslug}.hosts.{hslug}.projects.check-slug` — slug availability | lifecycle.go:handleCheckSlug | IMPLEMENTED | — | Checks per-user uniqueness, suggests alternatives |
| spec-CP:NATS:Publishes | `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create` — fan-out provisioning | projects.go:handleProjectCreate, project_http.go | IMPLEMENTED | — | Published using `subj.HostUserProjectsCreate()` typed helper |
| spec-CP:NATS:Publishes | `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.delete` — project teardown | projects.go:publishProjectsDeleteToHost | IMPLEMENTED | — | Published using `subj.HostUserProjectsDelete()` typed helper |
| spec-CP:NATS:KV | `mclaude-hosts` — shared KV bucket, created at startup | projects.go:ensureHostsKV | IMPLEMENTED | — | Created by `StartProjectsSubscriber` on startup; History=1 |
| spec-CP:NATS:KV | `mclaude-sessions-{uslug}` — per-user KV, created on registration | projects.go:ensurePerUserSessionsKV | IMPLEMENTED | — | Created by `ensureUserResources`; History=64 |
| spec-CP:NATS:KV | `mclaude-projects-{uslug}` — per-user KV, created on registration | projects.go:ensurePerUserProjectsKV | IMPLEMENTED | — | Created by `ensureUserResources`; History=1 |
| spec-CP:NATS:KV | `MCLAUDE_SESSIONS_{uslug}` — per-user stream, created on registration | projects.go:ensurePerUserSessionsStream | IMPLEMENTED | — | LimitsPolicy, MaxAge=30d, FileStorage, DiscardOld, subjects=`mclaude.users.{uslug}.hosts.*.projects.*.sessions.>` |
| spec-CP:InternalBehavior:Startup | 1. Connects to Postgres (fatal exit on failure) + migrate | main.go:68-74 | IMPLEMENTED | — | Fatal on connect fail, calls `db.Migrate()` |
| spec-CP:InternalBehavior:Startup | 2. Loads OAuth provider config | main.go:79-87 | IMPLEMENTED | — | Reads providers.json, warns on failure |
| spec-CP:InternalBehavior:Startup | 3. Loads account signing key | main.go:89-91 | IMPLEMENTED | — | `loadOrGenerateAccountKey()` |
| spec-CP:InternalBehavior:Startup | 4. Creates HTTP server with all route handlers | main.go:149-150 | IMPLEMENTED | — | `srv.RegisterRoutes(mux)` |
| spec-CP:InternalBehavior:Startup | 5. Connects to hub NATS (retry, unlimited reconnects) | main.go:112-122 | IMPLEMENTED | — | `RetryOnFailedConnect(true)`, `MaxReconnects(-1)` |
| spec-CP:InternalBehavior:Startup | 6. Ensures shared mclaude-hosts KV exists | projects.go:StartProjectsSubscriber | IMPLEMENTED | — | `ensureHostsKV(js)` before subscribers start |
| spec-CP:InternalBehavior:Startup | 7. Subscribes to $SYS + lifecycle subjects | main.go:127-140 | IMPLEMENTED | — | `StartSysSubscriber`, `StartLifecycleSubscribers` |
| spec-CP:InternalBehavior:Startup | 8. Starts GitLab token refresh goroutine | main.go:143 | IMPLEMENTED | — | `StartGitLabRefreshGoroutine` (every 15 min) |
| spec-CP:InternalBehavior:Startup | 9. Seeds dev user when DEV_SEED=true | main.go:146-149 | IMPLEMENTED | — | seedDev creates user, host, project, KV entries, provisioning request |
| spec-CP:InternalBehavior:Startup | 10. Starts main + admin HTTP listeners | main.go:152-161 | IMPLEMENTED | — | Main on `:PORT`, admin on `127.0.0.1:ADMIN_PORT` |
| spec-CP:Auth:UserJWT | User JWT with per-user-resource scoped permissions (ADR-0054) | nkeys.go:UserSubjectPermissions | IMPLEMENTED | — | Explicit per-user KV buckets, per-host entries, no `$JS.API.>` wildcards |
| spec-CP:Auth:HostJWT | Host JWT scoped to `mclaude.hosts.{hslug}.>`, zero JetStream, 5-min TTL | nkeys.go:HostSubjectPermissions, IssueHostJWT | IMPLEMENTED | — | `const hostTTLSecs = 5 * 60`, only core pub/sub + `$SYS` |
| spec-CP:Auth:AgentJWT | Session-agent JWT per-project scoped, 5-min TTL | nkeys.go:SessionAgentSubjectPermissions, IssueSessionAgentJWT | IMPLEMENTED | — | Per-project KV write permissions, quota publish |
| spec-CP:Auth:ChallengeResponse | Lookup order: users.nkey_public → hosts.public_key → agent_credentials.nkey_public | challenge.go:issueJWTForNKey | IMPLEMENTED | — | Sequential lookup with first-match-wins |
| spec-CP:Auth:Revocation | revokeNKeyJWT adds NKey to account JWT revocation list via $SYS.REQ.CLAIMS.UPDATE | lifecycle.go:revokeNKeyJWT | IMPLEMENTED | — | Full implementation: decode account JWT, add revocation, re-sign with operator key, publish via sysAccount connection |
| spec-CP:Auth:Middleware | Auth middleware extracts user UUID, access boundary enforcement | auth.go:authMiddleware | IMPLEMENTED | — | Extracts userID from JWT claims.Name, checks URL uslug matches user slug (admins bypass) |
| spec-CP:Postgres:Schema | hosts table with type CHECK, role CHECK | db.go:schema | IMPLEMENTED | — | `CHECK (type IN ('machine', 'cluster'))`, `CHECK (role IN ('owner', 'user'))` |
| spec-CP:Postgres:Schema | host_access table — composite PK (host_id, user_id) | db.go:schema | IMPLEMENTED | — | `PRIMARY KEY (host_id, user_id)` |
| spec-CP:Postgres:Schema | agent_credentials — UNIQUE(user_id, host_slug, project_slug) | db.go:schema | IMPLEMENTED | — | Both UNIQUE constraint and nkey_public UNIQUE |
| spec-CP:Postgres:Schema | attachments table with indexes | db.go:schema | IMPLEMENTED | — | idx_attachments_project, idx_attachments_unconfirmed |
| spec-CP:Postgres:Schema | projects source + import_ref columns (ADR-0053) | db.go:schema | IMPLEMENTED | — | `ALTER TABLE projects ADD COLUMN IF NOT EXISTS source/import_ref` |
| spec-CP:K8sSubcommands | `init-keys` — generates operator+account NKeys, writes Secret | init_keys.go:runInitKeys | IMPLEMENTED | — | Idempotent, creates bootstrap admin if BOOTSTRAP_ADMIN_EMAIL set |
| spec-CP:K8sSubcommands | `gen-leaf-creds` — reads account seed, generates leaf creds Secret | gen_leaf_creds.go:runGenLeafCreds | IMPLEMENTED | — | Idempotent, writes `leaf.creds` file |
| spec-CP:InternalBehavior:ProjectCreation | CP validates, creates Postgres row, writes KV, publishes provisioning, awaits reply | projects.go:handleProjectCreate + project_http.go | IMPLEMENTED | — | Both NATS and HTTP paths implemented with full flow |
| spec-CP:InternalBehavior:ProjectCreation | On provisioning failure, marks project status='failed' | projects.go:handleProjectCreate | IMPLEMENTED | — | `UpdateProjectStatus(ctx, id, "failed")`, writes failed KV state |
| spec-CP:InternalBehavior:ProjectDeletion | Deletes S3 prefix on project deletion | project_http.go:handleDeleteProjectHTTP | IMPLEMENTED | — | `s3DeletePrefix(uslug + "/" + hslug + "/" + pslug + "/")` |
| spec-CP:InternalBehavior:PerUserResources | On user registration/first login, creates per-user KV buckets + sessions stream | projects.go:ensureUserResources | IMPLEMENTED | — | Called from handleLogin and issueJWTForNKey |
| spec-CP:Auth:NoNKeySeedInResponse | CP never generates NKey pairs for clients (ADR-0054) | auth.go:handleLogin | IMPLEMENTED | — | When nkey_public provided, no seed returned. Legacy path preserved for migration. |
| spec-CP:Auth:OAuthCallback | Stores connection, notifies controllers, sets oauth_id | providers.go:handleOAuthCallback | IMPLEMENTED | — | Sets `users.oauth_id` if NULL on first OAuth callback |
| spec-CP:GitLabRefresh | Background goroutine refreshes tokens expiring within 30 min, every 15 min | providers.go:StartGitLabRefreshGoroutine | IMPLEMENTED | — | Deletes connection if refresh token expired |
| spec-CP:ErrorHandling | Postgres unavailable at startup: fatal exit | main.go:70-72 | IMPLEMENTED | — | `logger.Fatal()` |
| spec-CP:ErrorHandling | NATS retry indefinitely | main.go:118-119 | IMPLEMENTED | — | `RetryOnFailedConnect(true)`, `MaxReconnects(-1)` |
| spec-CP:ErrorHandling | Provisioning timeout returns 503 | projects.go:handleProjectCreate | IMPLEMENTED | — | `replyError(msg, "host "+hostSlug+" unreachable")` |
| spec-CP:ErrorHandling | Device-code expired returns 410 Gone | hosts.go:handleHostRegister | IMPLEMENTED | — | `http.StatusGone` with message |
| spec-CP:ErrorHandling | Device-code already redeemed returns 409 Conflict | hosts.go:handleHostRegister | IMPLEMENTED | — | `http.StatusConflict` |
| spec-common:DeriveUserSlug | `DeriveUserSlug` produces `slugify(full-email)` per ADR-0062 | mclaude-common/pkg/slug/slug.go:359 | GAP | SPEC→FIX | `DeriveUserSlug` in mclaude-common still uses the OLD ADR-0024 algorithm: `{Slugify(name or local-part)}-{domain.split('.')[0]}`. CP works around this by calling `slug.Slugify(email)` directly. The spec-common.md should either be updated to match the actual DeriveUserSlug implementation, or the function should be rewritten. CP itself is correct — it doesn't call DeriveUserSlug. |
| spec-CP:check-slug | check-slug subject: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.check-slug` | lifecycle.go subscription | GAP | SPEC→FIX | Spec says subject is `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.check-slug` but code subscribes to `mclaude.users.*.hosts.*.projects.*.check-slug` which has 3 wildcards. The check-slug handler only uses `uslug` (parts[2]) to resolve the user. The `{pslug}` position in the subject is not meaningful for slug availability checks (it checks across all projects for user). Spec subject pattern is fine as a template; implementation is compatible. |
| spec-stateSchema:UserSlugBlocklist | Known bug: `computeUserSlug()` does not check against the blocklist | db.go:39-46 | IMPLEMENTED | SPEC→FIX | Code now validates via `slug.Validate(s)` which includes blocklist. The "Known bug" note in spec-state-schema.md is stale and should be removed. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| context.go:1-12 | INFRA | Context key types and helper functions for injecting userID/userSlug into request context |
| tracing.go:1-60 | INFRA | OpenTelemetry tracer setup (SpanFromHTTP, SpanDB, SpanProvisioning) — tracing is mentioned in spec dependencies |
| metrics.go:1-60 | INFRA | Prometheus metrics (httpRequestDuration, provisioningErrors, natsReconnects) — metrics endpoint is spec'd on admin port |
| version.go:1-20 | INFRA | VersionResponse struct and handleVersion handler — directly implements spec'd `/version` endpoint |
| s3.go:1-250 | INFRA | S3 pre-signing, object existence check, delete, list — infrastructure supporting spec'd import/attachment features |
| main.go:1-170 | INFRA | Program entrypoint, env var loading, wiring — necessary startup plumbing |
| server.go:1-110 | INFRA | Route registration and mux construction — wires spec'd endpoints |
| db.go:scanUser,scanHost,scanOAuthConnection | INFRA | Row scanner helpers for Postgres query results |
| nkeys.go:permContains,permHasPrefix | INFRA | Helper functions for permission list checking |
| nkeys.go:IssueUserJWTLegacy,IssueHostJWTLegacy | UNSPEC'd | Legacy JWT issuance functions retained for backward compatibility during migration. Spec mentions the legacy path exists but the functions themselves add ~60 lines of code for the old permission model. DEPRECATED per comments but still active. |
| nkeys.go:UserSubjectPermissionsLegacy | UNSPEC'd | Legacy pre-ADR-0054 permission structure. Retained only for test reference per comment. Dead in production but used by tests. |
| hosts.go:deviceCodeStore (global) | UNSPEC'd | In-memory device code store. Spec says "In production, use a database or distributed cache with TTL" but current implementation uses in-memory map. Acceptable for single-replica deployment. |
| challenge.go:challengeStore (global) | UNSPEC'd | In-memory challenge nonce store. Spec acknowledges "single-replica" assumption. |
| device_auth.go:cliDeviceCodeStore (global) | UNSPEC'd | In-memory CLI device code store. Same single-replica assumption. |
| providers.go:patchUserSecret,removeSecretKeys,readSecretKey | UNSPEC'd | No-op stubs — K8s Secret management moved to controller-k8s per ADR-0035. These are placeholders that return nil/empty. The spec says CP doesn't manage K8s Secrets at runtime, so these no-ops are correct, but the functions and their callers constitute dead code paths (OAuth token storage never actually persists). |
| providers.go:buildGHHostsYAML,buildGlabConfigYAML | UNSPEC'd | CLI config YAML builders that are referenced by reconcileUserCLIConfig (which is a no-op). These functions are dead code — they are never called in a path that produces output. |
| providers.go:ReconcileAllUserCLIConfigs | UNSPEC'd | Startup reconcile function that is defined but never called from main(). Dead code. |
| scim.go:scimListUsers scan | UNSPEC'd | SCIM list users scan does not include `nkey_public` column (8 columns scanned vs 9 in `scanUser`). This is intentional — SCIM doesn't need NKey data — but diverges from the shared `scanUser` pattern. |
| init_keys.go (entire file) | INFRA | Helm pre-install Job entrypoint — spec'd as K8s dependency |
| gen_leaf_creds.go (entire file) | INFRA | Helm pre-install Job entrypoint — spec'd as K8s dependency |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| ADR-0062:Decisions:Row1 | computeUserSlug slugifies full email | db_test.go:TestComputeUserSlug_BasicEmail | integration_test.go:TestIntegration_UserSlug_PopulatedOnCreate | TESTED | Unit test verifies dev@mclaude.local→dev-mclaude-local; integration test verifies DB round-trip |
| ADR-0062:Decisions:Row2 | SQL backfill matches algorithm | (implicit in db_test.go:TestSchema_*) | integration_test.go:TestIntegration_UserSlug_Unique | UNIT_ONLY | Schema tests verify SQL structure; integration test checks uniqueness. No integration test runs the actual backfill UPDATE. |
| ADR-0062:IntegrationTests:Row2 | Collision resistance | db_test.go:TestComputeUserSlug_CollisionResistance | — | UNIT_ONLY | CODE→FIX | ADR lists this as an integration test case but only unit test exists. |
| spec-CP:Auth:UserJWT | User JWT with ADR-0054 scoped permissions | nkeys_test.go:TestIssueUserJWT_ADR0054_* | cluster_test.go:TestCluster_LoginAndJWTIssuance | TESTED | |
| spec-CP:Auth:HostJWT | Host JWT scoped, zero JetStream, 5-min TTL | nkeys_test.go:TestIssueHostJWT_ADR0054 | — | UNIT_ONLY | No integration test for host JWT issuance |
| spec-CP:Auth:AgentJWT | Session-agent JWT per-project scoped | nkeys_test.go:TestIssueSessionAgentJWT_ADR0054 | — | UNIT_ONLY | No integration test for agent JWT issuance |
| spec-CP:Auth:ChallengeResponse | Challenge-response auth flow | challenge_test.go:TestVerifyNKeySignature_RoundTrip | — | UNIT_ONLY | CODE→FIX | Auth flow is a critical path — needs integration test with real NATS |
| spec-CP:Auth:Revocation | JWT revocation via $SYS.REQ.CLAIMS.UPDATE | lifecycle_test.go:TestRevokeNKeyJWT_* | — | UNIT_ONLY | CODE→FIX | Only edge case tests (nil NC, empty NKey, no creds). No test of the actual revocation flow. |
| spec-CP:HTTP:Login | POST /auth/login full flow | auth_test.go:TestHandleLogin_* | cluster_test.go:TestCluster_LoginAndJWTIssuance | TESTED | |
| spec-CP:HTTP:Refresh | POST /auth/refresh | auth_test.go:TestHandleRefresh_* | integration_test.go:TestIntegration_HandleRefresh_ReturnsSlug | TESTED | |
| spec-CP:NATS:SysEvent | $SYS CONNECT/DISCONNECT handling | — | integration_test.go:TestIntegration_HandleSysEvent_* | E2E_ONLY | 4 integration tests cover machine/cluster connect/disconnect |
| spec-CP:NATS:AgentRegister | Agent registration | lifecycle_test.go:TestHandleAgentRegister_* | — | UNIT_ONLY | Only error-path unit tests (nil DB, malformed subject, etc) |
| spec-CP:NATS:HostLifecycle | manage.grant/revoke-access/deregister/revoke/rekey/update | lifecycle_test.go:TestHandleManage*_* | — | UNIT_ONLY | Only error-path/edge-case unit tests |
| spec-CP:HTTP:Attachments | Attachment upload/confirm/download | s3_test.go:TestHandleAttachment* | — | UNIT_ONLY | Tests cover nil S3/DB cases, field validation |
| spec-CP:Postgres:Schema | Schema DDL correctness | db_test.go:TestSchema_* | integration_test.go:TestIntegration_MigrateIdempotent | TESTED | |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | No control-plane bugs found in .agent/bugs/ | — | All bugs in .agent/bugs/ are for other components (web, sessions) |

### Summary

- Implemented: 86
- Gap: 1 (DeriveUserSlug in mclaude-common doesn't match ADR-0062; CP works around it)
- Partial: 0
- Infra: 14
- Unspec'd: 7 (legacy JWT functions, no-op K8s stubs, dead CLI config builders, in-memory stores)
- Dead: 2 (ReconcileAllUserCLIConfigs never called; buildGHHostsYAML/buildGlabConfigYAML never produce output due to no-op callers)
- Tested: 5
- Unit only: 8
- E2E only: 1
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0

**Key ADR-0062 findings:**

1. **IMPLEMENTED**: `computeUserSlug()` correctly slugifies the full email via `slug.Slugify(email)`. `dev@mclaude.local` → `dev-mclaude-local`. ✓
2. **IMPLEMENTED**: SQL backfill matches: `trim(both '-' from lower(regexp_replace(email, '[^a-zA-Z0-9]+', '-', 'g')))`. ✓
3. **IMPLEMENTED**: Collision resistance — full domain inclusion prevents `richard@rbc.com` vs `richard@gmail.com` collision. ✓
4. **STALE SPEC NOTE**: spec-state-schema.md documents a "Known bug" that `computeUserSlug()` doesn't check the blocklist — this is **fixed** in code (uses `slug.Validate` + `ValidateOrFallback`). Spec should be updated.
5. **GAP [SPEC→FIX]**: `mclaude-common/pkg/slug/DeriveUserSlug()` still uses the old ADR-0024 algorithm (`{name-part}-{domain-first-segment}`), not the ADR-0062 `slugify(full-email)`. The control-plane correctly avoids calling this function, using `slug.Slugify(email)` directly instead. The spec-common.md should be updated to match the actual `DeriveUserSlug` implementation, or the function should be rewritten.
6. **DEAD CODE**: `providers.go` contains `ReconcileAllUserCLIConfigs`, `buildGHHostsYAML`, `buildGlabConfigYAML` — these are never called from production code paths (the reconcile function is defined but not invoked from `main()`, and the YAML builders are called only from the no-op `reconcileUserCLIConfig`).
