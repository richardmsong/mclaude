# NATS Security Threat Model

## Overview

Security model for NATS in mclaude's event-driven architecture. Covers identity permissions, subject isolation, trust boundaries, KV access control, rate limiting, and threat scenarios for both managed clusters and BYOH (Bring Your Own Hardware) targets.

NATS is the central nervous system. If NATS is compromised, the entire platform is compromised. This document assumes NATS itself is trustworthy and focuses on how identities interact with it.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| KV write authority | Split: controller writes project status, control-plane writes metadata + access | Controller knows actual resource state — no reason to round-trip through control-plane. Control-plane owns access lists and project metadata. |
| Access control | Control-plane routes commands to controllers | Control-plane checks Postgres access, then forwards to `mclaude.clusters.{clusterId}.>`. Controller never sees unauthorized events. No KV access map, no JWT-encoded user lists. |
| Subject structure | User subjects separate from cluster subjects | SPA publishes to `mclaude.{userId}.projects.*`. Control-plane forwards to `mclaude.clusters.{clusterId}.projects.*`. Controllers subscribe only to their cluster's private subjects. |
| GP cluster permissions | Wildcard — all users | GP (managed) clusters accept any user. Control-plane forwards without per-user restriction. |
| BYOH trust level | Same NATS permissions as managed controller | BYOH controller only sees pre-validated commands on its `mclaude.clusters.{clusterId}.>` subject. Cannot see other clusters. Cannot see unauthorized user events. |
| Rate limiting | NATS JWT-level per-client limits | `pub.max`, `data`, `payload` fields in JWT. Prevents any single identity from flooding the system. |
| Cluster IDs | UUIDs, not user-chosen names | Prevents guessing/targeting. Even if you know a cluster exists, you need the UUID to address it. |
| Status update path | Controller writes KV directly | Controller is the authority on resource state. Direct KV write avoids latency and removes control-plane from the critical path. |
| Liveness detection | NATS `$SYS` presence events | No heartbeats. NATS emits connect/disconnect. Control-plane subscribes with system account permissions, updates KV. Covers agent health and controller liveness. |

---

## Identity Model

Every NATS connection authenticates with a JWT signed by the account operator. Each JWT encodes:
- Identity type (user, control-plane, controller)
- Subject permissions (pub/sub allow/deny lists)
- Rate limits
- Expiry

### Identity Types

| Identity | Issued By | Lifetime | Permissions Scope |
|----------|-----------|----------|-------------------|
| SPA (browser user) | Control-plane on login | Session duration (matches HTTP JWT) | `mclaude.{userId}.>` |
| Control-plane | Static (deployed with account seed) | Long-lived | `mclaude.>`, `$KV.>`, `$JS.>` |
| K8s controller | Control-plane on registration | Long-lived, rotatable | `mclaude.clusters.{clusterId}.>`, `$KV.mclaude-projects.{clusterId}.>` |
| BYOH controller | Control-plane on `mclaude register` | Long-lived, rotatable | `mclaude.clusters.{clusterId}.>`, `$KV.mclaude-projects.{clusterId}.>` |
| Session-agent | Controller (via scoped signing key) | Session duration | `mclaude.{userId}.sessions.{clusterId}.{sessionId}.>` |

### JWT Issuance Flow

```
SPA:
  1. User logs in via HTTP (POST /auth/login or OAuth callback)
  2. Control-plane issues HTTP JWT + NATS JWT (signed with account seed)
  3. SPA connects to NATS with the NATS JWT
  4. NATS server validates JWT signature against account public key

Controller:
  1. Admin registers cluster (or user runs mclaude register)
  2. Control-plane issues:
     a. Controller NATS JWT (signed with account seed, scoped to cluster)
     b. Scoped signing key for the cluster (registered on account,
        ceiling: mclaude.*.sessions.{clusterId}.*.>)
  3. Controller stores both credentials
  4. On reconnect: controller uses stored JWT (not re-issued each time)

Session-agent:
  1. Controller signs session-agent JWT with its cluster's signing key
     (scoped to mclaude.{userId}.sessions.{clusterId}.{sessionId}.>)
  2. NATS server validates: signing key is registered on account,
     permissions are within the signing key's ceiling
  3. Controller mounts JWT as Secret in pod (K8s) or passes to process (BYOH)
```

---

## Subject Permissions

### SPA (Browser User)

```
Publish allow:
  mclaude.{userId}.>

Subscribe allow:
  mclaude.{userId}.>

Publish deny:
  $KV.>          # cannot write KV
  $JS.>          # cannot manipulate JetStream directly
  mclaude.system.>  # cannot publish system events
```

A user can only interact with their own subjects. They publish project create/update/delete commands and subscribe to their own session events and KV watch notifications.

### Control-Plane

```
Publish allow:
  mclaude.>              # all user subjects (receives user events)
  mclaude.clusters.>     # forwards commands to controllers
  $KV.mclaude-sessions.> # session state KV
  $JS.>                  # JetStream management (create streams, consumers)
  _INBOX.>               # request/reply

Subscribe allow:
  mclaude.>              # receives all user events
  $SYS.ACCOUNT.>         # presence detection (connect/disconnect events)
  $KV.>                  # KV watch (all buckets)
  $JS.>                  # JetStream management
  _INBOX.>               # request/reply
```

Control-plane is the router: receives user commands on `mclaude.*.projects.>`, checks access in Postgres, forwards to `mclaude.clusters.{clusterId}.projects.*`. Writes access lists and session metadata to KV. Does NOT write project status — that's the controller's responsibility. Subscribes to `$SYS` for presence detection (agent/controller liveness).

### K8s Controller / BYOH Controller

```
Publish allow:
  $KV.mclaude-projects.{clusterId}.>                     # writes project status directly to KV
  _INBOX.>                                   # request/reply

Subscribe allow:
  mclaude.clusters.{clusterId}.>             # pre-validated commands from control-plane

Publish deny:
  mclaude.*.projects.>   # cannot publish user-level commands
  $KV.mclaude-sessions.> # cannot write session state
  $JS.>                   # cannot manipulate JetStream
```

The controller subscribes only to its own cluster's private command subject (`mclaude.clusters.{clusterId}.>`). It never sees raw user events — control-plane validates access and forwards only authorized commands. No wildcard user subscriptions, no access map, no information disclosure.

The controller writes project status directly to the `mclaude-projects` KV bucket. It cannot write to other KV buckets (access lists, sessions).

### Session-Agent

```
Publish allow:
  mclaude.{userId}.sessions.{clusterId}.{sessionId}.>

Subscribe allow:
  mclaude.{userId}.sessions.{clusterId}.{sessionId}.>
```

Narrowest scope. A session-agent can only interact with its own session's subjects. Cannot see other sessions, other users, or any project-level events.

---

## KV Access Control

NATS KV operations map to JetStream subjects under `$KV.<bucket>.<key>`. KV write authority is split by bucket:

| KV Operation | Subject | `mclaude-projects` | `mclaude-sessions` |
|--------------|---------|---------------------|--------------------|
| Put (write) | `$KV.<bucket>.<key>` | Controller | Control-plane |
| Get (read) | `$JS.API.DIRECT.GET.<stream>.<subject>` | All | All |
| Watch | `$KV.<bucket>.>` subscribe | All | All |
| Delete | `$KV.<bucket>.<key>` (purge header) | Controller | Control-plane |

Access control is enforced in Postgres at routing time, not via a KV bucket.

This means:
- **SPA can watch KV** (subscribe to `$KV.mclaude-projects.{clusterId}.>`) — reads project status changes in real-time
- **Controller writes project status** (publish to `$KV.mclaude-projects.{clusterId}.>`) — directly updates status as it provisions
- **Control-plane writes session state** — owns session metadata
- **SPA and controllers cannot write each other's KV buckets** — enforced at JWT level

### KV Buckets

| Bucket | Purpose | Writer | Readers |
|--------|---------|--------|---------|
| `mclaude-projects` | Project status, metadata | Controller (status, key: `{clusterId}.{projectId}`), Control-plane (metadata on create) | SPA (per-user filtered) |
| `mclaude-sessions` | Active session state | Control-plane | SPA |
| `mclaude-clusters` | Per-user accessible cluster list | Control-plane (key: `{userId}`) | SPA (user watches own key) |

---

## Presence Detection

NATS emits system events when clients connect and disconnect. This replaces all heartbeat-based liveness detection.

### How It Works

NATS server publishes to `$SYS.ACCOUNT.{accountId}.CONNECT` and `$SYS.ACCOUNT.{accountId}.DISCONNECT` on every client connect/disconnect. The event payload includes the client's JWT claims (identity type, cluster ID, user ID, session ID).

Control-plane subscribes to `$SYS.ACCOUNT.>` (requires system account permissions in NATS config). On each event:

| Client type | Connect action | Disconnect action |
|-------------|---------------|-------------------|
| Controller | Mark cluster online in KV | Mark cluster offline in KV, stop routing new projects |
| Session-agent | Mark agent healthy in project KV | Mark agent offline in project KV |
| SPA | (no action) | (no action — SPA reconnects automatically) |

### Why Not Heartbeats

- **Zero latency on disconnect**: NATS detects TCP drop immediately. Heartbeats have a staleness window (miss 2-3 intervals before declaring dead).
- **No polling or timers**: No 30s heartbeat loop in session-agent, no 5s health check in SPA, no 90s staleness threshold.
- **Handles crashes and hangs**: NATS has built-in ping/pong. If a client hangs (process alive but unresponsive), NATS kills the connection and emits disconnect.
- **Less code**: Eliminates HeartbeatMonitor (client), heartbeat loop (session-agent), `mclaude-heartbeats` KV bucket, staleness detection logic.

### NATS Config Requirement

Control-plane needs system account permissions to subscribe to `$SYS.>`:

```
# In NATS server config
system_account: SYS
accounts: {
  SYS: { users: [{ user: sys, password: ... }] }
  MCLAUDE: { ... }
}
```

Or via operator JWT: the control-plane's user JWT includes `"bearerToken": true` with system account access.

---

## Rate Limiting

NATS JWTs support per-connection rate limits:

```json
{
  "pub": {
    "max": 100       // max messages per second
  },
  "data": 10485760,  // 10MB/s max data throughput
  "payload": 1048576 // 1MB max message size
}
```

### Per-Identity Rate Limits

| Identity | pub.max | data (bytes/s) | payload (bytes) |
|----------|---------|-----------------|-----------------|
| SPA (user) | 50/s | 5MB/s | 512KB |
| Control-plane | unlimited | unlimited | 4MB |
| K8s controller | 200/s | 20MB/s | 1MB |
| BYOH controller | 100/s | 10MB/s | 1MB |
| Session-agent | 100/s | 10MB/s | 1MB |

**OPEN QUESTION**: Are these limits reasonable? Need production telemetry to tune. Session events (terminal output) may need higher throughput. Start conservative and loosen based on monitoring.

**OPEN QUESTION**: Should BYOH controllers have tighter limits than managed K8s controllers? BYOH runs on untrusted hardware — tighter limits reduce blast radius of a compromised BYOH. But too tight limits break legitimate use.

---

## Trust Boundaries

```
┌────────────────────────────────────────────────────┐
│                  Fully Trusted                      │
│  Control-plane: account seed, KV write, all subs   │
│  NATS server: enforces JWT permissions              │
└──────────────────────┬─────────────────────────────┘
                       │
┌──────────────────────┼─────────────────────────────┐
│              Partially Trusted                      │
│  K8s controller: managed infra, known operator      │
│  Runs in mclaude-system namespace with K8s RBAC     │
│  NATS: scoped to its clusterId                      │
└──────────────────────┬─────────────────────────────┘
                       │
┌──────────────────────┼─────────────────────────────┐
│              Minimally Trusted                      │
│  BYOH controller: user-owned hardware               │
│  Physical access = owner's responsibility            │
│  NATS: same scoping as K8s controller               │
│  Additional risk: hardware not under platform mgmt   │
└──────────────────────┬─────────────────────────────┘
                       │
┌──────────────────────┼─────────────────────────────┐
│              Untrusted Input                         │
│  SPA (browser): user-generated commands              │
│  Session-agent: runs arbitrary user code             │
│  NATS: narrowly scoped, rate-limited                 │
└────────────────────────────────────────────────────┘
```

### What "Partially Trusted" Means

A managed K8s controller runs on infrastructure we operate. We trust the runtime environment but not the software blindly — it still gets scoped NATS permissions. If the controller binary is compromised:
- It can only affect its own cluster's projects
- It only receives pre-validated commands from control-plane (never raw user events)
- It can only write to its own cluster's slice of the `mclaude-projects` KV bucket (`$KV.mclaude-projects.{clusterId}.>`) — cannot write other clusters' status or session state
- It cannot see other clusters' commands
- It can only mint session-agent JWTs for its own cluster (scoped signing key, ceiling enforced by NATS server) — cannot mint controller, SPA, or control-plane JWTs

### What "Minimally Trusted" Means

A BYOH controller runs on a user's laptop or personal server. We have zero control over the runtime. The same NATS scoping applies, but:
- The user could inspect the binary and extract credentials
- The user could modify the binary to behave maliciously within its permissions
- Physical compromise of the hardware = full compromise of that controller identity

This is acceptable because:
- BYOH targets are private by default (only the registering user)
- A compromised BYOH can only affect projects routed to it
- The user is attacking their own resources
- Admin-shared BYOH targets carry higher risk (other users' projects on untrusted hardware) — admin must make an informed decision

---

## Threat Scenarios

### T1: Rogue BYOH Reads Other Users' Commands

**Threat**: A BYOH controller tries to see commands for users who aren't authorized on its cluster.

**Impact**: None. The BYOH controller subscribes to `mclaude.clusters.{clusterId}.>` — a private subject that only control-plane publishes to. Control-plane only forwards commands from authorized users. The controller never sees raw user events (it has no `mclaude.*.>` subscribe permission).

**Residual risk**: If admin shares a BYOH cluster with multiple users, the BYOH controller sees the forwarded commands for all authorized users. This is inherent — the controller needs the project spec to provision it. The BYOH owner has physical access to the hardware running the controller, so they could read the data anyway. Admin must make an informed decision when sharing BYOH targets.

### T2: BYOH Spoofs Project Status via KV

**Threat**: A compromised BYOH controller writes fake project status to the `mclaude-projects` KV bucket, claiming a project is Ready when it's actually failed.

**Impact**: Users see incorrect project status for projects on that cluster. SPA shows "Ready" but the project is broken.

**Mitigation**: KV keys are cluster-prefixed (`{clusterId}.{projectId}`). Controller JWT is scoped to `$KV.mclaude-projects.{itsClusterId}.>`. NATS blocks writes to other clusters' keys at the server level. A compromised controller can only write bogus status for projects on its own cluster.

**Residual risk**: Status confusion limited to the compromised cluster's projects. The underlying resources are untouched. Users will quickly notice when sessions don't work. Admin can deregister the cluster (revoke JWT) to stop the poisoning.

### T3: DoS on BYOH Target

**Threat**: A malicious user spams project create events targeting a BYOH cluster: `mclaude.{userId}.projects.create` with the BYOH cluster's UUID in the payload.

**Analysis**: The user publishes to `mclaude.{userId}.projects.create` with the BYOH cluster's UUID in the payload. Control-plane receives it.

**But**: Control-plane checks Postgres access before forwarding. If the user isn't authorized for the BYOH cluster, the command is rejected at control-plane — it never reaches the controller.

**Mitigation**:
- UUID cluster IDs prevent guessing
- Control-plane rejects unauthorized commands before forwarding (Postgres access check)
- NATS rate limiting on the user's connection caps the spam rate at control-plane
- Even if the user spams, only control-plane sees the load — the BYOH controller sees nothing

**Residual risk**: CPU waste on control-plane for rejected commands. Mitigated by rate limiting. Not a viable DoS vector against the BYOH target.

### T4: KV Poisoning

**Threat**: A compromised identity writes malicious data to NATS KV.

**Mitigation by bucket:**
- `mclaude-sessions` (session state): only control-plane can write. **Residual risk: zero.**
- `mclaude-projects` (project status): controllers can write. A compromised controller could write bogus status for any project (see T2). SPA is denied `$KV.mclaude-projects.{clusterId}.>` publish. **Residual risk: status confusion, mitigated by monitoring.**

Access control is enforced in Postgres at routing time (no KV access bucket). The project status bucket is scoped per-cluster via KV key prefix (`{clusterId}.{projectId}`) and JWT scoping (`$KV.mclaude-projects.{clusterId}.>`) — a compromised controller can only affect its own cluster's status.

### T5: Subject Hijacking

**Threat**: A controller subscribes to subjects outside its authorized cluster ID, e.g., `mclaude.clusters.*.>` instead of `mclaude.clusters.{itsClusterId}.>`, or to user subjects like `mclaude.*.projects.>`.

**Mitigation**: JWT permissions restrict the subscribe pattern. The NATS server rejects subscribe requests that don't match the JWT's allowed subjects. The controller literally cannot subscribe to other clusters' command subjects or to raw user subjects.

**Residual risk**: Zero — enforced at the NATS server level.

### T6: Session-Agent Breakout

**Threat**: A session-agent (running user code) attempts to read other users' sessions or publish to project-level subjects.

**Mitigation**: Session-agent JWT is scoped to `mclaude.{userId}.sessions.{clusterId}.{sessionId}.>` only. It cannot:
- See other users' sessions
- See its own other sessions
- Publish project create/update/delete commands
- Read or write KV
- Manipulate JetStream

**Residual risk**: Zero for NATS-level attacks. The session-agent can still do damage within its container (file system, network calls to external services). That's a container isolation concern, not a NATS concern.

### T7: Replay Attack

**Threat**: An attacker captures a NATS message (e.g., project create) and replays it later.

**Analysis**: NATS doesn't natively prevent replay at the transport level. However:
- Messages are authenticated (JWT-verified connection)
- Project create is idempotent (Postgres `ON CONFLICT` or duplicate check)
- Status events are idempotent (KV put overwrites)
- Session events are ephemeral (terminal output — replay shows stale output, no damage)

**Mitigation**: Idempotent handlers. An attacker would need an active, authenticated NATS connection to replay — at which point they already have valid credentials.

**Residual risk**: Negligible. Replay of a create event creates a duplicate check, not a duplicate project.

### T8: JWT Credential Theft

**Threat**: An attacker extracts NATS JWT credentials from:
- SPA browser memory (DevTools)
- Session-agent pod (filesystem or env)
- BYOH controller binary (decompilation)

**Impact**: Attacker gets a NATS connection with the stolen identity's permissions.

**Mitigation**:
- SPA JWTs have short expiry (session duration)
- Session-agent JWTs are scoped to a single session (minimal blast radius)
- Controller JWTs are long-lived but scoped to one cluster
- All JWTs can be revoked by the control-plane (NATS supports revocation lists)
- Rate limiting caps what a stolen credential can do

**Residual risk**: Temporary impersonation within the stolen identity's scope until JWT expires or is revoked. The narrower the scope, the lower the risk.

### T9: Mass JWT Refresh Attack

**Threat**: A cluster access change (add/remove cluster, add/remove user from cluster) requires refreshing all affected JWTs.

**Analysis**: In the current design, this is a non-issue. Controller JWTs encode a static subscribe pattern (`mclaude.clusters.{clusterId}.>`). Access control is enforced by control-plane at routing time (Postgres check), not by JWT scoping. When a user's cluster access changes, control-plane simply starts or stops forwarding their commands — no JWT refresh needed for anyone.

User JWTs are also unaffected: they publish to `mclaude.{userId}.projects.*` regardless of which clusters they can access. The access decision happens at control-plane, not at NATS.

**Mitigation**: N/A — the architecture avoids the problem entirely.

**Residual risk**: None. The only JWT refresh scenario is account seed rotation (nuclear option, see Account Seed Distribution).

---

## Account Seed & Signing Key Distribution

The account seed (`NATS_ACCOUNT_SEED`) is the most sensitive credential in the system. It signs all NATS JWTs. It must never leave the control-plane.

### Signing Hierarchy

```
Account Seed (control-plane only)
├── Signs: SPA user JWTs
├── Signs: controller JWTs
├── Signs: scoped signing keys (one per cluster)
│
├── K8s Cluster Signing Key (scoped)
│   └── Signs: session-agent JWTs (ceiling: mclaude.*.sessions.{clusterId}.*.>)
│
└── BYOH Cluster Signing Key (scoped)
    └── Signs: session-agent JWTs (ceiling: mclaude.*.sessions.{clusterId}.*.>)
```

### How It Works

NATS supports **scoped signing keys**: a signing key registered on an account with a permission ceiling. Any JWT signed by the key is automatically clamped to that ceiling by the NATS server, regardless of what the JWT claims say.

1. Control-plane holds the account seed. Never shared.
2. On cluster registration, control-plane generates a signing key scoped to `mclaude.*.sessions.{clusterId}.*.>` and registers it on the account.
3. The signing key is given to the controller (K8s or BYOH).
4. Controller uses the signing key to mint session-agent JWTs. Each JWT is scoped to `mclaude.{userId}.sessions.{clusterId}.{sessionId}.>`.
5. NATS server validates: signing key is registered on the account, JWT permissions are within the signing key's ceiling.

### Why Not the Account Seed?

If a controller had the account seed, a compromise would let the attacker mint arbitrary JWTs — impersonate control-plane, read all user data, write all KV buckets. With a scoped signing key, a compromised controller can only mint session-scoped JWTs for its own cluster. The blast radius is bounded.

### Per-Identity Credential

| Identity | Credential | Issued By | Signs |
|----------|-----------|-----------|-------|
| Control-plane | Account seed | Operator (deploy-time) | SPA JWTs, controller JWTs, signing keys |
| K8s controller | Controller JWT + cluster signing key | Control-plane (registration) | Session-agent JWTs (scoped to its cluster) |
| BYOH controller | Controller JWT + cluster signing key | Control-plane (`mclaude register`) | Session-agent JWTs (scoped to its cluster) |
| SPA | User JWT | Control-plane (login) | Nothing |
| Session-agent | Session JWT | Controller (pod/process creation) | Nothing |

---

## Network Security

### TLS

All NATS connections use TLS. The NATS server presents a certificate trusted by:
- In-cluster clients: via Kubernetes CA or mounted cert
- BYOH controllers: via system trust store or explicit CA
- SPA (WebSocket): via the same TLS termination as the web app (Traefik/ingress)

### Network Policies (K8s)

```yaml
# NATS server: only accept connections from:
# - control-plane pods (same namespace)
# - controller pods (same namespace)
# - session-agent pods (all namespaces)
# - ingress (for WebSocket from SPA)
```

BYOH controllers connect via the external NATS endpoint (through ingress). They cannot access internal cluster networking.

---

## Monitoring & Alerting

### Metrics to Watch

| Metric | Source | Alert Threshold |
|--------|--------|-----------------|
| Unauthorized subscribe attempts | NATS server logs | Any occurrence |
| `$KV.>` publish from non-control-plane | NATS server logs | Any occurrence (should be impossible) |
| Connection rate per account | NATS monitoring (`/connz`) | >100/min sustained |
| Message rate per connection | NATS monitoring (`/connz`) | Approaching JWT `pub.max` |
| Slow consumer warnings | NATS server logs | Any occurrence |
| JWT expiry failures | NATS server logs | >10/min (indicates rotation issue) |
| KV watch lag | Control-plane metrics | >5s behind |

### Audit Trail

Every NATS connection is logged with:
- Client IP
- JWT claims (identity type, scoped permissions)
- Connect/disconnect timestamps

**OPEN QUESTION**: Should NATS messages themselves be logged? Full message logging is expensive and contains user data. Options:
1. Log subject + metadata only (no payload) — sufficient for security audit
2. Full message logging to a dedicated stream — for forensics, with retention policy
3. No message logging — rely on application-level logging in control-plane and controllers

---

## Revocation

NATS supports JWT revocation via revocation lists maintained by the account.

### Revocation Scenarios

| Scenario | Action |
|----------|--------|
| User session ends (logout) | Revoke SPA JWT. NATS disconnects the browser client. |
| BYOH controller deregistered | Revoke controller JWT. NATS disconnects the BYOH. |
| User removed from platform | Revoke all user JWTs (SPA + session-agents). |
| Account seed compromised | Rotate account seed. Reissue ALL JWTs. Nuclear option. |
| Suspicious BYOH activity | Revoke BYOH controller JWT immediately. |

### Revocation Propagation

NATS revocations take effect within seconds. The NATS server checks the revocation list on every publish/subscribe and disconnects revoked clients.

Control-plane maintains the revocation list and publishes it to the NATS server via the account JWT update mechanism.

---

## Implementation Priorities

### Must Have (v1)

- JWT scoping per identity type (SPA, control-plane, controller, session-agent)
- `$KV.>` restricted to control-plane
- Controller access check via KV-backed in-memory map
- Rate limiting per identity type
- UUID cluster IDs

### Should Have (v1 if time permits)

- NATS monitoring endpoints exposed to Prometheus
- Alert rules for unauthorized access attempts
- Revocation support for deregistered controllers

### Deferred

- Delegated signing keys for BYOH (depends on account seed decision)
- Full message audit logging
- Cross-region NATS super-cluster security
- mTLS between NATS nodes (single-node in v1)
