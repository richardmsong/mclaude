# ADR: BYOH Architecture Redesign — Per-Session Agent Model

**Status**: draft
**Status history**:
- 2026-04-30: draft

> Depends on:
> - `adr-0054-nats-jetstream-permission-tightening.md` — defines the per-project agent JWT model and host subject scheme (`mclaude.hosts.{hslug}.>`) that this ADR extends to BYOH hosts.
> - `adr-0035-unified-host-architecture.md` — defines the unified host identity model (`hosts` table, `mclaude-controller-local` binary, daemon mode).

## Overview

Redesign BYOH (Bring Your Own Host) session management to replace the single-process daemon mode with a host-controller + per-session-agent architecture that mirrors the K8s model. The current daemon mode in `mclaude-session-agent` uses a single NATS connection with cross-project JetStream access, which is incompatible with ADR-0054's per-project credential scoping. Additionally, redesign the quota reporting system (`mclaude.users.{uslug}.quota`) whose subject structure is incompatible with the new permission model.

## Motivation

### 1. Daemon mode breaks per-project isolation

The `mclaude-session-agent --daemon` mode runs as a single long-lived process on BYOH hosts. It:
- Holds a single set of NATS credentials (the host JWT)
- Opens all KV buckets for all projects on the host
- Watches all sessions across all projects
- Publishes quota updates and lifecycle events for all sessions

ADR-0054 introduces per-project agent JWTs: each session-agent gets a JWT scoped to exactly one project's KV keys and subjects. The daemon's cross-project access pattern cannot be expressed in a single per-project JWT — it is fundamentally incompatible.

### 2. Quota subject doesn't fit the permission model

The current quota subject `mclaude.users.{uslug}.quota` has no `.hosts.` or `.projects.` tokens. Under ADR-0054's permission model:
- **Host JWTs** are scoped to `mclaude.hosts.{hslug}.>` — they cannot publish to `mclaude.users.{uslug}.quota`
- **Agent JWTs** are scoped to per-project subjects — they cannot publish to a user-wide subject
- **User JWTs** could subscribe, but who publishes?

The quota system needs a subject structure that fits within the scoped permission model.

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

- `mclaude-session-agent` binary has two modes: **agent mode** (per-session, used in K8s) and **daemon mode** (BYOH, cross-project)
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
│  │  ├── Watches job queue for its host  │            │
│  │  ├── Requests agent credentials      │            │
│  │  │   from CP                         │            │
│  │  ├── Spawns per-session agents       │            │
│  │  └── Reports host-level health       │            │
│  └──────────────────────────────────────┘            │
│                                                      │
│  ┌──────────────┐  ┌──────────────┐                  │
│  │ Agent (proj1) │  │ Agent (proj2) │  ...           │
│  │ JWT: proj1    │  │ JWT: proj2    │                │
│  │ scope only    │  │ scope only    │                │
│  └──────────────┘  └──────────────┘                  │
└──────────────────────────────────────────────────────┘
```

- Host controller manages session lifecycle (analogous to `mclaude-controller-k8s` but for processes instead of pods)
- Per-session agents spawned as subprocesses with per-project JWTs (matching K8s model)
- Host controller uses host JWT (`mclaude.hosts.{hslug}.>`) for control-plane communication
- Quota system TBD — requires separate design

## Open Questions

> **All questions below are OPEN — no decisions have been made.**

### Q1: How does the BYOH host controller spawn per-session agents?

Options under consideration:
- **Subprocess per session**: host controller `exec`s a new `mclaude-session-agent` process for each session, passing per-project JWT via env/file. Matches K8s model (one pod = one process). Clean isolation. Higher overhead per session.
- **Goroutine with separate NATS connection**: host controller spawns a goroutine per session, each with its own NATS connection using the per-project JWT. Lower overhead, but a crash in one goroutine could affect others. Less isolation.

### Q2: How does the host controller manage session lifecycle?

Options under consideration:
- **Watch job queue KV**: controller watches `mclaude-job-queue-{uslug}` for entries targeting its host. Similar to current daemon pattern but with host-scoped credentials.
- **Receive NATS commands**: controller subscribes to `mclaude.hosts.{hslug}.api.sessions.>` and receives explicit create/stop/delete commands from the control-plane.
- **Hybrid**: KV for durable state, NATS commands for real-time signals.

### Q3: Where does quota reporting live?

Options under consideration:
- **Per-agent reports to CP**: each session-agent reports its own token usage to the control-plane via a project-scoped subject. CP aggregates.
- **Host-level aggregation**: host controller collects usage from local agents and reports a single aggregate to CP.
- **KV-based**: agents write usage to per-user KV keys; CP reads on demand.

### Q4: Who is the authoritative source for quota?

- **CP sets limits**: control-plane knows billing/plan limits from Postgres.
- **Edge reports usage**: session-agents make the API calls and know exact token counts.
- **Problem**: CP can't poll 10k+ users' real-time usage. Edge agents know usage but not limits.
- **Needs design**: push vs. pull, aggregation strategy, staleness tolerance.

### Q5: Does quota need cross-host aggregation?

A user may have sessions on `laptop-a` (BYOH) and `us-east` (K8s cluster) sharing the same quota pool. Questions:
- Does each host track independent usage, with CP as the aggregation point?
- Is there a distributed counter pattern that works across hosts?
- What's the acceptable staleness window for quota enforcement?

### Q6: How does the host controller request agent credentials from CP?

Expected to match K8s model:
- Controller publishes to `mclaude.hosts.{hslug}.api.agents.credentials` (request/reply)
- CP validates group membership, project ownership, host assignment
- CP mints per-project JWT and returns it
- Controller passes JWT to spawned agent

Open sub-questions:
- Does the controller pre-fetch credentials or fetch on-demand at session start?
- How does credential refresh work for long-running sessions? (Agent refreshes its own JWT per ADR-0054, or controller manages refresh?)

### Q7: What replaces the daemon's KV watches?

Current daemon watches shared KV buckets for all projects. Under the new model:
- **Host controller** watches the job queue for its host (needs host-scoped access to job queue KV)
- **Per-session agents** watch their own project's KV (per-project JWT, same as K8s)

Open sub-question: job queue KV is currently per-user (`mclaude-job-queue-{uslug}`). If a host serves multiple users (group/team host), the controller needs to watch multiple users' job queues — how is this scoped?

### Q8: What component is the host controller?

Options:
- **`mclaude-controller-local`** (existing binary from ADR-0035): extend it with session-agent spawning and per-project credential management.
- **`mclaude-server`** (existing macOS launchd service): already manages local state; could absorb controller responsibilities.
- **New component**: dedicated `mclaude-host-controller` binary.

## Dependencies

| ADR | Relationship |
|-----|-------------|
| ADR-0054 (NATS JetStream permission tightening) | **Prerequisite.** Defines the per-project agent JWT model and host subject scheme that necessitates this redesign. |
| ADR-0035 (Unified host architecture) | **Context.** Defines the current host identity model, `mclaude-controller-local`, and daemon mode. This ADR supersedes the daemon mode portion. |

## Scope

**In scope:**
- BYOH host controller design (lifecycle, credential management, agent spawning)
- Quota system redesign (subject structure, authority model, aggregation)
- Daemon mode deprecation path

**Out of scope:**
- K8s controller changes (already uses per-project model)
- NATS permission changes (covered by ADR-0054)
- Session import (covered by ADR-0053)
