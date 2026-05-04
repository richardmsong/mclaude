## Run: 2026-04-28T00:00:00Z

**Component:** mclaude-control-plane
**ADR:** docs/adr-0050-fix-session-creation-pipeline.md (Status: accepted)
**Specs:** docs/mclaude-control-plane/spec-control-plane.md, docs/spec-state-schema.md

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| ADR-0050 Decision 1 | Add `Slug`, `UserSlug`, `HostSlug` string fields to `ProjectKVState` Go struct | projects.go:14-20 | IMPLEMENTED | — | All three fields present with correct JSON tags |
| ADR-0050 Decision 1 | `writeProjectKV` signature gains `userSlug, hostSlug string` params | projects.go:135 | IMPLEMENTED | — | Signature: `writeProjectKV(nc, userID, userSlug, hostSlug, proj)` |
| ADR-0050 Decision 1 | All call sites of `writeProjectKV` updated to pass slug fields | projects.go:128, main.go:138, providers.go:418, providers.go:471 | IMPLEMENTED | — | All 4 call sites pass `userSlug` and `hostSlug` |
| ADR-0050 Decision 2 | `ProvisionRequest` gains `UserID` and `ProjectID` string fields alongside existing `UserSlug`, `HostSlug`, `ProjectSlug` | projects.go:26-33 | IMPLEMENTED | — | All five fields present: `UserID`, `UserSlug`, `HostSlug`, `ProjectID`, `ProjectSlug` |
| ADR-0050 Decision 2 | HTTP handler populates all five fields: slug fields from `user.Slug`/`proj.Slug`/`req.HostSlug`, UUID fields from `user.ID`/`proj.ID` | projects.go:96-108 | PARTIAL | SPEC→FIX | Code is a NATS subscriber (not HTTP handler), but correctly populates all five fields. Spec says "HTTP handler" but implementation is NATS subscriber. See broader gap below. |
| ADR-0050 Decision 5 | `IssueSessionAgentJWT` gains `userSlug string` param | nkeys.go:174 | GAP | CODE→FIX | Signature is `IssueSessionAgentJWT(userID string, accountKP nkeys.KeyPair)` — missing `userSlug` param. ADR Impact says "in control-plane copy if exists" and it does exist. |
| ADR-0050 Decision 5 | `SessionAgentSubjectPermissions(userID, userSlug)` returns PubAllow/SubAllow = `[mclaude.{UUID}.>, mclaude.users.{uslug}.hosts.*.>, _INBOX.>, $JS.*.API.>]` | nkeys.go:37-42 | GAP | CODE→FIX | Function takes only `userID`, returns only `[mclaude.{UUID}.>]`. Missing: host-scoped prefix, `_INBOX.>`, `$JS.*.API.>`. |
| ADR-0050 Decision 6 | `seedDev` publishes NATS request to `mclaude.users.{userSlug}.hosts.local.api.projects.create` with full `ProvisionRequest` payload | main.go:152-175 | IMPLEMENTED | — | Uses `nc.Request` with 30s timeout, correct subject pattern, full ProvisionRequest including both UUIDs and slugs |
| ADR-0050 Decision 6 | Uses `nc.Request` with 30s timeout | main.go:162 | IMPLEMENTED | — | `nc.Request(provSubject, provData, 30*time.Second)` |
| ADR-0050 Decision 6 | Logs result | main.go:164-173 | IMPLEMENTED | — | Both success and error paths logged |
| ADR-0050 Decision 6 | Non-fatal on failure (controller may not be running yet during startup race) | main.go:163-174 | IMPLEMENTED | — | Error logged as Warn, execution continues |
| ADR-0050 Decision 7 | `seedDev` queries the `local` host's ID from DB to populate `ProvisionRequest.HostSlug` | main.go:117-120 | IMPLEMENTED | — | Queries `hosts` table for `slug='local'`, uses result for ProvisionRequest and KV writes |
| spec-control-plane:Startup:9 | seedDev writes the `local` host's `mclaude-hosts` KV entry with `online=true` (dev-only path) | main.go:123-143 | IMPLEMENTED | — | Full HostKVState written with online=true |
| spec-control-plane:Startup:9 | After writing KV entries, publishes a NATS provisioning request for the default project | main.go:147-175 | IMPLEMENTED | — | Publishes to `mclaude.users.{uslug}.hosts.local.api.projects.create` for each project |
| spec-control-plane:HTTP | `POST /api/users/{uslug}/projects` — Creates a project on a specified host | (not found) | GAP | SPEC→FIX | No HTTP endpoint exists. Project creation is via NATS subscriber `mclaude.users.*.api.projects.create` in projects.go:64. The spec explicitly states "There is no mclaude.*.api.projects.create subscriber on control-plane any more" but the code has one. The NATS subscriber implements equivalent functionality (DB write, KV write, provisioning request) but the interface doesn't match. Pre-existing gap, not introduced by ADR-0050. |
| spec-control-plane:NATS-Pub | Provisioning request published to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision` | projects.go:109 | GAP | SPEC→FIX | Code publishes to `…api.projects.create`, not `…api.projects.provision`. The state-schema lists both subjects — `.provision` for HTTP-handler path, `.create` for SPA/seedDev path. Since HTTP handler doesn't exist, `.provision` is never published. The `.create` subject is correct for the current NATS-subscriber architecture. Spec should align with code. |
| spec-state-schema:ProjectKVState | Value includes `slug`, `userSlug`, `hostSlug`, `name`, `gitUrl`, `status`, `sessionCount`, `worktrees`, `createdAt`, `lastActiveAt`, `gitIdentityId` | projects.go:7-20 | PARTIAL | CODE→FIX | Go struct has: `ID`, `Slug`, `UserSlug`, `HostSlug`, `Name`, `GitURL`, `Status`, `CreatedAt`, `GitIdentityID`. Missing from code: `sessionCount`, `worktrees`, `lastActiveAt`. Missing from spec: `id` (present in code). |
| spec-state-schema:ProvisionRequest | Payload: `{userID, userSlug, hostSlug, projectID, projectSlug, gitUrl, gitIdentityId}` | projects.go:26-33 | IMPLEMENTED | — | All seven fields present in the struct |
| spec-state-schema:MCProject-CRD | `MCProjectSpec` includes `userSlug` and `projectSlug` (ADR-0050) | N/A (controller-k8s) | — | — | Not in control-plane scope |
| spec-state-schema:session-agent-JWT | PubAllow/SubAllow = `[mclaude.{userID}.>, mclaude.users.{userSlug}.hosts.*.>, _INBOX.>, $JS.*.API.>]` | nkeys.go:37-42 | GAP | CODE→FIX | Same as Decision 5 gap above. Permissions only include `[mclaude.{userID}.>]`. |
| spec-control-plane:NATS-Sub | There is no `mclaude.*.api.projects.create` subscriber on control-plane any more | projects.go:64 | GAP | SPEC→FIX | Code subscribes to `mclaude.users.*.api.projects.create`. The spec says this subscriber should NOT exist. This is a pre-existing architectural gap (project creation via NATS vs HTTP). The NATS subscriber works correctly for the current architecture. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| projects.go:1-5 | INFRA | Package declaration, imports |
| projects.go:7-20 | Phase 1 covered | `ProjectKVState` struct — evaluated above |
| projects.go:26-33 | Phase 1 covered | `ProvisionRequest` struct — evaluated above |
| projects.go:36-42 | INFRA | `ProvisionReply` struct and `ProvisionTimeoutSeconds` constant |
| projects.go:44-130 | Phase 1 covered | `StartProjectsSubscriber` — NATS subscriber and KV bucket creation |
| projects.go:135-162 | Phase 1 covered | `writeProjectKV` — KV write function |
| projects.go:164-192 | INFRA | `ensureProjectsKV`, `ensureJobQueueKV`, `ensureHostsKV`, `ensureSessionsKV` — bucket creation helpers. All match spec startup steps. |
| projects.go:195-201 | INFRA | `replyError` — error response helper |
| main.go:1-11 | INFRA | Package declaration, imports |
| main.go:13-19 | INFRA | Subcommand routing for init-keys and gen-leaf-creds |
| main.go:21-107 | INFRA | Main function: env loading, DB connect, NATS connect, subscriber start, server start. Matches spec startup sequence. |
| main.go:110-177 | Phase 1 covered | `seedDev` function — evaluated above |
| main.go:179-199 | INFRA | `generateNATSUserCreds` — ephemeral control-plane NATS auth |
| main.go:201-209 | INFRA | `loadOrGenerateAccountKey` — account NKey loading |
| main.go:211-217 | INFRA | `envOr` helper |
| nkeys.go:1-29 | Phase 1 covered (UserSubjectPermissions) | User JWT permissions — matches spec |
| nkeys.go:34-42 | Phase 1 covered | `SessionAgentSubjectPermissions` — GAP reported |
| nkeys.go:44-56 | INFRA | `HostSubjectPermissions` — matches spec for per-host JWTs |
| nkeys.go:58-108 | INFRA | NKey pair generation helpers |
| nkeys.go:110-142 | INFRA | `IssueUserJWT` — matches spec |
| nkeys.go:144-166 | INFRA | `IssueHostJWT` — matches spec |
| nkeys.go:168-197 | Phase 1 covered | `IssueSessionAgentJWT` — GAP reported |
| nkeys.go:199-215 | INFRA | `DecodeUserJWT` — JWT validation |
| server.go:* | INFRA | Route registration, admin mux, middleware. Matches spec HTTP endpoint table (except missing project CRUD HTTP routes). |
| auth.go:* | INFRA | Login, refresh, auth middleware. Matches spec Authentication and JWT Signing section. |
| db.go:* | INFRA | DB types, CRUD, schema migration. Matches spec Postgres section. |
| hosts.go:* | INFRA | Host CRUD, device-code flow. Matches spec host endpoints. |
| admin.go:* | INFRA | Admin routes (cluster register, grant, user CRUD). Matches spec admin endpoints. |
| sys_subscriber.go:* | INFRA | $SYS presence tracking. Matches spec NATS Subscribes section (CONNECT/DISCONNECT handling). |
| context.go:* | INFRA | Context key helpers |
| providers.go:* | INFRA | OAuth provider integration. Matches spec OAuth Provider Integration section. |
| tracing.go:* | INFRA | OpenTelemetry tracing |
| metrics.go:* | INFRA | Prometheus metrics |
| version.go:* | INFRA | GET /version handler |
| init_keys.go:* | INFRA | Helm pre-install Job subcommand. Matches spec Operator+account NKeys section. |
| gen_leaf_creds.go:* | INFRA | Helm pre-install Job for leaf creds |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| ADR-0050 D1 | ProjectKVState slug fields | None found | None found | UNTESTED | No test verifies ProjectKVState includes slug fields |
| ADR-0050 D1 | writeProjectKV passes slug fields | None found | None found | UNTESTED | No test for writeProjectKV with slug params |
| ADR-0050 D2 | ProvisionRequest includes UserID/ProjectID | None found | None found | UNTESTED | No test for ProvisionRequest struct contents |
| ADR-0050 D5 | SessionAgentSubjectPermissions returns expanded perms | nkeys_test.go:51 (TestSessionAgentSubjectPermissions) | None found | UNIT_ONLY | Test exists but validates OLD permissions (only UUID prefix). Would need updating when code is fixed. |
| ADR-0050 D5 | IssueSessionAgentJWT accepts userSlug | nkeys_test.go:324 (TestIssueSessionAgentJWT_SubjectScopes) | None found | UNIT_ONLY | Test exists but validates OLD signature without userSlug. Would need updating when code is fixed. |
| ADR-0050 D6 | seedDev publishes provisioning request | None found | None found | UNTESTED | No test for seedDev provisioning flow |
| ADR-0050 D7 | seedDev resolves host slug from DB | None found | None found | UNTESTED | No test for seedDev host slug resolution |
| ADR-0050 Integration | seedDev creates project + KV + provisions | None found | None found | UNTESTED | ADR Integration Test Cases section not present in ADR-0050 |

### Phase 4 — Bug Triage

No `.agent/bugs/` directory found or no open bugs for `mclaude-control-plane`.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No open bugs to triage |

### Summary

- Implemented: 10
- Gap: 4
- Partial: 2
- Infra: 26 (file-level blocks)
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 2
- E2E only: 0
- Untested: 6
- Bugs fixed: 0
- Bugs open: 0

#### Gap/Partial Details

**GAP [CODE→FIX]:** `SessionAgentSubjectPermissions(userID)` — ADR-0050 says function should take `(userID, userSlug)` and return `[mclaude.{UUID}.>, mclaude.users.{uslug}.hosts.*.>, _INBOX.>, $JS.*.API.>]` for both PubAllow and SubAllow. Code returns only `[mclaude.{UUID}.>]`. (nkeys.go:37-42)

**GAP [CODE→FIX]:** `IssueSessionAgentJWT(userID, accountKP)` — ADR-0050 says it should gain `userSlug string` param and pass it to `SessionAgentSubjectPermissions`. Current signature lacks `userSlug`. (nkeys.go:174)

**GAP [SPEC→FIX]:** `POST /api/users/{uslug}/projects` HTTP endpoint does not exist. Project creation is via NATS subscriber `mclaude.users.*.api.projects.create` (projects.go:64). The spec explicitly says "There is no mclaude.*.api.projects.create subscriber on control-plane any more" but the code has one. Pre-existing architectural divergence — the NATS subscriber correctly implements the creation flow. Spec should align with the current NATS-based architecture or the code should migrate to HTTP. (server.go, projects.go:64)

**GAP [SPEC→FIX]:** Provisioning request subject — spec says `…api.projects.provision`, code publishes to `…api.projects.create`. State-schema lists both subjects. Since HTTP handler doesn't exist, `.provision` is never used. The `.create` subject is correct for the current architecture. (projects.go:109)

**PARTIAL [SPEC→FIX]:** Decision 2 says "HTTP handler populates all five fields" — code is a NATS subscriber (not HTTP handler) but correctly populates all five fields. The interface type is wrong per spec, but the data correctness is right. (projects.go:96-108)

**PARTIAL [CODE→FIX]:** `ProjectKVState` value schema — spec includes `sessionCount`, `worktrees`, `lastActiveAt` which are missing from the Go struct. These may be populated elsewhere or are aspirational spec fields. (projects.go:7-20)
