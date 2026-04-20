# Spec: Control Plane

## Role

The control plane is the central API server and orchestration layer for mclaude. It authenticates users, issues NATS JWTs, manages user and project records in Postgres, provisions per-project Kubernetes resources via a controller-runtime reconciler, handles OAuth provider integrations (GitHub/GitLab), and exposes a break-glass admin API. It bridges browser clients to NATS by issuing scoped credentials and bridges the SPA to Kubernetes by reconciling MCProject custom resources into namespaces, deployments, secrets, and PVCs.

## Deployment

Runs as a single-replica Kubernetes pod in the `mclaude-system` namespace, built from an Alpine-based container image. Listens on two ports: the main API port (default 8080) for public and authenticated routes, and a loopback-only admin port (default 9091) for break-glass operations and Prometheus metrics.

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `EXTERNAL_URL` | Yes | (none -- exits on startup if empty) | Externally-accessible base URL (e.g. `https://mclaude.internal`) |
| `DATABASE_URL` / `DATABASE_DSN` | No | (none) | Postgres connection string; runs without persistence if unset |
| `NATS_URL` | No | `nats://localhost:4222` | Internal NATS broker URL |
| `NATS_WS_URL` | No | (empty) | External WebSocket URL for browser clients; empty means client derives from origin |
| `NATS_ACCOUNT_SEED` | No | (ephemeral generated) | NKey account seed for signing JWTs; generate ephemeral if unset (dev only) |
| `ADMIN_TOKEN` | No | (empty) | Static bearer token for admin port; empty disables admin access |
| `HELM_RELEASE_NAME` | No | `mclaude` | Used to locate the session-agent-template ConfigMap |
| `PORT` | No | `8080` | Main API listen port |
| `ADMIN_PORT` | No | `9091` | Admin API listen port |
| `JWT_EXPIRY_SECONDS` | No | `28800` (8h) | NATS user JWT lifetime in seconds |
| `DEV_OAUTH_TOKEN` | No | (empty) | Injected into user-secrets for dev environments |
| `DEV_SEED` | No | `false` | When `true`, creates a dev user (`dev@mclaude.local` / `dev`) and default project on startup |
| `MIN_CLIENT_VERSION` | No | `0.0.0` | Minimum SPA/CLI version reported by `/version` |
| `SERVER_VERSION` | No | (empty) | Server version string reported by `/version` |
| `PROVIDERS_CONFIG_PATH` | No | `/etc/mclaude/providers.json` | Path to OAuth provider config (Helm ConfigMap mount) |

## Interfaces

### HTTP Endpoints -- Main Port

**Public (no auth):**

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/auth/login` | Authenticate with email+password; returns NATS JWT, NKey seed, and user ID |
| `POST` | `/auth/refresh` | Exchange a valid JWT from the Authorization header for a new JWT |
| `GET` | `/version` | Returns `minClientVersion` and `serverVersion` |
| `GET` | `/health` | Returns 200 OK |
| `GET` | `/healthz` | Kubernetes liveness probe |
| `GET` | `/readyz` | Kubernetes readiness probe |
| `GET` | `/auth/providers/{id}/callback` | OAuth callback -- exchanges code for token, stores connection, redirects browser |

**Protected (NATS JWT in Authorization header):**

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/auth/me` | Returns authenticated user info and connected OAuth providers |
| `GET` | `/api/providers` | Lists admin-configured OAuth providers (from Helm) |
| `POST` | `/api/providers/pat` | Adds a Personal Access Token connection (auto-detects GitHub or GitLab) |
| `POST` | `/api/providers/{id}/connect` | Initiates OAuth flow for an admin-configured provider; returns `{redirectUrl}` |
| `GET` | `/api/connections/{id}/repos` | Lists repositories accessible via a connected provider |
| `DELETE` | `/api/connections/{id}` | Disconnects a provider (revokes token, removes secrets, deletes DB row) |
| `PATCH` | `/api/projects/{id}` | Updates a project's `gitIdentityId` |

### HTTP Endpoints -- Admin Port (9091, loopback only)

All routes require the `ADMIN_TOKEN` bearer token.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/admin/users` | Lists all users |
| `POST` | `/admin/users` | Creates a user (id, email, name, optional password) |
| `DELETE` | `/admin/users/{id}` | Deletes a user (cascades to projects and connections) |
| `POST` | `/admin/sessions/stop` | Break-glass session stop (records intent in DB) |
| `GET` | `/metrics` | Prometheus metrics |

### NATS Subjects

The control plane subscribes and publishes on core NATS subjects. For the full subject catalog, see `spec-state-schema.md` -- NATS Subjects.

**Subscriptions:**

| Subject | Protocol | Description |
|---------|----------|-------------|
| `mclaude.*.api.projects.create` | Request/reply | Creates a project (DB row + KV entry + MCProject CR) |

**Publishes:**

| Subject | Description |
|---------|-------------|
| `mclaude.users.{uslug}.api.projects.updated` | Notifies SPA after project status changes |

### NATS KV Buckets

The control plane ensures these KV buckets exist on startup and writes to them:

- **`mclaude-projects`** -- created by `ensureProjectsKV`; writes on project creation and updates (see `spec-state-schema.md` -- NATS KV Buckets)
- **`mclaude-job-queue`** -- created by `ensureJobQueueKV`; bucket creation only, writes are handled by the daemon

### Postgres

Manages the `users`, `projects`, and `oauth_connections` tables. Schema is applied on startup via idempotent DDL (`CREATE TABLE IF NOT EXISTS`, `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`). For full schema, see `spec-state-schema.md` -- Postgres.

### Kubernetes Resources Created/Watched

The control plane creates and reconciles the following per-user/per-project Kubernetes resources. For full resource schemas, see `spec-state-schema.md` -- Kubernetes Resources.

**CRD: `MCProject` (`mcprojects.mclaude.io/v1alpha1`)**
- Scope: Namespaced in `mclaude-system`
- Created on project creation via NATS handler or dev seed
- Drives the reconciler loop

**Per-user namespace (`mclaude-{userId}`)** containing:
- `ServiceAccount` (`mclaude-sa`), `Role` (`mclaude-role`), `RoleBinding` (`mclaude-role`)
- `Secret` (`user-secrets`) -- NATS creds, OAuth tokens, CLI config files
- `ConfigMap` (`user-config`) -- Claude Code seed configuration
- ImagePullSecrets (copied from `mclaude-system`)

**Per-project resources** in the user namespace:
- `PVC` (`project-{projectId}`) -- project data
- `PVC` (`nix-{projectId}`) -- Nix store cache
- `Deployment` (`project-{projectId}`) -- session-agent pod (replicas: 1, Recreate strategy)

**Watched in control-plane namespace:**
- `ConfigMap` (`{release}-session-agent-template`) -- image, resource limits, PVC sizes, corporate CA settings. Changes re-enqueue all MCProject CRs.

## Internal Behavior

### Startup Sequence

1. Connects to Postgres and runs idempotent schema migration.
2. Loads OAuth provider config from `PROVIDERS_CONFIG_PATH`, resolving client secrets from K8s Secrets.
3. Loads or generates the NATS account NKey.
4. Initializes the K8s provisioner (nil if not running in cluster).
5. Creates a controller-runtime Manager and registers the MCProject reconciler (when in cluster).
6. Creates the HTTP server with all route handlers.
7. Connects to NATS (retry on failure, unlimited reconnects).
8. Starts the NATS projects subscriber (`mclaude.*.api.projects.create`).
9. Starts the GitLab token refresh goroutine (every 15 minutes).
10. Runs startup CLI config reconcile for all users (background goroutine), cleaning up orphaned OAuth connections.
11. Optionally seeds a dev user and default project when `DEV_SEED=true`.
12. Starts the main HTTP listener and admin HTTP listener.

### Reconciler Loop

The MCProject reconciler is a controller-runtime reconciler that watches MCProject CRs and all owned resources (Deployments, PVCs, Secrets, ConfigMaps, ServiceAccounts). On each reconcile cycle it:

1. Loads the session-agent-template ConfigMap for image, resource, and PVC configuration.
2. Ensures the user namespace exists with correct labels (including `mclaude.io/user-namespace: "true"` when corporate CA is enabled for trust-manager targeting).
3. Ensures RBAC resources (ServiceAccount, Role, RoleBinding).
4. Ensures user-config ConfigMap and user-secrets Secret (with NATS credentials and optional dev OAuth token).
5. Copies imagePullSecrets from the control-plane namespace.
6. Ensures project PVC and Nix PVC.
7. Ensures the session-agent Deployment with Recreate strategy.
8. Updates MCProject status phase (Pending -> Provisioning -> Ready or Failed) and conditions (NamespaceReady, RBACReady, SecretsReady, DeploymentReady).

The reconciler also watches the session-agent-template ConfigMap in the control-plane namespace. When it changes (e.g., new image tag after Helm upgrade), all MCProject CRs are re-enqueued so deployments pick up the new configuration.

On every update reconcile, the full pod template (env vars, image, volumes, imagePullSecrets, annotations) is rebuilt and applied. Kubernetes only triggers a rollout if the template actually changed.

### Corporate CA Support

When the session-agent-template ConfigMap has `corporateCAEnabled: "true"`, the reconciler:
- Adds label `mclaude.io/user-namespace: "true"` to user namespaces so trust-manager syncs the CA bundle ConfigMap.
- Injects a `corporate-ca` volume, volume mount at `/etc/ssl/certs/corporate-ca-certificates.crt`, and `NODE_EXTRA_CA_CERTS` env var into the pod template.
- Annotates the pod template with `mclaude.io/ca-bundle-hash` (SHA-256 of ConfigMap data) so CA rotations trigger a Recreate rollout.

### Authentication and JWT Signing

Login validates email and bcrypt password hash against Postgres. On success, the control plane generates a fresh NKey user pair and issues a NATS user JWT signed by the account key pair. The JWT is scoped to `mclaude.{userId}.>` with `_INBOX.>` for request/reply. JWTs expire per `JWT_EXPIRY_SECONDS` (default 8h). The seed is returned alongside the JWT so the client can sign NATS connection nonces. The auth middleware on protected routes decodes and validates the JWT to extract the user ID.

Session-agent JWTs are long-lived (no expiry), scoped to `mclaude.{userId}.>` without `_INBOX.>`, and stored as NATS credential files in the user-secrets Secret.

### OAuth Provider Integration

Supports GitHub and GitLab providers configured via Helm (`providers.json`). OAuth flows use an in-memory state store with 10-minute TTL and cryptographic random state tokens. On successful callback, tokens are stored in the user-secrets K8s Secret and connection metadata in the `oauth_connections` Postgres table. PAT connections bypass OAuth and auto-detect provider type by probing GitHub and GitLab API endpoints.

After any connection change (connect, disconnect, token refresh), the control plane reconciles `gh-hosts.yml` and `glab-config.yml` in user-secrets so the session-agent `gh` and `glab` CLIs have current credentials.

### GitLab Token Refresh

A background goroutine runs every 15 minutes, querying `oauth_connections` for GitLab tokens expiring within 30 minutes. For each, it exchanges the refresh token for a new access token, updates the K8s Secret and DB, and rebuilds CLI config. If the refresh token is expired or revoked, the connection is automatically deleted.

### Project Creation Flow

1. SPA publishes to `mclaude.users.{uslug}.api.projects.create` (request/reply).
2. Control plane validates the request, including optional `gitIdentityId` hostname match.
3. Creates a Postgres `projects` row (with optional `git_identity_id`).
4. Creates an MCProject CR in `mclaude-system` (or falls back to direct provisioning).
5. Writes the project to the `mclaude-projects` KV bucket.
6. Replies with the new project ID.
7. The reconciler detects the new MCProject CR and provisions K8s resources.

## Error Handling

- **Postgres unavailable at startup**: fatal exit.
- **Postgres unavailable at runtime**: affected HTTP handlers return 503; admin routes return 503. NATS project creation replies with error.
- **NATS connection failure**: retries indefinitely with unlimited reconnects. KV writes are best-effort; DB row is authoritative.
- **K8s provisioner unavailable**: logged as warning; project creation succeeds (DB + KV) but session-agent pod does not start. Logged as error.
- **Reconciler failures**: individual conditions (NamespaceReady, RBACReady, SecretsReady, DeploymentReady) are set to False with error message. MCProject phase set to Provisioning or Failed. Requeued after 15-30 seconds.
- **Session-agent-template ConfigMap missing**: reconciler uses safe defaults (latest image, 10Gi PVCs, 200m/256Mi requests).
- **OAuth callback errors**: browser redirected to return URL with `?error={code}` (csrf, denied, exchange_failed, profile_failed, storage, gitlab_one_identity).
- **OAuth token exchange failure**: logged; browser redirected with error.
- **PAT validation failure**: returns 400 with descriptive error (invalid token vs. unreachable provider).
- **GitLab refresh failure**: logged as warning per connection; expired refresh tokens trigger automatic connection deletion.
- **K8s Secret conflict on patch**: retried once (optimistic concurrency).
- **EXTERNAL_URL not set**: fatal exit on startup.
- **Duplicate user email**: admin create returns 409 Conflict.

## Dependencies

- **Postgres**: user, project, and OAuth connection persistence. Pool: 2-10 connections.
- **NATS**: project creation subscriber, KV bucket management. Connects with retry-on-fail and unlimited reconnects.
- **Kubernetes API**: MCProject CRD reconciliation, namespace/deployment/secret/configmap/PVC management, imagePullSecret copying, provider client secret resolution. Nil when not running in cluster.
- **OAuth providers** (optional): GitHub and/or GitLab instances for token exchange, profile fetch, token revocation, token refresh, and repo listing.
- **Session-agent-template ConfigMap**: Helm-deployed ConfigMap in `mclaude-system` providing image, resource, PVC, and corporate CA configuration for session-agent pods.
- **OpenTelemetry** (optional): tracer provider for distributed tracing (HTTP, DB, provisioning spans).
