# Audit Round 8: Code vs Spec Gaps

**Date:** 2026-04-29
**Auditor:** worker-agent (code-vs-spec)
**Scope:** All 6 spec files cross-referenced against all listed production code files.
**Method:** Full read of every spec and every code file, systematic cross-reference for undocumented behaviors.

---

## New Unflagged Gaps

### GAP 1: adminRegisterCluster uses arbitrary user_id instead of calling admin

**GAP:** `adminRegisterCluster` assigns the cluster host row to `(SELECT id FROM users LIMIT 1)` — an arbitrary user — instead of the authenticated admin who called the endpoint.

- **Code:** `mclaude-control-plane/admin.go` line ~201 — `INSERT INTO hosts ... VALUES ($1, (SELECT id FROM users LIMIT 1), ...)`
- **Spec:** `docs/mclaude-control-plane/spec-control-plane.md` — POST /admin/clusters: "Creates a hosts row with type='cluster', role='owner' for the calling admin"
- **Detail:** The spec requires the cluster host row be owned by the calling admin. The code picks whichever user has the lowest-ordered `id` in the table, which is non-deterministic. The admin bearer token on the loopback port doesn't carry user identity — the handler needs a mechanism to resolve the admin's user_id (e.g. from a secondary header, query param, or request body field).

---

### GAP 2: CreateProjectWithIdentity does not set slug column on INSERT

**GAP:** New projects created via the NATS `mclaude.users.*.api.projects.create` path get an empty `slug` column in Postgres and an empty `slug` field in the KV value.

- **Code:** `mclaude-control-plane/db.go` `CreateProjectWithIdentity()` — INSERT omits the `slug` column; returned `Project.Slug` is `""`.
- **Spec:** `docs/spec-state-schema.md` — projects table: `slug TEXT NOT NULL` described as "Derived as slugify(name) with numeric suffix on collision within scope. Immutable after creation."
- **Detail:** The `CreateProjectWithIdentity` INSERT statement sets `(id, user_id, name, git_url, status, created_at, git_identity_id)` but never computes or sets `slug`. The backfill in the schema DDL (`UPDATE projects SET slug = lower(regexp_replace(name, ...)) WHERE slug = ''`) only runs on startup migration, not inline during the INSERT. So between creation and next restart, the project has `slug=''`. This also means `writeProjectKV` writes an empty `slug` field to the KV value, and the `ProvisionRequest.ProjectSlug` sent to the controller is empty.

---

### GAP 3: projects.host_id column nullable in DDL despite spec requiring NOT NULL

**GAP:** The Postgres DDL defines `host_id` as nullable (`TEXT REFERENCES hosts(id) ON DELETE CASCADE`) but the spec schema says `NOT NULL`.

- **Code:** `mclaude-control-plane/db.go` schema constant — `host_id TEXT REFERENCES hosts(id) ON DELETE CASCADE` (no NOT NULL).
- **Spec:** `docs/spec-state-schema.md` — projects table: `host_id TEXT NOT NULL FK→hosts ON DELETE CASCADE`.
- **Detail:** The Go struct has `HostID *string` with comment "nullable during migration; NOT NULL for new installs" but the DDL never enforces NOT NULL even on fresh installs. `CreateProjectWithIdentity` also doesn't set `host_id`, so new projects have `host_id = NULL`. The backfill in the DO block sets `host_id` from the user's `local` host for orphaned projects, but newly created projects via the NATS path don't get `host_id` set at all until the next migration backfill.

---

### GAP 4: Session-agent HOST_SLUG never fatals on absence — falls back to "h-unknown"

**GAP:** The session-agent binary silently falls back through hostname → "h-unknown" when `HOST_SLUG` is missing, instead of fatally exiting as the spec requires.

- **Code:** `mclaude-session-agent/main.go` lines ~85-95 — cascading fallbacks: `slug.Slugify(*flagHostname)` → `slug.Slugify(os.Hostname())` → `"h-unknown"`.
- **Spec:** `docs/mclaude-session-agent/spec-session-agent.md` — Configuration table: `HOST_SLUG` "**Required.** ... Hard fail at startup on absence." Error handling table: "HOST_SLUG / --host not provided → Fatal: agent or daemon refuses to start with FATAL: HOST_SLUG required (set via env or --host flag) (no fallback)."
- **Detail:** The spec explicitly states no fallback and a fatal exit on absence. The code has three levels of fallback before reaching a hardcoded "h-unknown" string. This means a session-agent can start without valid host configuration and publish to incorrect NATS subjects (`mclaude.users.*.hosts.h-unknown.…`), which no client is watching.

---

### GAP 5: QuotaMonitor subscribes to UUID-based quota subject; daemon publishes to slug-based

**GAP:** The QuotaMonitor (per-session goroutine) subscribes to `mclaude.users.{userID}.quota` (UUID), but the daemon's `runQuotaPublisher` publishes to `mclaude.users.{userSlug}.quota` (slug). They use different identifiers and won't match.

- **Code:** `mclaude-session-agent/quota_monitor.go` line ~68 — `subject := "mclaude.users." + userID + ".quota"` (UUID).
- **Code:** `mclaude-session-agent/daemon_jobs.go` line ~135 — `subject := subj.UserQuota(d.cfg.UserSlug)` (slug).
- **Spec:** `docs/spec-state-schema.md` — NATS Subjects table: `mclaude.users.{uslug}.quota` — Publisher: daemon. Subscriber: QuotaMonitor.
- **Detail:** `newQuotaMonitor()` receives `userID` (UUID) from `a.userID` in agent.go and builds the subscription subject with the UUID. The daemon publishes to the slug-based subject per the spec. Unless userID and userSlug happen to be identical (which they never are — UUIDs vs slugified email), quota signals from the daemon never reach the session-agent's QuotaMonitor. Quota threshold enforcement is effectively broken for scheduled jobs.

---

### GAP 6: session_job_complete lifecycle event includes prUrl field; spec says "No prUrl"

**GAP:** The QuotaMonitor's `publishExitLifecycle` includes a `prUrl` field in the `session_job_complete` lifecycle event, but the spec explicitly says "No prUrl".

- **Code:** `mclaude-session-agent/quota_monitor.go` line ~147 — `m.publishLifec(m.sessionID, "session_job_complete", map[string]string{"prUrl": m.completionPR, "jobId": ..., "branch": ...})`
- **Spec:** `docs/spec-state-schema.md` — Lifecycle Event Payloads / session_job_complete: payload is `{type, sessionId, jobId, branch, ts}`. "No prUrl — callers capture artifacts (PR URL, commit SHA, results) via git log, PR body, or external logging."
- **Detail:** The code scans Claude's output for a `SESSION_JOB_COMPLETE:{prUrl}` marker and includes the extracted PR URL in the lifecycle event. The spec's ADR-0034 target removes this field. The `prUrl` in the lifecycle payload is separate from the `prUrl` in the JobEntry struct (which is flagged under "ADR-0034 migration not complete").

---

### GAP 7: SPA session-store DEL handler extracts slug from KV key but map is keyed by UUID

**GAP:** When a session KV entry is deleted, the `SessionStore.startWatching` callback extracts the last dot-separated token from the key (which is the session slug) and calls `this._sessions.delete(sessionSlug)`, but the `_sessions` map is keyed by `state.id` (UUID). The delete is a no-op and ghost sessions remain in the UI.

- **Code:** `mclaude-web/src/stores/session-store.ts` — DEL handler: `const sessionId = parts[parts.length - 1]; this._sessions.delete(sessionId)`.
- **Code:** `mclaude-web/src/stores/session-store.ts` — PUT handler: `this._sessions.set(state.id, state)` (keyed by UUID from JSON value).
- **Spec:** `docs/mclaude-web/spec-web.md` — "KVEntry.operation may be 'PUT' | 'DEL' | 'PURGE'. The session store's kvWatch callback must handle DEL ... by removing the corresponding entry from the _sessions map."
- **Detail:** KV keys are `{uslug}.{hslug}.{pslug}.{sslug}` where `sslug` is the session slug (e.g. "new-session"). But `_sessions` is keyed by UUID (e.g. "a1b2c3d4-..."). `parts[parts.length - 1]` returns the slug, and `this._sessions.delete("new-session")` silently fails because the map has no entry with that key. Deleted sessions persist in the UI until a full page reload.

---

## Summary

**7 new unflagged gaps found.**

| # | Severity | Component | Summary |
|---|----------|-----------|---------|
| 1 | Medium | control-plane | adminRegisterCluster uses arbitrary user_id |
| 2 | High | control-plane | New projects get empty slug |
| 3 | Medium | control-plane | host_id nullable despite spec NOT NULL |
| 4 | Medium | session-agent | HOST_SLUG never fatals, falls back to "h-unknown" |
| 5 | High | session-agent | QuotaMonitor subscription uses UUID, publisher uses slug — quota signals lost |
| 6 | Low | session-agent | session_job_complete includes stale prUrl field |
| 7 | High | web SPA | Session DEL handler uses slug key but map is keyed by UUID — ghost sessions |
