# ADR: Fix Host Liveness KV Writes

**Status**: accepted
**Status history**:
- 2026-04-28: accepted

## Overview

`mclaude-control-plane`'s `$SYS` subscriber updates Postgres `last_seen_at` on host CONNECT/DISCONNECT events but never writes to the `mclaude-hosts` KV bucket. The SPA's `HeartbeatMonitor` watches `mclaude-hosts` for `{online: bool}` entries but the bucket is empty, so every project always appears unhealthy and the "heartbeat stale" banner is permanently shown. Additionally, the `users` table is missing the `slug` column (present in spec but absent from `db.go`), causing the SPA to fall back to using the user UUID as the subject/KV key prefix — which diverges from the `{uslug}.{hslug}` key scheme in the spec.

## Motivation

User `dev@mclaude.local` reports "default project heartbeat stale" banner on every page load. The control-plane specs (`spec-control-plane.md` lines 106–107, `spec-state-schema.md` lines 170–189) are unambiguous: on `$SYS.ACCOUNT.*.CONNECT`, the control plane must upsert `mclaude-hosts` KV with `online=true`; on DISCONNECT, set `online=false`. The code omits this entirely. Five concurrent bugs cause the failure:

1. **Missing KV writes in `sys_subscriber.go`**: CONNECT/DISCONNECT handlers update Postgres only; `mclaude-hosts` KV is never touched.
2. **`mclaude-hosts` bucket never created**: No `ensureHostsKV` call exists anywhere in the control-plane startup path. KV watchers get an error or empty stream.
3. **SPA format/key mismatch in `heartbeat-monitor.ts`**: The monitor reads `{ts: string}` and does time-delta health check, but `mclaude-hosts` KV values are `{online: bool, ...}`. Additionally, it extracts the key's second segment as "projectId" and calls `isHealthy(projectId)`, but callers pass a project UUID while the health map keys are host slugs.
4. **`session-list-vm.ts` wrong key type**: Calls `heartbeatMonitor.isHealthy(p.id)` (project UUID) instead of `heartbeatMonitor.isHealthy(p.hostSlug ?? 'local')` (host slug).
5. **Dev seed missing KV entry**: The `local` host auto-created by migration has no `public_key`, so no `$SYS` CONNECT event ever fires for it. Dev users never see an online host.
6. **`users.slug` column missing from DB schema**: `spec-state-schema.md` defines `users.slug TEXT UNIQUE NOT NULL` but `db.go`'s DDL omits the column. Without it, `IssueUserJWT` puts the user UUID in `claims.Name`, the login response lacks `userSlug`, and the SPA uses the UUID as the slug — diverging from the `{uslug}.{hslug}` KV key scheme.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| `users.slug` migration | Add `ALTER TABLE users ADD COLUMN IF NOT EXISTS slug TEXT UNIQUE NOT NULL DEFAULT '';` and backfill `UPDATE users SET slug = lower(regexp_replace(split_part(email, '@', 1), '[^a-zA-Z0-9]+', '-', 'g')) WHERE slug = '';` after the column add. Apply the same `slugify(email_local_part)` rule used by ADR-0024. | `users.slug` is in the spec but absent from the code. Without it the KV key prefix is the UUID instead of the slug, breaking the `{uslug}.{hslug}` scheme. The backfill uses `email` local-part as the canonical derivation (consistent with ADR-0024's slug charset). |
| JWT slug claim | Update `IssueUserJWT` to put the user slug (not the user UUID) in `claims.Name`. Update login / refresh handlers to look up `users.slug` and include it in `LoginResponse.UserSlug`. | The JWT `Name` field is the `uslug` the SPA reads to construct KV key patterns. Using the slug here closes the loop between DB, JWT, and SPA. |
| `ensureHostsKV` placement | Add to `StartProjectsSubscriber` alongside existing `ensureProjectsKV` / `ensureJobQueueKV` calls; store the returned `nats.KeyValue` as `Server.hostsKV`. | Consistent with existing bucket-creation pattern; guarantees bucket exists before `$SYS` events fire or `seedDev` runs. |
| KV write key format | `{uslug}.{hslug}` — where `uslug` is looked up from `users.slug` (via JOIN) for each affected row. | Matches spec-state-schema.md `mclaude-hosts` Key format. |
| KV write in `handleSysEvent` — machine | On CONNECT: `SELECT h.slug AS hslug, h.name, h.type, h.role, u.slug AS uslug FROM hosts h JOIN users u ON h.user_id = u.id WHERE h.public_key = $1 AND h.type = 'machine'`; upsert `{uslug}.{hslug}` with `{slug:hslug, type, name, role, online:true, lastSeenAt:RFC3339}`. On DISCONNECT: same lookup; write `online:false`, no `lastSeenAt` field. | Per spec-state-schema.md `$SYS` event table: CONNECT → `online=true`; DISCONNECT → `online=false`, `last_seen_at` not rewritten. |
| KV write in `handleSysEvent` — cluster | On Leafnode CONNECT: look up `clusterSlug` from `hosts WHERE public_key = $1 AND type = 'cluster' LIMIT 1`; then `SELECT u.slug AS uslug, h.slug AS hslug, h.name, h.type, h.role FROM hosts h JOIN users u ON h.user_id = u.id WHERE h.slug = $1 AND h.type = 'cluster'`; upsert KV for each row. Same lookup for DISCONNECT with `online:false`. | Cluster-shared liveness: all user rows with same host slug updated together. |
| Dev seed KV write | After seeding the dev user and `local` host, look up the `local` host row and write `{uslug}.local → {slug:'local', type:'machine', name:'Local Machine', role:'owner', online:true, lastSeenAt:RFC3339}` to `mclaude-hosts`. Use the user's `slug` (now populated) for the key prefix. | Dev local host has no NKey; `$SYS` CONNECT never fires for it. Dev seed pre-populates the KV so the SPA renders the host as online. This is dev-only; production uses `$SYS` events exclusively. |
| `HeartbeatMonitor` format fix | Change KV entry parse from `{ts: string}` to `{online: boolean}`. Set health map key to the entry key's second segment (the host slug). `isHealthy(hostSlug)` returns the `online` boolean directly — no time-delta check. | Matches `mclaude-hosts` KV value schema. Removes the 60-second threshold that was designed for periodic heartbeats (which ADR-0035 removed). Online/offline is now driven by `$SYS` events, which are immediate. |
| `SessionListVM` key fix | Change `heartbeatMonitor.isHealthy(p.id)` to `heartbeatMonitor.isHealthy(p.hostSlug ?? 'local')`. | `isHealthy` is now keyed by host slug; `p.hostSlug` falls back to `'local'` which is the slug of the default machine host. |
| Rename `HeartbeatMonitor` | Not in this ADR — rename to `HostStatusStore` per ADR-0045 is deferred until ADR-0045 moves to accepted. | Rename is cosmetic; the bug is behavioral. Keeping the class name avoids scope creep and a larger refactor on a draft ADR. |

## Impact

**Specs updated in this commit:**
- `docs/spec-state-schema.md` — `users` table: add missing `slug` column; `mclaude-hosts` Writers: note dev-seed write path.
- `docs/mclaude-control-plane/spec-control-plane.md` — startup step 9: note KV write; NATS KV Buckets `mclaude-hosts`: note dev-seed as secondary write trigger.

**Components implementing the change**:
- `mclaude-control-plane`: `db.go` (add `users.slug` migration + backfill), `auth.go` (`IssueUserJWT` uses slug, `LoginResponse` gains `UserSlug`), `projects.go` (add `ensureHostsKV`, `Server.hostsKV` field), `sys_subscriber.go` (KV writes on CONNECT/DISCONNECT with user slug JOIN), `main.go` (`seedDev` writes local host KV with user slug)
- `mclaude-web`: `stores/heartbeat-monitor.ts` (format + key type fix), `viewmodels/session-list-vm.ts` (host slug lookup)

## Scope

**In v1**:
- `users.slug` column added to DB migration with backfill
- `IssueUserJWT` uses user slug; login response gains `userSlug`
- `ensureHostsKV` added to control-plane startup
- `$SYS` CONNECT/DISCONNECT writes `mclaude-hosts` KV with `{uslug}.{hslug}` keys
- Dev seed writes `local` host KV with `online=true`
- `HeartbeatMonitor` reads `online: bool`, keys by host slug
- `SessionListVM` calls `isHealthy(p.hostSlug ?? 'local')`

**Explicitly deferred**:
- Rename `HeartbeatMonitor` → `HostStatusStore` (ADR-0045 scope, currently draft)
- `ensureSessionsKV` creation (separate gap, not related to this symptom)
- Project KV key migration to slug-based format (separate from liveness)
- JWT subject scope update to use user slug (JWT currently scoped to UUID-based subjects; subject rewrite is a separate ADR)
