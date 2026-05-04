## Run: 2026-05-02T00:00:00Z

ADR: docs/adr-0067-minio-in-cluster.md
Spec: docs/charts-mclaude/spec-helm.md

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 19 | "MinIO packaging — Inline in `mclaude-cp` chart (Deployment + Service + PVC in chart templates)" | spec-helm.md §MinIO (line 74) | REFLECTED | — | Spec: "All resources gated by `minio.enabled` (default `true`)" under mclaude-cp |
| 19 | All MinIO templates gated by `minio.enabled` | spec-helm.md line 74 | REFLECTED | — | Spec states this explicitly |
| 20 | "MinIO image `minio/minio:RELEASE.2025-04-22T22-12-26Z`" | spec-helm.md line 74, config table line 136 | REFLECTED | — | Both the section prose and config table reflect the pinned image tag |
| 21 | "Bucket creation — Kubernetes Job (post-install hook) using `minio/mc` image" | spec-helm.md line 82 | REFLECTED | — | Job entry in MinIO table describes the post-install/post-upgrade hook and mc image |
| 22 | "External URL `https://minio.mclaude.richardmcsong.com` via `ingress.minioHost`; wildcard cert covers this" | spec-helm.md line 129 | REFLECTED | — | Config table describes minioHost with same rationale about wildcard cert requirement |
| 23 | "TLS terminated at Ingress (Traefik), MinIO runs plain HTTP inside cluster" | spec-helm.md line 84 | REFLECTED | — | Security section: "External access is TLS-terminated at Traefik Ingress" |
| 24 | "S3_ENDPOINT → `https://{{ ingress.minioHost }}`" sourced from `ingress.minioHost` | spec-helm.md line 58 | REFLECTED | — | Control-Plane Deployment notes include `S3_ENDPOINT=https://{ingress.minioHost}` |
| 25 | Credentials from Secret `{release}-minio`, keys `access-key`/`secret-key`; overridable via `minio.existingSecret` | spec-helm.md lines 81, 143–144 | REFLECTED | — | Secret row and config table both reflect this |
| 26 | "Bucket name `mclaude`" | spec-helm.md line 142 | REFLECTED | — | Config table: `minio.bucket` default `mclaude` |
| 27 | "Persistence enabled (PVC, same as NATS and Postgres)" | spec-helm.md lines 80, 139 | REFLECTED | — | PVC row and config table reflect persistence |
| 47 | Pod security context for MinIO Deployment: uses standard helper `mclaude-cp.podSecurityContext` (sets `runAsNonRoot: true`, `runAsUser: 1000`, `runAsGroup: 1000`, `fsGroup: 1000`) | spec-helm.md line 78 | PARTIAL | SPEC→FIX | Spec only mentions `podSecurityContext.fsGroup: 1000`. The helper also sets `runAsNonRoot: true`, `runAsUser: 1000`, `runAsGroup: 1000` — a developer implementing from spec alone would not know to apply those values |
| 47 | Container security context for MinIO Deployment: uses standard `mclaude-cp.securityContext` helper | spec-helm.md line 78 | GAP | SPEC→FIX | Spec does not mention container security context for the MinIO Deployment at all. A developer would not know to apply the standard container securityContext |
| 48 | Service `{release}-minio`: ClusterIP, port 9000 (S3 API) and port 9001 (console) | spec-helm.md line 79 | REFLECTED | — | Service row matches |
| 49 | PVC `{release}-minio-data`: `accessModes: [ReadWriteOnce]`, size from `minio.persistence.size`, storageClass conditional | spec-helm.md line 80 | REFLECTED | — | PVC row reflects all three fields |
| 50 | Secret `{release}-minio`: keys `access-key`/`secret-key` from `minio.rootUser`/`minio.rootPassword`; created only when `minio.existingSecret` unset | spec-helm.md line 81 | REFLECTED | — | Secret row matches |
| 51–64 | Bucket Job: both containers share `emptyDir` volume `mc-config` at `/root/.mc` (init writes alias config; main container reads it) | spec-helm.md line 82 | GAP | SPEC→FIX | Spec describes init container and main container behavior but omits the shared `emptyDir` volume named `mc-config` mounted at `/root/.mc`. Without this, the alias config written by the init container would not be visible to the main container |
| 64 | "Omit both `mclaude-cp.podSecurityContext` and `mclaude-cp.securityContext` helpers (mc image may run as root; one-time admin operation)" | spec-helm.md line 82 | GAP | SPEC→FIX | Spec does not state that the bucket Job omits both security context helpers. A developer implementing from spec would apply the standard helpers and potentially break the mc container which may need to run as root |
| 65–106 | minio-ingress.yaml: gated by `and .Values.ingress.enabled .Values.ingress.minioHost` | spec-helm.md line 93 | REFLECTED | — | Ingress table: "Created when `ingress.enabled` AND `ingress.minioHost` non-empty" |
| 67–75 | `deepCopy`+`mergeOverwrite` for annotations; `nginx.ingress.kubernetes.io/proxy-body-size: "0"` override; ExternalDNS annotations for minioHost | spec-helm.md line 93 | REFLECTED | — | Ingress row captures deepCopy+mergeOverwrite, proxy-body-size "0", and ExternalDNS annotations |
| 77–91 | TLS: iterates `ingress.tls`, appends `minioHost` to each entry's `hosts` list | spec-helm.md line 93 | REFLECTED | — | Ingress row: "TLS: iterates `ingress.tls`, appends `minioHost` to each entry's `hosts` list" |
| 212–217 | Control-plane: `S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY` env vars; `S3_REGION` intentionally omitted | spec-helm.md line 58 | REFLECTED | — | Control-Plane Deployment note covers all four vars and explicitly notes S3_REGION is omitted |
| 153–160 | `values-aks.yaml`: `minio.persistence.storageClass: managed-csi-premium` | spec-helm.md line 275 | REFLECTED | — | Values table entry for values-aks.yaml describes this override |
| 163–177 | `values-airgap.yaml`: image registry overrides for minio and mcImage | spec-helm.md line 274 | REFLECTED | — | Values table entry covers both registry overrides |
| 183–190 | `values-k3d-ghcr.yaml`: `ingress.minioHost: minio.mclaude.richardmcsong.com` | spec-helm.md line 273 | REFLECTED | — | Values table entry for values-k3d-ghcr.yaml matches |
| 193–199 | `values-dev.yaml`: `minio.enabled: false`; rationale: no valid S3_ENDPOINT without Ingress | spec-helm.md line 271 | REFLECTED | — | Values table entry matches including rationale |
| 201–208 | `values-e2e.yaml`: `minio.enabled: false`; rationale: stub CP doesn't read env vars/S3 | spec-helm.md line 272 | REFLECTED | — | Values table entry matches including rationale |
| 243 | "MinIO runs inside the cluster with a ClusterIP Service — not directly accessible from outside" | spec-helm.md line 84 | REFLECTED | — | Security paragraph |
| 244 | "External access only via Ingress (TLS terminated)" | spec-helm.md line 84 | REFLECTED | — | Security paragraph |
| 245 | "Credentials stored in Kubernetes Secret, mounted as env vars" | spec-helm.md line 84 | REFLECTED | — | Security paragraph (implied by Secret + env var pattern) |
| 246 | "Bucket is private — all object access requires presigned URLs signed by control-plane" | spec-helm.md line 84 | REFLECTED | — | Security paragraph: "All object access by clients requires pre-signed URLs signed by the control-plane" |
| 247 | "MinIO console (port 9001) is not exposed via Ingress by default" | spec-helm.md lines 79, 84 | REFLECTED | — | Service row and Security paragraph both state this |

### Summary

- Reflected: 26
- Gap: 3
- Partial: 1
