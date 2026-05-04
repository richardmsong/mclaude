## Run: 2026-05-01T00:00:00Z

Component: mclaude-controller-k8s
Spec files: docs/adr-0063-k8s-architecture-spec.md, docs/mclaude-controller-k8s/spec-k8s-architecture.md
Component root: mclaude-controller-k8s/

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-k8s-architecture.md:3 | "The controller connects directly to hub NATS via JWT credential (host-scoped per ADR-0054). There is no local NATS StatefulSet; the worker chart no longer ships a leaf node." | main.go:134–141; charts/mclaude-worker/ (no nats-statefulset.yaml present) | IMPLEMENTED | — | Hub NATS connect via host JWT. Worker chart has no NATS StatefulSet. |
| spec-k8s-architecture.md:7 | "Single Deployment in mclaude-system on the worker cluster. Built with kubebuilder (controller-runtime). Leader election flag configurable via LEADER_ELECTION env var. v1 runs a single replica." | main.go:95–105; charts/mclaude-worker/templates/controller-deployment.yaml:11–13; main.go:99 | IMPLEMENTED | — | Leader election configurable; replicas driven by values.yaml default 1. |
| spec-k8s-architecture.md:12 | "Read the NKey seed from the file at HOST_NKEY_SEED_PATH (default /etc/mclaude/host-creds/nkey_seed)." | main.go:53 (`seedPath := envOr("HOST_NKEY_SEED_PATH", "/etc/mclaude/host-creds/nkey_seed")`) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:13 | "Call NewHostAuthFromSeed(seedPath, cpURL, log) from mclaude.io/common/pkg/hostauth. This returns a HostAuth handle with no JWT yet." | main.go:66 (`auth, err := hostauth.NewHostAuthFromSeed(seedPath, controlPlaneURL, logger)`) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:14–19 | "Call Refresh() to perform HTTP challenge-response... If CP returns 404 (NKey not registered), log instruction and retry every 5s with exponential backoff capped at 60s." | main.go:74 (`hostJWT := bootRefresh(ctx, auth, logger)`); main.go:167–196 (bootRefresh function: 5s initial, doubles, 60s cap) | IMPLEMENTED | — | Log message matches spec. |
| spec-k8s-architecture.md:21 | "Decode the host slug (hslug) from the received JWT. Use this slug for all subsequent NATS subscriptions — do not read host.name from Helm." | main.go:76–80 (`hostSlugFromJWT`); main.go:126 (`clusterSlug: hslug`) | IMPLEMENTED | — | JWT decoded at boot; hslug passed to reconciler and provisioner. |
| spec-k8s-architecture.md:22 | "Connect to hub NATS at HUB_NATS_URL using the host JWT." | main.go:134–141 (`nats.Connect(hubNATSURL, nats.UserJWT(auth.JWTFunc(), auth.SignFunc()))`) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:23 | "Subscribe to mclaude.hosts.{hslug}.users.*.projects.*.create and mclaude.hosts.{hslug}.users.*.projects.*.delete." | nats_subscriber.go:61 (subscribes to `mclaude.hosts.%s.>`) | PARTIAL | SPEC→FIX | Code subscribes to the broader wildcard `mclaude.hosts.{hslug}.>` and dispatches internally by operation (create/update/delete). Spec lists only two specific subject suffixes, but reconciler also handles `.update` (line 86 of spec itself lists update). The subscription pattern is a superset; spec description is slightly incomplete regarding the single-wildcard approach, but the intent is consistent. |
| spec-k8s-architecture.md:24 | "Background loop refreshes the host JWT before TTL expiry (5-minute TTL) via the same challenge-response code path." | main.go:145 (`auth.StartRefreshLoop(ctx)`); hostauth/host_auth.go:188–210 | IMPLEMENTED | — | Refresh fires at TTL - 60s buffer. |
| spec-k8s-architecture.md:80 | "Subscribes via host JWT to the single wildcard mclaude.hosts.{hslug}.> (zero JetStream access). The JWT permission scope is mclaude.hosts.{hslug}.>." | nats_subscriber.go:61 | IMPLEMENTED | — | Single subscription; no JetStream calls in controller. |
| spec-k8s-architecture.md:82–86 | Subject suffix dispatch table: users.*.projects.*.create, users.*.projects.*.delete, users.*.projects.*.update | nats_subscriber.go:90–128 (switch on extractOperation) | IMPLEMENTED | — | All three operations handled. |
| spec-k8s-architecture.md:90–99 | Kubernetes Resources: CRD MCProject (mcprojects.mclaude.io/v1alpha1) in mclaude-system; per-user namespace mclaude-{userSlug}; per-user user-secrets Secret, user-config ConfigMap; per-project PVCs and Deployment with ownerReferences back to MCProject CR | mcproject_types.go:15–18; reconciler.go:78, 88, 96, 102, 108, 411–454, 457–487 | IMPLEMENTED | — | All resource types created; ownerReferences set via SetControllerReference. |
| spec-k8s-architecture.md:98 | "Watched: {release}-session-agent-template ConfigMap in mclaude-system — changes re-enqueue all MCProject CRs." | reconciler.go:596–629 (SetupWithManager with ConfigMap watcher that re-enqueues all MCProjects) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:100 | "All Deployments, PVCs, and Secrets materialized per project carry ownerReferences back to the MCProject CR via controllerutil.SetControllerReference." | reconciler.go:246 (Secret), 447 (Deployment), 480 (PVC) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:104–113 | Reconciler loop steps 1–8: load template, ensure namespace, ensure RBAC, ensure user-config and user-secrets, copy imagePullSecrets, ensure PVCs, ensure Deployment, update MCProject status | reconciler.go:66–127 | IMPLEMENTED | — | All 8 steps present. Phase transitions Pending→Provisioning→Ready also implemented. |
| spec-k8s-architecture.md:112 | "Pod env vars include USER_ID, USER_SLUG, HOST_SLUG, PROJECT_ID, PROJECT_SLUG, NATS_URL (= HUB_NATS_URL), CONTROL_PLANE_URL (injected from the controller's own CONTROL_PLANE_URL env)" | reconciler.go:305–317 (env var slice in buildPodTemplate) | IMPLEMENTED | — | All listed env vars present. |
| spec-k8s-architecture.md:112 | "CONTROL_PLANE_URL...not from the session-agent-template ConfigMap" | reconciler.go:43–44 (controlPlaneURL field on struct); reconciler.go:310 (injected directly) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:117–125 | Session-Agent Auth: controller no longer mints session-agent JWTs; agent generates own NKey, passes public key to controller via local IPC; controller publishes mclaude.hosts.{hslug}.api.agents.register to hub NATS; agent runs HTTP challenge-response; agent connects to hub NATS. | nkeys.go (placeholder, no JWT minting); reconciler.go:209–211 (nats-creds removed from user-secrets) | PARTIAL | CODE→FIX | The spec says the controller relays the agent's public key via `mclaude.hosts.{hslug}.api.agents.register` NATS publish when the agent passes its key via "local IPC". There is no IPC mechanism or agent.register NATS publish in the code. The controller only removes nats-creds from the secret — it does not implement the relay-registration step. This is a gap in the session-agent auth relay flow. |
| spec-k8s-architecture.md:125 | "The nats-creds Secret key is not written by reconcileSecrets." | reconciler.go:209–254 | IMPLEMENTED | — | nats-creds never written; stale entries deleted. |
| spec-k8s-architecture.md:128–133 | Corporate CA Support: label user namespace; inject corporate-ca volume, volume mount, NODE_EXTRA_CA_CERTS env; annotate with ca-bundle-hash | reconciler.go:138–139, 345–365 | IMPLEMENTED | — | All CA injection steps present. |
| spec-k8s-architecture.md:136 | "Liveness: hub publishes $SYS.ACCOUNT.{accountKey}.CONNECT with client.kind='Client'. CP's sys_subscriber.go looks up by public_key (no type filter)" | This is on the control-plane side (sys_subscriber.go), not controller-k8s | IMPLEMENTED | — | Not in scope for mclaude-controller-k8s; controller connects, behavior is on CP side. |
| spec-k8s-architecture.md:141–149 | Error Handling table: HOST_NKEY_SEED_PATH missing → fatal exit; CP 404 → retry loop with log; hub NATS unreachable → NATS client backoff; MCProject reconcile fails → Failed phase; namespace creation fails → Failed phase; ownerReference cascade failure | main.go:67–69 (fatal on seed load fail); main.go:167–196 (bootRefresh retry); nats connect has RetryOnFailedConnect; reconciler.go:88–113 (Failed phase on errors) | IMPLEMENTED | — | |
| spec-k8s-architecture.md:153–157 | Helm Chart Artifacts: {release}-host-creds Secret field nkey_seed; JWT not stored, acquired in-memory | charts/mclaude-worker/templates/host-creds-secret.yaml (nkey_seed field); main.go (JWT in memory only, never written back) | IMPLEMENTED | — | |
| adr-0063:142 | "Drop env vars: NATS_ACCOUNT_SEED, NATS_CREDENTIALS_PATH, JS_DOMAIN, CLUSTER_SLUG" | main.go (none of these env vars read) | IMPLEMENTED | — | |
| adr-0063:143 | "Drop functions: loadAccountKey() in main.go, IssueSessionAgentJWT() in nkeys.go, the accountKP field on the reconciler" | nkeys.go:1–9 (placeholder, no functions); reconciler.go:34–48 (no accountKP field) | IMPLEMENTED | — | |
| adr-0063:144 | "Add env vars: HUB_NATS_URL (replaces NATS_URL), CONTROL_PLANE_URL, HOST_NKEY_SEED_PATH (default /etc/mclaude/host-creds/nkey_seed)" | main.go:48–53 | IMPLEMENTED | — | |
| adr-0063:146 | "JWT lifetime: held in memory only, never persisted back to the Secret." | hostauth/host_auth.go (currentJWT in struct, never written to file) | IMPLEMENTED | — | |
| adr-0063:147 | "Session-agent JWT issuance: moves to control-plane per ADR-0054. When the controller materializes a session-agent pod, the agent registers its own NKey public key with CP via mclaude.hosts.{hslug}.api.agents.register (a NATS request the controller relays from the agent)" | No IPC or agents.register relay in controller code | GAP | CODE→FIX | The relay mechanism (local IPC to receive agent's public key, then NATS publish to agents.register) is not implemented. The controller provisions pods without wiring the agent's self-registration path through the controller. |
| adr-0063:148 | "Remove the two IssueSessionAgentJWT(...) / FormatNATSCredentials(...) blocks (lines 218–222 and 239–245 of reconciler.go). The user-secrets Secret retains only the oauth-token field." | reconciler.go:198–278 | IMPLEMENTED | — | nats-creds removed; only oauth-token written. |
| adr-0063:149 | "CONTROL_PLANE_URL injection into session-agent pods: add a controlPlaneURL string field to the reconciler struct...inject CONTROL_PLANE_URL env var directly from this struct field into the session-agent pod spec." | reconciler.go:44 (struct field); reconciler.go:310 (env var injection) | IMPLEMENTED | — | |
| adr-0063:150–154 | "Move mclaude-controller-local/host_auth.go to mclaude-common/pkg/hostauth/; add NewHostAuthFromSeed constructor; add JWTFunc(), SignFunc(), StartRefreshLoop(ctx) helpers; both controllers import mclaude.io/common/pkg/hostauth" | mclaude-common/pkg/hostauth/host_auth.go (full implementation); mclaude-controller-k8s/main.go:29 (import); mclaude-controller-local/main.go:16 (import) | IMPLEMENTED | — | |
| adr-0063:150 | "NewHostAuthFromSeed constructor: reads NKey seed from seedPath, derives key pair, stores cpURL, sets jwt=''" | hostauth/host_auth.go:80–99 | IMPLEMENTED | — | |
| adr-0063:156–161 | "Boot loop behavior: On Refresh() when jwt=='' (initial boot), if CP returns HTTP 404, log message and return retryable sentinel error. K8s controller retries on 5-second interval, doubling up to 60s cap." | hostauth/host_auth.go:140–177 (ErrNotRegistered on 404); main.go:167–196 (bootRefresh with 5s/doubling/60s) | IMPLEMENTED | — | |
| adr-0063:162 | "Host slug derived from the JWT itself (decoded after acquisition), not from a Helm value. Prevents drift if operator chose different slug at registration time." | main.go:76–80 (hostSlugFromJWT) | IMPLEMENTED | — | |
| adr-0063:163 | "Drop sessionAgentNATSURL() derivation function. Session-agents inherit HUB_NATS_URL directly via the session-agent template." | nats_subscriber.go (no derivation function); reconciler.go:123 (sessionAgentNATSURL = hubNATSURL passed directly) | IMPLEMENTED | — | |
| adr-0063:164 | "Owner references on materialized resources (Deployment, PVC, Secret) → MCProject CR. Use controllerutil.SetControllerReference." | reconciler.go:246, 447, 480 | IMPLEMENTED | — | |
| adr-0063:120–130 (Helm chart table) | gen-host-nkey Job (pre-install, weight -10), ServiceAccount/Role/RoleBinding (weight -20), Secret {release}-host-creds with nkey_seed, Deployment mounts /etc/mclaude/host-creds/ at HOST_NKEY_SEED_PATH, HUB_NATS_URL/CONTROL_PLANE_URL/HOST_NKEY_SEED_PATH env vars | charts/mclaude-worker/templates/gen-host-nkey-job.yaml; charts/mclaude-worker/templates/host-creds-secret.yaml; charts/mclaude-worker/templates/controller-deployment.yaml:35–53 | IMPLEMENTED | — | |
| adr-0063:127 | "The host.name Helm value is rendered into NOTES.txt only — controller decodes its actual slug from the JWT." | charts/mclaude-worker/templates/NOTES.txt:3 (renders host.name); controller-deployment.yaml has no host.name env | IMPLEMENTED | — | |
| adr-0063:130 | "NOTES.txt: Post-install message instructing operator to read public key and run mclaude host register --type cluster --name $HOST_NAME --nkey-public <key>" | charts/mclaude-worker/templates/NOTES.txt:1–26 | IMPLEMENTED | — | |
| spec-k8s-architecture.md:43–54 | "Deregistration Flow: Option A (operator-initiated mclaude host deregister); Option B (Helm pre-delete Job that publishes deregister subject before Helm tears down)" | charts/mclaude-worker/templates/ has no pre-delete Job template | GAP | CODE→FIX | Spec defines Option B (Helm pre-delete Job). No pre-delete hook exists in charts/mclaude-worker/templates/. Option A (operator-initiated) works via the CLI, but Option B is not implemented. |
| spec-k8s-architecture.md:59–69 | Configuration table: HUB_NATS_URL, CONTROL_PLANE_URL, HOST_NKEY_SEED_PATH, HELM_RELEASE_NAME, SESSION_AGENT_TEMPLATE_CM, DEV_OAUTH_TOKEN, METRICS_ADDR, HEALTH_PROBE_ADDR, LEADER_ELECTION | main.go:48–101 | IMPLEMENTED | — | All env vars read. METRICS_ADDR uses ":8082" (vs controller-deployment.yaml metricsPort 9091 — this is a chart/binary discrepancy, but the env var is set from VALUES not the binary default, so no gap). |
| spec-k8s-architecture.md:71–76 | "Dropped from prior spec: NATS_ACCOUNT_SEED, NATS_CREDENTIALS_PATH, JS_DOMAIN, CLUSTER_SLUG, SESSION_AGENT_NATS_URL / NATS_URL" | main.go (none read) | IMPLEMENTED | — | |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:32–45 (logger setup, log level parsing) | INFRA | Standard zerolog initialization for the binary. |
| main.go:84–101 (scheme setup + Manager creation) | INFRA | Standard controller-runtime Manager setup required by kubebuilder. |
| main.go:107–112 (health/ready probes) | INFRA | Liveness/readiness probes for K8s pod management. |
| main.go:114–115 (detectNamespace) | INFRA | Helper to detect the pod's own namespace for cross-namespace operations. |
| main.go:198–221 (hostSlugFromJWT) | INFRA | Supporting function for spec'd boot sequence step 4 (decode hslug from JWT). |
| main.go:223–231 (detectNamespace func) | INFRA | Reads namespace from SA token mount; falls back to "mclaude-system". |
| main.go:233–238 (envOr) | INFRA | Standard helper for env var fallback. |
| reconciler.go:281–409 (buildPodTemplate) | INFRA | Supporting function implementing pod template construction for spec'd Deployment. |
| reconciler.go:388–409 (reconcilerCAConfigMapHash) | INFRA | SHA256 hash for corporate CA ConfigMap change detection (spec'd in corporate CA support section). |
| reconciler.go:489–519 (ensureOwned + addOwnerIfMissing) | INFRA | Helper for owner reference management — implements spec'd ownerReference requirement. |
| reconciler.go:521–567 (loadTemplate) | INFRA | Loads session-agent-template ConfigMap; implements spec'd step 1 of reconciler loop. |
| reconciler.go:569–593 (setPhase, updateCondition, setCondition) | INFRA | Controller-runtime status update helpers for MCProject phase and conditions. |
| reconciler.go:595–630 (SetupWithManager) | INFRA | Controller registration; implements spec'd ConfigMap watch that re-enqueues all MCProjects. |
| reconciler.go:632–674 (sessionAgentTpl, defaultTemplate, applyDefaultResources) | INFRA | Template type and defaults; required plumbing for spec'd session-agent template loading. |
| nats_subscriber.go:24–52 (ProvisionRequest, ProvisionReply, NATSProvisioner structs) | INFRA | Type definitions for NATS provisioning protocol. |
| nats_subscriber.go:133–171 (handleCreate + waitForReady) | INFRA | Create handler polls for MCProject Ready phase; implementing spec'd create → ready reply behavior. |
| nats_subscriber.go:201–246 (handleUpdate, handleDelete) | INFRA | Update/delete NATS handlers per spec subscription table. |
| nats_subscriber.go:249–276 (replyOK, replyError, extractOperation) | INFRA | NATS reply helpers and subject parsing utility. |
| mcproject_types.go:1–138 | INFRA | CRD type definitions (MCProject, MCProjectSpec, MCProjectStatus, phases, conditions, DeepCopy). Required by kubebuilder for the MCProject CRD spec'd in the architecture. |
| nkeys.go:1–9 | INFRA | Retained placeholder file documenting ADR-0063 decision to move JWT minting out of controller. |
| hostauth/host_auth.go:1–289 | INFRA | All shared hostauth package code is required infrastructure implementing the spec'd boot sequence (NewHostAuthFromSeed, Refresh, JWTFunc, SignFunc, StartRefreshLoop, ErrNotRegistered). |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec:12–13 / adr:144 | HOST_NKEY_SEED_PATH read; NewHostAuthFromSeed constructor | hostauth/host_auth_test.go:118–151 (TestNewHostAuthFromSeed_*) | None | UNIT_ONLY | Critical boot path; no integration test against real K8s cluster + real CP. |
| spec:14–19 / adr:156–161 | Refresh() HTTP challenge-response + ErrNotRegistered on 404 + retry loop | hostauth/host_auth_test.go:196–331 (TestRefresh_*); controller_test.go (bootRefresh covered indirectly) | None | UNIT_ONLY | Uses httptest.Server; no real CP endpoint tested. Critical auth path — should have integration test. |
| spec:21 / adr:162 | Host slug decoded from JWT after challenge-response | controller_test.go:492–532 (TestADR0063_HostSlugFromJWT) | None | UNIT_ONLY | |
| spec:23 / nats_subscriber.go:61 | Single wildcard subscription mclaude.hosts.{hslug}.> | controller_test.go:536–552 (TestADR0063_SingleSubscription) | None | UNIT_ONLY | |
| spec:82–86 | Subject dispatch: create/update/delete operations | controller_test.go:242–259 (TestExtractOperation) | None | UNIT_ONLY | |
| spec:93 | Per-user namespace mclaude-{userSlug} per ADR-0062 | controller_test.go:294–345 (TestADR0062_NamespaceUsesSlug) | None | UNIT_ONLY | Uses fake client; not tested against real K8s. |
| spec:100 | ownerReferences on Deployment/PVC/Secret via SetControllerReference | controller_test.go:160–238 (TestGap7_MultiOwner for SA); deployment/PVC owner ref not unit-tested directly | None | UNIT_ONLY | PVC and Deployment ownerRef not explicitly tested; SA proxy tested. |
| spec:104–113 | Full reconciler loop: namespace, RBAC, secrets, deployment, phase transitions | controller_test.go:74–109 (TestGap3_PendingPhaseTransition) | None | UNIT_ONLY | Fake K8s client; no real cluster test. |
| spec:112 | CONTROL_PLANE_URL injected into session-agent pod env | controller_test.go:396–421 (TestADR0063_ControlPlaneURLInjected) | None | UNIT_ONLY | |
| spec:117–125 | Session-agent auth relay (IPC → agents.register NATS publish) | Not tested (not implemented) | None | UNTESTED | GAP in implementation — no relay exists. |
| spec:125 | nats-creds not written by reconcileSecrets; stale entries removed | controller_test.go:426–489 (TestADR0063_NoNATSCredsInSecrets, TestADR0063_StaleNATSCredsRemoved) | None | UNIT_ONLY | |
| spec:128–133 | Corporate CA injection (volume, mount, NODE_EXTRA_CA_CERTS, annotation) | controller_test.go:112–155 (TestGap4_ClaudeCodeTmpDir covers pod template; no dedicated CA test) | None | UNIT_ONLY | No unit test specifically for corporateCAEnabled branch. |
| spec:43–54 | Deregistration Option B: Helm pre-delete Job | Not implemented | None | UNTESTED | GAP — pre-delete Job not in chart. |
| adr-0063:150 | NewHostAuthFromSeed constructor in mclaude-common/pkg/hostauth | hostauth/host_auth_test.go:118–151 | None | UNIT_ONLY | |
| adr-0063:150 | JWTFunc(), SignFunc(), StartRefreshLoop() in hostauth package | hostauth/host_auth_test.go:388–426 (SignFunc tests); JWTFunc tested indirectly | None | UNIT_ONLY | StartRefreshLoop not directly unit-tested. |

### Phase 4 — Bug Triage

No open bugs with Component matching mclaude-controller-k8s.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| — | — | — | No bugs filed against this component. |

### Summary

- Implemented: 32
- Gap: 2
- Partial: 1
- Infra: 21
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 13
- E2E only: 0
- Untested: 2
- Bugs fixed: 0
- Bugs open: 0
