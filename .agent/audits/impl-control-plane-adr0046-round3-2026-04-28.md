# Implementation Audit: mclaude-control-plane (ADR-0046)

**Component**: mclaude-control-plane  
**Component root**: `mclaude-control-plane/`  
**ADR evaluated**: `docs/adr-0046-fix-host-liveness-kv-writes.md` (status: accepted)  
**Specs evaluated**: `docs/spec-state-schema.md`, `docs/mclaude-control-plane/spec-control-plane.md`  

---

## Run: 2026-04-28T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-state-schema.md users table | `slug TEXT UNIQUE NOT NULL` column on `users` | db.go:schema — `ALTER TABLE users ADD COLUMN IF NOT EXISTS slug TEXT NOT NULL DEFAULT ''; ... CREATE UNIQUE INDEX IF NOT EXISTS users_slug_uniq ON users (slug);` | IMPLEMENTED | — | Uniqueness enforced via separate index; functionally equivalent to inline UNIQUE constraint |
| spec-state-schema.md users table | slug Derivation: `{slugify(name or email_local_part)}-{domain_first_segment}` | db.go:computeUserSlug (local-part only); schema SQL backfill `split_part(email, '@', 1)` | PARTIAL | SPEC→FIX | ADR-0046 decision explicitly uses email local-part only ("consistent with ADR-0024's slug charset") — no domain-first segment appended. Code implements `alice` for `alice@example.com`; spec says it should be `alice-example`. The spec example `"slug": "alice-gmail"` suggests the full format, but ADR-0046 overrides with local-part-only. Spec should be updated to match the ADR decision. |
| spec-state-schema.md users table | slug: `Immutable after creation; email changes do not rewrite the slug` | No explicit immutable-guard in code | PARTIAL | SPEC→FIX | No `UPDATE users SET slug = ...` path on email-change exists (no email-change endpoint), so immutability holds implicitly; no enforcement code needed. Spec is correct; code meets intent. |
| spec-state-schema.md `mclaude-hosts` bucket | `Created by: control-plane (ensureHostsKV)` | projects.go:ensureHostsKV + StartProjectsSubscriber call | IMPLEMENTED | — | |
| spec-state-schema.md `mclaude-hosts` bucket | Key format: `{uslug}.{hslug}` | sys_subscriber.go: `fmt.Sprintf("%s.%s", uslug, hslug)` in both machine and cluster paths | IMPLEMENTED | — | |
| spec-state-schema.md `mclaude-hosts` bucket | Value schema: `{slug, type, name, role, online, lastSeenAt}` | sys_subscriber.go: HostKVState struct in sys_subscriber.go | IMPLEMENTED | — | |
| spec-state-schema.md `mclaude-hosts` Writers | `$SYS.ACCOUNT.*.CONNECT` writes full value object with `online=true`, `lastSeenAt=now` | sys_subscriber.go: handleSysEvent machine CONNECT path | IMPLEMENTED | — | |
| spec-state-schema.md `mclaude-hosts` Writers | `$SYS.ACCOUNT.*.DISCONNECT` writes only `{online: false}` — read-modify-write, preserves other fields | sys_subscriber.go: handleSysEvent machine DISCONNECT path (read-modify-write; state.Online = false only) | IMPLEMENTED | — | |
| spec-state-schema.md `mclaude-hosts` Writers | `lastSeenAt` not rewritten on disconnect | sys_subscriber.go: DISCONNECT path does not set LastSeenAt | IMPLEMENTED | — | |
| spec-state-schema.md `mclaude-hosts` Writers | Dev seed write: `DEV_SEED=true` startup writes bootstrap user's `local` machine host with `online=true` | main.go:seedDev — queries `local` host, writes HostKVState with Online=true to `{user.Slug}.local` | IMPLEMENTED | — | |
| spec-state-schema.md `mclaude-hosts` | History: 1 | projects.go:ensureHostsKV — `History: 1` (implicit default, but bucket config has no explicit History field; NATS KV default is 1) | IMPLEMENTED | — | Default KV History in nats.go is 1; ensureProjectsKV explicitly sets it; ensureHostsKV relies on default |
| spec-state-schema.md `mclaude-sessions` | History: 64 | projects.go:ensureSessionsKV — `History: 64` | IMPLEMENTED | — | |
| spec-state-schema.md `mclaude-sessions` | `Created by: control-plane (ensureSessionsKV)` | projects.go:StartProjectsSubscriber calls ensureSessionsKV | IMPLEMENTED | — | |
| spec-state-schema.md Login Response Shape | Response fields: `user: {id, slug}`, `jwt`, `nkeySeed`, `hubUrl`, `hosts[]`, `projects[]` | auth.go:LoginResponse struct — flat `userId`, `userSlug`, `jwt`, `nkeySeed`, `natsUrl`; NO `hosts[]` or `projects[]` | GAP | UNCLEAR | `hosts` and `projects` arrays absent from login response; code also uses `natsUrl` not `hubUrl`; flat `userId`/`userSlug` vs nested `user` object. This may be a pre-existing gap predating ADR-0046. The SPA may fetch hosts+projects separately. Direction depends on whether the SPA actually needs them at login time. |
| spec-control-plane.md Startup Step 6 | Subscribes to `$SYS.ACCOUNT.*.CONNECT/DISCONNECT` for host liveness | main.go — StartSysSubscriber called after StartProjectsSubscriber | IMPLEMENTED | — | |
| spec-control-plane.md Startup Step 7 | Ensures KV buckets exist (`mclaude-projects`, `mclaude-hosts`, `mclaude-sessions`, `mclaude-job-queue`) | projects.go:StartProjectsSubscriber creates all four buckets | IMPLEMENTED | — | |
| spec-control-plane.md Startup Step 6/7 ordering | Spec lists $SYS subscribe (step 6) before KV ensure (step 7) | main.go: StartProjectsSubscriber (KV) called BEFORE StartSysSubscriber | PARTIAL | SPEC→FIX | ADR-0046 decision explicitly states KV must be created first so `hostsKV` is set before $SYS events fire. Spec step numbering should be swapped: KV ensure (now step 6) before $SYS subscribe (now step 7). |
| spec-control-plane.md Startup Step 9 | `DEV_SEED=true`: writes the `local` host's `mclaude-hosts` KV entry with `online=true` | main.go:seedDev — KV write for local host with online=true | IMPLEMENTED | — | |
| spec-control-plane.md $SYS Subjects | CONNECT Client: SELECT machine host by public_key, update last_seen_at, upsert KV online=true | sys_subscriber.go:handleSysEvent Client/isConnect path | IMPLEMENTED | — | |
| spec-control-plane.md $SYS Subjects | CONNECT Client: no match → ignore | sys_subscriber.go: qerr check on QueryRow; if err, no write | IMPLEMENTED | — | |
| spec-control-plane.md $SYS Subjects | CONNECT Leafnode: SELECT cluster host, update last_seen_at ALL rows with matching slug, upsert KV for each user row | sys_subscriber.go:handleSysEvent Leafnode/isConnect path | IMPLEMENTED | — | |
| spec-control-plane.md $SYS Subjects | DISCONNECT: same lookup, sets KV online=false, does NOT rewrite last_seen_at | sys_subscriber.go:DISCONNECT paths for both Client and Leafnode | IMPLEMENTED | — | |
| spec-control-plane.md NATS KV Buckets | `mclaude-hosts` — created by `ensureHostsKV`; control-plane is sole writer | projects.go + sys_subscriber.go + main.go (seedDev) | IMPLEMENTED | — | |
| spec-control-plane.md NATS KV Buckets | `mclaude-sessions` — created by `ensureSessionsKV`; bucket creation only (session-agent writes) | projects.go:ensureSessionsKV called; no writes to mclaude-sessions in control-plane | IMPLEMENTED | — | |
| spec-control-plane.md Authentication | `IssueUserJWT` keeps `claims.Name = userID` (UUID) | nkeys.go:IssueUserJWT — `claims.Name = userID` | IMPLEMENTED | — | |
| spec-control-plane.md Authentication | `LoginResponse` gains `UserSlug` from `user.Slug` | auth.go:LoginResponse.UserSlug field; handleLogin populates `UserSlug: user.Slug` | IMPLEMENTED | — | |
| spec-control-plane.md Authentication | `handleRefresh` looks up user by UUID (from `claims.Name`) and returns `UserSlug: user.Slug` | auth.go:handleRefresh — `db.GetUserByID(r.Context(), claims.Name)` then `UserSlug: user.Slug` | IMPLEMENTED | — | |
| spec-control-plane.md Authentication | Full Login Response: `{user, jwt, nkeySeed, hubUrl, hosts[], projects[]}` | auth.go:LoginResponse lacks hosts[], projects[], nested user{}, uses natsUrl not hubUrl | GAP | UNCLEAR | Same as spec-state-schema.md Login Response gap above |
| spec-control-plane.md readyz endpoint | Readiness probe checks Postgres connectivity only | server.go:handleReadyz — returns 200 OK unconditionally, no Postgres check | GAP | CODE→FIX | Spec says readyz must verify Postgres. Current code returns 200 always, meaning Postgres failures won't take pod out of Kubernetes endpoints. Pre-existing gap, not ADR-0046-specific. |
| ADR-0046 Decisions | `users.slug` migration: `ALTER TABLE users ADD COLUMN IF NOT EXISTS slug TEXT UNIQUE NOT NULL DEFAULT ''` + backfill | db.go:schema — column add + UPDATE backfill + separate unique index | IMPLEMENTED | — | Uses separate CREATE UNIQUE INDEX rather than inline UNIQUE; functionally identical |
| ADR-0046 Decisions | `ensureHostsKV` placement in `StartProjectsSubscriber`; `Server.hostsKV` field | projects.go + server struct in auth.go | IMPLEMENTED | — | |
| ADR-0046 Decisions | Dev seed: write `{uslug}.local` KV with all fields | main.go:seedDev — constructs HostKVState with all fields, puts `user.Slug + ".local"` | IMPLEMENTED | — | |
| ADR-0046 Decisions | `ensureSessionsKV` with History=64 | projects.go:ensureSessionsKV — `History: 64` | IMPLEMENTED | — | |

---

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| db.go:1-15 (imports) | INFRA | Standard imports |
| db.go:computeUserSlug | INFRA | Helper for slug derivation, used by CreateUser and spec'd by ADR-0046 |
| db.go:User struct | INFRA | Row type for spec'd users table |
| db.go:Host struct | INFRA | Row type for spec'd hosts table |
| db.go:ConnectDB, Close, Migrate | INFRA | DB lifecycle — spec'd under "Manages Postgres tables" |
| db.go:GetUserByEmail, GetUserByID | INFRA | Spec'd readers |
| db.go:CreateUser | INFRA | Spec'd writer |
| db.go:SetUserAdmin, DeleteUser | INFRA | Spec'd writers |
| db.go:Project, CreateProject, CreateProjectWithIdentity, GetProjectsByUser, GetProjectByID, UpdateProjectGitIdentity | INFRA | Spec'd under Projects section |
| db.go:OAuthConnection, CreateOAuthConnection, GetOAuthConnectionByID, GetOAuthConnectionsByUser, DeleteOAuthConnection, GetExpiringGitLabConnections, UpdateTokenExpiry | INFRA | Spec'd OAuth integration |
| db.go:schema const | INFRA | DDL spec'd by state schema |
| sys_subscriber.go:HostKVState | INFRA | Spec'd value type for mclaude-hosts KV |
| sys_subscriber.go:sysConnectEvent | INFRA | Parse helper for $SYS event payload |
| sys_subscriber.go:StartSysSubscriber | INFRA | Spec'd startup behavior |
| sys_subscriber.go:handleSysEvent | INFRA | Spec'd $SYS CONNECT/DISCONNECT behavior |
| projects.go:ProjectKVState, ProvisionRequest, ProvisionReply | INFRA | Spec'd KV/NATS types |
| projects.go:StartProjectsSubscriber | INFRA | KV bucket creation + project subscriber — spec'd |
| projects.go:writeProjectKV, ensureProjectsKV, ensureJobQueueKV, ensureHostsKV, ensureSessionsKV | INFRA | Spec'd KV helpers |
| projects.go:replyError | INFRA | Error reply helper for NATS messages |
| auth.go:LoginRequest, LoginResponse | INFRA | Spec'd auth types |
| auth.go:Server struct + NewServer + SetNATSConn | INFRA | Spec'd server type |
| auth.go:handleLogin, handleRefresh, handleMe | INFRA | Spec'd endpoints |
| auth.go:authMiddleware, bearerToken, checkPassword, HashPassword, bcryptCheck | INFRA | Spec'd auth machinery |
| main.go:main | INFRA | Startup sequence spec'd in spec-control-plane.md |
| main.go:seedDev | INFRA | Spec'd under DEV_SEED startup behavior |
| main.go:generateNATSUserCreds, loadOrGenerateAccountKey, envOr | INFRA | Startup plumbing for NATS credentials and env config |
| nkeys.go:UserSubjectPermissions, SessionAgentSubjectPermissions, HostSubjectPermissions | INFRA | Spec'd JWT permission scopes |
| nkeys.go:GenerateOperatorNKey, GenerateAccountNKey, GenerateUserNKey | INFRA | Spec'd NKey generation helpers |
| nkeys.go:IssueUserJWT, IssueHostJWT, IssueSessionAgentJWT, DecodeUserJWT | INFRA | Spec'd JWT issuance |
| server.go:RegisterRoutes, AdminMux, adminAuthMiddleware, handleMetrics | INFRA | Spec'd route registration and admin port |

No UNSPEC'd or DEAD code blocks found in ADR-0046-relevant files.

---

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| users.slug column added + backfill | slug column added with backfill | nkeys_test.go (indirect) | integration_test.go:TestIntegration_UserSlug_PopulatedOnCreate, TestIntegration_UserSlug_Unique | TESTED | Unit test: nkeys_test.go tests IssueUserJWT; slug DB tests are integration-level |
| users.slug derivation (email local-part) | computeUserSlug derives from email local-part | No unit test for computeUserSlug directly | TestIntegration_UserSlug_PopulatedOnCreate verifies `alice-smith` from `alice.smith@example.com` | UNIT_ONLY | Actually E2E_ONLY — no unit test for computeUserSlug; integration test covers it |
| LoginResponse UserSlug field | handleLogin returns userSlug | auth_test.go:TestLoginResponse_NATSURLField (struct field check) | TestIntegration_HandleLogin_UserSlugInResponse | TESTED | Integration test verifies `userSlug` field in response |
| handleRefresh returns UserSlug | handleRefresh looks up user by UUID, returns userSlug | auth_test.go:TestHandleRefresh_NilDB | TestIntegration_HandleRefresh_ReturnsSlug | TESTED | Both unit (nil DB path) and integration (happy path) |
| ensureHostsKV + ensureSessionsKV created on startup | StartProjectsSubscriber creates all 4 KV buckets | None (unit) | TestIntegration_StartProjectsSubscriberCreatesKVBuckets — verifies all 4 buckets exist with correct History | E2E_ONLY | No unit test; integration test is thorough |
| $SYS CONNECT machine host: KV write online=true | handleSysEvent machine CONNECT writes mclaude-hosts KV | None (unit) | TestIntegration_HandleSysEvent_MachineConnect | E2E_ONLY | Integration test verifies key, slug, online=true, lastSeenAt set |
| $SYS DISCONNECT machine host: KV write online=false, preserve lastSeenAt | handleSysEvent machine DISCONNECT read-modify-write | None (unit) | TestIntegration_HandleSysEvent_MachineDisconnect | E2E_ONLY | Integration test verifies online=false and lastSeenAt preserved |
| $SYS CONNECT cluster (Leafnode): KV write for all user rows | handleSysEvent Leafnode CONNECT writes KV for each user | None (unit) | TestIntegration_HandleSysEvent_ClusterConnect | E2E_ONLY | Integration test verifies both user rows get online=true |
| $SYS DISCONNECT cluster: KV online=false for all rows | handleSysEvent Leafnode DISCONNECT | None (unit) | TestIntegration_HandleSysEvent_ClusterDisconnect | E2E_ONLY | Integration test verifies online=false and lastSeenAt preserved for both rows |
| seedDev writes local host KV with online=true | DEV_SEED=true seedDev writes mclaude-hosts KV | None (unit) | TestIntegration_SeedDev_WritesLocalHostKV | E2E_ONLY | Integration test verifies key written with online=true, slug=local, lastSeenAt set |
| IssueUserJWT keeps claims.Name = userID | JWT claims.Name is UUID | nkeys_test.go (exists) | TestIntegration_NATSSubjectPermissions | TESTED | nkeys_test.go has IssueUserJWT tests |
| mclaude-sessions History=64 | ensureSessionsKV creates bucket with History=64 | None (unit) | TestIntegration_StartProjectsSubscriberCreatesKVBuckets checks history=64 | E2E_ONLY | |

**Note on integration test bar**: The $SYS event handler tests and KV bucket creation tests use a real NATS server via testcontainers. However, the testcontainers NATS does not run operator-mode JWT enforcement. Tests verify KV write behavior against a plain NATS server — this does not validate that the JWT scopes are correct for a production hub with operator mode. These tests are nevertheless appropriate for the behavioral tests (KV writes on CONNECT/DISCONNECT) specified in ADR-0046.

---

### Phase 4 — Bug Triage

No bug files found in `.agent/bugs/` matching the `mclaude-control-plane` component.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| — | No open bugs | — | No `.agent/bugs/` files reference this component |

---

### Summary

- Implemented: 28
- Gap: 2 (Login Response fields + readyz Postgres check)
- Partial: 3 (slug derivation format, startup step ordering, slug immutability guard)
- Infra: 37
- Unspec'd: 0
- Dead: 0
- Tested: 3
- Unit only: 0
- E2E only: 9
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0

---

### Findings Summary

**GAP [UNCLEAR]: Login Response shape** — spec-state-schema.md and spec-control-plane.md both describe a login response containing `user: {id, slug}` (nested), `hubUrl`, `hosts[]`, and `projects[]`. The code's `LoginResponse` struct is flat (`userId`, `userSlug`, `natsUrl`) and omits `hosts` and `projects` arrays. This is a pre-existing divergence not introduced by ADR-0046. Direction is UNCLEAR because: (a) the SPA may fetch hosts/projects via separate API calls, not from login; (b) the field name `natsUrl` vs `hubUrl` could be a spec naming issue; (c) the code may be intentionally simpler and the spec aspirational. Requires the caller to determine which side should change.

**GAP [CODE→FIX]: `/readyz` does not check Postgres** — spec-control-plane.md: "Kubernetes readiness probe (checks Postgres connectivity only — NATS outage must not mark pod unready)". Code returns `http.StatusOK` unconditionally without querying Postgres. Pre-existing gap unrelated to ADR-0046.

**PARTIAL [SPEC→FIX]: User slug derivation format** — spec-state-schema.md says `{slugify(name or email_local_part)}-{domain_first_segment}` but ADR-0046 decision explicitly uses email local-part only. Code is consistent with ADR-0046. Spec description (and example `"alice-gmail"`) should be updated to reflect the local-part-only format.

**PARTIAL [SPEC→FIX]: Startup step 6/7 ordering in spec** — spec-control-plane.md lists step 6 as $SYS subscribe and step 7 as KV ensure. Code reverses this (KV first, $SYS second) because ADR-0046 explicitly requires `hostsKV` to be set before `$SYS` events fire. Spec step numbering should swap 6 and 7.

**PARTIAL [SPEC→FIX]: Slug immutability guard** — spec says slug is immutable; no email-change endpoint exists so immutability holds implicitly. No code enforcement needed; spec correctly describes the intent.
