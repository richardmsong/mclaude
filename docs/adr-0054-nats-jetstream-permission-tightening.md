# ADR: NATS JetStream Permission Tightening

**Status**: draft
**Status history**:
- 2026-04-29: draft

## Overview
Tighten NATS JetStream permissions for all identity types (user, session-agent, host) from broad wildcards (`$JS.API.>`, `$JS.*.API.>`) to explicit, scoped allow-lists. This is a prerequisite for ADR-0053 (Session Import), which introduces per-user Object Store buckets that require user-scoped JetStream access.

## Motivation
The current NATS permission model grants every identity type broad JetStream API access:

| Identity | Current permission | What it allows |
|----------|-------------------|----------------|
| User (SPA/CLI) | `$JS.API.>` pub+sub | ALL JetStream operations on ALL streams, KV buckets, and Object Store buckets |
| Session-agent | `$JS.API.>`, `$JS.*.API.>`, unscoped `$KV.mclaude-sessions.>` etc. | ALL JetStream ops + access to ALL users' KV entries |
| Host | `$JS.*.API.>` | ALL domain-prefixed JetStream operations |

**Consequences:**
- A compromised user JWT can read/write/delete any other user's sessions, projects, hosts
- A rogue session-agent can access all users' data across all KV buckets
- A compromised host credential has full JetStream access across all domains
- ADR-0053's per-user Object Store buckets are meaningless if any user JWT can access any bucket

The fix: replace broad wildcards with the minimum JetStream subjects each identity type needs, scoped to their own user/host/project namespace.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Isolation model | Per-user JetStream resources | Hard NATS-level isolation. Separate KV buckets, event streams, and Object Store buckets per user. No payload-inspection gaps. NATS handles many streams at scale. |
| Approach | Replace wildcards with explicit allow-lists referencing per-user resource names | Each identity's JWT lists the exact stream/bucket names it can touch. |
| KV buckets | Per-user: `mclaude-sessions-{uslug}`, `mclaude-projects-{uslug}`, `mclaude-hosts-{uslug}`, `mclaude-job-queue-{uslug}` | Eliminates shared-bucket prefix-scoping gaps. Direct-get is physically scoped to the user's own bucket. |
| Events stream | Per-user: `MCLAUDE_EVENTS_{uslug}` | Consumer creation is scoped by stream name in the subject. No filter-bypass gap. |
| Object Store | Per-user: `mclaude-imports-{uslug}` (ADR-0053) | Already per-user by design. Consistent with the rest. |
| Host/controller JetStream | None — removed entirely | Hosts and controllers only use NATS subjects for provisioning. They never needed JetStream. |
| Host subject scheme | `mclaude.hosts.{hslug}.>` (supersedes ADR-0035's `mclaude.users.{uslug}.hosts.{hslug}.>` for host-side subscriptions) | Removes user prefix from host subscriptions. Group membership enforced by control-plane (which already intercepts all requests). Host JWT is constant-size regardless of group membership. User/agent/SPA subjects unchanged. |
| Session-agent scope | Per-project | Each agent runs per-project and gets a JWT scoped to that project's KV keys only. Stolen credential on a shared BYOH host exposes one project, not all of the user's data across all hosts. No credential reissuance concern — each new project spawns a new agent with its own JWT at spawn time. |
| Credential lifecycle | Short TTL + proactive refresh for all identity types | Host JWTs: 5 min TTL (frequent membership changes). Session-agent JWTs: 5 min TTL (consistent). User JWTs: keep existing ~8h TTL (SPA already refreshes every 60s; users don't change permissions as frequently). All refresh via NKey signature challenge. |
| Session-agent JWT issuance | Control-plane only (host controllers no longer hold the account key) | Hosts request agent credentials from the control-plane via NATS. CP validates group membership, project ownership, and host assignment before minting. Removes the account signing key from all host controllers — hosts can request credentials but not forge them. |
| Session-agent refresh auth | NKey signature challenge | Agent signs a nonce with its NKey seed, control-plane validates against stored public key, issues new JWT. Most robust — survives JWT expiry, cryptographic proof of identity. |
| Migration | Self-healing via credential refresh | Deploy new permission code. On next refresh cycle (within 24h), session-agents automatically receive tightened JWTs. For immediate effect, restart agents to trigger immediate refresh. |
| Bucket lifecycle | Control-plane creates per-user buckets on user registration | Buckets are provisioned once. If a bucket is missing at runtime, the control-plane creates it on demand. |
| Host binding model | Every host is bound to a group; groups have members | Unifies single-user BYOH, team BYOH, and platform hosts. Platform host = "everyone" group. No separate code paths. |
| Group nesting | Flat groups in v1; nesting is additive later | Schema uses `group_members(group_id, member_id, member_type)` from day one so nesting is a resolver change, not a reorganization. |
| Group membership revocation | Revoke + on-demand refresh signal | On membership change, old host JWT is revoked (NATS revocation list), "refresh now" pushed to host. Revocation entry auto-expires after 5 min (JWT TTL). No revocation list bloat even under high churn. |

## Threat Model

### Permission Diagrams

#### 1. Identity Hierarchy

Who owns what, and what credentials they hold.

```mermaid
graph TD
    OP["Platform Operator<br/><i>fully trusted</i>"]

    OP --- CP["Control Plane<br/>account signing key<br/>issues all JWTs"]
    OP --- NATS["NATS Hub Server"]
    OP --- K8S["K8s Controller us-east<br/>JWT: hosts.us-east"]

    UA["User A (alice)"] --- SPA_A["SPA / CLI<br/>JWT: users.alice.hosts.*"]
    UA --- HOST_A["BYOH Host laptop-a<br/>JWT: hosts.laptop-a"]
    UA --- AGENT_A["Session-Agent<br/>JWT: per-user, 5 min TTL"]

    UB["User B (bob)"] --- SPA_B["SPA / CLI<br/>JWT: users.bob.hosts.*"]
    UB --- HOST_B["BYOH Host laptop-b<br/>JWT: hosts.laptop-b"]
    UB --- AGENT_B["Session-Agent<br/>JWT: per-user, 5 min TTL"]
```

#### 2. Proposed Permission Map

NATS data topology in the center, partitioned by user. User-side identities on left/right.
Shared platform host below, with per-user namespaces containing session-agents.

```mermaid
flowchart TB
    %% ── Left: User A's identities ──
    subgraph Left["User A (alice)"]
        SPA_A["Alice SPA / CLI"]
        subgraph BYOH_A["BYOH Host laptop-a"]
            CTRL_A["Local Controller"]
        end
    end

    %% ── Right: User B's identities ──
    subgraph Right["User B (bob)"]
        SPA_B["Bob SPA / CLI"]
        subgraph BYOH_B["BYOH Host laptop-b"]
            CTRL_B["Local Controller"]
        end
    end

    %% ── Center: NATS data topology ──
    subgraph NATS["NATS Hub — JetStream Resources"]
        subgraph Partition_A["alice's resources (separate streams/buckets)"]
            KV_A["KV: sessions-alice<br/>KV: projects-alice<br/>KV: hosts-alice<br/>KV: job-queue-alice"]
            OBJ_A["Obj Store:<br/>imports-alice"]
            EVT_A["Stream:<br/>EVENTS_alice"]
        end
        subgraph Partition_B["bob's resources (separate streams/buckets)"]
            KV_B["KV: sessions-bob<br/>KV: projects-bob<br/>KV: hosts-bob<br/>KV: job-queue-bob"]
            OBJ_B["Obj Store:<br/>imports-bob"]
            EVT_B["Stream:<br/>EVENTS_bob"]
        end
    end

    %% ── Bottom: Shared platform host ──
    subgraph SharedHost["Shared Platform Host us-east (operator-managed)"]
        K8S_CTRL["K8s Controller<br/><i>JWT: hosts.us-east — no JetStream</i>"]
        subgraph NS_A["namespace: mclaude-alice"]
            AGENT_A["Session-Agent A"]
        end
        subgraph NS_B["namespace: mclaude-bob"]
            AGENT_B["Session-Agent B"]
        end
    end

    %% ── Edges: Alice SPA ──
    SPA_A -- "R: KV watch/get" --> KV_A
    SPA_A -- "R/W: upload imports" --> OBJ_A
    SPA_A -- "R: subscribe own events" --> EVT_A

    %% ── Edges: Bob SPA ──
    SPA_B -- "R: KV watch/get" --> KV_B
    SPA_B -- "R/W: upload imports" --> OBJ_B
    SPA_B -- "R: subscribe own events" --> EVT_B

    %% ── Edges: Session-Agent A (on shared host) ──
    AGENT_A -- "R/W: manage sessions,<br/>projects, jobs" --> KV_A
    AGENT_A -- "R: download imports" --> OBJ_A
    AGENT_A -- "R/W: publish lifecycle" --> EVT_A

    %% ── Edges: Session-Agent B (on shared host) ──
    AGENT_B -- "R/W: manage sessions,<br/>projects, jobs" --> KV_B
    AGENT_B -- "R: download imports" --> OBJ_B
    AGENT_B -- "R/W: publish lifecycle" --> EVT_B

    %% ── BYOH hosts: no JetStream ──
    CTRL_A -.-x|"no JetStream"| NATS
    CTRL_B -.-x|"no JetStream"| NATS

    %% ── K8s controller: no JetStream ──
    K8S_CTRL -.-x|"no JetStream"| NATS
```

**Key properties:**
- Alice's and Bob's data live in **physically separate** JetStream streams and KV buckets. No shared resources.
- Each JWT contains the user slug in every resource name -- it cannot reference another user's buckets/streams.
- Session-agents on the shared host are in separate K8s namespaces; each agent's JWT is scoped to its user's resources only.
- All host controllers (BYOH and platform) use `mclaude.hosts.{hslug}.>` — constant-size JWT regardless of group membership. Zero JetStream access.
- Group membership enforcement is in the control-plane, which already intercepts all requests.
- Users (SPA/CLI) get read-only KV, read/write Object Store, read-only events.
- Session-agents get read/write KV + events, read-only Object Store.

### Trust Tiers

| Identity | Trust level | What they control | What they should NOT access |
|----------|-------------|-------------------|---------------------------|
| **Platform operator** | Fully trusted | NATS, control-plane, platform hosts. Holds account signing key. | N/A — they own everything. |
| **User** (SPA/CLI) | Partially trusted | Their own projects, sessions, hosts. Can register BYOH hosts. | Other users' projects, sessions, hosts, imports. |
| **BYOH host** | Partially trusted | Local filesystem, local controller, session-agents. Owner = user. | Other users' NATS resources. (On their own machine they have filesystem access anyway.) |
| **Platform host controller** | Operator-trusted infrastructure | Provisioning for ALL users on that cluster. Has wildcard user scope. | JetStream resources outside its operational needs (it doesn't need KV/ObjectStore access). |
| **Session-agent** | Per-user scoped service | Sessions and projects for its assigned user. | Other users' sessions, projects, hosts, imports. |

### Attack Vectors

#### V1: Cross-User Data Access via JetStream Wildcard

**Attacker:** User A (compromised SPA, stolen JWT, or malicious user)
**Target:** User B's sessions, projects, or imports

| Attack path | Current state | Why it works |
|-------------|--------------|--------------|
| Read User B's session state | `$JS.API.DIRECT.GET.KV_mclaude-sessions` with key `{bob}.{host}.{project}.{session}` | User A's JWT has `$JS.API.>` pub+sub — allows ANY JetStream API call, including direct-get on any KV key. |
| Watch User B's session updates | Create consumer on `KV_mclaude-sessions` filtered to `{bob}.*` | `$JS.API.CONSUMER.CREATE.*` is unrestricted. |
| Read User B's project state | Same pattern on `KV_mclaude-projects` | Same wildcard. |
| Read User B's import archive | `$O.mclaude-imports-{bob}.*` | Object Store subjects go through `$JS.API.>` controlled streams. |

**Regular NATS subjects are already scoped:** User A cannot publish to `mclaude.users.{bob}.hosts.*.>` — this is correctly denied. The gap is purely in JetStream API access.

#### V2: User A Operating on a Host Not Bound to Them

**Attacker:** User A
**Target:** Host "laptop-b" (owned by User B)

| Attack path | Current state | Why it works / doesn't |
|-------------|--------------|------------------------|
| Send session-create to User B's host | Publish to `mclaude.users.{bob}.hosts.{laptop-b}.projects.{p}.api.sessions.create` | **BLOCKED.** User A's JWT only allows pub on `mclaude.users.{alice}.hosts.*.>`. Subject-level isolation works. |
| Send provisioning request to User B's host | Publish to `mclaude.users.{bob}.hosts.{laptop-b}.api.projects.create` | **BLOCKED.** Same subject scoping. |
| Inject fake project into User B's KV namespace | Write to `KV_mclaude-projects` with key `{userIdB}.{projectId}` via `$JS.API.>` | **EXPLOITABLE.** User A's `$JS.API.>` allows direct put on any KV bucket with any key. No server-side key-namespace validation. |
| Inject fake session into User B's KV namespace | Write to `KV_mclaude-sessions` with key `{bob}.{laptop-b}.{project}.{session}` | **EXPLOITABLE.** Same — KV doesn't validate writer identity against key namespace. |
| Read User B's host configuration | Direct-get on `KV_mclaude-hosts` with key `{bob}.{laptop-b}` | **EXPLOITABLE.** Same JetStream wildcard. |

**Key finding:** Subject-level permissions correctly prevent User A from *commanding* User B's hosts (session create, project provision). But JetStream wildcards allow User A to *read and write* User B's state data directly in KV, bypassing all control-plane validation.

#### V3: Rogue Host Accessing Unauthorized User Data

**Attacker:** Host "laptop-a" (owned by User A — user has the NKey seed from `host register`)
**Target:** User B's data

| Attack path | Current state | Why it works |
|-------------|--------------|--------------|
| Read User B's sessions KV | `$JS.*.API.>` covers all domain-prefixed JetStream — includes `$JS.hub.API.DIRECT.GET.KV_mclaude-sessions` | Host JWT has `$JS.*.API.>` which matches any JetStream domain. |
| Read User B's projects KV | Same pattern | Same wildcard. |
| Delete User B's sessions | `$JS.*.API.STREAM.PURGE.KV_mclaude-sessions` | Stream-level operations are unrestricted. |
| Read/write any Object Store | `$JS.*.API.STREAM.INFO.OBJ_*`, `$O.*` subjects | All go through JetStream API. |

**Note:** On BYOH, the host owner IS the user, so "host accessing its own user's data" is expected. The threat is a BYOH host accessing OTHER users' data — which is currently possible via JetStream wildcards.

**Platform host controllers** have the same `$JS.*.API.>` wildcard. A compromised platform controller could read all users' data across all KV buckets. However, platform controllers are operator-managed infrastructure — the operator trusts their own controllers. The risk here is container compromise or supply-chain attacks against the controller image.

#### V4: Session-Agent Cross-User on Shared Platform Host

**Attacker:** User A's session-agent running on a shared K8s cluster
**Target:** User B's session-agent data on the same cluster

| Attack path | Current state | Why it works |
|-------------|--------------|--------------|
| Read User B's session states | Agent has `$KV.mclaude-sessions.>` (unscoped!) — subscribes to ALL session updates | The `>` wildcard matches all keys, not just the agent's user. |
| Read User B's project states | Agent has `$KV.mclaude-projects.>` (unscoped!) | Same. |
| Read User B's host config | Agent has `$KV.mclaude-hosts.>` (unscoped!) | Same. |
| Write to User B's session KV | Agent has `$KV.mclaude-sessions.>` in PubAllow | Can write fake session state under User B's key prefix. |
| Access User B's imports | Agent has `$JS.API.>` and `$JS.*.API.>` | Full JetStream access. |

**K8s provides container isolation** — agents run in separate pods in per-user namespaces. But NATS credentials are network-accessible: a compromised pod that exfiltrates its agent JWT + NKey seed has unlimited JetStream access until credential rotation. Container isolation is defense-in-depth; NATS permissions are the primary access control layer.

#### V5: KV Key Injection (Data Integrity)

This vector deserves separate treatment because it's not just a read attack — it's a write attack that can corrupt application state.

**Attacker:** Any identity with `$JS.API.>` (user, agent, or host)
**Attack:** Write a crafted KV entry to a bucket under another user's key prefix.

| Injection target | Impact |
|-----------------|--------|
| Fake session in `mclaude-sessions` under User B's prefix | User B's SPA shows a phantom session. If it references a malicious JSONL path, could trick session-agent into processing attacker-controlled data. |
| Fake project in `mclaude-projects` under User B's prefix | User B sees a project they didn't create. Provisioning could be triggered for a git URL controlled by the attacker. |
| Modified host config in `mclaude-hosts` | Could redirect provisioning requests, change host capabilities, or inject malicious configuration. |

**The control-plane and SPA trust KV entries implicitly** — they do not validate that the writer's identity matches the key's namespace prefix. KV is treated as a source of truth. This makes the JetStream wildcard gap exploitable for state manipulation, not just eavesdropping.

#### V6: Credential Theft Window

| Credential type | Current TTL | Blast radius if stolen |
|----------------|-------------|----------------------|
| User JWT (SPA) | ~8h | ALL JetStream resources for ALL users (until expiry) |
| User JWT (CLI) | Not yet implemented | Same as SPA |
| Session-agent JWT | No expiry | Permanent full JetStream access until key is revoked |
| Host JWT | No expiry | Permanent full JetStream access until key is revoked |

With tightened permissions + TTL on all credential types, the blast radius reduces to: "the compromised identity's own user namespace, for the duration of one TTL cycle."

### Isolation Boundaries Summary

| Boundary | Current enforcement | Gap | Fix |
|----------|-------------------|-----|-----|
| User A ↔ User B (NATS subjects) | Subject-level pub/sub scoping | None — correctly enforced | N/A |
| User A ↔ User B (JetStream/KV) | None (`$JS.API.>` wildcard) | Full cross-user read/write | Scope JetStream API subjects to user's own buckets |
| User A ↔ User B (Object Store) | None (Object Store uses JetStream API) | Full cross-user read/write | Per-user buckets + scoped permissions |
| Host ↔ other users (NATS subjects) | Subject-level scoping to `{uslug}.{hslug}` | None — correctly enforced | N/A |
| Host ↔ other users (JetStream) | None (`$JS.*.API.>` wildcard) | Full cross-user read/write | Scope host JetStream to operational needs only |
| Session-agent ↔ other users (KV) | None (unscoped `$KV.*.>` wildcards) | Full cross-user read/write | Scope KV subjects to agent's user prefix |
| Session-agent ↔ other users (JetStream) | None (`$JS.API.>` wildcard) | Full cross-user read/write | Scope JetStream API to user's own resources |
| Platform controller ↔ all users | By design (wildcard user for provisioning) | JetStream access broader than needed | Scope controller JetStream to provisioning operations only (it doesn't use KV/ObjectStore directly) |

## Full Permission Specifications by Identity

Complete pub/sub allow-lists for each identity type. Every subject is listed with its rationale.
Examples use `alice` as user slug, `laptop-a` as host slug, `us-east` as cluster slug.

### User (SPA / CLI) — example: alice

```
Pub.Allow:
  mclaude.users.alice.hosts.*.>          # Send commands to own hosts (create session, import, etc.)
  _INBOX.>                               # Request/reply pattern (NATS requirement)
  # KV buckets: read-only. Sessions written by agents, projects/hosts by control-plane.
  # No $KV publish permissions — all mutations go through CP or session-agents.
  $O.mclaude-imports-alice.>             # Object Store: upload import archive chunks + metadata
  $JS.API.DIRECT.GET.KV_mclaude-sessions-alice.>   # KV get: subject-form, covers all keys (users see all own data)
  $JS.API.DIRECT.GET.KV_mclaude-projects-alice.>   # KV get: read project state
  $JS.API.DIRECT.GET.KV_mclaude-hosts-alice.>      # KV get: read host state
  $JS.API.CONSUMER.CREATE.KV_mclaude-sessions-alice.>   # KV watch: filtered form, any consumer + filter on own bucket
  $JS.API.CONSUMER.CREATE.KV_mclaude-projects-alice.>   # KV watch
  $JS.API.CONSUMER.CREATE.KV_mclaude-hosts-alice.>      # KV watch
  $JS.API.CONSUMER.CREATE.MCLAUDE_EVENTS_alice.>        # Events: create consumer for replay
  $JS.API.STREAM.INFO.KV_mclaude-sessions-alice       # Stream info: needed by NATS client for KV init
  $JS.API.STREAM.INFO.KV_mclaude-projects-alice       # Stream info
  $JS.API.STREAM.INFO.KV_mclaude-hosts-alice          # Stream info
  $JS.API.STREAM.INFO.MCLAUDE_EVENTS_alice            # Stream info: event stream metadata
  $JS.API.STREAM.INFO.OBJ_mclaude-imports-alice       # Stream info: Object Store bucket metadata
  $JS.API.CONSUMER.INFO.KV_mclaude-sessions-alice.*   # Consumer info: needed by NATS client
  $JS.API.CONSUMER.INFO.KV_mclaude-projects-alice.*   # Consumer info
  $JS.API.CONSUMER.INFO.KV_mclaude-hosts-alice.*      # Consumer info
  $JS.API.CONSUMER.INFO.MCLAUDE_EVENTS_alice.*        # Consumer info: event stream consumers
  $JS.ACK.KV_mclaude-sessions-alice.>    # Ack consumed KV messages
  $JS.ACK.KV_mclaude-projects-alice.>    # Ack consumed KV messages
  $JS.ACK.KV_mclaude-hosts-alice.>       # Ack consumed KV messages
  $JS.ACK.MCLAUDE_EVENTS_alice.>         # Ack consumed event messages
  $JS.FC.KV_mclaude-sessions-alice.>     # Flow control: scoped to own streams
  $JS.FC.KV_mclaude-projects-alice.>
  $JS.FC.KV_mclaude-hosts-alice.>
  $JS.FC.MCLAUDE_EVENTS_alice.>
  $JS.FC.OBJ_mclaude-imports-alice.>

Sub.Allow:
  mclaude.users.alice.hosts.*.>          # Receive replies from own hosts
  _INBOX.>                               # Request/reply: all JetStream API responses arrive here.
                                         #   Residual: _INBOX.> allows subscribing to all reply subjects
                                         #   in the account. Low practical risk (random inbox prefixes).
                                         #   Future: per-identity inbox prefixes with allow_responses.
  $KV.mclaude-sessions-alice.>           # KV watch: push delivery of session state changes
  $KV.mclaude-projects-alice.>           # KV watch: push delivery of project state changes
  $KV.mclaude-hosts-alice.>              # KV watch: push delivery of host state changes
  MCLAUDE_EVENTS_alice.>                 # Event replay: push delivery from event stream consumers
  # No $O subscribe — users only upload to Object Store, not download.
  $JS.FC.KV_mclaude-sessions-alice.>     # Flow control: scoped to own streams
  $JS.FC.KV_mclaude-projects-alice.>
  $JS.FC.KV_mclaude-hosts-alice.>
  $JS.FC.MCLAUDE_EVENTS_alice.>
  $JS.FC.OBJ_mclaude-imports-alice.>
```

**What alice CANNOT do:** Anything involving `bob` — every resource name contains `alice`. No wildcards that could match other users' resources. No `$JS.API.STREAM.DELETE.*`, `$JS.API.STREAM.PURGE.*`, or `$JS.API.STREAM.CREATE.*` — users cannot create, delete, or purge streams. No KV writes — sessions are written by session-agents, projects and hosts by the control-plane. The SPA is read-only on all KV buckets; user actions that mutate state go through the control-plane via `mclaude.users.alice.hosts.*.>`.

---

### Session-Agent — example: agent for alice's project "myapp" on host "laptop-a"

**Why per-project scoping?** Each session-agent manages one project. On a shared BYOH
host, the host owner has root access and can read agent credentials from disk or memory.
With per-user scoping, a stolen credential exposes ALL of alice's data across all hosts
and projects — her entire JetStream partition. With per-project scoping, a stolen
credential exposes only the one project this agent manages. The blast radius shrinks
from "everything alice has everywhere" to "one project on one host."

This matters most on shared BYOH hosts where the host owner is a different person than
the user. On K8s, the cluster owner has equivalent access — `kubectl exec`, Secret reads,
volume mounts — so the threat model is identical to BYOH.

**No credential reissuance concern:** Each project spawns its own session-agent process
or pod. The control-plane mints the agent's JWT at spawn time with the project slug
baked in (host controller requests it via `mclaude.hosts.{hslug}.api.agents.credentials`).
New projects get new agents with new JWTs — no existing credential needs updating.

```
Pub.Allow:
  mclaude.users.alice.hosts.laptop-a.projects.myapp.>    # Lifecycle events, session updates — this project only
  _INBOX.>                                               # Request/reply (NATS requirement)
  $KV.mclaude-sessions-alice.laptop-a.myapp.>            # KV write: create/update/delete sessions for this project
  $KV.mclaude-projects-alice.laptop-a.myapp              # KV write: update this project's state (e.g., clear ImportObjectRef)
  $KV.mclaude-job-queue-alice.laptop-a.myapp.>           # KV write: manage this project's job queue entries
  # Direct-get: subject-form with full $KV.<bucket>.<key> path (C2 fix)
  $JS.API.DIRECT.GET.KV_mclaude-sessions-alice.$KV.mclaude-sessions-alice.laptop-a.myapp.>    # KV get: this project's sessions
  $JS.API.DIRECT.GET.KV_mclaude-projects-alice.$KV.mclaude-projects-alice.laptop-a.myapp      # KV get: this project's state
  $JS.API.DIRECT.GET.KV_mclaude-hosts-alice.$KV.mclaude-hosts-alice.laptop-a                  # KV get: this host's config (read-only)
  $JS.API.DIRECT.GET.KV_mclaude-job-queue-alice.$KV.mclaude-job-queue-alice.laptop-a.myapp.>  # KV get: this project's jobs
  # Consumer create: filtered form with full $KV.<bucket>.<key> filter subject (C1 fix)
  $JS.API.CONSUMER.CREATE.KV_mclaude-sessions-alice.*.$KV.mclaude-sessions-alice.laptop-a.myapp.>      # KV watch: this project's sessions
  $JS.API.CONSUMER.CREATE.KV_mclaude-projects-alice.*.$KV.mclaude-projects-alice.laptop-a.myapp        # KV watch: this project only
  $JS.API.CONSUMER.CREATE.KV_mclaude-hosts-alice.*.$KV.mclaude-hosts-alice.laptop-a                    # KV watch: this host only
  $JS.API.CONSUMER.CREATE.KV_mclaude-job-queue-alice.*.$KV.mclaude-job-queue-alice.laptop-a.myapp.>    # KV watch: this project's jobs
  $JS.API.CONSUMER.CREATE.MCLAUDE_EVENTS_alice.*.MCLAUDE_EVENTS_alice.laptop-a.myapp.>                 # Events: filtered to this project
  $JS.API.STREAM.INFO.KV_mclaude-sessions-alice          # Stream info (NATS client needs this for KV init)
  $JS.API.STREAM.INFO.KV_mclaude-projects-alice
  $JS.API.STREAM.INFO.KV_mclaude-hosts-alice
  $JS.API.STREAM.INFO.KV_mclaude-job-queue-alice
  $JS.API.STREAM.INFO.MCLAUDE_EVENTS_alice
  # No Object Store permissions — import downloads use a one-shot JWT (see below)
  $JS.API.CONSUMER.INFO.KV_mclaude-sessions-alice.*      # Consumer info (NATS client needs this)
  $JS.API.CONSUMER.INFO.KV_mclaude-projects-alice.*
  $JS.API.CONSUMER.INFO.KV_mclaude-hosts-alice.*
  $JS.API.CONSUMER.INFO.KV_mclaude-job-queue-alice.*
  $JS.API.CONSUMER.INFO.MCLAUDE_EVENTS_alice.*           # Consumer info: event stream consumers (L1 fix)
  $JS.ACK.KV_mclaude-sessions-alice.>                    # Ack consumed KV messages (consumer-specific tokens, not key names)
  $JS.ACK.KV_mclaude-projects-alice.>
  $JS.ACK.KV_mclaude-hosts-alice.>
  $JS.ACK.KV_mclaude-job-queue-alice.>
  $JS.ACK.MCLAUDE_EVENTS_alice.>
  $JS.FC.KV_mclaude-sessions-alice.>                     # Flow control: scoped to own streams (M2 fix)
  $JS.FC.KV_mclaude-projects-alice.>
  $JS.FC.KV_mclaude-hosts-alice.>
  $JS.FC.KV_mclaude-job-queue-alice.>
  $JS.FC.MCLAUDE_EVENTS_alice.>

Sub.Allow:
  mclaude.users.alice.hosts.laptop-a.projects.myapp.>    # Receive on this project's subjects only
  _INBOX.>                                               # Request/reply: all JetStream API responses arrive here
  $KV.mclaude-sessions-alice.laptop-a.myapp.>            # KV watch: push delivery of this project's sessions
  $KV.mclaude-projects-alice.laptop-a.myapp              # KV watch: push delivery of this project's state
  $KV.mclaude-hosts-alice.laptop-a                       # KV watch: push delivery of this host's config only
  $KV.mclaude-job-queue-alice.laptop-a.myapp.>           # KV watch: push delivery of this project's jobs
  MCLAUDE_EVENTS_alice.laptop-a.myapp.>                  # Event replay: push delivery scoped to this project (L4 fix)
  # No Object Store subscribe — import downloads use a one-shot JWT
  $JS.FC.KV_mclaude-sessions-alice.>                     # Flow control: scoped to own streams (M2 fix)
  $JS.FC.KV_mclaude-projects-alice.>
  $JS.FC.KV_mclaude-hosts-alice.>
  $JS.FC.KV_mclaude-job-queue-alice.>
  $JS.FC.MCLAUDE_EVENTS_alice.>
```

**How per-project scoping works at each layer:**

| Layer | Scoping mechanism | What it enforces |
|-------|-------------------|------------------|
| NATS subjects | `mclaude.users.alice.hosts.laptop-a.projects.myapp.>` | Agent can only publish/subscribe on its own project's command and event subjects |
| KV pub/sub | `$KV.mclaude-sessions-alice.laptop-a.myapp.>` | Agent can only write KV entries keyed under its project. KV watch only delivers its project's updates. |
| JetStream API | `$JS.API.*.KV_mclaude-sessions-alice` | Agent can call JetStream API on alice's session bucket (per-user bucket). Cannot access bob's buckets. |
| KV direct-get | `$JS.API.DIRECT.GET.KV_mclaude-sessions-alice.$KV.mclaude-sessions-alice.laptop-a.myapp.>` | Uses subject-form direct-get. The key portion is the **full message subject** (`$KV.<bucket>.<key>`), not just the key. Agent can only read its own project's keys. Implementation must use subject-form; payload-form is not permitted. |
| Stream info | `$JS.API.STREAM.INFO.KV_mclaude-sessions-alice` | **Residual metadata leakage (accepted).** Stream info is per-stream (per-user bucket), not per-key. Agent can query aggregate metadata (message count, byte size, first/last sequence) for alice's entire sessions bucket, not just its project. Required by the NATS client SDK for KV bucket initialization — called unconditionally before any KV operation. Leaks activity volume across all projects but not key contents or values. |
| Consumer info | `$JS.API.CONSUMER.INFO.KV_mclaude-sessions-alice.*` | **Residual metadata leakage (accepted).** The `*` wildcard allows querying info for any consumer on alice's bucket, not just the agent's own. Consumer names are ephemeral UUIDs — an attacker would need to guess or brute-force them. Even if found, consumer info reveals only delivery state (pending count, ack floor, last delivered sequence), not message contents. Cannot be tightened: consumer names are auto-generated at runtime by the NATS client. `$JS.API.CONSUMER.LIST` and `$JS.API.CONSUMER.NAMES` are NOT granted — agent cannot enumerate consumers. |

**Differences from User JWT:**
- Agent has KV **write** on sessions, projects, and job-queue (it manages state; users are read-only on KV).
- Agent has job-queue access (users don't need it).
- Agent NATS subjects are scoped to one project (`projects.myapp.>`), not all hosts (`hosts.*.>`).
- Agent has NO standing Object Store access. Import downloads use a one-shot JWT (see below).
- Agent does NOT have host KV write (it reads host config but doesn't modify it).

**One-shot import JWT:** When a project is provisioned via import, the control-plane mints
a second credential alongside the standard agent JWT. This one-shot JWT permits only the
exact Object Store subjects needed to download the specific import tarball:

```
One-shot Import JWT (max_connections: 1, 1h safety TTL, revoked on completion):
Pub.Allow:
  _INBOX.>                                                                                  # request/reply
  $JS.API.STREAM.INFO.OBJ_mclaude-imports-alice                                             # OS client init
  $JS.API.DIRECT.GET.OBJ_mclaude-imports-alice.$O.mclaude-imports-alice.M.<object-name>     # metadata fetch (subject-form)
  $JS.API.CONSUMER.CREATE.OBJ_mclaude-imports-alice.>                                       # consumer for chunk download
  $JS.API.CONSUMER.INFO.OBJ_mclaude-imports-alice.*                                         # consumer info
  $JS.ACK.OBJ_mclaude-imports-alice.>                                                       # ack consumed chunks
  $JS.FC.OBJ_mclaude-imports-alice.>                                                        # flow control
  # No $O.* publish — that would WRITE chunks/metadata. This JWT is download-only.
Sub.Allow:
  _INBOX.>                                       # request/reply responses
  $O.mclaude-imports-alice.C.<sha256-chunk-1>    # receive chunk data (push delivery from consumer)
  $O.mclaude-imports-alice.C.<sha256-chunk-2>
  ...
  $O.mclaude-imports-alice.M.<object-name>       # receive metadata (push delivery)
  $JS.FC.OBJ_mclaude-imports-alice.>             # flow control
```

**Lifecycle:**

1. `max_connections: 1` — prevents concurrent use while the agent is downloading.
   If the credential leaks and an attacker connects first, the agent's connection
   fails, signaling theft to the host controller.
2. **Revocation on completion** — agent signals download complete on its standard
   project subject (`mclaude.users.alice.hosts.laptop-a.projects.myapp.import.complete`).
   CP adds the one-shot JWT to the NATS revocation list. Credential is immediately dead.
3. **1-hour safety TTL** — backstop if the agent crashes or never signals completion.

The import object is NOT deleted from Object Store on completion — it may still be needed
(e.g., re-import to another host, retry after failure).

**Residual risk:** Between the agent disconnecting and CP processing the revocation signal,
the connection slot is free and the credential is still valid. An attacker who obtained the
one-shot JWT could connect during this window. However: revocation propagates and
disconnects them almost immediately (milliseconds to low seconds), yielding at most a few
packets of incomplete chunk data — not a usable tarball. Object Store downloads require
fetching metadata then all chunks sequentially; an interrupted download is unusable.
Additionally, on a BYOH host the host owner can already read the extracted tarball from
the filesystem, so this doesn't expand the attack surface.

Flow: Host controller receives both JWTs from CP → starts session-agent with one-shot JWT
(`max_connections: 1`) → agent downloads and extracts tarball → agent signals completion on
standard JWT → CP revokes one-shot JWT → agent continues with standard per-project JWT for
normal operation.

---

### BYOH Host / Platform Controller — all hosts use host-scoped subjects

**Subject scheme change from ADR-0035:** Host controllers subscribe to `mclaude.hosts.{hslug}.>` instead of `mclaude.users.{uslug}.hosts.{hslug}.>`. This removes the user prefix from the host's subscription subject, eliminating the need to enumerate group members in the host JWT.

**Why this is safe:** The control-plane already intercepts all user requests and re-publishes to the host. The SPA never talks to the host directly. The control-plane validates group membership before publishing to `mclaude.hosts.{hslug}.api.projects.*`. The NATS-level user-prefix check on the host side was redundant.

**What doesn't change:** User (SPA/CLI) JWTs, session-agent JWTs, KV structure, and event streams are all unaffected. Only the host controller's subscription and the control-plane's publish-to-host target change.

```
Pub.Allow:
  mclaude.hosts.laptop-a.>              # Provisioning: receive and reply to project create/delete
  _INBOX.>                              # Request/reply

Sub.Allow:
  mclaude.hosts.laptop-a.>              # Subscribe to provisioning requests for this host
  _INBOX.>                              # Request/reply
  $SYS.ACCOUNT.*.CONNECT               # System events: receive connection notifications (M3 fix: moved from Pub)
  $SYS.ACCOUNT.*.DISCONNECT            # System events: receive disconnection notifications
  # Residual (M4): account wildcard * matches all accounts. Harmless in single-account
  # architecture. If multi-account support is added (deferred), scope to specific account ID.
```

**No `$JS.*`, `$KV.*`, or `$O.*` subjects at all.** The host controller only uses NATS core pub/sub for provisioning commands. Session-agents get their own separate per-user JWTs with JetStream access.

**This JWT is identical regardless of group size.** A single-user BYOH host, a 200-person team workstation, and a platform host serving 100k users all have the same host JWT structure — one `mclaude.hosts.{hslug}.>` entry. Group membership enforcement is entirely in the control-plane.

**Design principle:** Every host is bound to a **group**, and a group has members. This unifies all host types:

| Host type | Group | Members |
|-----------|-------|---------|
| Single-user BYOH | Implicit group of 1 | `[alice]` |
| Team BYOH | Named group | `[alice, bob, carol]` |
| Platform host | The "everyone" group | `[*]` — operator policy, all current and future users |

The platform host is just the extreme case of a shared BYOH host -- the operator brings the host and shares it with everyone. There is no separate code path for "platform" vs "BYOH" — they differ only in group membership.

**NATS subject model for groups:**

Host JWTs always enumerate each permitted user explicitly in their pub/sub allow-list. A 200-member group produces ~200 subject entries (~20KB JWT) -- well within NATS limits. There is no lazy wildcard fallback for "large" groups.

The wildcard `mclaude.users.*.hosts.{hslug}.>` is reserved exclusively for the platform operator's "everyone" group. This is a deliberate policy decision ("any user, including future users, may use this host"), not a scaling escape hatch. Only the operator can create an "everyone" group.

**Why not per-group NATS accounts?** NATS accounts provide hard isolation between groups, but users routinely span multiple groups (e.g., Alice uses both her BYOH and the platform host). Per-group accounts would require Alice to have separate NATS identities per group, fragmenting her view of her own data. Cross-account KV watches, Object Store access, and event streaming all require explicit export/import plumbing per bucket per account. The complexity is not justified when per-user JetStream resources already isolate user data and hosts have no JetStream access.

**Group membership management:**

| Aspect | Design |
|--------|--------|
| Storage | Postgres: `groups` table (id, slug, name, owner_id, is_everyone bool), `group_members` table (group_id, member_id, member_type) — `member_type` supports flat members now, nested groups later |
| Host binding | `hosts` table gets `group_id` column (replaces implicit single-user binding) |
| JWT issuance | `HostSubjectPermissions(group)` enumerates all members as explicit subject entries. For the "everyone" group, uses wildcard `*`. |
| Credential refresh model | Short TTL (5 min) + on-demand refresh signal. Hosts routinely refresh every 5 min. On membership change, control-plane invalidates its in-memory membership cache for the affected group, pushes "refresh now" on the host's existing NATS subject; host immediately requests a new JWT. Old JWT is revoked (added to NATS revocation list). Revocation entries auto-expire after 5 min (the JWT's TTL). Sub-second propagation for explicit changes; revocation list stays small (at most: changes-in-last-5-min entries). |
| Membership cache | In-memory cache on control-plane: `groupSlug -> {members, resolvedAt}`. Cache hit skips Postgres entirely. Invalidated on membership mutations. Cache TTL 60s as fallback. At enterprise scale (10k+ hosts), reduces Postgres load from ~33 queries/sec to near zero in steady state. |
| API | `POST /groups`, `POST /groups/{gslug}/members`, `DELETE /groups/{gslug}/members/{uslug}` |
| CLI | `mclaude group create <name>`, `mclaude group add-member <gslug> <uslug>`, `mclaude host bind <hslug> <gslug>` |

---

### Platform K8s Controller — example: cluster us-east

```
Pub.Allow:
  mclaude.hosts.us-east.>               # Provisioning: this cluster (same as any host)
  _INBOX.>                              # Request/reply

Sub.Allow:
  mclaude.hosts.us-east.>               # Subscribe to provisioning for this cluster
  _INBOX.>                              # Request/reply
  $SYS.ACCOUNT.*.CONNECT               # System events: receive connection notifications
  $SYS.ACCOUNT.*.DISCONNECT            # System events: receive disconnection notifications
  # Residual: same account wildcard note as BYOH hosts (see M4 above)
```

**Identical structure to BYOH hosts.** With host-scoped subjects, the platform controller no longer needs a wildcard user (`mclaude.users.*.hosts.us-east.>`). It subscribes to `mclaude.hosts.us-east.>` — the same pattern as any other host. The control-plane publishes provisioning requests here after validating group membership. Zero JetStream access.

---

### Control Plane

```
(Full access — holds the account signing key. Operator-trusted infrastructure.)
```

The control-plane is the JWT issuer and the authoritative writer for project/host KV state. It connects to NATS with its own credentials and has access to all resources. If the control-plane is compromised, all credentials are compromised. Scoping it further is not meaningful — it IS the trust root for non-operator identities.

## NATS Subject Derivation Reference

How each NATS subject pattern is composed. Use this to verify the permission specs above.

**Sources:**
- [JetStream wire API Reference](https://docs.nats.io/reference/reference-protocols/nats_api_reference) — stream, consumer, and message API subjects; ACL patterns
- [ADR-115: Get Message Enhancement (Direct API)](https://github.com/nats-io/nats-architecture-and-design/issues/115) — subject-form direct-get (`$JS.API.DIRECT.GET.<stream>.<subject>`), permission scoping rationale
- [nats.net#770: Subject in GetDirectAsync differs across client libraries](https://github.com/nats-io/nats.net/issues/770) — confirms Go client uses subject-form by default, `.NET` fixed to match
- [nats.py#193: Reconnect on JWT expiry](https://github.com/nats-io/nats.py/issues/193) — confirms NATS server disconnects clients on JWT expiry
- [NATS JWT Guide](https://docs.nats.io/running-a-nats-service/nats_admin/security/jwt) — JWT fields including `max_connections`, TTL, revocation lists
- NATS server source: KV subjects in `kv.go`, Object Store subjects in `object.go`, consumer create filtered form in `jetstream_api.go`

### KV (Key-Value)

KV is built on JetStream streams. Bucket `foo` is backed by stream `KV_foo`.

| Operation | Subject | Notes |
|-----------|---------|-------|
| **Put** (write key) | `$KV.foo.mykey` | Client publishes value to this subject. Multi-token keys supported: `$KV.foo.a.b.c` |
| **Watch** (subscribe to changes) | `$KV.foo.>` | Push delivery. Client creates a consumer with filter `$KV.foo.>` (or narrower) and subscribes to the delivery subject. Messages arrive on `$KV.foo.<key>`. |
| **Get** (direct-get, payload form) | Pub to `$JS.API.DIRECT.GET.KV_foo` with payload `{"last_by_subj":"$KV.foo.mykey"}` | Response on `_INBOX`. Key is in payload, not subject — **cannot be permission-scoped by key**. |
| **Get** (direct-get, subject form) | Pub to `$JS.API.DIRECT.GET.KV_foo.$KV.foo.mykey` | Response on `_INBOX`. Key is the **full message subject** `$KV.<bucket>.<key>` appended to the API subject. **Can be permission-scoped.** Go client uses this form by default. |
| **Delete** (tombstone) | `$KV.foo.mykey` with `KV-Operation: DEL` header | Same subject as Put. |

**Key insight:** The "key" in KV direct-get subject form is the full message subject (`$KV.<bucket>.<key>`), not just the key portion. This is why permission entries look like `$JS.API.DIRECT.GET.KV_foo.$KV.foo.prefix.>` — the bucket name appears twice.

### Object Store

Object Store is built on JetStream streams. Bucket `bar` is backed by stream `OBJ_bar`.

| Operation | Subject | Notes |
|-----------|---------|-------|
| **Put chunk** (upload) | `$O.bar.C.<sha256>` | Content-addressed. **No object name or key in the chunk subject.** |
| **Put metadata** (upload) | `$O.bar.M.<object-name>` | Object name can contain `.` tokens: `$O.bar.M.path.to.file` |
| **Get** (download) | Creates an ordered consumer on `OBJ_bar`, filtered to the object's chunk subjects. Chunks delivered via push subscription to `$O.bar.C.<sha256>`. Metadata fetched via direct-get: `$JS.API.DIRECT.GET.OBJ_bar.$O.bar.M.<object-name>` |
| **Watch** (list/observe) | Consumer on `OBJ_bar` filtered to `$O.bar.M.>` | Metadata-only watch. |

**Key insight:** Chunk subjects are content-addressed by SHA256 — no object name, path, or owner info. Per-object scoping via subject permissions requires enumerating exact chunk SHAs. Per-bucket scoping (`$O.bar.>`) is the practical minimum for standing access.

### JetStream API

All JetStream API calls are request/reply: client publishes to `$JS.API.*`, server responds on `_INBOX.>`.

| Operation | Subject | Notes |
|-----------|---------|-------|
| **Stream info** | `$JS.API.STREAM.INFO.<stream>` | Per-stream, not per-key. Returns aggregate metadata. |
| **Consumer create** (unfiltered) | `$JS.API.CONSUMER.CREATE.<stream>` | Config in payload. Legacy form — modern clients prefer filtered form. |
| **Consumer create** (filtered, single filter) | `$JS.API.CONSUMER.CREATE.<stream>.<consumer>.<filter_subject>` | `<consumer>` is the consumer name (often auto-generated UUID). `<filter_subject>` is the **full message subject pattern** being filtered on (e.g., `$KV.foo.prefix.>`). NATS 2.10+. |
| **Consumer info** | `$JS.API.CONSUMER.INFO.<stream>.<consumer>` | Point lookup by consumer name. |
| **Consumer list/names** | `$JS.API.CONSUMER.LIST.<stream>`, `$JS.API.CONSUMER.NAMES.<stream>` | Enumerates all consumers. **Not granted to any non-CP identity.** |
| **Stream create/delete/purge** | `$JS.API.STREAM.CREATE.<stream>`, etc. | **Not granted to any non-CP identity.** |
| **Message ack** | `$JS.ACK.<stream>.<consumer>.<delivered>.<stream_seq>.<consumer_seq>.<timestamp>.<pending>` | Consumer-specific. The `>` wildcard after stream covers all consumer tokens. |
| **Flow control** | `$JS.FC.<stream>.<consumer_id>.<sequence>` | Server sends FC message; client must reply. Per-stream scoping: `$JS.FC.<stream>.>` |

**Key insight for consumer create:** The filter subject in position 3+ is the **full message subject**, not a key. For KV bucket `foo` with key `a.b.c`, the filter is `$KV.foo.a.b.c`, making the full API subject `$JS.API.CONSUMER.CREATE.KV_foo.<consumer>.$KV.foo.a.b.c`. The bucket name appears in both the stream name and the filter.

### NATS Core

| Operation | Subject | Notes |
|-----------|---------|-------|
| **Request/reply** | Pub to any subject with reply-to `_INBOX.<random>` | Response arrives on `_INBOX.<random>`. All JetStream API uses this. |
| **System events** | `$SYS.ACCOUNT.<account_id>.CONNECT`, `$SYS.ACCOUNT.<account_id>.DISCONNECT` | Published by server, not clients. Clients need Sub permission to receive. |

### mclaude Resource Naming

| Resource type | Stream name | Subject prefix | Key structure |
|---------------|-------------|----------------|---------------|
| Sessions KV | `KV_mclaude-sessions-{uslug}` | `$KV.mclaude-sessions-{uslug}.` | `{hslug}.{pslug}.{sslug}` |
| Projects KV | `KV_mclaude-projects-{uslug}` | `$KV.mclaude-projects-{uslug}.` | `{hslug}.{pslug}` |
| Hosts KV | `KV_mclaude-hosts-{uslug}` | `$KV.mclaude-hosts-{uslug}.` | `{hslug}` |
| Job Queue KV | `KV_mclaude-job-queue-{uslug}` | `$KV.mclaude-job-queue-{uslug}.` | `{hslug}.{pslug}.{jobid}` |
| Events stream | `MCLAUDE_EVENTS_{uslug}` | `MCLAUDE_EVENTS_{uslug}.` | `{hslug}.{pslug}.{sslug}.{seq}` |
| Imports Object Store | `OBJ_mclaude-imports-{uslug}` | `$O.mclaude-imports-{uslug}.` | `C.<sha256>` / `M.<object-name>` |
| App subjects | — | `mclaude.users.{uslug}.hosts.{hslug}.` | `projects.{pslug}.>` |
| Host subjects | — | `mclaude.hosts.{hslug}.` | `api.>` |

## Component Changes

### mclaude-control-plane
- `nkeys.go`: Rewrite `UserSubjectPermissions()`, `SessionAgentSubjectPermissions()`, `HostSubjectPermissions()` — permissions now reference per-user resource names (`KV_mclaude-sessions-{uslug}`, `MCLAUDE_EVENTS_{uslug}`, etc.). `SessionAgentSubjectPermissions()` now takes project slug + host slug and produces per-project scoped permissions.
- `nkeys.go`: Remove `$JS.API.>` and `$JS.*.API.>` wildcards from all identity types
- `nkeys.go`: Remove all `$JS.*` from `HostSubjectPermissions()` entirely
- `nkeys_test.go`: Update tests to verify new permissions deny cross-user access
- `auth.go`: On JWT refresh, issue new JWT with tightened permissions
- New: `mclaude.hosts.{hslug}.api.agents.credentials` NATS subscriber — host controllers request agent JWTs here. CP validates group membership + project ownership + host assignment, then mints per-project scoped agent JWT and returns it. This is the **only** path for agent JWT issuance.
- New: bucket/stream lifecycle management — create per-user KV buckets + event stream on user registration; create on demand if missing at runtime
- Data migration: move existing shared-bucket data to per-user buckets (one-time migration job)
- Remove account key from host registration response and cluster registration response — hosts no longer receive the signing key

### mclaude-controller-k8s
- Remove account key from controller entirely — controller no longer mints session-agent JWTs
- New: on project provisioning, request agent credentials from control-plane via `mclaude.hosts.{hslug}.api.agents.credentials` (NATS request/reply)
- Write received JWT + NKey seed into K8s Secret (same as today, just sourced from CP instead of locally generated)
- Remove `$JS.*.API.>` from controller's own JWT (it doesn't need JetStream)

### mclaude-session-agent
- Update KV bucket names from `mclaude-sessions` to `mclaude-sessions-{uslug}` (read from config/env)
- Update event stream name from `MCLAUDE_EVENTS` to `MCLAUDE_EVENTS_{uslug}`
- New credential refresh loop: periodically refreshes NATS JWT before TTL expiry (similar to SPA's 60s check cycle)
- On `permissions violation` error: immediate refresh + retry
- Credential refresh endpoint: NATS request/reply to control-plane for new JWT

### mclaude-web
- Update KV bucket names in `nats-client.ts` / `subj.ts` to use `mclaude-sessions-{uslug}`, `mclaude-projects-{uslug}`, etc.
- Update event stream name to `MCLAUDE_EVENTS_{uslug}`
- User slug available from auth store at connection time

### mclaude-cli
- Use per-user bucket/stream names (provided by login response or derived from user slug)

## Data Model
No schema changes. This ADR only changes the permission strings embedded in NATS user JWTs.

## Error Handling

| Error | Component | Behavior |
|-------|-----------|----------|
| Permission denied on JetStream operation | Any client | NATS returns `permissions violation` error. Client logs the denied subject and surfaces clear error. |
| Old user JWT with broad permissions | SPA/CLI | JWT refresh (every 60s for SPA, on-demand for CLI) issues new tightened JWT. Grace period: max 1 JWT TTL (~8h). |
| Old session-agent JWT | Session-agent | Proactive refresh (before TTL expiry) issues new tightened JWT from control-plane. Grace period: max 1 agent JWT TTL (e.g., 24h). |
| Permission error triggers immediate refresh | Session-agent | If a NATS operation fails with `permissions violation`, agent immediately attempts credential refresh before retrying. This handles edge cases where the agent's cached JWT predates the permission change. |
| Host with old JWT | Host registration | Hosts also adopt TTL + refresh. `mclaude host register` re-run if immediate refresh is needed. |

## Security
This ADR IS the security improvement. After implementation:
- Users cannot access other users' KV entries or Object Store buckets
- Session-agents are scoped to their user's namespace
- Hosts cannot access arbitrary JetStream resources
- The principle of least privilege is enforced at the NATS level

## Impact

Specs updated:
- `docs/mclaude-control-plane/spec-control-plane.md` — updated NATS permission model
- `docs/spec-state-schema.md` — permission model for KV buckets and Object Store
- `docs/mclaude-common/spec-common.md` — if permission helpers are shared

Components implementing the change:
- mclaude-control-plane (JWT issuance)
- mclaude-controller-k8s (session-agent JWT issuance)

## Scope

**v1:**
- Migrate from shared JetStream resources to per-user resources (KV buckets, event streams, Object Store buckets)
- Rewrite all JWT permission functions to reference per-user resource names
- Remove all `$JS.*` permissions from host/controller JWTs
- Data migration job: copy existing shared-bucket data to per-user buckets
- Update SPA, session-agent, CLI to use per-user resource names
- Add credential refresh loop to session-agent (TTL + NKey challenge)
- Reissue long-lived credentials (session-agent, host)

**Deferred:**
- Separate NATS accounts per user (full tenant isolation)
- Per-stream/per-consumer fine-grained ACLs beyond subject-level
- Audit logging for permission violations
- Shared sessions (pair programming, handoff, supervision, demo). Per-user isolation doesn't preclude sharing but sharing must be explicit and mediated — e.g., control-plane mirrors events to a second user's stream, or scoped time-limited credentials grant read-only access to a specific session. Not ambient access.
- Host eviction flow: when a user is removed from a host's group, the host owner controls the eviction policy (immediate termination or grace period). Before termination, session data (JSONL, worktree, memories) is archived to the user's Object Store bucket. The host controller kills the user's session-agents and deletes local data after archival completes. Control-plane updates project status and may migrate projects to another host the user has access to.

## Open questions

(none remaining)

## Open questions

(none remaining — factual items resolved)

**Resolved factual items:**
- NATS Object Store subjects: `$O.<bucket>.C.<sha256>` (chunks), `$O.<bucket>.M.<name>` (metadata). Backed by JetStream stream `OBJ_<bucket>`. Permission pattern `$O.mclaude-imports-{uslug}.>` covers all object operations.
- NATS deny lists take precedence over allow lists (standard NATS behavior). Confirmed.

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| User cannot access other user's KV | User A's JWT is denied when reading `$KV.mclaude-sessions.{userB}.>` | Create two test users, issue JWTs. Teardown: delete users. | control-plane (JWT issuance), NATS |
| User cannot access other user's Object Store | User A's JWT is denied when reading `$O.mclaude-imports-{userB}.>` | Create two test users + Object Store buckets. Teardown: delete users + buckets. | control-plane, NATS |
| Session-agent scoped to user | Session-agent JWT for user A is denied when accessing user B's sessions KV | Create two users, issue session-agent JWTs. Teardown: delete users. | control-plane/controller, NATS |
| SPA continues working with tightened permissions | All existing SPA operations (KV watch, events, publish) work with new JWT | Login as test user, exercise all SPA NATS operations. | web, control-plane, NATS |
| JWT refresh issues tightened permissions | After refresh, old broad permissions are replaced with scoped ones | Login with legacy JWT, trigger refresh, verify new permissions. | control-plane |
| Host cannot access arbitrary streams | Host JWT is denied on `$JS.API.STREAM.INFO.KV_mclaude-sessions` | Create test host, issue JWT. Teardown: delete host. | control-plane, NATS |

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| mclaude-control-plane (permissions) | ~150 | ~65k | Rewrite 3 permission functions + add Object Store helper |
| mclaude-control-plane (tests) | ~200 | ~80k | Cross-user denial tests, integration tests |
| mclaude-controller-k8s (permissions) | ~50 | ~30k | Sync session-agent permissions |
| mclaude-session-agent (refresh) | ~150 | ~65k | Credential refresh loop, NATS reconnect on new JWT |

**Total estimated tokens:** ~240k
**Estimated wall-clock:** ~3-4h
