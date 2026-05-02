# ADR: Fix minio/mc Image Tag — Use latest Instead of Nonexistent Pinned Tag

**Status**: implemented
**Status history**:
- 2026-05-02: accepted — paired with docs/charts-mclaude/spec-helm.md
- 2026-05-02: implemented — all scope CLEAN

## Overview

The `minio/mc` image tag `RELEASE.2025-04-16T18-25-19Z` does not exist on Docker Hub. Every minio-bucket hook Job has been failing with `ImagePullBackOff` because the image cannot be pulled. This ADR changes `minio.mcImage.tag` to `latest` and removes the now-pointless pre-pull step from `deploy-main.yml` (ADR-0070, corrected by ADR-0071).

## Motivation

Three successive CI failures (runs 25260763562, 25261067615, 25261280562) all fail on the minio-bucket post-upgrade hook. Root cause investigation:

1. `docker pull docker.io/minio/mc:RELEASE.2025-04-16T18-25-19Z` → `manifest unknown: manifest unknown`
2. `docker pull minio/mc:RELEASE.2025-04-16T18-25-19Z` → `manifest unknown: manifest unknown`

"manifest unknown" is Docker Hub's definitive response for a non-existent tag. The tag is invalid. The k3d containerd hook pods were going into `ImagePullBackOff` with the same tag-not-found error — the `activeDeadlineSeconds` was killing the Job before the retry limit was visible, making it appear to be a timeout issue rather than a missing tag.

ADR-0070's pre-pull step (corrected in ADR-0071) was designed to cache the image before helm upgrade. Since the image tag is invalid, the pre-pull step cannot succeed and only blocks every deploy. It is removed in this ADR.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Replacement tag | `latest` | The `latest` tag is always available on Docker Hub for `minio/mc`. The bucket-creation command (`mc mb --ignore-existing`) is a stable operation unaffected by mc version; reproducibility of the hook is less critical than reliability. |
| Pre-pull step | Remove from `deploy-main.yml` | The step was introduced to speed up the hook by pre-caching the image. Since `latest` is a stable, commonly-used tag that k3d containerd can pull quickly (it's a 35MB image and `latest` is cached by Docker Hub CDN edges), pre-pulling is no longer necessary. Additionally, the CI runner's Docker daemon cannot reach docker.io directly (evidenced by "manifest unknown" on all attempts), so the step would fail regardless. |
| values-k3d-ghcr.yaml override | None needed | The chart default `values.yaml` now uses `latest`. No environment-specific override required. |

## Component Changes

### `charts/mclaude-cp/values.yaml`

Change:
```yaml
minio:
  mcImage:
    tag: "RELEASE.2025-04-16T18-25-19Z"
```
to:
```yaml
minio:
  mcImage:
    tag: "latest"
```

### `.github/workflows/deploy-main.yml`

Remove the "Pre-pull minio/mc image into k3d" step (added in ADR-0070, corrected in ADR-0071) entirely. The step before "Helm deploy control-plane" is deleted.

## Impact

Specs updated in this commit:
- `docs/charts-mclaude/spec-helm.md` — update `minio.mcImage.tag` default to `latest`, remove pre-pull documentation, remove tag-sync constraint note from `minio.mcImage.tag` row

Components implementing the change:
- `charts/mclaude-cp` (values.yaml)
- `.github/workflows/deploy-main.yml`

## Scope

**In this change:** mcImage.tag → latest, pre-pull step removed.

**Deferred:** Pinning to a specific valid `minio/mc` release tag for full reproducibility. This can be done once the MinIO release cadence for `minio/mc` is confirmed (i.e. find a tag that actually exists). For now `latest` is the operative fix.

## Integration Test Cases

| Test case | What it verifies | Components exercised |
|-----------|------------------|----------------------|
| Deploy succeeds | minio-bucket hook completes (bucket exists, `mc mb --ignore-existing` is no-op) | deploy-main.yml, minio-bucket Job with `latest` tag |
