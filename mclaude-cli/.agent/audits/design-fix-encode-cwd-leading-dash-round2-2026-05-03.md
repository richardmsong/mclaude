## Run: 2026-05-03T00:00:00Z

**Gaps found: 1**

1. **TestEncodeCWDBasic root-path case expected value unspecified** — The ADR says "all expected values gain a leading `-`" but does not state what the `{"/", ""}` case should become. After removing `strings.TrimPrefix`, `EncodeCWD("/")` returns `"-"` (a single dash), not `""`. A developer applying the rule "gain a leading `-`" to the empty string `""` could interpret this as leaving `""` unchanged (there is no non-empty prefix to prepend to) or correctly infer `"-"`. Without an explicit updated expected value for the root case, a developer would have to stop and decide.
   - **Doc**: "The existing unit tests for `EncodeCWD` must be updated. `TestEncodeCWDBasic` has four cases — all expected values gain a leading `-` (e.g. `"Users-alice-code-myapp"` → `"-Users-alice-code-myapp"`)."
   - **Code**: `import_test.go:168` — `{"/", ""}` is the third case. The new implementation returns `"-"` for this input, which differs from both the current expected value and what "gaining a leading `-`" implies for an already-empty string.
