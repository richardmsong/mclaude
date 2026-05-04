## Run: 2026-04-28T00:00:00Z

# Spec Alignment Audit: ADR-0047 Fix User JWT Missing mclaude-hosts KV Permission

**ADR**: `docs/adr-0047-fix-user-jwt-kv-hosts-permission.md`
**Spec**: `docs/mclaude-control-plane/spec-control-plane.md`

## Phase 1 — ADR → Spec (Forward Pass)

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Decisions table, row 1 | "Add `$KV.mclaude-hosts.{uslug}.>` to SubAllow — `UserSubjectPermissions(userID, userSlug string)` gains `fmt.Sprintf("$KV.mclaude-hosts.%s.>", userSlug)` in SubAllow." | spec-control-plane.md — Authentication and JWT Signing §3 subscribe list: `$KV.mclaude-hosts.{userSlug}.>` | REFLECTED | — | Spec's subscribe list includes `$KV.mclaude-hosts.{userSlug}.>` |
| Decisions table, row 2 | "Add `$JS.API.DIRECT.GET.>` to SubAllow — `UserSubjectPermissions` adds `$JS.API.DIRECT.GET.>` to SubAllow." | spec-control-plane.md — Authentication and JWT Signing §3 subscribe list: `$JS.API.DIRECT.GET.>` | REFLECTED | — | Spec's subscribe list includes `$JS.API.DIRECT.GET.>` |
| Decisions table, row 3 | "`IssueUserJWT` gains `userSlug` parameter — Signature changes to `IssueUserJWT(userID, userSlug string, accountKP, expirySecs)`. `claims.Name` stays as `userID` (UUID). `UserSubjectPermissions` called with `(userID, userSlug)`." | spec-control-plane.md — Authentication and JWT Signing §3: `IssueUserJWT(userID, userSlug, accountKP, expirySecs)` and `claims.Name = userID` (UUID, used by `authMiddleware` for DB lookups) | REFLECTED | — | Spec shows updated signature and clarifies claims.Name stays UUID |
| Decisions table, row 4 | "Existing project/session KV permissions stay UUID-scoped — `$KV.mclaude-projects.{userID}.>` and `$KV.mclaude-sessions.{userID}.>` remain as-is." | spec-control-plane.md — Authentication and JWT Signing §3 subscribe list: `$KV.mclaude-projects.{userID}.>`, `$KV.mclaude-sessions.{userID}.>` | REFLECTED | — | Spec shows project and session KV permissions still use `{userID}` (UUID) prefix |

### Summary

- Reflected: 4
- Gap: 0
- Partial: 0

## Result

CLEAN — 4 ADR decisions reflected in spec, 0 gaps
