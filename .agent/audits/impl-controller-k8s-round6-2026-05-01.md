# Implementation Audit: mclaude-controller-k8s (Round 6)

## Run: 2026-05-01T00:00:00Z

Component root: `mclaude-controller-k8s/`
Spec files:
- `docs/adr-0063-k8s-architecture-spec.md` (status: accepted)
- `docs/mclaude-controller-k8s/spec-k8s-architecture.md`
- `docs/mclaude-common/spec-common.md` (pkg/hostauth section)

Production source files audited:
- `mclaude-controller-k8s/main.go`
- `mclaude-controller-k8s/reconciler.go`
- `mclaude-controller-k8s/nats_subscriber.go`
- `mclaude-controller-k8s/nkeys.go`
- `mclaude-controller-k8s/mcproject_types.go`
- `mclaude-common/pkg/hostauth/host_auth.go`

Test files:
- `mclaude-controller-k8s/controller_test.go`
- `mclaude-common/pkg/hostauth/host_auth_test.go`
- `mclaude-common/pkg/hostauth/host_auth_loop_test.go`

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-k8s-architecture.md:3 | "The K8s controller is a kubebuilder operator that runs inside a worker Kubernetes cluster." | main.go:95-102 (ctrl.NewManager), mcproject_types.go:83 (MCProject CRD) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:5 | "The controller connects directly to hub NATS via JWT credential (host-scoped per ADR-0054). There is no local NATS StatefulSet." | main.go:134-141 (nats.Connect with UserJWT), nkeys.go:1-8 (placeholder comment, no local NATS) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:9 | "Single Deployment in mclaude-system. Built with kubebuilder (controller-runtime). Leader election flag configurable via LEADER_ELECTION env var." | main.go:93-101 (LeaderElection: envOr("LEADER_ELECTION","false")=="true") | IMPLEMENTED | — | |
| spec-k8s-architecture.md:13 | "Boot step 1: Read the NKey seed from the file at HOST_NKEY_SEED_PATH (default /etc/mclaude/host-creds/nkey_seed)." | main.go:53 (seedPath := envOr("HOST_NKEY_SEED_PATH", "/etc/mclaude/host-creds/nkey_seed")) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:14 | "Boot step 2: Call NewHostAuthFromSeed(seedPath, cpURL, log)..." | main.go:66-69 (hostauth.NewHostAuthFromSeed(...)) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:15-20 | "Boot step 3: Call Refresh(). If 404, log registration instruction and retry every 5s with exponential backoff capped at 60s." | main.go:172-198 (bootRefresh: initial=5s, doubles, cap=60s; errors.Is(ErrNotRegistered)) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:21 | "Boot step 4: Decode host slug (hslug) from the received JWT. Use this slug for all NATS subscriptions — do not read host.name from Helm." | main.go:76-80 (hostSlugFromJWT(hostJWT)), main.go:126 (clusterSlug: hslug) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:22 | "Boot step 5: Connect to hub NATS at HUB_NATS_URL using the host JWT." | main.go:134-141 (nats.Connect(hubNATSURL, nats.UserJWT(auth.JWTFunc(), auth.SignFunc()))) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:23 | "Boot step 6: Subscribe to mclaude.hosts.{hslug}.> (single wildcard)." | nats_subscriber.go:61 (hostSubject := fmt.Sprintf("mclaude.hosts.%s.>", p.clusterSlug)) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:24 | "Boot step 7: Background loop refreshes the host JWT before TTL expiry (5-minute TTL)." | main.go:148 (auth.StartRefreshLoop(ctx)), host_auth.go:187-215 (StartRefreshLoop) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:59-68 | Configuration table: HUB_NATS_URL (Yes), CONTROL_PLANE_URL (Yes), HOST_NKEY_SEED_PATH (No, default /etc/mclaude/host-creds/nkey_seed), HELM_RELEASE_NAME (No, default mclaude-worker), SESSION_AGENT_TEMPLATE_CM (No), DEV_OAUTH_TOKEN (No), METRICS_ADDR (No, :8082), HEALTH_PROBE_ADDR (No, :8081), LEADER_ELECTION (No, false) | main.go:48-56 (all env vars read with correct defaults) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:70-75 | "Dropped env vars: NATS_ACCOUNT_SEED, NATS_CREDENTIALS_PATH, JS_DOMAIN, CLUSTER_SLUG, SESSION_AGENT_NATS_URL / NATS_URL" | main.go (none of these appear); nkeys.go (placeholder only, no loadAccountKey or IssueSessionAgentJWT) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:79 | "Subscribes via host JWT to the single wildcard mclaude.hosts.{hslug}.>" | nats_subscriber.go:61-68 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:81-85 | NATS subject suffix dispatch: users.*.projects.*.create (create MCProject CR), delete (delete MCProject CR), update (reconcileSecrets) | nats_subscriber.go:92-131 (switch on extractOperation: create/update/delete) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:91 | "CRD MCProject (mcprojects.mclaude.io/v1alpha1) in mclaude-system." | mcproject_types.go:15-18 (SchemeGroupVersion Group=mclaude.io, Version=v1alpha1) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:92 | "Per-user namespace mclaude-{userSlug} (ADR-0062)" | reconciler.go:89 (userNs := "mclaude-" + mcp.Spec.UserSlug) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:93 | "Per-user user-secrets Secret (OAuth tokens, CLI configs — not NATS credentials)" | reconciler.go:227-272 (reconcileSecrets: only oauth-token, no nats-creds) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:94 | "Per-user user-config ConfigMap." | reconciler.go:217-224 (user-config ConfigMap ensured) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:95 | "Per-project project-{projectId} and nix-{projectId} PVCs." | reconciler.go:440-444 (ensurePVCCR for project- and nix- PVCs) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:96 | "Per-project project-{projectId} Deployment with ownerReferences back to the MCProject CR." | reconciler.go:473-475 (ctrlutil.SetControllerReference(mcp, deploy, r.scheme)) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:97 | "Watched: {release}-session-agent-template ConfigMap in mclaude-system — changes re-enqueue all MCProject CRs." | reconciler.go:769-788 (Watches ConfigMap with EnqueueRequestsFromMapFunc) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:99 | "All Deployments, PVCs, and Secrets materialized per project carry ownerReferences back to the MCProject CR via controllerutil.SetControllerReference." | reconciler.go:473 (deploy), reconciler.go:640 (PVC), reconciler.go:561 (agent-nkey secret), reconciler.go:264 (user-secrets) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:104 | "Reconciler Loop step 1: Loads the session-agent-template ConfigMap." | reconciler.go:92-97 (r.loadTemplate(ctx)) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:105 | "Reconciler Loop step 2: Ensures user namespace mclaude-{userSlug} with correct labels (ADR-0062: namespace derived from userSlug, not UUID)." | reconciler.go:89,99-105 (reconcileNamespace with userNs = "mclaude-"+mcp.Spec.UserSlug) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:106 | "Reconciler Loop step 3: Ensures RBAC resources (ServiceAccount, Role, RoleBinding)." | reconciler.go:107-111 (reconcileRBAC) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:107 | "Reconciler Loop step 4: Ensures user-config ConfigMap and user-secrets Secret. user-secrets contains oauth-token. Does NOT contain NATS credentials." | reconciler.go:113-116 (reconcileSecrets), reconciler.go:227-297 (implementation) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:108 | "Reconciler Loop step 5: Copies imagePullSecrets from the controller's namespace." | reconciler.go:274-296 (copies DockerConfigJson secrets from controlPlaneNs) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:111 | "Reconciler Loop step 6b: Ensures per-project agent-nkey-{projectId} Secret; generates NKey pair, writes decorated seed to nkey_seed field, registers public key with CP via NATS agents.register. Idempotent — skips NATS call if Secret already exists with matching key." | reconciler.go:119-124 (reconcileAgentNKey), reconciler.go:510-578 (implementation) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:112 | "Reconciler Loop step 7: Ensures session-agent Deployment with ownerReferences. Pod env vars include USER_ID, USER_SLUG, HOST_SLUG, PROJECT_ID, PROJECT_SLUG, NATS_URL (=HUB_NATS_URL), CONTROL_PLANE_URL. Pod mounts agent-nkey-{projectId} Secret at /etc/mclaude/agent-nkey/." | reconciler.go:302-366 (buildPodTemplate with all listed env vars and volume mount) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:113 | "Reconciler Loop step 8: Updates MCProject status phase (Pending→Provisioning→Ready or Failed) and conditions." | reconciler.go:77-87 (Pending→Provisioning), reconciler.go:132-143 (Ready or back to Provisioning), reconciler.go:100-128 (conditions via updateCondition) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:117-124 | "Session-Agent Auth: controller generates NKey pair, stores seed in agent-nkey-{projectId} Secret, registers public key with CP via NATS request mclaude.hosts.{hslug}.api.agents.register {nkey_public, user_slug, project_slug} — host slug is implicit in the NATS subject." | reconciler.go:483-614 (reconcileAgentNKey + registerAgentNKey; subject fmt.Sprintf("mclaude.hosts.%s.api.agents.register", r.clusterSlug); payload: nkey_public, user_slug, project_slug) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:125 | "The nats-creds key is not written to user-secrets. The per-project agent-nkey-{projectId} Secret carries ownerReferences back to the MCProject CR." | reconciler.go:238-240 (delete nats-creds if present), reconciler.go:561 (SetControllerReference on agent-nkey secret) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:129-132 | "Corporate CA Support: adds label mclaude.io/user-namespace=true to user namespaces, injects corporate-ca volume+mount at /etc/ssl/certs/corporate-ca-certificates.crt, adds NODE_EXTRA_CA_CERTS, annotates pod with mclaude.io/ca-bundle-hash." | reconciler.go:155-169 (namespace label), reconciler.go:375-391 (volume+mount+env+annotation) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:136 | "Liveness: hub NATS CONNECT event → CP sys_subscriber looks up host by public_key (no type filter), updates last_seen_at, upserts mclaude-hosts KV online=true." | Spec describes CP behavior, not controller-k8s code. Controller side: main.go:134-141 (connects to hub NATS triggering the event). | IMPLEMENTED | — | K8s controller's responsibility is connecting; CP SYS dispatch is a CP concern |
| spec-k8s-architecture.md:141-149 | Error handling table: HOST_NKEY_SEED_PATH missing → fatal exit; 404 → log + retry; NATS unreachable at boot → retry; NATS drops at runtime → reconnect+refresh; reconcile fails → Failed phase. | main.go:67-68 (fatal on seed load error), main.go:172-198 (bootRefresh retry on ErrNotRegistered), main.go:134-141 (RetryOnFailedConnect+MaxReconnects(-1)), reconciler.go:103 (setPhase Failed on nsErr) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:155-157 | "Secret {release}-host-creds, field nkey_seed (decorated seed string). JWT is not stored here." | host_auth.go:80-98 (NewHostAuthFromSeed reads nkey_seed file, currentJWT starts as ""); main.go:53 (seed path) | IMPLEMENTED | — | |
| adr-0063:50 | "Host JWT cache: In-memory only. Controller acquires JWT via challenge-response on every boot; never writes JWT to Secret." | host_auth.go:37-44 (HostAuth struct has currentJWT in memory, no write-back to file/secret) | IMPLEMENTED | — | |
| adr-0063:55 | "Owner references on MCProject children already handle pod/PVC cleanup." | reconciler.go:473, 640, 561 (SetControllerReference on deploy, PVC, agent-nkey secret) | IMPLEMENTED | — | |
| adr-0063:144-148 | "Drop env vars: NATS_ACCOUNT_SEED, NATS_CREDENTIALS_PATH, JS_DOMAIN, CLUSTER_SLUG. Add: HUB_NATS_URL, CONTROL_PLANE_URL, HOST_NKEY_SEED_PATH." | main.go:48-53 (only new env vars present) | IMPLEMENTED | — | |
| adr-0063:149 | "Boot sequence: read nkey_seed → POST /api/auth/challenge + verify → receive host JWT → connect to hub NATS with JWT." | main.go:59-81 (comment + code sequence) | IMPLEMENTED | — | |
| adr-0063:150 | "JWT lifetime: held in memory only, never persisted back to the Secret." | host_auth.go (no file write in Refresh or StartRefreshLoop) | IMPLEMENTED | — | |
| adr-0063:163 | "Drop sessionAgentNATSURL() derivation function. Session-agents inherit HUB_NATS_URL directly via session-agent template — no derivation needed." | main.go:123 (sessionAgentNATSURL: hubNATSURL passed directly); no derivation function in code | IMPLEMENTED | — | |
| adr-0063:164 | "Owner references on materialized resources (Deployment, PVC, Secret) → MCProject CR. Use controllerutil.SetControllerReference." | reconciler.go:473,640,561,264 | IMPLEMENTED | — | |
| adr-0063:262 | "mclaude-common/pkg/hostauth: new package. Move host_auth.go from mclaude-controller-local; add NewHostAuthFromSeed (ErrNotRegistered on 404, callers implement retry); add JWTFunc(), SignFunc(), StartRefreshLoop(ctx)." | host_auth.go:1-294 (all four items implemented) | IMPLEMENTED | — | |
| spec-common.md:127 | "NewHostAuthFromCredsFile(credsPath, cpURL, log) — reads .creds file (NKey seed + pre-existing JWT). Used by mclaude-controller-local." | host_auth.go:52-73 | IMPLEMENTED | — | |
| spec-common.md:131 | "NewHostAuthFromSeed(seedPath, cpURL, log) — reads only NKey seed. No pre-existing JWT required. Used by mclaude-controller-k8s." | host_auth.go:80-98 | IMPLEMENTED | — | |
| spec-common.md:133-146 | "Refresh(ctx): POST /api/auth/challenge {nkey_public} → {challenge}; sign; POST /api/auth/verify {nkey_public, challenge, signature} → {jwt}. On 404 returns ErrNotRegistered and logs registration instruction." | host_auth.go:140-177 (Refresh), host_auth.go:218-252 (requestChallenge — 404→ErrNotRegistered), host_auth.go:256-293 (verifyChallenge) | IMPLEMENTED | — | |
| spec-common.md:148 | "PublicKey() string — returns the NKey public key (U-prefix)." | host_auth.go:101-110 | IMPLEMENTED | — | |
| spec-common.md:150 | "JWTFunc() nats.AuthTokenHandler — returns cached JWT, updated by Refresh(). Used to wire HostAuth into NATS client options." | host_auth.go:115-122 | IMPLEMENTED | — | |
| spec-common.md:152 | "SignFunc() nats.SignatureHandler — returns NKey sign function for NATS challenge-response. Passed to nats.Nkey(pubkey, signFunc)." | host_auth.go:126-129 | IMPLEMENTED | — | |
| spec-common.md:154 | "StartRefreshLoop(ctx) — background goroutine proactively refreshes host JWT before 5-minute TTL. Stops when ctx is cancelled." | host_auth.go:187-215 (StartRefreshLoop + startRefreshLoopWithInterval) | IMPLEMENTED | — | |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-8 | INFRA | Package declaration and ADR comment |
| main.go:10-30 | INFRA | Imports — standard boilerplate for controller-runtime + NATS |
| main.go:36-45 | INFRA | Logger setup (zerolog); LOG_LEVEL env var is a standard operational concern not requiring spec coverage |
| main.go:93-94 | INFRA | leaderElectionNs := envOr("LEADER_ELECTION_NAMESPACE","mclaude-system") — small operational knob, not spec'd but standard K8s operator pattern |
| main.go:114-115 | INFRA | detectNamespace() call — infrastructure for reading the pod's own namespace |
| main.go:164-168 | INFRA | mgr.Start(ctrl.SetupSignalHandler()) — standard controller-runtime entrypoint |
| main.go:200-224 | INFRA | hostSlugFromJWT helper: decodes JWT payload to extract "host-{hslug}" name; required infrastructure for boot step 4 |
| main.go:226-234 | INFRA | detectNamespace() — reads /var/run/secrets/kubernetes.io/serviceaccount/namespace; standard K8s in-cluster namespace detection |
| main.go:236-241 | INFRA | envOr() helper — standard env-var-with-default utility |
| reconciler.go:1-34 | INFRA | Package + imports |
| reconciler.go:55-59 | INFRA | natsConn interface — testability shim for *nats.Conn; supports the nc field on reconciler (spec'd behavior) |
| reconciler.go:414-435 | INFRA | reconcilerCAConfigMapHash() — deterministic SHA256 hash of ConfigMap for ca-bundle-hash annotation; required infrastructure for spec-k8s-architecture.md:132 (annotation requirement) |
| reconciler.go:649-664 | INFRA | ensureOwned() helper — CRUD utility used by reconcileRBAC to ensure owner refs on existing resources; necessary plumbing for the ownerReference spec |
| reconciler.go:666-679 | INFRA | addOwnerIfMissing() — idempotent owner-ref addition; required infrastructure for shared-resource ownership |
| reconciler.go:681-727 | INFRA | loadTemplate() — parses session-agent-template ConfigMap; required by reconciler step 1 |
| reconciler.go:729-753 | INFRA | setPhase(), updateCondition(), setCondition() — MCProject status helpers; required by reconciler step 8 |
| reconciler.go:755-790 | INFRA | SetupWithManager() — controller-runtime registration; boilerplate |
| reconciler.go:792-834 | INFRA | sessionAgentTpl struct + defaultTemplate() + applyDefaultResources() — template parsing types; required infrastructure |
| nats_subscriber.go:1-8 | INFRA | Package header + ADR comment |
| nats_subscriber.go:10-22 | INFRA | Imports |
| nats_subscriber.go:25-41 | INFRA | ProvisionRequest + ProvisionReply types — NATS wire format structs for provisioning messages |
| nats_subscriber.go:43-51 | INFRA | NATSProvisioner struct — holds state for subscriber |
| nats_subscriber.go:177-201 | INFRA | waitForReady() — polls MCProject CR for Ready/Failed phase; required by create handler (spec says "returns success when reconcile reaches Ready") |
| nats_subscriber.go:252-278 | INFRA | replyOK(), replyError(), extractOperation() — NATS reply helpers; required infrastructure |
| nkeys.go:1-9 | INFRA | Placeholder file confirming account-key code has been removed (ADR-0063) |
| mcproject_types.go:1-137 | INFRA | CRD type definitions (MCProject, MCProjectSpec, MCProjectStatus, phases, conditions, deep copy) — required Kubernetes boilerplate |
| host_auth.go:22-27 | INFRA | Constants hostJWTTTL (5min) and hostJWTRefreshBuffer (60s) — operational constants backing the refresh loop spec |
| host_auth.go:37-44 | INFRA | HostAuth struct with mu sync.RWMutex — concurrency-safe JWT storage; necessary for goroutine-safe JWTFunc() |
| host_auth.go:218-252 | INFRA | requestChallenge() — HTTP helper for POST /api/auth/challenge; implements Refresh() step 1 |
| host_auth.go:256-293 | INFRA | verifyChallenge() — HTTP helper for POST /api/auth/verify; implements Refresh() step 3 |
| host_auth.go:195-215 | INFRA | startRefreshLoopWithInterval() — internal helper extracted for testability; exposes the loop logic without changing public API |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-k8s-arch:13 | HOST_NKEY_SEED_PATH default /etc/mclaude/host-creds/nkey_seed | TestNewHostAuthFromSeed_Valid (host_auth_test.go:118) | None | UNIT_ONLY | Integration test against real NATS operator cluster deferred per ADR-0063 §Scope/Deferred |
| spec-k8s-arch:14 | NewHostAuthFromSeed constructor | TestNewHostAuthFromSeed_Valid, _InvalidPath, _InvalidContent (host_auth_test.go:118-152) | None | UNIT_ONLY | Same deferral |
| spec-k8s-arch:15-20 | Refresh() 404→ErrNotRegistered→log→retry 5s/60s cap | TestRefresh_404ReturnsErrNotRegistered (host_auth_test.go:312); bootRefresh logic in main.go covered by TestADR0063_HostSlugFromJWT (indirectly); no direct bootRefresh unit test | TestRefresh_404ReturnsErrNotRegistered | UNIT_ONLY | bootRefresh itself has no dedicated unit test but the building blocks (ErrNotRegistered propagation + backoff) are tested |
| spec-k8s-arch:21 | Decode hslug from JWT | TestADR0063_HostSlugFromJWT (controller_test.go:521) | None | UNIT_ONLY | Deferred |
| spec-k8s-arch:22 | Connect to hub NATS with host JWT via JWTFunc()+SignFunc() | No dedicated unit test for NATS connect; indirectly: TestRefresh_FullFlow verifies JWTFunc populates JWT | None | UNIT_ONLY | Integration test deferred per ADR-0063 |
| spec-k8s-arch:23 | Subscribe to mclaude.hosts.{hslug}.> | TestADR0063_SingleSubscription (controller_test.go:565) | None | UNIT_ONLY | Test validates subject format string; no live NATS subscription test |
| spec-k8s-arch:24 | StartRefreshLoop proactive JWT refresh before 5-min TTL | TestStartRefreshLoop_RefreshesBeforeTTL, _StopsOnContextCancel, _NoCPURL (host_auth_loop_test.go:47,103,147) | None | UNIT_ONLY | Deferred |
| spec-k8s-arch:59-68 | All env vars with correct defaults | TestEnvOrDefault (controller_test.go:292) | None | UNIT_ONLY | Only envOr helper tested; no test driving all env vars end-to-end |
| spec-k8s-arch:79/81-85 | Single subscription mclaude.hosts.{hslug}.> with create/delete/update dispatch | TestADR0063_SingleSubscription (controller_test.go:565); TestExtractOperation (controller_test.go:271) | None | UNIT_ONLY | Deferred |
| spec-k8s-arch:92 | Per-user namespace mclaude-{userSlug} not mclaude-{userId} | TestADR0062_NamespaceUsesSlug (controller_test.go:323) | None | UNIT_ONLY | Deferred |
| spec-k8s-arch:93 | user-secrets no nats-creds | TestADR0063_NoNATSCredsInSecrets, TestADR0063_StaleNATSCredsRemoved (controller_test.go:455,483) | None | UNIT_ONLY | Deferred |
| spec-k8s-arch:95 | project and nix PVCs with ownerReferences | TestGap2_PVCOwnerReferences (controller_test.go:931) | None | UNIT_ONLY | R5 backfill — unit test now present |
| spec-k8s-arch:96 | project Deployment with ownerReferences | TestGap1_DeploymentOwnerReference (controller_test.go:876) | None | UNIT_ONLY | R5 backfill — unit test now present |
| spec-k8s-arch:99 | All materialized resources carry ownerReferences via SetControllerReference | TestGap1_DeploymentOwnerReference, TestGap2_PVCOwnerReferences, TestADR0063_AgentNKeyOwnerReference (controller_test.go:876,931,754) | None | UNIT_ONLY | Deferred |
| spec-k8s-arch:107 | user-secrets oauth-token from DEV_OAUTH_TOKEN | TestADR0063_NoNATSCredsInSecrets (covers absence of nats-creds); no explicit test for DEV_OAUTH_TOKEN injection path | None | UNTESTED | No test verifies that oauth-token IS written when DEV_OAUTH_TOKEN is set |
| spec-k8s-arch:108 | Copies imagePullSecrets from controller's namespace | No dedicated test | None | UNTESTED | imagePullSecret copy path (reconciler.go:274-296) has no unit test |
| spec-k8s-arch:111 | Step 6b: agent-nkey Secret creation + NATS registration, idempotent | TestADR0063_AgentNKeySecretCreated, _Idempotent, _NATSRegistration, _OwnerReference (controller_test.go:585,629,690,754) | None | UNIT_ONLY | Deferred |
| spec-k8s-arch:112 | Pod env vars USER_ID, USER_SLUG, HOST_SLUG, PROJECT_ID, PROJECT_SLUG, NATS_URL, CONTROL_PLANE_URL; agent-nkey mount at /etc/mclaude/agent-nkey/ | TestADR0062_UserSlugEnvVar, TestADR0063_ControlPlaneURLInjected, TestADR0063_AgentNKeyMountedInPodTemplate (controller_test.go:378,425,802); HOST_SLUG is set to tpl.hostSlug; USER_ID and PROJECT_ID not explicitly tested | None | UNIT_ONLY | USER_ID and PROJECT_ID env vars not individually unit-tested, but covered by buildPodTemplate being called in multiple tests |
| spec-k8s-arch:129-132 | Corporate CA injection (namespace label, volume, mount, NODE_EXTRA_CA_CERTS, ca-bundle-hash annotation) | TestGap3_CorporateCAInjection (controller_test.go:995) — all 5 behaviors verified | None | UNIT_ONLY | R5 backfill — unit test now present |
| spec-common.md:127 | NewHostAuthFromCredsFile | TestNewHostAuthFromCredsFile_Valid, _InvalidPath, _InvalidContent (host_auth_test.go:77-112) | None | UNIT_ONLY | Deferred |
| spec-common.md:133-146 | Refresh() full flow including signature correctness | TestRefresh_FullFlow, _UpdatesStoredJWT, _ErrorWhenNoCPURL, _404ReturnsErrNotRegistered, _SignatureCorrectness (host_auth_test.go:196-382) | None | UNIT_ONLY | Deferred (real NATS JWT enforcement) |
| spec-common.md:148 | PublicKey() | TestPublicKey_Format, TestPublicKey_FromSeed (host_auth_test.go:158-190) | None | UNIT_ONLY | Deferred |
| spec-common.md:150 | JWTFunc() | TestNewHostAuthFromCredsFile_Valid (checks JWTFunc returns stored JWT), TestRefresh_UpdatesStoredJWT | None | UNIT_ONLY | Deferred |
| spec-common.md:152 | SignFunc() | TestSignFunc_ProducesSignature, TestSignFunc_SignatureVerifiable (host_auth_test.go:388-426) | None | UNIT_ONLY | Deferred |
| spec-common.md:154 | StartRefreshLoop() | TestStartRefreshLoop_RefreshesBeforeTTL, _StopsOnContextCancel, _NoCPURL (host_auth_loop_test.go:47,103,147) | None | UNIT_ONLY | Deferred |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| — | No open bugs with Component: mclaude-controller-k8s or mclaude-common/pkg/hostauth found in .agent/bugs/ | — | Files checked: 002-droid-shield-false-positives.md, 002-ptt-race-condition.md, 003-initial-message-injection-race.md, 005-getting-started-canned-text.md — none match this component |

### Summary

- Implemented: 47
- Gap: 0
- Partial: 0
- Infra: 28
- Unspec'd: 0
- Dead: 0
- Tested (unit+integration): 0
- Unit only: 25
- E2E only: 0
- Untested: 2
- Bugs fixed: 0
- Bugs open: 0
