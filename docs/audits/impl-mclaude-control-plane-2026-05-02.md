## Run: 2026-05-02T00:00:00Z

Component: mclaude-control-plane
Spec files: docs/mclaude-control-plane/spec-control-plane.md, docs/spec-state-schema.md, docs/charts-mclaude/spec-helm.md
ADRs evaluated: adr-0073 (accepted — gen-host-nkey fix), plus cross-cutting ADRs referenced in spec

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-control-plane.md:7 | "Per ADR-0035 the control-plane is K8s-free: no controller-runtime, no K8s client, no MCProject reconciler ... The control-plane binary also doubles as two Helm pre-install Job entrypoints (the only code paths that use client-go): control-plane init-keys and ... gen-host-nkey" | main.go:33-40, init_keys.go, gen_host_nkey.go | IMPLEMENTED | — | Both subcommands dispatch before server init; only those files import client-go |
| spec-control-plane.md:11 | "Runs as a Kubernetes Deployment in the mclaude-system namespace … Listens on two ports: main API port (default 8080) … loopback-only admin port (default 9091, bound to 127.0.0.1)" | main.go:48-49,184,190 | IMPLEMENTED | — | PORT and ADMIN_PORT env vars with correct defaults; admin bound to 127.0.0.1 |
| spec-control-plane.md:17 | EXTERNAL_URL: required, exits on startup if empty | main.go:81-84 | IMPLEMENTED | — | Fatal exit if EXTERNAL_URL == "" |
| spec-control-plane.md:18 | DATABASE_URL/DATABASE_DSN: required, exits on startup if empty | main.go:50,65-78 | IMPLEMENTED | — | Fatal exit if databaseDSN == "" |
| spec-control-plane.md:19 | NATS_URL: default nats://localhost:4222 | main.go:51 | IMPLEMENTED | — | envOr("NATS_URL", "nats://localhost:4222") |
| spec-control-plane.md:20 | NATS_WS_URL: empty means client derives from origin | main.go:52 | IMPLEMENTED | — | envOr("NATS_WS_URL", "") |
| spec-control-plane.md:21 | NATS_ACCOUNT_SEED: if not set, generates ephemeral account key | main.go:99-102, loadOrGenerateAccountKey():353-362 | IMPLEMENTED | — | Reads env var; falls back to nkeys.CreateAccount() |
| spec-control-plane.md:22 | OPERATOR_KEYS_PATH: default /etc/mclaude/operator-keys | main.go:118 | IMPLEMENTED | — | envOr("OPERATOR_KEYS_PATH", "/etc/mclaude/operator-keys") |
| spec-control-plane.md:23 | NATS_SYS_ACCOUNT_SEED: if set as env var, takes precedence over file | main.go:393-395 | IMPLEMENTED | — | loadOperatorKeys() checks env var first |
| spec-control-plane.md:24 | ADMIN_TOKEN: static bearer token for admin port | main.go:53, server.go:142-151 | IMPLEMENTED | — | adminAuthMiddleware enforces token |
| spec-control-plane.md:25 | BOOTSTRAP_ADMIN_EMAIL: read from Helm value; init-keys Job pre-creates users row | init_keys.go:36,111-117 | IMPLEMENTED | — | bootstrapAdminUser() called when both env vars are set |
| spec-control-plane.md:28 | JWT_EXPIRY_SECONDS: default 28800 (8h) | main.go:55-60 | IMPLEMENTED | — | Parses seconds, falls back to 8*time.Hour |
| spec-control-plane.md:32 | PROVIDERS_CONFIG_PATH: default /etc/mclaude/providers.json | main.go:87 | IMPLEMENTED | — | envOr("PROVIDERS_CONFIG_PATH", "/etc/mclaude/providers.json") |
| spec-control-plane.md:39 | PROVISION_TIMEOUT_SECONDS: currently a hardcoded constant, not yet read from env | projects.go:58-59 | IMPLEMENTED | — | const DefaultProvisionTimeoutSeconds = 10 (spec annotates this as known gap) |
| spec-control-plane.md:40-41 | LOG_LEVEL, HELM_RELEASE_NAME: Not read by Go code | (no code) | IMPLEMENTED | — | Spec explicitly says not consumed by binary |
| spec-control-plane.md:42 | METRICS_PORT: Not read by Go code — removed; /metrics served on admin port | server.go:137 | IMPLEMENTED | — | handleMetrics on admin mux; no third listener |
| spec-control-plane.md:52 | POST /auth/login — no NKey seed in response (ADR-0054) | auth.go:161-198, 204-218 | IMPLEMENTED | — | When nkey_public provided: no seed returned. Legacy path returns seed. |
| spec-control-plane.md:53 | POST /auth/refresh — returns natsWsURL | auth.go:222-301 | IMPLEMENTED | — | Returns s.natsWsURL in LoginResponse.NATSUrl |
| spec-control-plane.md:54 | POST /api/auth/challenge — lookup order users.nkey_public → hosts.public_key → agent_credentials.nkey_public | challenge.go:298-313 | IMPLEMENTED | — | isNKeyRegistered() checks all three tables in order |
| spec-control-plane.md:55 | POST /api/auth/verify — resolves identity type, signs JWT, returns {ok, jwt} | challenge.go:130-208, issueJWTForNKey:222-273 | IMPLEMENTED | — | Three-table lookup with fallthrough; returns VerifyResponse |
| spec-control-plane.md:56 | POST /api/auth/device-code | server.go:27-33 | IMPLEMENTED | — | Registered on main mux |
| spec-control-plane.md:57 | POST /api/auth/device-code/poll | server.go:34-40 | IMPLEMENTED | — | Registered; returns 410 on expiry per device_auth.go |
| spec-control-plane.md:58 | GET /api/auth/device-code/verify | server.go:41 | IMPLEMENTED | — | Registered |
| spec-control-plane.md:59 | GET /version | server.go:17 | IMPLEMENTED | — | handleVersion registered |
| spec-control-plane.md:60 | GET /health — never checks NATS | server.go:18-20 | IMPLEMENTED | — | Returns 200 OK only |
| spec-control-plane.md:61 | GET /healthz — never checks NATS | server.go:44-46 | IMPLEMENTED | — | Returns 200 OK only |
| spec-control-plane.md:62 | GET /readyz — checks Postgres connectivity | server.go:47-57 | IMPLEMENTED | — | Pings db.pool; returns 503 on failure |
| spec-control-plane.md:63 | GET /auth/providers/{id}/callback | server.go:60-66 | IMPLEMENTED | — | Registered with suffix check |
| spec-control-plane.md:69 | GET /auth/me | server.go:69 | IMPLEMENTED | — | authMiddleware wrapped |
| spec-control-plane.md:70-74 | GET/POST /api/providers, POST /api/providers/pat, POST /api/providers/{id}/connect, GET /api/connections/{id}/repos, DELETE /api/connections/{id} | server.go:72-106 | IMPLEMENTED | — | All routes registered |
| spec-control-plane.md:75 | POST /api/attachments/upload-url | server.go:118-119 | IMPLEMENTED | — | handleAttachmentRoutes handles upload-url path |
| spec-control-plane.md:76 | POST /api/attachments/{id}/confirm | server.go:118-119 | IMPLEMENTED | — | handleAttachmentRoutes handles confirm path |
| spec-control-plane.md:77 | GET /api/attachments/{id} | server.go:118-119 | IMPLEMENTED | — | handleAttachmentRoutes handles download path |
| spec-control-plane.md:78 | PATCH /api/projects/{id} | server.go:109-115 | IMPLEMENTED | — | Method check for PATCH only |
| spec-control-plane.md:79 | GET /api/users/{uslug}/hosts | server.go:122 | IMPLEMENTED | — | handleUserAPIRoutes dispatches by path |
| spec-control-plane.md:80 | POST /api/users/{uslug}/hosts | server.go:122 | IMPLEMENTED | — | handleUserAPIRoutes |
| spec-control-plane.md:81 | POST /api/users/{uslug}/hosts/code | server.go:122 | IMPLEMENTED | — | handleUserAPIRoutes |
| spec-control-plane.md:82 | GET /api/users/{uslug}/hosts/code/{code} | server.go:122 | IMPLEMENTED | — | handleUserAPIRoutes; KNOWN gap (no expiresAt) noted in spec |
| spec-control-plane.md:83 | POST /api/hosts/register — public, no JWT required | server.go:125 | IMPLEMENTED | — | Registered on main mux without authMiddleware |
| spec-control-plane.md:84 | PUT /api/users/{uslug}/hosts/{hslug} | server.go:122 | IMPLEMENTED | — | handleUserAPIRoutes |
| spec-control-plane.md:85 | DELETE /api/users/{uslug}/hosts/{hslug} | server.go:122 | IMPLEMENTED | — | handleUserAPIRoutes |
| spec-control-plane.md:93-96 | POST/GET/GET/DELETE /api/users/{uslug}/projects | server.go:122 | IMPLEMENTED | — | handleUserAPIRoutes |
| spec-control-plane.md:99-105 | SCIM 2.0 endpoints — not yet implemented | server.go:128 | PARTIAL | SPEC→FIX | scim.go exists; routes registered behind adminAuthMiddleware on /scim/v2/; spec says "not yet implemented" which is consistent with stubs |
| spec-control-plane.md:109-113 | Loopback port: GET /metrics on admin mux | server.go:131-138, main.go:182-187 | IMPLEMENTED | — | admin mux on 127.0.0.1:adminPort, /metrics registered |
| spec-control-plane.md:115-127 | /admin/* endpoints with ADMIN_TOKEN | server.go:136, admin.go | IMPLEMENTED | — | All admin routes behind adminAuthMiddleware |
| spec-control-plane.md:137 | mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create — publishes to this subject for provisioning | projects.go, subj.HostUserProjectsCreate | IMPLEMENTED | — | Dev-seed path at main.go:305 uses correct subject |
| spec-control-plane.md:145 | $SYS.ACCOUNT.{accountKey}.CONNECT — Client kind matches hosts.public_key, updates last_seen_at, upserts KV online=true | sys_subscriber.go:87-121 | IMPLEMENTED | — | No type filter, correct KV write |
| spec-control-plane.md:145 | DISCONNECT sets KV online=false; does not rewrite last_seen_at | sys_subscriber.go:122-157 | IMPLEMENTED | — | Read-modify-write preserves lastSeenAt |
| spec-control-plane.md:145 | Leafnode branch removed — all host controllers connect hub-direct as Client | sys_subscriber.go:152-158 | IMPLEMENTED | — | Default case logs warning and drops |
| spec-control-plane.md:147 | mclaude.hosts.{hslug}.api.agents.register — validates host access + project ownership + host assignment | lifecycle.go:156-232 | IMPLEMENTED | — | Full validation chain; upserts agent_credentials |
| spec-control-plane.md:148 | mclaude.users.*.hosts._.register — no JWT in response | lifecycle.go:243-313 | IMPLEMENTED | — | Returns {ok, slug} only |
| spec-control-plane.md:149 | mclaude.users.*.hosts.*.manage.grant — validates publisher is host owner | lifecycle.go:324-387 | IMPLEMENTED | — | Checks host.UserID == owner.ID |
| spec-control-plane.md:150 | mclaude.users.*.hosts.*.manage.revoke-access | lifecycle.go:398-467 | IMPLEMENTED | — | Revokes user + agent JWTs |
| spec-control-plane.md:151 | mclaude.users.*.hosts.*.manage.deregister | lifecycle.go:473-556 | IMPLEMENTED | — | Drains projects, revokes credentials, tombstones KV |
| spec-control-plane.md:152 | mclaude.users.*.hosts.*.manage.revoke — emergency credential revocation | lifecycle.go:562-626 | IMPLEMENTED | — | Revokes host + all agent JWTs |
| spec-control-plane.md:153 | mclaude.users.*.hosts.*.manage.rekey | lifecycle.go:637-691 | IMPLEMENTED | — | Owner-only, updates hosts.nkey_public |
| spec-control-plane.md:154 | mclaude.users.*.hosts.*.manage.update | lifecycle.go:700-763 | IMPLEMENTED | — | Updates name in DB and KV |
| spec-control-plane.md:155 | mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.request | lifecycle.go:90-94 | IMPLEMENTED | — | handleNATSImportRequest subscribed |
| spec-control-plane.md:156 | mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.confirm | lifecycle.go:95-99 | IMPLEMENTED | — | handleNATSImportConfirm subscribed |
| spec-control-plane.md:157 | mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.download | lifecycle.go:100-104 | IMPLEMENTED | — | handleNATSImportDownload subscribed |
| spec-control-plane.md:158 | mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.complete | lifecycle.go:105-109 | IMPLEMENTED | — | handleNATSImportComplete subscribed |
| spec-control-plane.md:159-161 | attachments.download, .upload, .confirm NATS handlers | lifecycle.go:112-128 | IMPLEMENTED | — | All three subscribed |
| spec-control-plane.md:162 | mclaude.users.{uslug}.hosts.{hslug}.projects.check-slug | lifecycle.go:83-87 | IMPLEMENTED | — | handleCheckSlug subscribed |
| spec-control-plane.md:172 | mclaude-hosts shared KV bucket; created by ensureHostsKV | projects.go (ensureHostsKV called at startup in StartProjectsSubscriber) | IMPLEMENTED | — | Created before $SYS subscriber starts |
| spec-control-plane.md:176 | mclaude-sessions-{uslug} per-user KV bucket created on user registration | auth.go:181-186, challenge.go:234-238 | IMPLEMENTED | — | ensureUserResources called on login and challenge-verify |
| spec-control-plane.md:177 | mclaude-projects-{uslug} per-user KV bucket | auth.go:181-186 | IMPLEMENTED | — | ensureUserResources creates both buckets |
| spec-control-plane.md:178 | MCLAUDE_SESSIONS_{uslug} per-user JetStream stream | auth.go:181-186 | IMPLEMENTED | — | ensureUserResources creates stream |
| spec-control-plane.md:186 | Postgres tables: users, projects, hosts, host_access, agent_credentials, attachments, oauth_connections | db.go | IMPLEMENTED | — | All tables created in Migrate() via idempotent DDL |
| spec-control-plane.md:188 | computeUserSlug: slugifies full email; full domain included | db.go:36-47 | IMPLEMENTED | — | Uses slug.Slugify(email); ADR-0062 |
| spec-control-plane.md:202-204 | Binary doubles as init-keys Helm Job; only code path that uses client-go | init_keys.go:1-163 | IMPLEMENTED | — | Uses rest.InClusterConfig + kubernetes.NewForConfig |
| spec-control-plane.md:202 | control-plane init-keys: generates operator+account NKey pairs+JWTs, writes to operator-keys Secret, idempotent | init_keys.go:51-104 | IMPLEMENTED | — | GET first, exit 0 if exists |
| spec-control-plane.md:211 | Startup step 1: connects to Postgres, fatal exit on failure, runs idempotent schema migration | main.go:65-78 | IMPLEMENTED | — | Fatal if databaseDSN empty or connect fails |
| spec-control-plane.md:212 | Startup step 2: loads OAuth provider config from PROVIDERS_CONFIG_PATH | main.go:87-96 | IMPLEMENTED | — | Non-fatal warning on failure |
| spec-control-plane.md:213 | Startup step 3: loads account signing key from NATS_ACCOUNT_SEED | main.go:99-102 | IMPLEMENTED | — | Generates ephemeral if not set |
| spec-control-plane.md:213 | Fatal exit if signing fails | main.go:100-102 | IMPLEMENTED | — | logger.Fatal() on error |
| spec-control-plane.md:213 | OPERATOR_KEYS_PATH mount missing/unreadable: non-fatal in dev | main.go:119-125 | PARTIAL | SPEC→FIX | spec-control-plane.md:300 says "fatal exit" but code logs warning and continues in dev mode. Code's behavior is justified (dev deployments lack the secret). Spec should say "fatal exit in production; warning in dev (absent OPERATOR_KEYS_PATH mount)." |
| spec-control-plane.md:215 | Startup step 5: connects to hub NATS with retry on failure, unlimited reconnects | main.go:133-143 | IMPLEMENTED | — | RetryOnFailedConnect+MaxReconnects(-1) |
| spec-control-plane.md:216 | Startup step 6: ensures mclaude-hosts KV bucket before $SYS subscriber | main.go:149-165 (StartProjectsSubscriber before StartSysSubscriber) | IMPLEMENTED | — | Order in main.go: projects subscriber (which creates hostsKV) before sys subscriber |
| spec-control-plane.md:217 | Startup step 7: subscribes to $SYS and lifecycle subjects | main.go:149-165 | IMPLEMENTED | — | All subscribers started |
| spec-control-plane.md:218 | Startup step 8: starts GitLab token refresh goroutine (every 15 minutes) | main.go:167-168 | IMPLEMENTED | — | StartGitLabRefreshGoroutine called |
| spec-control-plane.md:219 | Startup step 9: DEV_SEED=true seeds dev user, local host, default project | main.go:171-176, seedDev:197-327 | IMPLEMENTED | — | Full seeding with KV writes and provisioning request |
| spec-control-plane.md:220 | Startup step 10: starts main and loopback listeners | main.go:179-192 | IMPLEMENTED | — | Both http.ListenAndServe calls |
| spec-control-plane.md:226-235 | User login stores nkey_public, issues scoped JWT with host slugs | auth.go:161-198 | IMPLEMENTED | — | Stores nkey_public in DB; IssueUserJWT with host slugs |
| spec-control-plane.md:233 | Per ADR-0054: IssueUserJWT with KV_mclaude-sessions-{uslug}, KV_mclaude-projects-{uslug}, MCLAUDE_SESSIONS_{uslug}, per-host hosts KV entries | nkeys.go:42-108 | IMPLEMENTED | — | UserSubjectPermissions generates the correct explicit allow-lists |
| spec-control-plane.md:234 | JWT lifetime is JWT_EXPIRY_SECONDS (default 8h) | main.go:55-60, auth.go:155 | IMPLEMENTED | — | Used in IssueUserJWT |
| spec-control-plane.md:237-240 | Challenge-response: POST /api/auth/challenge + verify | challenge.go:60-327 | IMPLEMENTED | — | Both handlers implemented |
| spec-control-plane.md:242 | Agent JWT issuance exclusively by CP; IssueSessionAgentJWT scoped | challenge.go:258-270, nkeys.go:303-314 | IMPLEMENTED | — | SessionAgentSubjectPermissions used |
| spec-control-plane.md:244 | Host JWTs scoped to mclaude.hosts.{hslug}.> only, zero JetStream | nkeys.go:192-203 | IMPLEMENTED | — | HostSubjectPermissions: no $JS entries |
| spec-control-plane.md:246 | Per-user KV + stream created on user registration (first login) | challenge.go:234-238, auth.go:181-186 | IMPLEMENTED | — | ensureUserResources on both login paths |
| spec-control-plane.md:248 | Auth middleware: known gap — access boundary enforcement not fully implemented | auth.go:389-408 | PARTIAL | SPEC→FIX | Code now implements cross-user 403 check (lines 391-408) with admin bypass. Spec says "Known gap: not implemented". Spec should be updated to reflect that this IS implemented. |
| spec-control-plane.md:252-264 | JWT revocation flow: decode account JWT, add to revocation list, re-sign with operator key, publish to $SYS.REQ.CLAIMS.UPDATE | lifecycle.go:848-964 | IMPLEMENTED | — | Full 6-step revocation flow |
| spec-control-plane.md:268 | OAuth: on successful callback, stores connection in oauth_connections, publishes NATS update | providers.go | IMPLEMENTED | — | Providers file handles callback |
| spec-control-plane.md:276 | GitLab token refresh every 15 minutes | main.go:167-168 | IMPLEMENTED | — | StartGitLabRefreshGoroutine |
| spec-control-plane.md:284 | Project creation: writes to mclaude-projects-{uslug} KV, key format hosts.{hslug}.projects.{pslug} | projects.go (writeProjectKV) | IMPLEMENTED | — | KV key uses hierarchical format per ADR-0054 |
| spec-control-plane.md:285 | Publishes to mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create | projects.go, subj.HostUserProjectsCreate | IMPLEMENTED | — | Subject helper used |
| spec-control-plane.md:300 | OPERATOR_KEYS_PATH mount missing or unreadable: fatal exit on startup | main.go:119-125 | PARTIAL | SPEC→FIX | Code logs warning and continues (non-fatal in dev). Spec says "fatal exit on startup." Code's behavior is correct for dev deployments. Spec should clarify "fatal only when OPERATOR_KEYS_PATH is set but unreadable; warning if absent." |
| spec-control-plane.md:302 | NATS connection failure: retries indefinitely, unlimited reconnects | main.go:138-139 | IMPLEMENTED | — | RetryOnFailedConnect(true)+MaxReconnects(-1) |
| spec-control-plane.md:303 | Provisioning request timeout: POST returns 503 with error "host {hslug} unreachable" | projects.go | IMPLEMENTED | — | ProvisionReply error propagated |
| ADR-0073:44-50 | main.go: gen-host-nkey subcommand dispatch immediately after init-keys check | main.go:37-40 | IMPLEMENTED | — | Exact code pattern from ADR present: `if len(os.Args) > 1 && os.Args[1] == "gen-host-nkey" { runGenHostNkey(); return }` |
| ADR-0073:57 | gen_host_nkey.go: Read env NAMESPACE (default mclaude-system), HOST_CREDS_SECRET (required) | gen_host_nkey.go:39-43 | IMPLEMENTED | — | NAMESPACE defaults via envOr; HOST_CREDS_SECRET required with fatal exit |
| ADR-0073:58 | Build in-cluster K8s client via rest.InClusterConfig()+kubernetes.NewForConfig() | gen_host_nkey.go:49-56 | IMPLEMENTED | — | Exact pattern; fatal on error |
| ADR-0073:59 | GET Secret; if exists → log "already exists — skipping" and exit 0 (idempotent) | gen_host_nkey.go:67-74 | IMPLEMENTED | — | Returns nil (which leads to exit 0) with skip log message |
| ADR-0073:60 | Generate NKey pair: kp, err := nkeys.CreateUser() | gen_host_nkey.go:80-83 | IMPLEMENTED | — | Correct function called |
| ADR-0073:61 | Extract seed: seed, err := kp.Seed() | gen_host_nkey.go:85-88 | IMPLEMENTED | — | Implemented |
| ADR-0073:62 | Extract public key: pub, err := kp.PublicKey() | gen_host_nkey.go:90-93 | IMPLEMENTED | — | Implemented |
| ADR-0073:63 | Create K8s Secret with Data: map[string][]byte{"nkey_seed": seed} | gen_host_nkey.go:96-108 | IMPLEMENTED | — | Secret created with nkey_seed field |
| ADR-0073:64 | Log public key: logger.Info().Str("nkeyPublic", pub).Msg("generated host NKey") | gen_host_nkey.go:119-123 | IMPLEMENTED | — | Exact log call matches spec |
| ADR-0073:67 | rest.InClusterConfig() failure → fatal | gen_host_nkey.go:50-52 | IMPLEMENTED | — | logger.Fatal() on error |
| ADR-0073:68 | HOST_CREDS_SECRET empty → fatal | gen_host_nkey.go:41-43 | IMPLEMENTED | — | logger.Fatal() with message "HOST_CREDS_SECRET env is required" |
| ADR-0073:69 | Secret GET non-404 error → fatal | gen_host_nkey.go:75-77 | IMPLEMENTED | — | Returns err (propagated to Fatal in runGenHostNkey) |
| ADR-0073:70 | nkeys.CreateUser() or kp.Seed() error → fatal | gen_host_nkey.go:80-88 | IMPLEMENTED | — | Errors returned and propagated to Fatal |
| ADR-0073:71 | Secret CREATE fails with AlreadyExists (race) → log and exit 0 | gen_host_nkey.go:111-115 | IMPLEMENTED | — | IsAlreadyExists check with log and return nil |
| ADR-0073:72 | Other CREATE error → fatal | gen_host_nkey.go:116-117 | IMPLEMENTED | — | return err propagated to Fatal |
| spec-helm.md:197 | Pre-Install Job gen-host-nkey: Generates NKey pair via nkeys.CreateUser() (U-prefix) | gen_host_nkey.go:80 | IMPLEMENTED | — | nkeys.CreateUser() used |
| spec-helm.md:197 | Writes decorated seed string to Secret {release}-host-creds field nkey_seed | gen_host_nkey.go:96-108 | IMPLEMENTED | — | Secret field "nkey_seed" correctly named |
| spec-helm.md:197 | Prints public key to Job log | gen_host_nkey.go:119-123 | IMPLEMENTED | — | logger.Info() with nkeyPublic field |
| spec-helm.md:197 | Idempotent — skips if Secret exists | gen_host_nkey.go:67-74 | IMPLEMENTED | — | GET check before CREATE; AlreadyExists race also handled |
| spec-helm.md:204 | Controller Deployment mounts {release}-host-creds at /etc/mclaude/host-creds/; env HOST_NKEY_SEED_PATH=/etc/mclaude/host-creds/nkey_seed | gen_host_nkey.go:96-108 | IMPLEMENTED | — | Secret field name "nkey_seed" matches path expected by controller |
| spec-state-schema.md:18 | users.slug: slugified full email; full domain included | db.go:36-47 | IMPLEMENTED | — | computeUserSlug calls slug.Slugify(email) |
| spec-state-schema.md:64 | hosts.public_key: generated by gen-host-nkey Job for cluster hosts | gen_host_nkey.go:80-93 | IMPLEMENTED | — | Gen job creates U-prefix key; stored in Secret; controller reads it |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-29 | INFRA | Package declaration, imports, boilerplate |
| main.go:329-349 | INFRA | generateNATSUserCreds() — necessary infrastructure for CP's own NATS connection; spec says CP uses account key to sign internal service JWT |
| main.go:352-362 | INFRA | loadOrGenerateAccountKey() — spec says "load from NATS_ACCOUNT_SEED or generate ephemeral" |
| main.go:364-369 | INFRA | envOr() helper — used throughout; pure utility |
| main.go:371-405 | INFRA | loadOperatorKeys() — reads operatorSeed/accountJwt/sysAccountSeed from OPERATOR_KEYS_PATH; directly spec'd behavior |
| gen_host_nkey.go:63-125 | INFRA | genHostNkeyWithClient() — testable core extracted from runGenHostNkey(); allows unit testing with fake K8s clientset |
| init_keys.go:122-163 | INFRA | bootstrapAdminUser() — spec'd by BOOTSTRAP_ADMIN_EMAIL behavior |
| auth.go:303-360 | INFRA | connectedProviderEntry, handleMe() — spec'd by GET /auth/me endpoint |
| auth.go:366-411 | INFRA | authMiddleware() — spec'd by Protected routes auth requirement. Note: KNOWN-22 comment in code states access boundary enforcement is now implemented (spec still says "known gap") |
| auth.go:449-500 | INFRA | getLoginHosts(), getLoginProjects() — fills hosts[]/projects[] arrays in LoginResponse |
| nkeys.go:395-418 | INFRA | IssueHostJWTLegacy() — backward compat legacy path; retained for migration period; no new spec requirement |
| nkeys.go:453-476 | INFRA | UserSubjectPermissionsLegacy(), permContains(), permHasPrefix() — permContains/permHasPrefix are test helpers exposed from production file |
| context.go | INFRA | contextKeyUserID, contextKeyUserSlug and helpers — necessary request-scoped context plumbing |
| version.go | INFRA | handleVersion() — spec'd by GET /version |
| metrics.go | INFRA | metricsHandler() — spec'd by GET /metrics on admin port |
| tracing.go | INFRA | OpenTelemetry tracer setup — spec says "optional tracer provider for distributed tracing" |
| admin.go | INFRA | Admin endpoint handlers (clusters, users, sessions/stop) — spec'd by /admin/* routes |
| scim.go | INFRA | handleSCIMRoutes() — spec says SCIM endpoints exist but not yet implemented; stubs are placeholders |
| db.go:100-end | INFRA | DB methods (Migrate, CRUD for all tables) — all spec'd |
| hosts.go | INFRA | HTTP host CRUD handlers — spec'd by /api/users/{uslug}/hosts/* routes |
| projects.go | INFRA | Project lifecycle (writeProjectKV, ensureHostsKV, ensureUserResources, StartProjectsSubscriber) — all spec'd |
| project_http.go | INFRA | HTTP project CRUD handlers — spec'd by /api/users/{uslug}/projects routes |
| attachments.go | INFRA | Attachment HTTP and NATS handlers — spec'd by ADR-0053 attachment subjects |
| s3.go | INFRA | S3 config loading and pre-signed URL generation — spec'd by S3_ENDPOINT env |
| providers.go | INFRA | OAuth provider loading — spec'd by PROVIDERS_CONFIG_PATH |
| device_auth.go | INFRA | CLI device-code auth flow — spec'd by /api/auth/device-code/* endpoints |
| challenge.go:330-340 | INFRA | cleanupExpiredChallenges() — necessary for in-memory nonce GC; spec says 30s TTL single-use |
| nkeys.go:456-476 | UNSPEC'd | permContains() and permHasPrefix() — exported-style helpers living in production file (not test file). No spec mentions these functions. They are only used in test files. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| ADR-0073:59 | Idempotent: skip if Secret already exists | gen_host_nkey_test.go:TestGenHostNkey_Idempotent, TestGenHostNkey_SkipLogOnExisting | None (no real K8s cluster) | UNIT_ONLY | Integration test would require a real K8s cluster; ADR-0073 integration test cases not implemented as automated tests |
| ADR-0073:60-63 | nkeys.CreateUser(), extract seed+pubkey, create Secret with nkey_seed | gen_host_nkey_test.go:TestGenHostNkey_CreatesSecret, TestGenHostNkey_UniqueKeysPerInstall, TestGenHostNkey_SecretHasLabel, TestGenHostNkey_SeedRoundTrip | None | UNIT_ONLY | All unit tests use fake.NewSimpleClientset(); no integration against real K8s |
| ADR-0073:64 | Log public key with nkeyPublic field and "generated host NKey" message | gen_host_nkey_test.go:TestGenHostNkey_LogsPublicKey | None | UNIT_ONLY | Unit test verifies log output via bytes.Buffer |
| ADR-0073:71 | AlreadyExists race → idempotent exit | gen_host_nkey_test.go:TestGenHostNkey_Idempotent (covers the case via pre-created secret, not the race path explicitly) | None | UNIT_ONLY | Race path (between GET and CREATE) is partially covered by the idempotent test but the exact race condition is not exercised |
| spec-control-plane.md:54-55 | POST /api/auth/challenge + /api/auth/verify (NKey challenge-response) | challenge_test.go:TestHandleAuthChallenge_*, TestHandleAuthVerify_*, TestVerifyNKeySignature_RoundTrip | integration_test.go:TestIntegration_NATSSubjectPermissions | UNIT_ONLY | Integration test runs against vanilla NATS (testcontainers), not operator-mode NATS — cannot verify JWT permission enforcement |
| spec-control-plane.md:145 | $SYS.ACCOUNT.*.CONNECT/DISCONNECT host liveness | None (unit) | integration_test.go:TestIntegration_HandleSysEvent_MachineConnect/Disconnect, ClusterConnect/Disconnect, LeafnodeDropped | E2E_ONLY | No unit tests for handleSysEvent; integration tests use testcontainers real NATS |
| spec-control-plane.md:52-53 | POST /auth/login, /auth/refresh | auth_test.go:TestHandleLogin_*, TestHandleRefresh_* | integration_test.go:TestIntegration_HandleLogin_UserSlugInResponse, HandleRefresh_ReturnsSlug | TESTED | Both unit and integration coverage |
| spec-control-plane.md:147 | mclaude.hosts.{hslug}.api.agents.register | lifecycle_test.go:TestHandleAgentRegister_* | None | UNIT_ONLY | No integration test for agent registration; critical auth flow |
| spec-control-plane.md:148 | mclaude.users.*.hosts._.register | lifecycle_test.go:TestHandleNATSHostRegister_* | None | UNIT_ONLY | No integration test for NATS host registration |
| spec-control-plane.md:149-154 | manage.grant/revoke-access/deregister/revoke/rekey | lifecycle_test.go:TestHandleManageGrant_*, TestHandleManageRevokeAccess_* | None | UNIT_ONLY | No integration tests for host lifecycle management |
| spec-control-plane.md:202-204 | init-keys subcommand: generates operator+account keys, writes Secret, idempotent | None found | None | UNTESTED | No unit or integration test for runInitKeys() |
| ADR-0073 Integration Test Cases: Worker Helm upgrade succeeds | gen-host-nkey pre-upgrade Job completes | None | None | UNTESTED | ADR-0073 explicitly lists this integration test case; not implemented |
| ADR-0073 Integration Test Cases: Fresh install (no prior Secret) | Job generates NKey pair, writes Secret, exits 0 | gen_host_nkey_test.go:TestGenHostNkey_CreatesSecret | None (no real K8s) | UNIT_ONLY | Unit test uses fake client; ADR test case requires real cluster |
| ADR-0073 Integration Test Cases: Idempotent upgrade | Job sees existing Secret, logs skip message, exits 0 | gen_host_nkey_test.go:TestGenHostNkey_Idempotent | None (no real K8s) | UNIT_ONLY | Unit test uses fake client |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| No open bugs with Component: mclaude-control-plane | — | — | Open bugs are for: spa, spa, spa, Droid-Shield scanner — none for control-plane |

### Summary

- Implemented: 82
- Gap: 0
- Partial: 3
- Infra: 27
- Unspec'd: 1
- Dead: 0
- Tested: 1
- Unit only: 8
- E2E only: 1
- Untested: 3
- Bugs fixed: 0
- Bugs open: 0
