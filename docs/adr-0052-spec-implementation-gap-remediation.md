# ADR: Spec-Implementation Gap Remediation

**Status**: accepted
**Status history**:
- 2026-04-29: draft
- 2026-04-29: accepted — paired with spec-common.md, spec-web.md

## Overview

Remediation of remaining spec-vs-implementation gaps found by 18 rounds of cross-component audits (`.agent/audits/impl-code-vs-spec-gaps-*`). Of ~89 original gaps, ~80 were fixed in prior commits. This ADR tracks the 9 remaining code gaps, 6 stale spec annotations, and related spec corrections.

## Motivation

Rounds 17–18 verified the current codebase and confirmed 9 code gaps still open (1 critical, 1 high, 2 medium, 5 low), plus 6 spec annotations that describe bugs already fixed in code.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Spec vs code source of truth | Spec for behavior (9 code gaps); code for 6 stale annotations | Per-gap verification determined which side is correct |
| Deferred items | Per-project cost grouping, React Router v6 migration, shared Go types | New features / standalone refactors, not bug fixes |
| Stale spec annotations | Update specs to match code | Code was fixed, spec text is outdated |

## Remaining Code Gaps

| Gap ID | Component | Description | Severity |
|--------|-----------|-------------|----------|
| **CROSS-1 / WEB-1** | mclaude-web + control-plane | **SPA project creation broken end-to-end.** SPA `subjProjectsCreate(uslug, hslug)` produces host-scoped subject `mclaude.users.{uslug}.hosts.{hslug}.api.projects.create`, but control-plane subscribes to user-scoped `mclaude.users.*.api.projects.create` (wildcard `*` matches one token only). Requests route to controller instead, which doesn't create Postgres rows or KV entries, and replies in incompatible format (`{ok, projectSlug}` vs SPA-expected `{id}`). | **Critical** |
| CP-04 | control-plane | `handleDeleteProjectHTTP` (DELETE /api/users/{uslug}/projects/{pslug}) does a bare SQL delete with **no NATS notification** to controller or SPA. Controller never tears down K8s resources; SPA watchers never see the deletion. | High |
| CP-6 | control-plane | `handleCreateProjectHTTP` creates Postgres row but skips KV write, provisioning request, and `projects.updated` broadcast. HTTP-created projects are invisible to SPA and have no session-agent pod. | Medium |
| R7-G2 | control-plane | `adminDeleteUser` notifies controllers via NATS but does **not** revoke the deleted user's NATS JWT. User can still connect to NATS until JWT expires naturally (8h). | Medium |
| CP-3 | control-plane | `adminStopSession` publishes to `mclaude.users.{uslug}.api.sessions.stop`, a subject no component subscribes to. Break-glass admin stop is non-functional. | Medium |
| SA-3 | session-agent | `publishExitLifecycle` quota event publishes `m.lastU5` (5h utilization %) as `outputTokensSinceSoftMark`. Semantic mismatch — field name implies token count, value is utilization percentage. | Low |
| SA-4 | session-agent | `MCLAUDE_LIFECYCLE` JetStream stream created with `MaxAge: 7 days` but spec says 30 days. | Low |
| R8-G6 | session-agent | `session_job_complete` lifecycle event still includes stale `prUrl` field. ADR-0034 says "No prUrl". | Low |
| R7-G10 | charts/mclaude | CP Helm chart injects `METRICS_PORT: 9091` env var but Go code serves `/metrics` on `ADMIN_PORT`. Port 9091 has no listener. | Low |

## Stale Spec Annotations

Code was fixed but spec still describes the old broken behavior:

| Spec File | Stale Annotation | Reality |
|-----------|-----------------|---------|
| spec-control-plane.md §`/auth/refresh` | "returns `s.natsURL` (internal broker URL)" | Code returns `s.natsWsURL` (correct) |
| spec-control-plane.md §`/readyz` | "returns 200 unconditionally" | Code checks Postgres connectivity |
| spec-control-plane.md §`IssueUserJWT` | "double-counts expiry (now + 16h)" | Code uses single `time.Now().Unix() + expirySecs` (correct) |
| spec-control-plane.md §HTTP project CRUD | "no HTTP handlers in the code" | Handlers exist and are wired (CREATE incomplete — see CP-6) |
| spec-web.md §KV Watch DEL/PURGE | "`_sessions.delete(sessionSlug)` is a no-op" | Code does proper slug→UUID lookup |
| spec-session-agent.md §MCLAUDE_LIFECYCLE | "session-agent code does not currently create this stream on startup" | Code creates it at `agent.go:129-139` |

## Component Changes

### mclaude-control-plane
- **Fix project creation NATS subscriber** (CROSS-1) — subscribe to host-scoped subject pattern so SPA project creation requests reach the control-plane. Must create Postgres row, KV entry, and reply with `{id}`.
- **Complete `handleCreateProjectHTTP`** (CP-6) — after Postgres insert, add KV write, provisioning request, and `publishProjectsUpdated` broadcast
- Add NATS notification in `handleDeleteProjectHTTP` (CP-04) — call `publishProjectsDeleteToHost` and `publishProjectsUpdated` after SQL delete
- Add NATS JWT revocation when deleting a user (R7-G2) — add user's public key to account revocation list
- Fix `adminStopSession` to publish to correct host-scoped session subject (CP-3)

### mclaude-session-agent
- Fix `outputTokensSinceSoftMark` to contain actual token count, not utilization percentage (SA-3)
- Fix `MCLAUDE_LIFECYCLE` stream MaxAge from 7 days to 30 days (SA-4)
- Remove stale `prUrl` field from `session_job_complete` lifecycle event (R8-G6)

### charts/mclaude
- Remove unused `METRICS_PORT` env var from control-plane Helm template (R7-G10)

### docs/ (spec cleanups)
- Remove 6 stale "Known bug" annotations in spec-control-plane.md, spec-web.md, spec-session-agent.md

## Data Model

No changes.

## Error Handling

- NATS JWT revocation failure on user deletion: log error, continue with deletion (JWT expires naturally in 8h as fallback)

## Security

- NATS JWT revocation on user deletion prevents deleted-user NATS access (currently users retain access until 8h JWT expiry)

## Impact

**Components implementing code changes:**
- mclaude-control-plane (5 fixes: CROSS-1, CP-6, CP-04, R7-G2, CP-3)
- mclaude-session-agent (3 fixes: SA-3, SA-4, R8-G6)
- charts/mclaude (1 fix: R7-G10)

**Spec cleanups:**
- `docs/mclaude-control-plane/spec-control-plane.md` — remove 4 stale annotations
- `docs/mclaude-web/spec-web.md` — remove 1 stale annotation
- `docs/mclaude-session-agent/spec-session-agent.md` — remove 1 stale annotation

## Scope

### In scope
- 9 code gaps (1 critical, 1 high, 3 medium, 4 low)
- 6 stale spec annotation cleanups

### Deferred (separate ADRs)
- Per-project cost grouping view — new feature
- Custom hash routing → React Router v6 migration — standalone refactor
- Shared Go types for KV state in mclaude-common — nice-to-have

## Open questions

(none)

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| SPA project creation end-to-end | SPA publishes host-scoped project create → control-plane receives, creates Postgres row + KV entry, replies with `{id}` | Create test user + host; teardown: delete project | mclaude-web, control-plane, controller-k8s |
| Project HTTP deletion propagation | DELETE /api/users/{uslug}/projects/{pslug} → controller tears down K8s resources + SPA removes project | Create test user + project; teardown: none (project deleted) | control-plane, controller-k8s, mclaude-web |
| Admin stop session routing | Admin stop session command → session-agent receives on correct host-scoped subject | Create test session; teardown: stop session | control-plane, session-agent |
| User deletion JWT revocation | Delete user via admin API, verify NATS connection rejected immediately (not after 8h expiry) | Create test user, obtain NATS credentials; teardown: none (user deleted) | control-plane |

## Implementation Plan

| Component | Gaps | Est. lines | Notes |
|-----------|------|------------|-------|
| mclaude-control-plane | CROSS-1, CP-6, CP-04, R7-G2, CP-3 | ~120-180 | Fix project creation subscriber, complete HTTP create, project delete notification, JWT revocation, admin stop subject |
| mclaude-session-agent | SA-3, SA-4, R8-G6 | ~15-25 | Fix outputTokensSinceSoftMark, lifecycle stream MaxAge, remove prUrl |
| charts/mclaude | R7-G10 | ~5-10 | Remove unused METRICS_PORT env var |
| docs/ specs | 6 stale annotations | ~30-50 | Clean up "Known bug" annotations in 3 spec files |

**Total estimated lines:** ~170-265
