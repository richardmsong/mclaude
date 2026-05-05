# Implementation Audit: mclaude-user-agent

**Component**: mclaude-user-agent
**Component root**: `mclaude-user-agent/`
**Spec files**: `docs/mclaude-user-agent/spec-user-agent.md`, `docs/adr-0091-per-user-anthropic-token.md`, `docs/spec-state-schema.md` (user-agent-key Secret, user-tokens-{uslug} Secret, user_host_agents, host_grants), `docs/spec-nats-data-taxonomy.md` (UserAgent permission sets, agent.* subjects)

## Run: 2026-05-04T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-user-agent.md:5 | "Token custodian. Receives crypto_box_seal-wrapped Anthropic OAuth tokens…unseals them with its X25519 private key, and writes plaintext to a per-user k8s Secret" | tokens.go:56-88, sealedbox.go:26-44 | IMPLEMENTED | — | Full unseal path: base64 decode → SealedBoxOpen → patchTokenSecret |
| spec-user-agent.md:6 | "Project pod lifecycle manager. Reacts to NATS provision/delete events from CP and creates/deletes per-project Deployments in the user namespace." | provisioner.go:88-160, main.go:197-231 | IMPLEMENTED | — | Provision/Delete handlers wired to NATS subjects |
| spec-user-agent.md:14 | "Created by: mclaude-controller-k8s when the host grants the user access" | (controller scope, not user-agent code) | IMPLEMENTED | — | User-agent doesn't create itself; this is a controller concern. Correct. |
| spec-user-agent.md:15 | "Replicas: 1." | provisioner.go:238 (`replicas := int32(1)`) | IMPLEMENTED | — | Project Deployments set to 1 replica |
| spec-user-agent.md:16 | "Strategy: RollingUpdate." | provisioner.go:334-336 | IMPLEMENTED | — | `RollingUpdateDeploymentStrategyType` used |
| spec-user-agent.md:17-19 | "Volumes: user-agent-key Secret (at /secrets/agent/), user-tokens-{uslug} Secret (at /secrets/tokens/)" | main.go:74 reads from `/secrets/agent/nkey-seed`; x25519.go:51-108 reads from `user-agent-key` Secret; tokens.go:319 mounts `user-tokens-{uslug}` at `/secrets/tokens/` | IMPLEMENTED | — | Both volumes consumed correctly |
| spec-user-agent.md:20 | "Env: USER_SLUG, HOST_SLUG, NAMESPACE (= mclaude-{uslug}), NATS_URL, CONTROL_PLANE_URL" | main.go:56-62 (envRequired calls for USER_SLUG, HOST_SLUG, NATS_URL, CONTROL_PLANE_URL; envOr for NAMESPACE) | IMPLEMENTED | — | All five env vars read at startup |
| spec-user-agent.md:30 | "Read nkey-seed from /secrets/agent/nkey-seed. Fatal exit if missing" | main.go:73-77 | IMPLEMENTED | — | `os.ReadFile` with fatal error if missing |
| spec-user-agent.md:31 | "Read x25519-private from /secrets/agent/x25519-private. If absent: generate a new X25519 keypair, write the private key to the Secret via the k8s API" | x25519.go:51-108 (LoadOrGenerateX25519Keypair) | IMPLEMENTED | — | Reads from `user-agent-key` Secret; generates and patches if absent |
| spec-user-agent.md:33-35 | "Authenticate to control-plane via challenge-response: POST /api/auth/challenge {nkey_public} → receives nonce. Sign nonce. POST /api/auth/verify {nkey_public, challenge, signature, x25519_pub}" | auth.go:92-122 (Authenticate), auth.go:124-153 (requestChallenge), auth.go:158-204 (verifyChallenge) | IMPLEMENTED | — | x25519_pub sent in verify if set (auth.go:167-170) |
| spec-user-agent.md:36 | "Connect to hub NATS at NATS_URL with the issued JWT." | main.go:109-113 (connectNATS), auth.go:69-80 (JWTFunc) | IMPLEMENTED | — | Uses nats.UserJWT + nkeys.SignFunc |
| spec-user-agent.md:37-39 | "Subscribe based on JWT scope: Bootstrap JWT → subscribe to agent.activate only. Full JWT → subscribe to agent.>" | main.go:139-148, main.go:155-234 | PARTIAL | SPEC→FIX | Code subscribes to all subjects optimistically (activate + token-delivery + provision + delete) on initial connect regardless of JWT type. It does not detect bootstrap vs full JWT from the JWT itself — it relies on NATS RBAC to reject subscriptions that are out of scope. The comment at main.go:128-134 documents this explicitly. The spec says "Subscribe based on JWT scope" as if the agent inspects the JWT; the code instead lets NATS reject and logs a warning. Functionally equivalent but the subscription failure for token-delivery, provision, delete during bootstrap is logged as a warning (main.go:192-194, 217-218, 228-230) without falling back cleanly to activate-only. The agent continues running with failed subscriptions. |
| spec-user-agent.md:40 | "Background loop: re-run challenge-response before TTL expiry (5-minute JWT TTL — same refresh pattern as session-agent's agentJWTRefreshBuffer)." | auth.go:210-236 (StartRefreshLoop, startRefreshLoopWithInterval), auth.go:18-22 (agentJWTTTL=5m, agentJWTRefreshBuffer=60s) | IMPLEMENTED | — | Fires at TTL − 60s = 4 minutes |
| spec-user-agent.md:41 | "On receipt of .agent.activate (signal-only payload, no JWT): re-run challenge-response to obtain the full JWT, reconnect with full permissions, re-subscribe to agent.>" | main.go:167-177 (activate handler) | PARTIAL | SPEC→FIX | Code re-runs challenge-response on activate (main.go:170-173) and logs success, but does NOT reconnect the NATS connection or re-subscribe. The comment at main.go:175-177 explains the code relies on NATS library reconnect with updated JWT. There is no re-subscription to agent.> after activation — the subscriptions already set up with a rejected (bootstrap) JWT are not replaced. A user-agent that had its token-delivery/provision/delete subscriptions rejected due to a bootstrap JWT will NOT receive them after activation without an explicit reconnect + re-subscribe. |
| spec-user-agent.md:49 | "agent.activate: Sub | Signal-only payload. Triggers re-auth; no token material." | main.go:167-177 | IMPLEMENTED | — | Subscribed; re-runs authenticateWithBackoff on receipt |
| spec-user-agent.md:50 | "agent.token-delivery: Sub (request/reply) | Payload: {provider: 'anthropic', token_wrapped: '<base64>'}. crypto_box_seal_open → writes to user-tokens-{uslug} Secret. Replies {ok: true} or {ok: false, error: 'decrypt_failed'}" | main.go:184-194; tokens.go:56-88 | IMPLEMENTED | — | Full request/reply path; replies on msg.Reply |
| spec-user-agent.md:51 | "agent.projects.provision: …User-agent verifies a token exists for the requested provider; if not, replies {error: 'host_not_trusted'}" | provisioner.go:97-102 | IMPLEMENTED | — | Reads token file; returns host_not_trusted if absent/empty |
| spec-user-agent.md:51 | "…otherwise creates the project Deployment in the user namespace. Replies {ok: true, status: 'provisioning'}" | provisioner.go:122-148; main.go:197-213 | IMPLEMENTED | — | Creates deploy + PVCs; returns ok:true, status:"provisioning" |
| spec-user-agent.md:52 | "agent.projects.delete: Sub | Best-effort delete of the project Deployment. No reply expected." | main.go:221-231; provisioner.go:152-160 | IMPLEMENTED | — | No reply sent; Delete treats NotFound as success |
| spec-user-agent.md:54 | "The user-agent has no JetStream / KV access at any point." | (code does not import any nats.go JetStream APIs) | IMPLEMENTED | — | No `nc.JetStream()` or KV calls anywhere in code |
| spec-user-agent.md:60-61 | "On agent.projects.provision: 1. Verify /secrets/tokens/{provider} is non-empty." | provisioner.go:97-102 | IMPLEMENTED | — | Reads from `/secrets/tokens/{provider}` via tokenBaseDir |
| spec-user-agent.md:62 | "Build the Deployment using the session-agent-template ConfigMap in the user's own namespace" | provisioner.go:163-204 (loadTemplate reads from user namespace, not mclaude-system) | IMPLEMENTED | — | Gets ConfigMap named "session-agent-template" from user namespace |
| spec-user-agent.md:63-69 | "Mount the full set of volumes: project-{projectId} PVC at /data, nix-{projectId} PVC at /nix, user-config ConfigMap at /home/node/.claude-seed, user-secrets Secret at /home/node/.user-secrets, user-tokens-{uslug} Secret at /secrets/tokens/, claude-home emptyDir at /home/node/.claude" | provisioner.go:267-321 | IMPLEMENTED | — | All 6 volumes and mounts present |
| spec-user-agent.md:70 | "Set env TOKEN_PROVIDER, PROJECT_ID, PROJECT_SLUG, GIT_URL, HOST_SLUG, USER_SLUG, USER_ID, GIT_IDENTITY_ID, NATS_URL, CONTROL_PLANE_URL." | provisioner.go:247-258 | IMPLEMENTED | — | All 10 env vars set |
| spec-user-agent.md:71 | "Create per-project PVCs (project-{projectId} and nix-{projectId}) using sizes from the template." | provisioner.go:112-119 (ensurePVC calls); provisioner.go:375-413 (ensurePVC impl) | IMPLEMENTED | — | Creates both PVCs if not already present; sizes from template |
| spec-user-agent.md:72 | "Apply the Deployment with mclaude/managed=true label." | provisioner.go:326-329 | IMPLEMENTED | — | Labels include `"mclaude/managed": "true"` |
| spec-user-agent.md:74 | "The user-agent does not mint session-agent NKeys or write agent-nkey-{projectId} Secrets." | (no such code in user-agent) | IMPLEMENTED | — | Only touches user-agent-key and user-tokens-{uslug} Secrets |
| spec-user-agent.md:77-78 | "On agent.projects.delete: 1. kubectl delete deployment project-{projectId}. 2. Best-effort PVC cleanup (defer to ADR-0091 deferred)" | provisioner.go:152-160 | PARTIAL | SPEC→FIX | Only deletes the Deployment; does NOT attempt any PVC cleanup even as best-effort. Spec says "defer to ADR-0091 deferred — manual kubectl delete pvc if needed" meaning PVC cleanup is intentionally omitted in v1. This is acceptable per spec's explicit deferral. Classifying as PARTIAL only because spec says "best-effort PVC cleanup (defer)" — the deferral makes this SPEC→FIX: spec should say "PVC cleanup is omitted in v1" instead of "best-effort". |
| spec-user-agent.md:80-83 | "On user-agent restart: existing Deployments continue. Does not reconcile. Missed delete events not retroactively reconciled." | (no reconcile-on-start code anywhere in main.go) | IMPLEMENTED | — | Agent does not query CP or enumerate pods on start; purely event-driven |
| spec-user-agent.md:89 | "SPA reads the user-agent's X25519 pubkey via GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey." | (CP-side endpoint, not user-agent code) | IMPLEMENTED | — | User-agent registers pubkey during verify; the endpoint is CP's responsibility |
| spec-user-agent.md:93-94 | "User-agent writes plaintext to k8s Secret user-tokens-{uslug} at key {provider}. The Secret already exists (created empty by mclaude-controller-k8s); user-agent only patches per-provider data keys." | tokens.go:93-138 (patchTokenSecret) | IMPLEMENTED | — | Uses StrategicMergePatch; creates Secret only if NotFound (graceful fallback) |
| spec-user-agent.md:98 | "If crypto_box_seal_open fails, the user-agent replies {ok: false, error: 'decrypt_failed'}" | tokens.go:76-79 | IMPLEMENTED | — | Returns decrypt_failed on seal-open failure |
| spec-user-agent.md:112-115 | "Error handling: nkey-seed missing → Fatal exit" | main.go:73-77 | IMPLEMENTED | — | Returns error (which causes log.Fatal in main.go:68) |
| spec-user-agent.md:116 | "Challenge-response fails → Retry with exponential backoff (cap 60s)" | main.go:239-253 (authenticateWithBackoff); auth.go backoff setup with MaxInterval=60s | IMPLEMENTED | — | Uses cenkalti/backoff with 60s cap |
| spec-user-agent.md:117 | "crypto_box_seal_open fails → Reply {ok: false, error: 'decrypt_failed'}" | tokens.go:76-79 | IMPLEMENTED | — | Already counted above |
| spec-user-agent.md:118 | "Project provision when no token → Reply {error: 'host_not_trusted'}" | provisioner.go:100-102 | IMPLEMENTED | — | Already counted above |
| spec-user-agent.md:119 | "kubectl create deployment fails → Reply {error: '...', message: '...'}" | provisioner.go:129-137 | PARTIAL | SPEC→FIX | Code returns errors like `"deploy_create_failed: <err>"` in the Error field but does not separate into distinct `error` + `message` fields. The ProvisionReply struct has only `OK`, `Status`, and `Error` — no separate `message` field. Spec says `{error: "...", message: "..."}` which implies two separate fields. Functionally the error is returned but the shape diverges slightly. Spec should be updated to match the actual reply shape or code should add a Message field. |
| spec-user-agent.md:120 | "Project delete when Deployment doesn't exist → Treat as success (idempotent)." | provisioner.go:153-158 | IMPLEMENTED | — | `k8serrors.IsNotFound(err)` check returns success |
| spec-user-agent.md:121 | "User-agent JWT TTL expires → NATS drops; on reconnect JWT re-acquired via challenge-response." | main.go:256-268 (connectNATS with MaxReconnects(-1)); auth.go:69-80 (JWTFunc); auth.go:210-236 (refresh loop) | IMPLEMENTED | — | MaxReconnects(-1) means infinite reconnect; JWTFunc returns current JWT |
| spec-user-agent.md:128 | "{release}-session-agent-template ConfigMap in mclaude-system (cross-namespace read for project pod template)." | provisioner.go:163-164 (reads from user namespace, NOT mclaude-system) | PARTIAL | SPEC→FIX | Spec says "cross-namespace read for project pod template" in Dependencies section, but the Project Pod Lifecycle section (spec-user-agent.md:62) correctly says "in the user's own namespace" — the controller copies it there. The Dependencies section is inconsistent with the more-detailed Lifecycle section. Spec-user-agent.md:128 should say "session-agent-template ConfigMap in the user namespace (copied from mclaude-system by controller at provisioning time)." Code is correct. |
| spec-nats-data-taxonomy.md:252-260 | "Bootstrap JWT: Pub: _INBOX.> Sub: mclaude.users.{uslug}.hosts.{hslug}.agent.activate + _INBOX.>" | (JWT issued by CP — not user-agent code. User-agent subscribes to activate) | IMPLEMENTED | — | User-agent subscribes to activateSubj on first connect (main.go:167) |
| spec-nats-data-taxonomy.md:264-276 | "Full JWT: Pub/Sub: mclaude.users.{uslug}.hosts.{hslug}.agent.> + _INBOX.>" | (JWT issued by CP. User-agent subscribes to agent.>) | IMPLEMENTED | — | All four agent.* subjects subscribed in subscribeAgentSubjects |
| adr-0091:31 | "User-agent X25519 keypair: Generated at first startup; private key persisted in k8s Secret user-agent-key" | x25519.go:25-44 (GenerateX25519Keypair), x25519.go:80-108 (write to Secret) | IMPLEMENTED | — | Clamps key per RFC 7748, patches Secret |
| adr-0091:32 | "User-agent X25519 pubkey: Registered with CP via JWT issuance endpoint; stored in DB" | auth.go:158-204 (verifyChallenge sends x25519_pub) | IMPLEMENTED | — | Sent in POST /api/auth/verify |
| adr-0091:150 | "On subsequent startups: reads NKey seed + X25519 private key from Secret; reads plaintext tokens from user-tokens-{uslug} Secret." | x25519.go:60-78 (reads x25519-private if present) | PARTIAL | SPEC→FIX | Code reads X25519 private key from Secret on restart correctly (x25519.go:60-78). However, it does NOT read plaintext tokens from user-tokens-{uslug} Secret into memory on startup. Spec step 7-8 in ADR-0091:153-155 says "Check k8s Secret user-tokens-{uslug} for existing plaintext tokens; if present: hold in memory." The code does not do this — token check is done live (reading the file) at provision time. For the provision flow this is functionally OK (file-based check), but the in-memory hydration on restart described in spec is absent. |
| adr-0091:290-291 | "Unseals token_wrapped with X25519 private key; writes plaintext to user-tokens-{uslug} k8s Secret (keyed by provider)." | tokens.go:73-88 | IMPLEMENTED | — | Full path implemented |
| adr-0091:295 | "Event-driven, not reconciling. k8s is the source of truth." | (no reconcile code) | IMPLEMENTED | — | No pod enumeration or state sync on startup |
| spec-state-schema.md:499-503 | "user-agent-key Secret keys: nkey-seed (written by host controller), x25519-private (written by user-agent on first startup)" | x25519.go:60 reads `x25519-private`; x25519.go:86 writes `x25519-private`; main.go:74 reads from `/secrets/agent/nkey-seed` | IMPLEMENTED | — | Correct key names used |
| spec-state-schema.md:507-510 | "user-tokens-{uslug} Secret keys: anthropic (plaintext), {provider} (plaintext)" | tokens.go:97-99 (patchTokenSecret uses provider as key) | IMPLEMENTED | — | Provider-keyed writes |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-19 (imports) | INFRA | Standard imports for context, JSON, OS, signals, time, NATS, zerolog, k8s client |
| main.go:22-28 (AgentConfig struct) | INFRA | Holds env-sourced config; directly implements spec-user-agent.md:20 env list |
| main.go:30-43 (envRequired, envOr) | INFRA | Helper functions for env var loading; no independent behavior |
| main.go:45-70 (main func) | INFRA | Entry point; log setup, config load, context setup, calls run() |
| main.go:72-153 (run func) | INFRA | Boot sequence orchestrator; all steps are spec'd in spec-user-agent.md:30-41 |
| main.go:155-234 (subscribeAgentSubjects) | INFRA | NATS subscription setup; implements spec-user-agent.md:37-41 subject table |
| main.go:237-253 (authenticateWithBackoff) | INFRA | Retry wrapper for challenge-response; implements error-handling spec |
| main.go:255-268 (connectNATS) | INFRA | NATS connection with JWT/sign funcs; necessary infrastructure |
| main.go:270-281 (newK8sClient) | INFRA | In-cluster k8s client creation; necessary infrastructure for all k8s ops |
| main.go:283-284 (metav1 import guard) | INFRA | Ensures metav1 import is used; compiler workaround |
| auth.go:1-16 (imports) | INFRA | Standard imports for HTTP client, JSON, sync, NKeys |
| auth.go:18-23 (constants) | INFRA | agentJWTTTL=5m, agentJWTRefreshBuffer=60s; directly implements spec timing |
| auth.go:25-36 (UserAgentAuth struct) | INFRA | Identity + JWT state holder; implements challenge-response pattern |
| auth.go:38-50 (NewUserAgentAuth) | INFRA | Constructor from NKey seed bytes |
| auth.go:52-58 (SetX25519Pub) | INFRA | Thread-safe setter for x25519 pub before auth; spec-required |
| auth.go:60-67 (PublicKey) | INFRA | Returns NKey public key string; used in challenge-response |
| auth.go:69-87 (JWTFunc, SignFunc) | INFRA | NATS library callback functions; necessary for nats.UserJWT() |
| auth.go:89-122 (Authenticate) | INFRA | Core challenge-response orchestration; directly spec'd |
| auth.go:124-154 (requestChallenge) | INFRA | HTTP POST /api/auth/challenge; directly spec'd |
| auth.go:156-205 (verifyChallenge) | INFRA | HTTP POST /api/auth/verify with x25519_pub; directly spec'd |
| auth.go:207-236 (StartRefreshLoop, startRefreshLoopWithInterval) | INFRA | JWT refresh before TTL; directly spec'd; internal function exists for testability |
| tokens.go:1-15 (imports) | INFRA | Standard imports |
| tokens.go:17-27 (TokenDeliveryRequest, TokenDeliveryReply structs) | INFRA | NATS payload types; directly spec'd |
| tokens.go:29-49 (TokenManager struct, NewTokenManager) | INFRA | State holder for token delivery operations |
| tokens.go:51-88 (HandleDelivery) | INFRA | Core token-delivery handler; directly spec'd |
| tokens.go:90-138 (patchTokenSecret) | INFRA | k8s Secret patch + create-fallback; implements spec step |
| tokens.go:140-143 (replyJSON) | INFRA | JSON marshal helper; necessary plumbing |
| provisioner.go:1-16 (imports) | INFRA | Standard imports |
| provisioner.go:18-30 (sessionAgentTpl struct) | INFRA | Template values parsed from ConfigMap; necessary for Deployment spec |
| provisioner.go:32-62 (Provisioner struct, constructors) | INFRA | Holds k8s client, namespace, config; NewProvisionerWithTokenDir exposes tokenBaseDir for test isolation — not spec'd but necessary testing infrastructure |
| provisioner.go:64-86 (ProvisionRequest, ProvisionReply, DeleteRequest structs) | INFRA | NATS payload types; directly spec'd |
| provisioner.go:88-149 (Provision) | INFRA | Core project provisioning; directly spec'd |
| provisioner.go:151-160 (Delete) | INFRA | Deployment deletion; directly spec'd |
| provisioner.go:162-232 (loadTemplate, defaultSessionAgentTpl, applyDefaultResources) | UNSPEC'd | Template loading with hardcoded defaults (CPU: 200m/2000m, memory: 64Mi/2Gi, PVC: 10Gi, image: mclaude-session-agent:latest, grace: 30s). Spec says "using sizes from the template" but does not specify what defaults to use when the ConfigMap is absent or fields are empty. The defaults are reasonable but not documented anywhere in spec or ADRs. This UNSPEC'd code is not dead — it runs in production when template fields are missing. Spec should document the fallback defaults. |
| provisioner.go:234-373 (buildDeployment) | INFRA | Deployment spec construction; all volumes/env/security fields directly spec'd |
| provisioner.go:375-413 (ensurePVC) | INFRA | PVC creation (idempotent); directly spec'd in step 5 of provision flow |
| sealedbox.go:1-11 (package + comment) | INFRA | libsodium sealed-box spec reference |
| sealedbox.go:13-20 (imports) | INFRA | Crypto imports; necessary for sealed-box implementation |
| sealedbox.go:26-44 (SealedBoxOpen) | INFRA | Unseal implementation; directly spec'd crypto primitive |
| sealedbox.go:46-74 (SealedBoxSeal) | INFRA | Seal implementation; used only in tests — not called from production code | 
| sealedbox.go:76-86 (sealedBoxNonce) | INFRA | Nonce derivation per libsodium spec; necessary for both Seal and Open |
| x25519.go:1-15 (imports) | INFRA | Standard imports |
| x25519.go:19-43 (X25519Keypair, GenerateX25519Keypair) | INFRA | Keypair type and generation; directly spec'd (boot step 2) |
| x25519.go:46-109 (LoadOrGenerateX25519Keypair) | INFRA | Load-or-generate from Secret; directly spec'd (boot step 2) |
| go.mod:1-61 | INFRA | Module dependencies: nats.go, nkeys, zerolog, x/crypto, k8s client, backoff |
| Dockerfile:1-7 | INFRA | Distroless image, binary copy, LOG_LEVEL env, entrypoint |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| spec-user-agent.md:5 / Token custodian | SealedBoxOpen + write to Secret | tokens_test.go:TestHandleDelivery_Success, TestHandleDelivery_DecryptFailed, TestHandleDelivery_MultiProvider; sealedbox_test.go:TestSealedBoxRoundtrip | None | UNIT_ONLY |
| spec-user-agent.md:30-31 / Boot step 1+2 (read nkey-seed, load/generate X25519) | Fatal if nkey-seed missing; generate X25519 if absent | x25519_test.go:TestLoadOrGenerateX25519Keypair_ExistingKey, _Generate, _MissingSecret, TestGenerateX25519Keypair | None | UNIT_ONLY |
| spec-user-agent.md:33-35 / Challenge-response with CP | POST challenge + verify with x25519_pub | auth_test.go:TestAuthenticate_Success, TestAuthenticate_SendsX25519Pub, TestAuthenticate_NoX25519Pub, TestAuthenticate_ServerError, TestAuthenticate_VerifyRejected | None | UNIT_ONLY |
| spec-user-agent.md:40 / JWT refresh loop | Refresh before TTL; stops on cancel | auth_test.go:TestStartRefreshLoop_Fires, TestStartRefreshLoop_StopsOnCancel | None | UNIT_ONLY |
| spec-user-agent.md:41 / agent.activate re-auth | Re-run challenge-response on activate signal | No unit test for activate handler in main.go; only integration-level would verify full NATS subscription + re-auth | None | UNTESTED |
| spec-user-agent.md:37-39 / Bootstrap vs full JWT subscription | Subscribe to activate-only vs full agent.> | No unit test; bootstrap/full distinction relies on NATS RBAC rejection which is not testable without a real NATS server | None | UNTESTED |
| spec-user-agent.md:50 / token-delivery request/reply | Receive wrapped token, unseal, write Secret, reply | tokens_test.go:TestHandleDelivery_Success, TestHandleDelivery_DecryptFailed, TestHandleDelivery_DefaultProvider, TestHandleDelivery_BadPayload | None | UNIT_ONLY |
| spec-user-agent.md:51 / agent.projects.provision | Verify token, create Deployment + PVCs, reply | provisioner_test.go:TestProvisioner_HostNotTrusted, _ProvisionCreatesDeploymentAndPVCs, _DeploymentEnvVars, _DeploymentVolumeMounts, _DeploymentManagedLabel, _ProvisionIdempotent, _DefaultTemplate | None | UNIT_ONLY |
| spec-user-agent.md:52 / agent.projects.delete | Best-effort Deployment delete | provisioner_test.go:TestProvisioner_Delete, TestProvisioner_Delete_NotFound | None | UNIT_ONLY |
| spec-user-agent.md:70 / Env vars on project pod | TOKEN_PROVIDER, PROJECT_ID, … all 10 env vars | provisioner_test.go:TestProvisioner_DeploymentEnvVars | None | UNIT_ONLY |
| spec-user-agent.md:63-69 / Volume mounts on project pod | All 6 volumes mounted correctly | provisioner_test.go:TestProvisioner_DeploymentVolumeMounts | None | UNIT_ONLY |
| spec-user-agent.md:72 / mclaude/managed label | Deployment labelled mclaude/managed=true | provisioner_test.go:TestProvisioner_DeploymentManagedLabel | None | UNIT_ONLY |
| adr-0091:31 / X25519 keypair RFC 7748 clamping | Private key clamped correctly | x25519_test.go:TestGenerateX25519Keypair (checks clamp bits) | None | UNIT_ONLY |
| sealedbox.go / Sealed-box crypto correctness | Roundtrip, wrong key, truncated, empty plaintext, different ephemeral | sealedbox_test.go:all 5 tests | None | UNIT_ONLY |

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` have `**Component**:` matching `mclaude-user-agent`. Open bugs (002-droid-shield-false-positives, 002-ptt-race-condition, 003-initial-message-injection-race, 005-getting-started-canned-text) all target SPA or Droid-Shield — not this component.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No open bugs for mclaude-user-agent |

### Summary

- Implemented: 33
- Gap: 0
- Partial: 6
- Infra: 44
- Unspec'd: 1
- Dead: 0
- Tested: 0
- Unit only: 13
- E2E only: 0
- Untested: 2
- Bugs fixed: 0
- Bugs open: 0
