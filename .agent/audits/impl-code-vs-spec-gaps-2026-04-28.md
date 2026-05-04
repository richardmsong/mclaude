# Code vs Spec Gap Audit — ADR-0046 through ADR-0050 Components

Date: 2026-04-28

---

## Control-Plane Gaps

### GAP: Project creation still uses NATS subscriber, not HTTP-only
Location: `mclaude-control-plane/projects.go` → `StartProjectsSubscriber()` → `docs/mclaude-control-plane/spec-control-plane.md`
Detail: Code subscribes to `mclaude.users.*.api.projects.create` (a user-scoped NATS subject without host segment) for project creation. The SPA publishes to this subject (confirmed in `mclaude-web/src/lib/subj.ts` `subjProjectsCreate`). The spec explicitly states: "There is no `mclaude.*.api.projects.create` subscriber on control-plane any more — projects are created via HTTP (`POST /api/users/{uslug}/projects`)". The NATS-based project creation path is fully functional in code but the spec says it doesn't exist.

### GAP: Project KV key uses UUIDs, not slugs
Location: `mclaude-control-plane/projects.go` → `writeProjectKV()` line `kv.Put(userID+"."+proj.ID, val)` → `docs/spec-state-schema.md` (mclaude-projects section)
Detail: Code writes project KV entries with key `{userID}.{projectID}` (UUID-based, two segments). Spec says key format is `{uslug}.{hslug}.{pslug}` (slug-based, three segments, host-scoped). The code comment acknowledges this: "key migration to {uslug}.{hslug}.{pslug} is a separate ADR" — but the spec already describes the migrated format as current.

### GAP: ProjectKVState is missing spec-documented fields
Location: `mclaude-control-plane/projects.go` → `ProjectKVState` struct → `docs/spec-state-schema.md` (mclaude-projects value)
Detail: Code's `ProjectKVState` has 9 fields: `id, slug, userSlug, hostSlug, name, gitUrl, status, createdAt, gitIdentityId`. Spec's `ProjectState` value adds 3 more fields not in the code: `sessionCount` (int), `worktrees` (string array), `lastActiveAt` (RFC3339). These spec fields are never written by the code.

### GAP: ADMIN_TOKEN env var undocumented
Location: `mclaude-control-plane/main.go` → `adminToken := envOr("ADMIN_TOKEN", "")` → `docs/mclaude-control-plane/spec-control-plane.md` (env vars table)
Detail: Code reads `ADMIN_TOKEN` env var and passes it to `NewServer()`. The server uses it as a static bearer token for the loopback admin mux (`:9091`). The spec's env vars table does not list `ADMIN_TOKEN`. Spec says admin routes use per-user bearer tokens + `is_admin` check; the static admin token for the loopback port is undocumented.

### GAP: Account key loaded via NATS_ACCOUNT_SEED env var, not file mount
Location: `mclaude-control-plane/main.go` → `loadOrGenerateAccountKey()` → `docs/mclaude-control-plane/spec-control-plane.md` (startup sequence, env vars)
Detail: Code loads the account signing key from `NATS_ACCOUNT_SEED` env var (generates ephemeral if not set). Spec says account key comes from the `OPERATOR_KEYS_PATH` file mount (`/etc/mclaude/operator-keys/accountSeed`). The code does not read from `OPERATOR_KEYS_PATH` at all — it relies entirely on the env var. Spec even lists `OPERATOR_KEYS_PATH` as a config knob and describes the startup step: "Loads operator + account NKeys from `OPERATOR_KEYS_PATH`".

### GAP: Control-plane's own NATS JWT has no permission scoping
Location: `mclaude-control-plane/main.go` → `generateNATSUserCreds()` → `docs/spec-state-schema.md` (NATS Server Configuration)
Detail: Code generates an ephemeral user JWT with `claims.Name = "control-plane"` but no explicit publish/subscribe allow-lists. Spec states: "The control-plane's own NATS connection uses an ephemeral signing identity scoped only to subscribe `$SYS.>` and publish provisioning requests." The code's JWT has unrestricted permissions.

### GAP: Provisioning timeout is a hardcoded constant, not env-configurable
Location: `mclaude-control-plane/projects.go` → `const ProvisionTimeoutSeconds = 10` → `docs/mclaude-control-plane/spec-control-plane.md` (env vars)
Detail: Code uses `const ProvisionTimeoutSeconds = 10` as a hardcoded constant. Spec lists `PROVISION_TIMEOUT_SECONDS` as a configurable env var with default 10. Code does not read this env var.

### GAP: Dev seed uses 30s provisioning timeout
Location: `mclaude-control-plane/main.go` → `seedDev()` → `nc.Request(provSubject, provData, 30*time.Second)` → `docs/mclaude-control-plane/spec-control-plane.md`
Detail: The `seedDev` function uses a 30-second timeout for provisioning requests, while the production path uses 10 seconds and the spec says 10s. The extended timeout in dev seed is undocumented.

### GAP: NATS project-create subject has no host segment
Location: `mclaude-control-plane/projects.go` → subscribe to `mclaude.users.*.api.projects.create` → `docs/spec-state-schema.md` (NATS Subjects)
Detail: The subscription subject `mclaude.users.{uslug}.api.projects.create` has no `.hosts.{hslug}.` segment. The spec's NATS subject table only documents host-scoped subjects (`mclaude.users.{uslug}.hosts.{hslug}.api.projects.create`). This user-scoped subject pattern is not listed in the spec's subject catalog.

---

## Controller-K8s Gaps

### GAP: Controller authenticates via NATS_ACCOUNT_SEED, not NATS_CREDS_FILE
Location: `mclaude-controller-k8s/main.go` → `loadAccountKey()` + `generateNATSUserCreds()` → `docs/mclaude-controller/spec-controller.md` (Configuration table)
Detail: Code reads `NATS_ACCOUNT_SEED` and generates its own JWT (same pattern as control-plane). Spec lists `NATS_CREDS_FILE` as a required env var: "Path to the per-cluster controller JWT + NKey seed." The code does not read `NATS_CREDS_FILE`. The Helm chart sets both `NATS_ACCOUNT_SEED` and `NATS_CREDENTIALS_PATH` — the code uses only the former.

### GAP: JS_DOMAIN env var set by Helm but not read by code
Location: `charts/mclaude-worker/templates/controller-deployment.yaml` → `JS_DOMAIN` env var → `mclaude-controller-k8s/main.go` → `docs/mclaude-controller/spec-controller.md`
Detail: The Helm chart injects `JS_DOMAIN` as a required env var (with a `required` template function), and the spec lists it as required. But the Go binary in `main.go` never reads `JS_DOMAIN` from the environment.

### GAP: LEADER_ELECTION env var set by Helm but not read by code
Location: `charts/mclaude-worker/templates/controller-deployment.yaml` → `LEADER_ELECTION: "true"` → `mclaude-controller-k8s/main.go`
Detail: Chart injects `LEADER_ELECTION=true`, spec mentions "Leader election enabled", but the Go code never reads this env var — leader election is not configured in the `ctrl.Options` struct.

### GAP: LOG_LEVEL env var set by Helm but not read by code
Location: `charts/mclaude-worker/templates/controller-deployment.yaml` → `LOG_LEVEL` → `mclaude-controller-k8s/main.go`
Detail: Chart injects `LOG_LEVEL` from `controller.config.logLevel`, but the Go code never reads it. The zerolog logger uses default level.

### GAP: MCProject CR named {userSlug}-{projectSlug}, spec says {projectId}
Location: `mclaude-controller-k8s/nats_subscriber.go` → `handleCreate()` → `crName := fmt.Sprintf("%s-%s", req.UserSlug, req.ProjectSlug)` → `docs/spec-state-schema.md` (MCProject CRD)
Detail: Code names MCProject CRs as `{userSlug}-{projectSlug}` (e.g. `alice-billing`). Spec says: "Name: `{projectId}`" (UUID). The slug-based naming is more human-readable but diverges from the spec.

### GAP: "update" operation not implemented in NATS handler
Location: `mclaude-controller-k8s/nats_subscriber.go` → `handleProvisionRequest()` switch statement → `docs/mclaude-controller/spec-controller.md` (NATS subscriptions table)
Detail: Code handles "create", "provision", and "delete" operations. Spec documents four operations: provision, create, update, delete. The "update" operation (for refreshing secrets/credentials) is listed in the spec but has no handler in the code.

### GAP: Deployment named "project-{projectId}", spec says "mclaude-session-agent-{projectId}"
Location: `mclaude-controller-k8s/reconciler.go` → `deployName := "project-" + projectID` → `docs/spec-state-schema.md` (Deployment section)
Detail: Code creates Deployments named `project-{projectId}`. Spec says the deployment name is `mclaude-session-agent-{projectId}`. PVCs also use `project-{projectId}` prefix in code vs spec's expected naming.

### GAP: ServiceAccount named "mclaude-sa", spec says "mclaude-session-agent"
Location: `mclaude-controller-k8s/reconciler.go` → `reconcileRBAC()` → SA name "mclaude-sa" → `docs/spec-state-schema.md` (RBAC section)
Detail: Code creates ServiceAccount `mclaude-sa`, Role `mclaude-role`, RoleBinding `mclaude-role`. Spec says: "ServiceAccount: `mclaude-session-agent`".

### GAP: CLAUDE_CODE_TMPDIR not injected into pod env vars
Location: `mclaude-controller-k8s/reconciler.go` → `buildPodTemplate()` env list → `docs/spec-state-schema.md` (Deployment section)
Detail: Spec says the Deployment should set `CLAUDE_CODE_TMPDIR: /data/claude-tmp` for shell output file persistence across restarts. The code's `buildPodTemplate()` does not include this env var.

### GAP: claude-home emptyDir volume not documented
Location: `mclaude-controller-k8s/reconciler.go` → `buildPodTemplate()` volumes → `docs/spec-state-schema.md`
Detail: Code creates an `emptyDir` volume `claude-home` mounted at `/home/node/.claude`. This volume is not listed in the spec's resource description for the session-agent pod.

### GAP: METRICS_ADDR and HEALTH_PROBE_ADDR env vars not in spec
Location: `mclaude-controller-k8s/main.go` → `envOr("METRICS_ADDR", ":8082")` and `envOr("HEALTH_PROBE_ADDR", ":8081")` → `docs/mclaude-controller/spec-controller.md`
Detail: Code reads `METRICS_ADDR` (default `:8082`) and `HEALTH_PROBE_ADDR` (default `:8081`). The spec's Configuration table does not list these env vars.

---

## Session-Agent Gaps

### GAP: MCLAUDE_LIFECYCLE JetStream stream not created
Location: `mclaude-session-agent/agent.go` → `NewAgent()` (stream creation block) → `docs/spec-state-schema.md` (MCLAUDE_LIFECYCLE stream)
Detail: Code creates `MCLAUDE_EVENTS` and `MCLAUDE_API` streams but NOT `MCLAUDE_LIFECYCLE`. Spec says: "MCLAUDE_LIFECYCLE — Created by: session-agent (`CreateOrUpdateStream` — idempotent)". Lifecycle events are published to core NATS subjects (`mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}`) but without the stream, they are not persisted. Test code (`testutil/deps.go`) does create the stream, confirming it should exist.

### GAP: Event subject fallback to UUIDs when slug fields missing
Location: `mclaude-session-agent/session.go` → `start()` stdout router, event subject construction → `docs/mclaude-session-agent/spec-session-agent.md`
Detail: Code has a fallback that constructs the event subject using UUIDs (`userID`, `projectID`, `sessionID`) when slug fields (`UserSlug`, `HostSlug`, `ProjectSlug`, `Slug`) are empty. The spec documents only slug-based subjects. This UUID fallback path is undocumented.

### GAP: CredentialManager integration in worktree operations
Location: `mclaude-session-agent/agent.go` → `gitWorktreeAdd()` calls `a.credMgr.RefreshIfChanged()` → `docs/mclaude-session-agent/spec-session-agent.md`
Detail: Code refreshes git credentials via `CredentialManager.RefreshIfChanged(gitIdentityID)` before each `git worktree add` operation. The spec mentions "Refreshes credentials before each git worktree operation" but does not document `GIT_IDENTITY_ID` as a parameter for credential refresh selection. The `credMgr` field and `gitIdentityID` field on Agent are not described.

### GAP: publishPermDenied includes jobId field
Location: `mclaude-session-agent/agent.go` → `publishPermDenied()` → `docs/spec-state-schema.md` (session_permission_denied lifecycle event)
Detail: Code publishes `session_permission_denied` with fields: `type, sessionId, tool, jobId, ts`. Spec's lifecycle payload shows: `type, sessionId, tool, ts` — no `jobId` field documented.

---

## Web SPA Gaps

### GAP: ConversationVM defaults hostSlug to 'local'
Location: `mclaude-web/src/components/App.tsx` → `const resolvedHostSlug = project?.hostSlug ?? session?.hostSlug ?? route.hostSlug ?? 'local'` → `docs/mclaude-web/spec-web.md`
Detail: Code defaults `hostSlug` to `'local'` when no host information is available from project, session, or route. This default is not documented in the spec. It means sessions on projects without a known host will silently use 'local' as the host slug for all NATS subjects.

### GAP: SessionStore uses userId for project KV, userSlug for session KV
Location: `mclaude-web/src/stores/session-store.ts` → `startWatching()` → `docs/mclaude-web/spec-web.md`
Detail: Code watches `mclaude-projects` with `kvKeyProjectsForUser(this.userId)` (UUID-based prefix) and `mclaude-sessions` with `kvKeySessionsForUser(this.userSlug)` (slug-based prefix). This asymmetry is partially documented in spec-web.md under "SessionStore KV Watch Prefixes" but the root cause — that the control-plane still writes project KV keys with UUIDs while session-agent writes session KV keys with slugs — is only hinted at, not explicitly called out as a migration gap.

---

## Helm Chart Gaps

### GAP: Helm sets NATS_CREDENTIALS_PATH but code ignores it
Location: `charts/mclaude-worker/templates/controller-deployment.yaml` → `NATS_CREDENTIALS_PATH: /etc/nats/leaf-creds/leaf.creds` → `mclaude-controller-k8s/main.go`
Detail: The Helm template mounts `leaf-creds` volume and sets `NATS_CREDENTIALS_PATH` env var. The controller Go binary never reads this env var or loads the creds file — it uses `NATS_ACCOUNT_SEED` instead. The leaf-creds volume mount is unused by the controller binary.

### GAP: values.yaml has hubUrl but no spec reference
Location: `charts/mclaude-worker/values.yaml` → `hubUrl: ""` → `docs/charts-mclaude/spec-helm.md`
Detail: The `hubUrl` value exists in `values.yaml` with comment "Hub URL for SPA direct connections (informational, used for reference)" but is not consumed by any template or the Go code. Spec-helm.md does not list `hubUrl` as a worker chart knob.

### GAP: leaf-creds Secret source ambiguity
Location: `charts/mclaude-worker/values.yaml` → `leafCreds.jwt` / `leafCreds.seed` → `docs/charts-mclaude/spec-helm.md`
Detail: Spec says `leafCreds.existingSecret` or raw jwt/seed values. Values.yaml has both paths. But the controller code never reads the leaf creds — it generates its own JWT from `NATS_ACCOUNT_SEED`. The leaf-creds Secret is used only by the worker NATS StatefulSet for the leaf-node connection, not by the controller binary. This split usage is not clarified in spec.

### GAP: trustChain.resolverPreload and trustChain.accountPublicKey not in spec
Location: `charts/mclaude-worker/values.yaml` → `trustChain.resolverPreload: ""` and `trustChain.accountPublicKey: ""` → `docs/charts-mclaude/spec-helm.md`
Detail: Values.yaml has `trustChain.resolverPreload` and `trustChain.accountPublicKey` fields that are not listed in the spec-helm.md Configuration knobs table for the worker chart. The spec lists `trustChain.operatorJwt` and `trustChain.accountJwt` only.

---

## Cross-Component Gaps

### GAP: Provisioning uses .api.projects.create subject, spec separates provision and create
Location: `mclaude-control-plane/projects.go` (publisher) + `mclaude-controller-k8s/nats_subscriber.go` (subscriber) → `docs/spec-state-schema.md` (NATS Subjects)
Detail: Control-plane publishes provisioning requests to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.create`. Controller subscribes to `mclaude.users.*.hosts.{clusterSlug}.api.projects.>` and handles the `.create` suffix. The spec lists both `.api.projects.provision` and `.api.projects.create` as separate subjects with different semantics ("provision" for initial resource creation, "create" for future fan-out). In code, only `.create` is used — `.provision` is never published or handled.

### GAP: devOAuthToken flows through controller reconciler but lifecycle is undocumented
Location: `mclaude-controller-k8s/reconciler.go` → `reconcileSecrets()` (checks `r.devOAuthToken`) → `docs/mclaude-controller/spec-controller.md`
Detail: The code injects `DEV_OAUTH_TOKEN` as `oauth-token` in per-user `user-secrets` Secret (both on create and update paths). This is partially documented in spec but the full flow — Helm value → env var → reconciler field → Secret key — including the conditional behavior (only injected when non-empty, updated if value changes) is not spelled out.
