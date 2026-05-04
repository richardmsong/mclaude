## Run: 2026-05-02T00:00:00Z

Component: charts/mclaude-cp
ADR: docs/adr-0068-minio-k3d-memory-request.md (Status: accepted)
Spec: docs/charts-mclaude/spec-helm.md (Values files table, minio.resources row)

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0068:31-38 | `values-k3d-ghcr.yaml` gains `minio.resources.requests.memory: 32Mi` | values-k3d-ghcr.yaml:56-59 | IMPLEMENTED | — | Lines 56-59 contain exactly `minio:\n  resources:\n    requests:\n      memory: 32Mi` |
| adr-0068:40 | `values.yaml` default (128Mi) is unchanged | values.yaml:171-177 | IMPLEMENTED | — | `minio.resources.requests.memory: 128Mi` remains in values.yaml:174 |
| adr-0068:24 | CPU request: No change (100m default) | values-k3d-ghcr.yaml:56-59 | IMPLEMENTED | — | No cpu override in values-k3d-ghcr.yaml; values.yaml:172 still has `cpu: 100m` |
| adr-0068:25 | Memory limit: No change (512Mi) | values-k3d-ghcr.yaml:56-59 | IMPLEMENTED | — | No limits override in values-k3d-ghcr.yaml; values.yaml:175-177 retains `limits.memory: 512Mi` |
| spec-helm.md:273 | `values-k3d-ghcr.yaml` ... Overrides `minio.resources.requests.memory: 32Mi` (down from default 128Mi) ... (ADR-0068) | values-k3d-ghcr.yaml:56-59 | IMPLEMENTED | — | Spec row matches code exactly |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| values-k3d-ghcr.yaml:1-64 (all non-minio lines) | INFRA | All other keys in values-k3d-ghcr.yaml (controlPlane, spa, namespace, ingress, global) are covered by prior ADRs and spec-helm.md rows for the k3d-ghcr values file; they are pre-existing and not part of this ADR's scope. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0068:31-38 | `minio.resources.requests.memory: 32Mi` override in values-k3d-ghcr.yaml | n/a | n/a | UNTESTED |

Note: ADR-0068 explicitly states "No integration tests — change is config-only (Helm values override, no runtime behavior change)." UNTESTED is expected and correct per the ADR.

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| — | No open bugs found for charts/mclaude-cp or minio | — | `.agent/bugs/` contains no matching entries |

### Summary

- Implemented: 5
- Gap: 0
- Partial: 0
- Infra: 1
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 0
- Untested: 1 (expected — ADR explicitly waives tests for config-only change)
- Bugs fixed: 0
- Bugs open: 0
