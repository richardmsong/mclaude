# Spec: Helm Charts (mclaude-postgres + mclaude-cp + mclaude-worker)

Per ADR-0035 the previous single `mclaude` chart was split into `mclaude-cp` and `mclaude-worker`. Per ADR-0094, Postgres was further split out of `mclaude-cp` into its own `mclaude-postgres` chart so managed-DB swaps (RDS, Cloud SQL) work by simply not installing the `mclaude-postgres` release. The three charts share image references, persistence knobs, and ingress conventions, but each owns a distinct slice of the architecture.

| Chart | Purpose | Installed in |
|-------|---------|--------------|
| `mclaude-postgres` | PostgreSQL StatefulSet + Services. Single replica. Self-hosted in-cluster DB. Skipped entirely on managed-DB deployments. | The single `mclaude-cp` Kubernetes cluster (only when self-hosting). |
| `mclaude-cp` | Central control-plane: hub NATS, MinIO object storage, control-plane Deployment, SPA, operator/account NKey bootstrap. Connects to a Postgres instance addressed by `database.host` + `database.passwordSecretRef`. | The single `mclaude-cp` Kubernetes cluster. |
| `mclaude-worker` | Worker cluster: `mclaude-controller-k8s` operator (hub-direct, independently installable), session-agent template. | Each worker Kubernetes cluster (one per cluster host). |

A single-cluster degenerate deployment installs **all three** charts into the same K8s cluster, in order: `mclaude-postgres` → `mclaude-cp` → `mclaude-worker`. `mclaude-worker` connects directly to the hub NATS service. Each `helm install` waits for its release to be Ready before the next.

Non-Helm hosts register via `mclaude host register --name <name> --nkey-public <pubkey>` from a workstation.

---

## Chart: `mclaude-cp`

### Deployment

`helm install mclaude-cp charts/mclaude-cp -n mclaude-system` into the central cluster. Idempotent — safe to re-run for upgrades.

### Pre-install Job: `init-keys`

Generates the deployment-level operator + account NKey trust chain on first install. This is what makes the rest of the system bootstrappable without manual key surgery.

| Kind | Name | Notes |
|---|---|---|
| Job | `mclaude-cp-init-keys` | Helm pre-install hook (weight `-20`). Runs `mclaude-cp init-keys`, which calls into `mclaude-common/pkg/nats/operator-keys.go` for the actual NKey + JWT generation logic. Generates `operatorNKey` + `accountNKey` pairs and corresponding JWTs (account JWT signed by operator key, operator JWT self-signed). Writes them to Secret `mclaude-system/operator-keys`. Idempotent: if the Secret already exists, exits without regenerating. Also creates the bootstrap admin row in Postgres when `controlPlane.bootstrapAdminEmail` is set. |
| Secret | `mclaude-system/operator-keys` | Type `Opaque`, mode `0600`. Keys: `operatorJwt`, `operatorSeed`, `accountJwt`, `accountSeed`. Hub NATS pod template references this Secret for `resolver_preload`. Control-plane Deployment mounts it at `OPERATOR_KEYS_PATH` to sign per-host user JWTs. |

The Job is the only place the operator/account seeds are generated; subsequent deploys (and air-gapped installs) reuse the existing Secret. To rotate the trust chain, delete the Secret and re-run `helm upgrade`; cluster registrations + per-host JWTs must then be reissued.

### Hub NATS

| Kind | Name | Notes |
|---|---|---|
| StatefulSet | `{release}-nats` | Single replica (configurable). VolumeClaimTemplate `data` when persistence enabled. |
| Service | `{release}-nats` | ClusterIP. Ports: client (4222), websocket (8080), monitor (8222), **leafnodes (7422)**. |
| Service | `{release}-nats-headless` | Headless service for StatefulSet DNS. |
| ConfigMap | `{release}-nats-config` | `nats.conf` with the 3-tier trust chain (`operator`, `resolver: MEMORY`, `resolver_preload`) referencing `mclaude-system/operator-keys`. JetStream domain `hub`. `resolver: MEMORY` is the current implementation; upgrade to `resolver: nats` (full account resolver with JWT revocation) is tracked in ADR-0054 (draft). |

For the full NATS server config see `docs/spec-state-schema.md` — NATS Server Configuration.

### PostgreSQL

Per ADR-0094, Postgres is **not** part of `mclaude-cp` — it is its own chart (`mclaude-postgres`, see the `## Chart: mclaude-postgres` section below). `mclaude-cp` accepts a connection contract via `database.host` + `database.passwordSecretRef` and is agnostic to whether the Postgres on the other end is in-cluster, AKS-managed, RDS, or Cloud SQL.

For Postgres table schemas see `docs/spec-state-schema.md` — Postgres.

### TLS Certificate (per ADR-0097)

| Kind | Name | Notes |
|---|---|---|
| Certificate (cert-manager.io/v1) | `{release}-wildcard` (e.g. `mclaude-cp-wildcard`) | Rendered only when `tls.certManager.enabled=true`. Carries `helm.sh/resource-policy: keep` so `helm uninstall` does NOT delete it (preserves Let's Encrypt rate-limit budget across wipe/reinstall cycles). The derived Secret (cert-manager-managed, named per `ingress.tls[0].secretName`) persists naturally — no helm release tracks it for deletion. References ClusterIssuer `tls.certManager.issuerRef.name` (typically `mclaude-letsencrypt-prod` from ADR-0033 cluster bootstrap). When `tls.certManager.enabled=false` (default) the chart skips the Certificate entirely and operators pre-create the TLS Secret out-of-band (AKS Application Gateway, air-gapped corporate CA). |

### Control-Plane

| Kind | Name | Notes |
|---|---|---|
| Deployment | `{release}-control-plane` | Configurable replicas. Optional `dbmate` init container when `migrations.enabled` — uses the same `database.*` env contract as the main container (no hardcoded Postgres Secret name or DSN). Mounts `mclaude-system/operator-keys` at `OPERATOR_KEYS_PATH`. Mounts `provider-config` ConfigMap when OAuth providers are configured. **Postgres env contract** (per ADR-0094): `POSTGRES_PASSWORD` is loaded via `valueFrom.secretKeyRef` against the Secret named in `database.passwordSecretRef` (key `postgres-password`); `DATABASE_URL` is composed at pod runtime via env-var interpolation `postgres://{database.user}:$(POSTGRES_PASSWORD)@{database.host}:{database.port}/{database.name}?sslmode={database.sslmode}`. The `dbmate` init container (when enabled) and the `init-keys` Job use the identical contract. If the Secret named by `database.passwordSecretRef` does not exist, the pod stays in `CreateContainerConfigError` (env-from reference to a missing Secret); helm install with `--wait` fails fast. When `minio.enabled=true` and `ingress.minioHost` is non-empty: env vars `S3_ENDPOINT=https://{ingress.minioHost}`, `S3_BUCKET={minio.bucket}`, `S3_ACCESS_KEY_ID` and `S3_SECRET_ACCESS_KEY` from Secret `{release}-minio` (or `minio.existingSecret`). `S3_REGION` is intentionally omitted — `loadS3Config()` defaults to `us-east-1` which is correct for MinIO path-style signing. |
| Service | `{release}-control-plane` | ClusterIP. Port: http (8080). `/metrics` is served on the same port, unauthenticated. |
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
| Job | `{release}-minio-bucket` | Post-install/post-upgrade hook. `restartPolicy: Never`, `activeDeadlineSeconds: {{ minio.bucketJobDeadlineSeconds }}` (default 300), `backoffLimit: 3`, `hook-delete-policy: before-hook-creation,hook-succeeded`. Image: `minio/mc:latest` (ADR-0072: pinned tag `RELEASE.2025-04-16T18-25-19Z` was invalid on Docker Hub) via `mclaude-cp.image` helper. Both containers share an `emptyDir` volume named `mc-config` mounted at `/root/.mc` (init writes alias config; main container reads it). Init container: `mc alias set local http://{release}-minio:9000 $ACCESS_KEY $SECRET_KEY`, then loops on `mc ready local` (max 60s). Main container: `mc mb --ignore-existing local/{minio.bucket}`. Both security context helpers (`mclaude-cp.podSecurityContext`, `mclaude-cp.securityContext`) are intentionally omitted — the `minio/mc` image may run as root, and this is a one-time admin operation. |

**Security**: MinIO is only accessible via ClusterIP inside the cluster. External access is TLS-terminated at Traefik Ingress (`{release}-minio`). The MinIO console (port 9001) is not exposed via Ingress. All object access by clients requires pre-signed URLs signed by the control-plane — no direct bucket access.

### Ingress

| Kind | Name | Notes |
|---|---|---|
| Ingress | `{release}` | Routes `/auth`, `/api` to control-plane; `/` to SPA. Created when `ingress.enabled`. `/admin` and `/scim` paths are removed (ADR-0101). |
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
| `nats.config.maxControlLine` | `"16384"` | Max NATS protocol control-line length in bytes (ADR-0087). Default NATS value is 4096, which is too small for user JWTs with multiple host grants (~240 bytes per additional host). 16384 supports ~62 concurrent host grants per user. |
| `database.host` | `""` | **Required.** DNS-resolvable hostname for Postgres (e.g. `mclaude-postgres.mclaude-system.svc` for in-cluster, or `foo.us-east-1.rds.amazonaws.com` for managed). Chart fails if unset. |
| `database.port` | `5432` | Postgres port. |
| `database.user` | `mclaude` | Postgres user. |
| `database.name` | `mclaude` | Postgres database name. |
| `database.passwordSecretRef` | `""` | **Required.** Name of an existing Secret in the cp namespace with key `postgres-password`. Operator creates this Secret (CI does it via Bitwarden). The Secret name is `mclaude-postgres` for the in-cluster default; managed-DB users create their own Secret with the same `postgres-password` key. |
| `database.sslmode` | `disable` | Postgres SSL mode in the DSN. Override to `require` (or `verify-full` with mounted CA) for managed-DB. |
| `controlPlane.externalUrl` | `""` | **Required.** External URL (e.g. `https://dev.mclaude.richardmcsong.com`). Chart fails if unset. |
| `controlPlane.bootstrapAdminEmail` | `""` | First admin's email. The `init-keys` Job creates a `users` row with `is_admin=true` and a bcrypt password hash from `bootstrapAdminPassword`. UPSERTs on `email`: re-running helm upgrade is idempotent (existing user → update `is_admin=true` and refresh password if `bootstrapAdminPassword` is non-empty; otherwise preserve existing hash). |
| `controlPlane.bootstrapAdminPassword` | `""` | Password for the bootstrap admin. Hashed by `init-keys` and stored in `users.password_hash`. Required for CI to authenticate without OAuth. Empty on subsequent helm upgrades preserves the existing hash. |
| `controlPlane.providers` | `[]` | OAuth provider instances. |
| `controlPlane.config.jwtExpirySeconds` | `28800` | Per-host user JWT lifetime (8h). |
| `controlPlane.config.natsWsUrl` | `""` | External NATS WebSocket URL returned to browser clients on login. Injected as `NATS_WS_URL`. Empty means the SPA derives it from its own origin. |
| `controlPlane.config.logLevel` | `""` | Injected as `LOG_LEVEL`. **Not read by Go code** — zerolog uses its default level. |
| `controlPlane.service.httpPort` | `8080` | Main API listen port. Injected as `HTTP_PORT` env var. **Known mismatch:** Go code reads `PORT`, not `HTTP_PORT` — the chart env var name doesn't match. Currently works because both default to 8080. |
| `nats.config.jetstream.encryption.storeKeySecret` | `""` | ADR-0091. Name of a Kubernetes Secret holding the JetStream at-rest encryption key under field `store-key`. When set, the NATS ConfigMap renders a `jetstream { store_dir: ..., key: $STORE_KEY }` stanza and the NATS StatefulSet mounts the Secret. Required to protect plaintext tokens in `mclaude-user-info-{uslug}` on disk. Generate with `openssl rand -base64 32` and inject via Bitwarden in CI (see `.github/workflows/deploy-main.yml`). |
| `tls.certManager.enabled` | `false` | ADR-0097. When `true`, the chart renders a cert-manager `Certificate` CR for the wildcard hostname declared by `commonName`/`dnsNames`. The CR carries `helm.sh/resource-policy: keep` so `helm uninstall` does not delete it. Default `false` so each environment overlay opts in explicitly; `values-k3d-ghcr.yaml` flips to `true`. AKS/airgap leave it `false` and supply a pre-created TLS Secret via `ingress.tls[0].secretName`. |
| `tls.certManager.issuerRef.kind` | `ClusterIssuer` | Issuer kind for the Certificate. Required when `enabled=true`. |
| `tls.certManager.issuerRef.name` | `""` | Issuer name. Required when `enabled=true`. Cluster bootstrap (per ADR-0033) creates `mclaude-letsencrypt-prod`. |
| `tls.certManager.commonName` | `""` | Wildcard `commonName` (e.g. `*.mclaude.richardmcsong.com`). Required when `enabled=true`. |
| `tls.certManager.dnsNames` | `[]` | List of DNS SANs (typically the wildcard plus the apex domain). Required when `enabled=true`. |
| `tls.certManager.duration` | `2160h` | Certificate lifetime (90 days, LE default). |
| `tls.certManager.renewBefore` | `720h` | Renew window (30 days before expiry). |
| `controlPlane.config.jwtRefreshThresholdSeconds` | `900` | Injected as `JWT_REFRESH_THRESHOLD_SECONDS`. **Not read by Go code** — phantom env var with no effect. |
| `controlPlane.migrations.enabled` | `false` | Run dbmate schema migrations as an init container. The init container inherits the same `database.*` env contract as the main control-plane container (per ADR-0094): `POSTGRES_PASSWORD` from `database.passwordSecretRef` Secret + `DATABASE_URL` interpolation against `database.host`/`database.port`/`database.user`/`database.name`/`database.sslmode`. No hardcoded Postgres Secret name or hostname. |
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
| `minio.mcImage.tag` | `latest` | `minio/mc` image tag. Uses `latest` because the previously-pinned tag `RELEASE.2025-04-16T18-25-19Z` did not exist on Docker Hub (ADR-0072). The bucket-creation command (`mc mb --ignore-existing`) is stable across mc versions. |
| `minio.mcImage.pullPolicy` | `IfNotPresent` | `minio/mc` image pull policy. |
| `minio.bucketJobDeadlineSeconds` | `300` | `activeDeadlineSeconds` for the minio-bucket Job. Increased from the original 120s to allow time for a cold image pull in environments where the `minio/mc` image is not pre-cached (ADR-0070). |

The `slug-backfill` Job is removed — per ADR-0035 there is no migration of existing data; deployment is a clean break.

### Pre-requisite Secrets (created outside the chart)

`mclaude-cp` consumes a Postgres connection password via `database.passwordSecretRef` (default Secret name `mclaude-postgres`, key `postgres-password`). The Secret itself is created either by the `mclaude-postgres` chart's pre-requisite step (operator runbook) or by CI's Bitwarden fetch — see the `## Chart: mclaude-postgres` section below for the full contract. `mclaude-cp` only reads the Secret; it does not create it.

`mclaude-control-plane` (or the value of `controlPlane.existingSecret`) — no longer requires an `admin-token` key. The operator keys it previously carried are generated by the `init-keys` Job and stored in `mclaude-system/operator-keys`.

---

## Chart: `mclaude-postgres`

Per ADR-0094. Deploys an in-cluster PostgreSQL StatefulSet for self-hosted installs. **Skipped entirely on managed-DB deployments** — operators install only `mclaude-cp` (and `mclaude-worker` for cluster hosts) and point `database.host` at their managed endpoint.

### Deployment

`helm install mclaude-postgres charts/mclaude-postgres -n mclaude-system --set existingSecret=mclaude-postgres`. The Secret named by `existingSecret` must already exist with a `postgres-password` key (operator/CI pre-creates it; CI uses Bitwarden). Idempotent — safe to re-run for upgrades.

### Resources

| Kind | Name | Notes |
|---|---|---|
| StatefulSet | `{fullname}` | Single replica. Image `postgres:16-alpine`. Runs as UID 999. VolumeClaimTemplate `data` (RWO) when `persistence.enabled`. Env: `POSTGRES_DB={{ database.name | default "mclaude" }}`, `POSTGRES_USER={{ database.user | default "mclaude" }}`, `POSTGRES_PASSWORD` from `existingSecret` key `postgres-password`. |
| Service | `{fullname}` | ClusterIP. Port: 5432. The DNS name `{fullname}.{namespace}.svc` is what `mclaude-cp` sets `database.host` to in self-hosted installs (e.g. `mclaude-postgres.mclaude-system.svc` when released as `mclaude-postgres`). |
| Service | `{fullname}-headless` | Headless service for StatefulSet DNS. |

### Configuration knobs

| Key | Default | Description |
|---|---|---|
| `image.repository` | `postgres` | Postgres image. |
| `image.tag` | `16-alpine` | Pinned tag. |
| `replicas` | `1` | Single replica. v1 does not support HA replication; managed-DB is the prod HA path. |
| `persistence.enabled` | `true` | When false, uses `emptyDir` (dev/CI only). |
| `persistence.size` | `20Gi` | PVC size. |
| `persistence.storageClass` | `""` | Empty = cluster default. AKS overrides via `values-aks.yaml` (`managed-csi-premium`). |
| `existingSecret` | `""` | **Required.** Name of an existing Secret with key `postgres-password`. Chart `helm install` fails fast if empty — there is no chart-generated password fallback (avoids the helm-uninstall-deletes-password-but-PVC-survives footgun). |
| `database.user` | `mclaude` | Initial superuser created by the postgres image on first start. |
| `database.name` | `mclaude` | Initial database created by the postgres image on first start. |

### Pre-requisite Secret (created outside the chart)

| Secret | Required keys | Created by |
|--------|--------------|-----------|
| `mclaude-postgres` (default name; configurable via `existingSecret`) | `postgres-password` | Operator runbook (`kubectl create secret generic mclaude-postgres --from-literal=postgres-password=$(openssl rand -base64 24) -n mclaude-system`) for local dev; CI's Bitwarden fetch step in `.github/workflows/deploy-main.yml` for prod. |

The chart **does not generate** the password. Single source of truth.

### Cutover from the legacy in-chart Postgres

Per ADR-0094 the Postgres StatefulSet/Service templates have been **removed** from `mclaude-cp`. Existing deployments that ran the prior chart will have a `data-mclaude-cp-postgres-0` PVC bound to the old StatefulSet. Operator runbook for migration:

```bash
helm uninstall mclaude-cp -n mclaude-system
kubectl delete pvc data-mclaude-cp-postgres-0 -n mclaude-system   # explicit; survives helm uninstall
helm install mclaude-postgres charts/mclaude-postgres -n mclaude-system [...]
helm install mclaude-cp        charts/mclaude-cp        -n mclaude-system [...]
```

DB state is **not preserved** across the cutover. The project has no live prod deployments today; clean-slate migration is the trade-off for a chart that doesn't auto-rename PVCs.

### Values files

| File | Use |
|------|-----|
| `values.yaml` | Defaults. Local k3d uses these as-is plus `--set existingSecret=mclaude-postgres`. |
| `values-aks.yaml` | AKS production: `persistence.storageClass: managed-csi-premium`. |
| `values-e2e.yaml` | CI end-to-end tests: `persistence.enabled: false` (emptyDir). |

---

## Chart: `mclaude-worker`

Independently installable into any Kubernetes cluster that can reach the control-plane over HTTPS and hub NATS over WebSocket (port 443). Registers the worker as a host via `mclaude host register` — no local NATS StatefulSet, no leaf-node credentials.

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
mclaude host register --name "us-east" --nkey-public <pubkey>
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
| Job | `{release}-gen-host-nkey` | Pre-install and pre-upgrade hook (weight `-10`). Generates NKey pair via `nkeys.CreateUser()` (U-prefix). Writes decorated seed string to Secret `{release}-host-creds` field `nkey_seed`. Prints public key to Job log and NOTES.txt. Idempotent — skips if Secret exists. Creates Secret with `helm.sh/resource-policy: keep` so the seed survives `helm uninstall`. |
| Secret | `{release}-host-creds` | Single field: `nkey_seed`. Created exclusively by the gen-host-nkey Job (not a Helm chart template). Annotated `helm.sh/resource-policy: keep` — persists across `helm uninstall`. JWT is **not** stored here — acquired in-memory via challenge-response on boot. |

### Controller (`mclaude-controller-k8s`)

| Kind | Name | Notes |
|---|---|---|
| Deployment | `{release}-controller` | Single replica. Mounts `{release}-host-creds` at `/etc/mclaude/host-creds/`. Env: `HUB_NATS_URL`, `CONTROL_PLANE_URL`, `HOST_NKEY_SEED_PATH=/etc/mclaude/host-creds/nkey_seed`. |
| Service | `{release}-controller-metrics` | Prometheus scrape target. |
| ServiceAccount | `{release}-controller` | K8s SA used by the controller. |
| ClusterRole | `{release}-controller` | Per ADR-0091: namespace, deployment, secret, configmap, serviceaccount, role, rolebinding management. The `MCProject` CRD permission is **removed**. |
| ClusterRoleBinding | `{release}-controller` | Binds ClusterRole to ServiceAccount. |

The `mcprojects.mclaude.io` CustomResourceDefinition is **removed** by ADR-0091 — project pod lifecycle moves to the user-agent (event-driven, no CRD).

### Session-Agent Template

| Kind | Name | Notes |
|---|---|---|
| ConfigMap | `{release}-session-agent-template` | Static pod template values (image, imagePullPolicy, terminationGracePeriodSeconds, resourcesJson, projectPvcSize, projectPvcStorageClass, nixPvcSize, nixPvcStorageClass, corporateCAEnabled, corporateCAConfigMapName, corporateCAConfigMapKey). Per ADR-0091, read by **`mclaude-user-agent`** (not the controller) to build per-project Deployments. |

### User-Agent Template (ADR-0091)

| Kind | Name | Notes |
|---|---|---|
| ConfigMap | `{release}-user-agent-template` | Pod template values for the per-user-namespace `user-agent` Deployment: `image`, `imagePullPolicy`, and `resourcesJson`. Read by `mclaude-controller-k8s` when reconciling a `grants.create` event. (The user-agent's ServiceAccount name is fixed as `mclaude-user-agent` in the user namespace; not templated.) |

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
| `userAgent.image.repository` | `ghcr.io/richardmcsong/mclaude-user-agent` | ADR-0091. User-agent container image. |
| `userAgent.image.tag` | `""` | Image tag (empty = chart appVersion). |
| `userAgent.resources` | `{}` | User-agent pod resource requests/limits. |
| `userAgent.templateConfigMap.name` | `""` | Override `{release}-user-agent-template` ConfigMap name (rendered into the chart by default). |

### Single-Cluster Degenerate Install

```bash
helm install mclaude-cp     charts/mclaude-cp     -n mclaude-system --create-namespace
helm install mclaude-worker charts/mclaude-worker -n mclaude-system \
  --set controlPlane.url=https://cp.mclaude.example \
  --set host.name="local" \
  --set host.hubNatsUrl=nats://mclaude-cp-nats.mclaude-system.svc:4222

kubectl logs job/mclaude-worker-gen-host-nkey -n mclaude-system
mclaude host register --name "local" --nkey-public <pubkey>
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
| `values.yaml` (postgres) | Defaults for the `mclaude-postgres` chart: `postgres:16-alpine`, RWO 20Gi PVC, no storageClass override, single replica. `existingSecret` is empty by default — operators MUST set it on every install. |
| `values-aks.yaml` (postgres) | AKS production: `persistence.storageClass: managed-csi-premium`. |
| `values-e2e.yaml` (postgres) | CI end-to-end tests: `persistence.enabled: false` (emptyDir). |
| `values.yaml` (cp) | Defaults for all hub knobs. Production-oriented images, persistence enabled, nginx ingress. MinIO enabled by default (`minio.enabled: true`, `minio.persistence.enabled: true`). `ingress.minioHost` is empty — set it in a cluster-specific overlay. `database.host`, `database.passwordSecretRef` are empty — operator MUST set on every install. |
| `values-dev.yaml` (cp) | Local k3d development. Images built locally (`pullPolicy: Never`), persistence disabled, no ingress. `minio.enabled: false` — no valid `S3_ENDPOINT` without an Ingress hostname, and presigned URLs would not be reachable. |
| `values-e2e.yaml` (cp) | CI end-to-end tests. Python HTTP stub control-plane and SPA, real NATS, persistence disabled. `minio.enabled: false` — stub control-plane does not read env vars or call S3. Postgres is provided by the separate `mclaude-postgres` release (its own `values-e2e.yaml`). |
| `values-k3d-ghcr.yaml` (cp) | Local k3d preview with ghcr.io images. Traefik ingress with TLS on `*.mclaude.richardmcsong.com`. Per ADR-0097, sets `tls.certManager.enabled: true` with `issuerRef.name: mclaude-letsencrypt-prod`, `commonName: "*.mclaude.richardmcsong.com"`, and `dnsNames` covering the wildcard + apex. Sets `ingress.minioHost: minio.mclaude.richardmcsong.com` (direct subdomain of the wildcard cert, covered by `*.mclaude.richardmcsong.com`). Overrides `minio.resources.requests.memory: 32Mi` (down from default 128Mi) so MinIO can schedule on the shared k3d node whose memory request budget is consumed by session-agent pods (ADR-0068). |
| `values-airgap.yaml` (cp) | Air-gapped deployments. All images pulled via `global.imageRegistry`. Overrides `minio.image.registry` and `minio.mcImage.registry` to `registry.internal.example.com` so both MinIO server and `minio/mc` bucket-Job images are pulled from the internal mirror. Per ADR-0097, leaves `tls.certManager.enabled` at the default `false` — air-gapped sites use a corporate CA-issued TLS Secret pre-created by the operator (referenced via `ingress.tls[0].secretName`). cert-manager + DNS-01 are not used (no public DNS path for ACME). |
| `values-aks.yaml` (cp) | Azure Kubernetes Service production. `managed-csi-premium`, larger PVCs, scaled replicas. Sets `minio.persistence.storageClass: managed-csi-premium` (consistent with NATS storage class override). Per ADR-0097, leaves `tls.certManager.enabled` at the default `false` — Azure Application Gateway terminates TLS upstream of the Ingress, and the operator pre-creates a TLS Secret out-of-band (referenced via `ingress.tls[0].secretName`). cert-manager is not used. |
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
- For each worker chart install: the operator must run `mclaude host register --name <name> --nkey-public <pubkey>` against the running hub control-plane to register the host before the controller can connect.
