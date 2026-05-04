## Run: 2026-05-02T00:00:00Z

Component: charts/mclaude-cp (MinIO resources)
Spec files: docs/adr-0067-minio-in-cluster.md, docs/charts-mclaude/spec-helm.md

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0067:45 | All MinIO templates except minio-ingress.yaml are gated by `{{- if .Values.minio.enabled }}` | minio-deployment.yaml:1, minio-service.yaml:1, minio-pvc.yaml:1 (AND gate), minio-secret.yaml:1 (AND gate), minio-bucket-job.yaml:1 | IMPLEMENTED | — | All non-ingress templates have the gate; pvc and secret have additional gates as specified |
| adr-0067:48 | Image: `{{ include "mclaude-cp.image" (dict "imageValues" .Values.minio.image "global" .Values.global) }}`, `imagePullPolicy: {{ .Values.minio.image.pullPolicy }}` | minio-deployment.yaml:26-27 | IMPLEMENTED | — | Exact helper call and pullPolicy field match |
| adr-0067:48 | Container command: `["minio", "server", "/data", "--console-address", ":9001"]` | minio-deployment.yaml:29 | IMPLEMENTED | — | Exact match |
| adr-0067:48 | When persistence enabled: PVC `{{ printf "%s-minio-data" (include "mclaude-cp.fullname" .) }}` mounted at `/data`; when disabled: `emptyDir` | minio-deployment.yaml:54-60 | IMPLEMENTED | — | Both branches present |
| adr-0067:48 | Credential env vars `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` from Secret `{{ coalesce .Values.minio.existingSecret (printf "%s-minio" ...) }}`, keys `access-key` / `secret-key` | minio-deployment.yaml:38-47 | IMPLEMENTED | — | Exact match |
| adr-0067:48 | Resources: `{{- toYaml .Values.minio.resources \| nindent 12 }}` | minio-deployment.yaml:51-52 | IMPLEMENTED | — | Exact match |
| adr-0067:48 | Pod security context: `{{- include "mclaude-cp.podSecurityContext" . \| nindent 6 }}` | minio-deployment.yaml:22 | IMPLEMENTED | — | Helper applied at pod level |
| adr-0067:48 | Container security context: `{{- include "mclaude-cp.securityContext" . \| nindent 10 }}` | minio-deployment.yaml:28 | IMPLEMENTED | — | Helper applied at container level |
| adr-0067:49 | ClusterIP Service named `{{ include "mclaude-cp.fullname" . }}-minio`, port 9000 API, port 9001 console | minio-service.yaml:1-24 | IMPLEMENTED | — | Both ports present, ClusterIP type set |
| adr-0067:50 | PVC named `{{ printf "%s-minio-data" (include "mclaude-cp.fullname" .) }}`, `accessModes: ["ReadWriteOnce"]`, `resources.requests.storage: {{ .Values.minio.persistence.size }}`, storageClass conditional | minio-pvc.yaml:1-19 | IMPLEMENTED | — | Exact match including conditional storageClass |
| adr-0067:50 | Gated by `minio.enabled AND minio.persistence.enabled` | minio-pvc.yaml:1 | IMPLEMENTED | — | `{{- if and .Values.minio.enabled .Values.minio.persistence.enabled }}` |
| adr-0067:51 | Secret named `{{ include "mclaude-cp.fullname" . }}-minio`, keys `access-key` and `secret-key`, populated from `minio.rootUser` / `minio.rootPassword`; gated by `minio.enabled AND minio.existingSecret empty` | minio-secret.yaml:1-14 | IMPLEMENTED | — | Exact match; uses `b64enc` for base64 encoding |
| adr-0067:52-65 | Bucket Job: name `{release}-minio-bucket`, post-install/post-upgrade hook, delete-policy `before-hook-creation,hook-succeeded`, `restartPolicy: Never`, `activeDeadlineSeconds: 120`, `backoffLimit: 3` | minio-bucket-job.yaml:5,11-15 | IMPLEMENTED | — | All hook annotations and spec fields match |
| adr-0067:52-65 | Both containers use `minio/mc` image via helper, `imagePullPolicy` from `minio.mcImage.pullPolicy` | minio-bucket-job.yaml:26-27, 48-49 | IMPLEMENTED | — | Init and main container both use mcImage helper |
| adr-0067:52-65 | Both containers mount `ACCESS_KEY` / `SECRET_KEY` from `coalesce existingSecret ...`-minio secret | minio-bucket-job.yaml:33-42, 55-64 | IMPLEMENTED | — | Both init and main container have the env vars |
| adr-0067:55 | `emptyDir` volume named `mc-config` at `/root/.mc` in both containers | minio-bucket-job.yaml:43-45, 65-67, 69-70 | IMPLEMENTED | — | Volume defined once; both containers mount it |
| adr-0067:57-59 | Init container command: `mc alias set local http://{release}-minio:9000 $ACCESS_KEY $SECRET_KEY && i=0; while ...` | minio-bucket-job.yaml:31 | IMPLEMENTED | — | Exact shell string via `printf` template call |
| adr-0067:61-63 | Main container command: `mc mb --ignore-existing local/{minio.bucket}` | minio-bucket-job.yaml:53 | IMPLEMENTED | — | Exact match |
| adr-0067:65 | Omit both podSecurityContext and securityContext helpers in bucket Job | minio-bucket-job.yaml:1-71 | IMPLEMENTED | — | Neither helper is called in the Job pod spec |
| adr-0067:66-76 | minio-ingress.yaml gated by `and .Values.ingress.enabled .Values.ingress.minioHost` | minio-ingress.yaml:1 | IMPLEMENTED | — | Exact gate condition |
| adr-0067:66-76 | Annotations built via `deepCopy` + `mergeOverwrite` with base `ingress.annotations`, override `proxy-body-size: "0"`, ExternalDNS annotations conditional on `externalDnsTarget` | minio-ingress.yaml:11-17 | IMPLEMENTED | — | `deepCopy` present; all three annotation blocks correct |
| adr-0067:78-91 | TLS: iterates `ingress.tls`, reuses `secretName`, appends `minioHost` to `hosts` list for each entry | minio-ingress.yaml:22-31 | IMPLEMENTED | — | Exact range/append pattern |
| adr-0067:94-107 | spec.rules: `host: minioHost`, path `/`, pathType `Prefix`, backend `{release}-minio:9000` | minio-ingress.yaml:33-43 | IMPLEMENTED | — | Exact match |
| adr-0067:109-143 | values.yaml `minio:` block: all fields with correct defaults (enabled: true, image tag, resources, persistence, bucket, existingSecret, rootUser/rootPassword, mcImage) | values.yaml:164-195 | IMPLEMENTED | — | All fields and defaults match spec exactly |
| adr-0067:145-152 | `ingress.minioHost: ""` added to ingress block in values.yaml | values.yaml:216 | IMPLEMENTED | — | Present with correct empty default and comment |
| adr-0067:154-161 | values-aks.yaml: `minio.persistence.storageClass: managed-csi-premium` | values-aks.yaml:43-45 | IMPLEMENTED | — | Exact match |
| adr-0067:164-178 | values-airgap.yaml: `minio.image.registry` and `minio.mcImage.registry` both set to `registry.internal.example.com` | values-airgap.yaml:33-41 | IMPLEMENTED | — | Both image blocks present with correct registry |
| adr-0067:184-190 | values-k3d-ghcr.yaml: `ingress.minioHost: "minio.mclaude.richardmcsong.com"` | values-k3d-ghcr.yaml:50 | IMPLEMENTED | — | Exact value present |
| adr-0067:193-200 | values-dev.yaml: `minio.enabled: false` | values-dev.yaml:45-46 | IMPLEMENTED | — | Exact match |
| adr-0067:202-209 | values-e2e.yaml: `minio.enabled: false` | values-e2e.yaml:74-75 | IMPLEMENTED | — | Exact match |
| adr-0067:213-218 | control-plane Deployment: S3 env vars block gated by `minio.enabled AND ingress.minioHost`; `S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY` wired from correct sources | control-plane-deployment.yaml:119-134 | IMPLEMENTED | — | All four env vars present with correct sources |
| adr-0067:218 | `S3_REGION` intentionally omitted | control-plane-deployment.yaml:119-134 | IMPLEMENTED | — | No S3_REGION env var in template |
| spec-helm:74 | MinIO image `minio/minio:RELEASE.2025-04-22T22-12-26Z` via `mclaude-cp.image` helper | minio-deployment.yaml:26, values.yaml:168-169 | IMPLEMENTED | — | Image values and helper call match |
| spec-helm:78 | Deployment pod security context uses `mclaude-cp.podSecurityContext` helper; container security context uses `mclaude-cp.securityContext` helper | minio-deployment.yaml:22, 28 | IMPLEMENTED | — | Both helpers present |
| spec-helm:82 | Bucket Job: both containers intentionally omit `mclaude-cp.podSecurityContext` and `mclaude-cp.securityContext` | minio-bucket-job.yaml:22-70 | IMPLEMENTED | — | Neither helper appears in Job pod spec |
| spec-helm:82 | Bucket Job: `emptyDir` volume `mc-config` at `/root/.mc` shared by init and main container | minio-bucket-job.yaml:43-45, 65-67, 69-70 | IMPLEMENTED | — | Volume and mounts present in both containers |
| spec-helm:93 | Ingress `{release}-minio`: `deepCopy` + `mergeOverwrite`, `proxy-body-size: "0"`, ExternalDNS annotations, TLS iterates `ingress.tls` appending `minioHost` | minio-ingress.yaml:11-31 | IMPLEMENTED | — | Full annotation and TLS logic correct |
| spec-helm:58 | Control-plane: when `minio.enabled=true` AND `ingress.minioHost` non-empty: S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY; S3_REGION intentionally omitted | control-plane-deployment.yaml:119-134 | IMPLEMENTED | — | Matches spec exactly |
| adr-0067:220-222 | .agent/skills/deploy-local-preview/SKILL.md updated with MinIO troubleshooting entries | .agent/skills/deploy-local-preview/SKILL.md:392-394 | IMPLEMENTED | — | Three MinIO rows added to troubleshooting table |


### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| minio-deployment.yaml:6-8 | INFRA | Namespace and common labels — standard boilerplate present on all templates |
| minio-deployment.yaml:23 | INFRA | `imagePullSecrets` helper — infrastructure present on all other Deployment templates; consistent with NATS/Postgres/SPA |
| minio-service.yaml:6-9 | INFRA | Namespace and labels — standard boilerplate |
| minio-pvc.yaml:6-9 | INFRA | Namespace and labels — standard boilerplate |
| minio-secret.yaml:6-9 | INFRA | Namespace and labels — standard boilerplate |
| minio-bucket-job.yaml:6-10 | INFRA | Namespace, labels, component label — standard boilerplate |
| minio-bucket-job.yaml:17-21 | INFRA | Pod labels using selectorLabels helper — standard Job pod template boilerplate |
| minio-bucket-job.yaml:23 | INFRA | `imagePullSecrets` helper — infrastructure; needed for air-gapped/ghcr deployments |
| minio-ingress.yaml:5-9 | INFRA | Namespace and labels — standard boilerplate |
| minio-ingress.yaml:19-21 | INFRA | `ingressClassName` conditional — consistent with all other Ingress templates in the chart; standard infrastructure |
| control-plane-deployment.yaml:119-134 | INFRA | S3 env vars section — covered in Phase 1; no uncovered lines |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0067:48 / spec-helm:78 | MinIO Deployment template renders correctly | none | none | UNTESTED |
| adr-0067:49 / spec-helm:79 | MinIO Service template renders correctly | none | none | UNTESTED |
| adr-0067:50 / spec-helm:80 | MinIO PVC template renders correctly | none | none | UNTESTED |
| adr-0067:51 / spec-helm:81 | MinIO Secret template renders correctly | none | none | UNTESTED |
| adr-0067:52-65 / spec-helm:82 | Bucket Job template renders correctly | none | none | UNTESTED |
| adr-0067:66-107 / spec-helm:93 | MinIO Ingress template renders correctly | none | none | UNTESTED |
| adr-0067:213-218 / spec-helm:58 | S3 env vars in control-plane Deployment when minio.enabled+minioHost set | none | none | UNTESTED |
| adr-0067:45 | minio.enabled gate suppresses all MinIO resources | none | none | UNTESTED |

Note: Helm chart templates have no unit tests (no `helm unittest` test suite) and no e2e tests that specifically exercise the MinIO resource rendering. This is consistent with the rest of the chart — no other templates have Helm unit tests either.

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No open bugs reference charts/mclaude-cp or MinIO |

### Summary

- Implemented: 37
- Gap: 0
- Partial: 0
- Infra: 11
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 0
- Untested: 8
- Bugs fixed: 0
- Bugs open: 0
