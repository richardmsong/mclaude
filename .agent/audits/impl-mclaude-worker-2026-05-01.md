## Run: 2026-05-01T00:00:00Z

Component: `charts/mclaude-worker`
Specs: `docs/adr-0063-k8s-architecture-spec.md`, `docs/charts-mclaude/spec-helm.md`

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0063:43 | Worker chart fate: Strip + retain as independently installable chart. Drop NATS StatefulSet/ConfigMap/Service, gen-leaf-creds-job, leafCreds/leafUrl/nats.* values | No nats-statefulset.yaml, nats-configmap.yaml, nats-service.yaml, gen-leaf-creds-job.yaml in charts/mclaude-worker/templates/. No leafUrl/leafCreds/nats.* keys in values.yaml | IMPLEMENTED | | All leaf-NATS artifacts removed |
| adr-0063:70 | Pre-install Job `gen-host-nkey` generates NKey pair, writes seed to Secret `{release}-host-creds`, prints public key to Job log + NOTES.txt | gen-host-nkey-job.yaml:59-102 | IMPLEMENTED | | Job exists with correct name pattern |
| adr-0063:124 (Helm chart artifacts table) | Job hook weight `-10` | gen-host-nkey-job.yaml:69 | GAP | SPEC→FIX | The RBAC SA/Role/RoleBinding resources use weight `-20`; the Job itself uses weight `-10` per spec. This is actually correct — the SA/Role/RoleBinding must exist before the Job runs. The spec says weight -10 for the Job; code has -10 on the Job (line 69) and -20 on the supporting RBAC. This matches intent, but spec only mentions one weight (-10) for the whole Job block. Spec is ambiguous; code's split is reasonable. |
| adr-0063:124 | Job: "Idempotent — skips if Secret exists" | gen-host-nkey-job.yaml:89 — runs `control-plane gen-host-nkey` with env `HOST_CREDS_SECRET`; idempotency is in binary logic | IMPLEMENTED | | Idempotency is in the binary; the Job template passes `HOST_CREDS_SECRET` env pointing to the secret name |
| adr-0063:125 | Secret `{release}-host-creds`, single field `nkey_seed` (decorated S-prefix seed string). JWT is NOT persisted here. | host-creds-secret.yaml:10-21 | IMPLEMENTED | | Secret has single data field `nkey_seed: ""`; JWT field absent |
| adr-0063:126 | Deployment `{release}-controller-k8s`, mounts `{release}-host-creds` at `/etc/mclaude/host-creds/`, env: `HUB_NATS_URL`, `CONTROL_PLANE_URL`, `HOST_NKEY_SEED_PATH=/etc/mclaude/host-creds/nkey_seed` | controller-deployment.yaml:35-40, 55-58, 75-78 | IMPLEMENTED | | Deployment name is `{release}-controller` (not `{release}-controller-k8s`); see notes |
| adr-0063:126 | Deployment name `{release}-controller-k8s` | controller-deployment.yaml:5 — name is `{{ include "mclaude-worker.fullname" . }}-controller` | GAP | SPEC→FIX | ADR says `{release}-controller-k8s` but code uses `{release}-controller`. Spec-helm.md line 173 also says `{release}-controller` (matching code). The spec-helm.md is the authoritative template spec; ADR appears to use a more verbose name. The code matches spec-helm.md. ADR name should be corrected to match. |
| adr-0063:126 | `host.name` Helm value rendered into NOTES.txt only — controller does not read it for subscription scoping | NOTES.txt:3 — `Host name: {{ .Values.host.name }}` only; no env var for host.name on Deployment | IMPLEMENTED | | host.name not injected as env var to controller |
| adr-0063:128 | ConfigMap `{release}-session-agent-template` — static pod template values (image, imagePullPolicy, terminationGracePeriodSeconds, resourcesJson, projectPvcSize, projectPvcStorageClass, nixPvcSize, nixPvcStorageClass, corporateCAEnabled, corporateCAConfigMapName, corporateCAConfigMapKey) | session-agent-template.yaml:12-24 | IMPLEMENTED | | All 11 fields present |
| adr-0063:128 | ConfigMap includes `corporateCAConfigMapName` field | session-agent-template.yaml:23 | IMPLEMENTED | | Present |
| adr-0063:128 | ConfigMap does NOT include `corporateCABundleName` / `bundleName` as a separate field written to ConfigMap | session-agent-template.yaml: bundleName is in values.yaml:98 but NOT written to the ConfigMap | IMPLEMENTED | | bundleName in values but not propagated to ConfigMap — consistent with spec which only lists the 11 enumerated fields |
| adr-0063:128 | ClusterRole / ClusterRoleBinding `{release}-controller` — manages Namespace, Deployment, PVC, Secret, MCProject CRs | clusterrole.yaml:1-51, clusterrolebinding.yaml:1-17 | IMPLEMENTED | | Exact match |
| adr-0063:129 | NOTES.txt instructs operator to read public key from Job log and run `mclaude host register --type cluster --name "$HOST_NAME" --nkey-public <key>` | NOTES.txt:9-17 | IMPLEMENTED | | Instructions present with correct command |
| spec-helm.md:166 | Job `{release}-gen-host-nkey` pre-install hook, weight `-10` | gen-host-nkey-job.yaml:68-69 | IMPLEMENTED | | |
| spec-helm.md:167 | Secret `{release}-host-creds`, single field `nkey_seed` | host-creds-secret.yaml | IMPLEMENTED | | |
| spec-helm.md:173 | Deployment `{release}-controller`, mounts `{release}-host-creds` at `/etc/mclaude/host-creds/`, env: `HUB_NATS_URL`, `CONTROL_PLANE_URL`, `HOST_NKEY_SEED_PATH=/etc/mclaude/host-creds/nkey_seed` | controller-deployment.yaml:35-40, 55-58, 75-78 | IMPLEMENTED | | |
| spec-helm.md:176 | ClusterRole `{release}-controller`: namespaces, deployment, PVC, secret, configmap, serviceaccount, role, rolebinding, MCProject CRDs | clusterrole.yaml:12-50 | IMPLEMENTED | | |
| spec-helm.md:177 | ClusterRoleBinding `{release}-controller` | clusterrolebinding.yaml | IMPLEMENTED | | |
| spec-helm.md:178 | CRD `mcprojects.mclaude.io`, MCProject v1alpha1, Namespaced | mcproject-crd.yaml:1-99 | IMPLEMENTED | | |
| spec-helm.md:184 | ConfigMap `{release}-session-agent-template` (static pod template values) | session-agent-template.yaml | IMPLEMENTED | | |
| spec-helm.md:190 | `controlPlane.url` — Required; CP HTTP URL | values.yaml:24-25; controller-deployment.yaml:38 | IMPLEMENTED | | `required` guard in template |
| spec-helm.md:191 | `host.name` — Required; display name in NOTES.txt only | values.yaml:41; NOTES.txt:3 | IMPLEMENTED | | |
| spec-helm.md:192 | `host.hubNatsUrl` — Required; set as `HUB_NATS_URL` on controller | values.yaml:45-46; controller-deployment.yaml:35-36 | IMPLEMENTED | | |
| spec-helm.md:193 | `controller.replicas` default `1` | values.yaml:60 | IMPLEMENTED | | |
| spec-helm.md:194-203 | `sessionAgent.*` knobs: image, terminationGracePeriodSeconds, persistence, nix, corporateCA | values.yaml:76-100 | IMPLEMENTED | | |
| spec-helm.md:194 | `sessionAgent.terminationGracePeriodSeconds` default `86400` | values.yaml:89 — default is `120`, not `86400` | GAP | SPEC→FIX | Spec says default 86400 (24h); code has 120 (2 min). The code's choice (120s for dev) is arguably reasonable as a non-production default. Spec should be updated, OR the spec default should match production intent. This is a spec-vs-code mismatch in default value. |
| spec-helm.md:204 | `controller.config.devOAuthToken` — injected as `oauth-token` in per-user user-secrets via DEV_OAUTH_TOKEN env | values.yaml:71; controller-deployment.yaml:51-54 | IMPLEMENTED | | |
| spec-helm.md:128-130 | No local NATS StatefulSet, no leaf-node credentials | No nats-statefulset.yaml, no nats-service.yaml, no nats-configmap.yaml in worker templates | IMPLEMENTED | | |
| spec-helm.md:240 | `values-dev.yaml` (worker): Local k3d, single-cluster degenerate install | values-dev.yaml | IMPLEMENTED | | |
| spec-helm.md:241 | `values-airgap.yaml` (worker) | values-airgap.yaml | IMPLEMENTED | | |
| spec-helm.md:242 | `values-aks.yaml` (worker) | values-aks.yaml | IMPLEMENTED | | |
| adr-0063:185-195 | `templates/controller-deployment.yaml` changes: drop volume+mount for leaf-creds, drop NATS_ACCOUNT_SEED, JS_DOMAIN, CLUSTER_SLUG, NATS_CREDENTIALS_PATH, SESSION_AGENT_NATS_URL; add volume+mount for `{release}-host-creds` at `/etc/mclaude/host-creds/`; add HUB_NATS_URL, CONTROL_PLANE_URL, HOST_NKEY_SEED_PATH; rename NATS_URL → HUB_NATS_URL | controller-deployment.yaml — none of the old env vars present; new envs HUB_NATS_URL, CONTROL_PLANE_URL, HOST_NKEY_SEED_PATH present at lines 35-40; volume+mount at lines 55-58, 75-78 | IMPLEMENTED | | |
| adr-0063:43 | Add `values.yaml` keys: `controlPlane.url`, `host.name`, `host.hubNatsUrl` | values.yaml:24-46 | IMPLEMENTED | | |
| adr-0063:184 | Add `templates/gen-host-nkey-job.yaml` | gen-host-nkey-job.yaml | IMPLEMENTED | | |
| adr-0063:184 | Add `templates/host-creds-secret.yaml` | host-creds-secret.yaml | IMPLEMENTED | | |
| adr-0063:184 | Add `templates/NOTES.txt` | NOTES.txt | IMPLEMENTED | | |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| Chart.yaml:4 | UNSPEC'd | Description still says "worker NATS (leaf-linked to hub)" — stale text from before ADR-0063. Should say "hub-direct, independently installable mclaude-controller-k8s operator and session-agent template." |
| Chart.yaml:13 | UNSPEC'd | keywords still includes "nats" — not wrong per se but a cosmetic remnant of the old leaf-NATS description. Low severity. |
| gen-host-nkey-job.yaml:1-19 (SA/Role/RoleBinding hooks at weight -20) | INFRA | Supporting RBAC for the gen-host-nkey Job. Spec says the Job has weight -10; these infra resources use -20 so they exist before the Job runs. Necessary plumbing not explicitly spec'd but required for Job execution. |
| controller-deployment.yaml:42-43 (METRICS_ADDR env) | INFRA | Standard metrics-server config. Not spec'd but necessary for Prometheus scrape target which is spec'd (spec-helm.md:174). |
| controller-deployment.yaml:44-45 (HEALTH_PROBE_ADDR env) | INFRA | Necessary for liveness/readiness probes (controller-deployment.yaml:59-72). Required for K8s liveness probe behavior spec'd in ADR-0063:160. |
| controller-deployment.yaml:46-47 (LOG_LEVEL env) | INFRA | Standard operational knob; consistent with cp chart pattern. Not in the ADR-0063 controller env list but harmless operational config. |
| controller-deployment.yaml:48-49 (LEADER_ELECTION env hardcoded "true") | UNSPEC'd | ADR-0063 Deferred section (line 278) says "HA controller (multiple replicas with leader election) — LEADER_ELECTION env var is set in Helm but not read by the binary today." This env is present in the chart but the ADR marks it as deferred/not-yet-read. It's not harmful but is infrastructure for a deferred feature. |
| controller-deployment.yaml:50-51 (SESSION_AGENT_TEMPLATE_CM env) | INFRA | Points to the session-agent-template ConfigMap. Required for the controller to discover the template; not enumerated in ADR-0063's env list but necessary for spec'd behavior (session-agent template ConfigMap). |
| controller-deployment.yaml:59-72 (liveness/readiness probes) | INFRA | Standard K8s health probes. ADR-0063:160 references liveness probe as the expected failure mode surface. |
| controller-service.yaml | INFRA | Metrics service for Prometheus; spec'd in spec-helm.md:174. |
| serviceaccount.yaml | INFRA | Controller ServiceAccount; spec'd in spec-helm.md:175. |
| _helpers.tpl | INFRA | Standard Helm template helpers (fullname, labels, image resolution, security contexts). Not spec'd line-by-line but necessary plumbing. |
| values.yaml:114-118 (serviceAccount.create/annotations/name) | INFRA | Standard Helm SA create toggle. Not spec'd but necessary for serviceaccount.yaml conditional logic. |
| values.yaml:104-110 (tests.curl image config) | INFRA | Helm test support; standard pattern. Not spec'd in ADR-0063 but present in templates/tests/test-smoke.yaml. |
| templates/tests/test-smoke.yaml | INFRA | Helm test hook; standard operational plumbing. Not spec'd in ADR-0063. |

### Phase 3 — Test Coverage

Helm charts have no unit tests (no helm unittest framework in use). The only test artifact is a Helm test hook (test-smoke.yaml) that verifies the controller health endpoint post-deploy. No unit test framework for Go template rendering is present.

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| adr-0063:70 / spec-helm.md:166 | gen-host-nkey pre-install Job existence and behavior | None | None (Helm test only checks /healthz) | UNTESTED | No helm-unittest or equivalent tests verify Job manifest correctness |
| spec-helm.md:173 | Controller Deployment env vars (HUB_NATS_URL, CONTROL_PLANE_URL, HOST_NKEY_SEED_PATH) | None | None | UNTESTED | No template unit tests verify correct env var injection |
| spec-helm.md:176-177 | ClusterRole/ClusterRoleBinding existence | None | None | UNTESTED | No RBAC manifest tests |
| spec-helm.md:178 | MCProject CRD | None | None | UNTESTED | No CRD schema tests |
| spec-helm.md:184 | session-agent-template ConfigMap fields | None | None | UNTESTED | No template rendering tests verify all 11 fields |
| spec-helm.md:190-192 | Required values enforcement (controlPlane.url, host.name, host.hubNatsUrl) | None | None | UNTESTED | No tests verify `required` guards fire correctly |
| All chart resources | Post-deploy smoke | N/A | Helm test hook (test-smoke.yaml) — hits /healthz on controller-metrics service | E2E_ONLY | Smoke test only checks health endpoint, not NATS connectivity or NKey flow |

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` have `charts/mclaude-worker` or `mclaude-worker` as their component. All four open bugs (002-droid-shield, 002-ptt-race-condition, 003-initial-message-injection-race, 005-getting-started-canned-text) are in unrelated components (spa, droid-shield).

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No bugs with matching component |

### Summary

- Implemented: 30
- Gap: 3
- Partial: 0
- Infra: 11
- Unspec'd: 3
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 1 (smoke test)
- Untested: 6
- Bugs fixed: 0
- Bugs open: 0

**Gaps:**

1. `GAP [SPEC→FIX]`: ADR-0063 line 124 — spec says Job hook weight `-10` for the single Job block, but the implementation correctly splits into RBAC SA/Role/RoleBinding at weight `-20` and the Job at `-10`. The spec is ambiguous (only mentions one weight for the entire Job + RBAC block). The code's approach is correct and necessary; the ADR should clarify that supporting RBAC resources run at weight `-20`.

2. `GAP [SPEC→FIX]`: ADR-0063 line 126 — spec says Deployment name `{release}-controller-k8s`; code uses `{release}-controller` (matching `spec-helm.md:173`). The spec-helm.md is authoritative for template naming; ADR-0063 used a longer variant name. Update ADR-0063 line 126 to say `{release}-controller`.

3. `GAP [SPEC→FIX]`: spec-helm.md line 194 — `sessionAgent.terminationGracePeriodSeconds` default is documented as `86400` (24h) but values.yaml has `120` (2 min). Update spec-helm.md to reflect the actual default of `120`, or update values.yaml to `86400` if production intent was 24h.

**Unspec'd:**

1. `UNSPEC'd`: Chart.yaml:4 — description still contains "worker NATS (leaf-linked to hub)" — stale text from before ADR-0063. Should be updated to reflect hub-direct, independently installable topology.

2. `UNSPEC'd`: Chart.yaml:13 — `keywords` still includes "nats" as a remnant of the old leaf-NATS topology. Minor but stale.

3. `UNSPEC'd`: controller-deployment.yaml:48-49 — `LEADER_ELECTION` env hardcoded to `"true"`. ADR-0063 deferred section acknowledges this env is set but not read by the binary. Plumbing for a deferred feature; not harmful but should either be documented as deferred infra or gated by a values flag.
