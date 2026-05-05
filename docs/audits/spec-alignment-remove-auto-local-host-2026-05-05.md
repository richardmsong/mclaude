## Run: 2026-05-05T00:00:00Z

ADR: `docs/adr-0095-remove-auto-local-host.md`
Specs checked: `docs/mclaude-control-plane/spec-control-plane.md`, `docs/spec-state-schema.md`
Cross-spec scan: `docs/mclaude-web/spec-web.md`, `docs/ui/mclaude-web/spec-dashboard.md`, `docs/ui/mclaude-web/spec-host-picker.md`

---

### Phase 1 — ADR → Spec (forward pass)

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Decisions table | "Auto-local-host creation — Remove from `CreateUser` and schema migration backfill" | spec-state-schema.md:82 — "Hosts are only created through the BYOH attestation flow (ADR-0095) — no automatic host rows are inserted on user creation." | REFLECTED | — | spec-state-schema.md has a clear present-tense statement absorbing this decision, citing ADR-0095 |
| Decisions table | "Auto-local-host creation — Remove from `CreateUser` and schema migration backfill" | spec-control-plane.md — no present-tense statement that `CreateUser` no longer inserts a local host row; Phase 1 invariants table includes `TestIntegration_CreateUser_NoLocalHost` (line 351) but the body text of `CreateUser` or startup sequence still carries no explicit statement that auto-insertion is removed | PARTIAL | SPEC→FIX | spec-control-plane.md documents the test that verifies the removal but never states affirmatively in prose that `CreateUser` does not create a host row. A developer reading only the prose would not know. The Phase 1 Postgres state section (line 19) says "hosts … all empty" after install — that's the closest statement but doesn't address per-user-creation behavior. |
| Decisions table | "Project creation without hosts — Block with 422 `no_hosts` error" | spec-control-plane.md:111 — "POST /api/users/{uslug}/projects … Known gap (CP-6): creates Postgres row but skips KV write…" — no mention of 422 no_hosts guard | GAP | SPEC→FIX | The HTTP endpoint description says nothing about blocking when user has zero hosts. The `mclaude.users.*.api.projects.create` NATS note (line 180) also has no no_hosts guard documented. |
| Decisions table | "Project creation without hosts — Block with 422 `no_hosts` error" | spec-state-schema.md — no mention of 422 no_hosts at all | GAP | SPEC→FIX | spec-state-schema.md documents no error cases for project creation. |
| Decisions table | "Existing local host data — Delete all hosts with `slug='local'` via migration … ON DELETE CASCADE" | spec-state-schema.md:43 — `host_id` column: "NOT NULL FK→hosts ON DELETE CASCADE" — FK cascade is present | REFLECTED | — | The cascade behavior is correctly documented; the migration decision itself (delete slug='local' rows) need not be in the spec as it is a one-time migration detail |
| Decisions table | "SPA empty state — Show guidance to register a host when user has zero hosts" | spec-control-plane.md — no mention | GAP | SPEC→FIX | Deferred to follow-up per ADR §Component Changes note ("SPA changes are deferred to a follow-up") — but the spec should note the 422 as the safety net; the SPA empty-state itself is correctly deferred |
| Decisions table | "`CreateProjectWithIdentity` fallback — Remove local-host fallback; require explicit host_id" | spec-control-plane.md:180 — "Known bug: `host_id` column is nullable in the DDL despite the spec requiring NOT NULL — `CreateProjectWithIdentity` does not set `host_id`, so NATS-created projects have `host_id = NULL` until the backfill." | GAP | SPEC→FIX | The spec documents the old buggy behavior (fallback, nullable host_id, backfill) as still present. After ADR-0095 the fallback is removed and host_id is always set by callers; the spec-control-plane.md "Known bug" note on line 180 should be removed and replaced with a statement that `CreateProjectWithIdentity` requires an explicit `host_id` from callers. |
| Decisions table | "`CreateProjectWithIdentity` fallback — Remove local-host fallback; require explicit host_id" | spec-control-plane.md:309-316 §Project Creation Flow — step 3 "Resolves `host_id` from `(user_id/host_access, hostSlug)`" then step 4 "Creates a Postgres `projects` row (`host_id`, …)" — these steps imply host_id is resolved but do not clarify fallback is gone or that callers must provide hostSlug | PARTIAL | SPEC→FIX | The project creation flow should be updated to state that `hostSlug` is required (no fallback) and that a missing/empty hostSlug returns `no_hosts` or `host_required` as appropriate |
| Component Changes / projects.go | "legacy user-scoped subject (`mclaude.users.*.api.projects.create`) … add a `db.HasHosts(ctx, userID)` check. If zero hosts, reply with `{error: 'no_hosts', …}`" | spec-control-plane.md:180 — NATS handler note describes `mclaude.users.*.api.projects.create` but says nothing about HasHosts check | GAP | SPEC→FIX | The NATS subject handler description for `mclaude.users.*.api.projects.create` must document the HasHosts pre-check and `no_hosts` error response |
| Component Changes / projects.go | "Resolve host from the request payload's `hostSlug` field … if the user has exactly one host, use it; if multiple, reply `{error: 'host_required', …}`" | spec-control-plane.md:180 — handler note says nothing about hostSlug resolution or host_required error | GAP | SPEC→FIX | The hostSlug resolution logic and `host_required` error case are absent from spec-control-plane.md's NATS handler description |
| Component Changes / project_http.go | "`handleCreateProjectHTTP` — Same `HasHosts` pre-check and 422 response" | spec-control-plane.md:111 — POST /api/users/{uslug}/projects description only says it creates a Postgres row and Known gap (CP-6); no 422 guard | GAP | SPEC→FIX | HTTP endpoint entry needs to document the 422 `no_hosts` guard |
| Component Changes / attachments.go | "`handleNATSImportConfirm` … pass the `hslug` already extracted from the NATS subject … Resolve `hslug` to a `hostID` via a DB lookup before calling" | spec-control-plane.md:172-173 — import confirm handler described without mention of host resolution | REFLECTED | — | The import confirm subject already encodes `{uslug}.{hslug}` in the subject path and spec documents the handler at that subject. The change is internal implementation detail (how hostID is resolved), not a spec-level behavior change. No gap. |
| Component Changes / db.go migration | "New migration: DELETE FROM hosts WHERE slug = 'local'; projects.host_id FK has ON DELETE CASCADE" | spec-state-schema.md:43 — `host_id` column documents `ON DELETE CASCADE` | REFLECTED | — | Cascade is already documented; migration is a one-time deployment event, not a persistent spec-level behavior |
| Component Changes / db.go — schema migration backfill | "Remove the DO block (lines 1089-1104) that creates local hosts for users missing one. Remove the UPDATE projects SET host_id backfill" | spec-control-plane.md §Startup Sequence (line 232) — "runs idempotent schema migration including the ADR-0035 `hosts` table and `projects.host_id` column" — no mention of backfill | REFLECTED | — | The backfill was never explicitly called out in spec-control-plane.md startup sequence prose, so its removal requires no spec update here |
| Integration Tests | "`TestIntegration_CreateUser_NoLocalHost`" | spec-control-plane.md:351 — test is listed in Phase 1 invariants table | REFLECTED | — | |
| Integration Tests | "Project creation blocked without hosts / succeeds with registered host" | spec-control-plane.md — neither test is listed in Phase 1 invariants or NATS subject handler tests | GAP | SPEC→FIX | The two project-creation integration tests (blocked without hosts, succeeds with host) should be added to spec-control-plane.md §Integration Tests |
| Integration Tests | "`TestIntegration_Schema_NoSeedRows`" mentioned as separate test | spec-control-plane.md:349 — `TestIntegration_Schema_NoSeedRows` already listed | REFLECTED | — | |
| Error Handling | "User creates project with no hosts: 422 `{error: 'no_hosts', message: 'Register a host before creating a project'}`" | spec-control-plane.md §Error Handling (line 331-341) — no `no_hosts` entry | GAP | SPEC→FIX | Error handling section of spec-control-plane.md must add the `no_hosts` 422 case |
| Error Handling | "NATS project creation without host context: already has host slug in the subject pattern — no change needed" | spec-control-plane.md — host-scoped NATS project create handler is already documented | REFLECTED | — | |
| Error Handling | "Migration deletes local host that has active sessions: not possible — local hosts never had agents" | spec-control-plane.md / spec-state-schema.md — no mention needed; this is a rationale note, not a runtime behavior | REFLECTED | — | Not a spec-level decision |
| ADR §Impact | "remove any mention of automatic local host creation" from spec-control-plane.md | spec-control-plane.md — no prose in the main body mentions automatic local host creation for new users (the `KNOWN-16` comment is code-only; never appeared in spec prose) | REFLECTED | — | |
| ADR §Impact | "remove `KNOWN-16` references" from spec-control-plane.md | spec-control-plane.md — no KNOWN-16 reference found in spec text | REFLECTED | — | |
| ADR §Impact | "remove the 'Default machine host' section (lines ~82-86) and any known-bug callouts referencing slug='local' auto-creation" from spec-state-schema.md | spec-state-schema.md — no "Default machine host" section exists; line 82 contains the `hosts` table note "Hosts are only created through the BYOH attestation flow (ADR-0095)" — already updated | REFLECTED | — | The section referenced in the Impact was already removed/replaced |

---

### Phase 0b — Cross-spec consistency check

The ADR removes the concept of a permanent `slug='local'` auto-created host. Several other spec files still reference `'local'` as a default host slug:

| Concept | ADR decision | Spec with stale reference | Verdict | Direction | Notes |
|---------|-------------|--------------------------|---------|-----------|-------|
| `slug='local'` default host | Removed — no auto-created local host | `spec-host-picker.md:28` — "Default selection: the user's `local` machine host (the `slug=local`, `type=machine` row created at user signup)." | GAP | SPEC→FIX | After ADR-0095 there is no `slug=local` row created at signup. The host picker default should be the first registered host (or none if no hosts), not `slug=local`. Line 93 also: `getDefaultHostSlug(): string (reads localStorage, falls back to the 'local' machine host)` — this fallback is stale. |
| `slug='local'` default host | Removed | `spec-host-picker.md:124` — "User has only the default `local` host — Settings → Hosts shows just 🖥️ local…" | GAP | SPEC→FIX | This empty/error state assumes a `local` host always exists; after ADR-0095 it won't. The state should describe a zero-host user instead. |
| `hostSlug` defaulting to `'local'` | No auto-local host means `'local'` is not a valid fallback | `spec-web.md:175` — "Throughout the SPA, `hostSlug` defaults to `'local'` when not available… This means sessions on projects without a known host will silently use `'local'` as the host slug" | GAP | SPEC→FIX | After ADR-0095 `'local'` is not guaranteed to exist. The SPA hostSlug fallback logic spec should be updated to reflect that an explicit host is always required (or the fallback is removed). |
| E2E test project seeding uses `hosts.local` | `slug='local'` host removed | `spec-web.md:225-226` — "publishes `{name: 'e2e-default', hostSlug: 'local'}` to `mclaude.users.{uslug}.hosts.local.api.projects.create`" and "Waits up to 30 s … key prefix `hosts.local.projects.`" | GAP | SPEC→FIX | E2E global-setup seeds against `hosts.local` which won't exist after ADR-0095. This must be updated to register a real test host first, then seed against it. |

---

### Summary

- Reflected: 13
- Gap: 10
- Partial: 2

---

## Run: 2026-05-05T12:00:00Z

ADR: `docs/adr-0095-remove-auto-local-host.md`
Specs checked: `docs/mclaude-control-plane/spec-control-plane.md`, `docs/spec-state-schema.md`
Cross-spec scan: `docs/mclaude-web/spec-web.md`, `docs/ui/mclaude-web/spec-host-picker.md`

Re-evaluation after prior run: verifying which prior gaps have been resolved in the specs.

### Phase 0 — Gap resolution status from prior run

| Prior gap | ADR text | Spec location | Now | Notes |
|-----------|----------|---------------|-----|-------|
| `CreateUser` no local host — spec-control-plane.md prose | "Remove from CreateUser" | spec-control-plane.md | PARTIAL (unchanged) | Phase 1 Postgres state (line 19): "hosts … all empty" applies to deployment startup, not per-user creation. No prose statement that `CreateUser` does not insert a host row. The integration test `TestIntegration_CreateUser_NoLocalHost` is listed (line 351) but that is a test, not a behavioral spec statement. |
| 422 no_hosts on HTTP project create | "Block with 422 no_hosts error" | spec-control-plane.md:111 | REFLECTED | Line 111 now fully documents: Returns 422 `{error: "no_hosts"}` if zero hosts; 422 `{error: "host_required"}` if multiple hosts and hostSlug omitted; auto-selects when exactly one host. ADR-0095 cited inline. |
| NATS handler no_hosts + host_required | "add HasHosts check; reply no_hosts / host_required" | spec-control-plane.md:180 | REFLECTED | Line 180 note documents the HasHosts pre-check, no_hosts reply, auto-select for one host, and host_required for multiple hosts. |
| CreateProjectWithIdentity fallback removed | "Remove local-host fallback; require explicit host_id" | spec-control-plane.md:180 | REFLECTED | Line 180: "host_id is always set on INSERT (ADR-0095)". The "Known bug" note about host_id being nullable is gone; the NATS handler now documents explicit hostSlug resolution. |
| Project creation flow hostSlug required | "require explicit host_id" | spec-control-plane.md:309–316 | REFLECTED | Project creation flow steps now include hostSlug as required (lines 309–312 resolve host_id from hostSlug; no fallback language present). |
| Integration tests missing | "CreateProject blocked/succeeds tests" | spec-control-plane.md | REFLECTED | Lines 355–356: both `TestIntegration_CreateProject_BlockedWithoutHosts` and `TestIntegration_CreateProject_SucceedsWithHost` are now in Phase 1 invariants table. |
| Error handling no_hosts case | "422 {error: no_hosts, message: …}" | spec-control-plane.md §Error Handling | GAP (persists) | The §Error Handling section (lines 331–340) has no entry for the 422 no_hosts case. Entries exist for 503 provisioning timeout, 410 device-code expired, 409 device-code redeemed, 403 non-admin — but the no_hosts 422 is absent. |
| spec-host-picker.md slug='local' default | "no auto-created local host" | spec-host-picker.md:28 | REFLECTED | Line 28 now reads: "Default selection: the user's first registered host (stored in localStorage `mclaude.defaultHostSlug`). If no hosts are registered, the host picker is disabled and the UI shows an empty state prompting the user to register a host (ADR-0095)." — slug='local' reference removed. |
| spec-host-picker.md "only the default local host" state | "no auto-created local host" | spec-host-picker.md:124 | REFLECTED | Line 124 now reads: "User has zero hosts — Settings → Hosts shows an empty state: 'No hosts registered. Run `mclaude host register`…'" — stale "default local host" state removed. |
| spec-web.md hostSlug 'local' fallback | "no auto-local host; explicit host required" | spec-web.md:175 | REFLECTED | Line 175 now reads: "There is no hardcoded 'local' fallback — hosts only exist through the BYOH attestation flow (ADR-0095). … If the user has no registered hosts, the SPA shows an empty state prompting host registration before any project or session operations are available." |
| spec-web.md E2E seeds against hosts.local | "slug='local' removed" | spec-web.md:225–226 | REFLECTED | E2E global setup now includes step 8 "Host registration — registers a test host via … NATS register … Stores the resulting hostSlug" and uses `hostSlug` variable throughout — no hardcoded 'local'. |

### Phase 1 — Full forward pass (this run)

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Decisions table | "Auto-local-host creation — Remove from CreateUser and schema migration backfill" | spec-state-schema.md:82 — "Hosts are only created through the BYOH attestation flow (ADR-0095) — no automatic host rows are inserted on user creation." | REFLECTED | — | |
| Decisions table | "Auto-local-host creation — Remove from CreateUser" | spec-control-plane.md — no present-tense prose stating CreateUser does not create a host row; only the integration test name `TestIntegration_CreateUser_NoLocalHost` (line 351) implies it | PARTIAL | SPEC→FIX | The §Internal Behavior or §Deployment sections should state affirmatively that `CreateUser` does not insert a host row. A developer reading only the prose cannot determine this. |
| Decisions table | "Project creation without hosts — Block with 422 no_hosts error" | spec-control-plane.md:111 — documents 422 no_hosts, 422 host_required, auto-select for one host | REFLECTED | — | Fully documented with ADR-0095 citation. |
| Decisions table | "Existing local host data — Delete via migration; ON DELETE CASCADE" | spec-state-schema.md:43 — host_id FK documents ON DELETE CASCADE | REFLECTED | — | |
| Decisions table | "SPA empty state — Show guidance to register a host when user has zero hosts" | spec-host-picker.md:28,124 — fully documents zero-host empty state; spec-web.md:175 — no-host empty state documented | REFLECTED | — | Deferred SPA implementation; empty state spec is in place. |
| Decisions table | "CreateProjectWithIdentity fallback — Remove; require explicit host_id" | spec-control-plane.md:180 — "host_id is always set on INSERT (ADR-0095)"; no fallback language remains | REFLECTED | — | |
| Component Changes / projects.go NATS handler | "legacy user-scoped subject: HasHosts check; no_hosts reply; host_required reply" | spec-control-plane.md:180 — fully documents all three behaviors | REFLECTED | — | |
| Component Changes / project_http.go | "HTTP handleCreateProjectHTTP — HasHosts pre-check and 422" | spec-control-plane.md:111 — documents 422 no_hosts and 422 host_required on HTTP endpoint | REFLECTED | — | |
| Component Changes / attachments.go | "pass hslug to CreateProjectWithIdentity" | spec-control-plane.md:172 — import confirm handler already documents host-scoped subject | REFLECTED | — | Internal implementation detail; no spec-level behavior change |
| New migration | "DELETE FROM hosts WHERE slug='local'; ON DELETE CASCADE" | spec-state-schema.md:43 — CASCADE documented; migration is a one-time event | REFLECTED | — | |
| Error Handling | "User creates project with no hosts: 422 {error: no_hosts, message: Register a host…}" | spec-control-plane.md §Error Handling (lines 331–340) — no_hosts 422 case absent | GAP | SPEC→FIX | Error handling section must add: "User creates project with zero registered hosts: 422 `{error: 'no_hosts', message: 'Register a host before creating a project'}`" |
| Error Handling | "User creates project with multiple hosts and no hostSlug: 422 host_required" | spec-control-plane.md §Error Handling (lines 331–340) — host_required 422 case absent | GAP | SPEC→FIX | Error handling section must also add the host_required 422 case. |
| Integration Tests | "TestIntegration_CreateUser_NoLocalHost" | spec-control-plane.md:351 — listed | REFLECTED | — | |
| Integration Tests | "TestIntegration_CreateProject_BlockedWithoutHosts, TestIntegration_CreateProject_SucceedsWithHost" | spec-control-plane.md:355–356 — both listed | REFLECTED | — | |

### Summary

- Reflected: 13
- Gap: 2 (both on spec-control-plane.md §Error Handling — no_hosts and host_required 422 cases missing)
- Partial: 1 (spec-control-plane.md — no prose stating CreateUser does not insert a local host row)

