## Run: 2026-05-05T00:00:00Z

**Gaps found: 4**

1. **Migration sets `host_id = NULL` but the column is `NOT NULL`** — The ADR migration sets `UPDATE projects SET host_id = NULL WHERE host_id IN (SELECT id FROM hosts WHERE slug = 'local')` then deletes the local hosts. However, the existing schema migration at `db.go:1116-1128` conditionally enforces `ALTER TABLE projects ALTER COLUMN host_id SET NOT NULL` once all rows have a non-NULL `host_id`. Any install that has run the existing migration (which backfills all rows) will have `host_id NOT NULL`. Setting it to NULL will violate the constraint and the migration will error. The ADR must either (a) first `ALTER TABLE projects ALTER COLUMN host_id DROP NOT NULL`, (b) delete the projects directly instead of nullifying them, or (c) cascade-delete them. No instruction is given for which approach to take or how to handle the NOT NULL constraint before nullifying.
   - **Doc**: "UPDATE projects SET host_id = NULL WHERE host_id IN (SELECT id FROM hosts WHERE slug = 'local');"
   - **Code**: `db.go:1116-1128` — existing migration conditionally sets `host_id NOT NULL`; `db.go:1043-1051` — base schema defines `host_id TEXT REFERENCES hosts(id) ON DELETE CASCADE` (nullable at table creation, but NOT NULL can be set by the migration).

2. **`CreateProjectWithIdentity` signature does not accept `host_id`; the 422 guard placement is unspecified** — The ADR says: "Remove the fallback query … If `host_id` is not provided (empty), return an error rather than falling back to a local host." But `CreateProjectWithIdentity` (db.go:288) resolves `host_id` internally from the DB — it is not passed by the caller. `handleCreateProjectHTTP` (project_http.go:116) calls it without passing a host. The ADR must specify: does the `host_id` check happen inside `CreateProjectWithIdentity` (returning an error), or in `handleCreateProjectHTTP` before the DB call, or via a new `GetHostsByUser` pre-check? The line reference in the doc ("line 305") points to the `SELECT … local` fallback inside `CreateProjectWithIdentity`, but the 422 response (`no_hosts`) must be issued from the HTTP handler layer — a DB function returning a generic error cannot produce an HTTP 422 with a typed body. The two change sites are not reconciled.
   - **Doc**: "Remove the fallback query `SELECT id FROM hosts WHERE user_id = $1 AND slug = 'local'` (line 305). If `host_id` is not provided (empty), return an error rather than falling back to a local host." and "`project_http.go` — Return 422 `{error: "no_hosts", …}` if the user has no registered hosts."
   - **Code**: `project_http.go:116` calls `s.db.CreateProjectWithIdentity(…)` with no host argument; `db.go:288-323` resolves `host_id` internally; `db.go:303-312` — the fallback at line 305 resolves local then any host, and the result is used directly in the INSERT.

3. **`spec-state-schema.md` "Default machine host" section is not updated** — The state schema (spec-state-schema.md) at the `hosts` table contains a sub-section "#### Default machine host" (lines 82-86) that documents the `slug='local'` auto-creation behavior as the canonical system behavior, including a known-bug callout about `POST /admin/users` not creating it. The ADR's Impact section says to update `docs/mclaude-control-plane/spec-control-plane.md` but does not mention `docs/spec-state-schema.md`. After this ADR, the "Default machine host" section will be incorrect and misleading: it describes a behavior being deleted. A developer implementing the migration would leave the state schema contradicting the code.
   - **Doc**: "Impact — `docs/mclaude-control-plane/spec-control-plane.md`: remove any mention of automatic local host creation … remove `KNOWN-16` references."
   - **Code**: `docs/spec-state-schema.md:82-86` — "#### Default machine host" section describes auto-creation of `slug='local'` host on user creation and references the KNOWN bug about admin-created users.

4. **`CreateProjectWithIdentity` second fallback (`SELECT id FROM hosts WHERE user_id = $1 ORDER BY created_at LIMIT 1`) is not addressed** — The ADR specifies removing the `slug='local'` query at line 305. But the existing code has a two-step fallback: first it tries the local host; if that fails, it falls back to any host for the user (db.go:307-312). After removing the local-host query, this second fallback (`ORDER BY created_at LIMIT 1`) would silently pick an arbitrary host whenever no `host_id` is explicitly provided. The ADR does not mention this second fallback or whether it should also be removed, kept, or converted into the `no_hosts` error path. Leaving it in produces silent wrong behavior (arbitrary host assignment) for users with non-local hosts; removing it changes behavior for the NATS creation path.
   - **Doc**: "Remove the fallback query `SELECT id FROM hosts WHERE user_id = $1 AND slug = 'local'` (line 305)."
   - **Code**: `db.go:307-312` — after the local-host lookup fails, a second query `SELECT id FROM hosts WHERE user_id = $1 ORDER BY created_at LIMIT 1` runs and its result is used as the fallback `host_id`.

## Run: 2026-05-05T09:00:00Z

**Gaps found: 2**

1. **`attachments.go` `handleNATSImportConfirm` is an unaddressed caller of `CreateProjectWithIdentity`** — The ADR says the function signature gains an explicit `hostID string` parameter and "callers must resolve the host before calling." The ADR lists three call sites to update: `db.go` `CreateUser`, the migration DO block, and `CreateProjectWithIdentity` itself. But `attachments.go:558` has a fourth call to `CreateProjectWithIdentity` that is not mentioned anywhere in the ADR. The NATS import-confirm handler (`mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.confirm`) already has `hslug` in the subject — the host is known — but the current call passes no host. After the signature change, this call site will fail to compile. The ADR does not tell the developer how to handle this caller: whether to pass `hslug` directly (which requires resolving `host_id` from slug first), or something else.
   - **Doc**: "The function signature gains an explicit `hostID string` parameter. Callers must resolve the host before calling." — no mention of `attachments.go`.
   - **Code**: `attachments.go:558` — `s.db.CreateProjectWithIdentity(ctx, projID, user.ID, projectName, "", nil)` called with no host argument; `hslug` is available in scope at that point from `parseImportSubject(msg.Subject)` at line 517.

2. **Legacy NATS path `mclaude.users.*.api.projects.create` fallback behavior is under-specified when user has exactly one host** — The ADR says for the legacy subject: "Before calling `CreateProjectWithIdentity`, add a `db.HasHosts(ctx, userID)` check. If zero hosts, reply with `no_hosts`." Then: "Resolve host from the request payload's `hostSlug` field (or fall back to the user's first host if only one exists)." This introduces a new non-zero-host fallback ("first host if only one") that is conditional on host count — but the ADR does not specify what to do when the user has multiple hosts and `hostSlug` is empty in the payload. A developer reading this must stop and decide: error out, pick the first host anyway, or require `hostSlug`? The three behaviors have different consequences for existing SPA callers on the legacy subject.
   - **Doc**: "Resolve host from the request payload's `hostSlug` field (or fall back to the user's first host if only one exists)." — no specified behavior when `hostSlug` is absent and `len(hosts) > 1`.
   - **Code**: `projects.go:168` — current code uses `hostSlug` from subject (empty for legacy path) or payload; `db.go:307-312` — the second fallback (`ORDER BY created_at LIMIT 1`) is what currently handles the multi-host case, and the ADR says to remove it without defining a replacement.

## Run: 2026-05-05T10:15:00Z

**Gaps found: 1**

1. **`CreateProject` wrapper and test callers are unaddressed after the `CreateProjectWithIdentity` signature change** — The ADR specifies that `CreateProjectWithIdentity` gains an explicit `hostID string` parameter and enumerates three callers to update: `projects.go:191`, `project_http.go:116`, and `attachments.go:558`. However, `db.go:282-284` has a `CreateProject` wrapper function (`func (db *DB) CreateProject(ctx, id, userID, name, gitURL string) (*Project, error)`) that delegates to `CreateProjectWithIdentity` with a nil gitIdentityID and no host context. After the signature change this wrapper will fail to compile. Additionally, `adr0065_integration_test.go:40,98` calls `db.CreateProject(ctx, ..., userID, ...)` in integration tests that have no host context — those tests will also fail to compile. The ADR does not mention the `CreateProject` wrapper or any of its callers, leaving the developer unable to proceed without knowing what host (if any) these test-only call sites should use.
   - **Doc**: "The function signature gains an explicit `hostID string` parameter. Callers must resolve the host before calling." — enumerates `projects.go`, `project_http.go`, `attachments.go` only.
   - **Code**: `db.go:282-284` — `CreateProject` wrapper calls `CreateProjectWithIdentity` with no host; `adr0065_integration_test.go:40,98` — two `db.CreateProject` calls with no host that will fail to compile after the signature change.

## Run: 2026-05-05T12:00:00Z

**Gaps found: 1**

1. **Integration test callers of `CreateProject` have no specified host setup** — The ADR says `adr0065_integration_test.go:40,98` "must be updated to supply a host ID." Both call sites create a user and immediately call `db.CreateProject` — there is no host registered for the user at that point. The ADR does not say how these tests should acquire a host ID: whether they should insert a bare host row directly into Postgres for test setup, call the full NATS registration flow (which requires NATS infrastructure the tests may not have), or use a pre-seeded host from another part of the test. The line "must be updated to supply a host ID" is the complete specification — it tells the developer nothing about what host to create or how. A developer implementing this would have to stop and ask.
   - **Doc**: "All callers of `CreateProject` (including integration tests like `adr0065_integration_test.go:40,98`) must be updated to supply a host ID."
   - **Code**: `adr0065_integration_test.go:28-82` and `87-155` — both tests create a user (`db.CreateUser`) then immediately call `db.CreateProject` with no host; the tests have a real Postgres instance (via testcontainers) but the host registration flow requires both Postgres and NATS. No pattern in the test file shows how a host is directly inserted for test isolation.

## Run: 2026-05-05T13:45:00Z

CLEAN — no blocking gaps found.

All gaps from prior runs have been addressed in the current ADR text:
- Migration now specifies `DELETE FROM hosts WHERE slug = 'local'` with FK cascade, bypassing the NOT NULL conflict.
- `CreateProjectWithIdentity` now specifies explicit `hostID string` parameter with all callers enumerated including `attachments.go:558`.
- `CreateProject` wrapper addressed; test callers given exact `INSERT INTO hosts` setup SQL matching the existing `TestIntegration_CheckSlug_Taken` pattern (verified at `integration_test.go:2020`).
- Legacy NATS path multi-host behavior fully specified: zero hosts → `no_hosts`, one host → use it, multiple hosts with no `hostSlug` → `host_required`.
- `spec-state-schema.md` "Default machine host" section is listed in Impact for removal.
- `HasHosts` DB method is new (not yet in codebase) — correctly specified as a new addition.
