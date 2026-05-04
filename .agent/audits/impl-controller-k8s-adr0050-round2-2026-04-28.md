# Implementation Audit: mclaude-controller-k8s — ADR-0050 (Decisions 3–5)

## Run: 2026-04-28T00:00:00Z

Scope: Decisions 3, 4, 5 only. Pre-existing gaps (CLAUDE_CODE_TMPDIR, update operation, deployment naming) explicitly deferred per caller.

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| ADR-0050 D3 | Add `UserSlug` and `ProjectSlug` string fields to `MCProjectSpec` | mcproject_types.go:42-45 | IMPLEMENTED | — | `UserSlug string` and `ProjectSlug string` present with json tags |
| ADR-0050 D3 | Controller's `handleCreate` populates from `ProvisionRequest` | nats_subscriber.go:100-107 | IMPLEMENTED | — | `UserSlug: req.UserSlug`, `ProjectSlug: req.ProjectSlug` set in MCProject CR |
| ADR-0050 D4 | `USER_ID = Spec.UserID` | reconciler.go:198 | IMPLEMENTED | — | `{Name: "USER_ID", Value: userID}` where `userID = mcp.Spec.UserID` |
| ADR-0050 D4 | `PROJECT_ID = Spec.ProjectID` | reconciler.go:199 | IMPLEMENTED | — | `{Name: "PROJECT_ID", Value: projectID}` where `projectID = mcp.Spec.ProjectID` |
| ADR-0050 D4 | `USER_SLUG = Spec.UserSlug` | reconciler.go:202 | IMPLEMENTED | — | `{Name: "USER_SLUG", Value: mcp.Spec.UserSlug}` |
| ADR-0050 D4 | `PROJECT_SLUG = Spec.ProjectSlug` | reconciler.go:204 | IMPLEMENTED | — | `{Name: "PROJECT_SLUG", Value: mcp.Spec.ProjectSlug}` |
| ADR-0050 D4 | `HOST_SLUG = tpl.hostSlug` (cluster slug, unchanged) | reconciler.go:203 | IMPLEMENTED | — | `{Name: "HOST_SLUG", Value: tpl.hostSlug}` |
| ADR-0050 D4 | Remove TODO comments | reconciler.go (all) | IMPLEMENTED | — | No TODO comments remain in env var section |
| ADR-0050 D5 | `IssueSessionAgentJWT` gains `userSlug string` param | nkeys.go:17 | IMPLEMENTED | — | `func IssueSessionAgentJWT(userID, userSlug string, accountKP nkeys.KeyPair)` |
| ADR-0050 D5 | `SessionAgentSubjectPermissions(userID, userSlug)` returns PubAllow/SubAllow = `[mclaude.{UUID}.>, mclaude.users.{uslug}.hosts.*.>, _INBOX.>, $JS.*.API.>]` | nkeys.go:8-14 | IMPLEMENTED | — | Exact 4-element permission set matches spec |
| ADR-0050 D5 | Call site passes userSlug from MCProject spec | reconciler.go:172,183 | IMPLEMENTED | — | `IssueSessionAgentJWT(mcp.Spec.UserID, mcp.Spec.UserSlug, r.accountKP)` at both create and update paths |

### Summary

- Implemented: 11
- Gap: 0
- Partial: 0

```
CLEAN — 11 spec lines implemented, 0 gaps (decisions 3–5 of ADR-0050)
```
