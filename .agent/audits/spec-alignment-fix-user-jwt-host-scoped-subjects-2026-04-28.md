## Run: 2026-04-28T00:00:00Z

# Spec Alignment Evaluation: ADR-0049 — Fix User JWT Missing Host-Scoped Subject Permissions

**ADR**: `docs/adr-0049-fix-user-jwt-host-scoped-subjects.md`  
**Status**: accepted  
**Spec(s) checked**: `docs/mclaude-control-plane/spec-control-plane.md`

## Phase 1 — ADR → Spec (Forward Pass)

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Decisions table row 1 | "Add `mclaude.users.{uslug}.hosts.*.>` to PubAllow — `fmt.Sprintf("mclaude.users.%s.hosts.*.>", userSlug)` added to PubAllow in `UserSubjectPermissions`." | `spec-control-plane.md` § Authentication and JWT Signing, step 3: `publish: mclaude.{userID}.>, mclaude.users.{userSlug}.hosts.*.>, _INBOX.>, $JS.API.>` | REFLECTED | — | Spec explicitly lists `mclaude.users.{userSlug}.hosts.*.>` in the publish permissions. |
| Decisions table row 2 | "Add `mclaude.users.{uslug}.hosts.*.>` to SubAllow — Same pattern added to SubAllow." | `spec-control-plane.md` § Authentication and JWT Signing, step 3: `subscribe: mclaude.{userID}.>, mclaude.users.{userSlug}.hosts.*.>, _INBOX.>, ...` | REFLECTED | — | Spec explicitly lists `mclaude.users.{userSlug}.hosts.*.>` in the subscribe permissions. |
| Decisions table row 3 | "Keep `mclaude.{userID}.>` in both lists — Retain the existing UUID-prefixed wildcard. Backward compat while project/session KV keys and any remaining old-format subjects haven't been migrated." | `spec-control-plane.md` § Authentication and JWT Signing, step 3 Note: "`mclaude.{userID}.>` is retained for backward compatibility with un-migrated UUID-format subjects; `mclaude.users.{userSlug}.hosts.*.>` enables ADR-0035 host-scoped subjects. Both are removed when the full subject migration lands." | REFLECTED | — | Spec retains `mclaude.{userID}.>` in both pub and sub lists and explains backward-compat rationale. |
| Decisions table row 4 | "Spec correction — Revert `spec-control-plane.md` auth section to list both old and new subject prefixes, replacing the ADR-0047 regression." | `spec-control-plane.md` § Authentication and JWT Signing, step 3 | REFLECTED | — | Spec now documents both `mclaude.{userID}.>` (legacy UUID) and `mclaude.users.{userSlug}.hosts.*.>` (ADR-0035 slug-based) in a coherent note explaining intent and migration path. |

## Phase 2 — Summary

### Summary

- Reflected: 4
- Gap: 0
- Partial: 0

---

CLEAN — 4 ADR decisions reflected in spec, 0 gaps
