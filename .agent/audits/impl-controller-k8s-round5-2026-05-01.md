# Implementation Audit: mclaude-controller-k8s (Round 5)

## Run: 2026-05-01T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-k8s-architecture.md:5 | "The controller connects directly to hub NATS via JWT credential (host-scoped per ADR-0054). There is no local NATS StatefulSet; the worker chart no longer ships a leaf node." | main.go:134-141 (nats.Connect using JWTFunc/SignFunc); chart has no nats-statefulset.yaml | IMPLEMENTED | | |
| spec-k8s-architecture.md:9 | "Single Deployment in `mclaude-system` on the worker cluster. Built with kubebuilder (controller-runtime). Leader election flag configurable via `LEADER_ELECTION` env var. v1 runs a single replica." | main.go:95-102; controller-deployment.yaml:11,48; values.yaml:61 | IMPLEMENTED | | |
| spec-k8s-architecture.md:13 | "Read the NKey seed from the file at `HOST_NKEY_SEED_PATH` (default `/etc/mclaude/host-creds/nkey_seed`)." | main.go:53 (`seedPath := envOr("HOST_NKEY_SEED_PATH", "/etc/mclaude/host-creds/nkey_seed")`); host_auth.go:80-99 | IMPLEMENTED | | |
| spec-k8s-architecture.md:14 | "Call `NewHostAuthFromSeed(seedPath, cpURL, log)` from `mclaude.io/common/pkg/hostauth`. This returns a `HostAuth` handle with no JWT yet." | main.go:66-68 | IMPLEMENTED | | |
| spec-k8s-architecture.md:15-20 | "Call `Refresh()` to perform HTTP challenge-response … If CP returns 404 … log registration instruction … retry every 5s with exponential backoff capped at 60s." | main.go:74, 171-199 (`bootRefresh`); host_auth.go:140-178 (ErrNotRegistered logging) | IMPLEMENTED | | |
| spec-k8s-architecture.md:21 | "Decode the host slug (`hslug`) from the received JWT. Use this slug for all subsequent NATS subscriptions — do not read `host.name` from Helm." | main.go:76-80 (`hostSlugFromJWT`); main.go:201-224 | IMPLEMENTED | | |
| spec-k8s-architecture.md:22 | "Connect to hub NATS at `HUB_NATS_URL` using the host JWT." | main.go:134-141 | IMPLEMENTED | | |
| spec-k8s-architecture.md:23 | "Subscribe to `mclaude.hosts.{hslug}.>` (single wildcard). Messages are dispatched internally by subject suffix." | nats_subscriber.go:61 (`hostSubject := fmt.Sprintf("mclaude.hosts.%s.>", p.clusterSlug)`) | IMPLEMENTED | | |
| spec-k8s-architecture.md:24 | "Background loop refreshes the host JWT before TTL expiry (5-minute TTL) via the same challenge-response code path." | main.go:148 (`auth.StartRefreshLoop(ctx)`); host_auth.go:187-216 | IMPLEMENTED | | |
| spec-k8s-architecture.md:28 | "`gen-host-nkey` pre-install Job (weight `-10`) generates an NKey pair at `helm install` time, writes the decorated seed string to Secret `{release}-host-creds` data field `nkey_seed`, and prints the public key to the Job log and Helm NOTES.txt." | gen-host-nkey-job.yaml:69 (weight -10), command "control-plane gen-host-nkey"; host-creds-secret.yaml; NOTES.txt | IMPLEMENTED | | |
| spec-k8s-architecture.md:36-40 | "publishes `mclaude.users.{uslug}.hosts._.register {name, type, nkey_public}` over NATS. CP slugifies `--name`, creates a `hosts` row keyed by `public_key`, returns `{ok, slug}`." | Operator-side flow; documented in NOTES.txt:15 | IMPLEMENTED | | Part of operator flow, not controller code |
| spec-k8s-architecture.md:44-52 | "Deregistration Flow — Two options; both publish `mclaude.users.{uslug}.hosts.{hslug}.manage.deregister`… Option B (Helm pre-delete Job) — deferred" | Spec marks Option B as deferred; no pre-delete hook in chart templates | IMPLEMENTED | | Option B explicitly deferred in spec |
| spec-k8s-architecture.md:59-69 | "Configuration table: HUB_NATS_URL, CONTROL_PLANE_URL, HOST_NKEY_SEED_PATH, HELM_RELEASE_NAME, SESSION_AGENT_TEMPLATE_CM, DEV_OAUTH_TOKEN, METRICS_ADDR, HEALTH_PROBE_ADDR, LEADER_ELECTION" | main.go:48-57; controller-deployment.yaml:35-54 | IMPLEMENTED | | |
| spec-k8s-architecture.md:70-75 | "Dropped from prior spec: NATS_ACCOUNT_SEED, NATS_CREDENTIALS_PATH, JS_DOMAIN, CLUSTER_SLUG, SESSION_AGENT_NATS_URL/NATS_URL" | main.go has none of these env reads; chart has none; nkeys.go is empty placeholder | IMPLEMENTED | | |
| spec-k8s-architecture.md:79-85 | "Subscribes via host JWT to single wildcard `mclaude.hosts.{hslug}.>` (zero JetStream access). Dispatch table: users.*.projects.*.create → create; users.*.projects.*.delete → delete; users.*.projects.*.update → update" | nats_subscriber.go:61-68; handleProvisionRequest switch: case "create", "update", "delete" | IMPLEMENTED | | |
| spec-k8s-architecture.md:91 | "CRD `MCProject` (`mcprojects.mclaude.io/v1alpha1`) in `mclaude-system`." | mcproject_types.go:15-17 (SchemeGroupVersion); mcproject-crd.yaml in chart | IMPLEMENTED | | |
| spec-k8s-architecture.md:92 | "Per-user namespace `mclaude-{userSlug}` (ADR-0062) with ServiceAccount, Role, RoleBinding." | reconciler.go:89 (`userNs := "mclaude-" + mcp.Spec.UserSlug`); reconcileRBAC | IMPLEMENTED | | |
| spec-k8s-architecture.md:93 | "Per-user `user-secrets` Secret (OAuth tokens, CLI configs — not NATS credentials; see Session-Agent Auth)." | reconciler.go:216-296 (reconcileSecrets — no nats-creds written) | IMPLEMENTED | | |
| spec-k8s-architecture.md:94 | "Per-user `user-config` ConfigMap." | reconciler.go:217-225 | IMPLEMENTED | | |
| spec-k8s-architecture.md:95 | "Per-project `project-{projectId}` and `nix-{projectId}` PVCs." | reconciler.go:440-445 | IMPLEMENTED | | |
| spec-k8s-architecture.md:96 | "Per-project `project-{projectId}` Deployment with `ownerReferences` back to the MCProject CR." | reconciler.go:473 (`ctrlutil.SetControllerReference(mcp, deploy, r.scheme)`) | IMPLEMENTED | | |
| spec-k8s-architecture.md:97 | "Watched: `{release}-session-agent-template` ConfigMap in `mclaude-system` — changes re-enqueue all `MCProject` CRs." | reconciler.go:762-789 (SetupWithManager watches ConfigMap, enqueues all MCProjects) | IMPLEMENTED | | |
| spec-k8s-architecture.md:99 | "All Deployments, PVCs, and Secrets materialized per project carry `ownerReferences` back to the `MCProject` CR via `controllerutil.SetControllerReference`." | reconciler.go:473 (deploy), 640-641 (pvc), 264,561 (secret) | IMPLEMENTED | | |
| spec-k8s-architecture.md:103 | "Loads the session-agent-template ConfigMap for image, resource, and PVC configuration." | reconciler.go:92-97 (loadTemplate call) | IMPLEMENTED | | |
| spec-k8s-architecture.md:104 | "Ensures the user namespace `mclaude-{userSlug}` exists with correct labels (ADR-0062: namespace derived from `userSlug`, not UUID)." | reconciler.go:89, 147-178 | IMPLEMENTED | | |
| spec-k8s-architecture.md:105 | "Ensures RBAC resources (ServiceAccount, Role, RoleBinding)." | reconciler.go:180-213 | IMPLEMENTED | | |
| spec-k8s-architecture.md:106 | "Ensures the `user-config` ConfigMap and `user-secrets` Secret. The `user-secrets` Secret contains `oauth-token`… It does not contain NATS credentials." | reconciler.go:216-296 | IMPLEMENTED | | |
| spec-k8s-architecture.md:107 | "Copies imagePullSecrets from the controller's namespace." | reconciler.go:274-296 | IMPLEMENTED | | |
| spec-k8s-architecture.md:108-111 | "Step 6b. Ensures the per-project `agent-nkey-{projectId}` Secret: generates NKey pair, writes seed, registers public key with CP via NATS `agents.register`, sets ownerReferences. Idempotent." | reconciler.go:119-124 (call), 503-578 (reconcileAgentNKey), 560-563 (ownerRef) | IMPLEMENTED | | |
| spec-k8s-architecture.md:112 | "Ensures the session-agent Deployment … Pod env vars include USER_ID, USER_SLUG, HOST_SLUG, PROJECT_ID, PROJECT_SLUG, NATS_URL (= HUB_NATS_URL), CONTROL_PLANE_URL … Pod mounts `agent-nkey-{projectId}` Secret at `/etc/mclaude/agent-nkey/`." | reconciler.go:323-337, 353-366 | IMPLEMENTED | | |
| spec-k8s-architecture.md:113 | "Updates `MCProject` status: phase (`Pending → Provisioning → Ready` or `Failed`) and conditions." | reconciler.go:76-87, 132-144 | IMPLEMENTED | | |
| spec-k8s-architecture.md:117-125 | "Session-Agent Auth: controller generates NKey pair, stores seed in `agent-nkey-{projectId}` Secret, registers public key with CP via NATS `mclaude.hosts.{hslug}.api.agents.register {nkey_public, user_slug, project_slug}` — host slug implicit in subject." | reconciler.go:486-489 (agentRegisterRequest — no host_slug field), 585-587 (subject construction) | IMPLEMENTED | | |
| spec-k8s-architecture.md:119 | "The controller does not retain the seed after writing to the Secret." | reconciler.go:524-578 — kp goes out of scope after writing seed to Secret | IMPLEMENTED | | |
| spec-k8s-architecture.md:125 | "The `nats-creds` key is not written to `user-secrets`. The per-project `agent-nkey-{projectId}` Secret carries `ownerReferences` back to the MCProject CR." | reconciler.go:229-230 (comment + no write); 560-563 (ownerRef) | IMPLEMENTED | | |
| spec-k8s-architecture.md:129-132 | "Corporate CA Support: adds label `mclaude.io/user-namespace: true`, injects `corporate-ca` volume, mount at `/etc/ssl/certs/corporate-ca-certificates.crt`, `NODE_EXTRA_CA_CERTS` env var, `mclaude.io/ca-bundle-hash` annotation." | reconciler.go:156-158 (ns label), 375-391 (volumes, mounts, env, annotation) | IMPLEMENTED | | |
| spec-k8s-architecture.md:136 | "Liveness: CP's `sys_subscriber.go` looks up host by `public_key` (no type filter), updates `hosts.last_seen_at`, upserts `mclaude-hosts` KV `online=true`." | Not in controller-k8s code — this is a control-plane behavior | IMPLEMENTED | | Control-plane side; out of scope for this component |
| spec-k8s-architecture.md:141-149 | "Error Handling table: HOST_NKEY_SEED_PATH missing → fatal exit; CP 404 → log + retry 5s exp backoff cap 60s; Hub NATS unreachable after JWT → NATS client retry; etc." | main.go:66-68 (fatal on seed read fail); 171-199 (boot retry with backoff); 134-141 (RetryOnFailedConnect) | IMPLEMENTED | | |
| spec-common.md:128 | "`NewHostAuthFromCredsFile(credsPath, cpURL, log)` — reads .creds file. Used by mclaude-controller-local." | host_auth.go:52-74 | IMPLEMENTED | | |
| spec-common.md:131 | "`NewHostAuthFromSeed(seedPath, cpURL, log)` — reads only NKey seed. No pre-existing JWT required. Used by mclaude-controller-k8s." | host_auth.go:80-99 | IMPLEMENTED | | |
| spec-common.md:133-146 | "`Refresh(ctx)` — POST /api/auth/challenge + sign + POST /api/auth/verify → returns JWT. On 404 returns ErrNotRegistered with registration log message." | host_auth.go:140-178, 220-253 | IMPLEMENTED | | |
| spec-common.md:148 | "`PublicKey() string` — returns NKey public key (U-prefix)." | host_auth.go:103-110 | IMPLEMENTED | | |
| spec-common.md:150 | "`JWTFunc() nats.AuthTokenHandler` — returns function for `nats.UserJWT(...)`. Returns cached JWT. Callers must call `Refresh(ctx)` before connecting." | host_auth.go:115-123 | IMPLEMENTED | | |
| spec-common.md:152 | "`SignFunc() nats.SignatureHandler` — returns NKey sign function for NATS challenge-response. Passed to `nats.Nkey(pubkey, signFunc)`." | host_auth.go:126-130 | IMPLEMENTED | | |
| spec-common.md:154 | "`StartRefreshLoop(ctx context.Context)` — background goroutine that proactively refreshes JWT before 5-minute TTL expires. Stops when ctx cancelled." | host_auth.go:187-216 | IMPLEMENTED | | |
| spec-helm.md:130 | "mclaude-worker: Independently installable into any K8s cluster … no local NATS StatefulSet, no leaf-node credentials." | chart has no nats-statefulset.yaml, no nats-configmap.yaml, no nats-service.yaml, no gen-leaf-creds-job.yaml | IMPLEMENTED | | |
| spec-helm.md:162-164 | "Pre-install Job `{release}-gen-host-nkey` (weight -10). Generates NKey pair via `nkeys.CreateUser()`. Writes seed to Secret `{release}-host-creds` field `nkey_seed`. Prints public key to Job log and NOTES.txt. Idempotent." | gen-host-nkey-job.yaml:69 (weight -10); command "control-plane gen-host-nkey"; NOTES.txt | IMPLEMENTED | | |
| spec-helm.md:164 | "Secret `{release}-host-creds`: Single field: `nkey_seed`. JWT is not stored here." | host-creds-secret.yaml: single `nkey_seed` field with placeholder empty value | IMPLEMENTED | | |
| spec-helm.md:170 | "Deployment `{release}-controller`: Mounts `{release}-host-creds` at `/etc/mclaude/host-creds/`. Env: HUB_NATS_URL, CONTROL_PLANE_URL, HOST_NKEY_SEED_PATH=/etc/mclaude/host-creds/nkey_seed." | controller-deployment.yaml:35-40, 56-58 | IMPLEMENTED | | |
| spec-helm.md:171-175 | "ServiceAccount, ClusterRole, ClusterRoleBinding for controller." | serviceaccount.yaml, clusterrole.yaml, clusterrolebinding.yaml | IMPLEMENTED | | |
| spec-helm.md:175 | "CRD `mcprojects.mclaude.io` — MCProject v1alpha1. Namespaced." | mcproject-crd.yaml in chart | IMPLEMENTED | | |
| spec-helm.md:179-200 | "ConfigMap `{release}-session-agent-template`: image, imagePullPolicy, terminationGracePeriodSeconds, resourcesJson, projectPvcSize, projectPvcStorageClass, nixPvcSize, nixPvcStorageClass, corporateCAEnabled, corporateCAConfigMapName, corporateCAConfigMapKey." | session-agent-template.yaml:13-24 — all fields present | IMPLEMENTED | | |
| spec-helm.md:185-200 | "Configuration knobs: controlPlane.url, host.name, host.hubNatsUrl, controller.replicas, sessionAgent.* (image, terminationGracePeriodSeconds, persistence, nix, corporateCA.*), controller.config.devOAuthToken." | values.yaml matches all listed knobs | IMPLEMENTED | | |
| adr-0063:76 | "Session-agent pod boots, generates own NKey, host controller registers agent's public key with CP via `mclaude.hosts.{hslug}.api.agents.register`." | reconciler.go:503-615 (reconcileAgentNKey + registerAgentNKey) | IMPLEMENTED | | |
| adr-0063:127 | "controller: `CONTROL_PLANE_URL` injection into session-agent pods: add `controlPlaneURL` field to reconciler struct (mirroring the existing `sessionAgentNATSURL` field pattern). At reconcile time, inject `CONTROL_PLANE_URL` env var directly from this struct field." | reconciler.go:46-47 (`controlPlaneURL string` field); 327-328 (env injection) | IMPLEMENTED | | |
| adr-0063:164 | "Owner references on materialized resources (Deployment, PVC, Secret) → MCProject CR. Use `controllerutil.SetControllerReference`." | reconciler.go:473 (deploy), 640-641 (pvc), 264 (secret), 560-563 (agent-nkey) | IMPLEMENTED | | |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-8 | INFRA | Package comment and imports — boilerplate. |
| main.go:32-45 | INFRA | Logger setup (`LOG_LEVEL` env parsing, zerolog init). Spec mentions `LOG_LEVEL` as a config var. |
| main.go:55-57 | INFRA | `helmReleaseName`, `sessionAgentTemplateCM`, `devOAuthToken` env reads — all in spec configuration table. |
| main.go:84-105 | INFRA | controller-runtime Manager setup (scheme, metrics, health probes, leader election) — all spec-described. |
| main.go:107-112 | INFRA | Health/readiness check registration — spec says health probe on `:8081`. |
| main.go:114-115 | INFRA | `detectNamespace()` call — operational plumbing for pod namespace detection. |
| main.go:117-131 | INFRA | Reconciler struct initialization — wiring all spec-described fields. |
| main.go:133-141 | INFRA | NATS connect call with RetryOnFailedConnect — spec says "NATS client retries". |
| main.go:143-145 | INFRA | `reconciler.nc = nc` — wires NATS connection into reconciler for agent registration. Described in spec step 6b. |
| main.go:151-168 | INFRA | NATSProvisioner init and mgr.Start — boilerplate orchestration. |
| main.go:226-234 | INFRA | `detectNamespace()` helper — reads `/var/run/secrets/kubernetes.io/serviceaccount/namespace`. |
| main.go:236-241 | INFRA | `envOr()` helper — standard env var utility used throughout. |
| reconciler.go:54-59 | INFRA | `natsConn` interface for testability — enables fake injection in tests. |
| reconciler.go:299-411 | INFRA | `buildPodTemplate()` — spec describes the env vars and volume mounts to include; this is the implementing code. All fields are spec-described. |
| reconciler.go:414-435 | INFRA | `reconcilerCAConfigMapHash()` — computes hash for `mclaude.io/ca-bundle-hash` annotation (spec-described). |
| reconciler.go:649-679 | INFRA | `ensureOwned()` and `addOwnerIfMissing()` — utility for owner reference management; spec requires ownerRefs on all resources. |
| reconciler.go:681-727 | INFRA | `loadTemplate()` — reads session-agent-template ConfigMap; spec-described in Reconciler Loop step 1. |
| reconciler.go:729-753 | INFRA | `setPhase()`, `updateCondition()`, `setCondition()` — status update helpers for spec-described phase transitions. |
| reconciler.go:755-790 | INFRA | `SetupWithManager()` — controller-runtime watch registration; spec describes the watched resources. |
| reconciler.go:792-835 | INFRA | `sessionAgentTpl` struct, `defaultTemplate()`, `applyDefaultResources()` — data structures for template parsing. All fields are spec-described. |
| nats_subscriber.go:24-41 | INFRA | `ProvisionRequest` and `ProvisionReply` structs — wire format for NATS provisioning messages. Spec describes the request/reply flow. |
| nats_subscriber.go:43-51 | INFRA | `NATSProvisioner` struct — dependency container; all fields spec-described. |
| nats_subscriber.go:136-202 | INFRA | `handleCreate()` and `waitForReady()` — creation and polling logic for MCProject CRs. Spec says "returns success when reconcile reaches Ready." |
| nats_subscriber.go:204-225 | INFRA | `handleUpdate()` — re-reconciles user-secrets on update. Spec table says "update → triggers reconcileSecrets." |
| nats_subscriber.go:227-250 | INFRA | `handleDelete()` — deletes MCProject CR; spec says "Deletes the MCProject CR; ownerRefs cascade." |
| nats_subscriber.go:252-268 | INFRA | `replyOK()`, `replyError()` — NATS reply helpers. Spec says subject handlers are request/reply. |
| nats_subscriber.go:270-279 | INFRA | `extractOperation()` — extracts last token from NATS subject for dispatch. |
| mcproject_types.go:1-138 | INFRA | CRD type definitions, phase/condition constants, DeepCopy — all spec-described Kubernetes resource shapes. |
| nkeys.go:1-9 | INFRA | Empty placeholder file noting JWT-issuance code removal (ADR-0063). Harmless. |
| host_auth.go:23-27 | INFRA | TTL and refresh-buffer constants (5min TTL, 60s buffer) — spec says "5-minute TTL" and "safety margin." |
| host_auth.go:37-44 | INFRA | `HostAuth` struct fields — internal state; spec-consistent. |
| host_auth.go:218-294 | INFRA | `requestChallenge()` and `verifyChallenge()` — implementation of POST /api/auth/challenge + verify; spec-described HTTP endpoints. |
| gen-host-nkey-job.yaml:8-57 | INFRA | ServiceAccount/Role/RoleBinding for gen-host-nkey hook — spec table says "ServiceAccount/Role/RoleBinding `{release}-gen-host-nkey` (weight -20)." |
| controller-deployment.yaml:59-79 | INFRA | Liveness/readiness probes and resource requests/limits — operational requirements, consistent with spec config table. |
| values.yaml:1-122 | INFRA | All values are either in the spec configuration knobs table or infrastructure (global.imageRegistry, tests.curl image for chart tests). |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-k8s-architecture.md:13 | Read NKey seed from HOST_NKEY_SEED_PATH | host_auth_test.go:TestNewHostAuthFromSeed_Valid | None | UNIT_ONLY | Critical path (boot); no integration test against real CP |
| spec-k8s-architecture.md:15-20 | Refresh → challenge-response; 404 → ErrNotRegistered; retry 5s exp backoff | host_auth_test.go:TestRefresh_FullFlow, TestRefresh_404ReturnsErrNotRegistered; controller_test.go: (bootRefresh not directly tested) | None | UNIT_ONLY | Auth flow — critical path. Integration test bar applies. No test against real NATS+CP stack. |
| spec-k8s-architecture.md:21 | Decode hslug from JWT | controller_test.go:TestADR0063_HostSlugFromJWT | None | UNIT_ONLY | |
| spec-k8s-architecture.md:23 | Subscribe to `mclaude.hosts.{hslug}.>` (single wildcard) | controller_test.go:TestADR0063_SingleSubscription | None | UNIT_ONLY | Integration bar: NATS subject permissions w/ real JWT enforcement not tested |
| spec-k8s-architecture.md:24 | Background JWT refresh loop | host_auth_loop_test.go:TestStartRefreshLoop_RefreshesBeforeTTL, TestStartRefreshLoop_StopsOnContextCancel, TestStartRefreshLoop_NoCPURL | None | UNIT_ONLY | Auth flow — integration test bar applies |
| spec-k8s-architecture.md:92 | User namespace `mclaude-{userSlug}` (ADR-0062) | controller_test.go:TestADR0062_NamespaceUsesSlug | None | UNIT_ONLY | |
| spec-k8s-architecture.md:93 | user-secrets does not contain NATS credentials | controller_test.go:TestADR0063_NoNATSCredsInSecrets, TestADR0063_StaleNATSCredsRemoved | None | UNIT_ONLY | |
| spec-k8s-architecture.md:96 | Deployment with ownerReferences back to MCProject | controller_test.go has no explicit owner-ref test for Deployment (TestADR0063_AgentNKeyOwnerReference covers agent-nkey Secret only) | None | UNTESTED | No unit test verifying ownerRef on the project Deployment itself |
| spec-k8s-architecture.md:99 | PVCs carry ownerReferences back to MCProject CR | No explicit PVC owner-ref test in controller_test.go | None | UNTESTED | No test verifying ctrlutil.SetControllerReference on PVCs |
| spec-k8s-architecture.md:105 | RBAC resources (ServiceAccount, Role, RoleBinding) | controller_test.go:TestGap7_MultiOwner covers SA owner; no dedicated RBAC correctness test | None | UNIT_ONLY | |
| spec-k8s-architecture.md:108-111 | agent-nkey Secret: create, idempotent, NATS register, ownerRef | controller_test.go:TestADR0063_AgentNKeySecretCreated, TestADR0063_AgentNKeySecretIdempotent, TestADR0063_AgentNKeyNATSRegistration, TestADR0063_AgentNKeyOwnerReference, TestADR0063_AgentNKeySecretName | None | UNIT_ONLY | Data flow: NATS pub/sub — integration bar applies |
| spec-k8s-architecture.md:112 | session-agent pod env: USER_ID, USER_SLUG, HOST_SLUG, PROJECT_ID, PROJECT_SLUG, NATS_URL, CONTROL_PLANE_URL; agent-nkey mount | controller_test.go:TestADR0062_UserSlugEnvVar, TestADR0063_ControlPlaneURLInjected, TestADR0063_AgentNKeyMountedInPodTemplate | None | UNIT_ONLY | |
| spec-k8s-architecture.md:113 | Phase transitions: Pending → Provisioning → Ready or Failed | controller_test.go:TestGap3_PendingPhaseTransition | None | UNIT_ONLY | |
| spec-k8s-architecture.md:117 | NATS agents.register payload: {nkey_public, user_slug, project_slug} — no host_slug | controller_test.go:TestADR0063_AgentNKeyNATSRegistration (verifies no host_slug in payload) | None | UNIT_ONLY | |
| spec-k8s-architecture.md:129-132 | Corporate CA: label on namespace, volume, mount, NODE_EXTRA_CA_CERTS, ca-bundle-hash annotation | No test in controller_test.go for corporate CA injection | None | UNTESTED | |
| spec-common.md:131 | NewHostAuthFromSeed | host_auth_test.go:TestNewHostAuthFromSeed_Valid, TestNewHostAuthFromSeed_InvalidPath, TestNewHostAuthFromSeed_InvalidContent | None | UNIT_ONLY | |
| spec-common.md:133 | Refresh: challenge → sign → verify → return JWT; update stored JWT | host_auth_test.go:TestRefresh_FullFlow, TestRefresh_UpdatesStoredJWT, TestRefresh_ErrorWhenNoCPURL, TestRefresh_404ReturnsErrNotRegistered, TestRefresh_SignatureCorrectness | None | UNIT_ONLY | |
| spec-common.md:148 | PublicKey() | host_auth_test.go:TestPublicKey_Format, TestPublicKey_FromSeed | None | UNIT_ONLY | |
| spec-common.md:150 | JWTFunc() | host_auth_test.go:TestNewHostAuthFromCredsFile_Valid, TestNewHostAuthFromSeed_Valid (via JWTFunc()) | None | UNIT_ONLY | |
| spec-common.md:152 | SignFunc() | host_auth_test.go:TestSignFunc_ProducesSignature, TestSignFunc_SignatureVerifiable | None | UNIT_ONLY | |
| spec-common.md:154 | StartRefreshLoop(ctx) | host_auth_loop_test.go:TestStartRefreshLoop_RefreshesBeforeTTL, TestStartRefreshLoop_StopsOnContextCancel, TestStartRefreshLoop_NoCPURL | None | UNIT_ONLY | |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | No open bugs in `.agent/bugs/` with Component matching mclaude-controller-k8s | — | |

### Summary

- Implemented: 52
- Gap: 0
- Partial: 0
- Infra: 33
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 16
- E2E only: 0
- Untested: 3
- Bugs fixed: 0
- Bugs open: 0
