## Run: 2026-05-01T00:00:00Z

Component: `charts/mclaude-worker`
Primary spec: `docs/charts-mclaude/spec-helm.md` (worker section)
ADR: `docs/adr-0063-k8s-architecture-spec.md` (status: accepted)

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-helm.md:7-11 | Chart purpose table: `mclaude-worker` = worker cluster, `mclaude-controller-k8s` operator (hub-direct, independently installable), session-agent template. | `Chart.yaml:3-5` | IMPLEMENTED | | Description matches exactly. |
| spec-helm.md:10 | Single-cluster degenerate deployment installs both charts into same K8s cluster; mclaude-worker connects directly to the hub NATS service. | `values-dev.yaml:15` (`hubNatsUrl: nats://mclaude-cp-nats.mclaude-system.svc:4222`) | IMPLEMENTED | | Dev values file demonstrates degenerate single-cluster topology using in-cluster NATS URL. |
| spec-helm.md:130 | Independently installable into any K8s cluster that can reach CP over HTTPS and hub NATS over WebSocket (443). No local NATS StatefulSet, no leaf-node credentials. | Templates: no `nats-statefulset.yaml`, no `gen-leaf-creds-job.yaml`, no `leafCreds` value. | IMPLEMENTED | | All leaf-NATS artifacts absent. Chart is hub-direct only. |
| spec-helm.md:134-139 | `helm install mclaude-worker` with flags `controlPlane.url`, `host.name`, `host.hubNatsUrl`. | `values.yaml:23-46` (`controlPlane.url`, `host.name`, `host.hubNatsUrl`); `controller-deployment.yaml:36-40` (env vars required). | IMPLEMENTED | | All three required values are present in values.yaml and injected into the controller. |
| spec-helm.md:141-146 | After install, read NKey public key from pre-install Job: `kubectl logs job/mclaude-worker-gen-host-nkey -n mclaude-system`, then run `mclaude host register --type cluster --name ... --nkey-public <pubkey>`. | `NOTES.txt:9-16` | IMPLEMENTED | | NOTES.txt renders exact kubectl logs command with `{{ .Release.Name }}-gen-host-nkey` and the full `mclaude host register` command. |
| spec-helm.md:163-167 (Pre-Install Job table) | Job `{release}-gen-host-nkey`, pre-install hook weight `-10`, generates NKey pair, writes seed to Secret `{release}-host-creds` field `nkey_seed`, prints public key to log and NOTES.txt, idempotent. | `gen-host-nkey-job.yaml:59-102` | IMPLEMENTED | | Job present with `helm.sh/hook: pre-install,pre-upgrade`, `hook-weight: "-10"`, writes to `{release}-host-creds` secret, runs `control-plane gen-host-nkey` subcommand. |
| spec-helm.md:164 | Secret `{release}-host-creds`, single field `nkey_seed`. JWT is **not** stored here. | `host-creds-secret.yaml:1-22` | IMPLEMENTED | | Secret defined with `nkey_seed` field only. JWT storage is explicitly excluded. |
| spec-helm.md:166 (Pre-Install RBAC) | ServiceAccount / Role / RoleBinding for `gen-host-nkey` hook (weight `-20`) granting create/get Secret permission. | `gen-host-nkey-job.yaml:8-57` | IMPLEMENTED | | All three RBAC resources present with `hook-weight: "-20"`, grants `get` and `create` on secrets. |
| spec-helm.md:172-177 (Controller table) | Deployment `{release}-controller`, single replica, mounts `{release}-host-creds` at `/etc/mclaude/host-creds/`, env: `HUB_NATS_URL`, `CONTROL_PLANE_URL`, `HOST_NKEY_SEED_PATH=/etc/mclaude/host-creds/nkey_seed`. | `controller-deployment.yaml:5,11,35-40,56-78` | IMPLEMENTED | | All env vars present, volume mount at `/etc/mclaude/host-creds` with `readOnly: true`. |
| spec-helm.md:174 | Service `{release}-controller-metrics` (Prometheus scrape target). | `controller-service.yaml:1-21` | IMPLEMENTED | | Service renders as `{release}-controller-metrics`, ClusterIP, metrics port. |
| spec-helm.md:175 | ServiceAccount `{release}-controller`. | `serviceaccount.yaml:1-15` | IMPLEMENTED | | ServiceAccount rendered via `controllerServiceAccountName` helper. |
| spec-helm.md:176 | ClusterRole `{release}-controller`: grants namespace, deployment, PVC, secret, configmap, serviceaccount, role, rolebinding, and MCProject CRD management. | `clusterrole.yaml:1-51` | IMPLEMENTED | | All listed resource types present; also includes pods (read) and coordination.k8s.io leases (leader election). |
| spec-helm.md:177 | ClusterRoleBinding `{release}-controller`. | `clusterrolebinding.yaml:1-17` | IMPLEMENTED | | Present, binds ClusterRole to ServiceAccount. |
| spec-helm.md:178 | CRD `mcprojects.mclaude.io` — MCProject v1alpha1, Namespaced. | `mcproject-crd.yaml:1-99` | IMPLEMENTED | | CRD present, `scope: Namespaced`, group `mclaude.io`, version `v1alpha1`. |
| spec-helm.md:183-184 (Session-Agent Template table) | ConfigMap `{release}-session-agent-template` with fields: image, imagePullPolicy, terminationGracePeriodSeconds, resourcesJson, projectPvcSize, projectPvcStorageClass, nixPvcSize, nixPvcStorageClass, corporateCAEnabled, corporateCAConfigMapName, corporateCAConfigMapKey. | `session-agent-template.yaml:1-25` | IMPLEMENTED | | All 11 fields present and correctly mapped from `.Values`. |
| spec-helm.md:189 | `controlPlane.url` — Required, CP HTTP URL. | `values.yaml:23-25`; `controller-deployment.yaml:37-38` (uses `required` function). | IMPLEMENTED | | Value marked required in both doc and template via `required "controlPlane.url is required"`. |
| spec-helm.md:190 | `host.name` — Required, display name for NOTES.txt only. Controller derives slug from JWT. | `values.yaml:41-42`; `NOTES.txt:3,15` | IMPLEMENTED | | `host.name` used only in NOTES.txt. |
| spec-helm.md:191 | `host.hubNatsUrl` — Required, set as `HUB_NATS_URL` on controller Deployment. | `values.yaml:44-46`; `controller-deployment.yaml:35-36` (uses `required` function). | IMPLEMENTED | | Required guard present, env var `HUB_NATS_URL` set correctly. |
| spec-helm.md:192 | `controller.replicas` default `1`. | `values.yaml:58`; `controller-deployment.yaml:11`. | IMPLEMENTED | | Default 1, rendered into Deployment. |
| spec-helm.md:193 | `sessionAgent.image.*` — ghcr.io image. | `values.yaml:81-84` | IMPLEMENTED | | ghcr.io image with registry, repo, tag, pullPolicy. |
| spec-helm.md:194 | `sessionAgent.terminationGracePeriodSeconds` default `86400`. | `values.yaml:92`; `session-agent-template.yaml:15`. | IMPLEMENTED | | Default 86400. |
| spec-helm.md:195 | `sessionAgent.persistence.storageClass` default `""`. | `values.yaml:94-95`; `session-agent-template.yaml:19`. | IMPLEMENTED | | |
| spec-helm.md:196 | `sessionAgent.persistence.size` default `50Gi`. | `values.yaml:96`; `session-agent-template.yaml:18`. | IMPLEMENTED | | |
| spec-helm.md:197 | `sessionAgent.nix.storageClass` default `""`. | `values.yaml:98`; `session-agent-template.yaml:21`. | IMPLEMENTED | | |
| spec-helm.md:198 | `sessionAgent.nix.size` default `20Gi`. | `values.yaml:99`; `session-agent-template.yaml:22`. | IMPLEMENTED | | |
| spec-helm.md:199-202 | `sessionAgent.corporateCA.*` values (enabled, bundleName, configMapName, configMapKey). | `values.yaml:100-103`; `session-agent-template.yaml:23-25`. | PARTIAL | SPEC→FIX | `corporateCAConfigMapName` and `corporateCAConfigMapKey` are in template; `bundleName` is in values.yaml but **not** in `session-agent-template.yaml`. Spec lists `corporateCAConfigMapName` and `configMapKey` — both present. `bundleName` is declared in values but not rendered into the ConfigMap (no `corporatCABundleName` key in template). Spec lists all four fields but the ConfigMap only renders three. Minor: spec doesn't explicitly enumerate which fields go into the ConfigMap vs which are just Helm values, so this is a spec ambiguity. |
| spec-helm.md:204 | `controller.config.devOAuthToken` — injected as `oauth-token` in per-user `user-secrets` via `DEV_OAUTH_TOKEN` env. | `controller-deployment.yaml:51-54` | IMPLEMENTED | | `DEV_OAUTH_TOKEN` env var conditionally injected when non-empty. |
| spec-helm.md:207-217 (Single-Cluster Degenerate Install) | Install instructions using `nats://mclaude-cp-nats.mclaude-system.svc:4222` URL; then `kubectl logs job/mclaude-worker-gen-host-nkey` and `mclaude host register`. | `values-dev.yaml:14-15`; `NOTES.txt:9-16` | IMPLEMENTED | | Dev values match the doc example URL; NOTES.txt renders the post-install steps. |
| spec-helm.md:219-228 (Migration from Leaf-NATS) | `helm uninstall` then `helm install` with new values. Brief downtime, no in-place upgrade. | No template artifacts; this is an operator procedure documented in spec. | IMPLEMENTED | | The chart has no NATS StatefulSet or leaf-creds — the migration path is structurally enforced. |
| spec-helm.md:238-246 (Values files table) | Worker values files: `values.yaml`, `values-dev.yaml`, `values-airgap.yaml`, `values-aks.yaml` (worker). Spec also mentions `values-k3d-ghcr.yaml`. | All five files present in chart root. | IMPLEMENTED | | Chart has: values.yaml, values-dev.yaml, values-airgap.yaml, values-aks.yaml, values-k3d-ghcr.yaml. |
| ADR-0063:43 | Strip + retain worker chart as independently installable. Drop: `nats-statefulset.yaml`, `nats-configmap.yaml`, `nats-service.yaml`, `gen-leaf-creds-job.yaml`. Drop values `leafUrl`, `leafCreds`, `nats.*`. | Entire templates/ dir — none of these files exist. values.yaml has no `leafUrl`, `leafCreds`, or `nats.*` keys. | IMPLEMENTED | | All legacy leaf-NATS artifacts fully removed. |
| ADR-0063:43 / spec-helm.md:163 | Add `gen-host-nkey-job.yaml` (pre-install hook). | `templates/gen-host-nkey-job.yaml` | IMPLEMENTED | | Present. |
| ADR-0063:43 / spec-helm.md:164 | Add `host-creds-secret.yaml` (placeholder, populated by Job). | `templates/host-creds-secret.yaml` | IMPLEMENTED | | Present. |
| ADR-0063:43 / spec-helm.md (NOTES.txt) | Add `templates/NOTES.txt` (post-install operator instructions for `mclaude host register --nkey-public ...`). | `templates/NOTES.txt:1-26` | IMPLEMENTED | | Present with full operator instructions. |
| ADR-0063:43 | Add values.yaml keys: `controlPlane.url`, `host.name`, `host.hubNatsUrl`. | `values.yaml:23-46` | IMPLEMENTED | | All three present with Required comments. |
| ADR-0063:127 | Deployment mounts `{release}-host-creds` as volume at `/etc/mclaude/host-creds/`. Env: `HUB_NATS_URL`, `CONTROL_PLANE_URL`, `HOST_NKEY_SEED_PATH=/etc/mclaude/host-creds/nkey_seed`. | `controller-deployment.yaml:35-58,75-78` | IMPLEMENTED | | Volume mount at `/etc/mclaude/host-creds`, readOnly. All three env vars present. |
| ADR-0063:127 | `host.name` Helm value rendered into NOTES.txt only — controller does not read it for subscription scoping. | `NOTES.txt:3,15`; `controller-deployment.yaml` (no `host.name` env var) | IMPLEMENTED | | `host.name` appears only in NOTES.txt. Not injected as env var. |
| ADR-0063:127 / spec-helm.md: NOTES.txt | NOTES.txt instructs operator to read `kubectl logs job/{release}-gen-host-nkey` and run `mclaude host register --type cluster --name "$HOST_NAME" --nkey-public <key>`. | `NOTES.txt:9-16` | IMPLEMENTED | | Exact commands rendered with templated release name and `host.name` value. |
| ADR-0063:124 | ServiceAccount / Role / RoleBinding for gen-host-nkey hook at weight `-20`. | `gen-host-nkey-job.yaml:8-57` | IMPLEMENTED | | All three at weight `-20`. |
| ADR-0063:125 | Job at weight `-10`. Generates NKey via `nkeys.CreateUser()` (U-prefix). Writes decorated seed to Secret field `nkey_seed`. Idempotent — skips if Secret exists. | `gen-host-nkey-job.yaml:59-102` | IMPLEMENTED | | Weight `-10`. Runs `control-plane gen-host-nkey` subcommand (the binary implements idempotency). |
| ADR-0063:126 | Secret `{release}-host-creds`, single field `nkey_seed`. JWT not persisted. | `host-creds-secret.yaml:8-22` | IMPLEMENTED | | |
| ADR-0063:185-195 / spec controller-deployment changes | Drop env `NATS_ACCOUNT_SEED`, `JS_DOMAIN`, `CLUSTER_SLUG`, `NATS_CREDENTIALS_PATH`. Rename `NATS_URL` to `HUB_NATS_URL`. Drop `SESSION_AGENT_NATS_URL`. Add `HUB_NATS_URL`, `CONTROL_PLANE_URL`, `HOST_NKEY_SEED_PATH`. | `controller-deployment.yaml:34-54` | IMPLEMENTED | | Only `HUB_NATS_URL`, `CONTROL_PLANE_URL`, `HOST_NKEY_SEED_PATH` present. None of the dropped vars exist. |
| ADR-0063:185-195 | Drop volume + mount for `leaf-creds` Secret (path `/etc/nats/leaf-creds`). Add volume + mount for `{release}-host-creds` at `/etc/mclaude/host-creds/`. | `controller-deployment.yaml:55-78` | IMPLEMENTED | | Only `host-creds` volume exists; no `leaf-creds` volume. |
| ADR-0063:NOTES.txt behavior | NOTES.txt explains controller crashloop with clear log message if operator hasn't registered; retries every 5–60s exponential backoff. | `NOTES.txt:18-26` | IMPLEMENTED | | NOTES.txt describes exactly this behavior. |
| spec-helm.md:249-257 (Dependencies) | K8s 1.24+, RWO storage class, RWX for Nix PVCs on AKS, Ingress controller (cp only — workers do not expose public HTTP), pre-created Secrets, optional trust-manager, cert-manager + ExternalDNS for cp. | Chart has no Ingress templates (workers don't expose HTTP); no cp-only resources. | IMPLEMENTED | | Worker chart has zero Ingress resources. All CP-only items documented in cp section. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| `Chart.yaml:1-17` | INFRA | Standard Helm Chart.yaml — name, version, description, keywords, maintainers. Necessary boilerplate. |
| `templates/_helpers.tpl:1-125` | INFRA | Standard Helm template helpers: `fullname`, `name`, `chart`, `namespace`, `labels`, `selectorLabels`, `controllerServiceAccountName`, `image`, `imagePullSecrets`, `securityContext`, `podSecurityContext`. All helpers are referenced by other templates. |
| `templates/tests/test-smoke.yaml:1-47` | UNSPEC'd | Helm test pod that curls `/healthz` on the controller metrics service. The spec does not describe a Helm test resource, but a smoke test that verifies the controller health endpoint is a reasonable operational artifact. Not dead code — it runs with `helm test`. Spec should document it or this row is acceptable operator tooling. |
| `controller-deployment.yaml:41-54` (METRICS_ADDR, HEALTH_PROBE_ADDR, LOG_LEVEL, LEADER_ELECTION, SESSION_AGENT_TEMPLATE_CM, DEV_OAUTH_TOKEN) | INFRA | `METRICS_ADDR` and `HEALTH_PROBE_ADDR` are necessary for the liveness/readiness probes and metrics service. `LOG_LEVEL` is a standard operational knob. `LEADER_ELECTION` is gated by `controller.leaderElection` value (deferred per ADR-0063). `SESSION_AGENT_TEMPLATE_CM` passes the ConfigMap name to the controller binary. `DEV_OAUTH_TOKEN` is spec'd at spec-helm.md:204. All are necessary infrastructure. |
| `controller-deployment.yaml:59-73` (livenessProbe, readinessProbe) | INFRA | Liveness and readiness probes on port 8081 `/healthz` and `/readyz`. Standard K8s health probe pattern, referenced indirectly by the test-smoke.yaml pod. Not spec'd explicitly but mandatory for production-grade K8s operator deployment. |
| `host-creds-secret.yaml:16-21` (placeholder `nkey_seed: ""`) | INFRA | Placeholder empty value explained in comment — overwritten by the pre-install Job. Required so the Deployment volume mount doesn't fail if the Job pod hasn't completed when Helm renders the Secret. |
| `values.yaml:6-11` (global.imageRegistry, imagePullSecrets) | INFRA | Standard cross-chart air-gap and pull-secret support. Referenced by the `image` and `imagePullSecrets` helpers. |
| `values.yaml:12-13` (nameOverride, fullnameOverride) | INFRA | Standard Helm override knobs used by `_helpers.tpl`. |
| `values.yaml:15-17` (namespace.name) | INFRA | Namespace knob used by `mclaude-worker.namespace` helper. |
| `values.yaml:51-71` (controller.enabled, image, resources, service.metricsPort, config.logLevel) | INFRA | Standard controller configuration — image, resources, enable flag, log level. Referenced by controller-deployment.yaml and controller-service.yaml. |
| `values.yaml:108-113` (tests.curl.*) | INFRA | Image reference for the Helm test curl pod (test-smoke.yaml). |
| `values.yaml:115-122` (serviceAccount.*) | INFRA | ServiceAccount creation toggle and annotation overrides. Standard Helm SA management pattern. |
| `values-k3d-ghcr.yaml:1-36` | INFRA | Local k3d preview variant using ghcr.io images. Consistent with spec-helm.md:243 "Local k3d preview with ghcr.io images." |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-helm.md:130 | Chart is hub-direct, no local NATS StatefulSet | No unit test | `test-smoke.yaml` (Helm test) | E2E_ONLY | Helm test verifies controller health endpoint after real install. No unit test (Helm charts aren't unit-testable in the traditional sense without helm-unittest). |
| spec-helm.md:163-167 | gen-host-nkey Job writes seed to Secret, idempotent | No unit test | No integration test for the Job specifically | UNTESTED | The Job runs `control-plane gen-host-nkey` — testability of that subcommand lives in the control-plane binary tests. No Helm test verifies the Secret was created with correct content. |
| spec-helm.md:172-177 | Controller Deployment with correct env vars and mounts | No unit test | `test-smoke.yaml` verifies controller starts and health endpoint responds | E2E_ONLY | The smoke test validates the controller boots. Env var correctness would require `helm template` assertions (not present). |
| spec-helm.md:183-184 | session-agent-template ConfigMap fields | No unit test | No integration test | UNTESTED | No test verifies the ConfigMap fields are correctly rendered from values. |
| spec-helm.md:176 | ClusterRole grants correct permissions | No unit test | No integration test | UNTESTED | No `helm-unittest` or integration test verifies the RBAC rules match what the controller binary actually needs. |

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` reference `charts/mclaude-worker` or `mclaude-controller-k8s`.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No matching bugs found. |

### Summary

- Implemented: 37
- Gap: 0
- Partial: 1
- Infra: 12
- Unspec'd: 1
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 2
- Untested: 3
- Bugs fixed: 0
- Bugs open: 0
