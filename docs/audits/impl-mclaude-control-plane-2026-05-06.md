# Implementation Audit: mclaude-control-plane

## Run: 2026-05-06T00:00:00Z

Primary spec: `docs/mclaude-control-plane/spec-control-plane.md`
Primary ADR: `docs/adr-0097-unify-host-model.md` (status: accepted)
Cross-cutting: `docs/spec-state-schema.md`

---

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0097:28 | `type` column: Drop from DDL + migration | `db/db.go:1046-1058` (CREATE TABLE hosts has no type column); `db/db.go:1225-1230` (DO block drops type/role if exist) | IMPLEMENTED | — | DDL for fresh installs has no type column; DO block removes it on upgrade |
| adr-0097:29 | `role` column: Drop from DDL + migration | `db/db.go:1046-1058` (no role column); `db/db.go:1228-1230` | IMPLEMENTED | — | Same as above |
| adr-0097:71 | `db.Host` struct: remove `Type string` and `Role string` fields | `db/db.go:75-86` | IMPLEMENTED | — | Host struct has no Type or Role fields |
| adr-0097:72 | `scanHost` helper: remove `type` and `role` from the scan list | `db/db.go:412-423` | IMPLEMENTED | — | scanHost scans 10 fields, no type/role |
| adr-0097:73 | `GetHostBySlug`: remove `type, role` from SELECT and remove `AND role = 'owner'` filter | `db/db.go:463-474` | IMPLEMENTED | — | SELECT has no type/role; no role filter |
| adr-0097:74 | `GetHostByPublicKey`: remove `type, role` from SELECT | `db/db.go:479-490` | IMPLEMENTED | — | SELECT has no type/role |
| adr-0097:75-76 | `GetHostsByUser`: remove `type, role` from SELECT and scan | `db/db.go:428-446` | IMPLEMENTED | — | SELECT has no type/role |
| adr-0097:77 | `HostResponse` struct: remove `Type string` and `Role string` fields | `server/hosts.go:25-34` | IMPLEMENTED | — | HostResponse has no Type/Role fields; comment acknowledges ADR-0097 |
| adr-0097:78 | `POST /api/users/{uslug}/hosts` HTTP endpoint: remove entirely — BYOH dead | `server/hosts.go:167-182` (`handleHostRoutes`) | IMPLEMENTED | — | Comment at line 167 says "ADR-0097: POST removed"; no POST case in switch |
| adr-0097:78 | All INSERT INTO hosts: remove `type` and `role` from column lists | `server/hosts.go:453-457` (device-code host INSERT); `server/lifecycle.go:319-335` (NATS register INSERT) | IMPLEMENTED | — | Neither INSERT includes type/role |
| adr-0097:79 | `HostKVState` struct: remove `Type` field | `server/sys_subscriber.go:16-21` | IMPLEMENTED | — | HostKVState has Slug, Name, Online, LastSeenAt only |
| adr-0097:79 | Remove `h.type` from SQL queries in BOTH CONNECT and DISCONNECT handlers | `server/sys_subscriber.go:97-116` (CONNECT); `server/sys_subscriber.go:124-148` (DISCONNECT) | IMPLEMENTED | — | Both handlers SELECT only slug and name, no type |
| adr-0097:79 | Remove `htype` variable and `Type: htype` from the fallback struct literal | `server/sys_subscriber.go:137-139` | IMPLEMENTED | — | Fallback at line 138: `HostKVState{Slug: hslug, Name: hname}` — no Type field |
| adr-0097:80 | `ManageUpdateRequest`: remove `Type` field; validation checks `req.Name == ""` only | `server/lifecycle.go:744-748` (struct); `server/lifecycle.go:766-770` (validation) | IMPLEMENTED | — | ManageUpdateRequest has only Name field; validation checks `req.Name == ""` |
| adr-0097:81 | `LoginHostEntry`: remove `Type` and `Role` fields | `server/auth.go:29-33` | IMPLEMENTED | — | LoginHostEntry has ID, Slug, Name only |
| adr-0097:81 | Update `getLoginHosts` to stop assigning `h.Type` and `h.Role` | `server/auth.go:466-483` | IMPLEMENTED | — | getLoginHosts builds LoginHostEntry with ID/Slug/Name only |
| adr-0097:82 | `project_http.go:285`: remove `h.type` from SQL, no `COALESCE(h.type, '')` | `server/project_http.go:283-288` | IMPLEMENTED | — | Query at line 284-287 selects `p.id, COALESCE(h.slug, '')` — no h.type |
| adr-0097:63 | Remove `handleAdminClusters` (POST/GET), `handleAdminClusterGrants` (POST), `adminDeleteCluster` (DELETE) from admin routes | `server/admin.go:58-77` (`handleAdminRoutes`) | IMPLEMENTED | — | handleAdminRoutes has no /admin/clusters cases; comment at lines 28-30 documents removal |
| adr-0097:85 | `NATSHostRegisterRequest`: add `DefaultPlatform bool`, remove `Type string` | `server/lifecycle.go:255-259` | IMPLEMENTED | — | Struct has Name, NKeyPublic, DefaultPlatform; no Type |
| adr-0097:86 | INSERT in NATS handler: remove `type` and `role`; add `is_default_platform` when true; preserve ON CONFLICT upsert | `server/lifecycle.go:319-336` | IMPLEMENTED | — | Two INSERT paths (with/without is_default_platform), both without type/role; ON CONFLICT preserves name/public_key |
| adr-0097:87 | Check `is_admin` before allowing `defaultPlatform`; return `{error: "admin_required"}` if not admin | `server/lifecycle.go:299-308` | IMPLEMENTED | — | `if req.DefaultPlatform && !user.IsAdmin` branch returns admin_required |
| adr-0097:88 | Partial unique index collision returns `{error: "default_platform_exists"}` | `server/lifecycle.go:337-342` | IMPLEMENTED | — | String match on `idx_hosts_default_platform` returns default_platform_exists |
| adr-0097:89 | `DefaultPlatform` does NOT auto-grant on registration; auto-grant fires at JWT issuance via `issueJWTForNKey` | `server/challenge.go:261-267` | IMPLEMENTED | — | Auto-grant fires inside issueJWTForNKey on every challenge-response auth via GetDefaultPlatformHostSlug + UpsertHostGrant; not triggered at registration time |
| adr-0097:92 | `projects.go:287`: remove `host.Type == "cluster"` branch; always route to user-agent provision subject | `server/projects.go:284-285` | IMPLEMENTED | — | Comment "ADR-0097: all hosts route through user-agent unconditionally"; uses user-agent subject unconditionally |
| adr-0097:93 | Remove `hostType[0] == "cluster"` branch for deletion | `server/projects.go:556-567` (`publishProjectsDeleteToHost`) | IMPLEMENTED | — | publishProjectsDeleteToHost routes to user-agent subject unconditionally, no type branching |
| adr-0097:94 | `admin.go`, `hosts.go`: remove `h.Type` from `publishProjectsDeleteToHost`; use user-agent path unconditionally | `server/admin.go:182-185`; `server/hosts.go:269-278` | IMPLEMENTED | — | Both call publishProjectsDeleteToHost with no type argument; function uses user-agent path only |
| spec-control-plane:341 | `Removed admin cluster endpoints (/admin/clusters, /admin/clusters/grants): return 404` | `server/admin.go:58-77` | IMPLEMENTED | — | Default case returns 404; no /admin/clusters routes registered |
| spec-control-plane:309 | `Per ADR-0091 and ADR-0097, all hosts route the provisioning step through the user-agent…There is no type-based branching` | `server/projects.go:284-285`; `server/project_http.go:209-210` | IMPLEMENTED | — | Both NATS and HTTP project creation paths use user-agent subject without type check |
| spec-state-schema.md:57-67 | `hosts` table has no `type` or `role` columns | `db/db.go:1046-1058` | IMPLEMENTED | — | DDL has no type/role columns |

---

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| `server/hosts.go:61-103` (device code structs and store) | INFRA | Device-code host registration flow (POST /api/users/{uslug}/hosts/code). Spec (spec-control-plane.md) mentions device-code auth but does not enumerate these helper types. Necessary infrastructure for the device-code endpoint. |
| `server/hosts.go:303-476` (handleHostRegister) | INFRA | POST /api/hosts/register (device-code redemption) — referenced in spec-control-plane.md Integration Tests table ("TestIntegration_HostRegister_HTTP"). Legacy device-code path retained alongside NATS registration path. No conflict with ADR-0097 (different flow from /admin/clusters). |
| `server/admin.go:305-329` (adminPromoteUser) | INFRA | POST /admin/users/{uslug}/promote — listed in spec-control-plane.md admin endpoints table. |
| `server/lifecycle.go:147-157` (parseManageSubject) | INFRA | Helper for parsing manage.* subjects. Required by all handleManage* handlers. |
| `server/lifecycle.go:999-1165` (NATS reply helpers) | INFRA | replyNATSOK, replyNATSError, replyNATSForbidden, replyNATSNotFound — necessary infrastructure for all NATS handlers. |

---

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| adr-0097:68 (Schema: no type/role) | Fresh Migrate() on empty DB: hosts table has no type or role columns | — | `TestIntegration_ADR0097_Schema_NoTypeRole` (integration_test.go:2530) | E2E_ONLY | No unit test; integration test using real Postgres verifies both columns absent |
| adr-0097:63 (Admin cluster endpoints removed) | POST /admin/clusters returns 404 after removal | — | `TestIntegration_ADR0097_AdminCluster_Returns404` (integration_test.go:2553) | E2E_ONLY | Tests all 4 routes (POST/GET /admin/clusters, POST /admin/clusters/{cslug}/grants, DELETE /admin/clusters/{cslug}) |
| adr-0097:85-88 (NATS register: defaultPlatform admin) | Publish with defaultPlatform=true as admin → is_default_platform=true in DB | — | `TestIntegration_ADR0091_AdminCluster_DefaultPlatform` (grants_integration_test.go:770) | E2E_ONLY | Uses NATS registration; rewritten per ADR-0097 |
| adr-0097:88 (partial unique index conflict) | Second defaultPlatform registration returns {error: "default_platform_exists"} | — | `TestIntegration_ADR0091_AdminCluster_DefaultPlatform_Conflict` (grants_integration_test.go:835) | E2E_ONLY | Uses NATS registration |
| adr-0097:87 (non-admin defaultPlatform rejected) | Non-admin sets defaultPlatform → {error: "admin_required"} | — | `TestIntegration_ADR0097_RegisterHost_DefaultPlatform_NonAdmin` (integration_test.go:2489) | E2E_ONLY | Direct NATS request with non-admin user |
| adr-0097:92 (project routing) | Creating a project always routes to user-agent provision subject | — | `TestIntegration_ADR0097_ProjectProvisioning_UserAgentPath` (integration_test.go:2587) | E2E_ONLY | Verifies NATS publish goes to correct user-agent subject |
| adr-0097:79 (HostKVState no Type) | HostKVState has no Type field; CONNECT/DISCONNECT handlers query only slug/name | — | `TestIntegration_SysSubscriber_CONNECT` (integration_test.go:~575) | E2E_ONLY | Test verifies no type column in query path |
| adr-0097:81 (LoginHostEntry no Type/Role) | getLoginHosts returns entries without type/role fields | — | — | UNTESTED | No test verifies LoginHostEntry shape lacks type/role. The struct change is correct but uncovered by tests. |
| adr-0097:78 (POST /api/users/{uslug}/hosts removed) | POST /api/users/{uslug}/hosts returns 404 | — | — | UNTESTED | No test verifies this returns 404. The route is absent from handleHostRoutes but not explicitly tested. |
| adr-0097:183 (DefaultPlatform_AutoGrant raw INSERT) | TestIntegration_ADR0091_DefaultPlatform_AutoGrant raw INSERT uses no type/role | — | `TestIntegration_ADR0091_DefaultPlatform_AutoGrant` (grants_integration_test.go:920-923) | E2E_ONLY | Raw INSERT at line 920-923 uses `(id, user_id, slug, name, is_default_platform)` — no type/role |
| adr-0097:80 (ManageUpdateRequest Type removed) | manage.update only accepts name; req.Type removed | — | `TestIntegration_ManageUpdate` (integration_test.go, listed in spec) | E2E_ONLY | ManageUpdate integration test verifies name update; no explicit test that a Type field is rejected |

---

### Phase 4 — Bug Triage

No `.agent/bugs/` files found referencing mclaude-control-plane.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| — | (no open bugs for this component) | — | — |

---

### Summary

- Implemented: 27
- Gap: 0
- Partial: 0
- Infra: 5
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 9
- Untested: 2
- Bugs fixed: 0
- Bugs open: 0
