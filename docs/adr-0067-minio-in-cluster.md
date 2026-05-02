# ADR: MinIO In-Cluster Object Storage

**Status**: accepted
**Status history**:
- 2026-05-02: draft
- 2026-05-02: accepted — paired with docs/charts-mclaude/spec-helm.md

## Overview

Deploy MinIO as the S3-compatible object storage backend for the mclaude cluster. ADR-0053 designed the full import and attachment architecture around S3 and all code is implemented, but there is no backing store in any cluster deployment. This ADR wires MinIO into the Helm charts so that imports (`mclaude import`) and chat attachments work end-to-end.

## Motivation

ADR-0053 code is fully implemented in all components (control-plane `s3.go`, CLI `import.go`, session-agent `import.go`/`attachment.go`). When the control plane starts without S3 env vars it logs a warning and returns 503 for any attachment or import request. MinIO is the agreed-upon backend for self-hosted and dev deployments; this ADR adds the deployment manifest and wires the configuration.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| MinIO packaging | Inline in `mclaude-cp` chart (Deployment + Service + PVC in chart templates) | Consistent with how Postgres and NATS are packaged; avoids a separate chart release cycle |
| MinIO image | `minio/minio:RELEASE.2025-04-22T22-12-26Z` (stable MinIO release) | Official MinIO image; pinned tag for reproducibility |
| Bucket creation | Kubernetes Job (post-install hook) using `minio/mc` image | MinIO does not auto-create buckets; a one-shot `mc mb` Job runs after MinIO is ready |
| External URL | `https://minio.mclaude.richardmcsong.com` via a new `ingress.minioHost` values key (parallel to `ingress.natsHost`); wildcard cert `*.mclaude.richardmcsong.com` covers this | `ingress.host` is `dev.mclaude.richardmcsong.com` — prepending `minio.` would produce a two-level subdomain not covered by the wildcard cert; a separate `minioHost` value is required |
| TLS for MinIO | Terminate TLS at Ingress (Traefik), MinIO runs plain HTTP inside cluster | Consistent with how control-plane and SPA are exposed |
| S3_ENDPOINT value | `https://{{ ingress.minioHost }}` — sourced from the same `ingress.minioHost` values key | Matches the actual external hostname; CP admin requests hairpin through Traefik, acceptable for prod |
| Credentials | From Kubernetes Secret named `{{ include "mclaude-cp.fullname" . }}-minio` (keys: `access-key`, `secret-key`); overridable via `minio.existingSecret`; values file sets dev defaults | Consistent with postgres-password secret pattern; fullname helper keeps name consistent under nameOverride/fullnameOverride |
| Bucket name | `mclaude` | Single bucket, all data keyed by `{uslug}/{hslug}/{pslug}/...` prefix per ADR-0053 |
| Persistence | Enabled (PVC, same as NATS and Postgres) | Data must survive pod restarts |

## User Flow

No new user-facing flow — this ADR makes the existing import and attachment flows functional:

1. User runs `mclaude import` → CLI uploads archive to MinIO via presigned PUT URL
2. Session-agent downloads archive from MinIO via presigned GET URL
3. SPA uploads attachment → direct PUT to MinIO via presigned URL
4. Session-agent or SPA downloads attachment → direct GET from MinIO via presigned URL

All flows are defined in ADR-0053. This ADR adds only the infrastructure that makes them work.

## Component Changes

### charts/mclaude-cp

All MinIO templates except `minio-ingress.yaml` are gated by `{{- if .Values.minio.enabled }}`.

New templates:
- `templates/minio-deployment.yaml` — (gated by `minio.enabled`) MinIO Deployment (single replica). Image: `{{ include "mclaude-cp.image" (dict "imageValues" .Values.minio.image "global" .Values.global) }}`, `imagePullPolicy: {{ .Values.minio.image.pullPolicy }}`. Container command: `["minio", "server", "/data", "--console-address", ":9001"]`. When `minio.persistence.enabled`: PVC `{{ printf "%s-minio-data" (include "mclaude-cp.fullname" .) }}` mounted at `/data` via `volumes[].persistentVolumeClaim.claimName`. When persistence disabled: `emptyDir` volume mounted at `/data`. Credential env vars sourced from Secret `{{ coalesce .Values.minio.existingSecret (printf "%s-minio" (include "mclaude-cp.fullname" .)) }}`: `MINIO_ROOT_USER` from key `access-key`, `MINIO_ROOT_PASSWORD` from key `secret-key`. Resources: `{{- toYaml .Values.minio.resources | nindent 12 }}` (same pattern as all other Deployments). Pod security context: use the standard `{{- include "mclaude-cp.podSecurityContext" . | nindent 6 }}` helper (sets `runAsNonRoot: true`, `runAsUser: 1000`, `runAsGroup: 1000`, `fsGroup: 1000` — compatible with MinIO's UID 1000 process). Container security context: use the standard `{{- include "mclaude-cp.securityContext" . | nindent 10 }}` helper (same pattern as all other Deployments).
- `templates/minio-service.yaml` — (gated by `minio.enabled`) ClusterIP Service named `{{ include "mclaude-cp.fullname" . }}-minio` (port 9000 API, port 9001 console)
- `templates/minio-pvc.yaml` — (gated by `minio.enabled AND minio.persistence.enabled`) PersistentVolumeClaim named `{{ printf "%s-minio-data" (include "mclaude-cp.fullname" .) }}`. Spec follows the NATS/Postgres VolumeClaimTemplate pattern: `accessModes: ["ReadWriteOnce"]`; `resources.requests.storage: {{ .Values.minio.persistence.size }}`; `storageClassName` set only when `minio.persistence.storageClass` is non-empty (same conditional as existing templates: `{{- if .Values.minio.persistence.storageClass }}storageClassName: {{ .Values.minio.persistence.storageClass }}{{- end }}`).
- `templates/minio-secret.yaml` — (gated by `minio.enabled AND minio.existingSecret empty`) Secret named `{{ include "mclaude-cp.fullname" . }}-minio` with keys `access-key` and `secret-key`; populated from `minio.rootUser` / `minio.rootPassword` values
- `templates/minio-bucket-job.yaml` — (gated by `minio.enabled`) Job named `{{ include "mclaude-cp.fullname" . }}-minio-bucket`. Helm post-install/post-upgrade hook (`helm.sh/hook: post-install,post-upgrade`; `helm.sh/hook-delete-policy: before-hook-creation,hook-succeeded`). Pod spec: `restartPolicy: Never`, `activeDeadlineSeconds: 120`. Both containers use image `{{ include "mclaude-cp.image" (dict "imageValues" .Values.minio.mcImage "global" .Values.global) }}`, `imagePullPolicy: {{ .Values.minio.mcImage.pullPolicy }}`. Both mount:
  - `ACCESS_KEY` → `secretKeyRef: { name: {{ coalesce .Values.minio.existingSecret (printf "%s-minio" (include "mclaude-cp.fullname" .)) }}, key: access-key }`
  - `SECRET_KEY` → `secretKeyRef: { name: {{ coalesce .Values.minio.existingSecret (printf "%s-minio" (include "mclaude-cp.fullname" .)) }}, key: secret-key }`
  - `emptyDir` volume named `mc-config` at `/root/.mc` in both containers (init writes alias config; main container reads it)

  Init container `command`:
  ```
  ["sh", "-c", "mc alias set local http://{{ include \"mclaude-cp.fullname\" . }}-minio:9000 $ACCESS_KEY $SECRET_KEY && i=0; while [ $i -lt 60 ]; do mc ready local && break; i=$((i+1)); sleep 1; done"]
  ```
  Main container `command`:
  ```
  ["sh", "-c", "mc mb --ignore-existing local/{{ .Values.minio.bucket }}"]
  ```
  `backoffLimit: 3`. Omit both `mclaude-cp.podSecurityContext` and `mclaude-cp.securityContext` helpers (mc image may run as root; one-time admin operation).
- `templates/minio-ingress.yaml` — (gated by `and .Values.ingress.enabled .Values.ingress.minioHost` — same pattern as `nats-ws-ingress.yaml`) Ingress for MinIO API at `{{ .Values.ingress.minioHost }}`. Annotations: build using `mergeOverwrite` with base `ingress.annotations` overridden by MinIO-specific keys:
  ```
  {{- $base := deepCopy (.Values.ingress.annotations | default dict) }}
  {{- $overrides := dict "nginx.ingress.kubernetes.io/proxy-body-size" "0" }}
  {{- if .Values.ingress.externalDnsTarget }}
  {{- $_ := set $overrides "external-dns.alpha.kubernetes.io/hostname" .Values.ingress.minioHost }}
  {{- $_ := set $overrides "external-dns.alpha.kubernetes.io/target" .Values.ingress.externalDnsTarget }}
  {{- end }}
  {{- mergeOverwrite $base $overrides | toYaml | nindent 4 }}
  ```
  `deepCopy` is required — `mergeOverwrite` mutates the destination map in-place; without it, `.Values.ingress.annotations` is permanently modified for all templates that render alphabetically after `minio-ingress.yaml` (e.g., `nats-ws-ingress.yaml`). `deepCopy` produces an independent copy so only the local variable is mutated. This preserves Traefik-specific annotations from `ingress.annotations`, forces `proxy-body-size` to `"0"` (unlimited), and sets ExternalDNS annotations to the MinIO hostname and target.

  TLS: follows the `nats-ws-ingress.yaml` pattern — iterates `ingress.tls`, reuses each entry's `secretName`, and appends `minioHost` to the `hosts` list for each entry:
  ```
  {{- if .Values.ingress.tls }}
  tls:
    {{- range .Values.ingress.tls }}
    - secretName: {{ .secretName }}
      hosts:
        {{- range .hosts }}
        - {{ . | quote }}
        {{- end }}
        - {{ $.Values.ingress.minioHost | quote }}
    {{- end }}
  {{- end }}
  ```
  This ensures the wildcard TLS secret's `hosts` list includes `minio.mclaude.richardmcsong.com` so Traefik SNI routing works correctly.

  `spec.rules` (follows `nats-ws-ingress.yaml` pattern exactly):
  ```
  rules:
    - host: {{ .Values.ingress.minioHost | quote }}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: {{ include "mclaude-cp.fullname" . }}-minio
                port:
                  number: 9000
  ```

New values section:
```yaml
minio:
  enabled: true
  image:
    registry: docker.io
    repository: minio/minio
    tag: "RELEASE.2025-04-22T22-12-26Z"
    pullPolicy: IfNotPresent
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      cpu: 500m
      memory: 512Mi
  persistence:
    enabled: true
    storageClass: ""
    size: 20Gi
  ## Bucket to create on first install
  bucket: mclaude
  ## Existing Secret with keys "access-key" and "secret-key".
  ## If unset, chart creates the secret using minio.rootUser / minio.rootPassword.
  existingSecret: ""
  rootUser: "minioadmin"
  rootPassword: "minioadmin"
  ## Image for the minio/mc bucket-creation Job (separate from minio server image).
  ## Configurable so air-gapped deployments can mirror from an internal registry.
  mcImage:
    registry: docker.io
    repository: minio/mc
    tag: "RELEASE.2025-04-16T18-25-19Z"
    pullPolicy: IfNotPresent
```

New ingress value (added to the existing `ingress:` block in `values.yaml`):
```yaml
ingress:
  ## Hostname for MinIO S3 API. When set, creates a MinIO Ingress and wires
  ## S3_ENDPOINT on the control-plane Deployment. Must be a direct subdomain of
  ## the wildcard TLS cert (e.g. minio.mclaude.richardmcsong.com).
  minioHost: ""
```

### charts/mclaude-cp values-aks.yaml

```yaml
minio:
  persistence:
    storageClass: managed-csi-premium
```

MinIO enabled with AKS managed storage class, consistent with the NATS and Postgres `managed-csi-premium` overrides already in `values-aks.yaml`. Future ADR may replace MinIO with Azure Blob Storage; for now MinIO provides a consistent S3 interface across all deployment targets.

### charts/mclaude-cp values-airgap.yaml

Add image overrides so air-gapped deployments can mirror MinIO and mc from an internal registry:

```yaml
minio:
  image:
    registry: registry.internal.example.com
    repository: minio/minio
    tag: "RELEASE.2025-04-22T22-12-26Z"
  mcImage:
    registry: registry.internal.example.com
    repository: minio/mc
    tag: "RELEASE.2025-04-16T18-25-19Z"
```

(`registry.internal.example.com` matches the convention used by all other image entries in `values-airgap.yaml`.)

### charts/mclaude-cp values-k3d-ghcr.yaml

One new override required — set `ingress.minioHost` to the actual MinIO hostname:

```yaml
ingress:
  minioHost: "minio.mclaude.richardmcsong.com"
```

`minio.enabled: true` is the chart default; persistence is enabled by default. The MinIO Ingress is created when `ingress.minioHost` is non-empty.

### charts/mclaude-cp values-dev.yaml

```yaml
minio:
  enabled: false
```

MinIO disabled: `values-dev.yaml` has no Ingress and no `controlPlane.externalUrl`, so no valid `S3_ENDPOINT` can be derived and presigned URLs would not work. Avoids wasted resources and misleading bucket-Job runs.

### charts/mclaude-cp values-e2e.yaml

```yaml
minio:
  enabled: false
```

MinIO disabled: the e2e environment uses a Python stub control-plane that does not read env vars or call S3. MinIO would deploy but serve no purpose.

### control-plane Helm template

The control-plane Deployment template gains new env vars when `minio.enabled: true` and `ingress.minioHost` is non-empty:
- `S3_ENDPOINT` → `https://{{ .Values.ingress.minioHost }}` (external URL; presigned URLs are client-facing)
- `S3_BUCKET` → `{{ .Values.minio.bucket }}`
- `S3_ACCESS_KEY_ID` → from Secret `{{ coalesce .Values.minio.existingSecret (printf "%s-minio" (include "mclaude-cp.fullname" .)) }}` key `access-key`
- `S3_SECRET_ACCESS_KEY` → from Secret `{{ coalesce .Values.minio.existingSecret (printf "%s-minio" (include "mclaude-cp.fullname" .)) }}` key `secret-key`
- `S3_REGION` → not set (omitted from template); `loadS3Config()` defaults to `"us-east-1"` which is correct for MinIO path-style signing

### .agent/skills/deploy-local-preview/SKILL.md

Update: no new manual steps needed since MinIO deploys as part of the Helm chart. Add a troubleshooting entry for MinIO-related issues.

## Data Model

No new data model. MinIO stores blobs at:
```
mclaude/{uslug}/{hslug}/{pslug}/imports/{import-id}.tar.gz
mclaude/{uslug}/{hslug}/{pslug}/attachments/{attachment-id}
```
Key structure per ADR-0053.

## Error Handling

| Error | Component | Behavior |
|-------|-----------|----------|
| MinIO pod not ready | Control-plane | S3 presign calls succeed (URL is pre-signed, not live), but client uploads/downloads fail at MinIO | 
| MinIO bucket missing | Control-plane | Upload fails with 404 from MinIO; bucket-creation Job is idempotent, re-run it | 
| MinIO ingress unreachable (external) | CLI/SPA | Presigned URL request fails; verify `S3_ENDPOINT` matches the external Ingress hostname |
| MinIO PVC full | MinIO pod | Pod CrashLoops or returns 500; expand PVC or delete old objects |

## Security

- MinIO runs inside the cluster with a ClusterIP Service — not directly accessible from outside
- External access only via Ingress (TLS terminated); all presigned URLs use HTTPS
- Credentials stored in Kubernetes Secret, mounted as env vars (consistent with postgres/NATS pattern)
- Bucket is private — all object access requires presigned URLs signed by control-plane (ADR-0053)
- MinIO console (port 9001) is **not** exposed via Ingress by default

## Impact

Specs updated in this commit:
- `docs/charts-mclaude/spec-helm.md` — MinIO section in mclaude-cp chart values, Ingress rule

## Scope

**v1 (this ADR):**
- MinIO Deployment, Service, PVC, Secret, bucket-creation Job in mclaude-cp chart
- Ingress rule for MinIO API at `{{ ingress.minioHost }}` when `ingress.minioHost` is non-empty
- Wire `S3_*` env vars into control-plane Deployment when `minio.enabled: true` and `ingress.minioHost` is non-empty
- Update deploy-local-preview skill (troubleshooting entry for MinIO-related issues)

**Deferred:**
- Multi-replica MinIO (distributed mode) — production HA, out of scope for self-hosted v1
- MinIO lifecycle policies (auto-expiry) — use periodic cleanup Job instead
- MinIO versioning / object lock
- AWS S3 as alternative backend (credentials pattern already supports it; just set `S3_ENDPOINT` to AWS)
- MinIO console Ingress (expose the web UI)

## Open questions

(none remaining)

## Implementation Plan

| Component | New/changed lines (est.) | Notes |
|-----------|--------------------------|-------|
| charts/mclaude-cp templates (MinIO) | ~200 | Deployment, Service, PVC, Secret, bucket Job, Ingress rule |
| charts/mclaude-cp values files | ~50 | minio section in values.yaml; overrides in values-k3d-ghcr.yaml, values-dev.yaml, values-e2e.yaml, values-airgap.yaml |
| charts/mclaude-cp control-plane template | ~30 | Add S3_* env vars to CP Deployment when minio.enabled |
| docs/charts-mclaude/spec-helm.md | ~50 | MinIO section |
| .agent/skills/deploy-local-preview/SKILL.md | ~20 | Troubleshooting entry |

**Total estimated tokens:** ~200k (single dev-harness invocation on charts component)
**Estimated wall-clock:** ~1h
