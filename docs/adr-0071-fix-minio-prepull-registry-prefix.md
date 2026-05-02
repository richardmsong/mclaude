# ADR: Fix minio/mc Pre-Pull — Drop docker.io/ Registry Prefix

**Status**: accepted
**Status history**:
- 2026-05-02: accepted — paired with docs/charts-mclaude/spec-helm.md

## Overview

ADR-0070's deploy-main.yml pre-pull step used `docker.io/minio/mc:RELEASE.2025-04-16T18-25-19Z` as the image reference for `docker pull`. The self-hosted CI runner's Docker daemon returns "manifest unknown" when the `docker.io/` registry prefix is specified explicitly. The fix is to use the short form `minio/mc:RELEASE.2025-04-16T18-25-19Z` for both the `docker pull` and `k3d image import` commands.

## Motivation

After ADR-0070 shipped (commit 1b82cd3), the first CI run using the pre-pull step failed immediately:

```
Error response from daemon: manifest for minio/mc:RELEASE.2025-04-16T18-25-19Z not found: manifest unknown: manifest unknown
```

The Docker daemon on the self-hosted runner cannot resolve `docker.io/minio/mc:...` with the explicit registry prefix — it strips the prefix internally but then fails the manifest lookup. The same image tag IS pullable by k3d's containerd runtime (previous runs showed the hook starting an image pull, just too slowly). Using `minio/mc:RELEASE.2025-04-16T18-25-19Z` without the registry prefix works.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| docker pull reference format | `minio/mc:RELEASE.2025-04-16T18-25-19Z` (no registry prefix) | Docker daemon defaults to docker.io; omitting the prefix avoids the manifest resolution bug on the self-hosted runner. |
| k3d import reference format | Same short form `minio/mc:RELEASE.2025-04-16T18-25-19Z` | k3d image import uses the local Docker image name, then normalises to `docker.io/minio/mc:...` in containerd. Pod using `imagePullPolicy: IfNotPresent` finds it by the normalised name. |
| Env var name | Unchanged (`MINIO_MC_IMAGE`) | Only the value changes. |

## Component Changes

### `.github/workflows/deploy-main.yml`

Change the `MINIO_MC_IMAGE` env var in the "Pre-pull minio/mc image into k3d" step from:
```yaml
MINIO_MC_IMAGE: docker.io/minio/mc:RELEASE.2025-04-16T18-25-19Z
```
to:
```yaml
MINIO_MC_IMAGE: minio/mc:RELEASE.2025-04-16T18-25-19Z
```

The `docker pull "${MINIO_MC_IMAGE}"` and `k3d image import "${MINIO_MC_IMAGE}" -c "${K3D_CLUSTER}"` commands remain unchanged.

## Impact

Specs updated in this commit:
- `docs/charts-mclaude/spec-helm.md` — minio-bucket Job note updated to document short form (no `docker.io/` prefix) for docker pull

Components implementing the change:
- `.github/workflows/deploy-main.yml`

## Scope

Single-line env var value change. No chart changes. No values.yaml changes.

## Integration Test Cases

| Test case | What it verifies | Components exercised |
|-----------|------------------|----------------------|
| Deploy succeeds with cold k3d node | `docker pull minio/mc:...` succeeds; k3d import succeeds; minio-bucket hook completes within 300s | deploy-main.yml pre-pull step, minio-bucket Job |
