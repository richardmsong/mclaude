## Run: 2026-05-01T00:00:00Z

Component: mclaude-controller-k8s
Specs: docs/adr-0063-k8s-architecture-spec.md, docs/mclaude-controller-k8s/spec-k8s-architecture.md, docs/mclaude-common/spec-common.md, docs/charts-mclaude/spec-helm.md
Code: mclaude-controller-k8s/{main.go,reconciler.go,nats_subscriber.go,nkeys.go,mcproject_types.go,controller_test.go}, charts/mclaude-worker/{values.yaml,Chart.yaml,templates/*}

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-k8s-architecture.md:13 | Read the NKey seed from HOST_NKEY_SEED_PATH (default /etc/mclaude/host-creds/nkey_seed) | main.go:53 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:14 | Call NewHostAuthFromSeed(seedPath, cpURL, log) | main.go:66 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:15 | Call Refresh() → HTTP challenge-response; if 404 log instruction and retry | main.go:74, hostauth/host_auth.go:140-178 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:19-20 | Retry 5s initial, doubling, 60s cap | main.go:171-198 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:21 | Decode hslug from received JWT | main.go:76-80 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:22 | Connect to hub NATS at HUB_NATS_URL | main.go:134-141 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:23 | Subscribe to mclaude.hosts.{hslug}.> (single wildcard) | nats_subscriber.go:61 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:24 | Background loop refreshes host JWT before TTL expiry | main.go:148, hostauth/host_auth.go:187-210 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:79 | Single wildcard mclaude.hosts.{hslug}.>, dispatch internally by subject suffix | nats_subscriber.go:61, 90-128 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:83 | users.*.projects.*.create — Request/reply, create MCProject CR, return success when reconcile reaches Ready | nats_subscriber.go:94-102, 133-171, 173-199 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:84 | users.*.projects.*.delete — Deletes MCProject CR; owner refs cascade | nats_subscriber.go:115-124, 225-247 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:85 | users.*.projects.*.update — Re-reconciles user-secrets | nats_subscriber.go:104-113, 202-222 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:106 | Ensures user namespace mclaude-{userSlug} | reconciler.go:89, 147-178 | IMPLEMENTED | — | ADR-0062 |
| spec-k8s-architecture.md:108 | user-secrets contains oauth-token; does NOT contain NATS credentials | reconciler.go:216-296 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:111 | reconcileAgentNKey: generate NKey pair, write nkey_seed to agent-nkey-{projectId}, register via agents.register, set ownerReferences | reconciler.go:503-615 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:112 | Session-agent pod: NATS_URL=HUB_NATS_URL, CONTROL_PLANE_URL injected from controller's env | reconciler.go:323-336 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:112 | Pod mounts agent-nkey-{projectId} Secret at /etc/mclaude/agent-nkey/ | reconciler.go:354-366 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:99 | Deployments, PVCs, Secrets carry ownerReferences to MCProject via SetControllerReference | reconciler.go:473-475, 640-641, 561-563 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:119 | Register public key with CP via NATS request mclaude.hosts.{hslug}.api.agents.register {nkey_public, userSlug, hostSlug, projectSlug} | reconciler.go:483-492, 585-591 | PARTIAL | SPEC→FIX | Spec says {nkey_public, userSlug, hostSlug, projectSlug} but code sends only {nkey_public, user_slug, project_slug} with no host_slug. Comment in code (reconciler.go:483-485) correctly explains host_slug is implicit in subject. Spec should be updated to reflect no host_slug in payload. |
| spec-common.md:130 | NewHostAuthFromSeed(seedPath, cpURL, log) constructor | hostauth/host_auth.go:80-99 | IMPLEMENTED | — | |
| spec-common.md:133-145 | Refresh() challenge-response, 404→ErrNotRegistered, log message | hostauth/host_auth.go:140-178 | IMPLEMENTED | — | |
| spec-common.md:148 | PublicKey() returns NKey public key | hostauth/host_auth.go:101-110 | IMPLEMENTED | — | |
| spec-common.md:150 | JWTFunc() — returns cached JWT, calls Refresh if empty | hostauth/host_auth.go:115-122 | PARTIAL | SPEC→FIX | Spec says "calls Refresh(ctx) if the cached JWT is empty"; code returns the cached JWT directly without calling Refresh. At boot Refresh() is called before JWTFunc() is used, so this works in practice. Spec is imprecise — JWTFunc doesn't call Refresh, it just returns the cached value (relying on bootRefresh having already populated it). |
| spec-common.md:153 | SignFunc() — NKey sign function | hostauth/host_auth.go:126-130 | IMPLEMENTED | — | |
| spec-common.md:154 | StartRefreshLoop(ctx, conn) — conn *nats.Conn; calls conn.Close() + reconnect if JWT changes | hostauth/host_auth.go:187-210 | PARTIAL | SPEC→FIX | Spec says StartRefreshLoop(ctx, conn *nats.Conn) and should call conn.Close()+reconnect if JWT changes. Actual impl: StartRefreshLoop(ctx context.Context) — takes no conn argument, just refreshes the in-memory JWT without disconnecting/reconnecting NATS. In practice JWTFunc() returns the refreshed JWT on reconnect; NATS client uses nats.UserJWT which re-calls JWTFunc on each reconnect. The spec's explicit conn.Close()+reconnect description is prescriptive about mechanism, not outcome. Code's approach is reasonable (less disruptive). SPEC→FIX: spec should drop the conn.Close()+reconnect mechanism description. |
| spec-helm.md:163 | Job {release}-gen-host-nkey, pre-install hook, weight -10 | templates/gen-host-nkey-job.yaml:68-69 | IMPLEMENTED | — | |
| spec-helm.md:163 | Writes decorated seed to Secret {release}-host-creds field nkey_seed | templates/host-creds-secret.yaml, gen-host-nkey-job.yaml | IMPLEMENTED | — | |
| spec-helm.md:163 | Idempotent — skips if Secret exists | gen-host-nkey-job.yaml:89 (command control-plane gen-host-nkey) | IMPLEMENTED | — | Idempotency is in the gen-host-nkey command implementation |
| spec-helm.md:170 | Controller Deployment: mounts {release}-host-creds at /etc/mclaude/host-creds/ | templates/controller-deployment.yaml:56-58, 76-78 | IMPLEMENTED | — | |
| spec-helm.md:170 | Env: HUB_NATS_URL, CONTROL_PLANE_URL, HOST_NKEY_SEED_PATH=/etc/mclaude/host-creds/nkey_seed | templates/controller-deployment.yaml:35-40 | IMPLEMENTED | — | |
| spec-helm.md:181 | ConfigMap {release}-session-agent-template with static pod template values | templates/session-agent-template.yaml | IMPLEMENTED | — | |
| spec-helm.md:193 | sessionAgent.terminationGracePeriodSeconds default 86400 | values.yaml:92 | IMPLEMENTED | — | |
| spec-helm.md:186 | controlPlane.url required | values.yaml:24, controller-deployment.yaml:37 | IMPLEMENTED | — | |
| spec-helm.md:188 | host.hubNatsUrl required | values.yaml:45, controller-deployment.yaml:36 | IMPLEMENTED | — | |
| spec-helm.md:189 | controller.replicas default 1 | values.yaml:61 | IMPLEMENTED | — | |
| adr-0063:125 | ServiceAccount/Role/RoleBinding for gen-host-nkey at weight -20 | gen-host-nkey-job.yaml:8-57 | IMPLEMENTED | — | |
| adr-0063:126 | Job gen-host-nkey at weight -10 | gen-host-nkey-job.yaml:68-69 | IMPLEMENTED | — | |
| adr-0063:127 | Secret {release}-host-creds, single field nkey_seed | host-creds-secret.yaml | IMPLEMENTED | — | |
| adr-0063:128 | Deployment {release}-controller with HUB_NATS_URL/CONTROL_PLANE_URL/HOST_NKEY_SEED_PATH env vars | controller-deployment.yaml:35-40 | IMPLEMENTED | — | |
| adr-0063:129 | ConfigMap {release}-session-agent-template | session-agent-template.yaml | IMPLEMENTED | — | |
| adr-0063:130 | ClusterRole/ClusterRoleBinding {release}-controller | clusterrole.yaml, clusterrolebinding.yaml | IMPLEMENTED | — | |
| adr-0063:130 | NOTES.txt post-install with mclaude host register instructions | templates/NOTES.txt | IMPLEMENTED | — | |
| adr-0063:148 | StartRefreshLoop(ctx) in main boot sequence | main.go:148 | IMPLEMENTED | — | |
| adr-0063:163 | Drop sessionAgentNATSURL() derivation — pass HUB_NATS_URL through directly | reconciler.go:123 (sessionAgentNATSURL: hubNATSURL), no derivation function | IMPLEMENTED | — | |
| adr-0063:164 | Owner references on Deployment, PVC, Secret via SetControllerReference | reconciler.go:473-475, 640-641, 561-563 | IMPLEMENTED | — | |
| adr-0063:144 | Drop env vars: NATS_ACCOUNT_SEED, NATS_CREDENTIALS_PATH, JS_DOMAIN, CLUSTER_SLUG | controller-deployment.yaml (none present) | IMPLEMENTED | — | |
| adr-0063:144 | Add env vars: HUB_NATS_URL, CONTROL_PLANE_URL, HOST_NKEY_SEED_PATH | controller-deployment.yaml:35-40 | IMPLEMENTED | — | |
| adr-0063:144 | Drop functions: loadAccountKey(), IssueSessionAgentJWT(), accountKP field | nkeys.go (stub only), reconciler.go (no accountKP, no IssueSessionAgentJWT) | IMPLEMENTED | — | |
| adr-0063:148-149 | reconcileSecrets: remove nats-creds, retain only oauth-token | reconciler.go:216-296 | IMPLEMENTED | — | |
| adr-0063:149 | CONTROL_PLANE_URL injected into session-agent pods from reconciler's own field, not from ConfigMap | reconciler.go:328, 45-48 | IMPLEMENTED | — | |
| adr-0063:162 | Host slug decoded from JWT itself, not from Helm value | main.go:76-80 | IMPLEMENTED | — | |
| adr-0063:279 | LEADER_ELECTION controlled via controller.leaderElection Helm value | values.yaml:61, controller-deployment.yaml:48 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:60-68 | Configuration table: HUB_NATS_URL, CONTROL_PLANE_URL, HOST_NKEY_SEED_PATH, HELM_RELEASE_NAME, SESSION_AGENT_TEMPLATE_CM, DEV_OAUTH_TOKEN, METRICS_ADDR, HEALTH_PROBE_ADDR, LEADER_ELECTION | main.go:48-57, 93-101 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:70-75 | Dropped env vars: NATS_ACCOUNT_SEED, NATS_CREDENTIALS_PATH, JS_DOMAIN, CLUSTER_SLUG, SESSION_AGENT_NATS_URL/NATS_URL | code/chart have none of these | IMPLEMENTED | — | |
| spec-k8s-architecture.md:128-131 | Corporate CA: label namespace, inject volume/mount at /etc/ssl/certs/corporate-ca-certificates.crt, NODE_EXTRA_CA_CERTS, ca-bundle-hash annotation | reconciler.go:370-392 | IMPLEMENTED | — | |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:32-35 | INFRA | Controller-runtime logger setup (SetLogger + zerolog init) — required boilerplate for kubebuilder. |
| main.go:84-105 | INFRA | Schema registration and controller-runtime Manager initialization — kubebuilder boilerplate for operator boot. |
| main.go:107-112 | INFRA | Health/readiness probe registration — standard kubebuilder pattern. |
| main.go:114-115 | INFRA | detectNamespace() helper — reads SA namespace from pod mount for self-discovery. |
| main.go:226-234 | INFRA | detectNamespace() implementation — reads /var/run/secrets/kubernetes.io/serviceaccount/namespace. |
| main.go:236-241 | INFRA | envOr() helper — standard env-var-with-default utility. |
| nats_subscriber.go:93 (case "provision") | UNSPEC'd | "provision" alias for create operation — not in spec, appears to be legacy from prior implementation. The spec only describes "create", "delete", "update" operations. |
| reconciler.go:37-53 | INFRA | MCProjectReconciler struct definition — controller state fields. All fields are spec-driven or infra boilerplate. |
| reconciler.go:55-59 | INFRA | natsConn interface — testability shim for *nats.Conn. Required for unit testing. |
| reconciler.go:62-145 | INFRA | Reconcile() main loop with phase transitions (Pending→Provisioning→Ready) — implements the spec-'d reconciler loop. |
| reconciler.go:302-412 | INFRA | buildPodTemplate() — constructs pod spec from template, implements spec-d pod env vars and mounts. |
| reconciler.go:414-435 | INFRA | reconcilerCAConfigMapHash() — deterministic hash of CA ConfigMap data for annotation-driven rollout. Required by corporate CA spec. |
| reconciler.go:649-664 | INFRA | ensureOwned() and addOwnerIfMissing() — owner-reference management helpers for shared namespace resources. |
| reconciler.go:681-727 | INFRA | loadTemplate() — loads session-agent-template ConfigMap; falls back to defaultTemplate(). |
| reconciler.go:729-753 | INFRA | setPhase(), updateCondition(), setCondition() — MCProject status management helpers. |
| reconciler.go:755-790 | INFRA | SetupWithManager() — controller-runtime watch setup for MCProject, Deployment, PVC, Secret, ConfigMap. |
| reconciler.go:793-835 | INFRA | sessionAgentTpl struct, defaultTemplate(), applyDefaultResources() — template value holders with defaults. |
| mcproject_types.go:1-138 | INFRA | CRD type definitions, scheme registration, DeepCopy implementations — kubebuilder boilerplate. |
| nkeys.go:1-9 | INFRA | Placeholder stub comment — all JWT-minting code removed per ADR-0063. |
| templates/_helpers.tpl | INFRA | Helm template helpers (name, fullname, labels, image resolution) — standard Helm chart boilerplate. |
| templates/mcproject-crd.yaml | INFRA | CRD definition — spec-driven (spec-k8s-architecture.md:91). |
| templates/controller-service.yaml | INFRA | Metrics service — spec-helm.md:171 (Service {release}-controller-metrics). |
| templates/clusterrole.yaml | INFRA | ClusterRole — spec-helm.md:173. |
| templates/clusterrolebinding.yaml | INFRA | ClusterRoleBinding — spec-helm.md:173. |
| templates/serviceaccount.yaml | INFRA | ServiceAccount — spec-helm.md:172. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-k8s-architecture.md:13-20 | Boot: read seed → NewHostAuthFromSeed → bootRefresh with retry | TestNewHostAuthFromSeed_Valid, TestRefresh_404ReturnsErrNotRegistered (hostauth pkg) | None | UNIT_ONLY | Boot sequence integration test (real CP) absent. Critical auth path. |
| spec-k8s-architecture.md:21 | Decode hslug from JWT | TestADR0063_HostSlugFromJWT | None | UNIT_ONLY | Unit test verifies extraction; no integration test against real JWT. |
| spec-k8s-architecture.md:23 | Single subscription mclaude.hosts.{hslug}.> | TestADR0063_SingleSubscription (string check only) | None | UNIT_ONLY | Test only checks string construction, not actual NATS subscribe. |
| spec-k8s-architecture.md:83-85 | Dispatch by subject suffix (create/delete/update) | TestExtractOperation | None | UNIT_ONLY | extractOperation tested; full message handling not integration-tested. |
| spec-k8s-architecture.md:106 | Namespace named mclaude-{userSlug} per ADR-0062 | TestADR0062_NamespaceUsesSlug | None | UNIT_ONLY | |
| spec-k8s-architecture.md:108 | user-secrets: oauth-token only, no nats-creds | TestADR0063_NoNATSCredsInSecrets, TestADR0063_StaleNATSCredsRemoved | None | UNIT_ONLY | |
| spec-k8s-architecture.md:111 | reconcileAgentNKey: generate NKey, write Secret, register via NATS | TestADR0063_AgentNKeySecretCreated, TestADR0063_AgentNKeySecretIdempotent, TestADR0063_AgentNKeyNATSRegistration, TestADR0063_AgentNKeyOwnerReference | None | UNIT_ONLY | NATS tested via fakeNATSConn; no test against real NATS with JWT permissions. Critical data flow. |
| spec-k8s-architecture.md:112 | CONTROL_PLANE_URL injected into pod | TestADR0063_ControlPlaneURLInjected | None | UNIT_ONLY | |
| spec-k8s-architecture.md:112 | agent-nkey Secret mounted at /etc/mclaude/agent-nkey/ + AGENT_NKEY_PATH | TestADR0063_AgentNKeyMountedInPodTemplate | None | UNIT_ONLY | |
| spec-k8s-architecture.md:99 | Owner references via SetControllerReference | TestADR0063_AgentNKeyOwnerReference, TestGap7_MultiOwner | None | UNIT_ONLY | |
| spec-common.md:130 | NewHostAuthFromSeed constructor | TestNewHostAuthFromSeed_Valid, TestNewHostAuthFromSeed_InvalidPath, TestNewHostAuthFromSeed_InvalidContent | None | UNIT_ONLY | |
| spec-common.md:133-145 | Refresh challenge-response, ErrNotRegistered | TestRefresh_FullFlow, TestRefresh_UpdatesStoredJWT, TestRefresh_ErrorWhenNoCPURL, TestRefresh_404ReturnsErrNotRegistered, TestRefresh_SignatureCorrectness | None | UNIT_ONLY | Tests use httptest.Server; no real NATS operator JWT enforcement tested. Critical auth flow. |
| spec-common.md:148 | PublicKey() | TestPublicKey_Format, TestPublicKey_FromSeed | None | UNIT_ONLY | |
| spec-common.md:153 | SignFunc() | TestSignFunc_ProducesSignature, TestSignFunc_SignatureVerifiable | None | UNIT_ONLY | |
| spec-common.md:154 | StartRefreshLoop | None | None | UNTESTED | No test exercises the background refresh loop behavior (timing, JWT update in background). |
| spec-helm.md:193 | terminationGracePeriodSeconds: 86400 in values | None (chart-level) | None | UNTESTED | No test verifies the ConfigMap value flows into pod TerminationGracePeriodSeconds. |
| adr-0063:279 | LEADER_ELECTION Helm value wired to binary | TestEnvOrDefault (partial) | None | UNIT_ONLY | Tests envOr helper but not the full leader-election wiring. |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| 002-droid-shield-false-positives.md | Droid-Shield False Positives | N/A | Component: Droid-Shield pre-commit scanner — not mclaude-controller-k8s. |
| 002-ptt-race-condition.md | Push-to-Talk race condition | N/A | Component: spa — not mclaude-controller-k8s. |
| 003-initial-message-injection-race.md | First-run message injection race | N/A | Component: spa — not mclaude-controller-k8s. |
| 005-getting-started-canned-text.md | Getting Started canned text | N/A | Component: spa — not mclaude-controller-k8s. |

No open bugs match component mclaude-controller-k8s.

### Summary

- Implemented: 48
- Gap: 0
- Partial: 3
- Infra: 23
- Unspec'd: 1
- Dead: 0
- Tested: 0
- Unit only: 15
- E2E only: 0
- Untested: 2
- Bugs fixed: 0
- Bugs open: 0
