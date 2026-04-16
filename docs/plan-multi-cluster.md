# Multi-Cluster Architecture

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
Constraint: at most one `is_default = TRUE` per `user_id`

Writers: control-plane (GrantClusterAccess, RevokeClusterAccess, SetDefaultCluster)
Readers: control-plane (RBAC validation, project creation, login response)

**Modified `projects` table** — add `cluster_id`:

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `cluster_id` | TEXT | NOT NULL FK->clusters ON DELETE RESTRICT | Cluster the project is provisioned on |

Added to existing table via migration. `ON DELETE RESTRICT` prevents deleting a cluster with active projects.

**New HTTP endpoints:**

`POST /api/clusters` — Register a new worker cluster.
- Auth: admin-only (admin bearer token)
- Request: `{ name, natsUrl, natsWsUrl?, labels? }`
- Response: `{ id, name, jsDomain, leafCreds, hubNatsUrl, hubLeafPort, accountPubKey, status }`
- Behavior: generates a unique JetStream domain name from the cluster name, creates a leaf NKey pair, stores the cluster record, returns credentials for worker NATS configuration.

`GET /api/clusters` — List all clusters.
- Auth: authenticated user
- Response: `[{ id, name, jsDomain, status, labels }]` (no credentials)

`POST /api/clusters/{id}/members` — Grant a user access to a cluster.
- Auth: admin-only
- Request: `{ userId, role? }`
- Behavior: creates the `user_clusters` record. If this is the user's first cluster grant, sets `is_default = TRUE`.

`DELETE /api/clusters/{id}/members/{userId}` — Revoke cluster access.
- Auth: admin-only
- Behavior: deletes the `user_clusters` record. If this was the user's default, sets another grant as default (or none if last).

**Modified login response** — add cluster info:

```json
{
  "token": "...",
  "natsUrl": "wss://hub.mclaude.internal/nats",
  "natsJwt": "...",
  "natsNkeySeed": "...",
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

**Modified project creation** — cluster assignment:

`POST /api/projects` now accepts optional `clusterId`. If omitted, uses the user's default cluster. Validates the user has access to the target cluster. After creating the Postgres record, publishes a provisioning request to the worker controller via NATS.

**Provisioning via NATS** — replaces direct K8s CRD creation for remote clusters:

Subject: `mclaude.clusters.{clusterId}.projects.provision`
Payload: `{ userId, projectId, gitUrl }`
Reply: `{ status: "ok" | "error", message? }`

The control plane publishes this as a NATS request (request/reply). The message routes through the hub's leaf node to the target worker. For the local cluster (degenerate single-cluster mode), the control plane can still create CRDs directly as today — the NATS path is for remote workers.

**Hub NATS configuration** — the control plane cluster's NATS adds a leaf node listener:

```
leafnodes {
  port: 7422
}
```

All other NATS config (WebSocket, JetStream, max payload) remains as today.

### Worker Controller

New component deployed by the mclaude-worker Helm chart. Runs in the worker cluster's `mclaude-system` namespace.

**Responsibilities:**
- Subscribes to `mclaude.clusters.{clusterId}.projects.provision` via NATS (through the leaf node)
- On receiving a provisioning request: creates the MCProject CRD in the local cluster
- The existing reconciler (already part of the codebase) watches MCProject CRDs and provisions namespaces, PVCs, deployments, secrets — unchanged
- Publishes provisioning status back via NATS reply

**NATS connection:** Connects to the local worker NATS. Messages reach the hub transparently via the leaf node.

**K8s access:** Full cluster access (same ServiceAccount/RBAC as the existing reconciler). Only operates on its own cluster.

**Health:** Exposes `/healthz` and `/readyz` endpoints. Liveness probe checks NATS connection and K8s API access.

### Session Agent

No changes to the session-agent binary. It connects to its local worker NATS, publishes events and KV updates, subscribes to API commands — all unchanged. The leaf node connection between worker and hub NATS makes these messages visible to clients connected to the hub.

The one implicit change: session-agent events published on the worker NATS are now visible to hub-connected clients via leaf node routing (core NATS subjects) and domain-qualified JetStream (KV, event streams).

### SPA

**NATS connection management:**

The SPA maintains up to two NATS connections:
1. **Hub connection** (always open) — connects to `natsUrl` from login response. Used for the dashboard KV watches and as fallback for session interaction.
2. **Direct worker connection** (opened on demand) — when the user opens a session, SPA tries connecting to the worker's `directNatsUrl`. If successful, uses this for events and KV. If not, uses the hub connection with domain routing.

**Dashboard / session list:**

On login, the SPA opens domain-qualified KV watches through the hub for each cluster the user has projects on:

```typescript
for (const cluster of loginResponse.clusters) {
  const js = nats.jetstream({ domain: cluster.jsDomain })
  const kv = js.keyValue('mclaude-sessions')
  kv.watch(`${userId}.>`)  // all sessions on this cluster
}
```

State changes on any cluster update the session list in real time. The SessionStore aggregates entries from all cluster watchers into a single list, tagged with cluster metadata.

**Session detail view:**

When the user opens a session, the SPA determines the connection strategy:

1. If `directNatsUrl` is available and reachable: open a direct NATS connection to the worker. Subscribe to events and KV natively (no domain qualification needed).
2. If direct connection fails or no `directNatsUrl`: use the hub connection with domain-qualified JetStream subscriptions.

Input messages (`sessions.input`, `sessions.control`) are published on whichever connection is active. Leaf node routing ensures they reach the worker's session-agent regardless.

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
        credentials: ""      # path to leaf credentials file
  jetstream:
    domain: ""               # same as worker.jsDomain

controller:
  image:
    repository: mclaude-project/mclaude-worker-controller
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

A single-cluster deployment installs both charts on the same cluster. The hub NATS and worker NATS can be the same NATS instance (worker configured as leaf of localhost, or a combined chart that deploys one NATS with both hub and worker JetStream domains). The control plane auto-registers the local cluster on startup if no clusters exist in Postgres.

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
```

**Worker NATS** (each worker cluster):
```
port: 4222
websocket { port: 8080 }
leafnodes {
  remotes [{
    url: nats-leaf://hub.mclaude.internal:7422
    credentials: /etc/nats/leaf.creds
  }]
}
jetstream {
  store_dir: /data/jetstream
  domain: {jsDomain}    # e.g. "worker-a"
}
```

### New NATS Subjects

| Subject | Publisher | Subscriber | Payload | Transport |
|---------|-----------|------------|---------|-----------|
| `mclaude.clusters.{clusterId}.projects.provision` | control-plane | worker controller | `{ userId, projectId, gitUrl }` | Core NATS request/reply via leaf |
| `mclaude.clusters.{clusterId}.status` | worker controller | control-plane | `{ clusterId, status, sessionCount, capacity }` | Core NATS (periodic heartbeat) |

Existing subjects (`mclaude.{userId}.{projectId}.events.*`, `mclaude.{userId}.{projectId}.api.sessions.*`, etc.) are unchanged. They flow between hub and worker automatically via leaf node subject routing.

### KV Buckets — Unchanged

All existing KV buckets (`mclaude-sessions`, `mclaude-projects`, `mclaude-heartbeats`, `mclaude-job-queue`) remain on the worker NATS where the session-agents run. They are accessible from hub-connected clients via domain-qualified JetStream (`js.KeyValue('mclaude-sessions', { domain: 'worker-a' })`).

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
| Project creation on offline cluster | Provisioning NATS request times out | Control plane returns 503 with error message. Project record created in Postgres but status set to `pending`. Provisioning retried when cluster reconnects. |
| Hub NATS goes down | SPA and daemon lose connection | All real-time monitoring stops. Direct worker connections (if active) continue working. SPA shows disconnected state, auto-reconnects when hub returns. |

## Security

**Account key distribution:** The control plane generates one account NKey pair at initial setup. The account seed (private key) is stored as a K8s secret in the control plane namespace. The account public key is distributed to every worker NATS via the leaf credentials exchange during cluster registration.

**Leaf node credentials:** Each worker gets a unique leaf NKey pair. The private key is stored in Postgres (`clusters.leaf_creds`) and returned once during registration. The worker stores it as a K8s secret and references it in NATS config. Revoking a worker = deleting its leaf credential and restarting hub NATS (or using NATS credential revocation).

**User JWTs:** Unchanged — signed by the account seed, verified by the account public key. Valid on hub and all workers. JWT contains user ID and publish/subscribe permissions scoped to `mclaude.{userId}.>`.

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
