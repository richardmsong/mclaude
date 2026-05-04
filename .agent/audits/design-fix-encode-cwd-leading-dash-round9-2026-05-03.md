# Design Audit: ADR-0076 Fix EncodeCWD Leading Dash and Remove Auto-Created Local Host Row

## Run: 2026-05-03T00:00:00Z

### Result: CLEAN — no blocking gaps found.

---

## Verification Summary

**Design completeness:** All sections are unambiguous and actionable by a developer.

**Code accuracy checks:**
- `EncodeCWD` function exists in `mclaude-cli/cmd/import.go:146-149` — accurately described.
- Auto-created `local` host INSERT in `CreateUser` exists in `mclaude-control-plane/db.go:209-218` — exactly as documented.
- Migration backfill at `db.go:905-920` creates `local` rows for users — exactly as documented.
- `seedDev` local-host NATS KV write block in `main.go:255-285` (approximately) — accurately described.
- Hosts table schema (`db.go:839-857`) includes `public_key` and `user_jwt` columns — both nullable per spec.
- Test file `cmd/import_test.go` test cases match documented line numbers and assertions.

**Spec consistency:**
- ADR-0076 references `docs/mclaude-cli/spec-cli.md` for CWD encoding — spec section at lines 62-68 correctly documents the old (incorrect) behavior ("leading `-` is preserved" will be updated).
- `spec-state-schema.md` default machine host section (lines 80-85) documents the `slug='local'` behavior as something that should be there — this ADR correctly identifies it as a known bug to remove.
- Test expectations in `TestEncodeCWDBasic` and `TestEncodeCWDMatchesClaude` match the documented test updates exactly.

**Scope and prerequisites:**
- No dependency on other unreleased ADRs or features.
- Changes are clearly scoped to v1 with no deferred work.
- Error handling section confirms no new error paths are introduced — existing error messages remain valid.
- Security section correctly notes only a reduction in surface area (no unattested hosts appearing online).

**Data model:**
- All changes are deletions or migrations; no new columns or tables.
- The DELETE migration syntax is correct: targets bare rows with NULL or empty `public_key` and `user_jwt` (the auto-created rows).
- Spec-state-schema.md section on "Default machine host" (lines 80-85) will be deleted/updated in the same commit.

**Implementation specificity:**
- `EncodeCWD` fix: 1 line removed (`strings.TrimPrefix` call) — unambiguous.
- Test updates: 7 specific changes called out (4 test cases + 3 lines in `TestEncodeCWDMatchesClaude`) — all actionable.
- `CreateUser` deletion: exact lines 207-218 specified.
- Migration backfill deletion: exact lines 905-920 specified.
- New DELETE migration SQL provided in full.
- `seedDev` block deletion: approximately lines 255-295 (note: "approximately" is acceptable for larger blocks in high-level design; the block is visually identifiable as the query for `slug='local'` + subsequent KV write).

**Integration tests:**
- Two integration test cases specified with clear pass criteria.
- Test names and verification points are concrete and unambiguous.

---

## Minor notes (not blocking gaps)

1. **seedDev line range is approximate:** The design says "approximately lines 255-295" for the `seedDev` block. Reading the actual code (`main.go:255-285`), the block is slightly smaller. This is acceptable for a design document — the block is visually identifiable by its query for `slug='local'` and the comment at line 256. No developer would struggle to find it.

2. **Spec update timing:** The design correctly notes that `docs/mclaude-cli/spec-cli.md` lines 52 and 116 in ADR-0053 document the incorrect encoding. The design correctly states ADRs are immutable — this design document supersedes ADR-0053's decision without editing it. This is the right approach.

3. **Backwards compatibility:** Existing bare `local` rows created by previous auto-creation will be removed by the DELETE migration. The design confirms this is safe because rows created by `mclaude host register` have non-null credentials and are preserved.

---

## Gaps found: 0

All critical details are present:
- ✓ Function signatures and line numbers correct
- ✓ Test case inputs and expected outputs documented
- ✓ Migration SQL provided in full
- ✓ Error handling chain explained
- ✓ Integration test scenarios unambiguous
- ✓ Scope clearly delineated (v1 only)
- ✓ No dependencies on unreleased work

**Ready for implementation.**
