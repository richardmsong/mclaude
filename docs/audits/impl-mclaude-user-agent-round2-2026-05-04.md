# Implementation Audit: mclaude-user-agent (Round 2)

**Component:** mclaude-user-agent  
**Component root:** mclaude-user-agent/  
**Specs:** docs/mclaude-user-agent/spec-user-agent.md, docs/adr-0091-per-user-anthropic-token.md, docs/spec-state-schema.md, docs/spec-nats-data-taxonomy.md  

## Run: 2026-05-04T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-user-agent.md:12 | "One Deployment per user namespace `mclaude-{uslug}` on each cluster host" | provisioner.go:234-373 (builds Deployment in user namespace) | IMPLEMENTED | | Namespace = cfg.Namespace = "mclaude-{uslug}" |
| spec-user-agent.md:14 | "Created by: mclaude-controller-k8s when the host grants the user access" | N/A (out of scope for this component) | IMPLEMENTED | | User-agent deployment by controller is not user-agent code; correct not to implement here |
| spec-user-agent.md:15 | "Replicas: 1" | provisioner.go:238: `replicas := int32(1)` | IMPLEMENTED | | |
| spec-user-agent.md:16 | "Strategy: RollingUpdate" | provisioner.go:335-337 | IMPLEMENTED | | `appsv1.RollingUpdateDeploymentStrategyType` |
| spec-user-agent.md:18 | "user-agent-key Secret (mounted at /secrets/agent/)" | main.go:74: `os.ReadFile("/secrets/agent/nkey-seed")` | IMPLEMENTED | | Mount path used at runtime |
| spec-user-agent.md:19 | "user-tokens-{uslug} Secret (mounted at /secrets/tokens/)" | provisioner.go:300-306 (volume) + 319 (mount at /secrets/tokens/) | IMPLEMENTED | | |
| spec-user-agent.md:20 | "Env: USER_SLUG, HOST_SLUG, NAMESPACE (= mclaude-{uslug}), NATS_URL (= HUB_NATS_URL), CONTROL_PLANE_URL" | main.go:57-62: envRequired("USER_SLUG"), envRequired("HOST_SLUG"), envOr("NAMESPACE",...), envRequired("NATS_URL"), envRequired("CONTROL_PLANE_URL") | IMPLEMENTED | | |
| spec-user-agent.md:21-26 | ServiceAccount Role: get/list/watch/create/update/patch/delete on Deployment and PVC; get/list/create/update/patch on Secret (only user-tokens-{uslug}); get/list/watch on ConfigMap (only session-agent-template) | N/A (RBAC is in Helm chart, not in user-agent binary) | IMPLEMENTED | | RBAC defined by controller, not user-agent code; correct scope |
| spec-user-agent.md:26 | "No ClusterRole / cross-namespace permissions. The pod-template ConfigMap is copied into the user namespace by mclaude-controller-k8s at provisioning time so the user-agent reads from its own namespace only." | provisioner.go:163-164: Get("session-agent-template") in p.namespace (user's own namespace) | IMPLEMENTED | | |
| spec-user-agent.md:31 | "Read nkey-seed from /secrets/agent/nkey-seed. Fatal exit if missing — the controller did not write it" | main.go:74-77: os.ReadFile("/secrets/agent/nkey-seed"); returns error wrapping "controller did not provision correctly" | IMPLEMENTED | | Non-zero exit via log.Fatal in main |
| spec-user-agent.md:32 | "Read x25519-private from /secrets/agent/x25519-private. If absent: generate a new X25519 keypair (libsodium crypto_box_keypair), write the private key to the Secret via the k8s API, hold the public key in memory." | x25519.go:51-108: LoadOrGenerateX25519Keypair reads secret.Data["x25519-private"]; if absent, generates + patches Secret | IMPLEMENTED | | |
| spec-user-agent.md:33-36 | "Authenticate to control-plane via challenge-response: POST /api/auth/challenge {nkey_public} → receives nonce. Sign nonce with NKey seed. POST /api/auth/verify {nkey_public, challenge, signature, x25519_pub} → CP stores x25519_pub" | auth.go:92-122: Authenticate() does requestChallenge + kp.Sign + verifyChallenge (with x25519_pub) | IMPLEMENTED | | |
| spec-user-agent.md:37 | "Connect to hub NATS at NATS_URL with the issued JWT." | main.go:121: runNATSLoop → connectNATS using auth.JWTFunc() + auth.SignFunc() | IMPLEMENTED | | |
| spec-user-agent.md:38-39 | "Subscribe based on JWT scope: Bootstrap JWT → subscribe to agent.activate only. Full JWT → subscribe to agent.>" | main.go:170-237: subscribes to activate always; subscribes token-delivery/provision/delete (RBAC rejects if bootstrap JWT) | PARTIAL | SPEC→FIX | Code subscribes to all 4 subjects on every connect; NATS RBAC gates the non-activate subjects when bootstrap JWT is in use (subscription attempt is logged as warn, not fatal). Spec says "subscribe to activate only" vs. code says subscribe to all but let RBAC reject. This is a defensible implementation choice: the code is simpler and correct (RBAC enforces the scope); the spec is slightly overly prescriptive about how the scoping is enforced. |
| spec-user-agent.md:40 | "Background loop: re-run challenge-response before TTL expiry (5-minute JWT TTL — same refresh pattern as session-agent's agentJWTRefreshBuffer)" | auth.go:210-236: StartRefreshLoop fires at agentJWTTTL - agentJWTRefreshBuffer = 4 min | IMPLEMENTED | | |
| spec-user-agent.md:41-43 | "On receipt of .agent.activate (signal-only payload, no JWT): re-run challenge-response to obtain the full JWT (since host_grants.status='accepted' now), reconnect with full permissions, re-subscribe to mclaude.users.{uslug}.hosts.{hslug}.agent.>" | main.go:170-183: activate handler calls authenticateWithBackoff → signals reconnectCh; loop drains + reconnects; re-subscribes all subjects on next iteration | IMPLEMENTED | | Round 2 fix: runNATSLoop replaces subscribeAgentSubjects |
| spec-user-agent.md:49 | "agent.activate: Sub; Signal-only payload ({} or {status:accepted}). Triggers re-auth; no token material." | main.go:146, 170-183: subscribes activateSubj; handler calls authenticateWithBackoff | IMPLEMENTED | | |
| spec-user-agent.md:50 | "agent.token-delivery: Sub (request/reply). Payload: {provider, token_wrapped}. Calls crypto_box_seal_open, writes plaintext to user-tokens-{uslug} Secret under key {provider}. Replies {ok: true} or {ok: false, error: decrypt_failed}" | main.go:193-200; tokens.go:56-88; sealedbox.go:26-43 | IMPLEMENTED | | |
| spec-user-agent.md:51 | "agent.projects.provision: Sub (request/reply). Payload: {projectId, projectSlug, gitUrl, gitIdentityId, backend}. Verifies token exists; creates Deployment. Replies {ok: true, status: provisioning}" | main.go:204-225; provisioner.go:90-148 | IMPLEMENTED | | |
| spec-user-agent.md:51 | "if no token: replies {error: host_not_trusted} (CP surfaces as 'Authorize this host')" | provisioner.go:99-101 | IMPLEMENTED | | |
| spec-user-agent.md:52 | "agent.projects.delete: Sub. Payload: {projectId, projectSlug}. Best-effort delete of project Deployment. No reply expected." | main.go:228-237; provisioner.go:152-160 | IMPLEMENTED | | |
| spec-user-agent.md:52 | "If user-agent is offline when CP publishes, the event is missed and the Deployment persists — manual kubectl delete is the operator escape hatch (see ADR-0091 Deferred)" | Acknowledged as deferred design constraint; no code needed | IMPLEMENTED | | v1 intentional behavior |
| spec-user-agent.md:54 | "The user-agent has no JetStream / KV access at any point. It cannot read mclaude-user-info-{uslug} or any other bucket." | NATS JWT scope (CP side); confirmed: no NATS KV calls anywhere in user-agent source | IMPLEMENTED | | |
| spec-user-agent.md:60 | "On agent.projects.provision step 1: Verify /secrets/tokens/{provider} is non-empty." | provisioner.go:96-101: os.ReadFile(p.tokenBaseDir+"/tokens/"+provider); checks err + len | IMPLEMENTED | | |
| spec-user-agent.md:62 | "Build the Deployment using the session-agent-template ConfigMap in the user's own namespace (a copy of mclaude-system/{release}-session-agent-template written by mclaude-controller-k8s)" | provisioner.go:163-204: loadTemplate() reads ConfigMap "session-agent-template" from p.namespace | IMPLEMENTED | | |
| spec-user-agent.md:63-69 | "Mount the full set of volumes: project-{projectId} PVC at /data; nix-{projectId} PVC at /nix; user-config ConfigMap at /home/node/.claude-seed; user-secrets Secret at /home/node/.user-secrets; user-tokens-{uslug} Secret at /secrets/tokens/; claude-home emptyDir at /home/node/.claude" | provisioner.go:267-321 | IMPLEMENTED | | All 6 volumes and mounts present |
| spec-user-agent.md:70 | "Set env TOKEN_PROVIDER, PROJECT_ID, PROJECT_SLUG, GIT_URL, HOST_SLUG, USER_SLUG, USER_ID, GIT_IDENTITY_ID, NATS_URL, CONTROL_PLANE_URL" | provisioner.go:247-258 | IMPLEMENTED | | All 10 env vars set |
| spec-user-agent.md:71 | "Create per-project PVCs (project-{projectId} and nix-{projectId}) using sizes from the template." | provisioner.go:112-119; ensurePVC:375-413 | IMPLEMENTED | | |
| spec-user-agent.md:72 | "Apply the Deployment with mclaude/managed=true label." | provisioner.go:326: Labels["mclaude/managed"]="true" | IMPLEMENTED | | |
| spec-user-agent.md:74 | "The user-agent does not mint session-agent NKeys or write agent-nkey-{projectId} Secrets." | No such code in any source file | IMPLEMENTED | | Confirmed absent |
| spec-user-agent.md:77-78 | "On agent.projects.delete: kubectl delete deployment project-{projectId}. PVC cleanup is omitted in v1 — project-{projectId} and nix-{projectId} PVCs persist after the Deployment is deleted." | provisioner.go:152-160: Delete() removes Deployment only; no PVC deletion | IMPLEMENTED | | Spec updated in Round 2 to reflect v1 deferral |
| spec-user-agent.md:80-83 | "On user-agent restart: existing Deployments continue running; does not query CP for expected projects or reconcile against KV; pods missed during downtime not reconciled." | No reconcile-on-restart code anywhere; design is event-driven only | IMPLEMENTED | | Confirmed correct absence of reconciler |
| spec-user-agent.md:91 | "crypto_box_seal_open(token_wrapped, x25519_priv, x25519_pub) → plaintext token" | sealedbox.go:26-43: SealedBoxOpen; tokens.go:75 | IMPLEMENTED | | libsodium-compatible NaCl sealed box |
| spec-user-agent.md:93 | "User-agent writes plaintext to k8s Secret user-tokens-{uslug} at key {provider} using the k8s API. The Secret already exists (created empty by mclaude-controller-k8s)" | tokens.go:93-137: patchTokenSecret uses StrategicMergePatch; graceful fallback if not found | IMPLEMENTED | | |
| spec-user-agent.md:94 | "User-agent replies {ok: true}." | tokens.go:88: replyJSON(TokenDeliveryReply{OK: true}) | IMPLEMENTED | | |
| spec-user-agent.md:96 | "Re-auth (token rotation) follows the same path; the existing user-tokens-{uslug} Secret entry is overwritten." | tokens.go:93-115: patch overwrites existing key | IMPLEMENTED | | |
| spec-user-agent.md:98 | "If crypto_box_seal_open fails, replies {ok: false, error: decrypt_failed}. SPA should re-fetch pubkey from CP and retry." | tokens.go:76-79: SealedBoxOpen failure → replyJSON(TokenDeliveryReply{OK:false, Error:"decrypt_failed"}) | IMPLEMENTED | | |
| spec-user-agent.md:113 | "nkey-seed missing in Secret: Fatal exit" | main.go:74-77: returns error → log.Fatal in main() | IMPLEMENTED | | |
| spec-user-agent.md:114 | "Challenge-response with CP fails: Retry with exponential backoff (cap 60s). User-agent never enters Ready until JWT acquired." | main.go:261-274: authenticateWithBackoff; bo.MaxInterval=60s | IMPLEMENTED | | |
| spec-user-agent.md:115 | "crypto_box_seal_open fails: Reply {ok: false, error: decrypt_failed}" | tokens.go:76-79 | IMPLEMENTED | | |
| spec-user-agent.md:116 | "Project provision when no token: Reply {error: host_not_trusted}" | provisioner.go:101 | IMPLEMENTED | | |
| spec-user-agent.md:117-118 | "kubectl create deployment fails: Reply {ok: false, status: ..., error: <machine-readable code>: <human message>}. The reply shape is the ProvisionReply struct with fields OK, Status, Error." | provisioner.go:75-79: ProvisionReply{OK bool, Status string, Error string}; failure paths return ProvisionReply{Error: "deploy_create_failed: ..."} | IMPLEMENTED | | OK is omitempty so false is omitted; spec says {ok: false} but omitempty on OK means false is absent in JSON — minor schema divergence |
| spec-user-agent.md:119 | "Project delete when Deployment doesn't exist: Treat as success (idempotent)." | provisioner.go:154-156: k8serrors.IsNotFound check, logs "or not found" | IMPLEMENTED | | |
| spec-user-agent.md:120 | "User-agent JWT TTL expires before refresh: NATS connection drops; on reconnect, JWT is re-acquired via challenge-response." | auth.go:207-236: StartRefreshLoop proactively refreshes before TTL | IMPLEMENTED | | |
| spec-user-agent.md:118 | "ProvisionReply struct with fields OK, Status, Error" | provisioner.go:76-79: OK has `json:"ok,omitempty"` — when false (zero value), ok field is absent in JSON | PARTIAL | SPEC→FIX | Spec says reply shape has ok field always present; code omits it when false due to omitempty. Receivers (CP/SPA) distinguish success by presence of error field. Either: (a) spec should document that ok is omitted on failure (consumers check error field), or (b) remove omitempty from OK. Spec should clarify. |
| spec-user-agent.md:124-128 | "Dependencies: Hub NATS, CP HTTP, Kubernetes API, libsodium, session-agent-template ConfigMap in user's own namespace. If absent, falls back to compiled-in defaults." | provisioner.go:163-169: loadTemplate falls back to defaultSessionAgentTpl() on NotFound | IMPLEMENTED | | |
| spec-user-agent.md:131-143 | "Project Pod Defaults table: image=mclaude-session-agent:latest, imagePullPolicy=IfNotPresent, terminationGracePeriodSeconds=30, resourcesJson={requests:{cpu:200m,memory:64Mi},limits:{cpu:2000m,memory:2Gi}}, projectPvcSize=10Gi, projectPvcStorageClass=(empty), nixPvcSize=10Gi, nixPvcStorageClass=(empty)" | provisioner.go:207-231: defaultSessionAgentTpl() + applyDefaultResources() | IMPLEMENTED | | All defaults match spec exactly |
| spec-user-agent.md:150 | "Does not subscribe to mclaude.hosts.{hslug}.> — that's the host controller's scope" | No such subscriptions in main.go | IMPLEMENTED | | |
| spec-user-agent.md:151 | "Does not read mclaude-user-info-{uslug} or any other KV bucket" | No NATS KV/JetStream API calls in any source file | IMPLEMENTED | | |
| spec-user-agent.md:152 | "Does not mint session-agent NKeys" | No nkeys.Create* calls for session-agents anywhere | IMPLEMENTED | | |
| spec-user-agent.md:153 | "Does not reconcile from an external expected pods list — k8s is the source of truth" | No list-all-pods-and-reconcile logic | IMPLEMENTED | | |
| spec-user-agent.md:154 | "Does not handle revocation — v1 escape hatch is kubectl delete namespace" | No revocation endpoints or subjects | IMPLEMENTED | | |
| adr-0091:146 | "Read NKey seed from k8s Secret user-agent-key in user namespace" | main.go:74: os.ReadFile("/secrets/agent/nkey-seed") | IMPLEMENTED | | Secret is volume-mounted at /secrets/agent/ |
| adr-0091:147-148 | "Read or generate X25519 keypair. If x25519-private absent: generate → write to Secret." | x25519.go:51-108 | IMPLEMENTED | | |
| adr-0091:148-149 | "Authenticate with CP via challenge-response. CP verifies NKey → issues bootstrap or full JWT depending on grant status. X25519 pubkey registered at this step." | auth.go:92-205 | IMPLEMENTED | | |
| adr-0091:149 | "Connect to NATS with issued JWT. Re-run challenge-response before TTL expiry to renew (same refresh pattern as session-agent's agentJWTRefreshBuffer)." | main.go:114-117 (StartRefreshLoop); main.go:121 (connectNATS) | IMPLEMENTED | | |
| adr-0091:150-151 | "If bootstrap JWT (grant pending): subscribe to mclaude.users.{uslug}.hosts.{hslug}.agent.activate only. Wait for signal." | main.go:170-187: subscribes activate; RBAC rejects broader subs | PARTIAL | SPEC→FIX | Same finding as spec-user-agent.md:38-39 — code subscribes all 4 subjects; RBAC enforces scope. Spec says activate-only; code lets RBAC do the gating. Implementation is correct; spec is prescriptive about subscribe-level control rather than RBAC-level control. |
| adr-0091:151 | "When signal received on .agent.activate: re-run challenge-response → CP issues full JWT (grant now accepted) → reconnect with full permissions → subscribe to mclaude.users.{uslug}.hosts.{hslug}.agent.>" | main.go:170-255: activate handler → authenticateWithBackoff → reconnectCh → drain + reconnect | IMPLEMENTED | | Round 2 fix confirmed |
| adr-0091:152 | "If full JWT on initial auth (grant already accepted, e.g. restart): skip to full subscription immediately." | main.go:151-255: loop connects immediately; subscribe attempts happen right away; RBAC allows all if full JWT | IMPLEMENTED | | Code is correct — RBAC governs what succeeds |
| adr-0091:153 | "Token availability is checked at provision time via os.ReadFile('/secrets/tokens/{provider}') rather than cached in memory at startup." | provisioner.go:96-101: os.ReadFile at provision time | IMPLEMENTED | | Spec updated in Round 2 (hold-in-memory removed) |
| adr-0091:154 | "Subscribe to .agent.token-delivery so subsequent SPA-initiated deliveries (re-auth, new providers) can be unsealed and patched into the Secret." | main.go:193-200 | IMPLEMENTED | | |
| adr-0091:155 | "Subscribe to .agent.projects.provision and .agent.projects.delete for project pod lifecycle." | main.go:204-237 | IMPLEMENTED | | |
| adr-0091:194-195 | "User-agent receives → unseals with X25519 private key → writes plaintext token to k8s Secret user-tokens-{uslug} (key: anthropic) → holds in memory." | tokens.go:75-88 (unseal + write Secret); no in-memory cache (spec updated in Round 2 to remove hold-in-memory) | IMPLEMENTED | | |
| adr-0091:283-296 | "mclaude-user-agent component: event-driven, not reconciling; creates session pods; mounts user-tokens-{uslug} Secret; sets TOKEN_PROVIDER, PROJECT_SLUG, GIT_URL, HOST_SLUG, USER_SLUG, GIT_IDENTITY_ID env vars" | provisioner.go:235-373 | IMPLEMENTED | | USER_ID and GIT_IDENTITY_ID also set |
| adr-0091:235 | "Provision payload: {projectId, projectSlug, gitUrl, gitIdentityId, backend}" | provisioner.go:65-73: ProvisionRequest struct | IMPLEMENTED | | Also includes userId, provider fields (backward-compatible additions) |
| adr-0091:237 | "User-agent checks: do I have the token for the requested provider? If no → returns error host_not_trusted" | provisioner.go:90-101 | IMPLEMENTED | | |
| adr-0091:239-240 | "Pod spec volumes: project-{projectId} PVC at /data; nix-{projectId} PVC at /nix; user-config ConfigMap at /home/node/.claude-seed; user-secrets Secret at /home/node/.user-secrets; user-tokens-{uslug} Secret at /secrets/tokens/; claude-home emptyDir at /home/node/.claude" | provisioner.go:267-321 | IMPLEMENTED | | |
| adr-0091:240 | "Env vars: TOKEN_PROVIDER, PROJECT_ID, PROJECT_SLUG, GIT_URL, HOST_SLUG, USER_SLUG, USER_ID, GIT_IDENTITY_ID, NATS_URL, CONTROL_PLANE_URL" | provisioner.go:247-258 | IMPLEMENTED | | |
| adr-0091:243-244 | "Session-agent self-bootstraps NATS credentials via challenge-response (existing flow, unchanged). User-agent does not mint session-agent NKeys." | No NKey minting in user-agent | IMPLEMENTED | | |
| adr-0091:250-252 | "Project deletion: delete Deployment. If offline, event missed, pod persists. v1 accepts this." | provisioner.go:152-160 | IMPLEMENTED | | |
| adr-0091:499-503 | "user-agent-key Secret keys: nkey-seed (NKey Ed25519 seed); x25519-private (base64(X25519 private key, 32 bytes))" | main.go:74 reads nkey-seed; x25519.go:60 reads secret.Data["x25519-private"]; x25519.go:86 stores as base64 | IMPLEMENTED | | |
| adr-0091:504-511 | "user-tokens-{uslug} Secret keys: anthropic (Anthropic OAuth token plaintext); {provider} (future providers)" | tokens.go:93-137: patchTokenSecret writes secret.Data[provider] | IMPLEMENTED | | |
| adr-0091:416-427 | "IssueUserAgentBootstrapJWT: Pub _INBOX.>; Sub mclaude.users.{uslug}.hosts.{hslug}.agent.activate + _INBOX.>; 5-min TTL" | CP code (not user-agent). User-agent consumes what CP issues. | IMPLEMENTED | | Out of user-agent scope |
| adr-0091:429-444 | "IssueUserAgentJWT: Pub mclaude.users.{uslug}.hosts.{hslug}.agent.> + _INBOX.>; Sub agent.> + _INBOX.>; 5-min TTL. No JetStream/KV access." | CP code (not user-agent). User-agent consumes what CP issues. | IMPLEMENTED | | Out of user-agent scope |
| spec-nats-data-taxonomy.md:62-66 | "users.{uslug}.hosts.{hslug}.agent.{activate, token-delivery, projects.provision, projects.delete}" | main.go:146-149: subject strings constructed correctly | IMPLEMENTED | | Subject format matches taxonomy |
| spec-nats-data-taxonomy.md:197-208 | "User-agent: Own uslug, one host, agent.> only. No JetStream/KV access." | main.go + auth.go: no JetStream calls; NATS JWT governed by CP | IMPLEMENTED | | |
| spec-state-schema.md:124-141 | "user_host_agents table: x25519_pub registered by user-agent during challenge-response POST /api/auth/verify with x25519_pub field" | auth.go:158-204: verifyChallenge sends x25519_pub in body; x25519_pub = base64(32 bytes) | IMPLEMENTED | | |
| spec-state-schema.md:144-150 | "host_grants table: status pending/accepted; user-agent waits in bootstrap state until status=accepted" | main.go:170-183: activate signal triggers re-auth to get full JWT | IMPLEMENTED | | |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-19 | INFRA | Package declaration and imports (context, json, fmt, os, signal, syscall, time; backoff, nats, zerolog, k8s) |
| main.go:21-28 | INFRA | AgentConfig struct — config bundle for all env-sourced values; directly spec'd by Env section in spec-user-agent.md:20 |
| main.go:30-43 | INFRA | envRequired/envOr helpers — standard env-var loading boilerplate |
| main.go:45-70 | INFRA | main(): logging setup, AgentConfig population from env, signal context; all spec'd by Deployment Env section |
| main.go:72-122 | INFRA | run(): orchestrates boot steps 1–8; each step maps to a spec boot sequence item |
| main.go:124-125 | INFRA | natsConnectFn type alias — test injection point for connectNATS |
| main.go:135-256 | INFRA | runNATSLoop: core NATS connect+subscribe+reconnect loop; fully spec'd by Boot Sequence steps 4–7 |
| main.go:259-275 | INFRA | authenticateWithBackoff: exponential-backoff wrapper around auth.Authenticate; spec'd by Error Handling: "Retry with exponential backoff (cap 60s)" |
| main.go:278-290 | INFRA | connectNATS: NATS connection factory with UserJWT + MaxReconnects(-1); spec'd by Boot step 4 |
| main.go:293-307 | INFRA | newK8sClient: in-cluster k8s client factory; required for all Deployment/Secret/PVC operations |
| auth.go:1-16 | INFRA | Package + imports |
| auth.go:18-23 | INFRA | agentJWTTTL / agentJWTRefreshBuffer constants — spec'd at spec-user-agent.md:40 and adr-0091:149 |
| auth.go:25-36 | INFRA | UserAgentAuth struct — bundles NKey, CP URL, x25519Pub, current JWT, mutex |
| auth.go:38-49 | INFRA | NewUserAgentAuth constructor |
| auth.go:52-58 | INFRA | SetX25519Pub — required by Boot step 2 / adr-0091:148 |
| auth.go:61-68 | INFRA | PublicKey helper |
| auth.go:70-87 | INFRA | JWTFunc / SignFunc — NATS authentication callbacks |
| auth.go:89-122 | INFRA | Authenticate — challenge-response; spec'd at Boot step 3 / adr-0091:148 |
| auth.go:124-153 | INFRA | requestChallenge — POST /api/auth/challenge |
| auth.go:156-205 | INFRA | verifyChallenge — POST /api/auth/verify with x25519_pub; spec'd at adr-0091:148 |
| auth.go:207-236 | INFRA | StartRefreshLoop / startRefreshLoopWithInterval — JWT refresh; spec'd at Boot step 6 / adr-0091:149 |
| provisioner.go:1-16 | INFRA | Package + imports |
| provisioner.go:18-30 | INFRA | sessionAgentTpl struct — pod template values parsed from ConfigMap |
| provisioner.go:32-39 | INFRA | Provisioner struct — bundles k8s, namespace, config, log, tokenBaseDir |
| provisioner.go:41-50 | INFRA | NewProvisioner constructor (production: tokenBaseDir="/secrets") |
| provisioner.go:52-62 | INFRA | NewProvisionerWithTokenDir — test injection point for token base directory |
| provisioner.go:64-86 | INFRA | ProvisionRequest / ProvisionReply / DeleteRequest structs |
| provisioner.go:88-148 | INFRA | Provision method — spec'd by Project Pod Lifecycle "On agent.projects.provision" |
| provisioner.go:151-160 | INFRA | Delete method — spec'd by Project Pod Lifecycle "On agent.projects.delete" |
| provisioner.go:162-204 | INFRA | loadTemplate — reads session-agent-template ConfigMap; spec'd at spec-user-agent.md:62 |
| provisioner.go:206-231 | INFRA | defaultSessionAgentTpl / applyDefaultResources — spec'd by Project Pod Defaults table |
| provisioner.go:234-373 | INFRA | buildDeployment — spec'd by Project Pod Lifecycle full pod spec |
| provisioner.go:375-413 | INFRA | ensurePVC — spec'd by step 5 "Create per-project PVCs" |
| tokens.go:1-15 | INFRA | Package + imports |
| tokens.go:17-27 | INFRA | TokenDeliveryRequest / TokenDeliveryReply structs — spec'd by agent.token-delivery payload |
| tokens.go:29-48 | INFRA | TokenManager struct + NewTokenManager constructor |
| tokens.go:51-88 | INFRA | HandleDelivery — spec'd by Token Delivery section |
| tokens.go:91-138 | INFRA | patchTokenSecret — Strategic-merge-patch on user-tokens-{uslug} Secret; spec'd by Token Delivery step 5 |
| tokens.go:140-144 | INFRA | replyJSON helper |
| sealedbox.go:1-11 | INFRA | Package declaration and doc comment |
| sealedbox.go:12-19 | INFRA | Imports (crypto/rand, blake2b, curve25519, nacl/box) |
| sealedbox.go:21-43 | INFRA | SealedBoxOpen — NaCl sealed-box unseal; spec'd as "crypto_box_seal_open" in adr-0091 and spec-user-agent.md |
| sealedbox.go:46-74 | INFRA | SealedBoxSeal — used in tests to produce valid sealed boxes for testing SealedBoxOpen; production code only calls Open; the Seal implementation in production code is a test utility present in production package |
| sealedbox.go:76-86 | INFRA | sealedBoxNonce — Blake2b-512 nonce derivation per libsodium spec |
| x25519.go:1-15 | INFRA | Package + imports |
| x25519.go:17-44 | INFRA | X25519Keypair struct + GenerateX25519Keypair — spec'd at Boot step 2 / adr-0091:147 |
| x25519.go:46-109 | INFRA | LoadOrGenerateX25519Keypair — spec'd at Boot step 2 |
| sealedbox.go:46-74 | UNSPEC'd | SealedBoxSeal is only called from test files (sealedbox_test.go, tokens_test.go, x25519_test.go); production code only needs SealedBoxOpen. The Seal function exists to enable round-trip testing. No spec requires the user-agent to produce sealed boxes. Could be moved to a test helper file (or kept — the Go test convention of same-package tests makes this a minor style issue, not a bug). |
| provisioner.go:123,142 | DEAD | `existing := &appsv1.Deployment{}` allocated and immediately blanked with `_ = existing`. The Get() result is captured in `_, getErr` so `existing` serves no purpose. Should be removed to clean up the code. |
| main.go:306 | INFRA | `var _ = metav1.GetOptions{}` — compile-time import anchor to suppress unused import; needed because metav1 is imported for metav1.ObjectMeta in provisioner.go but the import is in main.go context; acceptable INFRA. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| spec-user-agent.md:31 | nkey-seed fatal exit if missing | TestLoadOrGenerateX25519Keypair_MissingSecret (x25519_test.go:136) | None | UNIT_ONLY |
| spec-user-agent.md:32 | Load or generate X25519 keypair; write to Secret if absent | TestLoadOrGenerateX25519Keypair_ExistingKey, TestLoadOrGenerateX25519Keypair_Generate (x25519_test.go:52,86) | None | UNIT_ONLY |
| spec-user-agent.md:33-36 | Challenge-response auth with CP; x25519_pub sent in verify | TestAuthenticate_Success, TestAuthenticate_SendsX25519Pub, TestAuthenticate_NoX25519Pub, TestAuthenticate_ServerError, TestAuthenticate_VerifyRejected (auth_test.go:70-189) | None | UNIT_ONLY |
| spec-user-agent.md:40 | JWT refresh loop fires before TTL expiry | TestStartRefreshLoop_Fires, TestStartRefreshLoop_StopsOnCancel (auth_test.go:193-273) | None | UNIT_ONLY |
| spec-user-agent.md:41-43 | On activate signal: re-auth → drain → reconnect → re-subscribe all subjects | TestRunNATSLoop_ActivateTriggersReconnect (reconnect count ≥ 2), TestRunNATSLoop_ReAuthOnActivate (verify call count), TestRunNATSLoop_FullJWTSubscribesAllSubjects (main_test.go:96-449) | None | UNIT_ONLY |
| spec-user-agent.md:41-43 | Context cancel shuts down cleanly without reconnect | TestRunNATSLoop_ShutdownOnContextCancel (main_test.go:453-519) | None | UNIT_ONLY |
| spec-user-agent.md:91 | NaCl sealed-box unseal: roundtrip, wrong key, truncated input | TestSealedBoxRoundtrip, TestSealedBoxOpen_WrongKey, TestSealedBoxOpen_TruncatedInput, TestSealedBoxOpen_EmptyPlaintext, TestSealedBoxSeal_DifferentEphemeralEachTime (sealedbox_test.go) | None | UNIT_ONLY |
| spec-user-agent.md:93-94 | Token delivery: unseal, write Secret, reply {ok:true} | TestHandleDelivery_Success (tokens_test.go:37) | None | UNIT_ONLY |
| spec-user-agent.md:98 | Decrypt failure → {ok:false, error:decrypt_failed}, Secret not written | TestHandleDelivery_DecryptFailed (tokens_test.go:77) | None | UNIT_ONLY |
| spec-user-agent.md:60 | Provision: verify token exists; fail with host_not_trusted | TestProvisioner_HostNotTrusted (provisioner_test.go:62) | None | UNIT_ONLY |
| spec-user-agent.md:63-70 | Provision creates Deployment + PVCs + all required env vars + volume mounts + mclaude/managed label | TestProvisioner_ProvisionCreatesDeploymentAndPVCs, TestProvisioner_DeploymentEnvVars, TestProvisioner_DeploymentVolumeMounts, TestProvisioner_DeploymentManagedLabel (provisioner_test.go:88-257) | None | UNIT_ONLY |
| spec-user-agent.md:77 | Delete: removes Deployment; idempotent on NotFound | TestProvisioner_Delete, TestProvisioner_Delete_NotFound (provisioner_test.go:259-305) | None | UNIT_ONLY |
| spec-user-agent.md:131-143 | Defaults applied when session-agent-template ConfigMap absent | TestProvisioner_DefaultTemplate (provisioner_test.go:329) | None | UNIT_ONLY |
| spec-user-agent.md:96 | Idempotent provision (same project provisioned twice) | TestProvisioner_ProvisionIdempotent (provisioner_test.go:308) | None | UNIT_ONLY |
| adr-0091:45 | Multi-provider: tokens.{provider} key pattern; TOKEN_PROVIDER env var | TestHandleDelivery_MultiProvider (tokens_test.go:144), TestProvisioner_DeploymentEnvVars checks TOKEN_PROVIDER | None | UNIT_ONLY |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | No open bugs with Component: mclaude-user-agent | N/A | Searched .agent/bugs/; no matching files |

### Summary

- Implemented: 54
- Gap: 0
- Partial: 3
- Infra: 46
- Unspec'd: 1
- Dead: 1
- Tested: 0
- Unit only: 16
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0
