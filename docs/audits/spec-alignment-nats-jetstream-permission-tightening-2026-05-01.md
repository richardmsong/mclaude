## Run: 2026-05-01T00:00:00Z

**ADR**: `docs/adr-0054-nats-jetstream-permission-tightening.md` (status: draft)

**Specs evaluated**:
- `docs/spec-state-schema.md`
- `docs/spec-nats-payload-schema.md`
- `docs/spec-nats-activity.md`
- `docs/spec-nats-data-taxonomy.md`
- `docs/mclaude-control-plane/spec-control-plane.md`
- `docs/mclaude-session-agent/spec-session-agent.md`
- `docs/mclaude-common/spec-common.md`

---

### Phase 1 — ADR → Spec (forward pass)

| # | ADR text | Spec location | Verdict | Direction | Notes |
|---|----------|---------------|---------|-----------|-------|
| 1 | "Per-user: `mclaude-sessions-{uslug}`, `mclaude-projects-{uslug}`" (Decisions: KV buckets) | spec-state-schema.md §NATS KV Buckets: still documents shared `mclaude-sessions` and `mclaude-projects` buckets | GAP | SPEC→FIX | State schema must rename to per-user buckets. spec-nats-payload-schema.md, spec-nats-activity.md, spec-nats-data-taxonomy.md all correctly reference per-user buckets. |
| 2 | "Shared: `mclaude-hosts` … Key format: `{hslug}`" (Decisions: KV buckets) | spec-state-schema.md §mclaude-hosts: key format is `{uslug}.{hslug}`, value includes `role` field | GAP | SPEC→FIX | State schema key format should be `{hslug}` (flat, no user prefix). `role` field removed from value. spec-nats-data-taxonomy.md correctly shows `{hslug}`. |
| 3 | "No job queue KV — quota-managed sessions use the session KV with extended fields (ADR-0044)" (Decisions) | spec-state-schema.md §mclaude-job-queue: still fully documented | GAP | SPEC→FIX | State schema must remove the `mclaude-job-queue` bucket section. spec-nats-payload-schema.md does not reference it. |
| 4 | "Per-user: `MCLAUDE_SESSIONS_{uslug}` (consolidates events, commands, lifecycle)" (Decisions: Sessions stream) | spec-state-schema.md §JetStream Streams: still documents separate MCLAUDE_EVENTS, MCLAUDE_API, MCLAUDE_LIFECYCLE | GAP | SPEC→FIX | State schema must replace three streams with consolidated `MCLAUDE_SESSIONS_{uslug}`. spec-nats-activity.md, spec-nats-data-taxonomy.md, spec-nats-payload-schema.md all correctly reference consolidated stream. |
| 5 | "Host/controller JetStream: None — removed entirely" (Decisions) | spec-state-schema.md §Per-host user JWT: `$JS.*.API.>` in pub/sub allows; §Per-cluster leaf JWT: same | GAP | SPEC→FIX | State schema JWT permissions must remove all `$JS.*` from host/controller JWTs. |
| 6 | "Host subject scheme: `mclaude.hosts.{hslug}.>` (supersedes ADR-0035)" (Decisions) | spec-state-schema.md §NATS Subjects: controllers subscribe to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` or `mclaude.users.*.hosts.{cslug}.api.projects.>` | GAP | SPEC→FIX | State schema must update controller subscriptions from user-scoped to host-scoped `mclaude.hosts.{hslug}.>`. Fan-out subjects must change. spec-nats-activity.md correctly uses `mclaude.hosts.{hslug}.>`. |
| 7 | "Session-agent scope: Per-project" (Decisions) | spec-session-agent.md: agent permissions still reference `mclaude.users.{userSlug}.hosts.*.>` (broad) | GAP | SPEC→FIX | Session-agent spec must describe per-project scoped JWT with subjects limited to one host and one project. |
| 8 | "Credential lifecycle: Short TTL + proactive refresh… All identities generate their own NKeys — CP never handles private key material" (Decisions) | spec-control-plane.md §Authentication: CP generates NKey pair at login, returns seed to SPA | GAP | SPEC→FIX | CP spec must be updated: SPA generates its own NKey; CP only receives public key and returns JWT (no seed). |
| 9 | "Session-agent JWT issuance: Control-plane only" (Decisions) | spec-control-plane.md: no mention of agent JWT issuance by CP; spec-session-agent.md §Credential Management: reads credentials from `user-secrets` K8s Secret | GAP | SPEC→FIX | CP spec must document agent credential issuance via HTTP challenge-response. Session-agent spec must update credential model. |
| 10 | "Credential auth: HTTP challenge-response — `POST /api/auth/challenge` + `POST /api/auth/verify`" (Decisions) | spec-control-plane.md §HTTP Endpoints: no `/api/auth/challenge` or `/api/auth/verify` endpoints listed | GAP | SPEC→FIX | CP spec must add the unified HTTP auth endpoints. spec-nats-payload-schema.md correctly documents them. |
| 11 | "NATS topology: All agents connect directly to hub NATS" (Decisions) | spec-state-schema.md §Worker NATS: documents leaf-node topology with `leafnodes.remotes` | GAP | SPEC→FIX | State schema must note leaf-node topology is removed from scope; agents connect directly to hub. |
| 12 | "Binary data: S3 with pre-signed URLs (ADR-0053)" (Decisions) | spec-nats-payload-schema.md, spec-nats-activity.md, spec-nats-data-taxonomy.md: all correctly reference S3, no Object Store | REFLECTED | — | |
| 13 | "Bucket lifecycle: CP creates per-user buckets on user registration" (Decisions) | spec-control-plane.md §NATS KV Buckets: creates shared buckets on startup, no per-user bucket creation | GAP | SPEC→FIX | CP spec must describe per-user bucket + stream creation on user registration. |
| 14 | "KV key changes — Sessions: `hosts.{hslug}.projects.{pslug}.sessions.{sslug}`" (Data Model) | spec-state-schema.md §mclaude-sessions: key format `{uslug}.{hslug}.{pslug}.{sslug}` | GAP | SPEC→FIX | State schema must adopt hierarchical key format with literal type-tokens. spec-nats-data-taxonomy.md correctly uses new format. |
| 15 | "KV key changes — Projects: `hosts.{hslug}.projects.{pslug}`" (Data Model) | spec-state-schema.md §mclaude-projects: key format `{userId}.{projectId}` | GAP | SPEC→FIX | State schema must adopt hierarchical key format. |
| 16 | "Session subject hierarchy: `sessions.create`, `sessions.{sslug}.events`, `sessions.{sslug}.input`, etc." (Data Model) | spec-state-schema.md §NATS Subjects: still documents `api.sessions.{create,input,delete,...}`, `events.{sslug}`, `lifecycle.{sslug}` | GAP | SPEC→FIX | State schema must update all session subjects to consolidated `sessions.>` hierarchy. spec-nats-payload-schema.md and spec-nats-activity.md correctly use new hierarchy. |
| 17 | "Postgres: `host_access` table with (host_id, user_id) composite PK" (Data Model) | spec-state-schema.md §Postgres: no `host_access` table | GAP | SPEC→FIX | State schema must add the `host_access` table. |
| 18 | "Postgres: `agent_credentials` table" (Data Model) | spec-state-schema.md §Postgres: no `agent_credentials` table | GAP | SPEC→FIX | State schema must add the `agent_credentials` table. |
| 19 | "Postgres: `users` table new `nkey_public TEXT UNIQUE` column" (Data Model) | spec-state-schema.md §users: no `nkey_public` column | GAP | SPEC→FIX | State schema must add `nkey_public` column to users table. |
| 20 | "Postgres: `hosts` table — `user_id` renamed to `owner_id`, `role` removed, `js_domain`/`leaf_url`/`account_jwt`/`direct_nats_url` removed, `user_jwt` renamed to `nats_jwt`, UNIQUE constraint changed from `(user_id, slug)` to `(slug)`" (Data Model) | spec-state-schema.md §hosts: still has `user_id`, `role`, `js_domain`, `leaf_url`, `account_jwt`, `direct_nats_url`, `user_jwt`, UNIQUE `(user_id, slug)` | GAP | SPEC→FIX | State schema hosts table must be rewritten per ADR-0054. |
| 21 | "HostKVState JSON: `{slug, name, type, online, lastSeenAt}` — no `user_id`, no `role`" (Data Model) | spec-state-schema.md §mclaude-hosts value: includes `role` field | GAP | SPEC→FIX | State schema must remove `role` from host KV value. spec-nats-payload-schema.md correctly shows no `role`. |
| 22 | "Consumer patterns: Dashboard (DeliverNew, lifecycle filter) and Chat view (DeliverAll, per-session)" (Data Model) | spec-nats-activity.md §2d, §2e: correctly documents both consumer patterns | REFLECTED | — | Also reflected in spec-nats-data-taxonomy.md §Streams. |
| 23 | "Reserved word additions: `create`, `delete`, `input`, `config`, `control`" (Data Model, Session subject hierarchy) | spec-common.md §slug Validate: blocklist is `users, hosts, projects, sessions, clusters, api, events, lifecycle, quota, terminal` — missing new words | GAP | SPEC→FIX | Common spec must add `create`, `delete`, `input`, `config`, `control` to the reserved-word blocklist. |
| 24 | "Session-agent: Switch from pull consumers to ordered push consumers" (Component Changes) | spec-session-agent.md §NATS JetStream Streams: documents pull consumers (`sa-cmd-{uslug}-{pslug}`, `sa-ctl-{uslug}-{pslug}`) | GAP | SPEC→FIX | Session-agent spec must replace pull consumers with ordered push consumers. |
| 25 | "Session-agent: Remove stream creation code (CreateOrUpdateStream)" (Component Changes) | spec-session-agent.md §NATS JetStream Streams: "agent idempotently creates or updates two JetStream streams on startup" | GAP | SPEC→FIX | Session-agent spec must state the agent no longer creates streams (CP creates them). |
| 26 | "Session-agent: Update KV bucket names to `mclaude-sessions-{uslug}`" (Component Changes) | spec-session-agent.md §NATS KV Buckets: references shared `mclaude-sessions` | GAP | SPEC→FIX | Session-agent spec must reference per-user buckets. |
| 27 | "Session-agent: New credential refresh loop — HTTP challenge-response to CP" (Component Changes) | spec-session-agent.md §Daemon: JWT Refresh: POSTs to refresh URL with current JWT (old mechanism) | GAP | SPEC→FIX | Session-agent spec must describe HTTP challenge-response refresh for all modes (not just daemon). |
| 28 | "mclaude-common: Update subject constants to consolidated `sessions.>` hierarchy" (Component Changes) | spec-common.md §subj: `FilterMclaudeAPI`, `FilterMclaudeEvents`, `FilterMclaudeLifecycle` still reference `api.sessions.>`, `events.*`, `lifecycle.*` | GAP | SPEC→FIX | Common spec must update filter constants to `sessions.>` hierarchy. |
| 29 | "mclaude-common: Update `ProjectsKVKey()` to `hosts.{hslug}.projects.{pslug}`" (Component Changes) | spec-common.md §subj KV key helpers: `ProjectsKVKey(u, h, p)` returns `{uslug}.{hslug}.{pslug}` | GAP | SPEC→FIX | Common spec must update key format to `hosts.{hslug}.projects.{pslug}` (literal type-tokens, no user prefix in key). |
| 30 | "mclaude-common: Update `SessionsKVKey()` to `hosts.{hslug}.projects.{pslug}.sessions.{sslug}`" (Component Changes) | spec-common.md §subj KV key helpers: `SessionsKVKey(u, h, p, s)` returns `{uslug}.{hslug}.{pslug}.{sslug}` | GAP | SPEC→FIX | Common spec must update key format with literal type-tokens. |
| 31 | "mclaude-common: Update `HostsKVKey()` — drop user slug prefix, new signature `HostsKVKey(hslug)`" (Component Changes) | spec-common.md §subj KV key helpers: `HostsKVKey(u, h)` returns `{uslug}.{hslug}` | GAP | SPEC→FIX | Common spec must change signature to `HostsKVKey(h)` returning `{hslug}`. |
| 32 | "mclaude-common: Remove `JobQueueKVKey()`" (Component Changes) | spec-common.md §subj KV key helpers: `JobQueueKVKey(u, jobId)` still listed | GAP | SPEC→FIX | Common spec must remove `JobQueueKVKey`. |
| 33 | "Full User JWT permission specification" (Full Permission Specs) | spec-state-schema.md §Per-host user JWT: `$JS.API.>`, `$JS.*.API.>` wildcards, `mclaude.{userID}.>` backward compat | GAP | SPEC→FIX | State schema must replace broad wildcards with the explicit per-user, per-bucket, per-stream allow-lists from ADR-0054. |
| 34 | "Full Session-Agent JWT permission specification" (Full Permission Specs) | spec-state-schema.md §Per-session-agent JWT: `$JS.API.>`, `$JS.*.API.>`, `$KV.mclaude-sessions.>` etc. | GAP | SPEC→FIX | State schema must replace with per-project scoped permissions from ADR-0054. |
| 35 | "Full Host JWT permission specification: `mclaude.hosts.{hslug}.>` only, zero JetStream" (Full Permission Specs) | spec-state-schema.md §Per-host user JWT: includes `$JS.*.API.>`, `$SYS.ACCOUNT.*.CONNECT/DISCONNECT` in Pub | GAP | SPEC→FIX | State schema must strip all `$JS.*` and move `$SYS.*` from Pub to Sub.Allow (M3 fix). |
| 36 | "Host registration: NATS-based with `mclaude.users.{uslug}.hosts._.register`, no JWT in response" (Host Lifecycle) | spec-control-plane.md §HTTP Endpoints: host registration via device-code HTTP flow, JWT in response | GAP | SPEC→FIX | CP spec must update host registration to NATS-based with NKey attestation model (no JWT in response; host self-authenticates via HTTP). |
| 37 | "Host access grants: `manage.grant`, `manage.revoke-access` via NATS" (Host Lifecycle) | spec-control-plane.md: no mention of grant/revoke-access handlers | GAP | SPEC→FIX | CP spec must add NATS handlers for host access management. spec-nats-payload-schema.md correctly documents these. |
| 38 | "Host deregistration, emergency revocation, rekey flows" (Host Lifecycle) | spec-control-plane.md: no mention of deregister/revoke/rekey handlers | GAP | SPEC→FIX | CP spec must add host lifecycle management handlers. spec-nats-payload-schema.md correctly documents these. |
| 39 | "CP subscribes to `mclaude.hosts.*.users.*.>` for fan-out" (Component Changes) | spec-control-plane.md §NATS Subjects (Subscribes): only lists `$SYS` subjects, `mclaude.users.*.api.projects.create` | GAP | SPEC→FIX | CP spec must add subscription to `mclaude.hosts.*.users.*.>` for fan-out project operations. |
| 40 | "NATS resolver: Switch from `resolver: MEMORY` to `resolver: nats` (full resolver)" (JWT Revocation Mechanism) | spec-state-schema.md §Hub NATS: `resolver: MEMORY` | GAP | SPEC→FIX | State schema must update hub NATS config to `resolver: nats`. |
| 41 | "Per-user sessions stream config: LimitsPolicy, MaxAge 30d, FileStorage" (Data Model) | spec-nats-activity.md §JetStream Resources: references `MCLAUDE_SESSIONS_{uslug}` with correct config | REFLECTED | — | Also reflected in spec-nats-data-taxonomy.md. |
| 42 | "Project fan-out: SPA publishes to `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create/delete`, both CP and host controller receive" (Component Changes, Host) | spec-nats-payload-schema.md §Project Subjects: correctly documents fan-out subjects | REFLECTED | — | spec-nats-activity.md §6 also correctly shows this. |
| 43 | "Agent public key registration: `mclaude.hosts.{hslug}.api.agents.register`" (Component Changes) | spec-nats-payload-schema.md §Agent Public Key Registration: correctly documented | REFLECTED | — | |
| 44 | "Session-agent: NATS subjects scoped to one project `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.>`" (Full Permission Specs) | spec-session-agent.md §NATS Subjects (Publish): uses `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.…` (all projects via host wildcard in actual JWT) | PARTIAL | SPEC→FIX | Session-agent spec describes subjects but doesn't document per-project scoping (one host, one project in JWT). Currently implies access to all projects via `hosts.*.>`. |
| 45 | "mclaude-common: Add `create`, `delete`, `input`, `config`, `control` to reserved-word blocklist" (Component Changes) | spec-common.md §slug: blocklist does not include these | GAP | SPEC→FIX | Same as #23 — common spec must update blocklist. |
| 46 | "Session-agent: Removed job queue KV" (Component Changes, deferred daemon) | spec-session-agent.md §NATS KV Buckets: still references `mclaude-job-queue` | GAP | SPEC→FIX | Session-agent spec must remove `mclaude-job-queue` references. |
| 47 | "Hosts are globally unique by slug. UNIQUE constraint on `(slug)` not `(user_id, slug)`" (Data Model) | spec-state-schema.md §hosts: `UNIQUE (user_id, slug)` | GAP | SPEC→FIX | Same as #20 — hosts table uniqueness must be `(slug)` globally. |
| 48 | "Flow control subjects: `$JS.FC.<stream>.>` in both Pub.Allow and Sub.Allow" (Full Permission Specs) | spec-state-schema.md §Per-session-agent JWT: no `$JS.FC.*` subjects | GAP | SPEC→FIX | State schema must include flow control subjects in JWT permissions. |
| 49 | "User JWT: No `$JS.API.STREAM.INFO.KV_mclaude-hosts` (removed to prevent host enumeration)" (Full Permission Specs) | spec-nats-activity.md §2c: correctly documents no STREAM.INFO for hosts | REFLECTED | — | |
| 50 | "Migration: Self-healing via credential refresh; clean cut-over" (Decisions) | No spec needs to capture migration — it's a one-time deployment action | REFLECTED | — | Not a spec concern. |
| 51 | "mclaude-control-plane: New `mclaude.hosts.{hslug}.api.agents.register` NATS subscriber" (Component Changes) | spec-control-plane.md §NATS Subjects: no mention of agents.register | GAP | SPEC→FIX | CP spec must add agent registration subscriber. |
| 52 | "Session state enum: `pending, running, paused, requires_action, completed, stopped, cancelled, needs_spec_fix, failed, error`" (spec-nats-payload-schema) | spec-nats-payload-schema.md §KV_mclaude-sessions: correctly lists full status enum | REFLECTED | — | |
| 53 | "Cluster-shared field duplication removed (no more per-user rows for clusters)" (Data Model, hosts table) | spec-state-schema.md §Cluster-shared field duplication: still documents the duplication pattern | GAP | SPEC→FIX | State schema must remove cluster-shared field duplication section. |
| 54 | "spec-control-plane.md §NATS KV Buckets still references `mclaude-job-queue`" | spec-control-plane.md §NATS KV Buckets: lists `mclaude-job-queue` | GAP | SPEC→FIX | CP spec must remove `mclaude-job-queue` bucket creation. |
| 55 | "spec-control-plane.md provisioning subjects use old scheme" | spec-control-plane.md §NATS Subjects (Publishes): `mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision` | GAP | SPEC→FIX | CP spec must update provisioning to fan-out scheme: CP publishes to `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create`. |
| 56 | "Login response: no nkeySeed returned — SPA generates its own NKey" (Credential auth) | spec-state-schema.md §Login Response: returns `nkeySeed` | GAP | SPEC→FIX | State schema login response must remove `nkeySeed` — SPA generates its own NKey, sends public key at login, receives only JWT. |
| 57 | "spec-state-schema.md §NATS Subjects still uses old subject patterns (`api.sessions.*`, `events.*`, `lifecycle.*`)" | spec-state-schema.md §NATS Subjects: all session subjects use old `api.sessions.*` / `events.*` / `lifecycle.*` patterns | GAP | SPEC→FIX | State schema must update all session NATS subjects to new `sessions.>` hierarchy. |

---

### Phase 2 — Summary

- **Reflected**: 10
- **Gap**: 44
- **Partial**: 1

**Verdict: NOT CLEAN** — 44 GAP + 1 PARTIAL findings.

The three cross-cutting specs (`spec-nats-payload-schema.md`, `spec-nats-activity.md`, `spec-nats-data-taxonomy.md`) have been comprehensively updated to reflect ADR-0054.

The following four specs have **not** been updated and contain the vast majority of gaps:

1. **`spec-state-schema.md`** (23 gaps) — Still documents the pre-ADR-0054 architecture: shared KV buckets, old key formats, old host table schema, three separate JetStream streams, old JWT permission model, old NATS server config, old login response.

2. **`spec-control-plane.md`** (10 gaps) — Missing HTTP auth endpoints, agent registration subscriber, host lifecycle management handlers, fan-out subscription, per-user bucket creation, updated provisioning subjects. Still documents old JWT issuance model.

3. **`spec-session-agent.md`** (6 gaps) — Still documents old JetStream streams, pull consumers, shared buckets, old credential model, job queue references.

4. **`spec-common.md`** (6 gaps) — Old KV key formats, old filter constants, missing reserved words, stale `JobQueueKVKey`.

All gaps have direction `SPEC→FIX` — the ADR decisions are correct and the specs should be updated to reflect them.
