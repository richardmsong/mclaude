# ADR: Remove automatic local host creation from CreateUser

**Status**: implemented
**Status history**:
- 2026-05-05: draft
- 2026-05-05: accepted — paired with spec-control-plane.md, spec-state-schema.md, spec-host-picker.md, spec-web.md
- 2026-05-05: implemented — all scope CLEAN

## Overview

Remove the automatic "local" stub host that is inserted whenever a user is created. Hosts should only exist through the normal BYOH attestation flow (`mclaude.users.*.hosts._.register`). The current behavior violates the single-code-path principle: `CreateUser` bypasses host attestation and inserts a bare row with no public key, no JWT, no agent — a host that can't do anything useful but confuses the UI into letting users create projects against it.

## Motivation

1. **Single code path violation.** `CreateUser` inserts a host row directly, bypassing the attestation flow that every real host goes through. The resulting row has no `public_key`, no `user_jwt`, no `nats_jwt` — it's a stub that looks registered but behaves differently from every other host in the system.

2. **UI confusion.** The SPA shows the stub "Local Machine" host and lets users create projects against it. Those projects immediately fail because there's no agent behind the host. Users see a "failed" project with no explanation.

3. **Backfill migration coupling.** The schema migration (db.go DO block) re-creates local hosts on every startup for any user missing one, preventing cleanup. This couples startup behavior to a legacy pattern that no longer serves a purpose.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Auto-local-host creation | Remove from `CreateUser` and schema migration backfill | Single code path — hosts only exist through attestation |
| Project creation without hosts | Block with 422 `no_hosts` error | Forces users to register a real host before creating projects. Clean enforcement. |
| Existing local host data | Delete all hosts with `slug='local'` via migration | Clean slate. No live prod deployments with real data on local hosts. Projects on local hosts get cascade-deleted or host_id nullified. |
| SPA empty state | Show guidance to register a host when user has zero hosts | Users need a clear path forward when they first log in |
| `CreateProjectWithIdentity` fallback | Remove local-host fallback; require explicit host_id | The NATS project creation path already includes host context from the subject pattern |

## User Flow

### New user (post-change)

1. User logs in via OAuth or password.
2. SPA shows empty dashboard: "No hosts registered. Run `mclaude host register` on your machine to get started."
3. User runs CLI registration on their machine → host appears in dashboard.
4. User creates a project on the registered host → works.

### Existing user with only a local host (migration)

1. Migration deletes the `local` host row.
2. Projects assigned to the local host: `host_id` set to NULL (projects already in `failed` status are effectively orphaned — they were never functional).
3. User logs in, sees empty host list, registers a real host.

## Component Changes

### `mclaude-control-plane`

**`db.go` — `CreateUser`:**
- Remove lines 230–241 (the `INSERT INTO hosts ... 'local'` block and `KNOWN-16` comment).

**`db.go` — schema migration backfill:**
- Remove the DO block (lines 1089–1104) that creates local hosts for users missing one.
- Remove the `UPDATE projects SET host_id` backfill (lines 1106–1109) that assigns orphaned projects to local hosts.

**`db.go` — `CreateProjectWithIdentity`:**
- Remove the entire host-resolution fallback block: both the `slug='local'` query (line 305) AND the second fallback `SELECT id FROM hosts WHERE user_id = $1 ORDER BY created_at LIMIT 1` (lines 307-312). Both silently assign an arbitrary host when none is explicitly provided.
- The function signature gains an explicit `hostID string` parameter. Callers must resolve the host before calling.

**`db.go` — `CreateProject` wrapper (lines 282-284):**
- This wrapper delegates to `CreateProjectWithIdentity` with no host context. After the signature change it must also accept `hostID string` and pass it through. All callers of `CreateProject` (including integration tests like `adr0065_integration_test.go:40,98`) must be updated to supply a host ID.
- **Test host setup:** integration tests that need a host should insert a test host row directly via `db.pool.Exec(ctx, "INSERT INTO hosts (id, user_id, slug, name, type, role, created_at) VALUES ($1, $2, 'test-host', 'Test Host', 'machine', 'owner', $3)", hostID, userID, now)` — same pattern used by other integration tests (e.g. `TestIntegration_CheckSlug_Taken`). No NATS registration flow needed for DB-level tests.

**`projects.go` — `handleProjectCreate` (NATS handler):**
- The host-scoped subject (`mclaude.users.{uslug}.hosts.{hslug}.api.projects.create`) already carries the host slug — no fallback needed. This is the primary path used by the SPA.
- The legacy user-scoped subject (`mclaude.users.*.api.projects.create`) has no host in the subject. Before calling `CreateProjectWithIdentity`, add a `db.HasHosts(ctx, userID)` check. If zero hosts, reply with `{error: "no_hosts", message: "Register a host before creating a project"}`.
- Resolve host from the request payload's `hostSlug` field. If `hostSlug` is empty: if the user has exactly one host, use it; if the user has multiple hosts, reply with `{error: "host_required", message: "Specify hostSlug — user has multiple hosts"}`.

**`attachments.go` — `handleNATSImportConfirm`:**
- This caller also invokes `CreateProjectWithIdentity` (line 558) with no host argument. After the signature change, pass the `hslug` already extracted from the NATS subject at line 517 (`parseImportSubject`). Resolve `hslug` to a `hostID` via a DB lookup before calling.

**`project_http.go` — `handleCreateProjectHTTP`:**
- Same `HasHosts` pre-check and 422 response. Note: this HTTP endpoint exists but is currently unused by the SPA (SPA uses the NATS path above). The guard is added for API completeness.

**New migration:**
- `DELETE FROM hosts WHERE slug = 'local';`
  The `projects.host_id` FK has `ON DELETE CASCADE`, so projects referencing deleted local hosts are automatically deleted. No need to NULL `host_id` first (which would violate the `NOT NULL` constraint added by the existing conditional migration).

### `mclaude-web` (SPA)

**Empty state when user has zero hosts:**
- Dashboard shows a message guiding the user to register a host.
- Project creation button disabled or hidden when no hosts exist.

(SPA changes are deferred to a follow-up — the CP enforcement via 422 is the safety net.)

## Data Model

No new tables. Changes:

- `hosts`: rows with `slug='local'` are deleted by migration.
- `projects`: rows referencing deleted local hosts are cascade-deleted (`ON DELETE CASCADE` FK on `host_id`).

## Error Handling

- **User creates project with no hosts:** 422 `{error: "no_hosts", message: "Register a host before creating a project"}`.
- **NATS project creation without host context:** already has host slug in the subject pattern — no change needed.
- **Migration deletes local host that has active sessions:** not possible — local hosts never had agents, so no sessions exist.

## Security

No security impact. Removing a stub that bypasses attestation is a security improvement — hosts can no longer exist without going through the NKey challenge/verify flow.

## Impact

- `docs/mclaude-control-plane/spec-control-plane.md`: remove any mention of automatic local host creation; document the 422 `no_hosts` error on project creation; remove `KNOWN-16` references.
- `docs/spec-state-schema.md`: remove the "Default machine host" section (lines ~82-86) and any known-bug callouts referencing the `slug='local'` auto-creation behavior.

## Scope

**In v1:**
- Remove auto-local-host from `CreateUser`, migration backfill, and `CreateProjectWithIdentity` fallback.
- Add migration to delete existing local hosts.
- Add 422 guard on project creation when user has no hosts.

**Deferred:**
- SPA empty-state UI for zero-host users (CP 422 is the safety net).
- Cleanup of `failed` projects that were created against local hosts.

## Open questions

_(none — all resolved)_

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| CreateUser no local host | After `CreateUser`, `SELECT * FROM hosts WHERE user_id = X AND slug = 'local'` returns 0 rows | Create user via bootstrap or admin API | mclaude-control-plane (db.go) |
| Project creation blocked without hosts | `POST /api/users/{uslug}/projects` returns 422 `no_hosts` when user has zero hosts | Create user, do NOT register a host | mclaude-control-plane (project_http.go) |
| Project creation succeeds with registered host | `POST /api/users/{uslug}/projects` succeeds when user has at least one registered host | Create user, register host via NATS | mclaude-control-plane (project_http.go, hosts.go) |
| Migration deletes local hosts | After migration runs, no rows exist in `hosts` with `slug='local'` | Pre-seed a user with a local host before migration | mclaude-control-plane (db.go migration) |

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| `mclaude-control-plane` (remove auto-local-host + add 422 guard + migration) | ~-40 / +30 lines | 60k | Remove CreateUser host insert, backfill DO block, CreateProjectWithIdentity fallback; add migration + 422 guard |
| Integration tests | ~40 lines | 40k | 4 test cases above |

**Total estimated tokens:** 100k
**Estimated wall-clock:** 1–2h
