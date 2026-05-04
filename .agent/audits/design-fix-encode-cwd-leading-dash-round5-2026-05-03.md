## Run: 2026-05-03T00:00:00Z

**Gaps found: 2**

1. **`TestEncodeCWDBasic` table does not match the actual test file** — The design doc specifies that `TestEncodeCWDBasic` has four cases with inputs at `cmd/import_test.go:166-170`. The actual table in the file at those lines is:

   | Input | want (current code) |
   |-------|---------------------|
   | `/Users/rsong/work/mclaude` | `Users-rsong-work-mclaude` |
   | `/home/alice/projects/my-app` | `home-alice-projects-my-app` |
   | `/` | `` |
   | `/tmp` | `tmp` |

   The ADR says "four cases — all expected values gain a leading `-`" and maps these to:

   | Input | Old `want` | New `want` |
   |-------|-----------|-----------|
   | `/Users/rsong/work/mclaude` | `Users-rsong-work-mclaude` | `-Users-rsong-work-mclaude` |
   | `/home/alice/projects/my-app` | `home-alice-projects-my-app` | `-home-alice-projects-my-app` |
   | `/` | `` | `-` |
   | `/tmp` | `tmp` | `-tmp` |

   This is consistent. However, the ADR also says the test at `setupImportDir` line 145 calls `cmd.EncodeCWD(fakeCWD)` to create the project directory and claims that after the fix the `TestRunImport*` tests need no changes. This is correct for the `setupImportDir` helper (line 145 in the actual code). However, the ADR does **not** account for `TestRunImportCWDEncoding` at line 441, which also calls `cmd.EncodeCWD(fakeCWD)` directly and uses the result for output assertion (`strings.Contains(out.String(), encodedCWD)` at line 497). That call is also self-consistent after the fix. The ADR's claim that `TestRunImport*` tests need no changes is correct.

   The real gap: the ADR says the `TestEncodeCWDMatchesClaude` test has three things to update (comment on line 181, assertion on line 183, error message on line 184), but the error message at line 184 is actually the `t.Errorf` format string — `"EncodeCWD = %q; want Users-rsong-work-mclaude"`. The ADR says to update it to use `"-Users-rsong-work-mclaude"`. This is accurate — all three lines exist in the file at the stated locations with the stated content. No gap here; the ADR's description is accurate.

   **Re-classification:** No gap for these two items — the code matches the ADR's description exactly. Moving on.

2. **`TestDiscoverSessions*` tests use directory names without a leading dash, but after the fix `EncodeCWD` always produces a leading dash** — The design doc says: "TestDiscoverSessions* tests pass literal strings directly to DiscoverSessions, bypassing EncodeCWD. Those strings do not need to be updated." However, one of those tests — `TestDiscoverSessionsFindsJSONL` at line 190 — creates a directory named `"Users-alice-code-myapp"` (no leading dash) and calls `cmd.DiscoverSessions(dir, "Users-alice-code-myapp")`. Similarly, `TestDiscoverSessionsListsAvailableOnMissing` at line 229 creates `"Users-alice-code-real"` and queries `"Users-alice-code-wrong"`. The ADR's justification ("they test the directory-reading logic independently") is architecturally sound: these tests do not call `EncodeCWD` and they will still pass after the fix, because `DiscoverSessions` works with any string. A developer implementing the ADR as written will leave these tests unchanged, and they will compile and pass.

   **Re-classification:** No gap — the ADR is correct that these tests are independent and do not need updating.

---

Re-examining more carefully for actual blocking gaps:

**The one real blocking gap:**

1. **`setupImportDir` creates the encoded directory under `"claude/projects"` but the project directory name after the fix will include a leading dash** — `setupImportDir` (line 144-145 in `import_test.go`) sets `fakeCWD = "/fake/project/myapp"` and calls `cmd.EncodeCWD(fakeCWD)` to derive the directory name. After the fix, `EncodeCWD("/fake/project/myapp")` returns `"-fake-project-myapp"` (leading dash). The directory created on disk will be `<tmpdir>/claude/projects/-fake-project-myapp`. This is fine — the fixture and the production code both call `EncodeCWD` with the same input, so they stay consistent.

   However, `TestRunImportCWDEncoding` at line 497 asserts `strings.Contains(out.String(), encodedCWD)` where `encodedCWD` is computed at line 441 via `cmd.EncodeCWD(fakeCWD)`. After the fix, this checks for `"-fake-project-myapp"` in the output, which is correct.

   **Re-classification:** No gap.

---

After a thorough cross-check of all code locations cited in the design document against the actual source files:

- `EncodeCWD` at `cmd/import.go:146-149` exists exactly as described (with `TrimPrefix`). The one-line fix is unambiguous.
- `TestEncodeCWDBasic` at `import_test.go:161-177` contains exactly the four cases and old values cited. Updates are fully specified.
- `TestEncodeCWDMatchesClaude` at `import_test.go:179-186` has exactly the three items listed. Updates are fully specified.
- `setupImportDir` at line 145 calls `cmd.EncodeCWD` — self-consistent, no change needed as stated.
- `TestRunImport*` — ADR's claim that no changes are needed is correct.
- `TestDiscoverSessions*` — ADR's claim that no changes are needed is correct.
- `TestIntegration_Import_HappyPath` exists at `cmd/integration_import_test.go:100`. It calls `seedImportDir` which uses `cmd.EncodeCWD` — self-consistent.
- `spec-cli.md` §CWD encoding algorithm already says "The leading `-` is preserved" — the spec has already been updated in the co-commit as stated.

CLEAN — no blocking gaps found.
