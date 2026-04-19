# Multi-Cluster Architecture

**Status**: accepted
**Status history**:
- 2026-04-15: accepted


## Overview

Separates mclaude into a central control plane and N independent worker clusters. The control plane handles authentication, authorization, cluster registry, project-to-cluster mapping, and discovery. Each worker cluster runs its own NATS and session-agents. Workers connect to the control plane's hub NATS as leaf nodes, giving the SPA a single connection point for real-time monitoring across all clusters. Clients prefer direct NATS connections to workers for active sessions, falling back through the hub when direct access is unavailable.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| NATS topology | Leaf nodes (hub on CP, workers as leaves) | Core pub/sub flows automatically between hub and leaves. JetStream isolation per worker via domains. Single connection point for clients that can't reach workers directly. |
| SPA session list | Domain-qualified KV watches across all clusters | The session list is a real-time monitoring dashboard, not just a picker. Users need instant visibility into session state changes across all clusters. KV watch traffic is lightweight (fires only on state changes). |
| Global metadata | Postgres on control plane | Cluster registry and project-to-cluster mapping are global concerns. Session state stays on worker NATS KV as today. Control plane joins tables to return per-project cluster/domain info at login. |
| Session placement | Project affinity + user-specified + RBAC | Projects are bound to a cluster at creation time. Users can specify a cluster if they have access. RBAC controls which clusters a user can use. |
| Client proxy | Hub NATS on control plane | Client connects to hub NATS via WebSocket. Leaf node routing delivers messages to the correct worker. No HTTP proxy needed — NATS handles it. |
| Leaf node auth | Shared credentials from control plane | CP generates leaf NKey pair at cluster registration, stores in Postgres, returns to worker. Worker configures NATS with the credential. |
| Cluster RBAC | Explicit grant required | Users must be granted access to each cluster. First grant becomes default. May evolve to support load-balanced auto-assignment. |
| Backwards compatibility | Single-cluster = degenerate multi-cluster | A single-cluster deployment is a control plane + one worker on the same cluster. No separate "standalone" mode — the existing chart becomes the worker chart with CP embedded via values. |
| Provisioning | Worker controller via NATS | Each worker runs its own controller. CP publishes provisioning requests through leaf nodes. Worker controller creates CRDs locally. No remote kubeconfig needed. |
| Helm charts | Separate: mclaude-cp + mclaude-worker | Control plane chart deploys hub NATS, Postgres, CP server, SPA. Worker chart deploys worker NATS (leaf config), worker controller, session-agent template. Independent lifecycle. |
| NATS auth | Shared account key | One account NKey across hub and all workers. CP signs user JWTs once — valid everywhere. Direct-to-worker and hub connections use the same JWT. CP is already the single trust root. |
| Client connection | Direct to worker preferred, hub fallback | Login returns both the hub NATS URL and the worker's direct NATS URL per project. Client tries direct first. If unreachable, connects to hub and uses domain routing. No re-auth needed (shared account key). |

## User Flow

1. **Login**: Client authenticates with control plane (`POST /auth/login`). Response includes the hub NATS URL, user JWT, NKey seed, and a list of projects with their cluster assignments (cluster ID, JetStream domain, direct NATS URL).

2. **Dashboard**: SPA connects to hub NATS via WebSocket. Opens one domain-qualified KV watch per cluster the user has projects on (`mclaude-sessions` bucket, key prefix `{userId}.>`, domain `{jsDomain}`). Session state changes on any cluster appear instantly in the list.

3. **Open session**: User clicks a session. SPA attempts direct NATS connection to the worker's WebSocket URL. If successful, subscribes to events and KV directly on the worker NATS (no domain qualification needed — it's local). If direct connection fails, SPA stays on the hub connection and uses domain-qualified JetStream for events and KV.

4. **Create session**: User creates a session on a project. SPA publishes `sessions.create` — routed through the hub leaf node to the correct worker. Worker's session-agent handles it identically to today.

5. **Create project**: User selects a cluster (or uses their default). Control plane validates RBAC, creates the Postgres record with cluster assignment, publishes a provisioning request through the hub to the target worker's controller.

6. **Admin: register cluster**: Admin registers a new worker cluster via `POST /api/clusters`. Control plane generates leaf credentials, stores the cluster record in Postgres. Admin configures the worker's NATS with the returned credentials and hub URL.

7. **Admin: grant cluster access**: Admin grants a user access to a cluster via `POST /api/clusters/{id}/members`. First grant becomes the user's default cluster.

## Component Changes

### Control Plane

**New Postgres tables:**

`clusters` — cluster registry:

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | TEXT | PRIMARY KEY | UUID v4 |
| `name` | TEXT | UNIQUE NOT NULL | Human-readable name (e.g. "us-west") |
| `js_domain` | TEXT | UNIQUE NOT NULL | JetStream domain name (e.g. "worker-a") |
| `nats_url` | TEXT | NOT NULL | Internal NATS URL for leaf node connection |
| `nats_ws_url` | TEXT | NOT NULL DEFAULT '' | External WebSocket URL for direct client connections |
| `leaf_creds` | TEXT | NOT NULL | Leaf node NKey credential (private — never sent to clients) |
| `status` | TEXT | NOT NULL DEFAULT 'active' | active, draining, offline |
| `labels` | JSONB | NOT NULL DEFAULT '{}' | Arbitrary key-value labels (region, tier, etc.) |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT NOW() | |

Writers: control-plane (RegisterCluster, UpdateCluster)
Readers: control-plane (discovery, login response, RBAC checks)

`user_clusters` — cluster RBAC grants:

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `user_id` | TEXT | NOT NULL FK->users ON DELETE CASCADE | |
| `cluster_id` | TEXT | NOT NULL FK->clusters ON DELETE CASCADE | |
| `role` | TEXT | NOT NULL DEFAULT 'member' | member, admin |
| `is_default` | BOOLEAN | NOT NULL DEFAULT FALSE | First grant = default |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT NOW() | |

Primary key: `(user_id, cluster_id)`
Constraint: `CREATE UNIQUE INDEX idx_user_clusters_default ON user_clusters (user_id) WHERE is_default = TRUE` — partial unique index ensuring at most one default per user

Writers: control-plane (GrantClusterAccess, RevokeClusterAccess, SetDefaultCluster)
Readers: control-plane (RBAC validation, project creation, login response)

**Modified `projects` table** — add `cluster_id`:

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `cluster_id` | TEXT | NOT NULL FK->clusters ON DELETE RESTRICT | Cluster the project is provisioned on |

Migration strategy: The control plane startup sequence runs in order: (1) `Migrate()` executes DDL — `CREATE TABLE IF NOT EXISTS clusters`, `CREATE TABLE IF NOT EXISTS user_clusters`, and `ALTER TABLE projects ADD COLUMN IF NOT EXISTS cluster_id TEXT REFERENCES clusters(id) ON DELETE RESTRICT` (column is nullable initially). (2) Auto-registration runs (see Backwards Compatibility) — inserts a cluster row if the table is empty. (3) A backfill query runs: `UPDATE projects SET cluster_id = $1 WHERE cluster_id IS NULL` using the auto-registered cluster ID. (4) `ALTER TABLE projects ALTER COLUMN cluster_id SET NOT NULL` enforces the constraint. Steps 2-4 are procedural Go code in the control plane startup path, not part of the DDL string. `ON DELETE RESTRICT` prevents deleting a cluster with active projects.

**New HTTP endpoints** — all cluster management endpoints are on the admin mux (`:9091`, bound to `127.0.0.1`), registered as separate route handlers under `/admin/clusters` (extending the existing admin mux that currently handles `/admin/` as a catch-all for user management):

`POST /admin/clusters` — Register a new worker cluster.
- Auth: admin bearer token (same as existing admin endpoints)
- Request: `{ name, natsUrl, natsWsUrl?, labels? }`
- Response (201): `{ id, name, jsDomain, leafNkeySeed, controllerCreds, hubNatsUrl, hubLeafPort, accountPubKey, operatorJwt, accountJwt, status }` — `leafNkeySeed` is the raw NKey seed for leaf node auth (NATS leaf connections use NKey pairs directly, not JWTs). `controllerCreds` is a full NATS `.creds` file (JWT + NKey seed, formatted by `FormatNATSCredentials`).
- Behavior: generates the JetStream domain name by slugifying the cluster name (lowercase, replace non-alphanumeric with `-`, trim, truncate to 63 chars). If the slug collides with an existing `js_domain`, appends a 4-char random suffix. Creates a leaf NKey pair (`nkeys.CreateUser()` — the seed is stored in `clusters.leaf_creds` and returned as `leafNkeySeed`). Issues a controller JWT via `IssueControllerJWT` (returned as `controllerCreds` in `.creds` format). Stores the cluster record in Postgres, returns credentials for worker NATS configuration.
- Error: 409 if `name` already exists.

`GET /admin/clusters` — List all clusters.
- Auth: admin bearer token
- Response (200): `[{ id, name, jsDomain, natsUrl, natsWsUrl, status, labels, createdAt }]` (no leaf credentials)

`POST /admin/clusters/{id}/members` — Grant a user access to a cluster.
- Auth: admin bearer token
- Request: `{ userId, role? }`
- Response (201): `{ userId, clusterId, role, isDefault }`
- Behavior: creates the `user_clusters` record. If this is the user's first cluster grant, sets `is_default = TRUE`.
- Error: 404 if cluster or user not found. 409 if grant already exists.

`DELETE /admin/clusters/{id}/members/{userId}` — Revoke cluster access.
- Auth: admin bearer token
- Response (200): `{ ok: true }`
- Behavior: deletes the `user_clusters` record. If this was the user's default, promotes the grant with the oldest `created_at` as the new default (or none if this was the last grant).
- Error: 404 if grant not found. 409 if user has projects on this cluster with `status IN ('active', 'pending')` — the user must delete or migrate their projects before losing cluster access.

**Modified login response** — add cluster info:

```json
{
  "token": "...",
  "natsUrl": "wss://hub.mclaude.internal/nats",
  "jwt": "...",
  "nkeySeed": "...",
  "projects": [
    {
      "id": "abc",
      "name": "mclaude",
      "clusterId": "c1",
      "clusterName": "us-west",
      "jsDomain": "worker-a",
      "directNatsUrl": "wss://worker-a.mclaude.internal/nats"
    }
  ],
  "clusters": [
    {
      "id": "c1",
      "name": "us-west",
      "jsDomain": "worker-a",
      "directNatsUrl": "wss://worker-a.mclaude.internal/nats"
    }
  ]
}
```

The `natsUrl` is always the hub. `directNatsUrl` per cluster/project is the worker's WebSocket URL (empty if not externally accessible).

The `AuthTokens` type gains `projects` and `clusters` arrays matching the JSON above:

```typescript
export interface ClusterInfo {
  id: string
  name: string
  jsDomain: string
  directNatsUrl: string  // empty if not externally accessible
}

export interface ProjectInfo {
  id: string
  name: string
  clusterId: string
  clusterName: string
  jsDomain: string
  directNatsUrl: string
}

export interface AuthTokens {
  jwt: string
  nkeySeed: string
  userId: string
  natsUrl?: string        // hub NATS URL (always set in multi-cluster)
  projects?: ProjectInfo[]
  clusters?: ClusterInfo[]
}
```

`AuthStore` stores these after login and exposes typed accessors:

```typescript
// Added to AuthStore:
getProjects(): ProjectInfo[] { return this._tokens?.projects ?? [] }
getClusters(): ClusterInfo[] { return this._tokens?.clusters ?? [] }
getJwt(): string { return this._tokens?.jwt ?? '' }
getNkeySeed(): string { return this._tokens?.nkeySeed ?? '' }
```

`SessionStore` reads `getClusters()` on initialization to open per-cluster KV watches. `EventStore`'s caller reads `getProjects()`, `getJwt()`, and `getNkeySeed()` to populate `EventStoreOptions` with the direct connection credentials.

**Modified project creation** — cluster assignment:

Project creation uses the existing NATS request/reply subject `mclaude.{userId}.api.projects.create`. The request payload gains an optional `clusterId` field: `{ projectId, name, gitUrl, clusterId? }`. If `clusterId` is omitted, the control plane uses the user's default cluster. The control plane validates the user has access to the target cluster before creating the Postgres record (which now includes `cluster_id`). After the Postgres insert, the control plane publishes a provisioning request to the worker controller via NATS.

**Provisioning via NATS** — replaces direct K8s CRD creation for remote clusters:

Subject: `mclaude.clusters.{clusterId}.projects.provision`
Payload: `{ userId, projectId, gitUrl }`
Reply: `{ status: "ok" | "error", message? }`

The control plane publishes this as a NATS request (request/reply). The message routes through the hub's leaf node to the target worker. For the local cluster (degenerate single-cluster mode), the control plane can still create CRDs directly as today — the NATS path is for remote workers.

**Hub NATS configuration** — the control plane cluster's NATS adds a leaf node listener and JWT-based authorization:

```
leafnodes {
  port: 7422
}

# JWT auth: operator → account → user chain
operator: $OPERATOR_JWT
resolver: MEMORY
resolver_preload: {
  $ACCOUNT_PUBLIC_KEY: $ACCOUNT_JWT
}
```

NATS JWT auth requires a three-tier trust chain: operator signs account JWTs, account signs user JWTs. The control plane generates both the operator and account NKey pairs at initial setup and issues an account JWT signed by the operator. The NATS server config uses `resolver: MEMORY` with the account JWT preloaded — no external resolver needed for a single-account setup. The operator JWT, account JWT, and account public key are injected via Helm values / K8s secrets. Worker NATS instances use the same `operator` + `resolver_preload` block (same trust chain), so user JWTs issued by the control plane are valid on all NATS instances.

All other NATS config (WebSocket, JetStream, max payload) remains as today.

### Worker Controller

New component deployed by the mclaude-worker Helm chart. Runs in the worker cluster's `mclaude-system` namespace.

**Responsibilities:**
- Subscribes to `mclaude.clusters.{clusterId}.projects.provision` via NATS (through the leaf node)
- On receiving a provisioning request: creates the MCProject CRD in the local cluster
- The existing reconciler (already part of the codebase) watches MCProject CRDs and provisions namespaces, PVCs, deployments, secrets — unchanged
- Publishes provisioning status back via NATS reply

**NATS connection:** Connects to the local worker NATS using a controller credentials file (JWT + NKey seed in standard `.creds` format). The control plane generates a controller user JWT during cluster registration (separate from end-user JWTs — permissions are scoped to `mclaude.clusters.{clusterId}.>` subjects for provisioning and heartbeats). The full credentials file (`controllerCreds`) is included in the `POST /admin/clusters` registration response (alongside `leafCreds`) and stored as a K8s secret in the worker's `mclaude-system` namespace. The controller JWT is issued by the same account key as user JWTs, so the worker NATS (which trusts the same account) validates it automatically:

```go
// IssueControllerJWT issues a NATS user JWT for a worker controller,
// scoped to mclaude.clusters.{clusterId}.> subjects. No expiry.
func IssueControllerJWT(clusterId string, accountKP nkeys.KeyPair) (jwt string, seed []byte, err error) {
    userKP, userSeed, err := GenerateUserNKey()
    claims := natsjwt.NewUserClaims(userKP.PublicKey)
    claims.Name = "controller-" + clusterId
    claims.Permissions.Pub.Allow = []string{"mclaude.clusters." + clusterId + ".>"}
    claims.Permissions.Sub.Allow = []string{"mclaude.clusters." + clusterId + ".>"}
    encoded, _ := claims.Encode(accountKP)
    return encoded, userSeed, nil
}
```

The controller connects with `nats.UserCredentials(credsFilePath)`. Messages reach the hub transparently via the leaf node.

**K8s access:** Full cluster access (same ServiceAccount/RBAC as the existing reconciler). Only operates on its own cluster.

**Health:** Exposes `/healthz` and `/readyz` endpoints. Liveness probe checks NATS connection and K8s API access.

### Session Agent

No changes to the session-agent binary. It connects to its local worker NATS, publishes events and KV updates, subscribes to API commands — all unchanged. The leaf node connection between worker and hub NATS makes these messages visible to clients connected to the hub.

The one implicit change: session-agent events published on the worker NATS are now visible to hub-connected clients via leaf node routing (core NATS subjects) and domain-qualified JetStream (KV, event streams).

### SPA

**Transport layer extensions:**

`INATSClient` gains an optional `domain` parameter as the last argument on JetStream methods:

- `kvWatch(bucket, key, callback, domain?: string)` — when `domain` is set, opens the KV watcher via `jetstream({ domain }).views.kv(bucket)` instead of `jetstream().views.kv(bucket)`. Returns unsubscriber.
- `kvGet(bucket, key, domain?: string)` — same pattern, domain-qualified KV get
- `jsSubscribe(stream, subject, startSeq, callback, domain?: string)` — creates ordered consumer via `jetstream({ domain }).consumers.get(stream, ...)` instead of `jetstream().consumers.get(...)`

Internally, the `nats.ws` library's `jetstream({ domain })` method handles subject rewriting (`$JS.{domain}.API.>` instead of `$JS.API.>`). When `domain` is omitted or empty, behavior is unchanged (local JetStream). The `NATSClient` implementation caches `JetStreamClient` instances per domain to avoid re-creating them on every call.

**NATS connection management:**

The SPA maintains up to two NATS connections:
1. **Hub connection** (always open) — owned by `AuthStore`, connects to `natsUrl` from login response. Used for the dashboard KV watches (domain-qualified) and as fallback for session interaction. All stores (`SessionStore`, `LifecycleStore`, `HeartbeatMonitor`) use this connection by default.
2. **Direct worker connection** (opened on demand, per-cluster) — owned by `EventStore`. When the user opens a session, `EventStore` creates a new `NATSClient` instance and connects to the worker's `directNatsUrl` using the same JWT/NKey from `AuthStore`. If successful, `EventStore` and the session-specific `kvWatch` use this connection (no domain qualification). If the direct connection fails, `EventStore` falls back to the hub connection with domain routing. The direct connection is closed when the user navigates away from the session detail view. Only one direct connection is open at a time — switching sessions closes the previous one.

**Dashboard / session list:**

On login, `SessionStore.startWatching()` opens domain-qualified KV watches through the hub for each cluster the user has projects on. State changes on any cluster update the session list in real time.

**SessionStore constructor changes:** `SessionStore` constructor gains an optional `clusters: ClusterInfo[]` parameter. `startWatching()` behavior depends on `clusters`:

- **Multi-cluster** (`clusters.length > 1`): iterates each cluster and opens a domain-qualified KV watch per cluster:
  ```typescript
  // Inside startWatching():
  for (const cluster of this._clusters) {
    const unsub = this._natsClient.kvWatch(
      'mclaude-sessions',
      `${this._userId}.>`,
      (entry) => this.handleKVUpdate(entry, cluster),
      cluster.jsDomain   // domain parameter
    )
    this._unwatchers.push(unsub)  // cleaned up by _stopWatching()
  }
  ```
- **Single-cluster** (`clusters` absent or length ≤ 1): uses the existing non-qualified `kvWatch` — backwards compatible, no code change.

`handleKVUpdate(entry: KVEntry, cluster?: ClusterInfo)` is a new private method that parses the KV entry, tags the resulting `SessionKVState` with cluster metadata (stored in a parallel `Map<sessionId, ClusterInfo>`), and adds/updates the session in the aggregated list. The `sessions` getter returns all sessions across clusters.

**Session detail view:**

When the user opens a session, the SPA determines the connection strategy:

1. If `directNatsUrl` is available and reachable: open a direct NATS connection to the worker using the same JWT/NKey. Subscribe to events via `jsSubscribe` and KV via `kvWatch` natively (no domain parameter). Track the last received JetStream sequence number.
2. If direct connection fails or no `directNatsUrl`: use the hub connection with domain-qualified JetStream subscriptions — pass `jsDomain` to `jsSubscribe` and `kvWatch`. Use the last known sequence number from `EventStore._lastSequence` as `startSeq` to resume without gaps.

Switching from direct to hub (or vice versa) resubscribes with the appropriate domain parameter and `startSeq`. The user sees a brief reconnection indicator but no data loss.

Input messages (`sessions.input`, `sessions.control`) are published on whichever connection is active. Leaf node routing ensures they reach the worker's session-agent regardless.

**EventStore constructor changes:** `EventStoreOptions` gains optional fields:

```typescript
interface EventStoreOptions {
  natsClient: INATSClient  // existing — hub connection
  userId: string           // existing
  projectId: string        // existing
  sessionId: string        // existing
  jsDomain?: string        // new — worker's JetStream domain for hub fallback
  directNatsUrl?: string   // new — worker's WebSocket URL for direct connection
  jwt?: string             // new — from AuthStore, for direct connection auth
  nkeySeed?: string        // new — from AuthStore, for direct connection auth
  createNATSClient?: () => INATSClient  // new — factory for direct connection client
}
```

When `directNatsUrl` is present, `EventStore` calls `createNATSClient()` in `start()` to instantiate a secondary client and attempts a direct connection using the provided `jwt`/`nkeySeed`. If successful, the direct client is used for `jsSubscribe` and `kvWatch` (no domain parameter). If the direct connection fails, the hub client is used with `jsDomain` passed to `jsSubscribe` and `kvWatch`. The secondary client is closed in `stop()`, called when the user navigates away from the session detail view.

**ConversationVM constructor changes:** `ConversationVM` gains an `authStore: AuthStore` parameter (added after the existing `natsClient` parameter). When creating `EventStoreOptions`, it reads cluster credentials from `authStore`:

```typescript
const project = authStore.getProjects().find(p => p.id === projectId)
const opts: EventStoreOptions = {
  natsClient, userId, projectId, sessionId,
  jsDomain: project?.jsDomain,
  directNatsUrl: project?.directNatsUrl,
  jwt: authStore.getJwt(),
  nkeySeed: authStore.getNkeySeed(),
  createNATSClient: () => new NATSClient(),
}
```

**Project creation:**

The create-project flow now includes cluster selection. If the user has access to multiple clusters, the UI shows a cluster picker. If only one cluster, it's auto-selected.

### Daemon

The daemon runs on the user's laptop and connects to the hub NATS (or directly to a worker for single-cluster setups). For multi-cluster:

- Job dispatch (`sessions.create`) routes through the hub to the correct worker via leaf nodes
- Quota monitoring is per-user (not per-cluster) — unchanged
- Lifecycle events from all clusters are visible through the hub — the lifecycle subscriber works unchanged

The daemon's NATS connection URL comes from the control plane login, same as the SPA.

### Helm Charts

**`charts/mclaude-cp/`** — Control plane chart:

Deploys:
- Hub NATS (with leaf node listener on port 7422, WebSocket on 8080)
- Postgres
- Control plane server (with cluster registry endpoints)
- SPA
- Ingress

New values:
```yaml
nats:
  leafnodes:
    port: 7422    # leaf node listener for workers
```

**`charts/mclaude-worker/`** — Worker chart:

Deploys:
- Worker NATS (with leaf node connection to hub, JetStream domain)
- Worker controller
- Session-agent template ConfigMap
- MCProject CRD

Values:
```yaml
worker:
  clusterId: ""              # set during registration
  jsDomain: ""               # set during registration

nats:
  leafnodes:
    remotes:
      - url: ""              # hub NATS leaf URL (nats-leaf://hub:7422)
        nkey: ""              # path to leaf NKey seed file (.nk)
  jetstream:
    domain: ""               # same as worker.jsDomain

controller:
  image:
    repository: richardmsong/mclaude-worker-controller
    tag: 0.1.0
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      cpu: 500m
      memory: 256Mi
```

**Backwards compatibility — single-cluster deployment:**

A single-cluster deployment installs both charts on the same cluster. In this mode, a single NATS instance serves both hub and worker roles — no leaf node connection to itself. The NATS config includes `leafnodes { port: 7422 }` (listener for future workers) and a JetStream domain matching the auto-registered cluster's `js_domain`. The SPA detects single-cluster mode by checking `loginResponse.clusters.length === 1` — when true, `SessionStore` uses non-domain-qualified KV watches (backwards compatible with the existing single-cluster code path). When `clusters.length > 1`, `SessionStore` opens domain-qualified watches per cluster. The control plane provisions projects via direct CRD creation in single-cluster mode (detects by checking if the target `cluster_id` matches the auto-registered cluster — stored as `LOCAL_CLUSTER_ID` in the control plane's startup state). Adding a second cluster later is seamless: the new worker connects as a leaf to the existing NATS, the login response returns two clusters, and the SPA starts using domain-qualified watches for both.

**Auto-registration:** On startup, if the `clusters` table is empty, the control plane auto-registers a local cluster with:
- `name`: value of `HELM_RELEASE_NAME` env var (default "mclaude")
- `js_domain`: value of `NATS_JS_DOMAIN` env var (default "default")
- `nats_url`: value of `NATS_URL` env var (default "nats://localhost:4222")
- `nats_ws_url`: value of `NATS_WS_URL` env var (empty if unset)
- `leaf_creds`: empty (local cluster — no leaf connection needed)
- `status`: "active"

**Idempotent startup sequence** (steps 2-4 from migration strategy run on every startup, not just first run):
- Step 2: `INSERT INTO clusters (...) ON CONFLICT (name) DO NOTHING` — safe to re-run.
- Step 3: `UPDATE projects SET cluster_id = $1 WHERE cluster_id IS NULL` — no-op if all rows already have a cluster_id. Runs unconditionally.
- Step 4: `ALTER TABLE projects ALTER COLUMN cluster_id SET NOT NULL` — idempotent (Postgres silently succeeds if the constraint already exists). Only fails if step 3 left NULL rows, which would indicate a bug.
- User grants: `INSERT INTO user_clusters (...) SELECT ... FROM users ... ON CONFLICT DO NOTHING` — safe to re-run.

This ensures crash recovery between any two steps completes correctly on restart.

## Data Model

### New Postgres Tables

See `clusters` and `user_clusters` in Component Changes > Control Plane above.

### Modified Postgres Tables

`projects` gains `cluster_id TEXT NOT NULL FK->clusters ON DELETE RESTRICT`.

### NATS Configuration

**Hub NATS** (control plane cluster):
```
port: 4222
websocket { port: 8080 }
leafnodes { port: 7422 }
jetstream {
  store_dir: /data/jetstream
  domain: hub
}

# JWT auth
operator: $OPERATOR_JWT
resolver: MEMORY
resolver_preload: {
  $ACCOUNT_PUBLIC_KEY: $ACCOUNT_JWT
}
```

Note on JetStream domain migration: adding `domain: hub` to an existing NATS instance does not break existing clients. Clients connected directly to that NATS continue using the default (unqualified) JetStream API (`$JS.API.>` still works alongside `$JS.hub.API.>`). The domain only matters for cross-cluster access — when a client on the hub wants to reach a worker's JetStream, it qualifies with the worker's domain. Existing single-cluster deployments gain the `domain` config during the upgrade but experience no behavioral change.

**Worker NATS** (each worker cluster):
```
port: 4222
websocket { port: 8080 }
leafnodes {
  remotes [{
    url: nats-leaf://hub.mclaude.internal:7422
    nkey: /etc/nats/leaf.nk
  }]
}
jetstream {
  store_dir: /data/jetstream
  domain: {jsDomain}    # e.g. "worker-a"
}

# Same JWT auth trust chain as hub
operator: $OPERATOR_JWT
resolver: MEMORY
resolver_preload: {
  $ACCOUNT_PUBLIC_KEY: $ACCOUNT_JWT
}
```

### New NATS Subjects

| Subject | Publisher | Subscriber | Payload | Transport |
|---------|-----------|------------|---------|-----------|
| `mclaude.clusters.{clusterId}.projects.provision` | control-plane | worker controller | `{ userId, projectId, gitUrl }` | Core NATS request/reply via leaf |
Controller liveness is detected via NATS `$SYS` presence events (connect/disconnect), not heartbeats. Control-plane subscribes to `$SYS.ACCOUNT.*.CONNECT` and `$SYS.ACCOUNT.*.DISCONNECT`, identifies the controller from JWT claims, and updates cluster status in Postgres and KV. When a controller disconnects (crash, network loss), control-plane marks the cluster `offline` and stops routing. When it reconnects, control-plane marks it `active` again. See `adr-2026-04-17-nats-security.md` for details.

Existing subjects (`mclaude.{userId}.{projectId}.events.*`, `mclaude.{userId}.{projectId}.api.sessions.*`, etc.) are unchanged. They flow between hub and worker automatically via leaf node subject routing. NATS leaf nodes import/export all core NATS subjects by default — no explicit `allow`/`deny` rules are needed in the `remotes` block. JetStream domain routing (`$JS.{domain}.API.>`) is also handled automatically by the NATS leaf node protocol.

### KV Buckets — Unchanged

All existing KV buckets (`mclaude-sessions`, `mclaude-projects`, `mclaude-job-queue`) remain on the worker NATS where the session-agents run. They are accessible from hub-connected clients via domain-qualified JetStream (`js.KeyValue('mclaude-sessions', { domain: 'worker-a' })`).

No new KV buckets are needed. The cluster registry is in Postgres, not NATS.

### JetStream Streams — Unchanged

`MCLAUDE_EVENTS`, `MCLAUDE_API`, `MCLAUDE_LIFECYCLE` remain on each worker NATS. Accessible from the hub via domain routing.

## Error Handling

| Failure | Detection | Behavior |
|---------|-----------|----------|
| Worker NATS leaf disconnects from hub | Hub NATS detects stale leaf connection | SPA KV watches for that cluster stop updating. SPA shows "cluster offline" indicator on affected sessions. Direct connections to the worker still work if network path exists. |
| Direct worker connection fails | SPA WebSocket connection error/timeout | SPA falls back to hub connection with domain-qualified JetStream. Transparent to user — may see brief reconnection indicator. |
| Worker controller crashes | K8s restarts pod; provisioning requests queue in NATS | Provisioning requests are NATS request/reply — control plane times out and returns error to user. Retry when controller comes back. |
| Cluster registration with duplicate domain | Postgres UNIQUE constraint on `js_domain` | `POST /api/clusters` returns 409 Conflict. |
| Project creation on inaccessible cluster | RBAC check in control plane | `POST /api/projects` returns 403 if user lacks cluster access. |
| Project creation on offline cluster | Provisioning NATS request times out (10s) | Control plane returns 503 with error message. Project record created in Postgres with `status = 'pending'` (new status value alongside `active` and `archived`; enforced by CHECK constraint `status IN ('active', 'pending', 'archived')`). The control plane's cluster heartbeat handler (which processes `mclaude.clusters.*.status` messages) checks for `pending` projects on a cluster whenever it transitions from `offline` to `active`: `SELECT id, user_id, git_url FROM projects WHERE cluster_id = $1 AND status = 'pending'`. For each, it re-sends the provisioning request. On successful reply, updates `status = 'active'`. |
| Hub NATS goes down | SPA and daemon lose connection | All real-time monitoring stops. Direct worker connections (if active) continue working. SPA shows disconnected state, auto-reconnects when hub returns. |

## Security

**Account key distribution:** The control plane generates one account NKey pair at initial setup. The account seed (private key) is stored as a K8s secret in the control plane namespace. The account public key is distributed to every worker NATS via the leaf credentials exchange during cluster registration.

**Leaf node credentials:** Each worker gets a unique leaf NKey pair. The NKey seed (private key) is stored in Postgres (`clusters.leaf_creds`) and returned once during registration as `leafNkeySeed`. The worker stores it as a K8s secret and references it in the NATS leaf node config (`nkey: /etc/nats/leaf.nk`). Leaf node auth uses raw NKey challenge-response — no JWT is needed. Revoking a worker = deleting its leaf credential and restarting hub NATS (or using NATS credential revocation).

**User JWTs:** Signed by the account seed, verified by the account public key. Valid on hub and all workers. JWT permissions are extended for cross-domain JetStream access — `UserSubjectPermissions` adds `$JS.*.API.>` to both pub and sub allow-lists (needed for domain-qualified KV watch and stream consumer creation through the hub). The existing `mclaude.{userId}.>`, `_INBOX.>`, and `$KV.*` permissions are unchanged.

**Cluster RBAC:** Enforced at the control plane HTTP layer. Users can only create projects on clusters they have access to. The NATS layer doesn't enforce cluster-level access — a user JWT technically works on any NATS instance with the shared account key. Cluster RBAC is an authorization policy, not a cryptographic boundary.

**Worker isolation:** Workers cannot access each other's JetStream data. Leaf node connections to the hub are point-to-point — worker-a's leaf doesn't see worker-b's subjects unless a hub-connected client explicitly subscribes to both domains.

## Performance

**Hub NATS as bottleneck:** All cross-cluster traffic flows through the hub NATS. This includes:
- KV watch updates from all clusters (lightweight — fires on state changes only, small JSON payloads)
- Event stream subscriptions for active sessions (high volume — every Claude stdout line when a user has a session open)
- Core NATS pub/sub for session input/lifecycle (low volume)

The event streams are the heavy part. A single active Claude session produces hundreds of events per minute. But event traffic only flows through the hub when the client is connected via the hub — direct worker connections bypass it entirely.

**Scaling considerations:**
- Few users, few clusters: hub handles everything easily
- Many users, many clusters: hub KV watch traffic grows linearly with (users x clusters). Direct connections for active sessions reduce hub event traffic.
- Load testing the hub relay is needed before scaling beyond a handful of clusters to determine the performance envelope.

**Mitigation strategies (deferred):**
- Hub NATS clustering (multiple hub nodes behind a load balancer)
- Selective KV watch scoping (only watch clusters with recent activity)
- Event stream pagination (don't stream full history through hub)

## Scope

### In scope
- Cluster registry in Postgres (clusters table, user_clusters table)
- Cluster RBAC (explicit grant, default cluster)
- Project-to-cluster assignment
- Hub NATS with leaf node listener
- Worker NATS with leaf node connection to hub
- Worker controller (provisions projects via NATS)
- SPA domain-qualified KV watches for dashboard
- SPA direct-to-worker connection with hub fallback
- Login response with cluster/domain metadata
- Separate Helm charts (mclaude-cp, mclaude-worker)
- Backwards-compatible single-cluster mode (degenerate case)

### Deferred
- Load-balanced cluster auto-assignment (currently explicit or default)
- Cluster capacity tracking and reporting
- Cross-cluster project migration
- Hub NATS clustering for horizontal scaling
- Performance testing and tuning
- Cluster health monitoring dashboard in SPA
- Automated cluster decommissioning (drain sessions, migrate projects)
- Per-cluster account keys (currently shared — acceptable given CP is single trust root)
- BYOM (bring your own model) per cluster

## Implementation Plan

Estimated effort to implement via dev-harness.

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| control-plane | ~1,200-1,500 | ~450k | 2 tables, 4 endpoints, login changes, NATS hub config, JWT/NKey issuance, auto-registration, migration |
| session-agent | ~200-300 | ~130k | Minor — leaf node config consumed from env, no new handlers |
| spa | ~800-1,000 | ~400k | AuthStore cluster accessors, SessionStore domain-qualified watches, EventStore dual NATS, ConversationVM cluster routing |
| helm | ~600-800 | ~200k | Split into mclaude-cp and mclaude-worker charts, NATS leaf config, new values |
| cli | ~100-150 | ~65k | Cluster selection flag, pass-through to session-agent |

**Total estimated lines:** ~2,900-3,750
**Total estimated tokens:** ~1.25M
**Estimated wall-clock:** ~1.5h of 5h budget (25%)
