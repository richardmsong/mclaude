# ADR: Fix CI not bootstrapping prerequisite secrets on fresh namespace

**Status**: accepted
**Status history**:
- 2026-05-03: accepted — Class A bug fix; CI deploy fails on fresh namespace because prerequisite secrets are absent

## Overview

The `mclaude-cp` Helm chart requires two secrets that are not created by the chart itself and are not managed by CI: `mclaude-postgres` (postgres password) and `mclaude-control-plane` (admin token). When the `mclaude-system` namespace is deleted and recreated, these secrets are lost and the CI deploy fails with `CreateContainerConfigError` on the postgres StatefulSet and control-plane Deployment.

## Motivation

After the `mclaude-system` namespace was deleted (due to cert-manager Challenge finalizer blocking termination), the CI deploy run failed silently. Even after fixing ADR-0081 (init-keys `optional: true`), both pods came up with `CreateContainerConfigError`:

```
Warning  Failed  kubelet  Error: secret "mclaude-postgres" not found
Warning  Failed  kubelet  Error: secret "mclaude-control-plane" not found
```

These are manually created prerequisites that are documented only in `NOTES.txt`. CI has no step to create them if absent.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Fix location | `.github/workflows/deploy-main.yml` | Add a "Ensure prerequisite secrets" step before `helm upgrade` |
| Postgres password | Fixed dev value `dev-postgres-password-k3d` | Dev cluster; deterministic value avoids rotation complexity; if postgres PVC is fresh the password initializes correctly |
| Admin token | Fixed dev value `dev-admin-token` | Dev cluster; integration tests read `ADMIN_TOKEN` from env or this known value; consistent with existing test invocations |
| Create-if-absent pattern | `kubectl create secret ... --dry-run=client -o yaml \| kubectl apply -f -` | Idempotent: no-ops when secret already exists; safe for upgrades |
| Scope | k3d dev cluster only | These are dev-specific fixed values. Production would use sealed secrets or external secrets operator |

## Component Changes

### `.github/workflows/deploy-main.yml`

Add a new step **"Ensure prerequisite secrets"** immediately before "Helm deploy control-plane":

```yaml
- name: Ensure prerequisite secrets
  run: |
    # mclaude-postgres: postgres password (required by postgres StatefulSet and control-plane)
    kubectl create secret generic mclaude-postgres \
      --from-literal=postgres-password="dev-postgres-password-k3d" \
      --namespace mclaude-system \
      --dry-run=client -o yaml | kubectl apply -f -

    # mclaude-control-plane: admin bearer token (required by control-plane Deployment)
    kubectl create secret generic mclaude-control-plane \
      --from-literal=admin-token="dev-admin-token" \
      --namespace mclaude-system \
      --dry-run=client -o yaml | kubectl apply -f -
```

## Error Handling

The `--dry-run=client -o yaml | kubectl apply -f -` pattern is idempotent — it no-ops when the secret already exists (apply sees no diff for Opaque secrets with unchanged data). If apply fails for another reason, the step fails loudly before Helm runs.

## Security

These are dev-cluster-only fixed secrets. The values (`dev-postgres-password-k3d`, `dev-admin-token`) are intentionally non-secret for local/CI dev environments. Production deployments use sealed secrets or an external secrets operator — this ADR does not change production behavior.

## Impact

No spec updates — this is an operational fix to CI bootstrap.

Components implementing the change:
- `.github/workflows/deploy-main.yml`

## Scope

**v1:**
- Add "Ensure prerequisite secrets" step to deploy-main.yml

## Integration Test Cases

No dedicated integration test — the observable outcome is that CI deploys succeed after namespace recreation. The existing `TestIntegration_Import_HappyPath` verifies the cluster is functional end-to-end.

## Implementation Plan

| Component | New/changed lines (est.) | Notes |
|-----------|--------------------------|-------|
| `.github/workflows/deploy-main.yml` | ~12 | New step before helm deploy control-plane |

**Total estimated tokens:** ~10k
