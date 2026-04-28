# ADR: Fix SessionStore Slug Regression

**Status**: accepted
**Status history**:
- 2026-04-28: accepted

## Overview

ADR-0046 introduced `users.slug` and changed `LoginResponse.UserSlug` from undefined to the actual user slug (e.g. `"dev"`). The SPA's `SessionStore` uses `userSlug` as the key prefix for watching both `mclaude-projects` and `mclaude-sessions` KV buckets. These buckets still use UUID-prefixed keys (`{userUUID}.{projectUUID}`) — they were not migrated to slug format. After ADR-0046, `userSlug = "dev"`, so `SessionStore` watches `dev.*` but finds no data (all entries are under `{UUID}.*`). The dashboard shows "No Sessions" and no projects.

## Motivation

After logging in fresh (post-ADR-0046 deploy), the dashboard showed "No Sessions" with no project list. The `SessionStore` constructor receives `userSlug = "dev"` and calls `kvKeyProjectsForUser("dev")` = `"dev.*"`. The `mclaude-projects` KV has a single entry keyed `{UUID}.{UUID}`. Watch pattern `dev.*` does not match `{UUID}.{UUID}`. Zero projects returned.

The `mclaude-sessions` bucket has the same problem. `mclaude-hosts` is the only bucket that correctly uses slug-keyed entries (written by ADR-0046's `seedDev`).

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| `SessionStore` uses `userId` (UUID) for KV watches | Pass `authState.userId` (UUID) as the `userSlug` argument to `SessionStore`. `HeartbeatMonitor` continues to receive `authState.userSlug` (slug). | `mclaude-projects` and `mclaude-sessions` KV keys are UUID-prefixed. Changing the watch prefix to slug without migrating the KV data silently returns empty results. The UUID prefix must match the data until the buckets are migrated. |
| `mclaude-hosts` KV watch keeps using `userSlug` | `HeartbeatMonitor` keeps receiving `userSlug` (`"dev"`). | `mclaude-hosts` entries are slug-keyed (`dev.local`) per ADR-0046. Correct as-is. |
| No spec change | Specs already document the intended slug-based key format everywhere. The UUID-based interim state is an implementation artifact, not a design decision. | This ADR records the regression and interim fix only. |

## Impact

No spec changes.

**Components implementing the change:**
- `mclaude-web`: `App.tsx` — pass `authState.userId` (not `authState.userSlug`) as `userSlug` to `SessionStore` constructor.

## Scope

**In v1:** `SessionStore` watches use `userId` (UUID prefix), matching existing KV data.

**Explicitly deferred:** Full project/session KV key migration to slug format (requires session-agent + control-plane changes, separate ADR).
