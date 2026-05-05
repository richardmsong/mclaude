## Run: 2026-05-04T00:00:00Z

Component: mclaude-controller-k8s
Spec files: docs/mclaude-controller-k8s/spec-k8s-architecture.md, docs/adr-0091-per-user-anthropic-token.md, docs/spec-state-schema.md (Kubernetes Resources section)

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0091:274 | "Publishes grants on `mclaude.hosts.{hslug}.grants.create` when the host operator (or automated policy) decides to grant access to a user." | nats_subscriber.go:38-46, nats_subscriber.go:54-56, nats_subscriber.go:80-104 | IMPLEMENTED | | Subscribe + dispatch to handleGrantCreate |
| adr-0091:275 | "Generates NKey pair for user-agent → writes seed to `user-agent-key` k8s Secret → registers pubkey with CP via `mclaude.hosts.{hslug}.api.agents.user-agent.register`." | user_reconciler.go:482-541, user_reconciler.go:544-571 | IMPLEMENTED | | ensureUserAgentKey + registerUserAgentNKey |
| adr-0091:276 | "Creates an **empty** `user-tokens-{uslug}` Secret in the user namespace (no data keys; populated later by user-agent on token delivery). This ensures the user-agent pod's required volume mount resolves on startup." | user_reconciler.go:573-595 | IMPLEMENTED | | ensureUserTokensSecret; idempotent skip on existing |
| adr-0091:277 | "Copies `{release}-session-agent-template` ConfigMap from `mclaude-system` into the user namespace as `session-agent-template` so the user-agent can read pod-template values from its own namespace (no cross-namespace RBAC). Re-copied on Helm upgrade and user-agent re-rolled." | user_reconciler.go:407-444 | PARTIAL | SPEC→FIX | Copy on change: IMPLEMENTED. Re-roll of user-agent Deployment on template change: NOT implemented — spec says "re-rolled" but code only re-copies the ConfigMap and does not restart the user-agent Deployment. Spec should clarify that re-roll is a future enhancement or code should add rollout restart. |
| adr-0091:278 | "Creates user namespace `mclaude-{uslug}` + RBAC (ServiceAccount, namespace-scoped Role granting verbs on `Deployment`, `PersistentVolumeClaim`, `Secret` (`user-tokens-{uslug}` only), `ConfigMap` (`session-agent-template` only) for `mclaude-user-agent`) + deploys user-agent Deployment on grant." | user_reconciler.go:83-136, user_reconciler.go:235-295 | IMPLEMENTED | | Namespace, RBAC for both mclaude-sa and mclaude-user-agent, Deployment |
| adr-0091:279 | "**Session pod creation removed** — delegated to user-agent." | main.go:8-12 (comment), nats_subscriber.go:13-15 (comment) | IMPLEMENTED | | Code comment + no create/delete handlers in dispatcher |
| adr-0091:280 | "Removes all `devOAuthToken` / `oauth-token` writes from user-secrets reconciler paths." | user_reconciler.go:352-385 | IMPLEMENTED | | ensureUserSecrets creates empty Secret, actively removes stale oauth-token and nats-creds keys |
| adr-0091:46 | "`DEV_OAUTH_TOKEN` — Removed from mclaude-controller-k8s, CP chart, CI workflow" | main.go:52-53 (comment + code), ensureUserAgentDeployment env list | IMPLEMENTED | | Comment at line 53 notes removal; no DEV_OAUTH_TOKEN in env construction |
| spec-k8s:3 | "Per ADR-0091, its scope is **per-user namespace setup** — Namespace, RBAC, the `user-agent-key` Secret with the user-agent's NKey seed, and the user-agent Deployment itself." | user_reconciler.go:75-136 | IMPLEMENTED | | ReconcileUser covers exactly these resources |
| spec-k8s:3 | "It does **not** create per-project resources; the user-agent in each user namespace owns project-pod lifecycle, project PVCs, and the `user-tokens-{uslug}` Secret." | nats_subscriber.go:13-15 | IMPLEMENTED | | No create/delete project handlers; comment documents removal |
| spec-k8s:5 | "The controller connects directly to hub NATS via JWT credential (host-scoped per ADR-0054)." | main.go:117-121 | IMPLEMENTED | | nats.Connect with UserJWT(auth.JWTFunc(), auth.SignFunc()) |
| spec-k8s:9-12 | Removed: `MCProject` CRD reconciliation, per-project Deployments, per-project PVCs, `agent-nkey-{projectId}` Secret, session-agent pod creation | main.go:84-88; nats_subscriber.go:14-15 | IMPLEMENTED | | MCProject not in scheme; no CRD removal handlers |
| spec-k8s:12 | Removed: `DEV_OAUTH_TOKEN` env var, `oauth-token` field in `user-secrets` | user_reconciler.go:352-385 | IMPLEMENTED | | Stale key removal + no oauth-token in new creation |
| spec-k8s:14-18 | Added: Subscribe to `mclaude.hosts.{hslug}.grants.create` → spawns user-agent | nats_subscriber.go:38-46, nats_subscriber.go:54-56 | IMPLEMENTED | | Single wildcard subscription dispatched by suffix |
| spec-k8s:26-37 | Boot sequence: seed file → NewHostAuthFromSeed → Refresh() → decode hslug from JWT → connect NATS → subscribe → background refresh | main.go:59-157 | IMPLEMENTED | | All 7 boot steps present |
| spec-k8s:28 | "If CP returns 404 (NKey not registered), log registration instruction…retry every 5s with exponential backoff capped at 60s." | main.go:160-188 | IMPLEMENTED | | bootRefresh with hostauth.ErrNotRegistered, 5s initial, 2x backoff, 60s cap |
| spec-k8s:34 | "Decode the host slug (`hslug`) from the received JWT." | main.go:78-81, main.go:191-212 | IMPLEMENTED | | hostSlugFromJWT extracts "host-{hslug}" prefix |
| spec-k8s:71-80 | Configuration table: HUB_NATS_URL, CONTROL_PLANE_URL, HOST_NKEY_SEED_PATH, HELM_RELEASE_NAME, USER_AGENT_TEMPLATE_CM, METRICS_ADDR, HEALTH_PROBE_ADDR, LEADER_ELECTION | main.go:54-100 | PARTIAL | SPEC→FIX | HUB_NATS_URL, CONTROL_PLANE_URL, HOST_NKEY_SEED_PATH, HELM_RELEASE_NAME, METRICS_ADDR, HEALTH_PROBE_ADDR, LEADER_ELECTION: IMPLEMENTED. USER_AGENT_TEMPLATE_CM: spec says "Explicit name of the user-agent template ConfigMap. Overrides `HELM_RELEASE_NAME`-derived name." but code only uses `{release}-user-agent-template` derived from HELM_RELEASE_NAME; USER_AGENT_TEMPLATE_CM env is never read. Spec should either document this as not-yet-implemented or drop it. |
| spec-k8s:91-97 | NATS Subscriptions table: grants.create → per-user provisioning; users.*.projects.*.update → re-reconcile user-secrets; users.*.projects.*.create REMOVED; users.*.projects.*.delete REMOVED | nats_subscriber.go:38-75 | IMPLEMENTED | | dispatch() routes correctly; create/delete give debug log |
| spec-k8s:98 | "users.*.projects.*.update — Request/reply. Re-reconciles `user-secrets` for the project." | nats_subscriber.go:58-74, nats_subscriber.go:106-121 | IMPLEMENTED | | handleProjectsUpdate calls ReconcileSecretsForUser |
| spec-k8s:104-111 | Kubernetes Resources: namespace, user-agent-key Secret, user-secrets Secret (no oauth-token), user-config ConfigMap, user-agent Deployment + ServiceAccount + Role + RoleBinding | user_reconciler.go:75-136 | IMPLEMENTED | | All resources created in correct order |
| spec-k8s:108 | "Per-user `user-secrets` Secret (OAuth credential helpers — `gh-hosts.yml`, `glab-config.yml`, `conn-{id}-*`). **No `oauth-token` field** (removed per ADR-0091)." | user_reconciler.go:352-385 | IMPLEMENTED | | Creates empty Data map; stale keys removed |
| spec-k8s:109 | "Per-user `user-config` ConfigMap." | user_reconciler.go:387-405 | IMPLEMENTED | | ensureUserConfig |
| spec-k8s:109 | "Per-user `user-agent` Deployment + ServiceAccount `mclaude-user-agent` + Role permitting `Deployment` and `Secret` access within the user namespace + RoleBinding." | user_reconciler.go:255-295, user_reconciler.go:625-721 | IMPLEMENTED | | Role scoped to user-tokens-{uslug} Secret and session-agent-template ConfigMap |
| spec-k8s:113-128 | Reconciler Loop (per-user): 8 steps including load template, ensure namespace, RBAC, user-secrets, user-config, session-agent-template, user-agent-key, user-tokens, imagePullSecrets, Deployment | user_reconciler.go:75-136 | IMPLEMENTED | | All 8 steps present in ReconcileUser |
| spec-k8s:115 | "1. Load the user-agent template ConfigMap for image, resource limits, and ServiceAccount config." | user_reconciler.go:79-80, user_reconciler.go:147-198 | IMPLEMENTED | | loadUserAgentTemplate reads {release}-user-agent-template |
| spec-k8s:116 | "2. Ensure namespace `mclaude-{userSlug}` (ADR-0062: derived from userSlug, not UUID), labels `mclaude.io/user-id={userId}` (when CP supplies it; otherwise just `mclaude.io/managed=true`)." | user_reconciler.go:200-229 | PARTIAL | SPEC→FIX | `mclaude.io/managed=true` label: IMPLEMENTED. `mclaude.io/user-id={userId}` label: NOT implemented — grants.create payload only has uslug, not userId. Spec says "when CP supplies it" which is currently never — spec should document that userId is not in the grants.create payload and this label is deferred, or the payload and CP publisher must be updated to supply userId. |
| spec-k8s:119 | "The `mclaude-user-agent` Role grants verbs on `Deployment`, `PersistentVolumeClaim`, `Secret` (only `user-tokens-{uslug}`), and `ConfigMap` (only the local `session-agent-template`) within the user namespace." | user_reconciler.go:260-287 | IMPLEMENTED | | All four resource types with correct ResourceNames restrictions |
| spec-k8s:122 | "5b. Copy `{release}-session-agent-template` from `mclaude-system` into the user namespace as `session-agent-template`…On Helm upgrade where the source ConfigMap changes, controller re-copies into all user namespaces and re-rolls user-agent Deployments via `kubectl rollout restart`." | user_reconciler.go:407-444 | PARTIAL | SPEC→FIX | Per-user copy with update on change: IMPLEMENTED. "re-rolls user-agent Deployments" across ALL user namespaces on source ConfigMap change: NOT implemented — controller has no watch on the source ConfigMap and no mechanism to enumerate all user namespaces and restart their Deployments. Spec requires the spec to be tightened: this re-roll-all-users behavior is not implemented and has no code path. |
| spec-k8s:123 | "6. Ensure `user-agent-key` Secret. If absent or missing `nkey-seed`: generate NKey via `nkeys.CreateUser()`, write decorated seed to `nkey-seed` field, then NATS request `mclaude.hosts.{hslug}.api.agents.user-agent.register {uslug, nkey_public}` to CP. Idempotent — skip registration if Secret already has the seed." | user_reconciler.go:482-541 | IMPLEMENTED | | Exact logic: check for nkey-seed, skip if present, generate+register+write if absent |
| spec-k8s:124 | "6b. Ensure **empty** `user-tokens-{uslug}` Secret (no data keys yet)…Idempotent: if the Secret already exists (with or without populated keys), skip." | user_reconciler.go:573-595 | IMPLEMENTED | | ensureUserTokensSecret skips on any existing state |
| spec-k8s:125 | "7. Ensure `user-agent` Deployment (single replica, RollingUpdate). Pod mounts: `user-agent-key` Secret at `/secrets/agent/`, `user-tokens-{uslug}` Secret at `/secrets/tokens/`. Env: `USER_SLUG`, `HOST_SLUG`, `NAMESPACE`, `NATS_URL` (= `HUB_NATS_URL`), `CONTROL_PLANE_URL`." | user_reconciler.go:625-721 | IMPLEMENTED | | All mounts and env vars present |
| spec-k8s:126 | "8. Copy imagePullSecrets from the controller's namespace into the user namespace." | user_reconciler.go:597-623 | IMPLEMENTED | | copyImagePullSecrets copies dockerconfigjson Secrets |
| spec-k8s:132-134 | "X25519 keypair generation is performed by the user-agent on first startup, not by the controller — controller only handles the NATS NKey for authentication." | user_reconciler.go:482-541 (nkeys.CreateUser only) | IMPLEMENTED | | Controller only creates NKey (Ed25519), not X25519 |
| spec-k8s:136-142 | Corporate CA Support: label `mclaude.io/user-namespace: "true"`, inject `corporate-ca` volume + mount at `/etc/ssl/certs/corporate-ca-certificates.crt` + `NODE_EXTRA_CA_CERTS` env + `mclaude.io/ca-bundle-hash` annotation | user_reconciler.go:140-143, user_reconciler.go:208-210, user_reconciler.go:219-221 | PARTIAL | CODE→FIX | Namespace labeling for corporateCAEnabled: IMPLEMENTED. Injection of corporate-ca volume, volume mount, NODE_EXTRA_CA_CERTS env, and ca-bundle-hash annotation into the user-agent Deployment pod spec: NOT implemented — ensureUserAgentDeployment does not branch on tpl.corporateCAEnabled for these additions. |
| spec-k8s:144-145 | Liveness: controller connects to hub NATS → hub publishes $SYS.ACCOUNT.{accountKey}.CONNECT → CP updates hosts.last_seen_at + mclaude-hosts KV online=true. | main.go:117-124 (NATS connect) | IMPLEMENTED | | Liveness is handled by CP sys_subscriber — controller just connects, the presence event is automatic |
| spec-k8s:149-157 | Error Handling table: HOST_NKEY_SEED_PATH missing → fatal; CP 404 → retry 5s backoff; NATS unreachable → retry; NATS drops → reconnect; provisioning fails → retry on resync; agents.register FORBIDDEN → log bug | main.go:68-76 (seed load), main.go:160-188 (bootRefresh), main.go:117-124 (NATS reconnect), user_reconciler.go (no retry-on-provisioning-failure) | PARTIAL | SPEC→FIX | Fatal on missing seed: IMPLEMENTED. 404 retry: IMPLEMENTED. NATS reconnect: IMPLEMENTED via MaxReconnects(-1). Provisioning failure → retry on next resync: NOT implemented — there is no periodic resync; failures are logged and the next retry only happens if grants.create is re-published. Spec's "controller retries on next resync" implies a periodic resync loop that doesn't exist. Spec should drop this or document that retry only happens on re-delivery of grants.create. |
| spec-state-schema:509-512 | MCProject CRD: "Created by: `mclaude-controller-k8s`" (Kubernetes Resources section describes CRD as if still active) | main.go:84-88 | GAP | SPEC→FIX | spec-state-schema.md §"CRD: `MCProject`" and §"Deployment: `project-{projectId}`" still says "Created by: control-plane" and "Reconciler watches MCProject CRs" — but ADR-0091 removed MCProject from controller-k8s and project pod ownership moved to user-agent. The spec-state-schema.md Kubernetes Resources section still contains the old CRD block (lines 511-539) and old per-project PVC/Deployment descriptions written before ADR-0091. These sections have not been updated to reflect the removal. |
| spec-state-schema:543 | "Labels: `mclaude.io/user-id={userId}`, `mclaude.io/managed=true`" (Namespace description) | user_reconciler.go:217-221 | GAP | CODE→FIX | spec-state-schema.md specifies the namespace should carry `mclaude.io/user-id={userId}` label. Code only sets `mclaude.io/managed=true`. The grants.create payload (GrantCreatePayload) only contains USlug; no userId is available. Either the label must be dropped from the spec, the payload must be extended, or the controller must look up the user ID separately. |
| spec-state-schema:657-659 | "Volumes: `user-agent-key` Secret (mounted at `/secrets/agent/`), `user-tokens-{uslug}` Secret (mounted at `/secrets/tokens/` — write access via the k8s API, not the mount, since user-agent writes new keys after unsealing)" | user_reconciler.go:651-667, user_reconciler.go:680-686 | IMPLEMENTED | | Both volumes and mounts present |
| spec-state-schema:659 | "Container: user-agent image with env vars `USER_SLUG`, `HOST_SLUG`, `NAMESPACE`, `NATS_URL`, `CONTROL_PLANE_URL`" | user_reconciler.go:674-679 | IMPLEMENTED | | All five env vars present |
| spec-state-schema:659 | "ServiceAccount: `mclaude-user-agent` — Role in user namespace permits `get/list/create/update/patch` on `Secret` (`user-tokens-{uslug}` only)" | user_reconciler.go:274-279 | PARTIAL | SPEC→FIX | Code grants verbs: get, update, patch (3 verbs). Spec says get/list/create/update/patch (5 verbs). Specifically missing: `list` and `create` on the Secret. The user-agent writes tokens to this Secret (k8s API PATCH) — whether it needs `create` or `list` depends on user-agent implementation. Direction: spec needs to be checked against user-agent's actual required verbs. |
| adr-0091:86 | "Host controller generates NKey pair (Ed25519) for the user-agent. Writes NKey seed to k8s Secret `user-agent-key` in user namespace." | user_reconciler.go:495-535 | IMPLEMENTED | | nkeys.CreateUser() (Ed25519) + write to user-agent-key |
| adr-0091:86 | "Host controller registers user-agent's NKey public key with CP via `mclaude.hosts.{hslug}.api.agents.user-agent.register` with payload `{ "uslug": "alice", "nkey_public": "U..." }`" | user_reconciler.go:544-571 | IMPLEMENTED | | registerUserAgentNKey with correct subject and payload |
| adr-0091:87 | "Host controller creates user namespace `mclaude-{uslug}` + deploys user-agent pod." | user_reconciler.go:83-136 | IMPLEMENTED | | Full provisioning flow |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|----------------|-------------|
| main.go:1-13 | INFRA | Package doc comment + package declaration |
| main.go:15-36 | INFRA | Standard imports (context, encoding, errors, fmt, os, strings, time, nats, zerolog, controller-runtime, k8s, hostauth) |
| main.go:38-57 | INFRA | main(): logger setup, LOG_LEVEL env, DEV_OAUTH_TOKEN removal comment — all spec-covered boot sequence |
| main.go:59-81 | INFRA | hostauth setup, bootRefresh, hostSlugFromJWT — spec boot steps 1-4 |
| main.go:84-103 | INFRA | controller-runtime scheme + manager setup — spec boot step (no MCProject CRD) |
| main.go:106-111 | INFRA | Health + readiness check registration |
| main.go:113-157 | INFRA | Namespace detection, NATS connect, auth.StartRefreshLoop, UserReconciler + NATSSubscriber wiring, mgr.Start |
| main.go:160-188 | INFRA | bootRefresh: exponential backoff retry loop — spec error handling |
| main.go:191-212 | INFRA | hostSlugFromJWT: JWT base64 decode + claim name extraction |
| main.go:214-222 | INFRA | detectNamespace: reads pod service account namespace file |
| main.go:224-229 | INFRA | envOr: env-with-default helper |
| nats_subscriber.go:1-26 | INFRA | Package doc + imports |
| nats_subscriber.go:28-34 | INFRA | NATSSubscriber struct definition |
| nats_subscriber.go:36-47 | INFRA | StartNATSSubscriber: single host-scoped subscription |
| nats_subscriber.go:49-75 | INFRA | dispatch: subject routing switch — grants.create + projects.update + default debug log |
| nats_subscriber.go:77-104 | INFRA | handleGrantCreate: payload parse + ReconcileUser call |
| nats_subscriber.go:106-121 | INFRA | handleProjectsUpdate: ReconcileSecretsForUser + reply |
| nats_subscriber.go:123-132 | INFRA | matchesProjectsUpdate: subject pattern matcher |
| nats_subscriber.go:134-142 | INFRA | extractProjectsUSlug: slug extractor |
| nats_subscriber.go:144-155 | INFRA | replyOK: NATS request/reply helper |
| nats_subscriber.go:157-168 | INFRA | replyError: NATS request/reply error helper |
| user_reconciler.go:1-33 | INFRA | Package doc + imports |
| user_reconciler.go:35-39 | INFRA | natsConn interface for test injection |
| user_reconciler.go:41-52 | INFRA | UserReconciler struct |
| user_reconciler.go:54-58 | INFRA | GrantCreatePayload struct |
| user_reconciler.go:60-71 | INFRA | userAgentRegisterRequest + userAgentRegisterReply structs |
| user_reconciler.go:73-137 | INFRA | ReconcileUser: the main provisioning sequence |
| user_reconciler.go:139-198 | INFRA | userAgentTpl, loadUserAgentTemplate, defaultUserAgentTemplate, applyDefaultUserAgentResources — template loading helpers |
| user_reconciler.go:200-229 | INFRA | ensureNamespace |
| user_reconciler.go:231-296 | INFRA | ensureRBAC, ensureServiceAccount, ensureRole, ensureRoleBinding |
| user_reconciler.go:352-385 | INFRA | ensureUserSecrets with stale key cleanup |
| user_reconciler.go:387-405 | INFRA | ensureUserConfig |
| user_reconciler.go:407-476 | INFRA | ensureSessionAgentTemplate + configMapDataEqual + configMapBinaryDataEqual |
| user_reconciler.go:478-541 | INFRA | ensureUserAgentKey |
| user_reconciler.go:543-571 | INFRA | registerUserAgentNKey |
| user_reconciler.go:573-595 | INFRA | ensureUserTokensSecret |
| user_reconciler.go:597-623 | INFRA | copyImagePullSecrets |
| user_reconciler.go:625-721 | INFRA | ensureUserAgentDeployment |
| user_reconciler.go:723-728 | INFRA | ReconcileSecretsForUser: helper called by projects.update handler |
| nkeys.go:1-8 | INFRA | Placeholder file explaining JWT removal — comment-only, no code |


### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0091:274 | Subscribe to grants.create | controller_test.go:1047-1055 (TestSingleNATSSubscription), TestMatchesProjectsUpdate | None found | UNIT_ONLY |
| adr-0091:275 | NKey generation + registration via user-agent.register | controller_test.go:547-589 (TestEnsureUserAgentKey_NATSRegistration), TestEnsureUserAgentKey_CreatesSecretWithSeed, TestEnsureUserAgentKey_Idempotent | None found | UNIT_ONLY |
| adr-0091:276 | Empty user-tokens-{uslug} Secret | controller_test.go:592-651 (TestEnsureUserTokensSecret_CreatedEmpty, TestEnsureUserTokensSecret_IdempotentWithPopulatedKeys) | None found | UNIT_ONLY |
| adr-0091:277 | session-agent-template copy | controller_test.go:810-895 (TestEnsureSessionAgentTemplate_Copies, UpdatesOnChange, NoSourceIsNoop) | None found | UNIT_ONLY |
| adr-0091:278 | Namespace + RBAC + user-agent Deployment on grant | controller_test.go:943-1019 (TestReconcileUser_AllResourcesCreated), TestEnsureRBAC_BothServiceAccountsCreated, TestEnsureRBAC_UserAgentRoleHasCorrectVerbs, TestEnsureRBAC_RoleBindingsCreated | None found | UNIT_ONLY |
| adr-0091:279 | Session pod creation removed | controller_test.go:1039-1055 (TestNoDEVOAuthTokenInCode, TestSingleNATSSubscription — compile-time) | None found | UNIT_ONLY |
| adr-0091:280 | oauth-token removed from user-secrets | controller_test.go:397-462 (TestEnsureUserSecrets_CreatedWithNoOAuthToken, TestEnsureUserSecrets_RemovesStaleOAuthToken) | None found | UNIT_ONLY |
| adr-0091:46 | DEV_OAUTH_TOKEN removed | controller_test.go:783-787 (in TestEnsureUserAgentDeployment_EnvVars) | None found | UNIT_ONLY |
| spec-k8s:28 | 404 retry with exponential backoff | None found | None found | UNTESTED |
| spec-k8s:71-80 | Configuration env vars | controller_test.go:740-787 (TestEnsureUserAgentDeployment_EnvVars) — partial (checks user-agent env, not startup env parsing) | None found | UNIT_ONLY |
| spec-k8s:98 | projects.update → re-reconcile user-secrets | controller_test.go:133-168 (TestMatchesProjectsUpdate, TestExtractProjectsUSlug) | None found | UNIT_ONLY |
| spec-k8s:136-142 | Corporate CA support | controller_test.go:213-229 (TestEnsureNamespace_CorporateCALabel) | None found | UNIT_ONLY |
| spec-k8s:125 | Deployment volume mounts + env | controller_test.go:688-737 (TestEnsureUserAgentDeployment_VolumeMounts), 740-787 (TestEnsureUserAgentDeployment_EnvVars) | None found | UNIT_ONLY |


### Phase 4 — Bug Triage

No open bugs with `**Component**: mclaude-controller-k8s` found in `.agent/bugs/`.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| — | — | — | No bugs matching this component |

### Summary

- Implemented: 28
- Gap: 2
- Partial: 5
- Infra: 40
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 13
- E2E only: 0
- Untested: 1
- Bugs fixed: 0
- Bugs open: 0
