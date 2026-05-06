---
**ADR-0105: Fix user-agent image tag in mclaude-worker Helm chart**

**Status**: accepted

**Status history**:
- 2026-05-06: accepted — fixing Class A bug: wrong image tag causes SchedulingGated

---

## Context

The `mclaude-worker` Helm chart deploys a `{release}-user-agent-template` ConfigMap whose `image` field is read by `mclaude-controller-k8s` when creating per-user `user-agent` Deployments. The image tag is resolved by:

```yaml
{{- $tag := coalesce .Values.userAgent.image.tag .Chart.AppVersion }}
image: {{ printf "%s:%s" .Values.userAgent.image.repository $tag | quote }}
```

`values.yaml` ships `userAgent.image.tag: ""`, so the tag falls back to `Chart.AppVersion = "0.1.0"`. CI has never published a `0.1.0` image tag — all builds publish to the `main` tag on `ghcr.io/richardmcsong/mclaude-user-agent`. As a result, newly-provisioned user namespaces get a pod stuck in `SchedulingGated` with `MANIFEST_UNKNOWN: The named manifest is not known to the registry`.

## Decision

Set `userAgent.image.tag: "main"` in `charts/mclaude-worker/values.yaml`. This overrides the `appVersion` fallback with the tag that CI actually publishes.

The chart `appVersion` remains `0.1.0` for Helm packaging purposes. When the project adopts semver image releases, `userAgent.image.tag` should be updated to match.

## Consequences

- Per-user `user-agent` Deployments will resolve to `ghcr.io/richardmcsong/mclaude-user-agent:main` and pull successfully.
- The `{release}-user-agent-template` ConfigMap value changes from `…:0.1.0` to `…:main` on next `helm upgrade`.
- No spec changes needed — the mechanism (empty tag → appVersion fallback) is correct; only the chart default was wrong.

## Scope

- `charts/mclaude-worker/values.yaml` — set `userAgent.image.tag: "main"`
- No changes to templates, specs, or other components.

## Integration test

Existing user-agent reconciler integration test (`mclaude-controller-k8s/controller/controller_test.go`) exercises the full reconcile path. No new test needed — the fix is a one-field chart value change.
