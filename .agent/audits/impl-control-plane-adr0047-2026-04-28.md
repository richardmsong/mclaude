# Implementation Audit: mclaude-control-plane vs ADR-0047

**Component**: mclaude-control-plane  
**ADR**: docs/adr-0047-fix-user-jwt-kv-hosts-permission.md  
**Spec**: docs/mclaude-control-plane/spec-control-plane.md (Authentication section)  
**Evaluator**: implementation-evaluator subagent  

## Run: 2026-04-28T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0047 / Decisions row 1 | "Add `$KV.mclaude-hosts.{uslug}.>` to SubAllow: `UserSubjectPermissions(userID, userSlug string)` gains `fmt.Sprintf("$KV.mclaude-hosts.%s.>", userSlug)` in SubAllow." | nkeys.go:26-29 (`kvHosts := fmt.Sprintf("$KV.mclaude-hosts.%s.>", userSlug)` in SubAllow list) | IMPLEMENTED | — | Exact match |
| adr-0047 / Decisions row 2 | "Add `$JS.API.DIRECT.GET.>` to SubAllow: `UserSubjectPermissions` adds `$JS.API.DIRECT.GET.>` to SubAllow." | nkeys.go:29 (`"$JS.API.DIRECT.GET.>"` in SubAllow list) | IMPLEMENTED | — | Exact match |
| adr-0047 / Decisions row 3 | "`IssueUserJWT` gains `userSlug` parameter. Signature changes to `IssueUserJWT(userID, userSlug string, accountKP, expirySecs)`. `claims.Name` stays as `userID` (UUID) for `authMiddleware` DB lookups." | nkeys.go:116 (`func IssueUserJWT(userID string, userSlug string, accountKP nkeys.KeyPair, expirySecs int64)`), nkeys.go:124 (`claims.Name = userID`) | IMPLEMENTED | — | Signature and claims.Name both match |
| adr-0047 / Decisions row 3 (cont.) | "`UserSubjectPermissions` is called with both `(userID, userSlug)`." | nkeys.go:119 (`perms := UserSubjectPermissions(userID, userSlug)`) | IMPLEMENTED | — | Exact match |
| adr-0047 / Decisions row 4 | "Existing `$KV.mclaude-projects.{userID}.>` and `$KV.mclaude-sessions.{userID}.>` remain as-is." | nkeys.go:24-25 (`kvProjects := fmt.Sprintf("$KV.mclaude-projects.%s.>", userID)`, `kvSessions := fmt.Sprintf("$KV.mclaude-sessions.%s.>", userID)`) | IMPLEMENTED | — | UUID scoping preserved |
| adr-0047 / Impact | "auth.go (callers pass user.Slug)" | auth.go:116 (`IssueUserJWT(user.ID, user.Slug, s.accountKP, expiresAt+expirySecs)` in handleLogin), auth.go:157 (`IssueUserJWT(user.ID, user.Slug, s.accountKP, expiresAt+expirySecs)` in handleRefresh) | IMPLEMENTED | — | Both handleLogin and handleRefresh callers pass user.Slug |
| spec-control-plane.md / Authentication — subscribe permissions | "subscribe: `mclaude.{userID}.>, _INBOX.>, $JS.API.>, $JS.API.DIRECT.GET.>, $KV.mclaude-projects.{userID}.>, $KV.mclaude-sessions.{userID}.>, $KV.mclaude-hosts.{userSlug}.>`" | nkeys.go:29 (SubAllow list has all 7 subjects in that order except `$JS.API.>` and `$JS.API.DIRECT.GET.>` appear after KV buckets — order difference only) | IMPLEMENTED | — | All 7 subjects present; order differs from spec but NATS does not enforce SubAllow order |
| spec-control-plane.md / Authentication — publish permissions | "publish: `mclaude.{userID}.>, _INBOX.>, $JS.API.>`" | nkeys.go:28 (`PubAllow: []string{prefix, "_INBOX.>", "$JS.API.>"}`) | IMPLEMENTED | — | Exact match |
| spec-control-plane.md / Authentication — IssueUserJWT signature | "`IssueUserJWT(userID, userSlug, accountKP, expirySecs)`" | nkeys.go:116 | IMPLEMENTED | — | Signature matches |
| spec-control-plane.md / Authentication — claims.Name | "`claims.Name = userID` (UUID, used by `authMiddleware` for DB lookups)" | nkeys.go:124 | IMPLEMENTED | — | Exact match |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| nkeys.go:1-9 (package + imports) | INFRA | Standard Go package declaration and imports for JWT/nkeys libraries |
| nkeys.go:11-15 (NATSPermissions struct) | INFRA | Struct holding pub/sub allowlists; plumbing for spec'd behavior |
| nkeys.go:16-21 (UserSubjectPermissions comment block) | INFRA | Doc comment explaining the permission structure |
| nkeys.go:33-46 (SessionAgentSubjectPermissions) | INFRA | Session-agent permissions — spec'd in spec-control-plane.md (no KV buckets for agents, no _INBOX); not in ADR-0047 scope but spec-covered |
| nkeys.go:48-59 (HostSubjectPermissions) | INFRA | Per-host/cluster JWT permissions (ADR-0035); not in ADR-0047 scope |
| nkeys.go:61-120 (NKeyPair, GenerateOperator/Account/UserNKey) | INFRA | NKey generation helpers; plumbing for IssueUserJWT |
| nkeys.go:121-148 (IssueUserJWT body beyond signature) | INFRA | Standard JWT issuance boilerplate, directly implements the spec'd behavior |
| nkeys.go:150-175 (IssueHostJWT) | INFRA | Per-host JWT issuance (ADR-0035 scope, not ADR-0047) |
| nkeys.go:177-209 (IssueSessionAgentJWT) | INFRA | Session-agent JWT issuance; spec-covered |
| nkeys.go:211-228 (DecodeUserJWT) | INFRA | JWT decode/validate helper; spec-covered (authMiddleware uses it) |
| auth.go:1-60 (LoginRequest/LoginResponse/Server structs + NewServer) | INFRA | Request/response types and server constructor; spec-covered |
| auth.go:61-130 (handleLogin) | INFRA | Login handler; spec-covered; passes user.Slug as verified in Phase 1 |
| auth.go:131-175 (handleRefresh) | INFRA | Refresh handler; spec-covered; passes user.Slug as verified in Phase 1 |
| auth.go:176-230 (handleMe, authMiddleware, bearerToken, checkPassword, HashPassword, bcryptCheck) | INFRA | Auth utilities; all spec-covered |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| adr-0047: `$KV.mclaude-hosts.{uslug}.>` in SubAllow | Add mclaude-hosts KV watch permission scoped to userSlug | `TestUserSubjectPermissions` (nkeys_test.go): asserts SubAllow contains `$KV.mclaude-hosts.alice-slug.>`; `TestIssueUserJWT_UUIDInClaimsName`: asserts `$KV.mclaude-hosts.dev.local.>` in decoded JWT SubAllow | `TestIntegration_NATSSubjectPermissions` connects to real NATS but EXPLICITLY FALLS BACK to no-auth mode (comment: "no operator configured so broker-side JWT enforcement is not tested here"). No test verifies NATS actually enforces the mclaude-hosts permission. | UNIT_ONLY | Auth flow critical path. Integration test does not exercise JWT permission enforcement on a real NATS operator-mode broker. Gap per integration test bar. |
| adr-0047: `$JS.API.DIRECT.GET.>` in SubAllow | Add JetStream direct-get permission to SubAllow | `TestUserSubjectPermissions` asserts `$JS.API.DIRECT.GET.>` in SubAllow | Same caveat as above — no operator-mode NATS enforcement tested | UNIT_ONLY | Auth flow critical path. Same gap as above. |
| adr-0047: `IssueUserJWT(userID, userSlug, ...)` signature | `userSlug` parameter added; `claims.Name = userID` | `TestIssueUserJWT_UUIDInClaimsName`: verifies claims.Name is UUID, hosts KV uses slug; `TestIssueUserJWT_ClaimsRoundTrip`, `TestIssueUserJWT_SubjectScopes` | `TestIntegration_HandleLogin_UserSlugInResponse`: real DB + HTTP, verifies `resp.UserSlug` is set. `TestIntegration_HandleRefresh_ReturnsSlug`: real DB + HTTP, verifies slug in refresh response | TESTED | Login/refresh slug-in-response: integration tested. JWT slug-scoped KV permission enforcement: not integration tested (NATS operator mode required). |
| adr-0047: auth.go callers pass `user.Slug` to `IssueUserJWT` | handleLogin and handleRefresh pass user.Slug | `TestHandleRefresh_NilDB` passes slug via `IssueUserJWT(userID, userSlug, ...)` | `TestIntegration_HandleLogin_UserSlugInResponse`, `TestIntegration_HandleRefresh_ReturnsSlug` | TESTED | Both callers covered by integration tests against real Postgres |
| spec-control-plane.md: UUID-scoped project/session KV stays | `$KV.mclaude-projects.{userID}.>`, `$KV.mclaude-sessions.{userID}.>` remain UUID-scoped | `TestUserSubjectPermissions`: verifies kvProjects and kvSessions use `alice123` (UUID), not `alice-slug` | No integration test verifies broker enforcement of project/session KV scoping | UNIT_ONLY | Same NATS operator-mode gap |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| .agent/bugs/fixed/004-heartbeat-stale-agent-down.md | "Agent down: mclaude -- heartbeat stale" | Already FIXED | Component: spa + session-agent (not control-plane). Already moved to fixed/ with `Status: fixed, Fixed: 2026-04-27`. No action needed. |
| .agent/bugs/001-new-project-crash-no-git-credentials.md | New project crash — no git credentials | OPEN | Component: session-agent (not control-plane). Not in ADR-0047 scope. |
| .agent/bugs/002-ptt-race-condition.md | Push-to-Talk race condition | OPEN | Component: spa. Not in ADR-0047 scope. |
| .agent/bugs/003-initial-message-injection-race.md | Initial message injection race | OPEN | Component: not control-plane per filename. Not in ADR-0047 scope. |
| .agent/bugs/005-getting-started-canned-text.md | Getting started canned text | OPEN | Component: unknown, but not control-plane per ADR-0047 scope. Not in scope. |

### Summary

- Implemented: 10
- Gap: 0
- Partial: 0
- Infra: 14
- Unspec'd: 0
- Dead: 0
- Tested: 2
- Unit only: 3
- E2E only: 0
- Untested: 0
- Bugs fixed: 0 (bug 004 was already fixed before this audit)
- Bugs open: 0 (none relate to control-plane + ADR-0047 scope)
