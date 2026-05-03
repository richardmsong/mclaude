# ADR: Fix init-keys Job failing on fresh namespace install

**Status**: accepted
**Status history**:
- 2026-05-03: accepted — Class A bug fix; POSTGRES_PASSWORD secretKeyRef must be optional

## Overview

The `mclaude-cp-init-keys` Helm pre-install Job fails with `DeadlineExceeded` on fresh namespace installs because the `POSTGRES_PASSWORD` env var references the `mclaude-postgres` Secret via a required `secretKeyRef`. The postgres Secret doesn't exist when the pre-install hook runs (postgres SubChart deploys after pre-install hooks complete). The pod never starts, and the Job times out after `activeDeadlineSeconds: 120`.

## Motivation

After the `mclaude-system` namespace was deleted and recreated, the CI deploy run failed:

```
Error: failed pre-install: resource not ready, name: mclaude-cp-init-keys-1, kind: Job, status: Failed
Warning  DeadlineExceeded  job-controller  Job was active longer than specified deadline
```

The postgres Secret (`mclaude-postgres`) doesn't exist in a fresh namespace. Without `optional: true` on the `secretKeyRef`, Kubernetes refuses to start the pod. The Job waits until its `activeDeadlineSeconds: 120` fires, then fails.

The init-keys binary only connects to postgres when both `BOOTSTRAP_ADMIN_EMAIL` and `DATABASE_URL` are set. In the k3d deployment (`values-k3d-ghcr.yaml`), `bootstrapAdminEmail` is not set — so postgres is never actually needed by init-keys on this cluster.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Fix location | `charts/mclaude-cp/templates/init-keys-job.yaml` | The Job template has the required `secretKeyRef`; the binary's conditional logic is already correct |
| Fix approach | Add `optional: true` to the `POSTGRES_PASSWORD` secretKeyRef | Pod starts even without the postgres Secret; if SECRET is absent, `POSTGRES_PASSWORD` is empty, `DATABASE_URL` interpolates to an unusable URL, but init-keys skips postgres when `bootstrapAdminEmail` is empty anyway |
| DATABASE_URL unchanged | Leave `DATABASE_URL` value template as-is | When `POSTGRES_PASSWORD` is absent, DATABASE_URL becomes `postgres://mclaude:@...` — init-keys won't use it when bootstrapAdminEmail is unset |

## Component Changes

### `charts/mclaude-cp`

**`templates/init-keys-job.yaml`** — add `optional: true` to the `POSTGRES_PASSWORD` secretKeyRef:

```yaml
# Before (required — pod won't start if mclaude-postgres Secret absent):
- name: POSTGRES_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ .Values.postgres.auth.existingSecret | default "mclaude-postgres" }}
      key: postgres-password

# After (optional — pod starts even when postgres hasn't been deployed yet):
- name: POSTGRES_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ .Values.postgres.auth.existingSecret | default "mclaude-postgres" }}
      key: postgres-password
      optional: true
```

## Error Handling

When `bootstrapAdminEmail` is set and the postgres Secret is absent (a genuine misconfiguration), init-keys will start but fail to connect to postgres (malformed DATABASE_URL from empty `POSTGRES_PASSWORD`). This is the correct behavior — the operator must ensure postgres is running before setting `bootstrapAdminEmail`.

## Security

No security impact. The secret remains used when postgres is deployed; `optional: true` only prevents pod-start failure when it's absent.

## Impact

No spec updates — spec-helm.md already correctly describes the job as only connecting to postgres "when `controlPlane.bootstrapAdminEmail` is set". This fix restores the spec's implied behavior that the job works on fresh installs without postgres.

Components implementing the change:
- `charts/mclaude-cp` (templates/init-keys-job.yaml)

## Scope

**v1:**
- Add `optional: true` to `POSTGRES_PASSWORD` secretKeyRef in init-keys-job.yaml

## Integration Test Cases

No integration test — this is a Helm template configuration fix. The observable outcome is that fresh namespace CI deploys succeed (init-keys Job completes) rather than failing with `DeadlineExceeded`.

## Implementation Plan

| Component | New/changed lines (est.) | Notes |
|-----------|--------------------------|-------|
| `charts/mclaude-cp/templates/init-keys-job.yaml` | 1 | Add `optional: true` under the secretKeyRef |

**Total estimated tokens:** ~10k
