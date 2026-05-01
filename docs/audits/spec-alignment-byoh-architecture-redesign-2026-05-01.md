## Run: 2026-05-01T00:00:00Z

**ADR**: `docs/adr-0058-byoh-architecture-redesign.md` (status: draft)

**Specs checked**:
- `docs/spec-state-schema.md`
- `docs/spec-nats-payload-schema.md`
- `docs/spec-nats-activity.md`
- `docs/mclaude-session-agent/spec-session-agent.md`
- `docs/mclaude-control-plane/spec-control-plane.md`
- `docs/mclaude-controller/spec-controller.md`

### Phase 1 — ADR → Spec (forward pass)

| # | ADR (section) | ADR text | Spec location | Verdict | Direction | Notes |
|---|---------------|----------|---------------|---------|-----------|-------|
| 1 | Q1 | "The host controller `exec`s a new `mclaude-session-agent` process for each project." | spec-controller.md §Local/Process supervision: "Starts `mclaude-session-agent --mode standalone...` as a child process" | REFLECTED | — | Controller spec already describes per-project subprocess spawning |
| 2 | Q1 | "Sessions within a project run inside the per-project agent process" | spec-session-agent.md §Role: "One Agent instance owns all sessions for a (userId, hostSlug, projectId) triple" | REFLECTED | — | Session agent spec already describes multi-session per-project model |
| 3 | Q1 | "Agent generates its own NKey pair at startup and passes the public key to the host controller via local IPC (stdout/file)" | spec-controller.md §Local/Process supervision | GAP | SPEC→FIX | Controller local spec doesn't mention NKey generation by agent or local IPC for public key exchange. It just says "starts a session-agent subprocess" with no credential exchange step. |
| 4 | Q2 | "Host controller subscribes to `mclaude.hosts.{hslug}.>` (ADR-0054 host-scoped scheme, superseding ADR-0035's `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>`)" | spec-controller.md §Local/NATS subscriptions: subjects listed as `mclaude.users.{USER_SLUG}.hosts.{HOST_SLUG}.api.projects.{provision,create,update,delete}` | GAP | SPEC→FIX | spec-controller.md local variant still uses the old ADR-0035 subject scheme. ADR-0058 explicitly notes this must be updated. spec-nats-activity.md §4 already uses the new scheme (`mclaude.hosts.laptop-a.>`) — divergence between specs. |
| 5 | Q2 | "SPA/CLI uses the existing HTTP endpoint... CP validates... and then CP itself publishes `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.{create,delete}` to NATS; the host controller receives this message" | spec-nats-payload-schema.md §Project Subjects: "Publisher: SPA/CLI \| Subscribers: CP + host controller" | GAP | UNCLEAR | ADR-0058 says CP publishes to fan-out subjects after HTTP validation ("Users do not publish directly to host-scoped NATS subjects"). spec-nats-payload-schema.md and spec-nats-activity.md §6 both show SPA publishing directly with CP + host controller as parallel subscribers. These are fundamentally different flow models. |
| 6 | Q2 | "There is no job queue KV — it was eliminated by ADR-0044" | spec-state-schema.md §NATS KV Buckets / mclaude-job-queue: full schema described | GAP | SPEC→FIX | spec-state-schema.md still documents `mclaude-job-queue` KV bucket as active. ADR-0044 eliminates it; ADR-0058 confirms it no longer exists. This is an ADR-0044 gap that ADR-0058 inherits. |
| 7 | Q2 | "The host controller does not watch any KV bucket for lifecycle management; all provisioning flows through NATS core subjects" | spec-controller.md §Local: no KV mentions | REFLECTED | — | spec-controller.md local variant doesn't list any KV bucket access, consistent with the decision |
| 8 | Q3 | "CP designates exactly one agent per user as the quota publisher. The designated agent's `runQuotaPublisher` goroutine polls... and publishes `QuotaStatus` to `mclaude.users.{uslug}.quota`" | spec-nats-activity.md §8e: "CP designates exactly one agent per user as the quota publisher" | REFLECTED | — | spec-nats-activity.md fully describes the designated-agent quota publisher model |
| 9 | Q3 | "each per-project agent subscribes to `mclaude.users.{uslug}.quota`... and run independent per-session `QuotaMonitor` goroutines" | spec-session-agent.md §NATS Subjects (Subscribe): "Quota status (per-session QuotaMonitor) via core NATS on `mclaude.users.{uslug}.quota`" | REFLECTED | — | Session agent spec describes per-session QuotaMonitor subscription |
| 10 | Q3 | Designated-agent quota publisher: "polls `api.anthropic.com/api/oauth/usage` every 60 seconds" | spec-state-schema.md §Quota Status: "Writers: daemon `runQuotaPublisher` (every 60s...)" | GAP | SPEC→FIX | spec-state-schema.md attributes quota publishing to "daemon" only. ADR-0058 moves this to the designated per-project agent. The writer should be "designated per-project agent" (or both daemon legacy and agent target). |
| 11 | Q6 | "Host controller registers the public key with CP via `mclaude.hosts.{hslug}.api.agents.register` (NATS request/reply). If CP returns `NOT_FOUND`, retry with exponential backoff (100ms, doubling, max 5s, max 10 attempts)" | spec-nats-payload-schema.md §Agent Public Key Registration: full subject and retry semantics described | REFLECTED | — | spec-nats-payload-schema.md documents this subject, payload, and retry logic accurately |
| 12 | Q6 | "Agent authenticates itself via HTTP challenge-response (`POST /api/auth/challenge` + `POST /api/auth/verify`) to get its per-project JWT" | spec-nats-payload-schema.md §Unified HTTP Credential Protocol: challenge-response flow documented | REFLECTED | — | — |
| 13 | Q6 | "Agent authenticates via HTTP challenge-response to get its per-project JWT" | spec-session-agent.md §Configuration, §Credential Management | GAP | SPEC→FIX | spec-session-agent.md does not describe agent HTTP challenge-response authentication for BYOH mode. It describes K8s credential management (Secret mounts) and daemon JWT refresh (POST to refresh URL), but not the ADR-0058/0054 model where agents generate NKey pairs and authenticate via HTTP. |
| 14 | Q6 | "No credential handoff between controller and agent — the controller never touches the agent's JWT or private key" | spec-controller.md §Local/Process supervision | GAP | SPEC→FIX | spec-controller.md local variant doesn't mention credential isolation between controller and agent. The provisioning flow just says "starts a session-agent subprocess" with no discussion of credential boundaries. |
| 15 | Q6 | "Agent JWTs have a 5-minute TTL... The agent runs the same HTTP challenge-response flow before TTL expiry (proactive refresh). On `permissions violation` error, the agent triggers an immediate refresh + retry" | spec-session-agent.md | GAP | SPEC→FIX | spec-session-agent.md describes daemon JWT refresh (POST to refresh URL, 15-min threshold on TTL) but not agent-level HTTP challenge-response refresh with 5-min TTL. The proactive refresh and permissions-violation retry are not documented. |
| 16 | Q6 | "Agent JWTs have a 5-minute TTL per ADR-0054" | spec-nats-activity.md §10: "Before the 5-min TTL expires, the agent refreshes via the same HTTP challenge-response" | REFLECTED | — | spec-nats-activity.md documents agent credential refresh with 5-min TTL |
| 17 | Q7 | "Host controller has zero JetStream access — no `$JS.*`, `$KV.*`, or `$O.*` subjects in its JWT" | spec-nats-activity.md §Identities: "Host JWT (`laptop-a`): `mclaude.hosts.laptop-a.>` only. Zero JetStream." | REFLECTED | — | — |
| 18 | Q7 | "Host controller has zero JetStream access" | spec-controller.md §Local/Authentication: "mclaude-controller-local: mclaude.users.{uslug}.hosts.{hslug}.> (the user's per-host JWT)" | PARTIAL | SPEC→FIX | spec-controller.md doesn't explicitly state zero JetStream for local controller. The old per-host user JWT (spec-state-schema.md) includes `$JS.*` permissions. The spec should explicitly state host controller has zero JetStream per ADR-0054/0058. |
| 19 | Q7 | "Per-project agents watch their own project's KV (`mclaude-sessions-{uslug}` bucket, scoped to `hosts.{hslug}.projects.{pslug}.sessions.>` key prefix)" | spec-nats-activity.md §8a: "Agent watches own session keys in `KV_mclaude-sessions-alice`, filtered to `hosts.laptop-a.projects.myapp.sessions.>`" | REFLECTED | — | — |
| 20 | Q7 | "Per-project agents watch their own project's KV (`mclaude-sessions-{uslug}` bucket)" | spec-session-agent.md §NATS KV Buckets: "mclaude-sessions (write)" | GAP | SPEC→FIX | spec-session-agent.md references the shared `mclaude-sessions` bucket (ADR-0035 model), not the per-user `mclaude-sessions-{uslug}` bucket (ADR-0054/0058 model). This is an ADR-0054 gap that ADR-0058 inherits. |
| 21 | Q8 | "`mclaude-controller-local` is extended with: ADR-0054 subject scheme (update subscription from old to `mclaude.hosts.{hslug}.>`)" | spec-controller.md §Local/NATS subscriptions | GAP | SPEC→FIX | Same as #4 — spec-controller.md local variant still uses old ADR-0035 subjects |
| 22 | Q8 | "`mclaude-controller-local` is extended with: Agent credential registration (on project provisioning, register the spawned agent's NKey public key with CP via `mclaude.hosts.{hslug}.api.agents.register`)" | spec-controller.md §Local/NATS subscriptions, §Local/Process supervision | GAP | SPEC→FIX | spec-controller.md local variant has no mention of agent credential registration. The provisioning flow ends at "starts a session-agent subprocess for that project. Replies success once the session-agent's `--ready` probe passes" — no NKey registration step. |
| 23 | Q8 | "`mclaude-controller-local` is extended with: Host credential refresh (timer-based HTTP challenge-response before 5-min JWT TTL expiry)" | spec-controller.md §Local | GAP | SPEC→FIX | spec-controller.md local variant has no mention of host credential refresh. The Authentication section says it uses "the user's per-host JWT" but doesn't describe refresh. spec-nats-activity.md §5 does describe this flow. |
| 24 | Deprecation Plan §Phase 1 | "`mclaude daemon` CLI command is updated to launch `mclaude-controller-local` instead of `mclaude-session-agent --daemon`" | spec-session-agent.md §Daemon Mode: "Runs as a long-lived process... with `--daemon --host <hslug>`" | GAP | SPEC→FIX | spec-session-agent.md describes daemon mode as the active, current BYOH model. No mention of CLI command redirecting to mclaude-controller-local. |
| 25 | Deprecation Plan §Phase 3 | "Remove `daemon.go`, `daemon_jobs.go`, and all daemon-specific code from `mclaude-session-agent`... The `runQuotaPublisher` goroutine moves to the designated per-project agent" | spec-session-agent.md §Daemon Mode, §Internal Behavior/Daemon sections | GAP | SPEC→FIX | spec-session-agent.md extensively documents daemon mode (JWT Refresh, Job Dispatcher, Liveness, HTTP endpoints) as current architecture with no deprecation notice. |
| 26 | Deprecation Plan | "No migration of in-flight sessions. Sessions running under daemon mode at cut-over time are terminated." | (no spec location) | GAP | SPEC→FIX | No spec documents the migration strategy or the expectation that in-flight sessions are terminated at cut-over. This should be noted in the session-agent or controller spec. |
| 27 | Target Architecture | "Host controller uses host JWT scoped to `mclaude.hosts.{hslug}.>` — zero JetStream access" | spec-controller.md §Local/Authentication | GAP | SPEC→FIX | spec-controller.md says local controller uses "the user's per-host JWT" which per spec-state-schema.md includes full JetStream access. ADR-0058 requires a host-specific JWT with zero JetStream, scoped to `mclaude.hosts.{hslug}.>` only. |
| 28 | Target Architecture | "Per-project agents spawned as subprocesses with per-project JWTs (matching K8s model — one process = one project)" | spec-session-agent.md §Deployment | PARTIAL | SPEC→FIX | spec-session-agent.md describes two modes: standalone (K8s, per-project) and daemon (BYOH, cross-project). The new model where BYOH also uses per-project agents spawned by the controller is not described. The "Standalone Mode (K8s)" section matches, but there's no description of BYOH standalone agents spawned by mclaude-controller-local. |
| 29 | Q2 | "CP publishes `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.{create,delete}` to NATS" | spec-control-plane.md §NATS Subjects/Publishes: subjects listed as `mclaude.users.{uslug}.hosts.{hslug}.api.projects.{provision,update,delete}` | GAP | SPEC→FIX | spec-control-plane.md still uses the old ADR-0035 provisioning subjects (`mclaude.users.{uslug}.hosts.{hslug}.api.projects.*`). ADR-0058 adopts the ADR-0054 fan-out scheme (`mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.*`). |
| 30 | Q6 | "Host controller registers the public key with CP via `mclaude.hosts.{hslug}.api.agents.register`" | spec-control-plane.md §NATS Subjects/Subscribes | GAP | SPEC→FIX | spec-control-plane.md does not list `mclaude.hosts.{hslug}.api.agents.register` as a subscribed subject. CP must subscribe to this subject to handle agent NKey registration requests from host controllers. |

### Summary

- Reflected: 12
- Gap: 16
- Partial: 2

### Verdict: NOT CLEAN

### Gap Details

**spec-controller.md (7 gaps)** — Most impacted. The local controller spec has not been updated for ADR-0058:
1. GAP [SPEC→FIX] #3: Missing NKey generation by agent and local IPC for public key exchange in process supervision flow
2. GAP [SPEC→FIX] #4/#21: NATS subscription subjects still use old ADR-0035 scheme (`mclaude.users.{uslug}.hosts.{hslug}.api.projects.>`) — must update to `mclaude.hosts.{hslug}.>` 
3. GAP [SPEC→FIX] #14: No mention of credential isolation (controller never touches agent JWT/private key)
4. GAP [SPEC→FIX] #22: No agent credential registration step (`mclaude.hosts.{hslug}.api.agents.register`) in provisioning flow
5. GAP [SPEC→FIX] #23: No host credential refresh via HTTP challenge-response (5-min TTL)
6. GAP [SPEC→FIX] #27: Authentication section says controller uses "per-host user JWT" (with JetStream) — should be host-specific JWT with zero JetStream

**spec-session-agent.md (5 gaps, 1 partial)** — Daemon mode described as current with no deprecation path:
1. GAP [SPEC→FIX] #13: No description of agent HTTP challenge-response authentication for BYOH mode
2. GAP [SPEC→FIX] #15: No agent-level HTTP challenge-response refresh with 5-min TTL
3. GAP [SPEC→FIX] #20: References shared `mclaude-sessions` bucket instead of per-user `mclaude-sessions-{uslug}`
4. GAP [SPEC→FIX] #24: Daemon mode described as active — no mention of CLI command redirecting to mclaude-controller-local
5. GAP [SPEC→FIX] #25: Daemon mode extensively documented with no deprecation notice
6. PARTIAL [SPEC→FIX] #28: No description of BYOH standalone agents spawned by mclaude-controller-local

**spec-control-plane.md (2 gaps)** — Missing ADR-0054/0058 subject scheme and agent registration:
1. GAP [SPEC→FIX] #29: Still uses old provisioning subject scheme
2. GAP [SPEC→FIX] #30: Does not list `mclaude.hosts.{hslug}.api.agents.register` as a subscribed subject

**spec-state-schema.md (2 gaps)** — Legacy data structures still documented:
1. GAP [SPEC→FIX] #6: `mclaude-job-queue` KV bucket still fully documented (eliminated by ADR-0044)
2. GAP [SPEC→FIX] #10: Quota publisher attributed to "daemon" — should be "designated per-project agent"

**spec-nats-payload-schema.md / spec-nats-activity.md (1 unclear):**
1. GAP [UNCLEAR] #5: ADR-0058 says CP publishes to fan-out subjects after HTTP validation; specs show SPA publishing directly. Fundamentally different flow models — unclear which side should change.

**Cross-spec (1 gap):**
1. GAP [SPEC→FIX] #26: No spec documents the migration strategy (in-flight sessions terminated at cut-over)

**Partial (1):**
1. PARTIAL [SPEC→FIX] #18: spec-controller.md doesn't explicitly state zero JetStream for local controller
