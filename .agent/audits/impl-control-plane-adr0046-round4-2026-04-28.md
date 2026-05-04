# Implementation Audit: mclaude-control-plane (ADR-0046) — Round 4

**Component**: mclaude-control-plane  
**Component root**: `mclaude-control-plane/`  
**ADR evaluated**: `docs/adr-0046-fix-host-liveness-kv-writes.md` (status: accepted)  
**Specs evaluated**: `docs/spec-state-schema.md`, `docs/mclaude-control-plane/spec-control-plane.md`  
**Focus**: Verify the two previously-flagged PARTIAL items are resolved (slug derivation format, startup step ordering). Note pre-existing out-of-scope gaps (login response shape, /readyz check).

---

## Run: 2026-04-28T12:30:00Z (Round 4)

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-state-schema.md users table | `slug TEXT UNIQUE NOT NULL` column on `users` | db.go:schema — `ALTER TABLE users ADD COLUMN IF NOT EXISTS slug TEXT NOT NULL DEFAULT ''; ... CREATE UNIQUE INDEX IF NOT EXISTS users_slug_uniq ON users (slug);` | IMPLEMENTED | — | |
| spec-state-schema.md users table slug derivation | `lower(regexp_replace(split_part(email, '@', 1), '[^a-zA-Z0-9]+', '-', 'g'))` (email local-part only) | db.go:computeUserSlug — identical formula; SQL backfill uses same expression | IMPLEMENTED | — | **Previously PARTIAL [SPEC→FIX] — RESOLVED.** Spec now explicitly states "email local-part only" with exact SQL formula. Code matches. |
| spec-state-schema.md users table | slug: `Immutable after creation; email changes do not rewrite the slug` | No email-change endpoint exists; no UPDATE SET slug path anywhere | IMPLEMENTED | — | Immutability holds implicitly; no enforcement guard needed |
| spec-state-schema.md `mclaude-hosts` bucket | `Created by: control-plane (ensureHostsKV)` | projects.go:ensureHostsKV + StartProjectsSubscriber call | IMPLEMENTED | — | |
| spec-state-schema.md `mclaude-hosts` bucket | Key format: `{uslug}.{hslug}` | sys_subscriber.go: `fmt.Sprintf("%s.%s", uslug, hslug)` in all paths | IMPLEMENTED | — | |
| spec-state-schema.md `mclaude-hosts` bucket | Value schema: `{slug, type, name, role, online, lastSeenAt}` | sys_subscriber.go:HostKVState struct | IMPLEMENTED | — | |
| spec-state-schema.md `mclaude-hosts` Writers | `$SYS.ACCOUNT.*.CONNECT` writes full value object with `online=true`, `lastSeenAt=now` | sys_subscriber.go:handleSysEvent Client CONNECT path | IMPLEMENTED | — | |
| spec-state-schema.md `mclaude-hosts` Writers | `$SYS.ACCOUNT.*.DISCONNECT` writes `{online: false}` — read-modify-write, preserves other fields, `lastSeenAt` not rewritten | sys_subscriber.go:handleSysEvent Client DISCONNECT path | IMPLEMENTED | — | |
| spec-state-schema.md `mclaude-hosts` Writers | Dev seed write: `DEV_SEED=true` writes bootstrap user's `local` host with `online=true` | main.go:seedDev — writes HostKVState with Online=true to `{user.Slug}.local` | IMPLEMENTED | — | |
| spec-state-schema.md `mclaude-hosts` | History: 1 | projects.go:ensureHostsKV — no explicit History field; NATS KV default is 1 | IMPLEMENTED | — | |
| spec-state-schema.md `mclaude-sessions` | `History: 64` | projects.go:ensureSessionsKV — `History: 64` | IMPLEMENTED | — | |
| spec-state-schema.md `mclaude-sessions` | `Created by: control-plane (ensureSessionsKV)` | projects.go:StartProjectsSubscriber calls ensureSessionsKV | IMPLEMENTED | — | |
| spec-state-schema.md Login Response Shape | `user: {id, slug}`, `jwt`, `nkeySeed`, `hubUrl`, `hosts[]`, `projects[]` | auth.go:LoginResponse — flat `userId`, `userSlug`, `jwt`, `nkeySeed`, `natsUrl`; NO `hosts[]` or `projects[]` | GAP (deferred) | UNCLEAR | **Pre-existing gap, out of scope for ADR-0046.** Not introduced by this ADR. Direction unclear — SPA may fetch hosts/projects separately. Noted per task instructions; no action required from this ADR. |
| spec-control-plane.md Startup Step 6 | Ensures KV buckets (`mclaude-projects`, `mclaude-hosts`, `mclaude-sessions`, `mclaude-job-queue`) — `mclaude-hosts` must exist before `$SYS` subscriber starts | projects.go:StartProjectsSubscriber creates all four buckets; called before StartSysSubscriber in main.go | IMPLEMENTED | — | **Previously PARTIAL [SPEC→FIX] — RESOLVED.** Spec now lists KV ensure as step 6 and $SYS subscribe as step 7. Code order matches. |
| spec-control-plane.md Startup Step 7 | Subscribes to `$SYS.ACCOUNT.*.CONNECT` and `$SYS.ACCOUNT.*.DISCONNECT` for host liveness | main.go:StartSysSubscriber called after StartProjectsSubscriber | IMPLEMENTED | — | |
| spec-control-plane.md Startup Step 9 | `DEV_SEED=true`: writes the `local` host's `mclaude-hosts` KV entry with `online=true` | main.go:seedDev — KV write for local host with Online=true | IMPLEMENTED | — | |
| spec-control-plane.md $SYS Subjects | CONNECT Client: SELECT machine host by public_key, update last_seen_at, upsert KV online=true | sys_subscriber.go:handleSysEvent Client/isConnect path | IMPLEMENTED | — | |
| spec-control-plane.md $SYS Subjects | CONNECT Leafnode: update last_seen_at ALL rows with matching slug, upsert KV for each user row | sys_subscriber.go:handleSysEvent Leafnode/isConnect path | IMPLEMENTED | — | |
| spec-control-plane.md $SYS Subjects | DISCONNECT: sets KV online=false for matched rows, does NOT rewrite last_seen_at | sys_subscriber.go:DISCONNECT paths for Client and Leafnode | IMPLEMENTED | — | |
| spec-control-plane.md $SYS Subjects | No match → ignore | sys_subscriber.go: qerr/Scan checks; on no match, code returns without KV write | IMPLEMENTED | — | |
| spec-control-plane.md NATS KV Buckets | `mclaude-hosts` — created by `ensureHostsKV`; control-plane is sole writer | projects.go + sys_subscriber.go + main.go (seedDev) | IMPLEMENTED | — | |
| spec-control-plane.md NATS KV Buckets | `mclaude-sessions` — created by `ensureSessionsKV`; bucket creation only | projects.go:ensureSessionsKV called; no session state writes from control-plane | IMPLEMENTED | — | |
| spec-control-plane.md Authentication | `IssueUserJWT` keeps `claims.Name = userID` (UUID) | nkeys.go:IssueUserJWT — `claims.Name = userID` | IMPLEMENTED | — | |
| spec-control-plane.md Authentication | `LoginResponse` gains `UserSlug` from `user.Slug` | auth.go:LoginResponse.UserSlug; handleLogin populates `UserSlug: user.Slug` | IMPLEMENTED | — | |
| spec-control-plane.md Authentication | `handleRefresh` looks up user by UUID (from `claims.Name`) and returns `UserSlug: user.Slug` | auth.go:handleRefresh — `db.GetUserByID(r.Context(), claims.Name)` then `UserSlug: user.Slug` | IMPLEMENTED | — | |
| spec-control-plane.md readyz endpoint | "Kubernetes readiness probe (checks Postgres connectivity only — NATS outage must not mark pod unready)" | server.go:handleReadyz returns 200 OK unconditionally, no Postgres check | GAP (deferred) | CODE→FIX | **Pre-existing gap, out of scope for ADR-0046.** Not introduced by this ADR. Noted per task instructions; no action required from this ADR. |
| ADR-0046 Decisions | `users.slug` migration: `ALTER TABLE users ADD COLUMN IF NOT EXISTS slug TEXT UNIQUE NOT NULL DEFAULT ''` + backfill | db.go:schema — column add + UPDATE backfill + separate unique index | IMPLEMENTED | — | |
| ADR-0046 Decisions | `ensureHostsKV` in `StartProjectsSubscriber`; `Server.hostsKV` field | projects.go + Server struct in auth.go | IMPLEMENTED | — | |
| ADR-0046 Decisions | Dev seed: write `{uslug}.local` KV with all fields | main.go:seedDev — constructs HostKVState with all fields | IMPLEMENTED | — | |
| ADR-0046 Decisions | `ensureSessionsKV` with History=64 | projects.go:ensureSessionsKV — `History: 64` | IMPLEMENTED | — | |

---

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| db.go (all production code) | INFRA | Users, hosts, projects, OAuth connections — all spec'd in state schema and spec-control-plane.md |
| sys_subscriber.go (all production code) | INFRA | HostKVState, sysConnectEvent, StartSysSubscriber, handleSysEvent — spec'd by ADR-0046 and $SYS subject table |
| projects.go (all production code) | INFRA | KV helpers, StartProjectsSubscriber — spec'd startup behavior |
| auth.go (all production code) | INFRA | Login, refresh, auth middleware — spec'd authentication flow |
| main.go (all production code) | INFRA | main(), seedDev, generateNATSUserCreds, loadOrGenerateAccountKey, envOr — spec'd startup sequence |
| nkeys.go (all production code) | INFRA | JWT issuance, NKey generation — spec'd trust chain |
| server.go (all production code) | INFRA | Route registration, admin mux — spec'd endpoints |

No UNSPEC'd or DEAD code blocks found.

---

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| users.slug derivation (email local-part) | `computeUserSlug` derives from email local-part only | No unit test for computeUserSlug | TestIntegration_UserSlug_PopulatedOnCreate | E2E_ONLY | No unit test; integration test verifies slug derivation |
| LoginResponse UserSlug field | handleLogin returns userSlug | auth_test.go (struct field check) | TestIntegration_HandleLogin_UserSlugInResponse | TESTED | Both unit and integration |
| handleRefresh returns UserSlug | handleRefresh looks up user by UUID, returns userSlug | auth_test.go:TestHandleRefresh_NilDB | TestIntegration_HandleRefresh_ReturnsSlug | TESTED | |
| ensureHostsKV + ensureSessionsKV created on startup | StartProjectsSubscriber creates all 4 KV buckets | None | TestIntegration_StartProjectsSubscriberCreatesKVBuckets | E2E_ONLY | Integration verifies all 4 buckets + correct History values |
| $SYS CONNECT machine host: KV write online=true | handleSysEvent machine CONNECT | None | TestIntegration_HandleSysEvent_MachineConnect | E2E_ONLY | |
| $SYS DISCONNECT machine host: KV write online=false, preserve lastSeenAt | handleSysEvent machine DISCONNECT read-modify-write | None | TestIntegration_HandleSysEvent_MachineDisconnect | E2E_ONLY | |
| $SYS CONNECT Leafnode: KV write for all user rows | handleSysEvent Leafnode CONNECT | None | TestIntegration_HandleSysEvent_ClusterConnect | E2E_ONLY | |
| $SYS DISCONNECT Leafnode: KV online=false for all rows | handleSysEvent Leafnode DISCONNECT | None | TestIntegration_HandleSysEvent_ClusterDisconnect | E2E_ONLY | |
| seedDev writes local host KV with online=true | DEV_SEED=true seedDev writes mclaude-hosts KV | None | TestIntegration_SeedDev_WritesLocalHostKV | E2E_ONLY | |
| IssueUserJWT keeps claims.Name = userID | JWT claims.Name is UUID | nkeys_test.go | TestIntegration_NATSSubjectPermissions | TESTED | |
| mclaude-sessions History=64 | ensureSessionsKV with History=64 | None | TestIntegration_StartProjectsSubscriberCreatesKVBuckets | E2E_ONLY | |

---

### Phase 4 — Bug Triage

No bugs in `.agent/bugs/` reference `mclaude-control-plane` as their component. The four open bugs (`001-new-project-crash-no-git-credentials.md`, `002-ptt-race-condition.md`, `003-initial-message-injection-race.md`, `005-getting-started-canned-text.md`) are all for other components.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| — | No open bugs for this component | — | |

---

### Summary

- Implemented: 28
- Gap (deferred, pre-existing, out-of-scope): 2
- Partial: 0  ← **both previous PARTIALs resolved**
- Infra: 37
- Unspec'd: 0
- Dead: 0
- Tested: 3
- E2E only: 8
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0

---

### Resolution of Previously-Flagged PARTIALs

**RESOLVED: Slug derivation format** (was PARTIAL [SPEC→FIX])  
`spec-state-schema.md` `users` table now explicitly reads: `"Derived at user creation as lower(regexp_replace(split_part(email, '@', 1), '[^a-zA-Z0-9]+', '-', 'g')) (email local-part only, slugified)"`. This matches `computeUserSlug` in `db.go` exactly. The previous mismatch (spec implied domain-first-segment suffix) no longer exists. Verdict upgraded to IMPLEMENTED.

**RESOLVED: Startup step ordering** (was PARTIAL [SPEC→FIX])  
`spec-control-plane.md` Startup Sequence now lists step 6 as "Ensures KV buckets (`mclaude-projects`, `mclaude-hosts`, `mclaude-sessions`, `mclaude-job-queue`) — `mclaude-hosts` must exist before the `$SYS` subscriber starts" and step 7 as "$SYS subscribe". The code in `main.go` calls `StartProjectsSubscriber` (KV) then `StartSysSubscriber` — matching the corrected spec order. Verdict upgraded to IMPLEMENTED.

### Pre-Existing Out-of-Scope Gaps (noted, no action required from ADR-0046)

**GAP [UNCLEAR] (deferred): Login Response shape** — spec-state-schema.md / spec-control-plane.md describe a login response containing `user: {id, slug}` (nested object), `hubUrl`, `hosts[]`, and `projects[]`. The code's `LoginResponse` struct is flat (`userId`, `userSlug`, `natsUrl`) and omits `hosts` and `projects` arrays. This is a pre-existing divergence unrelated to ADR-0046. Not actioned here.

**GAP [CODE→FIX] (deferred): `/readyz` does not check Postgres** — spec says "Kubernetes readiness probe (checks Postgres connectivity only)". Code returns `http.StatusOK` unconditionally. Pre-existing gap unrelated to ADR-0046. Not actioned here.
