# Implementation Audit: mclaude-control-plane × ADR-0049

**Component**: mclaude-control-plane  
**Spec files**: docs/adr-0049-fix-user-jwt-host-scoped-subjects.md, docs/mclaude-control-plane/spec-control-plane.md (Authentication section)  
**Production root**: mclaude-control-plane/

## Run: 2026-04-28T00:00:00Z

---

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0049 Decisions row 1 | "Add `mclaude.users.{uslug}.hosts.*.>` to PubAllow: `fmt.Sprintf("mclaude.users.%s.hosts.*.>", userSlug)` added to PubAllow in `UserSubjectPermissions`" | nkeys.go:30-32 (`hostsPrefix := fmt.Sprintf(…)` added to PubAllow slice) | IMPLEMENTED | — | `hostsPrefix` is the 4th element of PubAllow |
| adr-0049 Decisions row 2 | "Add `mclaude.users.{uslug}.hosts.*.>` to SubAllow: Same pattern added to SubAllow." | nkeys.go:33 (`hostsPrefix` appended to SubAllow slice) | IMPLEMENTED | — | `hostsPrefix` is the 8th element of SubAllow |
| adr-0049 Decisions row 3 | "Keep `mclaude.{userID}.>` in both lists. Retain the existing UUID-prefixed wildcard." | nkeys.go:27 (`prefix` first element of both PubAllow and SubAllow) | IMPLEMENTED | — | `prefix = fmt.Sprintf("mclaude.%s.>", userID)` retained |
| adr-0049 Decisions row 4 | "Spec correction: Revert spec-control-plane.md auth section to list both the old and new subject prefixes." | docs/mclaude-control-plane/spec-control-plane.md (Authentication section, JWT permissions note) | IMPLEMENTED | — | Spec now lists both `mclaude.{userID}.>` and `mclaude.users.{userSlug}.hosts.*.>` with backward-compat note |
| spec-control-plane.md Authentication | "publish: `mclaude.{userID}.>, mclaude.users.{userSlug}.hosts.*.>, _INBOX.>, $JS.API.>`" | nkeys.go:32: `PubAllow: []string{prefix, "_INBOX.>", "$JS.API.>", hostsPrefix}` | IMPLEMENTED | — | All four subjects present |
| spec-control-plane.md Authentication | "subscribe: `mclaude.{userID}.>, mclaude.users.{userSlug}.hosts.*.>, _INBOX.>, $JS.API.>, $JS.API.DIRECT.GET.>, $KV.mclaude-projects.{userID}.>, $KV.mclaude-sessions.{userID}.>, $KV.mclaude-hosts.{userSlug}.>`" | nkeys.go:33: `SubAllow: []string{prefix, "_INBOX.>", kvProjects, kvSessions, kvHosts, "$JS.API.>", "$JS.API.DIRECT.GET.>", hostsPrefix}` | IMPLEMENTED | — | All eight subjects present |
| spec-control-plane.md Authentication | "Note: `mclaude.{userID}.>` is retained for backward compatibility with un-migrated UUID-format subjects; `mclaude.users.{userSlug}.hosts.*.>` enables ADR-0035 host-scoped subjects." | nkeys.go comment block lines 20-26 and ADR-0049 Decisions row 3 | IMPLEMENTED | — | Code comment documents backward compat rationale |

---

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|----------------|-------------|
| nkeys.go:1-11 (package + imports) | INFRA | Standard package header and imports for fmt, nats-io/jwt/v2, nkeys |
| nkeys.go:13-16 (NATSPermissions struct) | INFRA | Type definition required to return permissions from helper functions |
| nkeys.go:18-26 (UserSubjectPermissions comment) | INFRA | Doc comment explaining the function |
| nkeys.go:47-59 (SessionAgentSubjectPermissions) | INFRA | Spec-described function (session-agent permissions, spec mentions no _INBOX.> for agents) |
| nkeys.go:61-68 (HostSubjectPermissions) | INFRA | ADR-0035 host JWT permissions — not in scope of ADR-0049 but spec-covered by ADR-0035 |
| nkeys.go:70-90 (GenerateOperator/AccountNKey helpers) | INFRA | Key generation utilities used by IssueUserJWT; operator bootstrap covered by startup sequence spec |
| nkeys.go:92-107 (GenerateUserNKey) | INFRA | User NKey generation called by IssueUserJWT |
| nkeys.go:109-148 (IssueUserJWT) | INFRA | Core JWT issuance; calls UserSubjectPermissions; fully spec'd |
| nkeys.go:150-177 (IssueHostJWT) | INFRA | ADR-0035 host JWT issuance; spec-covered |
| nkeys.go:179-203 (IssueSessionAgentJWT) | INFRA | Session-agent JWT issuance; spec-covered |
| nkeys.go:205-222 (DecodeUserJWT) | INFRA | JWT decode/validate; spec-covered by auth middleware description |

---

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|-----------------|---------|-------|
| adr-0049 / PubAllow host-scoped subject | `mclaude.users.{uslug}.hosts.*.>` in PubAllow | `nkeys_test.go:TestUserSubjectPermissions` (checks wantPub includes hostsPrefix); `TestIssueUserJWT_UUIDInClaimsName` (checks pub allow) | `integration_test.go:TestIntegration_NATSSubjectPermissions` — connects to NATS and verifies JWT round-trip but (a) uses a vanilla NATS server with no operator mode so JWT permission enforcement is not verified, and (b) only tests `mclaude.alice.test` subject, not the host-scoped prefix | UNIT_ONLY | Integration test does not verify that `mclaude.users.{uslug}.hosts.*.>` is actually accepted/rejected by a NATS broker running in operator mode. Test file comment explicitly acknowledges: "broker-side JWT enforcement is not tested here — that is covered by the e2e category." No e2e test was found. |
| adr-0049 / SubAllow host-scoped subject | `mclaude.users.{uslug}.hosts.*.>` in SubAllow | `nkeys_test.go:TestUserSubjectPermissions` (checks wantSub); `TestIssueUserJWT_UUIDInClaimsName` (checks sub allow) | Same as above — no operator-mode enforcement tested | UNIT_ONLY | Same gap as PubAllow — broker enforcement untested at integration level |
| adr-0049 / Keep `mclaude.{userID}.>` | UUID-prefix retained in both lists | `nkeys_test.go:TestIssueUserJWT_SubjectScopes`, `TestIssueUserJWT_UUIDInClaimsName` | `integration_test.go:TestIntegration_NATSSubjectPermissions` partially (connects but no operator mode enforcement) | UNIT_ONLY | Same operator-mode gap |
| spec-control-plane.md / Full publish list | All 4 publish subjects | `TestUserSubjectPermissions` verifies the exact slice `["mclaude.alice123.>","_INBOX.>","$JS.API.>","mclaude.users.alice-slug.hosts.*.>"]` | No operator-mode enforcement test | UNIT_ONLY | — |
| spec-control-plane.md / Full subscribe list | All 8 subscribe subjects | `TestUserSubjectPermissions` verifies the exact slice | No operator-mode enforcement test | UNIT_ONLY | — |

---

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | No open bugs with Component: mclaude-control-plane found in .agent/bugs/ | — | 4 open bugs exist; none reference the control-plane or JWT permissions |

---

### Summary

- Implemented: 7
- Gap: 0
- Partial: 0
- Infra: 11
- Unspec'd: 0
- Dead: 0
- Tested (unit+integration): 0
- Unit only: 5
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0

---

### Findings

**UNIT_ONLY [CODE→FIX]**: `mclaude.users.{uslug}.hosts.*.>` in PubAllow — unit tests verify the permission is present in the JWT claims, but no integration/e2e test verifies that a real NATS broker running in operator mode accepts subscriptions to `mclaude.users.{uslug}.hosts.{hslug}.projects.*.events.*` (the subject that triggered ADR-0049). The integration test explicitly notes broker enforcement is out of scope and defers to an e2e category. Auth flows must have integration test coverage per the evaluation bar. (`nkeys_test.go:TestUserSubjectPermissions`, integration gap in `integration_test.go`)

**UNIT_ONLY [CODE→FIX]**: `mclaude.users.{uslug}.hosts.*.>` in SubAllow — same broker-enforcement gap as PubAllow.

**UNIT_ONLY [CODE→FIX]**: `mclaude.{userID}.>` retained in PubAllow/SubAllow — unit-verified but not broker-enforcement tested.

**UNIT_ONLY [CODE→FIX]**: Full publish subject list (4 entries) — verified by unit test, not by operator-mode broker.

**UNIT_ONLY [CODE→FIX]**: Full subscribe subject list (8 entries) — verified by unit test, not by operator-mode broker.

> Note: ADR-0049 itself contains no `## Integration Test Cases` section. The UNIT_ONLY findings reflect the general integration test bar for auth flows, not an explicit ADR requirement. The functional implementation is fully correct — all 7 spec lines are IMPLEMENTED with zero gaps.
