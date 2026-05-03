# ADR: Fix Helm SSA conflicts from failed releases via --force flag

**Status**: implemented
**Status history**:
- 2026-05-03: accepted — Class A bug fix; Helm SSA ownership from failed releases blocks subsequent upgrades
- 2026-05-03: implemented — all scope CLEAN

## Overview

After a failed `helm upgrade --install`, the partial resources applied by the failed release retain SSA (Server-Side Apply) ownership under the `helm` field manager. A subsequent `helm upgrade --install` cannot take ownership of those fields — it fails with "conflict with 'helm' using apps/v1" on Deployment `.spec.template.spec.containers[name=...].image` and StatefulSet `.spec.volumeClaimTemplates`. Adding `--force` to the upgrade command makes Helm force-replace conflicting resources, resolving SSA ownership atomically.

## Motivation

After the `mclaude-system` namespace was deleted and recreated (due to the cert-manager Challenge finalizer incident), the cluster went through multiple failed deploy runs:

- Run 25281906831: `mclaude-cp-control-plane` and `mclaude-cp-postgres` StatefulSets had SSA conflicts — manually deleted and reran.
- Run 25282235756: SSA conflicts on `mclaude-cp-control-plane` Deployment and `mclaude-cp-spa` Deployment:

```
conflict occurred while applying object mclaude-system/mclaude-cp-control-plane apps/v1, Kind=Deployment:
Apply failed with 1 conflict: conflict with "helm" using apps/v1:
.spec.template.spec.containers[name="control-plane"].image
```

Each failed release leaves SSA ownership on whatever resources it managed to apply before failing, and subsequent Helm operations cannot override those claims without `--force`.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Fix | Add `--force` to both `helm upgrade --install` commands | `--force` makes Helm delete and recreate resources when SSA conflicts prevent in-place update; resolves ownership atomically without manual kubectl intervention |
| Scope | Both `mclaude-cp` and `mclaude-worker` upgrades | Both charts can encounter this after a failed release; applying consistently prevents asymmetric failures |
| Acceptable downtime | Yes — `--force` may restart pods | Dev cluster; brief pod restarts during upgrade are acceptable; this matches the behavior of a normal rolling upgrade anyway |

## Component Changes

### `.github/workflows/deploy-main.yml`

Add `--force` to both `helm upgrade --install` commands:

```yaml
# mclaude-cp (before):
helm upgrade --install "mclaude-cp" ./charts/mclaude-cp \
  ... \
  --wait --timeout 5m

# mclaude-cp (after):
helm upgrade --install "mclaude-cp" ./charts/mclaude-cp \
  ... \
  --force --wait --timeout 5m

# mclaude-worker (before):
helm upgrade --install "mclaude-worker" ./charts/mclaude-worker \
  ... \
  --wait --timeout 5m

# mclaude-worker (after):
helm upgrade --install "mclaude-worker" ./charts/mclaude-worker \
  ... \
  --force --wait --timeout 5m
```

## Error Handling

With `--force`, Helm replaces resources that cannot be updated in-place. If replacement fails (e.g., immutable field other than volumeClaimTemplates), Helm reports the error clearly instead of the opaque SSA conflict message.

## Security

No security impact.

## Impact

No spec updates — this is an operational CI fix.

Components implementing the change:
- `.github/workflows/deploy-main.yml`

## Scope

**v1:**
- Add `--force` to `helm upgrade --install mclaude-cp`
- Add `--force` to `helm upgrade --install mclaude-worker`

## Integration Test Cases

No dedicated integration test — the observable outcome is CI deploys succeed after failed releases without manual kubectl intervention.

## Implementation Plan

| Component | New/changed lines (est.) | Notes |
|-----------|--------------------------|-------|
| `.github/workflows/deploy-main.yml` | 2 | Add `--force` flag to each helm upgrade command |

**Total estimated tokens:** ~8k
