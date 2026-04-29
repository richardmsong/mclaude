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
| `slug` | TEXT | UNIQUE NOT NULL | Typed-slug identifier used in subjects, URLs, and KV keys. Derived at user creation as `lower(regexp_replace(split_part(email, '@', 1), '[^a-zA-Z0-9]+', '-', 'g'))` (email local-part only, slugified) with numeric suffix on collision. Immutable after creation; email changes do not rewrite the slug. |
| `email` | TEXT | UNIQUE NOT NULL | Login email |
| `name` | TEXT | NOT NULL | Display name (free-form UTF-8, max 128 chars, mutable) |
| `password_hash` | TEXT | NOT NULL DEFAULT '' | bcrypt hash (legacy / dev-seed; production users authenticate via OAuth) |
| `oauth_id` | TEXT | NULL | Provider's stable user identifier from the linked OAuth identity. NULL for unlinked rows (e.g. bootstrap-admin row created before first login). On first OAuth callback whose email matches the row, control-plane sets this to the provider's `id`. |
| `is_admin` | BOOLEAN | NOT NULL DEFAULT FALSE | Gate for `/admin/*` endpoints (cluster register, grant, user management). Set TRUE by the `init-keys` Helm Job for the bootstrap admin email; subsequent admin promotion via `POST /admin/users/{uslug}/promote`. |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT NOW() | |

Writers: control-plane (CreateUser, DeleteUser, PromoteAdmin, OAuth-callback link).
Readers: control-plane (GetUserByEmail, GetUserByID, GetUserBySlug, auth middleware, admin gate).

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
| `host_id` | TEXT | NOT NULL FK→hosts ON DELETE CASCADE | Host the project is provisioned on (machine or cluster host) |
| `git_identity_id` | TEXT | NULL FK→oauth_connections ON DELETE SET NULL | Optional link to an OAuth connection providing git credentials for this project's repo |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT NOW() | |

Index: `UNIQUE (user_id, host_id, slug)` — projects are unique-by-slug per user per host.

Writers: control-plane (CreateProject)
Readers: control-plane (GetProjectsByUser, GetProjectsByHost, GetProjectBySlug), `mclaude-controller-k8s` and `mclaude-controller-local` (via NATS request payload — they receive `host_id` indirectly through the host-scoped subject; they do not query Postgres directly).

### `hosts`

Single source of truth for both BYOH machines and K8s clusters per ADR-0035 (which supersedes ADR-0004 + ADR-0011 + ADR-0014). The `clusters` table no longer exists; cluster-shared infrastructure fields live as columns on `hosts` and are duplicated across the user rows that share a cluster.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | TEXT | PRIMARY KEY | UUID v4 |
| `user_id` | TEXT | NOT NULL FK→users ON DELETE CASCADE | Owning user |
| `slug` | TEXT | NOT NULL | Typed-slug identifier. Per-user unique (`UNIQUE (user_id, slug)`). For machine hosts, user-chosen during `mclaude host register`. For cluster hosts, admin-controlled and equal to the cluster's canonical name — every user granted access to the same cluster gets a host row with the same slug. Immutable. |
| `name` | TEXT | NOT NULL | Display name (free-form UTF-8, max 128 chars, mutable) |
| `type` | TEXT | NOT NULL CHECK (`type IN ('machine', 'cluster')`) | `machine` (BYOH laptop) or `cluster` (K8s worker) |
| `role` | TEXT | NOT NULL DEFAULT 'owner' CHECK (`role IN ('owner', 'user')`) | `owner` or `user`. Machine hosts always `owner`. Cluster hosts: registering admin = `owner`, granted users = `user`. Multiple owners per cluster are supported. |
| `js_domain` | TEXT | | JetStream domain for the cluster's worker NATS (e.g. `us-east`). NULL for machine hosts. Required for cluster hosts. |
| `leaf_url` | TEXT | | Worker NATS leaf-node URL (e.g. `nats-leaf://worker.example:7422`). NULL for machine hosts. Required for cluster hosts. |
| `account_jwt` | TEXT | | Account JWT used by the worker NATS for the trust chain. NULL for machine hosts. Required for cluster hosts. |
| `direct_nats_url` | TEXT | | Externally-reachable WebSocket URL the SPA uses for direct-to-worker NATS connections (e.g. `wss://us-east.mclaude.example/nats`). NULL for machine hosts. Optional for cluster hosts (NULL ⇒ SPA falls back to hub-via-leaf-node only). |
| `public_key` | TEXT | | NKey public key. For machine hosts: the daemon's NKey (host-generated during `mclaude host register`; private seed never leaves the machine). For cluster hosts: the cluster controller / leaf JWT's NKey (control-plane-generated at `mclaude cluster register`; the seed is returned to the admin once and stored in the worker NATS Secret). Used by `$SYS` presence to identify which host slug a connection belongs to. |
| `user_jwt` | TEXT | | Per-user JWT scoped to `mclaude.users.{uslug}.hosts.{hslug}.>`. NULL until issued by control-plane during registration / grant. |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT NOW() | |
| `last_seen_at` | TIMESTAMPTZ | | Updated by `$SYS.ACCOUNT.*.CONNECT` subscription on hub NATS. Authoritative historical record. The current `online` boolean lives in `mclaude-hosts` KV, not Postgres. |

Constraints:
- `UNIQUE (user_id, slug)` — hosts are unique-by-slug per user.
- `CHECK (type = 'machine' OR (js_domain IS NOT NULL AND leaf_url IS NOT NULL AND account_jwt IS NOT NULL))` — cluster-shared infrastructure fields are required on cluster hosts.

Writers: control-plane (`RegisterHost`, `GrantClusterAccess`, `RemoveHost`, `UpdateHostName`, `IssueHostJWT`, `$SYS` presence subscriber for `last_seen_at`).
Readers: control-plane (`GetHostsByUser`, `GetHostBySlug`, login handler, auth middleware, presence tracking), `mclaude-controller-k8s` (subscribes to `mclaude.users.*.hosts.{cluster-slug}.>` and uses cluster slug as a configured value).

Slug charset: per ADR-0024. The reserved-word blocklist remains `{users, hosts, projects, sessions, clusters, api, events, lifecycle, quota, terminal}` — `clusters` stays in the blocklist defensively even though no `mclaude.clusters.*` subjects exist anymore, to prevent future user/host/project slugs colliding with the historical token.

#### Cluster-shared field duplication

When the admin grants user B access to a cluster that user A already owns, control-plane copies the cluster-shared fields — `js_domain`, `leaf_url`, `account_jwt`, `direct_nats_url`, and `public_key` (the cluster controller's NKey public key) — from A's host row into the new row for B (same `slug`, `type='cluster'`, `role='user'`). Updates to cluster-shared fields propagate via a single `UPDATE hosts SET … WHERE slug = '<cluster-slug>' AND type = 'cluster'` statement.

Per-user fields that are **not** cluster-shared: `id`, `user_id`, `name`, `role`, `user_jwt`, `created_at`, `last_seen_at`.

#### Default machine host

On user creation, control-plane writes one row to `hosts` with `slug='local'`, `type='machine'`, `role='owner'`. The user's first project is associated with this host unless explicitly registered against a different one.

See `docs/adr-0035-unified-host-architecture.md` for the unified host model.

### `oauth_connections`

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | TEXT | PRIMARY KEY | UUID v4 |
| `user_id` | TEXT | NOT NULL FK→users ON DELETE CASCADE | Owning user |
| `provider_id` | TEXT | NOT NULL | Admin-configured provider instance ID (from `providers.json`) |
| `provider_type` | TEXT | NOT NULL | `github` or `gitlab` |
| `auth_type` | TEXT | NOT NULL | `oauth` or `pat` |
| `base_url` | TEXT | NOT NULL | Provider API base URL (e.g. `https://github.com`, `https://gitlab.example.com`) |
| `display_name` | TEXT | NOT NULL | Human-readable label (e.g. "GitHub — alice") |
| `provider_user_id` | TEXT | NOT NULL | Provider's stable user identifier |
| `username` | TEXT | NOT NULL DEFAULT '' | Provider username (used to resolve `GIT_IDENTITY_ID` → username in session-agent) |
| `scopes` | TEXT | NOT NULL DEFAULT '' | Granted OAuth scopes |
| `token_expires_at` | TIMESTAMPTZ | NULL | Token expiry (GitLab OAuth tokens expire; GitHub PATs do not) |
| `connected_at` | TIMESTAMPTZ | NOT NULL DEFAULT NOW() | |

Constraints:
- `UNIQUE (user_id, base_url, provider_user_id)` — one connection per user per provider identity.

Tokens are stored in per-user K8s Secrets (`user-secrets` in the user namespace), not in Postgres. The `oauth_connections` table stores only metadata. Secret keys follow the pattern `conn-{id}-token`, `conn-{id}-refresh-token`, `conn-{id}-username`.

Writers: control-plane (OAuth callback handler, PAT handler, GitLab token refresh goroutine).
Readers: control-plane (provider listing, credential rotation, connection deletion).

---

## NATS KV Buckets

All KV bucket keys use typed slugs as their identifier tokens per ADR-0024. Separator is `.` uniformly.

### `mclaude-sessions`

Created by: control-plane (`ensureSessionsKV` — `nats.KeyValueConfig{Bucket: "mclaude-sessions"}`)

Key format: `{uslug}.{hslug}.{pslug}.{sslug}` (dot-separated for wildcard matching; `{hslug}` per ADR-0035)

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
  "extraFlags": "string (optional — additional CLI flags persisted across restarts, e.g. --disallowedTools, --model)",
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
History: 64 (maximum supported by NATS KV; sufficient for resume tracking)

### `mclaude-projects`

Created by: control-plane (`ensureProjectsKV` — `nats.KeyValueConfig{Bucket: "mclaude-projects", History: 1}`)

Key format: `{userId}.{projectId}` (UUID-based; migration to `{uslug}.{hslug}.{pslug}` deferred per ADR-0050)

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
  "createdAt": "RFC3339",
  "gitIdentityId": "string | null"
}
```

Note: The spec target includes `sessionCount`, `worktrees`, and `lastActiveAt` fields, but these are not yet implemented in `ProjectKVState` (Go struct). Current Go implementation writes only the 9 fields shown above. The additional fields are planned for a future iteration:
```json
{
  "sessionCount": 0,
  "worktrees": ["string"],
  "lastActiveAt": "RFC3339"
}
```

Writers: control-plane (on project creation)
Readers: SPA (KV watch for project list), session-agent, daemon (`GET /jobs/projects`)
History: 1

### `mclaude-hosts`

Created by: control-plane (`ensureHostsKV`).

Key format: `{uslug}.{hslug}`

Value:
```json
{
  "slug": "string",
  "type": "machine | cluster",
  "name": "string",
  "role": "owner | user",
  "online": true,
  "lastSeenAt": "RFC3339"
}
```

Writers: control-plane only — single writer. Primary path: `$SYS.ACCOUNT.*.CONNECT` writes the full value object (`slug`, `type`, `name`, `role`, `online=true`, `lastSeenAt=now`). `$SYS.ACCOUNT.*.DISCONNECT` writes only `{online: false}` — all other fields (`slug`, `type`, `name`, `role`, `lastSeenAt`) are preserved from the previous entry (read-modify-write: fetch current value, set `online=false`, re-put). `lastSeenAt` is **not** rewritten on disconnect. Secondary path (dev only): on `DEV_SEED=true` startup, control-plane writes the bootstrap user's `local` machine host entry with `online=true` because the auto-created `local` host has no NKey and will never trigger a `$SYS` CONNECT event.
Readers: SPA (KV watch for host list + status).
History: 1

The previous `mclaude-clusters` KV is removed. SPA derives the per-user cluster list by filtering the `hosts` array in the login response (`hosts.filter(h => h.type === 'cluster')`); no separate KV bucket is needed. **Dead code:** `subj.ClustersKVKey()` in `mclaude-common/pkg/subj/subj.go` and `kvKeyUserClusters()` in `mclaude-web/src/lib/subj.ts` still reference this removed bucket.

The previous `mclaude-laptops` and `mclaude-heartbeats` buckets are removed. Liveness is `$SYS`-only per ADR-0035 (no periodic heartbeat publishes).

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

**Known gap — ADR-0034 migration not complete:** The Go `JobEntry` struct still carries ADR-0009 fields (`specPath`, `threshold`, `prUrl`) and does not yet have the ADR-0034 additions (`prompt`, `title`, `branchSlug`, `resumePrompt`, `softThreshold`, `hardHeadroomTokens`, `permPolicy`, `allowedTools`, `claudeSessionID`, `pausedVia`). The schema above describes the ADR-0034 target; the code implements the ADR-0009 schema.

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

Subject pattern: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.{create|input|restart|delete|control}`

Publishers: SPA (session commands), daemon (job dispatch)
Subscribers: session-agent (pull consumer for at-least-once delivery)

### `MCLAUDE_LIFECYCLE`

Specified in: `docs/adr-0003-k8s-integration.md`
Created by: session-agent (`CreateOrUpdateStream` — idempotent; same pattern as MCLAUDE_EVENTS / MCLAUDE_API). **Note: the session-agent code does not currently create this stream on startup** — only `MCLAUDE_EVENTS` and `MCLAUDE_API` are created. The test harness (`testutil/deps.go`) creates it for integration tests. Lifecycle events are published to core NATS subjects and are received by subscribers, but without the stream they are not persisted. This is a known gap — the stream creation should be added to the agent's startup sequence.

```
Name:      MCLAUDE_LIFECYCLE
Subjects:  mclaude.users.*.hosts.*.projects.*.lifecycle.*
Retention: LimitsPolicy
MaxAge:    30 days
Storage:   FileStorage
Discard:   DiscardOld
```

Subject pattern: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}`

Publishers: session-agent (`publishLifecycle`, `publishLifecycleExtra`, `publishPermDenied`), daemon (session_job_paused)
Subscribers: SPA (session list updates), daemon (`runLifecycleSubscriber` — writes terminal job state to KV)

---

## NATS Subjects (Core Pub/Sub)

These are fire-and-forget messages on core NATS (not JetStream). No persistence. All subjects use typed literals between slugs per ADR-0024 (extended by ADR-0035): every slug is preceded by a reserved word (`users`, `hosts`, `projects`, `sessions`, `api`, `events`, `lifecycle`, `quota`, `terminal`) that names what the following token is. The token `clusters` remains in the slug blocklist defensively but is not used as a subject typer — there are no `mclaude.clusters.*` subjects in the unified host model.

**User-level subjects** (no host scope):

| Subject Pattern | Publisher | Subscriber | Payload |
|----------------|-----------|------------|---------|
| `mclaude.users.{uslug}.quota` | daemon (`runQuotaPublisher`) | `QuotaMonitor` (per-session) | `QuotaStatus` JSON — leaf under user scope (not under `.api.`, since quota is a broadcast signal, not a request/reply endpoint) |
| `mclaude.users.{uslug}.api.projects.updated` | control-plane | SPA | Broadcast notification that project state has changed (project created, updated, or deleted). SPA invalidates its project list cache on receipt. **Not yet published by code** — SPA helper `subjProjectsUpdated()` exists but no control-plane code publishes to this subject. SPA currently discovers project changes through KV watches instead. |

**Host-scoped subjects** (per ADR-0035 — `.hosts.{hslug}.` inserted between user and project; the only project-scoped subject family):

| Subject Pattern | Publisher | Subscriber | Payload |
|----------------|-----------|------------|---------|
| `mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision` | control-plane | controller (request/reply, 10s timeout) | `{userID, userSlug, hostSlug, projectID, projectSlug, gitUrl, gitIdentityId}` |
| `mclaude.users.{uslug}.hosts.{hslug}.api.projects.create` | SPA, control-plane | controller (request/reply) | `{userID, userSlug, hostSlug, projectID, projectSlug, gitUrl}` |
| `mclaude.users.{uslug}.hosts.{hslug}.api.projects.update` | control-plane | controller (request/reply) | `{userSlug, hostSlug, projectSlug, …}` |
| `mclaude.users.{uslug}.hosts.{hslug}.api.projects.delete` | control-plane | controller (request/reply) | `{userSlug, hostSlug, projectSlug}` |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.input` | SPA, daemon | session-agent (JetStream, cmd consumer) | `{session_id, type, message}` — `session_id` is the mclaude UUID from sessions.create |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.control` | SPA | session-agent (JetStream, ctl consumer) | `{type, sessionSlug, request}` |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.create` | SPA, daemon | session-agent (JetStream, cmd consumer; publish + KV-watch, no reply) | `{name, branch, cwd, joinWorktree, extraFlags, permPolicy, quotaMonitor, requestId}` — success: session appears in KV; error: `api_error` event on `events._api` |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.delete` | SPA, daemon | session-agent | `{sessionSlug, requestId}` |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.restart` | SPA | session-agent (JetStream, cmd consumer) | `{sessionSlug, extraFlags?, requestId}` — kills process, optionally updates extraFlags in KV, relaunches with `--resume` |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.create` | SPA | session-agent | Spawn shell |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.delete` | SPA | session-agent | Kill terminal |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.resize` | SPA | session-agent | Resize PTY |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.{termId}.output` | session-agent | SPA | Raw PTY output bytes (ephemeral, core NATS, max 4KB per message) |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.{termId}.input` | SPA | session-agent | Raw keyboard input bytes (ephemeral, core NATS) |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events.{sslug}` | session-agent | SPA (via MCLAUDE_EVENTS stream) | raw stream-json |
| `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}` | session-agent, daemon | SPA, daemon (via MCLAUDE_LIFECYCLE stream) | lifecycle event JSON |

The four `…api.projects.{provision,create,update,delete}` subjects are received by exactly one of:
- `mclaude-controller-k8s` — subscribes with a wildcard at the user level: `mclaude.users.*.hosts.{cluster-slug}.api.projects.>`. Receives requests from every user granted access to the cluster, because all those users have host rows with the same slug as the cluster's canonical name.
- `mclaude-controller-local` (BYOH machine variant) — subscribes for its own user/host only: `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>`. Configured at startup from `--host` (or `~/.mclaude/active-host`).

There are **no** `mclaude.clusters.{cslug}.>` subjects. Cluster-scoped routing is achieved by user-level wildcards on host-scoped subjects.

**Hub-side system subjects (control-plane subscribes for liveness):**

| Subject Pattern | Publisher | Subscriber | Payload |
|----------------|-----------|------------|---------|
| `$SYS.ACCOUNT.{accountKey}.CONNECT` | NATS server (hub) | control-plane | Per-connection event; payload includes `client.kind` (`"Client"` / `"Leafnode"`) and `client.nkey` (the connecting client's NKey public key). |
| `$SYS.ACCOUNT.{accountKey}.DISCONNECT` | NATS server (hub) | control-plane | Same shape as CONNECT. |

The NATS server publishes one `$SYS.ACCOUNT.{accountKey}.CONNECT` event per client connection (and one DISCONNECT per drop). Because there is exactly one account in the install, every connection from every component (daemon, SPA, controller leaf-link, control-plane itself) fires this subject. The control-plane discriminates by `client.kind` and `client.nkey`:

| Event | `client.kind` | Lookup | Effect |
|-------|---------------|--------|--------|
| CONNECT | `Client` | `SELECT * FROM hosts WHERE public_key = client.nkey AND type = 'machine'` | If found: update `hosts.last_seen_at = NOW()` for that single row; upsert `mclaude-hosts` KV `{uslug}.{hslug}` with `online=true`. If not found: ignore (covers SPA's per-login ephemeral NKey, control-plane's own service connection, and any unexpected client). |
| CONNECT | `Leafnode` | `SELECT * FROM hosts WHERE public_key = client.nkey AND type = 'cluster' LIMIT 1` | If found: update `hosts.last_seen_at = NOW()` for **all** rows where `slug = found.slug AND type = 'cluster'` (cluster-shared liveness); upsert `mclaude-hosts` KV `{uslug}.{slug}` with `online=true` for each user row. |
| DISCONNECT | `Client` | same as above | Set KV `online=false` for the matched row. `last_seen_at` is **not** rewritten on disconnect (it tracks last-known-online). |
| DISCONNECT | `Leafnode` | same as above | Set KV `online=false` for all matching cluster rows. |

`$SYS` is the only liveness signal. There is no periodic heartbeat publish; "online" means a NATS connection from that host (or its leaf link, for clusters) is currently live. A daemon idle for 5 minutes still shows online until its connection actually drops.

The control-plane's own NATS connection uses an ephemeral user JWT signed by the account key (`claims.Name = "control-plane"`, no explicit publish/subscribe allow-lists — unrestricted within the account). It does not match any row in `hosts.public_key`, so the self-CONNECT event is naturally ignored. SPA connections similarly use the per-login ephemeral NKey delivered in the login response, which is not stored in `hosts.public_key`.

Note: `sessions.input`, `sessions.create`, etc. are captured by the `MCLAUDE_API` stream for at-least-once delivery. The session-agent consumes them via a JetStream pull consumer, not a core NATS subscription.

Note: All host-scoped subjects flow between hub and worker NATS transparently via leaf-node links. KV buckets and JetStream streams hosted on a worker are accessed from hub-connected clients using domain-qualified JetStream (`$JS.{domain}.API.>`); the SPA reads `jsDomain` from the login response and includes it in API calls only when present (i.e. only for cluster hosts).

---

## Kubernetes Resources (Dynamic, per-user/per-project)

Created by `mclaude-controller-k8s` (the cluster controller binary) per ADR-0035. The control-plane is K8s-free; it triggers provisioning by NATS request to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision` and the controller reconciles `MCProject` CRs in response.

### CRD: `MCProject` (`mcprojects.mclaude.io/v1alpha1`)

Scope: Namespaced (in `mclaude-system`)
Name: `{userSlug}-{projectSlug}` (e.g. `dev-default-project`)

```yaml
spec:
  userId: string         # required — UUID v4
  projectId: string      # required — UUID v4
  userSlug: string       # typed slug (ADR-0050) — present in CRD schema but not in `required` list; should be required
  projectSlug: string    # typed slug (ADR-0050) — present in CRD schema but not in `required` list; should be required
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

Created by: `mclaude-controller-k8s` (`reconcileNamespace`).
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

Writers: `mclaude-controller-k8s` (`reconcileSecrets`), control-plane OAuth callback + PAT handler + `reconcileUserCLIConfig`
Readers: session-agent pod (mounted read-only at `/home/node/.user-secrets`)

### ConfigMap: `user-config` (in `mclaude-{userId}`)

Contents: Claude Code workspace settings, hooks, seed configuration.

Writers: `mclaude-controller-k8s` (seeded from Helm template)
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
Readers: reconciler — watches this ConfigMap (filtered by name + namespace) in addition to reading it on startup; on change, re-enqueues all `MCProject` CRs so updated pod specs (e.g. new image) are applied without a manual `helm upgrade` (ADR-0043)

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

### Deployment: `project-{projectId}` (in `mclaude-{userId}`)

- Replicas: 1
- Strategy: `Recreate` — old pod must exit before new pod starts; prevents two pods consuming the same durable JetStream consumers simultaneously (ADR-0043)
- Volumes: project PVC (`project-data`), nix PVC, user-config ConfigMap, user-secrets Secret, `claude-home` emptyDir (mounted at `/home/node/.claude` — ephemeral per pod lifecycle, not persisted across restarts)
- Container: session-agent image with env vars `USER_ID`, `PROJECT_ID` (UUIDs for FK joins), `USER_SLUG`, `PROJECT_SLUG`, `HOST_SLUG` (slugs for NATS subject and KV key construction per ADR-0024 + ADR-0035)
- `CLAUDE_CODE_TMPDIR`: specified in the spec target but **not yet injected** by the reconciler's `buildPodTemplate()`. When implemented, should be set to `/data/claude-tmp` (PVC subPath on the `project-data` volume) so shell output files persist across pod restarts
- Restart policy: Always (pod restarts trigger `--resume` recovery)

`mclaude-controller-k8s` resolves `USER_SLUG`, `HOST_SLUG`, and `PROJECT_SLUG` directly from the host-scoped NATS subject of the provision request (`mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision`) plus the request payload. The controller does not read Postgres — control-plane owns Postgres. Session slugs are per-session and flow through NATS messages / KV state; they are not pod env vars.

Writers: `mclaude-controller-k8s` (`reconcileDeployment`)

### RBAC: ServiceAccount, Role, RoleBinding (in `mclaude-{userId}`)

- ServiceAccount: `mclaude-sa`
- Role: `mclaude-role` — allows get/watch/patch on ConfigMap `user-config` (for config reload) and get on Secret `user-secrets` (for NATS credentials and OAuth tokens mounted into session-agent pods)
- RoleBinding: `mclaude-role` — binds Role to ServiceAccount

Writers: `mclaude-controller-k8s` (`reconcileRBAC`)

---

## NATS Server Configuration

Per ADR-0035 every NATS server (hub on `mclaude-cp` cluster, leaf-node on each worker cluster) is configured with the same 3-tier operator → account → user trust chain. There is exactly one operator and one account per mclaude install.

### Hub NATS (in the `mclaude-cp` Kubernetes cluster)

Static configuration deployed by the `mclaude-cp` Helm chart (`nats-config` ConfigMap):

```
port: 4222                    # client connections
http_port: 8222               # monitoring
websocket.port: 8080          # browser clients
max_payload: 8MB              # large tool results
jetstream.store_dir: /data/jetstream
jetstream.max_file_store: configurable
jetstream.domain: hub         # hub's own JetStream domain

operator: $OPERATOR_JWT       # mounted from K8s Secret mclaude-system/operator-keys
resolver: MEMORY
resolver_preload:
  $ACCOUNT_PUBLIC_KEY: $ACCOUNT_JWT

leafnodes:
  listen: 0.0.0.0:7422        # accepts leaf-node connections from worker clusters

system_account: SYS
```

The hub trusts the operator JWT (self-signed); the operator JWT signs the single account JWT; the account signing key signs every per-host user JWT issued by control-plane and the per-cluster leaf JWTs issued during cluster registration.

### Worker NATS (in each worker Kubernetes cluster)

Deployed by the `mclaude-worker` Helm chart. Same 3-tier trust chain, plus a leaf-node remote pointing at the hub:

```
port: 4222
http_port: 8222
websocket.port: 8080
max_payload: 8MB
jetstream.store_dir: /data/jetstream
jetstream.domain: $JS_DOMAIN  # unique per worker, e.g. "us-east"

operator: $OPERATOR_JWT       # supplied by admin during cluster registration
resolver: MEMORY
resolver_preload:
  $ACCOUNT_PUBLIC_KEY: $ACCOUNT_JWT

leafnodes:
  remotes:
    - url: $LEAF_URL          # e.g. nats-leaf://hub.mclaude.example:7422
      credentials: /etc/nats/leaf.creds   # cluster controller / leaf JWT

system_account: SYS
```

Because every server validates against the same operator and account, the same JWT validates at hub AND any worker. This is what allows the SPA to swap a hub-via-leaf connection for a direct-to-worker connection without re-issuing credentials.

### Operator + account NKeys

Operator and account keys live in the K8s Secret `mclaude-system/operator-keys` in the central `mclaude-cp` cluster:

```
Secret: mclaude-system/operator-keys (Opaque, mode 0600)
Keys:
  operatorJwt   — self-signed operator JWT
  operatorSeed  — operator NKey seed
  accountJwt    — account JWT signed by the operator
  accountSeed   — account NKey seed (used by control-plane to sign user JWTs)
```

Generated on first deploy by the `mclaude-cp` Helm pre-install Job (`mclaude-cp init-keys`). The Job is idempotent: subsequent deploys check Secret existence and exit without regenerating. The hub NATS pod template references this Secret for `resolver_preload`. The control-plane Deployment also reads the Secret to sign per-host user JWTs.

When admin runs `mclaude cluster register`, the control-plane returns `operatorJwt` + `accountJwt` so the new worker cluster's NATS can be deployed with the same trust chain. BYOH machines do **not** receive these keys — they hold only their per-host user JWT.

### Per-host user JWT permissions

Issued by control-plane (`IssueHostJWT(userId, hostSlug)`), signed by the account signing key:

```
publish:   mclaude.users.{uslug}.hosts.{hslug}.>, _INBOX.>, $JS.*.API.>, $SYS.ACCOUNT.*.CONNECT, $SYS.ACCOUNT.*.DISCONNECT
subscribe: mclaude.users.{uslug}.hosts.{hslug}.>, _INBOX.>, $JS.*.API.>
```

### Per-cluster leaf / controller JWT permissions

Issued at cluster registration; doubles as the worker NATS leaf-node JWT and the cluster controller's NATS auth:

```
publish:   mclaude.users.*.hosts.{cluster-slug}.>, _INBOX.>, $JS.*.API.>, $SYS.ACCOUNT.*.CONNECT, $SYS.ACCOUNT.*.DISCONNECT
subscribe: mclaude.users.*.hosts.{cluster-slug}.>, _INBOX.>, $JS.*.API.>
```

The wildcard at the user level lets the controller receive provisioning requests from every user granted access to its cluster.

### Per-session-agent JWT permissions

Issued by `mclaude-controller-k8s` (`IssueSessionAgentJWT(userID, userSlug, accountKP)`), signed by the account signing key. Stored in the per-user `user-secrets` Secret in the user namespace. The session-agent uses this JWT to connect to worker NATS (or hub NATS in the degenerate single-cluster case).

```
publish:   mclaude.{userID}.>, mclaude.users.{userSlug}.hosts.*.>, _INBOX.>, $JS.API.>, $JS.*.API.>, $KV.mclaude-sessions.>, $KV.mclaude-projects.>, $KV.mclaude-hosts.>, $KV.mclaude-job-queue.>, $JS.ACK.>, $JS.FC.>, $JS.API.DIRECT.GET.>
subscribe: mclaude.{userID}.>, mclaude.users.{userSlug}.hosts.*.>, _INBOX.>, $JS.API.>, $JS.*.API.>, $KV.mclaude-sessions.>, $KV.mclaude-projects.>, $KV.mclaude-hosts.>, $KV.mclaude-job-queue.>, $JS.ACK.>, $JS.FC.>, $JS.API.DIRECT.GET.>
```

Permission breakdown:
- `mclaude.{userID}.>` — backward compatibility with UUID-format KV keys (ADR-0050 — key format migration deferred).
- `mclaude.users.{userSlug}.hosts.*.>` — ADR-0035 host-scoped subjects (events, lifecycle, API sessions, terminal).
- `_INBOX.>` — NATS request/reply patterns.
- `$JS.API.>` — JetStream API for direct connections (no domain prefix).
- `$JS.*.API.>` — JetStream API with domain qualification (through hub leaf-link).
- `$KV.mclaude-sessions.>`, `$KV.mclaude-projects.>`, `$KV.mclaude-hosts.>`, `$KV.mclaude-job-queue.>` — KV bucket read/write via NATS KV internal subjects.
- `$JS.ACK.>` — JetStream message acknowledgements.
- `$JS.FC.>` — JetStream flow control.
- `$JS.API.DIRECT.GET.>` — Direct KV get operations.

### Single-cluster degenerate case

When `mclaude-cp` and `mclaude-worker` are installed into the same Kubernetes cluster, the worker NATS leaf-node remote URL is `nats-leaf://localhost:7422` (or the in-cluster service DNS). All other configuration is identical; the SPA still uses host-qualified subjects, and JetStream domain qualification is inserted only when the host's `jsDomain` is non-empty in the login response.

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

### Host credentials directory (BYOH machines)

Path: `~/.mclaude/hosts/{hslug}/`

Contents:
- `nkey.seed` — NKey private seed (generated locally on host, never leaves machine; matches `hosts.public_key` in Postgres)
- `nats.creds` — NATS credentials file (JWT + NKey seed, host-scoped permissions per ADR-0035)
- `config.json` — host metadata (`{slug, hubUrl, userSlug}`)

Writers: `mclaude host register` (on registration), daemon JWT refresh loop.
Readers: daemon (NATS connection).

Active-host pointer: `~/.mclaude/active-host` is a symlink to the active `~/.mclaude/hosts/{hslug}/` directory. The daemon and the CLI consult this when `--host` is not provided.

BYOH machines never receive operator or account NKeys; they hold only their per-host user JWT, which is sufficient to authenticate as a NATS client of the hub.

### User-level credentials

Path: `~/.mclaude/auth.json` (mode `0600`)

Contents: bearer token from `mclaude login`, used for admin CLI HTTP calls (`mclaude cluster register`, `mclaude cluster grant`, `mclaude admin users …`). The CLI sends `Authorization: Bearer <token>` on every HTTP call. The token is user-scoped, not host-scoped — it lives outside the per-host directory because the same admin token is valid across all of the user's hosts.

Writers: `mclaude login`.
Readers: CLI (admin and cluster subcommands).

---

## Login Response Shape

`POST /auth/login` returns a JSON document used by the SPA to bootstrap.

**Current implementation (flat response):**
```json
{
  "natsUrl":   "wss://hub.mclaude.example/nats",
  "jwt":       "<per-host user JWT signed by the account key>",
  "nkeySeed":  "<NKey seed for client signing>",
  "userId":    "uuid",
  "userSlug":  "alice-gmail",
  "expiresAt": 1735689600
}
```

The SPA derives host and project data from KV watches (`mclaude-hosts`, `mclaude-projects`) rather than from the login response.

**Spec target (not yet implemented):** The ADR-0035 target includes a richer response with nested `user` object, `hosts[]` array (full host inventory), and `projects[]` array. This enables the SPA to bootstrap without waiting for KV watches:

```json
{
  "user":     { "id": "uuid", "slug": "alice-gmail" },
  "jwt":      "<per-host user JWT signed by the account key>",
  "nkeySeed": "<NKey seed for client signing>",
  "hubUrl":   "wss://hub.mclaude.example/nats",
  "hosts": [
    {
      "slug":        "mbp16",
      "name":        "alice's MBP",
      "type":        "machine",
      "role":        "owner",
      "online":      true,
      "lastSeenAt":  "RFC3339"
    },
    {
      "slug":          "us-east",
      "name":          "Production K8s",
      "type":          "cluster",
      "role":          "user",
      "online":        true,
      "lastSeenAt":    "RFC3339",
      "jsDomain":      "us-east",
      "directNatsUrl": "wss://us-east.mclaude.example/nats"
    }
  ],
  "projects": [
    { "slug": "myrepo",  "name": "My Repo",        "hostSlug": "mbp16",   "hostType": "machine" },
    { "slug": "billing", "name": "billing service","hostSlug": "us-east", "hostType": "cluster", "jsDomain": "us-east", "directNatsUrl": "wss://us-east.mclaude.example/nats" }
  ]
}
```

When implemented, the `hosts` array will be the single source of truth for host inventory. SPA will derive the cluster list with `hosts.filter(h => h.type === 'cluster')`. `jsDomain` and `directNatsUrl` will be present only on cluster-type entries — the SPA includes JetStream domain qualification only when these fields are present, so single-host BYOH deployments work unchanged.

## Lifecycle Event Payloads

Published to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}`. These are the event types and their payloads:

### `session_created`
```json
{ "type": "session_created", "sessionId": "...", "branch": "...", "ts": "RFC3339" }
```

### `session_stopped`
```json
{ "type": "session_stopped", "sessionId": "...", "exitCode": 0, "ts": "RFC3339" }
```

### `session_restarting`
```json
{ "type": "session_restarting", "sessionId": "...", "ts": "RFC3339" }
```

### `session_resumed`
```json
{ "type": "session_resumed", "sessionId": "...", "ts": "RFC3339" }
```

### `session_failed`
```json
{ "type": "session_failed", "sessionId": "...", "error": "...", "ts": "RFC3339" }
```

### `debug_attached`
```json
{ "type": "debug_attached", "sessionId": "...", "ts": "RFC3339" }
```

### `debug_detached`
```json
{ "type": "debug_detached", "sessionId": "...", "ts": "RFC3339" }
```

### `session_upgrading`
```json
{ "type": "session_upgrading", "sessionId": "...", "ts": "RFC3339" }
```
Published during graceful shutdown (zero-downtime upgrade) after all sessions reach idle.

### `session_permission_denied`
```json
{ "type": "session_permission_denied", "sessionId": "...", "tool": "...", "jobId": "...", "ts": "RFC3339" }
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
