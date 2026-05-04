## Run: 2026-05-02T00:00:00Z

ADR: `docs/adr-0067-minio-in-cluster.md`
Primary spec: `docs/charts-mclaude/spec-helm.md`

| # | ADR (section) | ADR text | Spec location | Verdict | Direction | Notes |
|---|---------------|----------|---------------|---------|-----------|-------|
| 1 | Decisions table | "MinIO packaging — Inline in `mclaude-cp` chart...gated by `minio.enabled`" | spec-helm.md lines 73-83 | REFLECTED | — | MinIO section present with all resources, gated by `minio.enabled` |
| 2 | Decisions table | "MinIO image — `minio/minio:RELEASE.2025-04-22T22-12-26Z`" | spec-helm.md line 74 | REFLECTED | — | Image tag and helper documented |
| 3 | Decisions table | "Bucket creation — Kubernetes Job (post-install hook) using `minio/mc` image" | spec-helm.md line 82 | REFLECTED | — | Full Job spec present |
| 4 | Decisions table | "External URL — `https://minio.mclaude.richardmcsong.com` via new `ingress.minioHost` values key" | spec-helm.md lines 129, 273 | REFLECTED | — | Key documented with example value in values-k3d-ghcr.yaml row |
| 5 | Decisions table | "`ingress.minioHost` separate key required — `minio.` subdomain not covered by wildcard cert" | spec-helm.md line 129 | REFLECTED | — | "Must be a direct subdomain of the wildcard TLS cert, not a subdomain of `ingress.host`" |
| 6 | Decisions table | "TLS terminate at Ingress (Traefik), MinIO runs plain HTTP inside cluster" | spec-helm.md line 84 | REFLECTED | — | "External access is TLS-terminated at Traefik Ingress" |
| 7 | Decisions table | "`S3_ENDPOINT = https://{{ ingress.minioHost }}`" | spec-helm.md line 58 | REFLECTED | — | Exact env var documented in Control-Plane table |
| 8 | Decisions table | "Credentials from Secret `{release}-minio` (keys: `access-key`, `secret-key`); overridable via `minio.existingSecret`" | spec-helm.md lines 81, 143-144 | REFLECTED | — | Secret and existingSecret knob both documented |
| 9 | Decisions table | "Bucket name `mclaude`; all data keyed by `{uslug}/{hslug}/{pslug}/...`" | spec-helm.md line 142 | REFLECTED | — | Full key structure documented |
| 10 | Decisions table | "Persistence enabled (PVC, same as NATS and Postgres)" | spec-helm.md line 80, 139 | REFLECTED | — | PVC resource and persistence knobs documented |
| 11 | Component Changes | "Image: `{{ include "mclaude-cp.image" ... }}`, `imagePullPolicy: {{ .Values.minio.image.pullPolicy }}`" | spec-helm.md line 78 | REFLECTED | — | Image via helper, respects global.imageRegistry |
| 12 | Component Changes | "Container command: `[\"minio\", \"server\", \"/data\", \"--console-address\", \":9001\"]`" | spec-helm.md line 78 | REFLECTED | — | "Command: `minio server /data --console-address :9001`" |
| 13 | Component Changes | "PVC mounted at `/data`; `emptyDir` when persistence disabled" | spec-helm.md line 78 | REFLECTED | — | Both cases documented |
| 14 | Component Changes | "`MINIO_ROOT_USER`/`MINIO_ROOT_PASSWORD` from Secret `{release}-minio`" | spec-helm.md line 78 | REFLECTED | — | Env vars and secret name documented |
| 15 | Component Changes | "Pod security context: standard `mclaude-cp.podSecurityContext` helper (`runAsNonRoot: true`, `runAsUser: 1000`, `runAsGroup: 1000`, `fsGroup: 1000`)" | spec-helm.md line 78 | REFLECTED | — | Full security context documented including UID 1000 rationale |
| 16 | Component Changes | "Container security context: standard `mclaude-cp.securityContext` helper" | spec-helm.md line 78 | REFLECTED | — | Documented |
| 17 | Component Changes | "ClusterIP Service named `{release}-minio` (port 9000 API, port 9001 console)" | spec-helm.md line 79 | REFLECTED | — | "ClusterIP. Port 9000 (S3 API), port 9001 (console). Not exposed via Ingress by default." |
| 18 | Component Changes | "PVC named `{release}-minio-data`, `accessModes: [ReadWriteOnce]`, `storageClass` conditional" | spec-helm.md line 80 | REFLECTED | — | Full PVC spec documented |
| 19 | Component Changes | "Secret named `{release}-minio`; created only when `minio.existingSecret` empty" | spec-helm.md line 81 | REFLECTED | — | Documented |
| 20 | Component Changes | "Job post-install/post-upgrade hook, `hook-delete-policy: before-hook-creation,hook-succeeded`, `restartPolicy: Never`, `activeDeadlineSeconds: 120`, `backoffLimit: 3`" | spec-helm.md line 82 | REFLECTED | — | All values documented |
| 21 | Component Changes | "Both containers mount `emptyDir` volume `mc-config` at `/root/.mc`" | spec-helm.md line 82 | REFLECTED | — | "Both containers share an `emptyDir` volume named `mc-config` mounted at `/root/.mc`" |
| 22 | Component Changes | "Init container: `mc alias set local ... $ACCESS_KEY $SECRET_KEY && ... mc ready local` (max 60s)" | spec-helm.md line 82 | REFLECTED | — | Init container command documented |
| 23 | Component Changes | "Main container: `mc mb --ignore-existing local/{minio.bucket}`" | spec-helm.md line 82 | REFLECTED | — | Documented |
| 24 | Component Changes | "Omit both security context helpers for bucket Job (mc image may run as root)" | spec-helm.md line 82 | REFLECTED | — | "Both security context helpers...are intentionally omitted — the `minio/mc` image may run as root" |
| 25 | Component Changes | "minio-ingress.yaml gated by `and .Values.ingress.enabled .Values.ingress.minioHost`" | spec-helm.md line 93 | REFLECTED | — | "Created when `ingress.enabled` AND `ingress.minioHost` non-empty" |
| 26 | Component Changes | "Annotations: `deepCopy`+`mergeOverwrite`; `nginx.ingress.kubernetes.io/proxy-body-size: \"0\"` override" | spec-helm.md line 93 | REFLECTED | — | Mechanism and proxy-body-size override documented |
| 27 | Component Changes | "ExternalDNS annotations set to `ingress.minioHost`/`ingress.externalDnsTarget`" | spec-helm.md line 93 | REFLECTED | — | Documented |
| 28 | Component Changes | "TLS: iterates `ingress.tls`, appends `minioHost` to each entry's `hosts` list" | spec-helm.md line 93 | REFLECTED | — | "TLS: iterates `ingress.tls`, appends `minioHost` to each entry's `hosts` list so Traefik SNI routing works" |
| 29 | Component Changes | "Ingress backend → `{release}-minio:9000`" | spec-helm.md line 93 | REFLECTED | — | Documented in Ingress table |
| 30 | Component Changes | "CP Deployment gains S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY when `minio.enabled=true` and `ingress.minioHost` non-empty" | spec-helm.md line 58 | REFLECTED | — | All four env vars and condition documented |
| 31 | Component Changes | "`S3_REGION` intentionally omitted; `loadS3Config()` defaults to `us-east-1`" | spec-helm.md line 58 | REFLECTED | — | "`S3_REGION` is intentionally omitted — `loadS3Config()` defaults to `us-east-1` which is correct for MinIO path-style signing" |
| 32 | Component Changes | "values.yaml minio section defaults (enabled: true, image tag pinned, resources, persistence 20Gi, bucket mclaude, rootUser/rootPassword minioadmin, mcImage tag pinned)" | spec-helm.md lines 133-149 | REFLECTED | — | All configuration knobs documented with defaults |
| 33 | Component Changes | "`ingress.minioHost` default `\"\"` in values.yaml" | spec-helm.md line 129 | REFLECTED | — | Documented in Configuration knobs table |
| 34 | Component Changes | "values-aks.yaml: `minio.persistence.storageClass: managed-csi-premium`" | spec-helm.md line 275 | REFLECTED | — | Documented in Values files table |
| 35 | Component Changes | "values-airgap.yaml: `minio.image.registry` and `minio.mcImage.registry` to `registry.internal.example.com`" | spec-helm.md line 274 | REFLECTED | — | Documented in Values files table |
| 36 | Component Changes | "values-k3d-ghcr.yaml: `ingress.minioHost: minio.mclaude.richardmcsong.com`" | spec-helm.md line 273 | REFLECTED | — | Documented in Values files table |
| 37 | Component Changes | "values-dev.yaml: `minio.enabled: false` — no valid S3_ENDPOINT without Ingress hostname" | spec-helm.md lines 271-272 | REFLECTED | — | Documented with rationale |
| 38 | Component Changes | "values-e2e.yaml: `minio.enabled: false` — stub CP does not read env vars or call S3" | spec-helm.md lines 272-273 | REFLECTED | — | Documented with rationale |
| 39 | Security | "MinIO console (port 9001) not exposed via Ingress by default" | spec-helm.md lines 79, 84 | REFLECTED | — | Documented in Service row and Security note |
| 40 | Security | "All object access requires presigned URLs signed by control-plane" | spec-helm.md line 84 | REFLECTED | — | "All object access by clients requires pre-signed URLs signed by the control-plane — no direct bucket access" |
| 41 | Overview | "MinIO as S3-compatible object storage for `mclaude-cp` chart" | spec-helm.md line 7 | REFLECTED | — | Chart purpose table includes "MinIO object storage" |

### Summary

- Reflected: 41
- Gap: 0
- Partial: 0
