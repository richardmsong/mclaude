# Spec: Control Plane

## Role

The control plane is the central API server and trust root for mclaude. It authenticates all identity types (users, hosts, agents) via a unified HTTP NKey challenge-response protocol (ADR-0054), issues scoped NATS JWTs (signed by the deployment-level account key), manages user / project / host records in Postgres, publishes provisioning requests over NATS to the appropriate controller (`mclaude-controller-k8s` for cluster hosts), manages host lifecycle (registration, access grants/revocation, deregistration, emergency credential revocation — ADR-0054), handles OAuth provider integrations (GitHub/GitLab), tracks host liveness via `$SYS` events, manages S3 pre-signed URLs for binary data (imports and attachments — ADR-0053), provisions per-user JetStream resources (KV buckets and sessions streams — ADR-0054), and exposes admin endpoints for cluster registration and access grants.

Per ADR-0035 the control-plane is **K8s-free**: no controller-runtime, no K8s client, no MCProject reconciler. All K8s mutation is delegated to `mclaude-controller-k8s` via NATS request/reply. The control-plane runs only inside the central `mclaude-cp` Kubernetes cluster (there is no local/standalone variant). All identity types generate their own NKeys — the control-plane never generates NKey pairs or handles private key material (ADR-0054).

## Deployment

Runs as a Kubernetes Deployment in the `mclaude-system` namespace of the central `mclaude-cp` cluster, built from an Alpine-based container image. Listens on a single port: the main API port (default 8080) for all routes — public, authenticated, and admin. Admin endpoints are protected by `isAdminMiddleware`, which validates the caller's JWT via the standard auth middleware and then checks that the authenticated user has `is_admin=true` in Postgres.

### Two-Phase Deployment

Deployment is split into two phases with an explicit CI gate between them:

**Phase 1 — Control Plane:** First `helm upgrade --install mclaude-postgres` (or operator-supplied managed DB), then `helm upgrade --install mclaude-cp`. After this phase the `mclaude-system` namespace contains exactly 5 running pods. The cluster also has `kube-system` (CoreDNS, traefik, metrics-server, etc.), `cert-manager`, and `external-dns` — cluster infrastructure, not counted here. `mclaude-cp-nats-0` (NATS hub), `mclaude-postgres-0` (Postgres, owned by the `mclaude-postgres` release per ADR-0094), the CP pod, the SPA pod, and the MinIO pod. Two Jobs complete and exit: `init-keys` (generates NKeys, UPSERTs bootstrap admin user) and `minio-bucket-job` (creates S3 bucket).

**Phase 1 Postgres state:** `users` — one row (bootstrap admin, `is_admin=true`). `hosts`, `projects`, `agent_credentials` — all empty.

**Phase 1 NATS state:** `mclaude-hosts` KV bucket exists and is empty. No per-user KV buckets or streams (created lazily on first login). No leafnode connections.

**What works at Phase 1:** `POST /auth/login` (bootstrap admin can authenticate, gets JWT), `POST /admin/users` (admin can create additional users), `/readyz` (200), NKey challenge/verify, device-code flow, SPA loads with empty dashboard.

**What requires Phase 2:** Sessions (no controller to provision session-agent pods), NATS provisioning requests (published by CP but nothing subscribed), any `$SYS.ACCOUNT.*.CONNECT` events (no hosts have ever connected).

**Phase 2 — First Platform Host:** `scripts/register-platform-host.sh` authenticates as the bootstrap admin via `POST /auth/login`, then runs `mclaude host register` to create the first host. The host row is created with `public_key` and `user_jwt` populated. `$SYS.ACCOUNT.*.CONNECT` fires and the CP writes the host's entry to `mclaude-hosts` KV as `online=true`.

The `go test -tags integration` suite runs after Phase 1 as the gate before Phase 2 starts.

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `EXTERNAL_URL` | Yes | (none -- exits on startup if empty) | Externally-accessible base URL (e.g. `https://dev.mclaude.richardmcsong.com`) |
| `DATABASE_URL` / `DATABASE_DSN` | Yes | (none -- exits on startup if empty) | Postgres connection string. Hosts/users/projects persistence is required for ADR-0035. Per ADR-0094, the helm chart composes this at pod runtime as `postgres://{database.user}:$(POSTGRES_PASSWORD)@{database.host}:{database.port}/{database.name}?sslmode={database.sslmode}` from the `database.*` chart values. `POSTGRES_PASSWORD` is loaded via `valueFrom.secretKeyRef` against the Secret named in `database.passwordSecretRef` (key `postgres-password`). The Postgres instance itself runs in a separate `mclaude-postgres` Helm release (in-cluster) or is operator-supplied (managed DB). |
| `NATS_URL` | No | `nats://localhost:4222` | Internal hub NATS broker URL |
| `NATS_WS_URL` | No | (empty) | External WebSocket URL for browser clients; empty means client derives from origin |
| `NATS_ACCOUNT_SEED` | Yes (production) | (none) | Account NKey seed string. Used directly to sign per-host user JWTs and session-agent JWTs. In production Helm deployments, populated from the `operator-keys` Secret's `accountSeed` key. If not set, generates an ephemeral account key (dev-only — not suitable for production). |
| `OPERATOR_KEYS_PATH` | No | `/etc/mclaude/operator-keys` | Mount path for the `mclaude-system/operator-keys` Secret. At runtime, provides `operatorSeed` (for re-signing the account JWT during revocation) and `sysAccountSeed` (for publishing to `$SYS.REQ.CLAIMS.UPDATE`). Also used by the `init-keys` subcommand. |
| `NATS_SYS_ACCOUNT_SEED` | No | (read from `OPERATOR_KEYS_PATH/sysAccountSeed`) | System account NKey seed string. CP uses system account credentials to publish `$SYS.REQ.CLAIMS.UPDATE` for JWT revocation (ADR-0054). In production, populated from the `operator-keys` Secret's `sysAccountSeed` key. If set as an env var, takes precedence over the file-based path. |
| `BOOTSTRAP_ADMIN_EMAIL` | No | (empty) | Email of the bootstrap admin user. The `init-keys` Job UPSERTs a `users` row with `is_admin=true` and a bcrypt password hash from `BOOTSTRAP_ADMIN_PASSWORD` (per ADR-0094). Idempotent on every helm upgrade: `INSERT ... ON CONFLICT (email) DO UPDATE SET is_admin=true, password_hash = CASE WHEN $2 = '' THEN users.password_hash ELSE bcrypt($2) END`. |
| `BOOTSTRAP_ADMIN_PASSWORD` | No | (empty) | Password for the bootstrap admin user. Hashed with bcrypt by the `init-keys` Job and stored in `users.password_hash`. Required when `BOOTSTRAP_ADMIN_EMAIL` is set on a fresh cluster — if email is set but password is empty and no user with that email exists yet, `init-keys` exits non-zero. On subsequent helm upgrades when the user already exists, an empty `BOOTSTRAP_ADMIN_PASSWORD` preserves the existing hash; a non-empty value rotates it. |
| `PORT` | No | `8080` | Main API listen port |
| `JWT_EXPIRY_SECONDS` | No | `28800` (8h) | Per-host user JWT lifetime in seconds |
| `MIN_CLIENT_VERSION` | No | `0.0.0` | Minimum SPA/CLI version reported by `/version` |
| `SERVER_VERSION` | No | (empty) | Server version string reported by `/version` |
| `PROVIDERS_CONFIG_PATH` | No | `/etc/mclaude/providers.json` | Path to OAuth provider config (Helm ConfigMap mount) |
| `PROVIDER_SECRET_{ID}` | No | (none) | OAuth client secret for provider `{ID}` (uppercased, dashes→underscores, e.g. `PROVIDER_SECRET_GITHUB`). Read by `LoadProviders()` per-provider. Fallback: file at `/etc/mclaude/secrets/{clientSecretRef}/client-secret`. |
| `S3_ENDPOINT` | Yes (production) | (none) | S3-compatible storage endpoint URL (e.g. `https://s3.amazonaws.com` or MinIO URL for self-hosted). Required for import and attachment support (ADR-0053). |
| `S3_BUCKET` | Yes (production) | (none) | S3 bucket name. Single bucket with per-user/host/project key prefixes: `{uslug}/{hslug}/{pslug}/imports/{id}` and `{uslug}/{hslug}/{pslug}/attachments/{id}`. |
| `S3_ACCESS_KEY_ID` | Yes (production) | (none) | S3 access key ID for pre-signed URL generation. |
| `S3_SECRET_ACCESS_KEY` | Yes (production) | (none) | S3 secret access key for pre-signed URL generation. |
| `PROVISION_TIMEOUT_SECONDS` | No | `10` | Per-request timeout for NATS provisioning request/reply. Note: currently a hardcoded constant in code (`const ProvisionTimeoutSeconds = 10`), not yet read from env. |
| `LOG_LEVEL` | No | (default) | **Not read by Go code** — injected by the `mclaude-cp` Helm template but not consumed by the binary. Zerolog uses its default level. |
| `HELM_RELEASE_NAME` | No | (none) | **Not read by Go code** — injected by the `mclaude-cp` Helm template but not consumed by the control-plane binary. (The controller-k8s binary does read this variable for ConfigMap lookup.) |
| `METRICS_PORT` | No | (none) | **Not read by Go code.** `/metrics` is served on the main port (`PORT`), unauthenticated. |

## Interfaces

### HTTP Endpoints -- Main Port

**Public (no auth):**

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/auth/login` | Authenticate with email+password. Request body includes `nkey_public` (the user's NKey public key, generated client-side). Returns the [Login Response](../spec-state-schema.md#login-response-shape) — per-host user JWT, hub URL, host inventory, and projects. **No NKey seed in the response** — all identity types generate their own NKeys; CP never handles private key material (ADR-0054). CP stores the public key in `users.nkey_public` for future challenge-response auth. |
| `POST` | `/auth/refresh` | Exchange a valid per-host JWT from the Authorization header for a new JWT (same host scope). Returns `s.natsWsURL` (external WebSocket URL) for browser clients. |
| `POST` | `/api/auth/challenge` | Unified NKey challenge-response authentication — step 1 (ADR-0054). Request: `{nkey_public}`. CP looks up the public key across `users.nkey_public`, `hosts.public_key`, `agent_credentials.nkey_public` (first match wins, determines identity type). Returns `{challenge}` — a base64-encoded random nonce (32 bytes), single-use, 30s TTL, stored in-memory. Error: `NOT_FOUND` if public key is unknown. |
| `POST` | `/api/auth/verify` | Unified NKey challenge-response authentication — step 2 (ADR-0054, extended by ADR-0091). Request: `{nkey_public, challenge, signature, x25519_pub?}`. CP base64-decodes `challenge` to raw nonce bytes, then verifies the Ed25519 signature over those raw bytes against the stored public key (`nkeys.FromPublicKey(pub).Verify(rawNonce, sig)`). Lookup order: `users.nkey_public` → `hosts.public_key` → `agent_credentials.nkey_public` → `user_host_agents.nkey_public`. When the NKey resolves to a `user_host_agents` row, `x25519_pub` is **required** (base64, 32 bytes); CP stores it in `user_host_agents.x25519_pub` and selects bootstrap-vs-full JWT based on `host_grants.status` for that (host_slug, user_slug). For other identity types, `x25519_pub` is ignored. `issueJWTForNKey` invokes `IssueUserAgentBootstrapJWT` or `IssueUserAgentJWT` (ADR-0091) for user-agent identities, otherwise the existing user/host/session-agent paths. Returns `{ok, jwt}`. Errors: `UNAUTHORIZED` (invalid signature), `FORBIDDEN` (host revoked), `EXPIRED` (challenge expired), `INVALID_REQUEST` (user-agent NKey without `x25519_pub`). |
| `POST` | `/api/auth/device-code` | Initiate device-code login flow for CLI (ADR-0053). Returns `{deviceCode, userCode, verificationUrl, expiresIn, interval}`. Stores pending auth in memory with 15-minute TTL. |
| `POST` | `/api/auth/device-code/poll` | CLI polls with device code. Returns `{status: "pending"}` while waiting, or `{status: "authorized", jwt, userSlug, natsUrl}` once the user completes verification. `natsUrl` is the NATS WebSocket URL from `s.natsWsURL` — omitted when empty (client derives from control-plane origin). Returns 410 Gone if expired. |
| `GET` | `/api/auth/device-code/verify` | Web UI endpoint where user enters the device code and authenticates. Serves the verification page — user enters code, authenticates, and the pending device-code record is completed. |
| `GET` | `/version` | Returns `minClientVersion` and `serverVersion` |
| `GET` | `/health` | Returns 200 OK (process alive check — never checks NATS, so pod stays alive for break-glass admin port) |
| `GET` | `/healthz` | Kubernetes liveness probe (same as `/health` — never checks NATS) |
| `GET` | `/readyz` | Kubernetes readiness probe. Checks Postgres connectivity — returns 503 when DB is unreachable so the pod stops receiving traffic. NATS outage does not mark pod unready. |
| `GET` | `/auth/providers/{id}/callback` | OAuth callback -- exchanges code for token, stores connection, redirects browser |

**Protected (per-host NATS JWT or admin bearer token in Authorization header):**

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/auth/me` | Returns authenticated user info and connected OAuth providers |
| `GET` | `/api/providers` | Lists admin-configured OAuth providers (from Helm) |
| `POST` | `/api/providers/pat` | Adds a Personal Access Token connection (auto-detects GitHub or GitLab) |
| `POST` | `/api/providers/{id}/connect` | Initiates OAuth flow for an admin-configured provider; returns `{redirectUrl}` |
| `GET` | `/api/connections/{id}/repos` | Lists repositories accessible via a connected provider |
| `DELETE` | `/api/connections/{id}` | Disconnects a provider (revokes token, removes secrets, deletes DB row) |
| `POST` | `/api/attachments/upload-url` | Request pre-signed S3 upload URL for an attachment (ADR-0053). Request: `{filename, mimeType, sizeBytes, projectSlug, hostSlug}`. CP validates user owns the project, generates S3 key (`{uslug}/{hslug}/{pslug}/attachments/{id}`), creates an `attachments` row with `confirmed=false`, signs upload URL (5-min TTL). Returns `{id, uploadUrl}`. Rejects if `sizeBytes` exceeds configurable limit (default 50 MB). |
| `POST` | `/api/attachments/{id}/confirm` | Confirm attachment upload (ADR-0053). CP verifies the S3 object exists, sets `attachments.confirmed=true`. Records metadata (filename, mimeType, sizeBytes). |
| `GET` | `/api/attachments/{id}` | Get attachment download URL (ADR-0053). CP validates requester owns the project, returns `{id, filename, mimeType, sizeBytes, downloadUrl}` with a pre-signed S3 download URL (5-min TTL). |
| `PATCH` | `/api/projects/{id}` | Updates a project's `gitIdentityId` |
| `GET` | `/api/users/{uslug}/grants` | ADR-0091. Lists pending and accepted `host_grants` for the user. Each entry includes `{hostSlug, status, createdAt, acceptedAt?, x25519Fingerprint?}` where the fingerprint is `sha256(user_host_agents.x25519_pub)` (hex), present only after the user-agent has registered. SPA shows "Host X granted you access — fingerprint Y" so the user can pin trust before accepting. Auth: self (`uslug` must match JWT subject; admin bypass per existing middleware). |
| `POST` | `/api/users/{uslug}/hosts/{hslug}/grant/accept` | ADR-0091. Body: `{x25519_fingerprint}` (hex). CP looks up `user_host_agents.x25519_pub` for `(hslug, uslug)`, computes `sha256(pub)`, compares to the supplied fingerprint. Match → sets `host_grants.status='accepted'`, `accepted_at=NOW()`, publishes `{}` (or `{"status":"accepted"}`) to `mclaude.users.{uslug}.hosts.{hslug}.agent.activate`; returns 204. Mismatch → 409 with `{error:"fingerprint_mismatch"}`. Pubkey not yet registered → 404 with `{error:"agent_not_registered"}`. Auth: self. |
| `GET` | `/api/users/{uslug}/hosts/{hslug}/user-agent/pubkey` | ADR-0091. Returns `{x25519_pub}` (base64) for the user-agent serving (uslug, hslug). SPA reads this to perform `crypto_box_seal(token, x25519_pub)` before publishing to `.agent.token-delivery`. 404 if `user_host_agents.x25519_pub` is NULL (agent not yet registered). Auth: self. |
| `GET` | `/api/users/{uslug}/hosts` | Lists hosts owned by or granted to the user. |
| `POST` | `/api/users/{uslug}/hosts` | Creates a machine host directly (`{slug, name, publicKey?}`). Bypasses the device-code flow. Inserts a `hosts` row with `type='machine'`, `role='owner'`, mints a per-host user JWT, returns host response. |
| `POST` | `/api/users/{uslug}/hosts/code` | Generates a 6-character device code for BYOH host registration. Accepts `{publicKey}` — the host's NKey public key, generated locally by the CLI. The server stores the public key with the code record. Returns `{code, expiresAt}` (10-minute TTL). |
| `GET` | `/api/users/{uslug}/hosts/code/{code}` | Polls device-code status. Returns `{status: "pending"}` while waiting, or `{status: "completed", slug, jwt, hubUrl}` once redeemed. Returns 410 Gone if expired, 404 if not found. **Known gap:** pending response does not include `expiresAt` — the expiry is stored server-side but not returned to the CLI. |
| `POST` | `/api/hosts/register` | **Public (no JWT required)** — Redeems a device code with `{code, name}`. The device code (generated by an authenticated user via `POST /api/users/{uslug}/hosts/code`) serves as the authorization credential. Creates a `hosts` row, mints a per-host user JWT, returns `{slug, jwt, hubUrl}`. |
| `PUT` | `/api/users/{uslug}/hosts/{hslug}` | Updates host display name. |
| `DELETE` | `/api/users/{uslug}/hosts/{hslug}` | Removes a host (cascades to its projects + sessions). For cluster hosts owned by other users, only the registering admin can delete. |

**HTTP project CRUD:**

HTTP handlers for project CRUD are wired and functional. `POST` (create) inserts a Postgres row but is incomplete — it does not write to KV, send a provisioning request, or broadcast `projects.updated` (see CP-6 in ADR-0052).

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/users/{uslug}/projects` | Creates a project on a specified host. Requires `hostSlug` in request body. Returns 422 `{error: "no_hosts"}` if the user has zero registered hosts. Returns 422 `{error: "host_required"}` if the user has multiple hosts and `hostSlug` is omitted. If the user has exactly one host and `hostSlug` is omitted, auto-selects it (ADR-0095). |
| `GET` | `/api/users/{uslug}/projects` | Lists all projects for the user. |
| `GET` | `/api/users/{uslug}/projects/{pslug}` | Gets a single project by slug. |
| `DELETE` | `/api/users/{uslug}/projects/{pslug}` | Deletes a project. |

**SCIM 2.0 (IdP user provisioning) — not yet implemented:**

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/scim/v2/Users` | IdP provisions user. **Not yet implemented.** |
| `PUT` | `/scim/v2/Users/{id}` | IdP updates user. **Not yet implemented.** |
| `DELETE` | `/scim/v2/Users/{id}` | IdP deprovisions user. **Not yet implemented.** |
| `GET` | `/scim/v2/Users` | IdP syncs user list. **Not yet implemented.** |

**Admin-only (`isAdminMiddleware` — valid JWT where `users.is_admin = true`):**

Admin routes are served on the main port (8080). `isAdminMiddleware` calls the standard JWT auth middleware first, then fetches the user from Postgres and checks `is_admin = true`. Returns 401 if no JWT, 403 if JWT valid but user is not admin.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/metrics` | Prometheus metrics (unauthenticated — internal scraping) |
| `POST` | `/admin/clusters` | Registers a new cluster (`{slug, name, jsDomain, leafUrl, directNatsUrl?, defaultPlatform?}`). Generates a per-cluster NKey pair, creates a `hosts` row owned by the calling admin user, mints a per-cluster leaf/controller JWT scoped to `mclaude.users.*.hosts.{slug}.>`, returns `{slug, leafJwt, leafSeed, jsDomain, directNatsUrl}`. The `defaultPlatform: bool` field (ADR-0091, default `false`) sets `hosts.is_default_platform=true`; the partial unique index ensures at most one default platform globally — collisions return 409 with `{error:"default_platform_exists"}`. **Known bug:** response omits `accountJwt`/`operatorJwt` (fields exist but unpopulated). |
| `GET` | `/admin/clusters` | Lists registered clusters (deduplicated across user rows). |
| `POST` | `/admin/clusters/{cslug}/grants` | Grants user access (`{userSlug}`). Creates a new `hosts` row for that user with `public_key = NULL`; copies `direct_nats_url` from the cluster owner row; mints a per-user JWT scoped to `mclaude.users.{userSlug}.hosts.{cslug}.>`. Idempotent (`ON CONFLICT DO NOTHING`). |
| `DELETE` | `/admin/clusters/{cslug}` | Removes the cluster. **Not yet implemented** — deferred per ADR-0035. |
| `POST` | `/admin/users` | Creates a user (`{id, email, name, password?, isAdmin?}`). Returns `{id, email, name, slug}`. |
| `POST` | `/admin/users/{uslug}/promote` | Sets `users.is_admin = true`. |
| `GET` | `/admin/users` | Lists all users. |
| `DELETE` | `/admin/users/{id}` | Deletes a user from Postgres (cascades to hosts, projects, oauth_connections). **Known gap:** does not revoke the user's NATS JWT or publish NATS delete requests to controllers — orphaned K8s resources persist until manual cleanup. |
| `POST` | `/admin/sessions/stop` | Break-glass session stop. Publishes a sessions.delete NATS message to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.delete`. Returns 202 Accepted (fire-and-forget). |

### NATS Subjects

Per ADR-0035 the control-plane communicates with controllers over host-scoped NATS subjects on the hub. For the full subject catalog see `spec-state-schema.md` -- NATS Subjects.

**Publishes (request/reply, 10s timeout):**

| Subject | Trigger | Description |
|---------|---------|-------------|
| `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision` | `POST /api/users/{uslug}/projects` or NATS project creation | ADR-0091. Replaces the old `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create` transplant. After CP creates the Postgres row + KV entry, it publishes to the **user-agent** in the user's namespace on the host. Payload: `{projectId, projectSlug, gitUrl, gitIdentityId}`. (The `backend` field listed in earlier drafts is deferred — v1 hardcodes the Anthropic backend; multi-backend selection per-project is a follow-up.) User-agent owns the pod creation step. Cluster vs machine host distinction collapses — cluster path goes through user-agent. |
| `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.delete` | Project delete | ADR-0091. Replaces the old `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.delete`. Best-effort — if user-agent is offline, the event is missed and the pod persists until manual `kubectl delete deployment`. v1 does not retroactively reconcile (see ADR-0091 Deferred). Payload: `{projectId, projectSlug, event: "deleted"}`. |
| `mclaude.users.{uslug}.hosts.{hslug}.agent.activate` | `POST /api/users/{uslug}/hosts/{hslug}/grant/accept` (or auto-accept on default platform) | ADR-0091. Signal-only payload (`{}` or `{"status":"accepted"}`). User-agent receives the signal, re-runs HTTP challenge-response, and obtains a full JWT (since `host_grants.status='accepted'` now). No JWT in the payload — the activation channel is unprivileged. |
| `mclaude.users.{uslug}.hosts.{hslug}.api.projects.update` | `PATCH /api/projects/{id}` | Asks the controller to apply project metadata changes (e.g., git identity rotation). |

**Subscribes:**

| Subject | Description |
|---------|-------------|
| `$SYS.ACCOUNT.{accountKey}.CONNECT` | Per-connection event. Switch on payload `client.kind`: `Client` → `SELECT * FROM hosts WHERE public_key = client.nkey LIMIT 1` (no type filter — matches both `type='machine'` and `type='cluster'`), update `last_seen_at` for the matched row, upsert `mclaude-hosts` KV `{hslug}` with `online=true`. No match → ignore (covers SPA's per-login ephemeral NKey and control-plane's own connection). The `Leafnode` branch is removed — all host controllers connect hub-direct as `"Client"` per ADR-0063. |
| `$SYS.ACCOUNT.{accountKey}.DISCONNECT` | Same lookup logic; sets `mclaude-hosts` KV `online=false` for the matched row(s). Does not rewrite `last_seen_at` (it tracks last-known-online). |
| `mclaude.hosts.{hslug}.api.agents.register` | Session-agent public key registration (ADR-0054). Host controllers register spawned agent NKey public keys here. Request: `{user_slug, project_slug, nkey_public}`. CP validates host access + project ownership + host assignment, then stores `(user_id, host_slug, project_slug) → nkey_public` in the `agent_credentials` table. Returns `{ok: true}`. Errors: `FORBIDDEN` (user does not have access to host), `NOT_FOUND` (project not assigned to host). The agent then authenticates itself via HTTP challenge-response to get its per-project JWT. |
| `mclaude.hosts.{hslug}.api.agents.user-agent.register` | ADR-0091. Host controller registers the user-agent NKey public key for `(uslug, hslug)`. Request: `{uslug, nkey_public}`. CP validates that a `host_grants` row exists for `(host_slug, user_slug)` — rejects with `FORBIDDEN` if the host hasn't granted this user access (prevents a host from registering agents for arbitrary users). Upserts `user_host_agents.nkey_public` for `(host_slug, user_slug)`. The user-agent itself authenticates via HTTP challenge-response on startup and supplies `x25519_pub` at that step. Returns `{ok: true}`. |
| `mclaude.hosts.{hslug}.grants.create` | ADR-0091. Host controller (or CP itself, on behalf of the default platform) publishes `{uslug}` to grant a user access. CP upserts `host_grants` with `status='pending'` (or `'accepted'` if this is the default platform's auto-grant). Returns `{ok: true}`. CP is a directory — it does not approve or deny; it simply records the host's intent so the user can see and accept. |
| `mclaude.users.*.hosts._.register` | Host registration (ADR-0054). User's CLI publishes `{name, type, nkey_public}`. CP creates host in Postgres (slug, name, type, user_id, public_key). Returns `{ok, slug}` — **no JWT** in the response; the host authenticates itself via HTTP challenge-response. The `_` sentinel in the hslug position cannot collide with real slugs (slugs are `[a-z0-9-]+`). |
| `mclaude.users.*.hosts.*.manage.grant` | Host access grant (ADR-0054). Request: `{userSlug}`. CP validates the publisher is the host owner, inserts `(host_id, user_id)` into `host_access` table, revokes the grantee's current NATS JWT (host list changed). Returns `{ok: true}`. |
| `mclaude.users.*.hosts.*.manage.revoke-access` | Host access revocation (ADR-0054). Request: `{userSlug}`. CP validates ownership, deletes `(host_id, user_id)` from `host_access`, revokes the grantee's NATS JWT + all agent JWTs for grantee's projects on that host. Active sessions on the host are terminated. |
| `mclaude.users.*.hosts.*.manage.deregister` | Host deregistration (ADR-0054). Authorization: host owner or platform operator. Drains all active projects (publishes delete for each), revokes host credential (adds to NATS revocation list), deletes host row from Postgres, tombstones `$KV.mclaude-hosts.{hslug}`, removes all stored agent NKey public keys for the host. |
| `mclaude.users.*.hosts.*.manage.revoke` | Emergency credential revocation (ADR-0054). Adds host JWT + all agent JWTs for sessions on that host to the NATS revocation list (immediate disconnect). Marks host as `online: false, status: "revoked"`. Re-activation requires a new `mclaude host register`. |
| `mclaude.users.*.hosts.*.manage.rekey` | Rotate host NKey public key (ADR-0054). Owner-only. Updates stored public key in `hosts.nkey_public`. The old JWT becomes useless (NATS nonce challenge fails against the new key). |
| `mclaude.users.*.hosts.*.manage.update` | Update host display name or type metadata. |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.request` | Import upload URL request (ADR-0053). CLI requests pre-signed S3 upload URL for session archive. Request: `{slug, sizeBytes}`. CP validates user auth, generates S3 key (`{uslug}/{hslug}/{pslug}/imports/{id}`), signs upload URL (5-min TTL). Returns `{importId, uploadUrl}`. Rejects if `sizeBytes` exceeds limit (default 500 MB). |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.confirm` | Import upload confirmation (ADR-0053). CLI signals upload is complete. Request: `{importId}`. CP validates upload exists in S3, creates project in Postgres with `source: "import"` and `import_ref`, writes `ProjectKVState` with `importRef`, dispatches provisioning to controller. |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.download` | Import download URL request (ADR-0053). Session-agent requests pre-signed S3 download URL for archive. CP validates requester (agent) owns the project. Returns `{downloadUrl}`. |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.complete` | Import completion signal (ADR-0053). Session-agent signals archive unpack is done. Standard envelope payload `{id, ts}`. CP deletes the S3 object. |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.download` | Attachment download URL (ADR-0053). Agent sends `{id}`, CP validates project ownership, returns `{downloadUrl}` (pre-signed S3 URL, 5-min TTL). |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.upload` | Attachment upload URL (ADR-0053). Agent sends `{filename, mimeType, sizeBytes}`, CP returns `{id, uploadUrl}` (pre-signed S3 URL, 5-min TTL). |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.confirm` | Attachment upload confirmation (ADR-0053). Agent sends `{id}`, CP records metadata in `attachments` table (sets `confirmed=true`). |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.check-slug` | Project slug availability check (ADR-0053). CLI sends `{slug}`. CP checks uniqueness of the slug per-user-per-host against Postgres. Returns `{available: true}` if the slug is free, or `{available: false, suggestion: "slug-2"}` with a suggested alternative if taken. |

Note: The control-plane also subscribes to `mclaude.users.*.api.projects.create` (a user-scoped NATS subject without the host segment) for backward-compatible NATS-based project creation from the SPA. The handler requires a registered host: returns `{error: "no_hosts"}` if the user has zero hosts, resolves `hostSlug` from the payload (auto-selects if user has exactly one host, returns `{error: "host_required"}` if multiple hosts and no `hostSlug`). `host_id` is always set on INSERT (ADR-0095). **Known bug:** `CreateProjectWithIdentity` does not compute or set the `slug` column on INSERT — new projects get `slug=''` until the next startup migration backfill runs.

### NATS KV Buckets and JetStream Streams

The control-plane manages two categories of JetStream resources:

**Shared (created on deployment startup):**

- **`mclaude-hosts`** -- shared KV bucket; created by `ensureHostsKV`. Control-plane is the sole writer. Writes are driven by `$SYS.ACCOUNT.*.CONNECT/DISCONNECT` events — a host entry is written only after the host's NKey authenticates to NATS. Read access is per-host in user JWTs (each user's JWT lists explicit host slugs they can read — NATS enforces host visibility).

**Per-user (created on user registration / first login):**

- **`mclaude-sessions-{uslug}`** -- per-user KV bucket for session state. Created by CP on user registration. Writes are handled by `mclaude-session-agent`. Replaces the shared `mclaude-sessions` bucket (ADR-0054).
- **`mclaude-projects-{uslug}`** -- per-user KV bucket for project state. Created by CP on user registration. CP writes on project creation and updates. Replaces the shared `mclaude-projects` bucket (ADR-0054).
- **`mclaude-user-info-{uslug}`** -- per-user, **client-only** KV bucket for tokens and preferences (ADR-0091). Created by CP on user registration. CP has full NATS access but never writes — tokens are written exclusively by SPA/CLI. JetStream at-rest encryption (`store_key`) required.
- **`MCLAUDE_SESSIONS_{uslug}`** -- per-user JetStream stream consolidating events, commands, and lifecycle (ADR-0054). Created by CP on user registration. Captures `mclaude.users.{uslug}.hosts.*.projects.*.sessions.>`. Config: `LimitsPolicy`, `MaxAge: 30d`, `FileStorage`. Replaces the shared `MCLAUDE_EVENTS`, `MCLAUDE_API`, `MCLAUDE_LIFECYCLE` streams.

If a per-user bucket or stream is missing at runtime (e.g., after a restore), the control-plane creates it on demand.

**Removed:** `mclaude-job-queue` (eliminated per ADR-0044 — quota-managed sessions use the session KV with extended fields). `mclaude-clusters`, `mclaude-laptops`, and `mclaude-heartbeats` removed per ADR-0035.

### Postgres

Manages the `users`, `projects`, `hosts`, `host_access`, `agent_credentials`, `user_host_agents`, `host_grants`, `attachments`, and `oauth_connections` tables (ADR-0054, ADR-0053, ADR-0091). Schema is applied on startup via idempotent DDL (`CREATE TABLE IF NOT EXISTS`, `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`). For full schema, see `spec-state-schema.md` -- Postgres.

**User slug derivation (ADR-0024, ADR-0062):** `computeUserSlug(email)` slugifies the full email address — lowercase, replace all non-`[a-z0-9]` runs with `-`, trim leading/trailing `-`, truncate to 63 chars. Examples: `dev@mclaude.local` → `dev-mclaude-local`, `richard.song@gmail.com` → `richard-song-gmail-com`. The full domain is included (not just the first segment) to prevent collisions between users on different domains. SQL backfill applies the same algorithm to existing rows.

Key tables added by ADR-0054/0053:
- **`host_access`** — `(host_id, user_id)` composite PK. Tracks per-user access grants to hosts. The host owner has implicit access (not stored here).
- **`agent_credentials`** — `(user_id, host_slug, project_slug) → nkey_public`. One active credential per user/host/project. Used for HTTP challenge-response auth. Cleaned up on deprovision.
- **`attachments`** — Attachment metadata for S3-stored binary data. Tracks upload confirmation state for garbage collection of abandoned uploads.

### Kubernetes Dependency

The control-plane has **no** K8s client at runtime and creates **no** K8s resources during normal operation. All MCProject CR creation, namespace provisioning, PVC management, RBAC reconciliation, and pod template construction live in `mclaude-controller-k8s` (see `docs/mclaude-controller-k8s/spec-k8s-architecture.md`). The control-plane reaches the controller via NATS request/reply on two subject patterns:

- **ADR-0054 host-scoped (primary, used by dev-seed):** `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create` — CP-initiated fan-out provisioning.
- **Legacy ADR-0035 user-scoped (SPA backward compat):** `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` — retained for SPA-initiated project creation.

The control-plane binary also doubles as two Helm pre-install Job entrypoints (the only code paths that use `client-go`):
- **`control-plane init-keys`** — Two independent steps with separate idempotence (per ADR-0094):
  1. **Key generation**: writes operator + account NKey pairs + JWTs to the `operator-keys` Secret in `mclaude-system`. Skipped if the Secret already exists. This step's early-return is scoped — it does NOT short-circuit step 2.
  2. **Bootstrap admin UPSERT**: when `BOOTSTRAP_ADMIN_EMAIL` is set, runs `INSERT INTO users (email, is_admin, password_hash, ...) VALUES ($1, true, bcrypt($2), ...) ON CONFLICT (email) DO UPDATE SET is_admin=true, password_hash = CASE WHEN $2 = '' THEN users.password_hash ELSE EXCLUDED.password_hash END`. Runs **unconditionally on every `helm upgrade`**, regardless of whether keys were just generated or already existed — the only gate is `BOOTSTRAP_ADMIN_EMAIL`.

  Per ADR-0094 Postgres is in a separately-installed `mclaude-postgres` release that runs before `mclaude-cp`, so the admin UPSERT step always finds the DB reachable. Failure modes: if the Secret named by `database.passwordSecretRef` is missing, the Job's pod stays in `CreateContainerConfigError`; if Postgres is unreachable, the binary exits non-zero on `db ping` and Helm aborts the install. Run by the `mclaude-cp` chart's pre-install Job. Env vars: `NAMESPACE` (default `mclaude-system`), `OPERATOR_KEYS_SECRET` (default `operator-keys`), `DATABASE_URL` (composed by the chart from `database.*` values), `BOOTSTRAP_ADMIN_EMAIL` / `BOOTSTRAP_ADMIN_PASSWORD`.
At runtime, the control-plane mounts one K8s Secret as a file:
- `mclaude-system/operator-keys` — mounted at `OPERATOR_KEYS_PATH` (default `/etc/mclaude/operator-keys`). Read-only. Provides `operatorJwt`, `accountJwt`, `accountSeed`, `operatorSeed`, and `sysAccountSeed` for signing per-host user JWTs and publishing JWT revocations to `$SYS.REQ.CLAIMS.UPDATE` (ADR-0054). The `gen-leaf-creds` subcommand (which generated worker NATS leaf credentials) was removed by ADR-0063.

## Internal Behavior

### Startup Sequence

1. Connects to Postgres (fatal exit on failure) and runs idempotent schema migration including the ADR-0035 `hosts` table and `projects.host_id` column.
2. Loads OAuth provider config from `PROVIDERS_CONFIG_PATH`, resolving client secrets from K8s Secrets.
3. Loads the account signing key from `NATS_ACCOUNT_SEED` env var. If set, parsed directly as an NKey seed. If not set, generates an ephemeral account key (dev-only — not suitable for production; subjects will not match production JWTs). Fatal exit if signing fails — control-plane cannot mint JWTs without a valid account key. Also loads the operator seed and system account seed from `OPERATOR_KEYS_PATH` (or `NATS_SYS_ACCOUNT_SEED` env var) for JWT revocation support (ADR-0054). Caches the current account JWT in memory for revocation modifications.
4. Creates the HTTP server with all route handlers.
5. Connects to hub NATS (retry on failure, unlimited reconnects).
6. Ensures the shared `mclaude-hosts` KV bucket exists — it must exist before the `$SYS` subscriber starts so CONNECT events can write to it immediately. Per-user KV buckets (`mclaude-sessions-{uslug}`, `mclaude-projects-{uslug}`) and the per-user sessions stream (`MCLAUDE_SESSIONS_{uslug}`) are created on user registration / first login, not at startup (ADR-0054).
7. Subscribes to `$SYS.ACCOUNT.*.CONNECT` and `$SYS.ACCOUNT.*.DISCONNECT` for host liveness. Also subscribes to host lifecycle subjects (`mclaude.users.*.hosts.*.manage.>`, `mclaude.users.*.hosts._.register`), session-agent registration (`mclaude.hosts.*.api.agents.register`), user-agent registration (`mclaude.hosts.*.api.agents.user-agent.register` — ADR-0091), host grants (`mclaude.hosts.*.grants.create` — ADR-0091), and import/attachment NATS handlers.
8. Starts the GitLab token refresh goroutine (every 15 minutes).
9. Starts the main HTTP listener on `PORT` (default 8080).

The bootstrap admin is a normal user with `is_admin=true`. The `init-keys` Job UPSERTs the row before the CP pod starts: email from `BOOTSTRAP_ADMIN_EMAIL`, bcrypt password hash from `BOOTSTRAP_ADMIN_PASSWORD` (if set), `is_admin=true`. Per ADR-0094 the SQL is `INSERT INTO users (email, is_admin, password_hash, ...) VALUES ($1, true, $hash, ...) ON CONFLICT (email) DO UPDATE SET is_admin = true, password_hash = CASE WHEN $passwordEmpty THEN users.password_hash ELSE EXCLUDED.password_hash END`. Idempotent on every helm upgrade — if the user already exists, promotes them to admin and rotates the password only when `BOOTSTRAP_ADMIN_PASSWORD` is non-empty. Per-user JetStream resources are created lazily on first login, same as any other user.

### Authentication and JWT Signing

Per ADR-0054, all identity types (user, host, agent) generate their own NKey pairs — the control-plane **never generates NKey pairs or handles private key material**. CP receives only public keys and returns signed JWTs.

**User login:** Login validates email and bcrypt password hash (or OAuth identity) against Postgres. The login request body includes `nkey_public` — the user's NKey public key generated client-side (SPA stores seed in `localStorage`; CLI stores seed in `~/.mclaude/auth.json`). On success, the control plane:

1. Stores the public key in `users.nkey_public` (for future challenge-response auth).
2. Loads the calling user's hosts: owned hosts (`SELECT … FROM hosts WHERE user_id = ?`) plus granted hosts (`SELECT … FROM host_access WHERE user_id = ?`).
3. Resolves the list of host slugs the user has access to (derived from owned + granted hosts).
4. Issues a user JWT (`IssueUserJWT(publicKey, userSlug, hostSlugs, accountKP, expirySecs)`) signed by the account signing key. `claims.Name = userID` (UUID). Permissions per ADR-0054 Full Permission Specifications — explicit, per-user-resource allow-lists referencing `KV_mclaude-sessions-{uslug}`, `KV_mclaude-projects-{uslug}`, `MCLAUDE_SESSIONS_{uslug}`, and per-host entries for the shared `mclaude-hosts` KV bucket. No `$JS.API.>` wildcards.
5. JWT lifetime is `JWT_EXPIRY_SECONDS` (default 8h). **No NKey seed in the response** — the client already has its seed.
6. The Login Response payload is returned: `{user, jwt, hubUrl, hosts[], projects[]}`.

**Challenge-response auth (all identity types):** The `POST /api/auth/challenge` + `POST /api/auth/verify` endpoints provide a unified authentication path for users, hosts, and agents (ADR-0054). Bootstrap and refresh use the same code path:
1. Entity calls `POST /api/auth/challenge {nkey_public}` → receives `{challenge}` (random nonce, 30s TTL).
2. Entity signs challenge nonce with its NKey seed.
3. Entity calls `POST /api/auth/verify {nkey_public, challenge, signature}` → CP verifies signature, resolves identity type (lookup order: `users.nkey_public` → `hosts.public_key` → `agent_credentials.nkey_public`), resolves current permissions, signs JWT, returns `{ok, jwt}`.

**Agent JWT issuance:** Session-agent JWTs are issued exclusively by the control-plane (ADR-0054). Host controllers no longer hold the account signing key. The flow: agent generates NKey at startup → host controller registers public key via NATS (`mclaude.hosts.{hslug}.api.agents.register`) → agent authenticates via HTTP challenge-response → receives per-project scoped JWT (5-min TTL). Agent JWTs are scoped to one user + one host + one project (`SessionAgentSubjectPermissions(uslug, hslug, pslug)`).

**Host JWT issuance:** Hosts register via NATS (`mclaude.users.{uslug}.hosts._.register`) with their NKey public key. No JWT in the registration response — the host authenticates via HTTP challenge-response. Host JWTs are scoped to `mclaude.hosts.{hslug}.>` only — zero JetStream access (ADR-0054). TTL: 5 minutes. Host controllers refresh via the same challenge-response flow before expiry.

**User-agent JWT issuance (ADR-0091):** The user-agent runs in `mclaude-{uslug}` on a host. Its NKey public key is registered by the host controller via `mclaude.hosts.{hslug}.api.agents.user-agent.register` (gated by an existing `host_grants` row for the same `(host, user)`). The user-agent authenticates via HTTP challenge-response on startup, supplying both the NKey signature and its X25519 public key in `POST /api/auth/verify`. CP stores `x25519_pub` in `user_host_agents` and selects which JWT to issue based on `host_grants.status`:

- `pending` → `IssueUserAgentBootstrapJWT` — Sub on `mclaude.users.{uslug}.hosts.{hslug}.agent.activate` only, plus `_INBOX.>`. No JetStream/KV. 5-minute TTL.
- `accepted` → `IssueUserAgentJWT` — Pub+Sub on `mclaude.users.{uslug}.hosts.{hslug}.agent.>` and `_INBOX.>`. No JetStream/KV. 5-minute TTL.

Single auth code path: the user-agent always re-runs challenge-response when its JWT nears expiry or when it receives the activation signal. Whatever JWT matches the current grant status is what gets issued. There is no separate "activation" credential — `.agent.activate` carries only an empty/status signal, never a JWT.

**User creation does not insert any host row** (ADR-0095). Hosts are only created through the BYOH attestation flow (`mclaude.users.*.hosts._.register` or device-code registration). Users start with zero hosts and must register one before creating projects.

**Per-user resource provisioning:** On every successful login (NKey path or legacy password path) and on every challenge-response verify, CP calls `ensureUserResources(nc, uslug)` which idempotently provisions four per-user JetStream resources: KV buckets `mclaude-sessions-{uslug}`, `mclaude-projects-{uslug}`, `mclaude-user-info-{uslug}` (ADR-0091), and stream `MCLAUDE_SESSIONS_{uslug}` (ADR-0054). Each step existence-checks before create, so re-running on every login is a no-op once the resources exist.

The chain is **non-short-circuiting** (per ADR-0098): each ensure-step's error is logged at WARN with the resource name and error string, but does not abort subsequent steps. The function returns the first observed error after all four steps have been attempted. This means:
- A transient failure in step N does not block steps N+1..4 — partial progress is preserved.
- A user who logged in via a legacy code path that previously didn't provision all four resources gets the missing ones backfilled on their next login.
- If `ensureUserResources` returns an error to its caller (login handler, challenge-response handler), the **login response still proceeds normally** — provisioning is best-effort and missing resources retry on the next login. The caller logs the error at WARN with the user slug.
- If NATS is fully unreachable at provisioning time, all four steps fail; login still proceeds; resources backfill on the next login when NATS is reachable.

**Default-platform auto-grant is invoked idempotently on every user JWT issuance** (not only on first user creation): `issueJWTForNKey` queries `SELECT slug FROM hosts WHERE is_default_platform = true LIMIT 1`; if a default platform exists, it `UpsertHostGrant`s a row `(host_slug=<default>, user_slug, status='accepted', accepted_at=NOW())` and publishes to `mclaude.hosts.{<default>}.grants.create` on behalf of the host. `UpsertHostGrant` uses `ON CONFLICT DO NOTHING` so re-fires are no-ops. The host controller spins up the user-agent + namespace on first delivery; subsequent fires are idempotent. Once the user-agent registers and authenticates, CP issues a full JWT immediately.

The auth middleware on protected routes decodes and validates the JWT against the account public key, extracts the user UUID into the request context, and enforces access boundaries: returns 403 Forbidden when the JWT subject (user slug) does not match the URL `{uslug}` parameter. Admin users bypass the slug-match check. Admin routes additionally require `isAdminMiddleware`: after JWT validation, the user's `is_admin` flag is fetched from Postgres; 403 Forbidden is returned if false. Admin = a normal user with `is_admin=true` — no separate token or port.

### JWT Revocation

Per ADR-0054, the control-plane uses the NATS full resolver (`resolver: nats`) and system account credentials to revoke JWTs at runtime. This mechanism is used by host access change propagation, emergency credential revocation (`manage.revoke`), access revocation (`manage.revoke-access`), and host deregistration (`manage.deregister`).

**Credentials:** CP loads the operator seed from `OPERATOR_KEYS_PATH` (`operatorSeed`) and the system account seed from `OPERATOR_KEYS_PATH` (`sysAccountSeed`) or the `NATS_SYS_ACCOUNT_SEED` env var. The operator seed is used to re-sign the account JWT after adding revocation entries. The system account seed is used to authenticate the `$SYS.REQ.CLAIMS.UPDATE` publish — only system account credentials have permission to update account claims at runtime.

**Revocation flow:**
1. CP decodes the current account JWT (cached in memory, refreshed on startup).
2. CP adds the target identity's NKey public key to the account JWT's `Revocations` map with a timestamp (`jwt.TimeRange` — revokes all JWTs issued before `now`).
3. CP re-signs the account JWT with the operator key.
4. CP publishes the updated account JWT to `$SYS.REQ.CLAIMS.UPDATE` using system account credentials.
5. NATS server processes the update, immediately closes connections whose JWT was issued before the revocation timestamp.
6. Revocation entries auto-expire after the revoked JWT's remaining TTL (max 8h for users, 5 min for hosts/agents). CP does NOT need to clean up old entries.

**Fallback without revocation:** Even without working revocation, credentials expire naturally (5 min for hosts/agents, ~8h for users). The 60s SPA refresh cycle and 5-min TTL for hosts/agents bound the window of stale credentials. Revocation provides sub-second propagation; TTL expiry is the backstop.

### OAuth Provider Integration

Supports GitHub and GitLab providers configured via Helm (`providers.json`). OAuth flows use an in-memory state store with 10-minute TTL and cryptographic random state tokens. On successful callback, the control-plane stores connection metadata in the `oauth_connections` Postgres table and publishes a NATS message on `mclaude.users.{uslug}.hosts.{hslug}.api.projects.update` so `mclaude-controller-k8s` (cluster hosts) can refresh the per-user `user-secrets` Secret with the new tokens. For BYOH machines, the host controller receives the same message and writes credentials into the local credential helpers (`~/.mclaude/projects/{pslug}/.credentials/`).

PAT connections bypass OAuth and auto-detect provider type by probing GitHub and GitLab API endpoints.

After any connection change (connect, disconnect, token refresh), the control-plane re-publishes the update message so `gh-hosts.yml` and `glab-config.yml` reconciliation continues to land on the correct host (cluster or machine). The control-plane never writes to K8s Secrets directly — that lives in `mclaude-controller-k8s`.

### GitLab Token Refresh

A background goroutine runs every 15 minutes, querying `oauth_connections` for GitLab tokens expiring within 30 minutes. For each, it exchanges the refresh token for a new access token, updates the DB, and republishes a `…api.projects.update` NATS message so the controller responsible for the affected hosts can rotate credentials. If the refresh token is expired or revoked, the connection is automatically deleted.

### Project Creation Flow

Per ADR-0091, cluster hosts route the provisioning step through the user-agent in the user namespace. Machine hosts use a host controller directly (no user-agent on BYOH).

1. SPA publishes to `mclaude.users.{uslug}.api.projects.create` (NATS) with `{name, hostSlug, gitUrl?, gitIdentityId?}`, or uses HTTP `POST /api/users/{uslug}/projects`.
2. Control plane validates the request, including optional `gitIdentityId` hostname match, and verifies the calling user has access to `hostSlug` (owned + granted via `host_access` table).
3. Resolves `host_id` from `(user_id/host_access, hostSlug)`.
4. Creates a Postgres `projects` row (`host_id`, `slug`, `name`, `git_url`, optional `git_identity_id`).
5. Writes the project to the per-user `mclaude-projects-{uslug}` KV bucket (key `hosts.{hslug}.projects.{pslug}` — hierarchical key format per ADR-0054).
6. **Cluster host (ADR-0091):** Publishes to `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision` with `{projectId, projectSlug, gitUrl, gitIdentityId, backend}`. Awaits the user-agent's reply (`PROVISION_TIMEOUT_SECONDS`). The user-agent creates the project Deployment in the user namespace with `user-tokens-{uslug}` Secret mounted at `/secrets/tokens/`. If the user-agent returns "host not trusted" (no token for the requested provider), the SPA shows "Authorize this host". Session-agent JWT issuance inside the pod is unchanged (existing `mclaude.hosts.{hslug}.api.agents.register`).
   **Machine host:** Publishes to `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create` (existing path, unchanged for BYOH). The host controller materializes `~/.mclaude/projects/{pslug}/worktree/` and starts a session-agent subprocess.
7. Replies to the SPA with `{projectId, slug, hostSlug, status}`.

If the agent times out or replies with an error, control-plane returns `503 Service Unavailable` with `{error: "host {hslug} unreachable"}` and rolls back the Postgres + KV writes (or marks `projects.status = 'failed'` — whichever the implementation chooses; see ADR-0035 Error Handling row "Project create on offline cluster").

### Project Deletion Flow (ADR-0091)

1. SPA/CLI publishes to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.delete` (existing handler).
2. CP removes the Postgres `projects` row, the `mclaude-projects-{uslug}` KV entry, and S3 prefix cleanup.
3. **Cluster host:** Publishes to `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.delete` with `{projectId, projectSlug}`. Best-effort — no reply expected. If the user-agent is offline, the event is missed and the project Deployment persists in the user namespace until manual `kubectl delete deployment project-{projectId}`. v1 does not retroactively reconcile (ADR-0091 Deferred).
   **Machine host:** Publishes to `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.delete` (existing path).

### Project Deletion and S3 Cleanup

On project deletion (ADR-0053), in addition to the standard Postgres + KV + provisioning teardown, CP deletes the S3 prefix `{uslug}/{hslug}/{pslug}/` — this removes all import archives and attachments associated with the project. The S3 cleanup is best-effort; orphaned objects are not harmful (they have no access paths once the project metadata is gone).

## Error Handling

- **Postgres unavailable at startup**: fatal exit.
- **Postgres unavailable at runtime**: affected HTTP handlers return 503; admin routes return 503.
- **`OPERATOR_KEYS_PATH` mount missing or unreadable**: warning logged at startup; JWT revocation is disabled (TTL-only fallback). Control-plane can still mint and validate JWTs without this path — only the `$SYS.REQ.CLAIMS.UPDATE` revocation path is unavailable. This is the expected behavior in dev environments that lack the operator-keys Secret mount.
- **NATS connection failure**: retries indefinitely with unlimited reconnects. KV writes are best-effort; DB row is authoritative.
- **Provisioning request timeout**: `POST /api/users/{uslug}/projects` returns 503 with `{error: "host {hslug} unreachable"}`. The Postgres row is rolled back (or marked failed); the SPA presents the host as offline. Project creation queueing for retry is out of scope for v1.
- **Device-code expired (>10 min)**: `POST /api/hosts/register` returns 410 Gone with `{error: "code expired, restart registration"}`.
- **Device-code already redeemed**: returns 409 Conflict.
- **Admin endpoint called by non-admin**: returns 403 Forbidden.
- **Project creation with no registered hosts**: returns 422 with `{error: "no_hosts", message: "Register a host before creating a project"}` (ADR-0095). Applies to both HTTP and NATS project creation paths.
- **Project creation with multiple hosts and no `hostSlug`**: returns 422 with `{error: "host_required", message: "Specify hostSlug — user has multiple hosts"}` (ADR-0095).

## Integration Tests

The `go test -tags integration` suite is the Phase 1 CI gate. All tests use `httptest.NewRecorder()` + direct handler calls. The `newIntegrationServer(t, db, nc) *Server` helper constructs a test server with a real Postgres (testcontainers) and real NATS (testcontainers). No static admin token — admin tests create an `is_admin=true` user via `db.CreateUser` + `db.SetUserAdmin`, log in via `handleLogin`, and use the resulting JWT.

**Phase 1 invariants:**

| Test name | What it verifies |
|-----------|-----------------|
| `TestIntegration_Schema_NoSeedRows` | Fresh `Migrate()` on empty DB: `hosts`, `projects`, `users` tables all empty |
| `TestIntegration_CreateUser_NoLocalHost` | After `CreateUser`, no row in `hosts` with `slug='local'` for that user |
| `TestIntegration_BootstrapAdminUser_FreshWithPassword` | `bootstrapAdminUser` with non-empty password on empty DB: user row created with non-empty `password_hash`, `is_admin=true`; login succeeds |
| `TestIntegration_BootstrapAdminUser_ExistingUserGetsPassword` | `bootstrapAdminUser` called on existing user with empty `password_hash`: hash is written, login succeeds after call |
| `TestIntegration_BootstrapAdminUser_EmailWithoutPassword_Errors` | `bootstrapAdminUser` with email set and empty password when user does not yet exist: returns a non-nil error; no user row is created |
| `TestIntegration_CreateProject_BlockedWithoutHosts` | `POST /api/users/{uslug}/projects` returns 422 `no_hosts` when user has zero registered hosts (ADR-0095) |
| `TestIntegration_CreateProject_SucceedsWithHost` | `POST /api/users/{uslug}/projects` succeeds when user has at least one registered host (ADR-0095) |

**Admin routes (main port, `isAdminMiddleware`):**

| Test name | Method / Path | What it verifies |
|-----------|--------------|-----------------|
| `TestIntegration_AdminCreateUser` | `POST /admin/users` | Creates user; response has `{id, email, name, slug}` |
| `TestIntegration_AdminListUsers` | `GET /admin/users` | Lists created users |
| `TestIntegration_AdminDeleteUser` | `DELETE /admin/users/{id}` | Deletes user; subsequent DB fetch returns nil |
| `TestIntegration_AdminMiddleware_RejectsNoToken` | `GET /admin/users` (no auth) | 401 without JWT |
| `TestIntegration_AdminMiddleware_RejectsNonAdmin` | `GET /admin/users` (non-admin JWT) | 403 with valid JWT for a non-admin user |

**Auth surface:**

| Test name | Method / Path | What it verifies |
|-----------|--------------|-----------------|
| `TestIntegration_HandleLogin_InvalidCreds` | `POST /auth/login` | 401 on wrong password |
| `TestIntegration_AuthMiddleware_Rejects_Unauthenticated` | `GET /auth/me` | 401 without JWT |
| `TestIntegration_AuthMiddleware_Accepts_ValidJWT` | `GET /auth/me` | 200 with valid JWT from login |
| `TestIntegration_HostRegister_HTTP` | `POST /api/hosts/register` | Creates host row with `public_key`; response contains JWT |
| `TestIntegration_NKeyChallenge_AndVerify` | `POST /api/auth/challenge` + verify | Full round-trip; returns scoped JWT |
| `TestIntegration_DeviceCode_StartAndPoll` | `POST /api/auth/device-code` + poll | Returns `{deviceCode, userCode}`; poll returns `pending` |
| `TestIntegration_HealthEndpoints` | `GET /health`, `/healthz`, `/readyz` | All return 200 with DB up |

**NATS subject handlers:**

NATS subject tests use `nc.Request` with a 3s timeout; `t.Fatal` on timeout.

| Test name | Subject | What it verifies |
|-----------|---------|-----------------|
| `TestIntegration_ManageGrant` | `mclaude.users.*.hosts.*.manage.grant` | Grantee appears in host access list after publish |
| `TestIntegration_ManageRevokeAccess` | `mclaude.users.*.hosts.*.manage.revoke-access` | Grant removed from DB |
| `TestIntegration_ManageDeregister` | `mclaude.users.*.hosts.*.manage.deregister` | Host row deleted from DB |
| `TestIntegration_ManageUpdate` | `mclaude.users.*.hosts.*.manage.update` | Host name updated in DB |
| `TestIntegration_ProjectCreate_NATS` | `mclaude.users.*.api.projects.create` | Project row created; KV entry written |
| `TestIntegration_CheckSlug_Available` | `mclaude.users.*.hosts.*.projects.check-slug` | `{available:true}` for unused slug |
| `TestIntegration_CheckSlug_Taken` | `mclaude.users.*.hosts.*.projects.check-slug` | `{available:false}` for existing slug |

**`cluster_test.go`:**

- `TestCluster_UserProjectInKV` — replaces `TestCluster_DevUserProjectInKV`. Creates an ephemeral user via `POST /admin/users` using an admin JWT obtained from `POST /auth/login` (not `ADMIN_TOKEN`). Publishes a project-create NATS message, verifies the KV entry, then tears down. All uses of `ADMIN_TOKEN` env var removed from `cluster_test.go`.

**Deleted tests (ADR-0089):**
- `TestIntegration_SeedDev_WritesLocalHostKV` — removed with seedDev
- **`$SYS` event for unknown account**: logged at info level and ignored.
- **OAuth callback errors**: browser redirected to return URL with `?error={code}` (csrf, denied, exchange_failed, profile_failed, storage, gitlab_one_identity).
- **OAuth token exchange failure**: logged; browser redirected with error.
- **PAT validation failure**: returns 400 with descriptive error (invalid token vs. unreachable provider).
- **GitLab refresh failure**: logged as warning per connection; expired refresh tokens trigger automatic connection deletion.
- **`EXTERNAL_URL` not set**: fatal exit on startup.
- **Duplicate user email**: admin create returns 409 Conflict.
- **Duplicate cluster slug** (admin tries to register a slug already in use): returns 409 Conflict.
- **Slug validation failure at ingress**: returns HTTP 400 with `{code: "invalid_slug", reason: "reserved_word|charset|length", field: "slug"}`.
- **Reserved-word match in slugify**: fallback kicks in automatically (deterministic `{prefix}-{6 base32}`).

## Dependencies

- **Postgres**: user, project, host, `host_access`, `agent_credentials`, `attachments`, and OAuth connection persistence. Pool: 2-10 connections.
- **Hub NATS**: provisioning request publisher, KV bucket management, `$SYS` presence subscriber, host lifecycle handler, agent registration handler, import/attachment NATS handlers. Connects with retry-on-fail and unlimited reconnects.
- **S3-compatible storage**: MinIO for self-hosted, AWS S3 for cloud (ADR-0053). Used for import archives and attachments. CP generates pre-signed URLs — never proxies binary data directly. Configured via `S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`.
- **`mclaude-system/operator-keys` Secret**: mounted at `OPERATOR_KEYS_PATH`. Provides operator + account NKeys / JWTs for the trust chain and `sysAccountSeed` for JWT revocation via `$SYS.REQ.CLAIMS.UPDATE` (ADR-0054). Generated by the `mclaude-cp` Helm pre-install Job (`mclaude-cp init-keys`).
- **`mclaude-controller-k8s`** (one per worker cluster): receives `mclaude.hosts.{cslug}.>` provisioning requests (ADR-0054 host-scoped scheme). Lives in `docs/mclaude-controller-k8s/spec-k8s-architecture.md`.
- **BYOH host controller** (one per BYOH machine, future): receives `mclaude.hosts.{hslug}.>` provisioning requests directly from hub NATS (ADR-0054/ADR-0058).
- **OAuth providers** (optional): GitHub and/or GitLab instances for token exchange, profile fetch, token revocation, token refresh, and repo listing.
- **OpenTelemetry** (optional): tracer provider for distributed tracing (HTTP, DB, NATS request spans).

The control-plane has no Kubernetes API client and does not depend on the K8s API for runtime operations.
