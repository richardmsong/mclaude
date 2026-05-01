# ADR: NATS JetStream Permission Tightening

**Status**: draft
**Status history**:
- 2026-04-29: draft

## Overview
Tighten NATS JetStream permissions for all identity types (user, session-agent, host) from broad wildcards (`$JS.API.>`, `$JS.*.API.>`) to explicit, scoped allow-lists. Binary data (imports, attachments) flows through S3 with pre-signed URLs (see ADR-0053), not NATS Object Store.

## Motivation
The current NATS permission model grants every identity type broad JetStream API access:

| Identity | Current permission | What it allows |
|----------|-------------------|----------------|
| User (SPA/CLI) | `$JS.API.>` pub+sub | ALL JetStream operations on ALL streams and KV buckets |
| Session-agent | `$JS.API.>`, `$JS.*.API.>`, unscoped `$KV.mclaude-sessions.>` etc. | ALL JetStream ops + access to ALL users' KV entries |
| Host | `$JS.*.API.>` | ALL domain-prefixed JetStream operations |

**Consequences:**
- A compromised user JWT can read/write/delete any other user's sessions, projects, hosts
- A rogue session-agent can access all users' data across all KV buckets
- A compromised host credential has full JetStream access across all domains
- Without scoped permissions, any user JWT can access any KV bucket or stream

The fix: replace broad wildcards with the minimum JetStream subjects each identity type needs, scoped to their own user/host/project namespace.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Isolation model | Per-user JetStream resources | Hard NATS-level isolation. Separate KV buckets and sessions streams per user. No payload-inspection gaps. NATS handles many streams at scale. Binary data (imports, attachments) in S3 (ADR-0053). |
| Approach | Replace wildcards with explicit allow-lists referencing per-user resource names | Each identity's JWT lists the exact stream/bucket names it can touch. |
| KV buckets | Per-user: `mclaude-sessions-{uslug}`, `mclaude-projects-{uslug}`. Shared: `mclaude-hosts` (per-host read scoping in JWT). No job queue KV — quota-managed sessions use the session KV with extended fields (ADR-0044). | Per-user buckets eliminate shared-bucket prefix-scoping gaps. Hosts KV is a single shared bucket (`mclaude-hosts`) — CP writes once per `$SYS` event — but **read access is scoped per-host in the user JWT**. Each user's JWT lists explicit host slugs they can read (derived from the user's host access list in Postgres at issuance time). On access change, CP revokes the user's JWT and the SPA reconnects with an updated JWT containing the new host list. No app-layer filtering needed — NATS enforces host visibility. The owner grants per-user access via `manage.grant`; CP stores grants in the `host_access` table. |
| Sessions stream | Per-user: `MCLAUDE_SESSIONS_{uslug}` (consolidates events, commands, lifecycle) | One stream per user captures all session activity under `mclaude.users.{uslug}.hosts.*.projects.*.sessions.>`. Consumer creation is scoped by stream name in the subject. No filter-bypass gap. |
| Binary data | S3 with pre-signed URLs (ADR-0053) | Imports and attachments go through S3, not NATS. Eliminates Object Store stream proliferation, one-shot JWTs, and chunk-level permission complexity. |
| Host/controller JetStream | None — removed entirely | Hosts and controllers only use NATS subjects for provisioning. They never needed JetStream. |
| Host subject scheme | `mclaude.hosts.{hslug}.>` (supersedes ADR-0035's `mclaude.users.{uslug}.hosts.{hslug}.>` for host-side subscriptions) | Removes user prefix from host subscriptions. Host access enforced by control-plane (which already intercepts all requests). Host JWT is constant-size regardless of how many users share the host. User/agent/SPA subjects unchanged. |
| Session-agent scope | Per-project | Each agent runs per-project and gets a JWT scoped to that project's KV keys only. Stolen credential on a shared BYOH host exposes one project, not all of the user's data across all hosts. No credential reissuance concern — each new project spawns a new agent with its own JWT at spawn time. |
| Credential lifecycle | Short TTL + proactive refresh for all identity types. All identities generate their own NKeys — CP never handles private key material. | Host JWTs: 5 min TTL. Session-agent JWTs: 5 min TTL. User JWTs: keep existing ~8h TTL (SPA already refreshes every 60s). On host access change, CP revokes affected user JWTs and the SPA reconnects with a new JWT reflecting the updated host list. All refresh via NATS-authenticated request (connection-level JWT + NKey validation by NATS server). NKey generation: SPA/CLI generates NKey pair in browser/process (`localStorage` for SPA — shared across tabs), sends public key at login. Hosts generate at registration. Agents generate at startup. |
| Session-agent JWT issuance | Control-plane only (host controllers no longer hold the account key) | Hosts request agent credentials from the control-plane via NATS. CP validates host access, project ownership, and host assignment before minting. Removes the account signing key from all host controllers — hosts can request credentials but not forge them. |
| Credential auth (all identities) | HTTP challenge-response | All identity types (user, host, agent) authenticate and refresh via the same HTTP endpoint: `POST /api/auth/challenge {nkey_public}` → `POST /api/auth/verify {nkey_public, challenge, signature}` → JWT. Bootstrap and refresh are the same code path. NATS is used only for public key registration (attestation), not for authentication or credential issuance. |
| NATS topology | All agents (BYOH and K8s) connect directly to hub NATS | Leaf-node topology removed from scope. When a leaf node has a JetStream domain set, `$JS.API.>` is suppressed — clients must use `$JS.{domain}.API.*` to reach hub. This adds complexity and brittleness (host operator must configure NATS correctly). Direct hub connection is simpler, more robust, and keeps one set of permission specs. Leaf-node topology can be added later as opt-in for offline resilience if needed. |
| Migration | Self-healing via credential refresh | Deploy new permission code. On next refresh cycle (within 24h), session-agents automatically receive tightened JWTs. For immediate effect, restart agents to trigger immediate refresh. |
| Bucket lifecycle | Control-plane creates per-user buckets on user registration; shared `mclaude-hosts` bucket created on deployment | Per-user buckets are provisioned once per user. The shared `mclaude-hosts` bucket is created once on deployment. Read access to host keys is controlled per-host in the user JWT (not at the bucket level). If a bucket is missing at runtime, the control-plane creates it on demand. |
| Host access model | Deferred to a future ADR. For now, the host owner (registering user) controls access. CP determines which users can use which hosts and encodes it in the JWT. | The NATS layer is access-model-agnostic — it enforces JWT permissions, not application-layer policies. |

## Threat Model

### Permission Diagrams

#### 1. Identity Hierarchy

Who owns what, and what credentials they hold.

```
                         Platform Operator
                           (fully trusted)
                     ┌──────────┼──────────────────┐
                     ▼          ▼                   ▼
              Control Plane   NATS Hub       K8s Controller us-east
            (account signing  Server         JWT: hosts.us-east
             key, issues all
                 JWTs)

   User A (alice)                          User B (bob)
   ├── SPA / CLI                           ├── SPA / CLI
   │   JWT: users.alice.hosts.*            │   JWT: users.bob.hosts.*
   ├── BYOH Host laptop-a                  ├── BYOH Host laptop-b
   │   JWT: hosts.laptop-a                 │   JWT: hosts.laptop-b
   └── Session-Agent                       └── Session-Agent
       JWT: per-user, 5 min TTL               JWT: per-user, 5 min TTL
```

#### 2. Proposed Permission Map

Three-layer architecture separating user identity, data, and compute. **Host operator ≠ user**: Bob owns BYOH host "laptop-b", but alice's session-agent on that host accesses alice's data partition — not bob's. Hosts live in the compute layer and have zero JetStream access; only session-agents (which carry per-user/per-project JWTs) touch JetStream resources.

```
════════════════════════════ User Identity Layer ════════════════════════════

  Alice (SPA/CLI)                                    Bob (SPA/CLI)
       │                                                  │
       │ R: KV watch/get                                  │ R: KV watch/get
       │ R/W: upload imports                              │ R/W: upload imports
       │ R: subscribe sessions                            │ R: subscribe sessions
       ▼                                                  ▼
════════════════════════════ NATS Data Layer ═════════════════════════════════

  ┌─── alice's partition ─────────┐  ┌─── bob's partition ──────────┐
  │ KV: sessions-alice            │  │ KV: sessions-bob             │
  │ KV: projects-alice            │  │ KV: projects-bob             │
  │ Stream: SESSIONS_alice        │  │ Stream: SESSIONS_bob         │
  └──────────▲──────────▲─────────┘  └──────────▲──────────▲────────┘
             │          │    ┌── KV: hosts ──┐   │          │
             │          │    │   (shared)    │   │          │
             │          │    └──────────────-┘   │          │
════════════════════════════ Compute Layer ═══════════════════════════════════

  ┌─── Bob's BYOH: laptop-b ──────────────┐  ┌─── K8s Host: us-east ──────────────┐
  │ owner: bob                             │  │ owner: operator                     │
  │ members: alice, bob                    │  │                                     │
  │                                        │  │ Controller ·····× no JetStream      │
  │ Controller ·····× no JetStream         │  │                                     │
  │                                        │  │ ns: mclaude-alice                   │
  │ Agent: alice/myapp ──► alice's data    │  │   Agent: alice/webapp ──► alice's   │
  │   (alice's per-project JWT)            │  │     (alice's per-project JWT)       │
  │                                        │  │                                     │
  │ Agent: bob/proj ──► bob's data         │  │ ns: mclaude-bob                     │
  │   (bob's per-project JWT)              │  │   Agent: bob/api ──► bob's data     │
  │                                        │  │     (bob's per-project JWT)         │
  └────────────────────────────────────────┘  └─────────────────────────────────────┘

  Agents connect UP to their user's data partition, not the host operator's.
```

**Key properties — three-layer separation:**
- **User Identity Layer** (top): SPA/CLI clients are the data owners. Alice and bob each see only their own partition.
- **NATS Data Layer** (middle): Per-user JetStream partitions (KV buckets, sessions streams). The `mclaude-hosts` KV is a shared bucket, but read access is scoped per-host in the user JWT — users can only read keys for hosts they have access to. Binary data (imports, attachments) handled by S3 (ADR-0053), not NATS.
- **Compute Layer** (bottom): ALL hosts — both BYOH and K8s — live here. Hosts are **not** inside user subgraphs. A host has an owner, and the owner controls who can use it (access model deferred to future ADR).
- **Host operator ≠ user**: Bob owns "laptop-b" (BYOH host). Alice has access to bob's host. Alice's session-agent runs on bob's host with **alice's per-project JWT** — it accesses **alice's data partition**, not bob's. The arrows from `Agent: alice/myapp` go UP to alice's partition, not bob's.
- **Hosts have zero JetStream access**: Controllers use only NATS core pub/sub (`mclaude.hosts.{hslug}.>`) for provisioning. The dashed-x edges indicate no JetStream connectivity. Only session-agents — which carry per-user/per-project scoped JWTs minted by the control-plane — touch JetStream resources.
- **K8s host**: Operator-managed, same pattern. Agents for multiple users run in separate namespaces, each with its own per-user/per-project JWT.
- Each JWT contains the user slug in every resource name — it cannot reference another user's buckets/streams.
- Users (SPA/CLI) get read-only KV (sessions, projects, hosts), read-only sessions stream.
- Session-agents get read/write KV + sessions stream, scoped to one project.

#### 3. Per-User Partition Detail (alice)

Zoom into alice's NATS partition. Hosts on the left as writers, alice's SPA on the right as reader. Three nested permission boundaries: user (NATS enforced), host (key prefix), project (key prefix). `W──►` = allowed write, `X` = denied by JWT scope.

Alice has two projects: `project-a` on bob's BYOH host `laptop-b`, and `project-b` on K8s host `cluster-a`.

```
 Writers                                                                                                  Readers          
 ───────                                                                                                  ───────          
                                                                                                                           
                           ╔═════════════════════════════════════════════════════════════════════╗                         
                           ║ USER BOUNDARY — bob's JWT cannot reference *-alice resources        ║                         
                           ║                                                                     ║                         
                           ║ ┌──────────── KV: sessions-alice ─────────────────────────────┐     ║                         
                           ║ │                                                             │     ║                         
                           ║ │ ┌ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ┐   │     ║                         
                           ║ │ : HOST BOUNDARY  laptop-b.*                             :   │     ║                         
                           ║ │ :                                                       :   │     ║                         
 ┌── HOST: laptop-b ───┐   ║ │ :                                                       :   │     ║                         
 │ project-a agent──W──┼───║─┼─:──► laptop-b.project-a.sess-001 ───────────────────────:───┼─────║──R──► Alice SPA         
 │            ┌─────X──┼───║─┼─:──X laptop-b.project-b.*  (DENIED — wrong project)     :   │     ║       (reads all        
 └────────────┼────────┘   ║ │ :                                                       :   │     ║         own keys)       
              │            ║ │ └ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ┘   │     ║            │            
              │            ║ │                                                             │     ║            │            
┌─────────────┘            ║ │ ┌ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ┐   │     ║            │            
│                          ║ │ : HOST BOUNDARY  cluster-a.*                            :   │     ║            │            
│                          ║ │ :                                                       :   │     ║            │            
│┌── HOST: cluster-a ──┐   ║ │ :                                                       :   │     ║            │            
││ project-b agent──W──┼───║─┼─:──► cluster-a.project-b.sess-003 ──────────────────────:───┼─────║────────────┘            
││            └────X───┼─┬─║─┼─:──X cluster-a.project-a.* (DENIED — wrong project)     :   │     ║                         
│└─────────────────────┘ │ ║ │ :                                                       :   │     ║                         
│                        │ ║ │ :                                                       :   │     ║                         
└──DENIED - wrong host─X─┘ ║ │ └ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ┘   │     ║                         
                           ║ │                                                             │     ║                         
                           ║ │ ··· R4: STREAM.INFO leaks aggregate stats across ALL keys   │     ║                         
                           ║ └─────────────────────────────────────────────────────────────┘     ║                         
                           ║                                                                     ║                         
                           ║ ┌──────────── KV: projects-alice ─────────────────────────────┐     ║                         
                           ║ │                                                             │     ║                         
                           ║ │ ┌ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ┐   │     ║                         
   CP ─────────────────W───║─┼─:──► (writes on provisioning)                           :   │     ║                         
 ┌── HOST: laptop-b ───┐   ║ │ :                                                       :   │     ║                         
 │ project-a agent──W──┼───║─┼─:──► laptop-b.project-a  (project state) ───────────────:───┼─────║──R──► Alice SPA         
 │            └────X───┼───║─┼─:──X cluster-a.* (DENIED)                               :   │     ║                         
 │            └────X───┼───║─┼─:──X laptop-b.project-b (DENIED)                        :   │     ║                         
 └─────────────────────┘   ║ │ :                                                       :   │     ║                         
                           ║ │ └ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ┘   │     ║                         
                           ║ └─────────────────────────────────────────────────────────────┘     ║                         
                           ║                                                                     ║                         
                           ║   (No job-queue KV — quota-managed sessions use session KV)  ║                         
                           ║                                                                     ║                         
                           ║   (Binary data: imports + attachments via S3 pre-signed URLs)       ║                         
                           ║   (ADR-0053 — no NATS Object Store)                                 ║                         
                           ║                                                                     ║                         
                           ║ ┌──────────── Stream: SESSIONS_alice ─────────────────────────┐     ║                         
                           ║ │ filter: ...users.alice.hosts.*.projects.*.sessions.>        │     ║                         
                           ║ │                                                             │     ║                         
                           ║ │ ┌ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ┐   │     ║                         
                           ║ │ : HOST BOUNDARY  consumer filtered to host.project      :   │     ║                         
 ┌── HOST: laptop-b ───┐   ║ │ :                                                       :   │     ║                         
 │ project-a agent──W──│───║─┼─:──► ...laptop-b.project-a.sessions.*.events   ──────┬──:───┼─────║──R──► Alice SPA         
 │            └────X───│───║─┼─:──X ...cluster-a.* (DENIED)                         │  :   │     ║                         
 │            └────X───┼───║─┼─:──X ...laptop-b.project-b.sessions.*.events (DENIED)│  :   │     ║                         
 └─────────────────────┘   ║ │ :                                                    └──:───┼─────║──X    project-b agent   
                           ║ │ :                                                       :   │     ║       cluster-a.* agents
   Alice SPA───────W───────║─┼─:──► ...laptop-b...project-a.sessions.*.input           :   │     ║                         
                           ║ │ :                                                       :   │     ║                         
                           ║ │ :                                                       :   │     ║                         
                           ║ │ └ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ┘   │     ║                         
                           ║ │ ··· R4: STREAM.INFO leaks aggregate stats across ALL        │     ║                         
                           ║ └─────────────────────────────────────────────────────────────┘     ║                         
                           ║                                                                     ║                         
                           ╚═════════════════════════════════════════════════════════════════════╝                         
OUTSIDE USER BOUNDARY:

┌──────────── KV: hosts (SHARED) ────────────────────────────────────┐
│ Shared bucket. Read access per-host in user JWT.                   │
│ On host access change, CP revokes JWT → SPA reconnects.            │
│                                                                    │
│ CP ($SYS) ──W──► laptop-b  {online: true,  lastSeenAt: ...}        │──R──► All SPAs
│                  cluster-a {online: true,  lastSeenAt: ...}        │       (per host in JWT)
│                  laptop-a  {online: false, lastSeenAt: ...}        │  
└────────────────────────────────────────────────────────────────────┘

Legend:
  ═══  user boundary   (NATS enforced — bucket/stream name contains user slug)
  ─ ─  host boundary   (JWT key-prefix scoped to host.project.*)
  W──► allowed write   X denied write (JWT scope mismatch)
  ──R──► read          ··· R4 leak (STREAM.INFO crosses boundaries)
```

**Permission boundary summary:**
- **User boundary** (`═══`): Enforced by NATS server. Bucket and stream names embed the user slug (`*-alice`). Bob's JWT cannot reference alice's resources at all. This is the hard security boundary.
- **Host+project boundary** (`─ ─`): Enforced by JWT key-prefix scoping. An agent for `laptop-b/project-a` has `$KV.mclaude-sessions-alice.hosts.laptop-b.projects.project-a.sessions.>` in its JWT. It can write to keys under that prefix (`W──►`) but is denied (`X`) on keys for other hosts or other projects on the same host. Consumer filters on the sessions stream enforce the same boundary for message delivery.
- **R4 leak** (`···`): `$JS.API.STREAM.INFO` crosses all internal boundaries. Any agent with stream info permission can query aggregate stats (message count, last activity time, subject cardinality) for the entire per-user bucket/stream — not just its own host/project. This is metadata surveillance; see R4 for the full field-by-field analysis.
- **Shared hosts KV**: The `mclaude-hosts` bucket is shared, but read access is per-host in the JWT. Users can only read keys for hosts they have access to. On access change, CP revokes the user JWT — the SPA reconnects with updated host permissions.

### Trust Tiers

| Identity | Trust level | What they control | What they should NOT access |
|----------|-------------|-------------------|---------------------------|
| **Platform operator** | Fully trusted | NATS, control-plane, platform hosts. Holds account signing key. | N/A — they own everything. |
| **User** (SPA/CLI) | Partially trusted | Their own projects, sessions, hosts. Can register BYOH hosts. | Other users' projects, sessions, hosts, imports. |
| **Host operator** (BYOH) | Partially trusted | Local filesystem, local controller, session-agents on their machine. May host other users' sessions. | Activity of other users on OTHER hosts. If alice uses bob's laptop, bob has root access to alice's sessions on his machine (accepted — he owns the hardware). But bob should not be able to see what alice is doing on hosts that are not bob's laptop. The per-project agent JWT scoping ensures this: bob can extract agent credentials from his machine, but those credentials are scoped to alice's project on bob's host — they cannot access alice's sessions on her own laptop or on a K8s cluster. Stream info metadata leakage (R4) is the exception: a stolen agent credential reveals aggregate metadata across all of alice's per-user streams, not just the project on bob's host. |
| **Platform host controller** | Operator-trusted infrastructure | Provisioning for ALL users on that cluster. Has wildcard user scope. | JetStream resources outside its operational needs (it doesn't need KV/ObjectStore access). |
| **Session-agent** | Per-user scoped service | Sessions and projects for its assigned user, scoped to one project on one host. | Other users' sessions, projects, hosts, imports. Other projects for the same user (per-project scoping). |

### Isolation Boundaries Summary

| Boundary | Enforcement mechanism | Residual risk |
|----------|---------------------|---------------|
| User A ↔ User B (NATS subjects) | Subject-level pub/sub scoping (`mclaude.users.{uslug}.hosts.*.>`) | None |
| User A ↔ User B (JetStream/KV) | Per-user KV buckets + scoped JetStream API subjects (`$JS.API.*.KV_mclaude-sessions-{uslug}`) | R4: stream info metadata leakage (user can query own stream info; no cross-user access) |
| Host ↔ other users (NATS subjects) | `mclaude.hosts.{hslug}.>` — host access enforced by control-plane | None |
| Host ↔ other users (JetStream) | Zero JetStream permissions — no `$JS.*` or `$KV.*` subjects in host JWT | None |
| Session-agent ↔ other users (KV) | Per-project scoped: `$KV.mclaude-sessions-{uslug}.hosts.{hslug}.projects.{pslug}.sessions.>` | R4: stream info metadata leakage (per-user stream info exposes cross-project metadata) |
| Session-agent ↔ other users (JetStream) | Per-user JetStream API subjects (`$JS.API.*.KV_mclaude-sessions-{uslug}`, `$JS.API.*.MCLAUDE_SESSIONS_{uslug}`) | R4: `SubjectFilter` in stream info reveals project/session topology |
| Platform controller ↔ all users | Zero JetStream permissions — provisioning only via NATS core subjects | R7: fraudulent credential requests if CP validation is incomplete |

### Residual Attack Vectors

**R1: Host access authorization** — The host access management endpoints must enforce that only the host owner (or platform operator) can grant access. Without this, any authenticated user could grant themselves access to any host. This is an application-layer authorization check, not a NATS-level concern. The access model is deferred to a future ADR.

**R2: Control-plane compromise** — CP holds the account signing key and is the sole JWT issuer. CP compromise = all credentials compromised. CP unavailability causes cascading credential expiry (5-min TTL for hosts/agents). This is inherent to centralized credential issuance and accepted.

**R3: Membership cache staleness** — 60-second cache TTL means a removed user could obtain credentials for up to 60 seconds after removal. The explicit "refresh now" signal handles the common case; the 60s window is the fallback.

**R4: Stream info / consumer info metadata leakage** — `$JS.API.STREAM.INFO.<stream>` returns the full `StreamState` struct. NATS permissions gate which subjects an identity can *publish to* (i.e., which streams it can query), but do NOT gate the request payload content. This means options like `SubjectFilter` and `DeletedDetails` in the request body are accessible to anyone with publish permission on the stream info subject. A per-project agent credential can query the per-user stream and extract metadata about ALL of the user's projects across all hosts.

**Attacker profile.** The attacker must hold a credential with `$JS.API.STREAM.INFO.<per-user-stream>` in its Pub.Allow. Two identity types have this:

1. **Session agent credential** — The primary threat vector. Agent JWTs are per-project scoped, but stream info subjects are per-user (not per-project), so any agent credential for alice grants stream info across all of alice's per-user streams. To obtain one, the attacker must:
   - **Compromise the host machine** where an agent runs (read credential from the agent process or filesystem), OR
   - **Be a malicious host owner** — the host owner has root access to the box and can extract agent credentials for any project provisioned on their host. This is the most realistic scenario: if alice provisions a project on bob's host, bob can extract the agent credential and use `SubjectFilter` to enumerate alice's full project/session topology across ALL hosts, not just bob's.
   - K8s cluster owners have equivalent access (same root-level control over agent pods).

2. **User JWT (SPA/CLI)** — alice herself has stream info on her own streams. This is not an attack vector (querying your own metadata is expected behavior).

The host controller identity does NOT have stream info permissions (it uses host-scoped subjects only), so compromising just the host controller credential is not sufficient for this attack.

Full `StreamState` response field analysis:

| Field | What it reveals | Threat |
|-------|----------------|--------|
| `Msgs`, `Bytes` | Total message count and byte size | Activity volume across all of alice's projects — attacker learns how active she is |
| `FirstTime` | Timestamp of oldest message | When alice's earliest persisted session activity occurred |
| `LastTime` | **Exact timestamp of most recent message** | **Most sensitive.** When alice was last active on any session on any project. Enables presence tracking. |
| `Consumers` | Number of active consumers | How many SPA tabs, agents, or watchers alice has running right now |
| `NumSubjects` | Number of unique subjects with messages | Cardinality of alice's host+project+session combinations across all hosts |
| `Subjects` (with `SubjectFilter`) | Per-subject message counts | **Worst case.** NATS permissions do not gate request payload content, so any agent with stream info permission can pass `SubjectFilter` and get back a map of every subject (every host.project.session combination) with per-subject message counts. This fully enumerates alice's project/session topology. |
| `NumDeleted` | Count of deleted messages | Whether alice deletes sessions/history |
| `FirstSeq`, `LastSeq` | Sequence numbers | History depth (gap between first and last) |
| `Deleted` (with `DeletedDetails`) | List of deleted sequence numbers | Which specific messages were deleted (requires `DeletedDetails` option in request — also not gated by permissions) |

This is metadata surveillance, not data access. An attacker with a stolen per-project agent credential can learn alice's usage patterns (when she's active, how much she uses the platform, how many projects/sessions she has, which hosts she uses) but cannot read message contents. The `Subjects` map with `SubjectFilter` is the most damaging — it fully enumerates alice's project topology. Per-project streams would close this gap but were rejected due to resource proliferation.

Consumer info leakage is lower-severity: consumer names are ephemeral UUIDs (an attacker must brute-force them), and even if found, consumer info reveals only delivery state (pending count, ack floor), not content. `$JS.API.CONSUMER.LIST` and `$JS.API.CONSUMER.NAMES` are NOT granted — agents cannot enumerate consumers.

**R5: `_INBOX.>` cross-client eavesdropping** — Single-account limitation. Random inbox prefixes make targeted eavesdropping impractical. Future: per-identity inbox prefixes.

**R6: (Resolved — Object Store removed)** — One-shot import JWTs are no longer used. Binary data flows through S3 with pre-signed URLs (ADR-0053). Pre-signed URLs have short TTLs and are scoped to specific S3 keys.

**R7: Secure introduction — fraudulent public key registration** — A compromised host controller can register agent public keys with CP for any user with access to the host (`mclaude.hosts.{hslug}.api.agents.register`). CP validates host access, project ownership, and host assignment before storing the key. NATS subject scoping prevents the host from impersonating a different host (it can only publish to its own `mclaude.hosts.{hslug}.>`). However, the host assignment check is an application-layer guard — if CP's implementation is incomplete or the check is bypassed, the blast radius is limited:

- **No data exfiltration.** The fraudulently-obtained agent credential is scoped to a specific project prefix (e.g., `laptop-a.myapp.>` in KV). If the project doesn't actually run on that host, those keys don't exist — there is nothing to read.
- **Write pollution.** The agent could write fake session/project data into alice's per-user KV namespace under the claimed project prefix. Alice would see a project she didn't create with sessions she didn't run (phishing / confusion vector).
- **Stream info metadata leakage.** The agent gains R4-level metadata visibility into alice's per-user streams. But the host owner already has root on the box and could extract any legitimate agent credential running there — this doesn't expand the existing blast radius for host owners.

**Attestation asymmetry by host type:**

- **K8s controllers** could in theory use Kubernetes ServiceAccount token exchange for secure introduction (projected SA token validated against the cluster's OIDC issuer). However, this provides limited additional security: if an attacker has broken RBAC enough to read the controller's NATS credentials from its Secret, they can almost certainly read agent credentials directly from agent pod Secrets in the same namespace — bypassing the introduction mechanism entirely. KSA token exchange only blocks the narrow scenario where the controller credential leaks through a non-RBAC channel (e.g., logs, monitoring) but agent credentials don't.
- **BYOH controllers** authenticate with NATS JWT + NKey seed on the host filesystem. Anyone with root access can extract these credentials. The security boundary is filesystem permissions — i.e., trust the host owner.

In both models, the host/cluster owner is the trust boundary. The real security boundary is the NATS permission model (what a credential can do once obtained), not the introduction mechanism (how the credential is obtained). The host assignment check at CP issuance time is defense-in-depth, not a security boundary — tightening it is a pure application-logic guard clause requiring no architectural changes. Comprehensive K8s multi-tenant security (RBAC hardening, namespace isolation, secret encryption, network policies, pod security standards) is deferred to a dedicated ADR.

## Full Permission Specifications by Identity

Complete pub/sub allow-lists for each identity type. Every subject is listed with its rationale.
Examples use `alice` as user slug, `laptop-a` as host slug, `us-east` as cluster slug.

### User (SPA / CLI) — example: alice

```
Pub.Allow:
  mclaude.users.alice.hosts.*.>          # Send commands to own hosts (session input, control, config, host management)
  _INBOX.>                               # Request/reply pattern (NATS requirement)
  # KV buckets: sessions/projects/hosts are read-only for users.
  # No Object Store permissions — imports and attachments use S3 with pre-signed URLs (ADR-0053)
  $JS.API.DIRECT.GET.KV_mclaude-sessions-alice.>   # KV get: subject-form, covers all keys (users see all own data)
  $JS.API.DIRECT.GET.KV_mclaude-projects-alice.>   # KV get: read project state
  # Hosts KV: per-host read scoping — one entry per host user has access to (example: laptop-a, cluster-a)
  $JS.API.DIRECT.GET.KV_mclaude-hosts.$KV.mclaude-hosts.laptop-a    # KV get: this host's state
  $JS.API.DIRECT.GET.KV_mclaude-hosts.$KV.mclaude-hosts.cluster-a   # KV get: this host's state
  $JS.API.CONSUMER.CREATE.KV_mclaude-sessions-alice.>   # KV watch: filtered form, any consumer + filter on own bucket
  $JS.API.CONSUMER.CREATE.KV_mclaude-projects-alice.>   # KV watch
  $JS.API.CONSUMER.CREATE.KV_mclaude-hosts.*.$KV.mclaude-hosts.laptop-a    # KV watch: this host only
  $JS.API.CONSUMER.CREATE.KV_mclaude-hosts.*.$KV.mclaude-hosts.cluster-a   # KV watch: this host only
  $JS.API.CONSUMER.CREATE.MCLAUDE_SESSIONS_alice.>      # Session stream: any consumer + filter on own stream
                                                        # Two patterns: (1) dashboard — DeliverNew, lifecycle.> filter (all sessions)
                                                        #               (2) chat view — DeliverAll, one specific session (full replay + live)
  $JS.API.STREAM.INFO.KV_mclaude-sessions-alice       # Stream info: needed by NATS client for KV init
  $JS.API.STREAM.INFO.KV_mclaude-projects-alice       # Stream info
  # No $JS.API.STREAM.INFO.KV_mclaude-hosts — removed to prevent host enumeration via SubjectFilter.
  # Users access hosts KV via direct-get and filtered consumer-create only (both per-host scoped in JWT).
  # IMPLEMENTATION NOTE: SPA must use raw JetStream API (direct-get + consumer-create) for the hosts
  # bucket, not the high-level KV client (e.g., js.KeyValue()) which requires STREAM.INFO internally.
  $JS.API.STREAM.INFO.MCLAUDE_SESSIONS_alice          # Stream info: session stream metadata

  $JS.API.CONSUMER.INFO.KV_mclaude-sessions-alice.*   # Consumer info: needed by NATS client
  $JS.API.CONSUMER.INFO.KV_mclaude-projects-alice.*   # Consumer info
  $JS.API.CONSUMER.INFO.KV_mclaude-hosts.*            # Consumer info (shared bucket; consumers are per-connection, not per-key)
  $JS.API.CONSUMER.INFO.MCLAUDE_SESSIONS_alice.*      # Consumer info: session stream consumers
  $JS.ACK.KV_mclaude-sessions-alice.>    # Ack consumed KV messages
  $JS.ACK.KV_mclaude-projects-alice.>    # Ack consumed KV messages
  $JS.ACK.KV_mclaude-hosts.>              # Ack consumed KV messages (shared bucket; ack tokens are opaque, not key-scoped)
  $JS.ACK.MCLAUDE_SESSIONS_alice.>       # Ack consumed session stream messages
  $JS.FC.KV_mclaude-sessions-alice.>     # Flow control: scoped to own streams
  $JS.FC.KV_mclaude-projects-alice.>
  $JS.FC.KV_mclaude-hosts.>              # Flow control: shared hosts bucket (flow control tokens are opaque)
  $JS.FC.MCLAUDE_SESSIONS_alice.>

Sub.Allow:
  mclaude.users.alice.hosts.*.>          # Receive replies from own hosts
  _INBOX.>                               # Request/reply: all JetStream API responses arrive here.
                                         #   Residual: _INBOX.> allows subscribing to all reply subjects
                                         #   in the account. Low practical risk (random inbox prefixes).
                                         #   Future: per-identity inbox prefixes with allow_responses.
  $KV.mclaude-sessions-alice.hosts.>     # KV watch: push delivery of session state changes
  $KV.mclaude-projects-alice.hosts.>     # KV watch: push delivery of project state changes
  $KV.mclaude-hosts.laptop-a              # KV watch: push delivery of this host's state changes
  $KV.mclaude-hosts.cluster-a            # KV watch: push delivery of this host's state changes
  # Session stream push delivery covered by mclaude.users.alice.hosts.*.> (messages use mclaude.users.* subjects)
  $JS.FC.KV_mclaude-sessions-alice.>     # Flow control: scoped to own streams
  $JS.FC.KV_mclaude-projects-alice.>
  $JS.FC.KV_mclaude-hosts.>              # Flow control: shared hosts bucket (flow control tokens are opaque)
  $JS.FC.MCLAUDE_SESSIONS_alice.>
```

**What alice CANNOT do:** Per-user resources (sessions, projects) all contain `alice` — no wildcards that could match other users' resources. The hosts KV bucket is shared (`mclaude-hosts`), but alice's JWT lists explicit host slugs she can read (derived from her host access list in Postgres). She cannot read host keys she doesn't have access to — NATS enforces this at the subject level. On access change, CP revokes alice's JWT; the SPA reconnects and receives a new JWT with the updated host list. No `mclaude.hosts.{hslug}.users.alice.>` publish permissions — project creation is CP-initiated (SPA uses HTTP `POST /api/users/{uslug}/projects`, CP validates and publishes to the host namespace). Users cannot publish directly to host-scoped subjects. No `$JS.API.STREAM.DELETE.*`, `$JS.API.STREAM.PURGE.*`, or `$JS.API.STREAM.CREATE.*` — users cannot create, delete, or purge streams. No KV writes to sessions, projects, or hosts — those are written by session-agents and the control-plane. No `$O.*` permissions — binary data uses S3 (ADR-0053).

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
baked in (host controller registers the agent's public key via `mclaude.hosts.{hslug}.api.agents.register`, then the agent authenticates via HTTP challenge-response to get its JWT).
New projects get new agents with new JWTs — no existing credential needs updating.

```
Pub.Allow:
  mclaude.users.alice.hosts.laptop-a.projects.myapp.>    # Lifecycle events, session updates — this project only
                                                         # (no credential subjects — auth/refresh is via HTTP challenge-response)
  _INBOX.>                                               # Request/reply (NATS requirement)
  $KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.>     # KV write: create/update/delete sessions for this project
  $KV.mclaude-projects-alice.hosts.laptop-a.projects.myapp               # KV write: update this project's state (e.g., clear importRef)
  # Direct-get: subject-form with full $KV.<bucket>.<key> path (C2 fix)
  $JS.API.DIRECT.GET.KV_mclaude-sessions-alice.$KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.>    # KV get: this project's sessions
  $JS.API.DIRECT.GET.KV_mclaude-projects-alice.$KV.mclaude-projects-alice.hosts.laptop-a.projects.myapp               # KV get: this project's state
  $JS.API.DIRECT.GET.KV_mclaude-hosts.$KV.mclaude-hosts.laptop-a                                                      # KV get: this host's config (read-only, shared bucket)
  # Consumer create: filtered form with full $KV.<bucket>.<key> filter subject (C1 fix)
  $JS.API.CONSUMER.CREATE.KV_mclaude-sessions-alice.*.$KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.>   # KV watch: this project's sessions
  $JS.API.CONSUMER.CREATE.KV_mclaude-projects-alice.*.$KV.mclaude-projects-alice.hosts.laptop-a.projects.myapp              # KV watch: this project only
  $JS.API.CONSUMER.CREATE.KV_mclaude-hosts.*.$KV.mclaude-hosts.laptop-a                                                    # KV watch: this host only (shared bucket)

  $JS.API.CONSUMER.CREATE.MCLAUDE_SESSIONS_alice.*.mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.>   # Session stream: filtered to this project (DeliverNew — agent processes commands as they arrive, no replay)
  $JS.API.STREAM.INFO.KV_mclaude-sessions-alice          # Stream info (NATS client needs this for KV init)
  $JS.API.STREAM.INFO.KV_mclaude-projects-alice
  # No $JS.API.STREAM.INFO.KV_mclaude-hosts — same as user JWT: removed to prevent host enumeration.
  # Agent reads host KV via per-host direct-get and filtered consumer-create only.
  # IMPLEMENTATION NOTE: Session-agent must use raw JetStream API (direct-get + consumer-create) for the
  # hosts bucket, not the high-level KV client (e.g., js.KeyValue()) which requires STREAM.INFO internally.
  $JS.API.STREAM.INFO.MCLAUDE_SESSIONS_alice             # Stream info: session stream metadata
  # No Object Store permissions — imports and attachments use S3 with pre-signed URLs (ADR-0053)
  $JS.API.CONSUMER.INFO.KV_mclaude-sessions-alice.*      # Consumer info (NATS client needs this)
  $JS.API.CONSUMER.INFO.KV_mclaude-projects-alice.*
  $JS.API.CONSUMER.INFO.KV_mclaude-hosts.*               # Consumer info (shared bucket)
  $JS.API.CONSUMER.INFO.MCLAUDE_SESSIONS_alice.*         # Consumer info: session stream consumers
  $JS.ACK.KV_mclaude-sessions-alice.>                    # Ack consumed KV messages (consumer-specific tokens, not key names)
  $JS.ACK.KV_mclaude-projects-alice.>
  $JS.ACK.KV_mclaude-hosts.>                             # Ack consumed KV messages (shared bucket)
  $JS.ACK.MCLAUDE_SESSIONS_alice.>                       # Ack consumed session stream messages
  $JS.FC.KV_mclaude-sessions-alice.>                     # Flow control: scoped to own streams (M2 fix)
  $JS.FC.KV_mclaude-projects-alice.>
  $JS.FC.KV_mclaude-hosts.>                              # Flow control: shared hosts bucket
  $JS.FC.MCLAUDE_SESSIONS_alice.>                        # Flow control: session stream
  mclaude.users.alice.quota                              # Pub: designated agent publishes quota updates (ADR-0044)

Sub.Allow:
  mclaude.users.alice.hosts.laptop-a.projects.myapp.>    # Receive on this project's subjects only
  mclaude.users.alice.quota                              # Receive quota updates from CP (ADR-0044)
  _INBOX.>                                               # Request/reply: all JetStream API responses arrive here
  $KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.>     # KV watch: push delivery of this project's sessions
  $KV.mclaude-projects-alice.hosts.laptop-a.projects.myapp               # KV watch: push delivery of this project's state
  $KV.mclaude-hosts.laptop-a                                              # KV watch: push delivery of this host's config only (shared bucket)
  # Session stream push delivery covered by mclaude.users.alice.hosts.laptop-a.projects.myapp.> (messages use mclaude.users.* subjects)
  # No Object Store permissions — imports and attachments use S3 (ADR-0053)
  $JS.FC.KV_mclaude-sessions-alice.>                     # Flow control: scoped to own streams (M2 fix)
  $JS.FC.KV_mclaude-projects-alice.>
  $JS.FC.KV_mclaude-hosts.>                              # Flow control: shared hosts bucket
  $JS.FC.MCLAUDE_SESSIONS_alice.>                        # Flow control: session stream
```

**How per-project scoping works at each layer:**

| Layer | Scoping mechanism | What it enforces |
|-------|-------------------|------------------|
| NATS subjects | `mclaude.users.alice.hosts.laptop-a.projects.myapp.>` | Agent can only publish/subscribe on its own project's command and event subjects |
| KV pub/sub | `$KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.>` | Agent can only write KV entries keyed under its project. KV watch only delivers its project's updates. |
| JetStream API | `$JS.API.*.KV_mclaude-sessions-alice` | Agent can call JetStream API on alice's session bucket (per-user bucket). Cannot access bob's buckets. |
| KV direct-get | `$JS.API.DIRECT.GET.KV_mclaude-sessions-alice.$KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.>` | Uses subject-form direct-get. The key portion is the **full message subject** (`$KV.<bucket>.<key>`), not just the key. Agent can only read its own project's keys. Implementation must use subject-form; payload-form is not permitted. |
| Stream info | `$JS.API.STREAM.INFO.KV_mclaude-sessions-alice` | **Residual metadata leakage (accepted).** Stream info is per-stream (per-user bucket), not per-key. Agent can query aggregate metadata for alice's entire sessions bucket, not just its project. Required by the NATS client SDK for KV bucket initialization — called unconditionally before any KV operation. Leaks activity volume AND project/session topology via `SubjectFilter` (NATS does not gate request payload content), but not message contents. See R4 in Post-Fix Residual Risks for the full field-by-field `StreamState` analysis. |
| Session stream info | `$JS.API.STREAM.INFO.MCLAUDE_SESSIONS_alice` | **Residual metadata leakage (accepted).** Same as KV buckets — stream info is per-stream (per-user), not per-project. Agent can query aggregate metadata for alice's entire sessions stream. Required by NATS client SDK for consumer initialization. Leaks activity volume AND project/session topology via `SubjectFilter` (NATS does not gate request payload content), but not message contents. Per-project streams would close this gap but cause resource proliferation (same trade-off as per-project KV buckets, rejected). See R4 in Post-Fix Residual Risks for the full field-by-field `StreamState` analysis. |
| Consumer info | `$JS.API.CONSUMER.INFO.KV_mclaude-sessions-alice.*` | **Residual metadata leakage (accepted).** The `*` wildcard allows querying info for any consumer on alice's bucket, not just the agent's own. Consumer names are ephemeral UUIDs — an attacker would need to guess or brute-force them. Even if found, consumer info reveals only delivery state (pending count, ack floor, last delivered sequence), not message contents. Cannot be tightened: consumer names are auto-generated at runtime by the NATS client. `$JS.API.CONSUMER.LIST` and `$JS.API.CONSUMER.NAMES` are NOT granted — agent cannot enumerate consumers. |

**Differences from User JWT:**
- Agent has KV **write** on sessions and projects (it manages state; users are read-only on those KV buckets).
- Agent writes session KV (including quota-managed session fields like `pausedVia`, `softThreshold` — see ADR-0044).
- Agent NATS subjects are scoped to one project (`projects.myapp.>`), not all hosts (`hosts.*.>`).
- Agent has NO Object Store access. Binary data (imports, attachments) uses S3 with pre-signed URLs (ADR-0053).
- Agent does NOT have host KV write (it reads host config but doesn't modify it).

---

### BYOH Host / Platform Controller — all hosts use host-scoped subjects

**Subject scheme change from ADR-0035:** Host controllers subscribe to `mclaude.hosts.{hslug}.>` instead of `mclaude.users.{uslug}.hosts.{hslug}.>`. This removes the user prefix from the host's subscription subject, eliminating the need to enumerate users in the host JWT.

**Project operations use fan-out (CP-initiated, not SPA-initiated):** The SPA/CLI continues to use the existing HTTP endpoint `POST /api/users/{uslug}/projects` (unchanged from ADR-0035). CP validates the request (authorization, slug uniqueness, host assignment), creates the Postgres record, writes the project KV entry, and then **CP itself publishes** `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create` to NATS. The host controller receives this message via its `mclaude.hosts.{hslug}.>` subscription and starts the agent subprocess. Error feedback flows through the HTTP response — if CP rejects the project (slug collision, invalid host, authorization failure), the SPA receives an HTTP error (409, 404, 403) with a JSON error body. The user JWT's `mclaude.hosts.{hslug}.users.{uslug}.>` Pub.Allow entries exist for session commands and host management (not project creation) — project creation remains an HTTP-to-CP-to-NATS flow.

**What doesn't change:** Session-agent JWTs, KV structure, and sessions streams are all unaffected. Session subjects remain under `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.>` (user namespace, not host namespace) — session events don't need to reach the host controller.

```
Pub.Allow:
  mclaude.hosts.laptop-a.>              # Receives: project create/delete (fan-out from users), agent public key registration
  _INBOX.>                              # Request/reply

Sub.Allow:
  mclaude.hosts.laptop-a.>              # Subscribe to project operations (fan-out from users) and agent registration
  _INBOX.>                              # Request/reply
  $SYS.ACCOUNT.*.CONNECT               # System events: receive connection notifications (M3 fix: moved from Pub)
  $SYS.ACCOUNT.*.DISCONNECT            # System events: receive disconnection notifications
  # Residual (M4): account wildcard * matches all accounts. Harmless in single-account
  # architecture. If multi-account support is added (deferred), scope to specific account ID.
```

**No `$JS.*`, `$KV.*`, or `$O.*` subjects at all.** The host controller only uses NATS core pub/sub for provisioning commands. Session-agents get their own separate per-user JWTs with JetStream access.

**This JWT is identical regardless of how many users share the host.** A single-user BYOH host, a team workstation, and a platform host all have the same host JWT structure — one `mclaude.hosts.{hslug}.>` entry. Host access enforcement is entirely in the control-plane.

**Design principle:** The NATS layer is access-model-agnostic. Host JWTs contain a single entry: `mclaude.hosts.{hslug}.>`. This is constant-size regardless of user count. Host access is enforced at the **control-plane application layer**: when a host requests agent credentials, CP checks that the requesting user has access to the host before minting the agent JWT. NATS permissions on the host JWT do not reference users at all.

The owner grants access to other users via `manage.grant` / `manage.revoke-access` (see Host Access Grants above). CP stores grants in the `host_access` table. The NATS layer is access-model-agnostic — it enforces JWT permissions, not application-layer policies.

### Host Lifecycle Management

#### Registration

Hosts are registered by an authenticated user via NATS (the CLI has user credentials from `mclaude login`):

1. Host controller starts → generates NKey pair → exposes public key (file on disk for BYOH, shared volume for K8s)
2. User runs `mclaude host register --name "My MacBook" --nkey-public UABC...` from anywhere
3. CLI publishes `mclaude.users.{uslug}.hosts._.register {name, type, nkey_public}` via NATS (user JWT provides attestation — "I vouch for this public key")
4. CP creates host in Postgres (slug, name, type, owner_id, nkey_public)
5. CP returns `{ok, slug}` on the reply subject — **no JWT**
6. Host controller authenticates itself via HTTP: `POST /api/auth/challenge {nkey_public}` → `POST /api/auth/verify {nkey_public, challenge, signature}` → receives JWT
7. Host controller connects to NATS

The CLI only passes the public key — it never touches JWTs or secrets. The host controller authenticates itself directly to CP. The CLI doesn't even need to be on the same machine as the host.

For K8s: operator reads the controller's public key from the pod (init container output or shared volume annotation), runs the same `mclaude host register` command from their workstation. The controller authenticates via HTTP on its own.

#### Ownership Model

The registering user is the permanent owner. Stored as `hosts.owner_id` in Postgres (FK to users, set at registration, immutable).

| Role | Authorization |
|------|--------------|
| **Host owner** (registering user) | Full control: rename, deregister, revoke credentials, grant/revoke access for other users |
| **Authorized user** | Use only: provision projects, create sessions. No management operations. |
| **Platform operator** | Full control over all hosts. Emergency revocation. |

The registering user is the permanent host owner. This keeps the model simple — the person who attested to the host's identity is responsible for it.

#### Host Access Grants

The owner controls who can use their host. Access is per-user, per-host — no groups or roles.

```
CLI → NATS: REQ mclaude.users.alice.hosts.laptop-a.manage.grant
            {"userSlug":"bob"}
CP:         Validates: alice owns laptop-a ✓
            Inserts (host_id, user_id) into host_access table.
            Revokes bob's current NATS JWT (his host list changed).
CP → NATS:  (reply) {"ok":true}
NATS server: Closes bob's SPA WebSocket (revoked JWT).
Bob's SPA:   Reconnects via HTTP auth → gets new JWT with laptop-a in host list.
```

Revoke access:
```
CLI → NATS: REQ mclaude.users.alice.hosts.laptop-a.manage.revoke-access
            {"userSlug":"bob"}
CP:         Validates: alice owns laptop-a ✓
            Deletes (host_id, user_id) from host_access table.
            Revokes bob's NATS JWT.
            Revokes all agent JWTs for bob's projects on laptop-a.
CP → NATS:  (reply) {"ok":true}
```

On access revocation, bob's active sessions on laptop-a are terminated — the agent JWTs are revoked, NATS disconnects them, and session KV is updated to `status: error`. Bob's SPA reconnects with a JWT that no longer includes laptop-a.

The owner always has implicit access to their own host (no explicit grant needed). The `host_access` table is simple: `(host_id, user_id, granted_at)`. CP resolves the user's accessible hosts at JWT issuance by querying owned hosts + granted hosts.

#### Configuration

| Operation | Subject / Endpoint | Who can do it | Effect |
|-----------|-------------------|---------------|--------|
| Rename | `mclaude.users.{uslug}.hosts.{hslug}.manage.update {name}` | Host owner, platform operator | Updates hosts KV + Postgres |
| Update type | `mclaude.users.{uslug}.hosts.{hslug}.manage.update {type}` | Host owner, platform operator | Metadata only |
| Grant access | `mclaude.users.{uslug}.hosts.{hslug}.manage.grant {userSlug}` | Host owner | Adds user to host_access, revokes grantee's JWT |
| Revoke access | `mclaude.users.{uslug}.hosts.{hslug}.manage.revoke-access {userSlug}` | Host owner | Removes from host_access, revokes grantee's + agent JWTs |

#### Deregistration

`mclaude.users.{uslug}.hosts.{hslug}.manage.deregister`

Authorization: host owner or platform operator. The deregistration flow:

1. **Drain sessions**: CP publishes `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.delete` for every active project on the host. Agents shut down gracefully (lifecycle.stopped events).
2. **Revoke host credential**: CP adds the host JWT to the NATS revocation list. Host controller is immediately disconnected.
3. **Clean up state**: CP deletes host row from Postgres. CP deletes `$KV.mclaude-hosts.{hslug}` (tombstone). SPA watchers see the host disappear.
4. **Clean up agent credentials**: CP removes all stored agent NKey public keys for this host.
5. **Orphaned projects**: Projects that were on this host are marked `status: "host_deregistered"` in the project KV. The SPA shows them as unavailable. The user can re-provision to another host or delete them.
6. **S3 data**: Import archives and attachments remain in S3 under the project prefix. They are not deleted on host deregistration — the user still owns the data.

#### Credential Revocation (Emergency)

If a BYOH host is compromised:

1. Host owner or platform operator calls `mclaude.users.{uslug}.hosts.{hslug}.manage.revoke`
2. CP adds the host JWT to the NATS revocation list (immediate disconnect)
3. CP adds all agent JWTs for sessions on that host to the revocation list
4. CP marks host as `online: false, status: "revoked"` in KV and Postgres
5. Host cannot reconnect — refresh requests are rejected
6. Re-activation requires a new `mclaude host register` (new NKey pair, new JWT)

This is distinct from deregistration — the host record remains in Postgres (for audit trail) but is permanently deactivated until re-registered.

#### JWT Revocation Mechanism (NATS-level)

Multiple flows above reference "CP adds the JWT to the NATS revocation list" (host access change propagation, emergency credential revocation, `manage.revoke-access`, deregistration). This section specifies the NATS-level mechanism that makes revocation possible.

**Prerequisite: switch from `resolver: MEMORY` to `resolver: nats` (full resolver).** The current hub NATS uses `resolver: MEMORY` with a preloaded account JWT. Memory resolvers do not support runtime claim updates — the account JWT is baked into the server config at startup. The full resolver (`resolver: nats`) stores account JWTs in a JetStream-backed `$SYS` KV store, enabling runtime updates via `$SYS.REQ.CLAIMS.UPDATE`.

**CP credentials for revocation:**
1. **Operator seed**: CP loads the operator seed from `OPERATOR_KEYS_PATH` (the same K8s Secret `mclaude-system/operator-keys` that already stores `operatorSeed`). The operator key is needed to re-sign the account JWT after adding revocation entries.
2. **System account credentials**: CP connects to NATS with system account credentials (system account NKey, loaded from the same operator-keys Secret) to publish `$SYS.REQ.CLAIMS.UPDATE`. The system account is created during the Helm pre-install job alongside the operator and account keys.

**Revocation flow:**
1. CP decodes the current account JWT (cached in memory, refreshed on startup).
2. CP adds the target user/host/agent NKey public key to the account JWT's `Revocations` map with a timestamp (`jwt.TimeRange` — revokes all JWTs issued before `now`).
3. CP re-signs the account JWT with the operator key.
4. CP publishes the updated account JWT to `$SYS.REQ.CLAIMS.UPDATE` using system account credentials.
5. NATS server processes the update, immediately closes connections whose JWT was issued before the revocation timestamp.
6. Revocation entries auto-expire after the revoked JWT's remaining TTL (max 8h for users, 5 min for hosts/agents). CP does NOT need to clean up old entries.

**Infrastructure changes required:**
- Hub NATS config: change `resolver: MEMORY` to `resolver: nats` with `resolver_preload` for bootstrap.
- Helm chart: add system account NKey seed to operator-keys Secret.
- CP startup: load operator seed + system account seed from `OPERATOR_KEYS_PATH`.
- CP: maintain cached account JWT, decode/modify/re-sign on revocation.

**Fallback without revocation:** Even without working revocation, credentials expire naturally (5 min for hosts/agents, ~8h for users). The 60s SPA refresh cycle and 5-min TTL for hosts/agents bound the window of stale credentials. Revocation provides sub-second propagation for the common case; TTL expiry is the backstop.

#### Host Lifecycle Subjects

Management subjects live under the user's host-scoped namespace, matching the existing `mclaude.users.{uslug}.hosts.*.>` in the user JWT Pub.Allow. CP subscribes to `mclaude.users.*.hosts.>` and validates the user is the host owner (or platform operator) before executing the operation.

| Subject | Purpose |
|---------|---------|
| `mclaude.users.{uslug}.hosts._.register` | Register a new host (stores public key, creates slug). `_` is a sentinel token in the hslug position — it can never collide with a real slug since slugs are constrained to `[a-z0-9-]+`. Returns `{ok, slug}` — no JWT (host authenticates itself via HTTP). |
| `mclaude.users.{uslug}.hosts.{hslug}.manage.update` | Rename, change type |

| `mclaude.users.{uslug}.hosts.{hslug}.manage.rekey` | Rotate the host's NKey public key. Owner-only. Updates the stored public key in place (`hosts.nkey_public`). SSH `known_hosts` model — when the host's key changes (reinstall, disk wipe), the owner explicitly re-attests. The old JWT becomes useless (NATS nonce challenge will fail against the new key). |
| `mclaude.users.{uslug}.hosts.{hslug}.manage.deregister` | Deregister (drain + cleanup) |
| `mclaude.users.{uslug}.hosts.{hslug}.manage.revoke` | Emergency revocation (immediate disconnect) |

All subjects match `mclaude.users.{uslug}.hosts.*.>` — no additional JWT permission entries needed. `_` cannot collide with real host slugs (slugs are `[a-z0-9-]+`, enforced at registration).

CP distinguishes management requests from session commands by the `manage.*` or `new.register` path segment. Session-related subjects use `projects.{pslug}.sessions.*`; management subjects use `manage.*`.

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

**Identical structure to BYOH hosts.** With host-scoped subjects, the platform controller no longer needs a wildcard user (`mclaude.users.*.hosts.us-east.>`). It subscribes to `mclaude.hosts.us-east.>` — the same pattern as any other host. The control-plane publishes provisioning requests here after validating host access. Zero JetStream access.

---

### Host credential refresh protocol

Analogous to the agent credential refresh specified in the Decisions table, host controllers refresh their own JWT on a timer and on-demand signal:

### Unified credential protocol (HTTP challenge-response)

All identity types (user, host, agent) authenticate and refresh via the same HTTP endpoint. NATS is used for public key registration (attestation) only. Authentication and JWT issuance happen over HTTP with NKey challenge-response.

#### HTTP auth endpoints

**Step 1: Challenge**
```
POST /api/auth/challenge
{
  "nkey_public": "UABC..."
}
→ {"challenge": "<random-nonce>"}
```

**Step 2: Verify**
```
POST /api/auth/verify
{
  "nkey_public": "UABC...",
  "challenge": "<nonce>",
  "signature": "<ed25519-signature-of-nonce>"
}
→ {"ok": true, "jwt": "<signed-jwt>"}
```

CP looks up the public key in Postgres (lookup order: `users.nkey_public` first, then `hosts.public_key`, then `agent_credentials.nkey_public` — first match wins, determines identity type), resolves current permissions (host access list → host slugs for users, project scope for agents), signs a JWT, and returns it. The challenge nonce is single-use and expires after 30 seconds. Nonces are stored in an **in-memory map** with automatic expiry (same pattern as the existing OAuth state store in `providers.go`). This assumes a **single-replica control-plane deployment**, which is the current architecture. If CP is scaled to multiple replicas in the future, nonce storage must move to a shared store (Postgres or Redis) to ensure a nonce generated by one replica can be verified by another.

**Error responses:**
```json
{"ok": false, "error": "unknown public key", "code": "NOT_FOUND"}
{"ok": false, "error": "invalid signature", "code": "UNAUTHORIZED"}
{"ok": false, "error": "host revoked", "code": "FORBIDDEN"}
{"ok": false, "error": "challenge expired", "code": "EXPIRED"}
```

#### Bootstrap and refresh are the same flow

There is no separate "bootstrap" vs "refresh" path. Every entity runs the same loop:

1. Generate NKey pair (once, at startup)
2. Call `POST /api/auth/challenge` + `POST /api/auth/verify` → get JWT
3. Connect to NATS with JWT + NKey
4. Before JWT expires (5-min TTL): repeat step 2, update NATS credentials in-flight
5. If JWT already expired (missed refresh window): NATS disconnects, repeat from step 2

One code path. One HTTP client. All identity types.

#### Public key registration (NATS, attestation only)

Before an entity can authenticate, its public key must be registered by a trusted party:

| Entity | Registered by | NATS subject | Stored in |
|--------|--------------|-------------|-----------|
| **User** | Self (at login) | N/A — HTTP login includes `nkey_public` | `users.nkey_public` |
| **Host** | User (CLI) | `mclaude.users.{uslug}.hosts._.register` | `hosts.nkey_public` |
| **Agent** | Host controller | `mclaude.hosts.{hslug}.api.agents.register` | `agent_credentials (uslug, hslug, pslug) → nkey_public` |

Registration is attestation: "I vouch for this public key." Authentication is proof: "I have the private key." The two are cleanly separated — NATS handles attestation, HTTP handles proof.

#### Agent public key registration (NATS)

**Subject:** `mclaude.hosts.{hslug}.api.agents.register` (covered by host's `mclaude.hosts.{hslug}.>`)

```json
Request:  {"user_slug": "alice", "project_slug": "myapp", "nkey_public": "UABC..."}
Response: {"ok": true}
```

The agent generates its own NKey at startup, passes the public key to the host controller via local IPC. The host controller registers it with CP via NATS. CP validates host access + project ownership + host assignment, then stores the mapping. The agent then authenticates itself via HTTP challenge-response to get its JWT.

**Error responses:**
```json
{"ok": false, "error": "user does not have access to host", "code": "FORBIDDEN"}
{"ok": false, "error": "project not assigned to host", "code": "NOT_FOUND"}
```

#### Host access change propagation

When CP processes a membership mutation:
1. CP revokes NATS JWTs of all affected users (added to NATS revocation list)
2. NATS server closes their connections
3. SPA/CLI reconnect fires → calls `POST /api/auth/challenge` + `verify` → gets new JWT with updated host list
4. Reconnects to NATS with new JWT

Even without explicit revocation, the 60s refresh cycle (for SPA) or pre-expiry refresh (for hosts/agents) ensures JWTs are updated within one TTL window. Revocation provides sub-second propagation for the common case.

| Aspect | Detail |
|--------|--------|
| **Revocation list size** | Entries auto-expire after the JWT's remaining TTL. In practice, consumed within seconds (clients reconnect immediately). |
| **Cache-bust on issuance** | When CP mints a JWT, it invalidates its in-memory access cache for the identity, then re-resolves from Postgres. |

**User refresh wire format:**

User JWT refresh uses the same HTTP challenge-response as hosts and agents — there is no separate endpoint. The SPA already has its NKey seed in `localStorage` (generated at login, shared across all tabs), so it can sign challenge nonces:

1. SPA calls `POST /api/auth/challenge {"nkey_public": "UABC..."}` → receives `{"challenge": "<nonce>"}`
2. SPA signs nonce with NKey seed from `localStorage`
3. SPA calls `POST /api/auth/verify {"nkey_public": "UABC...", "challenge": "<nonce>", "signature": "<sig>"}` → receives `{"ok": true, "jwt": "<new-jwt>"}`

No seed in the response — the SPA already has its NKey seed. CP looks up the stored public key for the user and signs the new JWT for it. The NKey identity does not change — same key pair, new JWT with updated permissions. On revocation-triggered disconnect, the SPA reconnects by running the same challenge-response flow with its existing seed.

**What changes in the JWT on membership update:**
- `Pub.Allow` gains/loses `$JS.API.DIRECT.GET.KV_mclaude-hosts.$KV.mclaude-hosts.{hslug}` entries (one per host)
- `Pub.Allow` gains/loses `$JS.API.CONSUMER.CREATE.KV_mclaude-hosts.*.$KV.mclaude-hosts.{hslug}` entries
- `Sub.Allow` gains/loses `$KV.mclaude-hosts.{hslug}` entries (KV watch push delivery)
- All other permissions (per-user buckets, streams) are unchanged — they reference `{uslug}`, not host slugs

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
- NATS server source: KV subjects in `kv.go`, consumer create filtered form in `jetstream_api.go`

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

### Object Store (reference only — not used by mclaude, see ADR-0053)

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

| Resource type | Stream name | Subject filter | Key structure |
|---------------|-------------|----------------|---------------|
| Sessions KV | `KV_mclaude-sessions-{uslug}` | `$KV.mclaude-sessions-{uslug}.>` | `hosts.{hslug}.projects.{pslug}.sessions.{sslug}` |
| Projects KV | `KV_mclaude-projects-{uslug}` | `$KV.mclaude-projects-{uslug}.>` | `hosts.{hslug}.projects.{pslug}` |
| Hosts KV | `KV_mclaude-hosts` (shared; per-host read scoping in JWT) | `$KV.mclaude-hosts.>` | `{hslug}` |
| ~~Job Queue KV~~ | — | — | Removed. Quota-managed sessions use session KV with extended fields (ADR-0044). |
| Sessions stream | `MCLAUDE_SESSIONS_{uslug}` | `mclaude.users.{uslug}.hosts.*.projects.*.sessions.>` | N/A (stream, not KV) |
| ~~Imports Object Store~~ | ~~`OBJ_mclaude-imports-{uslug}`~~ | — | Removed. Binary data uses S3 (ADR-0053). |
| App subjects | — | `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.>` | — |
| Host subjects | — | `mclaude.hosts.{hslug}.>` | — |

## Component Changes

### mclaude-control-plane
- `nkeys.go`: Rewrite `UserSubjectPermissions(uslug string, hostSlugs []string)`, `SessionAgentSubjectPermissions()`, `HostSubjectPermissions(hslug string)` (previously `(uslug, hslug string)`) — permissions now reference per-user resource names (`KV_mclaude-sessions-{uslug}`, `MCLAUDE_SESSIONS_{uslug}`, etc.). `UserSubjectPermissions()` now takes a list of host slugs (derived from owned + granted hosts) and emits per-host entries for the hosts KV (e.g., `$JS.API.DIRECT.GET.KV_mclaude-hosts.$KV.mclaude-hosts.{hslug}` per host). `HostSubjectPermissions()` produces `mclaude.hosts.{hslug}.>` only; update `IssueHostJWT` callers accordingly (drop `uslug` parameter). `SessionAgentSubjectPermissions(uslug, hslug, pslug string)` now takes user slug, host slug, and project slug and produces per-project scoped permissions.
- `nkeys.go`: Rewrite `IssueHostJWT(publicKey string, hslug string)`, `IssueSessionAgentJWT(publicKey string, uslug, hslug, pslug string)`, and `IssueUserJWT(publicKey string, uslug string, hostSlugs []string)` — none of these functions generate NKey pairs internally. They all accept an external NKey public key and return only the signed JWT (no seed). All identity types generate their own NKeys: SPA/CLI in the browser/process, hosts at registration, agents at startup. CP never handles private key material.
- New: NKey public key storage — CP stores public keys for all identity types at issuance time and uses them for JWT refresh (no public key in refresh requests):
  - **Users**: `users.nkey_public` Postgres column. Written at login (SPA/CLI sends public key in login request). Used by `POST /api/auth/challenge` + `POST /api/auth/verify` to sign new JWT (CP looks up the stored public key by the `nkey_public` in the challenge request). Cleared on logout / session expiry.
  - **Hosts**: `hosts.public_key` Postgres column (already exists; add `UNIQUE` constraint). Written at registration. The `UNIQUE` constraint ensures no two hosts share the same NKey and enables index-backed lookup in the challenge endpoint.
  - **Agents**: `agent_credentials` Postgres table storing `(uslug, hslug, pslug) → nkey_public`. Written at registration (via `mclaude.hosts.{hslug}.api.agents.register`), checked at refresh. On refresh, CP rejects requests where the stored public key doesn't match (prevents identity swapping). Entries cleaned up on deprovision. Agent public keys survive CP restart because they are persisted in Postgres. CP may maintain an in-memory cache over Postgres for fast lookups — on cache miss (e.g., after CP restart), CP falls back to Postgres. No re-issuance needed on CP restart.
- `nkeys.go`: Remove `$JS.API.>` and `$JS.*.API.>` wildcards from all identity types
- `nkeys.go`: Remove all `$JS.*` from `HostSubjectPermissions()` entirely
- `nkeys_test.go`: Update tests to verify new permissions deny cross-user access
- `auth.go`: On JWT refresh, issue new JWT with tightened permissions
- New: `mclaude.hosts.{hslug}.api.agents.register` NATS subscriber — host controllers register agent public keys here. CP validates host access + project ownership + host assignment, then stores the `(uslug, hslug, pslug) → nkey_public` mapping. The agent then authenticates via HTTP to get its JWT.
- New: `POST /api/auth/challenge` + `POST /api/auth/verify` HTTP endpoints — unified NKey challenge-response authentication for all identity types (user, host, agent). CP looks up the public key, determines identity type, resolves permissions, signs JWT. Replaces all NATS-based credential issuance and refresh.
- New: bucket/stream lifecycle management — create per-user KV buckets + `MCLAUDE_SESSIONS_{uslug}` stream on user registration; create shared `mclaude-hosts` bucket on deployment; create on demand if missing at runtime
- CP publishes to `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.{create,delete}` after processing the HTTP `POST /api/users/{uslug}/projects` request (validates host access, creates Postgres records, writes project KV). The host controller receives this message via its `mclaude.hosts.{hslug}.>` subscription and provisions the agent. Error feedback to the SPA is via the HTTP response (409/404/403).
- Remove account key from host registration response and cluster registration response — hosts no longer receive the signing key
- New: `host_access` Postgres table + migration
- Update `RegisterHost` endpoint: registering user is the permanent owner, implicit access (no `host_access` row needed for owner)
- Rewrite host queries that currently filter by `user_id` to use `owned + granted` access resolution (e.g., `GetHostsByUser` → `SELECT FROM hosts WHERE owner_id = ? UNION SELECT FROM hosts JOIN host_access ON ...`). Affected paths include: host list API, `$SYS` presence handler, login handler, cluster grant flow.
- New: `manage.grant` and `manage.revoke-access` NATS handlers — validate ownership, update `host_access`, revoke affected JWTs
- On access revocation: revoke grantee's user JWT + all agent JWTs for grantee's projects on that host. SPA reconnects with updated host list.
- Removed: NATS credential issuance and refresh subscribers (`api.agents.credentials`, `api.agents.refresh`, `api.credentials.refresh`). All credential operations moved to HTTP challenge-response (see "Unified credential protocol").

### mclaude-controller-k8s
- Remove account key from controller entirely — controller no longer mints session-agent JWTs
- Update provisioning subscription from `mclaude.users.*.hosts.{hslug}.>` to `mclaude.hosts.{hslug}.>` (host-scoped scheme per this ADR)
- New: on project provisioning (received from CP on `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create`), start the agent pod. The agent generates its own NKey pair at startup and exposes the public key (e.g., via init container writing to a shared volume). K8s controller reads the public key, then registers it with CP via `mclaude.hosts.{hslug}.api.agents.register` (NATS request/reply, retry with exponential backoff on `NOT_FOUND`). The agent authenticates itself via HTTP challenge-response (`POST /api/auth/challenge` + `verify`) to get its JWT. No K8s Secret needed for the JWT — the agent manages its own credentials.
- Remove `$JS.*.API.>` from controller's own JWT (it doesn't need JetStream)
- K8s controller connects directly to hub NATS (no worker NATS, no leaf node). This supersedes ADR-0035's leaf-node topology for K8s.
- Implement host credential refresh loop: timer-based HTTP challenge-response before 5-min TTL expiry (see "Unified credential protocol")

### mclaude-controller-local
- Update provisioning subscription from `mclaude.users.*.hosts.{hslug}.>` to `mclaude.hosts.{hslug}.>` (host-scoped scheme per this ADR)
- Must not hold or use the account signing key — local controller does not mint agent JWTs (same as K8s controller)
- On project provisioning (received from CP on `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create`), start the agent subprocess first. The agent generates its own NKey pair at startup and passes the public key back to the host controller via local IPC. Host controller registers the public key with CP via `mclaude.hosts.{hslug}.api.agents.register` (NATS request/reply). If CP returns `NOT_FOUND` (race: message arrived before CP finished Postgres write), retry with exponential backoff (100ms initial, doubling, max 5s, max 10 attempts). The agent then authenticates itself via HTTP challenge-response to get its JWT. No credential handoff between controller and agent.
- Implement host credential refresh loop (see "Host credential refresh protocol" section below)

### mclaude-session-agent
- Update KV bucket names from `mclaude-sessions` to `mclaude-sessions-{uslug}` (read from config/env)
- Remove stream creation code (CreateOrUpdateStream for MCLAUDE_EVENTS, MCLAUDE_API, MCLAUDE_LIFECYCLE) — the consolidated `MCLAUDE_SESSIONS_{uslug}` stream is created by the control-plane during user registration
- **Switch from pull consumers to ordered push consumers** for session command and event delivery. The current code uses pull consumers (`cons.Fetch()`) which require application-level ordering, gap detection, and redelivery handling. Ordered push consumers are managed by the NATS client library — they guarantee in-order delivery with automatic consumer recreation on sequence gaps (critical for chat interface correctness). The permission spec already supports this: consumer-create + subscribe to delivery subject. No `$JS.API.CONSUMER.MSG.NEXT` permission needed.
- New credential refresh loop: periodically refreshes NATS JWT before TTL expiry (similar to SPA's 60s check cycle)
- On `permissions violation` error: immediate refresh + retry
- Credential refresh: HTTP challenge-response to CP (`POST /api/auth/challenge` + `verify`). Same flow as bootstrap — agent signs the challenge nonce with its NKey seed, CP verifies against stored public key, returns fresh JWT. Timer-based, before 5-min TTL expiry.
- (Removed: job queue KV eliminated — see ADR-0044)
- ~~Update daemon job dispatch, KV watch filter, and key parsing in `daemon.go` / `daemon_jobs.go`~~ — **Deferred.** Daemon mode (`--daemon`) requires cross-project JetStream access incompatible with this ADR's per-project agent scoping. Deferred to BYOH architecture ADR.

### mclaude-common
- Update subject constants (`FilterMclaudeEvents`, `FilterMclaudeAPI`, `FilterMclaudeLifecycle`) to the consolidated `sessions.>` hierarchy defined in the "Session subject hierarchy" table in the Data Model section (e.g., `sessions.{sslug}.events`, `sessions.{sslug}.input`, `sessions.{sslug}.lifecycle.started`, etc.)
- Update `subj.ProjectsKVKey()` to produce `hosts.{hslug}.projects.{pslug}` format (was `{userId}.{hostId}.{projectId}`). Uses literal type-tokens to match the Resource Naming table and permission specs (e.g., `$KV.mclaude-projects-alice.hosts.laptop-a.projects.myapp`).
- Update `subj.SessionsKVKey()` to produce `hosts.{hslug}.projects.{pslug}.sessions.{sslug}` format (was `{userId}.{hostId}.{projectId}.{sessionId}`). Same hierarchical key format with literal type-tokens.
- Remove `subj.JobQueueKVKey()` — the job-queue bucket no longer exists (see Decisions table: "No job queue KV"). Callers updated to use session KV with extended fields (ADR-0044).
- Update `HostsKVKey()` — drop user slug prefix, new signature `HostsKVKey(hslug)` returning `{hslug}`. All callers updated (control-plane `$SYS` presence handler, SPA host watches).
- Update provisioning subject helpers to use `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.{create,delete}` (CP publishes after HTTP project creation, host controller receives via subscription). Rename functions to `HostUserProjects*` to reflect the new scheme.
- `slug/slug.go`: Add `create`, `delete`, `input`, `config`, `control` to the reserved-word blocklist. These are verb tokens used in the session subject hierarchy (`sessions.create`, `sessions.{sslug}.delete`, etc.) and must not be used as session slugs to prevent subject ambiguity. Add corresponding `reservedCreate`, `reservedDelete`, `reservedInput`, `reservedConfig`, `reservedControl` typed constants and entries in `reservedSet`.

### mclaude-web
- New: generate NKey pair in browser at login time. Store seed in `localStorage` (shared across all tabs — avoids multi-tab breakage where each tab generates a different NKey and overwrites `users.nkey_public`). On login, if an NKey seed already exists in `localStorage`, reuse it (derive the public key and send it in the login request) instead of generating a new one. Only generate a new NKey pair if no seed exists (first login or after logout clears it). Send `nkey_public` in the login request body. Receive only the JWT (no seed) in the response.
- On page refresh / new tab: if `localStorage` has a seed, derive the public key and use it for HTTP challenge-response (`POST /api/auth/challenge` + `POST /api/auth/verify`). If no seed (logged out or cleared), redirect to login.
- Update KV bucket names in `nats-client.ts` / `subj.ts` to use `mclaude-sessions-{uslug}`, `mclaude-projects-{uslug}`, etc.
- Update stream references from three separate streams (`MCLAUDE_EVENTS`, `MCLAUDE_API`, `MCLAUDE_LIFECYCLE`) to the consolidated `MCLAUDE_SESSIONS_{uslug}`
- Two session stream consumer patterns:
  - **Dashboard**: ordered push consumer with `DeliverNew` policy, filtered to `*.lifecycle.>`. Created on SPA connect. Provides real-time session lifecycle notifications without replaying history.
  - **Chat view**: ordered push consumer with `DeliverAll` policy, filtered to one specific session (`sessions.{sslug}.>`). Created when user opens a session. Replays full conversation history + live updates. Destroyed on navigate-away.
- User slug available from auth store at connection time
- Host list rendering: SPA fetches the initial host list from the login response (or `GET /api/users/{uslug}/hosts`). Runtime presence updates come from watching the shared `mclaude-hosts` KV bucket — the user JWT scopes which host keys are visible (per-host entries derived from owned + granted hosts). On access change, CP revokes the user JWT; the SPA reconnects with an updated JWT containing the new host list. NATS enforces visibility.

### mclaude-cli
- Use per-user bucket/stream names (provided by login response or derived from user slug)
- New: `mclaude host grant <hslug> <uslug>`, `mclaude host revoke-access <hslug> <uslug>`

## Data Model

### JetStream Resource Changes (per-user isolation)

**Per-user KV buckets** (replacing shared buckets):
- `mclaude-sessions-{uslug}` — session state (was shared `mclaude-sessions`)
- `mclaude-projects-{uslug}` — project state (was shared `mclaude-projects`)
- (Removed: `mclaude-job-queue` eliminated — quota-managed sessions use session KV, see ADR-0044)

**Shared KV bucket** (retained as shared):
- `mclaude-hosts` — host presence data (online/offline status, last seen). Shared bucket, but **read access is per-host in user JWTs**. Key format: `{hslug}` (no hierarchy prefix — the bucket name already scopes to hosts). Hosts are globally unique by slug. CP writes once per `$SYS.CONNECT/DISCONNECT` event. Each user's JWT lists explicit host slug entries (e.g., `$JS.API.DIRECT.GET.KV_mclaude-hosts.$KV.mclaude-hosts.laptop-a`) derived from owned + granted hosts at issuance time. On access change, CP revokes the user JWT; the SPA reconnects and receives updated host permissions. NATS enforces host visibility — no app-layer filtering needed.

**Per-user sessions stream** (replacing shared streams):
- `MCLAUDE_SESSIONS_{uslug}` — consolidates events, commands, and lifecycle (was shared `MCLAUDE_EVENTS`, `MCLAUDE_API`, `MCLAUDE_LIFECYCLE`). Captures `mclaude.users.{uslug}.hosts.*.projects.*.sessions.>`.

  **Stream configuration:**
  | Setting | Value | Rationale |
  |---------|-------|-----------|
  | Retention | `LimitsPolicy` | Standard limits-based retention. Messages are retained until MaxAge or disk limits, not until consumed. |
  | MaxAge | `30d` | Matches the longest-lived original stream (`MCLAUDE_EVENTS` had 30d). Lifecycle messages (previously 1h MaxAge in `MCLAUDE_LIFECYCLE`) and API commands (previously 24h in `MCLAUDE_API`) are now retained for 30d alongside events. This is acceptable — lifecycle messages are small and the per-subject volume is low. The 30d window provides consistent retention for debugging and history across all session activity types. |
  | Storage | `FileStorage` | Persistent storage for durability across server restarts. |
  | Subjects | `mclaude.users.{uslug}.hosts.*.projects.*.sessions.>` | Single filter captures all session activity (events, commands, lifecycle) for the user. |
  | MaxMsgsPerSubject | `-1` (unlimited) | No per-subject message limit; bounded by MaxAge. Individual sessions naturally produce bounded messages within 30d. |
  | Discard | `DiscardOld` | When limits are reached, discard oldest messages first. |

#### Session subject hierarchy

The consolidated `MCLAUDE_SESSIONS_{uslug}` stream captures all session subjects under `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.`. The following subjects replace the previously separate event, API command, and lifecycle streams:

| Subject | Purpose | Replaces |
|---------|---------|----------|
| `sessions.create` | Create a new session | `api.sessions.create` |
| `sessions.{sslug}.events` | Session event stream | `events.{sslug}` |
| `sessions.{sslug}.input` | Send input to session | `api.sessions.{sslug}.input` |
| `sessions.{sslug}.delete` | Delete session | `api.sessions.{sslug}.delete` |
| `sessions.{sslug}.control.interrupt` | Interrupt session | `api.sessions.{sslug}.control.interrupt` |
| `sessions.{sslug}.control.restart` | Restart session | `api.sessions.{sslug}.control.restart` |
| `sessions.{sslug}.config` | Update session config (model, permissions, etc.) | New |
| `sessions.{sslug}.lifecycle.started` | Session started event | `lifecycle.{sslug}.started` |
| `sessions.{sslug}.lifecycle.stopped` | Session stopped event | `lifecycle.{sslug}.stopped` |
| `sessions.{sslug}.lifecycle.error` | Session error event | `lifecycle.{sslug}.error` |

All subjects are prefixed with the full namespace: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.>`. The `MCLAUDE_SESSIONS_{uslug}` stream filter is `mclaude.users.{uslug}.hosts.*.projects.*.sessions.>`.

**Session slug blocklist additions:** The bare verb `sessions.create` (one token after `sessions.`) is structurally identical to a session-scoped subject if a session slug were `create`. While disambiguation by token count is possible (bare verb = 1 token; slug+operation = 2+ tokens), this is fragile and undocumented. To prevent ambiguity, the following verb tokens are added to the reserved-word blocklist in `mclaude-common/pkg/slug` (alongside existing reserved words like `users`, `hosts`, `sessions`, etc.): **`create`**, **`delete`**, **`input`**, **`config`**, **`control`**. This ensures no session (or other entity) slug can collide with any subject verb token used in the session hierarchy. The `mclaude-common/pkg/slug` package must be updated to include these five additional reserved words.

#### Consumer patterns

The SPA does not load the entire stream on connect. Two distinct consumer patterns serve different needs:

| Pattern | Filter | Deliver policy | When created | Purpose |
|---------|--------|---------------|--------------|---------|
| **Dashboard (notifications)** | `mclaude.users.{uslug}.hosts.*.projects.*.sessions.*.lifecycle.>` | `DeliverNew` | On SPA connect | Real-time lifecycle notifications ("session started", "session stopped", "session error") across all hosts and projects. Only messages published after consumer creation are delivered. Used for dashboard presence indicators, not conversation replay. |
| **Chat view (per-session)** | `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.>` | `DeliverAll` | When user opens a session | Full conversation replay + live updates for one specific session. Replays `.events`, `.input`, `.lifecycle` from the beginning. Destroyed when the user navigates away from the session. |

Both are ordered push consumers (ephemeral, client-generated NUID name, auto-reset on sequence gap). The dashboard consumer is lightweight (lifecycle events are small and infrequent). The per-session consumer is heavier but scoped to exactly one session — bounded by that session's message volume.

**Why not `DeliverAll` for the dashboard?** A user with months of session history across many hosts would replay thousands of lifecycle events on every page load. `DeliverNew` means the dashboard starts clean and accumulates state from live activity only. Historical session state comes from the sessions KV bucket (which is a point-in-time snapshot, not a replay).

This is a breaking rename. All publishers and subscribers must be updated: `mclaude-session-agent` (publisher), `mclaude-web` (subscriber), `mclaude-common` (subject constants and helpers), and any CLI or controller code that references session subjects. The `api.` and `lifecycle.` prefixes are eliminated — all session-related subjects now live under `sessions.>`.

**Note:** Terminal I/O subjects (`api.terminal.*`) are **not** part of this session subject rename. They remain under the `api.terminal.` prefix within the project scope (`mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.{suffix}`). Only session lifecycle, event, and control subjects are consolidated under `sessions.>`.

**(No Object Store)** — binary data (imports, attachments) uses S3 with pre-signed URLs (ADR-0053).

### Postgres Schema Changes

**New tables:**

```sql
CREATE TABLE host_access (
    host_id    TEXT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (host_id, user_id)
);
```

- Composite PK `(host_id, user_id)` prevents duplicate grants
- `ON DELETE CASCADE` on both FKs — if the host or user is deleted, the grant is cleaned up
- The owner has implicit access (not stored in this table) — CP resolves accessible hosts as `owned + granted`

```sql
CREATE TABLE agent_credentials (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    host_slug  TEXT NOT NULL,
    project_slug TEXT NOT NULL,
    nkey_public TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, host_slug, project_slug)
);
```

- `nkey_public UNIQUE` enables fast lookup by public key for `POST /api/auth/challenge` (CP searches across users, hosts, and agent_credentials by `nkey_public`)
- `UNIQUE (user_id, host_slug, project_slug)` enforces one active agent credential per user/host/project combination
- `ON DELETE CASCADE` on `user_id` — if the user is deleted, all their agent credentials are cleaned up
- Entries are also cleaned up on host deregistration and project deprovision (application-layer cleanup)

**Modified tables:**
- `users` table: new `nkey_public TEXT UNIQUE` column — stores the user's NATS NKey public key, set at login (SPA) or device-code auth (CLI). Used for JWT refresh via challenge-response. The `UNIQUE` constraint enables fast index-backed lookup by public key in `POST /api/auth/challenge` and prevents two users from sharing the same NKey (cryptographically infeasible but enforced at the schema level for correctness). CP looks up the stored public key by `nkey_public` in `POST /api/auth/challenge`. Cleared on logout / session expiry. Cross-table lookup order for `/api/auth/challenge`: `users.nkey_public` → `hosts.public_key` → `agent_credentials.nkey_public` (first match wins; determines identity type as user, host, or agent respectively). Per-column UNIQUE constraints guarantee at most one row per table, but do not prevent the same public key from appearing in multiple tables. In practice, cross-table collision is astronomically improbable: NKey pairs are Ed25519 keys derived from 256-bit random seeds, making the probability of two independently-generated keys colliding ~2⁻¹²⁸. Per-column UNIQUE is sufficient for lookup performance (index-backed scans); the ordered lookup rule (first match wins) handles the theoretical case deterministically.
- `hosts` — `user_id` column **renamed to `owner_id`** (`owner_id TEXT NOT NULL REFERENCES users(id)`, immutable, set at registration — the registering user is the permanent host owner). `UNIQUE` constraint changes from `(user_id, slug)` to `(slug)` (hosts are globally unique, not per-user). `public_key` column: add `UNIQUE` constraint (enables index-backed lookup in `/api/auth/challenge`; matches `users.nkey_public UNIQUE` and `agent_credentials.nkey_public UNIQUE`). Table is recreated with the new schema; existing host data is not migrated (see Migration section).
  - `role` column: **removed**. Access is now managed via `host_access` table.
  - `user_jwt` column: **renamed to `nats_jwt`**. This column stores the host controller's NATS JWT issued by the control-plane. The rename clarifies that it is the host's JWT, not a user's.
  - `js_domain`, `leaf_url`, `account_jwt`, `direct_nats_url` columns: **removed**. Leaf-node topology is superseded by direct hub NATS connection (see NATS topology decision). The associated `CHECK (type = 'machine' OR (js_domain IS NOT NULL AND leaf_url IS NOT NULL AND account_jwt IS NOT NULL))` constraint is also removed.
  - `HostKVState` (the KV value written on `$SYS` CONNECT/DISCONNECT): updated to reflect the new host model. It no longer includes `user_id` — the presence subscriber writes host slug + online status + `last_seen_at`. Other host metadata (type) is looked up from Postgres, not stored in KV.

    **HostKVState JSON schema:**
    ```json
    {
      "slug": "laptop-a",
      "name": "Richard's MacBook",
      "type": "machine",
      "online": true,
      "lastSeenAt": "2026-04-30T12:00:00Z"
    }
    ```
    Fields retained: `slug`, `name`, `type`, `online`, `lastSeenAt`. The `name` and `type` fields are kept in KV for SPA rendering (avoids a Postgres round-trip for every host in the list). Access information is NOT in KV — looked up from Postgres when needed.

### KV Key Format Changes

- Projects keys: changed from `{userId}.{hostId}.{projectId}` to `hosts.{hslug}.projects.{pslug}` (per-user bucket, hierarchical key with literal type-tokens)
- Sessions keys: changed from `{userId}.{hostId}.{projectId}.{sessionId}` to `hosts.{hslug}.projects.{pslug}.sessions.{sslug}` (same hierarchical format)
- Job queue keys: **removed** (job-queue bucket eliminated — see Decisions table)

## Error Handling

| Error | Component | Behavior |
|-------|-----------|----------|
| Permission denied on JetStream operation | Any client | NATS returns `permissions violation` error. Client logs the denied subject and surfaces clear error. |
| Old user JWT with broad permissions | SPA/CLI | JWT refresh (every 60s for SPA, on-demand for CLI) issues new tightened JWT. Grace period: max 1 JWT TTL (~8h). |
| Old session-agent JWT | Session-agent | Proactive refresh (before TTL expiry) issues new tightened JWT from control-plane. Grace period: max 1 agent JWT TTL (5 min). |
| Permission error triggers immediate refresh | Session-agent | If a NATS operation fails with `permissions violation`, agent immediately attempts credential refresh before retrying. This handles edge cases where the agent's cached JWT predates the permission change. |
| Host with old JWT | Host registration | Hosts also adopt TTL + refresh. `mclaude host register` re-run if immediate refresh is needed. |

## Security
This ADR IS the security improvement. After implementation:
- Users cannot access other users' KV entries
- Session-agents are scoped to their user's namespace
- Hosts cannot access arbitrary JetStream resources
- The principle of least privilege is enforced at the NATS level

## Impact

Specs updated:
- `docs/mclaude-control-plane/spec-control-plane.md` — updated NATS permission model
- `docs/spec-state-schema.md` — permission model for KV buckets
- `docs/mclaude-common/spec-common.md` — if permission helpers are shared

Components implementing the change:
- mclaude-control-plane (JWT issuance)
- mclaude-controller-k8s (session-agent JWT issuance)

## Scope

**v1:**
- Migrate from shared JetStream resources to per-user resources (KV buckets, sessions streams)
- Rewrite all JWT permission functions to reference per-user resource names
- Remove all `$JS.*` permissions from host/controller JWTs
- Update SPA, session-agent, CLI to use per-user resource names
- Add credential refresh loop to session-agent (TTL + NATS-authenticated refresh request)
- Reissue long-lived credentials (session-agent, host)

**Migration:** Deployment is a clean cut-over — existing shared-bucket data is not migrated. Old shared buckets (`mclaude-sessions`, `mclaude-projects`) are deleted after deployment. The `mclaude-hosts` bucket is recreated with a clean state (old host presence data is not migrated). The `hosts` Postgres table is recreated with the new schema (without per-user row duplication). New `host_access` table created empty. Existing host data is not migrated. This is acceptable for the current user base.

**Deferred:**
- BYOH daemon mode redesign — The session-agent `--daemon` mode requires cross-project JetStream access that neither the host JWT nor per-project agent JWT provides. BYOH architecture should be redesigned to match the K8s model (host controller manages session lifecycle, per-session agents with per-project JWTs). Deferred to a dedicated BYOH architecture ADR.
- ~~Quota system redesign~~ — **Resolved.** Quota is handled by ADR-0044 (co-requisite). The designated session-agent publishes and subscribes on `mclaude.users.{uslug}.quota` — this subject is included in the agent JWT Pub/Sub.Allow lists. ADR-0044 and ADR-0054 must be deployed together as a clean cut-over.
- Separate NATS accounts per user (full tenant isolation)
- Per-stream/per-consumer fine-grained ACLs beyond subject-level
- Audit logging for permission violations
- Shared sessions (pair programming, handoff, supervision, demo). Per-user isolation doesn't preclude sharing but sharing must be explicit and mediated — e.g., control-plane mirrors events to a second user's stream, or scoped time-limited credentials grant read-only access to a specific session. Not ambient access.
- Host eviction flow: when a user's access is revoked via `manage.revoke-access`, their active sessions on the host are terminated (agent JWTs revoked), session data is archived to S3. Control-plane updates project status and may migrate projects to another host the user has access to.
- Team/group-based access — The current model is per-user grants. A future ADR may introduce group-based access (grant a team access to a host) for organizations with many users.

## Resolved Factual Items

- NATS Object Store subjects (reference only — not used by mclaude): `$O.<bucket>.C.<sha256>` (chunks), `$O.<bucket>.M.<name>` (metadata). Backed by JetStream stream `OBJ_<bucket>`.
- NATS deny lists take precedence over allow lists (standard NATS behavior). Confirmed.

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| User cannot access other user's KV | User A's JWT is denied when reading `$KV.mclaude-sessions.{userB}.>` | Create two test users, issue JWTs. Teardown: delete users. | control-plane (JWT issuance), NATS |
| Agent auth requires registered public key | HTTP challenge with unregistered NKey returns `NOT_FOUND` | Register agent key, verify auth succeeds. Try auth with unregistered key, verify rejection. | control-plane |
| Session-agent scoped to user | Session-agent JWT for user A is denied when accessing user B's sessions KV | Create two users, issue session-agent JWTs. Teardown: delete users. | control-plane/controller, NATS |
| SPA continues working with tightened permissions | All existing SPA operations (KV watch, events, publish) work with new JWT | Login as test user, exercise all SPA NATS operations. | web, control-plane, NATS |
| JWT refresh issues tightened permissions | After refresh, old broad permissions are replaced with scoped ones | Login with legacy JWT, trigger refresh, verify new permissions. | control-plane |
| Host cannot access arbitrary streams | Host JWT is denied on `$JS.API.STREAM.INFO.KV_mclaude-sessions` | Create test host, issue JWT. Teardown: delete host. | control-plane, NATS |

## Implementation Plan

| Component | Work | Est. Lines Changed |
|-----------|------|-------------------|
| mclaude-control-plane | Permission function rewrites (per-user buckets, per-project scoping) | ~150 |
| mclaude-control-plane | Cross-user denial tests | ~200 |
| mclaude-control-plane | host_access table, grant/revoke-access handlers | ~200 |
| mclaude-control-plane | HTTP auth endpoints (challenge/verify) + agent registration subscriber | ~150 |
| mclaude-control-plane | Host/agent credential refresh subscribers | ~100 |
| mclaude-control-plane | Bucket lifecycle (per-user bucket creation on registration) | ~80 |
| mclaude-control-plane | Registration endpoint updates | ~60 |
| mclaude-control-plane | Host subject scheme migration (`$SYS` handler, provisioning) | ~80 |
| mclaude-controller-k8s | Remove account key, request credentials from CP, refresh loop | ~150 |
| mclaude-controller-k8s | Subject scheme update (provisioning subscription) | ~30 |
| mclaude-controller-local | Credential request from CP, pass to subprocess, refresh loop | ~200 |
| mclaude-session-agent | Bucket name changes (per-user), stream consolidation, key format | ~100 |
| mclaude-session-agent | Credential refresh loop | ~80 |
| mclaude-common | Subject constants + KV key helpers update | ~50 |
| mclaude-web | Bucket name + stream reference + user slug updates | ~40 |
| mclaude-cli | Host access commands (grant, revoke-access) | ~100 |
