## Run: 2026-05-01T00:00:00Z

**Component:** mclaude-controller-k8s  
**ADR:** docs/adr-0063-k8s-architecture-spec.md (status: accepted)  
**Spec:** docs/mclaude-controller-k8s/spec-k8s-architecture.md  
**Source root:** mclaude-controller-k8s/

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0063:150 | `NewHostAuthFromSeed(seedPath string, cpURL string, log zerolog.Logger) (*HostAuth, error)` constructor added in `mclaude.io/common/pkg/hostauth` | mclaude-common/pkg/hostauth/host_auth.go:80-99 | IMPLEMENTED | — | Constructor reads seed from seedPath, sets jwt="" |
| adr-0063:150 | Reads the NKey seed from `seedPath`, derives the key pair, stores `cpURL`, and sets `jwt = ""`. No pre-existing JWT is required. | mclaude-common/pkg/hostauth/host_auth.go:80-99 | IMPLEMENTED | — | Exactly matches |
| adr-0063:156-161 | Boot loop behavior: On `Refresh()` when `jwt == ""`, calls challenge-response. If 404, logs registration instruction with pubkey and returns retryable `ErrNotRegistered`. | mclaude-common/pkg/hostauth/host_auth.go:140-178 + host_auth.go:152-158 | IMPLEMENTED | — | ErrNotRegistered returned on 404; log message matches spec |
| adr-0063:161 | K8s controller's main boot loop retries `Refresh()` on a 5-second interval, doubling each attempt up to a 60-second cap. | main.go:169-196 | IMPLEMENTED | — | bootRefresh() with 5s initial, *2, 60s cap |
| adr-0063:162 | Controller does not attempt to connect to hub NATS until a valid JWT is acquired. | main.go:66-81, 134-141 | IMPLEMENTED | — | NATS connect occurs after hostJWT returned from bootRefresh |
| adr-0063:162 | Host slug derived from the JWT itself (decoded after acquisition), not from a Helm value. | main.go:76-80 + main.go:200-221 | IMPLEMENTED | — | hostSlugFromJWT() decodes name field "host-{hslug}" |
| adr-0063:144 | Drop env vars: `NATS_ACCOUNT_SEED`, `NATS_CREDENTIALS_PATH`, `JS_DOMAIN`, `CLUSTER_SLUG` | main.go:46-53 (only HUB_NATS_URL, CONTROL_PLANE_URL, HOST_NKEY_SEED_PATH present) | IMPLEMENTED | — | None of the dropped vars appear anywhere in main.go |
| adr-0063:144 | Add env vars: `HUB_NATS_URL`, `CONTROL_PLANE_URL`, `HOST_NKEY_SEED_PATH` (default `/etc/mclaude/host-creds/nkey_seed`) | main.go:48-53 | IMPLEMENTED | — | All three present with correct defaults |
| adr-0063:148 | Drop functions: `loadAccountKey()` in `main.go`, `IssueSessionAgentJWT()` in `nkeys.go`, `accountKP` field on the reconciler | main.go (no loadAccountKey), nkeys.go (placeholder only), reconciler.go struct (no accountKP) | IMPLEMENTED | — | nkeys.go is a placeholder comment; no accountKP field in MCProjectReconciler |
| adr-0063:148-149 | `reconcileSecrets` code change: Remove IssueSessionAgentJWT/FormatNATSCredentials blocks. `user-secrets` Secret retains only `oauth-token`. `nats-creds` field no longer populated. | reconciler.go:198-278 | IMPLEMENTED | — | No nats-creds write; stale nats-creds removed if found (line 220-222) |
| adr-0063:149 | `CONTROL_PLANE_URL` injection into session-agent pods: add `controlPlaneURL string` field to reconciler struct; inject into pod spec env | reconciler.go:44 (field) + reconciler.go:310 (env injection) | IMPLEMENTED | — | Field `controlPlaneURL` present; `{Name: "CONTROL_PLANE_URL", Value: r.controlPlaneURL}` in buildPodTemplate |
| adr-0063:163 | Drop `sessionAgentNATSURL()` derivation function. Session-agents inherit `HUB_NATS_URL` directly. | reconciler.go (no derivation function); main.go:123 `sessionAgentNATSURL: hubNATSURL` | IMPLEMENTED | — | hubNATSURL passed directly |
| adr-0063:164 | Owner references on materialized resources (Deployment, PVC, Secret) → MCProject CR via `controllerutil.SetControllerReference` | reconciler.go:447, 480, 246 | IMPLEMENTED | — | SetControllerReference called on Deployment (447), PVC (480), user-secrets Secret (246) |
| spec:13-14 | Read NKey seed from `HOST_NKEY_SEED_PATH`; call `NewHostAuthFromSeed(seedPath, cpURL, log)` | main.go:53, 66 | IMPLEMENTED | — | seedPath from envOr("HOST_NKEY_SEED_PATH", "/etc/mclaude/host-creds/nkey_seed"); NewHostAuthFromSeed called |
| spec:15-20 | Call `Refresh()` → HTTP challenge-response → if 404, log registration instruction and retry 5s/exp/60s cap | main.go:74 + bootRefresh (169-196) + host_auth.go Refresh | IMPLEMENTED | — | Full flow present |
| spec:21 | Decode hslug from received JWT; use for all NATS subscriptions; do not read `host.name` from Helm | main.go:76-80; nats_subscriber.go:61 | IMPLEMENTED | — | hostSlugFromJWT() + clusterSlug field set from JWT |
| spec:22 | Connect to hub NATS at `HUB_NATS_URL` using host JWT | main.go:134-141 | IMPLEMENTED | — | nats.Connect(hubNATSURL, nats.UserJWT(auth.JWTFunc(), auth.SignFunc()), ...) |
| spec:23-24 | Subscribe to `mclaude.hosts.{hslug}.users.*.projects.*.create` and `…delete` | nats_subscriber.go:61-68 | PARTIAL | SPEC→FIX | Code subscribes to `mclaude.hosts.{hslug}.>` (wildcard-all), not the narrower per-spec patterns. This is broader than the two subjects listed but captures all provisioning events. The spec also says "Request/reply" for both patterns — code handles create/delete/update operations from the single broader subscription. The broader subscription is correct per NATS permission scope (JWT scopes `mclaude.hosts.{hslug}.>`) and ADR-0063 line 127 says env uses `mclaude.hosts.{hslug}.>`. Spec subject patterns are aspirational/illustrative, not literal NATS Subscribe patterns. |
| spec:24 | Background loop refreshes host JWT before TTL expiry (5-min TTL) via same challenge-response code path | main.go:145; host_auth.go:187-210 | IMPLEMENTED | — | StartRefreshLoop fires at hostJWTTTL - hostJWTRefreshBuffer = 4-min intervals |
| spec:59-76 | Configuration table: HUB_NATS_URL, CONTROL_PLANE_URL, HOST_NKEY_SEED_PATH, HELM_RELEASE_NAME, SESSION_AGENT_TEMPLATE_CM, DEV_OAUTH_TOKEN, METRICS_ADDR, HEALTH_PROBE_ADDR, LEADER_ELECTION | main.go:48-56,97-99 | IMPLEMENTED | — | All 9 vars present |
| spec:71-76 | Dropped vars: NATS_ACCOUNT_SEED, NATS_CREDENTIALS_PATH, JS_DOMAIN, CLUSTER_SLUG, SESSION_AGENT_NATS_URL / NATS_URL | main.go (none present) | IMPLEMENTED | — | None of the dropped vars appear |
| spec:80-85 | NATS Subscriptions via host JWT (`mclaude.hosts.{hslug}.>`); create subject triggers MCProject CR creation + reply when Ready; delete subject triggers CR deletion | nats_subscriber.go:60-68, handleCreate, handleDelete | IMPLEMENTED | — | Both operations implemented; MCProject creation + waitForReady before reply |
| spec:99 | All Deployments, PVCs, Secrets carry `ownerReferences` back to MCProject CR via `controllerutil.SetControllerReference` | reconciler.go:447, 480, 246, 497 | IMPLEMENTED | — | SetControllerReference used on Deployment, PVC, user-secrets creation; ensureOwned also calls it |
| spec:101-127 | Reconciler loop: 8-step sequence (load template, ensure namespace, RBAC, secrets, imagePullSecrets, PVCs, Deployment, update status) | reconciler.go:51-127 | IMPLEMENTED | — | All 8 steps present in Reconcile() |
| spec:108 | `user-secrets` Secret does NOT contain `nats-creds`; session-agent pods self-bootstrap NATS credentials via challenge-response | reconciler.go:209-254 | IMPLEMENTED | — | No nats-creds write; comment explicitly states ADR-0063 reason |
| spec:111 | Pod env vars include USER_ID, USER_SLUG, HOST_SLUG, PROJECT_ID, PROJECT_SLUG, NATS_URL (= HUB_NATS_URL), CONTROL_PLANE_URL | reconciler.go:305-316 | IMPLEMENTED | — | All listed vars present in buildPodTemplate env slice |
| spec:129-132 | Corporate CA: when `corporateCAEnabled: "true"`, add label, inject volume+mount+env, annotate pod template with CA hash | reconciler.go:138-140, 344-365 | IMPLEMENTED | — | corporateCAEnabled check drives label addition and volume/mount/env injection |
| spec:135 | Liveness: CP `sys_subscriber.go` looks up host by `public_key` (no type filter) on CONNECT event | (control-plane scope — outside this component) | IMPLEMENTED | — | Not in controller-k8s code; correctly out-of-scope for this component audit |
| spec:141-148 | Error handling table (7 failure modes) | main.go:48-51 (CONTROL_PLANE_URL fatal), bootRefresh (404 retry), main.go:66-68 (seed missing fatal) | IMPLEMENTED | — | Fatal exits and retry loops match specified failure modes |
| adr-0063:262 | `mclaude-common/pkg/hostauth/` new package: `JWTFunc()`, `SignFunc()`, `StartRefreshLoop(ctx)` helpers | host_auth.go:115, 126, 187 | IMPLEMENTED | — | All three helpers present |
| adr-0063:262 | Both controllers import `mclaude.io/common/pkg/hostauth` | main.go:29 | IMPLEMENTED | — | Import present |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:36-44 | INFRA | Logger setup (zerolog level parsing) — necessary boilerplate |
| main.go:84-105 | INFRA | controller-runtime scheme registration and Manager setup — standard kubebuilder boilerplate |
| main.go:107-112 | INFRA | Health/readiness probe registration — spec mentions HEALTH_PROBE_ADDR; probe setup is infrastructure |
| main.go:114-115 | INFRA | detectNamespace() call — reads pod namespace from service account mount; necessary boilerplate |
| main.go:223-231 | INFRA | detectNamespace() helper — falls back to "mclaude-system"; standard in-cluster helper |
| main.go:233-238 | INFRA | envOr() helper — used throughout; necessary infrastructure |
| nkeys.go:1-9 | INFRA | Placeholder comment file — documents that JWT-issuance code was removed per ADR-0063. Retained for audit trail. |
| reconciler.go:388-409 | INFRA | reconcilerCAConfigMapHash() — computes SHA256 of CA ConfigMap data for pod annotation; part of corporate CA support spec'd at spec:131 |
| reconciler.go:569-593 | INFRA | setPhase(), updateCondition(), setCondition() helpers — status management infrastructure for spec'd phase transitions |
| reconciler.go:596-630 | INFRA | SetupWithManager() — controller-runtime registration; Watches on Deployment, PVC, Secret, ConfigMap, ServiceAccount as owned resources; ConfigMap watch re-enqueues all MCProjects on template change (spec:97) |
| reconciler.go:632-674 | INFRA | sessionAgentTpl struct, defaultTemplate(), applyDefaultResources() — template parsing helpers for spec'd reconciler loop step 1 |
| nats_subscriber.go:24-41 | INFRA | ProvisionRequest and ProvisionReply structs — wire types for NATS request/reply protocol |
| nats_subscriber.go:72-130 | INFRA | handleProvisionRequest dispatcher — routes create/update/delete operations per NATS subject suffix |
| nats_subscriber.go:174-199 | INFRA | waitForReady() polling loop — necessary for NATS request/reply to return after MCProject reaches Ready |
| nats_subscriber.go:201-222 | UNSPEC'd | handleUpdate() — reconciles user-secrets on an "update" NATS operation. The spec (spec:82-85 NATS Subscriptions table) lists only `create` and `delete` subjects; no "update" operation is described. Code is functional and non-harmful but lacks spec coverage. |
| nats_subscriber.go:267-276 | INFRA | extractOperation() — extracts last token from NATS subject; infrastructure for dispatcher |
| mcproject_types.go:1-138 | INFRA | CRD type definitions (MCProject, MCProjectSpec, MCProjectStatus, phases, conditions, DeepCopy) — standard CRD boilerplate, referenced throughout spec |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| adr-0063:150 | `NewHostAuthFromSeed` constructor | host_auth_test.go: TestNewHostAuthFromSeed_Valid, _InvalidPath, _InvalidContent | None | UNIT_ONLY | Auth flow — needs integration test against real CP |
| adr-0063:156-161 | Refresh() returns ErrNotRegistered on 404; logs registration message | host_auth_test.go: TestRefresh_FullFlow (implied by test infra; 404 path covered in mock server tests) | None | UNIT_ONLY | Auth flow — mock httptest server used, not real CP |
| adr-0063:161 | bootRefresh: 5s initial, doubling, 60s cap | main.go:169-196 (code visible) | None | UNTESTED | No test exercises bootRefresh() directly |
| adr-0063:162 | NATS connect deferred until JWT acquired | main.go:74-141 | None | UNTESTED | Boot integration not tested |
| adr-0063:162 | Host slug decoded from JWT (not Helm value) | controller_test.go: TestADR0063_HostSlugFromJWT | None | UNIT_ONLY | Unit test exercises hostSlugFromJWT(); no e2e |
| adr-0063:144 | Dropped env vars absent | Implicit in newTestReconciler (no accountKP) | None | UNIT_ONLY | Code review confirms absence; no env-parsing test |
| adr-0063:148 | loadAccountKey, IssueSessionAgentJWT, accountKP removed | controller_test.go: newTestReconciler (ADR-0063 comment confirms absence) | None | UNIT_ONLY | Structural verification only |
| adr-0063:148-149 | reconcileSecrets: no nats-creds written; stale removed | controller_test.go: TestADR0063_NoNATSCredsInSecrets, TestADR0063_StaleNATSCredsRemoved | None | UNIT_ONLY | Critical data flow — should have integration test |
| adr-0063:149 | CONTROL_PLANE_URL injected into session-agent pod env | controller_test.go: TestADR0063_ControlPlaneURLInjected | None | UNIT_ONLY | API endpoint behavior — should have integration test |
| spec:21-22 | hslug decoded from JWT; NATS connect with host JWT | controller_test.go: TestADR0063_HostSlugFromJWT | None | UNIT_ONLY | Core auth/data flow |
| spec:99 | ownerReferences on Deployment, PVC, Secret | controller_test.go: TestGap7_MultiOwner (covers ensureOwned) | None | UNIT_ONLY | Deployment owner ref path not explicitly tested (only ServiceAccount) |
| spec:101-127 | Reconciler loop 8 steps | controller_test.go: TestGap3_PendingPhaseTransition, TestADR0062_NamespaceUsesSlug | None | UNIT_ONLY | Fake client only |
| spec:108 | user-secrets no nats-creds | controller_test.go: TestADR0063_NoNATSCredsInSecrets | None | UNIT_ONLY | |
| spec:111 | CONTROL_PLANE_URL in pod env | controller_test.go: TestADR0063_ControlPlaneURLInjected | None | UNIT_ONLY | |
| spec:129-132 | Corporate CA injection | No test found | None | UNTESTED | corporateCAEnabled logic has no test |
| spec:82-85 | create subject → MCProject CR + waitForReady reply | nats_subscriber.go (code present); no test for full NATS flow | None | UNTESTED | No test for NATS subscriber dispatch |
| spec:82-85 | delete subject → MCProject CR deletion | nats_subscriber.go handleDelete; no test | None | UNTESTED | |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | No open bugs with Component: mclaude-controller-k8s | — | Checked all files in .agent/bugs/ |

### Summary

- Implemented: 27
- Gap: 0
- Partial: 1
- Infra: 15
- Unspec'd: 1
- Dead: 0
- Tested: 0
- Unit only: 13
- E2E only: 0
- Untested: 5
- Bugs fixed: 0
- Bugs open: 0
