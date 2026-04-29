# Spec: Helm Charts (mclaude-cp + mclaude-worker)

Per ADR-0035 the previous single `mclaude` chart is split into two independently-installed charts. Both share image references, persistence knobs, and ingress conventions, but each owns a distinct slice of the architecture.

| Chart | Purpose | Installed in |
|-------|---------|--------------|
| `mclaude-cp` | Central control-plane: hub NATS, Postgres, control-plane Deployment, SPA, operator/account NKey bootstrap. | The single `mclaude-cp` Kubernetes cluster. |
| `mclaude-worker` | Worker cluster: worker NATS (leaf-linked into hub), `mclaude-controller-k8s` operator, session-agent template. | Each worker Kubernetes cluster (one per cluster host). |

A single-cluster degenerate deployment installs **both** charts into the same K8s cluster, with the worker NATS leaf URL pointing at the in-cluster hub service.

BYOH machines do **not** use Helm — they install the `mclaude-cli` binary, run `mclaude host register`, and start `mclaude-controller-local`. See `docs/mclaude-controller/spec-controller.md`.

---

## Chart: `mclaude-cp`

### Deployment

`helm install mclaude-cp charts/mclaude-cp -n mclaude-system` into the central cluster. Idempotent — safe to re-run for upgrades.

### Pre-install Job: `init-keys`

Generates the deployment-level operator + account NKey trust chain on first install. This is what makes the rest of the system bootstrappable without manual key surgery.

| Kind | Name | Notes |
|---|---|---|
| Job | `mclaude-cp-init-keys` | Helm pre-install hook (weight `-20`). Runs `mclaude-cp init-keys`, which calls into `mclaude-common/pkg/nats/operator-keys.go` for the actual NKey + JWT generation logic (the same package home as `FormatNATSCredentials` in `pkg/nats/creds.go`, so the CLI can reuse the helpers for BYOH bootstrap). Generates `operatorNKey` + `accountNKey` pairs and corresponding JWTs (account JWT signed by operator key, operator JWT self-signed). Writes them to Secret `mclaude-system/operator-keys`. Idempotent: if the Secret already exists, exits without regenerating. Also creates the bootstrap admin row in Postgres when `controlPlane.bootstrapAdminEmail` is set. |
| Secret | `mclaude-system/operator-keys` | Type `Opaque`, mode `0600`. Keys: `operatorJwt`, `operatorSeed`, `accountJwt`, `accountSeed`. Hub NATS pod template references this Secret for `resolver_preload`. Control-plane Deployment mounts it at `OPERATOR_KEYS_PATH` to sign per-host user JWTs. |

The Job is the only place the operator/account seeds are generated; subsequent deploys (and air-gapped installs) reuse the existing Secret. To rotate the trust chain, delete the Secret and re-run `helm upgrade`; cluster registrations + per-host JWTs must then be reissued.

### Hub NATS

| Kind | Name | Notes |
|---|---|---|
| StatefulSet | `{release}-nats` | Single replica (configurable). VolumeClaimTemplate `data` when persistence enabled. |
| Service | `{release}-nats` | ClusterIP. Ports: client (4222), websocket (8080), monitor (8222), **leafnodes (7422)**. |
| Service | `{release}-nats-headless` | Headless service for StatefulSet DNS. |
| ConfigMap | `{release}-nats-config` | `nats.conf` with the 3-tier trust chain (`operator`, `resolver: MEMORY`, `resolver_preload`) referencing `mclaude-system/operator-keys`, plus `leafnodes { listen: 0.0.0.0:7422 }`. JetStream domain `hub`. |

For the full NATS server config see `docs/spec-state-schema.md` — NATS Server Configuration.

### PostgreSQL

| Kind | Name | Notes |
|---|---|---|
| StatefulSet | `{release}-postgres` | Single replica. Runs as UID 999. VolumeClaimTemplate `data` when persistence enabled. |
| Service | `{release}-postgres` | ClusterIP. Port: 5432. |
| Service | `{release}-postgres-headless` | Headless service for StatefulSet DNS. |

For Postgres table schemas see `docs/spec-state-schema.md` — Postgres.

### Control-Plane

| Kind | Name | Notes |
|---|---|---|
| Deployment | `{release}-control-plane` | Configurable replicas. Optional `dbmate` init container when `migrations.enabled`. Mounts `mclaude-system/operator-keys` at `OPERATOR_KEYS_PATH`. Mounts `provider-config` ConfigMap when OAuth providers are configured. |
| Service | `{release}-control-plane` | ClusterIP. Ports: http (8080), metrics (9091). |
| ConfigMap | `{release}-provider-config` | `providers.json` — OAuth provider instances. Created only when `controlPlane.providers` is non-empty. |
| ServiceAccount | `{release}-mclaude` | Used by the control-plane Deployment. **No K8s permissions** — the control-plane has no controller-runtime client per ADR-0035. |

The previous `ClusterRole` / `ClusterRoleBinding` granting cross-namespace pod/secret/CRD access is **removed** from `mclaude-cp`. Those permissions live in the `mclaude-worker` chart's controller ServiceAccount.

### SPA

| Kind | Name | Notes |
|---|---|---|
| Deployment | `{release}-spa` | nginx + the built SPA bundle. |
| Service | `{release}-spa` | Maps 80 → 8080. |

### Ingress

| Kind | Name | Notes |
|---|---|---|
| Ingress | `{release}` | Routes `/auth`, `/api`, `/admin`, `/scim` to control-plane; `/` to SPA. Created when `ingress.enabled`. |
| Ingress | `{release}-nats-ws` | NATS WebSocket on a dedicated hostname → hub NATS WebSocket port. Created when `ingress.natsHost` is set. |
| Ingress | `{release}-leaf` | Optional. Exposes `leafnodes` port 7422 over a TLS-terminated TCP/WS proxy when worker clusters live outside the hub's network. Created when `ingress.leafHost` is set. |

### Namespace

| Kind | Name | Notes |
|---|---|---|
| Namespace | `mclaude-system` | Created when `namespace.create` is true. |

### Configuration knobs

| Key | Default | Description |
|---|---|---|
| `global.imageRegistry` | `""` | Override image registry for all images (air-gapped mirror). |
| `namespace.create` | `true` | Whether the chart creates `mclaude-system`. |
| `nats.enabled` | `true` | Deploy hub NATS. |
| `nats.persistence.size` | `10Gi` | Hub JetStream PVC size. |
| `nats.leafNodes.listenPort` | `7422` | Leaf-node listen port on hub NATS. |
| `postgres.enabled` | `true` | Deploy Postgres. |
| `postgres.persistence.size` | `20Gi` | Postgres PVC size. |
| `controlPlane.externalUrl` | `""` | **Required.** External URL (e.g. `https://dev.mclaude.richardmcsong.com`). Chart fails if unset. |
| `controlPlane.bootstrapAdminEmail` | `""` | First admin's email. The `init-keys` Job pre-creates a `users` row with `is_admin=true`, `oauth_id=NULL`. The first OAuth login matching this email links the OAuth identity. |
| `controlPlane.providers` | `[]` | OAuth provider instances. |
| `controlPlane.config.devSeed` | `false` | Create `dev@mclaude.local` user, default `local` machine host, and a default project on startup. |
| `controlPlane.config.jwtExpirySeconds` | `28800` | Per-host user JWT lifetime (8h). |
| `controlPlane.migrations.enabled` | `false` | Run dbmate schema migrations as an init container. |
| `spa.enabled` | `true` | Deploy the SPA. |
| `ingress.enabled` | `true` | Create Ingress resources. |
| `ingress.host` | `""` | Platform hostname. |
| `ingress.natsHost` | `""` | Separate hostname for NATS WebSocket; second Ingress when set. |
| `ingress.leafHost` | `""` | Hostname for leaf-node ingress (cross-cluster WAN deployments). |
| `ingress.tls` | `[]` | TLS configuration. cert-manager + ExternalDNS produce wildcard certs per `docs/spec-tls-certs.md`. |
| `ingress.externalDnsTarget` | `""` | Override A-record target for ExternalDNS. |

The `slug-backfill` Job is removed — per ADR-0035 there is no migration of existing data; deployment is a clean break.

### Pre-requisite Secrets (created outside the chart)

- `mclaude-postgres` — key `postgres-password`.

`mclaude-control-plane` is **no longer** a pre-requisite — the operator keys it carried are now generated by the in-chart `init-keys` Job and stored in `mclaude-system/operator-keys`.

---

## Chart: `mclaude-worker`

### Deployment

`helm install mclaude-worker charts/mclaude-worker -n mclaude-system` into each worker cluster. Required values come from the response of `mclaude cluster register` (run against the hub).

```bash
mclaude cluster register --slug us-east --jetstream-domain us-east \
    --leaf-url nats-leaf://hub.mclaude.example:7422

# Returns: leafJwt, leafSeed, accountJwt, operatorJwt, jsDomain
# Admin places these into the worker's NATS Secret + Helm values.
```

### Worker NATS

| Kind | Name | Notes |
|---|---|---|
| StatefulSet | `{release}-nats` | Single replica (configurable). VolumeClaimTemplate `data` when persistence enabled. |
| Service | `{release}-nats` | ClusterIP. Ports: client (4222), websocket (8080), monitor (8222). |
| ConfigMap | `{release}-nats-config` | `nats.conf` with same 3-tier trust chain as the hub (`operator`, `accountJwt`, `resolver: MEMORY`), plus `leafnodes { remotes: [{url: $LEAF_URL, credentials: /etc/nats/leaf.creds}] }` and `jetstream { domain: $JS_DOMAIN }`. |
| Secret | `{release}-nats-leaf-creds` | Contains the leaf-node credentials (`leafJwt` + `leafSeed`) returned by `mclaude cluster register`. Mode `0600`. |
| Secret | `{release}-nats-trust` | Contains `operatorJwt` + `accountJwt` returned by `mclaude cluster register` — referenced from `nats-config` for `resolver_preload`. |

For full NATS server config see `docs/spec-state-schema.md` — NATS Server Configuration.

### Controller (`mclaude-controller-k8s`)

| Kind | Name | Notes |
|---|---|---|
| Deployment | `{release}-controller` | Single replica with leader election. Container is the kubebuilder operator binary. Mounts the per-cluster controller credentials (same Secret as `nats-leaf-creds`, since the controller JWT and leaf JWT are the same JWT per ADR-0035). |
| Service | `{release}-controller-metrics` | Prometheus scrape target. |
| ServiceAccount | `{release}-controller` | The K8s SA that the controller uses. |
| ClusterRole | `{release}-controller` | Grants namespace, pod, deployment, PVC, secret, configmap, serviceaccount, role, rolebinding, and `MCProject` CRD management across all namespaces. |
| ClusterRoleBinding | `{release}-controller` | Binds the ClusterRole to the ServiceAccount. |
| CustomResourceDefinition | `mcprojects.mclaude.io` | `MCProject` v1alpha1. Namespaced. |

### Session-Agent Template

| Kind | Name | Notes |
|---|---|---|
| ConfigMap | `{release}-session-agent-template` | Static pod template values (image, resources, PVC sizes, corporate CA settings). Read by `mclaude-controller-k8s` to build per-project Deployments. |

### Configuration knobs

| Key | Default | Description |
|---|---|---|
| `clusterSlug` | `""` | **Required.** The cluster's canonical slug. Must match the slug used at `mclaude cluster register`. |
| `jsDomain` | `""` | **Required.** JetStream domain — must match `--jetstream-domain` from registration. |
| `leafUrl` | `""` | **Required.** Hub leaf-node URL (e.g. `nats-leaf://hub.mclaude.example:7422`). For single-cluster degenerate installs, set to `nats-leaf://{cp-release}-nats.mclaude-system.svc:7422`. |
| `leafCreds.existingSecret` | `""` | Name of a pre-created Secret with `leafJwt` + `leafSeed`. If empty, the chart expects raw values via `leafCreds.jwt` / `leafCreds.seed`. Note: leaf creds are used by the **worker NATS StatefulSet** for the leaf-node connection to hub NATS. The controller binary does **not** read these creds — it authenticates via `NATS_ACCOUNT_SEED` and generates its own ephemeral user JWT. |
| `trustChain.operatorJwt` | `""` | Operator JWT from cluster registration response. |
| `trustChain.accountJwt` | `""` | Account JWT from cluster registration response. |
| `trustChain.accountPublicKey` | `""` | Account NKey public key. Used in NATS `resolver_preload` configuration. |
| `trustChain.resolverPreload` | `""` | Pre-formatted resolver preload entry for NATS config. Alternative to separate `accountPublicKey` + `accountJwt` when the admin provides a complete preload string. |
| `hubUrl` | `""` | Hub NATS URL for reference/SPA direct connections. Informational — not consumed by any template or Go binary. Retained for operator documentation purposes. |
| `nats.persistence.size` | `10Gi` | Worker JetStream PVC size. |
| `controller.replicas` | `1` | Controller replicas (leader election handles HA). |
| `sessionAgent.image.*` | ghcr.io image | Image used in per-project Deployments created by the controller. |
| `sessionAgent.terminationGracePeriodSeconds` | `86400` | Graceful shutdown window. |
| `sessionAgent.persistence.storageClass` | `""` | Storage class for project PVCs (RWO). |
| `sessionAgent.persistence.size` | `50Gi` | Project PVC size. |
| `sessionAgent.nix.storageClass` | `""` | Storage class for Nix store PVCs. |
| `sessionAgent.nix.size` | `20Gi` | Nix PVC size. |
| `sessionAgent.corporateCA.enabled` | `false` | Mount trust-manager CA bundle into session-agent pods. |
| `sessionAgent.corporateCA.bundleName` | `""` | trust-manager Bundle CR name. |
| `sessionAgent.corporateCA.configMapName` | `""` | ConfigMap name synced by trust-manager. |
| `sessionAgent.corporateCA.configMapKey` | `ca-certificates.crt` | Key in the ConfigMap containing PEM certs. |
| `sessionAgentNatsUrl` | `""` | NATS URL injected into session-agent pods (overrides the worker NATS default). For single-cluster deployments where KV buckets live on hub NATS, set to the hub NATS URL (e.g. `nats://mclaude-cp-nats.mclaude-system.svc.cluster.local:4222`). Exposed to the controller as `SESSION_AGENT_NATS_URL`. |
| `controller.config.devOAuthToken` | `""` | Claude API OAuth token for dev/CI environments. When set, the controller injects it as `oauth-token` in per-user `user-secrets` Secret. Exposed to the controller as `DEV_OAUTH_TOKEN`. |

### Single-cluster degenerate install

Install both charts into the same Kubernetes cluster:

```bash
helm install mclaude-cp     charts/mclaude-cp     -n mclaude-system --create-namespace
mclaude cluster register --slug local --jetstream-domain local \
    --leaf-url nats-leaf://mclaude-cp-nats.mclaude-system.svc:7422
helm install mclaude-worker charts/mclaude-worker -n mclaude-system \
    --set clusterSlug=local --set jsDomain=local \
    --set leafUrl=nats-leaf://mclaude-cp-nats.mclaude-system.svc:7422 \
    --set-file trustChain.operatorJwt=./operator.jwt \
    --set-file trustChain.accountJwt=./account.jwt \
    --set-file leafCreds.jwt=./leaf.jwt --set-file leafCreds.seed=./leaf.seed
```

Behavior is identical to multi-cluster except the leaf URL points at the in-cluster hub service.

---

## Values files

| Values file | Purpose |
|---|---|
| `values.yaml` (cp) | Defaults for all hub knobs. Production-oriented images, persistence enabled, nginx ingress. |
| `values-dev.yaml` (cp) | Local k3d development. Images built locally (`pullPolicy: Never`), persistence disabled, no ingress, devSeed enabled. |
| `values-e2e.yaml` (cp) | CI end-to-end tests. Python HTTP stubs for control-plane and SPA, real NATS/Postgres, persistence disabled. |
| `values-k3d-ghcr.yaml` (cp) | Local k3d preview with ghcr.io images. Traefik ingress with TLS on `*.mclaude.richardmcsong.com`. |
| `values-airgap.yaml` (cp) | Air-gapped deployments. All images pulled via `global.imageRegistry`. |
| `values-aks.yaml` (cp) | Azure Kubernetes Service production. `managed-csi-premium`, larger PVCs, scaled replicas. |
| `values.yaml` (worker) | Defaults. Production image refs, persistence enabled. |
| `values-dev.yaml` (worker) | Local k3d. Tailored for single-cluster degenerate install. |
| `values-airgap.yaml` (worker) | Air-gapped variant. |
| `values-aks.yaml` (worker) | Production worker. |

## Dependencies

- Kubernetes 1.24+ (CRD v1, batch/v1 Job).
- A storage class supporting RWO (NATS, Postgres) — not required when persistence is disabled.
- A storage class supporting RWX (Nix store PVCs in session-agent pods, e.g. `azurefile-csi-premium` on AKS) — only when session agents use shared Nix stores.
- An Ingress controller (nginx or Traefik) when `ingress.enabled` (cp chart only — workers do not expose public HTTP).
- Pre-created Secret `mclaude-postgres` in the cp namespace.
- Optional: trust-manager for corporate CA bundle injection into session-agent pods (worker chart).
- For HTTPS: **cert-manager** in the cluster with a `ClusterIssuer` that owns the Secret named in `ingress.tls[0].secretName`. See `docs/spec-tls-certs.md`.
- For DNS automation: **ExternalDNS** in the cluster, configured with the DigitalOcean provider and `domainFilters=[mclaude.richardmcsong.com]`. See `docs/spec-tls-certs.md`.
- For each worker chart install: a successful `mclaude cluster register` call against the running hub control-plane to mint the per-cluster trust-chain credentials.
