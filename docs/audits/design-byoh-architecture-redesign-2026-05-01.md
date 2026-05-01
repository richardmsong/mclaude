## Run: 2026-05-01T00:00:00Z

**Gaps found: 8**

1. **ADR-0058 uses ADR-0035 host subject scheme; ADR-0054 supersedes it** — ADR-0058 describes the host controller subscribing to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` (the ADR-0035 user-prefix scheme) and the target architecture diagram shows host JWT scoped to `mclaude.hosts.{hslug}.>`. However, ADR-0054 (which ADR-0058 declares as a prerequisite) changes the host provisioning subscription to `mclaude.hosts.{hslug}.>` and introduces a **fan-out** model where users publish to `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.{create,delete}`. ADR-0058's open questions (Q2, Q7) discuss the host controller receiving commands but never resolve which subject scheme it will use. The existing `mclaude-controller-local` code already uses ADR-0035's `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` — but ADR-0054 eliminates that subject for hosts. A developer implementing ADR-0058 would not know whether to follow the current code, the target architecture diagram, or ADR-0054's fan-out model.
   - **Doc**: "Host controller uses host JWT (`mclaude.hosts.{hslug}.>`) for control-plane communication" (Target Architecture) vs Q2 option "subscribes to `mclaude.hosts.{hslug}.api.sessions.>`"
   - **Code**: `mclaude-controller-local/controller.go:73` uses `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` — the ADR-0035 scheme, not the ADR-0054 scheme.

2. **Quota system declared TBD with no design direction** — The "Target Architecture" section says "Quota system TBD — requires separate design." Open questions Q3, Q4, and Q5 present multiple options but make no decisions. However, ADR-0044 (scheduling and quota) already designs the quota system with a specific architecture: a designated agent publishes to `mclaude.users.{uslug}.quota` and per-session `QuotaMonitor` goroutines handle enforcement. The code already implements this (`mclaude-session-agent/quota_monitor.go`). ADR-0058 does not acknowledge or reference ADR-0044's existing decisions at all. A developer would be blocked: does ADR-0058 intend to supersede ADR-0044's quota design? Or does ADR-0044's design apply unchanged? The Motivation section explicitly calls out that the quota subject is incompatible with the new permission model, but provides no resolution.
   - **Doc**: "Quota system TBD — requires separate design" + "Q3: Where does quota reporting live?" + "Q4: Who is the authoritative source for quota?" + "Q5: Does quota need cross-host aggregation?"
   - **Code**: `mclaude-session-agent/quota_monitor.go:48` already publishes to `mclaude.users.{uslug}.quota`. ADR-0044 designs the full quota lifecycle. Neither is referenced by ADR-0058.

3. **All 8 open questions are unresolved — no decisions have been made** — The document explicitly states "All questions below are OPEN — no decisions have been made." For a developer, this means the entire "Target Architecture" section is a non-binding sketch. Q1 (subprocess vs goroutine), Q2 (KV vs NATS commands vs hybrid), Q6 (credential fetching strategy), Q7 (multi-user job queue scoping), and Q8 (which component) are all blocking because the implementation depends on their resolution. The document cannot be implemented in its current state.
   - **Doc**: "All questions below are OPEN — no decisions have been made."
   - **Code**: N/A — no implementation decisions to verify.

4. **ADR-0054 already resolves the daemon mode question differently** — ADR-0054's "Deferred" section explicitly defers BYOH daemon mode redesign: "Daemon mode (`--daemon`) requires cross-project JetStream access incompatible with this ADR's per-project agent scoping. Deferred to a dedicated BYOH architecture ADR." ADR-0058 is that dedicated ADR, but ADR-0054 also specifies concrete details about how BYOH controllers and agents should work (fan-out provisioning, `mclaude.hosts.{hslug}.api.agents.register` for agent key registration, HTTP challenge-response for agent auth, host controller has zero JetStream access). ADR-0058's open questions re-open decisions that ADR-0054 already made. For example, Q6 asks "How does the host controller request agent credentials from CP?" and lists the ADR-0054 model as the "expected" answer but then adds open sub-questions. A developer would not know whether ADR-0054's agent credential model is settled or still open.
   - **Doc**: Q6 says "Expected to match K8s model (updated per ADR-0054 unified credential protocol)" then adds "Open sub-questions: Does the controller pre-fetch credentials or fetch on-demand at session start?"
   - **Code**: ADR-0054 specifies: "Agent generates NKey pair at startup, passes public key to controller via local IPC. Host controller registers public key via `mclaude.hosts.{hslug}.api.agents.register`... Agent authenticates itself via HTTP challenge-response to get its JWT. No credential handoff between controller and agent." This is definitive, not open.

5. **Q8 (which component is the host controller?) is already resolved in the codebase** — ADR-0058 lists three options for the host controller component: extend `mclaude-controller-local`, absorb into `mclaude-server`, or create a new binary. The codebase already has `mclaude-controller-local/` with a fully functional controller implementing provisioning, project delete, and process supervision — exactly the role ADR-0058 describes. ADR-0035 (implemented and accepted) created this binary. The question is already answered by the existing architecture. Presenting it as open blocks a developer who would reasonably use the existing `mclaude-controller-local`.
   - **Doc**: "Q8: What component is the host controller? Options: mclaude-controller-local (existing), mclaude-server, new binary"
   - **Code**: `mclaude-controller-local/controller.go` — fully functional BYOH process supervisor with provisioning, delete, and restart-on-crash. `mclaude-controller-local/supervisor.go` — child process supervision. Already used by `mclaude-cli/cmd/daemon.go`.

6. **Job queue KV multi-user scoping question contradicts ADR-0044** — Q7 mentions "job queue KV is currently per-user (`mclaude-job-queue-{uslug}`)" and asks how a host serving multiple users scopes its watches. However, ADR-0044 eliminates the job queue KV entirely: "No separate job queue — scheduled sessions are regular sessions with quota configuration... Eliminates `mclaude-job-queue-{uslug}` KV bucket." ADR-0054 also removes it: "No job queue KV — quota-managed sessions use the session KV with extended fields (ADR-0044)." The spec-state-schema confirms: "mclaude-job-queue: Removed. Quota-managed sessions use session KV with extended fields (ADR-0044)." The question is moot — the resource it asks about no longer exists.
   - **Doc**: Q7 sub-question: "job queue KV is currently per-user (`mclaude-job-queue-{uslug}`). If a host serves multiple users (group/team host), the controller needs to watch multiple users' job queues — how is this scoped?"
   - **Code**: `spec-state-schema.md` confirms job queue KV is removed. ADR-0044 Decisions table: "No separate job queue."

7. **Daemon mode deprecation path declared in-scope but not designed** — The Scope section lists "Daemon mode deprecation path" as in-scope, but the document provides no deprecation plan. There is no description of: (a) what happens to existing daemon mode users, (b) whether daemon mode code is removed or left as dead code, (c) whether there's a migration period, or (d) any fallback behavior. The existing daemon code in `mclaude-session-agent/daemon.go` is ~400 lines with 5 goroutines (`runJWTRefresh`, `runQuotaPublisher`, `runLifecycleSubscriber`, `runJobDispatcher`, `runJobsHTTP`). A developer tasked with the deprecation has no guidance.
   - **Doc**: Scope section: "In scope: ... Daemon mode deprecation path"
   - **Code**: `mclaude-session-agent/daemon.go` (396 lines), `mclaude-session-agent/daemon_jobs.go` — full daemon implementation still in codebase.

8. **Missing dependency on ADR-0044 in Dependencies table** — ADR-0058's Motivation §2 identifies the quota subject as incompatible with the new permission model and declares quota redesign as in-scope. ADR-0044 already designs the quota system (designated agent, per-session QuotaMonitor, elimination of job queue). ADR-0058 should list ADR-0044 as a dependency since its quota decisions directly affect whether ADR-0058 needs to redesign the quota system at all. Without this, a developer cannot determine the relationship between the two ADRs.
   - **Doc**: Dependencies table lists only ADR-0054 and ADR-0035. ADR-0044 is not mentioned anywhere in ADR-0058.
   - **Code**: ADR-0044 is a draft ADR with full quota system design including subject structure, authority model, and aggregation strategy.

---

## Run: 2026-05-01T07:11:07Z

**Gaps found: 2**

All 8 gaps from round 1 have been addressed. Questions are now resolved with explicit decisions, ADR-0044 is properly referenced as a dependency, the quota system integration is explained, the fan-out provisioning model is adopted from ADR-0054, and a three-phase daemon deprecation plan is provided. Two new blocking gaps remain:

1. **"Subprocess per session" contradicts both ADR-0054 and the existing codebase — should be "per project"** — ADR-0058 Q1 states "Decision: Subprocess per session" and says "The host controller `exec`s a new `mclaude-session-agent` process for each session." However, both ADR-0054 and the existing codebase use per-project granularity, not per-session:
   - ADR-0054 Decisions table (line 38): "Session-agent scope: Per-project — Each agent runs per-project and gets a JWT scoped to that project's KV keys only... each new project spawns a new agent with its own JWT at spawn time."
   - ADR-0054 Permission spec (line 374): "Why per-project scoping? Each session-agent manages one project."
   - `mclaude-controller-local/controller.go:27`: `children map[string]*supervisedChild // keyed by projectSlug` — the existing process supervisor keys children by project, not by session.
   - `mclaude-controller-local/supervisor.go:37`: "startChild creates and starts a supervised session-agent subprocess for the given project."
   - The session-agent in "agent mode" (non-daemon) already manages multiple sessions within one project — it watches the project's session KV keys and handles `sessions.create`/`sessions.delete` for all sessions in that project scope.

   A developer implementing ADR-0058 would be confused: should they spawn one process per session (as Q1 states) or one process per project (as ADR-0054 specifies and the existing code does)? Per-session spawning would also break the per-project JWT model — each session within the same project would need its own JWT, but ADR-0054's `agent_credentials` table has a `UNIQUE (user_id, host_slug, project_slug)` constraint, allowing only one agent credential per project. The Target Architecture section also says "Per-session agents spawned as subprocesses with per-project JWTs" — "per-session" agents with "per-project" JWTs is contradictory.
   - **Doc**: Q1 "Decision: Subprocess per session" and "one process = one session" (lines 88, 99, 101)
   - **Code**: ADR-0054 line 38: "Each agent runs per-project"; `controller.go:27`: children keyed by `projectSlug`; `agent_credentials` table: `UNIQUE (user_id, host_slug, project_slug)` — one credential per project, not per session.

2. **Fan-out provisioning attribution inconsistent with ADR-0054** — ADR-0058 Q2 and the Target Architecture section state: "Users publish project create/delete to `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.{create,delete}`." However, ADR-0054's BYOH Host / Platform Controller section (line 469) explicitly clarifies this is **CP-initiated, not SPA-initiated**: "The SPA/CLI continues to use the existing HTTP endpoint `POST /api/users/{uslug}/projects`... CP validates the request... and then **CP itself publishes** `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create` to NATS." The user JWT's `mclaude.hosts.{hslug}.users.{uslug}.>` Pub.Allow entries exist for session commands and host management, not project creation. A developer following ADR-0058's description would implement user-to-NATS direct project creation, which would bypass CP validation and fail NATS permission checks (since the user JWT's host-scoped Pub.Allow is for session commands, not project operations).
   - **Doc**: ADR-0058 line 90: "users publish to `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.{create,delete}`"; line 109: "Users publish project create/delete to `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.{create,delete}`"
   - **Code**: ADR-0054 line 469: "CP-initiated, not SPA-initiated... CP itself publishes `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create` to NATS."

---

## Run: 2026-05-01T07:30:00Z

CLEAN — no blocking gaps found.

**Round 2 fix verification:**

1. ✅ **"per session" → "per project" fix verified.** Q1 now reads "Decision: Subprocess per project." All references throughout the document consistently use per-project terminology. The Target Architecture section says "one process = one project; sessions run within the per-project agent process." The alignment with ADR-0054's `agent_credentials UNIQUE(user_id, host_slug, project_slug)` is explicitly noted. No residual "per session" spawning language remains.

2. ✅ **Fan-out provisioning attribution fix verified.** Q2 now correctly states "CP validates the request... and then **CP itself publishes** `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.{create,delete}` to NATS." The Target Architecture section also correctly says "SPA/CLI sends HTTP request to CP, CP validates and then **CP publishes**." The sentence "Users do not publish directly to host-scoped NATS subjects — project creation remains an HTTP-to-CP-to-NATS flow per ADR-0054" was added, eliminating ambiguity.

**Cross-ADR consistency verified:**
- ADR-0054 references: host subject scheme (`mclaude.hosts.{hslug}.>`), zero-JetStream host controller, per-project agent JWTs, unified HTTP challenge-response credential protocol, fan-out provisioning, agent credential registration via `mclaude.hosts.{hslug}.api.agents.register`, retry backoff params (100ms initial, doubling, max 5s, max 10 attempts) — all consistent.
- ADR-0044 references: designated-agent quota publisher, per-session `QuotaMonitor` goroutines, `mclaude.users.{uslug}.quota` subject, job queue KV elimination — all consistent.
- State schema cross-reference: KV bucket name `mclaude-sessions-{uslug}` and key format `hosts.{hslug}.projects.{pslug}.sessions.>` match ADR-0054's Data Model section.
- Daemon deprecation plan's 5 goroutines match codebase (`daemon.go`, `daemon_jobs.go`).
- Codebase verification: `mclaude-controller-local/controller.go` children keyed by `projectSlug`, `supervisor.go` spawns per-project — consistent with ADR-0058's decisions.
