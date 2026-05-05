# Implementation Audit: charts-mclaude (mclaude-cp + mclaude-worker + CI workflow)

**Component roots**: `charts/mclaude-cp/`, `charts/mclaude-worker/`, `.github/workflows/deploy-main.yml`
**Primary spec**: `docs/charts-mclaude/spec-helm.md`
**Cross-cutting ref**: `docs/adr-0091-per-user-anthropic-token.md`
**ADR-0091 Status**: accepted

## Run: 2026-05-04T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0091:363 | "Remove `controlPlane.devOAuthToken` value and env var." | `charts/mclaude-cp/values.yaml` (entire file), `charts/mclaude-cp/templates/control-plane-deployment.yaml` (entire file) | IMPLEMENTED | — | No `devOAuthToken` key in values.yaml; no `DEV_OAUTH_TOKEN` env var in control-plane-deployment.yaml. Global grep confirms zero occurrences. |
| adr-0091:364 | "Add JetStream at-rest encryption: `store_key` stanza in `nats-configmap.yaml`. Key provisioned via k8s Secret or Helm value." | `charts/mclaude-cp/values.yaml:50-58`, `charts/mclaude-cp/templates/nats-configmap.yaml:29-31`, `charts/mclaude-cp/templates/nats-statefulset.yaml:52-58` | IMPLEMENTED | — | `nats.config.jetstream.encryption.storeKeySecret` knob added; ConfigMap renders `key: $STORE_KEY` when set; StatefulSet injects env var `STORE_KEY` from the named Secret's `store-key` field. |
| spec-helm.md:121 | "`nats.config.jetstream.encryption.storeKeySecret` — ADR-0091. Name of a Kubernetes Secret holding the JetStream at-rest encryption key under field `store-key`. When set, the NATS ConfigMap renders a `jetstream { store_dir: ..., key: $STORE_KEY }` stanza and the NATS StatefulSet mounts the Secret." | `charts/mclaude-cp/values.yaml:50-58`, `charts/mclaude-cp/templates/nats-configmap.yaml:25-32`, `charts/mclaude-cp/templates/nats-statefulset.yaml:52-58` | IMPLEMENTED | — | Knob, ConfigMap stanza, and StatefulSet env var all present. |
| adr-0091:367 | "Remove `controller.config.devOAuthToken` and `DEV_OAUTH_TOKEN` env var." | `charts/mclaude-worker/values.yaml` (entire file), `charts/mclaude-worker/templates/controller-deployment.yaml` (entire file) | IMPLEMENTED | — | No `devOAuthToken` key in values.yaml; no `DEV_OAUTH_TOKEN` env var in controller-deployment.yaml. Global grep confirms zero occurrences. |
| adr-0091:368 | "Add user-agent Deployment template to user namespace provisioning." | `charts/mclaude-worker/templates/user-agent-template.yaml:1-20`, `charts/mclaude-worker/values.yaml:107-118` | IMPLEMENTED | — | `{release}-user-agent-template` ConfigMap template exists; `userAgent.*` knobs in values.yaml. |
| adr-0091:369 | "Remove session pod templates from host controller (moved to user-agent)." | `charts/mclaude-worker/templates/session-agent-template.yaml` still present | PARTIAL | SPEC→FIX | The spec says "Remove session pod templates from host controller" but the `{release}-session-agent-template` ConfigMap still exists in the worker chart. However, per `spec-helm.md:215`, the session-agent-template is intentionally retained — it is now read by `mclaude-user-agent` (not the controller) when copied to user namespaces. The controller copies this ConfigMap into user namespaces so user-agents can access it without cross-namespace RBAC. ADR-0091 says "removed" but spec-helm.md says "read by mclaude-user-agent". The spec is clearer: the template stays but ownership of reading it shifts. The ADR wording "remove session pod templates from host controller" is imprecise — it means the controller no longer *uses* it for pod creation, not that the ConfigMap is deleted. Spec→Fix: the ADR phrase should be clarified. Code is correct. |
| adr-0091:373 | "Remove `DEV_OAUTH_TOKEN` from Bitwarden fetch step and all `--set` references." | `.github/workflows/deploy-main.yml:181-232` | IMPLEMENTED | — | No `DEV_OAUTH_TOKEN` in any Bitwarden fetch or `helm upgrade` `--set` flags. Global grep of workflow file confirms zero occurrences. |
| spec-helm.md:206 | "ClusterRole `{release}-controller` — Per ADR-0091: namespace, deployment, secret, configmap, serviceaccount, role, rolebinding management. The `MCProject` CRD permission is **removed**." | `charts/mclaude-worker/templates/clusterrole.yaml:1-45` | IMPLEMENTED | — | ClusterRole contains rules for namespaces, deployments, PVCs, secrets, configmaps, serviceaccounts, roles, rolebindings, and leases. No MCProject/mclaude.io CRD permissions present. Comment in file confirms ADR-0091 removal. |
| spec-helm.md:209 | "The `mcprojects.mclaude.io` CustomResourceDefinition is **removed** by ADR-0091 — project pod lifecycle moves to the user-agent (event-driven, no CRD)." | All `charts/mclaude-worker/templates/*.yaml` files | IMPLEMENTED | — | No `CustomResourceDefinition` resource exists anywhere in the worker chart templates. No `mcprojects.mclaude.io` reference found (only in a comment in clusterrole.yaml noting removal). |
| spec-helm.md:215 | "ConfigMap `{release}-session-agent-template` — Static pod template values … Per ADR-0091, read by **`mclaude-user-agent`** (not the controller) to build per-project Deployments." | `charts/mclaude-worker/templates/session-agent-template.yaml:1-24` | IMPLEMENTED | — | ConfigMap exists with all documented fields: image, imagePullPolicy, terminationGracePeriodSeconds, resourcesJson, projectPvcSize, projectPvcStorageClass, nixPvcSize, nixPvcStorageClass, corporateCAEnabled, corporateCAConfigMapName, corporateCAConfigMapKey. |
| spec-helm.md:220 | "ConfigMap `{release}-user-agent-template` — Pod template values for the per-user-namespace `user-agent` Deployment (image, resources, ServiceAccount config). Read by `mclaude-controller-k8s` when reconciling a `grants.create` event." | `charts/mclaude-worker/templates/user-agent-template.yaml:1-20` | PARTIAL | SPEC→FIX | ConfigMap exists with `image`, `imagePullPolicy`, and `resourcesJson`. However, spec mentions "ServiceAccount config" as one of the fields, and the ConfigMap does not include any ServiceAccount-related field. The spec is vague — it's unclear what "ServiceAccount config" means concretely (SA name? SA annotations?). The controller would need the SA name to create user-agent Deployments; this may be derived at runtime from a naming convention rather than stored in the ConfigMap. Spec should be tightened to enumerate the exact fields or drop "ServiceAccount config" if it's not a ConfigMap field. |
| spec-helm.md:245 | "`userAgent.image.repository` — ADR-0091. User-agent container image." | `charts/mclaude-worker/values.yaml:108-111` | IMPLEMENTED | — | `userAgent.image.repository: ghcr.io/richardmcsong/mclaude-user-agent` present in values.yaml. |
| spec-helm.md:246 | "`userAgent.image.tag` — Image tag (empty = chart appVersion)." | `charts/mclaude-worker/values.yaml:112-113`, `charts/mclaude-worker/templates/user-agent-template.yaml:15` | IMPLEMENTED | — | Knob present and template uses `coalesce .Values.userAgent.image.tag .Chart.AppVersion`. |
| spec-helm.md:247 | "`userAgent.resources` — User-agent pod resource requests/limits." | `charts/mclaude-worker/values.yaml:114` | IMPLEMENTED | — | `userAgent.resources: {}` present. |
| spec-helm.md:248 | "`userAgent.templateConfigMap.name` — Override `{release}-user-agent-template` ConfigMap name." | `charts/mclaude-worker/values.yaml:115-118`, `charts/mclaude-worker/templates/user-agent-template.yaml:8` | IMPLEMENTED | — | Knob present; ConfigMap template uses `coalesce .Values.userAgent.templateConfigMap.name (printf "%s-user-agent-template" ...)`. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| `charts/mclaude-cp/templates/nats-statefulset.yaml:83-107` | INFRA | VolumeClaimTemplate and data volume configuration. Standard Helm persistence pattern; spec-helm.md documents the StatefulSet exists and references persistence knobs. |
| `charts/mclaude-cp/templates/control-plane-deployment.yaml:25-48` | INFRA | dbmate init container block. Gated by `controlPlane.migrations.enabled`; spec-helm.md:58 documents the Deployment and init container behavior. |
| `charts/mclaude-cp/templates/control-plane-deployment.yaml:97-152` | INFRA | S3 env vars, volumeMounts, probes, resources. All fully spec'd in spec-helm.md:58. |
| `charts/mclaude-worker/templates/clusterrole.yaml:41-44` | INFRA | Leader election (coordination.k8s.io/leases) ClusterRole rule. Not mentioned in spec-helm.md by name, but required by the controller's leader election flag (`controller.leaderElection`), which is spec'd at spec-helm.md (Configuration Knobs table for worker). Necessary infrastructure for the spec'd deployment. |
| `charts/mclaude-worker/templates/controller-deployment.yaml:48-50` | INFRA | `SESSION_AGENT_TEMPLATE_CM` env var passed to controller. Not in spec-helm.md Configuration Knobs table, but is a necessary wiring between the worker chart's configmap name and the controller binary. |
| `.github/workflows/deploy-main.yml:240-282` | UNSPEC'd | `deploy-worker` job has `if: false` — always disabled. The job body references `trustChain.*` Helm values (`trustChain.operatorJwt`, `trustChain.accountJwt`, etc.) that do not exist in `charts/mclaude-worker/values.yaml`. This job appears to be legacy/dead code from an older architecture that was not cleaned up. The spec and ADR do not describe this pattern. See notes. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0091:363 | Remove `controlPlane.devOAuthToken` | None | None | UNTESTED |
| adr-0091:364 | JetStream at-rest encryption storeKeySecret knob | None | None | UNTESTED |
| adr-0091:367 | Remove `controller.config.devOAuthToken` | None | None | UNTESTED |
| adr-0091:368 | User-agent Deployment template ConfigMap | None | None | UNTESTED |
| adr-0091:369 | Session pod templates removed from controller | None | None | UNTESTED |
| adr-0091:373 | DEV_OAUTH_TOKEN removed from CI workflow | None | None | UNTESTED |
| spec-helm.md:206 | MCProject CRD permission removed from ClusterRole | None | None | UNTESTED |

Note: Helm charts by nature lack traditional unit/integration tests for individual template values. The `charts/mclaude-cp/templates/tests/test-smoke.yaml` and `charts/mclaude-worker/templates/tests/test-smoke.yaml` are Helm test hooks but do not exercise ADR-0091 chart changes specifically. All ADR-0091 chart changes are UNTESTED at the chart level; correctness is verified by the audit inspection above.

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | No open bugs in `.agent/bugs/` for charts-mclaude component. | — | — |

### Summary

- Implemented: 11
- Gap: 0
- Partial: 2
- Infra: 5
- Unspec'd: 1
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 0
- Untested: 7
- Bugs fixed: 0
- Bugs open: 0
