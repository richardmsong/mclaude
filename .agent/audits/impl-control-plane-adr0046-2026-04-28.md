# Implementation Audit: mclaude-control-plane vs ADR-0046

**Component**: mclaude-control-plane
**ADR**: docs/adr-0046-fix-host-liveness-kv-writes.md (Status: accepted)
**Specs**: docs/spec-state-schema.md, docs/mclaude-control-plane/spec-control-plane.md

## Run: 2026-04-28T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| ADR-0046 Decisions / users.slug migration | "Add `ALTER TABLE users ADD COLUMN IF NOT EXISTS slug TEXT UNIQUE NOT NULL DEFAULT '';`" | db.go:270 | GAP | CODE→FIX | ALTER statement adds `slug TEXT NOT NULL DEFAULT ''` — the `UNIQUE` constraint is absent. No separate `CREATE UNIQUE INDEX` for users.slug exists either. |
| ADR-0046 Decisions / users.slug backfill | "backfill `UPDATE users SET slug = lower(regexp_replace(split_part(email, '@', 1), '[^a-zA-Z0-9]+', '-', 'g')) WHERE slug = ''`" | db.go:272–274 | IMPLEMENTED | — | Exact SQL present |
| ADR-0046 Decisions / JWT slug claim | "Update `IssueUserJWT` to put the user slug (not UUID) in `claims.Name`" | nkeys.go:117–121 | IMPLEMENTED | — | `IssueUserJWT(userSlug string, ...)` sets `claims.Name = userSlug` |
| ADR-0046 Decisions / JWT slug claim | "login response gains `userSlug`" | auth.go:29–32, auth.go:87–92 | IMPLEMENTED | — | `LoginResponse.UserSlug` field, populated as `user.Slug` in handleLogin |
| ADR-0046 Decisions / ensureHostsKV placement | "Add to `StartProjectsSubscriber` alongside existing `ensureProjectsKV`/`ensureJobQueueKV`; store returned `nats.KeyValue` as `Server.hostsKV`" | projects.go:51–56 | IMPLEMENTED | — | `ensureHostsKV(js)` called; result stored in `s.hostsKV` |
| ADR-0046 Decisions / KV write key format | "`{uslug}.{hslug}` — where uslug looked up from users.slug via JOIN" | sys_subscriber.go:88–100, sys_subscriber.go:162–179 | IMPLEMENTED | — | Both machine and cluster paths use `fmt.Sprintf("%s.%s", uslug, hslug)` with slug from JOIN |
| ADR-0046 Decisions / KV write in handleSysEvent — machine CONNECT | "On CONNECT: SELECT … from hosts h JOIN users u … WHERE h.public_key = $1 AND h.type = 'machine'; upsert `{uslug}.{hslug}` with `{slug, type, name, role, online:true, lastSeenAt:RFC3339}`" | sys_subscriber.go:83–103 | IMPLEMENTED | — | Full value object written with all fields including lastSeenAt |
| ADR-0046 Decisions / KV write in handleSysEvent — machine DISCONNECT | "On DISCONNECT: same lookup; write `online=false`, no `lastSeenAt` rewrite" | sys_subscriber.go:105–131 | IMPLEMENTED | — | Read-modify-write: fetches existing, sets online=false, preserves all other fields including lastSeenAt |
| ADR-0046 Decisions / KV write in handleSysEvent — cluster CONNECT | "On Leafnode CONNECT: look up clusterSlug from hosts WHERE public_key = $1 AND type = 'cluster' LIMIT 1; then SELECT all user rows; upsert KV for each row" | sys_subscriber.go:136–183 | IMPLEMENTED | — | Two-step lookup: first get clusterSlug, then query all user rows |
| ADR-0046 Decisions / KV write in handleSysEvent — cluster DISCONNECT | "Leafnode DISCONNECT with online=false" | sys_subscriber.go:185–225 | IMPLEMENTED | — | Read-modify-write per user row, sets online=false |
| ADR-0046 Decisions / Dev seed KV write | "After seeding dev user and local host, write `{uslug}.local → {slug:'local', type:'machine', name:'Local Machine', role:'owner', online:true, lastSeenAt:RFC3339}` to mclaude-hosts" | main.go:175–210 | IMPLEMENTED | — | DEV_SEED=true path writes full HostKVState for local host using user.Slug prefix |
| spec-state-schema.md / mclaude-hosts bucket creation | "Created by: control-plane (`ensureHostsKV`)" | projects.go:209–218 | IMPLEMENTED | — | `ensureHostsKV` creates bucket with History:1 |
| spec-state-schema.md / mclaude-hosts key format | "Key format: `{uslug}.{hslug}`" | sys_subscriber.go (multiple), main.go:207 | IMPLEMENTED | — | All write paths use the correct dot-separated slug format |
| spec-state-schema.md / mclaude-hosts value schema | `{slug, type, name, role, online, lastSeenAt}` | sys_subscriber.go:17–24 | IMPLEMENTED | — | `HostKVState` struct matches all 6 fields with correct JSON tags |
| spec-state-schema.md / mclaude-hosts DISCONNECT partial write | "DISCONNECT writes only `{online: false}` — read-modify-write: fetch current value, set `online=false`, re-put. `lastSeenAt` is NOT rewritten on disconnect" | sys_subscriber.go:107–131, sys_subscriber.go:192–224 | IMPLEMENTED | — | Both machine and cluster disconnect paths do read-modify-write; only `state.Online = false` is set |
| spec-state-schema.md / mclaude-hosts History:1 | `nats.KeyValueConfig{Bucket: "mclaude-hosts", History: 1}` | projects.go:212–217 | IMPLEMENTED | — | History=1 set |
| spec-state-schema.md / mclaude-hosts dev-seed note | "Secondary path (dev only): on DEV_SEED=true startup, control-plane writes bootstrap user's local machine host with online=true" | main.go:176–210 | IMPLEMENTED | — | Guarded by `user.Slug != ""` and DEV_SEED env check |
| spec-state-schema.md / users.slug column | "slug TEXT UNIQUE NOT NULL" | db.go:270 | GAP | CODE→FIX | ALTER statement adds slug WITHOUT UNIQUE. For new installs the CREATE TABLE also omits slug entirely (only added via ALTER). No unique index for users.slug exists. |
| spec-state-schema.md / $SYS CONNECT Client lookup | "SELECT * FROM hosts WHERE public_key = client.nkey AND type = 'machine'; update last_seen_at; upsert mclaude-hosts KV online=true" | sys_subscriber.go:68–103 | IMPLEMENTED | — | UPDATE last_seen_at + KV put (joined for uslug); correct guard for no-match case |
| spec-state-schema.md / $SYS DISCONNECT Client | "Set KV online=false for matched row. last_seen_at NOT rewritten" | sys_subscriber.go:104–131 | IMPLEMENTED | — | No last_seen_at update; read-modify-write for KV |
| spec-state-schema.md / $SYS CONNECT Leafnode | "update last_seen_at for ALL rows where slug=found.slug AND type='cluster'; upsert KV for each user row" | sys_subscriber.go:135–183 | IMPLEMENTED | — | Cluster-shared liveness across all granted users |
| spec-state-schema.md / $SYS DISCONNECT Leafnode | "Set KV online=false for all matching cluster rows" | sys_subscriber.go:184–225 | IMPLEMENTED | — | Iterates all user rows, sets online=false per row |
| spec-control-plane.md / startup step 7 (mclaude-sessions) | "Ensures KV buckets exist (`mclaude-projects`, `mclaude-hosts`, `mclaude-sessions`, `mclaude-job-queue`)" | projects.go:39–56 | GAP | CODE→FIX | `mclaude-sessions` bucket is NOT created by control-plane. Only projects, job-queue, and hosts are ensured. ADR-0046 explicitly defers ensureSessionsKV but the spec still lists it. |
| spec-control-plane.md / startup step 9 (dev-seed KV) | "writes local host's mclaude-hosts KV entry with online=true (dev-only path)" | main.go:175–210 | IMPLEMENTED | — | Matches spec description |
| spec-control-plane.md / NATS KV Buckets mclaude-hosts | "control-plane is the sole writer; production writes from $SYS; dev-seed path from DEV_SEED=true startup" | sys_subscriber.go, main.go:175–210 | IMPLEMENTED | — | Single-writer invariant maintained |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| sys_subscriber.go:1–14 | INFRA | Package declaration and imports |
| sys_subscriber.go:15–31 | INFRA | `HostKVState` struct + `sysConnectEvent` struct — data models for spec'd behavior |
| sys_subscriber.go:32–63 | INFRA | `StartSysSubscriber` function — subscribes to $SYS.ACCOUNT.*.CONNECT/DISCONNECT per spec |
| db.go:1–55 | INFRA | Package declaration, DB struct, DB types — boilerplate for spec'd data model |
| db.go:56–73 | INFRA | `computeUserSlug` helper — implements the slug derivation rule specified in ADR-0046 |
| auth.go:35–44 | INFRA | `Server` struct definition — necessary plumbing |
| projects.go:43–56 | INFRA | `StartProjectsSubscriber` preamble (JetStream setup) — necessary for ensureHostsKV call |
| projects.go:196–218 | INFRA | `ensureHostsKV` function — implements spec'd bucket creation |
| main.go:150–155 | INFRA | `seedDev` header + user lookup — pre-condition check for dev-seed KV write |
| main.go:156–174 | INFRA | Project KV seeding in seedDev — covers the mclaude-projects KV (not ADR-0046 scope but spec'd) |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| ADR-0046 / users.slug backfill | UPDATE users SET slug = … | None | None | UNTESTED | db_test.go checks schema fragments but not for slug column or backfill SQL |
| ADR-0046 / IssueUserJWT uses slug | claims.Name = userSlug | nkeys_test.go (TestIssueHostJWT tests IssueHostJWT; no test for IssueUserJWT slug claim specifically) | None | UNTESTED | No test verifies that IssueUserJWT puts the slug (not UUID) in claims.Name |
| ADR-0046 / LoginResponse.UserSlug | login response gains userSlug field | None | None | UNTESTED | auth_test.go has login tests but none check UserSlug is populated from user.Slug |
| ADR-0046 / ensureHostsKV | creates mclaude-hosts bucket on startup | None | None | UNTESTED | No test covers bucket creation; cluster_test.go tests $JS API access but not hostsKV specifically |
| ADR-0046 / $SYS CONNECT machine | upsert mclaude-hosts KV online=true | None | None | UNTESTED | Zero test coverage for handleSysEvent machine CONNECT path |
| ADR-0046 / $SYS DISCONNECT machine | read-modify-write online=false | None | None | UNTESTED | Zero test coverage for handleSysEvent machine DISCONNECT path |
| ADR-0046 / $SYS CONNECT Leafnode | cluster-shared KV upsert online=true | None | None | UNTESTED | Zero test coverage for handleSysEvent cluster CONNECT path |
| ADR-0046 / $SYS DISCONNECT Leafnode | read-modify-write online=false for all user rows | None | None | UNTESTED | Zero test coverage for handleSysEvent cluster DISCONNECT path |
| ADR-0046 / Dev seed KV write | writes local host to mclaude-hosts with online=true | None | None | UNTESTED | No test for seedDev KV write path |

### Phase 4 — Bug Triage

Checked `.agent/bugs/`: four open bugs found. None have `**Component**: mclaude-control-plane` — all reference mclaude-session-agent or mclaude-web scope.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| 001-new-project-crash-no-git-credentials.md | New project crash: no git credentials | OPEN | Component: session-agent; not applicable to this audit |
| 002-ptt-race-condition.md | PTT race condition | OPEN | Component: session-agent; not applicable |
| 003-initial-message-injection-race.md | Initial message injection race | OPEN | Component: session-agent; not applicable |
| 005-getting-started-canned-text.md | Getting started canned text | OPEN | Component: web; not applicable |

### Summary

- Implemented: 22
- Gap: 3
- Partial: 0
- Infra: 10
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 0
- Untested: 9
- Bugs fixed: 0
- Bugs open: 0 (none in this component)
