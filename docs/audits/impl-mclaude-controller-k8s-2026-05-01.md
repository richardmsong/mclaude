## Run: 2026-05-01T00:00:00Z

Component: mclaude-controller-k8s
Source directory: /Users/rsong/work/mclaude/mclaude-controller-k8s/
Spec files: docs/mclaude-controller/spec-controller.md, docs/spec-state-schema.md
ADRs evaluated: ADR-0062 (accepted), ADR-0040 (implemented), ADR-0050 (implemented), ADR-0043 (accepted), ADR-0061 (accepted), ADR-0035 (implemented), ADR-0042 (implemented)

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-controller:Deployment | "A single Deployment in the worker cluster's mclaude-system namespace. Built with kubebuilder (controller-runtime). Leader election enabled" | main.go:66-72 (ctrl.NewManager with LeaderElection:true) | IMPLEMENTED | — | Manager created with scheme, metrics, health, leader election. |
| spec-controller:Deployment | "The cluster's slug is configured at deploy time via the Helm value clusterSlug … and is required" | main.go:39-42 (CLUSTER_SLUG check, fatal on empty) | IMPLEMENTED | — | Fatal on missing CLUSTER_SLUG. |
| spec-controller:Config:CLUSTER_SLUG | "Used to build the wildcard NATS subscription" | nats_subscriber.go:69,76 (used in subject patterns) | IMPLEMENTED | — | |
| spec-controller:Config:NATS_URL | "Worker NATS service URL" | main.go:37 (envOr "NATS_URL") | IMPLEMENTED | — | |
| spec-controller:Config:NATS_ACCOUNT_SEED | "Account NKey seed. The controller generates its own ephemeral user JWT signed by this key" | main.go:48-51 (loadAccountKey), main.go:106-110 (generateNATSUserCreds) | IMPLEMENTED | — | |
| spec-controller:Config:NATS_CREDENTIALS_PATH | "Injected by Helm but not read by the controller binary" | — | IMPLEMENTED | — | Not read, as spec says. No code references it. |
| spec-controller:Config:JS_DOMAIN | "Injected by Helm but not yet read by the controller binary" | — | IMPLEMENTED | — | Not read, as spec says. |
| spec-controller:Config:HELM_RELEASE_NAME | "Used to locate the session-agent-template ConfigMap (default mclaude-worker)" | main.go:43 (envOr default "mclaude") | GAP | SPEC→FIX | Code defaults to "mclaude", spec says default "mclaude-worker". This is an intentional override via SESSION_AGENT_TEMPLATE_CM env. The spec default is misleading; the actual default is "mclaude" + "-session-agent-template". |
| spec-controller:Config:SESSION_AGENT_TEMPLATE_CM | "Explicit name of the session-agent-template ConfigMap. Overrides the HELM_RELEASE_NAME-derived name" | main.go:44 (envOr SESSION_AGENT_TEMPLATE_CM) | IMPLEMENTED | — | |
| spec-controller:Config:SESSION_AGENT_NATS_URL | "NATS URL injected into session-agent pods as NATS_URL. Defaults to the FQDN-qualified worker NATS URL" | main.go:84-85 (envOr with sessionAgentNATSURL fallback) | IMPLEMENTED | — | |
| spec-controller:Config:DEV_OAUTH_TOKEN | "When set, the reconciler injects it as oauth-token in per-user user-secrets Secret" | main.go:45, reconciler.go:192-194 (devOAuthToken check + set) | IMPLEMENTED | — | |
| spec-controller:Config:METRICS_ADDR | "Prometheus metrics listen address (default :8082)" | main.go:67 (envOr default ":8082") | IMPLEMENTED | — | |
| spec-controller:Config:HEALTH_PROBE_ADDR | "Health/readiness probe listen address (default :8081)" | main.go:68 (envOr default ":8081") | IMPLEMENTED | — | |
| spec-controller:Config:LEADER_ELECTION | "Injected by Helm as true but not yet read by the controller binary" | main.go:66 (LeaderElection: true, hardcoded) | GAP | SPEC→FIX | Spec says "not yet read by the controller binary" but code hardcodes LeaderElection:true. Spec is stale — leader election IS configured. |
| spec-controller:Config:LOG_LEVEL | "Injected by Helm but not yet read by the controller binary" | main.go:29-33 (reads LOG_LEVEL, parses zerolog level) | GAP | SPEC→FIX | Spec says "not yet read" but code reads LOG_LEVEL and configures zerolog. Spec is stale. |
| spec-controller:Config:LEADER_ELECTION_NAMESPACE | "Defaults to mclaude-system. Not yet implemented" | main.go:64-65 (envOr LEADER_ELECTION_NAMESPACE default "mclaude-system") | GAP | SPEC→FIX | Spec says "not yet implemented" but code reads env var and passes to Manager. Spec is stale. |
| spec-controller:NATS:ADR-0054-create | "mclaude.hosts.{CLUSTER_SLUG}.users.*.projects.*.create — Request/reply. CP-initiated fan-out provisioning" | nats_subscriber.go:76 (subscribes mclaude.hosts.{clusterSlug}.>), nats_subscriber.go:105-111 (handles create) | IMPLEMENTED | — | Host-scoped pattern subscription covers create. |
| spec-controller:NATS:ADR-0054-delete | "mclaude.hosts.{CLUSTER_SLUG}.users.*.projects.*.delete — Request/reply. Tears down the MCProject CR" | nats_subscriber.go:76, nats_subscriber.go:121-128 (handles delete) | IMPLEMENTED | — | |
| spec-controller:NATS:legacy-provision | "mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.provision — Request/reply. Legacy provisioning" | nats_subscriber.go:69 (subscribes legacy pattern), nats_subscriber.go:105-106 (switch handles "provision") | IMPLEMENTED | — | |
| spec-controller:NATS:legacy-create | "mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.create — Request/reply. Identical to provision" | nats_subscriber.go:69,105 | IMPLEMENTED | — | |
| spec-controller:NATS:legacy-update | "mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.update — Request/reply. Reconciles per-user user-secrets Secret. Not yet implemented." | nats_subscriber.go:113-120 (handleUpdate implemented) | GAP | SPEC→FIX | Spec says "Not yet implemented" but code has a working handleUpdate handler that reconciles secrets. Spec is stale. |
| spec-controller:NATS:legacy-delete | "mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.delete" | nats_subscriber.go:69,121-128 | IMPLEMENTED | — | |
| spec-controller:K8s:CRD | "CRD MCProject (mcprojects.mclaude.io/v1alpha1)" | mcproject_types.go:18-20 (SchemeGroupVersion) | IMPLEMENTED | — | |
| spec-controller:K8s:Namespace | "Per-user namespace mclaude-{userSlug} (ADR-0062)" | reconciler.go:79 (userNs := "mclaude-" + mcp.Spec.UserSlug) | IMPLEMENTED | — | ADR-0062 compliant. |
| spec-controller:K8s:Namespace-labels | "with correct labels (including mclaude.io/user-namespace: true when corporate CA is enabled)" | reconciler.go:112-115 (corporateCAEnabled label), reconciler.go:102-106 (user-id, managed labels) | IMPLEMENTED | — | |
| spec-controller:K8s:ADR-0062-migration | "On migration, the controller creates the new slug-named namespace if it does not exist; the old UUID-named namespace (mclaude-{userId}) is left for manual cleanup" | reconciler.go:96-126 (reconcileNamespace creates slug-named NS) | PARTIAL | SPEC→FIX | Controller creates slug-named namespace but does NOT check for old UUID-named namespace existence. The spec says "the controller creates the new slug-named namespace if it does not exist" which IS implemented. The "old UUID-named namespace is left for manual cleanup" part is passive — no code needed. However, the ADR-0062 decision says "Controller checks for old UUID-named namespace; if it exists and new slug-named namespace does not, it creates the new namespace". The active check for the old namespace is missing. |
| spec-controller:K8s:RBAC | "Ensures RBAC resources (ServiceAccount, Role, RoleBinding)" | reconciler.go:128-152 (reconcileRBAC) | IMPLEMENTED | — | SA=mclaude-sa, Role=mclaude-role, RoleBinding=mclaude-role. |
| spec-controller:K8s:RBAC-role-rules | "Role: mclaude-role — allows get/watch/patch on ConfigMap user-config and get on Secret user-secrets" | reconciler.go:137-140 (PolicyRules match) | IMPLEMENTED | — | |
| spec-controller:K8s:user-config | "Ensures the user-config ConfigMap" | reconciler.go:157-161 (reconcileSecrets creates user-config CM) | IMPLEMENTED | — | |
| spec-controller:K8s:user-secrets | "Ensures user-secrets Secret. NATS credentials in the Secret are session-agent JWTs minted by the controller via IssueSessionAgentJWT" | reconciler.go:163-212 (reconcileSecrets handles user-secrets) | IMPLEMENTED | — | |
| spec-controller:K8s:imagePullSecrets | "Copies imagePullSecrets from the controller's namespace" | reconciler.go:214-229 (copies DockerConfigJson secrets) | IMPLEMENTED | — | |
| spec-controller:K8s:project-PVC | "Ensures the project PVC project-{projectId}" | reconciler.go:281-282 (ensurePVCCR "project-"+projectID) | IMPLEMENTED | — | |
| spec-controller:K8s:nix-PVC | "Ensures the Nix PVC nix-{projectId}" | reconciler.go:284-285 (ensurePVCCR "nix-"+projectID) | IMPLEMENTED | — | |
| spec-controller:K8s:Deployment-strategy | "Ensures the session-agent Deployment with Recreate strategy" | reconciler.go:293,300 (RecreateDeploymentStrategyType) | IMPLEMENTED | — | Both create and update paths. |
| spec-controller:K8s:pod-env | "Pod env vars include USER_ID, USER_SLUG, HOST_SLUG, PROJECT_ID, PROJECT_SLUG" | reconciler.go:244-254 (env vars in buildPodTemplate) | IMPLEMENTED | — | All five present. |
| spec-controller:K8s:HOST_SLUG-source | "HOST_SLUG always equals CLUSTER_SLUG for cluster-managed pods" | reconciler.go:253 ({Name: "HOST_SLUG", Value: tpl.hostSlug}), reconciler.go:372,393 (tpl.hostSlug = r.clusterSlug) | IMPLEMENTED | — | |
| spec-controller:K8s:template-watch | "session-agent-template ConfigMap … changes re-enqueue all MCProject CRs" | reconciler.go:345-370 (Watches ConfigMap with EnqueueRequestsFromMapFunc) | IMPLEMENTED | — | Filtered by name+namespace predicate. |
| spec-controller:K8s:full-pod-template-rebuild | "On every update reconcile, the full pod template (env vars, image, volumes, imagePullSecrets, annotations) is rebuilt and applied" | reconciler.go:290-293 (existing.Spec.Template = r.buildPodTemplate) | IMPLEMENTED | — | |
| spec-controller:K8s:status-phases | "Updates MCProject status: phase (Pending → Provisioning → Ready or Failed)" | reconciler.go:73-78 (Pending transition), reconciler.go:79-82 (Provisioning), reconciler.go:90-95 (Ready), mcproject_types.go:62-67 (constants) | IMPLEMENTED | — | |
| spec-controller:K8s:status-conditions | "conditions (NamespaceReady, RBACReady, SecretsReady, DeploymentReady)" | reconciler.go:84-93 (updateCondition calls), mcproject_types.go:53-58 (constants) | IMPLEMENTED | — | |
| spec-controller:corporate-ca:label | "Adds label mclaude.io/user-namespace: true to user namespaces" | reconciler.go:104-106,113-115 | IMPLEMENTED | — | |
| spec-controller:corporate-ca:volume | "Injects a corporate-ca volume, volume mount at /etc/ssl/certs/corporate-ca-certificates.crt, and NODE_EXTRA_CA_CERTS env var" | reconciler.go:264-278 (CA volume, mount, env) | IMPLEMENTED | — | |
| spec-controller:corporate-ca:hash | "Annotates the pod template with mclaude.io/ca-bundle-hash (SHA-256 of the ConfigMap data)" | reconciler.go:279 (annotations set), reconciler.go:322-339 (reconcilerCAConfigMapHash) | IMPLEMENTED | — | |
| spec-controller:Auth | "Both controller variants authenticate to NATS via JWT signed by the deployment-level account key" | main.go:106-145 (generateNATSUserCreds), main.go:99-107 (nats.Connect with UserJWT) | IMPLEMENTED | — | |
| spec-controller:Auth:JWT-scope | "mclaude-controller-k8s: mclaude.users.*.hosts.{cluster-slug}.>" | main.go:127-137 (Pub.Allow/Sub.Allow with both legacy and host-scoped patterns) | IMPLEMENTED | — | Dual subscription per ADR-0061. |
| spec-controller:provisioning-request-shape | "{userID, userSlug, hostSlug, projectID, projectSlug, gitUrl, gitIdentityId}" | nats_subscriber.go:15-23 (ProvisionRequest struct) | IMPLEMENTED | — | All fields present. |
| spec-controller:provisioning-reply-success | "{ok: true, projectSlug}" | nats_subscriber.go:26-30 (ProvisionReply struct) | IMPLEMENTED | — | |
| spec-controller:provisioning-reply-failure | "{ok: false, error, code}" | nats_subscriber.go:26-30 (ProvisionReply struct) | IMPLEMENTED | — | |
| spec-controller:error:delete-not-found | "Delete request without matching project — Idempotent: reply {ok: true}" | nats_subscriber.go:160-162 (IsNotFound returns nil → replyOK) | IMPLEMENTED | — | |
| spec-controller:error:provision-fail | "Provision request: MCProject reconcile fails before Ready — Reply {ok: false, error, code}" | nats_subscriber.go:109-112 (replyError on handleCreate failure) | IMPLEMENTED | — | |
| ADR-0062:namespace-format | "K8s namespace format: mclaude-{userSlug} instead of mclaude-{userId}" | reconciler.go:79 (userNs := "mclaude-" + mcp.Spec.UserSlug) | IMPLEMENTED | — | |
| ADR-0062:MCProject-CR-naming | "{uslug}-{pslug}" | nats_subscriber.go:133 (crName := fmt.Sprintf("%s-%s", req.UserSlug, req.ProjectSlug)) | IMPLEMENTED | — | |
| ADR-0062:PVC-naming | "Keep project-{projectId} and nix-{projectId} (UUIDs)" | reconciler.go:281-285 | IMPLEMENTED | — | UUIDs preserved for PVCs. |
| ADR-0062:controller-uses-UserSlug | "reconcileNamespace() uses mcp.Spec.UserSlug instead of mcp.Spec.UserID" | reconciler.go:79 | IMPLEMENTED | — | |
| ADR-0062:integration-test:namespace-uses-slug | "Controller creates namespace mclaude-dev-mclaude-local (not UUID)" | controller_test.go:237-280 (TestADR0062_NamespaceUsesSlug) | IMPLEMENTED | — | Test verifies slug-based ns created, UUID-based ns NOT created. |
| ADR-0062:integration-test:user-slug-env | "Pod env var USER_SLUG=dev-mclaude-local" | controller_test.go:283-325 (TestADR0062_UserSlugEnvVar) | IMPLEMENTED | — | Test verifies USER_SLUG env matches slug not UUID. |
| spec-state-schema:MCProject-CRD:name | "Name: {userSlug}-{projectSlug}" | nats_subscriber.go:133 | IMPLEMENTED | — | |
| spec-state-schema:MCProject-CRD:scope | "Scope: Namespaced (in mclaude-system)" | nats_subscriber.go:139 (Namespace: p.controlPlaneNs) | IMPLEMENTED | — | |
| spec-state-schema:MCProject-CRD:spec | "spec: userId, projectId, userSlug, projectSlug, gitUrl, gitIdentityId" | mcproject_types.go:39-50 | IMPLEMENTED | — | All fields present in MCProjectSpec. |
| spec-state-schema:MCProject-CRD:status | "status: phase, userNamespace, conditions, lastReconciledAt" | mcproject_types.go:68-73 | IMPLEMENTED | — | |
| spec-state-schema:MCProject-CRD:userSlug-required | "present in CRD schema but not in required list; should be required" | mcproject_types.go:39-50 | PARTIAL | SPEC→FIX | The Go struct has all fields but there's no validation enforcing required. The spec notes this as a known gap ("should be required"). |
| spec-state-schema:Namespace | "Namespace: mclaude-{userSlug}. Labels: mclaude.io/user-id={userId}, mclaude.io/managed=true" | reconciler.go:100-106,110-116 | IMPLEMENTED | — | |
| spec-state-schema:Secret:user-secrets | "nats-creds, oauth-token, gh-hosts.yml, glab-config.yml, conn-{id}-token, conn-{id}-refresh-token, conn-{id}-username" | reconciler.go:163-212 | PARTIAL | CODE→FIX | Controller only manages nats-creds and oauth-token. gh-hosts.yml, glab-config.yml, and conn-{id}-* keys are described as written by "control-plane OAuth callback + PAT handler + reconcileUserCLIConfig" — not by this controller. The spec says "Writers: mclaude-controller-k8s (reconcileSecrets), control-plane...". The controller only handles its subset. This is correct — spec lists multiple writers. |
| spec-state-schema:ConfigMap:user-config | "Contents: Claude Code workspace settings, hooks, seed configuration" | reconciler.go:157-161 (creates empty ConfigMap) | PARTIAL | CODE→FIX | Controller creates an empty ConfigMap, not seeded from Helm template as spec states ("seeded from Helm template"). |
| spec-state-schema:ConfigMap:session-agent-template | "Watched: {release}-session-agent-template ConfigMap in mclaude-system — changes re-enqueue all MCProject CRs" | reconciler.go:345-370 | IMPLEMENTED | — | |
| spec-state-schema:PVC:project | "project-{projectId} in mclaude-{userSlug}" | reconciler.go:281-282 | IMPLEMENTED | — | |
| spec-state-schema:PVC:nix | "nix-{projectId} in mclaude-{userSlug}" | reconciler.go:284-285 | IMPLEMENTED | — | |
| spec-state-schema:Deployment | "Deployment: project-{projectId} in mclaude-{userSlug}. Replicas: 1. Strategy: Recreate" | reconciler.go:296-302 | IMPLEMENTED | — | |
| spec-state-schema:Deployment:volumes | "Volumes: project PVC (project-data), nix PVC, user-config ConfigMap, user-secrets Secret, claude-home emptyDir" | reconciler.go:258-264 | IMPLEMENTED | — | All five volumes present. |
| spec-state-schema:Deployment:env | "USER_ID, PROJECT_ID (UUIDs), USER_SLUG, PROJECT_SLUG, HOST_SLUG (slugs)" | reconciler.go:244-254 | IMPLEMENTED | — | |
| spec-state-schema:Deployment:CLAUDE_CODE_TMPDIR | "specified in the spec target but not yet injected" | reconciler.go:256 ({Name: "CLAUDE_CODE_TMPDIR", Value: "/data/claude-tmp"}) | GAP | SPEC→FIX | Spec says "not yet injected" but code injects it. Spec is stale. |
| spec-state-schema:RBAC | "ServiceAccount: mclaude-sa, Role: mclaude-role, RoleBinding: mclaude-role" | reconciler.go:128-152 | IMPLEMENTED | — | |
| spec-state-schema:NATS-subjects:host-scoped-create | "mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create — Publisher: control-plane, Subscriber: host controller" | nats_subscriber.go:76 (subscribes mclaude.hosts.{clusterSlug}.>) | IMPLEMENTED | — | |
| spec-state-schema:NATS-subjects:host-scoped-delete | "mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.delete" | nats_subscriber.go:76 | IMPLEMENTED | — | |
| ADR-0043:Recreate-strategy | "Deployment strategy set to Recreate. Both create and update branches" | reconciler.go:293,300 | IMPLEMENTED | — | |
| ADR-0043:ConfigMap-watch | "Add a watch on the session-agent-template ConfigMap to SetupWithManager" | reconciler.go:345-370 | IMPLEMENTED | — | |
| ADR-0043:CLAUDE_CODE_TMPDIR | "CLAUDE_CODE_TMPDIR=/data/claude-tmp env var; PVC subPath mount" | reconciler.go:256 (env), reconciler.go:270-271 (mount) | IMPLEMENTED | — | |
| ADR-0061:dual-subscription | "K8s controller subscribes to host-scoped subjects alongside existing user-scoped" | nats_subscriber.go:69,76 (both subscriptions), main.go:127-134 (JWT permissions for both) | IMPLEMENTED | — | |
| ADR-0040:controller-runtime-logger | "Call ctrl.SetLogger before ctrl.NewManager" | main.go:25 (ctrl.SetLogger(zap.New())) | IMPLEMENTED | — | |
| ADR-0040:NATS-JWT-auth | "Generate a user JWT from the account seed, connect with nats.UserJWT()" | main.go:99-107 | IMPLEMENTED | — | |
| spec-controller:NATS:JWT-permissions:$SYS | "Sub.Allow: $SYS.ACCOUNT.*.CONNECT, $SYS.ACCOUNT.*.DISCONNECT" (spec-state-schema host controller JWT, lines 670-674) | main.go:133-134 (Pub.Allow only), main.go:138-141 (Sub.Allow lacks $SYS) | GAP | UNCLEAR | The spec-state-schema host/controller JWT Sub.Allow includes $SYS.ACCOUNT.*.CONNECT/DISCONNECT. Code puts these in Pub.Allow but NOT Sub.Allow. The spec-state-schema section is for BYOH host controllers, not K8s. The K8s controller self-issues its own JWT with broader permissions. $SYS entries in Pub.Allow are likely unnecessary (NATS server publishes $SYS, clients subscribe). Spec-controller.md says K8s controller doesn't subscribe to $SYS — liveness is via leaf-link CONNECT observed by CP. Code has $SYS in wrong permission direction. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-16 | INFRA | Package declaration, imports |
| main.go:21-25 | INFRA | Logger setup (ctrl.SetLogger, zerolog init) — covered by ADR-0040 |
| main.go:147-157 | INFRA | detectNamespace() — reads SA namespace, falls back to mclaude-system. Standard k8s pattern. |
| main.go:160-173 | INFRA | sessionAgentNATSURL() — derives FQDN NATS URL for pods in other namespaces. Helper for SESSION_AGENT_NATS_URL default. |
| main.go:175-180 | INFRA | envOr() helper |
| reconciler.go:1-32 | INFRA | Package declaration, imports |
| reconciler.go:34-46 | INFRA | MCProjectReconciler struct definition — fields map to spec config variables |
| reconciler.go:232-280 | INFRA | buildPodTemplate() — the pod template construction is spec'd behavior, not just infra. Already covered in Phase 1. |
| reconciler.go:322-339 | INFRA | reconcilerCAConfigMapHash() — SHA256 hash helper for corporate CA annotation. Covered by corporate CA spec. |
| reconciler.go:341-370 | INFRA | SetupWithManager — controller-runtime registration. Covered by spec (template watch). |
| reconciler.go:373-409 | INFRA | sessionAgentTpl struct, defaultTemplate(), applyDefaultResources() — template parsing and defaults. Infrastructure supporting spec'd ConfigMap loading. |
| reconciler.go:310-320 | INFRA | ensurePVCCR — PVC creation helper. Behavior spec'd in Phase 1. |
| reconciler.go:322-339 | INFRA | reconcilerCAConfigMapHash — hash helper. |
| reconciler.go:341-370 | INFRA | SetupWithManager — controller-runtime wiring. |
| nats_subscriber.go:1-14 | INFRA | Package declaration, imports |
| nats_subscriber.go:15-31 | INFRA | ProvisionRequest/ProvisionReply struct definitions — covered by spec (provisioning request shape) |
| nats_subscriber.go:33-42 | INFRA | NATSProvisioner struct — fields covered by spec (NATS subscriptions section) |
| nats_subscriber.go:168-191 | INFRA | replyOK, replyError, extractOperation — helpers for spec'd request/reply behavior |
| nkeys.go:1-9 | INFRA | Package, imports |
| nkeys.go:11-28 | UNSPEC'd | SessionAgentSubjectPermissions() — returns overly broad permissions including `mclaude.{UUID}.>` (pre-ADR-0054 legacy). Also includes `$JS.API.>`, `$JS.*.API.>`, `$JS.ACK.>`, `$JS.FC.>`, `$JS.API.DIRECT.GET.>` which are broader than the per-project scoping in spec-state-schema session-agent JWT section. The spec-state-schema specifies per-project scoped permissions; this code returns user-wide permissions. |
| nkeys.go:30-58 | INFRA | IssueSessionAgentJWT() — JWT issuance function. Behavior covered by spec (NATS credentials in user-secrets Secret). |
| mcproject_types.go:1-14 | INFRA | Package declaration, imports, SchemeGroupVersion |
| mcproject_types.go:16-96 | INFRA | CRD type definitions — covered by spec (MCProject CRD section) |
| reconciler.go:155-230 | INFRA | reconcileSecrets — covered by spec in Phase 1 (user-secrets, user-config, imagePullSecrets) |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-controller:Deployment | "Leader election enabled" | — | — | UNTESTED | No test verifies manager creation with leader election. |
| spec-controller:Config:CLUSTER_SLUG | "required, fatal on empty" | — | — | UNTESTED | No test for fatal on missing env. |
| spec-controller:NATS:ADR-0054-create | "host-scoped provisioning" | TestExtractOperation (controller_test.go:197-219) | — | UNIT_ONLY | extractOperation tested; no integration test with real NATS + K8s. ADR-0062 lists integration test "Namespace uses slug" — exercised by unit test only. |
| spec-controller:NATS:legacy-provision | "legacy provisioning" | TestExtractOperation | — | UNIT_ONLY | |
| ADR-0062:namespace-format | "mclaude-{userSlug}" | TestADR0062_NamespaceUsesSlug (controller_test.go:237-280) | — | UNIT_ONLY | Unit test with fake client. ADR-0062 integration test case "Namespace uses slug — Controller creates namespace mclaude-dev-mclaude-local (not UUID)" — no real K8s cluster test. |
| ADR-0062:user-slug-env | "USER_SLUG=dev-mclaude-local" | TestADR0062_UserSlugEnvVar (controller_test.go:283-325) | — | UNIT_ONLY | ADR-0062 integration test case "Session agent receives correct USER_SLUG" — unit test only. |
| spec-controller:K8s:status-phases | "Pending → Provisioning → Ready" | TestGap3_PendingPhaseTransition (controller_test.go:76-106) | — | UNIT_ONLY | |
| spec-controller:Auth:JWT-scope | "Dual subscription permissions" | TestGap2_JWTPermissionScoping (controller_test.go:109-156) | — | UNIT_ONLY | Verifies JWT claims match expected permissions. No integration test with real NATS operator-mode JWT enforcement. |
| ADR-0043:CLAUDE_CODE_TMPDIR | "env var + PVC subPath mount" | TestGap4_ClaudeCodeTmpDir (controller_test.go:159-194) | — | UNIT_ONLY | |
| spec-controller:K8s:RBAC | "SA, Role, RoleBinding" | TestGap7_MultiOwner (controller_test.go:199-268) | — | UNIT_ONLY | Tests multi-owner pattern, not RBAC creation directly. |
| spec-controller:corporate-ca | "corporate CA volume, mount, env, hash" | — | — | UNTESTED | No test for corporate CA injection. |
| spec-controller:error:delete-not-found | "Idempotent delete" | — | — | UNTESTED | No test for idempotent delete. |
| spec-controller:provisioning-request-shape | "request/reply payload" | — | — | UNTESTED | No test for NATS message handling with full ProvisionRequest. |
| spec-controller:K8s:user-secrets | "NATS creds, oauth-token" | — | — | UNTESTED | No test for secret creation or update. |
| spec-controller:K8s:imagePullSecrets | "Copies from controller namespace" | — | — | UNTESTED | No test for imagePullSecret copying. |
| ADR-0061:dual-subscription | "host-scoped + legacy" | TestGap2_JWTPermissionScoping (JWT perms), TestExtractOperation (subject parsing) | — | UNIT_ONLY | No integration test with real NATS verifying message delivery on both patterns. |
| spec-controller:Config:SESSION_AGENT_NATS_URL | "FQDN derivation" | TestSessionAgentNATSURL (controller_test.go:328-343) | — | UNIT_ONLY | |
| spec-controller:Config:LOG_LEVEL | "LOG_LEVEL parsing" | TestGap9_LogLevelParsing (controller_test.go:222-235) | — | UNIT_ONLY | |
| spec-controller:K8s:template-watch | "ConfigMap watch re-enqueues MCProjects" | — | — | UNTESTED | No test for ConfigMap watch triggering re-enqueue. |

### Phase 4 — Bug Triage

No bugs in `.agent/bugs/` reference `mclaude-controller-k8s` as their component. No triage items.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No bugs filed against this component. |

### Summary

- Implemented: 40
- Gap: 5
- Partial: 3
- Infra: 22
- Unspec'd: 1
- Dead: 0
- Tested (unit+integration): 0
- Unit only: 10
- E2E only: 0
- Untested: 9
- Bugs fixed: 0
- Bugs open: 0

**Gaps (all SPEC→FIX unless noted):**

1. GAP [SPEC→FIX]: spec-controller Config table says HELM_RELEASE_NAME default is "mclaude-worker" — code defaults to "mclaude". Spec default is misleading.
2. GAP [SPEC→FIX]: spec-controller Config table says LEADER_ELECTION "not yet read by the controller binary" — code hardcodes LeaderElection:true. Spec is stale.
3. GAP [SPEC→FIX]: spec-controller Config table says LOG_LEVEL "not yet read by the controller binary" — code reads LOG_LEVEL and configures zerolog. Spec is stale.
4. GAP [SPEC→FIX]: spec-controller Config table says LEADER_ELECTION_NAMESPACE "Not yet implemented" — code reads env var and passes to Manager. Spec is stale.
5. GAP [SPEC→FIX]: spec-controller NATS table says legacy update "Not yet implemented" — code has handleUpdate. Spec is stale.
6. GAP [UNCLEAR]: $SYS.ACCOUNT.*.CONNECT/DISCONNECT in Pub.Allow instead of Sub.Allow. K8s controller doesn't need $SYS, but code has them in wrong direction.
7. GAP [SPEC→FIX]: spec-state-schema says CLAUDE_CODE_TMPDIR "not yet injected" — code injects it. Spec is stale.

**Partial items:**

1. PARTIAL [SPEC→FIX]: ADR-0062 migration — spec says "controller checks for old UUID-named namespace; if it exists..." but code does not perform this active check. The passive behavior (creating slug-named NS) IS implemented.
2. PARTIAL [CODE→FIX]: user-config ConfigMap created empty — spec says "seeded from Helm template". Controller creates empty; no seed data.
3. PARTIAL [SPEC→FIX]: MCProject CRD userSlug/projectSlug "should be required" per spec — Go struct has fields but no required validation.

**Unspec'd code:**

1. UNSPEC'd: nkeys.go:11-28 — SessionAgentSubjectPermissions returns user-wide permissions (mclaude.{UUID}.>, broad $JS.*.API.>, $KV.mclaude-sessions-{uslug}.>) instead of per-project scoped permissions defined in spec-state-schema session-agent JWT section. This is a legacy pre-ADR-0054 implementation that has not been narrowed to per-project scope.
