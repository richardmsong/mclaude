# Controller Separation & Multi-Cluster Architecture

## Overview

Extract the MCProject reconciler from the control-plane into its own binary (`mclaude-controller`). Redesign the control-plane to have zero K8s dependency — it becomes a pure HTTP+NATS+Postgres service. The controller is the sole K8s operator. Extend the architecture to support multi-cluster deployment and BYOH (Bring Your Own Hardware) targets.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Shared CRD types | New `mclaude-api/` Go module | Clean dependency, both binaries import. Go workspace (`go.work`) for local dev. |
| K8sProvisioner | Remove entirely | Reconciler replaces it. ~685 lines deleted. Single provisioning path. |
| Binary layout | `mclaude-controller/` top-level directory | Parallel to other components. Own go.mod, Dockerfile, CI workflow. |
| Scaffold | Kubebuilder full scaffold, then trim | Standard operator layout. Gives leader election, health probes, metrics boilerplate. |
| Leader election | Enabled from the start | Standard for K8s operators. Allows future HA (2+ replicas). |
| Control-plane K8s dependency | None | Control-plane is off-cluster capable. Publishes NATS events. Controller creates MCProject CRs. |
| CR mutations from HTTP handlers | Via NATS events | Control-plane publishes update/delete events. Controller applies them to CRs. No K8s client in control-plane. |
| RBAC | Separate ServiceAccounts + ClusterRoles | Least-privilege. Control-plane has no K8s permissions. Controller gets broad K8s permissions. |
| Suspend mechanism | Two annotations: `mclaude.io/suspend-spec`, `mclaude.io/suspend-resources` | Annotation (not spec field) — operator hint for incident response. |
| Cluster model | Every target runs a controller variant | K8s clusters run kubebuilder controller. Laptops/VMs run lightweight local controller. Uniform model. |
| Registration | CLI command (`mclaude register`) | HTTP auth first (get NATS creds), then NATS for ongoing communication. |
| Cluster access | Admin-assigned, defaults to registering user | BYOH private by default. Admin can share clusters to users/groups. |
| Provisioner interface | Shared interface, separate implementations | K8s controller: provisions pods. Local controller: manages processes. Shared NATS subscriber logic. |
| Command bus | NATS only, HTTP only for browser auth flows | Everything is an event. HTTP for OAuth callbacks, login, health probes only. |
| Admin break-glass | Removed | If NATS is down, the whole system is down. Use kubectl/psql directly. |
| KV writes | Control-plane is sole KV writer | See plan-nats-security.md for threat model. Controller publishes status events → control-plane updates KV. |
| NATS backup | Out of scope — separate plan for S3 archiver | NATS streams hold user session data. Needs durable backup strategy. |

---

## Current State

Single `mclaude-control-plane` binary runs:
1. HTTP server (auth, projects API, admin, OAuth providers)
2. NATS subscribers (`mclaude.*.api.projects.create`)
3. Controller-runtime Manager with MCProjectReconciler
4. K8sProvisioner (fallback when no Manager)
5. Background goroutines (GitLab token refresh, CLI config reconcile, dev seed)

The reconciler code (`reconciler.go`) has zero imports from HTTP/auth/NATS code. The separation boundary already exists in the code.

### Files Affected

**Deleted from control-plane:**
- `provision.go` (~685 lines) — K8sProvisioner, replaced by controller
- `provision_test.go` — associated tests
- `reconciler.go` — moves to mclaude-controller
- `reconciler_test.go` — moves to mclaude-controller

**Modified in control-plane:**
- `main.go` — remove Manager setup, K8sProvisioner init, controller-runtime imports
- `projects.go` — replace `CreateMCProject()` with NATS publish
- `providers.go` — replace `PatchMCProjectGitIdentity()` / `ClearMCProjectGitIdentityForConnection()` with NATS publish
- `mcproject_types.go` — moves to `mclaude-api/`
- `server.go` / `auth.go` — remove k8sClient, k8sProvisioner fields from Server struct

**New:**
- `mclaude-api/` — shared MCProject CRD types
- `mclaude-controller/` — kubebuilder-scaffolded operator
- Helm chart: new deployment, service account, cluster role for controller
- CI: new Docker workflow, updated deploy workflows

---

## Architecture

### Component Roles After Split

```
                    nginx ingress (mclaude-system)
                    /auth  /nats → control-plane + NATS
                    /*           → SPA static files
                           │
           ┌───────────────┼───────────────┐
           ▼               ▼               ▼
   control-plane       NATS            SPA
   (Postgres)       (JetStream + KV)
   (no K8s dep)            │
                    ┌──────┼──────┐
                    ▼      ▼      ▼
              controller  controller  local-controller
              (K8s #1)    (K8s #2)    (laptop)
                    │      │           │
                    ▼      ▼           ▼
              session-   session-    session-
              agents     agents      agent
```

**control-plane**: Auth (login, OAuth, JWT issuance), NATS subscriber (project CRUD → Postgres + KV), user/project metadata. Zero K8s dependency. Can run off-cluster.

**mclaude-controller (K8s)**: Kubebuilder operator. Watches MCProject CRs. Provisions namespaces, RBAC, secrets, PVCs, deployments. Subscribes to NATS for project lifecycle commands. Publishes status events.

**mclaude-controller (local)**: Lightweight daemon. Same NATS interface as K8s controller. Manages session-agent processes instead of pods. Runs on laptops, VMs, bare metal.

### Shared Types: `mclaude-api/`

```
mclaude-api/
├── go.mod              # module mclaude-api
├── types.go            # MCProject, MCProjectSpec, MCProjectStatus
├── register.go         # SchemeGroupVersion, AddToScheme, GVR
└── kv.go               # ProjectKVState (shared between control-plane and controller)

go.work:
  use (
    mclaude-api
    mclaude-control-plane
    mclaude-controller
  )
```

### Provisioner Interface

```go
type Provisioner interface {
    EnsureProject(ctx context.Context, spec ProjectSpec) error
    DeleteProject(ctx context.Context, projectID string) error
    ProjectStatus(ctx context.Context, projectID string) (Status, error)
}

// K8s implementation (mclaude-controller):
type K8sProvisioner struct { client client.Client }
// Creates MCProject CRs, reconciler provisions pods

// Local implementation (mclaude-controller local variant):
type LocalProvisioner struct { /* process table */ }
// Manages session-agent processes directly
```

---

## NATS Subject Taxonomy

### Project Lifecycle

```
mclaude.{userId}.clusters.{clusterId}.projects.create
mclaude.{userId}.clusters.{clusterId}.projects.update
mclaude.{userId}.clusters.{clusterId}.projects.delete
```

- **Publisher**: SPA (user selects target cluster)
- **Subscribers**:
  - Control-plane: `mclaude.*.clusters.*.projects.>` — writes Postgres, writes KV (Status: Pending)
  - Controller: `mclaude.*.clusters.{itsClusterId}.projects.>` — provisions resources

### Status Events

```
mclaude.system.clusters.{clusterId}.projects.{projectId}.status
```

- **Publisher**: Controller (after reconciliation)
- **Subscriber**: Control-plane — updates KV with current phase

### Target Registration

```
mclaude.{userId}.targets.register
mclaude.{userId}.targets.deregister
```

- **Publisher**: CLI (`mclaude register`)
- **Subscriber**: Control-plane — writes to Postgres targets table

### Session Events (unchanged)

```
mclaude.{userId}.sessions.{sessionId}.>
```

### Subject Permissions Per Identity

| Identity | Subscribe | Publish |
|----------|-----------|---------|
| SPA (user) | `mclaude.{userId}.>` | `mclaude.{userId}.>` |
| Control-plane | `mclaude.>` | `mclaude.>`, `$KV.>` (sole KV writer) |
| K8s controller | `mclaude.*.clusters.{clusterId}.>` | `mclaude.system.clusters.{clusterId}.status.>` |
| BYOH controller | `mclaude.*.clusters.{clusterId}.>` | `mclaude.system.clusters.{clusterId}.status.>` |

See `docs/plan-nats-security.md` for full threat model.

---

## Event Flow: Project Creation

```
1. User clicks "New Project" in SPA, selects cluster
2. SPA publishes: mclaude.{userId}.clusters.{clusterId}.projects.create
   payload: {name, gitURL, gitIdentityID}

3. Control-plane receives (subscribes to mclaude.*.clusters.*.projects.>):
   a. Validates request
   b. Creates project row in Postgres
   c. Writes KV: {id, name, gitURL, status: "Pending"}
   d. Replies to SPA with project ID

4. Controller receives (subscribes to mclaude.*.clusters.{itsClusterId}.projects.>):
   a. Checks access: is userId authorized for this cluster? (in-memory map from KV watch)
   b. If unauthorized → drop, log warning
   c. Creates MCProject CR in mclaude-system namespace
   d. Reconciler watches CR → provisions namespace, RBAC, secrets, PVCs, deployment

5. Controller publishes: mclaude.system.clusters.{clusterId}.projects.{projectId}.status
   payload: {projectId, userId, phase: "Provisioning"}

6. Control-plane receives status event:
   a. Updates KV: {status: "Provisioning"}

7. Controller reconciliation completes:
   publishes: {phase: "Ready"}

8. Control-plane updates KV: {status: "Ready"}

9. SPA KV watch fires → UI shows project as Ready
```

---

## Suspend Annotations

Two independent annotations for incident response:

### `mclaude.io/suspend-spec: "true"`

Stops the controller from overwriting the MCProject CR spec based on incoming NATS events. Use when you need to manually edit the CR spec without it being reverted.

- **Set**: `kubectl annotate mcproject/abc mclaude.io/suspend-spec=true`
- **Effect**: NATS update/create events for this project are ignored by the controller
- **Use case**: Manual spec override during incident. Change image, resources, env vars directly on the CR.
- **Remove**: `kubectl annotate mcproject/abc mclaude.io/suspend-spec-` → controller resumes syncing spec from NATS/KV

### `mclaude.io/suspend-resources: "true"`

Stops the controller from reconciling owned resources (deployments, secrets, RBAC) to match the MCProject spec. Use when you need to manually edit pods/deployments without the controller reverting your changes.

- **Set**: `kubectl annotate mcproject/abc mclaude.io/suspend-resources=true`
- **Effect**: Controller skips resource reconciliation for this MCProject
- **Use case**: Manual pod debugging, scaling, env var injection during incident
- **Remove**: `kubectl annotate mcproject/abc mclaude.io/suspend-resources-` → controller resumes reconciliation, converges resources to spec

### Normal Behavior (no annotations)

- NATS events update the MCProject CR spec (controller keeps CR in sync with last received event)
- Controller reconciles owned resources to match CR spec (drift correction)
- Manual edits to CR spec → reverted on next NATS event
- Manual edits to owned resources → reverted on next reconcile

### Intended State

The controller compares MCProject spec to the intended state from NATS KV (the `mclaude-projects` bucket). On every reconcile:
1. Read intended spec from KV
2. If CR spec differs from KV and `suspend-spec` is not set → revert CR spec to KV value
3. If `suspend-spec` is set → trust CR spec as-is
4. Reconcile owned resources from CR spec (unless `suspend-resources` is set)

---

## Multi-Cluster Architecture

### Cluster Types

**Managed K8s cluster**: Runs `mclaude-controller` (kubebuilder operator). Provisions session-agent pods. Registered by admin.

**BYOH target (laptop, VM, bare metal)**: Runs `mclaude-controller` (local variant). Manages session-agent processes. Registered by user via `mclaude register`.

Both are modeled as "targets" in the control-plane. Both run a controller variant. Both subscribe to the same NATS subject pattern.

### Targets Table (Postgres)

```sql
CREATE TABLE targets (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT NOT NULL,
    type          TEXT NOT NULL,  -- 'k8s' or 'local'
    registered_by UUID NOT NULL REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE target_access (
    target_id UUID NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    granted_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (target_id, user_id)
);
```

- BYOH targets: `registered_by` = the user, initial `target_access` row for the same user
- Managed clusters: `registered_by` = admin, `target_access` rows per admin assignment
- Admin can grant/revoke access via API

### Registration Flow

```
mclaude register --name "my-laptop" --server https://dev.mclaude.local
  1. POST /auth/login → gets JWT + NATS seed
  2. Connects to NATS
  3. Publishes mclaude.{userId}.targets.register {name, type: "local"}
  4. Control-plane creates target row + access row in Postgres
  5. Control-plane writes target to access-list KV (for controller authorization)
  6. Control-plane replies with target ID
  7. Local controller starts, subscribes to mclaude.*.clusters.{targetId}.>
```

### Cluster Picker (SPA)

- Single cluster available → no picker shown, auto-selects
- Multiple clusters → picker shown in new project flow
- Shows: cluster name, type (cloud/local), status (online/offline)
- BYOH targets show only for the owning user (unless admin-shared)

**OPEN QUESTION**: How does the SPA know which targets are available for the current user? Options:
1. Control-plane publishes target list to NATS KV on login
2. SPA requests target list via NATS request/reply
3. Target list included in login response

**OPEN QUESTION**: How does the SPA know a target is online/offline? Options:
1. Controller heartbeat to NATS → control-plane tracks in KV
2. SPA pings controller via NATS request/reply
3. Stale detection (last heartbeat > threshold)

---

## Controller Binary: `mclaude-controller/`

### Kubebuilder Scaffold

```
mclaude-controller/
├── api/
│   └── v1alpha1/         # imports from mclaude-api/
├── cmd/
│   └── main.go           # manager setup, leader election, NATS connection
├── internal/
│   └── controller/
│       ├── mcproject_controller.go
│       └── mcproject_controller_test.go
├── Dockerfile
├── Makefile
├── go.mod
├── go.sum
└── PROJECT               # kubebuilder project file
```

Trimmed from full scaffold:
- Remove `config/crd/` (CRD is in Helm chart)
- Remove `config/manager/` (deployment is in Helm chart)
- Remove `config/rbac/` (RBAC is in Helm chart)
- Keep `Makefile` for test/lint/build targets

### main.go Responsibilities

1. Load config from env: NATS_URL, NAMESPACE, CLUSTER_ID, NATS_ACCOUNT_SEED
2. Connect to NATS
3. Create controller-runtime Manager with leader election
4. Register MCProjectReconciler
5. Start NATS subscribers (project lifecycle commands)
6. Start Manager (blocking)

### Dependencies

```go
type MCProjectReconciler struct {
    client              client.Client
    scheme              *runtime.Scheme
    controlPlaneNs      string
    clusterID           string
    sessionAgentNATSURL string
    accountKP           nkeys.KeyPair
    nc                  *nats.Conn          // for status events + KV reads
    accessMap           sync.Map            // in-memory access list from KV watch
    logger              zerolog.Logger
}
```

### Account NKey

Both control-plane and controller need `accountKP` to sign NATS JWTs:
- Control-plane: signs user JWTs (browser clients)
- Controller: signs session-agent JWTs (pod NATS credentials)

Both read from `NATS_ACCOUNT_SEED` env var. Helm chart mounts the same Secret into both deployments.

---

## Helm Chart Changes

### New Templates

- `controller-deployment.yaml` — mclaude-controller Deployment
- `controller-service.yaml` — metrics/health Service
- `controller-serviceaccount.yaml` — dedicated SA
- `controller-clusterrole.yaml` — broad K8s permissions
- `controller-clusterrolebinding.yaml`

### Modified Templates

- `control-plane-deployment.yaml` — remove controller-runtime env vars, add NATS-only config
- `clusterrole.yaml` — scope down to nothing (control-plane has no K8s permissions)
- `serviceaccount.yaml` — control-plane SA (if still needed for in-cluster NATS auth)

### Potentially Removed

- `clusterrole.yaml` — if control-plane is fully off-cluster
- `clusterrolebinding.yaml` — same

### New Values

```yaml
controller:
  enabled: true
  image:
    registry: ghcr.io
    repository: richardmsong/mclaude-controller
    tag: "main"
    pullPolicy: IfNotPresent
  replicas: 1
  leaderElection:
    enabled: true
  clusterID: ""  # auto-generated UUID if empty
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      memory: 256Mi
```

### ClusterRole Split

**Controller ClusterRole:**
```yaml
rules:
  - apiGroups: [mclaude.io]
    resources: [mcprojects, mcprojects/status]
    verbs: [get, list, watch, create, update, patch]
  - apiGroups: [""]
    resources: [namespaces, secrets, configmaps, serviceaccounts, persistentvolumeclaims]
    verbs: [get, list, watch, create, update, delete]
  - apiGroups: [apps]
    resources: [deployments]
    verbs: [get, list, watch, create, update, delete]
  - apiGroups: [rbac.authorization.k8s.io]
    resources: [roles, rolebindings]
    verbs: [get, list, watch, create, update, delete]
  - apiGroups: [coordination.k8s.io]
    resources: [leases]
    verbs: [get, list, watch, create, update, patch, delete]  # leader election
```

**Control-plane ClusterRole:**
```yaml
# Empty — control-plane has no K8s permissions.
# If running in-cluster, only needs NATS connectivity (network policy, not RBAC).
# If running off-cluster, no K8s SA needed at all.
```

---

## CI Changes

### New Workflow: `docker-controller.yml`

```yaml
name: Docker - controller
on:
  push:
    branches: [main]
    paths: [mclaude-controller/**, mclaude-api/**]
    tags: [v*]
  workflow_call:
env:
  IMAGE: ghcr.io/richardmsong/mclaude-controller
jobs:
  build:
    # Same pattern as docker-control-plane.yml
    # Build Go binary, Docker image, push to GHCR
```

### Updated Workflows

- `ci.yml` — add `controller:` path filter + `test-controller` job
- `deploy-main.yml` — add `build-controller` job
- `deploy-preview.yml` — add `build-controller` job

---

## Rescue Operations

| Failure | Rescue Tool | Procedure |
|---------|-------------|-----------|
| Postgres corrupt/down | `psql` | Restore from backup |
| NATS down | `kubectl` | Restart NATS pods. JetStream has persistence. |
| NATS KV diverged from Postgres | Repair script | Read Postgres + K8s, rewrite KV entries |
| MCProject CR in bad state | `kubectl` | Set `suspend-spec`, edit CR, remove annotation |
| Controller stuck/crashing | `kubectl` | Check logs, rollout restart |
| Session-agent pod stuck | `kubectl` | Delete pod, controller recreates |
| Orphaned namespace (no CR) | `kubectl` | Delete namespace |
| Orphaned CR (no namespace) | Controller | Reconciles and recreates namespace |
| User locked out | `psql` | Reset password, fix user row |
| NATS credentials invalid | Control-plane restart | Reissues on reconnect |
| NATS data corruption | Rebuild | Wipe PVC, restart NATS, run KV repair script from Postgres + K8s |

---

## Error Handling

### Controller receives unauthorized project create

Controller checks in-memory access map. If userId is not authorized for this clusterId, message is dropped and warning logged. No error returned to SPA (SPA doesn't know about the controller).

### Controller can't reach NATS

Controller retries with exponential backoff. MCProject CRs continue to be reconciled (K8s watches are independent of NATS). Status events queue locally until NATS reconnects.

### Control-plane receives status event for unknown project

Log warning, ignore. Project may have been deleted between status emit and receipt.

### KV watch falls behind

Controller's in-memory access map may be stale. Worst case: controller provisions a project for a user who just lost access. Access map catches up on next KV update. Acceptable latency.

---

## Scope

### v1 — Implement Now

**OPEN QUESTION**: Should v1 include multi-cluster or just the controller separation?

Option A (controller separation only):
- `mclaude-api/` shared types module
- `mclaude-controller/` kubebuilder operator (K8s only)
- Remove K8sProvisioner from control-plane
- NATS command subjects for project lifecycle
- Suspend annotations
- Helm chart split (new deployment, RBAC split)
- CI workflows
- Single cluster assumed (no targets table, no cluster picker, no local controller)
- Subject structure accommodates future multi-cluster

Option B (full multi-cluster):
- Everything in Option A, plus:
- Targets table + registration flow
- Local controller binary
- Provisioner interface
- Cluster picker in SPA
- Admin cluster access API
- BYOH isolation
- Estimated 3-4x more work than Option A

### Deferred (separate plans)

- S3 archiver for NATS stream backup (`plan-nats-backup.md`)
- NATS security hardening — see `plan-nats-security.md`
- Horizontal scaling (multiple controller replicas with leader election)
- Cross-region failover

---

## Implementation Plan

### Option A: Controller Separation Only

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| `mclaude-api/` | ~150 | ~60k | New module: types, scheme, KV types |
| `mclaude-controller/` | ~800 | ~200k | Kubebuilder scaffold, move reconciler, add NATS subscriber, tests |
| `mclaude-control-plane/` | ~-600 (net deletion) | ~150k | Remove provisioner/reconciler, replace K8s calls with NATS publish |
| `charts/mclaude/` | ~200 | ~80k | New deployment, RBAC split, values |
| CI workflows | ~100 | ~40k | New docker-controller.yml, update deploy workflows |
| `go.work` | ~10 | — | Workspace setup |

**Total estimated tokens:** ~530k
**Estimated wall-clock:** ~2-3h

### Option B: Full Multi-Cluster (additive)

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| Everything in Option A | — | ~530k | — |
| `mclaude-controller/` local variant | ~500 | ~150k | Process management instead of K8s |
| `mclaude-control-plane/` targets | ~300 | ~100k | Targets table, registration handler, access API |
| `mclaude-web/` cluster picker | ~400 | ~120k | UI component, target list, selection flow |
| `mclaude-cli/` register command | ~200 | ~80k | Auth + NATS registration |

**Total estimated tokens:** ~980k
**Estimated wall-clock:** ~4-6h
