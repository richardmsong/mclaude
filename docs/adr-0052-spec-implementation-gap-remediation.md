# ADR: Spec-Implementation Gap Remediation

**Status**: accepted
**Status history**:
- 2026-04-29: draft
- 2026-04-29: accepted — paired with spec-common.md, spec-web.md

## Overview

This ADR documents the comprehensive remediation of spec-vs-implementation gaps uncovered by 16 rounds of cross-component audits plus 5 full-component audits (`impl-code-vs-spec-gaps-*` and `impl-*-full-*` in `.agent/audits/`). The audits originally identified ~89 unique gaps across every component. Post-verification against the current codebase, **the vast majority (~85) have already been fixed** in recent commits. This ADR tracks the remaining open gaps and spec documentation corrections.

## Motivation

The audit series (rounds 1–16, April 28–29 2026) systematically compared every component's code against its spec. While these audits drove a major wave of fixes (committed between April 28–29), four code gaps and nine spec documentation issues remain. This ADR captures the remaining work to close the remediation effort completely.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Spec vs code source of truth | Spec for behavior (4 remaining code gaps); code for 9 stale spec entries | Per-gap verification determined which side is correct |
| Deferred items | SCIM 2.0, HTTP project CRUD, admin promote/delete cluster, React Router v6, leader election, shared Go types — all deferred to separate ADRs | These are new features or standalone refactors, not bug fixes from the audit |
| Stale spec corrections | Update specs to match code | All 9 cases are clearly stale spec text where the code is correct |
| Shell tracking | Include full two-phase implementation | User confirmed: implement as spec describes (already verified as fixed) |

## Audit Verification Summary

Thorough codebase verification was performed on 2026-04-29 against all ~70+ in-scope gaps. Results:

### Already Fixed (~85 gaps) — No Action Needed

All work streams WS-1 through WS-8 were verified. Key fixes already landed:

**WS-1 (UUID/Slug Systemic):** All 8 gaps FIXED — QuotaMonitor uses slugs, daemon dispatches correctly, KV lookups use slug keys, project creation sets slug + host_id, web sends hostSlug.

**WS-2 (JWT Security):** All 3 gaps FIXED — IssueUserJWT single-expiry correct, controller JWT scoped, gen-leaf-creds permissions set.

**WS-3 (Control-Plane Core):** 15 of 17 gaps FIXED — provisioning rollback, correct NATS subject, OAuth publish, admin endpoints, slug blocklist, auth middleware, etc.

**WS-4 (Session-Agent):** 15 of 16 gaps FIXED — lifecycle payloads complete, NATS reconnect infinite, crash auto-restart, shell tracking, state constants, etc.

**WS-5 (Web SPA):** All 10 gaps FIXED — KV DEL handler correct, reconnect with JWT, requestId in payloads, cost tracking, lifecycle processing, etc.

**WS-6 (Controller-K8s):** All 7 gaps FIXED — provision waits for Ready, Pending phase set, RBAC ownership fixed, CLAUDE_CODE_TMPDIR injected, etc.

**WS-7 (Helm):** 2 of 3 gaps FIXED — Secret prerequisite resolved, undocumented keys/vars documented.

**WS-8 (Common):** All 4 gaps FIXED — subject helpers added, dead code removed.

### Remaining Open Gaps (4 code + 9 spec docs)

#### Code Gaps

| Gap ID | Component | Description | Severity |
|--------|-----------|-------------|----------|
| **CROSS-1 / WEB-1** | mclaude-web + control-plane | **SPA project creation broken end-to-end.** SPA `subjProjectsCreate(uslug, hslug)` now produces host-scoped subject `mclaude.users.{uslug}.hosts.{hslug}.api.projects.create`, but control-plane subscribes to user-scoped `mclaude.users.*.api.projects.create` (wildcard `*` matches one token only). Requests route to controller instead, which doesn't create Postgres rows or KV entries, and replies in incompatible format (`{ok, projectSlug}` vs SPA-expected `{id}`). | **Critical** |
| CP-04 (partial) | control-plane | `handleDeleteProjectHTTP` (DELETE /api/users/{uslug}/projects/{pslug}) does a bare SQL delete with **no NATS notification** to controller or SPA. Controller never tears down the project's K8s resources; SPA watchers never see the deletion. Host deletion and admin user deletion are properly notified — only this HTTP endpoint is missing the publish. | High |
| R7-G2 (partial) | control-plane | `adminDeleteUser` notifies controllers via NATS but does **not** revoke the deleted user's NATS JWT. User can still connect to NATS until JWT expires naturally (8h). Requires adding the user's public key to the NATS account revocation list. | Medium |
| CP-3 (updated) | control-plane | `adminStopSession` publishes to `mclaude.users.{uslug}.api.sessions.stop`, a subject no component subscribes to. Session-agent subscribes to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.delete`. Break-glass admin stop is non-functional via NATS. | Medium |
| SA-3 (new) | session-agent | `publishExitLifecycle` quota event publishes `m.lastU5` (5h utilization %) as `outputTokensSinceSoftMark`. Semantic mismatch — field name implies token count, value is utilization percentage. | Low |
| R8-G6 | session-agent | `session_job_complete` lifecycle event still includes stale `prUrl` field. Spec (ADR-0034) explicitly says "No prUrl". | Low |
| R7-G10 | charts/mclaude | CP Helm chart injects `METRICS_PORT: 9091` env var but Go code serves `/metrics` on `ADMIN_PORT`. Port 9091 has no listener. Either remove the env var from the chart or add a metrics listener. | Low |

#### Spec Documentation Corrections (WS-9)

7 of 9 corrections were already applied in prior commits. 2 remaining corrections applied in this ADR commit:

| Gap ID | Spec File | Description | Status |
|--------|-----------|-------------|--------|
| R15-GAP3 | spec-common.md | Added `UserHostProjectAPISessionsRestart` to subject helpers list | **Fixed in this commit** |
| R15-GAP3 (web) | spec-web.md | Removed stale note claiming Go helper is missing | **Fixed in this commit** |
| R14-GAP1 | spec-helm.md | Stale "Known bug" annotation | Already fixed |
| R15-GAP2 | spec-state-schema.md | `allowedTools` in sessions.create | Already fixed |
| Gap 4 | spec-common.md | ADR reference correction | Already fixed |
| Gap 6 | spec-common.md | Zero-dependency claim | Already fixed |
| Gap 10 | spec-controller.md | "Does not mint JWTs" | Already fixed |
| R14-GAP2 | spec-control-plane.md | PROVIDER_SECRET_{ID} docs | Already fixed |
| R14-GAP3 | spec-helm.md | Config values in spec table | Already fixed |
| R14-GAP4 | spec-control-plane.md | init-keys env vars | Already fixed |

## Component Changes

### mclaude-control-plane
- **Fix project creation NATS subscriber** (CROSS-1) — subscribe to host-scoped subject `mclaude.users.*.hosts.*.api.projects.create` (using `>` wildcard or multi-token pattern) so SPA project creation requests reach the control-plane, OR add a second subscriber alongside the existing one. Must create Postgres row, KV entry, and reply with `{id}`.
- Add NATS notification in `handleDeleteProjectHTTP` (CP-04) — call `publishProjectsDeleteToHost` and `publishProjectsUpdated` after SQL delete
- Add NATS JWT revocation when deleting a user (R7-G2) — add user's public key to account revocation list
- Fix `adminStopSession` to publish to correct host-scoped session subject that session-agent actually subscribes to (CP-3)

### mclaude-session-agent
- Fix `outputTokensSinceSoftMark` to contain actual token count, not utilization percentage (SA-3)
- Remove stale `prUrl` field from `session_job_complete` lifecycle event payload (R8-G6)

### charts/mclaude
- Remove unused `METRICS_PORT` env var from control-plane Helm template, or add a dedicated metrics listener on that port (R7-G10)

### docs/ (spec corrections)
- Update 5 spec files with 9 corrections per WS-9 table above

## Data Model

No changes.

## Error Handling

- NATS JWT revocation failure on user deletion: log error, continue with deletion (JWT expires naturally in 8h as fallback)

## Security

- NATS JWT revocation on user deletion prevents deleted-user NATS access (currently users retain access until 8h JWT expiry)

## Impact

**Specs updated in this commit:**
- `docs/mclaude-common/spec-common.md` — added `UserHostProjectAPISessionsRestart` to subject helpers list
- `docs/mclaude-web/spec-web.md` — removed stale note claiming Go helper is missing

**Components implementing code changes:**
- mclaude-control-plane (2 fixes), mclaude-session-agent (1 fix), charts/mclaude (1 fix)

## Scope

### In scope
- 4 remaining code gaps (2 control-plane, 1 session-agent, 1 helm)
- 9 spec documentation corrections across 5 spec files

### Already completed (verified 2026-04-29)
- ~85 code gaps across all components — fixed in commits between April 28–29

### Explicitly deferred (separate ADRs)
- SCIM 2.0 endpoints — new feature (note: evaluation found this was actually implemented)
- HTTP project CRUD endpoints — new feature
- `DELETE /admin/clusters/{cslug}` — new feature (note: evaluation found this was actually implemented)
- `POST /admin/users/{uslug}/promote` — new feature (note: evaluation found this was actually implemented)
- Per-project cost grouping view — new feature
- Custom hash routing → React Router v6 migration — standalone refactor
- Leader election for controller-k8s — deployment concern (note: evaluation found this was actually implemented)
- Shared Go types for KV state in mclaude-common — nice-to-have, not a bug

## Open questions

(none — all resolved)

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
| mclaude-control-plane | CROSS-1, CP-04, R7-G2, CP-3 | ~80-120 | Fix project creation subscriber, project delete notification, JWT revocation, admin stop subject |
| mclaude-session-agent | SA-3, R8-G6 | ~10-20 | Fix outputTokensSinceSoftMark semantic, remove prUrl field |
| charts/mclaude | R7-G10 | ~5-10 | Remove unused METRICS_PORT env var |
| docs/ specs | 2 corrections | ~10-15 | Applied in initial ADR commit |

**Total estimated lines:** ~105-165
**Total estimated tokens:** ~50-80k
