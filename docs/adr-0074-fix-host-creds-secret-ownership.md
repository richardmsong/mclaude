# ADR: Fix Host-Creds Secret Ownership — Remove Helm Template, Job is Sole Owner

**Status**: accepted
**Status history**:
- 2026-05-02: accepted — paired with docs/charts-mclaude/spec-helm.md

## Overview

`charts/mclaude-worker/templates/host-creds-secret.yaml` conflicts with the `gen-host-nkey` pre-install/pre-upgrade Job. Both try to own the `{release}-host-creds` Secret. The Job (ADR-0073) creates the Secret with a real `nkey_seed`. Then Helm applies the chart template which declares `nkey_seed: ""` — either silently overwriting the seed or producing an "original object not found" SSA conflict on upgrade. This ADR removes the chart template and makes the Job the sole owner of the Secret.

## Motivation

After ADR-0073 fixed the missing `gen-host-nkey` subcommand, CI deploy run 25261968593 showed the next failure:

```
Error: UPGRADE FAILED: original object Secret with the name "mclaude-worker-host-creds" not found
```

Investigation:
1. The gen-host-nkey pre-upgrade hook ran successfully — Secret exists in cluster with label `app.kubernetes.io/managed-by: mclaude-gen-host-nkey` and a real `nkey_seed` value.
2. Helm then tried to reconcile `host-creds-secret.yaml` (a regular chart template) against the same Secret.
3. Helm's server-side apply (SSA) found the Secret was not in the release's "last applied" metadata (the upgrade was previously failing, so Helm's tracking was inconsistent) → "original object not found".

Even if the SSA error is resolved, the template declaring `nkey_seed: ""` would overwrite the Job-written seed on every `helm upgrade`, breaking the controller's NKey authentication.

`spec-helm.md` already says: "Idempotent — skips if Secret exists" — the spec describes the Secret as Job-owned, not chart-template-owned. The template is wrong.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Remove `host-creds-secret.yaml` | Yes — delete from chart templates | The Secret is exclusively created/owned by the gen-host-nkey Job. A chart template for the same Secret creates dual ownership, causing SSA conflicts and seed overwrites. |
| `helm.sh/resource-policy: keep` on Secret | Yes — add to Job-created Secret | When `host-creds-secret.yaml` is removed from templates, Helm would try to delete the Secret on next upgrade (orphan cleanup). The `helm.sh/resource-policy: keep` annotation prevents Helm from deleting it. Also desirable independently: preserves the NKey seed across `helm uninstall` (avoids re-registration). |
| Secret lifecycle | Job creates on first install; persists forever | NKey seeds are stable host identities. Losing the seed would require re-running `mclaude host register`. The annotation ensures the seed survives upgrades and uninstalls. |

## Component Changes

### `charts/mclaude-worker/templates/host-creds-secret.yaml`

Delete this file entirely. The gen-host-nkey Job is the sole creator of the `{release}-host-creds` Secret.

### `mclaude-control-plane/gen_host_nkey.go`

Add annotations to the Secret when creating it:

```go
secret.Annotations = map[string]string{
    "helm.sh/resource-policy": "keep",
}
```

This prevents Helm from deleting the Secret during `helm uninstall` or when the template is removed from the chart.

## Impact

Specs updated in this commit:
- `docs/charts-mclaude/spec-helm.md` — update `{release}-host-creds` Secret row to note that the Secret is created by the Job (not a chart template), persists across uninstall, and carries `helm.sh/resource-policy: keep`

Components implementing the change:
- `charts/mclaude-worker` (delete host-creds-secret.yaml)
- `mclaude-control-plane` (gen_host_nkey.go — add annotation)

## Scope

**In this change:** Remove template, add `helm.sh/resource-policy: keep` annotation on Job-created Secret.

**Deferred:** Helm pre-delete hook to deregister the host before `helm uninstall` (mentioned in spec-helm.md as a future enhancement).

## Integration Test Cases

| Test case | What it verifies | Components exercised |
|-----------|------------------|----------------------|
| Worker Helm upgrade succeeds end-to-end | gen-host-nkey hook skips (Secret exists with `helm.sh/resource-policy: keep`); no SSA conflict; controller mounts Secret with real nkey_seed | mclaude-worker chart, gen-host-nkey Job, deploy-main.yml |
| `helm uninstall` keeps Secret | Secret survives uninstall (resource-policy annotation) | mclaude-worker chart |
