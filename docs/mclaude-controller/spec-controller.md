# Spec: Controller (K8s + Local)

The controller is the binary that owns per-host runtime resources. Per ADR-0035 it ships in two flavors that share the same NATS protocol and provisioning interface but differ in their substrate:

- `mclaude-controller-k8s` — kubebuilder operator that runs inside a worker Kubernetes cluster.
- `mclaude-controller-local` — process supervisor that runs on a BYOH machine (laptop, desktop, VM).

Both subscribe to host-scoped NATS subjects and reconcile project-level resources in response. Both report liveness implicitly via their NATS connection (the hub's `$SYS.ACCOUNT.*.CONNECT/DISCONNECT` events; control-plane updates `hosts.last_seen_at`).

> **ADR-0058 / ADR-0054 notice:** The BYOH local controller (`mclaude-controller-local`) uses the ADR-0054 host-scoped subject scheme (`mclaude.hosts.{hslug}.>`) with a host JWT that has **zero JetStream access**. Per-project agents spawned by the local controller obtain their own per-project JWTs via the ADR-0054 unified HTTP credential protocol. See the Variant 2 sections below for details.

## Role

The control-plane has no Kubernetes client and does not directly manage processes on BYOH machines. For K8s controllers, provisioning intent arrives on `mclaude.users.*.hosts.{cluster-slug}.api.projects.{provision,create,update,delete}`. For BYOH controllers (ADR-0054/0058), CP publishes fan-out messages on `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.{create,delete}` after validating the user's HTTP request; the host controller receives these via its `mclaude.hosts.{hslug}.>` subscription and materializes the per-project resources.

## Variant 1: `mclaude-controller-k8s`

### Deployment

A single Deployment in the worker cluster's `mclaude-system` namespace. Built with kubebuilder (controller-runtime). Leader election enabled — a future HA scale-out is supported by configuration; v1 runs a single replica.

The cluster's slug is configured at deploy time via the Helm value `clusterSlug` (e.g. `us-east`) and is required. It is identical to the slug all users granted to this cluster carry in their `hosts.slug` column.

### Configuration

| Variable | Required | Description |
|----------|----------|-------------|
| `CLUSTER_SLUG` | Yes | The cluster's canonical slug. Used to build the wildcard NATS subscription `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.>`. |
| `NATS_URL` | Yes | Worker NATS service URL (the same NATS that leaf-links into the hub). |
| `NATS_ACCOUNT_SEED` | Yes | Account NKey seed. The controller generates its own ephemeral user JWT signed by this key — same pattern as control-plane. In Helm, populated from the `operator-keys` Secret's `accountSeed`. |
| `NATS_CREDENTIALS_PATH` | No | Injected by Helm but **not read by the controller binary**. The leaf-creds file at this path is used only by the worker NATS StatefulSet for the leaf-node connection, not by the controller. Retained in the Helm template for future use. |
| `JS_DOMAIN` | No | Injected by Helm but **not yet read by the controller binary**. Reserved for future JetStream domain qualification. |
| `HELM_RELEASE_NAME` | No | Used to locate the session-agent-template ConfigMap (default `mclaude-worker`). |
| `SESSION_AGENT_TEMPLATE_CM` | No | Explicit name of the session-agent-template ConfigMap. Overrides the `HELM_RELEASE_NAME`-derived name. Set by Helm (`{{ .Release.Name }}-session-agent-template`). |
| `SESSION_AGENT_NATS_URL` | No | NATS URL injected into session-agent pods as `NATS_URL`. Defaults to the FQDN-qualified worker NATS URL. For single-cluster deployments where KV buckets live on hub NATS, set to the hub NATS URL (e.g. `nats://mclaude-cp-nats.mclaude-system.svc.cluster.local:4222`). |
| `DEV_OAUTH_TOKEN` | No | Claude API OAuth token for dev environments. When set, the reconciler injects it as `oauth-token` in per-user `user-secrets` Secret. Session-agent entrypoint reads this and exports `CLAUDE_CODE_OAUTH_TOKEN`. |
| `METRICS_ADDR` | No | Prometheus metrics listen address (default `:8082`). |
| `HEALTH_PROBE_ADDR` | No | Health/readiness probe listen address (default `:8081`). |
| `LEADER_ELECTION` | No | Injected by Helm as `"true"` but **not yet read by the controller binary**. Leader election is not currently configured in `ctrl.Options`. |
| `LOG_LEVEL` | No | Injected by Helm but **not yet read by the controller binary**. Zerolog uses its default level. |
| `LEADER_ELECTION_NAMESPACE` | No | Defaults to `mclaude-system`. **Not yet implemented** — neither the Go binary nor the Helm template reads this variable. |

### Interfaces

#### NATS subscriptions

Subscribes via the per-cluster JWT (issued at `POST /admin/clusters` time, scoped to `mclaude.users.*.hosts.{cluster-slug}.>`):

| Subject | Behavior |
|---------|----------|
| `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.provision` | Request/reply. Resolves `userSlug`, `hostSlug`, `projectSlug` from the subject + payload; creates the `MCProject` CR; returns success when reconcile reaches `Ready` (or 503-style failure). |
| `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.create` | Request/reply. Identical to provision today; reserved for future fan-out. |
| `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.update` | Request/reply. Reconciles per-user `user-secrets` Secret (NATS creds, OAuth tokens, CLI configs) and re-applies the pod template. **Not yet implemented** — the NATS handler dispatches `create`, `provision`, and `delete` but has no handler for `update`. Reserved for future credential refresh flow. |
| `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.delete` | Request/reply. Tears down the `MCProject` CR (and cascaded namespace/RBAC/PVCs/Deployment). |

The wildcard at the user level is what enables one cluster controller to receive provisioning requests from every user granted access to its cluster.

#### Kubernetes resources

For full schemas see `docs/spec-state-schema.md` — Kubernetes Resources. Summary:

- CRD `MCProject` (`mcprojects.mclaude.io/v1alpha1`).
- Per-user namespace `mclaude-{userId}` with ServiceAccount, Role, RoleBinding.
- Per-user `user-secrets` Secret (NATS credentials, OAuth tokens, CLI configs) and `user-config` ConfigMap.
- Per-project `project-{projectId}` and `nix-{projectId}` PVCs.
- Per-project `project-{projectId}` Deployment.
- Watched: `{release}-session-agent-template` ConfigMap in `mclaude-system` (image, resources, PVC sizes, corporate CA settings) — changes re-enqueue all `MCProject` CRs.

#### Reconciler loop

On each reconcile cycle:

1. Loads the session-agent-template ConfigMap for image, resource, and PVC configuration.
2. Ensures the user namespace exists with correct labels (including `mclaude.io/user-namespace: "true"` when corporate CA is enabled for trust-manager targeting).
3. Ensures RBAC resources (ServiceAccount, Role, RoleBinding).
4. Ensures the `user-config` ConfigMap and `user-secrets` Secret. NATS credentials in the Secret are session-agent JWTs minted by the controller via `IssueSessionAgentJWT` (signed by the account signing key).
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
| `--cp-url` / `CP_URL` | Yes | Control-plane HTTP base URL (e.g. `https://api.mclaude.internal`). Required for host JWT refresh via HTTP challenge-response. |
| `--creds-file` | No | Path to the host JWT credentials (default `~/.mclaude/hosts/{hslug}/nats.creds`). This is the ADR-0054 host-scoped JWT (zero JetStream). |
| `--data-dir` | No | Root for per-project worktrees (default `~/.mclaude/projects/`). Per-project data is stored at `{data-dir}/{uslug}/{pslug}/` for multi-user host support. |
| `LOG_LEVEL` | No | Default `info`. |

### Interfaces

#### NATS subscriptions

Subscribes via the host JWT (issued at `mclaude host register`, scoped to `mclaude.hosts.{hslug}.>` per ADR-0054):

| Subject | Behavior |
|---------|----------|
| `mclaude.hosts.{HOST_SLUG}.users.{uslug}.projects.{pslug}.create` | Fan-out from CP. Materializes `{data-dir}/{uslug}/{pslug}/worktree/`, clones git URL if provided, starts a session-agent subprocess for that project, registers the agent's NKey public key with CP (see Agent Credential Registration below), and replies success. |
| `mclaude.hosts.{HOST_SLUG}.users.{uslug}.projects.{pslug}.delete` | Fan-out from CP. Stops the session-agent subprocess (SIGINT, 10s grace, SIGKILL), removes `{data-dir}/{uslug}/{pslug}/`. |
| `mclaude.hosts.{HOST_SLUG}.api.agents.register` | Request/reply. Forwards agent NKey public key registration requests to CP (see Agent Credential Registration). |

The host controller subscribes to `mclaude.hosts.{HOST_SLUG}.>` — a single wildcard that captures all project lifecycle messages for any user with access to this host. There is no user-level wildcard; host access enforcement is entirely in the control-plane (ADR-0054).

> **Migration note (ADR-0058):** The previous subject scheme `mclaude.users.{uslug}.hosts.{hslug}.api.projects.{provision,create,update,delete}` (ADR-0035) is superseded. The `provision` and `update` verbs are no longer used; provisioning uses `create`, and credential refresh is handled via HTTP challenge-response (see Host Credential Refresh).

#### Process supervision

For each provisioned project:

- Starts `mclaude-session-agent --mode standalone --user-slug … --host … --project-slug … --data-dir {data-dir}/{uslug}/{pslug}/worktree` as a child process.
- Restarts the child on crash with a 2-second delay.
- On controller shutdown (SIGINT / SIGTERM), forwards SIGINT to all children (10s grace per child, then SIGKILL) and waits for all children to exit before the controller exits.

**Credential isolation (ADR-0058):** The host controller never touches the agent's JWT or private key. The agent generates its own NKey pair, authenticates directly with CP via HTTP challenge-response, and manages its own credential refresh. The controller's only role in the credential flow is registering the agent's public key with CP (see below).

#### NKey IPC (ADR-0058)

When a per-project agent subprocess starts:

1. The agent generates its own NKey pair at startup (the private seed never leaves the agent process).
2. The agent passes its **public key** to the host controller via local IPC (stdout line or file at a well-known path).
3. The host controller reads the public key and proceeds to Agent Credential Registration.

The host controller never receives, stores, or handles the agent's private key material.

#### Agent Credential Registration (ADR-0058)

After receiving the agent's NKey public key via local IPC, the host controller registers it with the control-plane:

1. Host controller publishes a NATS request to `mclaude.hosts.{HOST_SLUG}.api.agents.register` with the agent's public key, user slug, host slug, and project slug.
2. CP validates host access, project ownership, and host assignment, then stores the agent's public key in the `agent_credentials` table (`UNIQUE(user_id, host_slug, project_slug)` — one credential per project, not per session).
3. On `NOT_FOUND` response (project create not yet processed — fan-out race condition), the controller retries with exponential backoff: 100ms initial delay, doubling, max 5s interval, max 10 attempts.
4. Once registration succeeds, the agent authenticates itself directly via HTTP challenge-response (`POST /api/auth/challenge` + `POST /api/auth/verify`) to obtain its per-project JWT.

The agent's per-project JWT is scoped to exactly one project's KV keys and subjects (ADR-0054 per-project agent model). The granularity is per-project, not per-session — sessions within a project run inside the same per-project agent process.

### Host Credential Refresh (ADR-0054/0058)

The host JWT has a **5-minute TTL** (ADR-0054). The host controller refreshes its own credential via the unified HTTP challenge-response protocol:

1. Before TTL expiry (proactive refresh), the controller sends `POST /api/auth/challenge {nkey_public}` followed by `POST /api/auth/verify {nkey_public, challenge, signature}`.
2. CP validates the host's NKey signature and returns a fresh host JWT.
3. The controller reconnects to NATS with the new JWT.
4. On `permissions violation` error, the controller triggers an immediate refresh + retry.

The host controller is **not involved** in agent credential refresh — each per-project agent manages its own JWT refresh independently via the same HTTP challenge-response protocol.

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
- `mclaude-controller-local`: `mclaude.hosts.{hslug}.>` (host-scoped JWT per ADR-0054). **Zero JetStream access** — no `$JS.*`, `$KV.*`, or `$O.*` subjects in the host JWT. The host controller uses only NATS core pub/sub for provisioning commands. Per-project agents spawned by the controller obtain their own separate per-user/per-project JWTs with JetStream access via the ADR-0054 HTTP credential protocol.

> **ADR-0054 change:** The local controller previously used `mclaude.users.{uslug}.hosts.{hslug}.>` (the user's per-host JWT, which included JetStream access). Under ADR-0054, this is replaced by a host-specific JWT scoped to `mclaude.hosts.{hslug}.>` with zero JetStream permissions. The host JWT is constant-size regardless of how many users share the host.

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
- The host JWT in `~/.mclaude/hosts/{hslug}/nats.creds` (ADR-0054 host-scoped JWT, zero JetStream).
- `git`, `gh`, `glab` binaries (same as session-agent dependencies).
- The `mclaude-session-agent` binary on `$PATH`.

## Daemon Mode Deprecation (ADR-0058)

The `mclaude-session-agent --daemon` mode is deprecated and replaced by the `mclaude-controller-local` + per-project-agent architecture described above. The `mclaude daemon` CLI command now launches `mclaude-controller-local` instead of `mclaude-session-agent --daemon`.

**Deprecation phases:**

1. **Phase 1 — Parallel availability (current):** `mclaude-controller-local` is extended with ADR-0054 subject scheme, agent credential registration, and host credential refresh. `mclaude-session-agent --daemon` continues to exist but is not actively maintained.
2. **Phase 2 — Hard cut-over (ADR-0054 deployment):** Permission tightening makes daemon mode non-functional — host JWTs get zero JetStream access, and the daemon's cross-project access pattern is denied.
3. **Phase 3 — Code removal:** `daemon.go`, `daemon_jobs.go`, and all daemon-specific code are removed from `mclaude-session-agent`. The `runQuotaPublisher` goroutine moves to the designated per-project agent (ADR-0044).

**No migration of in-flight sessions.** Sessions running under daemon mode at cut-over time are terminated. Users re-create them under the new architecture.
