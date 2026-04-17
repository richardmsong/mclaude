## Run: 2026-04-16T00:00:00Z

Spec doc: docs/plan-reconciler-env-sync.md
Components: mclaude-control-plane/reconciler.go, charts/mclaude/templates/mcproject-crd.yaml, docs/plan-state-schema.md

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Notes |
|-----------------|-----------|---------------|---------|-------|
| plan-reconciler-env-sync.md:19-27 | Add `gitIdentityId` to OpenAPI v3 schema under `spec.properties` as optional string field with description "OAuth connection ID for git credential resolution" | charts/mclaude/templates/mcproject-crd.yaml:40-42 | IMPLEMENTED | Field present with correct type, description, and not in required list |
| plan-reconciler-env-sync.md:29 | Optional field (not in `required` list). Empty string means no identity | charts/mclaude/templates/mcproject-crd.yaml:27-29,40-42 | IMPLEMENTED | required list only contains userId and projectId; gitIdentityId is absent from required |
| plan-reconciler-env-sync.md:33-36 | Create path builds full Deployment spec including env vars — correct | reconciler.go:428-459 | IMPLEMENTED | Create path calls r.buildPodTemplate() which builds full env vars, volumes, imagePullSecrets |
| plan-reconciler-env-sync.md:37-47 | Update path: rebuild containers[] with current env vars/image/command/volumeMounts, rebuild volumes[], re-discover imagePullSecrets, set strategy to Recreate, call client.Update() | reconciler.go:411-420 | IMPLEMENTED | Update path sets existing.Spec.Template = r.buildPodTemplate(...) and sets Recreate strategy then calls r.client.Update() |
| plan-reconciler-env-sync.md:49-51 | Fix is to apply the same full pod template to existing Deployment; imagePullSecrets discovered dynamically by listing Secrets in user namespace | reconciler.go:303-323 | IMPLEMENTED | buildPodTemplate() lists Secrets in userNs (not controlPlaneNs) and filters for DockerConfigJson type |
| plan-reconciler-env-sync.md:54-63 | Env vars: USER_ID, PROJECT_ID, NATS_URL always present; GIT_URL only if non-empty; GIT_IDENTITY_ID only if non-empty | reconciler.go:325-337 | IMPLEMENTED | Exact conditional logic present: GIT_URL added if gitURL != ""; GIT_IDENTITY_ID added if gitIdentityID != "" |
| plan-reconciler-env-sync.md:65-66 | GIT_IDENTITY_ID is deliberately omitted (not set to empty string) when identity is cleared | reconciler.go:333-337 | IMPLEMENTED | Comment on line 333 explicitly states this; conditional only adds var when gitIdentityID != "" |
| plan-reconciler-env-sync.md:69-72 | Error handling: Deployment update failure causes reconciler to return error and controller-runtime retries with backoff | reconciler.go:420 | IMPLEMENTED | `return r.client.Update(ctx, existing)` returns error directly to reconcileDeployment caller which propagates to Reconcile() |
| plan-reconciler-env-sync.md:77 | In scope: Add `gitIdentityId` to canonical state schema (`docs/plan-state-schema.md`) | docs/plan-state-schema.md:309 | IMPLEMENTED | `gitIdentityId: string  # optional — oauth_connections.id for git credential resolution` present in MCProject CRD spec section |
| plan-reconciler-env-sync.md:78 | Reconciler update path syncs full container spec (env vars, volumes, imagePullSecrets, image, strategy) | reconciler.go:411-420 | IMPLEMENTED | All five elements rebuilt: template (env/volumes/imagePullSecrets/image via buildPodTemplate) + strategy explicitly set |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| reconciler.go:299-302 | INFRA | Function comment for buildPodTemplate — documentation boilerplate |
| reconciler.go:392-397 | INFRA | Function comment for reconcileDeployment citing plan docs — documentation boilerplate |
| reconciler.go:339-389 | INFRA | PodTemplateSpec construction body: labels, SecurityContext, Volumes list, Containers list — necessary plumbing to implement the spec'd "full pod template rebuild" behavior |
| reconciler.go:463-498 | INFRA | ensurePVCCR — PVC management needed by reconcileDeployment (pre-condition for Deployment to function); spec says PVCs are part of provisioning |
| reconciler.go:500-516 | INFRA | ensureOwned — generic helper for RBAC/ConfigMap idempotent creation; used by reconcileNamespace/reconcileRBAC/reconcileSecrets |
| reconciler.go:518-557 | INFRA | loadTemplate — reads session-agent-template ConfigMap; required infrastructure for tpl parameter used by buildPodTemplate |
| reconciler.go:559-588 | INFRA | setPhase/updateCondition/setCondition — status update helpers; required for MCProject status subresource behavior |
| reconciler.go:591-645 | INFRA | SetupWithManager — controller-runtime wiring; required to register watches and make reconciler functional |
| reconciler.go:647-672 | INFRA | CreateMCProject — helper to create MCProject CR; used by NATS handler; not directly in reconciler-env-sync scope but is infrastructure for CRD usage |
| reconciler.go:674-697 | INFRA | ClearMCProjectGitIdentityForConnection — clears GitIdentityID when OAuth connection deleted; not described in plan-reconciler-env-sync.md but is referenced in plan-scratch-to-git.md (git identity management) |
| reconciler.go:699-719 | INFRA | PatchMCProjectGitIdentity — updates GitIdentityID on CRD; referenced in error handling section of plan-reconciler-env-sync.md:71 |
| reconciler.go:721-748 | INFRA | defaultTemplate/applyDefaultResources — fallback defaults for dev/test; necessary plumbing for loadTemplate |
| charts/mclaude/templates/mcproject-crd.yaml:1-93 | INFRA | Full CRD YAML structure — boilerplate CRD scaffolding around the spec'd gitIdentityId addition; all other fields (userId, projectId, gitUrl, status) are described in plan-k8s-integration.md and plan-state-schema.md |

### Summary

- Implemented: 10
- Gap: 0
- Partial: 0
- Infra: 13
- Unspec'd: 0
- Dead: 0
