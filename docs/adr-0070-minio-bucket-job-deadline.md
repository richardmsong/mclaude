# ADR: MinIO Bucket Job Image Pre-Pull and Deadline Extension

**Status**: implemented
**Status history**:
- 2026-05-02: accepted — paired with docs/charts-mclaude/spec-helm.md
- 2026-05-02: implemented — all scope CLEAN

## Overview

The minio-bucket post-install/post-upgrade hook Job fails in k3d because the `minio/mc` image is not cached in the k3d containerd runtime and takes >120s to pull over the local network, exceeding the hardcoded `activeDeadlineSeconds: 120`. This ADR makes the deadline configurable (default 300s) and adds a pre-pull + k3d import step to the deploy workflow so the image is always cached before the hook runs.

## Motivation

After ADR-0067 shipped MinIO in-cluster, the minio-bucket Job has `activeDeadlineSeconds: 120` hardcoded. The CI runner is self-hosted on the same host as the k3d cluster. k3d nodes use their own containerd runtime (isolated from the host Docker daemon). On a cold k3d node, `docker.io/minio/mc:RELEASE.2025-04-16T18-25-19Z` must be pulled directly by containerd from docker.io, which takes >120s and causes the hook to fail with `context deadline exceeded`.

The bucket (`mclaude`) is idempotent (`mc mb --ignore-existing`) — once created, subsequent runs are no-ops. The problem is not the bucket creation itself but the image pull timing out before the job even starts.

The fix has two parts:
1. Increase the deadline so there is headroom for image pulls on cold nodes.
2. Pre-pull and import the image into k3d in the deploy workflow so subsequent runs never need to pull.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Deadline default | 300s | 5× the original 120s; enough for a cold pull of the ~25MB minio/mc image even on a slow connection. Production environments with pre-cached images see no difference. |
| Deadline configurability | New `minio.bucketJobDeadlineSeconds` value (default 300) | Allows air-gapped or production overrides without patching the template. Follows the pattern of other configurable Job timeouts in the chart. |
| Pre-pull mechanism | `docker pull` + `k3d image import` in `deploy-main.yml` before helm upgrade | The self-hosted runner shares the host Docker daemon. Pulling to Docker then importing to k3d is the standard k3d pattern. After import, `imagePullPolicy: IfNotPresent` means the job never pulls again. |
| k3d cluster name | `mclaude-dev` | Confirmed at the time of this ADR. Hardcoded in the deploy step since this workflow targets the dev cluster only. |
| Pre-pull placement | New step before "Helm deploy control-plane" in `deploy-main.yml` | The bucket job runs as a post-upgrade hook of `mclaude-cp`. The image must be in k3d before helm upgrade kicks off the hook. |

## Component Changes

### `charts/mclaude-cp/values.yaml`

Add under `minio`:
```yaml
bucketJobDeadlineSeconds: 300
```

### `charts/mclaude-cp/templates/minio-bucket-job.yaml`

Change line:
```yaml
  activeDeadlineSeconds: 120
```
to:
```yaml
  activeDeadlineSeconds: {{ .Values.minio.bucketJobDeadlineSeconds }}
```

### `.github/workflows/deploy-main.yml`

Add new step in the `deploy` job before "Helm deploy control-plane":

```yaml
- name: Pre-pull minio/mc image into k3d
  env:
    MINIO_MC_IMAGE: docker.io/minio/mc:RELEASE.2025-04-16T18-25-19Z
    K3D_CLUSTER: mclaude-dev
  run: |
    docker pull "${MINIO_MC_IMAGE}"
    k3d image import "${MINIO_MC_IMAGE}" -c "${K3D_CLUSTER}"
```

The image tag is kept in sync with `minio.mcImage.tag` in `values.yaml`. When upgrading the minio/mc image tag, update this step to match.

## Data Model

No data model changes.

## Error Handling

| Failure | Behavior |
|---------|----------|
| `docker pull` fails (network issue) | Step fails, deploy aborts before helm upgrade runs. Hook never executes. |
| `k3d image import` fails | Step fails, deploy aborts before helm upgrade. Hook would still pull from registry — but with 300s deadline it has more headroom. |
| Hook Job still exceeds 300s | Job fails with `context deadline exceeded`. Check k3d node resources and network connectivity. |

## Security

No new secrets. The `minio/mc` image is pulled from the official docker.io registry by the self-hosted runner.

## Impact

Specs updated in this commit:
- `docs/charts-mclaude/spec-helm.md` — add `minio.bucketJobDeadlineSeconds` knob, update minio-bucket Job description

Components implementing the change:
- `charts/mclaude-cp` (values.yaml, templates/minio-bucket-job.yaml)
- `.github/workflows/deploy-main.yml`

## Scope

**In this change:**
- `minio.bucketJobDeadlineSeconds` value (default 300)
- Template updated to use the value
- Pre-pull step in `deploy-main.yml`

**Deferred:**
- Automatic sync between `minio.mcImage.tag` and the pre-pull step in `deploy-main.yml` (would require a templating mechanism in the workflow). For now, the tag must be updated in both places when bumping the mc image.

## Integration Test Cases

| Test case | What it verifies | Components exercised |
|-----------|------------------|----------------------|
| Deploy succeeds with cold k3d node | minio-bucket hook completes within 300s deadline | deploy-main.yml pre-pull step, minio-bucket Job |
| `mc mb --ignore-existing` on existing bucket | No error, hook exits 0 | minio-bucket Job |
