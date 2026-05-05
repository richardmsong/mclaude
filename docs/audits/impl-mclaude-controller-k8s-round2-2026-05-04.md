# Implementation Audit: mclaude-controller-k8s (Round 2)

**Component**: mclaude-controller-k8s  
**Component root**: `mclaude-controller-k8s/`  
**Specs**: `docs/mclaude-controller-k8s/spec-k8s-architecture.md`, `docs/spec-state-schema.md`, `docs/adr-0091-per-user-anthropic-token.md`  
**Date**: 2026-05-04

## Run: 2026-05-04T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-k8s-architecture.md:3 | "K8s controller… scope is per-user namespace setup — Namespace, RBAC, user-agent-key Secret… user-agent Deployment. Does NOT create per-project resources" | main.go:84, user_reconciler.go:77-139 | IMPLEMENTED | — | No MCProject CRD, no per-project Deployments/PVCs |
| spec-k8s-architecture.md:11 | "MCProject CRD reconciliation, per-project Deployments, per-project PVCs, agent-nkey-{projectId} Secret, session-agent pod creation — REMOVED" | main.go:9-12, nats_subscriber.go:14-15, controller_test.go:54 | IMPLEMENTED | — | All project-pod paths removed |
| spec-k8s-architecture.md:12 | "DEV_OAUTH_TOKEN env var, oauth-token field in user-secrets — Removed entirely" | main.go:53, user_reconciler.go:359-391 | IMPLEMENTED | — | oauth-token and nats-creds stale-key removal on existing secrets |
| spec-k8s-architecture.md:16 | "Subscribe to mclaude.hosts.{hslug}.grants.create — triggers per-user provisioning: generate user-agent NKey, write user-agent-key Secret, register pubkey via mclaude.hosts.{hslug}.api.agents.user-agent.register, create namespace + user-agent Deployment + RBAC" | nats_subscriber.go:38-46, 55-56, 80-103; user_reconciler.go:77-139 | IMPLEMENTED | — | Full provisioning triggered on grants.create |
| spec-k8s-architecture.md:22 | "Single Deployment in mclaude-system on the worker cluster. Built with kubebuilder (controller-runtime). Leader election flag configurable via LEADER_ELECTION env var. v1 runs a single replica." | main.go:94-101 | IMPLEMENTED | — | LeaderElection env var respected; single Deployment |
| spec-k8s-architecture.md:26-36 | Boot sequence: (1) Read NKey seed from HOST_NKEY_SEED_PATH (2) NewHostAuthFromSeed (3) Refresh() with retry on ErrNotRegistered, log registration instruction, retry every 5s exponential backoff capped 60s (4) Decode hslug from JWT (5) Connect hub NATS (6) Subscribe mclaude.hosts.{hslug}.> (7) Background JWT refresh loop | main.go:59,68-83,76,117-128; bootRefreshWithInterval:168-196 | IMPLEMENTED | — | All 7 boot steps present; backoff initial=5s cap=60s |
| spec-k8s-architecture.md:28 | "If CP returns 404 (NKey not registered), log: 'NKey <pubkey> not registered with control-plane. To complete registration run: mclaude host register…' and retry every 5s with exponential backoff capped at 60s" | main.go:168-196 (bootRefreshWithInterval); hostauth package handles 404→ErrNotRegistered | IMPLEMENTED | — | ErrNotRegistered path retries with exponential backoff 5s→60s |
| spec-k8s-architecture.md:71-79 | Configuration table: HUB_NATS_URL (required), CONTROL_PLANE_URL (required), HOST_NKEY_SEED_PATH (optional, default /etc/mclaude/host-creds/nkey_seed), HELM_RELEASE_NAME (optional, default mclaude-worker), METRICS_ADDR (default :8082), HEALTH_PROBE_ADDR (default :8081), LEADER_ELECTION (default false) | main.go:54-60, 94-101 | IMPLEMENTED | — | All env vars present with correct defaults |
| spec-k8s-architecture.md:81-86 | "Dropped from prior spec: NATS_ACCOUNT_SEED, NATS_CREDENTIALS_PATH, JS_DOMAIN, CLUSTER_SLUG, SESSION_AGENT_NATS_URL/NATS_URL" | main.go (no references to any of these) | IMPLEMENTED | — | All dropped env vars absent from code |
| spec-k8s-architecture.md:90-96 | "Subscribes via host JWT to mclaude.hosts.{hslug}.> (zero JetStream access). Messages dispatched by subject suffix: grants.create → per-user provisioning; users.*.projects.*.create REMOVED; users.*.projects.*.delete REMOVED; users.*.projects.*.update → re-reconcile user-secrets" | nats_subscriber.go:38-46, 53-75 | IMPLEMENTED | — | Single wildcard sub; grants.create and projects.update handled; create/delete absent |
| spec-k8s-architecture.md:103-108 | "Per-user namespace mclaude-{userSlug}; user-agent-key Secret; user-secrets Secret (no oauth-token); user-config ConfigMap; user-agent Deployment + ServiceAccount + Role + RoleBinding; Watched: {release}-user-agent-template ConfigMap → changes trigger re-rollout of all user-agent Deployments" | user_reconciler.go:77-139, 151-180 | PARTIAL | SPEC→FIX | The "Watched: … changes trigger re-rollout" language in the Kubernetes Resources summary is inconsistent with the Reconciler Loop section 5b which correctly marks cross-namespace fan-out as deferred. Code reads the template on each reconcile (triggered by grants.create) but does NOT set up a controller-runtime watch. The "Watched" bullet should be replaced with the same "deferred" language in section 5b. |
| spec-k8s-architecture.md:114 | "On each mclaude.hosts.{hslug}.grants.create reception (and on resync):" | nats_subscriber.go:80-103, user_reconciler.go:77-139 | PARTIAL | SPEC→FIX | Code has NO periodic resync; the Error Handling table (line 154) explicitly says "no periodic resync". The phrase "(and on resync)" in the section header is a stale qualifier that contradicts the error handling text and the code. Should be removed. |
| spec-k8s-architecture.md:115 | "1. Load the user-agent template ConfigMap for image, resource limits, and ServiceAccount config." | user_reconciler.go:82, 151-180 | IMPLEMENTED | — | loadUserAgentTemplate() reads {release}-user-agent-template from mclaude-system |
| spec-k8s-architecture.md:116 | "2. Ensure namespace mclaude-{userSlug} (ADR-0062: derived from userSlug, not UUID), labels mclaude.io/user-id={userId} (when CP supplies it; otherwise just mclaude.io/managed=true)" | user_reconciler.go:207-236 | PARTIAL | SPEC→FIX | The spec-k8s-architecture.md still says mclaude.io/user-id={userId} but spec-state-schema.md:519 explicitly states this label was dropped by ADR-0091. The code correctly applies only mclaude.io/managed=true. The spec-k8s-architecture.md line 117 should be updated to remove the user-id label reference. |
| spec-k8s-architecture.md:118 | "3. Ensure ServiceAccount + Role + RoleBinding for both mclaude-sa and mclaude-user-agent. mclaude-user-agent Role grants verbs on Deployment, PersistentVolumeClaim, Secret (only user-tokens-{uslug}), ConfigMap (only session-agent-template). No ClusterRole or cross-namespace permissions." | user_reconciler.go:241-300 | IMPLEMENTED | — | All four resources in Role; ResourceNames scoped correctly; no ClusterRole |
| spec-k8s-architecture.md:119 | "4. Ensure user-secrets Secret (CLI helper configs only — no token field)." | user_reconciler.go:358-391 | IMPLEMENTED | — | No oauth-token; stale key removal on existing secrets |
| spec-k8s-architecture.md:120 | "5. Ensure user-config ConfigMap (Claude Code workspace settings)." | user_reconciler.go:393-411 | IMPLEMENTED | — | ensureUserConfig creates empty ConfigMap if absent |
| spec-k8s-architecture.md:121 | "5b. Copy {release}-session-agent-template from mclaude-system into user namespace as session-agent-template. Copy refreshes on grants.create re-delivery; cross-namespace fan-out is deferred — operators re-trigger by re-publishing grants.create or kubectl rollout restart." | user_reconciler.go:413-450 | IMPLEMENTED | — | ensureSessionAgentTemplate copies on create; updates if data changed; no-op if source absent |
| spec-k8s-architecture.md:122 | "6. Ensure user-agent-key Secret. If absent or missing nkey-seed: generate NKey via nkeys.CreateUser(), write decorated seed to nkey-seed field, then NATS request mclaude.hosts.{hslug}.api.agents.user-agent.register {uslug, nkey_public}. Idempotent — skip if seed exists." | user_reconciler.go:484-548 | IMPLEMENTED | — | Idempotent check on existing seed; NKey generated; NATS registration request sent |
| spec-k8s-architecture.md:123 | "6b. Ensure empty user-tokens-{uslug} Secret (no data keys yet) so user-agent's volume mount resolves. Controller never writes token material. Idempotent: if Secret already exists (with or without populated keys), skip." | user_reconciler.go:579-601 | IMPLEMENTED | — | ensureUserTokensSecret creates empty Secret; skips if already exists |
| spec-k8s-architecture.md:124 | "7. Ensure user-agent Deployment (single replica, RollingUpdate). Pod mounts: user-agent-key Secret at /secrets/agent/, user-tokens-{uslug} Secret at /secrets/tokens/. Env: USER_SLUG, HOST_SLUG, NAMESPACE, NATS_URL (= HUB_NATS_URL), CONTROL_PLANE_URL. ImagePullSecrets copied from controller's namespace." | user_reconciler.go:667-814 | IMPLEMENTED | — | All mounts, env vars, imagePullSecrets present |
| spec-k8s-architecture.md:125 | "8. Copy imagePullSecrets from controller's namespace into user namespace." | user_reconciler.go:603-629 | IMPLEMENTED | — | copyImagePullSecrets copies dockerconfigjson Secrets |
| spec-k8s-architecture.md:137-140 | "Corporate CA Support: when session-agent-template ConfigMap has corporateCAEnabled: true: (1) Adds mclaude.io/user-namespace: true label to user namespaces for trust-manager targeting. (2) Injects corporate-ca volume, mount at /etc/ssl/certs/corporate-ca-certificates.crt, NODE_EXTRA_CA_CERTS env var. (3) Annotates pod template with mclaude.io/ca-bundle-hash so CA rotations trigger a Recreate rollout." | user_reconciler.go:207-236 (namespace label), 709-757 (volume/mount/env/annotation) | PARTIAL | SPEC→FIX | The spec says CA rotations trigger a "Recreate" rollout. The code uses RollingUpdate strategy (line 803). The hash annotation change triggers a rolling update per the configured strategy, not Recreate. Code behavior (hash-driven RollingUpdate) is correct; spec wording "Recreate" should be changed to "rolling update" or just "pod template rollout". |
| spec-k8s-architecture.md:154 | "User-agent provisioning fails (RBAC, quota, image pull): Logs error. Controller retries only on re-delivery of mclaude.hosts.{hslug}.grants.create (no periodic resync)." | nats_subscriber.go:96-98 (error logged, no retry loop) | IMPLEMENTED | — | Error logged; no periodic resync; retry on re-delivery |
| spec-k8s-architecture.md:170 | "Dependencies: Kubernetes API for Namespace, Deployment, PVC, Secret, ConfigMap, RBAC, and MCProject CRD." | main.go:85-88 | PARTIAL | SPEC→FIX | MCProject CRD dependency line is stale — the CRD was removed by ADR-0091. Code registers only standard client-go scheme (no custom CRD). The Dependencies section should drop "and MCProject CRD". |
| spec-state-schema.md:635 | "mclaude-user-agent Role permits … get/list/watch on ConfigMap (project-pod template snapshot)" | user_reconciler.go:288-291 | PARTIAL | SPEC→FIX | Code grants only get on ConfigMap (ResourceNames: ["session-agent-template"]). State schema says get/list/watch. list/watch on a named-resource-scoped Rule is non-standard Kubernetes RBAC behavior — ResourceNames filtering doesn't apply to watch on list endpoints. get-only is the correct implementation. State schema should be corrected to get only. |
| adr-0091:272-280 (Component Changes) | "Creates empty user-tokens-{uslug} Secret in user namespace (no data keys; populated later by user-agent on token delivery). This ensures user-agent pod's required volume mount resolves on startup." | user_reconciler.go:579-601 | IMPLEMENTED | — | ensureUserTokensSecret creates empty Secret; idempotent |
| adr-0091:276-278 | "Copies {release}-session-agent-template ConfigMap from mclaude-system into user namespace as session-agent-template. Re-copied on Helm upgrade and user-agent re-rolled." | user_reconciler.go:413-450 | IMPLEMENTED | — | Copy-on-create + update-on-change implemented |
| adr-0091:278 | "Creates user namespace mclaude-{uslug} + RBAC (ServiceAccount, namespace-scoped Role granting verbs on Deployment, PersistentVolumeClaim, Secret (user-tokens-{uslug} only), ConfigMap (session-agent-template only) for mclaude-user-agent) + deploys user-agent Deployment on grant." | user_reconciler.go:241-300, 667-814 | IMPLEMENTED | — | Full RBAC and Deployment ensure per grant |
| adr-0091:279 | "Session pod creation removed — delegated to user-agent." | nats_subscriber.go:14-15; no session pod code anywhere in mclaude-controller-k8s | IMPLEMENTED | — | Zero session pod creation code |
| adr-0091:280 | "Removes all devOAuthToken / oauth-token writes from user-secrets reconciler paths." | user_reconciler.go:359-391 | IMPLEMENTED | — | Stale key removal + no new writes |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-13 | INFRA | Package comment, import declarations — standard Go boilerplate |
| main.go:38-40 | INFRA | ctrl.SetLogger() setup required before NewManager |
| main.go:42-50 | INFRA | LOG_LEVEL env var parsing + zerolog logger setup — operational plumbing |
| main.go:90-112 | INFRA | controller-runtime manager setup (scheme, metrics, healthz, readyz) — standard controller plumbing |
| main.go:114-115 | INFRA | detectNamespace() call — determines controller's own namespace |
| main.go:160-165 | INFRA | refresher interface definition — enables test injection of fake, standard Go interface pattern |
| main.go:199-220 | INFRA | hostSlugFromJWT() helper — necessary to decode hslug per boot step 4 (spec line 34) |
| main.go:222-230 | INFRA | detectNamespace() — reads /var/run/secrets/kubernetes.io/serviceaccount/namespace; falls back to mclaude-system |
| main.go:232-237 | INFRA | envOr() helper — standard env-with-default pattern used throughout |
| nats_subscriber.go:123-142 | INFRA | matchesProjectsUpdate() / extractProjectsUSlug() helpers — necessary parsing for users.*.projects.*.update routing |
| nats_subscriber.go:144-168 | INFRA | replyOK() / replyError() helpers — standard request/reply plumbing for projects.update handler |
| user_reconciler.go:37-41 | INFRA | natsConn interface — test injection interface for *nats.Conn, standard Go pattern |
| user_reconciler.go:141-149 | INFRA | userAgentTpl struct — holds template values read from ConfigMap; necessary plumbing |
| user_reconciler.go:182-204 | INFRA | defaultUserAgentTemplate() + applyDefaultUserAgentResources() — fallback defaults when ConfigMap absent; spec says "falls back to defaults" |
| user_reconciler.go:304-356 | INFRA | ensureServiceAccount() / ensureRole() / ensureRoleBinding() helpers — create-or-update patterns for RBAC; necessary for spec'd RBAC ensure steps |
| user_reconciler.go:452-482 | INFRA | configMapDataEqual() / configMapBinaryDataEqual() — equality helpers for session-agent-template update check; necessary plumbing |
| user_reconciler.go:550-577 | INFRA | registerUserAgentNKey() — NATS request/reply to cp.register; implements spec step 6's registration requirement |
| user_reconciler.go:631-659 | INFRA | caBundleHash() — SHA-256 hash over ConfigMap data for CA rotation annotation; implements Corporate CA Support |
| nkeys.go:1-7 | INFRA | Placeholder file explaining JWT issuance removal per ADR-0063; no production code |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| spec-k8s-architecture.md:26-36 (boot sequence + retry) | Boot: Refresh() with exponential backoff, initial 5s, cap 60s, retry on ErrNotRegistered | TestBootRefresh_SucceedsOnFirstTry, TestBootRefresh_RetriesOnErrNotRegistered, TestBootRefresh_RetriesOnGenericError, TestBootRefresh_ExponentialBackoffCapAtMax | None | UNIT_ONLY |
| spec-k8s-architecture.md:34 (hslug decode) | "Decode the host slug from the received JWT" | TestHostSlugFromJWT (6 cases including error cases) | None | UNIT_ONLY |
| spec-k8s-architecture.md:90-96 (NATS dispatch) | "Subscribe mclaude.hosts.{hslug}.> dispatch by suffix: grants.create, projects.update" | TestMatchesProjectsUpdate, TestExtractProjectsUSlug, TestSingleNATSSubscription | None | UNIT_ONLY |
| spec-k8s-architecture.md:116 (namespace) | "Ensure namespace mclaude-{userSlug} with mclaude.io/managed=true label" | TestEnsureNamespace_CreatesWithManagedLabel, TestEnsureNamespace_Idempotent | None | UNIT_ONLY |
| spec-k8s-architecture.md:137 (corporate CA namespace label) | "Adds mclaude.io/user-namespace: true label when corporateCAEnabled" | TestEnsureNamespace_CorporateCALabel | None | UNIT_ONLY |
| spec-k8s-architecture.md:118 (RBAC) | "ServiceAccount + Role + RoleBinding for mclaude-sa and mclaude-user-agent; Secret scoped to user-tokens-{uslug}; ConfigMap scoped to session-agent-template" | TestEnsureRBAC_BothServiceAccountsCreated, TestEnsureRBAC_UserAgentRoleHasCorrectVerbs, TestEnsureRBAC_RoleBindingsCreated, TestEnsureRBAC_Idempotent | None | UNIT_ONLY |
| spec-k8s-architecture.md:119 (user-secrets) | "user-secrets Secret with no oauth-token field; remove stale oauth-token" | TestEnsureUserSecrets_CreatedWithNoOAuthToken, TestEnsureUserSecrets_RemovesStaleOAuthToken | None | UNIT_ONLY |
| spec-k8s-architecture.md:122 (user-agent-key) | "Generate NKey, write nkey-seed, register pubkey via NATS; idempotent if seed exists" | TestEnsureUserAgentKey_CreatesSecretWithSeed, TestEnsureUserAgentKey_Idempotent, TestEnsureUserAgentKey_NATSRegistration | None | UNIT_ONLY |
| spec-k8s-architecture.md:123 (user-tokens Secret) | "Ensure empty user-tokens-{uslug} Secret; idempotent if populated" | TestEnsureUserTokensSecret_CreatedEmpty, TestEnsureUserTokensSecret_IdempotentWithPopulatedKeys | None | UNIT_ONLY |
| spec-k8s-architecture.md:124 (user-agent Deployment) | "Single replica, RollingUpdate, mounts user-agent-key + user-tokens, env vars USER_SLUG HOST_SLUG NAMESPACE NATS_URL CONTROL_PLANE_URL, no DEV_OAUTH_TOKEN" | TestEnsureUserAgentDeployment_Created, TestEnsureUserAgentDeployment_VolumeMounts, TestEnsureUserAgentDeployment_EnvVars, TestEnsureUserAgentDeployment_Idempotent | None | UNIT_ONLY |
| spec-k8s-architecture.md:121 (session-agent-template copy) | "Copy {release}-session-agent-template into user namespace; update on change; noop if source absent" | TestEnsureSessionAgentTemplate_Copies, TestEnsureSessionAgentTemplate_UpdatesOnChange, TestEnsureSessionAgentTemplate_NoSourceIsNoop | None | UNIT_ONLY |
| spec-k8s-architecture.md:125 (imagePullSecrets) | "Copy dockerconfigjson Secrets from controller namespace; skip opaque Secrets" | TestCopyImagePullSecrets | None | UNIT_ONLY |
| spec-k8s-architecture.md:137-140 (corporate CA) | "Inject corporate-ca volume, mount, NODE_EXTRA_CA_CERTS, ca-bundle-hash annotation; no injection when disabled" | TestEnsureUserAgentDeployment_CorporateCAInjected, TestEnsureUserAgentDeployment_CorporateCANotInjectedWhenDisabled, TestEnsureUserAgentDeployment_CorporateCAHashUpdatesOnCMChange, TestEnsureUserAgentDeployment_CorporateCADefaultConfigMapName, TestCaBundleHash | None | UNIT_ONLY |
| spec-k8s-architecture.md:114-125 (full reconcile) | "ReconcileUser: all resources created, NATS registration call made" | TestReconcileUser_AllResourcesCreated, TestReconcileUser_Idempotent | None | UNIT_ONLY |

### Phase 4 — Bug Triage

No open bugs with Component: mclaude-controller-k8s found in `.agent/bugs/`.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|

### Summary

- Implemented: 24
- Gap: 0
- Partial: 6
- Infra: 20
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 14
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0
