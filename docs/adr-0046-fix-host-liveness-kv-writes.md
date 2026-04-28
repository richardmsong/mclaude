# ADR: Fix Host Liveness KV Writes

**Status**: accepted
**Status history**:
- 2026-04-28: accepted

## Overview

`mclaude-control-plane`'s `$SYS` subscriber updates Postgres `last_seen_at` on host CONNECT/DISCONNECT events but never writes to the `mclaude-hosts` KV bucket. The SPA's `HeartbeatMonitor` watches `mclaude-hosts` for `{online: bool}` entries but the bucket is empty, so every project always appears unhealthy and the "heartbeat stale" banner is permanently shown.

## Motivation

User `dev@mclaude.local` reports "default project heartbeat stale" banner on every page load. The control-plane specs (`spec-control-plane.md` lines 106–107, `spec-state-schema.md` lines 170–189) are unambiguous: on `$SYS.ACCOUNT.*.CONNECT`, the control plane must upsert `mclaude-hosts` KV with `online=true`; on DISCONNECT, set `online=false`. The code omits this entirely. Three concurrent bugs cause the failure:

1. **Missing KV writes in `sys_subscriber.go`**: CONNECT/DISCONNECT handlers update Postgres only; `mclaude-hosts` KV is never touched.
2. **`mclaude-hosts` bucket never created**: No `ensureHostsKV` call exists anywhere in the control-plane startup path. KV watchers get an error or empty stream.
3. **SPA format/key mismatch in `heartbeat-monitor.ts`**: The monitor reads `{ts: string}` and does time-delta health check, but `mclaude-hosts` KV values are `{online: bool, ...}`. Additionally, it extracts the key's second segment as "projectId" and calls `isHealthy(projectId)`, but callers pass a project UUID while the health map keys are host slugs.
4. **`session-list-vm.ts` wrong key type**: Calls `heartbeatMonitor.isHealthy(p.id)` (project UUID) instead of `heartbeatMonitor.isHealthy(p.hostSlug ?? 'local')` (host slug).
5. **Dev seed missing KV entry**: The `local` host auto-created by migration has no `public_key`, so no `$SYS` CONNECT event ever fires for it. Dev users never see an online host.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| `ensureHostsKV` placement | Add to `StartProjectsSubscriber` alongside existing `ensureProjectsKV` / `ensureJobQueueKV` calls | Consistent with existing bucket-creation pattern; guarantees bucket exists before `$SYS` events can fire. |
| KV write in `handleSysEvent` | On CONNECT (machine): `SELECT h.slug, h.name, h.type, h.role, u.id FROM hosts h JOIN users u ON h.user_id = u.id WHERE h.public_key = $1 AND h.type = 'machine'`; upsert `{userId}.{hostSlug}` with `{slug, type, name, role, online: true, lastSeenAt}`. On DISCONNECT: same lookup, write `online: false`, no `lastSeenAt` update. | Matches spec-state-schema.md KV format exactly. Uses `userId` (UUID) as key prefix since `users` table has no `slug` column — this matches the SPA's `userSlug = userId` fallback. |
| KV write in `handleSysEvent` (cluster) | On Leafnode CONNECT: look up slug by public_key, then `SELECT u.id, h.slug, h.name, h.type, h.role FROM hosts h JOIN users u ON h.user_id = u.id WHERE h.slug = $1 AND h.type = 'cluster'`; upsert KV for all matching rows. Same for DISCONNECT. | Matches spec-state-schema.md cluster liveness logic. |
| Dev seed KV write | After creating the `local` host (or finding existing), write `{userId}.local → {slug:'local', type:'machine', name:'Local Machine', role:'owner', online:true, lastSeenAt:now}` to `mclaude-hosts`. | Dev local host has no NKey; `$SYS` CONNECT never fires for it. Dev seed must pre-populate so the SPA renders the host as online without requiring a running daemon. This is dev-only; production uses `$SYS` events. |
| `HeartbeatMonitor` format fix | Change KV entry parse from `{ts: string}` to `{online: boolean}`. Set health map key to the entry key's second segment (the host slug). `isHealthy(hostSlug)` returns the `online` boolean directly — no time-delta check. | Matches `mclaude-hosts` KV value schema. Removes the 60-second threshold that was designed for periodic heartbeats (which ADR-0035 removed). Online/offline is now driven by `$SYS` events, which are immediate. |
| `SessionListVM` key fix | Change `heartbeatMonitor.isHealthy(p.id)` to `heartbeatMonitor.isHealthy(p.hostSlug ?? 'local')`. | `isHealthy` is now keyed by host slug; `p.hostSlug` falls back to `'local'` which is the slug of the default machine host. |
| Rename `HeartbeatMonitor` | Not in this ADR — rename to `HostStatusStore` per ADR-0045 is deferred until ADR-0045 moves to accepted. | Rename is cosmetic; the bug is behavioral. Keeping the class name avoids scope creep and a larger refactor on a draft ADR. |

## Impact

No spec changes — all specs already describe the desired behavior. This ADR records the bug and the code fix.

**Components implementing the change**:
- `mclaude-control-plane`: `projects.go` (add `ensureHostsKV`), `sys_subscriber.go` (add KV writes + lookup query), `main.go` (seedDev writes local host KV)
- `mclaude-web`: `stores/heartbeat-monitor.ts` (format + key type fix), `viewmodels/session-list-vm.ts` (host slug lookup)

## Scope

**In v1**:
- `ensureHostsKV` added to control-plane startup
- `$SYS` CONNECT/DISCONNECT writes `mclaude-hosts` KV for machine and cluster hosts
- Dev seed writes `local` host KV with `online=true`
- `HeartbeatMonitor` reads `online: bool`, keys by host slug
- `SessionListVM` calls `isHealthy(p.hostSlug ?? 'local')`

**Explicitly deferred**:
- Rename `HeartbeatMonitor` → `HostStatusStore` (ADR-0045 scope, currently draft)
- `ensureSessionsKV` creation (separate gap, not related to this symptom)
- Project KV slug migration (separate from liveness)
