# Spec: Controller (K8s + Local)

The controller is the binary that owns per-host runtime resources. Per ADR-0035 it ships in two flavors that share the same NATS protocol and provisioning interface but differ in their substrate:

- `mclaude-controller-k8s` — kubebuilder operator that runs inside a worker Kubernetes cluster.
- `mclaude-controller-local` — process supervisor that runs on a BYOH machine (laptop, desktop, VM).

Both subscribe to host-scoped NATS subjects and reconcile project-level resources in response. Both report liveness implicitly via their NATS connection (the hub's `$SYS.ACCOUNT.*.CONNECT/DISCONNECT` events; control-plane updates `hosts.last_seen_at`).

## Role

The control-plane has no Kubernetes client and does not directly manage processes on BYOH machines. It publishes provisioning intent on `mclaude.users.{uslug}.hosts.{hslug}.api.projects.{provision,create,update,delete}`; the controller for that host receives the request, materializes the per-project resources, and replies success/failure.

## Variant 1: `mclaude-controller-k8s`

### Deployment

A single Deployment in the worker cluster's `mclaude-system` namespace. Built with kubebuilder (controller-runtime). Leader election enabled — a future HA scale-out is supported by configuration; v1 runs a single replica.

The cluster's slug is configured at deploy time via the Helm value `clusterSlug` (e.g. `us-east`) and is required. It is identical to the slug all users granted to this cluster carry in their `hosts.slug` column.

### Configuration

| Variable | Required | Description |
|----------|----------|-------------|
| `CLUSTER_SLUG` | Yes | The cluster's canonical slug. Used to build the wildcard NATS subscription `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.>`. |
| `NATS_URL` | Yes | Worker NATS service URL (the same NATS that leaf-links into the hub). |
| `NATS_CREDS_FILE` | Yes | Path to the per-cluster controller JWT + NKey seed (provisioned by `helm install mclaude-worker` from the cluster register response). |
| `JS_DOMAIN` | Yes | JetStream domain for this worker (matches `hosts.js_domain` for cluster-type rows that point here). |
| `HELM_RELEASE_NAME` | No | Used to locate the session-agent-template ConfigMap (default `mclaude-worker`). |
| `SESSION_AGENT_TEMPLATE_CM` | No | Explicit name of the session-agent-template ConfigMap. Overrides the `HELM_RELEASE_NAME`-derived name. Set by Helm (`{{ .Release.Name }}-session-agent-template`). |
| `SESSION_AGENT_NATS_URL` | No | NATS URL injected into session-agent pods as `NATS_URL`. Defaults to the FQDN-qualified worker NATS URL. For single-cluster deployments where KV buckets live on hub NATS, set to the hub NATS URL (e.g. `nats://mclaude-cp-nats.mclaude-system.svc.cluster.local:4222`). |
| `DEV_OAUTH_TOKEN` | No | Claude API OAuth token for dev environments. When set, the reconciler injects it as `oauth-token` in per-user `user-secrets` Secret. Session-agent entrypoint reads this and exports `CLAUDE_CODE_OAUTH_TOKEN`. |
| `LEADER_ELECTION_NAMESPACE` | No | Defaults to `mclaude-system`. |

### Interfaces

#### NATS subscriptions

Subscribes via the per-cluster JWT (issued at `POST /admin/clusters` time, scoped to `mclaude.users.*.hosts.{cluster-slug}.>`):

| Subject | Behavior |
|---------|----------|
| `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.provision` | Request/reply. Resolves `userSlug`, `hostSlug`, `projectSlug` from the subject + payload; creates the `MCProject` CR; returns success when reconcile reaches `Ready` (or 503-style failure). |
| `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.create` | Request/reply. Identical to provision today; reserved for future fan-out. |
| `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.update` | Request/reply. Reconciles per-user `user-secrets` Secret (NATS creds, OAuth tokens, CLI configs) and re-applies the pod template. |
| `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.delete` | Request/reply. Tears down the `MCProject` CR (and cascaded namespace/RBAC/PVCs/Deployment). |

The wildcard at the user level is what enables one cluster controller to receive provisioning requests from every user granted access to its cluster.

#### Kubernetes resources

For full schemas see `docs/spec-state-schema.md` — Kubernetes Resources. Summary:

- CRD `MCProject` (`mcprojects.mclaude.io/v1alpha1`).
- Per-user namespace `mclaude-{userId}` with ServiceAccount, Role, RoleBinding.
- Per-user `user-secrets` Secret (NATS credentials, OAuth tokens, CLI configs) and `user-config` ConfigMap.
- Per-project `project-{projectId}` and `nix-{projectId}` PVCs.
- Per-project `mclaude-session-agent-{projectId}` Deployment.
- Watched: `{release}-session-agent-template` ConfigMap in `mclaude-system` (image, resources, PVC sizes, corporate CA settings) — changes re-enqueue all `MCProject` CRs.

#### Reconciler loop

On each reconcile cycle:

1. Loads the session-agent-template ConfigMap for image, resource, and PVC configuration.
2. Ensures the user namespace exists with correct labels (including `mclaude.io/user-namespace: "true"` when corporate CA is enabled for trust-manager targeting).
3. Ensures RBAC resources (ServiceAccount, Role, RoleBinding).
4. Ensures the `user-config` ConfigMap and `user-secrets` Secret. NATS credentials in the Secret are the per-host JWT issued by control-plane (the controller does not mint JWTs).
5. Copies imagePullSecrets from the controller's namespace.
6. Ensures the project PVC and Nix PVC.
7. Ensures the session-agent Deployment with `Recreate` strategy. Pod env vars include `USER_ID`, `USER_SLUG`, `HOST_SLUG`, `PROJECT_ID`, `PROJECT_SLUG`, plus the standard NATS credentials mount.
8. Updates `MCProject` status: phase (`Pending → Provisioning → Ready` or `Failed`) and conditions (`NamespaceReady`, `RBACReady`, `SecretsReady`, `DeploymentReady`).

`HOST_SLUG` always equals `CLUSTER_SLUG` for cluster-managed pods — sourced from the subject's `{hslug}` token, which is invariant across users granted to the cluster.

On every update reconcile, the full pod template (env vars, image, volumes, imagePullSecrets, annotations) is rebuilt and applied. Kubernetes only triggers a rollout if the template actually changed.

#### Corporate CA Support

When the session-agent-template ConfigMap has `corporateCAEnabled: "true"`, the controller:
- Adds label `mclaude.io/user-namespace: "true"` to user namespaces so trust-manager syncs the CA bundle ConfigMap.
- Injects a `corporate-ca` volume, volume mount at `/etc/ssl/certs/corporate-ca-certificates.crt`, and `NODE_EXTRA_CA_CERTS` env var into the pod template.
- Annotates the pod template with `mclaude.io/ca-bundle-hash` (SHA-256 of the ConfigMap data) so CA rotations trigger a `Recreate` rollout.

### Liveness

The worker NATS leaf-links into hub NATS. When the leaf comes up, hub publishes `$SYS.ACCOUNT.{accountKey}.CONNECT`; control-plane's hub subscriber maps the account key to the cluster's host slug and updates `hosts.last_seen_at` for the registering admin's host row. Leaf disconnect flips `online=false`. There is no separate cluster status subject.

## Variant 2: `mclaude-controller-local`

### Deployment

Runs as a long-lived foreground process on the BYOH machine. Started by the user (`mclaude daemon --host <hslug>` or via a launchd / systemd unit they configure). One process per machine.

The host slug is sourced from `--host`, falling back to the target of `~/.mclaude/active-host` if unset. Hard fail at startup on absence — there is no implicit default for BYOH controllers.

### Configuration

| Flag / env | Required | Description |
|------------|----------|-------------|
| `--host` / `HOST_SLUG` | Yes | The local host's slug. |
| `--user-slug` / `USER_SLUG` | Yes | The owning user's slug. |
| `--hub-url` / `HUB_URL` | Yes | Hub NATS WebSocket URL (e.g. `wss://hub.mclaude.example/nats`). |
| `--creds-file` | No | Path to the per-host user JWT credentials (default `~/.mclaude/hosts/{hslug}/nats.creds`). |
| `--data-dir` | No | Root for per-project worktrees (default `~/.mclaude/projects/`). |
| `LOG_LEVEL` | No | Default `info`. |

### Interfaces

#### NATS subscriptions

Subscribes via the per-host user JWT (issued at `mclaude host register`):

| Subject | Behavior |
|---------|----------|
| `mclaude.users.{USER_SLUG}.hosts.{HOST_SLUG}.api.projects.provision` | Request/reply. Materializes `~/.mclaude/projects/{pslug}/worktree/`, clones git URL if provided, registers credential helpers from `~/.mclaude/projects/{pslug}/.credentials/`, then starts a session-agent subprocess for that project. Replies success once the session-agent's `--ready` probe passes. |
| `mclaude.users.{USER_SLUG}.hosts.{HOST_SLUG}.api.projects.create` | Request/reply. Identical to provision today. |
| `mclaude.users.{USER_SLUG}.hosts.{HOST_SLUG}.api.projects.update` | Request/reply. Refreshes credentials in `~/.mclaude/projects/{pslug}/.credentials/` and signals the session-agent to reload. |
| `mclaude.users.{USER_SLUG}.hosts.{HOST_SLUG}.api.projects.delete` | Request/reply. Stops the session-agent subprocess (SIGINT, 10s grace, SIGKILL), removes `~/.mclaude/projects/{pslug}/`. |

There is no user-level wildcard — a BYOH controller serves exactly one user/host.

#### Process supervision

For each provisioned project:

- Starts `mclaude-session-agent --mode standalone --user-slug … --host … --project-slug … --data-dir ~/.mclaude/projects/{pslug}` as a child process.
- Restarts the child on crash with a 2-second delay.
- On controller shutdown (SIGINT / SIGTERM), forwards SIGINT to all children and waits up to 30 seconds before exit.

### Liveness

When the local controller connects to hub NATS, hub publishes `$SYS.ACCOUNT.{accountKey}.CONNECT`; control-plane updates `hosts.last_seen_at` and `mclaude-hosts` KV `online=true`. On disconnect, `online=false`. The controller does not publish heartbeats.

## Shared Behavior

### Provisioning request shape

```json
{
  "userID":      "uuid-v4",
  "userSlug":    "alice-gmail",
  "hostSlug":    "us-east",
  "projectID":   "uuid-v4",
  "projectSlug": "billing",
  "gitUrl":      "https://github.com/alice/billing.git",
  "gitIdentityId": "uuid"
}
```

Reply on success:

```json
{ "ok": true, "projectSlug": "billing" }
```

Reply on failure:

```json
{ "ok": false, "error": "human-readable description", "code": "rbac_failed | image_pull_failed | git_clone_failed | …" }
```

The control-plane treats a NATS request timeout (`PROVISION_TIMEOUT_SECONDS`, default 10s) the same as a 503-style reply.

### Authentication

Both controller variants authenticate to NATS via JWT signed by the deployment-level account key. They never receive operator or account NKeys themselves; the worker NATS holds the account public key for trust-chain verification. JWT scopes:

- `mclaude-controller-k8s`: `mclaude.users.*.hosts.{cluster-slug}.>` (issued at cluster registration).
- `mclaude-controller-local`: `mclaude.users.{uslug}.hosts.{hslug}.>` (the user's per-host JWT — the same one the local session-agent uses).

A leaked controller JWT is scoped only to its host (or its cluster's wildcard at the user level for the K8s variant); rotation is by re-registration in both cases.

## Error Handling

| Failure | Behavior |
|---------|----------|
| Provision request: `MCProject` reconcile fails before `Ready` | Reply `{ok: false, error, code}`; control-plane returns 503 to the SPA; Postgres `projects.status` set to `failed`. |
| Provision request: BYOH `git clone` fails | Reply `{ok: false, code: "git_clone_failed"}`; same surfacing. |
| Update request without matching project | Reply `{ok: false, code: "not_found"}`; control-plane logs and returns 404. |
| Delete request without matching project | Idempotent: reply `{ok: true}` even if already gone. |
| Worker leaf-link drops (K8s only) | Worker NATS reconnects automatically. Provision requests issued during the gap return 503 to the SPA. The cluster shows offline in `mclaude-hosts` until the leaf re-links. |
| `HOST_SLUG` / `--host` not provided (local) | Fatal exit at startup with `FATAL: HOST_SLUG required`. |
| Account JWT signature mismatch | NATS rejects the controller connection at startup; the controller fails its readiness check. Operator intervention required (re-issue creds via `mclaude cluster register` or `mclaude host register`). |
| Single-cluster degenerate case (mclaude-cp + mclaude-worker in same K8s cluster) | Worker leaf URL is `localhost:7422`. Behavior is otherwise identical. |

## Dependencies

### `mclaude-controller-k8s`

- Worker NATS (leaf-linked into hub) and the per-cluster JWT credentials.
- Kubernetes API for namespaces, deployments, secrets, configmaps, PVCs, RBAC, and the `MCProject` CRD.
- The `{release}-session-agent-template` ConfigMap deployed by `mclaude-worker` Helm chart.
- Optional: trust-manager for corporate CA bundle injection.

### `mclaude-controller-local`

- Hub NATS reachable from the BYOH machine.
- The per-host JWT in `~/.mclaude/hosts/{hslug}/nats.creds`.
- `git`, `gh`, `glab` binaries (same as session-agent dependencies).
- The `mclaude-session-agent` binary on `$PATH`.
