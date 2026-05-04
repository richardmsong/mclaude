## Run: 2026-04-28T15:00:00Z

CLEAN — no blocking gaps found.

**Verification performed:**

- Read `docs/adr-0049-fix-user-jwt-host-scoped-subjects.md` in full.
- Read `mclaude-control-plane/nkeys.go` — confirmed `UserSubjectPermissions` exists with its current (pre-fix) implementation; `userSlug` parameter is already present and used for `$KV.mclaude-hosts.{userSlug}.>`.
- Read `docs/mclaude-control-plane/spec-control-plane.md` — the auth section (§ "Authentication and JWT Signing") already reflects the ADR-0049 intended state, listing both `mclaude.{userID}.>` and `mclaude.users.{userSlug}.hosts.*.>` in PubAllow and SubAllow.
- Read `mclaude-control-plane/nkeys_test.go` — existing tests (`TestUserSubjectPermissions`, `TestSubjectIsolation`) will need updating as part of implementation; this is routine and does not constitute a blocking design gap.

**Implementation is fully specified:**
1. In `UserSubjectPermissions` (`nkeys.go`), add `fmt.Sprintf("mclaude.users.%s.hosts.*.>", userSlug)` to both `PubAllow` and `SubAllow`.
2. Retain `mclaude.{userID}.>` for backward compat (already present).
3. Spec (`docs/mclaude-control-plane/spec-control-plane.md`) is already updated.
