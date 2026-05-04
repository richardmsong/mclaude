## Run: 2026-05-02T00:00:00Z

### ADR: docs/adr-0067-minio-in-cluster.md
### Spec: docs/charts-mclaude/spec-helm.md

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 19 | "MinIO packaging — Inline in `mclaude-cp` chart (Deployment + Service + PVC in chart templates)" | spec-helm.md: mclaude-cp section has no MinIO subsection | GAP | SPEC→FIX | Spec describes NATS, Postgres, Control-Plane, SPA, Ingress under mclaude-cp but says nothing about MinIO Deployment, Service, or PVC |
| 19 | "Consistent with how Postgres and NATS are packaged; avoids a separate chart release cycle" | spec-helm.md | GAP | SPEC→FIX | No MinIO entry in any table or narrative under mclaude-cp |
| 20 | "MinIO image — `minio/minio:RELEASE.2025-04-22T22-12-26Z`" | spec-helm.md | GAP | SPEC→FIX | Image tag not mentioned anywhere in spec |
| 21 | "Bucket creation — Kubernetes Job (post-install hook) using `minio/mc` image" | spec-helm.md | GAP | SPEC→FIX | No bucket-creation Job described in spec |
| 22 | "External URL — `https://minio.mclaude.richardmcsong.com` via a new `ingress.minioHost` values key (parallel to `ingress.natsHost`)" | spec-helm.md line 113: ingress.natsHost listed; no ingress.minioHost | GAP | SPEC→FIX | Configuration knobs table in spec lists `ingress.natsHost` but has no `ingress.minioHost` key |
| 22 | "wildcard cert `*.mclaude.richardmcsong.com` covers this" | spec-helm.md | GAP | SPEC→FIX | Ingress table in spec has no MinIO Ingress row |
| 23 | "TLS for MinIO — Terminate TLS at Ingress (Traefik), MinIO runs plain HTTP inside cluster" | spec-helm.md | GAP | SPEC→FIX | No MinIO TLS policy described |
| 24 | "S3_ENDPOINT value — `https://{{ ingress.minioHost }}` — sourced from the same `ingress.minioHost` values key" | spec-helm.md: Control-Plane section does not list S3_* env vars | GAP | SPEC→FIX | Control-plane env vars listed in spec don't include S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY |
| 25 | "Credentials — From Kubernetes Secret named `{{ include "mclaude-cp.fullname" . }}-minio` (keys: `access-key`, `secret-key`); overridable via `minio.existingSecret`" | spec-helm.md: Pre-requisite Secrets section lists postgres and admin-token secrets only | GAP | SPEC→FIX | MinIO secret not listed as a chart-created secret or in configuration knobs |
| 26 | "Bucket name — `mclaude`" | spec-helm.md | GAP | SPEC→FIX | Bucket name not mentioned anywhere in spec |
| 27 | "Persistence — Enabled (PVC, same as NATS and Postgres)" | spec-helm.md | GAP | SPEC→FIX | No MinIO PVC described in spec |
| 44–106 | All new templates: minio-deployment.yaml, minio-service.yaml, minio-pvc.yaml, minio-secret.yaml, minio-bucket-job.yaml, minio-ingress.yaml (all gated by minio.enabled) | spec-helm.md: mclaude-cp Deployment section | GAP | SPEC→FIX | None of these six templates appear anywhere in the spec's mclaude-cp resource tables |
| 108–142 | New values section: `minio.enabled`, `minio.image.*`, `minio.resources`, `minio.persistence.{enabled,storageClass,size}`, `minio.bucket`, `minio.existingSecret`, `minio.rootUser`, `minio.rootPassword`, `minio.mcImage.*` | spec-helm.md Configuration knobs table (lines 89–116) | GAP | SPEC→FIX | None of the minio.* configuration keys appear in the Configuration knobs table |
| 144–151 | New ingress value: `ingress.minioHost` — "When set, creates a MinIO Ingress and wires S3_ENDPOINT on the control-plane Deployment" | spec-helm.md line 113: ingress table has natsHost, leafHost; no minioHost | GAP | SPEC→FIX | `ingress.minioHost` key absent from Configuration knobs table and from Ingress resource table |
| 153–159 | values-aks.yaml: `minio.persistence.storageClass: managed-csi-premium` | spec-helm.md Values files table (lines 234–246) — aks row exists but no mention of MinIO | PARTIAL | SPEC→FIX | values-aks.yaml row exists in Values files table but doesn't describe MinIO-specific storageClass override |
| 163–178 | values-airgap.yaml: minio image overrides for air-gapped deployments | spec-helm.md Values files table — airgap row exists but no mention of MinIO | PARTIAL | SPEC→FIX | values-airgap.yaml row exists but MinIO image override not described |
| 182–190 | values-k3d-ghcr.yaml: `ingress.minioHost: "minio.mclaude.richardmcsong.com"` | spec-helm.md Values files table — values-k3d-ghcr.yaml row exists but no MinIO detail | PARTIAL | SPEC→FIX | values-k3d-ghcr.yaml row exists but minioHost override not described |
| 194–199 | values-dev.yaml: `minio.enabled: false` with rationale (no Ingress, no externalUrl, presigned URLs would not work) | spec-helm.md Values files table — values-dev.yaml row exists but no mention of MinIO disabled | PARTIAL | SPEC→FIX | values-dev.yaml row exists but MinIO disabled decision not described |
| 201–208 | values-e2e.yaml: `minio.enabled: false` with rationale (Python stub CP doesn't read env vars) | spec-helm.md Values files table — values-e2e.yaml row exists but no mention of MinIO disabled | PARTIAL | SPEC→FIX | values-e2e.yaml row exists but MinIO disabled decision not described |
| 212–218 | control-plane Deployment template gains S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY when minio.enabled and ingress.minioHost non-empty; S3_REGION omitted (defaults to us-east-1) | spec-helm.md: Control-Plane table and Configuration knobs — no S3_* env vars | GAP | SPEC→FIX | S3 env vars wired conditionally into CP Deployment entirely absent from spec |
| 243–247 | Security: MinIO console (port 9001) is not exposed via Ingress by default; ClusterIP only; presigned URLs use HTTPS; credentials in K8s Secret | spec-helm.md | GAP | SPEC→FIX | No security properties for MinIO described in spec |

### Summary

- Reflected: 0
- Gap: 15
- Partial: 5
