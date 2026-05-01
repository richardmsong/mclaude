# ADR: Fix ADR-0054 Migration Gaps — SPA Bucket Mismatch, Controller Subject Mismatch, NaN Badges

**Status**: accepted
**Status history**:
- 2026-05-01: accepted

## Overview

Three cross-component bugs caused by incomplete ADR-0054 migration: (1) the SPA reads from the old shared KV buckets (`mclaude-sessions`, `mclaude-projects`) while the session-agent writes to per-user buckets (`mclaude-sessions-{uslug}`, `mclaude-projects-{uslug}`), (2) the K8s controller subscribes to the old ADR-0035 provisioning subjects but the CP publishes to ADR-0054 subjects, (3) turn usage badges display NaN because the SPA reads empty/non-existent KV entries from the wrong bucket.

## Motivation

After the ADR-0054 implementation landed, the deployment appeared healthy (pods running, API responding), but the SPA showed sessions stuck in "Updating" state, the intro session never appeared after a state wipe, and token/cost badges displayed NaN. Root cause: spec-web.md was never updated to reference per-user KV buckets, and the K8s controller was never updated to subscribe to the new host-scoped subject pattern. The dev-harness implemented exactly what each component's local spec said — but the local specs disagreed with each other.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| SPA KV bucket names | Change to `mclaude-sessions-{uslug}` and `mclaude-projects-{uslug}` | Matches what session-agent and CP actually write to (ADR-0054). The old shared buckets no longer receive writes. |
| SPA KV key format | Change to `hosts.{hslug}.projects.{pslug}.sessions.{sslug}` (sessions) and `hosts.{hslug}.projects.{pslug}` (projects) | Per-user bucket keys omit user slug (it's in the bucket name). Matches spec-state-schema.md. |
| SPA JWT permissions | Legacy JWT (`IssueUserJWTLegacy`) must include per-user bucket access: `$KV.mclaude-sessions-{uslug}.>`, `$KV.mclaude-projects-{uslug}.>`, `$JS.API.STREAM.INFO.KV_mclaude-sessions-{uslug}`, `$JS.API.STREAM.INFO.KV_mclaude-projects-{uslug}` | SPA currently gets legacy JWT; it must be able to open the new per-user buckets. |
| K8s controller subject | Add subscription to ADR-0054 host-scoped pattern `mclaude.hosts.{CLUSTER_SLUG}.>` alongside existing `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.>` | Dual subscription during migration. The CP dev-seed publishes to the new pattern. Eventually the old pattern can be removed. |
| NaN badges | Resolved by bug #1 — once the SPA reads from the correct bucket, usage data will be present | No separate fix needed. |
| Old shared buckets | Not deleted or migrated — they will eventually be garbage collected. CP dev-seed already purges stale entries. | Clean cut-over per ADR-0054. No data migration needed. |

## Impact

**Exhaustive spec inventory** — every spec that references the old shared bucket names or old provisioning subjects:

| Spec | References old pattern | Update needed |
|------|----------------------|---------------|
| `docs/mclaude-web/spec-web.md` | `mclaude-sessions`, `mclaude-projects` (lines 35-36, 136, 164-165) | YES — change to per-user buckets + new key format |
| `docs/mclaude-controller/spec-controller.md` | `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.>` (lines 47-54) | YES — add host-scoped subscription |
| `docs/mclaude-control-plane/spec-control-plane.md` | `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` (line 195) | YES — note dual-path (host-scoped for dev-seed, user-scoped for SPA compat) |

**Components implementing the change:**
- `mclaude-web` — SPA bucket names, key formats, JWT scope
- `mclaude-controller-k8s` — dual subscription (host-scoped + legacy user-scoped)
- `mclaude-control-plane` — `IssueUserJWTLegacy` adds per-user bucket permissions

## Scope

**In v1:**
- SPA uses per-user KV buckets with correct key format
- Legacy JWT includes per-user bucket permissions
- K8s controller subscribes to host-scoped subjects
- NaN badges resolved (consequence of bucket fix)

**Deferred:**
- Remove old ADR-0035 user-scoped subscription from K8s controller (requires verifying no other publishers use the old pattern)
- Remove `IssueUserJWTLegacy` entirely (requires SPA migration to client-provided NKey public key)
- Clean up old shared KV bucket references in older ADRs (informational, no runtime impact)

## Integration Test Cases

| Test case | What it verifies | Components exercised |
|-----------|------------------|----------------------|
| SPA reads session from per-user bucket | SPA's SessionStore.kvWatch opens `mclaude-sessions-{uslug}` and receives entries written by session-agent to the same bucket | mclaude-web, mclaude-session-agent |
| SPA reads project from per-user bucket | SPA's SessionStore.kvWatch opens `mclaude-projects-{uslug}` and receives entries written by CP to the same bucket | mclaude-web, mclaude-control-plane |
| K8s controller receives host-scoped provision | CP dev-seed publishes to `mclaude.hosts.local.users.dev.projects.default-project.create` and controller receives + creates MCProject CR | mclaude-control-plane, mclaude-controller-k8s |
| Legacy JWT grants per-user bucket access | After login, SPA NATS connection can open `mclaude-sessions-{uslug}` KV bucket without permission denial | mclaude-control-plane (JWT issuance), mclaude-web (NATS connection) |

### Cross-component interface tests
| Test case | What it verifies | Components exercised |
|-----------|------------------|----------------------|
| SPA and agent agree on bucket name | SPA opens `mclaude-sessions-dev`, session-agent writes to `mclaude-sessions-dev` — same bucket | mclaude-web, mclaude-session-agent |
| CP and controller agree on provisioning subject | CP publishes to `mclaude.hosts.local.users.dev.projects.*.create`, controller subscribes to `mclaude.hosts.local.>` — message delivered | mclaude-control-plane, mclaude-controller-k8s |
