# NATS Security Threat Model

## Overview

Security model for NATS in mclaude's event-driven architecture. Covers identity permissions, subject isolation, trust boundaries, KV access control, rate limiting, and threat scenarios for both managed clusters and BYOH (Bring Your Own Hardware) targets.

NATS is the central nervous system. If NATS is compromised, the entire platform is compromised. This document assumes NATS itself is trustworthy and focuses on how identities interact with it.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| KV write authority | Control-plane is sole `$KV.>` publisher | Single source of truth. Controllers and SPAs read KV via watches, never write. Prevents split-brain. |
| Controller access checks | In-memory map from KV watch, not JWT scoping | Avoids mass JWT refresh when clusters/users change. Nanosecond map lookup. KV watch keeps it current. |
| Subject scoping | Cross-dimensional: userId × clusterId | Users bound to `mclaude.{userId}.*`, controllers bound to `mclaude.*.clusters.{clusterId}.*`. Intersection enforces isolation. |
| BYOH trust level | Same NATS permissions as managed controller | BYOH controller can only see events for its own clusterId. Cannot read other clusters. Physical access to BYOH hardware is the owner's responsibility. |
| Rate limiting | NATS JWT-level per-client limits | `pub.max`, `data`, `payload` fields in JWT. Prevents any single identity from flooding the system. |
| Cluster IDs | UUIDs, not user-chosen names | Prevents guessing/targeting. Even if you know a cluster exists, you need the UUID to address it. |
| Status update path | Controller → status event → control-plane → KV | Controllers never write KV directly. Control-plane validates and writes. |

**OPEN QUESTION**: Should controllers be allowed to write KV directly for status updates? Current design routes through control-plane for validation, but adds latency. If controller writes KV directly, we lose the validation layer but gain speed. Risk: a compromised controller could write arbitrary KV entries for its cluster's projects.

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
| K8s controller | Control-plane on registration | Long-lived, rotatable | `mclaude.*.clusters.{clusterId}.>` |
| BYOH controller | Control-plane on `mclaude register` | Long-lived, rotatable | `mclaude.*.clusters.{clusterId}.>` |
| Session-agent | Controller on pod creation | Session duration | `mclaude.{userId}.sessions.{sessionId}.>` |

### JWT Issuance Flow

```
SPA:
  1. User logs in via HTTP (POST /auth/login or OAuth callback)
  2. Control-plane issues HTTP JWT + NATS JWT (signed with account seed)
  3. SPA connects to NATS with the NATS JWT
  4. NATS server validates JWT signature against account public key

Controller:
  1. Admin registers cluster (or user runs mclaude register)
  2. Control-plane issues NATS JWT scoped to the cluster's UUID
  3. Controller connects with JWT
  4. On reconnect: controller uses stored credentials (not re-issued each time)

Session-agent:
  1. Controller creates pod with NATS credentials mounted as Secret
  2. Session-agent reads credentials from mounted path
  3. JWT scoped to the specific user's session subjects
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
  mclaude.>      # all mclaude subjects
  $KV.>          # sole KV writer
  $JS.>          # JetStream management (create streams, consumers)
  _INBOX.>       # request/reply

Subscribe allow:
  mclaude.>      # receives all events
  $KV.>          # KV watch
  $JS.>          # JetStream management
  _INBOX.>       # request/reply
```

Control-plane is the superuser. It reads all events, writes all KV, manages JetStream configuration.

### K8s Controller / BYOH Controller

```
Publish allow:
  mclaude.system.clusters.{clusterId}.>     # status events only
  _INBOX.>                                   # request/reply

Subscribe allow:
  mclaude.*.clusters.{clusterId}.>           # project commands for this cluster

Publish deny:
  $KV.>          # cannot write KV
  $JS.>          # cannot manipulate JetStream
```

The wildcard `*` in the subscribe pattern means the controller sees events from ALL users that target its cluster. This is by design — the controller needs to provision resources for any authorized user.

The controller can only PUBLISH to its own cluster's system status subjects. It cannot publish commands (create/update/delete) — those come from users.

### Session-Agent

```
Publish allow:
  mclaude.{userId}.sessions.{sessionId}.>

Subscribe allow:
  mclaude.{userId}.sessions.{sessionId}.>
```

Narrowest scope. A session-agent can only interact with its own session's subjects. Cannot see other sessions, other users, or any project-level events.

---

## KV Access Control

NATS KV operations map to JetStream subjects under `$KV.<bucket>.<key>`. By restricting `$KV.>` publish to control-plane only:

| KV Operation | Subject | Allowed For |
|--------------|---------|-------------|
| Put (write) | `$KV.<bucket>.<key>` | Control-plane only |
| Get (read) | `$JS.API.DIRECT.GET.<stream>.<subject>` | All (via JetStream API) |
| Watch | `$JS.API.CONSUMER.CREATE.<stream>` + `$KV.<bucket>.>` subscribe | All (subscribe) |
| Delete | `$KV.<bucket>.<key>` (with purge header) | Control-plane only |

This means:
- **SPA can watch KV** (subscribe to `$KV.mclaude-projects.>`) — reads project status changes in real-time
- **Controller can watch KV** (subscribe to `$KV.mclaude-access.>`) — reads access list for authorization checks
- **Neither can write KV** — only control-plane publishes to `$KV.>` subjects

### KV Buckets

| Bucket | Purpose | Writer | Readers |
|--------|---------|--------|---------|
| `mclaude-projects` | Project status, metadata | Control-plane | SPA (per-user filtered), Controller (per-cluster filtered) |
| `mclaude-access` | Cluster access lists (userId → clusterId mappings) | Control-plane | Controllers (watch for authorization) |
| `mclaude-sessions` | Active session state | Control-plane | SPA |

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
- It cannot write KV (so it can't poison the global state)
- It cannot see other clusters' events
- It cannot issue JWTs (doesn't have the account seed — **OPEN QUESTION**: it does have the account seed for signing session-agent JWTs. See "Account Seed Distribution" below.)

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

### T1: Rogue BYOH Yoinks Events

**Threat**: A BYOH controller subscribes to `mclaude.*.clusters.{clusterId}.>` and reads project create events intended for another user who was admin-assigned to the same BYOH target.

**Impact**: The BYOH operator sees project names, git URLs, and configuration for other users' projects routed to their hardware.

**Mitigation**: This is by design — if an admin routes User B's projects to User A's BYOH hardware, User A can see those project specs. Admins must understand this when sharing BYOH targets. The BYOH target listing in the admin UI should clearly warn about this.

**Residual risk**: Information disclosure to BYOH owner. Acceptable if admin explicitly opted in.

### T2: BYOH Spoofs Status Events

**Threat**: A compromised BYOH controller publishes fake status events (`mclaude.system.clusters.{clusterId}.projects.{projectId}.status`) claiming a project is Ready when it's actually failed.

**Impact**: Users see incorrect project status. SPA shows "Ready" but the project is broken.

**Mitigation**:
- Status events flow through control-plane before hitting KV. Control-plane can validate (e.g., cross-reference with recent create events).
- Rate limiting prevents flooding status events.
- Users will quickly notice the project doesn't work and report it.

**Residual risk**: Temporary status confusion. Low impact — the actual session won't function, and the user will see errors when they try to use it.

### T3: DoS on BYOH Target

**Threat**: A malicious user spams project create events targeting a BYOH controller's cluster ID: `mclaude.{userId}.clusters.{byohClusterId}.projects.create`.

**Analysis**: The user would need:
1. Know the BYOH cluster's UUID (randomly generated, not guessable)
2. Have NATS permissions to publish to their own `mclaude.{userId}.clusters.{byohClusterId}.projects.create`
3. The BYOH controller subscribes to `mclaude.*.clusters.{byohClusterId}.>`, so it would see these events

**But**: The controller checks the in-memory access map before acting. If userId is not authorized for this clusterId, the event is dropped immediately.

**Mitigation**:
- UUID cluster IDs prevent guessing
- Access map check drops unauthorized events (nanosecond operation)
- NATS rate limiting on the user's connection caps the spam rate
- Even if events arrive, they're dropped without provisioning anything

**Residual risk**: Minimal CPU waste on unauthorized event drops. Not a viable DoS vector.

### T4: KV Poisoning

**Threat**: A compromised controller or SPA attempts to write malicious data to NATS KV (e.g., overwriting project status, access lists).

**Mitigation**: `$KV.>` publish is restricted to control-plane only in the NATS JWT. Any attempt by other identities to publish to `$KV.>` subjects is rejected by the NATS server before the message is delivered.

**Residual risk**: Zero — enforced at the NATS server level via JWT permissions.

### T5: Subject Hijacking

**Threat**: A controller subscribes to subjects outside its authorized cluster ID, e.g., `mclaude.*.clusters.*.>` instead of `mclaude.*.clusters.{itsClusterId}.>`.

**Mitigation**: JWT permissions restrict the subscribe pattern. The NATS server rejects subscribe requests that don't match the JWT's allowed subjects. The controller literally cannot subscribe to other clusters' subjects.

**Residual risk**: Zero — enforced at the NATS server level.

### T6: Session-Agent Breakout

**Threat**: A session-agent (running user code) attempts to read other users' sessions or publish to project-level subjects.

**Mitigation**: Session-agent JWT is scoped to `mclaude.{userId}.sessions.{sessionId}.>` only. It cannot:
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

**Threat**: A cluster access change (add/remove cluster, add/remove user from cluster) requires refreshing all affected JWTs. If permissions were encoded in JWTs, changing cluster access would require refreshing every affected user's JWT simultaneously.

**Analysis**: This is why controllers self-check access from KV rather than relying on JWT scoping for cluster authorization. The JWT gives the controller permission to SUBSCRIBE to its cluster's subjects. But the controller additionally checks the KV-backed access map before ACTING on events.

**Mitigation**: Controller JWTs encode the subscribe pattern (`mclaude.*.clusters.{clusterId}.>`), which is static. Access changes are reflected in KV, which controllers watch. No JWT refresh needed for access list changes.

**Residual risk**: Brief window where a removed user's events are still received but dropped by access check. Acceptable latency (sub-second for KV watch propagation).

---

## Account Seed Distribution

The account seed (`NATS_ACCOUNT_SEED`) is the most sensitive credential in the system. It signs all NATS JWTs.

**Current distribution:**
- Control-plane: has account seed (signs user + controller JWTs)
- K8s controller: has account seed (signs session-agent JWTs)
- BYOH controller: has account seed (signs session-agent JWTs)

**OPEN QUESTION**: Should BYOH controllers have the account seed? If a BYOH controller is compromised, the attacker has the account seed and can issue arbitrary JWTs with any permissions. This is the single biggest security risk in the current design.

Options:
1. **BYOH gets the seed** (current) — simplest. BYOH can sign session-agent JWTs locally without round-tripping to control-plane. Risk: seed exposure on untrusted hardware.
2. **BYOH requests JWTs from control-plane** — BYOH sends a request to control-plane ("I need a JWT for session X"), control-plane signs and returns it. BYOH never has the seed. Adds latency to session creation. Adds a hard dependency on control-plane for session starts.
3. **Delegated signing with a sub-key** — Control-plane issues a delegated signing key scoped to the BYOH's cluster. BYOH can sign JWTs but only with permissions within its cluster scope. NATS supports signing keys, but the scoping may not be granular enough.
4. **Pre-issued session JWTs** — Control-plane pre-issues session-agent JWTs when the project is created and includes them in the create event. BYOH stores them and mounts them when creating sessions. Limits: fixed number of JWTs, requires re-issuance for new sessions.

**Recommendation**: Option 2 (request from control-plane) for BYOH targets. Managed K8s controllers keep the seed (managed infrastructure, lower risk). The latency of a NATS request/reply for JWT issuance is sub-10ms on a healthy system — acceptable for session creation.

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
