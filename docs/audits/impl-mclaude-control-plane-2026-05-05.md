## Run: 2026-05-05T00:00:00Z

Component: mclaude-control-plane
Scope: ADR-0095 (remove auto-local-host from CreateUser, HasHosts guard on project creation, migration deleting local hosts, CreateProjectWithIdentity signature change)
Primary spec: docs/mclaude-control-plane/spec-control-plane.md
ADR: docs/adr-0095-remove-auto-local-host.md (Status: accepted)

---

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0095:24 | Remove `INSERT INTO hosts ... 'local'` block from `CreateUser` | db.go:221-232 | IMPLEMENTED | — | CreateUser only inserts user row; no host insert present |
| adr-0095:27 | Remove DO block that creates local hosts for users missing one | db.go:996-1224 | IMPLEMENTED | — | No DO block creating local hosts in schema |
| adr-0095:28 | Remove `UPDATE projects SET host_id` backfill that assigns orphaned projects to local hosts | db.go:996-1224 | IMPLEMENTED | — | No such UPDATE in schema |
| adr-0095:30 | Remove slug='local' fallback AND second fallback SELECT from CreateProjectWithIdentity | db.go:279-302 | IMPLEMENTED | — | No fallback host resolution; hostID is required parameter |
| adr-0095:31 | Function signature gains explicit `hostID string` parameter | db.go:279, db.go:272 | IMPLEMENTED | — | Both CreateProjectWithIdentity and CreateProject wrapper have explicit hostID |
| adr-0095:33 | CreateProject wrapper also accepts hostID and passes through | db.go:272-274 | IMPLEMENTED | — | Wrapper delegates with hostID |
| adr-0095:33 | Integration test callers updated to supply a host ID (adr0065_integration_test.go:40,98) | adr0065_integration_test.go:49,116 | IMPLEMENTED | — | Both callers insert test host row and pass hostID |
| adr-0095:36-42 | DELETE FROM hosts WHERE slug='local'; projects cascade-deleted via ON DELETE CASCADE | db.go:1084, db.go:1042 | IMPLEMENTED | — | DELETE at line 1084; CASCADE FK at line 1042 |
| adr-0095:49 | NATS handleProjectCreate legacy path: HasHosts check; reply {error:"no_hosts"} when zero | projects.go:187-201 | IMPLEMENTED | — | HasHosts check present; correct JSON error reply |
| adr-0095:50 | NATS handleProjectCreate: single host auto-select; host_required if multiple | projects.go:203-234 | IMPLEMENTED | — | Full resolution logic present |
| adr-0095:53-54 | attachments.go handleNATSImportConfirm: resolve hslug to hostID via DB before CreateProjectWithIdentity | attachments.go:517,553-565 | IMPLEMENTED | — | parseImportSubject extracts hslug; GetHostBySlug resolves to hostID; host.ID passed to CreateProjectWithIdentity |
| adr-0095:57 | project_http.go handleCreateProjectHTTP: HasHosts pre-check; 422 no_hosts response | project_http.go:115-129 | IMPLEMENTED | — | HasHosts check; 422 + JSON error present |
| adr-0095:57 | project_http.go: single host auto-select; host_required if multiple and no hostSlug | project_http.go:131-166 | IMPLEMENTED | — | Full resolution logic present |
| spec-cp:273 | User creation does not insert any host row (ADR-0095) | db.go:219-232 | IMPLEMENTED | — | Code comment and implementation confirm |
| spec-cp:111 | POST /api/users/{uslug}/projects: 422 no_hosts; 422 host_required; auto-select if one host | project_http.go:115-166 | IMPLEMENTED | — | All three branches implemented |
| spec-cp:180 | NATS mclaude.users.*.api.projects.create: no_hosts; host_required; auto-select | projects.go:186-234 | IMPLEMENTED | — | Matches spec exactly |
| spec-cp:355 | Integration test TestIntegration_Schema_NoSeedRows | integration_test.go:822 | IMPLEMENTED | — | Test present and correct |
| spec-cp:356 | Integration test TestIntegration_CreateUser_NoLocalHost | integration_test.go:873 | IMPLEMENTED | — | Test present; verifies zero local host rows |
| spec-cp:359 | Integration test TestIntegration_CreateProject_BlockedWithoutHosts | integration_test.go:2069 | IMPLEMENTED | — | Test present; verifies 422 no_hosts |
| spec-cp:360 | Integration test TestIntegration_CreateProject_SucceedsWithHost | integration_test.go:2116 | IMPLEMENTED | — | Test present; verifies success path with registered host |
| adr-0095:128-131 | Four ADR integration test cases listed | integration_test.go:873,2069,2116 + db.go:1084 | PARTIAL | SPEC→FIX | Three of four cases implemented as tests. The fourth ("Migration deletes local hosts — pre-seed a local host row, run migration, verify deletion") has no dedicated test. The migration DELETE statement exists in code; only the test harness coverage is absent. ADR should either add the test or document the omission as acceptable given the one-time nature of the migration. |

---

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| db.go:1-134 | INFRA | Package, imports, DB struct, ConnectDB, Close, Migrate, scanUser — plumbing for spec'd behavior |
| db.go:218-232 | INFRA | CreateUser without host insertion — implements spec directly |
| db.go:269-302 | INFRA | CreateProject/CreateProjectWithIdentity with explicit hostID — implements spec directly |
| db.go:418-428 | INFRA | HasHosts — implements ADR-0095 guard |
| db.go:996-1224 | INFRA | Schema const: includes DELETE migration and all DDL — implements spec |
| project_http.go:1-335 | INFRA | HTTP handlers implementing spec'd endpoints; all ADR-0095 behavior present |
| projects.go:79-130 | INFRA | StartProjectsSubscriber — subscribes to user-scoped and host-scoped subjects per spec |
| projects.go:136-343 | INFRA | handleProjectCreate — implements ADR-0095 HasHosts guard and host resolution |
| attachments.go:509-605 | INFRA | handleNATSImportConfirm — resolves hslug to hostID before CreateProjectWithIdentity |
| db_test.go:140-144 | INFRA (test) | TestSchema_BackfillLocalHost: test name is stale (references "backfill for default 'local' machine host") but assertion still passes because DELETE FROM hosts WHERE slug = 'local' contains the string literal 'local'. Not production code — no action needed, but test name is misleading. |

---

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| adr-0095:24 / CreateUser no host | CreateUser inserts no host row | db_test.go: TestComputeUserSlug, TestSchema_* (compile-time schema check) | integration_test.go:873 TestIntegration_CreateUser_NoLocalHost | TESTED | Unit validates schema shape; integration validates against real DB |
| adr-0095:36 / Migration DELETE slug='local' | DELETE FROM hosts WHERE slug='local' runs in migration | db_test.go:140 TestSchema_BackfillLocalHost (tests 'local' appears in schema string — passes on DELETE, not INSERT) | None | UNIT_ONLY | No integration test seeds a local host row before migration and verifies it is deleted post-migration. ADR lists this as a required test case. Flag: CODE→FIX — add integration test. |
| adr-0095:49 / NATS no_hosts guard | NATS legacy handler returns no_hosts when zero hosts | None | integration_test.go:1913 TestIntegration_ProjectCreate_NATS tests success path (with host), not the zero-host error path | UNTESTED | The NATS path no_hosts error case has no test. HTTP path is tested; NATS path is not. FLAG: CODE→FIX |
| adr-0095:57 / HTTP no_hosts guard | HTTP handler returns 422 no_hosts | None | integration_test.go:2069 TestIntegration_CreateProject_BlockedWithoutHosts | E2E_ONLY | No unit test; integration test covers it |
| adr-0095:57 / HTTP host_required | HTTP handler returns 422 host_required (multiple hosts, no hostSlug) | None | None | UNTESTED | No test for multi-host no-hostSlug path on HTTP handler. FLAG: CODE→FIX |
| adr-0095:50 / NATS host_required | NATS handler returns host_required (multiple hosts, no hostSlug) | None | None | UNTESTED | No test for multi-host no-hostSlug path on NATS handler. FLAG: CODE→FIX |
| adr-0095:53-54 / attachments hostID | handleNATSImportConfirm resolves hslug to hostID | None | None | UNTESTED | No test for import confirm with explicit hostID resolution. FLAG: CODE→FIX |
| adr-0095:31 / explicit hostID | CreateProjectWithIdentity requires explicit hostID | db_test.go: compile-time (schema shape) | integration_test.go:2116 TestIntegration_CreateProject_SucceedsWithHost | TESTED | Integration test verifies project creation with explicit hostID |

---

### Phase 4 — Bug Triage

No open bugs with Component matching mclaude-control-plane.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | Open bugs are in spa/droid-shield components; none match mclaude-control-plane |

---

### Summary

- Implemented: 20
- Gap: 0
- Partial: 1
- Infra: 10
- Unspec'd: 0
- Dead: 0
- Tested: 3
- Unit only: 1
- E2E only: 1
- Untested: 4
- Bugs fixed: 0
- Bugs open: 0
