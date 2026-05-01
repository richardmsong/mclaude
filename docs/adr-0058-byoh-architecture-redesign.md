# ADR: BYOH Architecture Redesign — Per-Project Agent Model

**Status**: draft
**Status history**:
- 2026-04-30: draft

> Depends on:
> - `adr-0054-nats-jetstream-permission-tightening.md` — defines the per-project agent JWT model, host subject scheme (`mclaude.hosts.{hslug}.>` superseding ADR-0035's `mclaude.users.{uslug}.hosts.{hslug}.>`), fan-out provisioning model, unified HTTP challenge-response credential protocol, and zero-JetStream host controller. This ADR adopts all of ADR-0054's decisions for BYOH hosts.
> - `adr-0044-scheduling-and-quota.md` — designs the quota system: designated-agent publisher, per-session `QuotaMonitor`, `mclaude.users.{uslug}.quota` subject, elimination of job queue KV. This ADR adopts ADR-0044's quota design and extends it to the per-project agent model.
> - `adr-0035-unified-host-architecture.md` — defines the unified host identity model (`hosts` table, `mclaude-controller-local` binary, daemon mode). This ADR supersedes the daemon mode portion of ADR-0035.

## Overview

Redesign BYOH (Bring Your Own Host) session management to replace the single-process daemon mode with a host-controller + per-project-agent architecture that mirrors the K8s model. The current daemon mode in `mclaude-session-agent` uses a single NATS connection with cross-project JetStream access, which is incompatible with ADR-0054's per-project credential scoping. The quota system (designed by ADR-0044) works unchanged with per-project agents — the `mclaude.users.{uslug}.quota` subject is included in per-project agent JWT permissions per ADR-0054.

## Motivation

### 1. Daemon mode breaks per-project isolation

The `mclaude-session-agent --daemon` mode runs as a single long-lived process on BYOH hosts. It:
- Holds a single set of NATS credentials (the host JWT)
- Opens all KV buckets for all projects on the host
- Watches all sessions across all projects
- Publishes quota updates and lifecycle events for all sessions

ADR-0054 introduces per-project agent JWTs: each session-agent gets a JWT scoped to exactly one project's KV keys and subjects. The daemon's cross-project access pattern cannot be expressed in a single per-project JWT — it is fundamentally incompatible.

### 2. Quota subject compatibility resolved by ADR-0054 + ADR-0044

The quota subject `mclaude.users.{uslug}.quota` initially appeared incompatible with the new permission model because it has no `.hosts.` or `.projects.` tokens. However, ADR-0054's per-project agent JWT permissions explicitly include `mclaude.users.{uslug}.quota` in both Pub.Allow and Sub.Allow. ADR-0044 designs the full quota lifecycle: the designated agent publishes quota status, and all per-project `QuotaMonitor` goroutines subscribe. The subject works within the scoped permission model — no redesign needed. The host controller is uninvolved (zero JetStream, zero quota responsibility).

### 3. Architectural inconsistency

On K8s, the controller (`mclaude-controller-k8s`) manages session lifecycle by reconciling `MCProject` CRs, and each session runs as a separate pod with its own agent and per-project JWT. On BYOH, the daemon collapses controller + agent into a single process. This divergence means:
- Different code paths for session lifecycle management
- Different credential models (host JWT vs. per-project JWT)
- Different failure modes (daemon crash kills all sessions; K8s pod crash kills one)

## Current Architecture

```
┌─────────────────────────────────────────────────┐
│  BYOH Host (laptop-a)                           │
│                                                 │
│  ┌─────────────────────────────────────────┐    │
│  │  mclaude-session-agent --daemon         │    │
│  │                                         │    │
│  │  Single NATS connection (host JWT)      │    │
│  │  ├── Watches ALL project KV buckets     │    │
│  │  ├── Manages ALL sessions on host       │    │
│  │  ├── Publishes quota for ALL sessions   │    │
│  │  └── Publishes lifecycle for ALL        │    │
│  └─────────────────────────────────────────┘    │
└─────────────────────────────────────────────────┘
```

- `mclaude-session-agent` binary has two modes: **agent mode** (per-project, used in K8s) and **daemon mode** (BYOH, cross-project)
- Daemon uses host NATS credentials, opens shared KV buckets, manages all sessions on the host
- Quota is published by daemon on `mclaude.users.{uslug}.quota`, subscribed by agents
- Lifecycle events published by daemon on user-scoped subjects

## Target Architecture (Sketch)

```
┌──────────────────────────────────────────────────────┐
│  BYOH Host (laptop-a)                                │
│                                                      │
│  ┌──────────────────────────────────────┐            │
│  │  Host Controller                     │            │
│  │  NATS conn: host JWT                 │            │
│  │  (mclaude.hosts.{hslug}.>)           │            │
│  │                                      │            │
│  │  ├── Receives provisioning (fan-out) │            │
│  │  ├── Registers agent NKeys with CP   │            │
│  │  ├── Spawns per-project agents       │            │
│  │  └── Zero JetStream access           │            │
│  └──────────────────────────────────────┘            │
│                                                      │
│  ┌──────────────┐  ┌──────────────┐                  │
│  │ Agent (proj1) │  │ Agent (proj2) │  ...           │
│  │ JWT: proj1    │  │ JWT: proj2    │                │
│  │ scope only    │  │ scope only    │                │
│  └──────────────┘  └──────────────┘                  │
└──────────────────────────────────────────────────────┘
```

- **Host controller** (`mclaude-controller-local`) manages session lifecycle (analogous to `mclaude-controller-k8s` but for processes instead of pods)
- Per-project agents spawned as **subprocesses** with per-project JWTs (matching K8s model — one process = one project; sessions run within the per-project agent)
- Host controller uses host JWT scoped to `mclaude.hosts.{hslug}.>` (ADR-0054 scheme, superseding ADR-0035's `mclaude.users.{uslug}.hosts.{hslug}.>`) — zero JetStream access
- Host controller receives provisioning via **fan-out**: SPA/CLI sends HTTP request to CP, CP validates and then **CP publishes** `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.{create,delete}` to NATS; the host controller receives via its `mclaude.hosts.{hslug}.>` subscription (ADR-0054)
- **Quota system** per ADR-0044: each per-project agent subscribes to `mclaude.users.{uslug}.quota` (core NATS). The designated agent runs the quota publisher (polls Anthropic API). Per-session `QuotaMonitor` goroutines within each agent handle enforcement. No separate quota redesign needed — ADR-0044's design applies unchanged to per-project agents

## Resolved Questions

> **All questions below are RESOLVED** using decisions already made in ADR-0054, ADR-0044, and existing codebase state. Resolutions are recorded inline.

### Q1: How does the BYOH host controller spawn per-project agents?

**Decision: Subprocess per project.**

The host controller `exec`s a new `mclaude-session-agent` process for each project. This matches the K8s model (one pod = one project), provides clean isolation (a crash in one project's agent cannot affect others), and is already implemented in `mclaude-controller-local/supervisor.go` (process supervision with restart-on-crash, children keyed by `projectSlug`). The agent generates its own NKey pair at startup and passes the public key to the host controller via local IPC (stdout/file). Sessions within a project run inside the per-project agent process. This aligns with ADR-0054's per-project scoping: `agent_credentials` has `UNIQUE(user_id, host_slug, project_slug)` — one credential per project, not per session.

Goroutine-per-project was rejected: a panic in one goroutine tears down the entire process, which would kill all projects on the host. This violates the isolation property that motivates the redesign.

### Q2: How does the host controller manage session lifecycle?

**Decision: NATS fan-out provisioning per ADR-0054.**

The host controller subscribes to `mclaude.hosts.{hslug}.>` (ADR-0054 host-scoped scheme, superseding ADR-0035's `mclaude.users.{uslug}.hosts.{hslug}.>`). SPA/CLI uses the existing HTTP endpoint `POST /api/users/{uslug}/projects` to request project operations. CP validates the request (authorization, slug uniqueness, host assignment), creates Postgres records, writes project KV, and then **CP itself publishes** `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.{create,delete}` to NATS. The host controller receives this message via its subscription and starts/stops the agent subprocess. Users do not publish directly to host-scoped NATS subjects — project creation remains an HTTP-to-CP-to-NATS flow per ADR-0054.

There is no job queue KV — it was eliminated by ADR-0044 (quota-managed sessions use the session KV with extended fields). The host controller does not watch any KV bucket for lifecycle management; all provisioning flows through NATS core subjects.

**Note:** The existing `mclaude-controller-local/controller.go` currently subscribes to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` (the ADR-0035 scheme). This must be updated to `mclaude.hosts.{hslug}.>` per ADR-0054.

### Q3: Where does quota reporting live?

**Decision: Designated-agent quota publisher per ADR-0044.**

CP designates exactly one agent per user as the quota publisher. The designated agent's `runQuotaPublisher` goroutine polls `api.anthropic.com/api/oauth/usage` every 60 seconds and publishes `QuotaStatus` to `mclaude.users.{uslug}.quota` (core NATS, no JetStream retention). All other per-project agents subscribe to this subject and run independent per-session `QuotaMonitor` goroutines that evaluate `u5 >= softThreshold` for each session they manage.

This model works unchanged with per-project agents — each agent subscribes to the quota subject via its per-project JWT (ADR-0054 session-agent permissions include `mclaude.users.{uslug}.quota` in both Pub.Allow and Sub.Allow). No host-level aggregation needed; no KV-based approach needed.

### Q4: Who is the authoritative source for quota?

**Decision: Edge-reported usage, per-session enforcement within per-project agents per ADR-0044.**

The Anthropic OAuth usage API (`api.anthropic.com/api/oauth/usage`) is the authoritative source for the 5-hour rolling utilization window (`u5`). The designated agent polls it and broadcasts `QuotaStatus` to all agents. Each per-project agent runs per-session `QuotaMonitor` goroutines that independently enforce `softThreshold` against the broadcast `u5` value for each session they manage. There is no CP-side quota enforcement — CP does not poll usage. The staleness window is the 60-second polling interval. This is the ADR-0044 design, unchanged.

### Q5: Does quota need cross-host aggregation?

**Decision: No cross-host aggregation needed — the Anthropic API is the single source of truth.**

The Anthropic OAuth usage API returns the account-wide 5-hour utilization (`u5`), which already aggregates all API calls across all hosts and sessions. The designated agent polls this single endpoint. All agents on all hosts receive the same `QuotaStatus` broadcast via `mclaude.users.{uslug}.quota` (core NATS, routed through the hub to all connected agents regardless of host). No distributed counter or cross-host coordination is needed — the API is the aggregation point. Staleness window is the 60-second polling interval (ADR-0044).

### Q6: How does the host controller request agent credentials from CP?

**Decision: ADR-0054 unified credential protocol — settled, no open sub-questions.**

Per ADR-0054's definitive agent credential model:
1. Agent generates its own NKey pair at startup (private seed never leaves the agent process)
2. Agent passes its public key to the host controller via local IPC (stdout/file)
3. Host controller registers the public key with CP via `mclaude.hosts.{hslug}.api.agents.register` (NATS request/reply). If CP returns `NOT_FOUND` (project create not yet processed — fan-out race), retry with exponential backoff (100ms initial, doubling, max 5s, max 10 attempts)
4. Agent authenticates itself via HTTP challenge-response (`POST /api/auth/challenge` + `POST /api/auth/verify`) to get its per-project JWT
5. No credential handoff between controller and agent — the controller never touches the agent's JWT or private key

**Credential refresh:** The agent manages its own JWT refresh. Agent JWTs have a 5-minute TTL per ADR-0054. The agent runs the same HTTP challenge-response flow before TTL expiry (proactive refresh). On `permissions violation` error, the agent triggers an immediate refresh + retry. The host controller is not involved in agent credential refresh.

**Pre-fetch vs on-demand:** On-demand — the controller registers the agent's public key only after the agent subprocess starts and exposes its NKey public key. There is no pre-fetching.

### Q7: What replaces the daemon's KV watches?

**Decision: Host controller has zero JetStream access; per-project agents watch their own project KV.**

Under the new model per ADR-0054:
- **Host controller** has zero JetStream access — no `$JS.*`, `$KV.*`, or `$O.*` subjects in its JWT. It uses only NATS core pub/sub (`mclaude.hosts.{hslug}.>`) for provisioning commands. Provisioning arrives via fan-out, not KV watches.
- **Per-project agents** watch their own project's KV (`mclaude-sessions-{uslug}` bucket, scoped to `hosts.{hslug}.projects.{pslug}.sessions.>` key prefix) using their per-project JWT. This is identical to the K8s model.

The job queue KV (`mclaude-job-queue-{uslug}`) no longer exists — ADR-0044 eliminated it. Quota-managed sessions use the session KV with extended fields (e.g., `softThreshold`, `hardHeadroomTokens`, `autoContinue`). The multi-user scoping question is moot because: (a) the job queue doesn't exist, and (b) the host controller doesn't watch any KV buckets.

### Q8: What component is the host controller?

**Decision: `mclaude-controller-local` (existing binary from ADR-0035).**

The existing `mclaude-controller-local` binary already implements the host controller role:
- `controller.go`: subscribes to project API subjects, handles provisioning/delete/update requests via NATS request/reply
- `supervisor.go`: process supervision with restart-on-crash for session-agent subprocesses
- Used by `mclaude-cli/cmd/daemon.go` as the BYOH process supervisor

This binary is extended with:
1. **ADR-0054 subject scheme**: update subscription from `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` to `mclaude.hosts.{hslug}.>` (fan-out provisioning)
2. **Agent credential registration**: on project provisioning, register the spawned agent's NKey public key with CP via `mclaude.hosts.{hslug}.api.agents.register`
3. **Host credential refresh**: timer-based HTTP challenge-response before 5-min JWT TTL expiry

`mclaude-server` is not the right home — it manages the launchd service lifecycle, not session lifecycle. A new binary is unnecessary — the existing code already does 90% of the work.

## Dependencies

| ADR | Relationship |
|-----|-------------|
| ADR-0054 (NATS JetStream permission tightening) | **Prerequisite.** Defines the per-project agent JWT model, host subject scheme (`mclaude.hosts.{hslug}.>`), fan-out provisioning, unified HTTP credential protocol, and zero-JetStream host controller. Necessitates this redesign. |
| ADR-0044 (Session scheduling and quota management) | **Prerequisite.** Designs the quota system (designated-agent publisher, per-session `QuotaMonitor`, `mclaude.users.{uslug}.quota` subject). Eliminates the job queue KV. This ADR adopts ADR-0044's quota design for per-project agents. |
| ADR-0035 (Unified host architecture) | **Context.** Defines the current host identity model, `mclaude-controller-local`, and daemon mode. This ADR supersedes the daemon mode portion. |

## Daemon Mode Deprecation Plan

The `mclaude-session-agent --daemon` mode (implemented in `daemon.go`, ~396 lines, 5 goroutines) is replaced by the `mclaude-controller-local` + per-project-agent architecture. Deprecation proceeds in three phases:

### Phase 1: Parallel availability (this ADR's implementation)

- `mclaude-controller-local` is extended with ADR-0054 subject scheme, agent credential registration, and host credential refresh (see Q8 resolution)
- `mclaude-session-agent --daemon` continues to exist in the codebase but is not actively maintained
- `mclaude daemon` CLI command is updated to launch `mclaude-controller-local` instead of `mclaude-session-agent --daemon`
- Users who have existing launchd/systemd units pointing at `mclaude-session-agent --daemon` continue to work until ADR-0054's permission tightening is deployed

### Phase 2: Hard cut-over (ADR-0054 deployment)

- ADR-0054 deployment tightens NATS permissions: host JWTs get zero JetStream access, agent JWTs are per-project scoped
- `mclaude-session-agent --daemon` stops working — its cross-project JetStream access pattern is denied by the new permissions
- All BYOH hosts must use `mclaude-controller-local` + per-project agents
- This is the same clean cut-over as ADR-0054's migration strategy (no rolling upgrade)

### Phase 3: Code removal

- Remove `daemon.go`, `daemon_jobs.go`, and all daemon-specific code from `mclaude-session-agent`
- Remove `--daemon` flag from `mclaude-session-agent` CLI
- Remove daemon-related goroutines: `runJWTRefresh`, `runQuotaPublisher`, `runLifecycleSubscriber`, `runJobDispatcher`, `runJobsHTTP`
- The `runQuotaPublisher` goroutine is the exception — it moves to the designated per-project agent (per ADR-0044's quota publisher designation model), not removed entirely

**No migration of in-flight sessions.** Sessions running under daemon mode at cut-over time are terminated. Users re-create them under the new architecture. This is acceptable for the current user base (single-user, no production traffic).

## Scope

**In scope:**
- BYOH host controller design (lifecycle, credential management, agent spawning) — extends `mclaude-controller-local`
- Quota system integration with ADR-0044 (designated-agent publisher, per-session QuotaMonitor within per-project agents)
- Daemon mode deprecation path (three phases: parallel → hard cut-over → code removal)
- Subject scheme update from ADR-0035 to ADR-0054 (`mclaude.hosts.{hslug}.>` with fan-out provisioning)

**Out of scope:**
- K8s controller changes (already uses per-project model)
- NATS permission changes (covered by ADR-0054)
- Session import (covered by ADR-0053)
- Quota system redesign (not needed — ADR-0044's design applies unchanged)
