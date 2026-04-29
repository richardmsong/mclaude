# Spec: Control Plane

## Role

The control plane is the central API server and trust root for mclaude. It authenticates users, issues per-host NATS JWTs (signed by the deployment-level account key), manages user / project / host records in Postgres, publishes provisioning requests over NATS to the appropriate controller (`mclaude-controller-k8s` for cluster hosts, `mclaude-controller-local` for BYOH machines), handles OAuth provider integrations (GitHub/GitLab), tracks host liveness via `$SYS` events, and exposes admin endpoints for cluster registration and access grants.

Per ADR-0035 the control-plane is **K8s-free**: no controller-runtime, no K8s client, no MCProject reconciler. All K8s mutation is delegated to `mclaude-controller-k8s` via NATS request/reply. The control-plane runs only inside the central `mclaude-cp` Kubernetes cluster (there is no local/standalone variant).

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
| `OPERATOR_KEYS_PATH` | No | `/etc/mclaude/operator-keys` | Mount path for the `mclaude-system/operator-keys` Secret. **Note:** the runtime code does not currently read from this path — it uses `NATS_ACCOUNT_SEED` only. The file mount is used by the `init-keys` and `gen-leaf-creds` subcommands, and is spec'd as a fallback for future implementation. |
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
| `PROVISION_TIMEOUT_SECONDS` | No | `10` | Per-request timeout for NATS provisioning request/reply (`mclaude.users.{uslug}.hosts.{hslug}.api.projects.*`). Note: currently a hardcoded constant in code (`const ProvisionTimeoutSeconds = 10`), not yet read from env. `seedDev` uses a longer 30s timeout for the initial provisioning request during startup (controller may not be ready yet). |

## Interfaces

### HTTP Endpoints -- Main Port

**Public (no auth):**

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/auth/login` | Authenticate with email+password; returns the [Login Response](../spec-state-schema.md#login-response-shape) — per-host user JWT, NKey seed, hub URL, host inventory, and projects |
| `POST` | `/auth/refresh` | Exchange a valid per-host JWT from the Authorization header for a new JWT (same host scope). **Known bug:** returns `s.natsURL` (internal broker URL) instead of `s.natsWsURL` (external WebSocket URL). SPA refresh may receive an unusable `nats://` URL. |
| `GET` | `/version` | Returns `minClientVersion` and `serverVersion` |
| `GET` | `/health` | Returns 200 OK (process alive check — never checks NATS, so pod stays alive for break-glass admin port) |
| `GET` | `/healthz` | Kubernetes liveness probe (same as `/health` — never checks NATS) |
| `GET` | `/readyz` | Kubernetes readiness probe. **Known bug:** currently returns 200 unconditionally (identical to `/healthz`). Should check Postgres connectivity so the pod stops receiving traffic when DB is unreachable — NATS outage must not mark pod unready. |
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
| `PATCH` | `/api/projects/{id}` | Updates a project's `gitIdentityId` |
| `GET` | `/api/users/{uslug}/hosts` | Lists hosts owned by or granted to the user. |
| `POST` | `/api/users/{uslug}/hosts` | Creates a machine host directly (`{slug, name, publicKey?}`). Bypasses the device-code flow. Inserts a `hosts` row with `type='machine'`, `role='owner'`, mints a per-host user JWT, returns host response. **Known bug:** `IssueHostJWT(userID, ...)` passes UUID instead of slug (same as other host JWT issuance paths). |
| `POST` | `/api/users/{uslug}/hosts/code` | Generates a 6-character device code for BYOH host registration. Accepts `{publicKey}` — the host's NKey public key, generated locally by the CLI. The server stores the public key with the code record. Returns `{code, expiresAt}` (10-minute TTL). |
| `GET` | `/api/users/{uslug}/hosts/code/{code}` | Polls device-code status. Returns `{status: "pending"}` while waiting, or `{status: "completed", slug, jwt, hubUrl}` once redeemed. Returns 410 Gone if expired, 404 if not found. **Known gap:** pending response does not include `expiresAt` — the expiry is stored server-side but not returned to the CLI. |
| `POST` | `/api/hosts/register` | **Public (no JWT required)** — Redeems a device code with `{code, name}`. The device code (generated by an authenticated user via `POST /api/users/{uslug}/hosts/code`) serves as the authorization credential. Creates a `hosts` row, mints a per-host user JWT, returns `{slug, jwt, hubUrl}`. **Known bug:** `IssueHostJWT(entry.UserID, ...)` passes UUID instead of slug, producing incorrect JWT subject permissions (same bug as `adminGrantCluster`). |
| `PUT` | `/api/users/{uslug}/hosts/{hslug}` | Updates host display name. |
| `DELETE` | `/api/users/{uslug}/hosts/{hslug}` | Removes a host (cascades to its projects + sessions). For cluster hosts owned by other users, only the registering admin can delete. |

**HTTP project CRUD — not yet implemented:**

The following endpoints are spec targets but have no HTTP handlers in the code. Project creation currently uses the NATS-based path only (`mclaude.users.*.api.projects.create`). HTTP project CRUD will be implemented in a future iteration.

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/users/{uslug}/projects` | Creates a project on a specified host. **Not yet implemented.** |
| `GET` | `/api/users/{uslug}/projects` | Lists all projects for the user. **Not yet implemented.** |
| `GET` | `/api/users/{uslug}/projects/{pslug}` | Gets a single project by slug. **Not yet implemented.** |
| `DELETE` | `/api/users/{uslug}/projects/{pslug}` | Deletes a project. **Not yet implemented.** |

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
| `POST` | `/admin/clusters` | Registers a new cluster (`{slug, name, jsDomain, leafUrl, directNatsUrl?}`). Generates a per-cluster NKey pair, then creates a `hosts` row with `type='cluster'`, `role='owner'` for the calling admin; mints a per-cluster leaf/controller JWT scoped to `mclaude.users.*.hosts.{slug}.>`; returns `{slug, leafJwt, leafSeed, jsDomain, directNatsUrl}`. **Known bug:** response struct has `accountJwt` and `operatorJwt` fields but they are never populated — the admin must manually copy these from the `operator-keys` Secret. |
| `GET` | `/admin/clusters` | Lists registered clusters (deduplicated across user rows). |
| `POST` | `/admin/clusters/{cslug}/grants` | Grants user access (`{userSlug}`). Creates a new `hosts` row for that user; mints a per-user JWT scoped to `mclaude.users.{userSlug}.hosts.{cslug}.>`. **Known bug:** handler calls `GetUserByEmail(req.UserSlug)` instead of `GetUserBySlug(req.UserSlug)`. **Known bug:** `IssueHostJWT(user.ID, ...)` passes UUID instead of slug, producing incorrect JWT subject permissions. |
| `DELETE` | `/admin/clusters/{cslug}` | Removes the cluster. **Not yet implemented** — deferred per ADR-0035. Use direct SQL as a workaround. |
| `POST` | `/admin/users` | Creates a user (id, email, name, optional password, optional `isAdmin`). |
| `POST` | `/admin/users/{uslug}/promote` | Sets `users.is_admin = true`. **Not yet implemented** — DB method `SetUserAdmin` exists but HTTP handler is not wired. Use direct SQL as a workaround. |
| `GET` | `/admin/users` | Lists all users. |
| `DELETE` | `/admin/users/{id}` | Deletes a user: revokes NATS JWT, DELETEs from Postgres (cascades), publishes NATS delete requests to controllers. |
| `POST` | `/admin/sessions/stop` | Break-glass session stop (records intent in DB). |

### NATS Subjects

Per ADR-0035 the control-plane communicates with controllers over host-scoped NATS subjects on the hub. For the full subject catalog see `spec-state-schema.md` -- NATS Subjects.

**Publishes (request/reply, 10s timeout):**

| Subject | Trigger | Description |
|---------|---------|-------------|
| `mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision` | `POST /api/users/{uslug}/projects` | Asks the host's controller to create the per-project resources. Payload: `{userID, userSlug, hostSlug, projectID, projectSlug, gitUrl, gitIdentityId}` — includes both UUIDs (for K8s resource naming) and slugs (for NATS subjects + env vars). K8s Deployment + PVCs + Secrets for cluster hosts; `~/.mclaude/projects/{pslug}/worktree/` for machine hosts. |
| `mclaude.users.{uslug}.hosts.{hslug}.api.projects.update` | `PATCH /api/projects/{id}` | Asks the controller to apply project metadata changes. |
| `mclaude.users.{uslug}.hosts.{hslug}.api.projects.delete` | Project delete | Asks the controller to tear down per-project resources. |

**Subscribes:**

| Subject | Description |
|---------|-------------|
| `$SYS.ACCOUNT.{accountKey}.CONNECT` | Per-connection event. Switch on payload `client.kind`: `Client` → `SELECT * FROM hosts WHERE public_key = client.nkey AND type = 'machine'`, update `last_seen_at` for that single row, upsert `mclaude-hosts` KV `online=true`. `Leafnode` → `SELECT * FROM hosts WHERE public_key = client.nkey AND type = 'cluster' LIMIT 1`, then update `last_seen_at` for **all** rows where `slug = found.slug AND type = 'cluster'` and upsert KV for each user row (cluster-shared liveness). No match → ignore (covers SPA's per-login ephemeral NKey and control-plane's own connection). |
| `$SYS.ACCOUNT.{accountKey}.DISCONNECT` | Same lookup logic; sets `mclaude-hosts` KV `online=false` for the matched row(s). Does not rewrite `last_seen_at` (it tracks last-known-online). |

Note: The control-plane also subscribes to `mclaude.users.*.api.projects.create` (a user-scoped NATS subject without the host segment) for backward-compatible NATS-based project creation from the SPA. This subject is published by the SPA's `subjProjectsCreate` helper. The handler creates the Postgres row, writes to `mclaude-projects` KV, and publishes a provisioning request to the host-scoped controller subject. This NATS path coexists with the HTTP `POST /api/users/{uslug}/projects` endpoint; both are functional.

### NATS KV Buckets

The control plane ensures these KV buckets exist on startup and writes to them:

- **`mclaude-projects`** -- created by `ensureProjectsKV`; writes on project creation and updates (see `spec-state-schema.md` -- NATS KV Buckets)
- **`mclaude-hosts`** -- created by `ensureHostsKV`; control-plane is the sole writer. Production writes driven by `$SYS.ACCOUNT.*.CONNECT/DISCONNECT`. Dev-seed path: on `DEV_SEED=true`, `seedDev` writes the bootstrap user's `local` host entry with `online=true` (the auto-created `local` host has no NKey and never triggers `$SYS`).
- **`mclaude-sessions`** -- created by `ensureSessionsKV`; bucket creation only, writes are handled by `mclaude-session-agent`.
- **`mclaude-job-queue`** -- created by `ensureJobQueueKV`; bucket creation only, writes are handled by the daemon.

`mclaude-clusters`, `mclaude-laptops`, and `mclaude-heartbeats` are removed per ADR-0035.

### Postgres

Manages the `users`, `projects`, `hosts`, and `oauth_connections` tables. Schema is applied on startup via idempotent DDL (`CREATE TABLE IF NOT EXISTS`, `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`). For full schema, see `spec-state-schema.md` -- Postgres.

### Kubernetes Dependency

The control-plane has **no** K8s client at runtime and creates **no** K8s resources during normal operation. All MCProject CR creation, namespace provisioning, PVC management, RBAC reconciliation, and pod template construction live in `mclaude-controller-k8s` (see `docs/mclaude-controller/spec-controller.md`). The control-plane reaches the controller exclusively via NATS request/reply on `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>`.

The control-plane binary doubles as two Helm pre-install Job entrypoints (the only code paths that use `client-go`):
- **`control-plane init-keys`** — generates operator + account NKey pairs and JWTs, writes them to the `operator-keys` Secret in `mclaude-system`. Idempotent: exits 0 if the Secret already exists. Also creates the bootstrap admin row in Postgres when `BOOTSTRAP_ADMIN_EMAIL` is set. Run by the `mclaude-cp` chart's pre-install Job.
- **`control-plane gen-leaf-creds`** — reads the account seed from the `operator-keys` Secret, generates a NATS user JWT + NKey seed, writes them as a `.creds` file into a `mclaude-worker-nats-leaf-creds` Secret. Idempotent: exits 0 if the leaf-creds Secret already exists. Run by the `mclaude-worker` chart's pre-install Job. Env vars: `NAMESPACE` (default `mclaude-system`), `LEAF_CREDS_SECRET` (default `mclaude-worker-nats-leaf-creds`), `ACCOUNT_SEED_SECRET` (default `operator-keys`), `ACCOUNT_SEED_KEY` (default `accountSeed`).

At runtime, the control-plane mounts one K8s Secret as a file:
- `mclaude-system/operator-keys` — mounted at `OPERATOR_KEYS_PATH` (default `/etc/mclaude/operator-keys`). Read-only. Provides `operatorJwt`, `accountJwt`, `accountSeed`, `operatorSeed` for signing per-host user JWTs and per-cluster leaf JWTs.

## Internal Behavior

### Startup Sequence

1. Connects to Postgres (fatal exit on failure) and runs idempotent schema migration including the ADR-0035 `hosts` table and `projects.host_id` column.
2. Loads OAuth provider config from `PROVIDERS_CONFIG_PATH`, resolving client secrets from K8s Secrets.
3. Loads the account signing key from `NATS_ACCOUNT_SEED` env var. If set, parsed directly as an NKey seed. If not set, generates an ephemeral account key (dev-only — not suitable for production; subjects will not match production JWTs). Fatal exit if signing fails — control-plane cannot mint JWTs without a valid account key.
4. Creates the HTTP server with all route handlers.
5. Connects to hub NATS (retry on failure, unlimited reconnects).
6. Ensures KV buckets exist (`mclaude-projects`, `mclaude-hosts`, `mclaude-sessions`, `mclaude-job-queue`) — `mclaude-hosts` must exist before the `$SYS` subscriber starts so CONNECT events can write to it immediately.
7. Subscribes to `$SYS.ACCOUNT.*.CONNECT` and `$SYS.ACCOUNT.*.DISCONNECT` for host liveness.
8. Starts the GitLab token refresh goroutine (every 15 minutes).
9. Optionally seeds a dev user, a default `local` machine host for that user, and a default project on the `local` host when `DEV_SEED=true`. Also writes the `local` host's `mclaude-hosts` KV entry with `online=true` (dev-only path — the auto-created `local` host has no NKey, so no `$SYS` CONNECT event fires for it). After writing KV entries, publishes a NATS provisioning request (`mclaude.users.{uslug}.hosts.local.api.projects.create`) for the default project so the K8s controller creates the MCProject CR and session-agent pod (ADR-0050). Non-fatal on failure (controller may not be running yet during startup race — controller will process the project when the admin triggers provisioning).
10. Starts the main HTTP listener and the loopback metrics listener.

If `BOOTSTRAP_ADMIN_EMAIL` is set on first boot, control-plane upserts a `users` row with that email, `is_admin=true`, `oauth_id=NULL`. The first OAuth login matching that email links the OAuth identity to the bootstrap row.

### Authentication and JWT Signing

Login validates email and bcrypt password hash (or OAuth identity) against Postgres. On success, the control plane:

1. Loads the calling user's hosts (`SELECT … FROM hosts WHERE user_id = ?`).
2. Selects the host the SPA is requesting access to (defaults to the user's `local` machine host).
3. Generates a fresh NKey user pair and issues a user JWT (`IssueUserJWT(userID, userSlug, accountKP, expirySecs)`) signed by the account signing key from `OPERATOR_KEYS_PATH`. `claims.Name = userID` (UUID, used by `authMiddleware` for DB lookups). Permissions:
   - publish: `mclaude.{userID}.>, mclaude.users.{userSlug}.hosts.*.>, _INBOX.>, $JS.API.>`
   - subscribe: `mclaude.{userID}.>, mclaude.users.{userSlug}.hosts.*.>, _INBOX.>, $JS.API.>, $JS.API.DIRECT.GET.>, $KV.mclaude-projects.{userID}.>, $KV.mclaude-sessions.{userID}.>, $KV.mclaude-hosts.{userSlug}.>`
   Note: `mclaude.{userID}.>` is retained for backward compatibility with un-migrated UUID-format subjects; `mclaude.users.{userSlug}.hosts.*.>` enables ADR-0035 host-scoped subjects. Both are removed when the full subject migration lands.
4. JWT lifetime is `JWT_EXPIRY_SECONDS` (default 8h). The NKey seed is returned alongside the JWT so the client can sign NATS connection nonces.
5. The full Login Response payload (`spec-state-schema.md#login-response-shape`) is returned: `{user, jwt, nkeySeed, hubUrl, hosts[], projects[]}`.

Per-host user JWTs for daemons are minted at `mclaude host register` time (device-code flow) and refreshed via `POST /auth/refresh`.

The auth middleware on protected routes decodes and validates the JWT against the account public key, extracts the user slug + host slug, and enforces two access boundaries: (1) rejects with 403 when the JWT's `sub` user slug does not match the URL's `{uslug}` (cross-user access), and (2) rejects with 403 when the request targets a different host than the JWT was issued for. Admin routes additionally require `users.is_admin = true`.

### OAuth Provider Integration

Supports GitHub and GitLab providers configured via Helm (`providers.json`). OAuth flows use an in-memory state store with 10-minute TTL and cryptographic random state tokens. On successful callback, the control-plane stores connection metadata in the `oauth_connections` Postgres table and publishes a NATS message on `mclaude.users.{uslug}.hosts.{hslug}.api.projects.update` so `mclaude-controller-k8s` (cluster hosts) can refresh the per-user `user-secrets` Secret with the new tokens. For BYOH machines, `mclaude-controller-local` receives the same message and writes credentials into the local credential helpers (`~/.mclaude/projects/{pslug}/.credentials/`).

PAT connections bypass OAuth and auto-detect provider type by probing GitHub and GitLab API endpoints.

After any connection change (connect, disconnect, token refresh), the control-plane re-publishes the update message so `gh-hosts.yml` and `glab-config.yml` reconciliation continues to land on the correct host (cluster or machine). The control-plane never writes to K8s Secrets directly — that lives in `mclaude-controller-k8s`.

### GitLab Token Refresh

A background goroutine runs every 15 minutes, querying `oauth_connections` for GitLab tokens expiring within 30 minutes. For each, it exchanges the refresh token for a new access token, updates the DB, and republishes a `…api.projects.update` NATS message so the controller responsible for the affected hosts can rotate credentials. If the refresh token is expired or revoked, the connection is automatically deleted.

### Project Creation Flow

1. SPA publishes to `mclaude.users.{uslug}.api.projects.create` (NATS) with `{name, hostSlug, gitUrl?, gitIdentityId?}`. (HTTP `POST /api/users/{uslug}/projects` is spec'd but not yet implemented.)
2. Control plane validates the request, including optional `gitIdentityId` hostname match, and verifies the calling user has access to `hostSlug`.
3. Resolves `host_id` from `(user_id, hostSlug)`.
4. Creates a Postgres `projects` row (`host_id`, `slug`, `name`, `git_url`, optional `git_identity_id`).
5. Writes the project to the `mclaude-projects` KV bucket (key `{userId}.{projectId}` — UUID-based; migration to `{uslug}.{hslug}.{pslug}` deferred per ADR-0050).
6. Publishes a NATS request on `mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision` with `{userID, userSlug, hostSlug, projectID, projectSlug, gitUrl, gitIdentityId}`. Awaits the controller's reply (`PROVISION_TIMEOUT_SECONDS`).
   - For cluster hosts: `mclaude-controller-k8s` (subscribed to `mclaude.users.*.hosts.{cslug}.api.projects.>`) creates the `MCProject` CR in `mclaude-system` and reconciles namespace/RBAC/PVCs/Secrets/Deployment.
   - For machine hosts: `mclaude-controller-local` (subscribed to its own user/host's wildcard) materializes `~/.mclaude/projects/{pslug}/worktree/` and starts a session-agent subprocess.
7. Replies to the SPA with `{projectId, slug, hostSlug, status}`.

If the controller times out or replies with an error, control-plane returns `503 Service Unavailable` with `{error: "host {hslug} unreachable"}` and rolls back the Postgres + KV writes (or marks `projects.status = 'failed'` — whichever the implementation chooses; see ADR-0035 Error Handling row "Project create on offline cluster").

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

- **Postgres**: user, project, host, and OAuth connection persistence. Pool: 2-10 connections.
- **Hub NATS**: provisioning request publisher, KV bucket management, `$SYS` presence subscriber. Connects with retry-on-fail and unlimited reconnects.
- **`mclaude-system/operator-keys` Secret**: mounted at `OPERATOR_KEYS_PATH`. Provides operator + account NKeys / JWTs for the trust chain. Generated by the `mclaude-cp` Helm pre-install Job (`mclaude-cp init-keys`).
- **`mclaude-controller-k8s`** (one per worker cluster): receives `mclaude.users.*.hosts.{cslug}.api.projects.>` requests over the leaf-node link. Lives in `docs/mclaude-controller/spec-controller.md`.
- **`mclaude-controller-local`** (one per BYOH machine): receives `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` requests directly from hub NATS via the user's session-agent NATS connection. Lives in `docs/mclaude-controller/spec-controller.md`.
- **OAuth providers** (optional): GitHub and/or GitLab instances for token exchange, profile fetch, token revocation, token refresh, and repo listing.
- **OpenTelemetry** (optional): tracer provider for distributed tracing (HTTP, DB, NATS request spans).

The control-plane has no Kubernetes API client and does not depend on the K8s API for runtime operations.
