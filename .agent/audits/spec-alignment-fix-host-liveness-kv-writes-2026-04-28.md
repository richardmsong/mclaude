## Run: 2026-04-28T00:00:00Z

ADR: `docs/adr-0046-fix-host-liveness-kv-writes.md`  
Status: accepted  
Specs evaluated: `docs/spec-state-schema.md`, `docs/mclaude-control-plane/spec-control-plane.md`

---

## Phase 1 — ADR → Spec Forward Pass

| ADR (decision row) | ADR text | Spec location | Verdict | Direction | Notes |
|--------------------|----------|---------------|---------|-----------|-------|
| `users.slug` migration | "Add `ALTER TABLE users ADD COLUMN IF NOT EXISTS slug TEXT UNIQUE NOT NULL DEFAULT ''`; backfill with email local-part slugify" | spec-state-schema.md — `users` table: `slug TEXT UNIQUE NOT NULL`, description covers derivation from email_local_part | REFLECTED | — | Spec has the column and charset; backfill detail is implementation-only |
| JWT slug claim / LoginResponse | "Update `IssueUserJWT` to put the user slug (not UUID) in `claims.Name`. Login response gains `UserSlug`." | spec-state-schema.md — Login Response: `"user": { "id": "uuid", "slug": "alice-gmail" }`; spec-control-plane.md — full Login Response includes user object | REFLECTED | — | User slug is already in spec login response via `user.slug`. `claims.Name` is an internal JWT field not requiring spec documentation; behavioral result (slug available in login response) is covered. |
| `ensureHostsKV` placement | "Add to `StartProjectsSubscriber` alongside existing `ensureProjectsKV` / `ensureJobQueueKV` calls; store as `Server.hostsKV`." | spec-control-plane.md — Startup step 7: "Ensures KV buckets exist (`mclaude-projects`, `mclaude-hosts`, `mclaude-sessions`, `mclaude-job-queue`)"; NATS KV Buckets: "`mclaude-hosts` -- created by `ensureHostsKV`" | REFLECTED | — | Both spec files explicitly name `ensureHostsKV` and its placement in the startup sequence. |
| KV write key format | "`{uslug}.{hslug}` — uslug looked up via JOIN on `users.slug`" | spec-state-schema.md — `mclaude-hosts` Key format: `{uslug}.{hslug}` | REFLECTED | — | Exact match. |
| Machine CONNECT KV write | "On CONNECT: upsert `{uslug}.{hslug}` with `{slug:hslug, type, name, role, online:true, lastSeenAt:RFC3339}`" | spec-state-schema.md — `$SYS` event table: "CONNECT | Client → update `last_seen_at`; upsert `mclaude-hosts` KV `{uslug}.{hslug}` with `online=true`"; value schema includes all fields | REFLECTED | — | Full field set on CONNECT write matches KV value schema. |
| Machine DISCONNECT KV write — `online:false`, no `lastSeenAt` field | "On DISCONNECT: same lookup; write `online:false`, no `lastSeenAt` field." | spec-state-schema.md — `$SYS` DISCONNECT row: "Set KV `online=false`... `last_seen_at` is **not** rewritten on disconnect"; `mclaude-hosts` value schema shows `lastSeenAt: RFC3339` as a regular field with no note about it being absent on DISCONNECT writes | PARTIAL | SPEC→FIX | Spec text correctly says `last_seen_at` is not rewritten, but the KV value schema does not clarify that DISCONNECT writes omit `lastSeenAt` entirely from the payload (vs. preserving the previously-written value). A developer reading only the value schema would not know the DISCONNECT write payload is `{online: false}` only. The KV value schema comment or the Writers section should note that DISCONNECT writes contain only `{online: false}`. |
| Cluster/Leafnode CONNECT KV write | "On Leafnode CONNECT: look up clusterSlug; upsert KV for each user row with `online=true`." | spec-state-schema.md — `$SYS` event table: "CONNECT | Leafnode → update `last_seen_at` for all rows; upsert `mclaude-hosts` KV `{uslug}.{slug}` with `online=true` for each user row"; spec-control-plane.md — Subscribes section mirrors this | REFLECTED | — | Cluster-shared liveness write path fully described. |
| Cluster/Leafnode DISCONNECT KV write | "DISCONNECT with `online:false`" | spec-state-schema.md — "DISCONNECT | Leafnode → Set KV `online=false` for all matching cluster rows"; spec-control-plane.md — DISCONNECT subscription description | REFLECTED | — | Same omission concern as machine DISCONNECT, but this row shares the PARTIAL gap noted above. |
| Dev seed KV write | "Write `{uslug}.local → {slug:'local', type:'machine', name:'Local Machine', role:'owner', online:true, lastSeenAt:RFC3339}` to `mclaude-hosts`; dev-only; `$SYS` never fires for the `local` host." | spec-state-schema.md — `mclaude-hosts` Writers: "Secondary path (dev only): on `DEV_SEED=true` startup, control-plane writes the bootstrap user's `local` machine host entry with `online=true` because the auto-created `local` host has no NKey"; spec-control-plane.md — startup step 9 includes the dev-seed KV write | REFLECTED | — | Both specs capture the dev-seed write path explicitly. |
| `HeartbeatMonitor` format fix | "Change parse from `{ts: string}` to `{online: boolean}`. Key by host slug (second segment). `isHealthy` returns `online` bool directly — no time-delta." | spec-state-schema.md — `mclaude-hosts` value: `"online": true` (bool field); Readers: "SPA (KV watch for host list + status)" | REFLECTED | — | The spec defines `online: bool` in the KV value. SPA internal class behavior is not a spec-level concern; format alignment with spec is correct. |
| `SessionListVM` key fix | "Change `isHealthy(p.id)` to `isHealthy(p.hostSlug ?? 'local')`" | N/A — SPA ViewModel internal implementation | N/A | — | Not a spec-level decision; no spec statement needed. SPA passes host slug consistent with the `{uslug}.{hslug}` KV key scheme already in spec. |
| `HeartbeatMonitor` rename | "Not in this ADR — rename to `HostStatusStore` deferred until ADR-0045 accepted." | N/A — explicitly deferred | N/A | — | Skip per algorithm rules on deferred scope items. |

---

### Summary

- Reflected: 9
- Gap: 0
- Partial: 1

---

## Result

PARTIAL [SPEC→FIX]: "On DISCONNECT: same lookup; write `online:false`, no `lastSeenAt` field." → The `mclaude-hosts` KV value schema in `spec-state-schema.md` shows `lastSeenAt: RFC3339` as a regular field with no annotation that DISCONNECT writes omit it. The surrounding prose correctly states `last_seen_at` is not rewritten on disconnect, but the value schema itself (or the Writers note) should explicitly state that DISCONNECT payloads contain only `{online: false}` — otherwise a developer reading the schema alone would write the full value object on disconnect, clobbering the last-known `lastSeenAt`. (`spec-state-schema.md`, `mclaude-hosts` section, value schema or Writers note)

---

## Run: 2026-04-28T00:01:00Z (Round 3 — verify previous gap resolved)

ADR: `docs/adr-0046-fix-host-liveness-kv-writes.md`  
Status: accepted  
Specs evaluated: `docs/spec-state-schema.md`, `docs/mclaude-control-plane/spec-control-plane.md`

### Re-evaluation of Round 2 PARTIAL gap

**Previous finding**: `spec-state-schema.md` `mclaude-hosts` Writers note did not explicitly state that DISCONNECT writes contain only `{online: false}` — the value schema alone could mislead a developer into writing the full payload on DISCONNECT.

**Current spec text** (`spec-state-schema.md`, `mclaude-hosts` Writers):
> "`$SYS.ACCOUNT.*.DISCONNECT` writes only `{online: false}` — all other fields (`slug`, `type`, `name`, `role`, `lastSeenAt`) are preserved from the previous entry (read-modify-write: fetch current value, set `online=false`, re-put). `lastSeenAt` is **not** rewritten on disconnect."

The gap is fully resolved. The spec now explicitly names the partial-write contract and the read-modify-write pattern.

### Full forward pass (Round 3)

| ADR (decision row) | ADR text | Spec location | Verdict | Direction | Notes |
|--------------------|----------|---------------|---------|-----------|-------|
| `users.slug` migration | "Add column; backfill email local-part slugify" | spec-state-schema.md — `users.slug TEXT UNIQUE NOT NULL` with derivation description | REFLECTED | — | Column, constraints, and charset present. |
| JWT slug claim / LoginResponse | "Put user slug in `claims.Name`; login response gains `UserSlug`" | spec-state-schema.md — Login Response `"user": { "id": "uuid", "slug": "alice-gmail" }`; spec-control-plane.md — Login Response returns user object | REFLECTED | — | User slug present in login response. |
| `ensureHostsKV` placement | "Add alongside `ensureProjectsKV`/`ensureJobQueueKV`" | spec-control-plane.md — startup step 7 lists `mclaude-hosts`; NATS KV Buckets: "created by `ensureHostsKV`" | REFLECTED | — | |
| KV write key format | "`{uslug}.{hslug}` via JOIN on `users.slug`" | spec-state-schema.md — `mclaude-hosts` Key format: `{uslug}.{hslug}` | REFLECTED | — | |
| Machine CONNECT KV write | "Upsert full value with `online=true, lastSeenAt=RFC3339`" | spec-state-schema.md — "CONNECT writes the full value object (`slug`, `type`, `name`, `role`, `online=true`, `lastSeenAt=now`)" | REFLECTED | — | |
| Machine DISCONNECT KV write | "`online:false` only, no `lastSeenAt` rewrite" | spec-state-schema.md — "DISCONNECT writes only `{online: false}` — all other fields preserved (read-modify-write). `lastSeenAt` **not** rewritten on disconnect." | REFLECTED | — | Round 2 PARTIAL gap resolved. |
| Cluster/Leafnode CONNECT KV write | "Upsert KV for each user row with `online=true`" | spec-state-schema.md — Leafnode CONNECT row; spec-control-plane.md — `$SYS` subscription Leafnode branch | REFLECTED | — | |
| Cluster/Leafnode DISCONNECT KV write | "`online=false` for all matching cluster rows" | spec-state-schema.md — Leafnode DISCONNECT row; spec-control-plane.md — DISCONNECT subscription | REFLECTED | — | Shares the now-resolved read-modify-write clarification. |
| Dev seed KV write | "`{uslug}.local → {online:true, ...}`; dev-only because `local` has no NKey" | spec-state-schema.md — "Secondary path (dev only): ... `local` host has no NKey and will never trigger `$SYS`"; spec-control-plane.md — startup step 9 | REFLECTED | — | |
| `HeartbeatMonitor` format fix | "Parse `{online: boolean}`, key by host slug, no time-delta" | spec-state-schema.md — `mclaude-hosts` value: `"online": true` (bool); KV Readers: "SPA (KV watch for host list + status)" | REFLECTED | — | Format aligned; SPA internals not spec-level. |
| `SessionListVM` key fix | "`isHealthy(p.hostSlug ?? 'local')`" | N/A — SPA ViewModel internal | N/A | — | No spec statement needed. |
| Rename deferred | "Defer to ADR-0045" | N/A — explicitly deferred | N/A | — | Skipped. |

### Summary

- Reflected: 9
- Gap: 0
- Partial: 0 (Round 2 PARTIAL resolved)

## Result

CLEAN — 9 ADR decisions reflected in spec, 0 gaps, 0 partials. The single PARTIAL finding from Round 2 (`spec-state-schema.md` DISCONNECT write payload ambiguity) has been resolved: the spec now explicitly states DISCONNECT writes only `{online: false}` via read-modify-write, preserving all other fields.
