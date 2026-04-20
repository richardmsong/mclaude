# ADR: Bring Your Own Host (BYOH)

**Status**: draft
**Status history**:
- 2026-04-10: accepted
- 2026-04-19: reverted to draft — retroactive accepted tag incorrect; implementation not confirmed, needs per-ADR review
- 2026-04-20: rewritten against current NATS + control-plane + session-agent architecture — original body described the superseded relay/connector topology (see ADR-0003)
- 2026-04-20: paused pending slug-scheme overhaul ADR — BYOH inherits the new typed-literal subject/URL scheme from that ADR rather than specifying its own

> **Blocked on**: pending slug-scheme ADR. Subject examples in this doc use the target scheme (`mclaude.users.{uid}.hosts.{hid}.projects.{pid}.*`) but the exact charset, slugification rules, and reserved literals are fixed by the slug ADR. Resume this ADR via `/plan-feature --resume multi-laptop` after the slug ADR lands.

> File slug remains `adr-0004-multi-laptop.md` for link stability; the concept generalizes from "laptop" to any user-owned host (single machine or K8s cluster).

## Overview

Let a single user attach one or more of their **own hosts** to the mclaude control plane. A *host* is any machine that can run `mclaude-session-agent` and reach central NATS — either:

1. **Single machine** — a laptop, desktop, or cloud VM running the session-agent daemon locally. (decision pending: authoritative list of types in v1)
2. **K8s cluster** — a worker cluster running session-agent pods, leaf-noded into the hub NATS per ADR-0011.

All hosts appear as peers under the same user. Projects and sessions are scoped to a host; the SPA/CLI let the user target a specific host when creating a session and see which host a running session lives on.

This ADR supersedes the original "multi-laptop via tunnel map on relay" design. That design is dead — the relay was replaced by NATS subject routing (ADR-0003), and the k8s side is already covered by ADR-0011.

## Motivation

Users have more than one machine where they want to run Claude:

- A work MBP and a personal MBP (different codebases, different credentials).
- A laptop for interactive work and a beefy desktop/VM for long-running agent tasks.
- A K8s cluster for shared/team workloads plus a personal laptop for local repos.

Today the system models only two host shapes: "the daemon" (one per user) and "the cluster" (multiple supported via ADR-0011 but clusters are admin-owned, not user-owned). There is no first-class concept of a user-owned host, no registration flow, and no UI affordance for picking one.

Without BYOH, users either run one laptop-daemon and lose their other machines, or they have to manually swap credential files and restart.

## Architecture

```
                       ┌─────────────────────────────┐
                       │   mclaude-control-plane     │
                       │   (central, Postgres+NATS)  │
                       └───────────┬─────────────────┘
                                   │ (issues per-host NATS creds, tracks host registry)
          ┌────────────────────────┼─────────────────────────┐
          │                        │                         │
   ┌──────▼──────┐          ┌──────▼──────┐           ┌──────▼──────┐
   │ Host: mbp16 │          │ Host: mbp14 │           │ Host: k8s-a │
   │ (laptop)    │          │ (laptop)    │           │ (cluster,   │
   │ daemon      │          │ daemon      │           │  leaf node) │
   └─────────────┘          └─────────────┘           └─────────────┘
```

- Each host has its own identity (`hostId`) and its own NATS credentials.
- NATS subjects use a **split namespace**:
  - User-scoped APIs: `mclaude.{userId}.api.*` (no hostId — register host, list hosts, user profile, quota)
  - Host-scoped APIs/events: `mclaude.{userId}.hosts.{hostId}.{projectId}.api.*` and `.events.*`
- Per-host NATS credentials grant `mclaude.{userId}.hosts.{hostId}.>` — a compromised host cannot read another host's traffic for the same user.
- User (SPA) credentials grant `mclaude.{userId}.>` — covers both subtrees in one subscription.
- SPA and CLI discover hosts via the user-scoped `mclaude.{userId}.api.hosts.list` subject, fronted by the control-plane.
- Projects are host-scoped in v1. A project belongs to exactly one host; the same git repo cloned on two hosts shows as two projects.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Host is a first-class concept | Yes — named `host`, not `laptop` | Covers laptops, desktops, VMs, and K8s clusters uniformly. Per user feedback 2026-04-20: "BYOH can mean single machines (laptop/cloud VM) or k8s cluster." |
| Relationship to ADR-0011 multi-cluster | Unify: `host` is the abstraction; `type ∈ {machine, cluster}` | A cluster host is implemented per ADR-0011 (leaf-node, session-agent pods); a machine host runs the daemon. Same registry, same UI, same subject schema. Supersedes ADR-0011's separate cluster registry. |
| NATS subject namespacing | Split namespace with typed literals: user-scoped at `mclaude.users.{uid}.api.*`; host-scoped at `mclaude.users.{uid}.hosts.{hid}.projects.{pid}.api.*` | Per-host NATS credentials scope to `mclaude.users.{uid}.hosts.{hid}.>` for strong isolation. Typed literals (`users`, `hosts`, `projects`) make subjects self-describing and defend against subject injection (see slug-scheme ADR). 8–10 tokens, ~250 chars — under NATS limits (recommended ≤16 tokens, ≤256 chars; hard `MAX_CONTROL_LINE` default 4096 bytes). |
| Host registration flow | Web-initiated device code | User clicks "Add Host" in web Settings → code appears → user runs `mclaude-cli host register <code>` on the target machine → control-plane issues NATS credentials. No admin involvement. |
| Project scoping | Per-host | Project = `(userId, hostId, path)`. Same repo cloned on two hosts = two project rows. Simplest model; no cross-host state sync. |
| Host types supported in v1 | Machine + cluster from day one | Full unification. Existing ADR-0011 cluster records migrate into `hosts` with `type='cluster'`. Prevents a half-unified interim state. |
| Liveness/presence tracking | NATS connection presence via `$SYS.ACCOUNT.*.CONNECT`/`DISCONNECT` events | Zero extra traffic. Control-plane maintains in-memory presence map + KV mirror. Requires system-account access (control-plane already has it). |
| Default host selection in UI | Last-used per-user, stored in user KV | Matches expected behavior for a user who primarily works on one host. Fallback when last-used is offline: (decision pending — see Open Questions). |
| Existing daemon migration | Force explicit re-register | Clean break. Legacy subject shape (`mclaude.{userId}.{projectId}.*`) is removed. Daemons without host credentials are rejected with a clear error directing to `mclaude-cli host register`. |
| Key minting | Host generates NKey locally; submits public key via device-code flow; control-plane signs JWT | Private seed never leaves the host. A control-plane breach cannot impersonate hosts. Idiomatic NATS pattern. |
| Host removal | Immediate hard-kill with confirmation dialog. Revocation list pushed to NATS (not just DB-marked). | Confirmation dialog lists running sessions. On confirm: control-plane adds host's public NKey to the account's **NATS revocation list** with `revoked_at=now` and pushes the account update. NATS server disconnects the active host connection and rejects any reconnect attempt (JWTs issued before `revoked_at` fail validation). Revocation is not merely a DB flag — it's a NATS account claim update, without which the host could reconnect freely. |
| Offline fallback | Show empty dashboard scoped to last-used host with an "Offline — switch host" banner and dropdown | Preserves the user's mental scope. User explicitly opts in to "All" or another host, rather than the app silently moving them. |

## User Flow

(decision pending — fill out after Q&A resolves the registration/selection questions)

1. User logs in to mclaude.
2. User goes to Settings → Hosts and clicks "Add Host".
3. (decision pending: device-flow token, QR code, CLI command with paste-back?)
4. User runs `mclaude-cli host register <code>` (or equivalent) on the target machine.
5. The new host appears in the Hosts list.
6. When creating a session, the user picks a host from a dropdown (default: last used).
7. Sessions list shows a badge/column with the originating host.

## Component Changes

### `mclaude-control-plane`

- New host registry table in Postgres: `hosts(id, user_id, name, type, created_at, last_seen_at, ...)`.
- New NATS API subjects: (decision pending — e.g. `mclaude.{userId}.api.hosts.{create|list|delete}`).
- Issues per-host NATS credentials with subject permissions scoped to that host's subtree.
- Updates existing project/session subjects to include `hostId` (exact scheme: decision pending).

### `mclaude-session-agent`

- Daemon mode reads `hostId` from a config file or env var, includes it in every published subject.
- Cluster mode (K8s pods) inherits `hostId` from the cluster's leaf-node configuration.
- Publishes a periodic heartbeat to a known subject or writes to a `hosts_live` KV bucket (decision pending).

### `mclaude-cli`

- New subcommand `host` (or `hosts`) with: `register <code>`, `list`, `rm <id>`, `use <id>` (default target for new sessions from CLI).
- Stores registered-host state in `~/.mclaude/hosts.json` or similar (decision pending).

### `mclaude-web`

- Settings → Hosts screen: list with status (online/offline), "Add Host" flow, per-host actions.
- Session creation sheet: host picker dropdown.
- Dashboard session rows: host badge (or column when multiple hosts exist).
- Session Detail header: shows which host the session runs on.

## Data Model

(decision pending — depends on subject-namespacing decision)

Candidate Postgres table:
```sql
CREATE TABLE hosts (
  id         TEXT PRIMARY KEY,         -- e.g. "mbp16" or "cluster-a"
  user_id    TEXT NOT NULL REFERENCES users(id),
  name       TEXT NOT NULL,             -- user-facing label
  type       TEXT NOT NULL,             -- 'machine' | 'cluster'
  created_at TIMESTAMPTZ NOT NULL,
  last_seen_at TIMESTAMPTZ,             -- updated by heartbeat
  UNIQUE (user_id, name)
);
```

Candidate NATS KV bucket `hosts_live` keyed by `{userId}.{hostId}` with TTL for presence. (decision pending)

## Error Handling

(decision pending — populate after other decisions settle)

- Host offline at session-create time → ?
- Host disappears mid-session → ?
- Duplicate host name registration → ?
- Revoked credentials → ?

## Security

- Per-host NATS credentials scoped to only that host's subjects (deny cross-host access).
- Host registration code is single-use, short-TTL (decision pending: exact TTL).
- Removing a host revokes its NATS credentials immediately.

(decision pending — does a host's credentials give access to other users' data? Expected answer: no, scoped to userId subtree, but spell out.)

## Impact

Specs touched in the ADR co-commit (decision pending — finalized after Q&A):

- `docs/spec-state-schema.md` — add `hosts` Postgres table, new KV buckets, subject namespace changes
- `docs/ui/mclaude-web/spec-settings-web.md` — add Hosts section
- `docs/ui/mclaude-web/spec-dashboard.md` — host badge/column, host filter
- `docs/ui/mclaude-web/spec-session-detail.md` — host indicator
- `docs/feature-list.md` — add BYOH feature ID

Components implementing:
- `mclaude-control-plane`, `mclaude-session-agent`, `mclaude-cli`, `mclaude-web`, possibly `charts/mclaude` for the cluster-leaf topology.

## Scope

In v1 (decision pending — locking once Q&A finishes):
- Single-machine daemon hosts (laptop/VM).
- K8s cluster hosts (if the ADR-0011 relationship resolves to "extend").
- Host registration via CLI code-paste flow.
- Host picker in web UI for session creation.
- Per-host session list filter.

Deferred (decision pending):
- Session migration across hosts.
- Team-shared hosts (one host usable by multiple users).
- Auto-discovery (mDNS, Tailscale magic DNS).
- Per-host quotas / cost accounting.

## Open questions

- **Slug scheme overhaul**: cross-cutting decision raised by user — current `{userId}/{hostId}/{projectId}` positional tokens in subjects and URLs are hard to grok at a glance. Candidates: typed-literals between slugs (`users/{id}/hosts/{id}/projects/{id}`), prefixed slugs (`u_{id}`, `h_{id}`, `p_{id}`), or status quo. Affects subject schema, URL schema, and spec-state-schema. May warrant a companion ADR.
- **Device-code specifics**: code length, TTL, delivery (paste-only vs QR code), control-plane endpoint discovery by the CLI (env var, hardcoded in CLI, prompt on first run).
- **Host removal with running sessions**: the revocation is hard-kill; do we block the remove with a confirmation dialog listing active sessions, or silently proceed?
- **ADR-0011 data migration**: existing cluster registry rows — who owns them post-migration (org-wide admin vs per-user), and what happens to cluster hosts that are "shared" across users?
- **Host rename**: can the user rename a host after registration? If so, does the hostId (subject token) change (needs migration of every subscription and project row), or only a mutable display name?
- **Resolved — tracked in Decisions table above**: target topology, ADR-0011 relationship, subject namespacing, registration flow, project scoping, v1 host types, liveness, default host, existing daemon migration, key minting, revocation mechanism, offline fallback.

## Implementation Plan

(decision pending — estimated after design Q&A completes)

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| TBD       | TBD                      | TBD                       | TBD   |
