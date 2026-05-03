# ADR: Fix Helm 4 SSA conflict recovery — uninstall failed release before upgrade

**Status**: implemented
**Status history**:
- 2026-05-03: accepted — Class A bug fix; supersedes ADR-0083 which used --force (invalid with Helm 4 + SSA)
- 2026-05-03: implemented — all scope CLEAN

## Overview

Helm v4 uses Server-Side Apply (SSA) by default and the `--force` flag is incompatible with SSA mode (`invalid operation: cannot use server-side apply and force replace together`). ADR-0083's `--force` fix must be reverted. The correct Helm 4 approach: before each `helm upgrade --install`, if the release is in "failed" state, run `helm uninstall` to clear all SSA-owned resources and then proceed with a fresh install.

## Motivation

ADR-0083 added `--force` to the helm upgrade commands. CI run 25282334256 failed immediately:

```
Error: UPGRADE FAILED: invalid operation: cannot use server-side apply and force replace together
```

The CI runner has Helm v4.0.0. Helm 4 uses SSA by default; `--force` triggers client-side force-replace which conflicts with the SSA mode. The flag must be removed.

The underlying problem (SSA ownership conflicts from failed releases) remains. The correct fix for Helm 4 + SSA: if a release is in "failed" state, `helm uninstall` it first to clear all SSA ownership, then `helm upgrade --install` performs a clean first install with no pre-existing SSA claims.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Remove `--force` | Yes — incompatible with Helm 4 SSA | Helm 4 error is unambiguous; the flag cannot be used |
| Recovery strategy | `helm uninstall` on failed state before upgrade | Clears all SSA-owned resources atomically; subsequent install has no ownership conflicts |
| State detection | `helm status <release> -n mclaude-system 2>/dev/null \| grep -q "STATUS: failed"` | Simple, idempotent; no-ops when release is healthy or absent |
| PVC safety | StatefulSet PVCs survive `helm uninstall` | StatefulSet PVCs are created by the StatefulSet controller, not Helm; `helm uninstall` does not delete them |
| Secret safety | kubectl-managed secrets survive | `mclaude-postgres` and `mclaude-control-plane` are created by `kubectl apply`, not Helm; they are not in the release manifest |
| MinIO PVC | May be deleted on uninstall | Helm-managed PVC; data loss acceptable for dev cluster recovery scenario |
| Apply to both charts | `mclaude-cp` and `mclaude-worker` | Both can enter failed state after namespace disruptions |

## Component Changes

### `.github/workflows/deploy-main.yml`

**Revert ADR-0083 `--force` flag** and add "Cleanup failed release" steps:

```yaml
# Before "Helm deploy control-plane" step, add:
- name: Cleanup failed control-plane release
  run: |
    if helm status mclaude-cp -n mclaude-system 2>/dev/null | grep -q "STATUS: failed"; then
      echo "Release mclaude-cp is in failed state — uninstalling to clear SSA ownership"
      helm uninstall mclaude-cp -n mclaude-system
    fi

# Helm deploy control-plane (remove --force):
helm upgrade --install "mclaude-cp" ./charts/mclaude-cp \
  ... \
  --wait --timeout 5m    # no --force

# Before "Helm deploy worker" step, add:
- name: Cleanup failed worker release
  run: |
    if helm status mclaude-worker -n mclaude-system 2>/dev/null | grep -q "STATUS: failed"; then
      echo "Release mclaude-worker is in failed state — uninstalling to clear SSA ownership"
      helm uninstall mclaude-worker -n mclaude-system
    fi

# Helm deploy worker (remove --force):
helm upgrade --install "mclaude-worker" ./charts/mclaude-worker \
  ... \
  --wait --timeout 5m    # no --force
```

## Error Handling

If `helm uninstall` fails (e.g., release is already partially deleted), the step fails loudly before the upgrade runs. The operator can investigate manually. If the release is not in "failed" state, the step is a no-op.

## Security

No security impact. No secrets are deleted by this approach.

## Impact

No spec updates — this is an operational CI fix. Supersedes ADR-0083.

Components implementing the change:
- `.github/workflows/deploy-main.yml`

## Scope

**v1:**
- Remove `--force` from both `helm upgrade --install` commands
- Add "Cleanup failed control-plane release" step before "Helm deploy control-plane"
- Add "Cleanup failed worker release" step before "Helm deploy worker"

## Integration Test Cases

No dedicated integration test — observable outcome is CI deploys succeed after namespace disruptions without manual kubectl intervention.

## Implementation Plan

| Component | New/changed lines (est.) | Notes |
|-----------|--------------------------|-------|
| `.github/workflows/deploy-main.yml` | ~14 | Remove 2 `--force` flags; add 2 cleanup steps (~6 lines each) |

**Total estimated tokens:** ~10k
