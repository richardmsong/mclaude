# ADR: CI/CD Migration to Split Helm Charts

**Status**: implemented
**Status history**:
- 2026-04-26: draft
- 2026-04-26: accepted
- 2026-04-26: implemented

## Overview

Migrate the CI/CD workflows (`deploy-main.yml`, `deploy-preview.yml`, `ci.yml`) from the monolithic `charts/mclaude` Helm chart to the split `charts/mclaude-cp` + `charts/mclaude-worker` charts introduced by ADR-0035. Also add a Docker build job for the new `mclaude-controller-k8s` binary and CI test jobs for new components. Delete the old `charts/mclaude/` directory.

## Motivation

ADR-0035 split the Helm chart into `mclaude-cp` (control-plane + hub NATS + Postgres + SPA) and `mclaude-worker` (worker NATS + controller-k8s + session-agent template). The deploy workflows still reference `./charts/mclaude`, causing deploy failures — the old chart's pre-upgrade hooks conflict with the new schema (the `mclaude-slug-backfill-21` Job failed, breaking the current main deploy).

Additionally, the reconciler was extracted from `mclaude-control-plane` into the new `mclaude-controller-k8s` binary (ADR-0035 Stage 5), but there is no Docker build job or Helm deployment for it. Without the controller-k8s deployed, MCProject CRs are not reconciled — sessions cannot be created.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Deployment topology for dev | Single-cluster: deploy both `mclaude-cp` and `mclaude-worker` into `mclaude-system` namespace | Per ADR-0035: "Single-cluster deployments install both into the same cluster with the leaf-node config pointing at localhost." |
| Old chart | Delete `charts/mclaude/` entirely | Superseded; keeping it causes confusion. Pre-upgrade hooks are stale. |
| Helm release naming | `mclaude-cp` and `mclaude-worker` as separate releases | Two releases allows independent upgrades. Single-cluster install has both in `mclaude-system`. |
| Controller-k8s image build | Inline job in `deploy-main.yml` | Matches the existing pattern (control-plane, spa, session-agent are all inline jobs in deploy-main.yml). |
| Worker chart leafUrl (dev) | `nats://mclaude-cp-nats.mclaude-system.svc:7422` (K8s service DNS) | Standard K8s networking; works across pods without hostNetwork. More portable. |
| Preview deploys | Update `deploy-preview.yml` to use split charts | Same topology as main, with branch-specific release names (`mclaude-cp-preview-{slug}`, `mclaude-worker-preview-{slug}`). |
| CI test jobs | Add `test-cli` and `test-controller-k8s` jobs to `ci.yml` | New Go modules that need CI coverage. `controller-local` is tested locally for now (no K8s fixture needed). |
| Migration strategy | Manual `helm uninstall mclaude -n mclaude-system` before first deploy | Clean break per ADR-0035. Simpler than auto-detection in the workflow. One-time operation. |

## Component Changes

### `.github/workflows/deploy-main.yml`

1. Add `build-controller-k8s` job (inline, same pattern as build-control-plane):
   - Checkout, setup-go, `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o controller-k8s .` in `mclaude-controller-k8s/`
   - Docker buildx + push to `ghcr.io/richardmsong/mclaude-controller-k8s:{tag}`
2. Replace single `helm upgrade --install "mclaude" ./charts/mclaude` with two Helm steps:
   - `helm upgrade --install "mclaude-cp" ./charts/mclaude-cp -f ./charts/mclaude-cp/values-k3d-ghcr.yaml --set controlPlane.image.tag="${IMAGE_TAG}" --set spa.image.tag="${IMAGE_TAG}" --set controlPlane.devOAuthToken="${DEV_OAUTH_TOKEN}" --namespace mclaude-system --wait --timeout 5m`
   - `helm upgrade --install "mclaude-worker" ./charts/mclaude-worker -f ./charts/mclaude-worker/values-dev.yaml --set controller.image.tag="${IMAGE_TAG}" --set sessionAgent.image.tag="${IMAGE_TAG}" --namespace mclaude-system --wait --timeout 5m`
3. Deploy job `needs` adds `build-controller-k8s`.

### `.github/workflows/deploy-preview.yml`

1. Add `build-controller-k8s` job (conditional on `mclaude-controller-k8s/**` changes).
2. Replace single Helm install with two:
   - `helm upgrade --install "mclaude-cp-preview-${BRANCH_SLUG}" ./charts/mclaude-cp ...`
   - `helm upgrade --install "mclaude-worker-preview-${BRANCH_SLUG}" ./charts/mclaude-worker ...`
3. `cleanup-preview.yml` updated to uninstall both releases.

### `.github/workflows/ci.yml`

Add two jobs:
- `test-cli`: runs `go test -race ./...` in `mclaude-cli/`, triggered on `mclaude-cli/**` changes.
- `test-controller-k8s`: runs `go test -race ./...` in `mclaude-controller-k8s/`, triggered on `mclaude-controller-k8s/**` changes.

### `charts/mclaude/` (deletion)

Delete the entire directory. All references now point to `charts/mclaude-cp` and `charts/mclaude-worker`.

### `mclaude-controller-k8s/Dockerfile` (new)

New Dockerfile for the controller-k8s binary. Follows the same pattern as `mclaude-control-plane/Dockerfile` (multi-stage: Go build + scratch/distroless runtime).

### `charts/mclaude-worker/values-k3d-ghcr.yaml` (new)

Dev values for the worker chart in the k3d single-cluster setup:
- `clusterSlug: local`
- `jsDomain: local`
- `leafUrl: nats://mclaude-cp-nats.mclaude-system.svc:7422`
- `controller.image.registry: ghcr.io`
- `controller.image.repository: richardmsong/mclaude-controller-k8s`

## Error Handling

| Failure | Handling |
|---------|----------|
| Old "mclaude" release still installed | Helm will create new releases alongside it; resources may conflict. Operator must run `helm uninstall mclaude -n mclaude-system` first. |
| Controller-k8s build fails | Deploy job depends on it; deploy is skipped. Same pattern as other images. |
| Worker chart deploy fails but CP succeeds | Control-plane is up but no reconciler; sessions can't be created. SPA will show "host unreachable" per ADR-0035 error handling. |

## Security

No new secrets. Controller-k8s image uses the same GHCR auth as other images. The worker chart's leaf credentials come from the existing `mclaude-system/operator-keys` Secret (bootstrapped by the CP chart's init-keys Job).

## Impact

**No specs updated** — this is a CI/infra change, not a behavior change. ADR-0035's spec-helm.md already describes the split chart architecture.

**Components affected:** `.github/workflows/` (3 files), `charts/mclaude/` (deletion), `mclaude-controller-k8s/Dockerfile` (new), `charts/mclaude-worker/values-k3d-ghcr.yaml` (new).

## Scope

**In scope:**
- deploy-main.yml: controller-k8s build job + split Helm deploys
- deploy-preview.yml: same
- cleanup-preview.yml: uninstall both releases
- ci.yml: test-cli + test-controller-k8s jobs
- Delete charts/mclaude/
- mclaude-controller-k8s/Dockerfile
- charts/mclaude-worker/values-k3d-ghcr.yaml

**Out of scope:**
- controller-local Docker image / deployment (BYOH runs locally, not in K8s)
- Multi-cluster worker deployment (future — when a second cluster is registered)
- Automated migration from old release (manual uninstall is sufficient)

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| deploy-main.yml | ~40 | 30k | Add build job + split helm steps |
| deploy-preview.yml | ~50 | 30k | Same pattern + tag resolution for 4 images |
| cleanup-preview.yml | ~5 | 10k | Uninstall both releases |
| ci.yml | ~30 | 20k | Two new test jobs |
| charts/mclaude/ deletion | -500 | 10k | rm -rf |
| mclaude-controller-k8s/Dockerfile | ~20 | 15k | Copy existing pattern |
| charts/mclaude-worker/values-k3d-ghcr.yaml | ~20 | 10k | Dev values |

**Total estimated tokens:** ~125k
**Estimated wall-clock:** <1h
