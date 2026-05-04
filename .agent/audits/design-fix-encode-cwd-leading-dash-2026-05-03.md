## Run: 2026-05-03T00:00:00Z (Round 1)

**Gaps found: 2**

1. **Unit test expected values contradict the proposed fix** — The ADR says "existing unit tests for `EncodeCWD` must be updated: expected values change from `Users-foo-bar` to `-Users-foo-bar`." However, the existing tests in `cmd/import_test.go` (lines 161–186) assert the OLD (wrong) behavior — `"/Users/rsong/work/mclaude"` → `"Users-rsong-work-mclaude"` — and `TestEncodeCWDMatchesClaude` hardcodes that assertion with the comment "Claude Code uses `Users-rsong-work-mclaude`". The `setupImportDir` helper (line 145) calls `cmd.EncodeCWD(fakeCWD)` and uses its result to create a directory, so that helper will self-heal with the fix. But `TestEncodeCWDBasic` and `TestEncodeCWDMatchesClaude` will fail after the one-line removal because they still expect no leading dash. The ADR acknowledges the tests must be updated but does not specify which individual test cases change and what their new expected values become. A developer implementing this must know the exact new expected values for all four cases in `TestEncodeCWDBasic` (specifically `/` → `""` is still correct; `/tmp` → `-tmp` is the new result; `/Users/...` → `-Users-...` is the new result; `/home/...` → `-home-...` is the new result) and must rewrite `TestEncodeCWDMatchesClaude`'s assertion and its comment (which contradicts the fix). Without these specifics the developer must guess, and the comment "Claude Code uses `Users-rsong-work-mclaude`" is exactly the incorrect claim being fixed.
   - **Doc**: "The existing unit tests for `EncodeCWD` must be updated: expected values change from `Users-foo-bar` to `-Users-foo-bar`."
   - **Code**: `cmd/import_test.go:161–186` — `TestEncodeCWDBasic` asserts `{"/Users/rsong/work/mclaude", "Users-rsong-work-mclaude"}` and three other cases without leading dashes; `TestEncodeCWDMatchesClaude` asserts `encoded != "Users-rsong-work-mclaude"` with a comment that now states the wrong ground truth.

2. **Spec update scope is underspecified — `DiscoverSessions` tests use non-dash-prefixed encoded names directly** — `TestDiscoverSessionsFindsJSONL`, `TestDiscoverSessionsListsAvailableOnMissing`, and related tests (lines 190–243) hard-code encoded CWD strings like `"Users-alice-code-myapp"` and `"Users-alice-code-real"` as directory names passed directly to `DiscoverSessions`, bypassing `EncodeCWD`. These are test-internal strings; after the fix they remain valid inputs to `DiscoverSessions` (the function accepts any string). However, if a developer reads these tests while trying to understand what the correct encoding looks like, the tests give the wrong impression. The ADR says to "update the spec and existing tests" but the implementation plan table only lists `cmd/import_test.go` with "~5" lines for `EncodeCWD` unit tests. The `DiscoverSessions` tests that use literal wrong-format names are not addressed. A developer needs to know whether these are intentionally left as-is (they test `DiscoverSessions` directly with arbitrary strings, so they are valid) or must also be updated.
   - **Doc**: "The unit test `TestEncodeCWD` (or equivalent) in `cmd/import_test.go` must be updated to assert the leading dash is present." (no mention of `TestDiscoverSessions*` tests)
   - **Code**: `cmd/import_test.go:192` — `filepath.Join(dir, "Users-alice-code-myapp")`, `cmd/import_test.go:207` — `cmd.DiscoverSessions(dir, "Users-alice-code-myapp")`, `cmd/import_test.go:232–241` — `"Users-alice-code-real"` / `"Users-alice-code-wrong"` — all use no-leading-dash strings that look like the wrong encoding but are valid as arbitrary `DiscoverSessions` arguments.

---

## Run: 2026-05-03T00:00:00Z (Round 8)

**Gaps found: 2**

1. **ADR-0053 documents incorrect EncodeCWD behavior and is not updated in this ADR** — ADR-0076 claims to correct the encoding behavior and update the CLI spec. However, ADR-0053 (the original import feature ADR) also documents the INCORRECT encoding behavior in at least two places: (1) "CLI derives encoded CWD (absolute path with `/` → `-`, leading `-` stripped; e.g. `/Users/rsong/work/mclaude` → `Users-rsong-work-mclaude`)" and (2) "Derives encoded CWD from current directory using Claude Code's path encoding algorithm: take the absolute path (e.g. `/Users/rsong/work/mclaude`), replace every `/` with `-`, strip the leading `-` (e.g. `Users-rsong-work-mclaude`)". After ADR-0076 is implemented, these statements in ADR-0053 will contradict both the fixed code and the updated spec-cli.md. The ADR's "## Impact" section lists only `docs/mclaude-cli/spec-cli.md` as updated, with no mention of `docs/adr-0053-session-import-skill.md`. A developer would need to stop and ask: should ADR-0053 be updated too? Is it intentionally left as historical record, or is this an oversight?
   - **Doc**: Section "## Impact" states "Specs updated in this commit: docs/mclaude-cli/spec-cli.md" with no mention of ADR-0053
   - **Code**: `/Users/rsong/work/mclaude/docs/adr-0053-session-import-skill.md` lines 52 and 116 contain the old (strip leading dash) encoding description

2. **EncodeCWD function comment documents old incorrect behavior** — The docstring for `EncodeCWD` at `mclaude-cli/cmd/import.go:138-145` states: "Replace every `/` with `-`. Strip the leading `-`. Result: `Users-rsong-work-mclaude`." This comment will be incorrect after the one-line code fix is applied. The ADR does not mention updating this comment, so a developer implementing the fix must decide whether to update it. This blocks implementation because the developer must ask: should I update this comment too?
   - **Doc**: No mention of updating the function docstring in any section
   - **Code**: `/Users/rsong/work/mclaude/mclaude-cli/cmd/import.go:138-145` contains the outdated description

---

**Notes on Previously Identified Gaps (Round 1):**

The Round 1 evaluation flagged two gaps that are NOT blocking upon closer inspection:

1. **"Unit test expected values contradict the proposed fix"** — The ADR (lines 52-59) provides an explicit, detailed table with exact new expected values for all four `TestEncodeCWDBasic` test cases, plus specific line changes for `TestEncodeCWDMatchesClaude`. The context ("has four cases to update") and the table headers ("Old `want`" vs "New `want`") make the intent clear. The ADR is sufficiently specific.

2. **"DiscoverSessions tests use non-dash-prefixed encoded names"** — The ADR explicitly addresses this (line 61): "`TestDiscoverSessions*` tests pass literal strings directly to `DiscoverSessions`, bypassing `EncodeCWD` — no changes needed." The ADR is clear that these tests should not be changed because they test `DiscoverSessions` with arbitrary input strings.

These gaps were based on incomplete reading and do not block implementation.
