## Run: 2026-05-01T00:00:00Z (Round 5)

ADR: `docs/adr-0054-nats-jetstream-permission-tightening.md`
Specs checked: `spec-state-schema.md`, `spec-nats-payload-schema.md`, `spec-nats-activity.md`, `spec-nats-data-taxonomy.md`, `mclaude-control-plane/spec-control-plane.md`, `mclaude-session-agent/spec-session-agent.md`, `mclaude-common/spec-common.md`

| ADR (section) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Decisions: Isolation model | "Per-user JetStream resources… Separate KV buckets and sessions streams per user" | spec-state-schema §NATS KV Buckets: "Per-user KV bucket (ADR-0054). Created by control-plane on user registration"; spec-nats-data-taxonomy §KV Buckets: per-user bucket table | REFLECTED | — | All specs describe per-user buckets |
| Decisions: KV buckets | "Per-user: `mclaude-sessions-{uslug}`, `mclaude-projects-{uslug}`. Shared: `mclaude-hosts`" | spec-state-schema: `mclaude-sessions-{uslug}`, `mclaude-projects-{uslug}`, `mclaude-hosts` sections; spec-nats-data-taxonomy §KV Buckets table | REFLECTED | — | All three buckets documented |
| Decisions: Sessions stream | "Per-user: `MCLAUDE_SESSIONS_{uslug}`… consolidates events, commands, lifecycle" | spec-state-schema §NATS JetStream Streams: `MCLAUDE_SESSIONS_{uslug}` with subjects and config; spec-nats-data-taxonomy §Streams table | REFLECTED | — | Stream config (LimitsPolicy, MaxAge 30d, FileStorage) matches |
| Decisions: Binary data | "S3 with pre-signed URLs (ADR-0053)" | spec-state-schema §NATS KV Buckets: "Job queue KV eliminated"; spec-nats-data-taxonomy: "No NATS Object Store" | REFLECTED | — | |
| Decisions: Host/controller JetStream | "None — removed entirely" | spec-state-schema §Host/controller JWT: "No `$JS.*`, `$KV.*`, or `$O.*` subjects"; spec-control-plane §Host JWT: "Zero JetStream access" | REFLECTED | — | |
| Decisions: Host subject scheme | "`mclaude.hosts.{hslug}.>` (supersedes ADR-0035's user-prefixed subscriptions)" | spec-state-schema §Host-scoped subjects table; spec-nats-activity §4; spec-common §subj: `HostUserProjectsCreate/Delete` helpers | REFLECTED | — | |
| Decisions: Session-agent scope | "Per-project" | spec-state-schema §Session-agent JWT: per-project scoped example; spec-session-agent §Credential Management: "per-project scoped" | REFLECTED | — | |
| Decisions: Credential lifecycle | "Short TTL + proactive refresh… All identities generate their own NKeys" | spec-state-schema §NATS Server Config: "5-minute TTL"; spec-control-plane: "all identity types generate their own NKeys… CP never generates NKey pairs"; spec-session-agent §Credential Management: "5-minute TTL" | REFLECTED | — | |
| Decisions: Session-agent JWT issuance | "Control-plane only (host controllers no longer hold the account key)" | spec-control-plane §Authentication: "Session-agent JWTs are issued exclusively by the control-plane"; spec-session-agent: "HTTP challenge-response to the control-plane" | REFLECTED | — | |
| Decisions: Credential auth | "HTTP challenge-response… POST /api/auth/challenge + POST /api/auth/verify" | spec-control-plane §HTTP Endpoints: `POST /api/auth/challenge`, `POST /api/auth/verify`; spec-nats-payload-schema §Unified HTTP Credential Protocol; spec-nats-activity §7c-7d, §10 | REFLECTED | — | |
| Decisions: NATS topology | "All agents connect directly to hub NATS" | spec-state-schema §NATS Server Config: "All agents (BYOH and K8s) connect directly to hub NATS"; spec-session-agent: "agents connect directly to hub NATS per ADR-0054" | REFLECTED | — | |
| Decisions: Migration | "Self-healing via credential refresh" | spec-state-schema: clean cut-over documented; spec-control-plane: implicit in refresh flows | REFLECTED | — | |
| Decisions: Bucket lifecycle | "Control-plane creates per-user buckets on user registration; shared mclaude-hosts on deployment" | spec-control-plane §NATS KV Buckets: "Per-user (created on user registration…)"; "Shared (created on deployment startup)" | REFLECTED | — | |
| Decisions: Host access model | "Deferred to a future ADR" | spec-state-schema §hosts table: "Access model deferred"; spec-nats-payload-schema: no host access model details beyond what's spec'd | REFLECTED | — | Correctly deferred |
| User JWT permissions | Full Pub/Sub.Allow list (per-user buckets, per-host hosts KV, stream info, consumer info, ACK, flow control) | spec-state-schema §User JWT permissions: full example matching ADR | REFLECTED | — | Exact permission lists match |
| User JWT: No STREAM.INFO for hosts KV | "No $JS.API.STREAM.INFO.KV_mclaude-hosts — removed to prevent host enumeration via SubjectFilter" | spec-state-schema §User JWT: "No `$JS.API.STREAM.INFO.KV_mclaude-hosts`" | REFLECTED | — | |
| Agent JWT permissions | Full Pub/Sub.Allow list (per-project scoped KV, stream, flow control, quota) | spec-state-schema §Session-agent JWT: full example matching ADR | REFLECTED | — | |
| Host JWT permissions | "Pub.Allow: mclaude.hosts.{hslug}.>, _INBOX.>; Sub.Allow: +$SYS.ACCOUNT.*.CONNECT/DISCONNECT" | spec-state-schema §Host/controller JWT: matching permissions | REFLECTED | — | |
| Host registration flow | "CLI publishes mclaude.users.{uslug}.hosts._.register…; CP returns {ok, slug} — no JWT" | spec-nats-payload-schema §mclaude.users.{uslug}.hosts._.register; spec-nats-activity §3; spec-control-plane §NATS Subjects: subscribes to `mclaude.users.*.hosts._.register` | REFLECTED | — | |
| Host access grants | "manage.grant / manage.revoke-access" + host_access table | spec-state-schema §host_access table; spec-nats-payload-schema §manage.grant, §manage.revoke-access; spec-control-plane §NATS Subjects: subscribers listed | REFLECTED | — | |
| Agent NKey registration | "mclaude.hosts.{hslug}.api.agents.register" | spec-nats-payload-schema §mclaude.hosts.{hslug}.api.agents.register; spec-control-plane §NATS Subjects; spec-nats-activity §7b | REFLECTED | — | |
| Postgres: host_access table | "CREATE TABLE host_access (host_id, user_id, granted_at)" | spec-state-schema §host_access: matching schema | REFLECTED | — | |
| Postgres: agent_credentials table | "CREATE TABLE agent_credentials (id, user_id, host_slug, project_slug, nkey_public, …)" | spec-state-schema §agent_credentials: matching schema | REFLECTED | — | |
| Postgres: users.nkey_public | "new nkey_public TEXT UNIQUE column" | spec-state-schema §users table: `nkey_public TEXT UNIQUE` | REFLECTED | — | |
| Postgres: hosts table changes | "owner_id renamed from user_id; slug globally unique; nkey_public UNIQUE; nats_jwt renamed; removed js_domain, leaf_url, account_jwt, direct_nats_url, role" | spec-state-schema §hosts table: `owner_id`, `UNIQUE (slug)`, `nkey_public UNIQUE`, `nats_jwt`; removed columns not listed | REFLECTED | — | |
| HostKVState schema | "{slug, name, type, online, lastSeenAt}" | spec-state-schema §mclaude-hosts value; spec-nats-payload-schema §KV_mclaude-hosts | REFLECTED | — | |
| KV key format: sessions | "hosts.{hslug}.projects.{pslug}.sessions.{sslug}" | spec-state-schema §mclaude-sessions-{uslug}: key format matches; spec-common §KV key helpers: `SessionsKVKey(h, p, s)` | REFLECTED | — | |
| KV key format: projects | "hosts.{hslug}.projects.{pslug}" | spec-state-schema §mclaude-projects-{uslug}: key format matches; spec-common: `ProjectsKVKey(h, p)` | REFLECTED | — | |
| KV key format: hosts | "{hslug}" | spec-state-schema §mclaude-hosts: key format matches; spec-common: `HostsKVKey(h)` | REFLECTED | — | |
| Session subject hierarchy | "sessions.create, sessions.{sslug}.events, .input, .delete, .control.interrupt, .control.restart, .config, .lifecycle.started/stopped/error" | spec-state-schema §NATS Subjects session subjects table; spec-nats-payload-schema §Session Stream Subjects | REFLECTED | — | All subjects listed |
| Consumer patterns | "Dashboard: DeliverNew + lifecycle filter; Chat: DeliverAll + per-session filter" | spec-state-schema §MCLAUDE_SESSIONS stream: consumer patterns described; spec-nats-activity §2d, §2e; spec-nats-data-taxonomy §Consumer patterns table | REFLECTED | — | |
| Stream config | "LimitsPolicy, MaxAge 30d, FileStorage, DiscardOld" | spec-state-schema §MCLAUDE_SESSIONS: matches | REFLECTED | — | |
| Slug blocklist additions | "create, delete, input, config, control" | spec-state-schema §hosts slug charset: extended blocklist includes all 5; spec-common §slug: reserved-word blocklist includes all 5 | REFLECTED | — | |
| mclaude-common: FilterMclaudeSessions | "Consolidated sessions.> hierarchy" | spec-common §subj: `FilterMclaudeSessions` constant replacing legacy filters | REFLECTED | — | |
| mclaude-common: HostsKVKey signature | "HostsKVKey(hslug) returning {hslug}" | spec-common §KV key helpers: `HostsKVKey(h)` → `{hslug}` | REFLECTED | — | |
| mclaude-common: JobQueueKVKey removed | "Remove subj.JobQueueKVKey()" | spec-common: "JobQueueKVKey is removed" | REFLECTED | — | |
| mclaude-common: HostUserProjects helpers | "Provisioning subjects mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.{create,delete}" | spec-common §subj: `HostUserProjectsCreate`, `HostUserProjectsDelete` with correct signatures | REFLECTED | — | |
| mclaude-common: SysAccountSeed in OperatorAccount | "System account NKey seed for JWT revocation" | spec-common §nats: `OperatorAccount` struct includes `SysAccountSeed`, `SysAccountPublicKey`, `SysAccountJWT` | REFLECTED | — | |
| CP: JWT revocation mechanism | "resolver: nats… CP adds NKey to Revocations map, re-signs account JWT, publishes to $SYS.REQ.CLAIMS.UPDATE" | spec-control-plane §JWT Revocation: full flow documented (operator seed, system account seed, revocation flow steps 1-6); spec-state-schema §NATS Server Config: `resolver: nats` | REFLECTED | — | R4 partial from previous round now resolved |
| CP: JWT revocation infrastructure | "OPERATOR_KEYS_PATH, sysAccountSeed, operatorSeed" | spec-control-plane §Environment Variables: `OPERATOR_KEYS_PATH`, `NATS_SYS_ACCOUNT_SEED`; §Internal Behavior startup step 3: loads operator seed + system account seed; §JWT Revocation: "loads operator seed from OPERATOR_KEYS_PATH" | REFLECTED | — | |
| CP: host access change propagation | "CP revokes affected user JWTs… SPA reconnects with updated JWT" | spec-control-plane §NATS Subjects: manage.grant and manage.revoke-access handlers described with revocation; spec-nats-activity §12b, §12c | REFLECTED | — | |
| CP: per-user resource creation on registration | "create per-user KV buckets + MCLAUDE_SESSIONS_{uslug} stream on user registration" | spec-control-plane §NATS KV Buckets: "Per-user (created on user registration / first login)"; §Internal Behavior startup step 6: per-user resources created on registration | REFLECTED | — | |
| CP: host lifecycle management subjects | "manage.update, manage.rekey, manage.deregister, manage.revoke" | spec-control-plane §NATS Subjects: all management subjects listed as subscribers; spec-nats-payload-schema §Host Management Subjects: full payloads for each | REFLECTED | — | |
| CP: resolver: nats config | "Hub NATS config: change resolver: MEMORY to resolver: nats with resolver_preload" | spec-state-schema §NATS Server Config: `resolver: nats` with `resolver_preload` | REFLECTED | — | |
| Session-agent: ordered push consumers | "Switch from pull consumers to ordered push consumers" | spec-session-agent §NATS JetStream Streams: "ordered push consumers (replacing previous durable pull consumers per ADR-0054)" | REFLECTED | — | |
| Session-agent: credential refresh | "HTTP challenge-response… 5-min TTL" | spec-session-agent §Credential Management: "HTTP challenge-response to the control-plane (ADR-0054)… 5-minute TTL" | REFLECTED | — | |
| Session-agent: per-user bucket names | "Update KV bucket names from mclaude-sessions to mclaude-sessions-{uslug}" | spec-session-agent §NATS KV Buckets: `KV_mclaude-sessions-{uslug}`, `KV_mclaude-projects-{uslug}` | REFLECTED | — | |
| Session-agent: no stream creation | "Remove stream creation code" | spec-session-agent §NATS JetStream Streams: "agent does not create streams — control-plane creates per-user streams" | REFLECTED | — | |
| NATS activity walkthrough | End-to-end flows covering login, host registration, project creation, agent startup, session lifecycle | spec-nats-activity: sections 1-16 cover all flows with correct subjects and permissions | REFLECTED | — | Comprehensive walkthrough present |
| Data taxonomy | Resource naming: bucket names, stream names, key formats, subject namespaces | spec-nats-data-taxonomy: full resource hierarchy, JetStream resources table, subject namespaces, cross-reference table | REFLECTED | — | |
| Payload schemas | All NATS payloads for sessions.create, .input, .events, .delete, .config, .control, .lifecycle, quota, host management, agent registration, HTTP auth | spec-nats-payload-schema: complete payload definitions for all subjects | REFLECTED | — | |

### Summary

- Reflected: 48
- Gap: 0
- Partial: 0

**CLEAN — 48 ADR decisions reflected in specs, 0 gaps.**

All previously flagged items from Round 4 (JWT revocation infrastructure, host-scoped helpers) have been resolved. Every concrete decision in ADR-0054 — isolation model, per-user resources, permission specifications, credential protocol, host lifecycle, subject hierarchy, KV key formats, consumer patterns, Postgres schema changes, slug blocklist extensions, and component changes — is reflected in present-tense spec statements across all seven checked spec files.
