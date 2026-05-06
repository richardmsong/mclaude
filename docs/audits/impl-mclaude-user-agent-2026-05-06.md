## Run: 2026-05-06T00:00:00Z

Component: mclaude-user-agent
Spec files: docs/mclaude-user-agent/spec-user-agent.md, docs/adr-0091-per-user-anthropic-token.md, docs/adr-0092-anthropic-token-ux.md, docs/spec-state-schema.md

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-user-agent.md:3-5 | "Token custodian. Receives crypto_box_seal-wrapped Anthropic OAuth tokens... unseals them with its X25519 private key, and writes plaintext to a per-user k8s Secret" | tokens.go:58-91 (HandleDelivery), crypto/sealedbox.go:26-44 (SealedBoxOpen) | IMPLEMENTED | | Full flow: base64 decode → SealedBoxOpen → patchTokenSecret |
| spec-user-agent.md:6 | "Project pod lifecycle manager. Reacts to NATS provision/delete events... creates/deletes per-project Deployments in the user namespace. Event-driven; no CRD; no reconciler" | provisioner.go:100-168 (Provision/Delete), agent.go:131-164 (subscriptions) | IMPLEMENTED | | Event-driven: no reconcile loop, k8s is source of truth |
| spec-user-agent.md:14 | "Created by: mclaude-controller-k8s when host grants user access (mclaude.hosts.{hslug}.grants.create)" | NOT in user-agent code — this is a controller responsibility | IMPLEMENTED | | This is mclaude-controller-k8s scope, not user-agent code. User-agent is correct here. |
| spec-user-agent.md:30 | "Read nkey-seed from /secrets/agent/nkey-seed. Fatal exit if missing" | main.go:63-65 | IMPLEMENTED | | Returns error (fatal via log.Fatal in main) on ReadFile failure |
| spec-user-agent.md:31 | "Read x25519-private from /secrets/agent/x25519-private. If absent: generate a new X25519 keypair, write the private key to the Secret" | crypto/x25519.go:43-101 (LoadOrGenerateX25519Keypair) | IMPLEMENTED | | Reads Secret, generates if absent, patches back |
| spec-user-agent.md:32-35 | "Authenticate to control-plane via challenge-response: POST /api/auth/challenge {nkey_public} → nonce. Sign nonce. POST /api/auth/verify {nkey_public, challenge, signature, x25519_pub}" | auth/auth.go:89-205 (Authenticate, requestChallenge, verifyChallenge) | IMPLEMENTED | | x25519_pub sent in verify if set (auth.go:168-170) |
| spec-user-agent.md:36 | "Connect to hub NATS at NATS_URL with the issued JWT" | main.go:110 (RunNATSLoop), agent.go:51-56 (ConnectNATS) | IMPLEMENTED | | Uses UserJWT + SignFunc for JWT-authenticated NATS connection |
| spec-user-agent.md:37 | "Subscribe to all four agent subjects on every connect (activate, token-delivery, projects.provision, projects.delete). NATS RBAC enforces scope: with bootstrap JWT, non-activate subscriptions are rejected and logged as warnings" | agent.go:63-164 | IMPLEMENTED | | All four subscribed; errors on bootstrap JWT logged as warnings (lines 97-98, 112-113, 126, 151, 163) |
| spec-user-agent.md:38 | "Background loop: re-run challenge-response before TTL expiry (5-minute JWT TTL)" | auth/auth.go:210-236 (StartRefreshLoop); main.go:103-106 | IMPLEMENTED | | Refresh interval = TTL(5min) - buffer(60s) = 4min |
| spec-user-agent.md:39 | "On receipt of .agent.activate: re-run challenge-response to obtain full JWT, reconnect with full permissions, re-subscribe" | agent.go:63-76 (activate handler), agent.go:166-183 (reconnectCh drain+loop) | IMPLEMENTED | | AuthenticateWithBackoff called, then reconnectCh signals drain+reconnect |
| spec-user-agent.md:47 | "agent.activate: Signal-only payload. Triggers re-auth; no token material." | agent.go:63-76 | IMPLEMENTED | | Payload ignored, only signal matters |
| spec-user-agent.md:48 | "agent.token-delivery: Payload {provider, token_wrapped}. Unseal → write to user-tokens-{uslug} Secret under key {provider}. Reply {ok: true} or {ok: false, error: decrypt_failed}" | tokens.go:58-91, agent.go:86-98 | IMPLEMENTED | | Reply on success and failure handled |
| spec-user-agent.md:49 | "agent.projects.provision: Verify token exists; if not, reply {error: host_not_trusted}. Creates project Deployment. Reply {ok: true, status: provisioning}" | provisioner.go:100-157, agent.go:132-156 | IMPLEMENTED | | |
| spec-user-agent.md:50 | "agent.projects.delete: Best-effort delete. No reply expected. If offline when published, event is missed." | provisioner.go:160-168, agent.go:154-164 | IMPLEMENTED | | Delete is fire-and-forget; no reply sent |
| spec-user-agent.md:51 | "agent.token-revoke (ADR-0092): Payload {provider}. Deletes provider key from user-tokens-{uslug} Secret. Reply {ok:true} or {ok:false, error:...}. SPA uses 10s timeout." | tokens.go:162-181, agent.go:101-113 | IMPLEMENTED | | |
| spec-user-agent.md:52 | "agent.status (ADR-0092): Reply {hasToken: bool, tokenVersion: first-8-hex-of-sha256}. Also published unsolicited on startup and on token change." | tokens.go:183-212, agent.go:117-129 (subscribe), agent.go:128-129 (unsolicited startup), agent.go:94-96, 109-110 (unsolicited on change) | IMPLEMENTED | | |
| spec-user-agent.md:54 | "The user-agent has no JetStream / KV access at any point." | agent.go (no JetStream API calls anywhere) | IMPLEMENTED | | No KV/JetStream imports or calls |
| spec-user-agent.md:61 | "Verify /secrets/tokens/{provider} is non-empty" | provisioner.go:108-112 | IMPLEMENTED | | Reads file from tokenBaseDir + "/tokens/" + provider |
| spec-user-agent.md:62 | "Build the Deployment using the session-agent-template ConfigMap in the user's own namespace" | provisioner.go:171-213 (loadTemplate) | IMPLEMENTED | | Reads from p.namespace, not mclaude-system |
| spec-user-agent.md:63-69 | "Mount the full set of volumes: project PVC /data, nix PVC /nix, user-config /home/node/.claude-seed, user-secrets /home/node/.user-secrets, user-tokens Secret /secrets/tokens/, claude-home emptyDir /home/node/.claude" | provisioner.go:275-329 (volumes + volumeMounts) | IMPLEMENTED | | All 6 volumes mounted correctly |
| spec-user-agent.md:70 | "Set env TOKEN_PROVIDER, PROJECT_ID, PROJECT_SLUG, GIT_URL, HOST_SLUG, USER_SLUG, USER_ID, GIT_IDENTITY_ID, NATS_URL, CONTROL_PLANE_URL" | provisioner.go:255-266 | IMPLEMENTED | | All 10 env vars set |
| spec-user-agent.md:71 | "Create per-project PVCs (project-{projectId} and nix-{projectId}) using sizes from the template" | provisioner.go:122-129 (ensurePVC calls), 383-422 (ensurePVC impl) | IMPLEMENTED | | |
| spec-user-agent.md:72 | "Apply the Deployment with mclaude/managed=true label" | provisioner.go:335-340 (labels) | IMPLEMENTED | | Label set on Deployment ObjectMeta |
| spec-user-agent.md:74 | "The user-agent does not mint session-agent NKeys or write agent-nkey-{projectId} Secrets" | provisioner.go (no NKey generation for session agent) | IMPLEMENTED | | |
| spec-user-agent.md:77-78 | "kubectl delete deployment project-{projectId}. PVC cleanup omitted in v1" | provisioner.go:160-168 | IMPLEMENTED | | Only Deployment deleted; PVCs left intact |
| spec-user-agent.md:80-83 | "On user-agent restart: existing Deployments continue running. Does not query CP for expected projects. Missed events not retroactively reconciled." | main.go (no reconcile on startup), provisioner.go (no list/watch loop) | IMPLEMENTED | | Event-driven only; no startup reconciliation |
| spec-user-agent.md:89-98 | "Token Delivery flow: SPA sends wrapped token → user-agent unseals → writes plaintext to Secret. On failure: replies {ok: false, error: decrypt_failed}" | tokens.go:58-91 (HandleDelivery), agent.go:86-98 | IMPLEMENTED | | |
| spec-user-agent.md:113 | "nkey-seed missing: Fatal exit" | main.go:63-65 | IMPLEMENTED | | Returns error which causes log.Fatal in main |
| spec-user-agent.md:114 | "Challenge-response fails: Retry with exponential backoff (cap 60s). Never enters Ready until JWT acquired." | agent.go:188-201 (AuthenticateWithBackoff), auth/auth.go (Authenticate) | IMPLEMENTED | | MaxInterval=60s, retries until context cancellation |
| spec-user-agent.md:115 | "crypto_box_seal_open fails: Reply {ok: false, error: decrypt_failed}" | tokens.go:77-81 | IMPLEMENTED | | |
| spec-user-agent.md:116 | "Project provision when no token: Reply {error: host_not_trusted}" | provisioner.go:108-112 | IMPLEMENTED | | |
| spec-user-agent.md:117 | "kubectl create deployment fails: Reply with ProvisionReply{Error: machine-readable code}. OK field uses omitempty — present on success, omitted on failure." | provisioner.go:86-90 (ProvisionReply struct with omitempty), provisioner.go:136-149 | IMPLEMENTED | | OK is `json:"ok,omitempty"` so absent on failure |
| spec-user-agent.md:118 | "Project delete when Deployment doesn't exist: Treat as success (idempotent)" | provisioner.go:162-167 (IsNotFound check) | IMPLEMENTED | | |
| spec-user-agent.md:119 | "User-agent JWT TTL expires before refresh: NATS connection drops; on reconnect, JWT re-acquired via challenge-response" | auth/auth.go:210-236 (refresh loop); agent.go:215-227 (ConnectNATS with MaxReconnects(-1)) | PARTIAL | SPEC→FIX | Refresh loop re-authenticates before expiry. On natural NATS drop from expired JWT, ConnectNATS MaxReconnects(-1) attempts reconnect but uses the current JWT (not re-acquired). The refresh loop should handle renewal before drop. This is a spec ambiguity: "on reconnect" implies the NATS library auto-reconnects with fresh JWT. The JWTFunc returns currentJWT which is refreshed proactively — likely fine in practice, but the reconnect after natural JWT expiry won't re-run challenge-response unless RefreshLoop fires first. |
| spec-user-agent.md:131-143 | "Project Pod Defaults: image mclaude-session-agent:latest, imagePullPolicy IfNotPresent, terminationGracePeriodSeconds 30, resources {requests cpu 200m memory 64Mi, limits cpu 2000m memory 2Gi}, projectPvcSize 10Gi, nixPvcSize 10Gi" | provisioner.go:215-240 (defaultSessionAgentTpl, applyDefaultResources) | IMPLEMENTED | | All defaults match spec exactly |
| spec-user-agent.md:137 | "projectPvcStorageClass (empty — uses cluster default)" | provisioner.go:215-224 (storageClass is "" in default) | IMPLEMENTED | | Empty string → no storageClassName set in PVC |
| spec-user-agent.md:343 | "Deployment Strategy: Recreate — old pod must exit before new pod starts" (from spec-state-schema.md:612-613) | provisioner.go:343-345 | GAP | CODE→FIX | Code uses RollingUpdateDeploymentStrategyType. spec-state-schema.md:612 says Strategy: Recreate. spec-user-agent.md:16 says RollingUpdate for the USER-AGENT deployment, but the PROJECT pod deployment (spec-state-schema.md:612) must be Recreate. |
| spec-state-schema.md:610-618 | "Deployment: project-{projectId}. Strategy: Recreate — old pod must exit before new pod starts; prevents two pods consuming same durable JetStream consumers simultaneously (ADR-0043)" | provisioner.go:343-345 | GAP | CODE→FIX | Code sets RollingUpdateDeploymentStrategyType but spec-state-schema.md requires RecreateDeploymentStrategyType for project pods. The user-agent deployment itself is RollingUpdate (correct per spec-user-agent.md:16), but project pods need Recreate. |
| adr-0091:145-154 | "Boot step 5: Subscribe to all four agent.> subjects on every connect. NATS RBAC enforces scope: bootstrap JWT → only activate succeeds; full JWT → all four succeed. Single subscription path." | agent.go:63-164 | IMPLEMENTED | | Subscribe attempted on all subjects; failure logged as warning |
| adr-0091:155 | "Reconnect with full JWT after activate signal" | agent.go:166-183 | IMPLEMENTED | | Drain + reconnect loop |
| adr-0091:287-296 | "mclaude-user-agent: subscribes token-delivery, token-revoke; unseals token; writes to Secret; subscribes provision/delete; creates session pods" | tokens.go, provisioner.go, agent.go | IMPLEMENTED | | |
| adr-0092:297-298 | "New NATS subject: agent.token-revoke. SPA sends {provider: anthropic}, user-agent replies {ok: true} or {ok: false, error}" | tokens.go:162-181, agent.go:101-113 | IMPLEMENTED | | |
| adr-0092:298 | "New NATS subject: agent.status. Reply {hasToken: bool, tokenVersion}. User-agent also publishes unsolicited on startup and on token change." | tokens.go:183-212, agent.go:117-129 (req/reply), agent.go:128-129 (startup), agent.go:94-96 (after delivery), agent.go:109-110 (after revoke) | IMPLEMENTED | | |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:19-31 (envRequired, envOr helpers) | INFRA | Standard env-var helpers for reading configuration. Necessary for spec'd boot env vars (USER_SLUG, HOST_SLUG, etc.) |
| main.go:34-59 (main) | INFRA | Standard Go main: zerolog setup, Config construction, signal context, run(). Necessary bootstrap plumbing. |
| main.go:61-111 (run) | INFRA | Orchestrates the boot sequence per spec. Required entry point logic. |
| main.go:113-124 (newK8sClient) | INFRA | In-cluster Kubernetes client factory. Necessary for k8s API operations. |
| agent.go:15-16 (NATSConnectFn type) | INFRA | Dependency injection type for testing. Necessary for testability. |
| agent.go:26-183 (RunNATSLoop) | INFRA | Core NATS loop — spec'd behavior. Reviewed in Phase 1. |
| agent.go:186-202 (AuthenticateWithBackoff) | INFRA | Auth helper with exponential backoff — spec'd behavior. |
| agent.go:204-212 (publishUnsolicitedStatus) | INFRA | ADR-0092 unsolicited status publish — spec'd. |
| agent.go:214-227 (ConnectNATS) | INFRA | Production NATS connection factory. Spec'd. |
| provisioner.go:1-72 (types and constructors) | INFRA | Config struct, Provisioner struct, constructors, ProvisionRequest/Reply/DeleteRequest types. All spec'd in ADR-0091/spec-user-agent.md. |
| provisioner.go:242-381 (buildDeployment) | INFRA | Constructs the k8s Deployment per spec. Spec'd in full. SecurityContext (runAsNonRoot, runAsUser=1000, fsGroup=1000) is extra vs spec text, but necessary infrastructure for k8s security posture — not unspec'd behavior. |
| provisioner.go:383-422 (ensurePVC) | INFRA | Creates PVCs idempotently. Spec'd in spec-user-agent.md:71. |
| provisioner.go:215-240 (defaultSessionAgentTpl, applyDefaultResources) | INFRA | Default template values per spec-user-agent.md:131-143. |
| provisioner.go:170-213 (loadTemplate) | INFRA | Reads ConfigMap per spec-user-agent.md:62. Fallback to defaults per spec:128. |
| tokens.go:1-50 (types and TokenManager) | INFRA | TokenDeliveryRequest/Reply, TokenManager struct and constructor. All spec'd. |
| tokens.go:93-140 (patchTokenSecret) | INFRA | Strategic-merge-patch for Secret write. Implementation detail for spec'd Secret write. |
| tokens.go:220-248 (deleteTokenSecret) | INFRA | Strategic-merge-patch for Secret key removal. Implementation detail for spec'd revoke. |
| tokens.go:250-260 (replyJSON, replyRevokeJSON) | INFRA | JSON marshal helpers. Necessary plumbing for reply serialization. |
| tokens.go:191-212 (HandleStatus, buildStatusReply, tokenVersion) | INFRA | Spec'd by ADR-0092. tokenVersion = first 8 hex chars of sha256(token). |
| auth/auth.go:1-236 (all) | INFRA | UserAgentAuth implementing challenge-response. Entirely spec'd in spec-user-agent.md:32-38, ADR-0091 Boot steps. |
| crypto/x25519.go:1-101 (all) | INFRA | X25519 keypair generation and persistence. Spec'd in spec-user-agent.md:31, ADR-0091 Boot step 2. |
| crypto/sealedbox.go:1-83 (all) | INFRA | NaCl sealed-box implementation. Spec'd: "crypto_box_seal / crypto_box_seal_open" in spec-user-agent.md:5, ADR-0091. SealedBoxSeal (line 49-70) is used in tests but not in production binary — UNSPEC'd for production but justified as symmetric half of the sealed box primitive, used only in tests. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-user-agent.md:30 | nkey-seed missing: fatal exit | None (covered implicitly via main.go run function, no dedicated test) | None | UNTESTED | No test exercises the fatal path on missing nkey-seed |
| spec-user-agent.md:31 | x25519 keypair: load existing or generate new | crypto/x25519_test.go | None | UNIT_ONLY | Unit tests cover LoadOrGenerateX25519Keypair. No integration test with real k8s. |
| spec-user-agent.md:32-35 | Challenge-response auth | auth/auth_test.go (TestAuthenticate_Success, TestAuthenticate_SendsX25519Pub) | None | UNIT_ONLY | Auth tests use httptest server (mock CP). No integration test against real CP. Auth is a critical path — should have integration coverage. |
| spec-user-agent.md:37-39 | NATS loop: subscribe, activate reconnect, refresh | agent/agent_test.go (TestRunNATSLoop_ActivateTriggersReconnect, TestRunNATSLoop_ReAuthOnActivate, TestRunNATSLoop_FullJWTSubscribesAllSubjects, TestRunNATSLoop_ShutdownOnContextCancel) | None | UNIT_ONLY | Tests use plain NATS server (no RBAC). No integration test with operator-mode JWT enforcement. |
| spec-user-agent.md:48 | token-delivery: unseal → write Secret | agent/tokens_test.go (TestHandleDelivery_Success, TestHandleDelivery_DecryptFailed, TestHandleDelivery_DefaultProvider, TestHandleDelivery_MultiProvider, TestHandleDelivery_BadPayload) | None | UNIT_ONLY | Comprehensive unit tests. No integration test with real NATS + CP auth enforcement. |
| spec-user-agent.md:49 | projects.provision: token check, Deployment+PVCs | agent/provisioner_test.go (TestProvisioner_HostNotTrusted, TestProvisioner_ProvisionCreatesDeploymentAndPVCs, TestProvisioner_DeploymentEnvVars, TestProvisioner_DeploymentVolumeMounts, TestProvisioner_DeploymentManagedLabel, TestProvisioner_ProvisionIdempotent, TestProvisioner_DefaultTemplate) | None | UNIT_ONLY | Good unit coverage. No integration test with real k8s. |
| spec-user-agent.md:50 | projects.delete: idempotent delete | agent/provisioner_test.go (TestProvisioner_Delete, TestProvisioner_Delete_NotFound) | None | UNIT_ONLY | |
| spec-user-agent.md:51 | token-revoke | agent/tokens_test.go (TestHandleRevoke_Success, TestHandleRevoke_DefaultProvider, TestHandleRevoke_BadPayload, TestHandleRevoke_SecretNotFound) + agent/agent_test.go (TestRunNATSLoop_TokenRevokeHandlerResponds) | None | UNIT_ONLY | |
| spec-user-agent.md:52 | agent.status: hasToken + tokenVersion | agent/tokens_test.go (TestHandleStatus_NoToken, TestHandleStatus_WithToken, TestHandleStatus_AfterRevoke, TestTokenVersion) + agent/agent_test.go (TestRunNATSLoop_AgentStatusHandlerResponds, TestRunNATSLoop_UnsolicitedStatusOnStartup) | None | UNIT_ONLY | |
| spec-state-schema.md:612 | Deployment strategy Recreate for project pods | None — the strategy bug (RollingUpdate vs Recreate) has no test | None | UNTESTED | No test verifies deployment strategy type |
| adr-0091:592-608 (Integration Test Cases) | Default platform auto-grant + auto-accept → user-agent gets full JWT | None | None | UNTESTED | ADR-listed integration test case has no implementation |
| adr-0091:592-608 | Grant → accept → activate → user-agent activates | agent_test.go partially covers (TestRunNATSLoop_ActivateTriggersReconnect) | None | UNIT_ONLY | Unit covers NATS reconnect but not real CP + NATS with JWT enforcement |
| adr-0091:592-608 | Bootstrap JWT cannot read agent subjects | None | None | UNTESTED | ADR-listed test; no implementation |
| adr-0091:592-608 | CLI sets token → SPA delivers → session reads from Secret | None | None | UNTESTED | ADR-listed end-to-end test; no implementation |
| adr-0091:592-608 | User-agent pubkey registered | None | None | UNTESTED | ADR-listed test; no implementation |
| adr-0091:592-608 | SPA delivers token_wrapped → user-agent writes Secret | tokens_test.go (partial unit) | None | UNIT_ONLY | Unit only; no integration test with real NATS+CP |
| adr-0091:592-608 | User-agent provisions project pod | provisioner_test.go (unit) | None | UNIT_ONLY | |
| adr-0091:592-608 | User-agent deletes project pod | provisioner_test.go (TestProvisioner_Delete) | None | UNIT_ONLY | |
| adr-0091:592-608 | User-agent restart adopts existing pods | None | None | UNTESTED | ADR-listed test; no test exercises restart behavior |
| adr-0091:592-608 | Session-agent JWT issuance unchanged | None (session-agent scope) | None | UNTESTED | Out of user-agent scope — session-agent tests should cover |
| adr-0091:592-608 | User-agent cannot read mclaude-user-info | None | None | UNTESTED | Requires real NATS with JWT enforcement |
| adr-0091:592-608 | Session without token fails | provisioner_test.go (TestProvisioner_HostNotTrusted) | None | UNIT_ONLY | |
| adr-0092 Integration Tests | First-run auto-delivers to default platform | None | None | UNTESTED | ADR-listed; SPA concern but user-agent side untested |
| adr-0092 Integration Tests | Per-host revoke stops sessions | None | None | UNTESTED | ADR-listed; requires running session-agent + k8s |


### Phase 4 — Bug Triage

No open bugs with Component: mclaude-user-agent found in .agent/bugs/.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | | | No open bugs reference this component |

### Summary

- Implemented: 37
- Gap: 2
- Partial: 1
- Infra: 22
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 14
- E2E only: 0
- Untested: 11
- Bugs fixed: 0
- Bugs open: 0
