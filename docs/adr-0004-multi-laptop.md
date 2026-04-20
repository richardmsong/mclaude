# ADR: Bring Your Own Host (BYOH)

**Status**: accepted
**Status history**:
- 2026-04-10: accepted
- 2026-04-19: reverted to draft — retroactive accepted tag incorrect; implementation not confirmed
- 2026-04-20: rewritten against current NATS + control-plane + session-agent architecture
- 2026-04-20: paused pending slug-scheme ADR-0024
- 2026-04-20: resumed — ADR-0024 resolved; all decisions finalized; accepted

> File slug remains `adr-0004-multi-laptop.md` for link stability; the concept generalizes from "laptop" to any user-owned host.

## Overview

Let a single user attach one or more of their own **hosts** to the mclaude control plane. A *host* is any machine or cluster that can run `mclaude-session-agent` and reach central NATS:

1. **Machine** — a laptop, desktop, or cloud VM running the session-agent daemon locally.
2. **Cluster** — a K8s worker cluster running session-agent pods, leaf-noded into the hub NATS per ADR-0011.

Both types are first-class peers in a unified `hosts` table. The `clusters` table stays as infrastructure metadata (leaf-node config, NKey, NATS URL, capacity); the user-facing concept is always "host." The `user_clusters` join table from ADR-0011 is absorbed into `hosts` — each user's access to a cluster is a host row with `type='cluster'`.

This ADR **extends ADR-0024's subject scheme** by inserting `.hosts.{hslug}.` between the user and project levels in all project-scoped subjects, KV keys, and HTTP URLs.

This ADR **partially supersedes ADR-0011** (Multi-Cluster Architecture): the registry/identity/RBAC model is replaced by the hosts model. ADR-0011's infrastructure topology (leaf nodes, hub NATS, JetStream domains, worker controller) is preserved unchanged.

## Motivation

Users have more than one machine where they want to run Claude:

- A work MBP and a personal MBP (different codebases, different credentials).
- A laptop for interactive work and a beefy desktop/VM for long-running agent tasks.
- A K8s cluster for shared/team workloads plus a personal laptop for local repos.

Today the system models only two host shapes: "the daemon" (one per user) and "the cluster" (multiple supported via ADR-0011 but clusters are admin-owned, not user-owned). There is no first-class concept of a user-owned host, no registration flow, and no UI affordance for picking one.

Without BYOH, users either run one laptop-daemon and lose their other machines, or manually swap credential files and restart.

## Architecture

```
                       +-----------------------------+
                       |   mclaude-control-plane     |
                       |   (central, Postgres+NATS)  |
                       +-------------+---------------+
                                     | (issues per-host NATS creds, tracks host registry)
          +--------------------------+---------------------------+
          |                          |                           |
   +------v------+          +-------v-----+           +---------v---+
   | Host: mbp16 |          | Host: mbp14 |           | Host: k8s-a |
   | (machine)   |          | (machine)   |           | (cluster)   |
   | daemon      |          | daemon      |           | leaf node   |
   +-------------+          +-------------+           +-------------+
```

- Each host has its own identity (`hslug`) and its own NATS credentials (NKey pair generated on the host; public key submitted to control-plane during registration).
- NATS subjects use a **unified namespace** with typed literals:
  - User-level (no host): `mclaude.users.{uslug}.api.*`, `mclaude.users.{uslug}.quota`
  - Host-scoped (projects/sessions): `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.*`
  - Cluster infra: `mclaude.clusters.{cslug}.api.*` (preserved from ADR-0011)
- Per-host NATS credentials grant `mclaude.users.{uslug}.hosts.{hslug}.>` — a compromised host cannot read another host's traffic for the same user.
- User (SPA) credentials grant `mclaude.users.{uslug}.>` — covers all hosts in one subscription.
- Projects are host-scoped. A project belongs to exactly one host; the same git repo cloned on two hosts shows as two projects.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Host is a first-class concept | Yes — named `host`, not `laptop` | Covers laptops, desktops, VMs, and K8s clusters uniformly. |
| Relationship to ADR-0011 | Partial supersession: registry/identity/RBAC replaced; infra topology preserved | `host` is the user-facing abstraction; `cluster` stays as infrastructure. `user_clusters` absorbed into `hosts`. Leaf-node topology, JetStream domains, worker controller unchanged. |
| Subject scheme | Insert `.hosts.{hslug}.` between user and project in all project-scoped subjects (extends ADR-0024) | Subjects encode host affinity: `mclaude.users.alice-rbc.hosts.mbp16.projects.mclaude.api.sessions.control` is self-describing. Per-host NATS credential scoping uses subject prefix, not payload inspection. 10-12 tokens, ~250 chars — under NATS limits. |
| Host registration (authed CLI) | Direct API: `mclaude host register --name "My MBP"` calls `POST /api/users/{uslug}/hosts` | CLI already has OAuth token. Host generates NKey locally, submits public key. CP signs JWT with host-scoped permissions. Returns NATS creds. Private seed never leaves the host. |
| Host registration (unauthed CLI) | Device-code bridge: web shows 6-char code, user runs `mclaude host register <code> --server <url>` | Cross-device bridge when CLI isn't logged in on the new machine. 6-char alphanumeric, 10-min TTL. |
| Host token model | Host gets its own NATS NKey/JWT, independent of user token | Registration creates host-specific NATS credentials. Host never uses the user's OAuth token for NATS. User OAuth is only for the registration HTTP call (or device code verification). |
| Project scoping | Per-host: `(user_id, host_id, slug)` unique | Same repo cloned on two hosts = two project rows. Simplest model; no cross-host state sync. |
| Host types in v1 | Machine + cluster unified from day one | Both types in `hosts` table with `type` column. Existing ADR-0011 cluster records migrate into `hosts`. No half-unified interim state. |
| Host roles | Per-user rows with role column: `owner` or `user` | Machine hosts: always `role='owner'`. Cluster hosts: admin who registered = `owner`; granted users = `user`. Multiple owners supported. Owners can manage members; users can only remove self. |
| Cluster slug for granted users | Canonical from cluster name | All users who access a cluster get the same host slug (derived from cluster name). `cluster_id` FK is the canonical identity. Consistent naming across users. |
| Host display name | Mutable (slug immutable per ADR-0024) | Display name is free-form, editable. Slug derived at registration, never changes. Slug collision within user scope: numeric suffix (same as projects). |
| Liveness/presence | NATS `$SYS.ACCOUNT.*.CONNECT`/`DISCONNECT` events | Zero extra traffic. Control-plane maintains in-memory presence map. Requires system-account access (CP already has it). |
| Default host selection | Last-used per-user, stored in localStorage | Dashboard opens to last-used host. |
| Offline host fallback | Show offline dashboard + banner with host switcher | Preserves mental context. User explicitly switches to online host via dropdown. |
| Existing daemon migration | Force re-register | Clean break. Legacy user-level NATS creds stop working. Daemons without host credentials are rejected with a clear error directing to `mclaude host register`. Pre-GA, acceptable disruption. |
| NKey minting | Host generates NKey locally; submits public key via registration | Private seed never leaves the host. CP signs JWT. A CP breach cannot impersonate hosts. |
| Host removal | Hard-kill with confirmation dialog; NATS revocation list push | Confirmation lists running sessions. On confirm: CP adds host's public NKey to account revocation list, pushes account update. NATS server disconnects host immediately. |

## User Flow

### Register a machine host (authed)

1. User already has `mclaude-cli` installed and authed (`~/.claude/.credentials.json` has valid OAuth token).
2. User runs `mclaude host register --name "Work MBP"`.
3. CLI generates an NKey pair locally. Stores seed at `~/.mclaude/hosts/{hslug}/nkey.seed`.
4. CLI calls `POST /api/users/{uslug}/hosts` with `{name: "Work MBP", publicKey: "<nkey>", type: "machine"}`.
5. Control-plane creates `hosts` row, signs a JWT with host-scoped NATS permissions, returns it.
6. CLI saves NATS creds at `~/.mclaude/hosts/{hslug}/nats.creds`.
7. CLI prints: `Registered host 'work-mbp'. Start the daemon with: mclaude daemon --host work-mbp`.

### Register a machine host (unauthed — device code)

1. User opens Settings > Hosts in the web UI, clicks "Add Host."
2. Web calls `POST /api/users/{uslug}/hosts/code` — control-plane generates a 6-char alphanumeric code with 10-min TTL.
3. Web shows: "Run on your new machine: `mclaude host register ABC123 --server https://mclaude.internal`"
4. User runs the command on the target machine.
5. CLI calls `POST /api/hosts/register` with `{code: "ABC123"}`.
6. Control-plane verifies code, creates `hosts` row, signs JWT, returns creds.
7. CLI saves NKey seed + NATS creds under `~/.mclaude/hosts/{hslug}/`. Prints success.

### Register a cluster host

1. Admin registers the cluster via `POST /admin/clusters` (unchanged from ADR-0011). Creates `clusters` row with leaf-node NKey, NATS URL, etc.
2. Admin grants user access: `POST /admin/clusters/{cslug}/members` with `{userSlug, role: "owner"|"user"}`.
3. Control-plane creates a `hosts` row: `type='cluster', cluster_id=cslug, slug=<cluster-name-slug>`.
4. User sees the cluster host in Settings > Hosts and in the dashboard host picker.

### Creating a project

1. Dashboard is scoped to a host (host picker at top).
2. User clicks "New Project" — creates it on the currently-selected host.
3. New Project sheet: name + git URL. Host shown as read-only context. Slug preview shown.
4. Control-plane: `INSERT projects` with `host_id`. If host is `type='cluster'`, publishes to `mclaude.clusters.{cslug}.api.projects.provision`. If `type='machine'`, no provisioning needed — daemon picks up via KV watch.

### Creating a session

1. From a project (which is already on a host). No host picker needed — session runs on the project's host.
2. Same flow as today, but subjects now include the host slug.

### Dashboard with multiple hosts

1. Host picker dropdown at top: shows all hosts with online/offline status.
2. "All hosts" option shows sessions across all hosts with a host badge on each row.
3. Default: last-used host (localStorage).
4. Offline host: banner "mbp16 is offline" + "Switch to: [dropdown]" + "[All hosts]". Sessions grayed out.

## Component Changes

### `mclaude-control-plane`

- New `hosts` table in Postgres (see spec-state-schema.md).
- `projects.cluster_id` FK replaced by `projects.host_id` FK to `hosts`.
- New HTTP endpoints: `POST /api/users/{uslug}/hosts`, `GET /api/users/{uslug}/hosts`, `DELETE /api/users/{uslug}/hosts/{hslug}`, `POST /api/users/{uslug}/hosts/code`, `POST /api/hosts/register`.
- Admin endpoints: `POST /admin/clusters/{cslug}/members`, `DELETE /admin/clusters/{cslug}/members/{uslug}`.
- All project-scoped HTTP routes gain `hosts/{hslug}/`: `/api/users/{uslug}/hosts/{hslug}/projects/{pslug}/sessions/{sslug}`.
- Subject-publishing for project-scoped messages uses host-inclusive `pkg/subj` helpers.
- `user_clusters` table removed; replaced by `hosts` rows with `type='cluster'`.
- Reconciler: resolves `HOST_SLUG` from Postgres alongside `USER_SLUG` and `PROJECT_SLUG` when building pod templates.
- NATS JWT signing: host-scoped permissions (`mclaude.users.{uslug}.hosts.{hslug}.>`).
- Device-code storage: `host_registration_codes` table or in-memory map with 10-min expiry.
- Host presence: subscribe to `$SYS.ACCOUNT.*.CONNECT`/`DISCONNECT`, maintain in-memory map, expose via `GET /api/users/{uslug}/hosts` response.

### `mclaude-session-agent`

- Reads `HOST_SLUG` env var (set by reconciler for cluster hosts, or by daemon wrapper for machine hosts).
- All NATS subscriptions use host-inclusive subject shape via `pkg/subj` helpers.
- KV key construction includes `{hslug}`: sessions key = `{uslug}.{hslug}.{pslug}.{sslug}`.
- `SessionState` KV value gains `hostSlug` field.
- `JobEntry` gains `hostSlug` field. Dispatcher uses it for KV key construction.

### `mclaude-cli`

- New subcommand `host`: `register [--name]`, `list`, `rm <hslug>`, `use <hslug>`.
- `~/.mclaude/hosts/{hslug}/` directory per registered host: `nkey.seed`, `nats.creds`, `config.json`.
- `~/.mclaude/context.json` gains `hostSlug` field (already added by ADR-0024).
- `mclaude daemon --host <hslug>` starts daemon scoped to a specific host.
- Flag `-h`/`--host` on session/project commands overrides context default.

### `mclaude-web`

- Host picker dropdown in NavBar (or dashboard header).
- Settings > Hosts screen: list with status (online/offline), "Add Host" flow (device code), per-host actions (rename, remove).
- Dashboard session rows: host badge when viewing "All hosts".
- New Project sheet: host context shown as read-only.
- `src/lib/subj.ts` updated with host-inclusive subject helpers.
- All store NATS subscriptions and KV watches use host-inclusive subjects.
- React Router routes include `{hslug}`: `/users/{uslug}/hosts/{hslug}/projects/{pslug}/sessions/{sslug}`.

### `charts/mclaude`

- NATS permission templates updated for host-scoped grants.
- Signing key ceiling: `mclaude.users.*.hosts.*.projects.*.>` (replaces ADR-0024's `mclaude.users.*.projects.*.>`).
- Host registration code TTL configurable via values.

## Data Model

### Postgres changes

**New table: `hosts`** — see `spec-state-schema.md` for full schema.

Key columns: `id` (UUID PK), `user_id` (FK→users), `slug` (per-user unique), `name` (display, mutable), `type` (`machine`|`cluster`), `role` (`owner`|`user`), `cluster_id` (FK→clusters, NULL for machines), `public_key` (NKey public), `created_at`, `last_seen_at`.

**Modified table: `projects`** — `cluster_id` replaced by `host_id FK→hosts`.

**Removed table: `user_clusters`** — absorbed into `hosts` with `type='cluster'`.

### NATS subject changes (extends ADR-0024)

All project-scoped subjects gain `.hosts.{hslug}.` between user and project:

| ADR-0024 shape | ADR-0004 shape |
|---------------|----------------|
| `mclaude.users.{uslug}.projects.{pslug}.api.sessions.*` | `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.*` |
| `mclaude.users.{uslug}.projects.{pslug}.events.{sslug}` | `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events.{sslug}` |
| `mclaude.users.{uslug}.projects.{pslug}.lifecycle.{sslug}` | `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}` |
| `mclaude.users.{uslug}.projects.{pslug}.api.terminal.*` | `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.*` |

User-level subjects unchanged:
- `mclaude.users.{uslug}.api.projects.create` — payload gains `hostSlug` field
- `mclaude.users.{uslug}.api.projects.updated` — payload gains `hostSlug` field
- `mclaude.users.{uslug}.quota`

New host-level subject:
- `mclaude.users.{uslug}.hosts.{hslug}.status` — host presence heartbeat (machine hosts only; cluster hosts use `$SYS` events)

Cluster infra subjects unchanged:
- `mclaude.clusters.{cslug}.api.projects.provision` — payload gains `hostSlug`
- `mclaude.clusters.{cslug}.api.status`

### KV key changes

| Bucket | ADR-0024 key | ADR-0004 key |
|--------|-------------|-------------|
| `mclaude-sessions` | `{uslug}.{pslug}.{sslug}` | `{uslug}.{hslug}.{pslug}.{sslug}` |
| `mclaude-projects` | `{uslug}.{pslug}` | `{uslug}.{hslug}.{pslug}` |
| `mclaude-hosts` (was `mclaude-laptops`) | `{uslug}.{hostname}` | `{uslug}.{hslug}` |
| `mclaude-job-queue` | `{uslug}.{jobId}` | `{uslug}.{jobId}` (unchanged — jobs are user-scoped) |
| `mclaude-clusters` | `{uslug}` | `{uslug}` (unchanged) |

`mclaude-laptops` renamed to `mclaude-hosts`. Value schema expanded:
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

`SessionState` gains `hostSlug` field. `ProjectState` gains `hostSlug` field. `JobEntry` gains `hostSlug` field.

### JetStream filter changes

| Stream | ADR-0024 filter | ADR-0004 filter |
|--------|----------------|----------------|
| `MCLAUDE_API` | `mclaude.users.*.projects.*.api.sessions.>` | `mclaude.users.*.hosts.*.projects.*.api.sessions.>` |
| `MCLAUDE_EVENTS` | `mclaude.users.*.projects.*.events.*` | `mclaude.users.*.hosts.*.projects.*.events.*` |
| `MCLAUDE_LIFECYCLE` | `mclaude.users.*.projects.*.lifecycle.*` | `mclaude.users.*.hosts.*.projects.*.lifecycle.*` |

### HTTP URL changes

All project-scoped API routes gain `/hosts/{hslug}/`:

| ADR-0024 URL | ADR-0004 URL |
|-------------|-------------|
| `/api/users/{uslug}/projects` | `/api/users/{uslug}/hosts/{hslug}/projects` |
| `/api/users/{uslug}/projects/{pslug}` | `/api/users/{uslug}/hosts/{hslug}/projects/{pslug}` |
| `/api/users/{uslug}/projects/{pslug}/sessions` | `/api/users/{uslug}/hosts/{hslug}/projects/{pslug}/sessions` |
| `/api/users/{uslug}/projects/{pslug}/sessions/{sslug}` | `/api/users/{uslug}/hosts/{hslug}/projects/{pslug}/sessions/{sslug}` |
| `/api/users/{uslug}/jobs` | `/api/users/{uslug}/jobs` (unchanged — jobs are user-scoped) |

New host endpoints:
- `GET /api/users/{uslug}/hosts` — list hosts with presence status
- `POST /api/users/{uslug}/hosts` — register machine host (authed)
- `DELETE /api/users/{uslug}/hosts/{hslug}` — remove host
- `PUT /api/users/{uslug}/hosts/{hslug}` — rename display name
- `POST /api/users/{uslug}/hosts/code` — generate device code
- `POST /api/hosts/register` — complete device-code registration (no auth — code is the credential)

Admin cluster-member endpoints:
- `POST /admin/clusters/{cslug}/members` — grant user access (creates host row)
- `DELETE /admin/clusters/{cslug}/members/{uslug}` — revoke access (deletes host row + NATS revocation)

### NATS permission grant changes

**SPA (browser user)** — JWT minted on login, `sub = {uslug}`:
```
Publish allow:
  mclaude.users.{uslug}.>
  _INBOX.>
Subscribe allow:
  mclaude.users.{uslug}.>
  $KV.mclaude-sessions.>
  $KV.mclaude-projects.>
  $KV.mclaude-hosts.>
  $JS.API.DIRECT.GET.>
  _INBOX.>
Publish deny:
  $KV.>
  $JS.>
  mclaude.system.>
```

**Per-host (machine daemon)** — JWT signed with host-scoped permissions:
```
Publish allow:
  mclaude.users.{uslug}.hosts.{hslug}.>
  mclaude.users.{uslug}.quota
  _INBOX.>
Subscribe allow:
  mclaude.users.{uslug}.hosts.{hslug}.>
  $KV.mclaude-sessions.>
  $KV.mclaude-projects.>
  $KV.mclaude-job-queue.>
  $JS.API.DIRECT.GET.>
  _INBOX.>
```

**Session-agent (cluster)** — JWT minted by cluster signing key:
- Signing key ceiling: `mclaude.users.*.hosts.{hslug}.projects.*.>`
- Per-agent JWT claims narrow to specific user + project.

## Error Handling

| Scenario | Behavior |
|----------|----------|
| Host offline at session-create | HTTP 409 `{code: "host_offline", availableHosts: [...]}`. SPA shows "Host offline" with link to switch. |
| Host disappears mid-session | `$SYS` disconnect event fires. Control-plane marks host offline. Sessions on that host show `state=offline` in KV. SPA shows grayed sessions with "Host disconnected" badge. Sessions resume when host reconnects. |
| Duplicate host name at registration | Slug collision within user scope → numeric suffix (`my-mbp`, `my-mbp-2`). Same as project slugs per ADR-0024. |
| Revoked host credentials | NATS revocation list updated. Server disconnects active connection immediately, rejects reconnects. Daemon logs: "Credentials revoked. Re-register with: mclaude host register". |
| Device code expired | HTTP 410 `{code: "code_expired"}`. User generates a new code. |
| Device code wrong | HTTP 401 `{code: "invalid_code"}`. After 5 failures, code is invalidated. |
| Host removal with running sessions | Confirmation dialog lists running sessions + impact. On confirm: sessions killed, host row deleted, NATS revocation pushed. |
| Cross-host URL access | Middleware compares JWT host scope with URL `{hslug}`. Mismatch for host-scoped JWT → 403. SPA JWT has no host restriction (sees all). |

## Security

- **Per-host credential isolation**: each host gets its own NKey pair + JWT. Credentials scope to `mclaude.users.{uslug}.hosts.{hslug}.>`. A compromised machine host cannot read another host's sessions.
- **NKey generation on host**: private seed never leaves the machine. Control-plane only sees the public key. A CP breach cannot impersonate hosts.
- **Device code**: single-use, 6-char alphanumeric, 10-min TTL, invalidated after 5 failed attempts. Stored server-side only.
- **Host removal = NATS revocation**: not just a DB flag. Account revocation list is updated and pushed to NATS server. Immediate disconnect.
- **Cluster host isolation**: cluster signing key ceiling restricts session-agents to their host's subtree. Even if a session-agent is compromised, it cannot access other hosts on the same cluster.
- **Admin operations audited**: host grants/revocations logged with admin uslug + target uslug.

## Impact

Specs updated in this ADR's co-commit:

- `docs/spec-state-schema.md` — add `hosts` table, replace `user_clusters` with hosts, add `host_id` to projects, update all subject/KV/URL inventories with host level, rename `mclaude-laptops` to `mclaude-hosts`.
- `docs/adr-0011-multi-cluster.md` — status note: partially superseded by ADR-0004.
- `docs/adr-0024-typed-slugs.md` — status note: extended by ADR-0004 (host level in subjects).

Components implementing: `mclaude-control-plane`, `mclaude-session-agent`, `mclaude-cli`, `mclaude-web`, `charts/mclaude`.

Downstream: ADR-0011 infrastructure topology is preserved. ADR-0016 (NATS security) signing key ceiling updated from `mclaude.users.*.projects.*.>` to `mclaude.users.*.hosts.*.projects.*.>`.

## Scope

In v1:
- `hosts` table with machine + cluster types.
- Host registration: direct API (authed CLI) + device-code flow (unauthed CLI / web-initiated).
- Per-host NATS credentials with host-scoped permissions.
- Host-inclusive subject scheme for all project-scoped subjects, KV keys, HTTP URLs.
- Dashboard host picker with online/offline status.
- Settings > Hosts screen (list, add, rename, remove).
- Cluster hosts via admin grant (existing cluster infra from ADR-0011).
- Force re-register migration for existing daemons.

Deferred:
- Session migration across hosts.
- Team-shared hosts (one host usable by multiple users outside cluster model).
- Auto-discovery (mDNS, Tailscale magic DNS).
- Per-host quotas / cost accounting.
- Host health metrics dashboard.
- `hosts` table in BYOH: session-level host targeting (today sessions always run on the project's host).

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| **mclaude-common** | ~150 | ~40k | Add `HostSlug` type to pkg/subj, update all subject helpers to accept host param, add `hosts` to reserved words in pkg/slug |
| **mclaude-control-plane** | ~1,500 | ~100k | hosts table + migration, host registration endpoints, device-code flow, project routes restructured with host level, NATS JWT host-scoped signing, reconciler HOST_SLUG env, presence tracking |
| **mclaude-session-agent** | ~600 | ~70k | HOST_SLUG env var ingestion, subscription rewrites with host level, KV key format with hslug, SessionState/JobEntry hostSlug field |
| **mclaude-web** | ~1,000 | ~80k | Host picker component, Settings > Hosts screen, device-code UI, subj.ts host helpers, store/viewmodel host-scoping, route restructuring |
| **mclaude-cli** | ~500 | ~50k | `host` subcommand (register/list/rm/use), NKey generation, creds storage, daemon --host flag, context.json hostSlug |
| **charts/mclaude** | ~200 | ~30k | NATS permission templates with host scope, signing key ceiling update, mclaude-hosts KV creation |

**Total estimated tokens:** ~370k
**Estimated wall-clock:** ~2.5h of 5h budget (50%). mclaude-common lands first (sequential); remaining components land in parallel.

## Open questions

_All resolved — see Decisions table._
