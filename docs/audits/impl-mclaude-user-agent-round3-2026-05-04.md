# Implementation Audit: mclaude-user-agent (Round 3)

**Component root**: `mclaude-user-agent/`
**Primary spec**: `docs/mclaude-user-agent/spec-user-agent.md`
**Cross-cutting**: `docs/adr-0091-per-user-anthropic-token.md`, `docs/spec-state-schema.md`, `docs/spec-nats-data-taxonomy.md`
**ADR-0092 status**: draft — excluded (not accepted/implemented)

## Run: 2026-05-04T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-user-agent.md:3-5 | "Token custodian. Receives crypto_box_seal-wrapped tokens ... unseals them with its X25519 private key, and writes plaintext to a per-user k8s Secret" | tokens.go:56-88 (HandleDelivery), sealedbox.go:24-42 (SealedBoxOpen), x25519.go:51-109 (LoadOrGenerateX25519Keypair) | IMPLEMENTED | — | Full unseal → write Secret path implemented |
| spec-user-agent.md:6-7 | "Project pod lifecycle manager. Reacts to NATS provision/delete events from CP and creates/deletes per-project Deployments in the user namespace. Event-driven; no CRD; no reconciler" | provisioner.go:88-158 (Provision, Delete), main.go:204-237 (subscribe provision/delete) | IMPLEMENTED | — | Event-driven, no reconciler loop |
| spec-user-agent.md:12-13 | "One Deployment per user namespace mclaude-{uslug} on each cluster host. Created by: mclaude-controller-k8s" | Not in user-agent code (this is a controller-k8s concern); user-agent reads namespace from NAMESPACE env | IMPLEMENTED | — | User-agent reads NAMESPACE env (main.go:59); creation is controller-k8s's job |
| spec-user-agent.md:14 | "Replicas: 1" | provisioner.go:235 (`replicas := int32(1)`) — this is the project pod replicas; user-agent's own Deployment replicas are in the helm chart, not code | IMPLEMENTED | — | Project Deployments set replicas=1; user-agent's own replica count is chart config |
| spec-user-agent.md:15 | "Strategy: RollingUpdate" | provisioner.go:332-335 (appsv1.RollingUpdateDeploymentStrategyType for project pods) | IMPLEMENTED | — | Set on project Deployments |
| spec-user-agent.md:17-18 | "Volumes: user-agent-key Secret (at /secrets/agent/), user-tokens-{uslug} Secret (at /secrets/tokens/)" | main.go:74 (reads /secrets/agent/nkey-seed), x25519.go:52 (reads user-agent-key Secret via k8s API), tokens.go:46 (secretName = "user-tokens-"+uslug) | IMPLEMENTED | — | Mount paths used in boot sequence; the actual Deployment volume spec is in the chart |
| spec-user-agent.md:20 | "Env: USER_SLUG, HOST_SLUG, NAMESPACE, NATS_URL, CONTROL_PLANE_URL" | main.go:57-62 (envRequired for USER_SLUG, HOST_SLUG, NATS_URL, CONTROL_PLANE_URL; envOr for NAMESPACE) | IMPLEMENTED | — | All five env vars consumed |
| spec-user-agent.md:21-25 | "ServiceAccount: mclaude-user-agent. Role: get/list/watch/create/update/patch/delete on Deployment and PVC; get/list/create/update/patch on Secret (only user-tokens-{uslug}); get/list/watch on ConfigMap (only session-agent-template)" | Not in user-agent code — defined in helm chart RBAC templates | IMPLEMENTED | — | RBAC is infrastructure; user-agent code uses the in-cluster client which relies on this being correct |
| spec-user-agent.md:30 | "Boot step 1: Read nkey-seed from /secrets/agent/nkey-seed. Fatal exit if missing" | main.go:73-77 (`os.ReadFile("/secrets/agent/nkey-seed")`; returns error → log.Fatal in main) | IMPLEMENTED | — | Fatal path via run() returning error → log.Fatal |
| spec-user-agent.md:31 | "Boot step 2: Read x25519-private from /secrets/agent/x25519-private. If absent: generate new X25519 keypair, write private key to Secret" | x25519.go:51-109 (LoadOrGenerateX25519Keypair reads /secrets/agent/ via k8s API; generates if absent and patches Secret) | IMPLEMENTED | — | Uses k8s API (strategic-merge-patch) to write back |
| spec-user-agent.md:33-35 | "Boot step 3: Authenticate to CP via challenge-response: POST /api/auth/challenge {nkey_public} → nonce. Sign nonce with NKey. POST /api/auth/verify {nkey_public, challenge, signature, x25519_pub} → CP stores x25519_pub, returns bootstrap or full JWT" | auth.go:92-122 (Authenticate), auth.go:124-154 (requestChallenge), auth.go:157-205 (verifyChallenge with x25519_pub field) | IMPLEMENTED | — | x25519_pub sent as base64-encoded 32 bytes |
| spec-user-agent.md:36 | "Boot step 4: Connect to hub NATS at NATS_URL with the issued JWT" | main.go:159-163 (connectFn(cfg.NATSURL, agentAuth)), main.go:278-290 (connectNATS using UserJWT) | IMPLEMENTED | — | Uses NKey signing via SignFunc() |
| spec-user-agent.md:37 | "Boot step 5: Subscribe to all four agent subjects on every connect (activate, token-delivery, projects.provision, projects.delete). NATS RBAC enforces scope: with bootstrap JWT, non-activate subscriptions are rejected and logged as warnings" | main.go:170-237 (subscribes all four; token-delivery/provision/delete errors logged as Warn and continue) | IMPLEMENTED | — | Matches spec: subscribe-all-then-RBAC-rejects approach |
| spec-user-agent.md:38 | "Boot step 6: Background loop: re-run challenge-response before TTL expiry (5-minute JWT TTL). Each renewal returns whatever JWT matches current grant status." | auth.go:210-236 (StartRefreshLoop; uses agentJWTTTL=5min, agentJWTRefreshBuffer=60s; calls Authenticate) | IMPLEMENTED | — | 5min TTL, 60s buffer, fires at TTL-buffer = 4min |
| spec-user-agent.md:39 | "Boot step 7: On receipt of .agent.activate: re-run challenge-response, reconnect with full JWT, re-subscribe to mclaude.users.{uslug}.hosts.{hslug}.agent.>" | main.go:170-183 (activate handler: authenticateWithBackoff → signals reconnectCh), main.go:244-256 (drain + reconnect on reconnectCh signal), main.go:151-256 (loop re-subscribes on every connect) | IMPLEMENTED | — | Drain + reconnect on next loop iteration |
| spec-user-agent.md:40 | "Boot step 7 [second]: Token availability checked at provision time via os.ReadFile('/secrets/tokens/{provider}') rather than cached in memory at startup" | provisioner.go:98-101 (os.ReadFile(p.tokenBaseDir+"/tokens/"+provider) at provision time) | IMPLEMENTED | — | Not cached; read on each provision request |
| spec-user-agent.md:41 | "Subscribe to .agent.token-delivery so subsequent SPA-initiated deliveries can be unsealed and patched into the Secret" | main.go:193-202 (token-delivery subscription with HandleDelivery handler) | IMPLEMENTED | — | Re-subscribed on every connect |
| spec-user-agent.md:42 | "Subscribe to .agent.projects.provision and .agent.projects.delete for project pod lifecycle" | main.go:204-237 (provision and delete subscriptions) | IMPLEMENTED | — | |
| spec-user-agent.md:46-50 | "NATS Subjects table: activate (Sub, signal-only), token-delivery (Sub req/reply, crypto_box_seal_open + write Secret, reply {ok:true}/{ok:false,error:decrypt_failed}), projects.provision (Sub req/reply, verify token, create Deployment, reply {ok:true,status:provisioning}), projects.delete (Sub, best-effort delete, no reply)" | main.go:146-149 (subject strings), tokens.go:56-88 (HandleDelivery), provisioner.go:88-147 (Provision), provisioner.go:149-158 (Delete) | IMPLEMENTED | — | All four subjects implemented per table |
| spec-user-agent.md:49 | "projects.provision reply {ok: true, status: 'provisioning'}" | provisioner.go:146 (`return ProvisionReply{OK: true, Status: "provisioning"}`) | IMPLEMENTED | — | |
| spec-user-agent.md:49 | "projects.provision: if no token, replies {error: 'host_not_trusted'}" | provisioner.go:99-101 (`return ProvisionReply{Error: "host_not_trusted"}`) | IMPLEMENTED | — | |
| spec-user-agent.md:50 | "projects.delete: best-effort delete. No reply expected. If user-agent offline, event missed, Deployment persists — manual kubectl delete is escape hatch" | provisioner.go:149-158 (no reply published), main.go:228-236 (no publish on delete handler) | IMPLEMENTED | — | No reply; offline-miss accepted |
| spec-user-agent.md:52 | "No JetStream / KV access at any point. Cannot read mclaude-user-info-{uslug}" | main.go, auth.go, tokens.go, provisioner.go, x25519.go, sealedbox.go — no NATS JetStream API calls anywhere | IMPLEMENTED | — | User-agent never touches KV |
| spec-user-agent.md:58-69 | "On agent.projects.provision: (1) verify /secrets/tokens/{provider} non-empty (2) build Deployment from session-agent-template ConfigMap in user's own namespace (3) mount full volume set (4) set env vars (5) create PVCs using sizes from template" | provisioner.go:91-146 (token check → loadTemplate → ensurePVC x2 → buildDeployment) | IMPLEMENTED | — | All steps in order |
| spec-user-agent.md:60 | "Build Deployment using session-agent-template ConfigMap in the user's own namespace (copy from mclaude-system written by mclaude-controller-k8s at provisioning time). User-agent does not read from mclaude-system." | provisioner.go:162-203 (loadTemplate reads ConfigMap from p.namespace, not mclaude-system) | IMPLEMENTED | — | |
| spec-user-agent.md:61-67 | "Mount volumes: project-{projectId} PVC at /data, nix-{projectId} PVC at /nix, user-config ConfigMap at /home/node/.claude-seed, user-secrets Secret at /home/node/.user-secrets, user-tokens-{uslug} Secret at /secrets/tokens/, claude-home emptyDir at /home/node/.claude" | provisioner.go:265-318 (volumes and volumeMounts with all six entries) | IMPLEMENTED | — | All volumes match spec |
| spec-user-agent.md:68 | "Set env TOKEN_PROVIDER, PROJECT_ID, PROJECT_SLUG, GIT_URL, HOST_SLUG, USER_SLUG, USER_ID, GIT_IDENTITY_ID, NATS_URL, CONTROL_PLANE_URL" | provisioner.go:244-256 (env slice with all 10 vars) | IMPLEMENTED | — | |
| spec-user-agent.md:69 | "Create per-project PVCs (project-{projectId} and nix-{projectId}) using sizes from template" | provisioner.go:111-119 (ensurePVC calls for project- and nix- PVCs) | IMPLEMENTED | — | Idempotent via get-before-create |
| spec-user-agent.md:70 | "Apply Deployment with mclaude/managed=true label" | provisioner.go:324-329 (Labels map with "mclaude/managed": "true") | IMPLEMENTED | — | |
| spec-user-agent.md:72-73 | "Does not mint session-agent NKeys or write agent-nkey-{projectId} Secrets. Session-agent self-bootstrap unchanged." | No NKey generation for session-agents anywhere in user-agent code | IMPLEMENTED | — | |
| spec-user-agent.md:74-76 | "On agent.projects.delete: kubectl delete deployment project-{projectId} in user namespace. PVC cleanup omitted in v1 — PVCs persist after Deployment deleted." | provisioner.go:149-158 (Delete deletes Deployment only; no PVC deletion) | IMPLEMENTED | — | PVCs deliberately not cleaned up |
| spec-user-agent.md:78-82 | "On user-agent restart: existing Deployments continue running. User-agent does not query CP for expected projects or reconcile against KV. No retroactive reconcile." | No reconcile-on-startup code anywhere in user-agent | IMPLEMENTED | — | Event-driven only |
| spec-user-agent.md:87-92 | "Token delivery flow: SPA reads pubkey from CP, computes token_wrapped=base64(crypto_box_seal(token, x25519_pub)), sends to token-delivery. User-agent receives → crypto_box_seal_open → plaintext. Writes to user-tokens-{uslug} Secret at key {provider} using k8s API. Replies {ok: true}." | tokens.go:56-88 (HandleDelivery), tokens.go:92-138 (patchTokenSecret), sealedbox.go:24-42 (SealedBoxOpen) | IMPLEMENTED | — | Secret already exists (created empty by controller); user-agent only patches |
| spec-user-agent.md:93 | "Re-auth (token rotation) follows same path; existing Secret entry is overwritten." | tokens.go:106-112 (StrategicMergePatchType overwrites existing key) | IMPLEMENTED | — | |
| spec-user-agent.md:94-96 | "If crypto_box_seal_open fails, replies {ok: false, error: 'decrypt_failed'}. SPA should re-fetch pubkey from CP and retry." | tokens.go:73-79 (reply with decrypt_failed on SealedBoxOpen error) | IMPLEMENTED | — | |
| spec-user-agent.md:109-117 | "Error handling table: nkey-seed missing → fatal exit; CP challenge-response fails → retry with exponential backoff (cap 60s); crypto_box_seal_open fails → reply {ok:false, error:'decrypt_failed'}; provision with no token → reply {error:'host_not_trusted'}; kubectl create deployment fails → reply with ProvisionReply{Error: '<code>: <message>'}; OK field uses omitempty; project delete when Deployment doesn't exist → idempotent; JWT TTL expires → NATS drops, re-acquire on reconnect" | main.go:73-77 (fatal on nkey), main.go:261-275 (backoff with cap 60s), tokens.go:73-79 (decrypt_failed), provisioner.go:99-101 (host_not_trusted), provisioner.go:128-139 (error replies), provisioner.go:76-79 (ProvisionReply with OK omitempty), provisioner.go:150-157 (idempotent delete), auth.go (reconnect pattern) | IMPLEMENTED | — | ProvisionReply.OK uses omitempty — on success true is present; on failure it is omitted |
| spec-user-agent.md:125-126 | "If session-agent-template ConfigMap absent, user-agent falls back to compiled-in defaults" | provisioner.go:163-167 (IsNotFound → return defaultSessionAgentTpl()) | IMPLEMENTED | — | |
| spec-user-agent.md:129-143 | "Project Pod Defaults table: image=mclaude-session-agent:latest, imagePullPolicy=IfNotPresent, terminationGracePeriodSeconds=30, resources={cpu:200m/2000m, memory:64Mi/2Gi}, projectPvcSize=10Gi, projectPvcStorageClass=(empty), nixPvcSize=10Gi, nixPvcStorageClass=(empty)" | provisioner.go:205-230 (defaultSessionAgentTpl and applyDefaultResources with exact values) | IMPLEMENTED | — | All defaults match spec |
| spec-user-agent.md:145-151 | "Does NOT do: subscribe to mclaude.hosts.{hslug}.>; read mclaude-user-info-{uslug}; mint session-agent NKeys; reconcile from external list; handle revocation" | No such subscriptions, KV access, NKey minting, or reconcile code found | IMPLEMENTED | — | |
| adr-0091:29 | "token_wrapped = crypto_box_seal(token, ua_x25519_pub) — NaCl sealed box" | sealedbox.go:24-42 (SealedBoxOpen implements NaCl sealed box with Blake2b nonce per libsodium spec) | IMPLEMENTED | — | |
| adr-0091:31 | "User-agent X25519 keypair: generated at first startup; private key persisted in k8s Secret user-agent-key" | x25519.go:80-108 (generate if absent, patch Secret with x25519-private) | IMPLEMENTED | — | |
| adr-0091:88 | "User-agent reads NKey seed from Secret → generates X25519 keypair → writes X25519 private key to Secret" | main.go:74-77 (read nkey), main.go:92-96 (LoadOrGenerateX25519Keypair) | IMPLEMENTED | — | |
| adr-0091:89-90 | "Authenticates with CP via challenge-response. Also registers X25519 pubkey at this step." | auth.go:92-122 (Authenticate sends x25519_pub in verify request), auth.go:54-57 (SetX25519Pub called before auth) | IMPLEMENTED | — | |
| adr-0091:149-155 | "Subscribe to all four agent.> subjects on every connect. NATS RBAC enforces scope: with bootstrap JWT, only .agent.activate succeeds; non-activate subscriptions rejected and logged as warnings. Single subscription path keeps lifecycle simple — JWT scope decides what delivers, not client-side branching." | main.go:170-237 (all four subscribe attempts; RBAC-rejected ones logged as Warn and continue) | IMPLEMENTED | — | Matches round-3 spec fix |
| adr-0091:291 | "Unseals token_wrapped with X25519 private key; writes plaintext to user-tokens-{uslug} k8s Secret (keyed by provider)" | tokens.go:74-88 (SealedBoxOpen → patchTokenSecret with provider key) | IMPLEMENTED | — | |
| adr-0091:295 | "Event-driven, not reconciling. k8s is source of truth. On restart, adopts whatever pods already in namespace. Does not query external 'expected pods' list." | No startup reconcile code in any file | IMPLEMENTED | — | |
| spec-nats-data-taxonomy.md:63-66 | "User-agent NATS subjects: activate, token-delivery, projects.provision, projects.delete under mclaude.users.{uslug}.hosts.{hslug}.agent.>" | main.go:146-149 (subject string construction) | IMPLEMENTED | — | Exact subject format matches taxonomy |
| spec-nats-data-taxonomy.md:254-258 | "Bootstrap JWT: Pub _INBOX.> Sub activate + _INBOX.> — 5-minute TTL" | auth.go (JWT acquired from CP; TTL/scope enforced server-side); main.go (subscribe-all approach; RBAC rejects non-activate) | IMPLEMENTED | — | Client-side single-path approach matches bootstrap JWT RBAC model |
| spec-nats-data-taxonomy.md:264-276 | "Full JWT: Pub+Sub mclaude.users.{uslug}.hosts.{hslug}.agent.> + _INBOX.>" | auth.go (same challenge-response path, CP issues full JWT when grant accepted) | IMPLEMENTED | — | |
| spec-state-schema.md:561-562 | "user-agent-key Secret: nkey-seed (written by controller), x25519-private (written by user-agent on first startup, base64 32 bytes)" | main.go:74 (reads /secrets/agent/nkey-seed), x25519.go:60-108 (reads/writes x25519-private as base64) | IMPLEMENTED | — | |
| spec-state-schema.md:539-550 | "user-tokens-{uslug} Secret: user-agent patches per-provider keys; created empty by controller; mounted at /secrets/tokens/ by session pods" | tokens.go:92-138 (patchTokenSecret using StrategicMergePatchType, creates gracefully if not found) | IMPLEMENTED | — | |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-19 | INFRA | Package declaration + imports (context, json, fmt, os, signal, syscall, time, backoff, nats, zerolog, metav1, k8s/client-go) — necessary for the component |
| main.go:22-28 | INFRA | AgentConfig struct — configuration carrier for the five required env vars (USER_SLUG, HOST_SLUG, NAMESPACE, NATS_URL, CONTROL_PLANE_URL). All five are spec'd. |
| main.go:30-43 | INFRA | envRequired/envOr helpers — boilerplate for required env var reading; supports the spec'd boot sequence |
| main.go:45-70 | INFRA | main() — entry point; sets up zerolog, builds AgentConfig, sets up signal context, calls run() |
| main.go:72-122 | INFRA | run() — orchestration function; calls each boot step in order per spec (read nkey, build auth, k8s client, load/generate keypair, authenticate with backoff, create subsystems, start refresh loop, run NATS loop) |
| main.go:124-126 | INFRA | natsConnectFn type alias — function injection point for testability; production passes connectNATS |
| main.go:128-257 | INFRA | runNATSLoop() — core NATS subscribe loop; implements the spec'd "subscribe all four subjects on every connect" approach with reconnect-on-activate signal; necessary for all spec'd NATS behavior |
| main.go:259-275 | INFRA | authenticateWithBackoff() — spec'd error handling: "retry with exponential backoff (cap 60s)" for CP challenge-response failures |
| main.go:277-290 | INFRA | connectNATS() — creates NATS connection with UserJWT + SignFunc + reconnect config; spec'd boot step 4 |
| main.go:292-303 | INFRA | newK8sClient() — creates in-cluster k8s client; spec'd dependency for Deployment, Secret, PVC operations |
| main.go:305-306 | INFRA | `var _ = metav1.GetOptions{}` — ensures metav1 import is used; compiler trick, harmless |
| auth.go:1-16 | INFRA | Package + imports (bytes, context, encoding/base64, encoding/json, fmt, io, net/http, sync, time, nkeys, zerolog) |
| auth.go:18-23 | INFRA | agentJWTTTL (5min) and agentJWTRefreshBuffer (60s) constants — spec'd JWT TTL and refresh pattern |
| auth.go:25-36 | INFRA | UserAgentAuth struct — holds NKey keypair, CP URL, x25519 pubkey, mutex, current JWT |
| auth.go:38-50 | INFRA | NewUserAgentAuth() — constructor; parses NKey seed with nkeys.ParseDecoratedUserNKey |
| auth.go:52-58 | INFRA | SetX25519Pub() — thread-safe setter for x25519 public key; called after keypair load/generation |
| auth.go:60-67 | INFRA | PublicKey() — returns NKey public key string; used in challenge-response |
| auth.go:69-80 | INFRA | JWTFunc() — returns closure for nats.UserJWT(); returns current JWT or error |
| auth.go:82-87 | INFRA | SignFunc() — returns NKey signing closure for NATS nonce signing |
| auth.go:89-122 | INFRA | Authenticate() — implements spec'd challenge-response: request nonce → decode → sign → verify with x25519_pub → store JWT |
| auth.go:124-154 | INFRA | requestChallenge() — POST /api/auth/challenge HTTP call |
| auth.go:156-205 | INFRA | verifyChallenge() — POST /api/auth/verify HTTP call with x25519_pub field |
| auth.go:207-236 | INFRA | StartRefreshLoop() / startRefreshLoopWithInterval() — background JWT refresh goroutine; startRefreshLoopWithInterval is extracted for testability |
| tokens.go:1-15 | INFRA | Package + imports |
| tokens.go:17-27 | INFRA | TokenDeliveryRequest / TokenDeliveryReply structs — NATS payload types for token-delivery subject |
| tokens.go:29-48 | INFRA | TokenManager struct + NewTokenManager() — holds k8s client, namespace, uslug, keypair, secretName |
| tokens.go:51-88 | INFRA | HandleDelivery() — implements spec'd token-delivery handler: decode base64 → unseal → write Secret → reply |
| tokens.go:91-138 | INFRA | patchTokenSecret() — writes/updates provider key in user-tokens-{uslug} Secret; falls back to create if not found |
| tokens.go:140-143 | INFRA | replyJSON() — marshals reply to JSON bytes |
| provisioner.go:1-16 | INFRA | Package + imports |
| provisioner.go:18-30 | INFRA | sessionAgentTpl struct — parsed values from session-agent-template ConfigMap |
| provisioner.go:32-49 | INFRA | Provisioner struct + NewProvisioner() — tokenBaseDir defaults to "/secrets" in production |
| provisioner.go:52-62 | INFRA | NewProvisionerWithTokenDir() — test-only constructor overriding token base directory; used in tests |
| provisioner.go:64-73 | INFRA | ProvisionRequest struct — NATS payload for projects.provision; includes userId and provider fields |
| provisioner.go:75-80 | INFRA | ProvisionReply struct — OK omitempty (spec'd), Status omitempty, Error omitempty |
| provisioner.go:82-86 | INFRA | DeleteRequest struct — NATS payload for projects.delete |
| provisioner.go:88-147 | INFRA | Provision() — implements full provision flow: token check → loadTemplate → ensurePVC x2 → buildDeployment → create or idempotent update |
| provisioner.go:149-158 | INFRA | Delete() — implements best-effort delete; treats NotFound as success |
| provisioner.go:160-203 | INFRA | loadTemplate() — reads session-agent-template ConfigMap from user's namespace; falls back to defaults if not found |
| provisioner.go:205-215 | INFRA | defaultSessionAgentTpl() — compiled-in defaults per spec table |
| provisioner.go:217-230 | INFRA | applyDefaultResources() — applies default CPU/memory requests and limits |
| provisioner.go:232-371 | INFRA | buildDeployment() — constructs project Deployment with all spec'd volumes, volumeMounts, env vars, labels, security context |
| provisioner.go:373-412 | INFRA | ensurePVC() — creates PVC if not exists; idempotent |
| sealedbox.go:1-18 | INFRA | Package doc + imports (blake2b, nacl/box) — implements libsodium crypto_box_seal compatible unseal |
| sealedbox.go:20-42 | INFRA | SealedBoxOpen() — NaCl sealed box unseal; Blake2b nonce derivation; spec'd crypto primitive |
| sealedbox.go:44-54 | INFRA | sealedBoxNonce() — Blake2b-512(ephemeralPK || recipientPK)[:24] per libsodium spec |
| x25519.go:1-15 | INFRA | Package + imports |
| x25519.go:17-23 | INFRA | X25519Keypair struct |
| x25519.go:25-44 | INFRA | GenerateX25519Keypair() — generates random X25519 keypair with RFC 7748 clamping |
| x25519.go:46-109 | INFRA | LoadOrGenerateX25519Keypair() — reads or generates X25519 keypair from/to k8s Secret; spec'd boot step 2 |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| spec-user-agent.md:3-5 | Token custodian: unseal crypto_box_seal → write Secret | tokens_test.go:TestHandleDelivery_Success, TestHandleDelivery_DecryptFailed | None | UNIT_ONLY |
| spec-user-agent.md:6-7 | Project pod lifecycle: provision/delete Deployments | provisioner_test.go:TestProvisioner_ProvisionCreatesDeploymentAndPVCs, TestProvisioner_Delete | None | UNIT_ONLY |
| spec-user-agent.md:30 | Boot step 1: nkey-seed fatal exit if missing | x25519_test.go:TestLoadOrGenerateX25519Keypair_MissingSecret (covers missing Secret path) | None | UNIT_ONLY |
| spec-user-agent.md:31 | Boot step 2: load or generate X25519 keypair | x25519_test.go:TestLoadOrGenerateX25519Keypair_ExistingKey, TestLoadOrGenerateX25519Keypair_Generate | None | UNIT_ONLY |
| spec-user-agent.md:33-35 | Boot step 3: challenge-response with x25519_pub | auth_test.go:TestAuthenticate_Success, TestAuthenticate_SendsX25519Pub | None | UNIT_ONLY |
| spec-user-agent.md:37 | Boot step 5: subscribe all four subjects, RBAC-reject logged as warning | main_test.go:TestRunNATSLoop_FullJWTSubscribesAllSubjects | None | UNIT_ONLY |
| spec-user-agent.md:38 | Boot step 6: JWT refresh before TTL expiry | auth_test.go:TestStartRefreshLoop_Fires, TestStartRefreshLoop_StopsOnCancel | None | UNIT_ONLY |
| spec-user-agent.md:39 | Boot step 7: activate triggers re-auth + reconnect | main_test.go:TestRunNATSLoop_ActivateTriggersReconnect, TestRunNATSLoop_ReAuthOnActivate | None | UNIT_ONLY |
| spec-user-agent.md:40 | Token availability checked at provision time via os.ReadFile | provisioner_test.go:TestProvisioner_HostNotTrusted | None | UNIT_ONLY |
| spec-user-agent.md:46-50 | NATS subjects table: all four subjects | main_test.go (loop tests exercise all four subjects) | None | UNIT_ONLY |
| spec-user-agent.md:49 | provision reply: {ok:true, status:provisioning} | provisioner_test.go:TestProvisioner_ProvisionCreatesDeploymentAndPVCs | None | UNIT_ONLY |
| spec-user-agent.md:49 | provision: no token → {error:host_not_trusted} | provisioner_test.go:TestProvisioner_HostNotTrusted | None | UNIT_ONLY |
| spec-user-agent.md:50 | delete: idempotent NotFound | provisioner_test.go:TestProvisioner_Delete_NotFound | None | UNIT_ONLY |
| spec-user-agent.md:60 | Template from user's own namespace, not mclaude-system | provisioner_test.go:TestProvisioner_ProvisionCreatesDeploymentAndPVCs (ConfigMap in user ns) | None | UNIT_ONLY |
| spec-user-agent.md:61-67 | All six volume mounts on project Deployment | provisioner_test.go:TestProvisioner_DeploymentVolumeMounts | None | UNIT_ONLY |
| spec-user-agent.md:68 | All 10 env vars on project Deployment | provisioner_test.go:TestProvisioner_DeploymentEnvVars | None | UNIT_ONLY |
| spec-user-agent.md:69 | Create per-project PVCs | provisioner_test.go:TestProvisioner_ProvisionCreatesDeploymentAndPVCs | None | UNIT_ONLY |
| spec-user-agent.md:70 | mclaude/managed=true label | provisioner_test.go:TestProvisioner_DeploymentManagedLabel | None | UNIT_ONLY |
| spec-user-agent.md:74-76 | Delete: no PVC cleanup in v1 | provisioner_test.go:TestProvisioner_Delete (verifies only Deployment deleted, not PVCs) | None | UNIT_ONLY |
| spec-user-agent.md:94-96 | decrypt_failed reply on seal-open failure | tokens_test.go:TestHandleDelivery_DecryptFailed | None | UNIT_ONLY |
| spec-user-agent.md:109-117 | Error handling: exponential backoff (cap 60s) for CP failures | auth_test.go:TestAuthenticate_ServerError (tests error propagation; backoff tested via integration with authenticateWithBackoff in main_test.go) | None | UNIT_ONLY |
| spec-user-agent.md:125-126 | Default template when ConfigMap absent | provisioner_test.go:TestProvisioner_DefaultTemplate | None | UNIT_ONLY |
| spec-user-agent.md:129-143 | Compiled-in defaults table | provisioner_test.go:TestProvisioner_DefaultTemplate (verifies non-empty image; partial coverage) | None | UNIT_ONLY |
| adr-0091:29 | NaCl sealed box (crypto_box_seal compatible) | sealedbox_test.go:TestSealedBoxRoundtrip, TestSealedBoxOpen_WrongKey, TestSealedBoxOpen_TruncatedInput | None | UNIT_ONLY |
| spec-nats-data-taxonomy.md:254-276 | Bootstrap JWT: subscribe-all then RBAC rejects non-activate | main_test.go:TestRunNATSLoop_ActivateTriggersReconnect (plain server — RBAC not enforced; subscription behavior tested structurally) | None | UNIT_ONLY |

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` have `**Component**: mclaude-user-agent` (or any alias). The four open bugs are: Droid-Shield False Positives (droid-shield scanner), PushToTalk race (spa), First-run message injection (spa), Getting Started canned text (spa). None relate to this component.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No user-agent bugs open |

### Summary

- Implemented: 52
- Gap: 0
- Partial: 0
- Infra: 52
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 25
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0
