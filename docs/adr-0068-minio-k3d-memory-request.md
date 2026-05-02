# ADR: Reduce MinIO Memory Request for k3d

**Status**: accepted
**Status history**:
- 2026-05-02: draft
- 2026-05-02: accepted — paired with docs/charts-mclaude/spec-helm.md

## Overview

Adds a `values-k3d-ghcr.yaml` resource override that reduces the MinIO memory request from the default 128Mi to 32Mi, allowing MinIO to schedule on the single k3d node that is at 98% memory request allocation. The production default in `values.yaml` is unchanged.

## Motivation

After ADR-0067 deployed MinIO into the k3d cluster, the MinIO Deployment pod cannot schedule. The single k3d node has 7884Mi of memory requests allocated (98%) against only 1923Mi actual usage — ~25 active session-agent pods (64Mi each) plus infrastructure consume the request budget even though the node is not memory-pressured. The MinIO pod (128Mi request) cannot fit in the remaining ~180Mi of allocatable request headroom.

Precedent: ADR-0066 reduced session-agent memory request from 256Mi to 64Mi for the same reason — request budget exhaustion on the shared k3d node, not actual memory pressure.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Override placement | `values-k3d-ghcr.yaml` only | k3d-specific scheduling constraint; production default (128Mi) is appropriate for real workloads |
| New request value | 32Mi | Node has ~180Mi of unallocated request budget; 32Mi leaves headroom for other pods and is sufficient for MinIO serving presigned-URL traffic at dev scale |
| CPU request | No change (100m default) | CPU is not the bottleneck |
| Memory limit | No change (512Mi) | Limit remains high so MinIO can burst if needed |

## Component Changes

### charts/mclaude-cp

`values-k3d-ghcr.yaml` gains a resource override:

```yaml
minio:
  resources:
    requests:
      memory: 32Mi
```

No other file changes. `values.yaml` default (128Mi) is unchanged.

## Data Model

None.

## Error Handling

None — this is a scheduling-budget tweak with no runtime behavior change.

## Security

None.

## Impact

Specs updated in this commit:
- `docs/charts-mclaude/spec-helm.md` — update `values-k3d-ghcr.yaml` row in the Values files table to document the MinIO memory request override

## Scope

**v1 (this ADR):** Lower MinIO memory request in `values-k3d-ghcr.yaml` to 32Mi.

**Deferred:** Raising the k3d node memory ceiling (would require cluster reprovisioning) — orthogonal and not required.

## Integration Test Cases

No integration tests — change is config-only (Helm values override, no runtime behavior change).

## Implementation Plan

| Component | New/changed lines (est.) | Notes |
|-----------|--------------------------|-------|
| charts/mclaude-cp values-k3d-ghcr.yaml | 3 | Add `minio.resources.requests.memory: 32Mi` |

**Total estimated tokens:** ~20k (trivial single-file edit)
**Estimated wall-clock:** <5 min
