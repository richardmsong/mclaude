# Implementation Audit: mclaude-controller-k8s — ADR-0063

**Component**: mclaude-controller-k8s
**Component root**: mclaude-controller-k8s/
**Spec files**: docs/adr-0063-k8s-architecture-spec.md, docs/mclaude-controller-k8s/spec-k8s-architecture.md
**ADR status**: accepted
**Date**: 2026-05-01

## Run: 2026-05-01T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-k8s-architecture.md:4 | Controller connects directly to hub NATS via JWT credential (host-scoped per ADR-0054). There is no local NATS StatefulSet. | main.go:134-141; nats_subscriber.go:61-68 | IMPLEMENTED | — | Hub NATS connect via host JWT; no StatefulSet in chart. |
| spec-k8s-architecture.md:8 | Single Deployment in `mclaude-system` on the worker cluster. Built with kubebuilder (controller-runtime). | main.go:95-105; controller-deployment.yaml | IMPLEMENTED | — | Manager uses controller-runtime. Deployment in mclaude-system namespace. |
| spec-k8s-architecture.md:9 | Leader election flag configurable via `LEADER_ELECTION` env var. v1 runs a single replica. | main.go:99; values.yaml:62 | IMPLEMENTED | — | `envOr("LEADER_ELECTION", "false")` at main.go:99; replicas:1 in values.yaml |
| spec-k8s-architecture.md:13 | Boot step 1: Read the NKey seed from the file at `HOST_NKEY_SEED_PATH` (default `/etc/mclaude/host-creds/nkey_seed`). | main.go:53; hostauth/host_auth.go:80-98 | IMPLEMENTED | — | `envOr("HOST_NKEY_SEED_PATH", "/etc/mclaude/host-creds/nkey_seed")` + `NewHostAuthFromSeed` |
| spec-k8s-architecture.md:14 | Boot step 2: Call `NewHostAuthFromSeed(seedPath, cpURL, log)` from `mclaude.io/common/pkg/hostauth`. | main.go:66; hostauth/host_auth.go:80-98 | IMPLEMENTED | — | Package imported correctly; constructor exists. |
| spec-k8s-architecture.md:15-20 | Boot step 3: Call `Refresh()` to perform HTTP challenge-response. If CP returns 404, log registration instruction and retry every 5s with exponential backoff capped at 60s. | main.go:74,172-199; hostauth/host_auth.go:140-178 | IMPLEMENTED | — | `bootRefresh` at main.go:172-199 implements the exponential backoff (5s initial, doubling, 60s cap). |
| spec-k8s-architecture.md:21 | Boot step 4: Decode the host slug (`hslug`) from the received JWT. Use this slug for all subsequent NATS subscriptions — do not read `host.name` from Helm. | main.go:76-80; main.go:201-224 | IMPLEMENTED | — | `hostSlugFromJWT` decodes from JWT `name` field. `clusterSlug` assigned from decoded value. |
| spec-k8s-architecture.md:22 | Boot step 5: Connect to hub NATS at `HUB_NATS_URL` using the host JWT. | main.go:134-141 | IMPLEMENTED | — | `nats.Connect(hubNATSURL, nats.UserJWT(auth.JWTFunc(), auth.SignFunc()), ...)` |
| spec-k8s-architecture.md:23 | Boot step 6: Subscribe to `mclaude.hosts.{hslug}.users.*.projects.*.create` and `mclaude.hosts.{hslug}.users.*.projects.*.delete`. | nats_subscriber.go:61-68 | PARTIAL | SPEC→FIX | Code subscribes to single wildcard `mclaude.hosts.{hslug}.>`, then dispatches by subject suffix. Spec §Boot says two specific patterns; spec §NATS Subscriptions (line 79) says single `mclaude.hosts.{hslug}.>`. Code correctly matches §NATS Subscriptions. The §Boot description is overly specific and should be updated to match. |
| spec-k8s-architecture.md:24 | Boot step 7: Background loop refreshes the host JWT before TTL expiry (5-minute TTL) via the same challenge-response code path. | main.go:148; hostauth/host_auth.go:187-210 | IMPLEMENTED | — | `auth.StartRefreshLoop(ctx)` at main.go:148; refresh fires at TTL-60s. |
| spec-k8s-architecture.md:28-40 | Registration Flow: gen-host-nkey pre-install Job (weight -10) generates NKey pair, writes decorated seed to Secret `{release}-host-creds` field `nkey_seed`, prints public key to Job log and Helm NOTES.txt. | charts/mclaude-worker/templates/gen-host-nkey-job.yaml; charts/mclaude-worker/templates/NOTES.txt | IMPLEMENTED | — | Job weight -10 on Job, -20 on RBAC. Seed written to `{release}-host-creds`. NOTES.txt rendered correctly. |
| spec-k8s-architecture.md:44-54 | Deregistration Flow: Option A (operator workstation), Option B (Helm pre-delete Job) deferred to V1. | spec (doc only; no code change required) | IMPLEMENTED | — | Spec states Option B is deferred. ADR matches. No code gap. |
| spec-k8s-architecture.md:59-68 | Configuration table: `HUB_NATS_URL` (required), `CONTROL_PLANE_URL` (required), `HOST_NKEY_SEED_PATH`, `HELM_RELEASE_NAME`, `SESSION_AGENT_TEMPLATE_CM`, `DEV_OAUTH_TOKEN`, `METRICS_ADDR`, `HEALTH_PROBE_ADDR`, `LEADER_ELECTION`. | main.go:48-57; main.go:93-102 | IMPLEMENTED | — | All env vars present. |
| spec-k8s-architecture.md:70-75 | Dropped env vars: `NATS_ACCOUNT_SEED`, `NATS_CREDENTIALS_PATH`, `JS_DOMAIN`, `CLUSTER_SLUG`, `SESSION_AGENT_NATS_URL` / `NATS_URL`. | main.go (no references to these); controller-deployment.yaml (no occurrences) | IMPLEMENTED | — | None of the dropped vars appear in main.go or controller-deployment.yaml. |
| spec-k8s-architecture.md:79 | NATS Subscriptions: subscribes via host JWT to single wildcard `mclaude.hosts.{hslug}.>` (zero JetStream access). | nats_subscriber.go:61-68 | IMPLEMENTED | — | Single subscribe to `mclaude.hosts.{hslug}.>`. |
| spec-k8s-architecture.md:81-85 | Subject dispatch table: `users.*.projects.*.create`, `users.*.projects.*.delete`, `users.*.projects.*.update`. | nats_subscriber.go:92-128 | IMPLEMENTED | — | `extractOperation` dispatches on last token; create/delete/update all handled. |
| spec-k8s-architecture.md:90 | CRD `MCProject` (`mcprojects.mclaude.io/v1alpha1`) in `mclaude-system`. | mcproject_types.go:14-18 | IMPLEMENTED | — | `SchemeGroupVersion{Group:"mclaude.io", Version:"v1alpha1"}`, `Kind:"MCProject"`. |
| spec-k8s-architecture.md:91 | Per-user namespace `mclaude-{userSlug}` (ADR-0062) with ServiceAccount, Role, RoleBinding. | reconciler.go:89; reconciler.go:147-177; reconciler.go:180-213 | IMPLEMENTED | — | `userNs := "mclaude-" + mcp.Spec.UserSlug`; RBAC ensured per reconcile cycle. |
| spec-k8s-architecture.md:92 | Per-user `user-secrets` Secret (OAuth tokens, CLI configs — not NATS credentials). | reconciler.go:216-296 | IMPLEMENTED | — | `user-secrets` created with `oauth-token` only; `nats-creds` explicitly removed. |
| spec-k8s-architecture.md:93 | Per-user `user-config` ConfigMap. | reconciler.go:217-225 | IMPLEMENTED | — | `user-config` ConfigMap created via `ensureOwned`. |
| spec-k8s-architecture.md:94 | Per-project `project-{projectId}` and `nix-{projectId}` PVCs. | reconciler.go:440-445 | IMPLEMENTED | — | Both PVCs created in `reconcileDeployment`. |
| spec-k8s-architecture.md:95 | Per-project `project-{projectId}` Deployment with `ownerReferences` back to the MCProject CR. | reconciler.go:460-480 | IMPLEMENTED | — | `ctrlutil.SetControllerReference(mcp, deploy, r.scheme)` at reconciler.go:473. |
| spec-k8s-architecture.md:97 | Watched: `{release}-session-agent-template` ConfigMap in `mclaude-system` — changes re-enqueue all MCProject CRs. | reconciler.go:757-790 | IMPLEMENTED | — | `Watches` on ConfigMap with predicate filter + EnqueueRequestsFromMapFunc lists all MCProjects. |
| spec-k8s-architecture.md:99 | All Deployments, PVCs, and Secrets carry `ownerReferences` back to MCProject CR via `controllerutil.SetControllerReference`. | reconciler.go:473; reconciler.go:641-643 | IMPLEMENTED | — | Deployment and PVCs use SetControllerReference. Secrets via ensureOwned/addOwnerIfMissing. |
| spec-k8s-architecture.md:103-113 | Reconciler Loop steps 1-7 (load template, ensure namespace, RBAC, secrets, agent NKey, deployment, update status). | reconciler.go:62-145 | IMPLEMENTED | — | All steps present in Reconcile(). agentNKey (step 6b) at reconciler.go:121-124. |
| spec-k8s-architecture.md:111 | Step 6b: Ensures per-project `agent-nkey-{projectId}` Secret: generates NKey pair, writes decorated seed, registers public key with CP via NATS `agents.register`, sets `ownerReferences` back to MCProject. Idempotent — skips NATS registration if Secret already exists. | reconciler.go:503-579 | IMPLEMENTED | — | `reconcileAgentNKey` implements all of this. Idempotent check at line 519. |
| spec-k8s-architecture.md:112 | Step 7: Ensures session-agent Deployment with pod env vars: `USER_ID`, `USER_SLUG`, `HOST_SLUG`, `PROJECT_ID`, `PROJECT_SLUG`, `NATS_URL`, `CONTROL_PLANE_URL`. Pod mounts `agent-nkey-{projectId}` Secret at `/etc/mclaude/agent-nkey/`. | reconciler.go:302-411 | IMPLEMENTED | — | All env vars injected; agent-nkey volume+mount at reconciler.go:353-366. |
| spec-k8s-architecture.md:113 | Step 8: Updates MCProject status: phase (Pending → Provisioning → Ready or Failed) and conditions. | reconciler.go:77-87; reconciler.go:130-144 | IMPLEMENTED | — | Phase transitions Pending→Provisioning→Ready/Failed; conditions set per step. |
| spec-k8s-architecture.md:117-123 | Session-Agent Auth: controller generates NKey pair, stores decorated seed in `agent-nkey-{projectId}` Secret, registers public key with CP via NATS `mclaude.hosts.{hslug}.api.agents.register`. | reconciler.go:503-616 | IMPLEMENTED | — | Full flow implemented. NATS request to correct subject at reconciler.go:586. |
| spec-k8s-architecture.md:125 | The `nats-creds` key is not written to `user-secrets`. The per-project `agent-nkey-{projectId}` Secret carries `ownerReferences` back to MCProject CR. | reconciler.go:228-244; reconciler.go:560-562 | IMPLEMENTED | — | `nats-creds` removed if present; SetControllerReference on agent-nkey Secret. |
| spec-k8s-architecture.md:129-133 | Corporate CA Support: label `mclaude.io/user-namespace: "true"` on namespaces, corporate-ca volume/mount/env var, `mclaude.io/ca-bundle-hash` annotation. | reconciler.go:156-170; reconciler.go:371-390 | IMPLEMENTED | — | Label applied when `corporateCAEnabled`. Volume, mount, NODE_EXTRA_CA_CERTS, and annotation all injected. |
| spec-k8s-architecture.md:136 | Liveness: `$SYS.ACCOUNT.{accountKey}.CONNECT` with `client.kind = "Client"`. CP looks up host by `public_key` (no type filter). | (control-plane code, not controller-k8s) | IMPLEMENTED | — | Out of scope for this component. Cross-cutting spec (spec-state-schema.md:433) matches. |
| spec-k8s-architecture.md:141-149 | Error Handling table: seed missing (fatal), CP 404 (retry loop), hub unreachable at boot (NATS retry), hub drops at runtime (reconnect), reconcile fails (Failed phase), namespace fails (Failed phase). | main.go:66-68; main.go:172-199; hostauth/host_auth.go:228-229 | IMPLEMENTED | — | All error cases handled. |
| spec-k8s-architecture.md:155-157 | Helm Chart Artifacts: Secret `{release}-host-creds` field `nkey_seed`. JWT is NOT stored here. | charts/mclaude-worker/templates/host-creds-secret.yaml | IMPLEMENTED | — | Secret has `nkey_seed` field; no JWT field. |
| spec-k8s-architecture.md:161-166 | Dependencies: Hub NATS, CP HTTP, K8s API, session-agent-template ConfigMap, `mclaude.io/common/pkg/hostauth`, optional trust-manager. | main.go:29; go.mod | IMPLEMENTED | — | All imports present. |
| ADR-0063:43 | Worker chart: Drop NATS StatefulSet/ConfigMap/Service, drop gen-leaf-creds-job, drop leafUrl/leafCreds/nats.* values. | charts/mclaude-worker/values.yaml | IMPLEMENTED | — | No `leafUrl`, `leafCreds`, `nats.*` keys. No NATS StatefulSet/ConfigMap/Service templates. |
| ADR-0063:124-130 | Helm artifacts: SA/Role/RoleBinding `{release}-gen-host-nkey` (weight -20), Job `{release}-gen-host-nkey` (weight -10), Secret `{release}-host-creds`, Deployment `{release}-controller`, NOTES.txt. | charts/mclaude-worker/templates/ | IMPLEMENTED | — | All Helm artifacts present and match the table. |
| ADR-0063:127 | Controller Deployment env: `HUB_NATS_URL`, `CONTROL_PLANE_URL`, `HOST_NKEY_SEED_PATH`. `host.name` rendered into NOTES.txt only. | templates/controller-deployment.yaml:35-40 | IMPLEMENTED | — | Env vars match spec exactly. |
| ADR-0063:127 | Deployment mounts `{release}-host-creds` as volume at `/etc/mclaude/host-creds/`. | templates/controller-deployment.yaml:55-78 | IMPLEMENTED | — | Volume `host-creds` mounted readOnly at `/etc/mclaude/host-creds`. |
| ADR-0063:144 | Drop env vars: `NATS_ACCOUNT_SEED`, `NATS_CREDENTIALS_PATH`, `JS_DOMAIN`, `CLUSTER_SLUG`. | templates/controller-deployment.yaml; main.go | IMPLEMENTED | — | None of these env vars appear in controller Deployment or main.go. |
| ADR-0063:147-148 | Drop functions: `loadAccountKey()`, `IssueSessionAgentJWT()`, `accountKP` field. | nkeys.go; reconciler.go:37-53 | IMPLEMENTED | — | `nkeys.go` is an empty placeholder. No `accountKP` field on MCProjectReconciler. |
| ADR-0063:149-163 | `CONTROL_PLANE_URL` injection into session-agent pods via `controlPlaneURL` reconciler struct field (not from session-agent-template ConfigMap). | reconciler.go:46-48; reconciler.go:327-329 | IMPLEMENTED | — | `controlPlaneURL string` field; injected as `CONTROL_PLANE_URL` env var at line 328. |
| ADR-0063:150-154 | Move `host_auth.go` to `mclaude-common/pkg/hostauth/`. Add `NewHostAuthFromSeed` constructor. | mclaude-common/pkg/hostauth/host_auth.go | IMPLEMENTED | — | Package exists; both constructors present. |
| ADR-0063:155-161 | Boot loop: On 404, log registration instruction and return retryable ErrNotRegistered. Retry 5s/doubling/60s cap. Do not connect to NATS until JWT acquired. | main.go:172-199; hostauth/host_auth.go:150-158 | IMPLEMENTED | — | `bootRefresh` implements correct backoff. NATS connect happens after `hostJWT` returned. |
| ADR-0063:162 | Host slug derived from the JWT, not from a Helm value. | main.go:76-80; main.go:201-224 | IMPLEMENTED | — | `hostSlugFromJWT` decodes from JWT `name` field `"host-{hslug}"`. |
| ADR-0063:163 | Drop `sessionAgentNATSURL()` derivation function. Session-agents inherit `HUB_NATS_URL` directly. | reconciler.go:123; main.go:123 | IMPLEMENTED | — | `sessionAgentNATSURL` field set to `hubNATSURL` directly. No derivation function. |
| ADR-0063:164 | Owner references on Deployment, PVC, Secret → MCProject CR using `controllerutil.SetControllerReference`. | reconciler.go:473; reconciler.go:641-643; reconciler.go:560-562 | IMPLEMENTED | — | All three resource types have SetControllerReference calls. |
| ADR-0063:209-218 | Data Model: hosts CHECK constraint must be dropped. No new tables/KV/subjects. | (DB migration, not controller-k8s code) | IMPLEMENTED | — | Out of scope for this component. |
| spec-k8s-architecture.md:83 | `users.*.projects.*.update` subject: Re-reconciles user-secrets for the project. Triggers `reconcileSecrets`. | nats_subscriber.go:105-113; nats_subscriber.go:201-222 | IMPLEMENTED | — | `handleUpdate` calls `reconciler.reconcileSecrets`. |
| ADR-0063 §Helm artifacts | `gen-host-nkey` Job: generates NKey pair via `nkeys.CreateUser()`, writes decorated seed to `{release}-host-creds`. Idempotent — skips if Secret exists. | gen-host-nkey-job.yaml:89 | PARTIAL | SPEC→FIX | Job runs `control-plane gen-host-nkey` command (control-plane binary subcommand, using controlPlane.image). Spec implies the NKey generation is a direct library call `nkeys.CreateUser()` — it is, but inside the CP binary's subcommand, not a standalone tool. The idempotency and key generation are functionally correct. Spec should clarify that the Job container uses a CP binary subcommand, not a dedicated NKey tool. |
| spec-state-schema.md:375 | `mclaude.hosts.{hslug}.api.agents.register` payload: `{userSlug, hostSlug, projectSlug, nkey_public}` | reconciler.go:485-490 | GAP | CODE→FIX | Controller sends `json:"userSlug"`, `json:"hostSlug"`, `json:"projectSlug"` (camelCase). The control-plane `AgentRegisterRequest` struct (lifecycle.go:146-150) uses `json:"user_slug"` and `json:"project_slug"` (snake_case). Result: CP receives empty `user_slug` and `project_slug` → validation fails → agents cannot register their NKey. Also: controller sends an extra `hostSlug` field not expected by CP (CP extracts host slug from the subject). Fix: change `agentRegisterRequest` JSON tags to `json:"user_slug"`, `json:"project_slug"` and remove `HostSlug` field (or change to `json:"host_slug"` to match any future CP use). Also update spec-state-schema.md:375 to use snake_case notation. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-8 | INFRA | Package declaration and ADR comment header. |
| main.go:10-30 | INFRA | Standard imports — all used by the boot sequence and manager setup. |
| main.go:32-44 | INFRA | Logger setup (zerolog). Spec says "controller connects to hub NATS" — logging is implied infrastructure. |
| main.go:93-94 | INFRA | `leaderElectionNs` from env — infrastructure for the leader election option; spec mentions leader election. |
| main.go:226-241 | INFRA | `detectNamespace()` reads K8s service account namespace file for controlPlaneNs detection; `envOr()` helper. Both are plumbing required by spec'd boot sequence. |
| reconciler.go:1-34 | INFRA | Package declaration, imports — all used by spec'd reconciler methods. |
| reconciler.go:55-59 | INFRA | `natsConn` interface definition for testability — necessary infrastructure for the spec'd NATS registration call with mock support. |
| reconciler.go:299-411 | INFRA | `buildPodTemplate()` — fully spec'd (env vars, volumes, mounts). Internal helper, not a separate spec item. |
| reconciler.go:414-435 | INFRA | `reconcilerCAConfigMapHash()` — hash computation for CA bundle change detection. Spec mentions `mclaude.io/ca-bundle-hash` annotation on pod template; this is the implementation. |
| reconciler.go:498-501 | INFRA | `agentNKeySecretName()` helper — returns `"agent-nkey-" + projectID`. Direct implementation of spec'd name `agent-nkey-{projectId}`. |
| reconciler.go:618-680 | INFRA | `ensurePVCCR()`, `ensureOwned()`, `addOwnerIfMissing()` — helper functions for spec'd owner references and resource creation with idempotency. |
| reconciler.go:682-728 | INFRA | `loadTemplate()` — reads session-agent-template ConfigMap, spec'd as step 1 of reconciler loop. Defaults handling is necessary infrastructure. |
| reconciler.go:730-791 | INFRA | `setPhase()`, `updateCondition()`, `setCondition()`, `SetupWithManager()` — all required by spec'd reconciler loop and manager setup. |
| reconciler.go:793-835 | INFRA | `sessionAgentTpl` struct, `defaultTemplate()`, `applyDefaultResources()` — template parsing infrastructure for spec'd reconciler. |
| nkeys.go:1-9 | INFRA | Empty placeholder file; comment explains ADR-0063 removed all JWT issuance functions. No dead code — the file exists to document the deliberate removal. |
| nats_subscriber.go:1-8 | INFRA | Package declaration + header comment. |
| nats_subscriber.go:24-51 | INFRA | `ProvisionRequest`, `ProvisionReply`, `NATSProvisioner` struct — data types for spec'd NATS request/reply. |
| nats_subscriber.go:249-276 | INFRA | `replyOK()`, `replyError()`, `extractOperation()` — helpers for spec'd request/reply pattern. |
| mcproject_types.go:1-137 | INFRA | CRD type definitions for MCProject, MCProjectSpec, MCProjectStatus, MCProjectPhase — all spec'd in §Kubernetes Resources. |
| hostauth/host_auth.go:22-27 | INFRA | JWT TTL constants (5 min, 60s buffer) — spec says "5-minute TTL" and "refresh before TTL expiry". |
| hostauth/host_auth.go:101-110 | INFRA | `PublicKey()` — returns public key string. Used by boot sequence to log the pubkey before challenge-response. |
| hostauth/host_auth.go:212-288 | INFRA | `requestChallenge()`, `verifyChallenge()` — HTTP implementation of the spec'd challenge-response protocol (`POST /api/auth/challenge` + `POST /api/auth/verify`). |
| charts/mclaude-worker/templates/_helpers.tpl | INFRA | Helm helper functions (fullname, namespace, labels, etc.) — standard boilerplate for the spec'd chart. |
| charts/mclaude-worker/templates/clusterrole.yaml | INFRA | ClusterRole granting controller permission to manage Namespace, Deployment, PVC, Secret, MCProject CRs. Spec says "ClusterRole/ClusterRoleBinding `{release}-controller` — lets the controller manage Namespace, Deployment, PVC, Secret, MCProject CRs." |
| charts/mclaude-worker/templates/clusterrolebinding.yaml | INFRA | ClusterRoleBinding — pairs with ClusterRole above. |
| charts/mclaude-worker/templates/serviceaccount.yaml | INFRA | ServiceAccount for the controller Deployment — standard Helm artifact for any controller. |
| charts/mclaude-worker/templates/controller-service.yaml | INFRA | Service for metrics scraping — spec mentions `METRICS_ADDR` for Prometheus. |
| charts/mclaude-worker/templates/mcproject-crd.yaml | INFRA | CRD definition for MCProject — required for spec'd reconciler to function. |
| charts/mclaude-worker/templates/session-agent-template.yaml | INFRA | ConfigMap `{release}-session-agent-template` — spec'd in §Kubernetes Resources as `{release}-session-agent-template` ConfigMap. |
| charts/mclaude-worker/values-*.yaml | INFRA | Environment-specific value overrides (dev, airgap, aks, k3d) — operational infrastructure. |
| charts/mclaude-worker/templates/tests/test-smoke.yaml | INFRA | Helm test job for smoke testing. Not a spec gap — test infrastructure. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-k8s-architecture.md:13-24 | Boot sequence: seed load, NewHostAuthFromSeed, Refresh, hslug decode, NATS connect, subscribe, refresh loop. | TestADR0063_HostSlugFromJWT, TestNewHostAuthFromSeed_Valid, TestRefresh_FullFlow | None | UNIT_ONLY | Boot sequence is split across hostauth tests (unit, using httptest server) and controller_test.go. No integration test exercises the full boot against a real CP + real NATS. |
| spec-k8s-architecture.md:15-20 | CP 404 → ErrNotRegistered, retry every 5s with exponential backoff capped at 60s. | TestRefresh_404ReturnsErrNotRegistered | None | UNIT_ONLY | Unit test verifies ErrNotRegistered is returned. No integration test for the retry loop with a real CP. |
| spec-k8s-architecture.md:21 | Host slug decoded from JWT, not from Helm. | TestADR0063_HostSlugFromJWT | None | UNIT_ONLY | Multiple edge cases covered in unit test. No integration test. |
| spec-k8s-architecture.md:23 | Single wildcard subscribe to `mclaude.hosts.{hslug}.>`. | TestADR0063_SingleSubscription | None | UNIT_ONLY | Unit test verifies subject format. No integration test with real NATS. |
| spec-k8s-architecture.md:79-85 | Subject dispatch: create/delete/update operations. | TestExtractOperation | None | UNIT_ONLY | `extractOperation` tested in isolation. No integration test exercising the full NATS dispatch path. |
| spec-k8s-architecture.md:91 | Namespace `mclaude-{userSlug}` created by reconciler. | TestADR0062_NamespaceUsesSlug, TestADR0062_UserSlugEnvVar | None | UNIT_ONLY | Uses controller-runtime fake client. No integration test with real K8s cluster. |
| spec-k8s-architecture.md:92 | `user-secrets` does not contain `nats-creds`. Stale `nats-creds` removed. | TestADR0063_NoNATSCredsInSecrets, TestADR0063_StaleNATSCredsRemoved | None | UNIT_ONLY | Fake client tests. No integration test. |
| spec-k8s-architecture.md:95,99 | Deployment + PVCs with `ownerReferences` back to MCProject. | TestGap3_PendingPhaseTransition (indirectly) | None | UNIT_ONLY | Owner ref logic tested via ensureOwned/TestGap7_MultiOwner. Deployment owner ref not explicitly tested in isolation but covered by the reconcile flow tests. |
| spec-k8s-architecture.md:99 | Multiple MCProjects can share resources; ensureOwned adds additional owner references. | TestGap7_MultiOwner | None | UNIT_ONLY | Unit test verifies idempotent multi-owner behavior. |
| spec-k8s-architecture.md:103-113 | Reconciler Loop steps 1-8: phase transitions, namespace, RBAC, secrets, agentNKey, deployment, status. | TestGap3_PendingPhaseTransition, TestADR0062_NamespaceUsesSlug | None | UNIT_ONLY | Phase transition and namespace creation tested. Full integration through fake client only. |
| spec-k8s-architecture.md:111 | `reconcileAgentNKey`: NKey generation, Secret creation, NATS registration, idempotency, ownerReferences. | TestADR0063_AgentNKeySecretCreated, TestADR0063_AgentNKeySecretIdempotent, TestADR0063_AgentNKeyNATSRegistration, TestADR0063_AgentNKeyOwnerReference | None | UNIT_ONLY | Comprehensive unit test coverage with fake NATS connection. No integration test with real NATS + real CP. |
| spec-k8s-architecture.md:112 | Pod env vars: `CONTROL_PLANE_URL`, `CLAUDE_CODE_TMPDIR`, `AGENT_NKEY_PATH`, agent-nkey volume mount. | TestADR0063_ControlPlaneURLInjected, TestGap4_ClaudeCodeTmpDir, TestADR0063_AgentNKeyMountedInPodTemplate | None | UNIT_ONLY | All pod template fields tested in unit tests. |
| spec-k8s-architecture.md:117-123 | Session-Agent Auth: NATS subject `mclaude.hosts.{hslug}.api.agents.register`, correct payload fields. | TestADR0063_AgentNKeyNATSRegistration, TestADR0063_AgentNKeySecretName | None | UNIT_ONLY | Unit test verifies subject and payload. **Note: GAP found in payload — see Phase 1 row for spec-state-schema.md:375.** |
| spec-k8s-architecture.md:129-133 | Corporate CA support: namespace label, volume, mount, annotation. | (no dedicated test) | None | UNTESTED | `corporateCAEnabled` code path exists in `buildPodTemplate` and `reconcileNamespace` but no unit test exercises the corporate CA injection path. |
| hostauth/host_auth.go:80-99 | `NewHostAuthFromSeed`: reads seed file, derives key pair, JWT starts empty. | TestNewHostAuthFromSeed_Valid, TestNewHostAuthFromSeed_InvalidPath, TestNewHostAuthFromSeed_InvalidContent | None | UNIT_ONLY | |
| hostauth/host_auth.go:140-178 | `Refresh`: challenge-response flow, ErrNotRegistered on 404, JWT stored after success, signature correctness. | TestRefresh_FullFlow, TestRefresh_UpdatesStoredJWT, TestRefresh_ErrorWhenNoCPURL, TestRefresh_404ReturnsErrNotRegistered, TestRefresh_SignatureCorrectness | None | UNIT_ONLY | httptest server used — not a real CP with JWT enforcement. |
| hostauth/host_auth.go:187-210 | `StartRefreshLoop`: background JWT refresh before TTL expiry. | (no dedicated test) | None | UNTESTED | `StartRefreshLoop` is not directly tested. The goroutine behavior and ticker firing are not covered. |

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` reference `mclaude-controller-k8s` as their Component.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No open bugs target this component. |

### Summary

- Implemented: 46
- Gap: 1
- Partial: 2
- Infra: 30
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 14
- E2E only: 0
- Untested: 2
- Bugs fixed: 0
- Bugs open: 0
