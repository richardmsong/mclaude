# Spec: Control Plane

## Role

The control plane is the central API server and trust root for mclaude. It authenticates users, issues per-host NATS JWTs (signed by the deployment-level account key), manages user / project / host records in Postgres, publishes provisioning requests over NATS to the appropriate controller (`mclaude-controller-k8s` for cluster hosts, `mclaude-controller-local` for BYOH machines), handles OAuth provider integrations (GitHub/GitLab), tracks host liveness via `$SYS` events, and exposes admin endpoints for cluster registration and access grants.

Per ADR-0035 the control-plane is **K8s-free**: no controller-runtime, no K8s client, no MCProject reconciler. All K8s mutation is delegated to `mclaude-controller-k8s` via NATS request/reply. The control-plane runs only inside the central `mclaude-cp` Kubernetes cluster (there is no local/standalone variant).

## Deployment

Runs as a Kubernetes Deployment in the `mclaude-system` namespace of the central `mclaude-cp` cluster, built from an Alpine-based container image. Listens on two ports: the main API port (default 8080) for public and authenticated routes, and a loopback-only admin port (default 9091) for break-glass operations and Prometheus metrics. The admin endpoints under `/admin/*` (cluster register, grant, etc.) are served on the **main** port and protected by per-user `Authorization: Bearer <token>` plus a server-side `users.is_admin` check, so admin CLIs work over the public ingress.

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `EXTERNAL_URL` | Yes | (none -- exits on startup if empty) | Externally-accessible base URL (e.g. `https://dev.mclaude.richardmcsong.com`) |
| `DATABASE_URL` / `DATABASE_DSN` | Yes | (none -- exits on startup if empty) | Postgres connection string. Hosts/users/projects persistence is required for ADR-0035. |
| `NATS_URL` | No | `nats://localhost:4222` | Internal hub NATS broker URL |
| `NATS_WS_URL` | No | (empty) | External WebSocket URL for browser clients; empty means client derives from origin |
| `OPERATOR_KEYS_PATH` | No | `/etc/mclaude/operator-keys` | Mount path for the `mclaude-system/operator-keys` Secret. Reads `operatorJwt`, `accountJwt`, `accountSeed`, `operatorSeed`. Required to sign per-host user JWTs. |
| `BOOTSTRAP_ADMIN_EMAIL` | No | (empty) | Email of the first admin. Read from Helm value; the init-keys Job pre-creates a `users` row with `is_admin=true` and `oauth_id=NULL`; first OAuth login linking that email promotes the user. |
| `PORT` | No | `8080` | Main API listen port |
| `ADMIN_PORT` | No | `9091` | Loopback-only port for `/metrics` and break-glass routes. `/admin/*` routes (cluster register, grant) live on the main port and use bearer-token auth. |
| `JWT_EXPIRY_SECONDS` | No | `28800` (8h) | Per-host user JWT lifetime in seconds |
| `DEV_OAUTH_TOKEN` | No | (empty) | Injected into per-user secrets (cluster controller copies into the user namespace) for dev environments |
| `DEV_SEED` | No | `false` | When `true`, creates a dev user (`dev@mclaude.local` / `dev`), a default `local` machine host, and a default project on startup |
| `MIN_CLIENT_VERSION` | No | `0.0.0` | Minimum SPA/CLI version reported by `/version` |
| `SERVER_VERSION` | No | (empty) | Server version string reported by `/version` |
| `PROVIDERS_CONFIG_PATH` | No | `/etc/mclaude/providers.json` | Path to OAuth provider config (Helm ConfigMap mount) |
| `PROVISION_TIMEOUT_SECONDS` | No | `10` | Per-request timeout for NATS provisioning request/reply (`mclaude.users.{uslug}.hosts.{hslug}.api.projects.*`) |

## Interfaces

### HTTP Endpoints -- Main Port

**Public (no auth):**

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/auth/login` | Authenticate with email+password; returns the [Login Response](../spec-state-schema.md#login-response-shape) — per-host user JWT, NKey seed, hub URL, host inventory, and projects |
| `POST` | `/auth/refresh` | Exchange a valid per-host JWT from the Authorization header for a new JWT (same host scope) |
| `GET` | `/version` | Returns `minClientVersion` and `serverVersion` |
| `GET` | `/health` | Returns 200 OK |
| `GET` | `/healthz` | Kubernetes liveness probe |
| `GET` | `/readyz` | Kubernetes readiness probe |
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
| `POST` | `/api/users/{uslug}/projects` | Creates a project on a specified host (`{name, hostSlug, gitUrl?}`). Writes Postgres `projects` row + `mclaude-projects` KV, then publishes NATS request to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision` and waits for the controller's reply. |
| `GET` | `/api/users/{uslug}/hosts` | Lists hosts owned by or granted to the user. |
| `POST` | `/api/users/{uslug}/hosts/code` | Generates a 6-character device code for BYOH host registration. Accepts `{publicKey}` — the host's NKey public key, generated locally by the CLI. The server stores the public key with the code record. Returns `{code, expiresAt}` (10-minute TTL). |
| `GET` | `/api/users/{uslug}/hosts/code/{code}` | Polls device-code status. Returns `{status: "pending", expiresAt}` while waiting for dashboard redemption, or `{status: "completed", slug, jwt, hubUrl}` once redeemed. Returns 410 Gone if expired, 404 if not found. The CLI polls this endpoint after generating the code. |
| `POST` | `/api/hosts/register` | Redeems a device code from the dashboard with `{code, name}`. Control-plane looks up the stored `publicKey` from the code record, creates a `hosts` row with `type='machine'`, `role='owner'`, `public_key=<stored publicKey>`, mints a per-host user JWT signed against that public key, and returns `{slug, jwt, hubUrl}`. The private seed never leaves the host. |
| `PUT` | `/api/users/{uslug}/hosts/{hslug}` | Updates host display name. |
| `DELETE` | `/api/users/{uslug}/hosts/{hslug}` | Removes a host (cascades to its projects + sessions). For cluster hosts owned by other users, only the registering admin can delete. |

**Admin-only (bearer token + server-side `users.is_admin = true` check):**

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/admin/clusters` | Registers a new cluster (`{slug, name, jsDomain, leafUrl, directNatsUrl?}`). Generates a per-cluster NKey pair, then creates a `hosts` row with `type='cluster'`, `role='owner'` for the calling admin (cluster-shared fields populated, including `public_key=<cluster NKey pubkey>` and the optional `direct_nats_url`); mints a per-cluster leaf/controller JWT (signed by the account key) scoped to `mclaude.users.*.hosts.{slug}.>`; returns `{slug, leafJwt, leafSeed, accountJwt, operatorJwt, jsDomain, directNatsUrl}` for the admin to drop into the worker cluster's NATS Secret + Helm values. |
| `GET` | `/admin/clusters` | Lists registered clusters (deduplicated across user rows). |
| `POST` | `/admin/clusters/{cslug}/grants` | Grants user access (`{userSlug}`). Creates a new `hosts` row for that user with `slug=cslug`, `type='cluster'`, `role='user'`; copies cluster-shared fields (`js_domain`, `leaf_url`, `account_jwt`, `direct_nats_url`, `public_key`) from the existing cluster host; mints a per-user JWT scoped to `mclaude.users.{userSlug}.hosts.{cslug}.>`. |
| `DELETE` | `/admin/clusters/{cslug}` | Removes the cluster — deletes all `hosts` rows where `slug=cslug AND type='cluster'`, cascading to projects/sessions. In-flight session cleanup is manual (out of scope for v1). |
| `POST` | `/admin/users` | Creates a user (id, email, name, optional password, optional `isAdmin`). |
| `POST` | `/admin/users/{uslug}/promote` | Sets `users.is_admin = true`. |
| `GET` | `/admin/users` | Lists all users. |
| `DELETE` | `/admin/users/{id}` | Deletes a user (cascades to hosts, projects, connections). |
| `POST` | `/admin/sessions/stop` | Break-glass session stop (records intent in DB). |

### HTTP Endpoints -- Loopback Port (9091)

The loopback-only port hosts only Prometheus metrics. The previous `/admin/*` endpoints have moved to the main port (above) because admins manage clusters / users from their CLI over the public ingress; bearer-token auth + `is_admin` check provides the same privilege gate without requiring loopback access.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/metrics` | Prometheus metrics |

### NATS Subjects

Per ADR-0035 the control-plane communicates with controllers over host-scoped NATS subjects on the hub. For the full subject catalog see `spec-state-schema.md` -- NATS Subjects.

**Publishes (request/reply, 10s timeout):**

| Subject | Trigger | Description |
|---------|---------|-------------|
| `mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision` | `POST /api/users/{uslug}/projects` | Asks the host's controller to create the per-project resources (K8s Deployment + PVCs + Secrets for cluster hosts; `~/.mclaude/projects/{pslug}/worktree/` for machine hosts). |
| `mclaude.users.{uslug}.hosts.{hslug}.api.projects.update` | `PATCH /api/projects/{id}` | Asks the controller to apply project metadata changes. |
| `mclaude.users.{uslug}.hosts.{hslug}.api.projects.delete` | Project delete | Asks the controller to tear down per-project resources. |

**Subscribes:**

| Subject | Description |
|---------|-------------|
| `$SYS.ACCOUNT.{accountKey}.CONNECT` | Per-connection event. Switch on payload `client.kind`: `Client` → `SELECT * FROM hosts WHERE public_key = client.nkey AND type = 'machine'`, update `last_seen_at` for that single row, upsert `mclaude-hosts` KV `online=true`. `Leafnode` → `SELECT * FROM hosts WHERE public_key = client.nkey AND type = 'cluster' LIMIT 1`, then update `last_seen_at` for **all** rows where `slug = found.slug AND type = 'cluster'` and upsert KV for each user row (cluster-shared liveness). No match → ignore (covers SPA's per-login ephemeral NKey and control-plane's own connection). |
| `$SYS.ACCOUNT.{accountKey}.DISCONNECT` | Same lookup logic; sets `mclaude-hosts` KV `online=false` for the matched row(s). Does not rewrite `last_seen_at` (it tracks last-known-online). |

There is no `mclaude.*.api.projects.create` subscriber on control-plane any more — projects are created via HTTP (`POST /api/users/{uslug}/projects`); the NATS publish flows the other direction (control-plane → controller).

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

The control-plane has **no** K8s client and creates **no** K8s resources at runtime. All MCProject CR creation, namespace provisioning, PVC management, RBAC reconciliation, and pod template construction live in `mclaude-controller-k8s` (see `docs/mclaude-controller/spec-controller.md`). The control-plane reaches the controller exclusively via NATS request/reply on `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>`.

The control-plane does, however, mount one K8s Secret as a file:
- `mclaude-system/operator-keys` — mounted at `OPERATOR_KEYS_PATH` (default `/etc/mclaude/operator-keys`). Read-only. Provides `operatorJwt`, `accountJwt`, `accountSeed`, `operatorSeed` for signing per-host user JWTs and per-cluster leaf JWTs. Generated on first install by the Helm pre-install Job (`mclaude-cp init-keys`); reused on subsequent deploys.

## Internal Behavior

### Startup Sequence

1. Connects to Postgres (fatal exit on failure) and runs idempotent schema migration including the ADR-0035 `hosts` table and `projects.host_id` column.
2. Loads OAuth provider config from `PROVIDERS_CONFIG_PATH`, resolving client secrets from K8s Secrets.
3. Loads operator + account NKeys from `OPERATOR_KEYS_PATH` (the mounted Secret produced by the `init-keys` Helm pre-install Job). Fatal exit if missing — control-plane cannot mint JWTs without them.
4. Creates the HTTP server with all route handlers.
5. Connects to hub NATS (retry on failure, unlimited reconnects).
6. Subscribes to `$SYS.ACCOUNT.*.CONNECT` and `$SYS.ACCOUNT.*.DISCONNECT` for host liveness.
7. Ensures KV buckets exist (`mclaude-projects`, `mclaude-hosts`, `mclaude-sessions`, `mclaude-job-queue`).
8. Starts the GitLab token refresh goroutine (every 15 minutes).
9. Optionally seeds a dev user, a default `local` machine host for that user, and a default project on the `local` host when `DEV_SEED=true`. Also writes the `local` host's `mclaude-hosts` KV entry with `online=true` (dev-only path — the auto-created `local` host has no NKey, so no `$SYS` CONNECT event fires for it).
10. Starts the main HTTP listener and the loopback metrics listener.

If `BOOTSTRAP_ADMIN_EMAIL` is set on first boot, control-plane upserts a `users` row with that email, `is_admin=true`, `oauth_id=NULL`. The first OAuth login matching that email links the OAuth identity to the bootstrap row.

### Authentication and JWT Signing

Login validates email and bcrypt password hash (or OAuth identity) against Postgres. On success, the control plane:

1. Loads the calling user's hosts (`SELECT … FROM hosts WHERE user_id = ?`).
2. Selects the host the SPA is requesting access to (defaults to the user's `local` machine host).
3. Generates a fresh NKey user pair and issues a per-host user JWT signed by the account signing key from `OPERATOR_KEYS_PATH`. Permissions per ADR-0035:
   - publish: `mclaude.users.{uslug}.hosts.{hslug}.>, _INBOX.>, $JS.*.API.>, $SYS.ACCOUNT.*.CONNECT, $SYS.ACCOUNT.*.DISCONNECT`
   - subscribe: `mclaude.users.{uslug}.hosts.{hslug}.>, _INBOX.>, $JS.*.API.>`
4. JWT lifetime is `JWT_EXPIRY_SECONDS` (default 8h). The NKey seed is returned alongside the JWT so the client can sign NATS connection nonces.
5. The full Login Response payload (`spec-state-schema.md#login-response-shape`) is returned: `{user, jwt, nkeySeed, hubUrl, hosts[], projects[]}`.

Per-host user JWTs for daemons are minted at `mclaude host register` time (device-code flow) and refreshed via `POST /auth/refresh`.

The auth middleware on protected routes decodes and validates the JWT against the account public key, extracts the user slug + host slug, and rejects requests targeting a different host than the JWT was issued for. Admin routes additionally require `users.is_admin = true`.

### OAuth Provider Integration

Supports GitHub and GitLab providers configured via Helm (`providers.json`). OAuth flows use an in-memory state store with 10-minute TTL and cryptographic random state tokens. On successful callback, the control-plane stores connection metadata in the `oauth_connections` Postgres table and publishes a NATS message on `mclaude.users.{uslug}.hosts.{hslug}.api.projects.update` so `mclaude-controller-k8s` (cluster hosts) can refresh the per-user `user-secrets` Secret with the new tokens. For BYOH machines, `mclaude-controller-local` receives the same message and writes credentials into the local credential helpers (`~/.mclaude/projects/{pslug}/.credentials/`).

PAT connections bypass OAuth and auto-detect provider type by probing GitHub and GitLab API endpoints.

After any connection change (connect, disconnect, token refresh), the control-plane re-publishes the update message so `gh-hosts.yml` and `glab-config.yml` reconciliation continues to land on the correct host (cluster or machine). The control-plane never writes to K8s Secrets directly — that lives in `mclaude-controller-k8s`.

### GitLab Token Refresh

A background goroutine runs every 15 minutes, querying `oauth_connections` for GitLab tokens expiring within 30 minutes. For each, it exchanges the refresh token for a new access token, updates the DB, and republishes a `…api.projects.update` NATS message so the controller responsible for the affected hosts can rotate credentials. If the refresh token is expired or revoked, the connection is automatically deleted.

### Project Creation Flow

1. SPA calls `POST /api/users/{uslug}/projects` with `{name, hostSlug, gitUrl?, gitIdentityId?}`.
2. Control plane validates the request, including optional `gitIdentityId` hostname match, and verifies the calling user has access to `hostSlug`.
3. Resolves `host_id` from `(user_id, hostSlug)`.
4. Creates a Postgres `projects` row (`host_id`, `slug`, `name`, `git_url`, optional `git_identity_id`).
5. Writes the project to the `mclaude-projects` KV bucket (key `{uslug}.{hslug}.{pslug}`).
6. Publishes a NATS request on `mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision` with `{userSlug, hostSlug, projectSlug, gitUrl, gitIdentityId}`. Awaits the controller's reply (`PROVISION_TIMEOUT_SECONDS`).
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

## Dependencies

- **Postgres**: user, project, host, and OAuth connection persistence. Pool: 2-10 connections.
- **Hub NATS**: provisioning request publisher, KV bucket management, `$SYS` presence subscriber. Connects with retry-on-fail and unlimited reconnects.
- **`mclaude-system/operator-keys` Secret**: mounted at `OPERATOR_KEYS_PATH`. Provides operator + account NKeys / JWTs for the trust chain. Generated by the `mclaude-cp` Helm pre-install Job (`mclaude-cp init-keys`).
- **`mclaude-controller-k8s`** (one per worker cluster): receives `mclaude.users.*.hosts.{cslug}.api.projects.>` requests over the leaf-node link. Lives in `docs/mclaude-controller/spec-controller.md`.
- **`mclaude-controller-local`** (one per BYOH machine): receives `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` requests directly from hub NATS via the user's session-agent NATS connection. Lives in `docs/mclaude-controller/spec-controller.md`.
- **OAuth providers** (optional): GitHub and/or GitLab instances for token exchange, profile fetch, token revocation, token refresh, and repo listing.
- **OpenTelemetry** (optional): tracer provider for distributed tracing (HTTP, DB, NATS request spans).

The control-plane has no Kubernetes API client and does not depend on the K8s API for runtime operations.
