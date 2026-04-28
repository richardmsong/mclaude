# ADR: Fix User JWT Missing mclaude-hosts KV Permission

**Status**: accepted
**Status history**:
- 2026-04-28: accepted

## Overview

The SPA's NATS user JWT (`UserSubjectPermissions`) was missing `$KV.mclaude-hosts.{uslug}.>` from SubAllow. Every `HeartbeatMonitor.start()` call silently failed — NATS rejected the KV watch subscription — so the health map stayed empty and the "heartbeat stale" banner was permanent even after ADR-0046 wrote correct data into the bucket. Additionally, `IssueUserJWT` was not passing `userSlug` to `UserSubjectPermissions`, causing the KV project/session permissions to use the user UUID as the key prefix instead of the slug, mismatching the slug-keyed buckets from ADR-0046.

## Motivation

User `dev@mclaude.local` continued to see "⚠ Agent down: Default Project — heartbeat stale" after ADR-0046 deployed. Debugging showed:
1. Control plane correctly wrote `dev.local → {online: true}` to `mclaude-hosts` KV (ADR-0046).
2. SPA called `kvWatch('mclaude-hosts', 'dev.*', ...)` — but the NATS user JWT did not permit subscription to `$KV.mclaude-hosts.*`.
3. NATS silently dropped the subscription. `_health` map stayed empty. `isHealthy('local')` returned `false`.

Root cause: ADR-0004 (`adr-0004-multi-laptop.md` lines 420–424) explicitly lists `$KV.mclaude-hosts.>` in the user JWT SubAllow, but this was never added to `UserSubjectPermissions` in `nkeys.go`.

Secondary cause: `IssueUserJWT(userID, accountKP, expirySecs)` passed only the UUID to `UserSubjectPermissions`, so the KV scoped permissions (projects, sessions, hosts) all used the UUID as the key prefix. After ADR-0046 wrote KV keys with the slug prefix (`dev.local`), none of the slug-keyed KV data was reachable under UUID-scoped permissions.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Add `$KV.mclaude-hosts.{uslug}.>` to SubAllow | `UserSubjectPermissions(userID, userSlug string)` gains `fmt.Sprintf("$KV.mclaude-hosts.%s.>", userSlug)` in SubAllow. | ADR-0004 mandates this permission. Without it NATS rejects the SPA's KV watch on `mclaude-hosts`. |
| Add `$JS.API.DIRECT.GET.>` to SubAllow | `UserSubjectPermissions` adds `$JS.API.DIRECT.GET.>` to SubAllow. | ADR-0004 line 424 lists this as required for direct KV reads. Without it, NATS KV direct-get operations are rejected. |
| `IssueUserJWT` gains `userSlug` parameter | Signature changes to `IssueUserJWT(userID, userSlug string, accountKP, expirySecs)`. `claims.Name` stays as `userID` (UUID) for `authMiddleware` DB lookups. `UserSubjectPermissions` is called with both `(userID, userSlug)`. | Separates the concern: JWT identity (`claims.Name = UUID`) vs. KV key scoping (`userSlug`). `authMiddleware` uses `claims.Name` for `GetUserByID` — must remain UUID. SPA reads `userSlug` from `LoginResponse.UserSlug` (ADR-0046). |
| Existing project/session KV permissions stay UUID-scoped | `$KV.mclaude-projects.{userID}.>` and `$KV.mclaude-sessions.{userID}.>` remain as-is. | Project and session KV keys were not migrated to slug-based format in ADR-0046. Changing KV permissions without migrating the KV data would break existing watches. Deferred to a future slug-migration ADR. |

## Impact

**Specs updated in this commit:**
- `docs/mclaude-control-plane/spec-control-plane.md` — Authentication section: add `$KV.mclaude-hosts.{uslug}.>` and `$JS.API.DIRECT.GET.>` to the user JWT SubAllow list; document the `userSlug` parameter on `IssueUserJWT`.

**Components implementing the change:**
- `mclaude-control-plane`: `nkeys.go` (new `userSlug` param on `UserSubjectPermissions` + `IssueUserJWT`, add missing SubAllow entries), `auth.go` (callers pass `user.Slug`).

## Scope

**In v1:**
- `$KV.mclaude-hosts.{uslug}.>` added to SubAllow
- `$JS.API.DIRECT.GET.>` added to SubAllow
- `IssueUserJWT` passes `userSlug` to `UserSubjectPermissions`

**Explicitly deferred:**
- Migrate project/session KV keys from UUID to slug prefix (separate ADR)
- Full host-scoped JWT per spec (`mclaude.users.{uslug}.hosts.{hslug}.>`) — larger refactor, separate ADR
