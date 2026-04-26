# State Schema

Canonical reference for every piece of persistent and semi-persistent state in mclaude. All ADRs, other specs, evaluators, and implementation must be consistent with this document. When a feature adds, removes, or changes state, this document is updated first (in the same commit as the ADR that motivates the change).

The design-evaluator checks all `docs/adr-*.md` and `docs/spec-*.md` files against this schema for consistency. Discrepancies are surfaced as gaps.

---

## Postgres (control-plane)

Single PostgreSQL instance in the control-plane cluster. Managed by `mclaude-control-plane/db.go`.

### `users`

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | TEXT | PRIMARY KEY | UUID v4 |
| `slug` | TEXT | UNIQUE NOT NULL | Typed-slug identifier used in subjects, URLs, and KV keys. Derived at user creation as `{slugify(name or email_local_part)}-{domain_first_segment}` with numeric suffix on collision. Immutable after creation; email changes do not rewrite the slug. |
| `email` | TEXT | UNIQUE NOT NULL | Login email |
| `name` | TEXT | NOT NULL | Display name (free-form UTF-8, max 128 chars, mutable) |
| `password_hash` | TEXT | NOT NULL DEFAULT '' | bcrypt hash |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT NOW() | |

Writers: control-plane (CreateUser, DeleteUser)
Readers: control-plane (GetUserByEmail, GetUserByID, GetUserBySlug, auth)

Slug charset: `[a-z0-9][a-z0-9-]{0,62}`, excluding leading `_` and the reserved-word blocklist `{users, hosts, projects, sessions, clusters, api, events, lifecycle, quota, terminal}`. See `docs/adr-0024-typed-slugs.md`.

### `projects`

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | TEXT | PRIMARY KEY | UUID v4 |
| `user_id` | TEXT | NOT NULL FK→users ON DELETE CASCADE | Owner |
| `slug` | TEXT | NOT NULL | Typed-slug identifier. Unique within the owning user (`UNIQUE (user_id, slug)`). Derived as `slugify(name)` with numeric suffix on collision within scope. Immutable after creation. |
| `name` | TEXT | NOT NULL | Display name (free-form UTF-8, max 128 chars, mutable, e.g. "mclaude") |
| `git_url` | TEXT | NOT NULL DEFAULT '' | Optional git remote |
| `status` | TEXT | NOT NULL DEFAULT 'active' | active, pending, archived |
| `host_id` | TEXT | NOT NULL FK→hosts ON DELETE RESTRICT | Host the project is provisioned on (machine or cluster host) |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT NOW() | |

Index: `UNIQUE (user_id, host_id, slug)` — projects are unique-by-slug per user per host.

Writers: control-plane (CreateProject)
Readers: control-plane (GetProjectsByUser, GetProjectsByHost, reconciler, GetProjectBySlug)

### `hosts`

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | TEXT | PRIMARY KEY | UUID v4 |
| `user_id` | TEXT | NOT NULL FK→users ON DELETE CASCADE | Owning user |
| `slug` | TEXT | NOT NULL | Typed-slug identifier. Per-user unique (`UNIQUE (user_id, slug)`). For machine hosts, derived from display name via `slugify()`. For cluster hosts, canonical from cluster name. Immutable. |
| `name` | TEXT | NOT NULL | Display name (free-form UTF-8, max 128 chars, mutable) |
| `type` | TEXT | NOT NULL | `machine` or `cluster` |
| `role` | TEXT | NOT NULL DEFAULT 'owner' | `owner` or `user`. Machine hosts always `owner`. Cluster hosts: registering admin = `owner`, granted users = `user`. Multiple owners supported. |
| `cluster_id` | TEXT | FK→clusters ON DELETE CASCADE | NULL for machine hosts. FK to `clusters` for cluster hosts. |
| `public_key` | TEXT | | NKey public key. NOT NULL for machine hosts (generated on host, submitted during registration). NULL for cluster hosts (cluster signing key handles credential issuance). Enforced by CHECK: `type = 'cluster' OR public_key IS NOT NULL`. |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT NOW() | |
| `last_seen_at` | TIMESTAMPTZ | | Updated by presence tracking |

Index: `UNIQUE (user_id, slug)` — hosts are unique-by-slug per user.

Writers: control-plane (RegisterHost, GrantClusterAccess, RemoveHost, UpdateHostName)
Readers: control-plane (GetHostsByUser, GetHostBySlug, reconciler, auth middleware, presence tracking)

Slug charset: same as all slugs per ADR-0024. `hosts` is a reserved word.

See `docs/adr-0004-multi-laptop.md` for the full BYOH design.

### `clusters`

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | TEXT | PRIMARY KEY | UUID v4 |
| `slug` | TEXT | UNIQUE NOT NULL | Typed-slug identifier used in cluster subjects and admin URLs. Derived as `slugify(name)` with numeric suffix on collision. Immutable after creation. |
| `name` | TEXT | UNIQUE NOT NULL | Display name (free-form, e.g. "us-west") |
| `js_domain` | TEXT | UNIQUE NOT NULL | JetStream domain name (e.g. "worker-a") |
| `nats_url` | TEXT | NOT NULL | Internal NATS URL for leaf node connection |
| `nats_ws_url` | TEXT | NOT NULL DEFAULT '' | External WebSocket URL for direct client connections |
| `leaf_creds` | TEXT | NOT NULL | Leaf node NKey credential (private) |
| `status` | TEXT | NOT NULL DEFAULT 'active' | active, draining, offline |
| `labels` | JSONB | NOT NULL DEFAULT '{}' | Arbitrary key-value labels (region, tier, etc.) |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT NOW() | |

Writers: control-plane (RegisterCluster, UpdateCluster)
Readers: control-plane (discovery, login response, RBAC checks)

*`user_clusters` table removed — absorbed into `hosts` table with `type='cluster'`. See ADR-0004.*

---

## NATS KV Buckets

All KV bucket keys use typed slugs as their identifier tokens per ADR-0024. Separator is `.` uniformly.

### `mclaude-sessions`

Created by: control-plane (`ensureSessionsKV` — `nats.KeyValueConfig{Bucket: "mclaude-sessions"}`)

Key format: `{uslug}.{hslug}.{pslug}.{sslug}` (dot-separated for wildcard matching; `{hslug}` per ADR-0004)

Value: `SessionState`
```json
{
  "id": "string (UUID v4)",
  "slug": "string (session slug)",
  "userSlug": "string",
  "hostSlug": "string",
  "projectSlug": "string",
  "projectId": "string (UUID v4)",
  "branch": "string",
  "worktree": "string",
  "cwd": "string",
  "name": "string",
  "state": "idle | running | requires_action | updating | restarting | failed | plan_mode | waiting_for_input | unknown",
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

Key format: `{uslug}.{hslug}.{pslug}` (host-scoped per ADR-0004)

Value: `ProjectState`
```json
{
  "id": "string (UUID v4)",
  "slug": "string",
  "userSlug": "string",
  "hostSlug": "string",
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

### `mclaude-clusters`

Created by: control-plane

Key format: `{uslug}` (per-user view of accessible clusters)

Value: JSON list of cluster slugs the user has access to.

Writers: control-plane (on cluster membership change)
Readers: SPA (user watches own key)
History: 1

### `mclaude-hosts`

Created by: control-plane (pre-created; opened by daemon in `NewDaemon`)

Key format: `{uslug}.{hslug}` (per ADR-0004)

Value:
```json
{
  "slug": "string",
  "type": "machine | cluster",
  "name": "string",
  "status": "online | offline",
  "machineId": "string (machine hosts only)",
  "lastSeen": "RFC3339"
}
```

Writers: daemon (`writeHostKV` — on startup + every 12h for machine hosts), control-plane (for cluster host status)
Readers: SPA (KV watch for host list + status), daemon (on startup)
History: 1

### `mclaude-job-queue`

Created by: control-plane (`ensureJobQueueKV` — `nats.KeyValueConfig{Bucket: "mclaude-job-queue", History: 1}`)

Key format: `{uslug}.{jobId}` (dot-separated; `{jobId}` stays UUID v4)

Value: `JobEntry`
```json
{
  "id": "string (UUID v4)",
  "userId": "string (UUID v4)",
  "userSlug": "string",
  "hostSlug": "string",
  "projectId": "string (UUID v4)",
  "projectSlug": "string",
  "sessionId": "string (UUID v4)",
  "sessionSlug": "string",
  "claudeSessionID": "string (Claude Code session ID; captured from system/init stream-json event; used for --resume fallback)",
  "prompt": "string (free-text initial user message)",
  "title": "string (display label; falls back to branchSlug)",
  "branchSlug": "string (matches ^[a-z0-9][a-z0-9-]*$)",
  "resumePrompt": "string (caller-supplied nudge on resume; empty = platform default)",
  "priority": 5,
  "softThreshold": 75,
  "hardHeadroomTokens": 50000,
  "autoContinue": false,
  "permPolicy": "managed | auto | allowlist | strict-allowlist",
  "allowedTools": ["string", "..."],
  "status": "queued | starting | running | paused | completed | failed | needs_spec_fix | cancelled",
  "pausedVia": "quota_soft | quota_hard | \"\" (empty when not paused)",
  "branch": "schedule/{branchSlug} (no short-ID suffix; same slug = shared worktree)",
  "failedTool": "string",
  "error": "string",
  "retryCount": 0,
  "resumeAt": "RFC3339 | null (set on autoContinue-paused jobs; = 5h reset time from QuotaStatus.r5)",
  "createdAt": "RFC3339",
  "startedAt": "RFC3339 | null",
  "completedAt": "RFC3339 | null"
}
```

Field origins: `specPath`, `threshold`, and `prUrl` from ADR-0009 are removed (see ADR-0034). `prompt`, `title`, `branchSlug`, `resumePrompt`, `softThreshold`, `hardHeadroomTokens`, `permPolicy`, `allowedTools`, `claudeSessionID`, `pausedVia` are introduced by ADR-0034.

Writers: daemon HTTP server (`POST /jobs`), daemon dispatcher (status transitions), daemon lifecycle subscriber (terminal states)
Readers: daemon dispatcher (KV watch), daemon HTTP server (`GET /jobs`)
History: 1

Dispatcher uses slug fields (`userSlug`, `hostSlug`, `projectSlug`, `sessionSlug`) to construct KV keys into `mclaude-sessions`. UUID fields (`userId`, `projectId`, `sessionId`) remain for Postgres foreign-key joins.

---

## NATS JetStream Streams

### `MCLAUDE_EVENTS`

Created by: session-agent (`CreateOrUpdateStream` — idempotent, authoritative)

```
Name:      MCLAUDE_EVENTS
Subjects:  mclaude.users.*.hosts.*.projects.*.events.*
Retention: LimitsPolicy
MaxAge:    30 days
Storage:   FileStorage
Discard:   DiscardOld
```

Subject pattern: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events.{sslug|_api}`

Publishers: session-agent (raw stream-json from Claude Code process; `_api` suffix for API responses — the `_` prefix is reserved for internal sentinels and does not collide with slugs, which cannot start with `_`)
Subscribers: SPA (JetStream consumer for live conversation replay)

### `MCLAUDE_API`

Created by: session-agent (`CreateOrUpdateStream` — idempotent)

```
Name:      MCLAUDE_API
Subjects:  mclaude.users.*.hosts.*.projects.*.api.sessions.>
Retention: LimitsPolicy
MaxAge:    1 hour
Storage:   FileStorage
Discard:   DiscardOld
```

Subject pattern: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.{create|input|resume|delete|control}`

Publishers: SPA (session commands), daemon (job dispatch)
Subscribers: session-agent (pull consumer for at-least-once delivery)

### `MCLAUDE_LIFECYCLE`

Specified in: `docs/adr-0003-k8s-integration.md`
Created by: not yet created in production code (test-only in `testutil/deps.go`)

```
Name:      MCLAUDE_LIFECYCLE
Subjects:  mclaude.users.*.hosts.*.projects.*.lifecycle.*
Retention: LimitsPolicy
MaxAge:    TBD
Storage:   FileStorage
Discard:   DiscardOld
```

Subject pattern: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}`

Publishers: session-agent (`publishLifecycle`, `publishLifecycleExtra`, `publishPermDenied`), daemon (session_job_paused)
Subscribers: SPA (session list updates), daemon (`runLifecycleSubscriber` — writes terminal job state to KV)

---

## NATS Subjects (Core Pub/Sub)

These are fire-and-forget messages on core NATS (not JetStream). No persistence. All subjects use typed literals between slugs per ADR-0024 (extended by ADR-0004): every slug is preceded by a reserved word (`users`, `hosts`, `projects`, `sessions`, `clusters`, `api`, `events`, `lifecycle`, `quota`, `terminal`) that names what the following token is.

**User-level subjects** (no host scope):

| Subject Pattern | Publisher | Subscriber | Payload |
|----------------|-----------|------------|---------|
| `mclaude.users.{uslug}.api.projects.create` | SPA | control-plane (request/reply) | `{projectSlug, hostSlug, name, gitUrl}` |
| `mclaude.users.{uslug}.api.projects.updated` | control-plane | SPA | `{projectSlug, hostSlug, status}` |
| `mclaude.users.{uslug}.quota` | daemon (`runQuotaPublisher`) | `QuotaMonitor` (per-session) | `QuotaStatus` JSON — leaf under user scope (not under `.api.`, since quota is a broadcast signal, not a request/reply endpoint) |

**Host-scoped subjects** (per ADR-0004 — `.hosts.{hslug}.` inserted between user and project):

| Subject Pattern | Publisher | Subscriber | Payload |
|----------------|-----------|------------|---------|
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.input` | SPA, daemon | session-agent | `{type, message, sessionSlug}` |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.control` | SPA | session-agent | `{type, sessionSlug, request}` |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.create` | SPA, daemon | session-agent (request/reply) | `{branch, permPolicy, quotaMonitor}` |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.delete` | SPA, daemon | session-agent | `{sessionSlug}` |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.*` | SPA | session-agent | terminal I/O |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events.{sslug}` | session-agent | SPA (via MCLAUDE_EVENTS stream) | raw stream-json |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}` | session-agent, daemon | SPA, daemon (via MCLAUDE_LIFECYCLE stream) | lifecycle event JSON |
| `mclaude.users.{uslug}.hosts.{hslug}.status` | daemon (machine hosts) | control-plane, SPA | host presence heartbeat |

**Cluster infrastructure subjects** (unchanged from ADR-0011):

| Subject Pattern | Publisher | Subscriber | Payload |
|----------------|-----------|------------|---------|
| `mclaude.clusters.{cslug}.api.projects.provision` | control-plane | worker controller (request/reply) | `{userSlug, hostSlug, projectSlug, gitUrl}` |
| `mclaude.clusters.{cslug}.api.status` | worker controller | control-plane | `{clusterSlug, status, sessionCount, capacity}` |

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
  userId: string         # required
  projectId: string      # required
  gitUrl: string         # optional
  gitIdentityId: string  # optional — oauth_connections.id for git credential resolution
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
- Container: session-agent image with env vars `USER_ID`, `PROJECT_ID` (UUIDs for FK joins), `USER_SLUG`, `PROJECT_SLUG`, `HOST_SLUG` (slugs for NATS subject and KV key construction per ADR-0024 + ADR-0004)
- Restart policy: Always (pod restarts trigger `--resume` recovery)

The reconciler resolves `USER_SLUG`, `HOST_SLUG`, and `PROJECT_SLUG` from Postgres (`users.slug`, `hosts.slug`, `projects.slug`) when building the pod template. Session slugs are per-session and flow through NATS messages / KV state — they are not pod env vars.

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

## Local File State (Host Daemon)

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

### Host credentials directory (per ADR-0004)

Path: `~/.mclaude/hosts/{hslug}/`

Contents:
- `nkey.seed` — NKey private seed (generated locally on host, never leaves machine)
- `nats.creds` — NATS credentials file (JWT + NKey seed, host-scoped permissions)
- `config.json` — host metadata (`{slug, serverUrl, userSlug}`)

Writers: `mclaude host register` (on registration), daemon JWT refresh loop
Readers: daemon (NATS connection)

### NATS credentials file (legacy — pre-BYOH)

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

Note: Legacy user-scoped credentials are rejected after BYOH migration. Daemons must re-register via `mclaude host register`.

---

## Lifecycle Event Payloads

Published to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}`. These are the event types and their payloads:

### `session_created`
```json
{ "type": "session_created", "sessionId": "...", "branch": "...", "ts": "RFC3339" }
```

### `session_stopped`
```json
{ "type": "session_stopped", "sessionId": "...", "ts": "RFC3339" }
```

### `session_permission_denied`
```json
{ "type": "session_permission_denied", "sessionId": "...", "tool": "...", "ts": "RFC3339" }
```

### `session_job_complete`
```json
{ "type": "session_job_complete", "sessionId": "...", "jobId": "...", "branch": "...", "ts": "RFC3339" }
```

Published by: QuotaMonitor's `publishExitLifecycle` when the session ended naturally (no platform-injected marker, Stop hook allowed, `sessions.delete` subsequently invoked by dispatcher). No `prUrl` — callers capture artifacts (PR URL, commit SHA, results) via git log, PR body, or external logging.

### `session_job_paused`
```json
{
  "type": "session_job_paused",
  "sessionId": "...",
  "jobId": "...",
  "pausedVia": "quota_soft | quota_hard",
  "u5": 76,
  "r5": "RFC3339",
  "outputTokensSinceSoftMark": 12345,
  "ts": "RFC3339"
}
```

Published by: QuotaMonitor's `publishExitLifecycle` on soft-stop turn-end OR hard-stop interrupt. Both variants leave the Claude Code subprocess alive; `pausedVia` distinguishes them. `r5` is sourced from the monitor's most recent `QuotaStatus` snapshot and is load-bearing — the lifecycle subscriber uses it to set `JobEntry.ResumeAt` when `autoContinue` is true. `outputTokensSinceSoftMark` is present for `quota_hard` only (unset/zero for `quota_soft`). Supersedes ADR-0009's `session_quota_interrupted` (consolidated into this event).

### `session_job_cancelled`
```json
{ "type": "session_job_cancelled", "sessionId": "...", "jobId": "...", "ts": "RFC3339" }
```

Published by: daemon dispatcher when handling `DELETE /jobs/{id}`, after publishing `sessions.delete` on `subj.UserHostProjectAPISessionsDelete`. The session-agent's existing `handleDelete` reaps the subprocess (its internal `stopAndWait` sends the `control_request` interrupt) and publishes its own generic `session_stopped` event; the dispatcher's additional `session_job_cancelled` publish is what marks this as a job cancellation. The lifecycle subscriber picks it up and writes `Status = cancelled`. The dispatcher does NOT send a separate `sessions.input` interrupt — that would be redundant with `stopAndWait`'s internal interrupt.

### `session_job_failed`
```json
{ "type": "session_job_failed", "sessionId": "...", "jobId": "...", "error": "...", "ts": "RFC3339" }
```

---

## Quota Status

Published to `mclaude.users.{uslug}.quota` (core NATS, not JetStream).

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
