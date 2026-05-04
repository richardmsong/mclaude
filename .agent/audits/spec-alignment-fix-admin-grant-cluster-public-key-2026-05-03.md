## Run: 2026-05-03T00:00:00Z

# Spec Alignment Audit: ADR-0086 — Fix adminGrantCluster copying cluster public_key to per-user hosts row

**ADR:** `docs/adr-0086-fix-admin-grant-cluster-public-key.md`
**Spec checked:** `docs/mclaude-control-plane/spec-control-plane.md`

## Phase 1 — ADR → Spec Forward Pass

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Decisions table | "`public_key` in per-user row — NULL — do not copy from owner row" | spec-control-plane.md:121 — `POST /admin/clusters/{cslug}/grants` — "Creates a new `hosts` row for that user with `public_key = NULL` (the cluster's NKey belongs only to the owner row — per-user rows authenticate via `user_jwt`)" | REFLECTED | — | Spec explicitly states public_key is NULL for per-user grant rows. |
| Decisions table | "`direct_nats_url` in per-user row — Keep copying from owner row" | spec-control-plane.md:121 — "copies `direct_nats_url` from the cluster owner row" | REFLECTED | — | Spec states direct_nats_url is copied from owner row. |
| Decisions table | "Spec stale known-bugs — Remove `GetUserByEmail` and UUID-slug known bugs — Both are already fixed" | spec-control-plane.md:80-83 — The spec at line 80 contains "**Known bug:** `IssueHostJWT(userID, ...)` passes UUID instead of slug" for `POST /api/users/{uslug}/hosts`, and line 83 contains the same known bug for `POST /api/hosts/register`. The spec at line 121 (`adminGrantCluster`) previously had a known bug for UUID-slug which has been removed. The spec at line 83 still notes `IssueHostJWT(entry.UserID, ...)` passes UUID. The ADR says "same bug as adminGrantCluster" was also present in `adminGrantCluster` and is now fixed there. | PARTIAL | SPEC→FIX | The ADR says both `GetUserByEmail` (UUID-slug on adminGrantCluster) and UUID-slug known bugs are "already fixed in the code." The spec's `adminGrantCluster` row (line 121) no longer mentions either stale known bug — that's clean. However, spec lines 80 and 83 still carry UUID-slug known-bug notes for `POST /api/users/{uslug}/hosts` and `POST /api/hosts/register`. Per the ADR "line 509 calls `IssueHostJWTLegacy(user.Slug, ...)`" — this refers to adminGrantCluster specifically. The ADR says those two bugs (GetUserByEmail and UUID-slug) are fixed only in the adminGrantCluster context. The remaining known-bug notes on lines 80 and 83 are for other handlers; the ADR does not claim those are fixed. The adminGrantCluster entry at line 121 correctly omits any stale known-bug language. This is actually REFLECTED for the adminGrantCluster handler. The ADR's statement about removed spec entries is already satisfied. |
| Component Changes | "adminGrantCluster — remove public_key from both the SELECT and the INSERT" | spec-control-plane.md:121 — endpoint description reflects this outcome (public_key = NULL, no mention of fetching or inserting public_key). | REFLECTED | — | Spec describes the post-fix behavior without referencing the old SELECT/INSERT of public_key. |
| Error Handling | "INSERT can still conflict on `(user_id, slug)` — handled by `ON CONFLICT DO NOTHING` (idempotent re-grant)" | spec-control-plane.md:121 — no mention of ON CONFLICT / idempotent re-grant behavior | GAP | SPEC→FIX | The ADR explicitly decides the grant endpoint is idempotent (ON CONFLICT DO NOTHING). The spec's description of `POST /admin/clusters/{cslug}/grants` does not mention that re-granting is idempotent or handled via ON CONFLICT DO NOTHING. A developer implementing from the spec would not know the idempotency guarantee. |
| Security | "Per-user cluster rows do not hold the cluster's NKey. User auth uses JWT challenge-response via `user_jwt`." | spec-control-plane.md:121 — "per-user rows authenticate via `user_jwt`" | REFLECTED | — | Spec states per-user rows use user_jwt for auth, not NKey. |

### Summary

- Reflected: 4
- Gap: 1
- Partial: 1 (re-evaluated as effectively REFLECTED — stale known-bug removals apply only to adminGrantCluster, which spec line 121 correctly reflects)

**Corrected summary (after re-analysis of Partial):**

The "Partial" row on known-bug removal: the ADR says it removes stale known-bug entries for `GetUserByEmail` (now `GetUserBySlug`) and UUID-slug from `adminGrantCluster`. Checking spec line 121 — the adminGrantCluster entry calls out `IssueHostJWTLegacy(user.Slug, cslug, ...)` (correct slug), and mentions no GetUserByEmail bug. The stale entries were already removed from this handler's spec description. This is REFLECTED.

- Reflected: 5
- Gap: 1
- Partial: 0

---

## Run: 2026-05-03T12:00:00Z

# Spec Alignment Audit: ADR-0086 — Re-evaluation (second pass)

**ADR:** `docs/adr-0086-fix-admin-grant-cluster-public-key.md`
**Spec checked:** `docs/mclaude-control-plane/spec-control-plane.md`

## Phase 0 — Gather

ADR status: `accepted`. Impact section states: "No spec updates — the spec already says 'Creates a new `hosts` row for that user' without specifying `public_key`." ADR also states stale known-bug entries are removed. Spec file referenced: `docs/mclaude-control-plane/spec-control-plane.md`.

## Phase 1 — ADR → Spec Forward Pass

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Decisions table | "`public_key` in per-user row — NULL — do not copy from owner row" | spec-control-plane.md:121 — "Creates a new `hosts` row for that user with `public_key = NULL` (the cluster's NKey belongs only to the owner row — per-user rows authenticate via `user_jwt`)" | REFLECTED | — | Spec explicitly states public_key is NULL for per-user grant rows and gives the semantic reason. |
| Decisions table | "`direct_nats_url` in per-user row — Keep copying from owner row" | spec-control-plane.md:121 — "copies `direct_nats_url` from the cluster owner row" | REFLECTED | — | Spec states direct_nats_url is copied from owner row. |
| Decisions table | "Remove `GetUserByEmail` and UUID-slug known bugs — Both are already fixed in the code: line 501 calls `GetUserBySlug`; line 509 calls `IssueHostJWTLegacy(user.Slug, ...)`" | spec-control-plane.md:121 — adminGrantCluster entry uses `IssueHostJWTLegacy(user.Slug, cslug, accountKP)` with no known-bug annotation | REFLECTED | — | The adminGrantCluster spec entry has no stale known-bug note. The ADR's claim is satisfied for this handler. |
| Decisions table (stale cross-reference) | ADR fixes the UUID-slug bug in adminGrantCluster only. Spec line 83 ends with "(same bug as `adminGrantCluster`)" — after this fix, adminGrantCluster no longer has this bug, making the cross-reference stale and misleading. | spec-control-plane.md:83 — `POST /api/hosts/register` — "**Known bug:** `IssueHostJWT(entry.UserID, ...)` passes UUID instead of slug, producing incorrect JWT subject permissions (same bug as `adminGrantCluster`)." | GAP | SPEC→FIX | After ADR-0086 fixes adminGrantCluster, the parenthetical "(same bug as `adminGrantCluster`)" on spec line 83 is no longer accurate — adminGrantCluster no longer has this bug. A developer reading spec line 83 would incorrectly believe adminGrantCluster still has the UUID-slug defect. The cross-reference should be removed or updated. |
| Component Changes | "adminGrantCluster — remove public_key from both the SELECT and the INSERT" | spec-control-plane.md:121 — endpoint description reflects this outcome (public_key = NULL; no mention of fetching or inserting public_key from owner row). | REFLECTED | — | Spec describes the post-fix behavior. |
| Error Handling | "INSERT can still conflict on `(user_id, slug)` — handled by `ON CONFLICT DO NOTHING` (idempotent re-grant)" | spec-control-plane.md:121 — "Idempotent — if the user/cluster pair already exists (`ON CONFLICT (user_id, slug) DO NOTHING`), returns 201 without creating a duplicate row." | REFLECTED | — | Spec fully reflects the idempotency guarantee and the specific conflict clause. The previous run's GAP finding was incorrect — the spec does contain this language. |
| Security | "Per-user cluster rows do not hold the cluster's NKey. User auth uses JWT challenge-response via `user_jwt`." | spec-control-plane.md:121 — "per-user rows authenticate via `user_jwt`" | REFLECTED | — | Spec states per-user rows use user_jwt for auth, not NKey. |

### Summary

- Reflected: 6
- Gap: 1
- Partial: 0

---

## Run: 2026-05-03T18:00:00Z

# Spec Alignment Audit: ADR-0086 — Re-evaluation (third pass)

**ADR:** `docs/adr-0086-fix-admin-grant-cluster-public-key.md`
**Spec checked:** `docs/mclaude-control-plane/spec-control-plane.md`

## Phase 0 — Gather

ADR status: `accepted`. Spec file: `docs/mclaude-control-plane/spec-control-plane.md`. The previous run (round 2) identified one GAP: a stale cross-reference "(same bug as `adminGrantCluster`)" on spec line 83. This pass verifies the current state of the spec to determine whether that GAP has been resolved.

## Phase 1 — ADR → Spec Forward Pass

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Decisions table | "`public_key` in per-user row — NULL — do not copy from owner row" | spec-control-plane.md:121 — "Creates a new `hosts` row for that user with `public_key = NULL` (the cluster's NKey belongs only to the owner row — per-user rows authenticate via `user_jwt`)" | REFLECTED | — | Spec explicitly states public_key is NULL for per-user grant rows and gives the semantic reason. |
| Decisions table | "`direct_nats_url` in per-user row — Keep copying from owner row" | spec-control-plane.md:121 — "copies `direct_nats_url` from the cluster owner row" | REFLECTED | — | Spec states direct_nats_url is copied from owner row. |
| Decisions table | "Remove `GetUserByEmail` and UUID-slug known bugs — Both are already fixed in the code: line 501 calls `GetUserBySlug`; line 509 calls `IssueHostJWTLegacy(user.Slug, ...)`" | spec-control-plane.md:121 — adminGrantCluster entry uses `IssueHostJWTLegacy(user.Slug, cslug, accountKP)` with no known-bug annotation | REFLECTED | — | The adminGrantCluster spec entry carries no stale known-bug note. ADR claim is satisfied. |
| Decisions table (stale cross-reference — previously GAP) | ADR fixes UUID-slug bug in adminGrantCluster. Prior run found spec line 83 said "(same bug as `adminGrantCluster`)". | spec-control-plane.md:83 — current text: "**Known bug:** `IssueHostJWT(entry.UserID, ...)` passes UUID instead of slug, producing incorrect JWT subject permissions." — no parenthetical cross-reference to adminGrantCluster present. | REFLECTED | — | The stale cross-reference identified in round 2 has been removed from the spec. Spec line 83 no longer references adminGrantCluster. Prior GAP is resolved. |
| Component Changes | "adminGrantCluster — remove public_key from both the SELECT and the INSERT" | spec-control-plane.md:121 — endpoint description reflects this outcome (public_key = NULL; no mention of fetching or inserting public_key from owner row). | REFLECTED | — | Spec describes the post-fix behavior. |
| Error Handling | "INSERT can still conflict on `(user_id, slug)` — handled by `ON CONFLICT DO NOTHING` (idempotent re-grant)" | spec-control-plane.md:121 — "Idempotent — if the user/cluster pair already exists (`ON CONFLICT (user_id, slug) DO NOTHING`), returns 201 without creating a duplicate row." | REFLECTED | — | Spec fully reflects the idempotency guarantee and the specific conflict clause. |
| Security | "Per-user cluster rows do not hold the cluster's NKey. User auth uses JWT challenge-response via `user_jwt`." | spec-control-plane.md:121 — "per-user rows authenticate via `user_jwt`" | REFLECTED | — | Spec states per-user rows use user_jwt for auth, not NKey. |

### Summary

- Reflected: 7
- Gap: 0
- Partial: 0
