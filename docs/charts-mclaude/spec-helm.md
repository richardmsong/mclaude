# Spec: Helm Charts (mclaude-cp + mclaude-worker)

Per ADR-0035 the previous single `mclaude` chart is split into two independently-installed charts. Both share image references, persistence knobs, and ingress conventions, but each owns a distinct slice of the architecture.

| Chart | Purpose | Installed in |
|-------|---------|--------------|
| `mclaude-cp` | Central control-plane: hub NATS, Postgres, MinIO object storage, control-plane Deployment, SPA, operator/account NKey bootstrap. | The single `mclaude-cp` Kubernetes cluster. |
| `mclaude-worker` | Worker cluster: `mclaude-controller-k8s` operator (hub-direct, independently installable), session-agent template. | Each worker Kubernetes cluster (one per cluster host). |

A single-cluster degenerate deployment installs **both** charts into the same K8s cluster; `mclaude-worker` connects directly to the hub NATS service.

BYOH machines do **not** use Helm — they install the `mclaude-cli` binary, run `mclaude host register`, and start `mclaude-controller-local`. See `docs/mclaude-controller-local/spec-controller-local.md`.

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
| ConfigMap | `{release}-nats-config` | `nats.conf` with the 3-tier trust chain (`operator`, `resolver: nats`, `resolver_preload`) referencing `mclaude-system/operator-keys`. JetStream domain `hub`. `resolver: nats` enables JWT revocation via `$SYS.REQ.CLAIMS.UPDATE` (ADR-0054). |

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
| Deployment | `{release}-control-plane` | Configurable replicas. Optional `dbmate` init container when `migrations.enabled`. Mounts `mclaude-system/operator-keys` at `OPERATOR_KEYS_PATH`. Mounts `provider-config` ConfigMap when OAuth providers are configured. When `minio.enabled=true` and `ingress.minioHost` is non-empty: env vars `S3_ENDPOINT=https://{ingress.minioHost}`, `S3_BUCKET={minio.bucket}`, `S3_ACCESS_KEY_ID` and `S3_SECRET_ACCESS_KEY` from Secret `{release}-minio` (or `minio.existingSecret`). `S3_REGION` is intentionally omitted — `loadS3Config()` defaults to `us-east-1` which is correct for MinIO path-style signing. |
| Service | `{release}-control-plane` | ClusterIP. Ports: http (8080), metrics (9091). |
| ConfigMap | `{release}-provider-config` | `providers.json` — OAuth provider instances. Created only when `controlPlane.providers` is non-empty. |
| ServiceAccount | `{release}-mclaude` | Used by the control-plane Deployment. **No K8s permissions** — the control-plane has no controller-runtime client per ADR-0035. |

The previous `ClusterRole` / `ClusterRoleBinding` granting cross-namespace pod/secret/CRD access is **removed** from `mclaude-cp`. Those permissions live in the `mclaude-worker` chart's controller ServiceAccount.

### SPA

| Kind | Name | Notes |
|---|---|---|
| Deployment | `{release}-spa` | nginx + the built SPA bundle. |
| Service | `{release}-spa` | Maps 80 → 8080. |

### MinIO

S3-compatible object storage for import archives and chat attachments per ADR-0053. All resources gated by `minio.enabled` (default `true`). Image: `minio/minio:RELEASE.2025-04-22T22-12-26Z` via the `mclaude-cp.image` helper (respects `global.imageRegistry`).

| Kind | Name | Notes |
|---|---|---|
| Deployment | `{release}-minio` | Single replica. Command: `minio server /data --console-address :9001`. PVC `{release}-minio-data` at `/data` (when `minio.persistence.enabled`); `emptyDir` otherwise. Env vars `MINIO_ROOT_USER`/`MINIO_ROOT_PASSWORD` from Secret `{release}-minio`. Pod security context uses standard `mclaude-cp.podSecurityContext` helper (`runAsNonRoot: true`, `runAsUser: 1000`, `runAsGroup: 1000`, `fsGroup: 1000` — compatible with MinIO's UID 1000 process). Container security context uses standard `mclaude-cp.securityContext` helper (same pattern as all other Deployments). Resources: `minio.resources`. |
| Service | `{release}-minio` | ClusterIP. Port 9000 (S3 API), port 9001 (console). Not exposed via Ingress by default. |
| PersistentVolumeClaim | `{release}-minio-data` | `accessModes: [ReadWriteOnce]`. Size: `minio.persistence.size`. StorageClass: `minio.persistence.storageClass` (empty = cluster default). Created only when `minio.persistence.enabled`. |
| Secret | `{release}-minio` | Keys `access-key` and `secret-key`, populated from `minio.rootUser`/`minio.rootPassword`. Created only when `minio.existingSecret` is unset. |
| Job | `{release}-minio-bucket` | Post-install/post-upgrade hook. `restartPolicy: Never`, `activeDeadlineSeconds: 120`, `backoffLimit: 3`, `hook-delete-policy: before-hook-creation,hook-succeeded`. Image: `minio/mc:RELEASE.2025-04-16T18-25-19Z` via `mclaude-cp.image` helper. Both containers share an `emptyDir` volume named `mc-config` mounted at `/root/.mc` (init writes alias config; main container reads it). Init container: `mc alias set local http://{release}-minio:9000 $ACCESS_KEY $SECRET_KEY`, then loops on `mc ready local` (max 60s). Main container: `mc mb --ignore-existing local/{minio.bucket}`. Both security context helpers (`mclaude-cp.podSecurityContext`, `mclaude-cp.securityContext`) are intentionally omitted — the `minio/mc` image may run as root, and this is a one-time admin operation. |

**Security**: MinIO is only accessible via ClusterIP inside the cluster. External access is TLS-terminated at Traefik Ingress (`{release}-minio`). The MinIO console (port 9001) is not exposed via Ingress. All object access by clients requires pre-signed URLs signed by the control-plane — no direct bucket access.

### Ingress

| Kind | Name | Notes |
|---|---|---|
| Ingress | `{release}` | Routes `/auth`, `/api`, `/admin`, `/scim` to control-plane; `/` to SPA. Created when `ingress.enabled`. |
| Ingress | `{release}-nats-ws` | NATS WebSocket on a dedicated hostname → hub NATS WebSocket port. Created when `ingress.natsHost` is set. |
| Ingress | `{release}-leaf` | Optional. Exposes `leafnodes` port 7422 over a TLS-terminated TCP/WS proxy when worker clusters live outside the hub's network. Created when `ingress.leafHost` is set. |
| Ingress | `{release}-minio` | MinIO S3 API at `ingress.minioHost` → `{release}-minio:9000`. Created when `ingress.enabled` AND `ingress.minioHost` non-empty. Inherits `ingress.annotations` (via `deepCopy`+`mergeOverwrite`) with `nginx.ingress.kubernetes.io/proxy-body-size: "0"` override (unlimited — supports 500MB import archives). ExternalDNS annotations point at `ingress.minioHost`/`ingress.externalDnsTarget`. TLS: iterates `ingress.tls`, appends `minioHost` to each entry's `hosts` list so Traefik SNI routing works. |

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
| `controlPlane.config.natsWsUrl` | `""` | External NATS WebSocket URL returned to browser clients on login. Injected as `NATS_WS_URL`. Empty means the SPA derives it from its own origin. |
| `controlPlane.config.logLevel` | `""` | Injected as `LOG_LEVEL`. **Not read by Go code** — zerolog uses its default level. |
| `controlPlane.devOAuthToken` | `""` | Claude API OAuth token for dev environments. Injected as `DEV_OAUTH_TOKEN`. Passed to per-user secrets for session-agent Claude API access. |
| `controlPlane.service.httpPort` | `8080` | Main API listen port. Injected as `HTTP_PORT` env var. **Known mismatch:** Go code reads `PORT`, not `HTTP_PORT` — the chart env var name doesn't match. Currently works because both default to 8080. |
| `controlPlane.service.adminPort` | `9090` | Loopback admin port. **Note:** chart default is `9090`, Go code default is `9091`. Helm deployments use 9090. |
| `controlPlane.service.metricsPort` | `9091` | Declared as a separate metrics container port. **Known issue:** Go code does not create a third listener — `/metrics` is served on the admin port. Port 9091 has no listener in Helm deployments. Prometheus scrapes should target the admin port instead. |
| `controlPlane.config.jwtRefreshThresholdSeconds` | `900` | Injected as `JWT_REFRESH_THRESHOLD_SECONDS`. **Not read by Go code** — phantom env var with no effect. |
| `controlPlane.migrations.enabled` | `false` | Run dbmate schema migrations as an init container. |
| `spa.enabled` | `true` | Deploy the SPA. |
| `ingress.enabled` | `true` | Create Ingress resources. |
| `ingress.host` | `""` | Platform hostname. |
| `ingress.natsHost` | `""` | Separate hostname for NATS WebSocket; second Ingress when set. |
| `ingress.minioHost` | `""` | Hostname for MinIO S3 API Ingress (e.g. `minio.mclaude.richardmcsong.com`). Must be a direct subdomain of the wildcard TLS cert, not a subdomain of `ingress.host`. When non-empty AND `ingress.enabled`, creates the MinIO Ingress and wires `S3_ENDPOINT` on the control-plane Deployment. |
| `ingress.leafHost` | `""` | Hostname for leaf-node ingress (cross-cluster WAN deployments). |
| `ingress.tls` | `[]` | TLS configuration. cert-manager + ExternalDNS produce wildcard certs per `docs/spec-tls-certs.md`. |
| `ingress.externalDnsTarget` | `""` | Override A-record target for ExternalDNS. |
| `minio.enabled` | `true` | Deploy MinIO in-cluster S3-compatible storage. When `false`, no MinIO resources are created and `S3_*` env vars are omitted from the control-plane Deployment. |
| `minio.image.registry` | `docker.io` | MinIO server image registry. Override for air-gapped deployments. |
| `minio.image.repository` | `minio/minio` | MinIO server image repository. |
| `minio.image.tag` | `RELEASE.2025-04-22T22-12-26Z` | MinIO server image tag (pinned for reproducibility). |
| `minio.image.pullPolicy` | `IfNotPresent` | MinIO server image pull policy. |
| `minio.resources` | `requests: {cpu: 100m, memory: 128Mi}, limits: {cpu: 500m, memory: 512Mi}` | MinIO Deployment resource requests and limits. |
| `minio.persistence.enabled` | `true` | Create a PVC for MinIO data. When `false`, data is stored in `emptyDir` (lost on pod restart). |
| `minio.persistence.storageClass` | `""` | Storage class for MinIO PVC. Empty string uses the cluster default. AKS overrides to `managed-csi-premium`. |
| `minio.persistence.size` | `20Gi` | MinIO PVC size. |
| `minio.bucket` | `mclaude` | S3 bucket name created on first install by the bucket-creation Job. All objects keyed as `{uslug}/{hslug}/{pslug}/...` per ADR-0053. |
| `minio.existingSecret` | `""` | Name of an existing Kubernetes Secret with keys `access-key` and `secret-key`. When set, the chart does not create its own Secret and `minio.rootUser`/`minio.rootPassword` are ignored. |
| `minio.rootUser` | `minioadmin` | MinIO root username. Used only when `minio.existingSecret` is unset. **Change for production.** |
| `minio.rootPassword` | `minioadmin` | MinIO root password. Used only when `minio.existingSecret` is unset. **Change for production.** |
| `minio.mcImage.registry` | `docker.io` | `minio/mc` image registry for the bucket-creation Job. Override for air-gapped deployments. |
| `minio.mcImage.repository` | `minio/mc` | `minio/mc` image repository. |
| `minio.mcImage.tag` | `RELEASE.2025-04-16T18-25-19Z` | `minio/mc` image tag. |
| `minio.mcImage.pullPolicy` | `IfNotPresent` | `minio/mc` image pull policy. |

The `slug-backfill` Job is removed — per ADR-0035 there is no migration of existing data; deployment is a clean break.

### Pre-requisite Secrets (created outside the chart)

- `mclaude-postgres` — key `postgres-password`.

`mclaude-control-plane` (or the value of `controlPlane.existingSecret`) — key `admin-token` (static bearer token for the loopback admin port). The operator keys it previously carried are now generated by the in-chart `init-keys` Job and stored in `mclaude-system/operator-keys`, but the `admin-token` key is still required.

---

## Chart: `mclaude-worker`

Independently installable into any Kubernetes cluster that can reach the control-plane over HTTPS and hub NATS over WebSocket (port 443). Registers the worker cluster as a host via the existing BYOH `mclaude host register` flow — no separate `mclaude cluster register` command, no local NATS StatefulSet, no leaf-node credentials.

### Deployment

```bash
helm install mclaude-worker charts/mclaude-worker -n mclaude-system --create-namespace \
  --set controlPlane.url=https://cp.mclaude.example \
  --set host.name="us-east" \
  --set host.hubNatsUrl=nats-wss://nats.mclaude.example:443
```

After install, read the NKey public key from the pre-install Job and attest the host from a workstation:

```bash
kubectl logs job/mclaude-worker-gen-host-nkey -n mclaude-system
mclaude host register --type cluster --name "us-east" --nkey-public <pubkey>
```

### Deregistration

Before uninstalling, deregister the host so CP removes the `hosts` row and revokes the JWT:

```bash
mclaude host deregister --slug us-east
helm uninstall mclaude-worker -n mclaude-system
```

A Helm pre-delete hook Job (Option B: automated deregistration without operator intervention) is a planned future enhancement — not implemented in V1.

### Pre-Install Job and NKey Secret

| Kind | Name | Notes |
|---|---|---|
| Job | `{release}-gen-host-nkey` | Pre-install hook (weight `-10`). Generates NKey pair via `nkeys.CreateUser()` (U-prefix). Writes decorated seed string to Secret `{release}-host-creds` field `nkey_seed`. Prints public key to Job log and NOTES.txt. Idempotent — skips if Secret exists. |
| Secret | `{release}-host-creds` | Single field: `nkey_seed`. JWT is **not** stored here — acquired in-memory via challenge-response on boot. |

### Controller (`mclaude-controller-k8s`)

| Kind | Name | Notes |
|---|---|---|
| Deployment | `{release}-controller` | Single replica. Mounts `{release}-host-creds` at `/etc/mclaude/host-creds/`. Env: `HUB_NATS_URL`, `CONTROL_PLANE_URL`, `HOST_NKEY_SEED_PATH=/etc/mclaude/host-creds/nkey_seed`. |
| Service | `{release}-controller-metrics` | Prometheus scrape target. |
| ServiceAccount | `{release}-controller` | K8s SA used by the controller. |
| ClusterRole | `{release}-controller` | Grants namespace, deployment, PVC, secret, configmap, serviceaccount, role, rolebinding, and `MCProject` CRD management. |
| ClusterRoleBinding | `{release}-controller` | Binds ClusterRole to ServiceAccount. |
| CustomResourceDefinition | `mcprojects.mclaude.io` | `MCProject` v1alpha1. Namespaced. |

### Session-Agent Template

| Kind | Name | Notes |
|---|---|---|
| ConfigMap | `{release}-session-agent-template` | Static pod template values (image, imagePullPolicy, terminationGracePeriodSeconds, resourcesJson, projectPvcSize, projectPvcStorageClass, nixPvcSize, nixPvcStorageClass, corporateCAEnabled, corporateCAConfigMapName, corporateCAConfigMapKey). Read by `mclaude-controller-k8s` to build per-project Deployments. |

### Configuration Knobs

| Key | Default | Description |
|---|---|---|
| `controlPlane.url` | `""` | **Required.** Control-plane HTTP URL. Used for challenge-response auth and session-agent bootstrap. |
| `host.name` | `""` | **Required.** Display name rendered into NOTES.txt only. Controller derives its actual slug from the JWT. |
| `host.hubNatsUrl` | `""` | **Required.** Hub NATS WebSocket URL (e.g. `nats-wss://nats.mclaude.example:443`). Set as `HUB_NATS_URL` on the controller Deployment. |
| `controller.replicas` | `1` | Controller replicas. |
| `sessionAgent.image.*` | ghcr.io image | Image for per-project session-agent Deployments. |
| `sessionAgent.resources.requests.cpu` | `200m` | CPU request for session-agent pods. |
| `sessionAgent.resources.requests.memory` | `64Mi` | Memory request for session-agent pods. Kept low so test pods can be scheduled alongside dev pods in k3d (ADR-0066). AKS overrides via `values-aks.yaml`. |
| `sessionAgent.resources.limits.cpu` | `2000m` | CPU limit for session-agent pods. |
| `sessionAgent.resources.limits.memory` | `2Gi` | Memory limit for session-agent pods. |
| `sessionAgent.terminationGracePeriodSeconds` | `86400` | Graceful shutdown window. |
| `sessionAgent.persistence.storageClass` | `""` | Storage class for project PVCs (RWO). |
| `sessionAgent.persistence.size` | `50Gi` | Project PVC size. |
| `sessionAgent.nix.storageClass` | `""` | Storage class for Nix store PVCs. |
| `sessionAgent.nix.size` | `20Gi` | Nix PVC size. |
| `sessionAgent.corporateCA.enabled` | `false` | Mount trust-manager CA bundle into session-agent pods. Rendered as `corporateCAEnabled` in the session-agent-template ConfigMap. |
| `sessionAgent.corporateCA.bundleName` | `""` | trust-manager Bundle CR name. Consumed directly by the controller binary at runtime (not stored in the session-agent-template ConfigMap) — used for Bundle CR lookup when labeling user namespaces. |
| `sessionAgent.corporateCA.configMapName` | `""` | ConfigMap name synced by trust-manager. Rendered as `corporateCAConfigMapName` in the session-agent-template ConfigMap. |
| `sessionAgent.corporateCA.configMapKey` | `ca-certificates.crt` | PEM certs key in ConfigMap. Rendered as `corporateCAConfigMapKey` in the session-agent-template ConfigMap. |
| `controller.config.devOAuthToken` | `""` | Claude API OAuth token for dev/CI. Injected as `oauth-token` in per-user `user-secrets` Secret via `DEV_OAUTH_TOKEN` env. |

### Single-Cluster Degenerate Install

```bash
helm install mclaude-cp     charts/mclaude-cp     -n mclaude-system --create-namespace
helm install mclaude-worker charts/mclaude-worker -n mclaude-system \
  --set controlPlane.url=https://cp.mclaude.example \
  --set host.name="local" \
  --set host.hubNatsUrl=nats://mclaude-cp-nats.mclaude-system.svc:4222

kubectl logs job/mclaude-worker-gen-host-nkey -n mclaude-system
mclaude host register --type cluster --name "local" --nkey-public <pubkey>
```

### Migration from Leaf-NATS Worker Chart

```bash
helm uninstall mclaude-worker -n mclaude-system
helm install mclaude-worker charts/mclaude-worker -n mclaude-system \
  --set controlPlane.url=... --set host.name=... --set host.hubNatsUrl=...
# Then run mclaude host register (see Deployment section above)
```

Brief downtime for K8s-hosted projects during cutover. No in-place upgrade path.

---

## Values files

| Values file | Purpose |
|---|---|
| `values.yaml` (cp) | Defaults for all hub knobs. Production-oriented images, persistence enabled, nginx ingress. MinIO enabled by default (`minio.enabled: true`, `minio.persistence.enabled: true`). `ingress.minioHost` is empty — set it in a cluster-specific overlay. |
| `values-dev.yaml` (cp) | Local k3d development. Images built locally (`pullPolicy: Never`), persistence disabled, no ingress, devSeed enabled. `minio.enabled: false` — no valid `S3_ENDPOINT` without an Ingress hostname, and presigned URLs would not be reachable. |
| `values-e2e.yaml` (cp) | CI end-to-end tests. Python HTTP stub control-plane and SPA, real NATS/Postgres, persistence disabled. `minio.enabled: false` — stub control-plane does not read env vars or call S3. |
| `values-k3d-ghcr.yaml` (cp) | Local k3d preview with ghcr.io images. Traefik ingress with TLS on `*.mclaude.richardmcsong.com`. Sets `ingress.minioHost: minio.mclaude.richardmcsong.com` (direct subdomain of the wildcard cert, covered by `*.mclaude.richardmcsong.com`). |
| `values-airgap.yaml` (cp) | Air-gapped deployments. All images pulled via `global.imageRegistry`. Overrides `minio.image.registry` and `minio.mcImage.registry` to `registry.internal.example.com` so both MinIO server and `minio/mc` bucket-Job images are pulled from the internal mirror. |
| `values-aks.yaml` (cp) | Azure Kubernetes Service production. `managed-csi-premium`, larger PVCs, scaled replicas. Sets `minio.persistence.storageClass: managed-csi-premium` (consistent with NATS and Postgres storage class overrides). |
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
- For each worker chart install: the operator must run `mclaude host register --type cluster --name <name> --nkey-public <pubkey>` against the running hub control-plane to register the cluster host before the controller can connect.
