# Reconciler Deployment Sync

## Overview

The MCProject reconciler creates session-agent Deployments but does not keep them in sync when the MCProject spec changes after initial creation. Specifically, when `gitIdentityId` is added or changed on an MCProject CR, the running pod's environment variables are not updated — the session-agent continues using stale (or empty) git credentials until manually restarted.

This design fixes the reconciler to fully sync the Deployment spec on every reconcile, and adds the missing `gitIdentityId` field to the CRD schema.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Sync granularity | Full container spec rebuild on every reconcile | Simpler, idempotent, catches any spec drift — not just env vars. K8s no-ops when nothing changed. |
| Restart trigger | Rely on K8s Deployment controller | When the pod template changes (env vars, image, volumes), K8s automatically triggers a rolling restart. No annotation hacks needed. |
| CRD schema | Add `gitIdentityId` as a formal optional field | The Go struct (`MCProjectSpec`) and reconciler create path already use this field, but the CRD YAML schema omits it. `kubectl patch` warns about unknown fields. Formalizing it makes the schema self-documenting and consistent with the Go types. |

## Component Changes

### Helm Chart (`charts/mclaude/templates/mcproject-crd.yaml`)

Add `gitIdentityId` to the OpenAPI v3 schema under `spec.properties`:

```yaml
gitIdentityId:
  type: string
  description: OAuth connection ID for git credential resolution
```

Optional field (not in `required` list). Empty string means no identity — session-agent uses default credentials.

### Control-Plane Reconciler (`mclaude-control-plane/reconciler.go`)

The `reconcileDeployment` function currently has two paths:

1. **Create path** (Deployment doesn't exist): builds full Deployment spec including env vars. This is correct.
2. **Update path** (Deployment exists): only updates `image` and `strategy`. **This is the bug.**

Fix the update path to rebuild the full container spec:

```
existing Deployment found
  → rebuild containers[] with current env vars, image, command, volumeMounts
  → rebuild volumes[] with current volume list
  → re-discover imagePullSecrets (list Secrets in user namespace, same as create path)
  → set strategy to Recreate
  → call client.Update()
```

The reconciler already computes the correct env vars (including `GIT_IDENTITY_ID` when `gitIdentityId` is non-empty), volumes, and imagePullSecrets on the create path. The fix is to apply the same full pod template to the existing Deployment instead of only updating the image. This includes `imagePullSecrets`, which the create path discovers dynamically by listing Secrets in the user namespace.

K8s Deployment controller compares the pod template hash. If the template changed (new env var, different image), it triggers a rolling restart. If nothing changed, the Update is a no-op.

#### Env var computation (existing, unchanged)

```
Always present:
  USER_ID        = mcp.Spec.UserID
  PROJECT_ID     = mcp.Spec.ProjectID
  NATS_URL       = r.sessionAgentNATSURL

Conditional:
  GIT_URL          — only if mcp.Spec.GitURL != ""
  GIT_IDENTITY_ID  — only if mcp.Spec.GitIdentityID != ""
```

`GIT_IDENTITY_ID` is deliberately omitted (not set to empty string) when the identity is cleared. The session-agent checks `os.Getenv("GIT_IDENTITY_ID") != ""` to decide whether to switch accounts.

## Error Handling

- If the Deployment update fails, the reconciler returns an error and controller-runtime retries with backoff (existing behavior).
- If the CRD update from `PatchMCProjectGitIdentity` fails, it logs a warning and continues (existing behavior, non-fatal).
- If the session-agent pod crashes after a restart (e.g., invalid git credentials), it enters CrashLoopBackOff — the user sees this in the UI as an offline agent (via NATS presence detection) and can fix the identity in Settings.

## Scope

**In scope:**
- Add `gitIdentityId` to CRD schema (YAML) and canonical state schema (`docs/spec-state-schema.md`)
- Reconciler update path syncs full container spec (env vars, volumes, imagePullSecrets, image, strategy)

**Deferred:**
- Reconciler watching Secret changes (token refresh) — session-agent already handles this via `RefreshIfChanged` on the mounted Secret
- Rollback behavior if new identity fails — session-agent already has `git auth error during initial clone — aborting` which triggers CrashLoopBackOff

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| Helm chart CRD | 3 lines | ~30k | Add field to YAML schema |
| Control-plane reconciler | 15-25 lines | ~60k | Refactor update path to sync full container spec |
| Control-plane tests | 20-30 lines | included above | Test that reconciler updates env vars on existing Deployment |

**Total estimated tokens:** ~90k
**Estimated wall-clock:** <30min
