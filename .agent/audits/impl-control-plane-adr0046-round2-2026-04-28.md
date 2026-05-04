# Implementation Audit: mclaude-control-plane × ADR-0046

**Component**: mclaude-control-plane  
**Root**: `mclaude-control-plane/`  
**Specs evaluated**:
- `docs/adr-0046-fix-host-liveness-kv-writes.md` (Status: accepted)
- `docs/spec-state-schema.md`
- `docs/mclaude-control-plane/spec-control-plane.md`

## Run: 2026-04-28T00:00:00Z

---

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0046 Decision: users.slug migration | `ALTER TABLE users ADD COLUMN IF NOT EXISTS slug TEXT UNIQUE NOT NULL DEFAULT ''` + backfill from email local-part | `db.go:schema` — `ALTER TABLE users ADD COLUMN IF NOT EXISTS slug TEXT NOT NULL DEFAULT '';` + `UPDATE users SET slug = lower(regexp_replace(...)) WHERE slug = '';` + `CREATE UNIQUE INDEX IF NOT EXISTS users_slug_uniq ON users (slug);` | IMPLEMENTED | — | Schema includes CREATE TABLE DDL with `slug TEXT UNIQUE NOT NULL DEFAULT ''` and the ALTER/backfill/index for upgrades |
| adr-0046 Decision: JWT slug claim — IssueUserJWT uses slug | `Update IssueUserJWT to put the user slug (not the user UUID) in claims.Name` | `nkeys.go:95-130` — `IssueUserJWT(userSlug string, ...)` stores `claims.Name = userSlug` | IMPLEMENTED | — | Function signature and comment explicitly state userSlug |
| adr-0046 Decision: login response gains userSlug | `login response gains UserSlug` | `auth.go:34-35` — `UserSlug string json:"userSlug,omitempty"`; `auth.go:120-127` — `UserSlug: user.Slug` | IMPLEMENTED | — | handleLogin sets UserSlug from users.slug DB column |
| adr-0046 Decision: handleRefresh includes userSlug | `Update ... refresh handlers to look up users.slug and include it in LoginResponse.UserSlug` | `auth.go:155-167` — `UserSlug: claims.Name // claims.Name is now the user slug` | IMPLEMENTED | — | Refresh re-uses claims.Name which is the slug |
| adr-0046 Decision: ensureHostsKV in StartProjectsSubscriber | `Add to StartProjectsSubscriber alongside existing ensureProjectsKV / ensureJobQueueKV calls; store the returned nats.KeyValue as Server.hostsKV` | `projects.go:46-61` — `hostsKV, err := ensureHostsKV(js)` + `s.hostsKV = hostsKV`; `auth.go:56-57` — `hostsKV nats.KeyValue` field | IMPLEMENTED | — | ensureHostsKV is called in order alongside ensureProjectsKV and ensureJobQueueKV |
| adr-0046 Decision: KV key format {uslug}.{hslug} | `{uslug}.{hslug} — where uslug is looked up from users.slug (via JOIN) for each affected row` | `sys_subscriber.go:71-73, 111-113` — `key := fmt.Sprintf("%s.%s", uslug, hslug)` with `u.slug AS uslug` from JOIN | IMPLEMENTED | — | Key construction uses SQL JOIN to get user slug |
| adr-0046 Decision: machine CONNECT KV write | `On CONNECT: SELECT ... FROM hosts h JOIN users u ON h.user_id = u.id WHERE h.public_key = $1 AND h.type = 'machine'; upsert {uslug}.{hslug} with {slug:hslug, type, name, role, online:true, lastSeenAt:RFC3339}` | `sys_subscriber.go:55-90` — `case "Client":` + isConnect branch | IMPLEMENTED | — | Query, fields and key format match spec exactly |
| adr-0046 Decision: machine DISCONNECT KV write | `On DISCONNECT: same lookup; write online=false, no lastSeenAt field` | `sys_subscriber.go:91-123` — DISCONNECT branch reads-modifies-writes: fetches existing, sets Online=false, re-puts. LastSeenAt preserved from existing entry. | IMPLEMENTED | — | Read-modify-write pattern matches ADR spec. Fallback when unmarshal fails creates struct without LastSeenAt (nil), which is acceptable error handling. |
| adr-0046 Decision: cluster Leafnode CONNECT | `On Leafnode CONNECT: look up clusterSlug FROM hosts WHERE public_key = $1 AND type = 'cluster' LIMIT 1; then SELECT u.slug, h.slug, h.name, h.type, h.role FROM hosts h JOIN users u ... WHERE h.slug = $1 AND h.type = 'cluster'; upsert KV for each row` | `sys_subscriber.go:127-179` — `case "Leafnode":` + isConnect branch with two-phase query | IMPLEMENTED | — | Two-phase query (first get slug, then get all user rows) matches ADR approach |
| adr-0046 Decision: cluster Leafnode DISCONNECT | `Same lookup for DISCONNECT with online=false` | `sys_subscriber.go:180-218` — Leafnode DISCONNECT branch reads-modifies-writes for each cluster user row | IMPLEMENTED | — | Consistent with machine DISCONNECT pattern |
| adr-0046 Decision: dev seed KV write | `After seeding dev user and local host, look up local host row and write {uslug}.local → {slug:'local', type:'machine', name:'Local Machine', role:'owner', online:true, lastSeenAt:RFC3339} to mclaude-hosts` | `main.go:107-139` — seedDev queries local host then writes HostKVState{Online:true, LastSeenAt:&now} with key `user.Slug + "." + localHostSlug` | IMPLEMENTED | — | Only writes when user.Slug != "" (guard present) |
| spec-control-plane.md:startup-7 | `Ensures KV buckets exist (mclaude-projects, mclaude-hosts, mclaude-sessions, mclaude-job-queue)` | `projects.go:41-69` — all four ensureXxxKV calls in StartProjectsSubscriber | IMPLEMENTED | — | All four buckets created in order |
| spec-state-schema.md:mclaude-hosts CONNECT | `$SYS CONNECT → control-plane ... upsert mclaude-hosts KV {uslug}.{hslug} with online=true` | `sys_subscriber.go:55-90` | IMPLEMENTED | — | Matches schema spec |
| spec-state-schema.md:mclaude-hosts DISCONNECT | `$SYS DISCONNECT → writes only {online: false} — all other fields preserved from previous entry (read-modify-write)` | `sys_subscriber.go:91-123` | IMPLEMENTED | — | Read-modify-write as spec requires |
| spec-state-schema.md:mclaude-hosts dev-seed | `Secondary path (dev only): on DEV_SEED=true startup, writes bootstrap user's local machine host entry with online=true` | `main.go:107-139` | IMPLEMENTED | — | Dev-only guard; idempotent via KV Put |
| spec-state-schema.md:users.slug | `slug TEXT UNIQUE NOT NULL — Typed-slug identifier. Derived at user creation as {slugify(email_local_part)}` | `db.go:39-43` — computeUserSlug + `db.go:schema` — slug column in DDL + `db.go:144-148` — CreateUser passes slug | IMPLEMENTED | — | slug computed and persisted on creation |
| spec-state-schema.md:mclaude-hosts Value schema | `{slug, type, name, role, online, lastSeenAt}` | `sys_subscriber.go:15-23` — HostKVState struct matches all six fields | IMPLEMENTED | — | JSON tags match spec field names |
| spec-state-schema.md:mclaude-sessions History | `History: all versions (for resume tracking)` | `projects.go:178-183` — `ensureSessionsKV` creates with `History: 1` | GAP | SPEC→FIX | NATS KV max history is 64; "all versions" is not achievable (nats.KeyValueConfig has no "unlimited" option). Spec should be updated to specify `MaxHistory: 64` or acknowledge the NATS constraint. This is a pre-existing gap; ADR-0046 only added the ensureSessionsKV call. |
| spec-state-schema.md:Login Response Shape | `{user: {id, slug}, jwt, nkeySeed, hubUrl, hosts[], projects[]}` | `auth.go:26-41` — LoginResponse struct has `userId`, `userSlug`, `jwt`, `nkeySeed`, `natsUrl`, `expiresAt` — missing `hosts[]`, `projects[]`, `hubUrl` (uses `natsUrl`), and nested `user` object | GAP | SPEC→FIX | Pre-existing structural mismatch from ADR-0035 (not introduced by ADR-0046). ADR-0046 only added `userSlug`. Hosts/projects/hubUrl were not in scope of this ADR. Direction SPEC→FIX because this is a phased rollout and the response shape docs are ahead of implementation. |
| adr-0046 handleRefresh UserID after slug migration | Implied: refresh response should return correct userId (UUID) and userSlug (slug) | `auth.go:155-167` — `UserID: claims.Name` sets UserID=slug (not UUID) after ADR-0046 because claims.Name is now slug. handleRefresh has no DB access to re-fetch UUID. | PARTIAL | UNCLEAR | Before ADR-0046 claims.Name was UUID so UserID was correct. After ADR-0046, claims.Name is slug, so UserID=slug in refresh responses. The SPA's dependency on `userId` being a UUID (vs slug) is unclear from the spec. Could be CODE→FIX (add DB lookup in handleRefresh) or SPEC→FIX (clarify userId in refresh is slug-based). |

---

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| `db.go:1-15` (imports) | INFRA | Standard Go imports for the DB layer |
| `db.go:24-48` (User struct + computeUserSlug) | INFRA | Spec-implementing data structures and slug derivation function |
| `db.go:49-74` (Host struct) | INFRA | ADR-0035 specified Host model |
| `db.go:76-100` (ConnectDB, Close) | INFRA | Startup sequence step 1 (spec-control-plane.md) |
| `db.go:102-106` (Migrate) | INFRA | Startup step 1 — spec-control-plane.md |
| `db.go:108-250` (CRUD functions) | INFRA | All spec-covered Postgres readers/writers per spec-state-schema.md |
| `db.go:252-400` (OAuthConnection + CRUD) | INFRA | OAuth connection persistence per spec-control-plane.md |
| `db.go:402-450` (schema const) | INFRA | DDL for all spec-covered tables |
| `auth.go:1-40` (LoginRequest, LoginResponse, Server struct) | INFRA | Spec-covering data types |
| `auth.go:42-170` (handlers and auth middleware) | INFRA | All spec-covered HTTP endpoints |
| `projects.go:1-200` (project/KV functions) | INFRA | Spec-covering project creation and KV bucket management |
| `sys_subscriber.go:1-220` (entire file) | INFRA | ADR-0046 specified $SYS subscriber logic |
| `main.go:1-80` (startup) | INFRA | Spec-covering startup sequence |
| `main.go:82-145` (seedDev) | INFRA | Spec-covering DEV_SEED path (spec-control-plane.md startup step 9) |
| `main.go:147-200` (generateNATSUserCreds, loadOrGenerateAccountKey, envOr) | INFRA | Startup helpers; accountKey loading per spec-control-plane.md |
| `nkeys.go:1-200` (entire file) | INFRA | JWT issuance functions specified in spec-control-plane.md Authentication section |

No DEAD or UNSPEC'd code blocks found. The entire production codebase maps to spec-covered behavior.

---

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| adr-0046: users.slug schema column | slug TEXT UNIQUE NOT NULL + backfill | `db_test.go:TestSchema_UsersSlugColumn`, `TestSchema_UsersSlugUniqueIndex` | `integration_test.go:TestIntegration_UserSlug_PopulatedOnCreate`, `TestIntegration_UserSlug_Unique` | TESTED | Both unit (schema string check) and integration (real Postgres) coverage |
| adr-0046: IssueUserJWT uses slug | claims.Name = userSlug | `nkeys_test.go:TestIssueUserJWT_SlugInClaimsName` | `integration_test.go:TestIntegration_HandleLogin_UserSlugInResponse` | TESTED | Direct unit test and integration test through HTTP handler |
| adr-0046: login response userSlug | LoginResponse.UserSlug = users.slug | — | `integration_test.go:TestIntegration_HandleLogin_UserSlugInResponse` | E2E_ONLY | No dedicated unit test for this field; covered by integration test against real DB |
| adr-0046: ensureHostsKV in StartProjectsSubscriber | hostsKV created on startup | — | `integration_test.go:TestIntegration_StartProjectsSubscriberCreatesKVBuckets` | E2E_ONLY | Verified via real NATS; no unit test for bucket creation |
| adr-0046: machine CONNECT KV write | handleSysEvent writes online=true | — | `integration_test.go:TestIntegration_HandleSysEvent_MachineConnect` | E2E_ONLY | Real NATS + real Postgres; no unit test |
| adr-0046: machine DISCONNECT KV write | handleSysEvent writes online=false, preserves lastSeenAt | — | `integration_test.go:TestIntegration_HandleSysEvent_MachineDisconnect` | E2E_ONLY | Real NATS + real Postgres; verifies lastSeenAt preservation |
| adr-0046: cluster Leafnode CONNECT KV write | Leafnode CONNECT upserts KV for all user rows | — | — | UNTESTED | No integration or unit test covers the `case "Leafnode"` CONNECT branch. Gap: CODE→FIX — add integration test for Leafnode CONNECT writing hostsKV for multiple cluster user rows. |
| adr-0046: cluster Leafnode DISCONNECT KV write | Leafnode DISCONNECT sets online=false for all cluster user rows | — | — | UNTESTED | No test for Leafnode DISCONNECT path. Gap: CODE→FIX — add integration test. |
| adr-0046: dev seed local host KV write | seedDev writes {uslug}.local → online=true | — | — | UNTESTED | No test verifies seedDev writes to mclaude-hosts KV. Gap: CODE→FIX — add integration test for DEV_SEED=true path that checks hostsKV entry. |
| spec-state-schema.md:mclaude-hosts value schema | {slug, type, name, role, online, lastSeenAt} | — | `integration_test.go:TestIntegration_HandleSysEvent_MachineConnect` (checks slug, online, lastSeenAt) | E2E_ONLY | Partial field verification in integration test; type/name/role not explicitly asserted |
| spec-state-schema.md:mclaude-sessions bucket | ensureSessionsKV creates bucket | — | `integration_test.go:TestIntegration_StartProjectsSubscriberCreatesKVBuckets` | E2E_ONLY | Bucket existence verified; History value not checked |
| spec-control-plane.md:startup-7 | All four KV buckets created | — | `integration_test.go:TestIntegration_StartProjectsSubscriberCreatesKVBuckets` | E2E_ONLY | All four buckets checked for existence and History=1 |

---

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` have `**Component**: mclaude-control-plane`. Checked files:
- `001-new-project-crash-no-git-credentials.md` — Component: mclaude-controller-local (not control-plane)
- `002-ptt-race-condition.md` — Component: mclaude-session-agent
- `003-initial-message-injection-race.md` — Component: mclaude-session-agent
- `005-getting-started-canned-text.md` — Component: mclaude-web

Fixed bug `fixed/004-heartbeat-stale-agent-down.md` mentions the heartbeat KV bucket. This was fixed by ADR-0046 — the hostsKV bucket now exists and is written by $SYS events. Already marked as fixed.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none open for this component) | — | — | — |

---

### Summary

- Implemented: 17
- Gap: 2
- Partial: 1
- Infra: 16
- Unspec'd: 0
- Dead: 0
- Tested: 1
- Unit only: 0
- E2E only: 7
- Untested: 3
- Bugs fixed: 0 (fixed/004 already marked fixed)
- Bugs open: 0

---

## Non-clean findings

**GAP [SPEC→FIX]**: `"History: all versions (for resume tracking)"` → `ensureSessionsKV` creates the bucket with `History: 1`; NATS KV has no "unlimited" option (max is 64). Spec should be updated to specify `History: 64` or acknowledge the NATS constraint. (`projects.go:178-183`)

**GAP [SPEC→FIX]**: Login Response Shape in `spec-state-schema.md` and `spec-control-plane.md` shows `{user: {id, slug}, jwt, nkeySeed, hubUrl, hosts[], projects[]}` but implementation has flat `{userId, userSlug, jwt, nkeySeed, natsUrl, expiresAt}` without `hosts[]`, `projects[]`. Pre-existing gap from ADR-0035; ADR-0046 only added `userSlug`. Direction: SPEC→FIX — spec is ahead of phased implementation; the response shape spec should be marked as v-next or the implementation gap tracked as a separate ADR. (`auth.go:26-41`)

**PARTIAL [UNCLEAR]**: After ADR-0046, `handleRefresh` sets `UserID: claims.Name` which is now the user slug (not UUID) since `IssueUserJWT` stores the slug in `claims.Name`. Before ADR-0046 `claims.Name` was the UUID so `UserID` was correct in refresh responses. Now `UserID = slug` in refresh responses. Whether this breaks SPA behavior depends on whether the SPA uses `userId` from the refresh response as a UUID or is tolerant of slug values. (`auth.go:155-167`) — Could be CODE→FIX (add DB lookup in handleRefresh to re-fetch UUID) or SPEC→FIX (clarify that refresh response userId equals slug-based JWT name).

**UNTESTED [CODE→FIX]**: Cluster Leafnode CONNECT KV write — no integration or unit test covers the `case "Leafnode"` CONNECT branch in `handleSysEvent`. Should have an integration test that creates a cluster host row, fires a Leafnode CONNECT event, and verifies all user rows get `online=true` in hostsKV. (`sys_subscriber.go:127-179`)

**UNTESTED [CODE→FIX]**: Cluster Leafnode DISCONNECT KV write — no test for the Leafnode DISCONNECT path. (`sys_subscriber.go:180-218`)

**UNTESTED [CODE→FIX]**: Dev seed local host KV write — no test verifies that `seedDev` writes the `{uslug}.local` entry to `mclaude-hosts` KV when `DEV_SEED=true`. (`main.go:107-139`)
