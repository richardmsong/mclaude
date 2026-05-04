# Design Evaluation: ADR-0052 Spec-Implementation Gap Remediation

**Date:** 2026-04-29
**Evaluator:** design-evaluator subagent
**ADR:** `/Users/rsong/work/mclaude/docs/adr-0052-spec-implementation-gap-remediation.md`

## Verdict

**CLEAN — no blocking gaps found.**

The ADR is a well-structured, comprehensive remediation plan. Phasing, dependencies, and fix directions are sound. No issues prevent implementation from proceeding correctly.

## Advisory Findings (Non-Blocking)

### 1. Most gaps already implemented in codebase

Cross-referencing 12+ high-severity gaps against the current codebase shows nearly all have already been fixed in recent commits (`99f0e03` through `aee179d`). Verified:

| Gap | Status | Evidence |
|-----|--------|----------|
| R8-G5 (QuotaMonitor slug) | ✅ Fixed | `quota_monitor.go`: `newQuotaMonitor` takes `userSlug`, subscribes to `"mclaude.users." + userSlug + ".quota"` |
| R8-G2 (CreateProjectWithIdentity slug) | ✅ Fixed | `db.go`: Computes `projectSlug`, resolves `hostID`, inserts both |
| R7-G5 (IssueUserJWT double-expiry) | ✅ Fixed | `nkeys.go`: Single `time.Now().Unix() + expirySecs`, `IssuerAccount` set |
| R8-G7 (KV DEL ghost sessions) | ✅ Fixed | `session-store.ts`: Slug-to-UUID lookup on DEL operation |
| R9-GAP6 (session_job_paused r5) | ✅ Fixed | `quota_monitor.go`: Includes `r5`, `pausedVia`, `outputTokensSinceSoftMark` |
| R10-GAP3 (createProject hostSlug) | ✅ Fixed | `session-list-vm.ts`: `hostSlug: hslug` in payload |
| R11-GAP1 (NATS reconnect) | ✅ Fixed | `main.go:289-290`: `MaxReconnects(-1)`, `RetryOnFailedConnect(true)` |
| Controller JWT (Gap 2) | ✅ Fixed | `main.go:173-181`: Permissions scoped to cluster slug |
| CP-02 (provisioning rollback) | ✅ Fixed | `projects.go:211-216`: Sets `status='failed'` on failure |
| R12-GAP1 (auto-restart crash) | ✅ Fixed | `agent.go:1287+`: Crash watcher implemented |
| CP-01 (DATABASE_DSN fatal) | ✅ Fixed | `main.go:74`: `logger.Fatal().Msg("DATABASE_DSN required")` |
| CP-11 (OAuth oauth_id) | ✅ Fixed | `providers.go:410-413`: Sets `oauth_id` on first OAuth login |
| R9-GAP9 (slug blocklist) | ✅ Fixed | `db.go:38+`, `hosts.go:506+`: Reserved-word validation |
| R9-GAP5 (session_job_cancelled) | ✅ Fixed | `daemon_jobs.go:874-878`: Lifecycle event published |

**Recommendation:** Update ADR status from `draft` to `accepted` (or `implemented`) to reflect that the work described has been completed.

### 2. Four "deferred" items were actually implemented

The ADR's "Explicitly deferred" section lists 8 items. Four of them have been implemented:

| Deferred Item | Actual Status | Evidence |
|---------------|---------------|----------|
| SCIM 2.0 endpoints (KNOWN-20) | ✅ Implemented | `scim.go`: Full SCIM 2.0 `/scim/v2/Users` endpoints with GET, POST, PATCH, DELETE |
| `POST /admin/users/{uslug}/promote` (KNOWN-10) | ✅ Implemented | `admin.go:481-504`: `adminPromoteUser` handler |
| `DELETE /admin/clusters/{cslug}` (KNOWN-21) | ✅ Implemented | `admin.go:456-468`: `adminDeleteCluster` handler |
| Leader election for controller-k8s (Gap 6) | ✅ Implemented | `main.go:66-75`: `LeaderElection: true` with configurable namespace |

**Impact:** None (conservative — ADR under-promises). The deferred list should be updated to remove these items.

### 3. NATS JWT revocation on user deletion still missing

Gap R7-G2 specifies "DELETE /admin/users missing NATS JWT revocation & controller notification". The code at `admin.go:190+` publishes project delete notifications and user-level `projects.updated` events, but there is no NATS JWT revocation call. In NATS operator mode, revoking a user JWT typically requires adding the user's public key to the account's revocation list.

**Impact:** Non-blocking. User JWTs expire naturally (8h configured lifetime). Deleted users retain NATS access until JWT expiry. The security section of the ADR correctly identifies this as needed but the implementation may require NATS operator-level changes beyond the control-plane.

### 4. Integration test cases could be broader

The 6 integration test cases cover the most critical paths but miss:
- Controller JWT permission scoping verification (WS-2 Gap 2)
- NATS reconnect on BYOH disconnect (WS-4 R11-GAP1)
- Auto-restart on Claude process crash (WS-4 R12-GAP1)
- Two-phase shell tracking / graceful shutdown (WS-4 R9-GAP3/R9-GAP4)

**Impact:** Non-blocking. Unit tests exist for all of these in the component test files. Integration tests would provide additional confidence but aren't required for the remediation to proceed.

## Internal Consistency Check

| Check | Result |
|-------|--------|
| Gaps in work streams but also deferred? | ✅ No overlap — all WS items are distinct from deferred items |
| Component changes match work stream tables? | ✅ Consistent — each component section lists exactly the gaps from its work streams |
| Cross-component dependencies in phasing? | ✅ Phase 1 (common helpers) → Phase 2 (consumers) is correct |
| CP .provision subject consistent with controller-k8s? | ✅ Both reference `api.projects.provision` subject |

## Completeness Check

| Check | Result |
|-------|--------|
| Phase 1 → Phase 2 dependency | ✅ Correctly captured |
| Phase 3 independence | ✅ Spec-only changes, no code deps |
| Cross-component WS-1 coordination | ✅ UUID/slug fixes in SA, CP, and web are independent — each fixes its own call sites |
| Estimated line counts reasonable? | ✅ Consistent with scope of changes per component |
