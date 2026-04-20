# Spec: Helm Chart

## Role

The `mclaude` Helm chart deploys the mclaude AI coding platform onto a Kubernetes cluster. It provisions NATS JetStream (event bus and KV state), PostgreSQL (user/project metadata), the control-plane API server, a single-page application (SPA), RBAC for cross-namespace reconciliation, and a session-agent pod template that the control-plane uses to spawn per-project workloads at runtime. An optional slug-backfill migration Job runs as a Helm hook before upgrades.

## Deployment

The chart is installed via `helm install` / `helm upgrade` into the `mclaude-system` namespace. The base `values.yaml` contains all defaults. Environment-specific overrides layer on top:

| Values file | Purpose |
|---|---|
| `values.yaml` | Defaults for all knobs. Production-oriented images, persistence enabled, nginx ingress. |
| `values-dev.yaml` | Local k3d development. Images built locally (`pullPolicy: Never`), persistence disabled, no ingress, devSeed enabled, slug-backfill disabled. |
| `values-e2e.yaml` | CI end-to-end tests. Python HTTP stubs for control-plane and SPA, real NATS/Postgres, persistence disabled, slug-backfill disabled. |
| `values-k3d-ghcr.yaml` | Local k3d preview with ghcr.io images. Traefik ingress with TLS, NATS WebSocket on a dedicated subdomain, GitHub OAuth provider pre-configured, persistence enabled. |
| `values-airgap.yaml` | Air-gapped deployments. All images pulled from an internal registry mirror via `global.imageRegistry`. |
| `values-aks.yaml` | Azure Kubernetes Service production. `managed-csi-premium` storage class, larger PVC sizes, scaled replicas, higher resource limits. |

Pre-requisite Secrets (created outside the chart):
- `mclaude-postgres` -- key `postgres-password`
- `mclaude-control-plane` -- keys `admin-token`, `database-url`, `nats-operator-jwt`, `nats-operator-seed`

## Resources

All static resources deploy into the `mclaude-system` namespace (name configurable via `namespace.name`). Resource names are prefixed with the Helm release fullname (`{release}`).

### NATS

| Kind | Name | Notes |
|---|---|---|
| StatefulSet | `{release}-nats` | Single replica (configurable). VolumeClaimTemplate `data` when persistence enabled. |
| Service | `{release}-nats` | ClusterIP. Ports: client (4222), websocket (8080), monitor (8222). |
| Service | `{release}-nats-headless` | Headless service for StatefulSet DNS. |
| ConfigMap | `{release}-nats-config` | `nats.conf` -- server port, JetStream, WebSocket, max payload. |
| ConfigMap | `{release}-nats-permissions` | NATS permission grant templates for SPA, control-plane, controller, and session-agent JWTs. |

### PostgreSQL

| Kind | Name | Notes |
|---|---|---|
| StatefulSet | `{release}-postgres` | Single replica. Runs as UID 999. VolumeClaimTemplate `data` when persistence enabled. |
| Service | `{release}-postgres` | ClusterIP. Port: 5432. |
| Service | `{release}-postgres-headless` | Headless service for StatefulSet DNS. |

### Control-Plane

| Kind | Name | Notes |
|---|---|---|
| Deployment | `{release}-control-plane` | Configurable replicas. Optional `dbmate` init container when `migrations.enabled`. Mounts provider-config ConfigMap when OAuth providers are configured. |
| Service | `{release}-control-plane` | ClusterIP. Ports: http (8080), metrics (9091). Admin port (9090) is intentionally excluded -- access via port-forward only. |
| ConfigMap | `{release}-provider-config` | `providers.json` -- OAuth provider instances. Created only when `controlPlane.providers` is non-empty. |
| ServiceAccount | `{release}-mclaude` | Used by the control-plane Deployment. |
| ClusterRole | `{release}-control-plane` | Grants namespace, pod, deployment, PVC, secret, configmap, serviceaccount, role, rolebinding, and MCProject CRD management across all namespaces. |
| ClusterRoleBinding | `{release}-control-plane` | Binds the ClusterRole to the ServiceAccount. |

### Session-Agent Template

| Kind | Name | Notes |
|---|---|---|
| ConfigMap | `{release}-session-agent-template` | Static pod template values (image, resources, PVC sizes, corporate CA settings). The control-plane reconciler reads this to create per-project Deployments at runtime. |

### CRD

| Kind | Name | Notes |
|---|---|---|
| CustomResourceDefinition | `mcprojects.mclaude.io` | `MCProject` v1alpha1. Namespaced. Drives the reconciler's per-project provisioning (namespace, PVCs, secrets, RBAC, deployment). |

### Ingress

| Kind | Name | Notes |
|---|---|---|
| Ingress | `{release}` | Routes `/auth`, `/api`, `/scim` to control-plane; `/` catch-all to SPA. Created when `ingress.enabled`. |
| Ingress | `{release}-nats-ws` | Routes NATS WebSocket traffic on a dedicated hostname to the NATS WebSocket port. Created when `ingress.natsHost` is set. |

### Namespace

| Kind | Name | Notes |
|---|---|---|
| Namespace | `mclaude-system` | Created when `namespace.create` is true. |

### Slug Backfill Migration

| Kind | Name | Notes |
|---|---|---|
| Job | `{release}-slug-backfill-{revision}` | Helm pre-install/pre-upgrade hook (weight -10). Runs the `slug-backfill` binary from the control-plane image. Adds slug columns to Postgres tables and rekeys KV buckets to the typed-slug format. Idempotent. Deleted on success; retained on failure. Disabled by default in dev/e2e. |

## Configuration

### Global

| Key | Default | Description |
|---|---|---|
| `global.imageRegistry` | `""` | Override image registry for all images (air-gapped mirror). |
| `global.imagePullSecrets` | `[]` | Pull secrets applied to all pods. |
| `namespace.name` | `mclaude-system` | Target namespace. |
| `namespace.create` | `true` | Whether the chart creates the namespace. |

### NATS

| Key | Default | Description |
|---|---|---|
| `nats.enabled` | `true` | Deploy NATS. |
| `nats.replicas` | `1` | StatefulSet replicas. |
| `nats.config.maxPayload` | `"8388608"` | Max message size in bytes (8 MB). |
| `nats.config.maxFileStoreSize` | `"10737418240"` | JetStream file store limit (10 GB). |
| `nats.persistence.enabled` | `true` | Use PVC for JetStream data. |
| `nats.persistence.storageClass` | `""` | Storage class (cluster default if empty). |
| `nats.persistence.size` | `10Gi` | PVC size. |

For NATS KV buckets, streams, and subject schemas, see `spec-state-schema.md`.

### PostgreSQL

| Key | Default | Description |
|---|---|---|
| `postgres.enabled` | `true` | Deploy Postgres. |
| `postgres.auth.database` | `mclaude` | Database name. |
| `postgres.auth.username` | `mclaude` | Database user. |
| `postgres.auth.existingSecret` | `mclaude-postgres` | Secret containing the password. |
| `postgres.persistence.enabled` | `true` | Use PVC for data. |
| `postgres.persistence.size` | `20Gi` | PVC size. |

For Postgres table schemas, see `spec-state-schema.md`.

### Control-Plane

| Key | Default | Description |
|---|---|---|
| `controlPlane.externalUrl` | `""` | **Required.** External URL (e.g. `https://mclaude.internal`). Chart fails if unset. |
| `controlPlane.providers` | `[]` | OAuth provider instances (GitHub, GitLab, etc.). |
| `controlPlane.config.devSeed` | `false` | Create `dev@mclaude.local` user on startup. |
| `controlPlane.config.natsWsUrl` | `""` | NATS WebSocket URL returned to browsers. |
| `controlPlane.config.logLevel` | `info` | Log level. |
| `controlPlane.config.jwtExpirySeconds` | `28800` | JWT token lifetime (8 hours). |
| `controlPlane.config.jwtRefreshThresholdSeconds` | `900` | JWT refresh threshold (15 minutes). |
| `controlPlane.existingSecret` | `mclaude-control-plane` | Secret with admin-token, database-url, NATS operator credentials. |
| `controlPlane.devOAuthToken` | `""` | Dev-only OAuth token provisioned into per-user secrets. |
| `controlPlane.migrations.enabled` | `false` | Run dbmate schema migrations as an init container. |

### SPA

| Key | Default | Description |
|---|---|---|
| `spa.enabled` | `true` | Deploy the SPA. |
| `spa.containerPort` | `8080` | nginx container port (non-root). Service maps 80 to 8080. |
| `spa.replicas` | `1` | Deployment replicas. |

### Session-Agent

| Key | Default | Description |
|---|---|---|
| `sessionAgent.image.*` | ghcr.io image | Image used in per-project Deployments created by the reconciler. |
| `sessionAgent.terminationGracePeriodSeconds` | `86400` | 24-hour graceful shutdown window. |
| `sessionAgent.persistence.storageClass` | `""` | Storage class for project PVCs (RWO). |
| `sessionAgent.persistence.size` | `50Gi` | Project PVC size. |
| `sessionAgent.nix.storageClass` | `""` | Storage class for Nix store PVCs. |
| `sessionAgent.nix.size` | `20Gi` | Nix PVC size. |
| `sessionAgent.corporateCA.enabled` | `false` | Mount trust-manager CA bundle into session-agent pods. |
| `sessionAgent.corporateCA.bundleName` | `""` | trust-manager Bundle CR name. |
| `sessionAgent.corporateCA.configMapName` | `""` | ConfigMap name synced by trust-manager. |
| `sessionAgent.corporateCA.configMapKey` | `ca-certificates.crt` | Key in the ConfigMap containing PEM certs. |

### Ingress

| Key | Default | Description |
|---|---|---|
| `ingress.enabled` | `true` | Create Ingress resources. |
| `ingress.className` | `nginx` | IngressClass name. |
| `ingress.host` | `""` | Platform hostname. |
| `ingress.natsHost` | `""` | Separate hostname for NATS WebSocket. Creates a second Ingress when set. |
| `ingress.tls` | `[]` | TLS configuration (secretName + hosts). |

### Slug Backfill

| Key | Default | Description |
|---|---|---|
| `slugBackfill.enabled` | `true` | Run the typed-slug migration hook. |
| `slugBackfill.backoffLimit` | `0` | No retries -- failure requires human intervention. |
| `slugBackfill.activeDeadlineSeconds` | `300` | Job timeout (5 minutes). |

## Dependencies

- Kubernetes 1.24+ (CRD v1, batch/v1 Job)
- A storage class supporting RWO (for NATS and Postgres PVCs) -- not required when persistence is disabled
- A storage class supporting RWX (for Nix store PVCs in session-agent pods, e.g. `azurefile-csi-premium` on AKS) -- only when session agents use shared Nix stores
- An Ingress controller (nginx or Traefik) when `ingress.enabled`
- Pre-created Secrets: `mclaude-postgres` and `mclaude-control-plane` in the target namespace
- Optional: trust-manager for corporate CA bundle injection into session-agent pods
- Optional: cert-manager or manual TLS secret for HTTPS termination
