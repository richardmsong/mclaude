## Run: 2026-04-28T00:00:00Z

CLEAN — no blocking gaps found.

### Verification notes

Checked the following against the codebase and referenced specs:

1. **`UserSubjectPermissions(userID, userSlug string)`** — exists in `mclaude-control-plane/nkeys.go:22` with both parameters; adds `$KV.mclaude-hosts.{userSlug}.>` and `$JS.API.DIRECT.GET.>` to SubAllow exactly as the ADR decisions specify.

2. **`IssueUserJWT(userID, userSlug string, accountKP, expirySecs)`** — exists in `mclaude-control-plane/nkeys.go:116` with the updated signature; `claims.Name = userID` (UUID) confirmed; delegates to `UserSubjectPermissions(userID, userSlug)`.

3. **Callers updated** — `mclaude-control-plane/auth.go:112,168` pass `user.ID, user.Slug` as confirmed by grep.

4. **`spec-control-plane.md` updated** — The "Authentication and JWT Signing" section at the implementation point lists the full permission set including `$JS.API.DIRECT.GET.>` and `$KV.mclaude-hosts.{userSlug}.>`, matching the decisions table exactly.

5. **SPA watch key** — `HeartbeatMonitor.start()` calls `kvWatch('mclaude-hosts', kvKeyHostsForUser(userSlug), ...)` where `kvKeyHostsForUser('dev.local')` returns `'dev.local.*'`. The JWT permission `$KV.mclaude-hosts.dev.local.>` covers subscriptions to `$KV.mclaude-hosts.dev.local.*` (NATS `>` is a superset of `*`). This is correct.

Minor inaccuracy observed (non-blocking): The Motivation section describes the SPA watch as `kvWatch('mclaude-hosts', 'dev.*', ...)`, but the actual pattern is `dev.local.*` (the user slug, not a bare prefix). The Decisions section and implementation are correct regardless.
