## Run: 2026-05-03T17:12:00Z

CLEAN — no blocking gaps found.

**Verification Summary:**

1. **EncodeCWD fix** — Verified against actual filesystem at `~/.claude/projects/`:
   - Claude Code creates directories with leading dashes: `-Users-rsong-work-mclaude` for path `/Users/rsong/work/mclaude`
   - Current code in `cmd/import.go:146-149` incorrectly strips this dash via `strings.TrimPrefix(encoded, "-")`
   - Proposed fix (remove the `TrimPrefix` call) is correct
   - Test file shows current wrong expected values that need updating (e.g., line 166, 181, 183)

2. **Test updates** — Spec correctly identifies all needed changes:
   - `TestEncodeCWDBasic` (4 cases at lines 166-169): expected values need leading `-` added
   - `TestEncodeCWDMatchesClaude` (lines 179-186): comment, assertion, and error message all need the leading `-` added
   - `TestDiscoverSessions*` tests correctly noted as not needing changes (they bypass `EncodeCWD`)
   - `TestRunImport*` tests correctly noted as not needing changes (use `EncodeCWD` consistently in both setup and discovery)

3. **Database migration** — Migration SQL in spec is correct syntax for adding nullable TEXT column to `hosts` table. Existing schema in `db.go` shows pattern of `ALTER TABLE … ADD COLUMN IF NOT EXISTS …` for backward compatibility.

4. **`attestHostDirect` function** — Spec correctly describes:
   - Calls `GenerateUserNKey()` (confirmed present in `nkeys.go:242`)
   - Calls `IssueHostJWT()` with public key (confirmed present in `nkeys.go:297`)
   - Upserts `hosts` row with `ON CONFLICT (user_id, slug) DO UPDATE` syntax (matches existing patterns in `db.go`)
   - Returns `(jwt, seed, nil)` as documented

5. **`seedDev` changes** — Spec correctly identifies:
   - Current function signature at `main.go:197` takes `(ctx, db, nc, logger)` — spec adds `accountKP nkeys.KeyPair` parameter
   - Caller in `main()` has access to `s.accountKP` (verified in `main.go:104`)
   - Current code writes bare local host to NATS KV at lines 256-284; spec shows insertion point for `attestHostDirect` call
   - Error handling pattern matches existing code (log and continue, non-fatal)

6. **`GET /api/hosts/{slug}/seed` endpoint** — Spec correctly describes:
   - Auth-gated (matches existing pattern in `handleHostRoutes` checking `contextKeyUserID`)
   - Returns 404 if no seed stored (BYOH hosts have NULL seed)
   - Returns 403 if wrong user (seed is user-scoped via `hosts.user_id` FK)
   - Scope: dev-mode only (only `seedDev` stores a seed in `hosts` table)

7. **Spec consistency** — `docs/mclaude-cli/spec-cli.md` currently documents the WRONG encoding (line 68: "leading `-` is stripped") and needs updating to match the corrected behavior. Design doc correctly flags this in "Impact" section.

8. **Integration tests** — Spec lists three test cases with clear verification steps that map to actual code patterns (mock NATS responses, DB queries, JWT validation via nkeys package).

9. **Data model** — The `seed TEXT` nullable column on `hosts` table is clearly justified: dev-only, NULL for BYOH (client retains seed locally), allows `mclaude-controller-local` daemon to bootstrap NATS credentials without a browser flow.

10. **Security** — Endpoint properly scoped:
    - Auth-gated (requires user JWT)
    - User-scoped (filtered by `hosts.user_id`)
    - Private key material never sent to BYOH clients (they generate and retain locally)
    - Seed only stored for programmatically-attested hosts (dev mode)

All prerequisites, assumptions, error handling, and component changes are unambiguous and implementable. Implementation plan (lines 175-185) is realistic.
