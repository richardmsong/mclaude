# State Schema

Canonical reference for every piece of persistent and semi-persistent state in mclaude. All design docs, evaluators, and implementation must be consistent with this document. When a feature adds, removes, or changes state, this document is updated first.

The design-evaluator checks all `docs/plan-*.md` files against this schema for consistency. Discrepancies are surfaced as gaps.

---

## Postgres (control-plane)

Single PostgreSQL instance in the control-plane cluster. Managed by `mclaude-control-plane/db.go`.

### `users`

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | TEXT | PRIMARY KEY | UUID v4 |
| `email` | TEXT | UNIQUE NOT NULL | Login email |
| `name` | TEXT | NOT NULL | Display name |
| `password_hash` | TEXT | NOT NULL DEFAULT '' | bcrypt hash |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT NOW() | |

Writers: control-plane (CreateUser, DeleteUser)
Readers: control-plane (GetUserByEmail, GetUserByID, auth)

### `projects`

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | TEXT | PRIMARY KEY | UUID v4 |
| `user_id` | TEXT | NOT NULL FK→users ON DELETE CASCADE | Owner |
| `name` | TEXT | NOT NULL | Human-readable name (e.g. "mclaude") |
| `git_url` | TEXT | NOT NULL DEFAULT '' | Optional git remote |
| `status` | TEXT | NOT NULL DEFAULT 'active' | active, pending, archived |
| `cluster_id` | TEXT | NOT NULL FK→clusters ON DELETE RESTRICT | Cluster the project is provisioned on |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT NOW() | |

Writers: control-plane (CreateProject)
Readers: control-plane (GetProjectsByUser, reconciler)

### `clusters`

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | TEXT | PRIMARY KEY | UUID v4 |
| `name` | TEXT | UNIQUE NOT NULL | Human-readable name (e.g. "us-west") |
| `js_domain` | TEXT | UNIQUE NOT NULL | JetStream domain name (e.g. "worker-a") |
| `nats_url` | TEXT | NOT NULL | Internal NATS URL for leaf node connection |
| `nats_ws_url` | TEXT | NOT NULL DEFAULT '' | External WebSocket URL for direct client connections |
| `leaf_creds` | TEXT | NOT NULL | Leaf node NKey credential (private) |
| `status` | TEXT | NOT NULL DEFAULT 'active' | active, draining, offline |
| `labels` | JSONB | NOT NULL DEFAULT '{}' | Arbitrary key-value labels (region, tier, etc.) |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT NOW() | |

Writers: control-plane (RegisterCluster, UpdateCluster)
Readers: control-plane (discovery, login response, RBAC checks)

### `user_clusters`

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `user_id` | TEXT | NOT NULL FK→users ON DELETE CASCADE | |
| `cluster_id` | TEXT | NOT NULL FK→clusters ON DELETE CASCADE | |
| `role` | TEXT | NOT NULL DEFAULT 'member' | member, admin |
| `is_default` | BOOLEAN | NOT NULL DEFAULT FALSE | First grant = default |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT NOW() | |

Primary key: `(user_id, cluster_id)`
Constraint: `CREATE UNIQUE INDEX idx_user_clusters_default ON user_clusters (user_id) WHERE is_default = TRUE` — partial unique index ensuring at most one default per user

Writers: control-plane (GrantClusterAccess, RevokeClusterAccess, SetDefaultCluster)
Readers: control-plane (RBAC validation, project creation, login response)

---

## NATS KV Buckets

### `mclaude-sessions`

Created by: control-plane (`ensureSessionsKV` — `nats.KeyValueConfig{Bucket: "mclaude-sessions"}`)

Key format: `{userId}.{projectId}.{sessionId}` (dot-separated for wildcard matching)

Value: `SessionState`
```json
{
  "id": "string",
  "projectId": "string",
  "branch": "string",
  "worktree": "string",
  "cwd": "string",
  "name": "string",
  "state": "idle | busy | error",
  "stateSince": "RFC3339",
  "createdAt": "RFC3339",
  "model": "string",
  "capabilities": {
    "skills": ["string"],
    "tools": ["string"],
    "agents": ["string"]
  },
  "pendingControls": { "requestId": { ... } },
  "usage": {
    "inputTokens": 0,
    "outputTokens": 0,
    "cacheReadTokens": 0,
    "cacheWriteTokens": 0,
    "costUsd": 0.0
  },
  "replayFromSeq": 0,
  "joinWorktree": false
}
```

Writers: session-agent (on init, every state change, usage accumulation)
Readers: SPA (KV watch for real-time state), session-agent (recovery on resume)
History: all versions (for resume tracking)

### `mclaude-projects`

Created by: control-plane (`ensureProjectsKV` — `nats.KeyValueConfig{Bucket: "mclaude-projects", History: 1}`)

Key format: `{userId}.{projectId}`

Value: `ProjectState`
```json
{
  "id": "string",
  "name": "string",
  "gitUrl": "string",
  "status": "string",
  "sessionCount": 0,
  "worktrees": ["string"],
  "createdAt": "RFC3339",
  "lastActiveAt": "RFC3339",
  "gitIdentityId": "string | null"
}
```

Writers: control-plane (on project creation)
Readers: SPA (KV watch for project list), session-agent, daemon (`GET /jobs/projects`)
History: 1

### `mclaude-heartbeats`

Created by: control-plane (implicitly via session-agent first write; or explicitly if bucket pre-created)

Key format: `{userId}.{projectId}`

Value: `{"ts": "RFC3339"}`

Writers: session-agent (every 30s via heartbeat loop)
Readers: SPA (KV watch for agent liveness)
History: 1

### `mclaude-laptops`

Created by: control-plane (pre-created; opened by daemon in `NewDaemon`)

Key format: `{userId}.{hostname}`

Value:
```json
{
  "machineId": "string",
  "ts": "RFC3339"
}
```

Writers: daemon (`writeLaptopKV` — on startup + every 12h)
Readers: daemon (`checkHostnameCollision` — on startup)
History: 1

### `mclaude-job-queue`

Created by: control-plane (`ensureJobQueueKV` — `nats.KeyValueConfig{Bucket: "mclaude-job-queue", History: 1}`)

Key format: `{userId}/{jobId}`

Value: `JobEntry`
```json
{
  "id": "string (UUID v4)",
  "userId": "string",
  "projectId": "string",
  "specPath": "string",
  "priority": 5,
  "threshold": 75,
  "autoContinue": false,
  "status": "queued | starting | running | paused | completed | failed | needs_spec_fix | cancelled",
  "sessionId": "string",
  "branch": "schedule/{slug}-{shortId}",
  "prUrl": "string",
  "failedTool": "string",
  "error": "string",
  "retryCount": 0,
  "resumeAt": "RFC3339 | null",
  "createdAt": "RFC3339",
  "startedAt": "RFC3339 | null",
  "completedAt": "RFC3339 | null"
}
```

Writers: daemon HTTP server (`POST /jobs`), daemon dispatcher (status transitions), daemon lifecycle subscriber (terminal states)
Readers: daemon dispatcher (KV watch), daemon HTTP server (`GET /jobs`)
History: 1

---

## NATS JetStream Streams

### `MCLAUDE_EVENTS`

Created by: session-agent (`CreateOrUpdateStream` — idempotent, authoritative)

```
Name:      MCLAUDE_EVENTS
Subjects:  mclaude.*.*.events.*
Retention: LimitsPolicy
MaxAge:    30 days
Storage:   FileStorage
Discard:   DiscardOld
```

Subject pattern: `mclaude.{userId}.{projectId}.events.{sessionId|_api}`

Publishers: session-agent (raw stream-json from Claude Code process; `_api` suffix for API responses)
Subscribers: SPA (JetStream consumer for live conversation replay)

### `MCLAUDE_API`

Created by: session-agent (`CreateOrUpdateStream` — idempotent)

```
Name:      MCLAUDE_API
Subjects:  mclaude.*.*.api.sessions.>
Retention: LimitsPolicy
MaxAge:    1 hour
Storage:   FileStorage
Discard:   DiscardOld
```

Subject pattern: `mclaude.{userId}.{projectId}.api.sessions.{create|input|resume|delete|control}`

Publishers: SPA (session commands), daemon (job dispatch)
Subscribers: session-agent (pull consumer for at-least-once delivery)

### `MCLAUDE_LIFECYCLE`

Specified in: `docs/plan-k8s-integration.md`
Created by: not yet created in production code (test-only in `testutil/deps.go`)

```
Name:      MCLAUDE_LIFECYCLE
Subjects:  mclaude.*.*.lifecycle.*
Retention: LimitsPolicy
MaxAge:    TBD
Storage:   FileStorage
Discard:   DiscardOld
```

Subject pattern: `mclaude.{userId}.{projectId}.lifecycle.{sessionId}`

Publishers: session-agent (`publishLifecycle`, `publishLifecycleExtra`, `publishPermDenied`), daemon (session_job_paused)
Subscribers: SPA (session list updates), daemon (`runLifecycleSubscriber` — writes terminal job state to KV)

---

## NATS Subjects (Core Pub/Sub)

These are fire-and-forget messages on core NATS (not JetStream). No persistence.

| Subject Pattern | Publisher | Subscriber | Payload |
|----------------|-----------|------------|---------|
| `mclaude.{userId}.api.projects.create` | SPA | control-plane (request/reply) | `{projectId, name, gitUrl}` |
| `mclaude.{userId}.api.projects.updated` | control-plane | SPA | `{projectId, status}` |
| `mclaude.{userId}.quota` | daemon (`runQuotaPublisher`) | `QuotaMonitor` (per-session) | `QuotaStatus` JSON |
| `mclaude.{userId}.{projectId}.api.sessions.input` | SPA, daemon | session-agent | `{type, message, session_id}` |
| `mclaude.{userId}.{projectId}.api.sessions.control` | SPA | session-agent | `{type, session_id, request}` |
| `mclaude.{userId}.{projectId}.api.sessions.create` | SPA, daemon | session-agent (request/reply) | `{branch, permPolicy, quotaMonitor}` |
| `mclaude.{userId}.{projectId}.api.sessions.delete` | SPA, daemon | session-agent | `{sessionId}` |
| `mclaude.{userId}.{projectId}.api.terminal.*` | SPA | session-agent | terminal I/O |
| `mclaude.{userId}.{projectId}.events.{sessionId}` | session-agent | SPA (via MCLAUDE_EVENTS stream) | raw stream-json |
| `mclaude.{userId}.{projectId}.lifecycle.{sessionId}` | session-agent, daemon | SPA, daemon (via MCLAUDE_LIFECYCLE stream) | lifecycle event JSON |

| `mclaude.clusters.{clusterId}.projects.provision` | control-plane | worker controller (request/reply) | `{userId, projectId, gitUrl}` |
| `mclaude.clusters.{clusterId}.status` | worker controller | control-plane | `{clusterId, status, sessionCount, capacity}` |

Note: `sessions.input`, `sessions.create`, etc. are captured by the `MCLAUDE_API` stream for at-least-once delivery. The session-agent consumes them via a JetStream pull consumer, not a core NATS subscription.

Note: In multi-cluster deployments, all existing subjects flow between hub and worker NATS transparently via leaf node connections. KV buckets and JetStream streams are accessed from hub-connected clients using domain-qualified JetStream (`$JS.{domain}.API.>`).

---

## Kubernetes Resources (Dynamic, per-user/per-project)

Created by the control-plane reconciler (`mclaude-control-plane/reconciler.go`) or direct provisioner (`provision.go`).

### CRD: `MCProject` (`mcprojects.mclaude.io/v1alpha1`)

Scope: Namespaced (in `mclaude-system`)
Name: `{projectId}`

```yaml
spec:
  userId: string      # required
  projectId: string   # required
  gitUrl: string      # optional
status:
  phase: Pending | Provisioning | Ready | Failed
  userNamespace: string
  conditions:
    - type: string
      status: string
      reason: string
      message: string
      lastTransitionTime: date-time
  lastReconciledAt: date-time
```

Writers: control-plane (created on project creation)
Readers: reconciler (watches for changes, drives provisioning)

### Namespace: `mclaude-{userId}`

Labels: `mclaude.io/user-id={userId}`, `mclaude.io/managed=true`

Created by: reconciler (`reconcileNamespace`)
Contains all per-user resources below.

### Secret: `user-secrets` (in `mclaude-{userId}`)

| Key | Value | Description |
|-----|-------|-------------|
| `nats-creds` | NATS credentials file (JWT + NKey seed) | Session-agent NATS auth |
| `oauth-token` | OAuth bearer token (optional) | From DEV_OAUTH_TOKEN env |
| `gh-hosts.yml` | `gh` CLI hosts config (YAML) | Reconciler-managed GitHub credential helper config |
| `glab-config.yml` | `glab` CLI config (YAML) | Reconciler-managed GitLab credential helper config |
| `conn-{id}-token` | OAuth access token or PAT | One per `oauth_connections` row |
| `conn-{id}-refresh-token` | OAuth refresh token (GitLab only) | Rotated on each refresh cycle |
| `conn-{id}-username` | Provider username (plain text) | Used by session-agent to resolve `GIT_IDENTITY_ID` → username |

Writers: reconciler (`reconcileSecrets`), control-plane OAuth callback + PAT handler + `reconcileUserCLIConfig`
Readers: session-agent pod (mounted read-only at `/home/node/.user-secrets`)

### ConfigMap: `user-config` (in `mclaude-{userId}`)

Contents: Claude Code workspace settings, hooks, seed configuration.

Writers: reconciler (seeded from Helm template)
Readers: session-agent pod (mounted at `/home/node/.claude-seed`; pod watches for updates)

### ConfigMap: `{release}-session-agent-template` (in `mclaude-system`)

Static Helm-templated ConfigMap containing session-agent pod spec values:

| Key | Description |
|-----|-------------|
| `image` | Container image ref |
| `imagePullPolicy` | Always, IfNotPresent, Never |
| `terminationGracePeriodSeconds` | Graceful shutdown timeout |
| `resourcesJson` | CPU/memory requests and limits |
| `projectPvcSize` | Project PVC size |
| `projectPvcStorageClass` | Project PVC storage class |
| `nixPvcSize` | Nix PVC size |
| `nixPvcStorageClass` | Nix PVC storage class |

Writers: Helm install/upgrade
Readers: reconciler (reads on startup to template Deployments)

### PVC: `project-{projectId}` (in `mclaude-{userId}`)

- AccessMode: ReadWriteOnce
- Size: from session-agent-template ConfigMap
- StorageClass: from session-agent-template ConfigMap
- Contents: bare git repo, worktrees, Claude Code JSONL persistence
- Mounted at: `/data` in session-agent pod

### PVC: `nix-{projectId}` (in `mclaude-{userId}`)

- AccessMode: ReadWriteOnce
- Size: from session-agent-template ConfigMap
- StorageClass: from session-agent-template ConfigMap
- Contents: shared Nix store (cached tools)
- Mounted at: `/nix` in session-agent pod

### Deployment: `mclaude-session-agent-{projectId}` (in `mclaude-{userId}`)

- Replicas: 1
- Volumes: project PVC, nix PVC, user-config ConfigMap, user-secrets Secret
- Container: session-agent image with `--project-id`, `--user-id` args
- Restart policy: Always (pod restarts trigger `--resume` recovery)

Writers: reconciler (`reconcileDeployment`)

### RBAC: ServiceAccount, Role, RoleBinding (in `mclaude-{userId}`)

- ServiceAccount: `mclaude-session-agent`
- Role: allows get/watch on ConfigMaps (for config reload)
- RoleBinding: binds Role to ServiceAccount

Writers: reconciler (`reconcileRBAC`)

---

## NATS Server Configuration

Static configuration deployed via Helm ConfigMap (`nats-configmap.yaml`).

```
port: 4222              # client connections
http_port: 8222         # monitoring
websocket.port: 8080    # browser clients
max_payload: 8MB        # large tool results
jetstream.store_dir: /data/jetstream
jetstream.max_file_store: configurable
```

No auth resolver configured in NATS config — JWT verification uses the account public key baked into the NATS server config by the control-plane at deploy time.

---

## Local File State (Laptop/Daemon)

### `~/.claude/.credentials.json`

```json
{
  "claudeAiOauth": {
    "accessToken": "string"
  }
}
```

Writers: Claude Code (on OAuth login)
Readers: daemon `readOAuthToken` (for quota API polling)

### NATS credentials file (daemon mode)

Path: specified by `--nats-creds` flag or `DaemonConfig.NATSCredsFile`

Format:
```
-----BEGIN NATS USER JWT-----
<base64 JWT>
------END NATS USER JWT------

-----BEGIN USER NKEY SEED-----
<base64 NKey seed>
------END USER NKEY SEED------
```

Writers: control-plane (generated per-user), daemon JWT refresh loop
Readers: daemon (NATS connection), session-agent (NATS connection)

---

## Lifecycle Event Payloads

Published to `mclaude.{userId}.{projectId}.lifecycle.{sessionId}`. These are the event types and their payloads:

### `session_created`
```json
{ "type": "session_created", "sessionId": "...", "branch": "...", "ts": "RFC3339" }
```

### `session_stopped`
```json
{ "type": "session_stopped", "sessionId": "...", "ts": "RFC3339" }
```

### `session_quota_interrupted`
```json
{ "type": "session_quota_interrupted", "sessionId": "...", "jobId": "...", "threshold": 75, "u5": 76, "r5": "RFC3339", "ts": "RFC3339" }
```

### `session_permission_denied`
```json
{ "type": "session_permission_denied", "sessionId": "...", "tool": "...", "ts": "RFC3339" }
```

### `session_job_complete`
```json
{ "type": "session_job_complete", "sessionId": "...", "jobId": "...", "prUrl": "...", "branch": "...", "ts": "RFC3339" }
```

### `session_job_paused`
```json
{ "type": "session_job_paused", "sessionId": "...", "jobId": "...", "priority": 5, "u5": 76, "ts": "RFC3339" }
```

### `session_job_failed`
```json
{ "type": "session_job_failed", "sessionId": "...", "jobId": "...", "error": "...", "ts": "RFC3339" }
```

---

## Quota Status

Published to `mclaude.{userId}.quota` (core NATS, not JetStream).

```json
{
  "hasData": true,
  "u5": 42,
  "r5": "RFC3339",
  "u7": 15,
  "r7": "RFC3339",
  "ts": "RFC3339"
}
```

Writers: daemon `runQuotaPublisher` (every 60s, polls `api.anthropic.com/api/oauth/usage`)
Readers: `QuotaMonitor` per-session goroutine (NATS subscription)
