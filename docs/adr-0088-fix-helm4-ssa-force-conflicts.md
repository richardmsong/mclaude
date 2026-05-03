# ADR: Add --force-conflicts to helm upgrade for Helm 4 SSA normal-upgrade conflicts

**Status**: implemented
**Status history**:
- 2026-05-03: accepted — Class C spec gap; ADR-0084 covered failed-state SSA recovery but not normal-upgrade SSA conflicts
- 2026-05-03: implemented — all scope CLEAN

## Overview

ADR-0084 fixed SSA ownership conflicts for *failed* Helm releases by uninstalling before upgrade. However, normal (deployed-state) releases also fail with Helm 4 SSA conflicts: `conflict with "helm" using v1: .data.nats.conf`. The fix: add `--force-conflicts` to both `helm upgrade --install` commands so SSA takes ownership of any conflicting fields from previous applies.

## Motivation

CI run 25284014434 failed at "Helm deploy control-plane" despite the release being in "deployed" state (cleanup step was a no-op):

```
Error: UPGRADE FAILED: conflict occurred while applying object
mclaude-system/mclaude-cp-nats-config /v1, Kind=ConfigMap:
Apply failed with 1 conflict: conflict with "helm" using v1: .data.nats.conf
```

Multiple other resources had the same conflict pattern (Deployments, StatefulSets). The release was healthy; ADR-0084's conditional `helm uninstall` did not trigger. Helm 4 SSA conflicts can arise whenever the field-manager key from a previous apply does not match exactly, or when the SSA ownership record is in an inconsistent state.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Flag to add | `--force-conflicts` | Standard Helm 4 + kubectl SSA flag; tells the server to grant field ownership to the current applier even when another manager holds those fields. Safe when the conflicting manager is also "helm" — the same tool managing the same release. |
| Apply to both charts | `mclaude-cp` and `mclaude-worker` | Both exhibit the same SSA conflict pattern under Helm 4. |
| Keep ADR-0084 cleanup steps | Yes | Complement, not replace. Uninstall-on-failed clears SSA ownership fully for catastrophically broken releases. `--force-conflicts` handles the normal-upgrade conflict case. |

## Component Changes

### `.github/workflows/deploy-main.yml`

Add `--force-conflicts` to both `helm upgrade --install` commands:

```yaml
# Control-plane (line ~207):
helm upgrade --install "mclaude-cp" ./charts/mclaude-cp \
  ...
  --force-conflicts \
  --wait --timeout 5m

# Worker (line ~239):
helm upgrade --install "mclaude-worker" ./charts/mclaude-worker \
  ...
  --force-conflicts \
  --wait --timeout 5m
```

## Error Handling

If `--force-conflicts` itself fails (unsupported flag in a future Helm version), the step exits non-zero and the deploy fails loudly. No silent data loss.

## Security

No security impact. `--force-conflicts` affects only SSA field ownership metadata, not resource content or RBAC. The chart content applied is still validated and identical to what would be applied without the flag.

## Impact

No spec updates — this is a CI operational fix. Supersedes the "normal-upgrade" gap left open by ADR-0084.

Components implementing the change:
- `.github/workflows/deploy-main.yml`

## Scope

**v1:**
- Add `--force-conflicts` to the `mclaude-cp` `helm upgrade --install` command
- Add `--force-conflicts` to the `mclaude-worker` `helm upgrade --install` command

## Integration Test Cases

No dedicated integration test — observable outcome is CI deploys succeed on healthy releases after ADR-0087's `nats-configmap.yaml` change (which triggered this failure). CI run following this fix should complete without SSA conflict errors.

## Implementation Plan

| Component | New/changed lines (est.) | Notes |
|-----------|--------------------------|-------|
| `.github/workflows/deploy-main.yml` | 2 | One `--force-conflicts` per helm upgrade block |

**Total estimated tokens:** ~3k
