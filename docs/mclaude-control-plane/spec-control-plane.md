# Spec: Control Plane

## Role

The control plane is the central API server and trust root for mclaude. It authenticates all identity types (users, hosts, agents) via a unified HTTP NKey challenge-response protocol (ADR-0054), issues scoped NATS JWTs (signed by the deployment-level account key), manages user / project / host records in Postgres, publishes provisioning requests over NATS to the appropriate controller (`mclaude-controller-k8s` for cluster hosts, `mclaude-controller-local` for BYOH machines), manages host lifecycle (registration, access grants/revocation, deregistration, emergency credential revocation — ADR-0054), handles OAuth provider integrations (GitHub/GitLab), tracks host liveness via `$SYS` events, manages S3 pre-signed URLs for binary data (imports and attachments — ADR-0053), provisions per-user JetStream resources (KV buckets and sessions streams — ADR-0054), and exposes admin endpoints for cluster registration and access grants.

Per ADR-0035 the control-plane is **K8s-free**: no controller-runtime, no K8s client, no MCProject reconciler. All K8s mutation is delegated to `mclaude-controller-k8s` via NATS request/reply. The control-plane runs only inside the central `mclaude-cp` Kubernetes cluster (there is no local/standalone variant). All identity types generate their own NKeys — the control-plane never generates NKey pairs or handles private key material (ADR-0054).

## Deployment

Runs as a Kubernetes Deployment in the `mclaude-system` namespace of the central `mclaude-cp` cluster, built from an Alpine-based container image. Listens on two ports: the main API port (default 8080) for public and authenticated routes, and a loopback-only admin port (default 9091, bound to `127.0.0.1`) for `/admin/*` break-glass endpoints and Prometheus metrics. Admin endpoints are protected by a static bearer token (`ADMIN_TOKEN` env var) — this port must not be exposed externally.

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `EXTERNAL_URL` | Yes | (none -- exits on startup if empty) | Externally-accessible base URL (e.g. `https://dev.mclaude.richardmcsong.com`) |
| `DATABASE_URL` / `DATABASE_DSN` | Yes | (none -- exits on startup if empty) | Postgres connection string. Hosts/users/projects persistence is required for ADR-0035. |
| `NATS_URL` | No | `nats://localhost:4222` | Internal hub NATS broker URL |
| `NATS_WS_URL` | No | (empty) | External WebSocket URL for browser clients; empty means client derives from origin |
| `NATS_ACCOUNT_SEED` | Yes (production) | (none) | Account NKey seed string. Used directly to sign per-host user JWTs and session-agent JWTs. In production Helm deployments, populated from the `operator-keys` Secret's `accountSeed` key. If not set, generates an ephemeral account key (dev-only — not suitable for production). |
| `OPERATOR_KEYS_PATH` | No | `/etc/mclaude/operator-keys` | Mount path for the `mclaude-system/operator-keys` Secret. At runtime, provides `operatorSeed` (for re-signing the account JWT during revocation) and `sysAccountSeed` (for publishing to `$SYS.REQ.CLAIMS.UPDATE`). Also used by the `init-keys` and `gen-leaf-creds` subcommands. |
| `NATS_SYS_ACCOUNT_SEED` | No | (read from `OPERATOR_KEYS_PATH/sysAccountSeed`) | System account NKey seed string. CP uses system account credentials to publish `$SYS.REQ.CLAIMS.UPDATE` for JWT revocation (ADR-0054). In production, populated from the `operator-keys` Secret's `sysAccountSeed` key. If set as an env var, takes precedence over the file-based path. |
| `ADMIN_TOKEN` | No | (empty) | Static bearer token for the admin port (9091). All `/admin/*` routes require this token via `Authorization: Bearer <token>`. |
| `BOOTSTRAP_ADMIN_EMAIL` | No | (empty) | Email of the first admin. Read from Helm value; the init-keys Job pre-creates a `users` row with `is_admin=true` and `oauth_id=NULL`; first OAuth login linking that email promotes the user. |
| `PORT` | No | `8080` | Main API listen port |
| `ADMIN_PORT` | No | `9091` | Loopback-only port (bound to `127.0.0.1`) for `/admin/*` break-glass routes and `/metrics`. |
| `JWT_EXPIRY_SECONDS` | No | `28800` (8h) | Per-host user JWT lifetime in seconds |
| `DEV_OAUTH_TOKEN` | No | (empty) | Injected into per-user secrets (cluster controller copies into the user namespace) for dev environments |
| `DEV_SEED` | No | `false` | When `true`, creates a dev user (`dev@mclaude.local` / `dev`), a default `local` machine host, and a default project on startup |
| `MIN_CLIENT_VERSION` | No | `0.0.0` | Minimum SPA/CLI version reported by `/version` |
| `SERVER_VERSION` | No | (empty) | Server version string reported by `/version` |
| `PROVIDERS_CONFIG_PATH` | No | `/etc/mclaude/providers.json` | Path to OAuth provider config (Helm ConfigMap mount) |
| `PROVIDER_SECRET_{ID}` | No | (none) | OAuth client secret for provider `{ID}` (uppercased, dashes→underscores, e.g. `PROVIDER_SECRET_GITHUB`). Read by `LoadProviders()` per-provider. Fallback: file at `/etc/mclaude/secrets/{clientSecretRef}/client-secret`. |
| `S3_ENDPOINT` | Yes (production) | (none) | S3-compatible storage endpoint URL (e.g. `https://s3.amazonaws.com` or MinIO URL for self-hosted). Required for import and attachment support (ADR-0053). |
| `S3_BUCKET` | Yes (production) | (none) | S3 bucket name. Single bucket with per-user/host/project key prefixes: `{uslug}/{hslug}/{pslug}/imports/{id}` and `{uslug}/{hslug}/{pslug}/attachments/{id}`. |
| `S3_ACCESS_KEY_ID` | Yes (production) | (none) | S3 access key ID for pre-signed URL generation. |
| `S3_SECRET_ACCESS_KEY` | Yes (production) | (none) | S3 secret access key for pre-signed URL generation. |
| `PROVISION_TIMEOUT_SECONDS` | No | `10` | Per-request timeout for NATS provisioning request/reply. Note: currently a hardcoded constant in code (`const ProvisionTimeoutSeconds = 10`), not yet read from env. `seedDev` uses a longer 30s timeout for the initial provisioning request during startup (controller may not be ready yet). |
| `LOG_LEVEL` | No | (default) | **Not read by Go code** — injected by the `mclaude-cp` Helm template but not consumed by the binary. Zerolog uses its default level. |
| `HELM_RELEASE_NAME` | No | (none) | **Not read by Go code** — injected by the `mclaude-cp` Helm template but not consumed by the control-plane binary. (The controller-k8s binary does read this variable for ConfigMap lookup.) |
| `METRICS_PORT` | No | (none) | **Not read by Go code** — removed from the `mclaude-cp` Helm template (ADR-0052 R7-G10). `/metrics` is served on the admin port (`ADMIN_PORT`). |

## Interfaces

### HTTP Endpoints -- Main Port

**Public (no auth):**

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/auth/login` | Authenticate with email+password. Request body includes `nkey_public` (the user's NKey public key, generated client-side). Returns the [Login Response](../spec-state-schema.md#login-response-shape) — per-host user JWT, hub URL, host inventory, and projects. **No NKey seed in the response** — all identity types generate their own NKeys; CP never handles private key material (ADR-0054). CP stores the public key in `users.nkey_public` for future challenge-response auth. |
| `POST` | `/auth/refresh` | Exchange a valid per-host JWT from the Authorization header for a new JWT (same host scope). Returns `s.natsWsURL` (external WebSocket URL) for browser clients. |
| `POST` | `/api/auth/challenge` | Unified NKey challenge-response authentication — step 1 (ADR-0054). Request: `{nkey_public}`. CP looks up the public key across `users.nkey_public`, `hosts.public_key`, `agent_credentials.nkey_public` (first match wins, determines identity type). Returns `{challenge}` (random nonce, single-use, 30s TTL, stored in-memory). Error: `NOT_FOUND` if public key is unknown. |
| `POST` | `/api/auth/verify` | Unified NKey challenge-response authentication — step 2 (ADR-0054). Request: `{nkey_public, challenge, signature}`. CP verifies the Ed25519 signature of the challenge nonce against the stored public key, resolves current permissions (host access list for users, project scope for agents, host scope for hosts), signs a JWT, and returns `{ok, jwt}`. Errors: `UNAUTHORIZED` (invalid signature), `FORBIDDEN` (host revoked), `EXPIRED` (challenge expired). |
| `POST` | `/api/auth/device-code` | Initiate device-code login flow for CLI (ADR-0053). Returns `{deviceCode, userCode, verificationUrl, expiresIn, interval}`. Stores pending auth in memory with 15-minute TTL. |
| `POST` | `/api/auth/device-code/poll` | CLI polls with device code. Returns `{status: "pending"}` while waiting, or `{jwt, userSlug}` once the user completes verification. Returns 410 Gone if expired. |
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
| `GET` | `/api/users/{uslug}/hosts` | Lists hosts owned by or granted to the user. |
| `POST` | `/api/users/{uslug}/hosts` | Creates a machine host directly (`{slug, name, publicKey?}`). Bypasses the device-code flow. Inserts a `hosts` row with `type='machine'`, `role='owner'`, mints a per-host user JWT, returns host response. **Known bug:** `IssueHostJWT(userID, ...)` passes UUID instead of slug (same as other host JWT issuance paths). |
| `POST` | `/api/users/{uslug}/hosts/code` | Generates a 6-character device code for BYOH host registration. Accepts `{publicKey}` — the host's NKey public key, generated locally by the CLI. The server stores the public key with the code record. Returns `{code, expiresAt}` (10-minute TTL). |
| `GET` | `/api/users/{uslug}/hosts/code/{code}` | Polls device-code status. Returns `{status: "pending"}` while waiting, or `{status: "completed", slug, jwt, hubUrl}` once redeemed. Returns 410 Gone if expired, 404 if not found. **Known gap:** pending response does not include `expiresAt` — the expiry is stored server-side but not returned to the CLI. |
| `POST` | `/api/hosts/register` | **Public (no JWT required)** — Redeems a device code with `{code, name}`. The device code (generated by an authenticated user via `POST /api/users/{uslug}/hosts/code`) serves as the authorization credential. Creates a `hosts` row, mints a per-host user JWT, returns `{slug, jwt, hubUrl}`. **Known bug:** `IssueHostJWT(entry.UserID, ...)` passes UUID instead of slug, producing incorrect JWT subject permissions (same bug as `adminGrantCluster`). |
| `PUT` | `/api/users/{uslug}/hosts/{hslug}` | Updates host display name. |
| `DELETE` | `/api/users/{uslug}/hosts/{hslug}` | Removes a host (cascades to its projects + sessions). For cluster hosts owned by other users, only the registering admin can delete. |

**HTTP project CRUD:**

HTTP handlers for project CRUD are wired and functional. `POST` (create) inserts a Postgres row but is incomplete — it does not write to KV, send a provisioning request, or broadcast `projects.updated` (see CP-6 in ADR-0052).

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/users/{uslug}/projects` | Creates a project on a specified host. **Known gap (CP-6):** creates Postgres row but skips KV write, provisioning request, and `publishProjectsUpdated` broadcast — HTTP-created projects are invisible to the SPA and have no session-agent pod. |
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

### HTTP Endpoints -- Loopback Port (9091)

The loopback-only port (bound to `127.0.0.1`) hosts admin break-glass endpoints and Prometheus metrics. All `/admin/*` routes require the static `ADMIN_TOKEN` bearer token via `Authorization: Bearer <token>`.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/metrics` | Prometheus metrics |

**Admin-only (static `ADMIN_TOKEN` bearer token on loopback port):**

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/admin/clusters` | Registers a new cluster (`{slug, name, jsDomain, leafUrl, directNatsUrl?}`). Generates a per-cluster NKey pair, creates a `hosts` row, mints a per-cluster leaf/controller JWT scoped to `mclaude.users.*.hosts.{slug}.>`, returns `{slug, leafJwt, leafSeed, jsDomain, directNatsUrl}`. **Known bug:** response omits `accountJwt`/`operatorJwt` (fields exist but unpopulated). **Known bug:** `hosts` row `user_id` is set to `(SELECT id FROM users LIMIT 1)` (arbitrary) instead of the calling admin — the loopback admin token carries no user identity. |
| `GET` | `/admin/clusters` | Lists registered clusters (deduplicated across user rows). |
| `POST` | `/admin/clusters/{cslug}/grants` | Grants user access (`{userSlug}`). Creates a new `hosts` row for that user; mints a per-user JWT scoped to `mclaude.users.{userSlug}.hosts.{cslug}.>`. **Known bug:** handler calls `GetUserByEmail(req.UserSlug)` instead of `GetUserBySlug(req.UserSlug)`. **Known bug:** `IssueHostJWT(user.ID, ...)` passes UUID instead of slug, producing incorrect JWT subject permissions. |
| `DELETE` | `/admin/clusters/{cslug}` | Removes the cluster. **Not yet implemented** — deferred per ADR-0035. Use direct SQL as a workaround. |
| `POST` | `/admin/users` | Creates a user (id, email, name, optional password). **Known gap:** spec says optional `isAdmin` field, but `AdminUserRequest` struct has no `IsAdmin` field — new users are always non-admin. |
| `POST` | `/admin/users/{uslug}/promote` | Sets `users.is_admin = true`. **Not yet implemented** — DB method `SetUserAdmin` exists but HTTP handler is not wired. Use direct SQL as a workaround. |
| `GET` | `/admin/users` | Lists all users. |
| `DELETE` | `/admin/users/{id}` | Deletes a user from Postgres (cascades to hosts, projects, oauth_connections). **Known gap:** does not revoke the user's NATS JWT (remains valid until expiry) and does not publish NATS delete requests to controllers — orphaned K8s resources persist until manual cleanup. |
| `POST` | `/admin/sessions/stop` | Break-glass session stop. **Known bug:** handler executes `UPDATE sessions SET status = 'stopped'` against a `sessions` table that does not exist in the schema (session state lives in NATS KV, not Postgres). Endpoint is effectively non-functional. |

### NATS Subjects

Per ADR-0035 the control-plane communicates with controllers over host-scoped NATS subjects on the hub. For the full subject catalog see `spec-state-schema.md` -- NATS Subjects.

**Publishes (request/reply, 10s timeout):**

| Subject | Trigger | Description |
|---------|---------|-------------|
| `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create` | `POST /api/users/{uslug}/projects` or NATS project creation | Fan-out provisioning (ADR-0054). CP validates the HTTP request (authorization, slug uniqueness, host assignment), creates Postgres records, writes project KV, then publishes to this host-scoped subject. Both the host controller (via `mclaude.hosts.{hslug}.>` subscription) and CP itself receive the message. Payload: `{userID, userSlug, hostSlug, projectID, projectSlug, gitUrl, gitIdentityId}`. |
| `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.delete` | Project delete | Asks the controller to tear down per-project resources. Fan-out scheme — host controller receives via `mclaude.hosts.{hslug}.>` subscription. |
| `mclaude.users.{uslug}.hosts.{hslug}.api.projects.update` | `PATCH /api/projects/{id}` | Asks the controller to apply project metadata changes (e.g., git identity rotation). |

**Subscribes:**

| Subject | Description |
|---------|-------------|
| `$SYS.ACCOUNT.{accountKey}.CONNECT` | Per-connection event. Switch on payload `client.kind`: `Client` → `SELECT * FROM hosts WHERE public_key = client.nkey AND type = 'machine'`, update `last_seen_at` for that single row, upsert `mclaude-hosts` KV `online=true`. `Leafnode` → `SELECT * FROM hosts WHERE public_key = client.nkey AND type = 'cluster' LIMIT 1`, then update `last_seen_at` for **all** rows where `slug = found.slug AND type = 'cluster'` and upsert KV for each user row (cluster-shared liveness). No match → ignore (covers SPA's per-login ephemeral NKey and control-plane's own connection). |
| `$SYS.ACCOUNT.{accountKey}.DISCONNECT` | Same lookup logic; sets `mclaude-hosts` KV `online=false` for the matched row(s). Does not rewrite `last_seen_at` (it tracks last-known-online). |
| `mclaude.hosts.{hslug}.api.agents.register` | Agent public key registration (ADR-0054). Host controllers register spawned agent NKey public keys here. Request: `{user_slug, project_slug, nkey_public}`. CP validates host access + project ownership + host assignment, then stores `(user_id, host_slug, project_slug) → nkey_public` in the `agent_credentials` table. Returns `{ok: true}`. Errors: `FORBIDDEN` (user does not have access to host), `NOT_FOUND` (project not assigned to host). The agent then authenticates itself via HTTP challenge-response to get its per-project JWT. |
| `mclaude.users.*.hosts._.register` | Host registration (ADR-0054). User's CLI publishes `{name, type, nkey_public}`. CP creates host in Postgres (slug, name, type, owner_id, nkey_public). Returns `{ok, slug}` — **no JWT** in the response; the host authenticates itself via HTTP challenge-response. The `_` sentinel in the hslug position cannot collide with real slugs (slugs are `[a-z0-9-]+`). |
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

Note: The control-plane also subscribes to `mclaude.users.*.api.projects.create` (a user-scoped NATS subject without the host segment) for backward-compatible NATS-based project creation from the SPA. This subject is published by the SPA's `subjProjectsCreate` helper. The handler creates the Postgres row, writes to `mclaude-projects` KV, and publishes a provisioning request to the host-scoped controller subject. This NATS path coexists with the HTTP `POST /api/users/{uslug}/projects` endpoint; both are functional. **Known bug:** `CreateProjectWithIdentity` does not compute or set the `slug` column on INSERT — new projects get `slug=''` until the next startup migration backfill runs. This also means `ProvisionRequest.ProjectSlug` is empty. **Known bug:** `host_id` column is nullable in the DDL despite the spec requiring NOT NULL — `CreateProjectWithIdentity` does not set `host_id`, so NATS-created projects have `host_id = NULL` until the backfill.

### NATS KV Buckets and JetStream Streams

The control-plane manages two categories of JetStream resources:

**Shared (created on deployment startup):**

- **`mclaude-hosts`** -- shared KV bucket; created by `ensureHostsKV`. Control-plane is the sole writer. Production writes driven by `$SYS.ACCOUNT.*.CONNECT/DISCONNECT`. Read access is per-host in user JWTs (each user's JWT lists explicit host slugs they can read — NATS enforces host visibility). Dev-seed path: on `DEV_SEED=true`, `seedDev` writes the bootstrap user's `local` host entry with `online=true`.

**Per-user (created on user registration / first login):**

- **`mclaude-sessions-{uslug}`** -- per-user KV bucket for session state. Created by CP on user registration. Writes are handled by `mclaude-session-agent`. Replaces the shared `mclaude-sessions` bucket (ADR-0054).
- **`mclaude-projects-{uslug}`** -- per-user KV bucket for project state. Created by CP on user registration. CP writes on project creation and updates. Replaces the shared `mclaude-projects` bucket (ADR-0054).
- **`MCLAUDE_SESSIONS_{uslug}`** -- per-user JetStream stream consolidating events, commands, and lifecycle (ADR-0054). Created by CP on user registration. Captures `mclaude.users.{uslug}.hosts.*.projects.*.sessions.>`. Config: `LimitsPolicy`, `MaxAge: 30d`, `FileStorage`. Replaces the shared `MCLAUDE_EVENTS`, `MCLAUDE_API`, `MCLAUDE_LIFECYCLE` streams.

If a per-user bucket or stream is missing at runtime (e.g., after a restore), the control-plane creates it on demand.

**Removed:** `mclaude-job-queue` (eliminated per ADR-0044 — quota-managed sessions use the session KV with extended fields). `mclaude-clusters`, `mclaude-laptops`, and `mclaude-heartbeats` removed per ADR-0035.

### Postgres

Manages the `users`, `projects`, `hosts`, `host_access`, `agent_credentials`, `attachments`, and `oauth_connections` tables (ADR-0054, ADR-0053). Schema is applied on startup via idempotent DDL (`CREATE TABLE IF NOT EXISTS`, `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`). For full schema, see `spec-state-schema.md` -- Postgres.

Key tables added by ADR-0054/0053:
- **`host_access`** — `(host_id, user_id)` composite PK. Tracks per-user access grants to hosts. The host owner has implicit access (not stored here).
- **`agent_credentials`** — `(user_id, host_slug, project_slug) → nkey_public`. One active credential per user/host/project. Used for HTTP challenge-response auth. Cleaned up on deprovision.
- **`attachments`** — Attachment metadata for S3-stored binary data. Tracks upload confirmation state for garbage collection of abandoned uploads.

### Kubernetes Dependency

The control-plane has **no** K8s client at runtime and creates **no** K8s resources during normal operation. All MCProject CR creation, namespace provisioning, PVC management, RBAC reconciliation, and pod template construction live in `mclaude-controller-k8s` (see `docs/mclaude-controller/spec-controller.md`). The control-plane reaches the controller via NATS request/reply on two subject patterns:

- **ADR-0054 host-scoped (primary, used by dev-seed):** `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create` — CP-initiated fan-out provisioning.
- **Legacy ADR-0035 user-scoped (SPA backward compat):** `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` — retained for SPA-initiated project creation.

The control-plane binary also doubles as two Helm pre-install Job entrypoints (the only code paths that use `client-go`):
- **`control-plane init-keys`** — generates operator + account NKey pairs and JWTs, writes them to the `operator-keys` Secret in `mclaude-system`. Idempotent: exits 0 if the Secret already exists. Also creates the bootstrap admin row in Postgres when `BOOTSTRAP_ADMIN_EMAIL` is set. Run by the `mclaude-cp` chart's pre-install Job. Env vars: `NAMESPACE` (default `mclaude-system`), `OPERATOR_KEYS_SECRET` (default `operator-keys`).
- **`control-plane gen-leaf-creds`** — reads the account seed from the `operator-keys` Secret, generates a NATS user JWT + NKey seed, writes them as a `.creds` file into a `mclaude-worker-nats-leaf-creds` Secret. The JWT has no explicit pub/sub permissions (unrestricted within the account). This is a separate credential from the scoped per-cluster JWT issued by `POST /admin/clusters` — the gen-leaf-creds JWT is used only by the worker NATS StatefulSet for the leaf-node connection. Idempotent: exits 0 if the leaf-creds Secret already exists. Run by the `mclaude-worker` chart's pre-install Job. Env vars: `NAMESPACE` (default `mclaude-system`), `LEAF_CREDS_SECRET` (default `mclaude-worker-nats-leaf-creds`), `ACCOUNT_SEED_SECRET` (default `operator-keys`), `ACCOUNT_SEED_KEY` (default `accountSeed`).

At runtime, the control-plane mounts one K8s Secret as a file:
- `mclaude-system/operator-keys` — mounted at `OPERATOR_KEYS_PATH` (default `/etc/mclaude/operator-keys`). Read-only. Provides `operatorJwt`, `accountJwt`, `accountSeed`, `operatorSeed`, and `sysAccountSeed` for signing per-host user JWTs, per-cluster leaf JWTs, and publishing JWT revocations to `$SYS.REQ.CLAIMS.UPDATE` (ADR-0054).

## Internal Behavior

### Startup Sequence

1. Connects to Postgres (fatal exit on failure) and runs idempotent schema migration including the ADR-0035 `hosts` table and `projects.host_id` column.
2. Loads OAuth provider config from `PROVIDERS_CONFIG_PATH`, resolving client secrets from K8s Secrets.
3. Loads the account signing key from `NATS_ACCOUNT_SEED` env var. If set, parsed directly as an NKey seed. If not set, generates an ephemeral account key (dev-only — not suitable for production; subjects will not match production JWTs). Fatal exit if signing fails — control-plane cannot mint JWTs without a valid account key. Also loads the operator seed and system account seed from `OPERATOR_KEYS_PATH` (or `NATS_SYS_ACCOUNT_SEED` env var) for JWT revocation support (ADR-0054). Caches the current account JWT in memory for revocation modifications.
4. Creates the HTTP server with all route handlers.
5. Connects to hub NATS (retry on failure, unlimited reconnects).
6. Ensures the shared `mclaude-hosts` KV bucket exists — it must exist before the `$SYS` subscriber starts so CONNECT events can write to it immediately. Per-user KV buckets (`mclaude-sessions-{uslug}`, `mclaude-projects-{uslug}`) and the per-user sessions stream (`MCLAUDE_SESSIONS_{uslug}`) are created on user registration / first login, not at startup (ADR-0054).
7. Subscribes to `$SYS.ACCOUNT.*.CONNECT` and `$SYS.ACCOUNT.*.DISCONNECT` for host liveness. Also subscribes to host lifecycle subjects (`mclaude.users.*.hosts.*.manage.>`, `mclaude.users.*.hosts._.register`), agent registration (`mclaude.hosts.*.api.agents.register`), and import/attachment NATS handlers.
8. Starts the GitLab token refresh goroutine (every 15 minutes).
9. Optionally seeds a dev user, a default `local` machine host for that user, and a default project on the `local` host when `DEV_SEED=true`. Also creates per-user KV buckets and the sessions stream for the dev user. Writes the `local` host's `mclaude-hosts` KV entry with `online=true` (dev-only path — the auto-created `local` host has no NKey, so no `$SYS` CONNECT event fires for it). After writing KV entries, publishes a NATS provisioning request (`mclaude.hosts.local.users.{uslug}.projects.{pslug}.create`) for the default project so the controller creates per-project resources (ADR-0050). Non-fatal on failure (controller may not be running yet during startup race).
10. Starts the main HTTP listener and the loopback metrics listener.

If `BOOTSTRAP_ADMIN_EMAIL` is set on first boot, control-plane upserts a `users` row with that email, `is_admin=true`, `oauth_id=NULL`. The first OAuth login matching that email links the OAuth identity to the bootstrap row.

### Authentication and JWT Signing

Per ADR-0054, all identity types (user, host, agent) generate their own NKey pairs — the control-plane **never generates NKey pairs or handles private key material**. CP receives only public keys and returns signed JWTs.

**User login:** Login validates email and bcrypt password hash (or OAuth identity) against Postgres. The login request body includes `nkey_public` — the user's NKey public key generated client-side (SPA stores seed in `localStorage`; CLI stores seed in `~/.mclaude/auth.json`). On success, the control plane:

1. Stores the public key in `users.nkey_public` (for future challenge-response auth).
2. Loads the calling user's hosts: owned hosts (`SELECT … FROM hosts WHERE owner_id = ?`) plus granted hosts (`SELECT … FROM host_access WHERE user_id = ?`).
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

**Per-user resource provisioning:** On user registration (first login), CP creates per-user KV buckets (`mclaude-sessions-{uslug}`, `mclaude-projects-{uslug}`) and the per-user sessions stream (`MCLAUDE_SESSIONS_{uslug}`). These resources are created once per user and persist.

The auth middleware on protected routes decodes and validates the JWT against the account public key and extracts the user UUID into the request context. **Known gap:** the spec-described access boundary enforcement (cross-user 403 when JWT `sub` doesn't match URL `{uslug}`, cross-host 403) is not implemented — the middleware only extracts the user ID without checking URL parameters. Admin routes use a separate static `ADMIN_TOKEN` middleware on the loopback port (not per-user `is_admin` checks).

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

Supports GitHub and GitLab providers configured via Helm (`providers.json`). OAuth flows use an in-memory state store with 10-minute TTL and cryptographic random state tokens. On successful callback, the control-plane stores connection metadata in the `oauth_connections` Postgres table and publishes a NATS message on `mclaude.users.{uslug}.hosts.{hslug}.api.projects.update` so `mclaude-controller-k8s` (cluster hosts) can refresh the per-user `user-secrets` Secret with the new tokens. For BYOH machines, `mclaude-controller-local` receives the same message and writes credentials into the local credential helpers (`~/.mclaude/projects/{pslug}/.credentials/`).

PAT connections bypass OAuth and auto-detect provider type by probing GitHub and GitLab API endpoints.

After any connection change (connect, disconnect, token refresh), the control-plane re-publishes the update message so `gh-hosts.yml` and `glab-config.yml` reconciliation continues to land on the correct host (cluster or machine). The control-plane never writes to K8s Secrets directly — that lives in `mclaude-controller-k8s`.

### GitLab Token Refresh

A background goroutine runs every 15 minutes, querying `oauth_connections` for GitLab tokens expiring within 30 minutes. For each, it exchanges the refresh token for a new access token, updates the DB, and republishes a `…api.projects.update` NATS message so the controller responsible for the affected hosts can rotate credentials. If the refresh token is expired or revoked, the connection is automatically deleted.

### Project Creation Flow

1. SPA publishes to `mclaude.users.{uslug}.api.projects.create` (NATS) with `{name, hostSlug, gitUrl?, gitIdentityId?}`, or uses HTTP `POST /api/users/{uslug}/projects`.
2. Control plane validates the request, including optional `gitIdentityId` hostname match, and verifies the calling user has access to `hostSlug` (owned + granted via `host_access` table).
3. Resolves `host_id` from `(owner_id/host_access, hostSlug)`.
4. Creates a Postgres `projects` row (`host_id`, `slug`, `name`, `git_url`, optional `git_identity_id`).
5. Writes the project to the per-user `mclaude-projects-{uslug}` KV bucket (key `hosts.{hslug}.projects.{pslug}` — hierarchical key format per ADR-0054).
6. Publishes to `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create` with `{userID, userSlug, hostSlug, projectID, projectSlug, gitUrl, gitIdentityId}` (fan-out scheme per ADR-0054). Awaits the controller's reply (`PROVISION_TIMEOUT_SECONDS`).
   - For cluster hosts: `mclaude-controller-k8s` (subscribed to `mclaude.hosts.{cslug}.>`) creates the `MCProject` CR in `mclaude-system` and reconciles namespace/RBAC/PVCs/Secrets/Deployment.
   - For machine hosts: `mclaude-controller-local` (subscribed to `mclaude.hosts.{hslug}.>`) materializes `~/.mclaude/projects/{pslug}/worktree/` and starts a session-agent subprocess. The agent generates its own NKey pair; the controller registers the public key with CP via `mclaude.hosts.{hslug}.api.agents.register`; the agent authenticates via HTTP challenge-response.
7. Replies to the SPA with `{projectId, slug, hostSlug, status}`.

If the controller times out or replies with an error, control-plane returns `503 Service Unavailable` with `{error: "host {hslug} unreachable"}` and rolls back the Postgres + KV writes (or marks `projects.status = 'failed'` — whichever the implementation chooses; see ADR-0035 Error Handling row "Project create on offline cluster").

### Project Deletion and S3 Cleanup

On project deletion (ADR-0053), in addition to the standard Postgres + KV + provisioning teardown, CP deletes the S3 prefix `{uslug}/{hslug}/{pslug}/` — this removes all import archives and attachments associated with the project. The S3 cleanup is best-effort; orphaned objects are not harmful (they have no access paths once the project metadata is gone).

## Error Handling

- **Postgres unavailable at startup**: fatal exit.
- **Postgres unavailable at runtime**: affected HTTP handlers return 503; admin routes return 503.
- **`OPERATOR_KEYS_PATH` mount missing or unreadable**: fatal exit on startup — control-plane cannot mint per-host JWTs.
- **NATS connection failure**: retries indefinitely with unlimited reconnects. KV writes are best-effort; DB row is authoritative.
- **Provisioning request timeout**: `POST /api/users/{uslug}/projects` returns 503 with `{error: "host {hslug} unreachable"}`. The Postgres row is rolled back (or marked failed); the SPA presents the host as offline. Project creation queueing for retry is out of scope for v1.
- **Device-code expired (>10 min)**: `POST /api/hosts/register` returns 410 Gone with `{error: "code expired, restart registration"}`.
- **Device-code already redeemed**: returns 409 Conflict.
- **Admin endpoint called by non-admin**: returns 403 Forbidden.
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
- **`mclaude-controller-k8s`** (one per worker cluster): receives `mclaude.hosts.{cslug}.>` provisioning requests (ADR-0054 host-scoped scheme). Lives in `docs/mclaude-controller/spec-controller.md`.
- **`mclaude-controller-local`** (one per BYOH machine): receives `mclaude.hosts.{hslug}.>` provisioning requests directly from hub NATS (ADR-0054/ADR-0058). Lives in `docs/mclaude-controller/spec-controller.md`.
- **OAuth providers** (optional): GitHub and/or GitLab instances for token exchange, profile fetch, token revocation, token refresh, and repo listing.
- **OpenTelemetry** (optional): tracer provider for distributed tracing (HTTP, DB, NATS request spans).

The control-plane has no Kubernetes API client and does not depend on the K8s API for runtime operations.
